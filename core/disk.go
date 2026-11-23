package core

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	ErrDiskChanged = errors.New("файл на диске изменён в обход duet")
	ErrStopping    = errors.New("агент останавливается")
)

// DiskChangedError — файл изменён на диске, а взять это правкой владельца пока нельзя
// (есть непросмотренный ответ, агент пропал посреди работы, открыт редактор).
type DiskChangedError struct{ Paths []string }

func (e *DiskChangedError) Error() string {
	return ErrDiskChanged.Error() + ": " + strings.Join(e.Paths, ", ")
}
func (e *DiskChangedError) Is(t error) bool { return t == ErrDiskChanged }

// Disk — файлы, с которыми duet сверяет ревью. Пути абсолютные.
type Disk interface {
	// Read возвращает содержимое файла; exists == false, если файла нет.
	Read(path string) (content string, exists bool, err error)
	// Write пишет файл, создавая недостающие папки.
	Write(path, content string) error
	// Remove удаляет файл; если его нет — не ошибка.
	Remove(path string) error
	// Stamp — быстрый отпечаток файла (время изменения и размер), "" если файла нет.
	// Пока отпечаток не изменился, файл не перечитывается.
	Stamp(path string) (string, error)
}

type osDisk struct{}

func (osDisk) Read(p string) (string, bool, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

func (osDisk) Write(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// WriteFile сохраняет права уже существующего файла и не ломает ссылки на него.
	return os.WriteFile(p, []byte(content), 0o644)
}

func (osDisk) Remove(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (osDisk) Stamp(p string) (string, error) {
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%v", st.ModTime().UnixNano(), st.Size(), st.Mode()), nil
}

// NoticeReason — почему duet поменял файл, о котором знает агент.
type NoticeReason string

const (
	NoticeOwnerEdit   NoticeReason = "owner-edit"  // правка владельца, в том числе взятая с диска
	NoticeRevert      NoticeReason = "revert"      // «вернуть к базе»
	NoticeRejected    NoticeReason = "rejected"    // ответ отклонён
	NoticeInterrupted NoticeReason = "interrupted" // работа прервана
	NoticeAccepted    NoticeReason = "accepted"    // ревью принято целиком: Files — принятые файлы
)

// Notice — сообщение агенту: какие файлы поменялись и почему, какие комментарии вернулись владельцу.
// Агент перечитывает с диска ровно эти файлы.
type Notice struct {
	Reason   NoticeReason `json:"reason"`
	Files    []string     `json:"files,omitempty"`
	Comments []string     `json:"comments,omitempty"`
	At       time.Time    `json:"at"`
}

func (c *Core) notice(r *Review, reason NoticeReason, files, comments []string) {
	if len(files) == 0 && len(comments) == 0 {
		return
	}
	// Правки владельца копятся в одно сообщение: агент перечитает каждый файл один раз.
	if n := len(r.Notices); n > 0 && len(comments) == 0 && (reason == NoticeOwnerEdit || reason == NoticeRevert) &&
		r.Notices[n-1].Reason == reason {
		last := &r.Notices[n-1]
		for _, f := range files {
			if !slices.Contains(last.Files, f) {
				last.Files = append(last.Files, f)
			}
		}
		last.At = c.now()
		return
	}
	r.Notices = append(r.Notices, Notice{Reason: reason, Files: files, Comments: comments, At: c.now()})
}

// wakes — сообщение стоит того, чтобы будить свободного агента. Правку владельца агент
// узнаёт со следующими комментариями: иначе каждое сохранение в редакторе тратило бы его ход.
func (n Notice) wakes() bool { return n.Reason != NoticeOwnerEdit && n.Reason != NoticeRevert }

// TakeNotices — агент забирает накопленные сообщения.
func (c *Core) TakeNotices(reviewID string) ([]Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return nil, err
	}
	if err := c.validateAcceptedNotice(r); err != nil {
		return nil, err
	}
	out := r.Notices
	if len(out) == 0 {
		return nil, nil
	}
	r.Notices = nil
	return out, c.save()
}

// expected — что должно лежать на диске: версия из непросмотренного ответа, иначе текущая.
func (c *Core) expected(r *Review, f *File) (Version, bool) {
	if _, moved := r.Moved[f.Path]; moved {
		return Version{}, false
	}
	if p := r.pending(); p != nil {
		if n, ok := p.Files[f.Path]; ok {
			return f.Versions[n], true
		}
	}
	if f.Current < 0 {
		return Version{}, false
	}
	return f.Versions[f.Current], true
}

// readDisk — что лежит на диске, как версия.
func (c *Core) readDisk(path string) (Version, error) {
	if st, err := c.disk.Stamp(path); err == nil {
		c.stamps[path] = st
	}
	s, ok, err := c.disk.Read(path)
	if err != nil {
		return Version{}, err
	}
	return Version{Content: s, Deleted: !ok}, nil
}

// writeDisk пишет версию на диск: «удалён» — удаляет файл.
func (c *Core) writeDisk(path string, v Version) error {
	if c.disk == nil {
		return nil
	}
	var err error
	if v.Deleted {
		err = c.disk.Remove(path)
	} else {
		err = c.disk.Write(path, v.Content)
	}
	if st, e := c.disk.Stamp(path); e == nil {
		c.stamps[path] = st
	}
	delete(c.drift, path)
	if f := c.s.Files[path]; f != nil {
		c.invalidateRepositoryDiff(c.s.Reviews[f.ReviewID], path)
	}
	return err
}

// watched — duet следит за диском ревью: оно уже понадобилось после запуска, агент не работает
// и не останавливается, и ревью не принято целиком. Пока в ревью есть пометка «изменён на диске»,
// слежка не прекращается, иначе ревью стало бы принятым целиком с непросмотренным на диске.
func (c *Core) watched(r *Review) bool {
	if c.disk == nil || !c.synced[r.ID] || r.Working || r.Busy || r.Stopping {
		return false
	}
	return !c.allDone(r) || len(c.drifted(r)) > 0
}

func (c *Core) files(r *Review) []*File {
	var out []*File
	for _, p := range r.Paths {
		if _, moved := r.Moved[p]; !moved {
			out = append(out, c.s.Files[p])
		}
	}
	return out
}

// adoptable — изменение на диске можно сразу взять правкой владельца: менять текущую версию
// можно и редактор правки не открыт.
func (c *Core) adoptable(r *Review) bool { return c.checkChange(r) == nil && !r.Editing }

// checkFile сверяет один файл с диском. Изменение на диске сразу становится правкой владельца,
// а если сейчас это нельзя — файл помечен «изменён на диске» до тех пор, пока станет можно.
// Возвращает true, если правка взята.
func (c *Core) checkFile(r *Review, f *File) bool {
	if r.Completeness != nil {
		for _, p := range r.Completeness.Problems {
			if p.Path == "" {
				return false
			}
		}
	}
	for _, p := range c.pathProblems(r, f.Path) {
		if p.Code != "unknown-base" {
			return false
		}
	}
	exp, ok := c.expected(r, f)
	if !ok {
		c.undrift(r, f.Path)
		return false
	}
	d, err := c.readDisk(f.Path)
	if err != nil {
		return false // не прочитали — оставляем как было, попробуем в следующий раз
	}
	if same(d, exp) {
		c.undrift(r, f.Path)
		return false
	}
	if c.adoptable(r) {
		c.adopt(r, f, d)
		return true
	}
	if old, ok := c.drift[f.Path]; !ok || !same(*old, d) {
		c.changed(r.ID)
	}
	c.drift[f.Path] = &d
	return false
}

// adopt — то, что лежит на диске, становится текущей версией как правка владельца.
func (c *Core) adopt(r *Review, f *File, d Version) {
	c.invalidateRepositoryDiff(r, f.Path)
	delete(c.drift, f.Path)
	d.Author = "owner"
	n := f.Base
	if f.Base < 0 || !same(f.Versions[f.Base], d) {
		n = c.addVersion(f, d)
	}
	c.reanchor(f, f.Current, n, nil)
	f.Current = n
	c.notice(r, NoticeOwnerEdit, []string{f.Path}, nil)
	fd := c.diskTaken[r.ID]
	if fd == nil || c.now().Sub(fd.At) > 3*time.Second {
		fd = &FromDisk{Seq: c.fromDiskSeq(r.ID) + 1}
		c.diskTaken[r.ID] = fd
	}
	fd.At = c.now()
	if !slices.Contains(fd.Files, f.Path) {
		fd.Files = append(fd.Files, f.Path)
	}
	c.changed(r.ID)
}

// FromDisk — duet взял изменения с диска правкой владельца (для подсказки в клиенте).
// Изменения, взятые одно за другим, собираются вместе под одним номером.
type FromDisk struct {
	Seq   int       `json:"seq"`
	Files []string  `json:"files"`
	At    time.Time `json:"at"`
}

func (c *Core) fromDiskSeq(id string) int {
	if fd := c.diskTaken[id]; fd != nil {
		return fd.Seq
	}
	return 0
}

// undrift снимает пометку «изменён на диске».
func (c *Core) undrift(r *Review, path string) {
	if _, ok := c.drift[path]; ok {
		delete(c.drift, path)
		c.changed(r.ID)
	}
}

// recheck сверяет всё ревью, если за ним следим, иначе снимает пометки.
func (c *Core) recheck(r *Review, progress func(done, total int)) {
	fs := c.files(r)
	if !c.watched(r) {
		for _, f := range fs {
			c.undrift(r, f.Path)
		}
		return
	}
	adopted := false
	for i, f := range fs {
		if c.checkFile(r, f) {
			adopted = true
		}
		if progress != nil {
			progress(i+1, len(fs))
		}
	}
	c.adopted(r, adopted)
}

// adopted — после сверки, в которой взяты правки с диска: ревью могло стать принятым целиком,
// а состояние нужно сохранить, даже если дальше действие откажет.
func (c *Core) adopted(r *Review, any bool) {
	if !any {
		return
	}
	c.acceptedCheck(r)
	if err := c.save(); err != nil {
		log.Printf("duet: сохранить правку с диска: %v", err)
	}
}

// touch — в ревью что-то происходит: оно сверяется с диском, и дальше duet за ним следит.
func (c *Core) touch(r *Review) {
	if c.disk == nil || c.repositoryChecks[r.ID] {
		return
	}
	c.synced[r.ID] = true
	c.recheck(r, nil)
}

// commit — сохранить состояние после изменения ревью и сверить его с диском.
func (c *Core) commit(r *Review) error {
	c.touch(r)
	c.acceptedCheck(r)
	c.changed(r.ID)
	return c.save()
}

// acceptedCheck — ревью стало принятым целиком: агент получает сообщение со списком принятого.
// Ревью, которое было принятым с самого начала (агент прислал файлы без правок), не считается.
func (c *Core) acceptedCheck(r *Review) {
	if c.repositoryChecks[r.ID] {
		return
	}
	if c.repositoryDir(r) != "" && (r.Busy || r.Editing) {
		if !c.allDone(r) || len(c.drifted(r)) > 0 {
			r.Open = true
			c.removeAcceptedNotices(r)
		}
		return
	}
	if r.Open && c.filesDone(r) && !r.Editing && !r.Busy && c.repositoryDir(r) != "" {
		if _, err := c.finishCollected(r, nil, nil); err != nil {
			r.Open = true
			return
		}
	}
	done := c.allDone(r) && len(c.drifted(r)) == 0
	if !done {
		c.removeAcceptedNotices(r)
		r.Open = true
		return
	}
	if !r.Open {
		return
	}
	r.Open = false
	var files []string
	for _, f := range c.files(r) {
		if f.Current >= 0 {
			files = append(files, f.Path)
		}
	}
	c.notice(r, NoticeAccepted, files, nil)
}

// Sync — владелец открыл ревью: duet сверяет его с диском, сообщая прогресс,
// и дальше следит за ним. После запуска duet сам ничего не сверяет.
func (c *Core) Sync(reviewID string, progress func(done, total int)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if c.disk == nil {
		return nil
	}
	if !c.synced[r.ID] {
		c.synced[r.ID] = true
		c.changed(r.ID)
	}
	if check := r.Completeness; check != nil && !check.Complete && !r.Busy && !r.Working && !r.Editing && !r.Stopping &&
		!c.repositoryChecks[r.ID] && slices.ContainsFunc(check.Problems, func(p RepositoryProblem) bool {
		return p.Code == "conflict" || p.Code == "deferred-base"
	}) {
		if _, err := c.finishCollected(r, nil, nil); err != nil {
			return err
		}
	}
	c.recheck(r, progress)
	return nil
}

// Poll — одна проверка диска: перечитываются только файлы, у которых поменялся отпечаток.
func (c *Core) Poll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.disk == nil {
		return
	}
	for id := range c.synced {
		if c.repositoryChecks[id] {
			continue
		}
		r := c.s.Reviews[id]
		if !c.watched(r) {
			c.recheck(r, nil)
			continue
		}
		adopted := false
		for _, f := range c.files(r) {
			if st, err := c.disk.Stamp(f.Path); err == nil && st == c.stamps[f.Path] {
				if _, held := c.drift[f.Path]; !held || !c.adoptable(r) {
					continue
				}
			}
			if c.checkFile(r, f) {
				adopted = true
			}
		}
		c.adopted(r, adopted)
	}
}

