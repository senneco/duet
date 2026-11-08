// Правка владельца: окно с исходником текущей версии. Здесь — то, что не зависит от браузера:
// подсветка исходника по строкам, место и выделение из окна чтения, отступ для Tab.

import { esc, paint } from './util.js';
import { tokenize } from './syntax.js';
import { target, byteToChar, lineStarts, oldToNew } from './anchors.js';

// editLinesHTML — исходник по строкам с подсветкой синтаксиса. Строк ровно столько, сколько
// у текста в textarea (последний перевод строки даёт пустую строку), чтобы переносы совпадали.
export function editLinesHTML(text, lang) {
  const ls = text.split('\n');
  const toks = lang ? tokenize(text, lang) : null;
  return ls.map((l, k) => `<div class="el">${(toks ? paint(l, toks[k], [], '') : esc(l)) || '​'}</div>`).join('');
}

// editSelection — что было выделено в окне чтения, как начало и конец в символах текущей версии.
// Выделение только в удалённом тексте — точка, где этот текст был. null — выделения нет.
export function editSelection(sel, base, current, chunks) {
  if (!sel || !sel.a || !sel.b) return null;
  const tg = target(sel.a, sel.b, base || '', current, chunks || []);
  if (tg && tg.range) return [byteToChar(current, tg.range.start), byteToChar(current, tg.range.end)];
  if (tg && tg.was !== undefined) {
    const i = byteToChar(current, tg.at);
    return [i, i];
  }
  // Выделение без текста (одни пробелы): курсор в начало строки, где оно начиналось.
  const p = sel.a.side === 'n' ? sel.a : null;
  const starts = lineStarts(current);
  if (p) {
    const i = Math.min(starts[Math.min(p.line, starts.length - 1)] + p.col, current.length);
    return [i, i];
  }
  const m = oldToNew(chunks || [], sel.a.line, current);
  return [m.at, m.at];
}

// lineOf — строка (с 0), в которой стоит символ i.
export function lineOf(text, i) {
  let n = 0;
  for (let k = 0; k < i && k < text.length; k++) if (text.charCodeAt(k) === 10) n++;
  return n;
}

// indentUnit — чем отступает Tab: табуляцией, если файл ей отступает, иначе пробелами, как в файле.
export function indentUnit(text, kind) {
  let tabs = 0, spaces = 0, min = 0;
  for (const l of text.split('\n')) {
    if (l.startsWith('\t')) tabs++;
    else {
      const m = /^( +)\S/.exec(l);
      if (m) { spaces++; if (!min || m[1].length < min) min = m[1].length; }
    }
  }
  if (tabs > spaces) return '\t';
  if (min === 2 || min === 4) return ' '.repeat(min);
  return kind === 'code' ? '    ' : '  ';
}
