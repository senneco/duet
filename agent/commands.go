package agent

import (
	"fmt"
	"strings"

	"github.com/senneco/duet/core"
)

// Who — сессия агента, от имени которой пришла команда.
type Who struct {
	Session string            `json:"session"`
	Agent   string            `json:"agent"` // claude, codex
	PID     int               `json:"pid"`
	PIDs    []int             `json:"pids"` // окна агента: сессия жива, пока жив хоть одно
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
}

func (w Who) session() core.Session {
	pids := w.PIDs
	if w.PID > 0 {
		pids = append([]int{w.PID}, pids...)
	}
	return core.Session{ID: w.Session, Agent: w.Agent, PIDs: pids, Cwd: w.Cwd, Env: w.Env}
}

// Problem — ошибка, которую агент должен прочитать и исправить (код выхода 1, текст в stderr).
type Problem struct{ Text string }

func (p *Problem) Error() string { return p.Text }

func problem(format string, a ...any) error { return &Problem{fmt.Sprintf(format, a...)} }

func (s *Service) check(w Who) error {
	if !safeID(w.Session) {
		return problem("duet: no agent session. Run duet commands from a Claude Code or Codex session.")
	}
	return nil
}

// SubmitFile — файл, который агент отдаёт на ревью. Пути уже абсолютные, содержимое прочитано.
type SubmitFile struct {
	Path     string         `json:"path"`
	Content  string         `json:"content"`
	Deleted  bool           `json:"deleted"`
	Original *core.Original `json:"original"`
	Blocks   []core.Block   `json:"blocks"`
}

type SubmitReq struct {
	Who   Who          `json:"who"`
	Title string       `json:"title"`
	Note  string       `json:"note"`
	New   bool         `json:"new"` // новая задача — новое ревью
	Files []SubmitFile `json:"files"`
}

// Submit — отдать на ревью: в ревью сессии или, если его нет или задача новая, в новое.
func (s *Service) Submit(q SubmitReq) (string, error) {
	if err := s.check(q.Who); err != nil {
		return "", err
	}
	defer s.syncMarkers()
	var subs []core.Submission
	for _, f := range q.Files {
		subs = append(subs, core.Submission{Path: f.Path, Content: f.Content, Deleted: f.Deleted, Original: f.Original, Blocks: f.Blocks})
	}
	cwd := q.Who.Cwd
	se, ok := s.core.SessionInfo(q.Who.Session)
	var b strings.Builder
	if !ok || se.Review == "" || q.New {
		if len(subs) == 0 {
			return "", problem("duet: nothing to submit: list the files.")
		}
		title := q.Title
		if title == "" {
			title = rel(cwd, subs[0].Path)
		}
		id, notAdded, err := s.core.CreateReview(title, subs)
		if err != nil {
			return "", err
		}
		if q.Note != "" {
			s.core.SetNote(id, q.Note)
		}
		if err := s.core.Attach(q.Who.session(), id); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "New review %s: %s\nWrite this link in the chat for the owner.\n", id, s.URL(id))
		writeNotAdded(&b, cwd, notAdded)
		return b.String(), nil
	}
	id := se.Review
	if err := s.core.Attach(q.Who.session(), id); err != nil {
		return "", s.attachErr(err)
	}
	// Файлы, которые уже в ревью, приходят ответом сами: duet берёт их с диска.
	var add []core.Submission
	var already []string
	for _, sub := range subs {
		if s.core.Owns(q.Who.Session, sub.Path) {
			already = append(already, sub.Path)
			if sub.Original != nil {
				if err := s.core.SetOriginal(id, sub.Path, *sub.Original); err == nil {
					fmt.Fprintf(&b, "Original version set for %s.\n", rel(cwd, sub.Path))
				}
			}
			if sub.Blocks != nil {
				s.core.SetBlocks(id, sub.Path, sub.Blocks)
			}
			continue
		}
		add = append(add, sub)
	}
	notAdded, err := s.core.AddFiles(id, add)
	if err != nil {
		return "", err
	}
	if q.Note != "" {
		s.core.SetNote(id, q.Note)
	}
	fmt.Fprintf(&b, "Review %s: %s\n", id, s.URL(id))
	if len(add) > len(notAdded) {
		var ps []string
		for _, a := range add {
			ps = append(ps, a.Path)
		}
		fmt.Fprintf(&b, "Added: %s\n", rels(cwd, ps))
	}
	if len(already) > 0 {
		fmt.Fprintf(&b, "Already in the review: %s. Your changes to them reach the owner as your response by themselves.\n", rels(cwd, already))
	}
	writeNotAdded(&b, cwd, notAdded)
	return b.String(), nil
}

func writeNotAdded(b *strings.Builder, cwd string, notAdded []core.Busy) {
	for _, n := range notAdded {
		fmt.Fprintf(b, "Not added: %s is busy in review %s (an agent works there or it has an unviewed response). Tell the owner; send it again when asked.\n", rel(cwd, n.Path), n.ReviewID)
	}
}

func (s *Service) attachErr(err error) error {
	if errorsIs(err, core.ErrOtherAgent) {
		return problem("duet: another agent is working in this review right now. Tell the owner (in their language): wait until it finishes, or interrupt it in duet.")
	}
	return err
}

