package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/senneco/duet/core"
)

// HookReq — хук Claude Code или Codex: событие как его прислал агент.
type HookReq struct {
	Agent   string          `json:"agent"`
	Payload json.RawMessage `json:"payload"`
}

// HookResp — что хук выводит: Stdout (JSON для агента), Stderr и код выхода.
type HookResp struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
}

type payload struct {
	SessionID      string          `json:"session_id"`
	Event          string          `json:"hook_event_name"`
	Cwd            string          `json:"cwd"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	StopHookActive bool            `json:"stop_hook_active"`
}

func hookContext(event, text string) string {
	out, _ := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": event, "additionalContext": text}})
	return string(out)
}

// Hook — событие сессии агента. Сессии, которые duet не знает, сюда обычно не доходят:
// хук выходит сам по отсутствию пометки.
func (s *Service) Hook(q HookReq) HookResp {
	var p payload
	if json.Unmarshal(q.Payload, &p) != nil || p.SessionID == "" {
		return HookResp{}
	}
	id := p.SessionID
	if p.Event == "SessionEnd" {
		s.core.Detach(id)
		s.syncMarkers()
		return HookResp{}
	}
	se, ok := s.core.SessionInfo(id)
	if !ok {
		s.syncMarkers()
		return HookResp{}
	}
	if se.Review == "" { // отключили перехватом: сказать при первой возможности, не будя
		if p.Event == "UserPromptSubmit" || p.Event == "PostToolUse" {
			k := s.core.TakeKicked(id)
			s.syncMarkers()
			if k != "" {
				return HookResp{Stdout: hookContext(p.Event, kicked(k))}
			}
		}
		return HookResp{}
	}
	cwd := se.Cwd
	switch p.Event {
	case "UserPromptSubmit":
		s.core.SetBusy(id, true)
		return s.deliverNow(id, cwd, p.Event, nil)
	case "PreToolUse":
		s.core.SetBusy(id, true)
		for _, path := range writes(p) {
			track := inside(cwd, path)
			if err := s.core.WriteCheck(id, path, track); err != nil {
				if errorsIs(err, core.ErrNotConnected) {
					return HookResp{}
				}
				return HookResp{Stderr: denied(cwd, err), Code: 2}
			}
		}
		if err := s.core.WriteCheck(id, "", false); errorsIs(err, core.ErrStopping) {
			return HookResp{Stderr: denied(cwd, err), Code: 2}
		}
		return HookResp{}
	case "PostToolUse":
		var extra []string
		for _, path := range writes(p) {
			if s.core.AskOriginal(id, path) {
				extra = append(extra, askOriginal(cwd, path))
			}
		}
		if bad, why := s.core.Violations(id); len(bad) > 0 {
			extra = append(extra, violation(cwd, bad, why))
		}
		return s.deliverNow(id, cwd, p.Event, extra)
	case "Stop":
		res, err := s.core.Stop(id, p.StopHookActive)
		if err != nil || len(res.Unanswered) == 0 {
			return HookResp{}
		}
		out, _ := json.Marshal(map[string]string{"decision": "block", "reason": unanswered(cwd, res.Unanswered)})
		return HookResp{Stdout: string(out)}
	}
	return HookResp{}
}

// deliverNow — занятый агент получает комментарии и сообщения посреди работы.
func (s *Service) deliverNow(id, cwd, event string, extra []string) HookResp {
	cms, ns, err := s.core.Deliver(id)
	if err != nil {
		return HookResp{}
	}
	text := delivery(cwd, cms, ns)
	if text != "" {
		extra = append(extra, text)
	}
	if len(extra) == 0 {
		return HookResp{}
	}
	return HookResp{Stdout: hookContext(event, strings.Join(extra, "\n\n"))}
}

var patchFile = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+)$|^\*\*\* Move to: (.+)$`)

// writes — файлы, которые инструмент правки собирается записать. Команды сюда не попадают.
func writes(p payload) []string {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
		Input        string `json:"input"`
	}
	json.Unmarshal(p.ToolInput, &in)
	var raw []string
	switch p.ToolName {
	case "Write", "Edit", "MultiEdit":
		raw = append(raw, in.FilePath)
	case "NotebookEdit":
		raw = append(raw, in.NotebookPath)
	case "apply_patch":
		patch := in.Command
		if patch == "" {
			patch = in.Input
		}
		if patch == "" {
			var s string
			if json.Unmarshal(p.ToolInput, &s) == nil {
				patch = s
			}
		}
		for _, m := range patchFile.FindAllStringSubmatch(patch, -1) {
			raw = append(raw, m[1]+m[2])
		}
	}
	var out []string
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !filepath.IsAbs(r) {
			r = filepath.Join(p.Cwd, r)
		}
		out = append(out, Canon(r))
	}
	return out
}

// inside — файл в папке агента, и duet может сам добавить его в ревью. Служебные папки
// (.git, папки агентов) и случай, когда агент запущен прямо в домашней папке, не считаются.
func inside(cwd, path string) bool {
	if cwd == "" {
		return false
	}
	if home, err := os.UserHomeDir(); err == nil && Canon(home) == cwd {
		return false
	}
	r, err := filepath.Rel(cwd, path)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return false
	}
	for _, part := range strings.Split(r, string(filepath.Separator)) {
		switch part {
		case ".git", ".claude", ".codex", "node_modules":
			return false
		}
	}
	return true
}

// Canon — абсолютный путь без символических ссылок (/tmp и /private/tmp — один файл).
// Файла может ещё не быть: раскрывается самая длинная существующая часть.
func Canon(p string) string {
	p, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	rest := ""
	cur := p
	for {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
