package core

import (
	"errors"
	"fmt"
)

var (
	ErrNoAgent        = errors.New("нет агента")
	ErrCommentStatus  = errors.New("с комментарием в этом статусе так нельзя")
	ErrBadRange       = errors.New("неверное место в тексте")
	ErrNotInResponse  = errors.New("ответ не касается файла")
	ErrNothingToSend  = errors.New("нечего отправлять")
	ErrNothingRunning = errors.New("агент ничего не делает")
)

// CommentStatus — статус комментария.
type CommentStatus string

const (
	CommentUnsent   CommentStatus = "unsent"   // не отправлен
	CommentWaiting  CommentStatus = "waiting"  // ожидает
	CommentWorking  CommentStatus = "working"  // в работе
	CommentAnswered CommentStatus = "answered" // отвечен
	CommentQuestion CommentStatus = "question" // вопрос
	CommentResolved CommentStatus = "resolved" // решён
	CommentDeleted  CommentStatus = "deleted"  // удалён владельцем или выброшен с ответом; хранится в истории
)

// Range — место в тексте версии: байты [Start, End).
type Range struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Message — одно сообщение в ветке комментария.
type Message struct {
	Author   string   `json:"author"` // owner, agent
	Text     string   `json:"text"`
	Images   []string `json:"images,omitempty"`
	Question bool     `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`  // варианты в вопросе агента
	Response int      `json:"response,omitempty"` // ответ агента, в котором пришло сообщение
	At       string   `json:"at"`
}

type Comment struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Whole   bool   `json:"whole,omitempty"` // ко всему файлу
	Version int    `json:"version"`         // версия, к которой сейчас привязан
	Range   Range  `json:"range"`
	Quote   string `json:"quote,omitempty"` // текст, к которому оставлен
	Changed bool   `json:"changed,omitempty"`
	Removed bool   `json:"removed,omitempty"`
	// InResponse — оставлен в окне ответа к версии из ответа с этим номером.
	InResponse int           `json:"inResponse,omitempty"`
	Status     CommentStatus `json:"status"`
	Prev       CommentStatus `json:"prev,omitempty"` // статус до ответа владельца в ветке
	Delivered  bool          `json:"delivered,omitempty"`
	Thread     []Message     `json:"thread"`
}

// Answer — ответ агента на комментарий.
type Answer struct {
	CommentID string
	Text      string
	Question  bool
	Options   []string
	// Anchor — новое место в версии файла из ответа, если агент его указал.
	Anchor *Range
}

// AgentComment — комментарий, как его получает агент.
type AgentComment struct {
	ID     string
	Path   string
	Whole  bool
	Quote  string
	Thread []Message
}

// CommentView — комментарий для клиента.
type CommentView struct {
	ID         string
	Whole      bool
	Range      Range
	Was        string // цитата «было: …», если текст изменён, удалён или удалён файл
	Changed    bool   // «текст изменён»
	Removed    bool
	InResponse bool
	Status     CommentStatus
	Thread     []Message
}

func (c *Core) comment(r *Review, id string) (*File, *Comment, error) {
	for _, p := range r.Paths {
		if _, moved := r.Moved[p]; moved {
			continue
		}
		f := c.s.Files[p]
		for _, cm := range f.Comments {
			if cm.ID == id && cm.Status != CommentDeleted {
				return f, cm, nil
			}
		}
	}
	return nil, nil, fmt.Errorf("комментарий %s: %w", id, ErrNotFound)
}

func (c *Core) eachComment(r *Review, fn func(f *File, cm *Comment)) {
	for _, p := range r.Paths {
		if _, moved := r.Moved[p]; moved {
			continue
		}
		f := c.s.Files[p]
		for _, cm := range f.Comments {
			if cm.Status != CommentDeleted {
				fn(f, cm)
			}
		}
	}
}

