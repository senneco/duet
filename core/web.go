package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReviewMatch — найденное ревью без содержимого файлов. Поиск ничего не меняет.
type ReviewMatch struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Paths []string `json:"paths"`
}

// FindReviews ищет по точным путям; без путей показывает кандидатов внутри cwd.
// Переехавшие файлы принадлежат только новому ревью. Время создания не влияет на выбор.
func (c *Core) FindReviews(paths []string, cwd string) []ReviewMatch {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	out := []ReviewMatch{}
	for _, r := range c.s.Reviews {
		m := ReviewMatch{ID: r.ID, Title: r.Title}
		for _, f := range c.files(r) {
			match := want[f.Path]
			if len(paths) == 0 && cwd != "" {
				p, err := filepath.Rel(cwd, f.Path)
				match = err == nil && p != ".." && !strings.HasPrefix(p, ".."+string(filepath.Separator))
			}
			if match {
				m.Paths = append(m.Paths, f.Path)
			}
		}
		if len(m.Paths) > 0 {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PrepareWeb подключает сессию и передаёт изменения с диска до открытия браузера.
// Проверки и передача выполняются под одной блокировкой: поиск мог устареть,
// файлы могли переехать, а владелец — начать правку или прервать работу.
func (c *Core) PrepareWeb(s Session, reviewID string, paths []string) (DoneResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return DoneResult{}, err
	}
	for _, p := range paths {
		if f := c.s.Files[p]; f != nil && f.ReviewID != r.ID {
			return DoneResult{}, c.repositoryConflict(r, p, f.ReviewID)
		}
		if c.s.Files[p] == nil && c.disk != nil && s.Cwd != "" {
			if _, statErr := os.Lstat(p); os.IsNotExist(statErr) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				w, detectErr := c.provider.Detect(ctx, s.Cwd)
				cancel()
				rel := ""
				if w != nil {
					rel, _ = filepath.Rel(w.Root, p)
				}
				if detectErr == nil && (w == nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
					return DoneResult{}, fmt.Errorf("material %s does not exist; use `duet submit` for prepared files before opening the review", p)
				}
			}
		}
		if c.s.Files[p] == nil && (c.disk == nil || s.Cwd == "") {
			return DoneResult{}, fmt.Errorf("file %s is not in review %s; submit all task files before opening it", p, r.ID)
		}
	}
	if r.Stopping {
		return DoneResult{}, ErrStopping
	}
	if r.Editing {
		return DoneResult{}, ErrEditing
	}
	if err := c.attach(s, r); err != nil {
		return DoneResult{}, err
	}
	if un := c.unanswered(r, nil); len(un) > 0 {
		return DoneResult{}, &UnansweredError{un}
	}
	if s.Cwd != "" {
		r.RepositoryDir = s.Cwd
	}
	for _, p := range paths {
		found := false
		for _, old := range r.Materials {
			if old == p {
				found = true
			}
		}
		if !found {
			r.Materials = append(r.Materials, p)
		}
	}
	return c.finishCollected(r, paths, nil)
}
