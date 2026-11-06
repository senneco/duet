package app

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed skill/SKILL.md
var skill []byte

// waitTimeout — сколько Claude Code держит фоновый хук, который ждёт комментариев (секунды).
const waitTimeout = 7 * 24 * 3600

// ourHook — команда хука duet (любая установленная копия duet).
var ourHook = regexp.MustCompile(`duet'? (hook|wait) (claude|codex)$`)

// install — команда duet в ~/.local/bin (DUET_BIN), скилл и хуки duet в Claude Code и Codex,
// которые есть на этой машине. Повторный запуск ставит новую версию и заменяет прежние хуки duet,
// чужие настройки не трогает.
func (cfg Config) install(stdout io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	home, _ := os.UserHomeDir()
	binDir := os.Getenv("DUET_BIN")
	if binDir == "" {
		binDir = filepath.Join(home, ".local", "bin")
	}
	target := filepath.Join(binDir, "duet")
	if t, err := filepath.EvalSymlinks(target); err != nil || t != exe {
		if err := copyExe(exe, target); err != nil {
			return err
		}
		exe = target
	}
	fmt.Fprintf(stdout, "duet: %s\n", exe)

	claudeDir, claudeSet := os.LookupEnv("CLAUDE_CONFIG_DIR")
	if !claudeSet {
		claudeDir = filepath.Join(home, ".claude")
	}
	codexDir, codexSet := os.LookupEnv("CODEX_HOME")
	if !codexSet {
		codexDir = filepath.Join(home, ".codex")
	}
	hasClaude := claudeSet || exists(claudeDir) || onPath("claude")
	hasCodex := codexSet || exists(codexDir) || onPath("codex")
	q := shellQuote(exe)

	if hasClaude {
		if err := installClaude(claudeDir, q); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Claude Code: skill and hooks installed (%s)\n", claudeDir)
	}
	if hasCodex {
		if err := installCodex(codexDir, q, exe); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Codex: skill, hooks and command rules installed (%s)\n", codexDir)
	}
	if !hasClaude && !hasCodex {
		fmt.Fprintln(stdout, "Neither Claude Code nor Codex found: install one and run `duet install` again.")
		return nil
	}

	fmt.Fprintln(stdout, "\nTell the owner:")
	if !inPath(binDir) {
		fmt.Fprintf(stdout, "- %s is not in PATH, and agents call duet by name: add it to PATH in the shell profile (offer to do it).\n", binDir)
	}
	fmt.Fprintln(stdout, "- duet works in agent sessions started from now on: start a new session.")
	if hasCodex {
		fmt.Fprintln(stdout, "- Codex asks once to trust the duet hooks: allow them.")
	}
	fmt.Fprintln(stdout, "- To use it, ask the agent to send the work for review (\"отдай на ревью\").")
	return nil
}

func installClaude(dir, q string) error {
	if err := writeFile(filepath.Join(dir, "skills", "duet", "SKILL.md"), skill); err != nil {
		return err
	}
	cmd := func(sub string) map[string]any {
		return map[string]any{"type": "command", "command": q + " " + sub + " claude"}
	}
	wait := cmd("wait")
	wait["asyncRewake"] = true
	wait["timeout"] = waitTimeout
	return editJSON(filepath.Join(dir, "settings.json"), func(s map[string]any) {
		setHooks(s, map[string][]map[string]any{
			"UserPromptSubmit": {cmd("hook")},
			"PreToolUse":       {cmd("hook")},
			"PostToolUse":      {cmd("hook")},
			"Stop":             {cmd("hook"), wait},
			"SessionEnd":       {cmd("hook")},
		})
		perms, _ := s["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			s["permissions"] = perms
		}
		allow, _ := perms["allow"].([]any)
		for _, a := range allow {
			if a == "Bash(duet:*)" {
				return
			}
		}
		perms["allow"] = append(allow, "Bash(duet:*)")
	})
}

func installCodex(dir, q, exe string) error {
	if err := writeFile(filepath.Join(dir, "skills", "duet", "SKILL.md"), skill); err != nil {
		return err
	}
	ccmd := func() map[string]any {
		return map[string]any{"type": "command", "command": q + " hook codex", "timeout": 30}
	}
	err := editJSON(filepath.Join(dir, "hooks.json"), func(s map[string]any) {
		setHooks(s, map[string][]map[string]any{
			"UserPromptSubmit": {ccmd()},
			"PreToolUse":       {ccmd()},
			"PostToolUse":      {ccmd()},
			"Stop":             {ccmd()},
			"SessionEnd":       {ccmd()},
		})
	})
	if err != nil {
		return err
	}
	// Команды duet ходят к ядру на этой машине и пишут в папку данных: их песочница Codex не пускает.
	rules := fmt.Sprintf("# duet: agent commands talk to the local duet core.\nprefix_rule(\n    pattern = [\"duet\"],\n    decision = \"allow\",\n)\nprefix_rule(\n    pattern = [%q],\n    decision = \"allow\",\n)\n", exe)
	if err := writeFile(filepath.Join(dir, "rules", "duet.rules"), []byte(rules)); err != nil {
		return err
	}
	return enableCodexHooks(filepath.Join(dir, "config.toml"))
}

// copyExe кладёт программу на место атомарно: работающее ядро старой версии не ломается.
func copyExe(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	tmp := to + ".tmp"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, to)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func inPath(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func shellQuote(s string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_./-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// editJSON меняет JSON-файл настроек. Прежний файл сохраняется рядом (.duet-backup).
func editJSON(path string, fn func(map[string]any)) error {
	s := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := os.WriteFile(path+".duet-backup", data, 0o644); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fn(s)
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// setHooks убирает прежние хуки duet и добавляет новые; чужие хуки остаются как были.
func setHooks(s map[string]any, add map[string][]map[string]any) {
	hooks, _ := s["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		s["hooks"] = hooks
	}
	for event, groups := range hooks {
		list, _ := groups.([]any)
		var keep []any
		for _, g := range list {
			gm, _ := g.(map[string]any)
			hs, _ := gm["hooks"].([]any)
			var rest []any
			for _, h := range hs {
				hm, _ := h.(map[string]any)
				if c, _ := hm["command"].(string); ourHook.MatchString(c) {
					continue
				}
				rest = append(rest, h)
			}
			if len(rest) == 0 && len(hs) > 0 {
				continue
			}
			if gm != nil {
				gm["hooks"] = rest
			}
			keep = append(keep, g)
		}
		if len(keep) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keep
		}
	}
	for event, hs := range add {
		list, _ := hooks[event].([]any)
		var group []any
		for _, h := range hs {
			group = append(group, h)
		}
		hooks[event] = append(list, map[string]any{"hooks": group})
	}
}

// enableCodexHooks включает хуки Codex, если владелец их выключил (`[features] hooks = false`).
// По умолчанию в Codex они включены, тогда файл не меняется.
func enableCodexHooks(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	lines := strings.Split(string(data), "\n")
	section, changed := "", false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			section = t
			continue
		}
		if section == "[features]" && strings.ReplaceAll(t, " ", "") == "hooks=false" {
			lines[i] = "hooks = true"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := os.WriteFile(path+".duet-backup", data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}
