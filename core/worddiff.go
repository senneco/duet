package core

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Отличия для показа. Ядро само решает, что подсветить: слова, фразы, предложения или абзацы
// целиком. Клиенты только рисуют.
//
// Как считается:
//  1. Отличия по строкам, затем внутри изменённых участков — по словам (токенам).
//  2. Склейка: короткие неизменные куски между правками входят в подсветку, чтобы не было дыр.
//  3. Текст: абзац переписан больше чем наполовину — показан целиком удалённым и добавленным;
//     предложение переписано больше чем наполовину — подсвечено целиком.
//  4. Строки сводятся в пары «было → стало» по общим словам; строки без общего — удалены или добавлены.
//  5. Код: строка переписана больше чем наполовину — подсвечена целиком.
//
// Гарантия: всё, что не подсвечено, совпадает в обеих версиях. Единственное исключение —
// перенос строки внутри абзаца текста считается тем же, что пробел (сценарий 72).

// ChunkKind — вид куска отличий.
type ChunkKind string

const (
	ChunkSame    ChunkKind = "same"    // строки не менялись
	ChunkChanged ChunkKind = "changed" // старые строки стали новыми; изменённое подсвечено
	ChunkDeleted ChunkKind = "deleted" // строки удалены целиком
	ChunkAdded   ChunkKind = "added"   // строки добавлены целиком
)

// Span — подсвеченное место в строке: байты [From, To) текста строки.
type Span struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// DiffLine — строка одной из версий.
type DiffLine struct {
	N     int    `json:"n"`    // номер строки в своей версии, с 1
	Text  string `json:"text"` // без перевода строки
	Marks []Span `json:"marks,omitempty"`
	// Break — изменён сам перевод строки в конце: строки склеили или разбили по-новому.
	Break bool `json:"break,omitempty"`
}

// Chunk — кусок отличий. В «same» строк Old и New поровну и они одинаковые;
// в «deleted» есть только Old, в «added» — только New.
type Chunk struct {
	Kind ChunkKind  `json:"kind"`
	Old  []DiffLine `json:"old,omitempty"`
	New  []DiffLine `json:"new,omitempty"`
}

// Пороги. Подбираются по живым примерам.
const (
	// tokenBudget — шагов поиска на один изменённый участок; не хватило — участок переписан целиком.
	tokenBudget = 4_000_000
	// absorbWords — сколько неизменных слов между двумя правками в тексте входит в подсветку.
	absorbWords = 2
)

// IsProse — файл показывается как текст (абзацы и предложения), а не как код.
func IsProse(p string) bool {
	base := path.Base(p)
	switch strings.ToLower(path.Ext(base)) {
	case ".md", ".markdown", ".mdx", ".txt", ".text", ".rst", ".adoc", ".asciidoc", ".org", ".wiki":
		return true
	case "":
		switch base {
		case "Makefile", "Dockerfile", "Jenkinsfile", "Gemfile", "Rakefile", "Procfile", "BUILD", "WORKSPACE":
			return false
		}
		// Страницы Вики и тикеты обычно без расширения.
		return !strings.HasPrefix(base, ".")
	}
	return false
}

type tokKind uint8

const (
	tkWord tokKind = iota
	tkSpace
	tkNewline
	tkPunct
)

type token struct {
	s, e int // байты в тексте
	line int
	kind tokKind
}

type lineKind uint8

const (
	lkProse lineKind = iota
	lkBlank
	lkCode
)

// side — одна из двух версий.
type side struct {
	text    string
	lines   []string
	offs    []int
	toks    []token
	lineTok []int      // первый токен строки; последний элемент — len(toks)
	kind    []lineKind // вид строки
	block   []int      // номер абзаца строки, -1 у пустых
	keys    []int      // ключ токена для сравнения (только в изменённых участках)
	m       []int      // m[t] — токен другой версии, с которым совпал t, иначе -1
	real    []bool     // t изменён по поиску отличий, до склейки: по нему считаются доли
}

func isSpace(r rune) bool { return r != '\n' && unicode.IsSpace(r) }
func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

