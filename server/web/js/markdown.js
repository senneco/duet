// Markdown для режима «документ»: разбор на блоки и отрисовка с подсветкой правок внутри текста.
// Блок — заголовок, абзац, пункт списка, блок кода, таблица, цитата, черта. Отличия ядра приходят по
// строкам, поэтому каждый блок помнит свои строки, а подсветка правок переносится из строк в текст.

import { esc, inRanges, paint } from './util.js';
import { langOf, tokenize } from './syntax.js';

const FENCE = /^(\s*)(`{3,}|~{3,})\s*([^`\s]*)/;
const HEADING = /^\s{0,3}(#{1,6})(\s+|$)/;
const HR = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
const QUOTE = /^\s{0,3}>/;
const ITEM = /^(\s*)([-*+]|\d{1,9}[.)])(\s+|$)/;
const TABLE_SEP = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;
const SETEXT = /^\s{0,3}(=+|-+)\s*$/;

const blank = (l) => /^\s*$/.test(l);

function startsBlock(lines, i) {
  const l = lines[i];
  return FENCE.test(l) || HEADING.test(l) || HR.test(l) || QUOTE.test(l) || ITEM.test(l) || isTable(lines, i);
}

function isTable(lines, i) {
  return lines[i].includes('|') && i + 1 < lines.length && lines[i + 1].includes('-') && TABLE_SEP.test(lines[i + 1]);
}

// parseBlocks — блоки текста. start, end — номера строк (с 0), end не входит.
export function parseBlocks(lines) {
  const out = [];
  const n = lines.length;
  let i = 0;
  // Шапка YAML в начале файла.
  if (n > 1 && /^---\s*$/.test(lines[0])) {
    const j = lines.findIndex((l, k) => k > 0 && /^(---|\.\.\.)\s*$/.test(l));
    if (j > 0) {
      out.push({ type: 'code', start: 0, end: j + 1, lang: 'yaml', inner: [1, j] });
      i = j + 1;
    }
  }
  while (i < n) {
    const l = lines[i];
    if (blank(l)) { i++; continue; }
    let m;
    if ((m = FENCE.exec(l))) {
      const mark = m[2];
      let j = i + 1;
      while (j < n && !new RegExp(`^\\s*${mark[0] === '`' ? '`' : '~'}{${mark.length},}\\s*$`).test(lines[j])) j++;
      const closed = j < n;
      out.push({ type: 'code', start: i, end: closed ? j + 1 : n, lang: m[3], inner: [i + 1, closed ? j : n] });
      i = closed ? j + 1 : n;
      continue;
    }
    if ((m = HEADING.exec(l))) { out.push({ type: 'heading', level: m[1].length, start: i, end: i + 1 }); i++; continue; }
    if (HR.test(l)) { out.push({ type: 'hr', start: i, end: i + 1 }); i++; continue; }
    if (isTable(lines, i)) {
      let j = i + 2;
      while (j < n && !blank(lines[j]) && lines[j].includes('|')) j++;
      out.push({ type: 'table', start: i, end: j });
      i = j;
      continue;
    }
    if (QUOTE.test(l)) {
      let j = i + 1;
      while (j < n && QUOTE.test(lines[j])) j++;
      out.push({ type: 'quote', start: i, end: j });
      i = j;
      continue;
    }
    if ((m = ITEM.exec(l))) {
      let j = i + 1;
      while (j < n && !blank(lines[j]) && !startsBlock(lines, j)) j++;
      const marker = m[2];
      out.push({ type: 'item', start: i, end: j, indent: m[1].replace(/\t/g, '    ').length, ordered: /\d/.test(marker), marker });
      i = j;
      continue;
    }
    let j = i + 1;
    while (j < n && !blank(lines[j]) && !startsBlock(lines, j) && !SETEXT.test(lines[j])) j++;
    if (j < n && SETEXT.test(lines[j]) && !blank(lines[j])) {
      out.push({ type: 'heading', level: lines[j].trim()[0] === '=' ? 1 : 2, start: i, end: j + 1, setext: true });
      i = j + 1;
      continue;
    }
    out.push({ type: 'para', start: i, end: j });
    i = j;
  }
  return out;
}

// ── отрисовка ──

// Source — текст блока, собранный из кусков строк, и где в нём каждая строка.
class Source {
  constructor(rangesOf) {
    this.text = '';
    this.rs = [];
    this.rangesOf = rangesOf;
  }
  // add — кусок строки line с позиции from (до to). Между строками — перевод строки,
  // он подсвечен, если подсвечены обе стороны: так подсветка абзаца без дыр.
  add(line, lineText, from, to = lineText.length, sep = '\n') {
    if (this.text) {
      const prev = this.rs.length && this.rs[this.rs.length - 1][1] === this.text.length;
      this.text += sep;
      if (prev && inRanges(this.rangesOf(line), from)) this.rs[this.rs.length - 1][1] = this.text.length;
    }
    const at = this.text.length;
    for (const [a, b] of this.rangesOf(line)) {
      const x = Math.max(a, from), y = Math.min(b, to);
      if (y > x) {
        const s = at + x - from, e = at + y - from;
        const last = this.rs[this.rs.length - 1];
        if (last && last[1] === s) last[1] = e;
        else this.rs.push([s, e]);
      }
    }
    this.text += lineText.slice(from, to);
  }
}

