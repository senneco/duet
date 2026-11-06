// Комментарии на экране: карточки под текстом, окно комментария, отметки в тексте, своё выделение
// и правки в несколько строк одной фигурой. Всё, что решает поведение, делает ядро; здесь только вид.

import { esc } from './util.js';
import { parseP, byteToChar, lineStarts, charToPos } from './anchors.js';

const DOTS = '<span class="dots"><i></i><i></i><i></i><i></i><i></i><i></i></span>';

export const imageUrl = (p) => '/api/images/' + String(p).slice(String(p).lastIndexOf('/') + 1);

// Что с веткой: свой неотправленный комментарий, свой неотправленный ответ в ветке, вопрос агента.
export function threadState(c) {
  const th = c.thread || [];
  const last = th[th.length - 1] || {};
  const fresh = c.status === 'unsent' && th.length === 1;
  const draft = c.status === 'unsent' && th.length > 1 && last.author === 'owner';
  let ask = -1;
  for (let i = th.length - 1; i >= 0; i--) if (th[i].author === 'agent') { if (th[i].question) ask = i; break; }
  return { fresh, draft, ask, last };
}

const imgs = (m) => (m.images && m.images.length ? `<div class="mimg">${m.images.map((p) => `<a href="${esc(imageUrl(p))}" target="_blank"><img src="${esc(imageUrl(p))}" alt=""></a>`).join('')}</div>` : '');

// shownComments — какие комментарии файла на экране и где. resp — открыто окно ответа: комментарии
// на своих местах в версии из ответа (c.resp), включая написанные в окне. Иначе — без написанных
// в окне, а ответ агента из непросмотренного ответа (pending — его номер) спрятан: «откройте ответ».
export function shownComments(list, { resp, pending } = {}) {
  if (resp) return list.filter((c) => c.resp).map((c) => ({ ...c, ...c.resp }));
  return list.filter((c) => !c.inResponse).map((c) => (pending && (c.thread || []).some((m) => m.author === 'agent' && m.response === pending)
    ? { ...c, pend: true, thread: c.thread.filter((m) => m.response !== pending) } : c));
}

