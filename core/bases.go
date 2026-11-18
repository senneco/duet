package core

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/senneco/duet/vcs"
)

// BaseSource хранит происхождение независимо от совпадения текстов.
// Kind: none, agent, repository, owner, unknown (неоднозначные старые данные).
type BaseSource struct {
	Kind      string `json:"kind"`
	Workspace string `json:"workspace,omitempty"`
	Revision  string `json:"revision,omitempty"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

type BaseRecord struct {
	Version int        `json:"version"`
	Source  BaseSource `json:"source"`
	At      time.Time  `json:"at"`
}

type BaseSourceError struct{ Path, Reason string }

func (e *BaseSourceError) Error() string {
	return fmt.Sprintf("происхождение базы %s: %s", e.Path, e.Reason)
}

// Старый флаг исходника не является записью явного принятия. Не используем
// совпадение текстов или название источника, написанное агентом, как доказательство.
func migrateBase(f *File) {
	if f.BaseSource.Kind != "" {
		return
	}
	source := BaseSource{Kind: "unknown", Detail: "старые данные не определяют происхождение базы"}
	switch {
	case f.Base < 0 && !f.BaseIsOriginal:
		source = BaseSource{Kind: "none"}
	case f.Base >= 0 && f.Base < len(f.Versions):
		author := f.Versions[f.Base].Author
		if f.BaseIsOriginal && author == "original" {
			// Старый AcceptFile не менял флаг при Current == Base. Это могло
			// случиться и до последующих правок, поэтому даже различие текстов
			// сейчас не доказывает, что владелец никогда не принимал исходник.
			source.Detail = "исходник агента; старый формат не записывал принятие при совпадении с базой"
		} else if !f.BaseIsOriginal && (author == "agent" || author == "owner" || author == "disk") {
			source = BaseSource{Kind: "owner"}
		}
	}
	f.BaseSource = source
	// Миграция добавляет метаданные, но не меняет старые версии и комментарии.
	f.BaseHistory = append(f.BaseHistory, BaseRecord{Version: f.Base, Source: source})
}

func (c *Core) recordBase(f *File, source BaseSource) {
	f.BaseSource = source
	f.BaseHistory = append(f.BaseHistory, BaseRecord{Version: f.Base, Source: source, At: c.now()})
}

// SetRepositoryBases применяет только исходники согласованного снимка к уже
// зарегистрированным файлам ревью. Все пути снимка должны принадлежать ревью.
// Новые текущие версии, выбор ревью и сквозные вызовы подключаются в шаге 7p2.
// При любой ошибке проверки ни одна база не меняется. Диск не читается и не пишется.
func (c *Core) SetRepositoryBases(reviewID string, snapshot vcs.Snapshot) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, err := c.review(reviewID)
	if err != nil {
		return err
	}
	if snapshot.Protocol != vcs.ProtocolVersion || !snapshot.Complete() {
		return fmt.Errorf("нельзя применить базы: снимок репозитория неполон или несовместим")
	}
	if snapshot.Workspace == nil {
		return nil
	} // вне репозитория сохраняются исходники агента
	if err := c.checkChange(r); err != nil {
		return err
	}
	if r.Editing {
		return ErrEditing
	}
	if r.Busy {
		return ErrAgentWorking
	}
	w := snapshot.Workspace
	if w.ID == "" || w.Provider == "" || !filepath.IsAbs(w.Root) || filepath.Clean(w.Root) != w.Root {
		return fmt.Errorf("неверная рабочая копия снимка")
	}
	seen := map[string]bool{}
	for _, e := range snapshot.Entries {
		if !vcs.ValidPath(e.Path) || seen[e.Path] {
			return fmt.Errorf("неверный или повторный путь снимка")
		}
		seen[e.Path] = true
		path := filepath.Join(w.Root, filepath.FromSlash(e.Path))
		f, err := c.file(r, path)
		if err != nil {
			return err
		}
		if len(e.Diagnostics) > 0 || e.Original.Kind != "text" && e.Original.Kind != "absent" {
			return fmt.Errorf("исходник %s недоступен", path)
		}
		if f.BaseSource.Kind == "unknown" {
			return &BaseSourceError{Path: path, Reason: f.BaseSource.Detail}
		}
	}
	changed := false
	for _, e := range snapshot.Entries {
		f := c.s.Files[filepath.Join(w.Root, filepath.FromSlash(e.Path))]
		if f.BaseSource.Kind == "owner" {
			continue
		}
		source := BaseSource{Kind: "repository", Workspace: w.ID, Revision: snapshot.Revision, Path: e.Path}
		if source == f.BaseSource && ((e.Original.Kind == "absent" && f.Base < 0) ||
			(e.Original.Kind == "text" && f.Base >= 0 && !f.Versions[f.Base].Deleted && f.Versions[f.Base].Content == e.Original.Text)) {
			continue
		}
		f.Base = -1
		if e.Original.Kind == "text" {
			f.Base = c.addVersion(f, Version{Content: e.Original.Text, Author: "original"})
		}
		f.BaseIsOriginal = f.Base >= 0
		f.OriginSource = fmt.Sprintf("%s, commit %s, %s", w.Provider, snapshot.Revision, e.Path)
		if snapshot.Revision == "" {
			f.OriginSource = w.Provider + ", без коммита, " + e.Path
		}
		c.recordBase(f, source)
		changed = true
	}
	if !changed {
		return nil
	}
	r.Open = true
	c.changed(r.ID)
	return c.save()
}
