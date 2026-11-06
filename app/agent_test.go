package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/senneco/duet/agent"
	"github.com/senneco/duet/core"
)

// Сквозные проверки протокола агента на настоящей программе: команды, хуки, пробуждение, живость.

type res struct {
	out, err string
	code     int
}

// agentRun — команда duet из сессии агента session в папке dir.
func (e *env) agentRun(dir, session, stdin string, args ...string) res {
	e.t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DUET_HOME="+e.home, fmt.Sprintf("DUET_PORT=%d", e.port),
		"DUET_SESSION="+session, "DUET_AGENT=claude", fmt.Sprintf("DUET_PID=%d", os.Getpid()), "DUET_BROWSER="+e.browser())
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatal(err)
	}
	return res{out.String(), errb.String(), code}
}

// hookPayload — событие хука, как его присылает Claude Code.
func hookPayload(session, event, cwd string, extra map[string]any) string {
	m := map[string]any{"session_id": session, "hook_event_name": event, "cwd": cwd}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (e *env) hook(dir, session, event string, extra map[string]any) res {
	e.t.Helper()
	return e.agentRun(dir, session, hookPayload(session, event, dir, extra), "hook", "claude")
}

// owner — действие владельца через API, как из браузера.
func (e *env) owner(review, action string, body map[string]any) map[string]any {
	e.t.Helper()
	port := e.info().Port
	data, _ := json.Marshal(body)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/reviews/%s/%s", port, review, action), "application/json", bytes.NewReader(data))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("%s: %v", action, out)
	}
	return out
}

func (e *env) reviewState(review string) map[string]any {
	e.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/reviews/%s", e.info().Port, review))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func project(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "duet-proj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return agent.Canon(dir)
}

// 44, 49, 51, 104: отдать на ревью, разбудить свободного агента комментарием, ответить «готово».
func TestAgentRoundTrip(t *testing.T) {
	e := setup(t)
	dir := project(t)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("new text\n"), 0o644)

	// 104: хук сессии, которая не подключена, ничего не делает и ядро не запускает.
	if r := e.hook(dir, "s1", "PreToolUse", nil); r.code != 0 || r.out != "" {
		t.Fatal(r)
	}
	if _, err := os.Stat(filepath.Join(e.home, "server.json")); err == nil {
		t.Fatal("хук запустил ядро")
	}

	r := e.agentRun(dir, "s1", `{"title": "Тест", "start": "a.md", "files": [{"path": "a.md", "original": {"text": "old text\n", "source": "git, HEAD"}}]}`, "submit")
	if r.code != 0 || !strings.Contains(r.out, "/r/r1") {
		t.Fatal(r)
	}
	// 121: ревью откроется на файле, который агент отметил начальным.
	if rv := e.reviewView("r1"); rv.Start != filepath.Join(dir, "a.md") {
		t.Fatalf("начальный файл: %q", rv.Start)
	}
	if _, err := os.Stat(filepath.Join(e.home, "sessions", "s1")); err != nil {
		t.Fatal("нет пометки сессии")
	}
	// Агент закончил отвечать в чате — свободен.
	if r := e.hook(dir, "s1", "Stop", nil); r.code != 0 {
		t.Fatal(r)
	}

	// Фоновый хук ждёт; владелец отправляет комментарий — агент просыпается с текстом комментария.
	waiter := exec.Command(bin, "wait", "claude")
	waiter.Env = append(os.Environ(), "DUET_HOME="+e.home, fmt.Sprintf("DUET_PORT=%d", e.port))
	waiter.Stdin = strings.NewReader(hookPayload("s1", "Stop", dir, nil))
	var wakeMsg bytes.Buffer
	waiter.Stderr = &wakeMsg
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- waiter.Wait() }()
	time.Sleep(300 * time.Millisecond)
	path := filepath.Join(dir, "a.md")
	cid := e.owner("r1", "comment", map[string]any{"path": path, "text": "сделай короче"})["id"].(string)
	e.owner("r1", "send", nil)
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 2 {
			t.Fatalf("ожидание закончилось не пробуждением: %v", err)
		}
	case <-time.After(10 * time.Second):
		waiter.Process.Kill()
		t.Fatal("агента не разбудили")
	}
	if msg := wakeMsg.String(); !strings.Contains(msg, "DUET:") || !strings.Contains(msg, "сделай короче") || !strings.Contains(msg, "["+cid+"] a.md") {
		t.Fatal(msg)
	}

	// Агент правит файл инструментом и отвечает.
	if r := e.hook(dir, "s1", "PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": path}}); r.code != 0 {
		t.Fatal(r)
	}
	os.WriteFile(path, []byte("text\n"), 0o644)
	// 113: без ответа на комментарий закончить нельзя.
	if r := e.agentRun(dir, "s1", "", "done"); r.code != 1 || !strings.Contains(r.err, cid) {
		t.Fatal(r)
	}
	// JSON одним аргументом — так зовёт Codex.
	r = e.agentRun(dir, "s1", "", "done", fmt.Sprintf(`{"answers": [{"id": %q, "text": "сократил"}]}`, cid))
	if r.code != 0 || !strings.Contains(r.out, "Done") {
		t.Fatal(r)
	}
	if st := e.reviewState("r1"); st["hasResponse"] != true {
		t.Fatal(st)
	}
	// 110: пока ответ не просмотрен, менять файл ревью нельзя.
	r = e.hook(dir, "s1", "PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": path}})
	if r.code != 2 || !strings.Contains(r.err, "not viewed") {
		t.Fatal(r)
	}
	// 118: `duet web` из сессии открывает её ревью.
	if r := e.agentRun(dir, "s1", "", "web"); !strings.Contains(r.out, "/r/r1") {
		t.Fatal(r)
	}
}