// cardHTML — карточка комментария. o: t — тексты, n — номер, on — выбран, edit — окно вместо
// своего неотправленного текста, reply — окно ответа под веткой.
export function cardHTML(c, o) {
  const t = o.t;
  const { fresh, draft, ask, last } = threadState(c);
  const stName = { unsent: t.cUnsent, waiting: t.cWaiting, working: t.cWorking, answered: t.cAnswered, question: t.cQuestion }[c.status] || c.status;
  const stt = c.status === 'working'
    ? `<span class="stt working">${DOTS} ${stName}</span>`
    : `<span class="stt ${c.status}">${c.status === 'question' ? '? ' : c.status === 'answered' ? '↩ ' : ''}${stName}</span>`;
  const point = !c.whole && c.range && c.range.start === c.range.end && c.was;
  const where = c.whole ? t.cWhole : point ? t.cRemoved : '';
  const inr = c.inResponse && o.inResp ? `<span class="inr">· ${t.rInResp}</span>` : '';
  let h = `<div class="cc${o.on ? ' on' : ''} st-${c.status}" data-cid="${esc(c.id)}"><div class="ch"><span class="no">${o.n || ''}</span>${stt}${inr}<span class="sp"></span>${where ? `<span class="wh">${where}</span>` : ''}</div>`;
  if (c.was) h += `<div class="was"><b>${t.cWas}</b>«${esc(c.was)}»${c.changed && !point ? `<i>${t.cChanged}</i>` : ''}</div>`;
  const th = c.thread || [];
  th.forEach((m, i) => {
    const mine = m.author === 'owner';
    const editingThis = o.edit && ((fresh && i === 0) || (draft && i === th.length - 1));
    if (editingThis) { h += o.edit; return; }
    if (i === 0 && mine) { h += `<div class="m">${esc(m.text)}${imgs(m)}</div>`; return; }
    if (!mine) {
      h += `<div class="m ag"><div class="au">${t.cAgent}${m.question ? ' · ' + t.cAsks : ''}</div>${esc(m.text)}${imgs(m)}`;
      if (i === ask && m.options && m.options.length) {
        const picked = draft ? last.text : null;
        h += '<div class="opts">' + m.options.map((x, k) => `<button class="opt${picked === x ? ' on' : ''}" data-cact="opt" data-i="${k}"><kbd>${k + 1}</kbd>${esc(x)}</button>`).join('')
          + `<button class="opt" data-cact="reply"><kbd>${m.options.length + 1}</kbd>${t.cOwn}</button></div>`;
      }
      h += '</div>';
      return;
    }
    const isDraft = draft && i === th.length - 1;
    const pickedOpt = isDraft && ask >= 0 && (th[ask].options || []).includes(m.text);
    h += `<div class="m${isDraft ? ' draft' : ''}"><div class="au">${t.cYou}${isDraft ? ' · ' + t.cNotSent : ''}</div>${pickedOpt ? `<span class="dim">${t.cPicked}</span>` : ''}${esc(m.text)}${imgs(m)}</div>`;
  });
  // Ответ агента ещё не просмотрен: его текст — только в окне ответа.
  if (c.pend) h += `<div class="pend" data-act="response">${t.rPending} <kbd>␣r</kbd></div>`;
  if (o.reply) h += o.reply;
  if (o.on && !o.edit && !o.reply && !c.pend) {
    const A = (act, label, key) => `<button data-cact="${act}">${label}${key ? ` <kbd>${key}</kbd>` : ''}</button>`;
    let acts = '';
    if (fresh) acts = A('edit', t.cEdit, '↵') + A('delete', t.cDelete, 'x');
    else if (draft) acts = A('edit', t.cEditReply, '↵') + A('delete', t.cDeleteReply, 'x');
    else if (c.status === 'question') acts = A('reply', t.cReply, '↵') + A('dismiss', t.cDismiss, 'x');
    else if (c.status === 'answered') acts = A('reply', t.cDisagree, '↵');
    if (acts) h += `<div class="acts">${acts}</div>`;
  }
  return h + '</div>';
}

// editorHTML — окно комментария или ответа. ed: whole, images [путь], ask — «выбросить?», reply.
export function editorHTML(ed, t) {
  const pics = (ed.images || []).length
    ? `<div class="imgs">${ed.images.map((p, i) => `<span class="thumb"><img src="${esc(imageUrl(p))}" alt=""><button data-eact="unimg" data-i="${i}" title="×">×</button></span>`).join('')}</div>`
    : '';
  const ft = ed.ask
    ? `<div class="ft ask"><span class="q">${ed.kind === 'reply' || ed.draft ? t.cDeleteReplyQ : t.cDeleteQ}</span><span class="sp"></span><button class="btn err" data-eact="discard">${t.cDelete} <kbd>↵</kbd></button><button class="btn2" data-eact="back">${t.cCancel} <kbd>esc</kbd></button></div>`
    : `<div class="ft"><span class="hint">${t.cImgHint}</span><span class="sp"></span><button class="btn2" data-eact="cancel">${t.cCancel} <kbd>esc</kbd></button><button class="btn" data-eact="save">${t.cSave} <kbd>⌘↵</kbd></button></div>`;
  const cls = ['ced'];
  if (ed.whole) cls.push('whole');
  if (ed.kind !== 'new') cls.push('incard');
  return `<div class="${cls.join(' ')}" id="ced">${ed.whole && ed.kind === 'new' ? `<div class="wh">${t.cWhole}</div>` : ''}<textarea placeholder="${esc(ed.kind === 'reply' ? t.cReplyPh : t.cPlaceholder)}"${ed.ask ? ' readonly' : ''}></textarea>${pics}${ft}</div>`;
}

// ── места в тексте ──

