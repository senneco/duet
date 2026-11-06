// Package core хранит ревью, версии файлов, базы и ответы агента
// и обеспечивает главные гарантии duet.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

var (
	ErrUnviewedResponse = errors.New("есть непросмотренный ответ агента")
	ErrAgentWorking     = errors.New("агент работает")
	ErrAgentNotWorking  = errors.New("агент не работает")
	ErrNoResponse       = errors.New("нет ответа агента")
	ErrNotFound         = errors.New("не найдено")
	ErrMoved            = errors.New("файл перешёл в другое ревью")
	ErrEditing          = errors.New("открыт редактор правки")
)

// FileState — состояние файла в дереве.
type FileState string

const (
	StateNew             FileState = "new"      // не открывали
	StateOpened          FileState = "opened"   // открывали, не принят
	StateAccepted        FileState = "accepted" // принят
	StateDeleted         FileState = "deleted"  // удалён, не принят
	StateDeletedAccepted FileState = "deleted-accepted"
	StateMoved           FileState = "moved" // перешёл в другое ревью
)

// Version — одна версия файла. Номер версии — её индекс в File.Versions.
type Version struct {
	Content string    `json:"content"`
	Deleted bool      `json:"deleted,omitempty"`
	Author  string    `json:"author"` // agent, original, owner
	At      time.Time `json:"at"`
}

// Original — исходная версия файла со слов агента.
type Original struct {
	Content string
	Source  string // откуда взята: «из git, HEAD» и т. п.
}

// Submission — файл, присланный агентом.
type Submission struct {
	Path     string
	Content  string
	Deleted  bool
	Original *Original
	Blocks   []Block // блоки, размеченные агентом; nil — не менять
}

type File struct {
	Path     string    `json:"path"`
	ReviewID string    `json:"review"`
	Versions []Version `json:"versions"`
	// Base — номер версии базы, -1 если базы нет.
	Base int `json:"base"`
	// BaseIsOriginal — база всё ещё исходная версия (пометка «до задачи»).
	BaseIsOriginal bool   `json:"baseIsOriginal,omitempty"`
	OriginSource   string `json:"originSource,omitempty"`
	// Current — номер текущей версии, -1 если файла ещё нет в текущей версии.
	Current int  `json:"current"`
	Opened  bool `json:"opened,omitempty"`
	// Comments — все комментарии файла, включая закрытые и удалённые (история).
	Comments []*Comment `json:"comments,omitempty"`
	Blocks   []Block    `json:"blocks,omitempty"`
}

type ResponseStatus string

const (
	ResponsePending  ResponseStatus = "pending"
	ResponseAccepted ResponseStatus = "accepted"
	ResponseRejected ResponseStatus = "rejected"
	// ResponseInterrupted — заход прерван владельцем, сделанное выброшено.
	ResponseInterrupted ResponseStatus = "interrupted"
)

// Response — ответ агента: новые версии файлов поверх текущей.
type Response struct {
	ID      int               `json:"id"`
	Files   map[string]int    `json:"files"`             // путь → номер версии
	Answers map[string]Answer `json:"answers,omitempty"` // комментарий → ответ агента
	Status  ResponseStatus    `json:"status"`
	At      time.Time         `json:"at"`
}

// touches — ответ касается файла: меняет его или отвечает на его комментарий.
func (p *Response) touches(c *Core, path string) bool {
	if _, ok := p.Files[path]; ok {
		return true
	}
	if f, ok := c.s.Files[path]; ok {
		for _, cm := range f.Comments {
			if _, ok := p.Answers[cm.ID]; ok {
				return true
			}
		}
	}
	return false
}

type Busy struct {
	Path     string `json:"path"`
	ReviewID string `json:"reviewId"`
}