// Watch проверяет диск раз в interval, пока не вызвана возвращённая функция.
func (c *Core) Watch(interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				c.Poll()
			}
		}
	}()
	return func() { close(done) }
}

// drifted — файлы ревью с пометкой «изменён на диске», по порядку.
func (c *Core) drifted(r *Review) []string {
	var out []string
	for _, f := range c.files(r) {
		if _, ok := c.drift[f.Path]; ok {
			out = append(out, f.Path)
		}
	}
	return out
}

// AgentStopped — агент остановился после «прервать»: duet возвращает диск.
func (c *Core) AgentStopped(reviewID string) error { return c.restoreNow(reviewID) }

// RestoreAnyway — «вернуть файлы всё равно»: агент не остановился, владелец возвращает диск сам.
func (c *Core) RestoreAnyway(reviewID string) error { return c.restoreNow(reviewID) }

func (c *Core) restoreNow(reviewID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if !r.Stopping {
		return nil
	}
	err = c.restore(r)
	if e := c.commit(r); err == nil {
		err = e
	}
	return err
}

// restore — после «прервать»: диск возвращается к текущим версиям без проверки,
// файлы, созданные в выброшенном заходе, удаляются.
func (c *Core) restore(r *Review) error {
	var restored []string
	var firstErr error
	if c.disk != nil {
		for _, f := range c.files(r) {
			want := Version{Deleted: true}
			if v, ok := c.expected(r, f); ok {
				want = v
			}
			d, err := c.readDisk(f.Path)
			if err == nil && same(d, want) {
				continue
			}
			if err := c.writeDisk(f.Path, want); err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			restored = append(restored, f.Path)
		}
	}
	sort.Strings(restored)
	r.Stopping = false
	c.notice(r, NoticeInterrupted, restored, r.Returned)
	r.Returned = nil
	r.Restored = restored
	return firstErr
}
