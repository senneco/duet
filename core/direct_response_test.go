package core

import "testing"

func TestDirectResponsePreservesBaseAndHistory(t *testing.T) {
	c, dir := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "old", Original: orig("old")}, Submission{Path: "/other", Content: "unreviewed"})
	must(t, c.AcceptFile(r, "/a"))
	respond(t, c, r, Submission{Path: "/a", Content: "new"})
	if rv, _ := c.Review(r); rv.HasResponse || rv.Accepted {
		t.Fatalf("%+v", rv)
	}
	v := fv(t, c, r, "/a")
	if v.Current != "new" || v.Base != "old" || v.State == StateAccepted {
		t.Fatalf("%+v", v)
	}
	c = reopen(t, dir)
	if c.s.Reviews[r].Responses[0].Status != ResponseDirect {
		t.Fatal("история прямого ответа потеряна")
	}
	// Следующий ответ уже касается непринятого файла.
	respond(t, c, r, Submission{Path: "/a", Content: "latest"})
	if rv, _ := c.Review(r); !rv.HasResponse {
		t.Fatal("нужно окно ответа")
	}
	must(t, c.AcceptResponse(r))
	if v := fv(t, c, r, "/a"); v.Base != "old" || v.Current != "latest" {
		t.Fatalf("%+v", v)
	}
	must(t, c.AcceptFile(r, "/a"))
	if v := fv(t, c, r, "/a"); v.Base != "latest" || v.State != StateAccepted {
		t.Fatalf("%+v", v)
	}
}

func TestDirectAnswerVisibleUntilFileAccepted(t *testing.T) {
	for _, question := range []bool{false, true} {
		t.Run(map[bool]string{false: "answer", true: "question"}[question], func(t *testing.T) {
			c, _ := open(t)
			r := create(t, c, Submission{Path: "/a", Content: doc, Original: orig(doc)})
			must(t, c.AcceptFile(r, "/a"))
			id := addC(t, c, r, "/a", sel(doc, "два"), "замечание")
			sendTake(t, c, r)
			_, err := c.AgentFinish(r, nil, []Answer{{CommentID: id, Text: "ответ", Question: question}})
			must(t, err)
			v := fv(t, c, r, "/a")
			if rv, _ := c.Review(r); rv.HasResponse {
				t.Fatal("окно не нужно")
			}
			if !cv(t, c, r, "/a", id).Direct || v.Base != doc || v.Current != doc {
				t.Fatalf("%+v", v)
			}
			if !question && v.State == StateAccepted {
				t.Fatal("ответ требует принятия файла")
			}
			must(t, c.AcceptFile(r, "/a"))
			if question {
				if status(t, c, r, "/a", id) != CommentQuestion {
					t.Fatal("вопрос потерян")
				}
			} else if len(fv(t, c, r, "/a").Comments) != 0 {
				t.Fatal("ответ не решён")
			}
		})
	}
}

func TestNewFileAndAcceptedFileBecomeCurrent(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "old", Original: orig("old")})
	must(t, c.AcceptFile(r, "/a"))
	respond(t, c, r, Submission{Path: "/a", Content: "new"}, Submission{Path: "/new", Content: "created"})
	if rv, _ := c.Review(r); rv.HasResponse {
		t.Fatal("новый файл не требует окна")
	}
	if v := fv(t, c, r, "/a"); v.Current != "new" || v.Base != "old" {
		t.Fatalf("%+v", v)
	}
}

func TestTwoNewFilesBecomeCurrentWithoutBase(t *testing.T) {
	c, dir := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "old", Original: orig("old")})
	respond(t, c, r, Submission{Path: "/new1", Content: "first"}, Submission{Path: "/new2", Content: "second"})
	c = reopen(t, dir)
	if rv, _ := c.Review(r); rv.HasResponse || rv.Accepted {
		t.Fatalf("%+v", rv)
	}
	for path, content := range map[string]string{"/new1": "first", "/new2": "second"} {
		v := fv(t, c, r, path)
		if v.Absent || v.HasBase || v.Current != content || v.State == StateAccepted {
			t.Fatalf("%+v", v)
		}
		must(t, c.AcceptFile(r, path))
		if v := fv(t, c, r, path); !v.HasBase || v.Base != content || v.State != StateAccepted {
			t.Fatalf("%+v", v)
		}
	}
}

func TestNewFileWithUnacceptedFileKeepsWholeResponsePending(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "old"})
	respond(t, c, r, Submission{Path: "/a", Content: "new"}, Submission{Path: "/new", Content: "created"})
	if rv, _ := c.Review(r); !rv.HasResponse {
		t.Fatal("непринятый файл требует окна")
	}
	if v := fv(t, c, r, "/new"); !v.Absent || v.HasBase {
		t.Fatalf("%+v", v)
	}
}

func TestDirectResponseKeepsExternalDiskEditUnaccepted(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "old\n", "old\n")
	must(t, c.AcceptFile(r, a))
	must(t, c.AgentStart(r))
	p.write("a.md", "new\n")
	_, err := c.AgentFinish(r, []Submission{{Path: a, Content: "new\n"}}, nil)
	must(t, err)
	p.write("a.md", "owner\n")
	c.Poll()
	v := fv(t, c, r, a)
	if v.Base != "old\n" || v.Current != "owner\n" || v.State == StateAccepted {
		t.Fatalf("%+v", v)
	}
	if p.read("a.md") != "owner\n" {
		t.Fatal("правка владельца перезаписана")
	}
}