func newSide(text string, prose bool) *side {
	s := &side{text: text}
	s.lines, s.offs = splitLines(text)
	s.lineTok = make([]int, 0, len(s.lines)+1)
	for li, ln := range s.lines {
		s.lineTok = append(s.lineTok, len(s.toks))
		base := s.offs[li]
		for p := 0; p < len(ln); {
			st := p
			r, sz := utf8.DecodeRuneInString(ln[p:])
			p += sz
			var k tokKind
			switch {
			case r == '\n':
				k = tkNewline
			case isSpace(r):
				k = tkSpace
				for p < len(ln) {
					r, sz = utf8.DecodeRuneInString(ln[p:])
					if !isSpace(r) {
						break
					}
					p += sz
				}
			case isWord(r):
				k = tkWord
				for p < len(ln) {
					r, sz = utf8.DecodeRuneInString(ln[p:])
					if !isWord(r) {
						break
					}
					p += sz
				}
			default:
				k = tkPunct
			}
			s.toks = append(s.toks, token{base + st, base + p, li, k})
		}
	}
	s.lineTok = append(s.lineTok, len(s.toks))
	s.keys = make([]int, len(s.toks))
	s.m = make([]int, len(s.toks))
	for i := range s.m {
		s.m[i] = -1
	}
	s.classify(prose)
	return s
}

// classify размечает строки: текст, пустая, код; и собирает строки текста в абзацы.
func (s *side) classify(prose bool) {
	n := len(s.lines)
	s.kind = make([]lineKind, n)
	s.block = make([]int, n)
	blk, open, fence := -1, false, false
	for i, ln := range s.lines {
		t := strings.TrimSpace(ln)
		isFence := strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
		switch {
		case !prose || isFence || fence:
			if isFence {
				fence = !fence
			}
			s.kind[i] = lkCode
			if t == "" {
				s.kind[i] = lkBlank
			}
			blk++
			s.block[i] = blk
			open = false
		case t == "":
			s.kind[i] = lkBlank
			s.block[i] = -1
			open = false
		default:
			s.kind[i] = lkProse
			if !open || startsBlock(t) {
				blk++
			}
			s.block[i] = blk
			open = !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "|")
		}
	}
}

// startsBlock — строка начинает новый абзац даже без пустой строки перед ней.
func startsBlock(t string) bool {
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "|") {
		return true
	}
	if len(t) >= 2 && strings.ContainsRune("-*+", rune(t[0])) && t[1] == ' ' {
		return true
	}
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(t) && (t[i] == '.' || t[i] == ')') && t[i+1] == ' '
}

func (s *side) str(t int) string { return s.text[s.toks[t].s:s.toks[t].e] }

func (s *side) nlines() int { return len(s.lines) }

// wd — подсчёт отличий двух версий.
type wd struct {
	a, b   *side
	intern map[string]int
}

// DiffText считает отличия двух текстов. prose — показывать как текст (см. IsProse).
func DiffText(old, nw string, prose bool) []Chunk {
	w := &wd{a: newSide(old, prose), b: newSide(nw, prose), intern: map[string]int{}}
	if old == nw {
		return w.allSame()
	}
	w.align()
	w.slide()
	w.refine()
	w.slideNewlines(w.a, w.b)
	w.slideNewlines(w.b, w.a)
	for _, s := range []*side{w.a, w.b} {
		s.real = make([]bool, len(s.m))
		for t, u := range s.m {
			s.real[t] = u < 0
		}
	}
	w.cleanup(0, len(w.a.toks), 0, len(w.b.toks))
	whole := map[*side]map[int]bool{w.a: w.paragraphs(w.a, w.b), w.b: w.paragraphs(w.b, w.a)}
	w.sentences(w.a, w.b, whole[w.a])
	w.sentences(w.b, w.a, whole[w.b])
	w.cleanup(0, len(w.a.toks), 0, len(w.b.toks))
	return w.build()
}

func (w *wd) allSame() []Chunk {
	if w.a.nlines() == 0 {
		return nil
	}
	ch := Chunk{Kind: ChunkSame}
	for l := range w.a.lines {
		ch.Old = append(ch.Old, w.line(w.a, l, false))
		ch.New = append(ch.New, w.line(w.b, l, false))
	}
	return []Chunk{ch}
}

