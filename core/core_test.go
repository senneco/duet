package core

import (
	"errors"
	"testing"
)

func open(t *testing.T) (*Core, string) {
	t.Helper()
	dir := t.TempDir()
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.disk = nil // выдуманные пути: эти тесты проверяют поведение без диска
	return c, dir
}

// reopen — как после перезапуска duet, без диска.
func reopen(t *testing.T, dir string) *Core {
	t.Helper()
	c, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.disk = nil
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func create(t *testing.T, c *Core, files ...Submission) string {
	t.Helper()
	id, _, err := c.CreateReview("ревью", files)
	must(t, err)
	return id
}

func fv(t *testing.T, c *Core, r, p string) FileView {
	t.Helper()
	v, err := c.File(r, p)
	must(t, err)
	return v
}

func respond(t *testing.T, c *Core, r string, files ...Submission) {
	t.Helper()
	must(t, c.AgentStart(r))
	if _, err := c.AgentFinish(r, files, nil); err != nil {
		t.Fatal(err)
	}
}

func orig(s string) *Original { return &Original{Content: s, Source: "из git, HEAD"} }

// 1
func TestNewFileWithoutOriginal(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a.md", Content: "x"})
	v := fv(t, c, r, "/a.md")
	if v.HasBase || v.State != StateNew || v.Current != "x" {
		t.Fatalf("%+v", v)
	}
}

// 2
func TestOriginalBecomesBase(t *testing.T) {
	c, _ := open(t)
	r := create(t, c,
		Submission{Path: "/a", Content: "new", Original: orig("old")},
		Submission{Path: "/b", Content: "same", Original: orig("same")})
	a := fv(t, c, r, "/a")
	if !a.HasBase || a.Base != "old" || !a.BaseIsOriginal || a.OriginSource != "из git, HEAD" || a.State == StateAccepted {
		t.Fatalf("%+v", a)
	}
	if b := fv(t, c, r, "/b"); b.State != StateNew {
		t.Fatalf("%+v", b)
	}
}

// 3
func TestAcceptFile(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "new", Original: orig("old")})
	must(t, c.AcceptFile(r, "/a"))
	v := fv(t, c, r, "/a")
	if v.State != StateAccepted || v.Base != "new" || v.BaseIsOriginal {
		t.Fatalf("%+v", v)
	}
}

// 4, 5
func TestResponseShowsRippleAndKeepsAccepted(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"}, Submission{Path: "/b", Content: "b"})
	must(t, c.AcceptFile(r, "/a"))
	respond(t, c, r, Submission{Path: "/a", Content: "2"}, Submission{Path: "/b", Content: "changed"})
	rv, _ := c.Review(r)
	if !rv.HasResponse {
		t.Fatal("нет ответа")
	}
	a := fv(t, c, r, "/a")
	if !a.Ripple || a.State != StateAccepted || a.Current != "1" || *a.Response != "2" {
		t.Fatalf("%+v", a)
	}
	if b := fv(t, c, r, "/b"); !b.Ripple {
		t.Fatal("нет ряби по затронутому файлу")
	}
	if err := c.AcceptFile(r, "/a"); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err)
	}
	// Оба файла затронуты смешанным ответом.
}

// 6, 32
func TestAcceptResponse(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "draft", Original: orig("1")})
	must(t, c.MarkOpened(r, "/a"))
	respond(t, c, r, Submission{Path: "/a", Content: "2"})
	must(t, c.AcceptResponse(r))
	v := fv(t, c, r, "/a")
	if v.Current != "2" || v.Base != "1" || v.State != StateOpened || v.Ripple {
		t.Fatalf("%+v", v)
	}
}

// Совпадение с исходником после ответа не является принятием файла.
func TestAcceptResponseBackToBase(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "2", Original: orig("1")})
	respond(t, c, r, Submission{Path: "/a", Content: "1"})
	must(t, c.AcceptResponse(r))
	if v := fv(t, c, r, "/a"); v.State != StateNew {
		t.Fatalf("%+v", v)
	}
}

