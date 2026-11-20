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

	"github.com/senneco/duet/vcs"
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
	BaseIsOriginal bool         `json:"baseIsOriginal,omitempty"`
	OriginSource   string       `json:"originSource,omitempty"`
	BaseSource     BaseSource   `json:"baseSource"`
	BaseHistory    []BaseRecord `json:"baseHistory,omitempty"`
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
	ResponseDirect   ResponseStatus = "direct" // сразу в основной просмотр, без принятия владельцем
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
	Materials     []string      `json:"materials,omitempty"`
	RepositoryDir string        `json:"repositoryDir,omitempty"`
	Completeness  *Completeness `json:"completeness,omitempty"`

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
	Note string `json:"note,omitempty"` // записка агента следующему агенту (владельцу не показывается)
	// Start — файл, с которого владельцу начать: ревью открывается на нём.
	Start string `json:"start,omitempty"`
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

	provider         vcs.Provider
	repositoryChecks map[string]bool
	// disk — файлы проекта; nil — у файлов нет диска (страницы Вики, тесты без диска).
	disk Disk
	// synced — ревью, которые сверялись с диском после запуска; за ними duet следит.
	synced map[string]bool
	// drift — файлы с пометкой «изменён на диске»: путь → что лежит на диске.
	drift  map[string]*Version
	stamps map[string]string
	// diskTaken — последние изменения, взятые с диска правкой владельца, по ревью (для подсказки).
	diskTaken map[string]*FromDisk
	subsState
}

// Open открывает (или создаёт) хранилище в папке dir.
func Open(dir string) (*Core, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	c := &Core{provider: vcs.Registry{Home: dir}, path: filepath.Join(dir, "state.json"), now: time.Now, disk: osDisk{},
		synced: map[string]bool{}, drift: map[string]*Version{}, stamps: map[string]string{},
		diskTaken: map[string]*FromDisk{}}
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
	for _, f := range c.s.Files {
		migrateBase(f)
	}
	return c, nil
}