// key — ключ токена для сравнения. Перевод строки внутри текста равен пробелу:
// абзац, разбитый на строки по-новому, не подсвечивается.
func (w *wd) key(s *side, t int) int {
	str := s.str(t)
	if s.softBreak(t) {
		str = " " // перевод строки внутри абзаца
	}
	id, ok := w.intern[str]
	if !ok {
		id = len(w.intern)
		w.intern[str] = id
	}
	return id
}

// softBreak — t перевод строки внутри абзаца текста (а не конец абзаца или пункта списка).
func (s *side) softBreak(t int) bool {
	l := s.toks[t].line
	return s.toks[t].kind == tkNewline && s.kind[l] == lkProse &&
		l+1 < s.nlines() && s.kind[l+1] == lkProse && s.block[l+1] == s.block[l]
}

// align — отличия по строкам, затем по токенам внутри изменённых участков.
func (w *wd) align() {
	A, B := w.a, w.b
	hs := diffLines(A.lines, B.lines)
	d := &differ{a: A.keys, b: B.keys, ma: A.m, mb: B.m, patience: true}
	i, j := 0, 0
	for _, h := range append(hs, hunk{A.nlines(), A.nlines(), B.nlines(), B.nlines()}) {
		for ; i < h.A0; i, j = i+1, j+1 {
			for k := 0; k < A.lineTok[i+1]-A.lineTok[i]; k++ {
				d.match(A.lineTok[i]+k, B.lineTok[j]+k)
			}
		}
		ta0, ta1, tb0, tb1 := A.lineTok[h.A0], A.lineTok[h.A1], B.lineTok[h.B0], B.lineTok[h.B1]
		if ta0 < ta1 && tb0 < tb1 {
			for t := ta0; t < ta1; t++ {
				A.keys[t] = w.key(A, t)
			}
			for t := tb0; t < tb1; t++ {
				B.keys[t] = w.key(B, t)
			}
			d.budget = tokenBudget
			d.run(ta0, ta1, tb0, tb1)
		}
		i, j = h.A1, h.B1
	}
}

// unmatch снимает совпадение токена t версии s (и его пары в версии o).
// soft — не трогать пару «перевод строки ↔ перевод строки»: конец строки и так виден.
func unmatch(s, o *side, t int, soft bool) bool {
	u := s.m[t]
	if u < 0 {
		return false
	}
	if soft && s.toks[t].kind == tkNewline && o.toks[u].kind == tkNewline {
		return false
	}
	s.m[t] = -1
	o.m[u] = -1
	return true
}

type seg struct {
	eq             bool
	i0, i1, j0, j1 int
}

// segs режет участок на чередующиеся куски: совпавшие и изменённые.
func (w *wd) segs(i0, i1, j0, j1 int) []seg {
	var out []seg
	i, j := i0, j0
	for i < i1 || j < j1 {
		s := seg{i0: i, j0: j}
		if i < i1 && j < j1 && w.a.m[i] == j {
			s.eq = true
			for i < i1 && j < j1 && w.a.m[i] == j {
				i++
				j++
			}
		} else {
			for i < i1 && w.a.m[i] < 0 {
				i++
			}
			for j < j1 && w.b.m[j] < 0 {
				j++
			}
		}
		s.i1, s.j1 = i, j
		out = append(out, s)
	}
	return out
}

// size — длина изменённого куска в байтах без переводов строк (большая из двух сторон).
func (w *wd) size(s seg) int {
	n := func(sd *side, t0, t1 int) int {
		c := 0
		for t := t0; t < t1; t++ {
			if sd.toks[t].kind != tkNewline {
				c += sd.toks[t].e - sd.toks[t].s
			}
		}
		return c
	}
	return max(n(w.a, s.i0, s.i1), n(w.b, s.j0, s.j1))
}

// cleanup склеивает правки: неизменный кусок между двумя правками входит в подсветку, если он
// короткий — одни пробелы и знаки, не больше двух слов текста или не длиннее правок по бокам.
// Через пустую строку (границу абзаца) и через конец строки кода не склеивает.
func (w *wd) cleanup(i0, i1, j0, j1 int) {
	for {
		ss := w.segs(i0, i1, j0, j1)
		changed := false
		for k := 1; k+1 < len(ss); k++ {
			s := ss[k]
			if !s.eq || ss[k-1].eq || ss[k+1].eq || !w.absorbable(s, ss[k-1], ss[k+1]) {
				continue
			}
			for t := s.i0; t < s.i1; t++ {
				if unmatch(w.a, w.b, t, true) {
					changed = true
				}
			}
		}
		if !changed {
			return
		}
	}
}

