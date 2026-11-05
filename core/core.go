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
}

type ResponseStatus string

const (
	ResponsePending  ResponseStatus = "pending"
	ResponseAccepted ResponseStatus = "accepted"
	ResponseRejected ResponseStatus = "rejected"
)

// Response — ответ агента: новые версии файлов поверх текущей.
type Response struct {
	ID     int            `json:"id"`
	Files  map[string]int `json:"files"` // путь → номер версии
	Status ResponseStatus `json:"status"`
	At     time.Time      `json:"at"`
}

type Busy struct {
	Path     string
	ReviewID string
}

type Review struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Created   time.Time         `json:"created"`
	Paths     []string          `json:"paths"`
	Moved     map[string]string `json:"moved,omitempty"` // путь → ревью, куда ушёл
	NotAdded  []Busy            `json:"notAdded,omitempty"`
	Working   bool              `json:"working,omitempty"`
	Responses []*Response       `json:"responses,omitempty"` // вся история
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
}

// Core — ядро. Все методы безопасны для одновременного вызова.
type Core struct {
	mu   sync.Mutex
	path string
	s    state
	now  func() time.Time
}

// Open открывает (или создаёт) хранилище в папке dir.
func Open(dir string) (*Core, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Core{path: filepath.Join(dir, "state.json"), now: time.Now}
	c.s = state{Reviews: map[string]*Review{}, Files: map[string]*File{}}
	data, err := os.ReadFile(c.path)
	if err == nil {
		if err := json.Unmarshal(data, &c.s); err != nil {
			return nil, fmt.Errorf("чтение %s: %w", c.path, err)
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
		_, touched := p.Files[f.Path]
		return touched
	}
	return false
}

// CreateReview — агент отдаёт работу на ревью. Присланные версии сразу становятся текущими.
// Возвращает id ревью и файлы, которые не добавлены, потому что заняты в другом ревью.
func (c *Core) CreateReview(title string, files []Submission) (string, []Busy, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.s.Seq++
	r := &Review{ID: fmt.Sprintf("r%d", c.s.Seq), Title: title, Created: c.now()}
	c.s.Reviews[r.ID] = r
	for _, sub := range files {
		if f, ok := c.s.Files[sub.Path]; ok && f.ReviewID != r.ID && c.busy(f) {
			r.NotAdded = append(r.NotAdded, Busy{sub.Path, f.ReviewID})
			continue
		}
		c.addFirst(r, sub)
	}
	return r.ID, r.NotAdded, c.save()
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
	return notAdded, c.save()
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
		f.Current = c.addVersion(f, v)
	}
	r.Paths = append(r.Paths, f.Path)
	delete(r.Moved, f.Path)
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
	r.Working = true
	return c.save()
}

// AgentFinish — агент закончил. Присланные версии, отличные от текущих, становятся ответом.
// Возвращает true, если ответ появился.
func (c *Core) AgentFinish(reviewID string, files []Submission) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return false, err
	}
	if !r.Working {
		return false, ErrAgentNotWorking
	}
	resp := &Response{ID: len(r.Responses) + 1, Files: map[string]int{}, Status: ResponsePending, At: c.now()}
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
			r.Paths = append(r.Paths, f.Path)
		}
		v := Version{Content: sub.Content, Deleted: sub.Deleted, Author: "agent"}
		if f.Current >= 0 && same(f.Versions[f.Current], v) {
			continue
		}
		resp.Files[f.Path] = c.addVersion(f, v)
	}
	r.Working = false
	if len(resp.Files) == 0 {
		return false, c.save()
	}
	r.Responses = append(r.Responses, resp)
	return true, c.save()
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
	if st == ResponseAccepted {
		for path, v := range p.Files {
			c.s.Files[path].Current = v
		}
	}
	p.Status = st
	return c.save()
}

// checkChange — можно ли менять текущую версию файлов ревью.
func (c *Core) checkChange(r *Review) error {
	if r.pending() != nil {
		return ErrUnviewedResponse
	}
	if r.Working {
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
	if p := r.pending(); p != nil {
		if _, ok := p.Files[path]; ok {
			return ErrUnviewedResponse
		}
	}
	if f.Current < 0 {
		return fmt.Errorf("файл %s: %w", path, ErrNotFound)
	}
	f.Base = f.Current
	f.BaseIsOriginal = false
	return c.save()
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
	if f.Base >= 0 && same(f.Versions[f.Base], v) {
		f.Current = f.Base
	} else {
		f.Current = c.addVersion(f, v)
	}
	return c.save()
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
	f.Current = f.Base
	return c.save()
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
	return c.save()
}

// FileView — всё, что клиенту нужно знать о файле.
type FileView struct {
	Path            string
	State           FileState
	MovedTo         string
	Ripple          bool // непросмотренный ответ касается файла
	HasBase         bool
	Base            string
	BaseIsOriginal  bool
	OriginSource    string
	Current         string
	CurrentDeleted  bool
	Response        *string // версия из непросмотренного ответа
	ResponseDeleted bool
}

func isAccepted(f *File) bool {
	return f.Base >= 0 && f.Current >= 0 && same(f.Versions[f.Base], f.Versions[f.Current])
}

func (c *Core) view(r *Review, path string) FileView {
	if to, ok := r.Moved[path]; ok {
		return FileView{Path: path, State: StateMoved, MovedTo: to}
	}
	f := c.s.Files[path]
	v := FileView{Path: path, HasBase: f.Base >= 0, BaseIsOriginal: f.BaseIsOriginal, OriginSource: f.OriginSource}
	if f.Base >= 0 {
		v.Base = f.Versions[f.Base].Content
	}
	if f.Current >= 0 {
		v.Current = f.Versions[f.Current].Content
		v.CurrentDeleted = f.Versions[f.Current].Deleted
	}
	if p := r.pending(); p != nil {
		if n, ok := p.Files[path]; ok {
			v.Ripple = true
			s := f.Versions[n].Content
			v.Response = &s
			v.ResponseDeleted = f.Versions[n].Deleted
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
	ID          string
	Title       string
	Created     time.Time
	Working     bool
	HasResponse bool
	Accepted    bool       // ревью принято целиком
	Files       []FileView // файлы, которых нет в текущей версии (только в ответе), не показываются
	NotAdded    []Busy
}

func (c *Core) reviewView(r *Review) ReviewView {
	rv := ReviewView{ID: r.ID, Title: r.Title, Created: r.Created, Working: r.Working, HasResponse: r.pending() != nil, NotAdded: r.NotAdded}
	all := true
	for _, p := range r.Paths {
		v := c.view(r, p)
		if v.State != StateMoved && c.s.Files[p].Current < 0 && !v.Ripple {
			continue
		}
		rv.Files = append(rv.Files, v)
		if v.State != StateMoved && v.State != StateAccepted && v.State != StateDeletedAccepted {
			all = false
		}
	}
	rv.Accepted = all && !r.Working && !rv.HasResponse
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