func (c *Core) count(r *Review, sts ...CommentStatus) int {
	n := 0
	c.eachComment(r, func(_ *File, cm *Comment) {
		for _, s := range sts {
			if cm.Status == s {
				n++
			}
		}
	})
	return n
}

func (c *Core) stamp() string { return c.now().Format("2006-01-02T15:04:05.000Z07:00") }

// AddComment — владелец оставляет комментарий. sel == nil — ко всему файлу.
// inResponse — в окне ответа, к версии из непросмотренного ответа.
func (c *Core) AddComment(reviewID, path string, sel *Range, text string, images []string, inResponse bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return "", err
	}
	f, err := c.file(r, path)
	if err != nil {
		return "", err
	}
	cm := &Comment{Path: path, Status: CommentUnsent, Whole: sel == nil, Version: f.Current}
	if inResponse {
		p := r.pending()
		n, ok := -1, false
		if p != nil {
			n, ok = p.Files[path]
		}
		if !ok {
			return "", ErrNotInResponse
		}
		cm.Version = n
		cm.InResponse = p.ID
	}
	if sel != nil {
		if cm.Version < 0 {
			return "", ErrBadRange
		}
		v := f.Versions[cm.Version]
		if v.Deleted || sel.Start < 0 || sel.End < sel.Start || sel.End > len(v.Content) {
			return "", ErrBadRange
		}
		cm.Range = *sel
		cm.Quote = v.Content[sel.Start:sel.End]
	}
	c.s.Seq++
	cm.ID = fmt.Sprintf("c%d", c.s.Seq)
	cm.Thread = []Message{{Author: "owner", Text: text, Images: images, At: c.stamp()}}
	f.Comments = append(f.Comments, cm)
	return cm.ID, c.save()
}

// EditComment — правка неотправленного комментария (или неотправленного ответа в ветке).
func (c *Core) EditComment(reviewID, id, text string, images []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	_, cm, err := c.comment(r, id)
	if err != nil {
		return err
	}
	if cm.Status != CommentUnsent {
		return ErrCommentStatus
	}
	m := &cm.Thread[len(cm.Thread)-1]
	m.Text, m.Images, m.At = text, images, c.stamp()
	return c.save()
}

// DeleteComment — удалить неотправленный комментарий. Если это ответ владельца в ветке,
// удаляется только этот ответ, и комментарий возвращается в прежний статус.
func (c *Core) DeleteComment(reviewID, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	_, cm, err := c.comment(r, id)
	if err != nil {
		return err
	}
	if cm.Status != CommentUnsent {
		return ErrCommentStatus
	}
	if cm.Prev != "" {
		cm.Thread = cm.Thread[:len(cm.Thread)-1]
		cm.Status, cm.Prev = cm.Prev, ""
	} else {
		cm.Status = CommentDeleted
	}
	return c.save()
}

// Reply — владелец отвечает в ветке на «вопрос» или «отвечен». Ответ ждёт `␣s`.
func (c *Core) Reply(reviewID, id, text string, images []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	_, cm, err := c.comment(r, id)
	if err != nil {
		return err
	}
	if cm.Status != CommentAnswered && cm.Status != CommentQuestion {
		return ErrCommentStatus
	}
	if r.pending() != nil {
		return ErrUnviewedResponse
	}
	cm.Thread = append(cm.Thread, Message{Author: "owner", Text: text, Images: images, At: c.stamp()})
	cm.Prev, cm.Status = cm.Status, CommentUnsent
	return c.save()
}

// DismissQuestion — владелец убирает вопрос агента: комментарий решён, агенту ничего не уходит.
func (c *Core) DismissQuestion(reviewID, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	_, cm, err := c.comment(r, id)
	if err != nil {
		return err
	}
	if cm.Status != CommentQuestion {
		return ErrCommentStatus
	}
	cm.Status = CommentResolved
	return c.save()
}

