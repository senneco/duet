package core

import (
	"errors"
	"testing"
)

// Сессии агентов: подключение, занятость, заходы с диска. Сценарии — в docs/specs/agent-protocol.md.

// agentReview — агент сессии s1 отдал файл на ревью и закончил отвечать (свободен).
func agentReview(t *testing.T, c *Core, p *proj, name, content, original string) (string, string) {
	t.Helper()
	r, a := newReview(t, c, p, name, content, original)
	must(t, c.Attach(Session{ID: "s1", Agent: "claude", Cwd: p.dir}, r))
	stop(t, c, "s1")
	return r, a
}

func stop(t *testing.T, c *Core, s string) StopResult {
	t.Helper()
	res, err := c.Stop(s, false)
	must(t, err)
	return res
}

func comment(t *testing.T, c *Core, r, path, text string) string {
	t.Helper()
	id, err := c.AddComment(r, path, nil, text, nil, false)
	must(t, err)
	return id
}

// 50: агент занят в чате — отправленные комментарии «ожидают», пока он не освободится.
func TestCommentsWaitWhileAgentChats(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.SetBusy("s1", true))
	id := comment(t, c, r, a, "поправь")
	must(t, c.Send(r))
	if st := status(t, c, r, a, id); st != CommentWaiting {
		t.Fatal(st)
	}
	if c.Pending("s1") != true {
		t.Fatal("должно быть что передать")
	}
	stop(t, c, "s1")
	cms, _, err := c.Deliver("s1")
	must(t, err)
	if len(cms) != 1 || status(t, c, r, a, id) != CommentWorking || !rv(t, c, r).AgentBusy {
		t.Fatal(cms)
	}
}

// 49, 51: свободный агент получает комментарии, отвечает «готово» — появляется ответ с правками с диска.
func TestDoneTakesEditsFromDisk(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	id := comment(t, c, r, a, "поправь")
	must(t, c.Send(r))
	if st := status(t, c, r, a, id); st != CommentWorking {
		t.Fatal(st)
	}
	cms, _, err := c.Deliver("s1")
	must(t, err)
	if len(cms) != 1 {
		t.Fatal(cms)
	}
	must(t, c.WriteCheck("s1", a, false))
	p.write("a.md", "3\n")
	res, err := c.Done("s1", []Answer{{CommentID: id, Text: "сделал"}})
	must(t, err)
	v := fv(t, c, r, a)
	if !res.Responded || v.Response == nil || *v.Response != "3\n" || v.Current != "2\n" {
		t.Fatalf("%+v %+v", res, v)
	}
	if status(t, c, r, a, id) != CommentAnswered {
		t.Fatal(status(t, c, r, a, id))
	}
	// Ответ лежит на диске, как и ожидается: пометки «изменён на диске» нет.
	if rv(t, c, r).DiskChanged != nil {
		t.Fatal(rv(t, c, r).DiskChanged)
	}
}

// 52, 113: «готово» без ответа на взятый комментарий не принимается.
func TestDoneNeedsEveryAnswer(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	id1 := comment(t, c, r, a, "раз")
	id2 := comment(t, c, r, a, "два")
	must(t, c.Send(r))
	c.Deliver("s1")
	_, err := c.Done("s1", []Answer{{CommentID: id1, Text: "ок"}})
	var ue *UnansweredError
	if !errors.As(err, &ue) || len(ue.Comments) != 1 || ue.Comments[0].ID != id2 {
		t.Fatal(err)
	}
	// Хук Stop тоже не даёт закончить.
	if res := stop(t, c, "s1"); len(res.Unanswered) != 2 {
		t.Fatal(res)
	}
	if _, err := c.Done("s1", []Answer{{CommentID: id1, Text: "ок"}, {CommentID: id2, Text: "ок"}}); err != nil {
		t.Fatal(err)
	}
	if !rv(t, c, r).HasResponse {
		t.Fatal("нет ответа")
	}
}

// 114: пока агент работал, пришли новые комментарии — «готово» отдаёт их, ответ появится потом.
func TestDoneHandsNewComments(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	id1 := comment(t, c, r, a, "раз")
	must(t, c.Send(r))
	c.Deliver("s1")
	id2 := comment(t, c, r, a, "два")
	must(t, c.Send(r))
	if status(t, c, r, a, id2) != CommentWaiting {
		t.Fatal(status(t, c, r, a, id2))
	}
	must(t, c.WriteCheck("s1", a, false))
	p.write("a.md", "3\n")
	res, err := c.Done("s1", []Answer{{CommentID: id1, Text: "ок"}})
	must(t, err)
	if res.Responded || len(res.New) != 1 || res.New[0].ID != id2 || rv(t, c, r).HasResponse {
		t.Fatalf("%+v", res)
	}
	p.write("a.md", "4\n")
	res, err = c.Done("s1", []Answer{{CommentID: id2, Text: "ок"}})
	must(t, err)
	v := fv(t, c, r, a)
	if !res.Responded || *v.Response != "4\n" || status(t, c, r, a, id1) != CommentAnswered || status(t, c, r, a, id2) != CommentAnswered {
		t.Fatalf("%+v %+v", res, v)
	}
}

