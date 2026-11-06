package core

import (
	"errors"
	"strings"
	"testing"
)

func sel(text, sub string) *Range {
	i := strings.Index(text, sub)
	if i < 0 {
		panic(sub)
	}
	return &Range{i, i + len(sub)}
}

func addC(t *testing.T, c *Core, r, path string, s *Range, text string) string {
	t.Helper()
	id, err := c.AddComment(r, path, s, text, nil, false)
	must(t, err)
	return id
}

func cv(t *testing.T, c *Core, r, path, id string) CommentView {
	t.Helper()
	for _, x := range fv(t, c, r, path).Comments {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("комментарий %s не виден", id)
	return CommentView{}
}

func status(t *testing.T, c *Core, r, path, id string) CommentStatus {
	t.Helper()
	return cv(t, c, r, path, id).Status
}

// Отправить, агент забирает и отвечает «сделал» по каждому.
func sendTake(t *testing.T, c *Core, r string) []AgentComment {
	t.Helper()
	must(t, c.Send(r))
	got, err := c.Take(r)
	must(t, err)
	return got
}

const doc = "один\nдва\nтри\nчетыре\n"

func setup(t *testing.T) (*Core, string) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/a", Content: doc, Original: orig(doc)})
	return c, r
}

// 12, 13: свободный агент сразу берёт, занятый — «ожидает» и получает посреди работы.
func TestSendFreeAndBusy(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	must(t, c.Send(r))
	if s := status(t, c, r, "/a", a); s != CommentWorking {
		t.Fatal(s)
	}
	got, _ := c.Take(r)
	if len(got) != 1 || got[0].Quote != "два" || got[0].Thread[0].Text != "А" {
		t.Fatalf("%+v", got)
	}
	b := addC(t, c, r, "/a", nil, "Б")
	must(t, c.Send(r))
	if s := status(t, c, r, "/a", b); s != CommentWaiting {
		t.Fatal(s)
	}
	if !fv(t, c, r, "/a").AgentWorking {
		t.Fatal("точки в дереве") // 33
	}
	got, _ = c.Take(r)
	if len(got) != 1 || got[0].ID != b || !got[0].Whole {
		t.Fatalf("%+v", got)
	}
	if s := status(t, c, r, "/a", b); s != CommentWorking {
		t.Fatal(s)
	}
	ok, err := c.AgentFinish(r, nil, []Answer{{CommentID: a, Text: "сделал"}, {CommentID: b, Text: "сделал"}})
	must(t, err)
	if !ok {
		t.Fatal("должен быть ответ") // 66: ответ без правок
	}
}

// 14, 65, 3: «отвечен» → `␣a` → «решён»; файл не принят, пока не нажали `␣a`.
func TestAnsweredResolvedByAccept(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	sendTake(t, c, r)
	if _, err := c.AgentFinish(r, nil, []Answer{{CommentID: a, Text: "не буду"}}); err != nil {
		t.Fatal(err)
	}
	if !fv(t, c, r, "/a").Ripple {
		t.Fatal("рябь: ответ касается файла")
	}
	if err := c.AcceptFile(r, "/a"); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err)
	}
	must(t, c.AcceptResponse(r))
	v := fv(t, c, r, "/a")
	if v.State == StateAccepted || status(t, c, r, "/a", a) != CommentAnswered {
		t.Fatalf("%+v", v)
	}
	must(t, c.AcceptFile(r, "/a"))
	v = fv(t, c, r, "/a")
	if v.State != StateAccepted || len(v.Comments) != 0 {
		t.Fatalf("%+v", v)
	}
	rv, _ := c.Review(r)
	if !rv.Accepted {
		t.Fatal("ревью принято целиком")
	}
}