// 53, 117: забытый файл — duet добавляет его и просит исходную версию.
func TestAgentForgottenFile(t *testing.T) {
	e := setup(t)
	dir := project(t)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("a\n"), 0o644)
	e.agentRun(dir, "s1", "", "submit", "a.md")
	b := filepath.Join(dir, "b.md")
	if r := e.hook(dir, "s1", "PreToolUse", map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": b}}); r.code != 0 {
		t.Fatal(r)
	}
	os.WriteFile(b, []byte("b\n"), 0o644)
	r := e.hook(dir, "s1", "PostToolUse", map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": b}})
	if !strings.Contains(r.out, "b.md was not in the review") {
		t.Fatal(r)
	}
	// 54: файл вне папки агента не добавляется.
	outside := filepath.Join(os.TempDir(), "duet-outside.md")
	e.hook(dir, "s1", "PreToolUse", map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": outside}})
	r = e.agentRun(dir, "s1", "", "original", "b.md", "--text", "b0\n", "--source", "copy")
	if r.code != 0 || !strings.Contains(r.out, "saved") {
		t.Fatal(r)
	}
	e.hook(dir, "s1", "Stop", nil)
	files := e.reviewState("r1")["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("%v", files)
	}
}

// 106, 107: подхватить ревью другим агентом.
func TestAgentConnect(t *testing.T) {
	e := setup(t)
	dir := project(t)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("a\n"), 0o644)
	e.agentRun(dir, "s1", "", "submit", "a.md")
	// A ещё выполняет команду (занят) — перехватить нельзя.
	if r := e.agentRun(dir, "s2", "", "connect", "http://duet.localhost:4848/r/r1"); r.code != 1 || !strings.Contains(r.err, "another agent") {
		t.Fatal(r)
	}
	e.hook(dir, "s1", "Stop", nil)
	r := e.agentRun(dir, "s2", "", "connect", "http://duet.localhost:4848/r/r1")
	if r.code != 0 || !strings.Contains(r.out, "a.md") {
		t.Fatal(r)
	}
	r = e.hook(dir, "s1", "UserPromptSubmit", nil)
	if !strings.Contains(r.out, "no longer connected") {
		t.Fatal(r)
	}
}

// 58, 108: процесс агента пропал — через несколько секунд агент не подключён. Выход — сразу.
func TestAgentLiveness(t *testing.T) {
	e := setup(t)
	dir := project(t)
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("a\n"), 0o644)
	proc := exec.Command("sleep", "60")
	proc.Start()
	cmd := exec.Command(bin, "submit", "a.md")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DUET_HOME="+e.home, fmt.Sprintf("DUET_PORT=%d", e.port), "DUET_SESSION=s1", "DUET_AGENT=claude", fmt.Sprintf("DUET_PID=%d", proc.Process.Pid))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	if e.reviewState("r1")["agent"] != true {
		t.Fatal("не подключён")
	}
	proc.Process.Kill()
	proc.Wait()
	deadline := time.Now().Add(6 * time.Second)
	for e.reviewState("r1")["agent"] == true {
		if time.Now().After(deadline) {
			t.Fatal("пропавший агент всё ещё подключён")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(e.home, "sessions", "s1")); err == nil {
		t.Fatal("пометка сессии осталась")
	}

	e.agentRun(dir, "s2", "", "submit", "--new", "a.md")
	e.hook(dir, "s2", "SessionEnd", nil)
	if e.reviewState("r2")["agent"] == true {
		t.Fatal("после выхода агент подключён")
	}
}