// anchorIndex — куски текста текущей версии по строкам: строка → [[символ, элемент]].
export function anchorIndex(root) {
  const m = new Map();
  for (const el of root.querySelectorAll('[data-p^="n"]')) {
    const p = parseP(el.dataset.p);
    if (!p) continue;
    if (!m.has(p.line)) m.set(p.line, []);
    m.get(p.line).push([p.col, el]);
  }
  return m;
}

const textIn = (el) => {
  const out = [];
  const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
  let n;
  while ((n = w.nextNode())) out.push(n);
  return out;
};

// domPoint — место (строка, символ) текущей версии → точка в тексте на экране.
export function domPoint(idx, line, col, isEnd) {
  const list = idx.get(line);
  if (!list) return null;
  let hit = null;
  for (const [c0, el] of list) {
    const len = el.textContent.length;
    if (col >= c0 && col < c0 + len) { hit = [c0, el]; break; }
    if (col === c0 + len) hit = [c0, el];
    if (!isEnd && col === c0 && !hit) hit = [c0, el];
  }
  if (!hit) {
    if (!list.length) return null;
    const [c0, el] = list[list.length - 1];
    if (col < c0) return null;
    hit = [c0, el];
  }
  let rest = col - hit[0];
  let last = null;
  for (const n of textIn(hit[1])) {
    if (rest <= n.length) return [n, rest];
    rest -= n.length;
    last = n;
  }
  return last ? [last, last.length] : [hit[1], 0];
}

// pointPos — точка в тексте на экране → место в исходнике {side, line, col}.
export function pointPos(node, off) {
  const el = (node.nodeType === 3 ? node.parentElement : node)?.closest('[data-p]');
  if (!el) return null;
  const p = parseP(el.dataset.p);
  if (!p) return null;
  if (node.nodeType !== 3) {
    // Точка между элементами: считаем символы до неё.
    const r = document.createRange();
    r.setStart(el, 0);
    try { r.setEnd(node, off); } catch { return null; }
    return { ...p, col: p.col + r.toString().length };
  }
  let col = p.col;
  for (const n of textIn(el)) {
    if (n === node) return { ...p, col: col + off };
    col += n.length;
  }
  return null;
}

// anchorRanges — места комментариев на экране (Range), по id.
export function anchorRanges(root, comments, current) {
  const idx = anchorIndex(root);
  const starts = lineStarts(current);
  const out = new Map();
  for (const c of comments) {
    if (c.whole || !c.range || c.range.end <= c.range.start) continue;
    const s = charToPos(starts, byteToChar(current, c.range.start));
    const e = charToPos(starts, byteToChar(current, c.range.end));
    const a = domPoint(idx, s.line, s.col, false);
    const b = domPoint(idx, e.line, e.col, true);
    if (!a || !b) continue;
    const r = document.createRange();
    try { r.setStart(a[0], a[1]); r.setEnd(b[0], b[1]); } catch { continue; }
    if (!r.collapsed) out.set(c.id, r);
  }
  return out;
}

const HL = ['unsent', 'waiting', 'working', 'answered', 'question'];

// paintAnchors — текст с комментарием подчёркнут цветом статуса, у выбранного ещё и подсвечен.
export function paintAnchors(ranges, comments, active) {
  if (typeof CSS === 'undefined' || !CSS.highlights || typeof Highlight === 'undefined') return;
  const groups = Object.fromEntries(HL.map((k) => [k, []]));
  const on = [];
  for (const c of comments) {
    const r = ranges.get(c.id);
    if (!r) continue;
    (groups[c.status] || groups.waiting).push(r);
    if (c.id === active) on.push(r);
  }
  for (const k of HL) CSS.highlights.set('cm-' + k, new Highlight(...groups[k]));
  const h = new Highlight(...on);
  h.priority = 1;
  CSS.highlights.set('cm-on', h);
}