// renderBlock — блок как HTML. rangesOf(i) — подсвеченные места строки i, cls — класс подсветки.
export function renderBlock(b, lines, rangesOf = () => [], cls = '') {
  const src = new Source(rangesOf);
  switch (b.type) {
    case 'heading': {
      if (b.setext) {
        for (let i = b.start; i < b.end - 1; i++) src.add(i, lines[i], lines[i].length - lines[i].trimStart().length);
      } else {
        const l = lines[b.start];
        const m = HEADING.exec(l);
        const tail = /\s+#+\s*$/.exec(l);
        src.add(b.start, l, m[0].length, tail && tail.index >= m[0].length ? tail.index : l.length);
      }
      return `<h${b.level}>${inline(src, cls)}</h${b.level}>`;
    }
    case 'para':
      for (let i = b.start; i < b.end; i++) src.add(i, lines[i], lines[i].length - lines[i].trimStart().length);
      return `<p>${inline(src, cls)}</p>`;
    case 'quote':
      for (let i = b.start; i < b.end; i++) {
        const m = /^\s{0,3}>\s?/.exec(lines[i]);
        src.add(i, lines[i], m[0].length);
      }
      return `<blockquote><p>${inline(src, cls)}</p></blockquote>`;
    case 'item': {
      const l = lines[b.start];
      const m = ITEM.exec(l);
      let from = m[0].length;
      let box = '';
      const task = /^\[([ xX])\]\s+/.exec(l.slice(from));
      if (task) {
        box = `<span class="box">${task[1] === ' ' ? '☐' : '☑'}</span>`;
        from += task[0].length;
      }
      src.add(b.start, l, from);
      for (let i = b.start + 1; i < b.end; i++) src.add(i, lines[i], lines[i].length - lines[i].trimStart().length);
      const lvl = Math.min(6, Math.floor(b.indent / 2));
      const mark = b.ordered ? `<span class="lm n">${esc(b.marker)}</span>` : '<span class="lm"></span>';
      return `<div class="li" style="--lvl:${lvl}">${mark}<div class="lc">${box}${inline(src, cls)}</div></div>`;
    }
    case 'hr':
      return '<hr>';
    case 'table':
      return table(b, lines, rangesOf, cls);
    case 'code':
      return `<pre class="cb">${codeLines(b, lines, rangesOf, cls)}</pre>`;
  }
  return '';
}

// codeLineList — строки блока кода с подсветкой синтаксиса и правок, по одной.
export function codeLineList(b, lines, rangesOf, cls) {
  const [a, z] = b.inner;
  const body = lines.slice(a, z);
  const toks = tokenize(body.join('\n'), langOf(b.lang));
  return body.map((l, k) => `<span class="cl">${paint(l, toks && toks[k], rangesOf(a + k), cls) || '​'}</span>`);
}

export const codeLines = (b, lines, rangesOf, cls) => codeLineList(b, lines, rangesOf, cls).join('');

function cells(line) {
  // Границы ячеек: «|» вне `кода` и не экранированные.
  const out = [];
  let start = 0;
  let code = false;
  const s = line;
  let i = 0;
  const lead = /^\s*\|?/.exec(s)[0].length;
  start = lead;
  for (i = lead; i < s.length; i++) {
    if (s[i] === '\\') { i++; continue; }
    if (s[i] === '`') code = !code;
    if (s[i] === '|' && !code) { out.push([start, i]); start = i + 1; }
  }
  if (s.slice(start).trim()) out.push([start, s.length]);
  return out.map(([a, z]) => {
    while (a < z && /\s/.test(s[a])) a++;
    while (z > a && /\s/.test(s[z - 1])) z--;
    return [a, z];
  });
}

function table(b, lines, rangesOf, cls) {
  const align = cells(lines[b.start + 1]).map(([a, z]) => {
    const t = lines[b.start + 1].slice(a, z);
    return t.startsWith(':') && t.endsWith(':') ? 'center' : t.endsWith(':') ? 'right' : '';
  });
  const row = (i, tag) => {
    const cs = cells(lines[i]);
    return '<tr>' + cs.map(([a, z], k) => {
      const src = new Source(rangesOf);
      src.add(i, lines[i], a, z);
      const al = align[k] ? ` style="text-align:${align[k]}"` : '';
      return `<${tag}${al}>${inline(src, cls)}</${tag}>`;
    }).join('') + '</tr>';
  };
  let h = '<table><thead>' + row(b.start, 'th') + '</thead><tbody>';
  for (let i = b.start + 2; i < b.end; i++) h += row(i, 'td');
  return h + '</tbody></table>';
}