type Review struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Created  time.Time         `json:"created"`
	Paths    []string          `json:"paths"`
	Moved    map[string]string `json:"moved,omitempty"` // путь → ревью, куда ушёл
	NotAdded []Busy            `json:"notAdded,omitempty"`
	Working  bool              `json:"working,omitempty"`
	Agent    bool              `json:"agent,omitempty"`   // агент подключён
	Session  string            `json:"session,omitempty"` // сессия подключённого агента
	// Busy — подключённый агент чем-то занят (отвечает в чате, работает): комментарии «ожидают».
	Busy bool `json:"busy,omitempty"`
	// Turn — агент в заходе: взял комментарии или начал менять файлы ревью.
	Turn bool   `json:"turn,omitempty"`
	Note string `json:"note,omitempty"` // записка агента «начните отсюда»
	// Open — ревью было не принято целиком: когда оно станет принятым, агент получит сообщение.
	Open    bool `json:"open,omitempty"`
	Editing bool `json:"editing,omitempty"` // открыт редактор правки
	// Stopping — владелец нажал «прервать», агент ещё не остановился; диск не трогаем.
	Stopping bool `json:"stopping,omitempty"`
	// Returned — комментарии, вернувшиеся владельцу при «прервать», для сообщения агенту.
	Returned []string `json:"returned,omitempty"`
	// Restored — файлы, которые duet перезаписал после последнего «прервать».
	Restored []string `json:"restored,omitempty"`
	// Notices — сообщения агенту, которые он ещё не забрал.
	Notices []Notice `json:"notices,omitempty"`
	// Draft — то, что агент уже сделал в заходе, который ещё продолжается.
	Draft     *Response   `json:"draft,omitempty"`
	Responses []*Response `json:"responses,omitempty"` // вся история
}

func (r *Review) pending() *Response {
	if n := len(r.Responses); n > 0 && r.Responses[n-1].Status == ResponsePending {
		return r.Responses[n-1]
	}
	return nil
}

type state struct {
	Seq     int                `json:"seq"`
	Reviews map[string]*Review `json:"reviews"`
	Files   map[string]*File   `json:"files"`
	// Sessions — сессии агентов, о которых знает duet: подключённые и отключённые перехватом.
	Sessions map[string]*Session `json:"sessions,omitempty"`
}

// Core — ядро. Все методы безопасны для одновременного вызова.
type Core struct {
	mu   sync.Mutex
	path string
	s    state
	now  func() time.Time
	// disk — файлы проекта; nil — у файлов нет диска (страницы Вики, тесты без диска).
	disk Disk
	// synced — ревью, которые сверялись с диском после запуска; за ними duet следит.
	synced map[string]bool
	// drift — файлы с пометкой «изменён на диске»: путь → что лежит на диске.
	drift  map[string]*Version
	stamps map[string]string
	subsState
}

