package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/senneco/duet/vcs"
)

// Completeness — последняя проверка, а не обещание неизменности диска после неё.
type Completeness struct {
	CheckedAt time.Time           `json:"checkedAt"`
	Complete  bool                `json:"complete"`
	Workspace *vcs.Workspace      `json:"workspace,omitempty"`
	Revision  string              `json:"revision,omitempty"`
	Problems  []RepositoryProblem `json:"problems,omitempty"`
	Diff      map[string]string   `json:"diff,omitempty"`
}

type RepositoryProblem struct {
	Path     string `json:"path,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	ReviewID string `json:"reviewId,omitempty"`
}

func (c *Core) repositoryDir(r *Review) string {
	if r.RepositoryDir != "" {
		return r.RepositoryDir
	}
	if s := c.s.Sessions[r.Session]; s != nil {
		return s.Cwd
	}
	return ""
}

var errRepositoryChanged = errors.New("ревью изменилось во время проверки репозитория; повторите проверку")
var errRepositoryChecking = errors.New("проверка этого ревью уже выполняется")

// repositoryInputs освобождает блокировку на время чтения репозитория.
// Состояние ревью проверяется перед применением снимка.
func (c *Core) repositoryInputs(r *Review, additional []string) ([]Submission, error) {
	dir := c.repositoryDir(r)
	if c.disk == nil || dir == "" {
		return c.fromDisk(r)
	}
	if c.repositoryChecks == nil {
		c.repositoryChecks = make(map[string]bool)
	}
	if c.repositoryChecks[r.ID] {
		return nil, errRepositoryChecking
	}
	paths := append([]string(nil), additional...)
	paths = append(paths, r.Materials...)
	for _, f := range c.files(r) {
		paths = append(paths, f.Path)
	}
	discussions := c.discussionPaths(r)
	timeout, err := vcs.ReadTimeout(filepath.Dir(c.path))
	if err != nil {
		return nil, err
	}
	parent := context.Background()
	work := r.backgroundStop
	if work != nil && work.running {
		parent = work.ctx
	}
	ctx, cancel := vcs.WithReadTimeout(parent, timeout)
	defer cancel()
	before, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	type fileState struct{ base, current, versions int }
	fileStates := make(map[string]fileState)
	for _, f := range c.files(r) {
		fileStates[f.Path] = fileState{f.Base, f.Current, len(f.Versions)}
	}
	provider := c.provider
	c.repositoryChecks[r.ID] = true
	c.mu.Unlock()
	snap := vcs.CollectWithMaterials(ctx, provider, dir, paths, discussions)
	// Старые записи и прямые клиенты могут использовать другое имя того же корня.
	// Нормализуем только сам корень, не ссылки внутри проверяемого дерева.
	if snap.Workspace != nil {
		normalized := false
		for i, path := range paths {
			p := pathInRoot(snap.Workspace.Root, path)
			normalized = normalized || p != path
			paths[i] = p
		}
		if normalized {
			snap = vcs.CollectWithMaterials(ctx, provider, dir, paths, discussions)
		}
	}
	c.mu.Lock()
	delete(c.repositoryChecks, r.ID)
	if parent.Err() != nil {
		return nil, errRepositoryChanged
	}
	after, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if string(before) != string(after) || dir != c.repositoryDir(r) {
		return nil, errRepositoryChanged
	}
	for _, f := range c.files(r) {
		if fileStates[f.Path] != (fileState{f.Base, f.Current, len(f.Versions)}) {
			return nil, errRepositoryChanged
		}
	}
	r.RepositoryDir = dir
	aliases := map[string]string{}
	collisions := map[string]bool{}
	if snap.Workspace != nil {
		for path := range c.s.Files {
			key := pathInRoot(snap.Workspace.Root, path)
			if previous, ok := aliases[key]; ok && previous != path {
				collisions[key] = true
			} else {
				aliases[key] = path
			}
		}
	}
	storedPath := func(path string) string {
		if old, ok := aliases[path]; ok {
			return old
		}
		return path
	}

	status := &Completeness{CheckedAt: c.now(), Workspace: snap.Workspace, Revision: snap.Revision}
	r.Completeness = status
	problem := func(path, code, message, review string) {
		p := RepositoryProblem{path, code, message, review}
		for _, old := range status.Problems {
			if old == p {
				return
			}
		}
		status.Problems = append(status.Problems, p)
	}
	for _, d := range snap.Diagnostics {
		path := d.Path
		if path != "" && snap.Workspace != nil {
			path = filepath.Join(snap.Workspace.Root, filepath.FromSlash(path))
		}
		problem(storedPath(path), d.Code, d.Message, "")
	}
	// Не смешиваем версии разных состояний даже при доступной части снимка.
	if !snap.Consistent {
		if len(status.Problems) == 0 {
			problem("", "inconsistent", "не удалось получить согласованное состояние", "")
		}
		c.changed(r.ID)
		return nil, nil
	}
	var subs []Submission
	var externalErr error
	seen := map[string]bool{}
	add := func(path string, v Version) {
		if seen[path] {
			return
		}
		seen[path] = true
		f := c.s.Files[path]
		if f != nil && f.ReviewID != r.ID {
			if !isAccepted(f) || !same(f.Versions[f.Current], v) {
				problem(path, "conflict", "файл принадлежит другому ревью", f.ReviewID)
			}
			return
		}
		if f == nil {
			f = &File{Path: path, ReviewID: r.ID, Base: -1, Current: -1, BaseSource: BaseSource{Kind: "none"}}
			c.s.Files[path] = f
			r.addPath(path)
			delete(r.Moved, path)
		}
		b, ok := c.expected(r, f)
		if ok && same(b, v) {
			if r.Draft == nil {
				return
			}
			if _, ok := r.Draft.Files[path]; !ok {
				return
			}
		}
		subs = append(subs, Submission{Path: path, Content: v.Content, Deleted: v.Deleted})
	}
	blocked := r.Editing || r.Stopping
	if blocked {
		problem("", "deferred", "проверка не применена: открыт редактор или идёт остановка", "")
	}
	if snap.Workspace != nil {
		successors := map[string]bool{}
		for _, id := range r.Moved {
			successors[id] = true
		}
		w := *snap.Workspace
		status.Diff = make(map[string]string)
		for _, e := range snap.Entries {
			path := filepath.Join(w.Root, filepath.FromSlash(e.Path))
			if collisions[path] {
				problem(path, "path-alias", "один файл зарегистрирован по нескольким путям; требуется разрешить принадлежность", "")
				continue
			}
			path = storedPath(path)
			f := c.s.Files[path]
			if f != nil && f.ReviewID != r.ID {
				source := c.s.Reviews[f.ReviewID]
				// Фоновая проверка прежнего ревью не забирает файлы обратно.
				if (r.Moved[path] != "" || successors[source.ID]) && !slices.Contains(additional, path) {
					problem(path, "moved", "Ревью файла продолжается здесь", source.ID)
					continue
				}
				if blocked || len(e.Diagnostics) > 0 {
					continue
				}
				if reason := c.transferBlock(f); reason != "" {
					problem(path, "conflict", reason, source.ID)
					continue
				}
				c.moveFile(r, f)
			}
			if f != nil && unresolvedBase(f) {
				problem(path, "unknown-base", f.BaseSource.Detail, "")
			}
			if blocked {
				continue
			}
			// Неподдерживаемые объекты остаются диагностическими строками, не текстом.
			if len(e.Diagnostics) > 0 {
				continue
			}
			if f == nil {
				f = &File{Path: path, ReviewID: r.ID, Base: -1, Current: -1, BaseSource: BaseSource{Kind: "none"}}
				c.s.Files[path] = f
				r.addPath(path)
				delete(r.Moved, path)
			}
			// Сохраняем сравнение в окне ответа. Сам файл уже успешно прочитан.
			if p := r.pending(); p == nil || !p.touches(c, path) {
				c.applyRepositoryBase(f, w, snap.Revision, e)
			}
			switch {
			case e.InDiff && e.Original.Kind == "absent" && e.Current.Kind == "text":
				status.Diff[path] = "added"
			case e.InDiff:
				status.Diff[path] = "changed"
			case e.Original.Kind == e.Current.Kind && e.Original.Text == e.Current.Text:
				status.Diff[path] = "unchanged"
			}
			add(path, Version{Content: e.Current.Text, Deleted: e.Current.Kind == "absent"})
		}
	}
	// Только достоверно внешние материалы: ошибка провайдера сюда не попадает.
	for _, path := range snap.External {
		path = storedPath(path)
		if blocked || seen[path] {
			continue
		}
		d, err := c.readDisk(path)
		if err != nil {
			problem(path, "current", err.Error(), "")
			externalErr = err
			continue
		}
		if d.Deleted && c.s.Files[path] == nil {
			externalErr = fmt.Errorf("material %s does not exist; use `duet submit` for prepared files before opening the review", path)
			problem(path, "missing-material", externalErr.Error(), "")
			continue
		}
		add(path, d)
	}
	for _, f := range c.files(r) {
		if unresolvedBase(f) {
			problem(f.Path, "unknown-base", f.BaseSource.Detail, "")
		}
	}
	status.Complete = len(status.Problems) == 0
	if !status.Complete {
		r.Open = true
		c.removeAcceptedNotices(r)
	}
	c.changed(r.ID)
	return subs, externalErr
}

func (c *Core) removeAcceptedNotices(r *Review) {
	ns := r.Notices[:0]
	for _, n := range r.Notices {
		if n.Reason != NoticeAccepted {
			ns = append(ns, n)
		}
	}
	r.Notices = ns
}

// Один путь для web, done, stop и проверки перед полным принятием.
func (c *Core) finishCollected(r *Review, additional []string, answers []Answer) (DoneResult, error) {
	return c.finishCollectedTurn(r, additional, answers, true)
}

func (c *Core) finishCollectedTurn(r *Review, additional []string, answers []Answer, deliverComments bool) (DoneResult, error) {
	for _, a := range answers {
		_, cm, err := c.comment(r, a.CommentID)
		if err != nil {
			return DoneResult{}, err
		}
		if cm.Status != CommentWorking || !cm.Delivered {
			return DoneResult{}, fmt.Errorf("комментарий %s не в работе у агента: %w", cm.ID, ErrCommentStatus)
		}
	}

	subs, err := c.repositoryInputs(r, additional)
	if errors.Is(err, errRepositoryChanged) || errors.Is(err, errRepositoryChecking) {
		return DoneResult{}, err
	}
	if err != nil {
		if saveErr := c.save(); saveErr != nil {
			return DoneResult{}, saveErr
		}
		return DoneResult{}, err
	}
	if !c.allDone(r) || len(subs) > 0 {
		r.Open = true
		c.removeAcceptedNotices(r)
	}
	if r.Editing || r.Stopping {
		return DoneResult{}, c.save()
	}
	if len(subs) == 0 && (!r.Working || !r.Turn && len(answers) == 0) {
		return DoneResult{}, c.save()
	}
	r.Working, r.Turn = true, true
	ok, err := c.finish(r, subs, answers)
	if err != nil {
		return DoneResult{}, err
	}
	res := DoneResult{Responded: ok}
	if r.Working && deliverComments {
		res.New = c.take(r)
	}
	return res, c.save()
}

// RecheckRepository — явный повтор после исправления причины, без смены подключения.
func (c *Core) RecheckRepository(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(id)
	if err != nil {
		return err
	}
	if r.Working || r.Busy {
		return ErrAgentWorking
	}
	if r.Editing {
		return ErrEditing
	}
	if r.Stopping {
		return ErrStopping
	}
	if _, err = c.finishCollected(r, nil, nil); err != nil {
		return err
	}
	return c.commit(r)
}

// RecoverRepository moves confirmed conflicts into the current review without changing its agent.
func (c *Core) RecoverRepository(id, sourceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(id)
	if err != nil {
		return err
	}
	source, err := c.review(sourceID)
	if err != nil {
		return err
	}
	if source == r || r.Completeness == nil || source.Completeness == nil ||
		r.Completeness.Workspace == nil || source.Completeness.Workspace == nil ||
		*r.Completeness.Workspace != *source.Completeness.Workspace {
		return errors.New("ревью должны принадлежать одной проверенной рабочей копии")
	}
	for _, review := range []*Review{r, source} {
		if review.Editing {
			return ErrEditing
		}
		if review.Stopping {
			return ErrStopping
		}
		if review.Busy || review.Working {
			return ErrAgentWorking
		}
		if c.repositoryChecks[review.ID] {
			return errRepositoryChecking
		}
	}
	var paths []string
	for _, problem := range r.Completeness.Problems {
		if problem.Code != "conflict" || problem.ReviewID != source.ID {
			continue
		}
		f := c.s.Files[problem.Path]
		if f == nil || f.ReviewID != source.ID {
			return errRepositoryChanged
		}
		if reason := c.transferBlock(f); reason != "" {
			return errors.New(reason)
		}
		paths = append(paths, f.Path)
	}
	if len(paths) == 0 {
		return errors.New("нет конфликтующих файлов для переноса")
	}
	for _, path := range paths {
		c.track(r, path)
	}
	c.changed(r.ID)
	if err := c.save(); err != nil {
		return err
	}
	if _, err := c.finishCollected(r, nil, nil); err != nil {
		return err
	}
	return c.commit(r)
}

func (c *Core) pathProblems(r *Review, path string) []RepositoryProblem {
	var out []RepositoryProblem
	if r.Completeness != nil {
		for _, p := range r.Completeness.Problems {
			if p.Path == path {
				out = append(out, p)
			}
		}
	}
	if f := c.s.Files[path]; f != nil && f.ReviewID == r.ID && unresolvedBase(f) {
		found := false
		for _, p := range out {
			if p.Code == "unknown-base" {
				found = true
			}
		}
		if !found {
			out = append(out, RepositoryProblem{Path: path, Code: "unknown-base", Message: f.BaseSource.Detail})
		}
	}
	return out
}
func (c *Core) problemView(r *Review, path string) (FileView, bool) {
	if r.Moved[path] != "" {
		return FileView{}, false
	}
	if f := c.s.Files[path]; f != nil && f.ReviewID == r.ID {
		return FileView{}, false
	}
	ps := c.pathProblems(r, path)
	return FileView{Path: path, State: StateNew, ProblemOnly: true, Problems: ps}, len(ps) > 0
}
func (c *Core) addProblemViews(r *Review, rv *ReviewView) {
	seen := map[string]bool{}
	for _, f := range rv.Files {
		seen[f.Path] = true
	}
	if r.Completeness == nil {
		return
	}
	for _, p := range r.Completeness.Problems {
		if p.Path == "" || seen[p.Path] {
			continue
		}
		if v, ok := c.problemView(r, p.Path); ok {
			rv.Files = append(rv.Files, v)
		} else {
			v := c.view(r, p.Path)
			v.ProblemOnly = v.Absent && !v.Ripple
			rv.Files = append(rv.Files, v)
		}
		seen[p.Path] = true
	}
}

func (c *Core) repositoryConflict(r *Review, path, other string) error {
	return c.recordRepositoryConflict(r, path, other, "файл принадлежит другому ревью")
}

func (c *Core) recordRepositoryConflict(r *Review, path, other, reason string) error {
	if !slices.Contains(r.Materials, path) {
		r.Materials = append(r.Materials, path)
	}
	if r.Completeness == nil {
		r.Completeness = &Completeness{}
	}
	r.Completeness.Complete = false
	problem := RepositoryProblem{Path: path, Code: "conflict", Message: reason, ReviewID: other}
	if !slices.Contains(r.Completeness.Problems, problem) {
		r.Completeness.Problems = append(r.Completeness.Problems, problem)
	}
	r.Open = true
	c.removeAcceptedNotices(r)
	c.changed(r.ID)
	if err := c.save(); err != nil {
		return err
	}
	return fmt.Errorf("файл %s, ревью %s: %s", path, other, reason)
}

// Одинаковая защита для обеих точек выдачи сообщений.
func (c *Core) validateAcceptedNotice(r *Review) error {
	for _, n := range r.Notices {
		if n.Reason == NoticeAccepted {
			if _, err := c.finishCollected(r, nil, nil); err != nil {
				return err
			}
			if !c.allDone(r) || len(c.drifted(r)) > 0 {
				c.removeAcceptedNotices(r)
				r.Open = true
				return c.save()
			}
			return nil
		}
	}
	return nil
}

// pathInRoot сохраняет относительный путь внутри корня, включая ссылки как объекты.
func pathInRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if canonical, err := filepath.EvalSymlinks(parent); err == nil && canonical == root {
			rel, err := filepath.Rel(parent, path)
			if err == nil {
				return filepath.Join(root, rel)
			}
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return path
}

func (c *Core) invalidateRepositoryDiff(r *Review, path string) {
	if r != nil && r.Completeness != nil {
		delete(r.Completeness.Diff, path)
	}
}

func completenessView(in *Completeness) *Completeness {
	if in == nil {
		return nil
	}
	out := *in
	out.Diff = nil
	out.Problems = append([]RepositoryProblem(nil), in.Problems...)
	if in.Workspace != nil {
		w := *in.Workspace
		out.Workspace = &w
	}
	return &out
}
