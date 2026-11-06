package core

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// Тесты на настоящих файлах во временной папке проекта.

type proj struct {
	t     *testing.T
	dir   string // папка проекта
	state string // папка данных duet
}

func newProj(t *testing.T) (*Core, *proj) {
	t.Helper()
	p := &proj{t: t, dir: t.TempDir(), state: t.TempDir()}
	c, err := Open(p.state)
	must(t, err)
	return c, p
}

func (p *proj) path(name string) string { return filepath.Join(p.dir, name) }

// write — файл пишет агент или владелец мимо duet.
func (p *proj) write(name, content string) string {
	p.t.Helper()
	path := p.path(name)
	must(p.t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(p.t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func (p *proj) remove(name string) { must(p.t, os.Remove(p.path(name))) }

// read — что лежит на диске; "<нет>", если файла нет.
func (p *proj) read(name string) string {
	p.t.Helper()
	b, err := os.ReadFile(p.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return "<нет>"
	}
	must(p.t, err)
	return string(b)
}

func rv(t *testing.T, c *Core, r string) ReviewView {
	t.Helper()
	v, err := c.Review(r)
	must(t, err)
	return v
}

// newReview — агент написал файл на диск и отдал его на ревью вместе с исходной версией.
func newReview(t *testing.T, c *Core, p *proj, name, content, original string) (string, string) {
	t.Helper()
	path := p.write(name, content)
	return create(t, c, Submission{Path: path, Content: content, Original: orig(original)}), path
}

func notices(t *testing.T, c *Core, r string) []Notice {
	t.Helper()
	n, err := c.TakeNotices(r)
	must(t, err)
	for i := range n {
		n[i].At = time.Time{}
	}
	return n
}

func wantNotices(t *testing.T, c *Core, r string, want ...Notice) {
	t.Helper()
	got := notices(t, c, r)
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("сообщения агенту:\n got %+v\nwant %+v", got, want)
	}
}

// 18, 28: правка владельца сразу пишется на диск, агент получит «правка владельца».
func TestEditWritesDisk(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.Edit(r, a, "3\n"))
	if p.read("a.md") != "3\n" {
		t.Fatal(p.read("a.md"))
	}
	wantNotices(t, c, r, Notice{Reason: NoticeOwnerEdit, Files: []string{a}})
	wantNotices(t, c, r) // забранные сообщения больше не приходят
}

// 19: «вернуть к базе» пишет базу на диск.
func TestRevertWritesDisk(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.RevertToBase(r, a))
	if p.read("a.md") != "1\n" || fv(t, c, r, a).State != StateAccepted {
		t.Fatal(p.read("a.md"))
	}
	// Файл снова совпадает с базой — ревью принято целиком, агент узнаёт и об этом.
	wantNotices(t, c, r, Notice{Reason: NoticeRevert, Files: []string{a}}, Notice{Reason: NoticeAccepted, Files: []string{a}})
}

// 23: на диске не то, что ожидается, — duet не пишет и предлагает выбор.
func TestWriteRefusedWhenDiskChanged(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	p.write("a.md", "мимо duet\n")
	for _, err := range []error{c.Edit(r, a, "3\n"), c.RevertToBase(r, a)} {
		var de *DiskChangedError
		if !errors.As(err, &de) || !errors.Is(err, ErrDiskChanged) || de.Paths[0] != a {
			t.Fatal(err)
		}
	}
	if p.read("a.md") != "мимо duet\n" || fv(t, c, r, a).Current != "2\n" {
		t.Fatal("ничего не должно поменяться")
	}
	if v := fv(t, c, r, a); !v.DiskChanged || v.Disk != "мимо duet\n" {
		t.Fatalf("%+v", v)
	}
	wantNotices(t, c, r)
}