// 109: правка из чата приходит ответом, когда агент закончил отвечать.
func TestChatEditBecomesResponse(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.SetBusy("s1", true))
	must(t, c.WriteCheck("s1", a, false))
	if !rv(t, c, r).Working {
		t.Fatal("агент должен работать")
	}
	p.write("a.md", "3\n")
	c.Poll()
	if rv(t, c, r).DiskChanged != nil {
		t.Fatal("правка агента — не «изменён на диске»")
	}
	res := stop(t, c, "s1")
	v := fv(t, c, r, a)
	if !res.Responded || v.Response == nil || *v.Response != "3\n" || rv(t, c, r).Working {
		t.Fatalf("%+v %+v", res, v)
	}
}

// 112: агент поменял файл ревью командой, пока отвечал в чате, — это тоже ответ, а не «изменён на диске».
func TestCommandEditBecomesResponse(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.Sync(r, nil))
	must(t, c.SetBusy("s1", true))
	p.write("a.md", "3\n")
	c.Poll()
	if rv(t, c, r).DiskChanged != nil {
		t.Fatal("пока агент занят, за диском не следим")
	}
	if res := stop(t, c, "s1"); !res.Responded {
		t.Fatal(res)
	}
	if v := fv(t, c, r, a); v.Response == nil || *v.Response != "3\n" {
		t.Fatalf("%+v", v)
	}
}

// 110, 111: непросмотренный ответ или правка владельца — менять файлы ревью нельзя.
func TestWriteRefused(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.SetEditing(r, true))
	if err := c.WriteCheck("s1", a, false); !errors.Is(err, ErrEditing) {
		t.Fatal(err)
	}
	must(t, c.SetEditing(r, false))
	must(t, c.WriteCheck("s1", a, false))
	p.write("a.md", "3\n")
	stop(t, c, "s1")
	if err := c.WriteCheck("s1", a, false); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err)
	}
	// Файлы вне ревью и вне папки агента duet не касаются.
	if err := c.WriteCheck("s1", "/elsewhere/x.md", false); err != nil {
		t.Fatal(err)
	}
}

// 53, 117: забытый файл в папке агента попадает в ответ; исходная версия даёт отличие от неё.
func TestForgottenFile(t *testing.T) {
	c, p := newProj(t)
	r, _ := agentReview(t, c, p, "a.md", "2\n", "1\n")
	b := p.write("b.md", "old\n")
	must(t, c.SetBusy("s1", true))
	must(t, c.WriteCheck("s1", b, true))
	if !c.AskOriginal("s1", b) || c.AskOriginal("s1", b) {
		t.Fatal("просить исходную версию один раз")
	}
	p.write("b.md", "new\n")
	must(t, c.SetOriginal(r, b, Original{Content: "old\n", Source: "git, HEAD"}))
	if res := stop(t, c, "s1"); !res.Responded {
		t.Fatal(res)
	}
	diff, err := c.ResponseDiff(r, b)
	must(t, err)
	if len(diff) == 0 || diff[0].Kind == ChunkAdded { // исходная версия видна, файл не целиком новый
		t.Fatalf("%+v", diff)
	}
	must(t, c.AcceptResponse(r))
	v := fv(t, c, r, b)
	if v.Base != "old\n" || v.Current != "new\n" || !v.BaseIsOriginal {
		t.Fatalf("%+v", v)
	}
}

// 106: свободный агент A — ревью переходит к B, A узнаёт об этом.
func TestTakeOverFromIdleAgent(t *testing.T) {
	c, p := newProj(t)
	r, _ := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.Attach(Session{ID: "s2", Agent: "codex"}, r))
	if rv(t, c, r).AgentKind != "codex" {
		t.Fatal(rv(t, c, r))
	}
	if k := c.TakeKicked("s1"); k != r {
		t.Fatal(k)
	}
	if _, ok := c.SessionInfo("s1"); ok {
		t.Fatal("сессия A забыта")
	}
}