// Open открывает (или создаёт) хранилище в папке dir.
func Open(dir string) (*Core, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Core{path: filepath.Join(dir, "state.json"), now: time.Now, disk: osDisk{},
		synced: map[string]bool{}, drift: map[string]*Version{}, stamps: map[string]string{}}
	c.subs = map[*Subscription]bool{}
	c.s = state{Reviews: map[string]*Review{}, Files: map[string]*File{}, Sessions: map[string]*Session{}}
	data, err := os.ReadFile(c.path)
	if err == nil {
		if err := json.Unmarshal(data, &c.s); err != nil {
			return nil, fmt.Errorf("чтение %s: %w", c.path, err)
		}
		if c.s.Sessions == nil {
			c.s.Sessions = map[string]*Session{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return c, nil
}

func (c *Core) save() error {
	data, err := json.Marshal(&c.s)
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

func (c *Core) review(id string) (*Review, error) {
	r, ok := c.s.Reviews[id]
	if !ok {
		return nil, fmt.Errorf("ревью %s: %w", id, ErrNotFound)
	}
	return r, nil
}

func (c *Core) file(r *Review, path string) (*File, error) {
	if to, ok := r.Moved[path]; ok {
		return nil, fmt.Errorf("%s → %s: %w", path, to, ErrMoved)
	}
	f, ok := c.s.Files[path]
	if !ok || f.ReviewID != r.ID {
		return nil, fmt.Errorf("файл %s: %w", path, ErrNotFound)
	}
	return f, nil
}

func (c *Core) addVersion(f *File, v Version) int {
	v.At = c.now()
	f.Versions = append(f.Versions, v)
	return len(f.Versions) - 1
}

func same(a, b Version) bool { return a.Deleted == b.Deleted && (a.Deleted || a.Content == b.Content) }

// busy — файл нельзя забрать из его ревью: агент там работает или есть непросмотренный ответ по файлу.
func (c *Core) busy(f *File) bool {
	r := c.s.Reviews[f.ReviewID]
	if r.Working {
		return true
	}
	if p := r.pending(); p != nil {
		return p.touches(c, f.Path)
	}
	return false
}

// CreateReview — агент отдаёт работу на ревью. Присланные версии сразу становятся текущими.
// Возвращает id ревью и файлы, которые не добавлены, потому что заняты в другом ревью.
func (c *Core) CreateReview(title string, files []Submission) (string, []Busy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Seq++
	r := &Review{ID: fmt.Sprintf("r%d", c.s.Seq), Title: title, Created: c.now(), Agent: true}
	c.s.Reviews[r.ID] = r
	for _, sub := range files {
		if f, ok := c.s.Files[sub.Path]; ok && f.ReviewID != r.ID && c.busy(f) {
			r.NotAdded = append(r.NotAdded, Busy{sub.Path, f.ReviewID})
			continue
		}
		c.addFirst(r, sub)
	}
	return r.ID, r.NotAdded, c.commit(r)
}

// AddFiles — агент досылает в ревью файлы, которых там ещё не было.
// Уже существующие файлы так менять нельзя: изменения приходят только ответом.
func (c *Core) AddFiles(reviewID string, files []Submission) ([]Busy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return nil, err
	}
	var notAdded []Busy
	for _, sub := range files {
		if f, ok := c.s.Files[sub.Path]; ok {
			if f.ReviewID == r.ID {
				return nil, fmt.Errorf("файл %s уже в ревью, изменения присылаются ответом", sub.Path)
			}
			if c.busy(f) {
				b := Busy{sub.Path, f.ReviewID}
				notAdded = append(notAdded, b)
				r.NotAdded = append(r.NotAdded, b)
				continue
			}
		}
		c.addFirst(r, sub)
	}
	return notAdded, c.commit(r)
}

func (c *Core) addFirst(r *Review, sub Submission) {
	f, ok := c.s.Files[sub.Path]
	if ok {
		// Переход из старого ревью со всем состоянием.
		old := c.s.Reviews[f.ReviewID]
		if old.Moved == nil {
			old.Moved = map[string]string{}
		}
		old.Moved[f.Path] = r.ID
		f.ReviewID = r.ID
		f.Opened = false
		// Принятый файл мог меняться мимо duet после принятия: база — исходная версия от агента.
		if sub.Original != nil && isAccepted(f) && !same(f.Versions[f.Base], Version{Content: sub.Original.Content}) {
			n := c.addVersion(f, Version{Content: sub.Original.Content, Author: "original"})
			c.reanchor(f, f.Current, n, nil)
			f.Base, f.Current = n, n
			f.BaseIsOriginal = true
			f.OriginSource = sub.Original.Source
		}
	} else {
		f = &File{Path: sub.Path, ReviewID: r.ID, Base: -1, Current: -1}
		c.s.Files[sub.Path] = f
		if sub.Original != nil {
			f.Base = c.addVersion(f, Version{Content: sub.Original.Content, Author: "original"})
			f.BaseIsOriginal = true
			f.OriginSource = sub.Original.Source
		}
	}
	v := Version{Content: sub.Content, Deleted: sub.Deleted, Author: "agent"}
	if f.Current >= 0 && same(f.Versions[f.Current], v) {
		// не плодим одинаковых версий
	} else if f.Base >= 0 && f.Current < 0 && same(f.Versions[f.Base], v) {
		f.Current = f.Base
	} else {
		n := c.addVersion(f, v)
		c.reanchor(f, f.Current, n, nil)
		f.Current = n
	}
	if sub.Blocks != nil {
		f.Blocks = sub.Blocks
	}
	r.addPath(f.Path)
	delete(r.Moved, f.Path)
}

func (r *Review) addPath(p string) {
	for _, q := range r.Paths {
		if q == p {
			return
		}
	}
	r.Paths = append(r.Paths, p)
}

// AgentStart — агент взял работу по ревью.
func (c *Core) AgentStart(reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if r.pending() != nil {
		return ErrUnviewedResponse
	}
	if r.Stopping {
		return ErrStopping
	}
	if r.Editing {
		return ErrEditing
	}
	r.Working = true
	r.Turn = true
	return c.commit(r)
}

// AgentFinish — агент закончил заход: присланные версии, отличные от текущих, и ответы на комментарии.
// Если на какой-то комментарий агент не ответил, тот снова «ожидает». Если есть «ожидает»,
// агент продолжает работу, а ответ копится и появится, когда он закончит снова.
// Возвращает true, если ответ появился.
func (c *Core) AgentFinish(reviewID string, files []Submission, answers []Answer) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return false, err
	}
	return c.finish(r, files, answers)
}

func (c *Core) finish(r *Review, files []Submission, answers []Answer) (bool, error) {
	if !r.Working {
		return false, ErrAgentNotWorking
	}
	d := r.Draft
	if d == nil {
		d = &Response{ID: len(r.Responses) + 1, Files: map[string]int{}, Answers: map[string]Answer{}, Status: ResponsePending}
	}
	if d.Answers == nil {
		d.Answers = map[string]Answer{}
	}
	for _, a := range answers {
		_, cm, err := c.comment(r, a.CommentID)
		if err != nil {
			return false, err
		}
		if cm.Status != CommentWorking || !cm.Delivered {
			return false, fmt.Errorf("комментарий %s не в работе у агента: %w", cm.ID, ErrCommentStatus)
		}
	}
	for _, sub := range files {
		if _, moved := r.Moved[sub.Path]; moved {
			return false, fmt.Errorf("%s: %w", sub.Path, ErrMoved)
		}
		f, ok := c.s.Files[sub.Path]
		if ok && f.ReviewID != r.ID {
			return false, fmt.Errorf("файл %s в другом ревью", sub.Path)
		}
		if !ok {
			f = &File{Path: sub.Path, ReviewID: r.ID, Base: -1, Current: -1}
			c.s.Files[sub.Path] = f
			r.addPath(f.Path)
		}
		v := Version{Content: sub.Content, Deleted: sub.Deleted, Author: "agent"}
		if f.Current >= 0 && same(f.Versions[f.Current], v) {
			delete(d.Files, f.Path)
			continue
		}
		if n, ok := d.Files[f.Path]; ok && same(f.Versions[n], v) {
			continue
		}
		d.Files[f.Path] = c.addVersion(f, v)
	}
	for _, a := range answers {
		if a.Anchor == nil && a.AnchorText != "" {
			a.Anchor = c.findAnchor(r, d, a)
		}
		d.Answers[a.CommentID] = a
	}
	// Не отвеченные агентом комментарии снова ждут его.
	c.eachComment(r, func(_ *File, cm *Comment) {
		if _, ok := d.Answers[cm.ID]; cm.Status == CommentWorking && !ok {
			cm.Status, cm.Delivered = CommentWaiting, false
		}
	})
	d.At = c.now()
	if c.count(r, CommentWaiting) > 0 {
		r.Draft = d
		c.changed(r.ID)
		return false, c.save()
	}
	r.Working = false
	r.Turn = false
	r.Draft = nil
	if len(d.Files) == 0 && len(d.Answers) == 0 {
		return false, c.commit(r)
	}
	for id, a := range d.Answers {
		_, cm, _ := c.comment(r, id)
		cm.Thread = append(cm.Thread, Message{Author: "agent", Text: a.Text, Question: a.Question, Options: a.Options, Response: d.ID, At: c.stamp()})
		cm.Status = CommentAnswered
		if a.Question {
			cm.Status = CommentQuestion
		}
		cm.Delivered = false
	}
	r.Responses = append(r.Responses, d)
	return true, c.commit(r)
}

// AcceptResponse — владелец просмотрел ответ: версии из него становятся текущими.
func (c *Core) AcceptResponse(reviewID string) error {
	return c.closeResponse(reviewID, ResponseAccepted)
}

// RejectResponse — ответ выбрасывается целиком, текущие версии не меняются. Ответ остаётся в истории.
func (c *Core) RejectResponse(reviewID string) error {
	return c.closeResponse(reviewID, ResponseRejected)
}

func (c *Core) closeResponse(reviewID string, st ResponseStatus) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	p := r.pending()
	if p == nil {
		return ErrNoResponse
	}
	if r.Working {
		return ErrAgentWorking
	}
	var files []string
	for path := range p.Files {
		files = append(files, path)
	}
	sort.Strings(files)
	if st == ResponseRejected {
		// Диск возвращается к текущей версии, но только если там лежит ответ, как ожидается.
		var fs []*File
		for _, path := range files {
			fs = append(fs, c.s.Files[path])
		}
		if err := c.diskCheck(r, fs...); err != nil {
			return err
		}
		for _, f := range fs {
			want := Version{Deleted: true}
			if f.Current >= 0 {
				want = f.Versions[f.Current]
			}
			if err := c.writeDisk(f.Path, want); err != nil {
				return err
			}
		}
	}
	var returned []string
	if st == ResponseAccepted {
		for path, v := range p.Files {
			f := c.s.Files[path]
			anchors := map[string]Range{}
			for _, cm := range f.Comments {
				if a, ok := p.Answers[cm.ID]; ok && a.Anchor != nil {
					anchors[cm.ID] = *a.Anchor
				}
			}
			c.reanchor(f, f.Current, v, anchors)
			f.Current = v
		}
	}
	c.eachComment(r, func(_ *File, cm *Comment) {
		if cm.InResponse == p.ID {
			if st == ResponseAccepted {
				cm.InResponse = 0
			} else {
				cm.Status = CommentDeleted // написанные в окне комментарии уходят вместе с ответом
			}
		}
		if _, ok := p.Answers[cm.ID]; ok && st == ResponseRejected {
			returned = append(returned, cm.ID)
			th := cm.Thread[:0]
			for _, m := range cm.Thread {
				if m.Response != p.ID {
					th = append(th, m)
				}
			}
			cm.Thread = th
			cm.Status, cm.Prev, cm.Delivered = CommentUnsent, "", false
			for i := len(th) - 1; i >= 0; i-- {
				if th[i].Author == "agent" {
					cm.Prev = CommentAnswered
					if th[i].Question {
						cm.Prev = CommentQuestion
					}
					break
				}
			}
		}
	})
	p.Status = st
	if st == ResponseRejected {
		sort.Strings(returned)
		c.notice(r, NoticeRejected, files, returned)
	}
	return c.commit(r)
}

