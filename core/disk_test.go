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

// 205: файл поменяли на диске, пока открыт редактор, — правка не сохраняется без подтверждения;
// с подтверждением то, что было на диске, остаётся в истории.
func TestEditWhileDiskChanged(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.SetEditing(r, true))
	p.write("a.md", "мимо duet\n")
	c.Poll()
	if v := fv(t, c, r, a); v.Current != "2\n" || !v.DiskChanged {
		t.Fatalf("пока открыт редактор, изменение ждёт: %+v", v)
	}
	was := "2\n"
	if err := c.EditFrom(r, a, "моё\n", &was, false); !errors.Is(err, ErrChangedMeanwhile) {
		t.Fatal(err)
	}
	must(t, c.EditFrom(r, a, "моё\n", &was, true))
	must(t, c.SetEditing(r, false))
	if p.read("a.md") != "моё\n" || fv(t, c, r, a).Current != "моё\n" || fv(t, c, r, a).DiskChanged {
		t.Fatal(p.read("a.md"))
	}
	f := c.s.Files[a]
	if prev := f.Versions[len(f.Versions)-2]; prev.Author != "disk" || prev.Content != "мимо duet\n" {
		t.Fatalf("%+v", prev)
	}
}

// 205: изменение на диске взято, пока владелец правил в другой вкладке, — текущая уже не та.
func TestEditFromStaleText(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.Edit(r, a, "3\n"))
	was := "2\n"
	if err := c.EditFrom(r, a, "4\n", &was, false); !errors.Is(err, ErrChangedMeanwhile) {
		t.Fatal(err)
	}
	if p.read("a.md") != "3\n" {
		t.Fatal(p.read("a.md"))
	}
}

// 209: удалённый файл править нельзя.
func TestEditDeletedFile(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	p.remove("a.md")
	must(t, c.Sync(r, nil))
	if err := c.Edit(r, a, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// 7, 90, 41: отклонить ответ — диск к текущей версии: изменённый вернулся,
// созданный удалён, удалённый восстановлен вместе с папками.
func TestRejectRestoresDisk(t *testing.T) {
	c, p := newProj(t)
	a, d := p.write("a.md", "1\n"), p.write("sub/deep/d.md", "d\n")
	r := create(t, c, Submission{Path: a, Content: "1\n", Original: orig("before\n")}, Submission{Path: d, Content: "d\n", Original: orig("d\n")})
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

// pendingSetup — агент прислал ответ: a «1» → «2».
func pendingSetup(t *testing.T) (*Core, *proj, string, string) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "1\n", "до правок\n")
	must(t, c.AgentStart(r))
	p.write("a.md", "2\n")
	_, err := c.AgentFinish(r, []Submission{{Path: a, Content: "2\n"}}, nil)
	must(t, err)
	must(t, c.Sync(r, nil))
	return c, p, r, a
}

// 218: при отклонении файл, изменённый на диске, не возвращается, а становится правкой владельца.
func TestRejectKeepsDiskChange(t *testing.T) {
	c, p, r, a := pendingSetup(t)
	p.write("a.md", "владелец\n")
	notices(t, c, r)
	must(t, c.RejectResponse(r))
	v := fv(t, c, r, a)
	if p.read("a.md") != "владелец\n" || v.Current != "владелец\n" || v.DiskChanged || v.State == StateAccepted {
		t.Fatalf("%q %+v", p.read("a.md"), v)
	}
	if x := rv(t, c, r); x.HasResponse || x.FromDisk == nil || !reflect.DeepEqual(x.FromDisk.Files, []string{a}) {
		t.Fatalf("%+v", x)
	}
	wantNotices(t, c, r, Notice{Reason: NoticeRejected, Files: []string{a}}, Notice{Reason: NoticeOwnerEdit, Files: []string{a}})
}

// 213, 201: изменение на диске сразу становится правкой владельца; комментарий едет с текстом.
func TestDiskChangeIsOwnerEdit(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	cm := addC(t, c, r, a, sel("2\n", "2"), "к двойке")
	p.write("a.md", "0\n2\n")
	must(t, c.Sync(r, nil))
	v := fv(t, c, r, a)
	if v.DiskChanged || v.Current != "0\n2\n" || v.State == StateAccepted || v.Base != "1\n" {
		t.Fatalf("%+v", v)
	}
	if x := cv(t, c, r, a, cm); x.Range != *sel("0\n2\n", "2") {
		t.Fatalf("комментарий должен уехать вместе с текстом: %+v", x)
	}
	if fd := rv(t, c, r).FromDisk; fd == nil || fd.Seq != 1 || !reflect.DeepEqual(fd.Files, []string{a}) {
		t.Fatalf("%+v", fd)
	}
	if c.s.Files[a].Versions[c.s.Files[a].Current].Author != "owner" {
		t.Fatal("автор — владелец")
	}
	wantNotices(t, c, r, Notice{Reason: NoticeOwnerEdit, Files: []string{a}})
	// Состояние сохранено: после перезапуска правка на месте.
	c2, err := Open(p.state)
	must(t, err)
	if fv(t, c2, r, a).Current != "0\n2\n" {
		t.Fatal("правка с диска не сохранена")
	}
}

// 213: удаление на диске — файл удалён; появление — снова есть.
func TestDiskDeleteIsOwnerEdit(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	p.remove("a.md")
	must(t, c.Sync(r, nil))
	if v := fv(t, c, r, a); v.State != StateDeleted || v.DiskChanged {
		t.Fatalf("%+v", v)
	}
	p.write("a.md", "1\n")
	c.Poll()
	if v := fv(t, c, r, a); v.State != StateAccepted {
		t.Fatalf("%+v", v)
	}
}

// 215: вернули на диске как было (git checkout туда и обратно) — файл снова как база и принят.
func TestDiskBackToBase(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "1\n", "1\n")
	b := p.write("b.md", "b2\n")
	_, err := c.AddFiles(r, []Submission{{Path: b, Content: "b2\n", Original: orig("b1\n")}})
	must(t, err)
	must(t, c.Sync(r, nil))
	p.write("a.md", "ветка\n")
	c.Poll()
	if fv(t, c, r, a).State == StateAccepted {
		t.Fatal("не принят")
	}
	p.write("a.md", "1\n")
	c.Poll()
	if v := fv(t, c, r, a); v.State != StateAccepted || v.Current != "1\n" {
		t.Fatalf("%+v", v)
	}
	// Правки по нескольким файлам подряд — одно сообщение агенту.
	p.write("b.md", "b3\n")
	c.Poll()
	n := notices(t, c, r)
	if len(n) != 1 || n[0].Reason != NoticeOwnerEdit || !reflect.DeepEqual(n[0].Files, []string{a, b}) {
		t.Fatalf("%+v", n)
	}
}

