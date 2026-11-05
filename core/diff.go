package core

import "strings"

// hunk — изменённый участок: строки [A0,A1) старого текста заменены строками [B0,B1) нового.
type hunk struct{ A0, A1, B0, B1 int }

// splitLines режет текст на строки, оставляя "\n" в конце каждой.
// Возвращает строки и смещения их начала; последний элемент смещений — длина текста.
func splitLines(s string) ([]string, []int) {
	var lines []string
	offs := []int{0}
	for len(s) > 0 {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			i = len(s) - 1
		}
		lines = append(lines, s[:i+1])
		offs = append(offs, offs[len(offs)-1]+i+1)
		s = s[i+1:]
	}
	return lines, offs
}

// lineBudget — сколько шагов поиска можно потратить на отличия по строкам.
// Если не хватило, оставшийся участок считается переписанным целиком: это честно, просто грубее.
const lineBudget = 50_000_000

// diffLines — отличия по строкам.
func diffLines(a, b []string) []hunk {
	ids := map[string]int{}
	key := func(ls []string) []int {
		ks := make([]int, len(ls))
		for i, l := range ls {
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}
			ks[i] = id
		}
		return ks
	}
	d := newDiffer(key(a), key(b), lineBudget)
	d.run(0, len(a), 0, len(b))
	return hunks(d.ma, d.mb)
}

// hunks собирает изменённые участки из совпадений.
func hunks(ma, mb []int) []hunk {
	var hs []hunk
	i, j := 0, 0
	for i < len(ma) || j < len(mb) {
		if i < len(ma) && j < len(mb) && ma[i] == j {
			i++
			j++
			continue
		}
		a0, b0 := i, j
		for i < len(ma) && ma[i] < 0 {
			i++
		}
		for j < len(mb) && mb[j] < 0 {
			j++
		}
		hs = append(hs, hunk{a0, i, b0, j})
	}
	return hs
}

// differ ищет наибольшую общую подпоследовательность двух последовательностей
// (алгоритм Майерса с поиском середины, память линейная).
// Результат — совпадения: ma[i] = j, если a[i] совпал с b[j], иначе -1.
type differ struct {
	a, b   []int
	ma, mb []int
	budget int
	// patience — сначала выравнивать по опорам: элементам, которые встречаются ровно один раз
	// в обеих частях. Так частые слова («the», скобки) не уводят выравнивание в сторону.
	patience bool
}

func newDiffer(a, b []int, budget int) *differ {
	d := &differ{a: a, b: b, ma: make([]int, len(a)), mb: make([]int, len(b)), budget: budget}
	for i := range d.ma {
		d.ma[i] = -1
	}
	for j := range d.mb {
		d.mb[j] = -1
	}
	return d
}

func (d *differ) match(i, j int) {
	d.ma[i] = j
	d.mb[j] = i
}

// run ищет совпадения между a[a0:a1] и b[b0:b1].
func (d *differ) run(a0, a1, b0, b1 int) {
	for a0 < a1 && b0 < b1 && d.a[a0] == d.b[b0] {
		d.match(a0, b0)
		a0++
		b0++
	}
	for a0 < a1 && b0 < b1 && d.a[a1-1] == d.b[b1-1] {
		a1--
		b1--
		d.match(a1, b1)
	}
	if a0 == a1 || b0 == b1 || d.budget <= 0 {
		return
	}
	if d.patience && d.anchors(a0, a1, b0, b1) {
		return
	}
	x, y, ok := d.bisect(a0, a1, b0, b1)
	if !ok || (x == a0 && y == b0) || (x == a1 && y == b1) {
		return // общего нет или не хватило шагов: участок переписан целиком
	}
	d.run(a0, x, b0, y)
	d.run(x, a1, y, b1)
}

