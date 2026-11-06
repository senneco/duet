// Места в тексте файла. Отрисовка помечает каждый кусок текста местом в исходнике (data-p: сторона
// o — база или n — текущая, строка с 0, символ строки). Здесь эти места переводятся в байты текущей
// версии, как их хранит ядро, и обратно. Ничего не зависит от браузера — проверяется тестами.

// utf8 — длина строки в байтах UTF-8.
export function utf8(s) {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c < 0xdc00) { n += 4; i++; }
    else n += 3;
  }
  return n;
}

// byteToChar — байт текста → индекс символа (UTF-16).
export function byteToChar(text, b) {
  let bytes = 0;
  for (let i = 0; i < text.length; i++) {
    if (bytes >= b) return i;
    const c = text.charCodeAt(i);
    if (c < 0x80) bytes += 1;
    else if (c < 0x800) bytes += 2;
    else if (c >= 0xd800 && c < 0xdc00) { bytes += 4; i++; }
    else bytes += 3;
  }
  return text.length;
}

export const charToByte = (text, i) => utf8(text.slice(0, i));

// lineStarts — где в тексте начинается каждая строка.
export function lineStarts(text) {
  const out = [0];
  for (let i = 0; i < text.length; i++) if (text.charCodeAt(i) === 10) out.push(i + 1);
  return out;
}

// parseP — «n12.5» → {side: 'n', line: 12, col: 5}.
export function parseP(p) {
  const m = /^([on])(\d+)\.(\d+)$/.exec(p || '');
  return m ? { side: m[1], line: +m[2], col: +m[3] } : null;
}

// posToChar — место (строка, символ) → индекс в тексте; символ не дальше конца строки.
export function posToChar(text, starts, line, col) {
  if (line >= starts.length) return text.length;
  const s = starts[line];
  const e = line + 1 < starts.length ? starts[line + 1] - 1 : text.length;
  return Math.min(s + col, e);
}

// charToPos — индекс в тексте → место (строка, символ).
export function charToPos(starts, i) {
  let lo = 0, hi = starts.length - 1;
  while (lo < hi) {
    const m = (lo + hi + 1) >> 1;
    if (starts[m] <= i) lo = m; else hi = m - 1;
  }
  return { line: lo, col: i - starts[lo] };
}

// oldToNew — где в текущей версии то, что было в строке базы oldLine (с 0). Неизменённая строка —
// та же строка; изменённая — начало строки, которая её заменила; удалённая — начало следующей
// строки текущей версии (или конец текста). same — строка не менялась.
export function oldToNew(chunks, oldLine, current) {
  const starts = lineStarts(current);
  const n1 = oldLine + 1;
  for (let ci = 0; ci < chunks.length; ci++) {
    const c = chunks[ci];
    const k = (c.old || []).findIndex((l) => l.n === n1);
    if (k < 0) continue;
    if (c.kind === 'same') return { at: starts[c.new[k].n - 1], same: true, line: c.new[k].n - 1 };
    if (c.new && c.new.length) {
      const l = c.new[Math.min(k, c.new.length - 1)].n - 1;
      return { at: starts[l], same: false, line: l };
    }
    for (let j = ci + 1; j < chunks.length; j++) {
      const nx = chunks[j].new || [];
      if (nx.length) return { at: starts[nx[0].n - 1], same: false, line: nx[0].n - 1 };
    }
    return { at: current.length, same: false, line: starts.length - 1 };
  }
  return { at: current.length, same: false, line: starts.length - 1 };
}

// target — что выделил владелец: a, b — начало и конец выделения ({side, line, col}, a раньше b).
// Ответ: {range: {start, end}} — место в текущей версии (байты) или {at, was} — комментарий
// к удалённому тексту: точка в текущей версии, где он был, и сам этот текст. null — нечего комментировать.
export function target(a, b, base, current, chunks) {
  if (!a || !b) return null;
  const cs = lineStarts(current);
  const bs = lineStarts(base || '');
  // Неизменённый текст слева в двух колонках — это тот же текст справа.
  const toNew = (p) => {
    if (p.side === 'n') return p;
    const m = oldToNew(chunks, p.line, current);
    return m.same ? { side: 'n', line: m.line, col: p.col } : null;
  };
  const na = toNew(a), nb = toNew(b);
  if (na && nb) {
    const s = posToChar(current, cs, na.line, na.col);
    const e = posToChar(current, cs, nb.line, nb.col);
    if (e <= s || !current.slice(s, e).trim()) return null;
    return { range: { start: charToByte(current, s), end: charToByte(current, e) } };
  }
  if (a.side === 'o' && b.side === 'o') {
    const s = posToChar(base, bs, a.line, a.col);
    const e = posToChar(base, bs, b.line, b.col);
    const was = base.slice(s, e);
    if (!was.trim()) return null;
    return { at: charToByte(current, oldToNew(chunks, a.line, current).at), was };
  }
  // Выделение захватило и удалённое, и новое: комментарий к новой части.
  const s = na ? posToChar(current, cs, na.line, na.col) : oldToNew(chunks, a.line, current).at;
  const e = nb ? posToChar(current, cs, nb.line, nb.col) : oldToNew(chunks, b.line, current).at;
  if (e <= s || !current.slice(s, e).trim()) return null;
  return { range: { start: charToByte(current, s), end: charToByte(current, e) } };
}

// commentOrder — комментарии по месту: ко всему файлу сначала, потом по началу места.
export function commentOrder(list) {
  return [...list].sort((x, y) => (x.whole === y.whole ? (x.whole ? 0 : x.range.start - y.range.start || x.range.end - y.range.end) : x.whole ? -1 : 1));
}