// Send — `␣s`: отправить все неотправленные комментарии ревью.
// Агент свободен — комментарии сразу «в работе», занят — «ожидают».
func (c *Core) Send(reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if r.pending() != nil {
		return ErrUnviewedResponse
	}
	if r.Editing {
		return ErrEditing
	}
	if !r.Agent {
		return ErrNoAgent
	}
	st := CommentWorking
	if r.Working {
		st = CommentWaiting
	}
	n := 0
	c.eachComment(r, func(_ *File, cm *Comment) {
		if cm.Status == CommentUnsent && cm.InResponse == 0 {
			cm.Status, cm.Prev, cm.Delivered = st, "", false
			n++
		}
	})
	if n == 0 {
		return ErrNothingToSend
	}
	r.Working = true
	return c.save()
}

// Take — агент забирает новые комментарии: «ожидает» становятся «в работе».
// Возвращает все ещё не полученные агентом комментарии.
func (c *Core) Take(reviewID string) ([]AgentComment, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return nil, err
	}
	var out []AgentComment
	c.eachComment(r, func(f *File, cm *Comment) {
		if cm.Status == CommentWaiting {
			cm.Status = CommentWorking
		}
		if cm.Status == CommentWorking && !cm.Delivered {
			cm.Delivered = true
			out = append(out, AgentComment{ID: cm.ID, Path: cm.Path, Whole: cm.Whole || c.fileDeleted(f), Quote: cm.Quote, Thread: append([]Message(nil), cm.Thread...)})
		}
	})
	if len(out) > 0 {
		r.Working = true
	}
	return out, c.save()
}

// Connect — агент подключился к ревью.
func (c *Core) Connect(reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	r.Agent = true
	return c.save()
}

// Disconnect — сессия агента закрыта. «Ожидает» возвращаются владельцу, «в работе» остаются.
func (c *Core) Disconnect(reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	r.Agent = false
	c.eachComment(r, func(_ *File, cm *Comment) {
		if cm.Status == CommentWaiting {
			cm.Status = CommentUnsent
		}
	})
	if c.count(r, CommentWorking) == 0 && r.Draft == nil {
		r.Working = false
	}
	return c.save()
}

// Interrupt — «прервать»: текущий заход агента выбрасывается целиком,
// «ожидает» и «в работе» становятся «не отправлен».
func (c *Core) Interrupt(reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if !r.Working {
		return ErrNothingRunning
	}
	if r.Draft != nil {
		r.Draft.Status = ResponseInterrupted
		r.Responses = append(r.Responses, r.Draft)
		r.Draft = nil
	}
	c.eachComment(r, func(_ *File, cm *Comment) {
		if cm.Status == CommentWaiting || cm.Status == CommentWorking {
			cm.Status, cm.Delivered = CommentUnsent, false
		}
	})
	r.Working = false
	return c.save()
}

func (c *Core) fileDeleted(f *File) bool { return f.Current >= 0 && f.Versions[f.Current].Deleted }

// reanchor переносит комментарии файла, привязанные к версии from, на версию to.
// answers — места, которые агент указал сам.
func (c *Core) reanchor(f *File, from, to int, anchors map[string]Range) {
	if from == to || to < 0 {
		return
	}
	for _, cm := range f.Comments {
		if cm.Status == CommentDeleted || cm.Version != from {
			continue
		}
		cm.Version = to
		if cm.Whole {
			continue
		}
		nv := f.Versions[to]
		if nv.Deleted {
			continue // файл удалён: место остаётся в цитате «было»
		}
		if a, ok := anchors[cm.ID]; ok && a.Start >= 0 && a.Start <= a.End && a.End <= len(nv.Content) {
			cm.Range = a
			cm.Changed = true
			continue
		}
		old := ""
		if from >= 0 {
			old = f.Versions[from].Content
		}
		rg, changed, removed := remap(old, nv.Content, cm.Range)
		cm.Range = rg
		cm.Changed = cm.Changed || changed
		cm.Removed = cm.Removed || removed
	}
}