// 7 и история
func TestRejectResponse(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	respond(t, c, r, Submission{Path: "/a", Content: "2"}, Submission{Path: "/new", Content: "n"})
	must(t, c.RejectResponse(r))
	rv, _ := c.Review(r)
	if rv.HasResponse || len(rv.Files) != 1 {
		t.Fatalf("%+v", rv)
	}
	if v := fv(t, c, r, "/a"); v.Current != "1" || v.Ripple {
		t.Fatalf("%+v", v)
	}
	h, _ := c.Responses(r)
	if len(h) != 1 || h[0].Status != ResponseRejected {
		t.Fatalf("%+v", h)
	}
}

// 9: закрыть окно — ответ остаётся. В ядре это просто отсутствие действия.
func TestResponseStaysUntilDecided(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	respond(t, c, r, Submission{Path: "/a", Content: "2"})
	must(t, c.MarkOpened(r, "/a"))
	if rv, _ := c.Review(r); !rv.HasResponse {
		t.Fatal()
	}
}

// 10
func TestBlockedWhileUnviewedResponse(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1", Original: orig("0")}, Submission{Path: "/b", Content: "b", Original: orig("0")})
	respond(t, c, r, Submission{Path: "/a", Content: "2"})
	for name, err := range map[string]error{
		"правка":         c.Edit(r, "/b", "x"),
		"вернуть к базе": c.RevertToBase(r, "/b"),
		"принять файл":   c.AcceptFile(r, "/a"),
	} {
		if !errors.Is(err, ErrUnviewedResponse) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if v := fv(t, c, r, "/b"); v.Current != "b" {
		t.Fatal("правка прошла")
	}
}

// Ответ нельзя принять, пока агент работает; агент без изменений ответа не создаёт.
func TestNoResponseWithoutChanges(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	must(t, c.AgentStart(r))
	if err := c.Edit(r, "/a", "x"); !errors.Is(err, ErrAgentWorking) {
		t.Fatal(err)
	}
	got, err := c.AgentFinish(r, []Submission{{Path: "/a", Content: "1"}}, nil)
	must(t, err)
	if got {
		t.Fatal("пустой ответ")
	}
}

// 18
func TestOwnerEdit(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	must(t, c.AcceptFile(r, "/a"))
	must(t, c.Edit(r, "/a", "2"))
	if v := fv(t, c, r, "/a"); v.Current != "2" || v.State == StateAccepted {
		t.Fatalf("%+v", v)
	}
	must(t, c.Edit(r, "/a", "1"))
	if v := fv(t, c, r, "/a"); v.State != StateAccepted {
		t.Fatalf("правка обратно к базе: %+v", v)
	}
}

// 19
func TestRevertToBase(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "2", Original: orig("1")})
	must(t, c.RevertToBase(r, "/a"))
	if v := fv(t, c, r, "/a"); v.Current != "1" || v.State != StateNew {
		t.Fatalf("%+v", v)
	}
}

// 27, 29
func TestFileMovesWithState(t *testing.T) {
	c, _ := open(t)
	r1 := create(t, c, Submission{Path: "/a", Content: "2", Original: orig("1")})
	must(t, c.MarkOpened(r1, "/a"))
	r2 := create(t, c, Submission{Path: "/a", Content: "3", Original: orig("ignored")})
	v := fv(t, c, r2, "/a")
	if v.Base != "1" || v.Current != "3" || v.State != StateOpened {
		t.Fatalf("%+v", v)
	}
	old := fv(t, c, r1, "/a")
	if old.State != StateMoved || old.MovedTo != r2 {
		t.Fatalf("%+v", old)
	}
	if err := c.Edit(r1, "/a", "x"); !errors.Is(err, ErrMoved) {
		t.Fatal(err)
	}
	if err := c.AcceptFile(r1, "/a"); !errors.Is(err, ErrMoved) {
		t.Fatal(err)
	}
	if rv, _ := c.Review(r1); !rv.Accepted {
		t.Fatal("старое ревью без своих файлов должно быть принято")
	}
}

// Принятое при переходе остаётся принятым.
func TestMovedAcceptedStaysAccepted(t *testing.T) {
	c, _ := open(t)
	r1 := create(t, c, Submission{Path: "/a", Content: "1"})
	must(t, c.AcceptFile(r1, "/a"))
	r2 := create(t, c, Submission{Path: "/a", Content: "1"})
	if v := fv(t, c, r2, "/a"); v.State != StateAccepted {
		t.Fatalf("%+v", v)
	}
}

