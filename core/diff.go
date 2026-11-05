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

// diffLines — отличия по строкам (алгоритм Майерса).
func diffLines(a, b []string) []hunk {
	// Общие начало и конец отрезаем сразу: обычно правка маленькая.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	x := a[pre : len(a)-suf]
	y := b[pre : len(b)-suf]
	n, m := len(x), len(y)
	if n == 0 && m == 0 {
		return nil
	}
	max := n + m
	off := max
	v := make([]int, 2*max+2)
	var trace [][]int
	d := 0
loop:
	for ; d <= max; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var i int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				i = v[off+k+1]
			} else {
				i = v[off+k-1] + 1
			}
			j := i - k
			for i < n && j < m && x[i] == y[j] {
				i++
				j++
			}
			v[off+k] = i
			if i >= n && j >= m {
				break loop
			}
		}
	}
	// Обратный проход: какие строки совпали.
	type pair struct{ i, j int }
	var eq []pair
	i, j := n, m
	for dd := d; dd >= 0; dd-- {
		vv := trace[dd]
		k := i - j
		var pk int
		if k == -dd || (k != dd && vv[off+k-1] < vv[off+k+1]) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		pi := vv[off+pk]
		pj := pi - pk
		for i > pi && j > pj && i > 0 && j > 0 {
			i--
			j--
			eq = append(eq, pair{i, j})
		}
		if dd > 0 {
			i, j = pi, pj
		}
	}
	// eq идёт с конца; собираем изменённые участки между совпадениями.
	var hs []hunk
	ai, bj := 0, 0
	for p := len(eq) - 1; p >= -1; p-- {
		ei, ej := n, m
		if p >= 0 {
			ei, ej = eq[p].i, eq[p].j
		}
		if ei > ai || ej > bj {
			hs = append(hs, hunk{ai + pre, ei + pre, bj + pre, ej + pre})
		}
		ai, bj = ei+1, ej+1
	}
	return hs
}