// remap переносит место из старого текста в новый по отличиям строк.
func remap(old, nw string, rg Range) (Range, bool, bool) {
	ol, oo := splitLines(old)
	nl, no := splitLines(nw)
	hs := diffLines(ol, nl)
	// Номер новой строки для каждой неизменённой старой строки, иначе -1.
	m := make([]int, len(ol))
	ai, bj := 0, 0
	for _, h := range append(hs, hunk{len(ol), len(ol), len(nl), len(nl)}) {
		for ; ai < h.A0; ai, bj = ai+1, bj+1 {
			m[ai] = bj
		}
		for ; ai < h.A1; ai++ {
			m[ai] = -1
		}
		bj = h.B1
	}
	lineOf := func(pos int) int {
		for i := 0; i < len(ol); i++ {
			if pos < oo[i+1] {
				return i
			}
		}
		return len(ol) - 1
	}
	if len(ol) == 0 {
		return Range{}, false, true
	}
	l1 := lineOf(rg.Start)
	l2 := l1
	if rg.End > rg.Start {
		l2 = lineOf(rg.End - 1)
	}
	if m[l1] >= 0 && m[l2] >= 0 && m[l2]-m[l1] == l2-l1 {
		inserted := false
		for _, h := range hs {
			if h.A0 == h.A1 && h.A0 > l1 && h.A0 <= l2 {
				inserted = true
			}
		}
		if !inserted {
			return Range{no[m[l1]] + rg.Start - oo[l1], no[m[l2]] + rg.End - oo[l2]}, false, false
		}
	}
	// Текст переписан или удалён: берём новые строки, которыми заменили старые.
	ns, ne := -1, -1
	if m[l1] >= 0 {
		ns = m[l1]
	}
	if m[l2] >= 0 {
		ne = m[l2] + 1
	}
	for _, h := range hs {
		overlap := (h.A0 < h.A1 && h.A0 <= l2 && h.A1 > l1) || (h.A0 == h.A1 && h.A0 > l1 && h.A0 <= l2)
		if !overlap {
			continue
		}
		if ns < 0 || h.B0 < ns {
			ns = h.B0
		}
		if h.B1 > ne {
			ne = h.B1
		}
	}
	if ns < 0 {
		ns = 0
	}
	if ne <= ns {
		// Место удалили целиком: висим на строке, где оно было.
		if len(nl) == 0 {
			return Range{}, false, true
		}
		if ns >= len(nl) {
			ns = len(nl) - 1
		}
		return Range{no[ns], lineEnd(nl, no, ns)}, false, true
	}
	start := no[ns]
	if m[l1] >= 0 {
		start += rg.Start - oo[l1]
	}
	end := lineEnd(nl, no, ne-1)
	if m[l2] >= 0 {
		end = no[m[l2]] + rg.End - oo[l2]
	}
	return Range{start, end}, true, false
}

// lineEnd — конец строки i без перевода строки.
func lineEnd(lines []string, offs []int, i int) int {
	e := offs[i+1]
	if len(lines[i]) > 0 && lines[i][len(lines[i])-1] == '\n' {
		e--
	}
	return e
}

func (c *Core) commentViews(r *Review, f *File) []CommentView {
	var out []CommentView
	del := c.fileDeleted(f)
	for _, cm := range f.Comments {
		if cm.Status == CommentDeleted || cm.Status == CommentResolved {
			continue
		}
		v := CommentView{ID: cm.ID, Whole: cm.Whole || del, Range: cm.Range, Changed: cm.Changed, Removed: cm.Removed,
			InResponse: cm.InResponse != 0, Status: cm.Status, Thread: append([]Message(nil), cm.Thread...)}
		if !cm.Whole && (cm.Changed || cm.Removed || del) {
			v.Was = cm.Quote
		}
		if v.Whole {
			v.Range = Range{}
		}
		out = append(out, v)
	}
	return out
}