// 30
func TestBusyFileNotMoved(t *testing.T) {
	c, _ := open(t)
	r1 := create(t, c, Submission{Path: "/a", Content: "1"}, Submission{Path: "/b", Content: "1"})
	respond(t, c, r1, Submission{Path: "/a", Content: "2"})
	r2, notAdded, err := c.CreateReview("второе", []Submission{{Path: "/a", Content: "9"}, {Path: "/b", Content: "9"}})
	must(t, err)
	if len(notAdded) != 0 {
		t.Fatal(notAdded)
	}
	if v := fv(t, c, r2, "/a"); v.Current != "1" || v.Response == nil || *v.Response != "9" {
		t.Fatal(v)
	}
	if rv(t, c, r1).HasResponse {
		t.Fatal("старое ревью всё ещё управляет ответом")
	}
	// агент работает в ревью — файл тоже занят
	must(t, c.AcceptResponse(r2))
	must(t, c.AgentStart(r2))
	if _, na, _ := c.CreateReview("третье", []Submission{{Path: "/a", Content: "9"}}); len(na) != 1 {
		t.Fatal("файл ушёл из работающего ревью")
	}
}

// 31
func TestOpened(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	must(t, c.MarkOpened(r, "/a"))
	if v := fv(t, c, r, "/a"); v.State != StateOpened {
		t.Fatalf("%+v", v)
	}
}

// 34
func TestDeletedFile(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	must(t, c.AcceptFile(r, "/a"))
	respond(t, c, r, Submission{Path: "/a", Deleted: true})
	if v := fv(t, c, r, "/a"); v.State != StateDeleted {
		t.Fatalf("%+v", v)
	}
	must(t, c.AcceptFile(r, "/a"))
	if v := fv(t, c, r, "/a"); v.State != StateDeletedAccepted {
		t.Fatalf("%+v", v)
	}
	rv, _ := c.Review(r)
	if len(rv.Files) != 1 || !rv.Accepted {
		t.Fatalf("%+v", rv)
	}
}

// Новый файл без базы сразу доступен для ревью.
func TestNewFileInResponse(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	respond(t, c, r, Submission{Path: "/n", Content: "n"})
	if v := fv(t, c, r, "/n"); v.Ripple || v.Current != "n" || v.Response != nil {
		t.Fatalf("%+v", v)
	}
	if v := fv(t, c, r, "/n"); v.Current != "n" || v.HasBase || v.State != StateNew {
		t.Fatalf("%+v", v)
	}
}

func TestReviewAccepted(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"}, Submission{Path: "/b", Content: "1"})
	must(t, c.AcceptFile(r, "/a"))
	if rv, _ := c.Review(r); rv.Accepted {
		t.Fatal()
	}
	must(t, c.AcceptFile(r, "/b"))
	if rv, _ := c.Review(r); !rv.Accepted {
		t.Fatal()
	}
}

func TestAddFilesAndNoSilentChange(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "1"})
	_, err := c.AddFiles(r, []Submission{{Path: "/b", Content: "b"}})
	must(t, err)
	if _, err := c.AddFiles(r, []Submission{{Path: "/a", Content: "2"}}); err == nil {
		t.Fatal("файл поменялся мимо ответа")
	}
	if rv, _ := c.Review(r); len(rv.Files) != 2 {
		t.Fatal()
	}
}

// Перезапуск
func TestPersistence(t *testing.T) {
	c, dir := open(t)
	r := create(t, c, Submission{Path: "/a", Content: "2", Original: orig("1")})
	must(t, c.MarkOpened(r, "/a"))
	respond(t, c, r, Submission{Path: "/a", Content: "3"})
	c2 := reopen(t, dir)
	v := fv(t, c2, r, "/a")
	if v.Base != "1" || v.Current != "2" || *v.Response != "3" || v.State != StateOpened || !v.BaseIsOriginal {
		t.Fatalf("%+v", v)
	}
	must(t, c2.RejectResponse(r))
	c3 := reopen(t, dir)
	if h, _ := c3.Responses(r); len(h) != 1 || h[0].Status != ResponseRejected {
		t.Fatal(h)
	}
	r2 := create(t, c3, Submission{Path: "/z", Content: "z"})
	if r2 == r {
		t.Fatal("повтор id")
	}
}