// 7, 90, 41: отклонить ответ — диск к текущей версии: изменённый вернулся,
// созданный удалён, удалённый восстановлен вместе с папками.
func TestRejectRestoresDisk(t *testing.T) {
	c, p := newProj(t)
	a, d := p.write("a.md", "1\n"), p.write("sub/deep/d.md", "d\n")
	r := create(t, c, Submission{Path: a, Content: "1\n", Original: orig("1\n")}, Submission{Path: d, Content: "d\n", Original: orig("d\n")})
	cm := addC(t, c, r, a, nil, "поправь")
	sendTake(t, c, r)
	// Агент пишет на диск: меняет a, создаёт b, удаляет папку с d.
	p.write("a.md", "2\n")
	b := p.write("b.md", "новый\n")
	must(t, os.RemoveAll(p.path("sub")))
	_, err := c.AgentFinish(r, []Submission{{Path: a, Content: "2\n"}, {Path: b, Content: "новый\n"}, {Path: d, Deleted: true}},
		[]Answer{{CommentID: cm, Text: "сделал"}})
	must(t, err)
	must(t, c.RejectResponse(r))
	if p.read("a.md") != "1\n" || p.read("b.md") != "<нет>" || p.read("sub/deep/d.md") != "d\n" {
		t.Fatal(p.read("a.md"), p.read("b.md"), p.read("sub/deep/d.md"))
	}
	wantNotices(t, c, r, Notice{Reason: NoticeRejected, Files: sorted(a, b, d), Comments: []string{cm}})
	if x := rv(t, c, r); len(x.DiskChanged) != 0 {
		t.Fatalf("%+v", x)
	}
}

func sorted(s ...string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// 23 при отклонении: на диске не версия из ответа — ничего не пишется, ответ остаётся.
func TestRejectRefusedWhenDiskChanged(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "1\n", "1\n")
	must(t, c.AgentStart(r))
	p.write("a.md", "2\n")
	_, err := c.AgentFinish(r, []Submission{{Path: a, Content: "2\n"}}, nil)
	must(t, err)
	p.write("a.md", "владелец\n")
	if err := c.RejectResponse(r); !errors.Is(err, ErrDiskChanged) {
		t.Fatal(err)
	}
	if !rv(t, c, r).HasResponse || p.read("a.md") != "владелец\n" {
		t.Fatal("ответ и диск не тронуты")
	}
}

// 80, 81: правка мимо duet помечается, «взять с диска» делает её правкой владельца.
func TestTakeFromDisk(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	cm := addC(t, c, r, a, sel("2\n", "2"), "к двойке")
	p.write("a.md", "0\n2\n")
	must(t, c.Sync(r, nil))
	v := fv(t, c, r, a)
	if !v.DiskChanged || v.Disk != "0\n2\n" || !reflect.DeepEqual(rv(t, c, r).DiskChanged, []string{a}) {
		t.Fatalf("%+v", v)
	}
	if ch, err := c.DiskDiff(r, a); err != nil || len(ch) == 0 {
		t.Fatal(ch, err)
	}
	must(t, c.TakeFromDisk(r, nil))
	v = fv(t, c, r, a)
	if v.DiskChanged || v.Current != "0\n2\n" || v.State == StateAccepted || len(rv(t, c, r).DiskChanged) != 0 {
		t.Fatalf("%+v", v)
	}
	if x := cv(t, c, r, a, cm); x.Range != *sel("0\n2\n", "2") {
		t.Fatalf("комментарий должен уехать вместе с текстом: %+v", x)
	}
	wantNotices(t, c, r, Notice{Reason: NoticeOwnerEdit, Files: []string{a}})
}

// 80, 81: удаление мимо duet тоже изменение; взятое с диска — файл удалён.
func TestTakeDeletedFromDisk(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	p.remove("a.md")
	must(t, c.Sync(r, nil))
	if v := fv(t, c, r, a); !v.DiskChanged || !v.DiskDeleted {
		t.Fatalf("%+v", v)
	}
	must(t, c.TakeFromDisk(r, []string{a}))
	if v := fv(t, c, r, a); v.State != StateDeleted {
		t.Fatalf("%+v", v)
	}
}

