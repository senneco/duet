package core

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
		f, ok := c.s.Files[p]
		if !ok || f.ReviewID != r.ID {
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
	// Чтение с диска должно вернуть ошибку, а не превратиться в «нет правок».
	subs, err := c.fromDisk(r)
	if err != nil {
		return DoneResult{}, err
	}
	if len(subs) == 0 && !r.Working {
		return DoneResult{}, nil
	}
	r.Working, r.Turn = true, true
	ok, err := c.finish(r, subs, nil)
	if err != nil {
		return DoneResult{}, err
	}
	res := DoneResult{Responded: ok}
	if r.Working {
		res.New = c.take(r)
	}
	return res, c.save()
}
