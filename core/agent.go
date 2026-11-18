package core

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrOtherAgent — к ревью подключён другой агент, и он сейчас занят: перехватить нельзя.
	ErrOtherAgent = errors.New("в ревью работает другой агент")
	// ErrNotConnected — сессия агента не подключена ни к какому ревью.
	ErrNotConnected = errors.New("сессия не подключена к ревью")
	// ErrHasBase — у файла уже есть база, исходная версия не нужна.
	ErrHasBase = errors.New("у файла уже есть база")
)

// Session — сессия агента (Claude Code, Codex), о которой знает duet.
type Session struct {
	ID    string `json:"id"`
	Agent string `json:"agent"` // claude, codex
	// Review — ревью, к которому подключена сессия; "" — отключена перехватом (см. Kicked).
	Review string `json:"review,omitempty"`
	// PIDs — процессы агента (его окна): пропали все — сессия закрыта. Пусто — не знаем.
	PIDs []int  `json:"pids,omitempty"`
	Cwd  string `json:"cwd,omitempty"` // папка, где запущен агент
	// Env — то, что нужно, чтобы достучаться до сессии (например, CODEX_HOME).
	Env map[string]string `json:"env,omitempty"`
	// Kicked — ревью, от которого сессию отключил другой агент; агент ещё не знает об этом.
	Kicked string `json:"kicked,omitempty"`
	// Asked — файлы, для которых duet уже попросил исходную версию.
	Asked []string `json:"asked,omitempty"`
}

// Block — блок файла, размеченный агентом для навигации.
type Block struct {
	Title string `json:"title"`
	Level string `json:"level"` // top (⇈), high (↑), low (↓)
	At    string `json:"at"`    // строка, с которой блок начинается
}

// Attach подключает сессию к ревью. Прежнее ревью этой сессии отключается.
// Если к ревью подключена другая живая сессия и она свободна, ревью переходит к новой,
// а старая узнает об этом при следующей возможности. Если она занята — ErrOtherAgent.
func (c *Core) Attach(s Session, reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	return c.attach(s, r)
}

func (c *Core) attach(s Session, r *Review) error {
	if r.Agent && r.Session != s.ID {
		if r.Working || r.Busy {
			return ErrOtherAgent
		}
		if old, ok := c.s.Sessions[r.Session]; ok {
			c.disconnect(r)
			old.Review, old.Kicked = "", r.ID
			c.s.Sessions[old.ID] = old
		} else {
			c.disconnect(r)
		}
		r.Notices = nil // новый агент получит ревью целиком, старые сообщения ему ни к чему
	}
	if cur, ok := c.s.Sessions[s.ID]; ok && cur.Review != "" && cur.Review != r.ID {
		if prev, ok := c.s.Reviews[cur.Review]; ok {
			c.disconnect(prev)
			if prev.Stopping {
				c.restore(prev)
			}
			c.commit(prev)
		}
	}
	if cur, ok := c.s.Sessions[s.ID]; ok && cur.Review == r.ID {
		s.Asked = cur.Asked
	}
	if cur, ok := c.s.Sessions[s.ID]; ok && len(s.PIDs) == 0 {
		s.PIDs = cur.PIDs
	}
	s.Review, s.Kicked = r.ID, ""
	c.s.Sessions[s.ID] = &s
	r.Agent, r.Session = true, s.ID
	r.Busy = true // агент сейчас выполняет команду
	return c.commit(r)
}

// Detach — сессия закрыта (выход, /clear, процесс пропал).
func (c *Core) Detach(sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.s.Sessions[sessionID]
	if !ok {
		return nil
	}
	delete(c.s.Sessions, sessionID)
	r, ok := c.s.Reviews[s.Review]
	if !ok || r.Session != s.ID {
		return c.save()
	}
	c.disconnect(r)
	var err2 error
	if r.Stopping {
		err2 = c.restore(r)
	}
	if err := c.commit(r); err != nil {
		return err
	}
	return err2
}

// SessionInfo — сессия и её ревью; ok == false, если duet о сессии не знает.
func (c *Core) SessionInfo(sessionID string) (Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.s.Sessions[sessionID]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// Sessions — все сессии, о которых знает duet.
func (c *Core) Sessions() []Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Session
	for _, s := range c.s.Sessions {
		out = append(out, *s)
	}
	return out
}

// TakeKicked — агент узнаёт, что его отключили от ревью; сессия забывается.
func (c *Core) TakeKicked(sessionID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.s.Sessions[sessionID]
	if !ok || s.Kicked == "" {
		return ""
	}
	k := s.Kicked
	if s.Review == "" {
		delete(c.s.Sessions, sessionID)
	} else {
		s.Kicked = ""
	}
	c.save()
	return k
}

