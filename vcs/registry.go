package vcs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Registry перечитывает регистрацию при сборе: перезапуск сервера не нужен.
type Registry struct{ Home string }

var pluginName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func validName(name string) bool { return pluginName.MatchString(name) && name != "git" }
func (r Registry) dir() string   { return filepath.Join(r.Home, "plugins") }
func (r Registry) List() ([]Executable, error) {
	entries, err := os.ReadDir(r.dir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Executable
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.dir(), e.Name()))
		if err != nil {
			return nil, err
		}
		var p Executable
		if err := decode(data, &p); err != nil {
			return nil, fmt.Errorf("plugin registration %s: %w", e.Name(), err)
		}
		if !validName(p.Name) || e.Name() != p.Name+".json" || !filepath.IsAbs(p.Path) {
			return nil, fmt.Errorf("invalid plugin registration %s", e.Name())
		}
		out = append(out, p)
	}
	return out, nil
}
func (r Registry) Add(ctx context.Context, p Executable) error {
	if !validName(p.Name) {
		return fmt.Errorf("invalid plugin name (git is reserved)")
	}
	path, err := filepath.Abs(p.Path)
	if err != nil {
		return err
	}
	p.Path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	if err := p.Check(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(r.dir(), 0700); err != nil {
		return err
	}
	// Публикуем целый файл атомарно, не заменяя существующую регистрацию.
	f, err := os.CreateTemp(r.dir(), ".registration-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(p)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(f.Name(), filepath.Join(r.dir(), p.Name+".json"))
}
func (r Registry) Remove(name string) error {
	if !validName(name) {
		return fmt.Errorf("invalid plugin name")
	}
	return os.Remove(filepath.Join(r.dir(), name+".json"))
}
func (r Registry) find(name string) (Provider, error) {
	if name == "git" {
		return Git{}, nil
	}
	ps, err := r.List()
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.Name == name {
			return p, nil
		}
	}
	return nil, fmt.Errorf("plugin %s is no longer registered", name)
}
func (r Registry) Detect(ctx context.Context, dir string) (*Workspace, error) {
	ps, err := r.List()
	if err != nil {
		return nil, err
	}
	var found *Workspace
	for _, p := range ps {
		w, err := p.Detect(ctx, dir)
		if err != nil {
			return nil, err
		}
		if w == nil {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("multiple plugins recognize directory: %s, %s", found.Provider, w.Provider)
		}
		found = w
	}
	if found != nil {
		return found, nil
	}
	return (Git{}).Detect(ctx, dir)
}
func (r Registry) State(ctx context.Context, w Workspace) (State, error) {
	p, err := r.find(w.Provider)
	if err != nil {
		return State{}, err
	}
	return p.State(ctx, w)
}
func (r Registry) Original(ctx context.Context, w Workspace, revision, path string) (Content, error) {
	p, err := r.find(w.Provider)
	if err != nil {
		return Content{}, err
	}
	return p.Original(ctx, w, revision, path)
}