// checkChange — можно ли менять текущую версию файлов ревью.
func (c *Core) checkChange(r *Review) error {
	if r.pending() != nil {
		return ErrUnviewedResponse
	}
	if r.Stopping {
		return ErrStopping
	}
	if r.Working || c.count(r, CommentWaiting, CommentWorking) > 0 {
		return ErrAgentWorking
	}
	return nil
}

// AcceptFile — `␣a`: текущая версия становится базой.
func (c *Core) AcceptFile(reviewID, path string) error {
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
	if p := r.pending(); p != nil && p.touches(c, path) {
		return ErrUnviewedResponse
	}
	if f.Current < 0 {
		return fmt.Errorf("файл %s: %w", path, ErrNotFound)
	}
	c.touch(r)
	if _, ok := c.drift[path]; ok {
		return &DiskChangedError{[]string{path}}
	}
	if f.Base != f.Current {
		f.Base = f.Current
		f.BaseIsOriginal = false
	}
	for _, cm := range f.Comments {
		if cm.Status == CommentAnswered {
			cm.Status = CommentResolved
		}
	}
	return c.commit(r)
}

// Edit — правка владельца.
func (c *Core) Edit(reviewID, path, content string) error {
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
	if err := c.checkChange(r); err != nil {
		return err
	}
	v := Version{Content: content, Author: "owner"}
	if f.Current >= 0 && same(f.Versions[f.Current], v) {
		return nil
	}
	if err := c.diskCheck(r, f); err != nil {
		return err
	}
	if err := c.writeDisk(path, v); err != nil {
		return err
	}
	n := f.Base
	if f.Base < 0 || !same(f.Versions[f.Base], v) {
		n = c.addVersion(f, v)
	}
	c.reanchor(f, f.Current, n, nil)
	f.Current = n
	c.notice(r, NoticeOwnerEdit, []string{path}, nil)
	return c.commit(r)
}

