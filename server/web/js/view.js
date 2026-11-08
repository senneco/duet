// Отрисовка отличий, которые посчитало ядро: код (одна и две колонки, свёрнутые куски) и документ
// (markdown по блокам). Здесь ничего не пересчитывается: только раскладка кусков ядра в HTML.
// Правило подсветки: светлый фон собирает правку в блок, тёмным подсвечен текст, которого не было
// (или который убрали). Строки, новые или удалённые целиком, подсвечены целиком.

import { esc, ranges, wholeRange, paint, splitLines } from './util.js';
import { parseBlocks, renderBlock, codeLineList } from './markdown.js';
import { tokenize } from './syntax.js';

const CTX = 3; // строк вокруг правки в коде
const MIN_FOLD = 4; // меньше — не сворачиваем

// lineMaps — для каждой строки обеих версий: вид куска, подсвеченные места, номер куска.
export function lineMaps(chunks) {
  const o = new Map();
  const n = new Map();
  const o2n = new Map();
  chunks.forEach((c, ci) => {
    const rs = (l) => (c.kind === 'same' ? [] : c.kind === 'changed' ? ranges(l.text, l.marks) : wholeRange(l.text));
    (c.old || []).forEach((l, k) => o.set(l.n, { kind: c.kind, rs: rs(l), chunk: ci, k }));
    (c.new || []).forEach((l, k) => n.set(l.n, { kind: c.kind, rs: rs(l), chunk: ci, k }));
    if (c.kind === 'same') (c.old || []).forEach((l, k) => o2n.set(l.n, c.new[k].n));
  });
  return { o, n, o2n };
}

export const hasChanges = (chunks) => chunks.some((c) => c.kind !== 'same');

// ── код ──

// renderCode — исходник с отличиями. opts: cols (1|2), lang, oldText, newText, fold, expanded (Set
// номеров кусков, которые владелец развернул), labels {base, current} для шапки двух колонок.
export function renderCode(chunks, opts) {
  const { cols = 1, lang = '', fold = true, expanded = new Set() } = opts;
  const ot = tokenize(opts.oldText || '', lang);
  const nt = tokenize(opts.newText || '', lang);
  const folding = fold && hasChanges(chunks);
  const side = (l, toks, rs, cls) => paint(l.text, toks && toks[l.n - 1], rs, cls) || '​';
  let h = '';
  let inChange = false;
  // data-p — место текста строки в исходнике (сторона, строка с 0, символ): по нему выделение
  // переводится в место в файле.
  const row1 = (cls, o, n, sg, html, first) =>
    `<div class="ln ${cls}${first ? ' cg' : ''}"${n ? ` data-n="${n}"` : ''}${o ? ` data-o="${o}"` : ''}><span class="no">${o || ''}</span><span class="no">${n || ''}</span><span class="sg">${sg}</span><span class="tx" data-p="${n ? 'n' + (n - 1) : 'o' + (o - 1)}.0">${html}</span></div>`;
  const cell = (no, html, cls, r2) => `<span class="no${r2 ? ' r2' : ''}">${no || ''}</span><span class="tx ${cls}" data-p="${r2 ? 'n' : 'o'}${no - 1}.0">${html}</span>`;
  const hatch = (r2) => `<span class="no${r2 ? ' r2' : ''} hatch"></span><span class="tx hatch"></span>`;
  const row2 = (left, right, n, o, cls) => `<div class="rr${cls ? ' ' + cls : ''}"${n ? ` data-n="${n}"` : ''}${o ? ` data-o="${o}"` : ''}>${left}${right}</div>`;

  chunks.forEach((c, ci) => {
    if (c.kind === 'same') {
      inChange = false;
      const L = c.new.length;
      const head = ci === 0 ? 0 : CTX;
      const tail = ci === chunks.length - 1 ? 0 : CTX;
      const hidden = L - head - tail;
      const show = (k) => {
        const o = c.old[k], n = c.new[k];
        if (cols === 2) h += row2(cell(o.n, side(o, ot, [], ''), ''), cell(n.n, side(n, nt, [], ''), '', true), n.n, o.n);
        else h += row1('', o.n, n.n, '', side(n, nt, [], ''));
      };
      if (folding && hidden >= MIN_FOLD && !expanded.has(ci)) {
        for (let k = 0; k < head; k++) show(k);
        const o0 = c.old[head].n, n0 = c.new[head].n;
        const label = opts.foldText ? opts.foldText(hidden) : String(hidden);
        h += `<div class="fold" data-fold="${ci}"><span class="ar">▸</span> <span class="fl">${esc(label)}</span> <span class="rg">${o0}–${o0 + hidden - 1} → ${n0}–${n0 + hidden - 1}</span></div>`;
        for (let k = L - tail; k < L; k++) show(k);
      } else {
        for (let k = 0; k < L; k++) show(k);
      }
      return;
    }
    const first = !inChange;
    inChange = true;
    const olds = c.old || [];
    const news = c.new || [];
    const ors = (l) => (c.kind === 'changed' ? ranges(l.text, l.marks) : wholeRange(l.text));
    if (cols === 2) {
      const m = Math.max(olds.length, news.length);
      for (let k = 0; k < m; k++) {
        const o = olds[k], n = news[k];
        const left = o ? cell(o.n, side(o, ot, ors(o), 'wd'), 'del') : hatch(false);
        const right = n ? cell(n.n, side(n, nt, ors(n), 'wa'), 'add', true) : hatch(true);
        h += row2(left, right, n && n.n, o && o.n, first && k === 0 ? 'cg' : '');
      }
    } else {
      olds.forEach((o, k) => { h += row1('del', o.n, 0, '−', side(o, ot, ors(o), 'wd'), first && k === 0); });
      news.forEach((n, k) => { h += row1('add', 0, n.n, '+', side(n, nt, ors(n), 'wa'), first && !olds.length && k === 0); });
    }
  });
  if (cols === 2) {
    return `<div class="code2"><div class="hd"><div>${opts.labels?.base || ''}</div><div>${opts.labels?.current || ''}</div></div>${h}</div>`;
  }
  return `<div class="code">${h}</div>`;
}