// sessionReview — ревью, к которому подключена сессия.
func (c *Core) sessionReview(sessionID string) (*Session, *Review, error) {
	s, ok := c.s.Sessions[sessionID]
	if !ok || s.Review == "" {
		return nil, nil, ErrNotConnected
	}
	r, ok := c.s.Reviews[s.Review]
	if !ok || r.Session != s.ID {
		return nil, nil, ErrNotConnected
	}
	return s, r, nil
}

// SetBusy — агент начал (true) или закончил (false) что-то делать: отвечать в чате, работать.
func (c *Core) SetBusy(sessionID string, busy bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil {
		return err
	}
	if r.Busy == busy {
		return nil
	}
	r.Busy = busy
	return c.commit(r)
}

// Owns — файл в ревью сессии.
func (c *Core) Owns(sessionID, path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil {
		return false
	}
	f, ok := c.s.Files[path]
	return ok && f.ReviewID == r.ID
}

// WriteCheck — агент сессии собирается записать файл ревью (или файл, который duet добавит в ревью).
// Если можно, заход начинается, в том числе поверх непросмотренного ответа. Иначе — ошибка:
// владелец правит, «прервать», файл занят в другом ревью (*BusyError).
func (c *Core) WriteCheck(sessionID, path string, track bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil {
		return err
	}
	if r.Stopping {
		return ErrStopping
	}
	f, ok := c.s.Files[path]
	mine := ok && f.ReviewID == r.ID
	if !mine && !track {
		return nil
	}
	if ok && !mine && c.busy(f) {
		return &BusyError{Busy{path, f.ReviewID}}
	}
	if r.Editing {
		return ErrEditing
	}
	r.Busy = true
	if !r.Working {
		r.Working, r.Turn = true, true
	}
	r.Turn = true
	if !mine {
		c.track(r, path)
	}
	return c.commit(r)
}

// BusyError — файл занят в другом ревью.
type BusyError struct{ Busy Busy }

func (e *BusyError) Error() string {
	return fmt.Sprintf("файл %s занят в ревью %s", e.Busy.Path, e.Busy.ReviewID)
}

// track — забытый файл: duet сам добавляет его в ревью. Версии придут ответом.
func (c *Core) track(r *Review, path string) {
	f, ok := c.s.Files[path]
	if ok {
		old := c.s.Reviews[f.ReviewID]
		if old.Moved == nil {
			old.Moved = map[string]string{}
		}
		old.Moved[path] = r.ID
		c.changed(old.ID)
		f.ReviewID = r.ID
		f.Opened = false
	} else {
		f = &File{Path: path, ReviewID: r.ID, Base: -1, Current: -1, BaseSource: BaseSource{Kind: "none"}}
		c.s.Files[path] = f
	}
	r.addPath(path)
	delete(r.Moved, path)
}

// AskOriginal — нужно ли просить у агента исходную версию файла: файл в ревью без базы,
// и duet ещё не просил. Отмечает, что попросил.
func (c *Core) AskOriginal(sessionID, path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, r, err := c.sessionReview(sessionID)
	if err != nil {
		return false
	}
	f, ok := c.s.Files[path]
	if !ok || f.ReviewID != r.ID || f.Base >= 0 || f.BaseSource.Kind != "none" {
		return false
	}
	for _, p := range s.Asked {
		if p == path {
			return false
		}
	}
	s.Asked = append(s.Asked, path)
	c.save()
	return true
}

// SetOriginal — агент прислал исходную версию файла, у которого ещё нет базы. Она становится базой.
func (c *Core) SetOriginal(reviewID, path string, o Original) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	f, err := c.file(r, path)
	if err != nil {
		return err
	}
	if f.Base >= 0 || f.BaseSource.Kind != "none" {
		return ErrHasBase
	}
	f.Base = c.addVersion(f, Version{Content: o.Content, Author: "original"})
	f.BaseIsOriginal = true
	f.OriginSource = o.Source
	c.recordBase(f, BaseSource{Kind: "agent", Detail: o.Source})
	return c.commit(r)
}

// SetStart — файл, с которого владельцу начать. Файл должен быть в ревью.
func (c *Core) SetStart(reviewID, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if _, err := c.file(r, path); err != nil {
		return err
	}
	r.Start = path
	return c.commit(r)
}

// SetNote — записка агента для следующего агента, который подхватит ревью.
func (c *Core) SetNote(reviewID, note string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	r.Note = note
	return c.commit(r)
}

// SetBlocks — блоки файла, размеченные агентом.
func (c *Core) SetBlocks(reviewID, path string, blocks []Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	f, err := c.file(r, path)
	if err != nil {
		return err
	}
	f.Blocks = blocks
	return c.commit(r)
}