// RevertToBase — «вернуть к базе».
func (c *Core) RevertToBase(reviewID, path string) error {
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
	if err := c.checkChange(r); err != nil {
		return err
	}
	if f.Base < 0 {
		return fmt.Errorf("у файла %s нет базы", path)
	}
	if f.Current == f.Base {
		return nil
	}
	if err := c.diskCheck(r, f); err != nil {
		return err
	}
	if err := c.writeDisk(path, f.Versions[f.Base]); err != nil {
		return err
	}
	c.reanchor(f, f.Current, f.Base, nil)
	f.Current = f.Base
	c.notice(r, NoticeRevert, []string{path}, nil)
	return c.commit(r)
}

// SetEditing — владелец открыл или закрыл редактор правки.
func (c *Core) SetEditing(reviewID string, on bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if on {
		if err := c.checkChange(r); err != nil {
			return err
		}
	}
	r.Editing = on
	return c.commit(r)
}

// MarkOpened — владелец открыл файл.
func (c *Core) MarkOpened(reviewID, path string) error {
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
	if f.Opened {
		return nil
	}
	f.Opened = true
	return c.commit(r)
}

// FileView — всё, что клиенту нужно знать о файле.
type FileView struct {
	Path            string        `json:"path"`
	State           FileState     `json:"state"`
	MovedTo         string        `json:"movedTo"`
	Ripple          bool          `json:"ripple"` // непросмотренный ответ касается файла
	HasBase         bool          `json:"hasBase"`
	Base            string        `json:"base"`
	BaseIsOriginal  bool          `json:"baseIsOriginal"`
	OriginSource    string        `json:"originSource"`
	Current         string        `json:"current"`
	CurrentDeleted  bool          `json:"currentDeleted"`
	Response        *string       `json:"response"` // версия из непросмотренного ответа
	ResponseDeleted bool          `json:"responseDeleted"`
	Comments        []CommentView `json:"comments"`     // открытые комментарии: без решённых и удалённых
	AgentWorking    bool          `json:"agentWorking"` // есть «ожидает» или «в работе» — в дереве бегают точки
	// DiskChanged — «изменён на диске»: на диске не то, что ожидается. Disk — что там лежит.
	DiskChanged bool    `json:"diskChanged"`
	Disk        string  `json:"disk"`
	DiskDeleted bool    `json:"diskDeleted"`
	Blocks      []Block `json:"blocks"` // блоки агента: строка, с которой блок начинается
}

