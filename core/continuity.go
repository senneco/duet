package core

import (
	"path/filepath"
	"slices"

	"github.com/senneco/duet/vcs"
)

// Файлы с незавершённым обсуждением входят в ревью своей рабочей копии,
// даже когда после обновления PR они уже совпадают с коммитом.
func (c *Core) discussionPaths(target *Review) func(vcs.Workspace) []string {
	// Старые ревью могли появиться до первого сбора репозитория.
	// Под блокировкой копируем только ключи. Проверяем пути после Detect.
	type material struct {
		path      string
		workspace vcs.Workspace
	}
	var materials []material
	for _, source := range c.s.Reviews {
		if source == target {
			continue
		}
		var workspace vcs.Workspace
		if check := source.Completeness; check != nil && check.Workspace != nil {
			workspace = *check.Workspace
		}
		pending := source.pending()
		for _, f := range c.files(source) {
			if target.Moved[f.Path] != "" {
				continue
			}
			open := slices.ContainsFunc(f.Comments, func(cm *Comment) bool {
				return cm.Status != CommentResolved && cm.Status != CommentDeleted
			})
			if open || pending != nil && pending.touches(c, f.Path) {
				materials = append(materials, material{f.Path, workspace})
			}
		}
	}
	return func(w vcs.Workspace) []string {
		var paths []string
		for _, m := range materials {
			if m.workspace.ID != "" && m.workspace != w {
				continue
			}
			path := pathInRoot(w.Root, m.path)
			if rel, err := filepath.Rel(w.Root, path); err == nil && vcs.ValidPath(filepath.ToSlash(rel)) {
				paths = append(paths, path)
			}
		}
		return paths
	}
}

// Вызывается под блокировкой непосредственно перед переносом.
func (c *Core) transferBlock(f *File) string {
	source := c.s.Reviews[f.ReviewID]
	switch {
	case source.Editing:
		return "закончите правку в открытом редакторе прежнего ревью"
	case source.Stopping:
		return "дождитесь остановки агента в прежнем ревью"
	case source.Busy || source.Working || source.Draft != nil:
		return "агент другого ревью работает. Дождитесь завершения его работы"
	case c.repositoryChecks[source.ID]:
		return "в прежнем ревью идёт проверка файлов. Повторите после её завершения"
	}
	return ""
}

// Номера ответов локальны для ревью. Черновик и ответы перед последним
// непросмотренным ответом могут появиться в любом порядке.
func (r *Review) nextResponseID() int {
	id := 0
	if r.Draft != nil {
		id = r.Draft.ID
	}
	for _, p := range r.Responses {
		id = max(id, p.ID)
	}
	return id + 1
}

// Для закрытых ответов сохраняем историю источника и собираем части одного
// ответа под общим номером в новом ревью. Непросмотренные части объединяются
// в обычном окне ответа, по которому владелец сможет принять решение здесь.
func (r *Review) transferredResponse(source *Review, from *Response) *Response {
	originReview, originID := from.OriginReview, from.OriginID
	if originReview == "" {
		originReview, originID = source.ID, from.ID
	}
	pending := r.pending()
	if from.Status == ResponsePending {
		if pending != nil {
			return pending
		}
	} else {
		for _, p := range r.Responses {
			if (p.OriginReview == originReview && p.OriginID == originID) ||
				(r.ID == originReview && p.ID == originID && p.Status == from.Status) {
				return p
			}
		}
	}
	p := &Response{ID: r.nextResponseID(), Status: from.Status, At: from.At,
		Files: map[string]int{}, Answers: map[string]Answer{}}
	if from.Status != ResponsePending {
		p.OriginReview, p.OriginID = originReview, originID
	}
	if pending == nil {
		r.Responses = append(r.Responses, p)
	} else {
		r.Responses = slices.Insert(r.Responses, len(r.Responses)-1, p)
	}
	return p
}

func (c *Core) moveFile(r *Review, f *File) {
	source := c.s.Reviews[f.ReviewID]
	ids := map[int]int{}
	for _, from := range source.Responses {
		if !from.touches(c, f.Path) {
			continue
		}
		to := r.transferredResponse(source, from)
		ids[from.ID] = to.ID
		if from.Status == ResponsePending {
			to.At = c.now()
			from.At = to.At
		}
		if n, ok := from.Files[f.Path]; ok {
			to.Files[f.Path] = n
			if from.Status == ResponsePending {
				delete(from.Files, f.Path)
			}
		}
		for _, cm := range f.Comments {
			if answer, ok := from.Answers[cm.ID]; ok {
				if to.Answers == nil {
					to.Answers = map[string]Answer{}
				}
				to.Answers[cm.ID] = answer
				if from.Status == ResponsePending {
					delete(from.Answers, cm.ID)
				}
			}
		}
		if from.Status == ResponsePending && len(from.Files) == 0 && len(from.Answers) == 0 {
			from.Status = ResponseMoved
		}
	}
	for _, cm := range f.Comments {
		cm.InResponse = ids[cm.InResponse]
		for i := range cm.Thread {
			cm.Thread[i].Response = ids[cm.Thread[i].Response]
		}
	}
	if source.Moved == nil {
		source.Moved = map[string]string{}
	}
	source.Moved[f.Path] = r.ID
	f.ReviewID = r.ID
	r.addPath(f.Path)
	delete(r.Moved, f.Path)
	r.NotAdded = slices.DeleteFunc(r.NotAdded, func(b Busy) bool { return b.Path == f.Path })
	c.removeAcceptedNotices(source)
	if check := source.Completeness; check != nil {
		check.Complete = false
		p := RepositoryProblem{Path: f.Path, Code: "moved", Message: "Ревью файла продолжается здесь", ReviewID: r.ID}
		if !slices.Contains(check.Problems, p) {
			check.Problems = append(check.Problems, p)
		}
	}
	c.changed(source.ID)
}

// Старая проверка могла сохраниться вместе с баннером. Снимаем только причину,
// для которой восстановлен исходный текст. Остальные ошибки проверки сохраняются.
func (c *Core) clearLegacyWarnings() {
	for _, r := range c.s.Reviews {
		check := r.Completeness
		if check == nil || len(check.Problems) == 0 {
			continue
		}
		check.Problems = slices.DeleteFunc(check.Problems, func(p RepositoryProblem) bool {
			f := c.s.Files[p.Path]
			return p.Code == "unknown-base" && f != nil && !unresolvedBase(f)
		})
		if len(check.Problems) == 0 {
			check.Complete = !check.CheckedAt.IsZero()
		}
	}
}