// 82: «перезаписать» — на диске ожидаемое, прежнее содержимое осталось в истории.
func TestOverwrite(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	b := p.write("b.md", "b\n")
	_, err := c.AddFiles(r, []Submission{{Path: b, Content: "b\n"}})
	must(t, err)
	p.write("a.md", "мимо\n")
	p.remove("b.md")
	must(t, c.Overwrite(r, nil)) // «перезаписать все»
	if p.read("a.md") != "2\n" || p.read("b.md") != "b\n" || len(rv(t, c, r).DiskChanged) != 0 {
		t.Fatal(p.read("a.md"), p.read("b.md"))
	}
	f := c.s.Files[a]
	if last := f.Versions[len(f.Versions)-1]; last.Author != "disk" || last.Content != "мимо\n" {
		t.Fatalf("%+v", last)
	}
	wantNotices(t, c, r, Notice{Reason: NoticeOverwritten, Files: []string{a, b}})
}

// 83: пока есть пометка, файл нельзя принять, комментарии нельзя отправить, ревью не принято целиком.
func TestDriftBlocks(t *testing.T) {
	c, p := newProj(t)
	a, b := p.write("a.md", "2\n"), p.write("b.md", "b\n")
	r := create(t, c, Submission{Path: a, Content: "2\n", Original: orig("1\n")}, Submission{Path: b, Content: "b\n", Original: orig("b\n")})
	addC(t, c, r, a, nil, "А")
	p.write("b.md", "мимо\n") // b принят, но на диске другое
	p.write("a.md", "мимо\n")
	if err := c.AcceptFile(r, a); !errors.Is(err, ErrDiskChanged) {
		t.Fatal(err)
	}
	if err := c.Send(r); !errors.Is(err, ErrDiskChanged) {
		t.Fatal(err)
	}
	// Владелец вернул a, но b по-прежнему расходится: ревью всё равно не принято целиком.
	must(t, c.Overwrite(r, []string{a}))
	must(t, c.AcceptFile(r, a))
	for _, cm := range fv(t, c, r, a).Comments {
		must(t, c.DeleteComment(r, cm.ID))
	}
	x := rv(t, c, r)
	if x.Accepted || !reflect.DeepEqual(x.DiskChanged, []string{b}) {
		t.Fatalf("%+v", x)
	}
	must(t, c.Overwrite(r, nil))
	if x := rv(t, c, r); !x.Accepted {
		t.Fatalf("%+v", x)
	}
}

// 84: на диске снова ожидаемое — пометка снимается сама.
func TestDriftClearsItself(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.Sync(r, nil))
	p.write("a.md", "мимо duet\n")
	c.Poll()
	if !fv(t, c, r, a).DiskChanged {
		t.Fatal("пометка")
	}
	p.write("a.md", "2\n")
	c.Poll()
	if fv(t, c, r, a).DiskChanged {
		t.Fatal("пометка должна сняться")
	}
}

// 85: при непросмотренном ответе ожидается версия из ответа; «взять с диска» — только после ответа.
func TestDriftWithPendingResponse(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "1\n", "1\n")
	must(t, c.AgentStart(r))
	p.write("a.md", "2\n")
	_, err := c.AgentFinish(r, []Submission{{Path: a, Content: "2\n"}}, nil)
	must(t, err)
	must(t, c.Sync(r, nil))
	if fv(t, c, r, a).DiskChanged {
		t.Fatal("на диске ответ — так и должно быть")
	}
	p.write("a.md", "мимо\n")
	c.Poll()
	if !fv(t, c, r, a).DiskChanged {
		t.Fatal("пометка")
	}
	if err := c.TakeFromDisk(r, nil); !errors.Is(err, ErrUnviewedResponse) {
		t.Fatal(err)
	}
	must(t, c.Overwrite(r, nil))
	if p.read("a.md") != "2\n" {
		t.Fatal(p.read("a.md"))
	}
}