// isAccepted — текущая версия совпадает с базой, и нет комментариев «отвечен», ждущих `␣a`.
func isAccepted(f *File) bool {
	if f.Base < 0 || f.Current < 0 || !same(f.Versions[f.Base], f.Versions[f.Current]) {
		return false
	}
	for _, cm := range f.Comments {
		if cm.Status == CommentAnswered {
			return false
		}
	}
	return true
}

func (c *Core) view(r *Review, path string) FileView {
	if to, ok := r.Moved[path]; ok {
		return FileView{Path: path, State: StateMoved, MovedTo: to}
	}
	f := c.s.Files[path]
	v := FileView{Path: path, HasBase: f.Base >= 0, BaseIsOriginal: f.BaseIsOriginal, OriginSource: f.OriginSource, Blocks: f.Blocks}
	if f.Base >= 0 {
		v.Base = f.Versions[f.Base].Content
	}
	if f.Current >= 0 {
		v.Current = f.Versions[f.Current].Content
		v.CurrentDeleted = f.Versions[f.Current].Deleted
	}
	if p := r.pending(); p != nil {
		v.Ripple = p.touches(c, path)
		if n, ok := p.Files[path]; ok {
			s := f.Versions[n].Content
			v.Response = &s
			v.ResponseDeleted = f.Versions[n].Deleted
		}
	}
	if d, ok := c.drift[path]; ok {
		v.DiskChanged, v.Disk, v.DiskDeleted = true, d.Content, d.Deleted
	}
	v.Comments = c.commentViews(r, f)
	for _, cm := range f.Comments {
		if cm.Status == CommentWaiting || cm.Status == CommentWorking {
			v.AgentWorking = true
		}
	}
	acc := isAccepted(f)
	switch {
	case v.CurrentDeleted && acc:
		v.State = StateDeletedAccepted
	case v.CurrentDeleted:
		v.State = StateDeleted
	case acc:
		v.State = StateAccepted
	case f.Opened:
		v.State = StateOpened
	default:
		v.State = StateNew
	}
	return v
}

