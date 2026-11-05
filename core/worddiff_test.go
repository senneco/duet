package core

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// render — отличия текстом: «~-»/«~+» — пара «было → стало», «- »/«+ » — строка удалена/добавлена,
// [..] — подсветка, ↵ — изменён перевод строки.
func render(chs []Chunk) string {
	var b strings.Builder
	ln := func(p string, l DiffLine) {
		t := l.Text
		for k := len(l.Marks) - 1; k >= 0; k-- {
			m := l.Marks[k]
			t = t[:m.From] + "[" + t[m.From:m.To] + "]" + t[m.To:]
		}
		if l.Break {
			t += "↵"
		}
		fmt.Fprintf(&b, "%s%s\n", p, t)
	}
	for _, c := range chs {
		switch c.Kind {
		case ChunkSame:
			for _, l := range c.New {
				ln("  ", l)
			}
		case ChunkChanged:
			for _, l := range c.Old {
				ln("~-", l)
			}
			for _, l := range c.New {
				ln("~+", l)
			}
		case ChunkDeleted:
			for _, l := range c.Old {
				ln("- ", l)
			}
		case ChunkAdded:
			for _, l := range c.New {
				ln("+ ", l)
			}
		}
	}
	return b.String()
}

func checkDiff(t *testing.T, old, nw string, prose bool, want string) {
	t.Helper()
	got := render(DiffText(old, nw, prose))
	if got != want {
		t.Fatalf("\nхотели:\n%s\nполучили:\n%s", want, got)
	}
}

// 68
func TestDiffOneWord(t *testing.T) {
	checkDiff(t, "The cat sat on the mat.\n", "The dog sat on the mat.\n", true,
		"~-The [cat] sat on the mat.\n~+The [dog] sat on the mat.\n")
	checkDiff(t, "func f(kind GlassKind) {}\n", "func f(kind Character) {}\n", false,
		"~-func f(kind [GlassKind]) {}\n~+func f(kind [Character]) {}\n")
}

// 69
func TestDiffPhraseWithoutGaps(t *testing.T) {
	checkDiff(t, "I like red apples and green pears. The sky is blue.\n",
		"I like blue apples and yellow pears. The sky is blue.\n", true,
		"~-I like [red apples and green] pears. The sky is blue.\n~+I like [blue apples and yellow] pears. The sky is blue.\n")
	// Склейка не идёт через конец предложения.
	checkDiff(t, "Палитра от бледного, как GitHub, до сочного. Поверхности остаются ровными, тихими и спокойными всегда.\n",
		"Палитра от бледного до сочного. Поверхности остаются ровными, яркими и спокойными всегда.\n", true,
		"~-Палитра от бледного[, как GitHub,] до сочного. Поверхности остаются ровными, [тихими] и спокойными всегда.\n"+
			"~+Палитра от бледного до сочного. Поверхности остаются ровными, [яркими] и спокойными всегда.\n")
}

// 68, 69: вставка подсвечена только в новой строке, даже если у соседних строк одинаковый конец.
func TestDiffInsertionOnly(t *testing.T) {
	checkDiff(t, "  - ответ агента, после которого файл не изменился, принятие не снимает;\n  - правка владельца снимает принятие;\n",
		"  - ответ агента, после которого файл не изменился и на комментарии к нему агент не отвечал, принятие не снимает;\n"+
			"  - ответ агента с ответом на комментарий к файлу снимает принятие;\n  - правка владельца снимает принятие;\n", true,
		"~-  - ответ агента, после которого файл не изменился, принятие не снимает;\n"+
			"~+  - ответ агента, после которого файл не изменился[ и на комментарии к нему агент не отвечал], принятие не снимает;\n"+
			"+   - ответ агента с ответом на комментарий к файлу снимает принятие;\n    - правка владельца снимает принятие;\n")
}