// 86: пока агент работает, правки на диске не помечаются.
func TestNoDriftWhileWorking(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	addC(t, c, r, a, nil, "А")
	sendTake(t, c, r)
	p.write("a.md", "агент пишет\n")
	must(t, c.Sync(r, nil))
	c.Poll()
	if fv(t, c, r, a).DiskChanged {
		t.Fatal("агент работает")
	}
}

// 87, 60: после запуска duet ничего не сверяет; ревью сверяется, когда его открыли
// или в нём что-то происходит.
func TestRestartSyncsOnDemand(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	r2, b := newReview(t, c, p, "b.md", "2\n", "1\n")
	p.write("a.md", "пока duet был выключен\n")
	p.write("b.md", "пока duet был выключен\n")
	c, err := Open(p.state)
	must(t, err)
	c.Poll()
	if x := rv(t, c, r); x.Synced || len(x.DiskChanged) != 0 {
		t.Fatalf("после запуска ничего не сверяется: %+v", x)
	}
	var steps [][2]int
	must(t, c.Sync(r, func(done, total int) { steps = append(steps, [2]int{done, total}) }))
	if !reflect.DeepEqual(steps, [][2]int{{1, 1}}) {
		t.Fatal(steps)
	}
	if x := rv(t, c, r); !x.Synced || !reflect.DeepEqual(x.DiskChanged, []string{a}) {
		t.Fatalf("%+v", x)
	}
	// Во втором ревью что-то происходит — оно тоже сверяется.
	addC(t, c, r2, b, nil, "Б")
	if !fv(t, c, r2, b).DiskChanged {
		t.Fatal("ревью сверено по действию")
	}
}

// 88: ревью, принятое целиком, за диском не следит; снова не принято — сверяется.
func TestAcceptedReviewNotWatched(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.AcceptFile(r, a))
	if !rv(t, c, r).Accepted {
		t.Fatal("принято целиком")
	}
	p.write("a.md", "другая задача\n")
	must(t, c.Sync(r, nil))
	if x := rv(t, c, r); !x.Accepted || len(x.DiskChanged) != 0 {
		t.Fatalf("%+v", x)
	}
	addC(t, c, r, a, nil, "ещё одно")
	if x := rv(t, c, r); x.Accepted || !reflect.DeepEqual(x.DiskChanged, []string{a}) {
		t.Fatalf("%+v", x)
	}
}

// 89: принятый файл переходит в новое ревью — база из исходной версии агента.
func TestMovedAcceptedTakesNewOriginal(t *testing.T) {
	c, p := newProj(t)
	r1, a := newReview(t, c, p, "a.md", "1\n", "0\n")
	must(t, c.AcceptFile(r1, a))
	p.write("a.md", "5\n") // коммиты мимо duet
	p.write("a.md", "6\n")
	r2 := create(t, c, Submission{Path: a, Content: "6\n", Original: orig("5\n")})
	v := fv(t, c, r2, a)
	if v.Base != "5\n" || !v.BaseIsOriginal || v.OriginSource != "из git, HEAD" || v.Current != "6\n" {
		t.Fatalf("%+v", v)
	}
	// Без исходной версии база остаётся старой.
	must(t, c.AcceptFile(r2, a))
	p.write("a.md", "7\n")
	r3 := create(t, c, Submission{Path: a, Content: "7\n"})
	if v := fv(t, c, r3, a); v.Base != "6\n" || v.BaseIsOriginal {
		t.Fatalf("%+v", v)
	}
	// Непринятый файл переходит со всем состоянием, исходная версия не нужна.
	r4 := create(t, c, Submission{Path: a, Content: "8\n", Original: orig("7\n")})
	if v := fv(t, c, r4, a); v.Base != "6\n" {
		t.Fatalf("%+v", v)
	}
}