// ── текст внутри блока ──

// inline — разметка внутри текста (код, жирный, курсив, ссылки) и подсветка правок поверх.
// Подсветка оборачивает только текст и не пересекает теги, поэтому HTML всегда правильный.
function inline(src, cls) {
  const toks = [];
  parse(src.text, 0, src.text.length, toks);
  let out = '';
  for (const t of toks) {
    if (t.h !== undefined) { out += t.h; continue; }
    let i = 0;
    while (i < t.s.length) {
      const on = cls && inRanges(src.rs, t.at + i);
      let j = i + 1;
      while (j < t.s.length && (cls && inRanges(src.rs, t.at + j)) === on) j++;
      const s = esc(t.s.slice(i, j));
      out += on ? `<mark class="${cls}">${s}</mark>` : s;
      i = j;
    }
  }
  return out;
}

const PUNCT = /[!-/:-@[-`{-~]/;
const word = (c) => !!c && /[\p{L}\p{N}]/u.test(c);

function parse(s, from, to, out) {
  let i = from;
  const text = (str, at) => {
    const last = out[out.length - 1];
    if (last && last.s !== undefined && last.at + last.s.length === at) last.s += str;
    else out.push({ s: str, at });
  };
  const tag = (h) => out.push({ h });
  while (i < to) {
    const c = s[i];
    if (c === '\\' && i + 1 < to && PUNCT.test(s[i + 1])) { text(s[i + 1], i + 1); i += 2; continue; }
    if (c === '`') {
      let k = i;
      while (k < to && s[k] === '`') k++;
      const run = k - i;
      const close = s.indexOf('`'.repeat(run), k);
      if (close >= 0 && close + run <= to && s[close + run] !== '`') {
        tag('<code>');
        text(s.slice(k, close), k);
        tag('</code>');
        i = close + run;
        continue;
      }
      text(s.slice(i, k), i);
      i = k;
      continue;
    }
    const two = s.slice(i, i + 2);
    if ((two === '**' || two === '__' || two === '~~') && !(two === '__' && word(s[i - 1]))) {
      const close = s.indexOf(two, i + 2);
      if (close > i + 2 && close + 2 <= to && !/\s/.test(s[i + 2]) && !/\s/.test(s[close - 1])) {
        const t = two === '~~' ? 's' : 'strong';
        tag(`<${t}>`);
        parse(s, i + 2, close, out);
        tag(`</${t}>`);
        i = close + 2;
        continue;
      }
    }
    if ((c === '*' || c === '_') && s[i + 1] !== c && !/\s/.test(s[i + 1] || ' ') && !(c === '_' && word(s[i - 1]))) {
      let j = i + 1;
      while (j < to) {
        j = s.indexOf(c, j);
        if (j < 0 || j >= to) { j = -1; break; }
        if (s[j + 1] !== c && s[j - 1] !== c && !/\s/.test(s[j - 1]) && !(c === '_' && word(s[j + 1]))) break;
        j++;
      }
      if (j > i + 1) {
        tag('<em>');
        parse(s, i + 1, j, out);
        tag('</em>');
        i = j + 1;
        continue;
      }
    }
    if (c === '[' || (c === '!' && s[i + 1] === '[')) {
      const img = c === '!';
      const lb = img ? i + 1 : i;
      const rb = s.indexOf(']', lb + 1);
      if (rb > 0 && rb < to && s[rb + 1] === '(') {
        const rp = s.indexOf(')', rb + 2);
        if (rp > 0 && rp < to) {
          const url = s.slice(rb + 2, rp).trim().split(/\s+/)[0];
          if (img) {
            tag('<span class="img">');
            parse(s, lb + 1, rb, out);
            tag('</span>');
          } else {
            tag(`<a${safeHref(url)} target="_blank" rel="noopener noreferrer">`);
            parse(s, lb + 1, rb, out);
            tag('</a>');
          }
          i = rp + 1;
          continue;
        }
      }
    }
    if ((c === 'h' || c === '<') && !word(s[i - 1])) {
      const m = /^<?(https?:\/\/[^\s<>]*[^\s<>.,;:!?)\]'"])>?/.exec(s.slice(i, to));
      if (m) {
        tag(`<a${safeHref(m[1])} target="_blank" rel="noopener noreferrer">`);
        text(m[0], i);
        tag('</a>');
        i += m[0].length;
        continue;
      }
    }
    text(c, i);
    i++;
  }
}

function safeHref(url) {
  if (/^(https?:|mailto:)/i.test(url)) return ` href="${esc(url)}"`;
  return '';
}