// 70
func TestDiffWholeSentence(t *testing.T) {
	checkDiff(t, "Первое предложение не меняется вообще никак. Кот сидит на окне и смотрит. Третье тоже остаётся на месте.\n",
		"Первое предложение не меняется вообще никак. Пёс лежит у двери и спит. Третье тоже остаётся на месте.\n", true,
		"~-Первое предложение не меняется вообще никак. [Кот сидит на окне и смотрит.] Третье тоже остаётся на месте.\n"+
			"~+Первое предложение не меняется вообще никак. [Пёс лежит у двери и спит.] Третье тоже остаётся на месте.\n")
}

// 71
func TestDiffWholeParagraph(t *testing.T) {
	checkDiff(t, "# Заголовок\n\nИсполнять каждую задачу один раз. Попробовать запускать задачи за один вызов.\n\nКонец.\n",
		"# Заголовок\n\nИсполнять каждую выбранную проверку в своём режиме. Выполнять проверки двумя последовательными вызовами.\n\nКонец.\n", true,
		"  # Заголовок\n  \n- Исполнять каждую задачу один раз. Попробовать запускать задачи за один вызов.\n"+
			"+ Исполнять каждую выбранную проверку в своём режиме. Выполнять проверки двумя последовательными вызовами.\n  \n  Конец.\n")
}

// 72
func TestDiffRewrap(t *testing.T) {
	checkDiff(t, "This is a long paragraph that is wrapped\nat a fixed width so that every line is\nshort enough to read in an editor.\n",
		"This is a long paragraph that is now wrapped\nat a fixed width so that every line\nis short enough to read in an editor.\n", true,
		"~-This is a long paragraph that is wrapped\n~+This is a long paragraph that is [now ]wrapped\n"+
			"~-at a fixed width so that every line is\n~-short enough to read in an editor.\n"+
			"~+at a fixed width so that every line\n~+is short enough to read in an editor.\n")
	// В коде перенос строки — настоящее изменение.
	checkDiff(t, "a(b,\n  c)\n", "a(b, c)\n", false, "~-a(b,↵\n~-[  ]c)\n~+a(b,[ ]c)\n")
}

// 73
func TestDiffCodeLines(t *testing.T) {
	checkDiff(t, "x := foo(a, b)\n", "x := bar(a, c)\n", false,
		"~-x := [foo](a, [b])\n~+x := [bar](a, [c])\n")
	checkDiff(t, "\treturn alpha(beta, gamma)\n", "\treturn delta(epsilon, zeta)\n", false,
		"~-\t[return alpha(beta, gamma)]\n~+\t[return delta(epsilon, zeta)]\n")
	// Блок кода в markdown сравнивается как код: без предложений и абзацев.
	checkDiff(t, "Текст.\n\n```\nx := foo(a, b)\n```\n", "Текст.\n\n```\nx := bar(a, c)\n```\n", true,
		"  Текст.\n  \n  ```\n~-x := [foo](a, [b])\n~+x := [bar](a, [c])\n  ```\n")
}

// 74
func TestDiffUnrelatedLines(t *testing.T) {
	checkDiff(t, "a := 1\nb := compute(x)\nc := sum(a, b)\n", "a := 1\nfmt.Println(y)\nc := sum(a, d)\n", false,
		"  a := 1\n- b := compute(x)\n+ fmt.Println(y)\n~-c := sum(a, [b])\n~+c := sum(a, [d])\n")
	// Строки, похожие на другие только частыми словами, тоже не сводятся в пару.
	checkDiff(t, "// FAR_HAZE is how strong the far haze is\nconst FAR_HAZE = 0.22\n// under is what a surface stands on in a kind of glass\n",
		"// under is what a surface stands on at a step\n", false,
		"- // FAR_HAZE is how strong the far haze is\n- const FAR_HAZE = 0.22\n"+
			"~-// under is what a surface stands on [in] a [kind of glass]\n~+// under is what a surface stands on [at] a [step]\n")
}