// 14, 63: ответ в ветке на «отвечен» ждёт `␣s`.
func TestReplyWaitsForSend(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	sendTake(t, c, r)
	c.AgentFinish(r, nil, []Answer{{CommentID: a, Text: "сделал"}})
	if err := c.Reply(r, a, "нет", nil); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err) // 10
	}
	must(t, c.AcceptResponse(r))
	must(t, c.Reply(r, a, "не согласен", nil))
	if s := status(t, c, r, "/a", a); s != CommentUnsent {
		t.Fatal(s)
	}
	must(t, c.EditComment(r, a, "не согласен!", nil))
	got := sendTake(t, c, r)
	if len(got) != 1 || len(got[0].Thread) != 3 || got[0].Thread[2].Text != "не согласен!" {
		t.Fatalf("%+v", got)
	}
}

// Удаление неотправленного ответа в ветке возвращает прежний статус.
func TestDeleteReply(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	sendTake(t, c, r)
	c.AgentFinish(r, nil, []Answer{{CommentID: a, Text: "А или Б?", Question: true, Options: []string{"А", "Б"}}})
	must(t, c.AcceptResponse(r))
	must(t, c.Reply(r, a, "А", nil))
	must(t, c.DeleteComment(r, a))
	if s := status(t, c, r, "/a", a); s != CommentQuestion {
		t.Fatal(s)
	}
}

// 15, 64, 24: вопрос держит ревью непринятым; убрать — «решён».
func TestQuestion(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	sendTake(t, c, r)
	c.AgentFinish(r, nil, []Answer{{CommentID: a, Text: "А или Б?", Question: true, Options: []string{"А", "Б"}}})
	must(t, c.AcceptResponse(r))
	x := cv(t, c, r, "/a", a)
	if x.Status != CommentQuestion || len(x.Thread[1].Options) != 2 {
		t.Fatalf("%+v", x)
	}
	must(t, c.AcceptFile(r, "/a"))
	rv, _ := c.Review(r)
	if rv.Accepted || rv.Questions != 1 {
		t.Fatalf("%+v", rv)
	}
	must(t, c.DismissQuestion(r, a))
	rv, _ = c.Review(r)
	if !rv.Accepted {
		t.Fatalf("%+v", rv)
	}
}

// 24: неотправленный комментарий держит ревью непринятым.
func TestUnsentBlocksReviewAccepted(t *testing.T) {
	c, r := setup(t)
	addC(t, c, r, "/a", nil, "А")
	rv, _ := c.Review(r)
	if rv.Accepted || rv.Unsent != 1 || fv(t, c, r, "/a").State != StateAccepted {
		t.Fatalf("%+v", rv)
	}
}

// 16, 11: не отвеченный комментарий снова «ожидает», агент продолжает, ответа пока нет.
func TestUnansweredGoesBack(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	b := addC(t, c, r, "/a", nil, "Б")
	sendTake(t, c, r)
	ok, err := c.AgentFinish(r, []Submission{{Path: "/a", Content: "x\n"}}, []Answer{{CommentID: a, Text: "сделал"}})
	must(t, err)
	if ok || status(t, c, r, "/a", b) != CommentWaiting {
		t.Fatal("ответа быть не должно, Б ожидает")
	}
	rv, _ := c.Review(r)
	if rv.HasResponse || !rv.Working {
		t.Fatalf("%+v", rv)
	}
	got, _ := c.Take(r)
	if len(got) != 1 || got[0].ID != b {
		t.Fatalf("%+v", got)
	}
	ok, err = c.AgentFinish(r, nil, []Answer{{CommentID: b, Text: "сделал"}})
	must(t, err)
	v := fv(t, c, r, "/a")
	if !ok || v.Response == nil || *v.Response != "x\n" {
		t.Fatalf("ответ копится целиком: %+v", v)
	}
	must(t, c.AcceptResponse(r))
	if status(t, c, r, "/a", a) != CommentAnswered || status(t, c, r, "/a", b) != CommentAnswered {
		t.Fatal("оба отвечены")
	}
}

