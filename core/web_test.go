package core

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFindReviewsByFiles(t *testing.T) {
	c, p := newProj(t)
	r1, a := newReview(t, c, p, "a.md", "a\n", "0\n")
	r2, b := newReview(t, c, p, "b.md", "b\n", "0\n")
	if got := c.FindReviews([]string{a}, p.dir); len(got) != 1 || got[0].ID != r1 {
		t.Fatal(got)
	}
	if got := c.FindReviews([]string{a, b}, p.dir); len(got) != 2 {
		t.Fatal(got)
	}
	if got := c.FindReviews(nil, p.dir); len(got) != 2 {
		t.Fatal(got)
	}
	if got := c.FindReviews(nil, p.dir+"-other"); len(got) != 0 {
		t.Fatal(got)
	}
	// Файл, переехавший в другое ревью, не находится по прежнему владельцу.
	must(t, c.AcceptFile(r1, a))
	notAdded, err := c.AddFiles(r2, []Submission{{Path: a, Content: "a\n"}})
	must(t, err)
	if len(notAdded) != 0 {
		t.Fatal(notAdded)
	}
	if got := c.FindReviews([]string{a}, p.dir); len(got) != 1 || got[0].ID != r2 {
		t.Fatal(got)
	}
}

func TestPrepareWebPreservesPendingAndBase(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "one\n", "zero\n")
	id := comment(t, c, r, a, "Уточнить")
	must(t, c.WriteCheck("s1", a, false))
	p.write("a.md", "two\n")
	stop(t, c, "s1")
	before := fv(t, c, r, a)
	if before.Response == nil {
		t.Fatal("нет исходного непросмотренного ответа")
	}
	p.write("a.md", "three\n")
	res, err := c.PrepareWeb(Session{ID: "s2", Agent: "claude", Cwd: p.dir}, r, []string{a})
	must(t, err)
	v := fv(t, c, r, a)
	if !res.Responded || v.Base != "zero\n" || v.Current != "one\n" || v.Response == nil || *v.Response != "three\n" {
		t.Fatalf("%+v %+v", res, v)
	}
	if len(v.Comments) != 1 || v.Comments[0].ID != id || status(t, c, r, a, id) != CommentUnsent {
		t.Fatal(v.Comments)
	}
	if se, _ := c.SessionInfo("s2"); se.Review != r {
		t.Fatal(se)
	}
	if rv(t, c, r).Accepted {
		t.Fatal("открытие не должно принимать ревью")
	}
}

func TestPrepareWebRefusesUnsafeChanges(t *testing.T) {
	for _, mode := range []string{"busy", "editing", "stopping", "missing", "moved"} {
		t.Run(mode, func(t *testing.T) {
			c, p := newProj(t)
			r, a := agentReview(t, c, p, "a.md", "one\n", "zero\n")
			paths := []string{a}
			var want error
			switch mode {
			case "busy":
				must(t, c.SetBusy("s1", true))
				want = ErrOtherAgent
			case "editing":
				must(t, c.SetEditing(r, true))
				want = ErrEditing
			case "stopping":
				must(t, c.WriteCheck("s1", a, false))
				must(t, c.Interrupt(r))
				want = ErrStopping
			case "missing":
				paths = append(paths, filepath.Join(p.dir, "new.md"))
			case "moved":
				must(t, c.AcceptFile(r, a))
				_, _, err := c.CreateReview("Другое", []Submission{{Path: a, Content: "one\n"}})
				must(t, err)
			}
			p.write("a.md", "two\n")
			_, err := c.PrepareWeb(Session{ID: "s2"}, r, paths)
			if err == nil || want != nil && !errors.Is(err, want) {
				t.Fatalf("ожидался отказ %v, получили %v", want, err)
			}
			if _, ok := c.SessionInfo("s2"); ok {
				t.Fatal("отказ не должен подключать новую сессию")
			}
			if mode != "moved" {
				v := fv(t, c, r, a)
				if v.Base != "zero\n" || v.Current != "one\n" || v.Response != nil {
					t.Fatal(v)
				}
			}
		})
	}
}

func TestPrepareWebKeepsAcceptedBase(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "one\n", "zero\n")
	must(t, c.AcceptFile(r, a))
	p.write("a.md", "two\n")
	res, err := c.PrepareWeb(Session{ID: "s2"}, r, []string{a})
	must(t, err)
	v := fv(t, c, r, a)
	if !res.Responded || v.Base != "one\n" || v.Current != "two\n" || v.Response != nil || rv(t, c, r).Accepted {
		t.Fatalf("%+v %+v", res, v)
	}
}