// File возвращает файл ревью.
func (c *Core) File(reviewID, path string) (FileView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return FileView{}, err
	}
	if _, ok := r.Moved[path]; !ok {
		if _, err := c.file(r, path); err != nil {
			return FileView{}, err
		}
	}
	return c.view(r, path), nil
}

// ReviewView — ревью целиком.
type ReviewView struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Created     time.Time  `json:"created"`
	Working     bool       `json:"working"`
	HasResponse bool       `json:"hasResponse"`
	Accepted    bool       `json:"accepted"`  // ревью принято целиком
	Agent       bool       `json:"agent"`     // агент подключён
	AgentBusy   bool       `json:"agentBusy"` // подключённый агент занят (в чате или работает)
	AgentKind   string     `json:"agentKind"` // claude, codex
	Note        string     `json:"note"`      // записка агента «начните отсюда»
	AgentLost   bool       `json:"agentLost"` // агент пропал посреди работы: есть «в работе», а агента нет
	Unsent      int        `json:"unsent"`    // неотправленные комментарии (без написанных в окне ответа)
	Questions   int        `json:"questions"` // неотвеченные вопросы агента
	Files       []FileView `json:"files"`     // файлы, которых нет в текущей версии (только в ответе), не показываются
	NotAdded    []Busy     `json:"notAdded"`
	Stopping    bool       `json:"stopping"`    // «останавливается…» после «прервать»
	Restored    []string   `json:"restored"`    // файлы, которые duet перезаписал после последнего «прервать»
	DiskChanged []string   `json:"diskChanged"` // файлы с пометкой «изменён на диске»
	Synced      bool       `json:"synced"`      // ревью сверено с диском после запуска duet
}

// allDone — ревью принято целиком, если не считать расхождений с диском.
func (c *Core) allDone(r *Review) bool {
	if r.Working || r.Stopping || r.pending() != nil {
		return false
	}
	for _, f := range c.files(r) {
		if f.Current >= 0 && !isAccepted(f) {
			return false
		}
	}
	done := true
	c.eachComment(r, func(_ *File, cm *Comment) {
		switch cm.Status {
		case CommentUnsent, CommentQuestion, CommentWaiting, CommentWorking:
			done = false
		}
	})
	return done
}

func (c *Core) reviewView(r *Review) ReviewView {
	rv := ReviewView{ID: r.ID, Title: r.Title, Created: r.Created, Working: r.Working, HasResponse: r.pending() != nil, NotAdded: r.NotAdded,
		Stopping: r.Stopping, Restored: r.Restored, DiskChanged: c.drifted(r), Synced: c.synced[r.ID]}
	for _, p := range r.Paths {
		v := c.view(r, p)
		if v.State != StateMoved && c.s.Files[p].Current < 0 && !v.Ripple {
			continue
		}
		rv.Files = append(rv.Files, v)
	}
	c.eachComment(r, func(_ *File, cm *Comment) {
		switch {
		case cm.Status == CommentUnsent && cm.InResponse == 0:
			rv.Unsent++
		case cm.Status == CommentQuestion:
			rv.Questions++
		}
	})
	rv.Agent = r.Agent
	rv.AgentBusy = r.Agent && r.Busy
	rv.Note = r.Note
	if s, ok := c.s.Sessions[r.Session]; ok && r.Agent {
		rv.AgentKind = s.Agent
	}
	rv.AgentLost = !r.Agent && c.count(r, CommentWorking) > 0
	rv.Accepted = c.allDone(r) && len(rv.DiskChanged) == 0
	return rv
}

// Review возвращает ревью.
func (c *Core) Review(id string) (ReviewView, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(id)
	if err != nil {
		return ReviewView{}, err
	}
	return c.reviewView(r), nil
}

// Reviews — все ревью, новые сверху.
func (c *Core) Reviews() []ReviewView {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ReviewView
	for _, r := range c.s.Reviews {
		out = append(out, c.reviewView(r))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Created.After(out[j].Created) || (out[i].Created.Equal(out[j].Created) && out[i].ID > out[j].ID)
	})
	return out
}

// Responses — история ответов ревью, включая отклонённые.
func (c *Core) Responses(reviewID string) ([]Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return nil, err
	}
	out := make([]Response, len(r.Responses))
	for i, p := range r.Responses {
		out[i] = *p
	}
	return out, nil
}