// 7: отклонить ответ — комментарии «не отправлен», написанные в окне — удаляются.
func TestRejectReturnsComments(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	sendTake(t, c, r)
	c.AgentFinish(r, []Submission{{Path: "/a", Content: "один\n2\nтри\nчетыре\n"}}, []Answer{{CommentID: a, Text: "сделал"}})
	w, err := c.AddComment(r, "/a", sel("один\n2\n", "2"), "в окне", nil, true)
	must(t, err)
	must(t, c.RejectResponse(r))
	x := cv(t, c, r, "/a", a)
	if x.Status != CommentUnsent || len(x.Thread) != 1 || x.Changed {
		t.Fatalf("%+v", x)
	}
	for _, y := range fv(t, c, r, "/a").Comments {
		if y.ID == w {
			t.Fatal("комментарий из окна должен уйти вместе с ответом")
		}
	}
	got := sendTake(t, c, r)
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

// 8, 9: комментарии в окне сохраняются при закрытии и становятся обычными при принятии.
func TestCommentsInResponseWindow(t *testing.T) {
	c, r := setup(t)
	respond(t, c, r, Submission{Path: "/a", Content: "один\n2\nтри\nчетыре\n"})
	if _, err := c.AddComment(r, "/a", nil, "x", nil, false); err != nil {
		t.Fatal(err) // писать можно, не отправляя
	}
	w, err := c.AddComment(r, "/a", sel("один\n2\n", "2"), "в окне", nil, true)
	must(t, err)
	if !cv(t, c, r, "/a", w).InResponse {
		t.Fatal("в окне")
	}
	if err := c.Send(r); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err)
	}
	must(t, c.AcceptResponse(r))
	x := cv(t, c, r, "/a", w)
	if x.InResponse || x.Status != CommentUnsent || x.Range != *sel("один\n2\n", "2") {
		t.Fatalf("%+v", x)
	}
	rv, _ := c.Review(r)
	if rv.Unsent != 2 {
		t.Fatalf("%+v", rv)
	}
}

// 26: нет агента — ничего не отправляется.
func TestSendWithoutAgent(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	must(t, c.Disconnect(r))
	if err := c.Send(r); !errors.Is(err, ErrNoAgent) {
		t.Fatal(err)
	}
	if status(t, c, r, "/a", a) != CommentUnsent {
		t.Fatal()
	}
	must(t, c.Connect(r))
	must(t, c.Send(r))
}

// 35, 22: агент отключился — «ожидает» владельцу, «в работе» остаются; прервать — всё владельцу.
func TestDisconnectAndInterrupt(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	sendTake(t, c, r)
	b := addC(t, c, r, "/a", nil, "Б")
	must(t, c.Send(r))
	must(t, c.Disconnect(r))
	if status(t, c, r, "/a", a) != CommentWorking || status(t, c, r, "/a", b) != CommentUnsent {
		t.Fatal()
	}
	rv, _ := c.Review(r)
	if !rv.AgentLost {
		t.Fatal("агент пропал посреди работы")
	}
	must(t, c.Interrupt(r))
	if status(t, c, r, "/a", a) != CommentUnsent {
		t.Fatal()
	}
	if err := c.Edit(r, "/a", "x"); err != nil {
		t.Fatal(err)
	}
}

// 20, 36: правка запрещена при «в работе»; при открытом редакторе нельзя отправить.
func TestEditBlocks(t *testing.T) {
	c, r := setup(t)
	addC(t, c, r, "/a", nil, "А")
	sendTake(t, c, r)
	if err := c.Edit(r, "/a", "x"); !errors.Is(err, ErrAgentWorking) {
		t.Fatal(err)
	}
	c2, r2 := setup(t)
	addC(t, c2, r2, "/a", nil, "А")
	must(t, c2.SetEditing(r2, true))
	if err := c2.Send(r2); !errors.Is(err, ErrEditing) {
		t.Fatal(err)
	}
	must(t, c2.SetEditing(r2, false))
	must(t, c2.Send(r2))
}

