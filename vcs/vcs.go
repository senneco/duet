// Package vcs собирает проверяемый снимок рабочей копии, не изменяя её.
package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const ProtocolVersion = 1
const MaxAttempts = 3

type Workspace struct {
	Provider string `json:"provider"`
	Root     string `json:"root"`
	ID       string `json:"id"`
}

type Diagnostic struct {
	Path    string `json:"path,omitempty"` // относительно корня; пусто — общая ошибка
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Change struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	PreviousPath string `json:"previousPath,omitempty"`
}

type State struct {
	Revision    string       `json:"revision"` // пусто — в рабочей копии ещё нет коммита
	Token       string       `json:"token"`    // меняется при смене коммита, списка или индекса
	Changes     []Change     `json:"changes"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

// Content различает существующий пустой текст, отсутствие и неподдерживаемый объект.
// Ошибка чтения возвращается отдельно и никогда не означает отсутствие.
type Content struct {
	Kind   string `json:"kind"` // text, absent, unsupported
	Text   string `json:"text,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Provider — общий контракт. Detect возвращает nil, nil только при достоверном
// отсутствии репозитория. Original читает именно указанную неизменяемую revision.
// Все операции только читают. Пути в State и Original относительны Root.
type Provider interface {
	Detect(context.Context, string) (*Workspace, error)
	State(context.Context, Workspace) (State, error)
	Original(context.Context, Workspace, string, string) (Content, error)
}

type Entry struct {
	Change
	InDiff      bool         `json:"-"`
	Original    Content      `json:"original"`
	Current     Content      `json:"current"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type Snapshot struct {
	Protocol    int          `json:"protocol"`
	Workspace   *Workspace   `json:"workspace,omitempty"`
	Revision    string       `json:"revision"`
	Token       string       `json:"token"`
	Entries     []Entry      `json:"entries"`
	External    []string     `json:"external,omitempty"` // материалы вне найденного корня
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
	Consistent  bool         `json:"consistent"`
	Attempts    int          `json:"attempts"`
}

func (s Snapshot) Complete() bool { return s.Consistent && len(s.Diagnostics) == 0 }

func ValidPath(path string) bool {
	return path != "" && utf8.ValidString(path) && !strings.ContainsRune(path, 0) &&
		!filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path &&
		path != "." && path != ".." && !strings.HasPrefix(path, "../")
}

// Collect объединяет обязательные изменения с дополнительными путями (файлы
// ревью и материалы агента). Относительные дополнительные пути считаются от dir.
// Два чтения содержимого окружены тремя проверками состояния. Это ограниченная
// проверка стабильности, а не блокировка чужих процессов и не постоянное наблюдение.
func Collect(ctx context.Context, p Provider, dir string, additional []string) Snapshot {
	return CollectWithMaterials(ctx, p, dir, additional, nil)
}

// CollectWithMaterials добавляет сохранённые материалы только после определения
// рабочей копии. При повторе сбора принадлежность определяется заново.
func CollectWithMaterials(ctx context.Context, p Provider, dir string, additional []string, workspaceMaterials func(Workspace) []string) Snapshot {
	out := Snapshot{Protocol: ProtocolVersion}
	fail := func(code string, err error) Snapshot {
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
			code = "timeout"
			err = ReadTimeoutError(ctx, err)
		}
		out.Diagnostics = append(out.Diagnostics, Diagnostic{Code: code, Message: err.Error()})
		out.Consistent = false
		return out
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fail("detect", err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return fail("detect", err)
	}
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		out = Snapshot{Protocol: ProtocolVersion, Attempts: attempt}
		if err := ctx.Err(); err != nil {
			return fail("cancelled", err)
		}
		w, err := p.Detect(ctx, abs)
		if err != nil {
			return fail("detect", err)
		}
		out.Workspace = w
		if err := ctx.Err(); err != nil {
			return fail("cancelled", err)
		}
		if w == nil {
			out.External = append([]string(nil), additional...)
			out.Consistent = true
			return out
		}
		if w.ID == "" || w.Provider == "" || !filepath.IsAbs(w.Root) || filepath.Clean(w.Root) != w.Root {
			return fail("contract", fmt.Errorf("неверная идентичность рабочей копии"))
		}
		state, err := p.State(ctx, *w)
		if err != nil {
			return fail("state", err)
		}
		out.Revision, out.Token = state.Revision, state.Token
		if state.Token == "" {
			return fail("contract", fmt.Errorf("отсутствует отпечаток состояния"))
		}
		changes := map[string]Change{}
		for _, ch := range state.Changes {
			if !ValidPath(ch.Path) || ch.PreviousPath != "" && !ValidPath(ch.PreviousPath) {
				return fail("contract", fmt.Errorf("неверный путь в списке изменений"))
			}
			if _, exists := changes[ch.Path]; exists {
				return fail("contract", fmt.Errorf("повтор пути %q", ch.Path))
			}
			changes[ch.Path] = ch
			// Старый путь тоже материал: его исходник и удаление не теряются.
		}
		for _, ch := range state.Changes {
			if ch.PreviousPath != "" {
				if _, exists := changes[ch.PreviousPath]; !exists {
					changes[ch.PreviousPath] = Change{Path: ch.PreviousPath, Status: "renamed-from"}
				}
			}
		}
		materials := make(map[string]bool)
		pathsToRead := additional
		if workspaceMaterials != nil {
			pathsToRead = slices.Concat(additional, workspaceMaterials(*w))
		}
		for _, path := range pathsToRead {
			if !filepath.IsAbs(path) {
				path = filepath.Join(abs, path)
			}
			rel, err := filepath.Rel(w.Root, filepath.Clean(path))
			rel = filepath.ToSlash(rel)
			if err != nil || !ValidPath(rel) {
				out.External = append(out.External, path)
				continue
			}
			if _, exists := changes[rel]; !exists {
				changes[rel] = Change{Path: rel, Status: "material"}
				materials[rel] = true
			}
		}
		paths := make([]string, 0, len(changes))
		for path := range changes {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		read := func() ([]Entry, error) {
			entries := make([]Entry, 0, len(paths))
			for _, path := range paths {
				e := Entry{Change: changes[path], InDiff: !materials[path]}
				if err := ctx.Err(); err != nil {
					return entries, err
				}
				var err error
				e.Original, err = p.Original(ctx, *w, state.Revision, path)
				if errors.Is(err, context.DeadlineExceeded) {
					return entries, err
				}
				e.check("original", e.Original, err)
				e.Current, err = readCurrent(w.Root, path)
				e.check("current", e.Current, err)
				if e.Original.Kind == "text" && e.Current.Kind == "text" && e.Original.Mode != "" && e.Original.Mode != e.Current.Mode {
					e.Diagnostics = append(e.Diagnostics, Diagnostic{Path: path, Code: "unsupported-mode", Message: "изменение режима файла не поддерживается текстовым ревью"})
				}
				entries = append(entries, e)
			}
			return entries, ctx.Err()
		}
		first, err := read()
		if err != nil {
			return fail("original", err)
		}
		middle, err := p.State(ctx, *w)
		if err != nil {
			out.Entries = first
			return fail("state", err)
		}
		second, err := read()
		if err != nil {
			return fail("original", err)
		}
		last, err := p.State(ctx, *w)
		if err != nil {
			out.Entries = second
			return fail("state", err)
		}
		finalWorkspace, err := p.Detect(ctx, abs)
		if err != nil {
			return fail("detect", err)
		}
		if err := ctx.Err(); err != nil {
			return fail("cancelled", err)
		}
		out.Entries = second
		out.Diagnostics = append(out.Diagnostics, state.Diagnostics...)
		for _, e := range second {
			out.Diagnostics = append(out.Diagnostics, e.Diagnostics...)
		}
		if reflect.DeepEqual(w, finalWorkspace) && reflect.DeepEqual(state, middle) && reflect.DeepEqual(state, last) && reflect.DeepEqual(first, second) {
			out.Consistent = true
			return out
		}
		if ctx.Err() != nil {
			return fail("cancelled", ctx.Err())
		}
	}
	return fail("changed", fmt.Errorf("рабочая копия менялась во время %d попыток сбора", MaxAttempts))
}

func (e *Entry) check(stage string, content Content, err error) {
	if err != nil {
		e.Diagnostics = append(e.Diagnostics, Diagnostic{Path: e.Path, Code: stage, Message: err.Error()})
		return
	}
	label := "Файл на диске"
	if stage == "original" {
		label = "Исходная версия файла"
	}
	switch content.Kind {
	case "text", "absent":
	case "unsupported":
		e.Diagnostics = append(e.Diagnostics, Diagnostic{Path: e.Path, Code: "unsupported", Message: label + " — " + content.Reason})
	default:
		e.Diagnostics = append(e.Diagnostics, Diagnostic{Path: e.Path, Code: "contract", Message: label + " — отсутствует результат чтения"})
	}
}

func textContent(data []byte, mode string) Content {
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return Content{Kind: "unsupported", Mode: mode, Reason: "бинарный файл или текст не в UTF-8"}
	}
	return Content{Kind: "text", Text: string(data), Mode: mode}
}

func readCurrent(root, path string) (Content, error) {
	// Не переходим по ссылкам и не обходим самостоятельные вложенные репозитории.
	parts := strings.Split(path, "/")
	current := root
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return Content{Kind: "absent"}, nil
		}
		if err != nil {
			return Content{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return Content{Kind: "unsupported", Reason: "символическая ссылка"}, nil
		}
		if info.IsDir() {
			_, err := os.Lstat(filepath.Join(current, ".git"))
			if err == nil {
				return Content{Kind: "unsupported", Reason: "вложенный репозиторий"}, nil
			}
			if !os.IsNotExist(err) {
				return Content{}, err
			}
			if i < len(parts)-1 {
				continue
			}
		}
		if !info.Mode().IsRegular() {
			return Content{Kind: "unsupported", Reason: "объект не является обычным файлом"}, nil
		}
		if i < len(parts)-1 {
			return Content{Kind: "absent"}, nil
		}
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return Content{}, err
	}
	defer r.Close()
	data, err := r.ReadFile(path)
	if err != nil {
		return Content{}, err
	}
	info, err := r.Lstat(path)
	if err != nil {
		return Content{}, err
	}
	if !info.Mode().IsRegular() {
		return Content{Kind: "unsupported", Reason: "тип объекта изменился при чтении"}, nil
	}
	mode := "100644"
	if info.Mode().Perm()&0o111 != 0 {
		mode = "100755"
	}
	return textContent(data, mode), nil
}