// placeCards — карточки под тем, к чему они относятся. list — [{c, html}] по порядку.
// Комментарий к удалённому тексту (точка) — перед тем, что идёт после удалённого; в двух колонках —
// на заштрихованном месте справа. Ко всему файлу — в начале.
export function placeCards(sc, list, current) {
  sc.querySelectorAll('.cc-slot').forEach((e) => e.remove());
  sc.querySelectorAll('.cc-in-hatch').forEach((e) => e.classList.remove('cc-in-hatch'));
  if (!list.length) return;
  const box = sc.querySelector('.doc, .code, .two, .code2');
  if (!box) return;
  const kind = box.classList.contains('two') ? 'two' : box.classList.contains('code2') ? 'code2' : box.classList.contains('code') ? 'code' : 'doc';
  const rows = [...box.children].filter((e) => e.hasAttribute('data-n') || e.hasAttribute('data-o') || e.classList.contains('fold'));
  const starts = lineStarts(current);
  const slot = (html) => {
    const d = document.createElement('div');
    if (kind === 'two') { d.className = 'r cc-slot'; d.innerHTML = `<div class="c"></div><div class="c">${html}</div>`; }
    else if (kind === 'code2') { d.className = 'cc-slot cc2'; d.innerHTML = `<div></div><div>${html}</div>`; }
    else { d.className = `cc-slot cc-${kind}`; d.innerHTML = html; }
    return d;
  };
  const whole = [];
  const after = new Map(); // элемент → карточки после него
  const before = new Map(); // элемент → карточки перед ним
  const end = [];
  const push = (m, k, h) => { if (!m.has(k)) m.set(k, []); m.get(k).push(h); };
  for (const { c, html } of list) {
    if (c.whole) { whole.push(html); continue; }
    const point = c.range.start === c.range.end;
    const line = charToPos(starts, byteToChar(current, point ? c.range.start : Math.max(c.range.start, c.range.end - 1))).line + 1;
    if (point) {
      // Удалённое показано в паре с тем, что его заменило («было / стало» или строка двух колонок) —
      // карточка после этой пары. Иначе — перед тем, что идёт после удалённого.
      let own = null;
      for (const r of rows) { if (r.hasAttribute('data-n') && +r.dataset.n <= line) own = r; else if (r.hasAttribute('data-n')) break; }
      if (own && kind !== 'code' && (own.classList.contains('del') || own.querySelector('.b.del, .tx.del'))) { push(after, own, html); continue; }
      const next = rows.find((r) => r.hasAttribute('data-n') && +r.dataset.n >= line);
      if (!next) { end.push(html); continue; }
      push(before, next, html);
    } else {
      let row = null;
      for (const r of rows) { if (r.hasAttribute('data-n') && +r.dataset.n <= line) row = r; else if (r.hasAttribute('data-n')) break; }
      if (!row) { end.push(html); continue; }
      // Код: строка, которой кончается выделение; документ: блок целиком.
      push(after, row, html);
    }
  }
  if (whole.length) {
    const s = slot(whole.join(''));
    s.classList.add('cc-whole');
    const hd = box.querySelector(':scope > .hd');
    if (hd) hd.after(s); else box.prepend(s);
  }
  for (const [row, hs] of after) {
    let at = row;
    // Пара «было / стало» — карточка после «стало».
    while (at.nextElementSibling && at.nextElementSibling.classList.contains('chg') && at.nextElementSibling.classList.contains('add') && !at.nextElementSibling.hasAttribute('data-o') && at.classList.contains('del')) at = at.nextElementSibling;
    at.after(slot(hs.join('')));
  }
  for (const [row, hs] of before) {
    const prev = row.previousElementSibling;
    // Две колонки: удалённое слева, справа штриховка — карточка прямо на штриховке.
    const hatch = kind === 'two' && prev && prev.querySelector(':scope > .c:last-child > .b.hatch');
    if (hatch) {
      hatch.classList.add('cc-in-hatch');
      const d = document.createElement('div');
      d.className = 'cc-slot cc-hatch';
      d.innerHTML = hs.join('');
      hatch.append(d);
      continue;
    }
    row.before(slot(hs.join('')));
  }
  if (end.length) box.append(slot(end.join('')));
}

