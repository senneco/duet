package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/senneco/duet/core"
)

type fixture struct {
	t    *testing.T
	c    *core.Core
	s    *Service
	dir  string
	path string
	r    string
}

// setup — агент сессии s1 отдал a.md на ревью и свободен.
func setup(t *testing.T, kind string) *fixture {
	t.Helper()
	home := t.TempDir()
	c, err := core.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, c: c, dir: Canon(t.TempDir())}
	f.s = New(c, home, func(id string) string { return "http://duet.localhost/r/" + id })
	f.path = filepath.Join(f.dir, "a.md")
	os.WriteFile(f.path, []byte("one\n"), 0o644)
	_, err = f.s.Submit(SubmitReq{Who: Who{Session: "s1", Agent: kind, Cwd: f.dir}, Files: []SubmitFile{{Path: f.path, Content: "one\n", Original: &core.Original{Content: "zero\n"}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.r = "r1"
	f.hook("Stop", nil)
	return f
}

func (f *fixture) hook(event string, extra map[string]any) HookResp {
	m := map[string]any{"session_id": "s1", "hook_event_name": event, "cwd": f.dir}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return f.s.Hook(HookReq{Agent: "claude", Payload: b})
}

func (f *fixture) sendComment(text string) {
	f.t.Helper()
	if _, err := f.c.AddComment(f.r, f.path, nil, text, nil, false); err != nil {
		f.t.Fatal(err)
	}
	if err := f.c.Send(f.r); err != nil {
		f.t.Fatal(err)
	}
}

// 116: «прервать» посреди работы — агенту запрещено всё, после остановки диск возвращён и агента будят.
func TestInterruptThroughHooks(t *testing.T) {
	f := setup(t, "claude")
	f.sendComment("x")
	text, _ := f.s.Wait(context.Background(), "s1")
	if !strings.Contains(text, "1 review comment") {
		t.Fatal(text)
	}
	edit := map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": f.path}}
	if r := f.hook("PreToolUse", edit); r.Code != 0 {
		t.Fatal(r)
	}
	os.WriteFile(f.path, []byte("two\n"), 0o644)
	if err := f.c.Interrupt(f.r); err != nil {
		t.Fatal(err)
	}
	r := f.hook("PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}})
	if r.Code != 2 || !strings.Contains(r.Stderr, "interrupted") {
		t.Fatal(r)
	}
	f.hook("Stop", nil)
	if b, _ := os.ReadFile(f.path); string(b) != "one\n" {
		t.Fatal(string(b))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	text, _ = f.s.Wait(ctx, "s1")
	if !strings.Contains(text, "interrupted your work") || !strings.Contains(text, "a.md") {
		t.Fatal(text)
	}
}

// 52: Stop без ответа на взятые комментарии не даёт закончить.
func TestStopBlocksUnanswered(t *testing.T) {
	f := setup(t, "claude")
	f.sendComment("x")
	f.s.Wait(context.Background(), "s1")
	r := f.hook("Stop", nil)
	var out struct{ Decision, Reason string }
	json.Unmarshal([]byte(r.Stdout), &out)
	if out.Decision != "block" || !strings.Contains(out.Reason, "not answered") {
		t.Fatal(r)
	}
	// Агент уже получил напоминание и всё равно останавливается — не держим его.
	if r := f.hook("Stop", map[string]any{"stop_hook_active": true}); r.Stdout != "" {
		t.Fatal(r)
	}
}

// 50: занятый агент получает комментарии посреди работы.
func TestCommentsMidWork(t *testing.T) {
	f := setup(t, "claude")
	f.hook("UserPromptSubmit", nil)
	f.sendComment("поправь заголовок")
	r := f.hook("PostToolUse", map[string]any{"tool_name": "Bash"})
	if !strings.Contains(r.Stdout, "поправь заголовок") || !strings.Contains(r.Stdout, "additionalContext") {
		t.Fatal(r)
	}
}

// 49: свободный агент Codex получает комментарии очередью своей сессии.
func TestCodexQueue(t *testing.T) {
	f := setup(t, "codex")
	var got []string
	f.s.Queue = func(s core.Session, text string) error {
		got = append(got, s.ID+": "+text)
		return nil
	}
	f.sendComment("x")
	f.s.deliverCodex()
	if len(got) != 1 || !strings.HasPrefix(got[0], "s1: DUET: the owner sent 1 review comment") {
		t.Fatal(got)
	}
	// Агент занят ответом на сообщение — больше не шлём.
	f.s.deliverCodex()
	if len(got) != 1 {
		t.Fatal(got)
	}
}

func TestInside(t *testing.T) {
	cases := map[string]bool{"/p/a.md": true, "/p/sub/b.md": true, "/p/.git/x": false, "/q/a.md": false, "/p": false, "/p/.claude/m.md": false}
	for p, want := range cases {
		if inside("/p", p) != want {
			t.Error(p)
		}
	}
}

func TestApplyPatchPaths(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"command": "*** Begin Patch\n*** Update File: /p/a.md\n@@\n-x\n+y\n*** Add File: b.md\n+z\n*** End Patch"})
	got := writes(payload{ToolName: "apply_patch", ToolInput: in, Cwd: "/p"})
	if len(got) != 2 || !strings.HasSuffix(got[0], "/p/a.md") || !strings.HasSuffix(got[1], "/p/b.md") {
		t.Fatal(got)
	}
}

// 110: агент поменял файл ревью командой, пока ответ не просмотрен, — duet сразу просит вернуть.
func TestCommandEditWhileUnviewed(t *testing.T) {
	f := setup(t, "claude")
	f.hook("UserPromptSubmit", nil)
	if r := f.hook("PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": f.path}}); r.Code != 0 {
		t.Fatal(r)
	}
	os.WriteFile(f.path, []byte("two\n"), 0o644)
	f.hook("Stop", nil) // ответ появился
	f.hook("UserPromptSubmit", nil)
	os.WriteFile(f.path, []byte("two\nthree\n"), 0o644) // командой, мимо инструмента правки
	r := f.hook("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "cat >> a.md"}})
	if !strings.Contains(r.Stdout, "you changed review files with a command") {
		t.Fatal(r)
	}
}