// Dir — папка данных duet.
func (c *Core) Dir() string { return filepath.Dir(c.path) }

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
	} else {
		f = &File{Path: sub.Path, ReviewID: r.ID, Base: -1, Current: -1, BaseSource: BaseSource{Kind: "none"}}
		c.s.Files[sub.Path] = f
		if sub.Original != nil {
			f.Base = c.addVersion(f, Version{Content: sub.Original.Content, Author: "original"})
			f.BaseIsOriginal = true
			f.OriginSource = sub.Original.Source
			c.recordBase(f, BaseSource{Kind: "agent", Detail: sub.Original.Source})
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
	// p — непросмотренный ответ, поверх которого агент продолжает (владелец отправил из окна ответа).
	p := r.pending()
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
			f = &File{Path: sub.Path, ReviewID: r.ID, Base: -1, Current: -1, BaseSource: BaseSource{Kind: "none"}}
			c.s.Files[sub.Path] = f
			r.addPath(f.Path)
		}
		v := Version{Content: sub.Content, Deleted: sub.Deleted, Author: "agent"}
		if b, ok := c.expected(r, f); ok && same(b, v) {
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
	// Пока агент работал, ответ, поверх которого он продолжал, могли принять: правки,
	// совпавшие с тем, что теперь ожидается, — не правки.
	for path, n := range d.Files {
		if b, ok := c.expected(r, c.s.Files[path]); ok && same(b, c.s.Files[path].Versions[n]) {
			delete(d.Files, path)
		}
	}
	if len(d.Files) == 0 && len(d.Answers) == 0 {
		return false, c.commit(r)
	}
	target := d
	if p != nil {
		c.mergeInto(r, p, d)
		target = p
	}
	direct := p == nil
	for _, path := range r.Paths {
		f := c.s.Files[path]
		// Файл, впервые появляющийся в ответе без базы, можно сразу читать целиком.
		newFile := f.Current < 0 && f.Base < 0
		if d.touches(c, path) && !isAccepted(f) && !newFile {
			direct = false
		}
	}
	for id, a := range d.Answers {
		_, cm, _ := c.comment(r, id)
		cm.Thread = append(cm.Thread, Message{Author: "agent", Text: a.Text, Question: a.Question, Options: a.Options, Response: target.ID, At: c.stamp()})
		cm.Status = CommentAnswered
		if a.Question {
			cm.Status = CommentQuestion
		}
		cm.Delivered = false
	}
	if p == nil {
		r.Responses = append(r.Responses, d)
	}
	if direct {
		if err := c.closeResponseLocked(r.ID, ResponseDirect); err != nil {
			return false, err
		}
		return true, nil
	}
	return true, c.commit(r)
}

// mergeInto — агент закончил продолжение поверх непросмотренного ответа p: продолжение вливается
// в p, и ответ один — «текущая → последнее». Написанное в окне и места, указанные агентом раньше,
// переносятся на новые версии.
func (c *Core) mergeInto(r *Review, p, d *Response) {
	for path, v := range d.Files {
		f := c.s.Files[path]
		from := f.Current
		if n, ok := p.Files[path]; ok {
			from = n
		}
		to := v
		if f.Current >= 0 && same(f.Versions[f.Current], f.Versions[v]) || f.Current < 0 && f.Versions[v].Deleted {
			to = f.Current // агент вернул файл к текущей версии (или удалил созданный в ответе)
			delete(p.Files, path)
		} else {
			p.Files[path] = v
		}
		if from == to {
			continue
		}
		for _, cm := range f.Comments {
			if a, ok := p.Answers[cm.ID]; ok && a.Anchor != nil {
				a.Anchor = c.moveRange(f, from, to, *a.Anchor)
				p.Answers[cm.ID] = a
			}
			// Комментарии на текущей версии перенесутся при принятии вместе с остальными.
			if from == f.Current || cm.InResponse != p.ID || cm.Version != from || cm.Status == CommentDeleted {
				continue
			}
			cm.Version = to
			if cm.Whole || to < 0 || f.Versions[to].Deleted {
				continue
			}
			rg, changed, removed := remap(f.Versions[from].Content, f.Versions[to].Content, cm.Range)
			cm.Range = rg
			cm.Changed = cm.Changed || changed
			cm.Removed = cm.Removed || removed
		}
	}
	for id, a := range d.Answers {
		p.Answers[id] = a
	}
	p.At = d.At
}

// moveRange — место из версии from в версии to; nil, если в версии to текста нет.
func (c *Core) moveRange(f *File, from, to int, rg Range) *Range {
	if to < 0 || f.Versions[to].Deleted {
		return nil
	}
	old := ""
	if from >= 0 {
		old = f.Versions[from].Content
	}
	nr, _, _ := remap(old, f.Versions[to].Content, rg)
	return &nr
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
	return c.closeResponseLocked(reviewID, st)
}

func (c *Core) closeResponseLocked(reviewID string, st ResponseStatus) error {
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	p := r.pending()
	if p == nil {
		return ErrNoResponse
	}
	if r.Working && st == ResponseRejected {
		return ErrAgentWorking // агент продолжает поверх ответа: отклонить можно, когда он закончит
	}
	var files []string
	for path := range p.Files {
		files = append(files, path)
	}
	sort.Strings(files)
	if st == ResponseRejected && c.disk != nil {
		// Диск возвращается к текущей версии. Файл, который поменяли на диске в обход duet,
		// не трогаем: после отклонения то, что на диске, станет правкой владельца.
		for _, path := range files {
			f := c.s.Files[path]
			d, err := c.readDisk(path)
			if err != nil {
				continue
			}
			if !same(d, f.Versions[p.Files[path]]) {
				c.drift[path] = &d // ревью не станет принятым целиком, пока это не взято правкой
				continue
			}
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
	if st == ResponseAccepted || st == ResponseDirect {
		for path, v := range p.Files {
			f := c.s.Files[path]
			anchors := map[string]Range{}
			for _, cm := range f.Comments {
				if a, ok := p.Answers[cm.ID]; ok && a.Anchor != nil {
					anchors[cm.ID] = *a.Anchor
				}
			}
			c.reanchor(f, f.Current, v, anchors)
			// Комментарии из окна уже на версии из ответа: место, указанное агентом, ставим сразу.
			for _, cm := range f.Comments {
				if a, ok := anchors[cm.ID]; ok && cm.InResponse == p.ID && cm.Version == v && !cm.Whole &&
					a.Start >= 0 && a.Start <= a.End && a.End <= len(f.Versions[v].Content) {
					cm.Range, cm.Changed = a, true
				}
			}
			f.Current = v
		}
	}
	c.eachComment(r, func(_ *File, cm *Comment) {
		if cm.InResponse == p.ID {
			if st == ResponseAccepted || st == ResponseDirect {
				cm.InResponse = 0
			} else {
				cm.Status = CommentDeleted // написанные в окне комментарии уходят вместе с ответом
				return
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
	if f.Base != f.Current || f.BaseSource.Kind != "owner" {
		f.Base = f.Current
		f.BaseIsOriginal = false
		c.recordBase(f, BaseSource{Kind: "owner"})
	}
	for _, cm := range f.Comments {
		if cm.Status == CommentAnswered {
			cm.Status = CommentResolved
		}
	}
	return c.commit(r)
}

// Edit — правка владельца. was — текст, который владелец начал править; если текущая версия
// с тех пор поменялась (например, файл поменяли на диске), правка не сохраняется — ErrChangedMeanwhile,
// пока владелец не подтвердит (force). Пустой was — не проверять.
func (c *Core) Edit(reviewID, path, content string) error {
	return c.EditFrom(reviewID, path, content, nil, false)
}

// ErrChangedMeanwhile — файл поменяли, пока владелец его правил.
var ErrChangedMeanwhile = errors.New("файл поменяли, пока вы его правили")

// EditFrom — правка владельца с проверкой, что файл не поменяли, пока его правили.
func (c *Core) EditFrom(reviewID, path, content string, was *string, force bool) error {
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
	if f.Current < 0 || f.Versions[f.Current].Deleted {
		return fmt.Errorf("файл %s удалён: %w", path, ErrNotFound)
	}
	c.touch(r) // изменение на диске, которое ещё не заметили, — сначала правкой, потом сравниваем
	// На диске может лежать не текущая версия: изменение ждёт (открыт редактор) или ревью
	// принято целиком и за диском не следим. Молча его не перезаписываем.
	var onDisk *Version
	if c.disk != nil {
		if d, err := c.readDisk(path); err == nil && !same(d, f.Versions[f.Current]) {
			onDisk = &d
		}
	}
	if !force && (onDisk != nil || (was != nil && f.Versions[f.Current].Content != *was)) {
		return ErrChangedMeanwhile
	}
	if onDisk != nil {
		// Владелец сохранил правку поверх изменения на диске: то, что было на диске, остаётся в истории.
		dv := *onDisk
		dv.Author = "disk"
		c.addVersion(f, dv)
	}
	v := Version{Content: content, Author: "owner"}
	if same(f.Versions[f.Current], v) {
		if onDisk != nil {
			if err := c.writeDisk(path, v); err != nil {
				return err
			}
			return c.commit(r)
		}
		return nil
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
	c.touch(r) // изменение на диске сначала становится правкой: «вернуть к базе» выбрасывает и его
	if f.Current == f.Base {
		return c.commit(r)
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
	Problems       []RepositoryProblem `json:"problems,omitempty"`
	ProblemOnly    bool                `json:"problemOnly,omitempty"`
	RepositoryDiff string              `json:"repositoryDiff,omitempty"`

	Path            string        `json:"path"`
	State           FileState     `json:"state"`
	MovedTo         string        `json:"movedTo"`
	Ripple          bool          `json:"ripple"` // непросмотренный ответ касается файла
	HasBase         bool          `json:"hasBase"`
	Base            string        `json:"base"`
	BaseIsOriginal  bool          `json:"baseIsOriginal"`
	OriginSource    string        `json:"originSource"`
	BaseSource      BaseSource    `json:"baseSource"`
	Current         string        `json:"current"`
	CurrentDeleted  bool          `json:"currentDeleted"`
	Absent          bool          `json:"absent"`   // текущей версии нет: файл есть только в непросмотренном ответе
	Response        *string       `json:"response"` // версия из непросмотренного ответа
	ResponseDeleted bool          `json:"responseDeleted"`
	Comments        []CommentView `json:"comments"`     // открытые комментарии: без решённых и удалённых
	AgentWorking    bool          `json:"agentWorking"` // есть «ожидает» или «в работе» — в дереве бегают точки
	// Open и Questions — сколько веток и вопросов доступно в основном просмотре (для дерева).
	Open      int `json:"open"`
	Questions int `json:"questions"`
	// DiskChanged — «изменён на диске»: на диске не то, что ожидается. Disk — что там лежит.
	DiskChanged bool    `json:"diskChanged"`
	Disk        string  `json:"disk"`
	DiskDeleted bool    `json:"diskDeleted"`
	Blocks      []Block `json:"blocks"` // блоки агента: строка, с которой блок начинается
}

// isAccepted — текущая версия совпадает с базой, и нет комментариев «отвечен», ждущих `␣a`.
func isAccepted(f *File) bool {
	if f.BaseSource.Kind == "repository" || f.BaseSource.Kind == "unknown" {
		return false
	}
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
	v := FileView{Problems: c.pathProblems(r, path), Path: path, HasBase: f.Base >= 0, BaseIsOriginal: f.BaseIsOriginal, OriginSource: f.OriginSource, BaseSource: f.BaseSource, Blocks: f.Blocks}
	if f.Base >= 0 {
		v.Base = f.Versions[f.Base].Content
	}
	if f.Current >= 0 {
		v.Current = f.Versions[f.Current].Content
		v.CurrentDeleted = f.Versions[f.Current].Deleted
	} else {
		v.Absent = true
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
	if r.Completeness != nil && !v.DiskChanged && len(v.Problems) == 0 {
		v.RepositoryDiff = r.Completeness.Diff[path]
		if v.RepositoryDiff == "unchanged" {
			for _, problem := range r.Completeness.Problems {
				if problem.Path == "" {
					v.RepositoryDiff = ""
					break
				}
			}
		}
	}
	v.ProblemOnly = f.Current < 0 && !v.Ripple && len(v.Problems) > 0
	v.Comments = c.commentViews(r, f)
	for _, cv := range v.Comments {
		visible := cv.Status != CommentAnswered || cv.Direct || v.RepositoryDiff == "unchanged"
		if p := r.pending(); p != nil {
			for _, m := range cv.Thread {
				if m.Author == "agent" && m.Response == p.ID {
					visible = true
				}
			}
		}
		if !cv.InResponse && visible {
			v.Open++
			if cv.Status == CommentQuestion {
				v.Questions++
			}
		}
	}
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
	if v, ok := c.problemView(r, path); ok {
		return v, nil
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
	Completeness *Completeness `json:"completeness,omitempty"`

	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Created     time.Time  `json:"created"`
	Working     bool       `json:"working"`
	HasResponse bool       `json:"hasResponse"`
	Response    *Pending   `json:"response"`  // непросмотренный ответ
	Accepted    bool       `json:"accepted"`  // ревью принято целиком
	Agent       bool       `json:"agent"`     // агент подключён
	AgentBusy   bool       `json:"agentBusy"` // подключённый агент занят (в чате или работает)
	AgentKind   string     `json:"agentKind"` // claude, codex
	Start       string     `json:"start"`     // файл, с которого начать, если агент его отметил
	AgentLost   bool       `json:"agentLost"` // агент пропал посреди работы: есть «в работе», а агента нет
	Unsent      int        `json:"unsent"`    // неотправленные комментарии (без написанных в окне ответа)
	Questions   int        `json:"questions"` // неотвеченные вопросы агента
	Files       []FileView `json:"files"`     // файлы, которых нет в текущей версии (только в ответе), не показываются
	NotAdded    []Busy     `json:"notAdded"`
	Stopping    bool       `json:"stopping"`    // «останавливается…» после «прервать»
	Restored    []string   `json:"restored"`    // файлы, которые duet перезаписал после последнего «прервать»
	DiskChanged []string   `json:"diskChanged"` // изменены на диске, станут правкой владельца, когда станет можно
	FromDisk    *FromDisk  `json:"fromDisk"`    // последние изменения, взятые с диска правкой владельца
	Editing     bool       `json:"editing"`     // открыт редактор правки
	// Locked — почему сейчас нельзя менять текущую версию (править, вернуть к базе):
	// "unviewed-response", "stopping", "agent-working"; "" — можно.
	Locked string `json:"locked"`
	Synced bool   `json:"synced"` // ревью сверено с диском после запуска duet
}

// lastResp — к какому ответу агента относится последнее сообщение ветки.
func lastResp(cm *Comment) int {
	if n := len(cm.Thread); n > 0 {
		return cm.Thread[n-1].Response
	}
	return 0
}

// windowUnsent — неотправленное, написанное в окне ответа p: комментарий к версии из ответа или ответ в ветке.
func windowUnsent(cm *Comment, p *Response) bool {
	return cm.Status == CommentUnsent && (cm.InResponse == p.ID || lastResp(cm) == p.ID)
}

// Pending — непросмотренный ответ агента для окна ответа.
type Pending struct {
	ID        int       `json:"id"`
	At        time.Time `json:"at"`
	Files     []string  `json:"files"`     // файлы, которых касается ответ, в порядке ревью
	Answers   int       `json:"answers"`   // на сколько комментариев ответил агент
	Questions int       `json:"questions"` // из них вопросов
	Mine      int       `json:"mine"`      // написано в окне ответа: комментарии к версии из ответа и ответы в ветках
	Unsent    int       `json:"unsent"`    // из написанного в окне не отправлено: уходит по `␣s` из окна
}

// allDone — ревью принято целиком, если не считать расхождений с диском.
func (c *Core) allDone(r *Review) bool {
	return (r.Completeness == nil || r.Completeness.Complete) && c.filesDone(r)
}

func (c *Core) filesDone(r *Review) bool {
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
	rv := ReviewView{Completeness: completenessView(r.Completeness), ID: r.ID, Title: r.Title, Created: r.Created, Working: r.Working, HasResponse: r.pending() != nil, NotAdded: r.NotAdded,
		Stopping: r.Stopping, Restored: r.Restored, DiskChanged: c.drifted(r), Synced: c.synced[r.ID],
		FromDisk: c.diskTaken[r.ID], Editing: r.Editing}
	switch c.checkChange(r) {
	case ErrUnviewedResponse:
		rv.Locked = "unviewed-response"
	case ErrStopping:
		rv.Locked = "stopping"
	case ErrAgentWorking:
		rv.Locked = "agent-working"
	}
	for _, p := range r.Paths {
		v := c.view(r, p)
		if v.State != StateMoved && c.s.Files[p].Current < 0 && !v.Ripple {
			continue
		}
		rv.Files = append(rv.Files, v)
	}
	c.addProblemViews(r, &rv)
	pend := r.pending()
	c.eachComment(r, func(_ *File, cm *Comment) {
		switch {
		case cm.Status == CommentUnsent && cm.InResponse == 0 && !(pend != nil && lastResp(cm) == pend.ID):
			rv.Unsent++ // ответ в ветке из окна ответа считается, только когда ответ принят
		case cm.Status == CommentQuestion:
			rv.Questions++
		}
	})
	if p := r.pending(); p != nil {
		pv := &Pending{ID: p.ID, At: p.At, Files: []string{}}
		for _, f := range rv.Files {
			if f.Ripple {
				pv.Files = append(pv.Files, f.Path)
			}
		}
		for _, a := range p.Answers {
			pv.Answers++
			if a.Question {
				pv.Questions++
			}
		}
		c.eachComment(r, func(_ *File, cm *Comment) {
			if cm.Status == CommentDeleted || cm.Status == CommentResolved {
				return
			}
			if windowUnsent(cm, p) {
				pv.Unsent++
			}
			if cm.InResponse == p.ID {
				pv.Mine++
				return
			}
			for _, m := range cm.Thread {
				if m.Author == "owner" && m.Response == p.ID {
					pv.Mine++
					return
				}
			}
		})
		rv.Response = pv
	}
	rv.Agent = r.Agent
	rv.AgentBusy = r.Agent && r.Busy
	rv.Start = r.Start
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