// ── фигуры: выделение и правки в несколько строк ──

// lines — прямоугольники строк (в координатах origin), соседние куски одной строки слиты.
export function lines(rects, origin, pad = 0) {
  const rs = [...rects].filter((r) => r.width > 0.5 && r.height > 2)
    .map((r) => ({ l: r.left - origin.left - pad, r: r.right - origin.left + pad, t: r.top - origin.top, b: r.bottom - origin.top }))
    .sort((a, b) => a.t - b.t || a.l - b.l);
  const out = [];
  for (const r of rs) {
    const x = out.find((y) => Math.min(y.b, r.b) - Math.max(y.t, r.t) > Math.min(y.b - y.t, r.b - r.t) * 0.5);
    if (x) { x.l = Math.min(x.l, r.l); x.r = Math.max(x.r, r.r); x.t = Math.min(x.t, r.t); x.b = Math.max(x.b, r.b); }
    else out.push({ ...r });
  }
  return out.sort((a, b) => a.t - b.t);
}

function roundPoly(pts, R) {
  const n = pts.length;
  let d = '';
  for (let i = 0; i < n; i++) {
    const p = pts[i], a = pts[(i - 1 + n) % n], b = pts[(i + 1) % n];
    const da = Math.hypot(a[0] - p[0], a[1] - p[1]), db = Math.hypot(b[0] - p[0], b[1] - p[1]);
    const r = Math.min(R, da / 2, db / 2);
    const p1 = [p[0] + ((a[0] - p[0]) / da) * r, p[1] + ((a[1] - p[1]) / da) * r];
    const p2 = [p[0] + ((b[0] - p[0]) / db) * r, p[1] + ((b[1] - p[1]) / db) * r];
    d += `${i ? 'L' : 'M'}${p1[0].toFixed(1)} ${p1[1].toFixed(1)}Q${p[0].toFixed(1)} ${p[1].toFixed(1)} ${p2[0].toFixed(1)} ${p2[1].toFixed(1)}`;
  }
  return d + 'Z';
}

// shapePath — строки одной фигурой: соседние строки смыкаются, углы (и ступеньки) скруглены.
// Строки, которые не перекрываются по горизонтали, — отдельные куски.
export function shapePath(ls, R = 8, gap = 44) {
  const groups = [];
  let g = [];
  for (const x0 of ls) {
    const x = { ...x0 };
    if (g.length) {
      const p = g[g.length - 1];
      if (x.t - p.b < gap && x.l < p.r && p.l < x.r) { const m = (p.b + x.t) / 2; p.b = m; x.t = m; }
      else { groups.push(g); g = []; }
    }
    g.push(x);
  }
  if (g.length) groups.push(g);
  return groups.map((grp) => {
    const pts = [];
    grp.forEach((x) => { pts.push([x.r, x.t], [x.r, x.b]); });
    for (let i = grp.length - 1; i >= 0; i--) pts.push([grp[i].l, grp[i].b], [grp[i].l, grp[i].t]);
    const near = (p, q) => Math.abs(p[0] - q[0]) < 0.5 && Math.abs(p[1] - q[1]) < 0.5;
    const a = pts.filter((p, i) => !near(p, pts[(i + 1) % pts.length]));
    const b = a.filter((p, i) => {
      const x = a[(i - 1 + a.length) % a.length], y = a[(i + 1) % a.length];
      return !((Math.abs(x[0] - p[0]) < 0.5 && Math.abs(y[0] - p[0]) < 0.5) || (Math.abs(x[1] - p[1]) < 0.5 && Math.abs(y[1] - p[1]) < 0.5));
    });
    return b.length > 2 ? roundPoly(b, R) : '';
  }).join('');
}