// Правка с диска не будит свободного агента: он узнает о ней со следующими комментариями.
func TestDiskEditDoesNotWake(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	connectAgent(t, c, r, "s1")
	must(t, c.Sync(r, nil))
	p.write("a.md", "3\n")
	c.Poll()
	if c.Pending("s1") {
		t.Fatal("правка владельца не будит агента")
	}
	addC(t, c, r, a, nil, "А")
	must(t, c.Send(r, false))
	cms, ns, err := c.Deliver("s1")
	must(t, err)
	if len(cms) != 1 || len(ns) != 1 || ns[0].Reason != NoticeOwnerEdit {
		t.Fatalf("%+v %+v", cms, ns)
	}
}

// 216, 217: при непросмотренном ответе изменение на диске ждёт; файл нельзя принять;
// ответ приняли — то, что на диске, становится правкой поверх ответа.
func TestDiskChangeWaitsForResponse(t *testing.T) {
	c, p, r, a := pendingSetup(t)
	if fv(t, c, r, a).DiskChanged {
		t.Fatal("на диске ответ — так и должно быть")
	}
	p.write("a.md", "2\nмоё\n")
	c.Poll()
	if v := fv(t, c, r, a); !v.DiskChanged || v.Current != "1\n" {
		t.Fatalf("%+v", v)
	}
	if x := rv(t, c, r); x.Accepted || !reflect.DeepEqual(x.DiskChanged, []string{a}) {
		t.Fatalf("%+v", x)
	}
	must(t, c.AcceptResponse(r))
	if err := c.AcceptFile(r, a); err != nil {
		t.Fatal(err)
	}
	if v := fv(t, c, r, a); v.Current != "2\nмоё\n" || v.DiskChanged {
		t.Fatalf("%+v", v)
	}
	f := c.s.Files[a]
	if prev := f.Versions[len(f.Versions)-2]; prev.Content != "2\n" || prev.Author == "owner" {
		t.Fatalf("сначала версия из ответа, потом правка: %+v", prev)
	}
}