// 75
func TestDiffWhitespace(t *testing.T) {
	checkDiff(t, "func a() {\n\treturn 1\n}\n", "func a() {\n    return 1\n}\n", false,
		"  func a() {\n~-[\t]return 1\n~+[    ]return 1\n  }\n")
	checkDiff(t, "a\nb\n", "a \nb\n", false, "~-a\n~+a[ ]\n  b\n")
	checkDiff(t, "a\nb\n", "a\n\nb\n", false, "  a\n+ \n  b\n")
	checkDiff(t, "Один  пробел.\n", "Один пробел.\n", true, "~-Один[  ]пробел.\n~+Один[ ]пробел.\n")
	checkDiff(t, "a\n", "a", false, "~-a↵\n~+a\n")
}

// 76
func TestDiffMove(t *testing.T) {
	checkDiff(t, "alpha\nbeta\ngamma\n", "alpha\ngamma\nbeta\n", false, "  alpha\n- beta\n  gamma\n+ beta\n")
}

// 77, 78
func TestFileAndResponseDiff(t *testing.T) {
	c, _ := open(t)
	r := create(t, c, Submission{Path: "/new.md", Content: "a\nb\n"},
		Submission{Path: "/old.go", Content: "x := 1\n", Original: orig("x := 2\n")})
	chs, err := c.FileDiff(r, "/new.md")
	must(t, err)
	if got := render(chs); got != "+ a\n+ b\n" {
		t.Fatal(got)
	}
	chs, err = c.FileDiff(r, "/old.go")
	must(t, err)
	if got := render(chs); got != "~-x := [2]\n~+x := [1]\n" {
		t.Fatal(got)
	}
	if _, err := c.ResponseDiff(r, "/old.go"); err != ErrNoResponse {
		t.Fatal(err)
	}
	respond(t, c, r, Submission{Path: "/old.go", Content: "x := 1\ny := 3\n"}, Submission{Path: "/new.md", Deleted: true})
	chs, err = c.ResponseDiff(r, "/old.go")
	must(t, err)
	if got := render(chs); got != "  x := 1\n+ y := 3\n" {
		t.Fatal(got)
	}
	chs, err = c.ResponseDiff(r, "/new.md")
	must(t, err)
	if got := render(chs); got != "- a\n- b\n" {
		t.Fatal(got)
	}
	must(t, c.AcceptResponse(r))
	chs, err = c.FileDiff(r, "/new.md")
	must(t, err)
	if len(chs) != 0 {
		t.Fatal(render(chs))
	}
	chs, err = c.FileDiff(r, "/old.go")
	must(t, err)
	if got := render(chs); got != "~-x := [2]\n~+x := [1]\n+ y := 3\n" {
		t.Fatal(got)
	}
}

func TestDiffSame(t *testing.T) {
	checkDiff(t, "a\nb\n", "a\nb\n", false, "  a\n  b\n")
	if chs := DiffText("", "", true); len(chs) != 0 {
		t.Fatal(chs)
	}
}