// fromDisk — файлы ревью, которые на диске отличаются от текущих версий, как их прислал бы агент.
func (c *Core) fromDisk(r *Review) ([]Submission, error) {
	if c.disk == nil {
		return nil, nil
	}
	var out []Submission
	for _, f := range c.files(r) {
		d, err := c.readDisk(f.Path)
		if err != nil {
			return nil, err
		}
		b, ok := c.expected(r, f) // текущая или версия из ответа, поверх которого агент продолжает
		if !ok && d.Deleted {
			continue
		}
		if ok && same(b, d) {
			if r.Draft != nil {
				if _, ok := r.Draft.Files[f.Path]; ok {
					out = append(out, Submission{Path: f.Path, Content: d.Content, Deleted: d.Deleted})
				}
			}
			continue
		}
		out = append(out, Submission{Path: f.Path, Content: d.Content, Deleted: d.Deleted})
	}
	return out, nil
}

// diskDiffers — агент поменял файлы ревью на диске (например, командой), пока не был в заходе.
func (c *Core) diskDiffers(r *Review) bool {
	if c.disk == nil || r.Editing || r.Stopping {
		return false
	}
	subs, err := c.fromDisk(r)
	return err == nil && len(subs) > 0
}

// unanswered — комментарии, которые агент взял, но на которые ещё не ответил.
func (c *Core) unanswered(r *Review, answers []Answer) []AgentComment {
	got := map[string]bool{}
	for _, a := range answers {
		got[a.CommentID] = true
	}
	if r.Draft != nil {
		for id := range r.Draft.Answers {
			got[id] = true
		}
	}
	var out []AgentComment
	c.eachComment(r, func(f *File, cm *Comment) {
		if cm.Status == CommentWorking && cm.Delivered && !got[cm.ID] {
			out = append(out, c.agentComment(f, cm))
		}
	})
	return out
}

func (c *Core) agentComment(f *File, cm *Comment) AgentComment {
	return AgentComment{ID: cm.ID, Path: cm.Path, Whole: cm.Whole || c.fileDeleted(f), Quote: cm.Quote, Removed: cm.Removed, Thread: append([]Message(nil), cm.Thread...)}
}

// UnansweredError — агент хочет закончить, не ответив на взятые комментарии.
type UnansweredError struct{ Comments []AgentComment }

func (e *UnansweredError) Error() string {
	var ids []string
	for _, cm := range e.Comments {
		ids = append(ids, cm.ID)
	}
	return "нет ответа на комментарии: " + strings.Join(ids, ", ")
}

// DoneResult — что получилось, когда агент сказал «готово».
type DoneResult struct {
	Responded bool           `json:"responded"` // появился ответ
	New       []AgentComment `json:"new"`       // пока агент работал, пришли новые комментарии: заход продолжается
}

// Done — агент сказал «готово»: ответы на взятые комментарии, правки берутся с диска.
// На каждый взятый комментарий нужен ответ, иначе *UnansweredError.
func (c *Core) Done(sessionID string, answers []Answer) (DoneResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil {
		return DoneResult{}, err
	}
	if r.Stopping {
		return DoneResult{}, ErrStopping
	}
	if un := c.unanswered(r, answers); len(un) > 0 {
		return DoneResult{}, &UnansweredError{un}
	}
	if !r.Working {
		if len(answers) > 0 {
			return DoneResult{}, ErrAgentNotWorking
		}
		if !c.diskDiffers(r) {
			return DoneResult{}, nil
		}
		r.Working, r.Turn = true, true
	}
	res, err := c.finishDisk(r, answers)
	if err != nil {
		return res, err
	}
	return res, c.save()
}

// finishDisk — конец захода с правками с диска. Если пришли новые комментарии, агент сразу их получает.
func (c *Core) finishDisk(r *Review, answers []Answer) (DoneResult, error) {
	subs, err := c.fromDisk(r)
	if err != nil {
		return DoneResult{}, err
	}
	ok, err := c.finish(r, subs, answers)
	if err != nil {
		return DoneResult{}, err
	}
	res := DoneResult{Responded: ok}
	if r.Working {
		res.New = c.take(r)
	}
	return res, nil
}

// StopResult — что делать, когда агент закончил отвечать (хук Stop).
type StopResult struct {
	// Unanswered — агент взял комментарии и не ответил: закончить нельзя.
	Unanswered []AgentComment `json:"unanswered"`
	Responded  bool           `json:"responded"`
}