// Неотправленный можно править и удалять, отправленный — нет.
func TestEditDeleteUnsent(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", nil, "А")
	must(t, c.EditComment(r, a, "А2", nil))
	b := addC(t, c, r, "/a", nil, "Б")
	must(t, c.DeleteComment(r, b))
	sendTake(t, c, r)
	if err := c.EditComment(r, a, "x", nil); !errors.Is(err, ErrCommentStatus) {
		t.Fatal(err)
	}
	if n := len(fv(t, c, r, "/a").Comments); n != 1 {
		t.Fatal(n)
	}
}

// 37: текст сдвинулся — комментарий едет вместе с ним.
func TestAnchorShift(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "три"), "А")
	nw := "ноль\nодин\nдва\nтри\nчетыре\n"
	must(t, c.Edit(r, "/a", nw))
	x := cv(t, c, r, "/a", a)
	if x.Range != *sel(nw, "три") || x.Changed || x.Was != "" {
		t.Fatalf("%+v", x)
	}
}

// 38: текст переписали — висит на новом тексте, «было: …», «текст изменён».
func TestAnchorRewritten(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	nw := "один\nДВА с половиной\nтри\nчетыре\n"
	must(t, c.Edit(r, "/a", nw))
	x := cv(t, c, r, "/a", a)
	if x.Range != *sel(nw, "ДВА с половиной") || !x.Changed || x.Was != "два" {
		t.Fatalf("%+v", x)
	}
}

// 39: место удалили целиком — висит на строке, где оно было.
func TestAnchorRemoved(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	nw := "один\nтри\nчетыре\n"
	must(t, c.Edit(r, "/a", nw))
	x := cv(t, c, r, "/a", a)
	if x.Range != *sel(nw, "три") || !x.Removed || x.Was != "два" {
		t.Fatalf("%+v", x)
	}
}

// 40: агент указал новое место.
func TestAnchorByAgent(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	sendTake(t, c, r)
	nw := "два-с-половиной\nодин\nтри\nчетыре\n"
	c.AgentFinish(r, []Submission{{Path: "/a", Content: nw}}, []Answer{{CommentID: a, Text: "перенёс", Anchor: sel(nw, "два-с-половиной")}})
	must(t, c.AcceptResponse(r))
	x := cv(t, c, r, "/a", a)
	if x.Range != *sel(nw, "два-с-половиной") || x.Was != "два" {
		t.Fatalf("%+v", x)
	}
}

// Ответ агента сдвигает комментарии так же, как правка владельца.
func TestAnchorAfterResponse(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "четыре"), "А")
	nw := "один\nдва\nвставка\nтри\nчетыре\n"
	respond(t, c, r, Submission{Path: "/a", Content: nw})
	must(t, c.AcceptResponse(r))
	if x := cv(t, c, r, "/a", a); x.Range != *sel(nw, "четыре") || x.Changed {
		t.Fatalf("%+v", x)
	}
}

// 67 и комментарий ко всему файлу: файл удалён — комментарии ко всему файлу с «было: …».
func TestFileDeletedComments(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	w := addC(t, c, r, "/a", nil, "весь")
	respond(t, c, r, Submission{Path: "/a", Deleted: true})
	must(t, c.AcceptResponse(r))
	x := cv(t, c, r, "/a", a)
	if !x.Whole || x.Was != "два" {
		t.Fatalf("%+v", x)
	}
	if y := cv(t, c, r, "/a", w); !y.Whole || y.Was != "" {
		t.Fatalf("%+v", y)
	}
}

// 27: комментарии переходят вместе с файлом в новое ревью.
func TestCommentsMoveWithFile(t *testing.T) {
	c, r := setup(t)
	a := addC(t, c, r, "/a", sel(doc, "три"), "А")
	nw := "ноль\n" + doc
	r2, _, err := c.CreateReview("новое", []Submission{{Path: "/a", Content: nw}})
	must(t, err)
	x := cv(t, c, r2, "/a", a)
	if x.Range != *sel(nw, "три") {
		t.Fatalf("%+v", x)
	}
	if _, err := c.AddComment(r, "/a", nil, "x", nil, false); !errors.Is(err, ErrMoved) {
		t.Fatal(err)
	}
}