// renderLines — файл целиком без отличий: новый файл, удалённый, текст без правок.
export function renderLines(text, lang, side = 'n') {
  const ls = splitLines(text);
  const toks = tokenize(text, lang);
  let h = '';
  ls.forEach((l, k) => {
    h += `<div class="ln" data-n="${k + 1}"><span class="no"></span><span class="no">${k + 1}</span><span class="sg"></span><span class="tx"${side ? ` data-p="${side}${k}.0"` : ''}>${paint(l, toks && toks[k], [], '') || '​'}</span></div>`;
  });
  return `<div class="code plain">${h}</div>`;
}

// ── документ ──

// docUnits — раскладка документа на единицы показа. Единица — блоки старой и новой версии,
// связанные общими строками; если её касается правка — это изменённая единица (было / стало).
export function docUnits(chunks, oldText, newText) {
  const ol = splitLines(oldText);
  const nl = splitLines(newText);
  const maps = lineMaps(chunks);
  const ob = parseBlocks(ol);
  const nb = parseBlocks(nl);
  const parent = new Map();
  const find = (x) => {
    while (parent.get(x) !== x) {
      parent.set(x, parent.get(parent.get(x)));
      x = parent.get(x);
    }
    return x;
  };
  const add = (x) => { if (!parent.has(x)) parent.set(x, x); };
  const union = (a, b) => { add(a); add(b); parent.set(find(a), find(b)); };

  // Участок правки — кусок ядра. Удалённое и добавленное рядом — один участок: это «было → стало».
  // Остальные соседние куски связывает только общий блок (например, абзац из нескольких строк).
  const seg = [];
  let s = -1;
  chunks.forEach((c, ci) => {
    if (c.kind === 'same') { seg[ci] = -1; return; }
    const prev = chunks[ci - 1];
    const pair = prev && ((prev.kind === 'deleted' && c.kind === 'added') || (prev.kind === 'added' && c.kind === 'deleted'));
    if (!pair) s++;
    seg[ci] = s;
  });
  const nBlockOf = new Array(nl.length).fill(-1);
  nb.forEach((b, k) => { for (let i = b.start; i < b.end; i++) nBlockOf[i] = k; });

  ob.forEach((b, k) => {
    add('o' + k);
    for (let i = b.start; i < b.end; i++) {
      const inf = maps.o.get(i + 1);
      if (!inf) continue;
      if (inf.kind !== 'same') union('o' + k, 's' + seg[inf.chunk]);
      else {
        const nk = nBlockOf[maps.o2n.get(i + 1) - 1];
        if (nk >= 0) union('o' + k, 'n' + nk);
      }
    }
  });
  nb.forEach((b, k) => {
    add('n' + k);
    for (let i = b.start; i < b.end; i++) {
      const inf = maps.n.get(i + 1);
      if (inf && inf.kind !== 'same') union('n' + k, 's' + seg[inf.chunk]);
    }
  });
  // Участки, где поменялись только пустые строки: блоков нет, но правку всё равно видно.
  chunks.forEach((c, ci) => { if (seg[ci] >= 0) add('s' + seg[ci]); });

  const order = (side, line) => {
    const inf = (side === 'o' ? maps.o : maps.n).get(line + 1);
    return inf ? inf.chunk * 1e6 + inf.k : Infinity;
  };
  const units = new Map();
  for (const x of parent.keys()) {
    const r = find(x);
    if (!units.has(r)) units.set(r, { old: [], new: [], segs: [], key: Infinity });
    const u = units.get(r);
    const k = +x.slice(1);
    if (x[0] === 'o') { u.old.push(ob[k]); u.key = Math.min(u.key, order('o', ob[k].start)); }
    else if (x[0] === 'n') { u.new.push(nb[k]); u.key = Math.min(u.key, order('n', nb[k].start)); }
    else {
      u.segs.push(k);
      const ci = seg.indexOf(k);
      u.key = Math.min(u.key, ci * 1e6);
    }
  }
  const list = [...units.values()];
  for (const u of list) {
    u.old.sort((a, b) => a.start - b.start);
    u.new.sort((a, b) => a.start - b.start);
    u.dirty = u.segs.length > 0;
    if (u.dirty && !u.old.length && !u.new.length) {
      // Только пустые строки.
      const ci = seg.indexOf(u.segs[0]);
      u.blank = { added: (chunks[ci].new || []).length, deleted: (chunks[ci].old || []).length };
    }
  }
  list.sort((a, b) => a.key - b.key);
  return { units: list, ol, nl, maps };
}