// 103: установка ставит скилл и хуки, не трогает чужое и не задваивается.
func TestInstall(t *testing.T) {
	e := setup(t)
	claude := filepath.Join(e.home, "claude")
	codex := filepath.Join(e.home, "codex")
	os.MkdirAll(claude, 0o755)
	os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"model": "x", "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "other"}]}]}, "permissions": {"allow": ["Bash(ls:*)"]}}`), 0o644)
	os.MkdirAll(codex, 0o755)
	os.WriteFile(filepath.Join(codex, "config.toml"), []byte("model = \"y\"\n\n[features]\nhooks = false\n"), 0o644)
	binDir := filepath.Join(e.home, "bin")
	installed := filepath.Join(binDir, "duet")
	// Первый раз — скачанной программой (она кладёт себя в binDir), второй — уже установленной.
	for _, exe := range []string{bin, installed} {
		cmd := exec.Command(exe, "install")
		cmd.Env = append(os.Environ(), "DUET_HOME="+e.home, "DUET_BIN="+binDir, "CLAUDE_CONFIG_DIR="+claude, "CODEX_HOME="+codex)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	if st, err := os.Stat(installed); err != nil || st.Mode()&0o111 == 0 {
		t.Fatal("duet is not installed", err)
	}
	if hooks, _ := os.ReadFile(filepath.Join(codex, "hooks.json")); !strings.Contains(string(hooks), installed+" hook codex") {
		t.Fatalf("codex hooks call %s", hooks)
	}
	if conf, _ := os.ReadFile(filepath.Join(codex, "config.toml")); string(conf) != "model = \"y\"\n\n[features]\nhooks = true\n" {
		t.Fatalf("codex config: %s", conf)
	}
	var s struct {
		Model       string
		Hooks       map[string][]struct{ Hooks []map[string]any }
		Permissions struct{ Allow []string }
	}
	data, _ := os.ReadFile(filepath.Join(claude, "settings.json"))
	json.Unmarshal(data, &s)
	count := func(event, cmd string) int {
		n := 0
		for _, g := range s.Hooks[event] {
			for _, h := range g.Hooks {
				if c, _ := h["command"].(string); strings.HasSuffix(c, cmd) {
					n++
				}
			}
		}
		return n
	}
	if s.Model != "x" || count("Stop", "other") != 1 || count("Stop", "hook claude") != 1 || count("Stop", "wait claude") != 1 || count("PreToolUse", "hook claude") != 1 {
		t.Fatalf("%s", data)
	}
	if strings.Join(s.Permissions.Allow, ",") != "Bash(ls:*),Bash(duet:*)" {
		t.Fatal(s.Permissions.Allow)
	}
	for _, p := range []string{filepath.Join(claude, "skills/duet/SKILL.md"), filepath.Join(codex, "skills/duet/SKILL.md"), filepath.Join(codex, "hooks.json"), filepath.Join(codex, "rules/duet.rules")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

// Агента нет на машине — его настройки не появляются.
func TestInstallOnlyPresent(t *testing.T) {
	e := setup(t)
	claude := filepath.Join(e.home, "claude")
	home := filepath.Join(e.home, "user")
	os.MkdirAll(home, 0o755)
	cmd := exec.Command(bin, "install")
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH=/usr/bin:/bin", "DUET_HOME="+e.home, "DUET_BIN="+filepath.Join(e.home, "bin"), "CLAUDE_CONFIG_DIR="+claude)
	cmd.Env = filterEnv(cmd.Env, "CODEX_HOME")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(string(out))
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
		t.Fatalf("codex settings created without codex:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(claude, "settings.json")); err != nil {
		t.Fatal(err)
	}
}

func filterEnv(env []string, drop string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, drop+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func (e *env) reviewView(id string) core.ReviewView {
	e.t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/reviews/%s", e.port, id))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var rv core.ReviewView
	if err := json.NewDecoder(resp.Body).Decode(&rv); err != nil {
		e.t.Fatal(err)
	}
	return rv
}