// bisect находит точку, через которую идёт кратчайший путь правок,
// одновременно с начала и с конца.
func (d *differ) bisect(a0, a1, b0, b1 int) (int, int, bool) {
	n, m := a1-a0, b1-b0
	maxD := (n + m + 1) / 2
	off := maxD
	size := 2*maxD + 2
	v1 := make([]int, size)
	v2 := make([]int, size)
	for i := range v1 {
		v1[i] = -1
		v2[i] = -1
	}
	v1[off+1] = 0
	v2[off+1] = 0
	delta := n - m
	front := delta%2 != 0
	k1s, k1e, k2s, k2e := 0, 0, 0, 0
	for dd := 0; dd < maxD; dd++ {
		if d.budget <= 0 {
			return 0, 0, false
		}
		for k1 := -dd + k1s; k1 <= dd-k1e; k1 += 2 {
			ko := off + k1
			var x1 int
			if k1 == -dd || (k1 != dd && v1[ko-1] < v1[ko+1]) {
				x1 = v1[ko+1]
			} else {
				x1 = v1[ko-1] + 1
			}
			y1 := x1 - k1
			s := x1
			for x1 < n && y1 < m && d.a[a0+x1] == d.b[b0+y1] {
				x1++
				y1++
			}
			d.budget -= 1 + x1 - s
			v1[ko] = x1
			switch {
			case x1 > n:
				k1e += 2
			case y1 > m:
				k1s += 2
			case front:
				k2o := off + delta - k1
				if k2o >= 0 && k2o < size && v2[k2o] != -1 && x1 >= n-v2[k2o] {
					return a0 + x1, b0 + y1, true
				}
			}
		}
		for k2 := -dd + k2s; k2 <= dd-k2e; k2 += 2 {
			ko := off + k2
			var x2 int
			if k2 == -dd || (k2 != dd && v2[ko-1] < v2[ko+1]) {
				x2 = v2[ko+1]
			} else {
				x2 = v2[ko-1] + 1
			}
			y2 := x2 - k2
			s := x2
			for x2 < n && y2 < m && d.a[a1-x2-1] == d.b[b1-y2-1] {
				x2++
				y2++
			}
			d.budget -= 1 + x2 - s
			v2[ko] = x2
			switch {
			case x2 > n:
				k2e += 2
			case y2 > m:
				k2s += 2
			case !front:
				k1o := off + delta - k2
				if k1o >= 0 && k1o < size && v1[k1o] != -1 {
					x1 := v1[k1o]
					y1 := off + x1 - k1o
					if x1 >= n-x2 {
						return a0 + x1, b0 + y1, true
					}
				}
			}
		}
	}
	return 0, 0, false
}

// anchors выравнивает a[a0:a1] и b[b0:b1] по опорам и ищет совпадения между ними.
// false — опор нет.
func (d *differ) anchors(a0, a1, b0, b1 int) bool {
	type cnt struct{ na, nb, jb int }
	cs := make(map[int]cnt, a1-a0)
	for i := a0; i < a1; i++ {
		c := cs[d.a[i]]
		c.na++
		cs[d.a[i]] = c
	}
	for j := b0; j < b1; j++ {
		if c, ok := cs[d.b[j]]; ok {
			c.nb++
			c.jb = j
			cs[d.b[j]] = c
		}
	}
	d.budget -= (a1 - a0) + (b1 - b0)
	type pair struct{ i, j int }
	var ps []pair
	for i := a0; i < a1; i++ {
		if c := cs[d.a[i]]; c.na == 1 && c.nb == 1 {
			ps = append(ps, pair{i, c.jb})
		}
	}
	if len(ps) == 0 {
		return false
	}
	// Наибольшая возрастающая по j цепочка опор (раскладка пасьянсом).
	tops := []int{} // индексы в ps: конец лучшей цепочки каждой длины
	prev := make([]int, len(ps))
	for k, p := range ps {
		lo, hi := 0, len(tops)
		for lo < hi {
			mid := (lo + hi) / 2
			if ps[tops[mid]].j < p.j {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		prev[k] = -1
		if lo > 0 {
			prev[k] = tops[lo-1]
		}
		if lo == len(tops) {
			tops = append(tops, k)
		} else {
			tops[lo] = k
		}
	}
	chain := make([]pair, len(tops))
	for k, n := tops[len(tops)-1], len(tops)-1; k >= 0; k, n = prev[k], n-1 {
		chain[n] = ps[k]
	}
	pi, pj := a0, b0
	for _, p := range chain {
		d.run(pi, p.i, pj, p.j)
		d.match(p.i, p.j)
		pi, pj = p.i+1, p.j+1
	}
	d.run(pi, a1, pj, b1)
	return true
}
