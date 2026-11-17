package agent

import (
	"fmt"
	"strings"

	"github.com/senneco/duet/core"
)

// Reviews — кандидаты по файлам задачи или папке. Не подключает и не сверяет диск.
func (s *Service) Reviews(w Who, paths []string) string {
	return s.reviewMatches(w, s.core.FindReviews(paths, w.Cwd))
}

func (s *Service) reviewMatches(w Who, matches []core.ReviewMatch) string {
	var b strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&b, "- %s %q: %s\n", m.ID, m.Title, s.URL(m.ID))
		for _, p := range m.Paths {
			fmt.Fprintf(&b, "  %s\n", rel(w.Cwd, p))
		}
	}
	if len(matches) == 0 {
		b.WriteString("No matching reviews.\n")
	}
	b.WriteString("Use the files of the current task to choose; do not choose by date or directory alone. If no review fits, submit the prepared files with their original versions using `duet submit`, then open it. Ask the owner only if the task files still leave several suitable reviews.\n")
	return b.String()
}

type WebReq struct {
	Who    Who      `json:"who"`
	Review string   `json:"review"`
	Paths  []string `json:"paths"`
}

// Web — однозначный выбор по файлам и передача правок; без успеха браузер не открывается.
func (s *Service) Web(q WebReq) (string, error) {
	if err := s.check(q.Who); err != nil {
		return "", err
	}
	if se, ok := s.core.SessionInfo(q.Who.Session); ok && se.Kicked != "" && q.Review == "" {
		return "", problem("duet: another agent picked up this review. Do not take it back without the owner's explicit handoff.")
	}
	id := reviewID(q.Review)
	if id == "" {
		id = s.ReviewOf(q.Who.Session)
	}
	if id == "" {
		matches := s.core.FindReviews(q.Paths, q.Who.Cwd)
		if len(q.Paths) == 0 || len(matches) != 1 {
			return "", problem("duet: this session is not connected. Run `duet web --files <task file>...` with the prepared task files.\n%s", s.reviewMatches(q.Who, matches))
		}
		id = matches[0].ID
	}
	defer s.syncMarkers()
	res, err := s.core.PrepareWeb(q.Who.session(), id, q.Paths)
	var ue *core.UnansweredError
	if errorsAs(err, &ue) {
		return "", problem("Not ready to open. %s", unanswered(q.Who.Cwd, ue.Comments))
	}
	if errorsIs(err, core.ErrEditing) || errorsIs(err, core.ErrStopping) {
		return "", problem("%s", denied(q.Who.Cwd, err))
	}
	if err != nil {
		return "", s.attachErr(err)
	}
	if len(res.New) > 0 {
		return "", problem("Not ready to open: answer the new comments with `duet done`, then retry.\n%s", comments(q.Who.Cwd, res.New))
	}
	return id, nil
}
