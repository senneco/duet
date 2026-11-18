package vcs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Git — встроенный провайдер того же контракта, что и будущие внешние плагины.
type Git struct{}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	prefix := []string{"--no-optional-locks", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	// Окружение вызывающего процесса не должно подменить рабочую копию или индекс.
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") && !strings.HasPrefix(value, "LC_ALL=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return data, nil
}

func (Git) Detect(ctx context.Context, dir string) (*Workspace, error) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("каталог запуска не является каталогом")
	}
	for {
		_, err := os.Lstat(filepath.Join(root, ".git"))
		if err == nil {
			data, err := git(ctx, root, "rev-parse", "--show-toplevel")
			if err != nil {
				return nil, err
			} // повреждённый .git — ошибка, не отсутствие
			// git добавляет ровно один перевод строки; пробелы и переводы внутри имени допустимы.
			actual, err := filepath.EvalSymlinks(strings.TrimSuffix(string(data), "\n"))
			if err != nil {
				return nil, err
			}
			if actual != root {
				return nil, fmt.Errorf("git вернул другой корень рабочей копии")
			}
			return &Workspace{Provider: "git", Root: root, ID: "git:" + root}, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, nil
		}
		root = parent
	}
}

func revision(ctx context.Context, w Workspace) (string, error) {
	data, headErr := git(ctx, w.Root, "rev-parse", "--verify", "HEAD^{commit}")
	if headErr == nil {
		return strings.TrimSpace(string(data)), nil
	}
	ref, err := git(ctx, w.Root, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return "", headErr
	}
	_, err = git(ctx, w.Root, "show-ref", "--verify", "--quiet", strings.TrimSpace(string(ref)))
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	} // ветка без первого коммита
	return "", headErr
}

func (Git) State(ctx context.Context, w Workspace) (State, error) {
	rev, err := revision(ctx, w)
	if err != nil {
		return State{}, err
	}
	status, err := git(ctx, w.Root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none", "--renames")
	if err != nil {
		return State{}, err
	}
	index, err := git(ctx, w.Root, "ls-files", "--stage", "-v", "-z")
	if err != nil {
		return State{}, err
	}
	state := State{Revision: rev, Token: fmt.Sprintf("%x", sha256.Sum256(append(append([]byte(rev+"\x00"), status...), index...)))}
	records := strings.Split(string(status), "\x00")
	for i := 0; i < len(records)-1; i++ {
		line := records[i]
		if len(line) < 4 || line[2] != ' ' {
			return State{}, fmt.Errorf("неверная запись git status")
		}
		ch := Change{Path: strings.TrimSuffix(line[3:], "/"), Status: line[:2]}
		if strings.ContainsAny(ch.Status, "RC") {
			i++
			if i >= len(records)-1 {
				return State{}, fmt.Errorf("неполная запись переименования git")
			}
			ch.PreviousPath = records[i]
		}
		if ch.Status == "UU" || ch.Status == "AA" || ch.Status == "DD" || strings.Contains(ch.Status, "U") {
			state.Diagnostics = append(state.Diagnostics, Diagnostic{Path: ch.Path, Code: "conflict", Message: "неразрешённый конфликт git"})
		}
		state.Changes = append(state.Changes, ch)
	}
	// Такие флаги скрывают изменения от status. Не обещаем полный набор молча.
	for _, record := range strings.Split(string(index), "\x00") {
		if record == "" {
			continue
		}
		header, path, ok := strings.Cut(record, "\t")
		if !ok || len(header) < 2 {
			return State{}, fmt.Errorf("неверная запись индекса git")
		}
		tag := header[0]
		if tag == 'S' || tag >= 'a' && tag <= 'z' {
			state.Diagnostics = append(state.Diagnostics, Diagnostic{Path: path, Code: "hidden-index", Message: "флаг индекса скрывает возможные изменения рабочей копии"})
			found := false
			for _, ch := range state.Changes {
				if ch.Path == path {
					found = true
					break
				}
			}
			if !found {
				state.Changes = append(state.Changes, Change{Path: path, Status: "hidden-index"})
			}
		}
	}
	sort.Slice(state.Changes, func(i, j int) bool { return state.Changes[i].Path < state.Changes[j].Path })
	return state, nil
}

func (Git) Original(ctx context.Context, w Workspace, rev, path string) (Content, error) {
	if !ValidPath(path) {
		return Content{}, fmt.Errorf("неверный путь исходника")
	}
	if rev == "" {
		return Content{Kind: "absent"}, nil
	}
	// ls-tree отличает отсутствие пути от ошибки объекта. cat-file не запускает
	// внешние фильтры и получает blob по идентификатору из зафиксированного дерева.
	data, err := git(ctx, w.Root, "ls-tree", "-z", rev, "--", path)
	if err != nil {
		return Content{}, err
	}
	if len(data) == 0 {
		return Content{Kind: "absent"}, nil
	}
	header, found, ok := strings.Cut(strings.TrimSuffix(string(data), "\x00"), "\t")
	fields := strings.Fields(header)
	if !ok || found != path || len(fields) != 3 {
		return Content{}, fmt.Errorf("неверный ответ git ls-tree")
	}
	mode := fields[0]
	if mode != "100644" && mode != "100755" {
		return Content{Kind: "unsupported", Mode: mode, Reason: "тип объекта git " + mode}, nil
	}
	data, err = git(ctx, w.Root, "cat-file", "blob", fields[2])
	if err != nil {
		return Content{}, err
	}
	return textContent(data, mode), nil
}