const SVG = 'http://www.w3.org/2000/svg';

// shapeMarks — правка рисуется одной фигурой под текстом, если она переносится на несколько строк или
// разбита разметкой (жирное, ссылка, код): соседние куски одной правки без текста между ними — одна
// фигура. Сначала все замеры, потом все правки страницы: так раскладка считается один раз.
export function shapeMarks(root) {
  root.querySelectorAll('svg.mshape').forEach((s) => s.remove());
  root.querySelectorAll('mark.shaped').forEach((m) => m.classList.remove('shaped'));
  const HOST = '.tx, .cl, .chg, .b, li, td, th, p, h1, h2, h3, h4, h5, h6, .lc, pre';
  // Куски одной правки подряд: тот же блок, тот же цвет, между ними нет текста.
  const runs = [];
  let run = null;
  const gap = document.createRange();
  for (const m of root.querySelectorAll('mark.wa, mark.wd')) {
    const host = m.closest(HOST) || root;
    const cls = m.classList.contains('wa') ? 'wa' : 'wd';
    let joined = false;
    if (run && run.host === host && run.cls === cls) {
      const prev = run.marks[run.marks.length - 1];
      gap.setStartAfter(prev);
      gap.setEndBefore(m);
      joined = gap.toString() === '';
    }
    if (joined) run.marks.push(m);
    else { run = { host, cls, marks: [m] }; runs.push(run); }
  }
  const hosts = new Map();
  for (const r of runs) {
    const rects = r.marks.flatMap((m) => [...m.getClientRects()]);
    if (r.marks.length < 2 && rects.length < 2) continue;
    if (!hosts.has(r.host)) hosts.set(r.host, { hr: r.host.getBoundingClientRect(), list: [] });
    hosts.get(r.host).list.push([r, rects]);
  }
  const out = [];
  for (const [host, { hr, list }] of hosts) {
    let wa = '', wd = '';
    const marks = [];
    for (const [r, rects] of list) {
      const ls = lines(rects, hr);
      if (!ls.length) continue;
      marks.push(...r.marks);
      const d = shapePath(ls, 4, 30);
      if (r.cls === 'wa') wa += d; else wd += d;
    }
    if (wa || wd) out.push({ host, hr, wa, wd, marks });
  }
  for (const { host, hr, wa, wd, marks } of out) {
    marks.forEach((m) => m.classList.add('shaped'));
    host.classList.add('mhost');
    const svg = document.createElementNS(SVG, 'svg');
    svg.setAttribute('class', 'mshape');
    svg.setAttribute('width', Math.ceil(hr.width));
    svg.setAttribute('height', Math.ceil(hr.height));
    svg.innerHTML = (wa ? `<path class="wa" d="${wa}"/>` : '') + (wd ? `<path class="wd" d="${wd}"/>` : '');
    host.prepend(svg);
  }
}

const GRAD = '<defs><linearGradient id="selg" x1="0" x2="1" y1="0" y2="0"><stop class="a" offset="0"/><stop class="b" offset="1"/></linearGradient></defs>';

// drawSelection — своё выделение одной фигурой в слое sc. Возвращает строки фигуры (для меню и окна).
export function drawSelection(sc, range) {
  let svg = sc.querySelector(':scope > svg.selsvg');
  if (!svg) {
    svg = document.createElementNS(SVG, 'svg');
    svg.setAttribute('class', 'selsvg');
    sc.append(svg);
  }
  if (!range || range.collapsed) { svg.innerHTML = ''; return []; }
  const o = sc.getBoundingClientRect();
  const origin = { left: o.left - sc.scrollLeft, top: o.top - sc.scrollTop };
  const ls = lines(range.getClientRects(), origin, 2);
  svg.setAttribute('width', sc.scrollWidth);
  svg.setAttribute('height', sc.scrollHeight);
  svg.innerHTML = ls.length ? GRAD + `<path d="${shapePath(ls)}"/>` : '';
  return ls;
}