// 216: пока открыт редактор, изменение другого файла на диске ждёт, и этот файл нельзя принять;
// редактор закрыли — изменение стало правкой.
func TestHeldDiskChangeBlocksAccept(t *testing.T) {
	c, p := newProj(t)
	a, b := p.write("a.md", "2\n"), p.write("b.md", "b2\n")
	r := create(t, c, Submission{Path: a, Content: "2\n", Original: orig("1\n")}, Submission{Path: b, Content: "b2\n", Original: orig("b\n")})
	must(t, c.SetEditing(r, true))
	p.write("b.md", "мимо\n")
	must(t, c.Sync(r, nil))
	if v := fv(t, c, r, b); !v.DiskChanged || v.Current != "b2\n" {
		t.Fatalf("%+v", v)
	}
	if err := c.AcceptFile(r, b); !errors.Is(err, ErrDiskChanged) {
		t.Fatal(err)
	}
	must(t, c.SetEditing(r, false))
	if v := fv(t, c, r, b); v.DiskChanged || v.Current != "мимо\n" {
		t.Fatalf("%+v", v)
	}
	must(t, c.AcceptFile(r, b))
}

// 220, 87, 60: после запуска duet ничего не сверяет; ревью сверяется, когда его открыли
// или в нём что-то происходит, и изменения становятся правкой владельца.
func TestRestartSyncsOnDemand(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	r2, b := newReview(t, c, p, "b.md", "2\n", "1\n")
	p.write("a.md", "пока duet был выключен\n")
	p.write("b.md", "пока duet был выключен\n")
	c, err := Open(p.state)
	must(t, err)
	c.Poll()
	if x := rv(t, c, r); x.Synced || fv(t, c, r, a).Current != "2\n" {
		t.Fatalf("после запуска ничего не сверяется: %+v", x)
	}
	var steps [][2]int
	must(t, c.Sync(r, func(done, total int) { steps = append(steps, [2]int{done, total}) }))
	if !reflect.DeepEqual(steps, [][2]int{{1, 1}}) {
		t.Fatal(steps)
	}
	if x := rv(t, c, r); !x.Synced || fv(t, c, r, a).Current != "пока duet был выключен\n" {
		t.Fatalf("%+v", x)
	}
	// Во втором ревью что-то происходит — оно тоже сверяется.
	addC(t, c, r2, b, nil, "Б")
	if fv(t, c, r2, b).Current != "пока duet был выключен\n" {
		t.Fatal("ревью сверено по действию")
	}
}

// 220, 88: ревью, принятое целиком, за диском не следит; снова не принято — сверяется.
func TestAcceptedReviewNotWatched(t *testing.T) {
	c, p := newProj(t)
	r, a := newReview(t, c, p, "a.md", "2\n", "1\n")
	must(t, c.AcceptFile(r, a))
	if !rv(t, c, r).Accepted {
		t.Fatal("принято целиком")
	}
	p.write("a.md", "другая задача\n")
	must(t, c.Sync(r, nil))
	if x := rv(t, c, r); !x.Accepted || fv(t, c, r, a).Current != "2\n" {
		t.Fatalf("%+v", x)
	}
	addC(t, c, r, a, nil, "ещё одно")
	if x := rv(t, c, r); x.Accepted || fv(t, c, r, a).Current != "другая задача\n" {
		t.Fatalf("%+v", x)
	}
}

// 7p1 заменяет сценарий 89: явно принятая база сохраняется и в другом ревью.
func TestMovedAcceptedKeepsOwnerBase(t *testing.T) {
	c, p := newProj(t)
	r1, a := newReview(t, c, p, "a.md", "1\n", "0\n")
	must(t, c.AcceptFile(r1, a))
	p.write("a.md", "5\n") // коммиты мимо duet
	p.write("a.md", "6\n")
	r2 := create(t, c, Submission{Path: a, Content: "6\n", Original: orig("5\n")})
	v := fv(t, c, r2, a)
	if v.Base != "1\n" || v.BaseIsOriginal || v.BaseSource.Kind != "owner" || v.Current != "6\n" {
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
	must(t, c.Send(r, false)) // «ожидает»: заход продолжится
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
	if err := c.Send(r, false); !errors.Is(err, ErrStopping) {
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
	for i := 0; i < 200 && fv(t, c, r, a).Current != "мимо duet\n"; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if fv(t, c, r, a).Current != "мимо duet\n" {
		t.Fatal("правка с диска не появилась")
	}
}

// connectAgent — подключить сессию агента к ревью; агент свободен.
func connectAgent(t *testing.T, c *Core, r, id string) {
	t.Helper()
	must(t, c.Attach(Session{ID: id, Agent: "claude"}, r))
	must(t, c.SetBusy(id, false))
}
