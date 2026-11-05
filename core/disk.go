package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrDiskChanged = errors.New("файл на диске изменён в обход duet")
	ErrStopping    = errors.New("агент останавливается")
)

// DiskChangedError — на диске лежит не то, что ожидает duet. Владелец выбирает
// «взять с диска» (TakeFromDisk) или «перезаписать» (Overwrite).
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
	NoticeOverwritten NoticeReason = "overwritten" // владелец выбрал «перезаписать»
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
	r.Notices = append(r.Notices, Notice{Reason: reason, Files: files, Comments: comments, At: c.now()})
}

// TakeNotices — агент забирает накопленные сообщения.
func (c *Core) TakeNotices(reviewID string) ([]Notice, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
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
	return err
}

// watched — duet следит за диском ревью: оно уже понадобилось после запуска, агент не работает
// и не останавливается, и ревью не принято целиком. Пока в ревью есть пометка «изменён на диске»,
// слежка не прекращается, иначе ревью стало бы принятым целиком с непросмотренным на диске.
func (c *Core) watched(r *Review) bool {
	if c.disk == nil || !c.synced[r.ID] || r.Working || r.Stopping {
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

// checkFile сверяет один файл с диском и обновляет пометку «изменён на диске».
func (c *Core) checkFile(r *Review, f *File) {
	exp, ok := c.expected(r, f)
	if !ok {
		c.undrift(r, f.Path)
		return
	}
	d, err := c.readDisk(f.Path)
	if err != nil {
		return // не прочитали — оставляем как было, попробуем в следующий раз
	}
	if same(d, exp) {
		c.undrift(r, f.Path)
		return
	}
	if old, ok := c.drift[f.Path]; !ok || !same(*old, d) {
		c.changed(r.ID)
	}
	c.drift[f.Path] = &d
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
	for i, f := range fs {
		c.checkFile(r, f)
		if progress != nil {
			progress(i+1, len(fs))
		}
	}
}

// touch — в ревью что-то происходит: оно сверяется с диском, и дальше duet за ним следит.
func (c *Core) touch(r *Review) {
	if c.disk == nil {
		return
	}
	c.synced[r.ID] = true
	c.recheck(r, nil)
}

// commit — сохранить состояние после изменения ревью и сверить его с диском.
func (c *Core) commit(r *Review) error {
	c.touch(r)
	c.changed(r.ID)
	return c.save()
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
		r := c.s.Reviews[id]
		if !c.watched(r) {
			c.recheck(r, nil)
			continue
		}
		for _, f := range c.files(r) {
			if st, err := c.disk.Stamp(f.Path); err == nil && st == c.stamps[f.Path] {
				continue
			}
			c.checkFile(r, f)
		}
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

// diskCheck — перед записью: на диске должно лежать ожидаемое. Несовпавшие файлы помечаются.
func (c *Core) diskCheck(r *Review, fs ...*File) error {
	if c.disk == nil {
		return nil
	}
	var bad []string
	for _, f := range fs {
		exp, ok := c.expected(r, f)
		if !ok {
			continue
		}
		d, err := c.readDisk(f.Path)
		if err != nil {
			return err
		}
		if !same(d, exp) {
			c.drift[f.Path] = &d
			bad = append(bad, f.Path)
		}
	}
	if len(bad) > 0 {
		return &DiskChangedError{bad}
	}
	return nil
}

// pick — файлы ревью по путям; пусто — все файлы с пометкой «изменён на диске».
func (c *Core) pick(r *Review, paths []string) ([]*File, error) {
	if len(paths) == 0 {
		c.touch(r)
		paths = c.drifted(r)
	}
	var out []*File
	for _, p := range paths {
		f, err := c.file(r, p)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// TakeFromDisk — «взять с диска»: то, что лежит на диске, становится текущей версией
// как правка владельца. Пустой paths — все файлы ревью с пометкой «изменён на диске».
func (c *Core) TakeFromDisk(reviewID string, paths []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if c.disk == nil {
		return nil
	}
	if err := c.checkChange(r); err != nil {
		return err
	}
	fs, err := c.pick(r, paths)
	if err != nil {
		return err
	}
	var changed []string
	for _, f := range fs {
		d, err := c.readDisk(f.Path)
		if err != nil {
			return err
		}
		delete(c.drift, f.Path)
		d.Author = "owner"
		if f.Current >= 0 && same(f.Versions[f.Current], d) {
			continue
		}
		n := f.Base
		if f.Base < 0 || !same(f.Versions[f.Base], d) {
			n = c.addVersion(f, d)
		}
		c.reanchor(f, f.Current, n, nil)
		f.Current = n
		changed = append(changed, f.Path)
	}
	c.notice(r, NoticeOwnerEdit, changed, nil)
	return c.commit(r)
}

// Overwrite — «перезаписать»: duet пишет на диск ожидаемую версию.
// То, что лежало на диске, остаётся в истории файла. Пустой paths — все файлы с пометкой.
func (c *Core) Overwrite(reviewID string, paths []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if c.disk == nil {
		return nil
	}
	if r.Stopping {
		return ErrStopping
	}
	if r.Working {
		return ErrAgentWorking
	}
	fs, err := c.pick(r, paths)
	if err != nil {
		return err
	}
	var changed []string
	for _, f := range fs {
		exp, ok := c.expected(r, f)
		if !ok {
			continue
		}
		d, err := c.readDisk(f.Path)
		if err != nil {
			return err
		}
		if same(d, exp) {
			delete(c.drift, f.Path)
			continue
		}
		d.Author = "disk"
		c.addVersion(f, d)
		if err := c.writeDisk(f.Path, exp); err != nil {
			return err
		}
		changed = append(changed, f.Path)
	}
	c.notice(r, NoticeOverwritten, changed, nil)
	return c.commit(r)
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
			if f.Current >= 0 {
				want = f.Versions[f.Current]
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

// DiskDiff — отличия «ожидаемое → на диске» для файла с пометкой «изменён на диске».
func (c *Core) DiskDiff(reviewID, path string) ([]Chunk, error) {
	c.mu.Lock()
	r, err := c.review(reviewID)
	var f *File
	if err == nil {
		f, err = c.file(r, path)
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	d, ok := c.drift[path]
	exp, has := c.expected(r, f)
	c.mu.Unlock()
	if !ok || !has {
		return nil, fmt.Errorf("файл %s на диске не изменён: %w", path, ErrNotFound)
	}
	old, nw := exp.Content, d.Content
	if exp.Deleted {
		old = ""
	}
	if d.Deleted {
		nw = ""
	}
	return DiffText(old, nw, IsProse(path)), nil
}