func (w *wd) absorbable(s, prev, next seg) bool {
	code, onlySpace, stop := false, true, false
	words, nlA, nlB := 0, 0, 0
	for t := s.i0; t < s.i1; t++ {
		tk := w.a.toks[t]
		if w.a.kind[tk.line] != lkProse {
			code = true
		}
		switch tk.kind {
		case tkNewline:
			nlA++
			if !w.a.softBreak(t) {
				stop = true // конец абзаца или пункта: склейка через него не идёт
			}
		case tkWord:
			words++
			onlySpace = false
		case tkPunct:
			onlySpace = false
			if isTerminal(w.a.str(t)) && t+1 < len(w.a.toks) && (w.a.toks[t+1].kind == tkSpace || w.a.toks[t+1].kind == tkNewline) {
				stop = true // конец предложения: склейка через него не идёт
			}
		}
	}
	for t := s.j0; t < s.j1; t++ {
		tk := w.b.toks[t]
		if w.b.kind[tk.line] != lkProse {
			code = true
		}
		if tk.kind == tkNewline {
			nlB++
			if !w.b.softBreak(t) {
				stop = true
			}
		}
	}
	n := w.size(s)
	switch {
	case n == 0, nlA > 1, nlB > 1, code && nlA+nlB > 0, !code && stop:
		return false
	case onlySpace, !code && words <= absorbWords:
		return true
	}
	return n <= w.size(prev) && n <= w.size(next)
}

// span — токены [t0, t1) одного абзаца или строки.
type span struct{ t0, t1 int }

// share — переписано больше половины: слов, а если слов нет — значимых токенов.
func rewritten(s *side, sp span) bool {
	words, wch, other, och := 0, 0, 0, 0
	for t := sp.t0; t < sp.t1; t++ {
		switch s.toks[t].kind {
		case tkWord:
			words++
			if s.real[t] {
				wch++
			}
		case tkPunct:
			other++
			if s.real[t] {
				och++
			}
		}
	}
	if words > 0 {
		return wch*2 > words
	}
	return other > 0 && och*2 > other
}

// blocks — абзацы текста версии s.
func (s *side) blocks() []span {
	var out []span
	for l := 0; l < s.nlines(); {
		if s.kind[l] != lkProse {
			l++
			continue
		}
		e := l + 1
		for e < s.nlines() && s.kind[e] == lkProse && s.block[e] == s.block[l] {
			e++
		}
		out = append(out, span{s.lineTok[l], s.lineTok[e]})
		l = e
	}
	return out
}

// paragraphs: абзац переписан больше чем наполовину — показываем его целиком удалённым
// и добавленным. Возвращает такие абзацы (по первому токену).
func (w *wd) paragraphs(s, o *side) map[int]bool {
	whole := map[int]bool{}
	for _, b := range s.blocks() {
		if !rewritten(s, b) {
			continue
		}
		whole[b.t0] = true
		for t := b.t0; t < b.t1; t++ {
			unmatch(s, o, t, false)
		}
	}
	return whole
}

func isTerminal(str string) bool { return str == "." || str == "!" || str == "?" || str == "…" }
func isCloser(str string) bool   { return strings.Contains(`"'»)]”’`, str) && str != "" }

// sentences: предложение переписано больше чем наполовину — подсвечено целиком.
func (w *wd) sentences(s, o *side, whole map[int]bool) {
	for _, b := range s.blocks() {
		if whole[b.t0] {
			continue
		}
		start, last := -1, -1
		b.t0 = s.markerEnd(b.t0)
		flush := func(end int) {
			if start >= 0 && rewritten(s, span{start, end}) {
				for t := start; t < end; t++ {
					unmatch(s, o, t, true)
				}
			}
			start = -1
		}
		for t := b.t0; t < b.t1; t++ {
			k := s.toks[t].kind
			if k == tkSpace || k == tkNewline {
				continue
			}
			if start < 0 {
				start = t
			}
			last = t
			if k != tkPunct || !isTerminal(s.str(t)) {
				continue
			}
			end := t + 1
			for end < b.t1 && s.toks[end].kind == tkPunct && isCloser(s.str(end)) {
				end++
			}
			if end == b.t1 || s.toks[end].kind == tkSpace || s.toks[end].kind == tkNewline {
				flush(end)
				t = end - 1
				last = t
			}
		}
		flush(last + 1)
	}
}