// 107: агент A работает по комментариям — B не подключается.
func TestNoTakeOverFromWorkingAgent(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	comment(t, c, r, a, "x")
	must(t, c.Send(r))
	c.Deliver("s1")
	if err := c.Attach(Session{ID: "s2"}, r); !errors.Is(err, ErrOtherAgent) {
		t.Fatal(err)
	}
}

// 46: новое ревью в той же сессии отключает её от старого.
func TestNewReviewDetachesOld(t *testing.T) {
	c, p := newProj(t)
	r1, _ := agentReview(t, c, p, "a.md", "2\n", "1\n")
	r2, _ := newReview(t, c, p, "b.md", "2\n", "1\n")
	must(t, c.Attach(Session{ID: "s1"}, r2))
	if rv(t, c, r1).Agent || !rv(t, c, r2).Agent {
		t.Fatal("подключение")
	}
}

// 108: сессия закрыта — агент отключён, «ожидает» вернулись владельцу.
func TestDetach(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.SetBusy("s1", true))
	id := comment(t, c, r, a, "x")
	must(t, c.Send(r))
	must(t, c.Detach("s1"))
	if rv(t, c, r).Agent || status(t, c, r, a, id) != CommentUnsent {
		t.Fatal("отключение")
	}
}

// 115: «прервать», пока агент не взял комментарии, — диск возвращается сразу.
func TestInterruptBeforeTake(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	id := comment(t, c, r, a, "x")
	must(t, c.Send(r))
	must(t, c.Interrupt(r))
	if rv(t, c, r).Stopping || status(t, c, r, a, id) != CommentUnsent {
		t.Fatal(rv(t, c, r))
	}
}

// 56, 116: «прервать» посреди работы — запрет на запись, остановка, диск возвращён, агенту сообщение.
func TestInterruptWhileWorking(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	id := comment(t, c, r, a, "x")
	must(t, c.Send(r))
	c.Deliver("s1")
	must(t, c.WriteCheck("s1", a, false))
	p.write("a.md", "3\n")
	must(t, c.Interrupt(r))
	if !rv(t, c, r).Stopping {
		t.Fatal("должен останавливаться")
	}
	if err := c.WriteCheck("s1", a, false); !errors.Is(err, ErrStopping) {
		t.Fatal(err)
	}
	stop(t, c, "s1")
	if rv(t, c, r).Stopping || p.read("a.md") != "2\n" {
		t.Fatal(p.read("a.md"))
	}
	_, ns, err := c.Deliver("s1")
	must(t, err)
	if len(ns) != 1 || ns[0].Reason != NoticeInterrupted || len(ns[0].Comments) != 1 || ns[0].Comments[0] != id {
		t.Fatalf("%+v", ns)
	}
}

// 55: ревью принято целиком — агент получает список принятого.
func TestAcceptedNotice(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.AcceptFile(r, a))
	_, ns, err := c.Deliver("s1")
	must(t, err)
	if len(ns) != 1 || ns[0].Reason != NoticeAccepted || ns[0].Files[0] != a {
		t.Fatalf("%+v", ns)
	}
}

// 51: агент указал новое место комментария текстом.
func TestAnswerAnchorText(t *testing.T) {
	c, p := newProj(t)
	r, a := agentReview(t, c, p, "a.md", "one two three\n", "")
	id, err := c.AddComment(r, a, &Range{4, 7}, "x", nil, false)
	must(t, err)
	must(t, c.Send(r))
	c.Deliver("s1")
	must(t, c.WriteCheck("s1", a, false))
	p.write("a.md", "zero\nfour five\n")
	_, err = c.Done("s1", []Answer{{CommentID: id, Text: "ok", AnchorText: "five"}})
	must(t, err)
	must(t, c.AcceptResponse(r))
	for _, cm := range fv(t, c, r, a).Comments {
		if cm.ID == id && (cm.Range != Range{10, 14}) {
			t.Fatal(cm.Range)
		}
	}
}

// 119: записка и блоки хранятся и видны клиенту.
func TestNoteAndBlocks(t *testing.T) {
	c, p := newProj(t)
	path := p.write("a.md", "# A\ntext\n")
	r, _, err := c.CreateReview("x", []Submission{{Path: path, Content: "# A\ntext\n", Blocks: []Block{{Title: "A", Level: "top", At: "# A"}}}})
	must(t, err)
	must(t, c.SetNote(r, "начните с A"))
	if rv(t, c, r).Note != "начните с A" || len(fv(t, c, r, path).Blocks) != 1 {
		t.Fatal(rv(t, c, r))
	}
}
