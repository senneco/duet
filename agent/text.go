package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/senneco/duet/core"
)

// Всё, что duet пишет агенту. По-английски: агент отвечает владельцу на его языке сам.

// rel — путь для агента: относительно его папки, если файл внутри.
func rel(cwd, p string) string {
	if cwd != "" {
		if r, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
	}
	return p
}

func rels(cwd string, ps []string) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = rel(cwd, p)
	}
	return strings.Join(out, ", ")
}

func quote(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return fmt.Sprintf("%q", s)
}

// comments — комментарии с файлом, цитатой и всей веткой.
func comments(cwd string, cms []core.AgentComment) string {
	var b strings.Builder
	for _, cm := range cms {
		where := "whole file"
		if !cm.Whole && cm.Quote != "" {
			where = "on " + quote(cm.Quote, 400)
			if cm.Removed {
				where = "on text that is no longer in the file: " + quote(cm.Quote, 400)
			}
		}
		fmt.Fprintf(&b, "\n[%s] %s — %s\n", cm.ID, rel(cwd, cm.Path), where)
		for _, m := range cm.Thread {
			who := "owner"
			if m.Author == "agent" {
				who = "you"
			}
			text := strings.ReplaceAll(strings.TrimSpace(m.Text), "\n", "\n    ")
			fmt.Fprintf(&b, "  %s: %s\n", who, text)
			if len(m.Images) > 0 {
				fmt.Fprintf(&b, "    images: %s\n", strings.Join(m.Images, ", "))
			}
			if len(m.Options) > 0 {
				fmt.Fprintf(&b, "    options: %s\n", strings.Join(m.Options, " | "))
			}
		}
	}
	return b.String()
}

func newComments(cwd string, cms []core.AgentComment) string {
	n := "1 review comment"
	if len(cms) > 1 {
		n = fmt.Sprintf("%d review comments", len(cms))
	}
	return fmt.Sprintf("DUET: the owner sent %s. Tell the owner in one short line which comments you took "+
		"(e.g. \"Took 2 comments: rename X, shorten the intro\"), then work on them: edit the files directly. "+
		"When finished, answer every comment with `duet done` (see the duet skill).\n%s", n, comments(cwd, cms))
}

func notice(cwd string, n core.Notice) string {
	files := rels(cwd, n.Files)
	switch n.Reason {
	case core.NoticeOwnerEdit:
		return "DUET: the owner edited these files in duet. Re-read them from disk before changing them: " + files
	case core.NoticeRevert:
		return "DUET: the owner reverted these files to their accepted base. Re-read them from disk: " + files
	case core.NoticeOverwritten:
		return "DUET: these files were changed outside duet and the owner chose to overwrite them with the reviewed version. Re-read them from disk: " + files
	case core.NoticeRejected, core.NoticeInterrupted:
		what := "rejected your last response"
		if n.Reason == core.NoticeInterrupted {
			what = "interrupted your work"
		}
		s := "DUET: the owner " + what + ". Treat everything you did in it as not done; do not continue it."
		if len(n.Files) > 0 {
			s += " Files restored on disk to the version before it (re-read them): " + files + "."
		}
		if len(n.Comments) > 0 {
			s += " Comments returned to the owner: " + strings.Join(n.Comments, ", ") + "."
		}
		return s
	case core.NoticeAccepted:
		return "DUET: the owner accepted the whole review: " + files + ". Nothing is left to accept. " +
			"Check that you did not forget to send anything (files changed by commands, files changed before the review started) " +
			"and send forgotten ones with `duet submit`. Otherwise tell the owner that everything is accepted and ask what is next " +
			"(commit? publish?). Do not do anything else on your own."
	}
	return "DUET: " + string(n.Reason) + ": " + files
}

// delivery — комментарии и сообщения одним текстом.
func delivery(cwd string, cms []core.AgentComment, ns []core.Notice) string {
	var parts []string
	for _, n := range ns {
		parts = append(parts, notice(cwd, n))
	}
	if len(cms) > 0 {
		parts = append(parts, newComments(cwd, cms))
	} else if len(ns) > 0 {
		parts = append(parts, "Reply to the owner in one short line.")
	}
	return strings.Join(parts, "\n\n")
}

func kicked(id string) string {
	return "DUET: you are no longer connected to review " + id + ": another agent picked it up. Do not send anything to it."
}

func unanswered(cwd string, cms []core.AgentComment) string {
	return "DUET: you have not answered these review comments yet. Answer every one of them with `duet done` before you finish " +
		"(\"done …\", \"won't do, because …\", or a question with options).\n" + comments(cwd, cms)
}

func askOriginal(cwd, p string) string {
	r := rel(cwd, p)
	return fmt.Sprintf("DUET: %s was not in the review, so duet added it. If this file existed before your task, send its original "+
		"version now, e.g. `duet original %s --source \"git, HEAD\" --command \"git show HEAD:%s\"`. If the file is new, do nothing.", r, r, r)
}

func denied(cwd string, err error) string {
	var be *core.BusyError
	switch {
	case errorsIs(err, core.ErrStopping):
		return "DUET: the owner interrupted your work. Stop now: do not run any more tools. End your turn with one short line saying you stopped."
	case errorsIs(err, core.ErrUnviewedResponse):
		return "DUET: the owner has not viewed your previous response in duet yet, so review files cannot change now. " +
			"Do not change them in any way, commands included. Tell the owner (in their language) to open your response in duet first (␣r), and stop."
	case errorsIs(err, core.ErrEditing):
		return "DUET: the owner is editing a review file in duet right now. Do not change review files in any way, commands included; tell the owner you are waiting for them to finish."
	case errorsAs(err, &be):
		return fmt.Sprintf("DUET: %s is busy in another review (%s): another agent works there or it has an unviewed response. Do not edit it; tell the owner.", rel(cwd, be.Busy.Path), be.Busy.ReviewID)
	}
	return "DUET: " + err.Error()
}

// violation — агент поменял файлы ревью командой, когда это запрещено.
func violation(cwd string, files []string, why error) string {
	reason := "the owner has not viewed your previous response in duet yet"
	if errorsIs(why, core.ErrEditing) {
		reason = "the owner is editing in duet right now"
	}
	return "DUET: you changed review files with a command: " + rels(cwd, files) + ". Review files must not change now: " + reason +
		". Undo exactly this change right away (put the files back as they were before your command), do not change them again, " +
		"and tell the owner (in their language) to open your previous response in duet first (␣r)."
}