// sameLine — старая строка l совпала с новой целиком; номер новой строки или -1.
func (w *wd) sameLine(l int) int {
	A, B := w.a, w.b
	t0, t1 := A.lineTok[l], A.lineTok[l+1]
	if t0 == t1 || A.m[t0] < 0 {
		return -1
	}
	j := B.toks[A.m[t0]].line
	if B.lineTok[j] != A.m[t0] || B.lineTok[j+1]-B.lineTok[j] != t1-t0 || A.lines[l] != B.lines[j] {
		return -1
	}
	for k := 0; k < t1-t0; k++ {
		if A.m[t0+k] != A.m[t0]+k {
			return -1
		}
	}
	return j
}

// regions обходит участки несовпавших строк: старые [i0,i1), новые [j0,j1).
// same вызывается для каждой пары совпавших строк.
func (w *wd) regions(same func(i, j int), region func(i0, i1, j0, j1 int)) {
	na, nb := w.a.nlines(), w.b.nlines()
	sm := make([]int, na)
	for i := range sm {
		sm[i] = w.sameLine(i)
	}
	i, j := 0, 0
	for i < na || j < nb {
		if i < na && sm[i] == j {
			same(i, j)
			i++
			j++
			continue
		}
		ib := i
		for ib < na && sm[ib] < 0 {
			ib++
		}
		jb := nb
		if ib < na {
			jb = sm[ib]
		}
		region(i, ib, j, jb)
		i, j = ib, jb
	}
}

// grp — изменённые строки, сведённые в пару: старые [i0..i1], новые [j0..j1] включительно.
type grp struct{ i0, i1, j0, j1 int }

// groups сводит строки участка в пары. Строки связаны, если у них общее слово или обе
// совпали целиком (например, сменился только отступ). Общая скобка или точка с запятой строки
// не связывают. Совпадения между разными парами и со строками без пары снимаются.
func (w *wd) groups(i0, i1, j0, j1 int) []grp {
	A, B := w.a, w.b
	var gs []grp
	for t := A.lineTok[i0]; t < A.lineTok[i1]; t++ {
		u := A.m[t]
		k := A.toks[t].kind
		if u < 0 || (k != tkWord && k != tkPunct) {
			continue
		}
		li, lj := A.toks[t].line, B.toks[u].line
		if k != tkWord && (!full(A, li) || !full(B, lj)) {
			continue
		}
		if n := len(gs); n > 0 && (li <= gs[n-1].i1 || lj <= gs[n-1].j1) {
			gs[n-1].i1 = max(gs[n-1].i1, li)
			gs[n-1].j1 = max(gs[n-1].j1, lj)
		} else {
			gs = append(gs, grp{li, li, lj, lj})
		}
	}
	ga := make([]int, i1-i0)
	gb := make([]int, j1-j0)
	for k := range ga {
		ga[k] = -1
	}
	for k := range gb {
		gb[k] = -1
	}
	for g, x := range gs {
		for l := x.i0; l <= x.i1; l++ {
			ga[l-i0] = g
		}
		for l := x.j0; l <= x.j1; l++ {
			gb[l-j0] = g
		}
	}
	for t := A.lineTok[i0]; t < A.lineTok[i1]; t++ {
		if u := A.m[t]; u >= 0 {
			g := ga[A.toks[t].line-i0]
			if g < 0 || g != gb[B.toks[u].line-j0] {
				unmatch(A, B, t, false)
			}
		}
	}
	return gs
}