// 60: комментарии переживают перезапуск.
func TestCommentsPersist(t *testing.T) {
	c, dir := open(t)
	r := create(t, c, Submission{Path: "/a", Content: doc})
	a := addC(t, c, r, "/a", sel(doc, "два"), "А")
	c2 := reopen(t, dir)
	if x := cv(t, c2, r, "/a", a); x.Thread[0].Text != "А" || x.Range != *sel(doc, "два") {
		t.Fatalf("%+v", x)
	}
}

func TestDiffLines(t *testing.T) {
	a, _ := splitLines("a\nb\nc\nd\n")
	b, _ := splitLines("a\nx\nc\nd\ne\n")
	hs := diffLines(a, b)
	want := []hunk{{1, 2, 1, 2}, {4, 4, 4, 5}}
	if len(hs) != 2 || hs[0] != want[0] || hs[1] != want[1] {
		t.Fatalf("%+v", hs)
	}
}

func TestDiffRandom(t *testing.T) {
	seed := uint32(1)
	rnd := func(n int) int { seed = seed*1664525 + 1013904223; return int(seed>>16) % n }
	gen := func() []string {
		var s []string
		for i := rnd(12); i > 0; i-- {
			s = append(s, string(rune('a'+rnd(4)))+"\n")
		}
		return s
	}
	for it := 0; it < 3000; it++ {
		a, b := gen(), gen()
		hs := diffLines(a, b)
		var out []string
		ai, kept := 0, 0
		for _, h := range hs {
			out = append(out, a[ai:h.A0]...)
			kept += h.A0 - ai
			out = append(out, b[h.B0:h.B1]...)
			ai = h.A1
		}
		out = append(out, a[ai:]...)
		kept += len(a) - ai
		if strings.Join(out, "") != strings.Join(b, "") {
			t.Fatalf("%q %q %+v", a, b, hs)
		}
		// Совпавших строк столько же, сколько в наибольшей общей подпоследовательности.
		dp := make([][]int, len(a)+1)
		for i := range dp {
			dp[i] = make([]int, len(b)+1)
		}
		for i := len(a) - 1; i >= 0; i-- {
			for j := len(b) - 1; j >= 0; j-- {
				if a[i] == b[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else {
					dp[i][j] = max(dp[i+1][j], dp[i][j+1])
				}
			}
		}
		if kept != dp[0][0] {
			t.Fatalf("не минимально: %q %q %+v", a, b, hs)
		}
	}
}

// 151 (шаг 6b): комментарий к удалённому тексту висит точкой там, где был текст, с цитатой «было»;
// агент знает, что цитаты в файле больше нет. Точка едет вместе с текстом.
func TestRemovedTextComment(t *testing.T) {
	c, r := setup(t)
	at := strings.Index(doc, "три")
	a, err := c.AddRemovedComment(r, "/a", at, "старая строка", "Зачем удалил?", nil)
	must(t, err)
	x := cv(t, c, r, "/a", a)
	if x.Range != (Range{at, at}) || !x.Removed || x.Was != "старая строка" {
		t.Fatalf("%+v", x)
	}
	if _, err := c.AddRemovedComment(r, "/a", at, "", "А", nil); !errors.Is(err, ErrBadRange) {
		t.Fatal(err)
	}
	nw := "ноль\n" + doc
	must(t, c.Edit(r, "/a", nw))
	if x := cv(t, c, r, "/a", a); x.Range.Start != strings.Index(nw, "три") || x.Was != "старая строка" {
		t.Fatalf("%+v", x)
	}
	got := sendTake(t, c, r)
	if len(got) != 1 || !got[0].Removed || got[0].Quote != "старая строка" {
		t.Fatalf("%+v", got)
	}
}