// Connect — подхватить ревью по ссылке или номеру: агент получает файлы, записку и комментарии.
func (s *Service) Connect(w Who, review string) (string, error) {
	if err := s.check(w); err != nil {
		return "", err
	}
	defer s.syncMarkers()
	review = reviewID(review)
	ra, err := s.core.ForAgent(review)
	if err != nil {
		return "", problem("duet: no review %q.", review)
	}
	if err := s.core.Attach(w.session(), review); err != nil {
		return "", s.attachErr(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Connected to review %s %q: %s\n", ra.ID, ra.Title, s.URL(ra.ID))
	fmt.Fprintf(&b, "You know nothing about this task yet: read the files and the comments below before doing anything.\n")
	if ra.Note != "" {
		fmt.Fprintf(&b, "\nNote from the previous agent (start here):\n%s\n", ra.Note)
	}
	fmt.Fprintf(&b, "\nFiles:\n")
	for _, p := range ra.Files {
		fmt.Fprintf(&b, "- %s\n", rel(w.Cwd, p))
	}
	if len(ra.Comments) > 0 {
		fmt.Fprintf(&b, "\nOpen review comments (status in brackets; you will get the ones to work on when the owner sends them):\n")
		for _, cm := range ra.Comments {
			fmt.Fprintf(&b, "(%s)%s", ra.Statuses[cm.ID], comments(w.Cwd, []core.AgentComment{cm}))
		}
	}
	return b.String(), nil
}

// reviewID — номер ревью из ссылки http://duet.localhost:4848/r/<id> или как есть.
func reviewID(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "/r/"); i >= 0 {
		s = s[i+3:]
	}
	return strings.Trim(s, "/ ")
}

type DoneReq struct {
	Who     Who                     `json:"who"`
	Answers []core.Answer           `json:"answers"`
	Note    *string                 `json:"note"`
	Blocks  map[string][]core.Block `json:"blocks"` // путь → блоки
}

// Done — агент закончил заход: ответы на комментарии, правки duet берёт с диска.
func (s *Service) Done(q DoneReq) (string, error) {
	se, err := s.connected(q.Who)
	if err != nil {
		return "", err
	}
	res, err := s.core.Done(q.Who.Session, q.Answers)
	var ue *core.UnansweredError
	if errorsAs(err, &ue) {
		return "", &Problem{"Not done. " + unanswered(se.Cwd, ue.Comments)}
	}
	if errorsIs(err, core.ErrStopping) {
		return "", &Problem{denied(se.Cwd, err)}
	}
	if err != nil {
		return "", err
	}
	if q.Note != nil {
		s.core.SetNote(se.Review, *q.Note)
	}
	for p, bl := range q.Blocks {
		s.core.SetBlocks(se.Review, p, bl)
	}
	switch {
	case len(res.New) > 0:
		return "Answers saved. While you worked, the owner sent more comments. Tell the owner in one line which you took, " +
			"work on them, then call `duet done` again with answers to these.\n" + comments(se.Cwd, res.New), nil
	case res.Responded:
		return "Done. The owner will see your response in duet. Tell the owner in one short line.", nil
	}
	return "Nothing to finish: no taken comments and no changes to review files.", nil
}

func (s *Service) connected(w Who) (core.Session, error) {
	if err := s.check(w); err != nil {
		return core.Session{}, err
	}
	se, ok := s.core.SessionInfo(w.Session)
	if !ok || se.Review == "" {
		if k := s.core.TakeKicked(w.Session); k != "" {
			s.syncMarkers()
			return core.Session{}, &Problem{kicked(k)}
		}
		return core.Session{}, problem("duet: this session is not connected to a review. Submit files with `duet submit` or pick up a review with `duet connect <link>`.")
	}
	return se, nil
}

// Original — исходная версия файла, которую duet просил у агента.
func (s *Service) Original(w Who, path string, o core.Original) (string, error) {
	se, err := s.connected(w)
	if err != nil {
		return "", err
	}
	err = s.core.SetOriginal(se.Review, path, o)
	if errorsIs(err, core.ErrHasBase) {
		return "Nothing to do: " + rel(se.Cwd, path) + " already has a base.", nil
	}
	if errorsIs(err, core.ErrNotFound) {
		return "", problem("duet: %s is not in the review.", rel(se.Cwd, path))
	}
	if err != nil {
		return "", err
	}
	return "Original version of " + rel(se.Cwd, path) + " saved.", nil
}

// Take — забрать комментарии и сообщения сейчас.
func (s *Service) Take(w Who) (string, error) {
	se, err := s.connected(w)
	if err != nil {
		return "", err
	}
	cms, ns, err := s.core.Deliver(w.Session)
	if err != nil {
		return "", err
	}
	if len(cms) == 0 && len(ns) == 0 {
		return "Nothing new from the owner.", nil
	}
	return delivery(se.Cwd, cms, ns), nil
}

// Status — к какому ревью подключена сессия.
func (s *Service) Status(w Who) (string, error) {
	se, err := s.connected(w)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Connected to review %s: %s", se.Review, s.URL(se.Review)), nil
}

// ReviewOf — ревью сессии для `duet web`; "" — сессия не подключена.
func (s *Service) ReviewOf(session string) string {
	se, ok := s.core.SessionInfo(session)
	if !ok {
		return ""
	}
	return se.Review
}