// refine: строки сведены в пары — досравниваем слова внутри каждой пары. Общее выравнивание
// участка могло отдать слово из пары строке выше («//» в начале комментария).
func (w *wd) refine() {
	A, B := w.a, w.b
	d := &differ{a: A.keys, b: B.keys, ma: A.m, mb: B.m, patience: true}
	w.regions(func(int, int) {}, func(i0, i1, j0, j1 int) {
		for _, g := range w.groups(i0, i1, j0, j1) {
			for _, s := range w.segs(A.lineTok[g.i0], A.lineTok[g.i1+1], B.lineTok[g.j0], B.lineTok[g.j1+1]) {
				if !s.eq && s.i0 < s.i1 && s.j0 < s.j1 {
					d.budget = tokenBudget
					d.run(s.i0, s.i1, s.j0, s.j1)
				}
			}
		}
	})
}

func (w *wd) build() []Chunk {
	A, B := w.a, w.b
	var out []Chunk
	w.regions(func(i, j int) {
		if n := len(out); n > 0 && out[n-1].Kind == ChunkSame {
			out[n-1].Old = append(out[n-1].Old, w.line(A, i, false))
			out[n-1].New = append(out[n-1].New, w.line(B, j, false))
		} else {
			out = append(out, Chunk{Kind: ChunkSame, Old: []DiffLine{w.line(A, i, false)}, New: []DiffLine{w.line(B, j, false)}})
		}
	}, func(i0, i1, j0, j1 int) {
		out = append(out, w.region(i0, i1, j0, j1)...)
	})
	return out
}

// region показывает участок несовпавших строк: пары «было → стало», удалённые и добавленные.
func (w *wd) region(i0, i1, j0, j1 int) []Chunk {
	A, B := w.a, w.b
	gs := w.groups(i0, i1, j0, j1)
	// Код: строка переписана больше чем наполовину — подсвечена целиком.
	for _, x := range gs {
		w.codeLines(A, B, x.i0, x.i1)
		w.codeLines(B, A, x.j0, x.j1)
	}
	w.cleanup(A.lineTok[i0], A.lineTok[i1], B.lineTok[j0], B.lineTok[j1])
	for w.fillGaps(A, B, i0, i1) || w.fillGaps(B, A, j0, j1) {
	}
	w.rejoinNewlines(A, B, i0, i1)
	w.rejoinNewlines(B, A, j0, j1)

	var out []Chunk
	ci, cj := i0, j0
	loners := func(ie, je int) {
		if ci < ie {
			ch := Chunk{Kind: ChunkDeleted}
			for ; ci < ie; ci++ {
				ch.Old = append(ch.Old, w.line(A, ci, false))
			}
			out = append(out, ch)
		}
		if cj < je {
			ch := Chunk{Kind: ChunkAdded}
			for ; cj < je; cj++ {
				ch.New = append(ch.New, w.line(B, cj, false))
			}
			out = append(out, ch)
		}
	}
	for _, x := range gs {
		loners(x.i0, x.j0)
		ch := Chunk{Kind: ChunkChanged}
		for ; ci <= x.i1; ci++ {
			ch.Old = append(ch.Old, w.line(A, ci, true))
		}
		for ; cj <= x.j1; cj++ {
			ch.New = append(ch.New, w.line(B, cj, true))
		}
		out = append(out, ch)
	}
	loners(i1, j1)
	return out
}

func (w *wd) codeLines(s, o *side, l0, l1 int) {
	for l := l0; l <= l1; l++ {
		if s.kind[l] != lkCode {
			continue
		}
		first, last, total, ch := -1, -1, 0, 0
		for t := s.lineTok[l]; t < s.lineTok[l+1]; t++ {
			tk := s.toks[t]
			if tk.kind == tkSpace || tk.kind == tkNewline {
				continue
			}
			if first < 0 {
				first = t
			}
			last = t
			total += tk.e - tk.s
			if s.real[t] {
				ch += tk.e - tk.s
			}
		}
		if ch*2 > total {
			for t := first; t <= last; t++ {
				unmatch(s, o, t, true)
			}
		}
	}
}

// line — строка для показа; marks — подсветить несовпавшее.
func (w *wd) line(s *side, l int, marks bool) DiffLine {
	dl := DiffLine{N: l + 1, Text: strings.TrimSuffix(s.lines[l], "\n")}
	if !marks {
		return dl
	}
	base := s.offs[l]
	for t := s.lineTok[l]; t < s.lineTok[l+1]; t++ {
		tk := s.toks[t]
		if s.m[t] >= 0 {
			continue
		}
		if tk.kind == tkNewline {
			dl.Break = true
			continue
		}
		from, to := tk.s-base, tk.e-base
		if n := len(dl.Marks); n > 0 && dl.Marks[n-1].To == from {
			dl.Marks[n-1].To = to
		} else {
			dl.Marks = append(dl.Marks, Span{from, to})
		}
	}
	return dl
}

