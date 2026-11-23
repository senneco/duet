package core

import (
	"path/filepath"
	"testing"
)

func TestSavedLegacyWarningClearsOnOpen(t *testing.T) {
	c, id, root, path := repositoryReview(t)
	must(t, c.SetBusy("s", false))
	f := c.s.Files[path]
	f.BaseSource = BaseSource{Kind: "unknown", Detail: "старый формат"}
	check := c.s.Reviews[id].Completeness
	check.Complete = false
	legacy := RepositoryProblem{Path: path, Code: "unknown-base", Message: f.BaseSource.Detail}
	other := RepositoryProblem{Path: filepath.Join(root, "broken"), Code: "unsupported", Message: "binary"}
	check.Problems = []RepositoryProblem{legacy, other}
	must(t, c.save())
	c, err := Open(c.Dir())
	must(t, err)
	if v := rv(t, c, id); v.Completeness.Complete || len(v.Completeness.Problems) != 1 || v.Completeness.Problems[0] != other {
		t.Fatal(v.Completeness)
	}
	c.s.Reviews[id].Completeness.Problems = []RepositoryProblem{legacy}
	must(t, c.save())
	c, err = Open(c.Dir())
	must(t, err)
	v := fv(t, c, id, path)
	if !rv(t, c, id).Completeness.Complete || len(v.Problems) != 0 || isViewAccepted(v) || v.Base != "committed\n" {
		t.Fatal(v)
	}
	must(t, c.AcceptFile(id, path))
	if !rv(t, c, id).Accepted {
		t.Fatal("обычное принятие файла должно завершить ревью")
	}
}

func TestMovePartOfPendingResponseIntoExistingResponse(t *testing.T) {
	for _, reject := range []bool{false, true} {
		c, dir := open(t)
		source := create(t, c, Submission{Path: "/a", Content: "old"}, Submission{Path: "/b", Content: "keep"})
		target := create(t, c, Submission{Path: "/c", Content: "start"})
		// Разные номера текущих ответов и совпадающие номера старых ответов.
		respond(t, c, target, Submission{Path: "/c", Content: "first"})
		must(t, c.AcceptResponse(target))
		respond(t, c, target, Submission{Path: "/c", Content: "second"})
		cm := addC(t, c, source, "/a", nil, "замечание")
		sendTake(t, c, source)
		_, err := c.AgentFinish(source, []Submission{{Path: "/a", Content: "reply"}, {Path: "/b", Content: "other reply"}},
			[]Answer{{CommentID: cm, Text: "исправил"}})
		must(t, err)
		must(t, c.Disconnect(source))
		window, err := c.AddComment(source, "/a", sel("reply", "ply"), "проверить", nil, true)
		must(t, err)
		busy, err := c.AddFiles(target, []Submission{{Path: "/a", Content: "reply"}})
		must(t, err)
		if len(busy) != 0 {
			t.Fatal(busy)
		}
		c = reopen(t, dir)
		p := rv(t, c, target).Response
		src := rv(t, c, source).Response
		if src == nil || len(src.Files) != 1 || src.Files[0] != "/b" || src.Answers != 0 ||
			p == nil || p.Answers != 1 || len(p.Files) != 2 || p.Mine != 1 || p.Unsent != 1 {
			t.Fatalf("source=%+v target=%+v", src, p)
		}
		thread := cv(t, c, target, "/a", cm).Thread
		if len(thread) != 2 || thread[1].Text != "исправил" || thread[1].Response != p.ID ||
			!cv(t, c, target, "/a", window).InResponse {
			t.Fatal(thread)
		}
		must(t, c.RejectResponse(source))
		if v := fv(t, c, target, "/a"); v.Current != "old" || v.Response == nil || *v.Response != "reply" {
			t.Fatal(v)
		}
		if reject {
			must(t, c.RejectResponse(target))
			if v := fv(t, c, target, "/a"); v.Current != "old" || len(v.Comments) != 1 || v.Comments[0].Status != CommentUnsent {
				t.Fatal(v)
			}
		} else {
			must(t, c.AcceptResponse(target))
			if v := fv(t, c, target, "/a"); v.Current != "reply" || isViewAccepted(v) ||
				len(v.Comments) != 2 || cv(t, c, target, "/a", window).InResponse {
				t.Fatal(v)
			}
		}
	}
}

func TestCommittedDiscussionFollowsPRUpdate(t *testing.T) {
	c, source, root, path := repositoryReview(t)
	_, err := c.Stop("s", false)
	must(t, err)
	cm := comment(t, c, source, path, "проверить после обновления PR")
	must(t, c.MarkOpened(source, path))
	baseCommit(t, root)
	next := baseWrite(t, root, "next", "another change")
	target := create(t, c, Submission{Path: next, Content: "another change"})
	_, err = c.PrepareWeb(Session{ID: "next", Cwd: root}, target, []string{next})
	must(t, err)
	_, err = c.Stop("next", false)
	must(t, err)
	must(t, c.RecheckRepository(source))
	must(t, c.RecheckRepository(target))
	c, err = Open(c.Dir())
	must(t, err)
	v := fv(t, c, target, path)
	if !rv(t, c, target).Completeness.Complete || v.State != StateOpened || v.RepositoryDiff != "unchanged" ||
		len(v.Comments) != 1 || v.Comments[0].ID != cm || v.Comments[0].Status != CommentUnsent {
		t.Fatal(v)
	}
}