// renderDoc — документ с правками. opts: cols, labels {base, current}, words {was, now, added, removed,
// more, blank, gone(n)}, open — Set строк базы (data-o), у которых «как было» раскрыто.
//
// Одна колонка: видно только новое. Старая версия переписанного блока (.was.old) спрятана над ним,
// у нового блока (.chg.hasold) — отметка; удалённое без замены (.was.gone) свёрнуто в черту
// «удалено: N», соседние удалённые куски — одна черта. Раскрытое — .was.open. Подряд идущие новые куски — один блок: jn — слит со следующим,
// jp — с предыдущим. Блоки с «до» и удалённое ни с чем не сливаются.
// Две колонки: так же по каждой колонке (.c.jn / .c.jp), кроме строк «слева старое, справа новое».
export function renderDoc(chunks, oldText, newText, opts = {}) {
  const { cols = 1 } = opts;
  const w = opts.words || {};
  const open = opts.open || new Set();
  const { units, ol, nl, maps } = docUnits(chunks, oldText, newText);
  const oR = (i) => maps.o.get(i + 1)?.rs || [];
  const nR = (i) => maps.n.get(i + 1)?.rs || [];
  const blocks = (bs, lines, rangesOf, cls, side) =>
    bs.map((b) => `<div class="u" ${side === 'n' ? 'data-n' : 'data-o'}="${b.start + 1}">${renderBlock(b, lines, rangesOf, cls, side)}</div>`).join('');
  const oldMarked = (u) => u.old.some((b) => { for (let i = b.start; i < b.end; i++) if (oR(i).length || maps.o.get(i + 1)?.kind === 'deleted') return true; return false; });
  // Блок кода, в котором поменялись отдельные строки: показываем его один раз, построчно,
  // удалённые строки — над теми, что их заменили.
  const codeDiff = (u) => {
    const ob = u.old[0], nb = u.new[0];
    const oh = codeLineList(ob, ol, oR, 'wd', 'o');
    const nh = codeLineList(nb, nl, nR, 'wa', 'n');
    const inO = (n) => n - 1 >= ob.inner[0] && n - 1 < ob.inner[1];
    const inN = (n) => n - 1 >= nb.inner[0] && n - 1 < nb.inner[1];
    let h = '';
    for (const c of chunks) {
      if (c.kind !== 'same') for (const l of c.old || []) if (inO(l.n)) h += `<span class="cdel">${oh[l.n - 1 - ob.inner[0]]}</span>`;
      for (const l of c.new || []) {
        if (!inN(l.n)) continue;
        const x = nh[l.n - 1 - nb.inner[0]];
        h += c.kind === 'same' ? x : `<span class="cadd">${x}</span>`;
      }
    }
    return `<pre class="cb diff">${h}</pre>`;
  };
  const isCodeDiff = (u) => u.old.length === 1 && u.new.length === 1 && u.old[0].type === 'code' && u.new[0].type === 'code' && u.new[0].inner[1] - u.new[0].inner[0] > 6;

  // Сначала куски по порядку, потом слияние соседних и HTML.
  // Одна колонка: {k: plain | add | gone | old | hasold | other, at, n0, o0, html, s, gone}.
  // Две колонки: {at, l, r, cg} — l и r: вид подложки колонки (del | add | hatch | '') и HTML.
  const items = [];
  for (const u of units) {
    const n0 = u.new.length ? u.new[0].start + 1 : 0;
    const o0 = u.old.length ? u.old[0].start + 1 : 0;
    const at = `${n0 ? ` data-n="${n0}"` : ''}${o0 ? ` data-o="${o0}"` : ''}`;
    if (!u.dirty) {
      if (cols === 2) items.push({ at, l: ['', blocks(u.old, ol, oR, '', 'o')], r: ['', blocks(u.new, nl, nR, '', 'n')] });
      else items.push({ k: 'plain', html: blocks(u.new, nl, nR, '', 'n') });
      continue;
    }
    if (u.blank) {
      const t = u.blank.added ? w.blankAdded : w.blankRemoved;
      const cls = u.blank.added ? 'add' : 'del';
      if (cols === 2) items.push({ at, cg: true, l: ['', u.blank.added ? '<div class="b hatch ws"></div>' : `<div class="b del ws">${t}</div>`], r: ['', u.blank.added ? `<div class="b add ws">${t}</div>` : '<div class="b hatch ws"></div>'] });
      else items.push({ k: 'other', html: `<div class="chg ${cls} ws cg"${at}>${t}</div>` });
      continue;
    }
    const oldH = blocks(u.old, ol, oR, 'wd', 'o');
    const newH = blocks(u.new, nl, nR, 'wa', 'n');
    if (cols === 2) {
      items.push({ at, cg: true, l: u.old.length ? ['del', oldH] : ['hatch', ''], r: u.new.length ? ['add', newH] : ['hatch', ''] });
      continue;
    }
    if (isCodeDiff(u)) {
      items.push({ k: 'other', html: `<div class="chg mix cg"${at} data-s="${esc(w.changed || '')}">${codeDiff(u)}</div>` });
    } else if (!u.old.length) {
      items.push({ k: 'add', at, html: newH, s: w.added });
    } else if (!u.new.length) {
      const last = items[items.length - 1];
      if (last && last.k === 'gone') { last.html += oldH; last.gone += u.old.length; }
      else items.push({ k: 'gone', at, o0, html: oldH, s: w.removed, gone: u.old.length });
    } else if (!oldMarked(u)) {
      items.push({ k: 'add', at, html: newH, s: w.more });
    } else {
      items.push({ k: 'old', at, o0, html: oldH, s: w.was });
      items.push({ k: 'hasold', at: n0 ? ` data-n="${n0}"` : '', html: newH, s: w.now });
    }
  }

  let h = '';
  if (cols === 2) {
    const pair = (x) => x && x.l[0] === 'del' && x.r[0] === 'add';
    const joins = (x, y, side) => !!(x && y && x[side][0] && x[side][0] === y[side][0] && !pair(x) && !pair(y));
    items.forEach((x, i) => {
      const col = (side) => {
        const [k, html] = x[side];
        const j = (joins(x, items[i + 1], side) ? ' jn' : '') + (joins(items[i - 1], x, side) ? ' jp' : '');
        return `<div class="c${j}">${k ? `<div class="b ${k}">${html}</div>` : html}</div>`;
      };
      h += `<div class="r${x.cg ? ' cg' : ''}"${x.at}>${col('l')}${col('r')}</div>`;
    });
    return `<div class="two"><div class="hd"><div>${opts.labels?.base || ''}</div><div>${opts.labels?.current || ''}</div></div>${h}</div>`;
  }
  const joins = (x, y) => !!(x && y && x.k === 'add' && y.k === 'add');
  items.forEach((x, i) => {
    if (x.k === 'plain' || x.k === 'other') { h += x.html; return; }
    if (x.k === 'old' || x.k === 'gone') {
      // «Как было»: .was сворачивается целиком — .gx (сетка 0fr ↔ 1fr) прячет блок правки по высоте.
      const bar = x.k === 'gone' ? `<div class="gonebar">${esc(w.gone ? w.gone(x.gone) : String(x.gone))}</div>` : '';
      h += `<div class="was ${x.k} cg${open.has(x.o0) ? ' open' : ''}"${x.at} data-s="${esc(x.s || '')}">${bar}<div class="gx"><div class="gi"><div class="chg del">${x.html}</div></div></div></div>`;
      return;
    }
    const cls = ['chg', 'add'];
    if (x.k === 'hasold') cls.push('hasold'); else cls.push('cg');
    if (joins(x, items[i + 1])) cls.push('jn');
    if (joins(items[i - 1], x)) cls.push('jp');
    h += `<div class="${cls.join(' ')}"${x.at} data-s="${esc(x.s || '')}">${x.html}</div>`;
  });
  return `<article class="doc">${h}</article>`;
}

// renderDocPlain — документ целиком без отличий.
export function renderDocPlain(text, side = 'n') {
  const ls = splitLines(text);
  return `<article class="doc">${parseBlocks(ls).map((b) => `<div class="u" data-n="${b.start + 1}">${renderBlock(b, ls, () => [], '', side)}</div>`).join('')}</article>`;
}