// interruptSetup — агент взял комментарий, поменял a, создал x и сообщил о них, работает дальше.
func interruptSetup(t *testing.T) (*Core, *proj, string, string, string, string) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	cm := addC(t, c, r, a, nil, "А")
	sendTake(t, c, r)
	addC(t, c, r, a, nil, "Б")
	must(t, c.Send(r)) // «ожидает»: заход продолжится
	p.write("a.md", "наполовину\n")
	x := p.write("x.md", "черновик\n")
	_, err := c.AgentFinish(r, []Submission{{Path: a, Content: "наполовину\n"}, {Path: x, Content: "черновик\n"}}, []Answer{{CommentID: cm, Text: "сделал"}})
	must(t, err)
	return c, p, r, a, x, cm
}

// 21, 91: «прервать» — «останавливается…», диск не тронут; агент остановился — диск возвращён.
func TestInterruptWaitsForStop(t *testing.T) {
	c, p, r, a, x, _ := interruptSetup(t)
	notices(t, c, r)
	must(t, c.Interrupt(r))
	if !rv(t, c, r).Stopping || p.read("a.md") != "наполовину\n" {
		t.Fatal("пока агент не остановился, диск не трогаем")
	}
	if err := c.Send(r); !errors.Is(err, ErrStopping) {
		t.Fatal(err)
	}
	if err := c.Edit(r, a, "z"); !errors.Is(err, ErrStopping) {
		t.Fatal(err)
	}
	p.write("a.md", "ещё дописал\n")
	must(t, c.AgentStopped(r))
	if p.read("a.md") != "2\n" || p.read("x.md") != "<нет>" {
		t.Fatal(p.read("a.md"), p.read("x.md"))
	}
	v := rv(t, c, r)
	if v.Stopping || !reflect.DeepEqual(v.Restored, sorted(a, x)) || len(v.DiskChanged) != 0 {
		t.Fatalf("%+v", v)
	}
	n := notices(t, c, r)
	if len(n) != 1 || n[0].Reason != NoticeInterrupted || !reflect.DeepEqual(n[0].Files, sorted(a, x)) || len(n[0].Comments) != 2 {
		t.Fatalf("%+v", n)
	}
	must(t, c.Edit(r, a, "z\n"))
}

// 91: агент отключился или владелец нажал «вернуть файлы всё равно» — диск возвращён.
func TestInterruptRestoreOnDisconnectOrAnyway(t *testing.T) {
	c, p, r, _, _, _ := interruptSetup(t)
	must(t, c.Interrupt(r))
	must(t, c.Disconnect(r))
	if p.read("a.md") != "2\n" || p.read("x.md") != "<нет>" || rv(t, c, r).Stopping {
		t.Fatal(p.read("a.md"))
	}
	c, p, r, _, _, _ = interruptSetup(t)
	must(t, c.Interrupt(r))
	must(t, c.RestoreAnyway(r))
	if p.read("a.md") != "2\n" || p.read("x.md") != "<нет>" {
		t.Fatal(p.read("a.md"))
	}
}

// 22: агент пропал посреди работы — «прервать» возвращает диск сразу, ждать некого.
func TestInterruptWithoutAgentRestoresAtOnce(t *testing.T) {
	c, p, r, _, _, _ := interruptSetup(t)
	must(t, c.Disconnect(r))
	must(t, c.Interrupt(r))
	if p.read("a.md") != "2\n" || rv(t, c, r).Stopping {
		t.Fatal(p.read("a.md"))
	}
}

// Слежка в фоне: правка мимо duet появляется сама, без действий владельца.
func TestWatch(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	stop := c.Watch(5 * time.Millisecond)
	defer stop()
	p.write("a.md", "мимо duet\n")
	for i := 0; i < 200 && !fv(t, c, r, a).DiskChanged; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !fv(t, c, r, a).DiskChanged {
		t.Fatal("пометка не появилась")
	}
}