// content — текст версии n файла; удалённый или отсутствующий файл — пустой текст.
func content(f *File, n int) string {
	if n < 0 || f.Versions[n].Deleted {
		return ""
	}
	return f.Versions[n].Content
}

// FileDiff — отличия «база → текущая» (основной экран). Нет базы — всё добавлено,
// файл удалён — всё удалено.
func (c *Core) FileDiff(reviewID, p string) ([]Chunk, error) {
	c.mu.Lock()
	r, err := c.review(reviewID)
	var f *File
	if err == nil {
		f, err = c.file(r, p)
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	old, nw := content(f, f.Base), content(f, f.Current)
	c.mu.Unlock()
	return DiffText(old, nw, IsProse(p)), nil
}

// ResponseDiff — отличия «текущая → ответ» (окно ответа). Если ответ файл не меняет — ErrNoResponse.
func (c *Core) ResponseDiff(reviewID, p string) ([]Chunk, error) {
	c.mu.Lock()
	r, err := c.review(reviewID)
	var f *File
	if err == nil {
		f, err = c.file(r, p)
	}
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	resp := r.pending()
	n, ok := 0, false
	if resp != nil {
		n, ok = resp.Files[p]
	}
	if !ok {
		c.mu.Unlock()
		return nil, ErrNoResponse
	}
	from := f.Current
	if from < 0 {
		from = f.Base // файл, которого владелец ещё не видел: отличие от исходной версии, если она есть
	}
	old, nw := content(f, from), content(f, n)
	c.mu.Unlock()
	return DiffText(old, nw, IsProse(p)), nil
}

// full — все значимые токены строки совпали.
func full(s *side, l int) bool {
	for t := s.lineTok[l]; t < s.lineTok[l+1]; t++ {
		if k := s.toks[t].kind; (k == tkWord || k == tkPunct) && s.m[t] < 0 {
			return false
		}
	}
	return true
}

// slideNewlines: перевод строки остаётся со своей строкой. Поиск отличий мог отдать его пару
// соседней строке или пробелу посреди строки («foo» → «foo ,»). Если у строки, где продолжается
// её текст в другой версии, перевод строки свободен, пара переносится на него.
func (w *wd) slideNewlines(s, o *side) {
	for t, u := range s.m {
		if u < 0 || s.toks[t].kind != tkNewline {
			continue
		}
		first := s.lineTok[s.toks[t].line]
		p := t - 1
		for p >= first && s.m[p] < 0 {
			p--
		}
		if p < first {
			continue
		}
		lo := o.toks[s.m[p]].line
		v := o.lineTok[lo+1] - 1
		if v == u || o.toks[v].kind != tkNewline || o.m[v] >= 0 {
			continue
		}
		ok := true
		for x := min(u, v) + 1; x < max(u, v); x++ {
			if o.m[x] >= 0 {
				ok = false
				break
			}
		}
		if ok {
			o.m[u] = -1
			s.m[t] = v
			o.m[v] = t
		}
	}
}

// fillGaps: пробелы между двумя подсветками в одной строке тоже подсвечены — без дыр.
func (w *wd) fillGaps(s, o *side, l0, l1 int) bool {
	changed := false
	for l := l0; l < l1; l++ {
		last := -1 // последний несовпавший токен строки
		for t := s.lineTok[l]; t < s.lineTok[l+1]; t++ {
			tk := s.toks[t]
			if tk.kind == tkNewline {
				break
			}
			if s.m[t] >= 0 {
				if tk.kind != tkSpace {
					last = -1
				}
				continue
			}
			if last >= 0 {
				for x := last + 1; x < t; x++ {
					if unmatch(s, o, x, true) {
						changed = true
					}
				}
			}
			last = t
		}
	}
	return changed
}

// markerEnd пропускает метку в начале абзаца («- », «1. », «## »): она не часть предложения.
func (s *side) markerEnd(t0 int) int {
	t := t0
	for t < len(s.toks) && s.toks[t].kind == tkSpace {
		t++
	}
	if t >= len(s.toks) || !startsBlock(strings.TrimSpace(s.lines[s.toks[t].line])) {
		return t0
	}
	for t < len(s.toks) && s.toks[t].kind != tkSpace && s.toks[t].kind != tkNewline {
		if s.toks[t].kind == tkWord && !isDigits(s.str(t)) {
			return t0 // «|» таблицы или слово: метки нет
		}
		t++
	}
	return t
}

func isDigits(str string) bool {
	for _, r := range str {
		if r < '0' || r > '9' {
			return false
		}
	}
	return str != ""
}

// rejoinNewlines: после всех правил перевод строки мог остаться без пары, хотя перевод строки
// той строки, где продолжается его текст, тоже свободен. Это один и тот же конец строки — сводим.
func (w *wd) rejoinNewlines(s, o *side, l0, l1 int) {
	for l := l0; l < l1; l++ {
		first, t := s.lineTok[l], s.lineTok[l+1]-1
		if s.toks[t].kind != tkNewline || s.m[t] >= 0 {
			continue
		}
		p := t - 1
		for p >= first && s.m[p] < 0 {
			p--
		}
		if p < first {
			continue
		}
		cp := s.m[p]
		v := o.lineTok[o.toks[cp].line+1] - 1
		if o.toks[v].kind != tkNewline || o.m[v] >= 0 {
			continue
		}
		ok := true
		for x := cp + 1; x < v; x++ {
			if o.m[x] >= 0 {
				ok = false
				break
			}
		}
		if ok {
			s.m[t] = v
			o.m[v] = t
		}
	}
}

// slide: вставку или удаление часто можно сдвинуть на несколько токенов — «;» или конец строки
// есть и до, и после. Выбираем место, где правка — целые строки: кончается переводом строки
// и начинается с начала строки. Иначе соседние строки с одинаковым концом путаются
// («…не снимает;» отдаёт свою «;» добавленной строке ниже).
func (w *wd) slide() {
	for _, sg := range w.segs(0, len(w.a.toks), 0, len(w.b.toks)) {
		switch {
		case sg.eq:
		case sg.i0 == sg.i1:
			w.slideOne(w.b, w.a, sg.j0, sg.j1)
		case sg.j0 == sg.j1:
			w.slideOne(w.a, w.b, sg.i0, sg.i1)
		}
	}
}

// slideOne сдвигает несовпавший кусок [x0,x1) версии s, у которого в версии o пары нет.
func (w *wd) slideOne(s, o *side, x0, x1 int) {
	if x0 == 0 || x1 >= len(s.toks) || s.m[x0-1] < 0 || s.m[x1] < 0 || s.m[x0-1]+1 != s.m[x1] {
		return
	}
	same := func(p, q int) bool { return w.key(s, p) == w.key(s, q) }
	l := 0
	for x0-l-1 >= 0 && x1-l-1 >= x0 && s.m[x0-l-1] == s.m[x0-1]-l && same(x0-l-1, x1-l-1) {
		l++
	}
	r := 0
	for x1+r < len(s.toks) && x0+r < x1 && s.m[x1+r] == s.m[x1]+r && same(x1+r, x0+r) {
		r++
	}
	score := func(a, b int) int {
		sc := 0
		if s.toks[b-1].kind == tkNewline {
			sc += 2
		}
		if a == 0 || s.toks[a-1].kind == tkNewline {
			sc++
		}
		return sc
	}
	best, bestSc := 0, score(x0, x1)
	for d := -l; d <= r; d++ {
		if sc := score(x0+d, x1+d); sc > bestSc {
			best, bestSc = d, sc
		}
	}
	for k := 0; k < best; k++ { // вправо: x0+k берёт пару у x1+k
		u := s.m[x1+k]
		s.m[x1+k] = -1
		s.m[x0+k] = u
		o.m[u] = x0 + k
	}
	for k := 1; k <= -best; k++ { // влево: x1-k берёт пару у x0-k
		u := s.m[x0-k]
		s.m[x0-k] = -1
		s.m[x1-k] = u
		o.m[u] = x1 - k
	}
}