// residue — всё неподсвеченное в версии, по порядку. Должно совпасть в обеих версиях.
func residue(text string, chs []Chunk, old bool) string {
	lines, _ := splitLines(text)
	var b strings.Builder
	for _, c := range chs {
		ls := c.New
		if old {
			ls = c.Old
		}
		if c.Kind == ChunkDeleted || c.Kind == ChunkAdded {
			continue
		}
		for _, l := range ls {
			nl := strings.HasSuffix(lines[l.N-1], "\n")
			p := 0
			for _, m := range l.Marks {
				b.WriteString(l.Text[p:m.From])
				p = m.To
			}
			b.WriteString(l.Text[p:])
			if nl && !l.Break {
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// checkInvariants — сценарий 79 и целостность кусков.
func checkInvariants(t *testing.T, old, nw string, prose bool) {
	t.Helper()
	chs := DiffText(old, nw, prose)
	fail := func(f string, a ...any) {
		t.Helper()
		t.Fatalf("%s\nold=%q\nnew=%q\nprose=%v\n%s", fmt.Sprintf(f, a...), old, nw, prose, render(chs))
	}
	// Строки идут по порядку и все на месте.
	for _, sd := range []struct {
		text string
		old  bool
	}{{old, true}, {nw, false}} {
		lines, _ := splitLines(sd.text)
		n := 0
		for _, c := range chs {
			ls := c.New
			if sd.old {
				ls = c.Old
			}
			for _, l := range ls {
				n++
				if l.N != n || l.Text != strings.TrimSuffix(lines[n-1], "\n") {
					fail("строка %d не на месте", n)
				}
				for k, m := range l.Marks {
					if m.From >= m.To || m.To > len(l.Text) || (k > 0 && l.Marks[k-1].To >= m.From) {
						fail("подсветка %v в строке %d", m, n)
					}
					if k > 0 && strings.TrimSpace(l.Text[l.Marks[k-1].To:m.From]) == "" {
						fail("дыра из пробелов в строке %d", n)
					}
				}
			}
		}
		if n != len(lines) {
			fail("строк %d, а должно быть %d", n, len(lines))
		}
	}
	for _, c := range chs {
		if c.Kind == ChunkSame && len(c.Old) != len(c.New) {
			fail("неравный кусок")
		}
		for k := range c.Old {
			if c.Kind == ChunkSame && c.Old[k].Text != c.New[k].Text {
				fail("разные строки в «same»")
			}
		}
	}
	// 79: всё неподсвеченное совпадает. В тексте перенос строки равен пробелу.
	ro, rn := residue(old, chs, true), residue(nw, chs, false)
	if prose {
		ro, rn = strings.ReplaceAll(ro, "\n", " "), strings.ReplaceAll(rn, "\n", " ")
	}
	if ro != rn {
		fail("неподсвеченное различается:\n%q\n%q", ro, rn)
	}
}

var vocab = []string{"the", "cat", "dog", "кот", "пёс", "a", "of", "x", "foo", "bar", "(", ")", ",", ".", ";", "{", "}", "=", "//", "\t", "  ", " ", " ", " ", " ", "\n", "\n", "\n\n", "```\n", "- ", "# ", "!", "Привет", "мир"}

func randText(r *rand.Rand, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(vocab[r.Intn(len(vocab))])
	}
	return b.String()
}

func mutate(r *rand.Rand, s string) string {
	for k := r.Intn(4) + 1; k > 0; k-- {
		p := 0
		if len(s) > 0 {
			p = r.Intn(len(s) + 1)
		}
		for p < len(s) && !strings.HasPrefix(s[p:], "") {
			p++
		}
		// Режем только по границе символа.
		for p > 0 && p < len(s) && s[p]&0xC0 == 0x80 {
			p--
		}
		q := p + r.Intn(12)
		if q > len(s) {
			q = len(s)
		}
		for q > p && q < len(s) && s[q]&0xC0 == 0x80 {
			q--
		}
		switch r.Intn(3) {
		case 0:
			s = s[:p] + randText(r, r.Intn(6)+1) + s[p:]
		case 1:
			s = s[:p] + s[q:]
		default:
			s = s[:p] + randText(r, r.Intn(4)+1) + s[q:]
		}
	}
	return s
}

// 79
func TestDiffEveryChangeVisible(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		old := randText(r, r.Intn(60))
		nw := mutate(r, old)
		if r.Intn(5) == 0 {
			nw = randText(r, r.Intn(60))
		}
		checkInvariants(t, old, nw, i%2 == 0)
	}
}

func bigFile(lines int) string {
	r := rand.New(rand.NewSource(2))
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "Строка %d: %s\n", i, randText(r, 12))
	}
	return b.String()
}

func TestDiffFast(t *testing.T) {
	old := bigFile(20000)
	nw := strings.Replace(old, "Строка 10000:", "Строка десять тысяч:", 1)
	nw = strings.Replace(nw, "Строка 500:", "Строка пятьсот:", 1)
	start := time.Now()
	DiffText(old, nw, true)
	small := time.Since(start)
	r := rand.New(rand.NewSource(3))
	start = time.Now()
	DiffText(old, bigFile(20000)+randText(r, 2000), false)
	big := time.Since(start)
	t.Logf("20 000 строк: маленькая правка %v, всё переписано %v", small, big)
	if small > 300*time.Millisecond || big > 3*time.Second {
		t.Fatalf("медленно: %v, %v", small, big)
	}
}