// Stop — агент закончил отвечать в чате. Заход без комментариев (правка из чата) заканчивается сам.
// Если агент не ответил на взятые комментарии, закончить нельзя (кроме final — агент уже
// получил напоминание и всё равно останавливается). После «прервать» — агент остановился.
func (c *Core) Stop(sessionID string, final bool) (StopResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil {
		return StopResult{}, err
	}
	var res StopResult
	switch {
	case r.Stopping:
		err = c.restore(r)
	case r.Turn:
		if un := c.unanswered(r, nil); len(un) > 0 {
			if !final {
				return StopResult{Unanswered: un}, nil
			}
			break
		}
		var d DoneResult
		d, err = c.finishDisk(r, nil)
		res.Responded = d.Responded
		if len(d.New) > 0 {
			// Новые комментарии уже у агента в руках: пусть ответит.
			return StopResult{Unanswered: d.New}, c.save()
		}
	case c.diskDiffers(r):
		// Агент поменял файлы ревью командой, не начав заход.
		if !r.Working {
			r.Working = true
		}
		r.Turn = true
		var d DoneResult
		d, err = c.finishDisk(r, nil)
		res.Responded = d.Responded
		if len(d.New) > 0 {
			return StopResult{Unanswered: d.New}, c.save()
		}
	}
	r.Busy = false
	if e := c.commit(r); err == nil {
		err = e
	}
	return res, err
}

// Deliver — что пора передать агенту сессии: новые комментарии (они становятся «в работе»)
// и сообщения duet. Если ничего нет, оба пустые.
func (c *Core) Deliver(sessionID string) ([]AgentComment, []Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil {
		return nil, nil, err
	}
	if r.Stopping {
		return nil, nil, nil
	}
	cms := c.take(r)
	ns := r.Notices
	r.Notices = nil
	if len(cms) == 0 && len(ns) == 0 {
		return nil, nil, nil
	}
	c.changed(r.ID)
	return cms, ns, c.save()
}

// Pending — у агента свободной сессии есть что получить.
func (c *Core) Pending(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil || r.Stopping {
		return false
	}
	for _, n := range r.Notices {
		if n.wakes() {
			return true
		}
	}
	return c.count(r, CommentWaiting) > 0 || len(c.undelivered(r)) > 0
}

func (c *Core) undelivered(r *Review) []*Comment {
	var out []*Comment
	c.eachComment(r, func(_ *File, cm *Comment) {
		if cm.Status == CommentWorking && !cm.Delivered {
			out = append(out, cm)
		}
	})
	return out
}

// Busy — подключённый агент сессии занят.
func (c *Core) Busy(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	return err == nil && r.Busy
}

// ReviewForAgent — ревью целиком для агента, который его подхватил: файлы, записка, открытые комментарии.
type ReviewForAgent struct {
	ID       string                   `json:"id"`
	Title    string                   `json:"title"`
	Note     string                   `json:"note"`
	Files    []string                 `json:"files"`
	Comments []AgentComment           `json:"comments"` // открытые: не решённые и не удалённые
	Statuses map[string]CommentStatus `json:"statuses"`
}

func (c *Core) ForAgent(reviewID string) (ReviewForAgent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return ReviewForAgent{}, err
	}
	out := ReviewForAgent{ID: r.ID, Title: r.Title, Note: r.Note, Statuses: map[string]CommentStatus{}}
	for _, f := range c.files(r) {
		if f.Current >= 0 {
			out.Files = append(out.Files, f.Path)
		}
	}
	c.eachComment(r, func(f *File, cm *Comment) {
		if cm.Status == CommentResolved || cm.InResponse != 0 && cm.Status == CommentUnsent {
			return
		}
		out.Comments = append(out.Comments, c.agentComment(f, cm))
		out.Statuses[cm.ID] = cm.Status
	})
	return out, nil
}

// findAnchor — агент указал новое место комментария текстом: ищем его в версии из ответа.
func (c *Core) findAnchor(r *Review, d *Response, a Answer) *Range {
	f, cm, err := c.comment(r, a.CommentID)
	if err != nil || cm.Whole {
		return nil
	}
	n, ok := d.Files[f.Path]
	if p := r.pending(); !ok && p != nil {
		n, ok = p.Files[f.Path]
	}
	if !ok {
		return nil
	}
	i := strings.Index(f.Versions[n].Content, a.AnchorText)
	if i < 0 {
		return nil
	}
	return &Range{i, i + len(a.AnchorText)}
}

// Violations — агент поменял файлы командой во время правки владельца.
func (c *Core) Violations(sessionID string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, r, err := c.sessionReview(sessionID)
	if err != nil || c.disk == nil || r.Stopping {
		return nil, nil
	}
	if !r.Editing {
		return nil, nil
	}
	why := ErrEditing
	var out []string
	for _, f := range c.files(r) {
		exp, ok := c.expected(r, f)
		if !ok {
			continue
		}
		if d, err := c.readDisk(f.Path); err == nil && !same(d, exp) {
			out = append(out, f.Path)
		}
	}
	return out, why
}
