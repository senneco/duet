package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/senneco/duet/vcs"
)

func (cfg Config) plugin(args []string, out io.Writer) error {
	usage := "usage: duet plugin add NAME EXECUTABLE | remove NAME | list | check NAME DIRECTORY"
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	r := vcs.Registry{Home: cfg.Home}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch args[0] {
	case "add":
		if len(args) != 3 {
			return fmt.Errorf("%s", usage)
		}
		if err := r.Add(ctx, vcs.Executable{Name: args[1], Path: args[2]}); err != nil {
			return err
		}
		fmt.Fprintf(out, "Registered %s. Run duet plugin check %s DIRECTORY.\n", args[1], args[1])
		return nil
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("%s", usage)
		}
		return r.Remove(args[1])
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("%s", usage)
		}
		ps, err := r.List()
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(ps)
	case "check":
		if len(args) != 3 {
			return fmt.Errorf("%s", usage)
		}
		ps, err := r.List()
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.Name != args[1] {
				continue
			}
			if err := p.Check(ctx); err != nil {
				return err
			}
			dir, err := filepath.Abs(args[2])
			if err != nil {
				return err
			}
			if _, err := os.Stat(dir); err != nil {
				return err
			}
			timeout, err := vcs.ReadTimeout(cfg.Home)
			if err != nil {
				return err
			}
			readCtx, cancelRead := vcs.WithReadTimeout(context.Background(), timeout)
			defer cancelRead()
			s := vcs.Collect(readCtx, p, dir, nil)
			if err := json.NewEncoder(out).Encode(s); err != nil {
				return err
			}
			for _, diagnostic := range s.Diagnostics {
				if diagnostic.Code == "timeout" {
					return fmt.Errorf("%s", diagnostic.Message)
				}
			}
			if s.Workspace == nil || !s.Complete() {
				return fmt.Errorf("plugin check did not produce a complete repository snapshot; see diagnostics")
			}
			// Проверяем и выбор зарегистрированного провайдера.
			w, err := r.Detect(readCtx, s.Workspace.Root)
			if err != nil {
				return vcs.ReadTimeoutError(readCtx, err)
			}
			if w == nil || w.Provider != p.Name {
				return fmt.Errorf("plugin selection differs from check")
			}
			return nil
		}
		return fmt.Errorf("plugin %s is not registered", args[1])
	}
	return fmt.Errorf("%s", usage)
}
