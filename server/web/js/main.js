// Веб-клиент duet: список ревью и ревью (дерево, отличия, принятие), клавиши, тема и язык,
// живые обновления. Всё, что решает поведение, делает ядро; клиент показывает и передаёт действия.

import { esc, fileKind, splitLines } from './util.js';
import { langOf } from './syntax.js';
import { dict, defaultLang, fmtDate } from './i18n.js';
import { buildTree, flatten, fileOrder, isAccepted, counts } from './tree.js';
import { renderCode, renderDoc, renderDocPlain, renderLines } from './view.js';
import { target, commentOrder } from './anchors.js';
import { editLinesHTML, editSelection, lineOf, indentUnit } from './edit.js';
import { cardHTML, editorHTML, threadState, shownComments, placeCards, anchorRanges, paintAnchors, pointPos, shapeMarks, drawSelection } from './comments.js';

const $ = (s) => document.querySelector(s);

const store = {
  get(k, d) {
    try { const v = localStorage.getItem('duet:' + k); return v == null ? d : JSON.parse(v); } catch { return d; }
  },
  set(k, v) {
    try { localStorage.setItem('duet:' + k, JSON.stringify(v)); } catch { /* приватный режим */ }
  },
};

const S = {
  reviews: null,
  review: null,
  missing: false,
  tree: null,
  order: [],
  path: null,
  file: null,
  chunks: null,
  contentKey: '',
  cols: store.get('cols', 1),
  doc: store.get('doc', true),
  theme: store.get('theme', 'system'),
  lang: store.get('lang', defaultLang()),
  collapsed: new Set(),
  blocksHidden: false,
  expanded: new Set(),
  overlay: '', // menu | help
  toast: null,
  online: true,
  build: '',
  sync: null,
  listSel: 0,
  pending: '',
  blockLines: [],
  // комментарии (шаг 6b)
  sel: null, // своё выделение: {range, a, b, lines} — a, b — места в исходнике
  menuSel: false, // меню «Комментарий gc» у выделения
  ed: null, // открытое окно: {kind: new|edit|reply, id, whole, target, text, images, ask, x, y}
  act: null, // выбранный комментарий
  ask: null, // «удалить?» у выбранного комментария
  ranges: new Map(), // места комментариев на экране
  stopSince: 0, // когда увидели «останавливается…»
  // окно ответа (шаг 6c): {back: {path, anchor}} — что было под окном
  resp: null,
  // правка владельца (шаг 6d): {key, path, from, text, kind, lang, line, off, sel}
  edit: null,
  editAfterStop: null, // «прервать и править»: что сделать, когда агент остановится — edit | revert
  diskSeq: {}, // последняя показанная правка с диска по ревью
};
const T = () => dict(S.lang);

// ── API ──

class ApiError extends Error {
  constructor(status, body) {
    super(body.message || body.error || String(status));
    this.status = status;
    this.code = body.error;
  }
}

async function api(url, body) {
  const r = await fetch(url, body === undefined ? {} : { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw new ApiError(r.status, data);
  return data;
}
const rid = () => encodeURIComponent(S.review.id);
const act = (action, body = {}) => api(`/api/reviews/${rid()}/${action}`, body);

// ── маршрут ──

function routeId() {
  const m = /^\/r\/([^/]+)/.exec(location.pathname);
  return m ? decodeURIComponent(m[1]) : '';
}

function go(path) {
  history.pushState(null, '', path);
  enter();
}

window.addEventListener('popstate', enter);

async function enter() {
  if (S.edit && S.review) act('editing', { on: false }).catch(() => {});
  S.edit = null; S.editAfterStop = null;
  S.review = null; S.missing = false; S.path = null; S.file = null; S.chunks = null; S.contentKey = '';
  S.overlay = ''; S.toast = null; S.sync = null; S.expanded = new Set();
  S.sel = null; S.menuSel = false; S.ed = null; S.act = null; S.ask = null; S.resp = null;
  const id = routeId();
  if (!id) {
    await loadList();
    renderAll();
    return;
  }
  S.collapsed = new Set(store.get('collapsed:' + id, []));
  renderAll();
  await openReview(id);
}

// ── загрузка ──

async function loadList() {
  try { S.reviews = await api('/api/reviews'); } catch { /* ядро остановлено — покажет onerror */ }
}

async function loadReview(id) {
  try {
    S.review = await api('/api/reviews/' + encodeURIComponent(id));
    S.missing = false;
  } catch (e) {
    if (e.status === 404) { S.review = null; S.missing = true; }
    return;
  }
  rebuildTree();
  // «Вернуть файлы всё равно» — через 10 секунд после «останавливается…».
  if (S.review.stopping && !S.stopSince) { S.stopSince = Date.now(); setTimeout(renderOverlays, 10100); }
  if (!S.review.stopping) S.stopSince = 0;
  diskNotice(id);
  // Редактор в этой вкладке закрыт, а ядро считает его открытым (вкладку перезагрузили) — закрываем.
  if (S.review.editing && !S.edit) act('editing', { on: false }).catch(() => {});
  if (S.editAfterStop && !S.review.locked) {
    const x = S.editAfterStop;
    S.editAfterStop = null;
    setTimeout(() => (x === 'edit' ? startEdit() : askRevert()), 0);
  }
}

// diskNotice — duet взял изменения с диска правкой владельца: короткая подсказка.
function diskNotice(id) {
  const fd = S.review.fromDisk;
  if (!fd) return;
  const seen = S.diskSeq[id];
  S.diskSeq[id] = fd.seq;
  const fresh = Date.now() - new Date(fd.at).getTime() < 15000;
  if (seen !== undefined ? fd.seq <= seen : !fresh) return;
  const t = T();
  const name = (fd.files[0] || '').split('/').pop();
  toast({ ok: true, icon: '✎', cls: 'o', title: t.diskTitle(name, fd.files.length), text: t.diskText, short: `✎ ${t.diskTitle(name, fd.files.length)}` });
}

// Файлы, которых нет в текущей версии (есть только в ответе), видны только в окне ответа.
const shown = (files) => files.filter((f) => !f.absent);
const visibleFiles = () => (S.review ? (S.resp ? S.review.files : shown(S.review.files)) : []);
const pendingResp = () => (S.review && S.review.response) || null;

function rebuildTree() {
  const { tree } = buildTree(visibleFiles());
  S.tree = tree;
  S.order = fileOrder(tree);
}

// Файлы ответа в порядке дерева.
function respFiles() {
  const p = pendingResp();
  if (!p) return [];
  const set = new Set(p.files);
  return S.order.filter((f) => set.has(f.path));
}

function copyLink() {
  const btn = document.querySelector('[data-act="copylink"]');
  const link = btn ? btn.dataset.link : '';
  if (link && navigator.clipboard) navigator.clipboard.writeText(link).catch(() => {});
  toast({ ok: true, short: `✓ ${T().copied}`, title: T().copied });
}

// openReview — открыть ревью: сверить с диском, прочитать, открыть файл.
async function openReview(id) {
  act0(id, 'sync');
  await loadReview(id);
  if (!S.review) { renderAll(); return; }
  const saved = store.get('pos:' + id, null);
  const has = (p) => visibleFiles().some((f) => f.path === p);
  let path = null;
  let line = 0;
  let off;
  if (saved && has(saved.path)) { path = saved.path; line = saved.n; off = saved.off; }
  else {
    const start = S.review.files.find((f) => f.path === S.review.start);
    const fresh = S.order.find((f) => f.state === 'new');
    path = (start && start.state === 'new' ? start : fresh || S.order[0] || {}).path || null;
  }
  renderAll();
  if (path) await openFile(path, { line, off });
}

function act0(id, action) {
  api(`/api/reviews/${encodeURIComponent(id)}/${action}`, {}).catch(() => {});
}

async function openFile(path, { line = 0, off, keep = false } = {}) {
  const changed = path !== S.path;
  if (changed) {
    S.expanded = new Set(); S.blocksHidden = false; if (S.toast && !S.toast.ok) { S.toast = null; renderOverlays(); }
    S.sel = null; S.menuSel = false; S.act = null; S.ask = null;
    if (S.ed) S.ed = null;
  }
  S.path = path;
  const anchor = keep ? anchorOf() : null;
  let file, chunks;
  try {
    [file, chunks] = await Promise.all([
      api(`/api/reviews/${rid()}/file?path=${encodeURIComponent(path)}`),
      api(`/api/reviews/${rid()}/diff?path=${encodeURIComponent(path)}${S.resp ? '&mode=response' : ''}`).catch(() => []),
    ]);
  } catch {
    return;
  }
  if (S.path !== path) return; // пока грузили, открыли другой
  S.file = file;
  S.chunks = chunks;
  if (file.state === 'moved' && !S.reviews) await loadList();
  if (file.state === 'new' && !S.resp) act('opened', { path }).catch(() => {});
  renderAll();
  if (anchor) restoreAnchor(anchor);
  else if (line && off !== undefined) restoreAnchor({ n: line, off });
  else if (changed || line) scrollToLine(line);
  savePos();
}

// ── живые обновления ──

let es = null;
let refreshTimer = 0;
let dirty = new Set();

function connect() {
  es = new EventSource('/api/events');
  es.addEventListener('hello', (e) => {
    const { build } = JSON.parse(e.data);
    if (S.build && build !== S.build) { location.reload(); return; }
    S.build = build;
    const was = S.online;
    S.online = true;
    if (!was) renderAll();
    // После каждого подключения ревью открывается заново: после перезапуска duet сам ничего не сверяет.
    if (S.review) { act('sync').catch(() => {}); refresh(null); }
    else if (!routeId()) refresh(null);
  });
  es.addEventListener('change', (e) => refresh(JSON.parse(e.data).reviews));
  es.addEventListener('reset', () => refresh(null));
  es.addEventListener('sync', (e) => {
    const d = JSON.parse(e.data);
    if (S.review && d.review === S.review.id) {
      S.sync = d.done < d.total ? d : null;
      renderStatus();
    }
  });
  es.onerror = () => {
    if (S.online) { S.online = false; renderAll(); }
  };
}

function refresh(ids) {
  if (ids === null) dirty.add('*');
  else ids.forEach((i) => dirty.add(i));
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(async () => {
    const d = dirty;
    dirty = new Set();
    const id = routeId();
    if (!id) { await loadList(); renderAll(); return; }
    if (!d.has('*') && !d.has(id)) return;
    await loadReview(id);
    if (!S.review) { renderAll(); return; }
    if (S.resp && !pendingResp()) { await closeResp(); return; } // ответ приняли или отклонили в другой вкладке
    if (respStale()) {
      // Агент закончил: под руками ничего не меняется, только сигнал и «открыть заново».
      if (!S.resp.stale) { S.resp.stale = true; toast({ ok: true, short: `✓ ${T().rStaleToast}` }); }
      rebuildTree(); renderTree(); renderFrame(); renderStatus();
      return;
    }
    if (S.path && visibleFiles().some((f) => f.path === S.path)) await openFile(S.path, { keep: true });
    else renderAll();
  }, 60);
}

// ── отрисовка ──

function applyTheme() {
  const dark = S.theme === 'dark' || (S.theme === 'system' && matchMedia('(prefers-color-scheme: dark)').matches);
  document.body.classList.toggle('dark', dark);
  document.body.classList.toggle('light', !dark);
  document.documentElement.lang = S.lang;
}
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme);

function renderAll() {
  applyTheme();
  const id = routeId();
  document.body.classList.toggle('is-list', !id);
  renderHeader();
  if (!id) {
    $('#tree').innerHTML = '';
    $('#winbar').innerHTML = '';
    $('#banner').innerHTML = '';
    $('#map').innerHTML = '';
    $('#sc').innerHTML = listHTML();
    renderFrame();
    renderEdit();
    S.contentKey = '';
    document.title = 'duet';
  } else {
    renderFrame();
    renderTree();
    renderWinbar();
    renderBanner();
    renderContent();
    renderComments();
    renderEdit();
    document.title = S.review ? `${S.review.title || S.review.id} — duet` : 'duet';
  }
  renderStatus();
  renderOverlays();
}

const SNAKE = '<span class="dots"><i></i><i></i><i></i><i></i><i></i><i></i></span>';
const RIPPLE = (seen, still = !!(S.review && S.review.working)) => `<span class="rip${seen ? ' seen' : ''}${still ? ' still' : ''}"><i></i><i></i><i></i><b></b></span>`;

function renderHeader() {
  const t = T();
  const id = routeId();
  let left = `<a class="logo" href="/" data-go="/">duet</a>`;
  if (id) left += `<span class="sep">/</span><span class="ttl">${esc(S.review ? S.review.title || S.review.id : '')}</span>`;
  else left += `<span class="sep">/</span><span class="ttl dim">${esc(location.host)}</span>`;
  let right = '';
  if (id && S.file && S.file.state !== 'moved') {
    if (fileKind(S.file.path) === 'markdown' && !S.file.currentDeleted) {
      right += `<button class="tb" data-act="doc" title="␣m">${S.doc ? t.doc : t.source} <kbd>␣m</kbd></button>`;
    }
    right += `<button class="tb" data-act="cols" title="␣v">${S.cols === 2 ? t.two : t.one} <kbd>␣v</kbd></button><span class="tsep"></span>`;
  }
  right += `<button class="tb" data-act="lang">${S.lang === 'ru' ? '<b>RU</b><s>EN</s>' : '<s>RU</s><b>EN</b>'}</button>`;
  const th = { system: ['◐', t.themeSystem], dark: ['◑', t.themeDark], light: ['○', t.themeLight] }[S.theme];
  right += `<button class="tb" data-act="theme">${th[0]} ${th[1]}</button>`;
  $('#tabline').innerHTML = `${left}<span class="sp"></span>${right}`;
}

function fileIcon(f) {
  if (f.agentWorking) return SNAKE;
  if (f.ripple) return RIPPLE(f.state !== 'new');
  switch (f.state) {
    case 'new': return '<span class="d-new"></span>';
    case 'opened': return '<span class="d-seen"></span>';
    case 'accepted': return '<span class="d-ok">✓</span>';
    case 'deleted': return '<span class="d-gone"></span>';
    case 'deleted-accepted': return '<span class="d-ok gone">✓</span>';
    case 'moved': return '<span class="d-moved">↗</span>';
  }
  return '';
}

function fileTag(f) {
  const t = T();
  if (f.diskChanged) return `<span class="tag warn">${t.stDiskShort}</span>`;
  if (f.ripple) return `<span class="tag rpl">${S.lang === 'ru' ? 'ответ' : 'response'}</span>`;
  if (f.state === 'deleted' || f.state === 'deleted-accepted') return `<span class="tag">${t.stDeleted}</span>`;
  if (f.state === 'moved') return `<span class="tag">↗</span>`;
  return '';
}

function renderTree() {
  const el = $('#tree');
  if (!S.review) { el.innerHTML = ''; return; }
  const ind = (n) => '<span class="ind"></span>'.repeat(n);
  let h = '';
  const rset = S.resp ? new Set((pendingResp() || {}).files || []) : null;
  el.classList.toggle('rmode', !!S.resp);
  for (const r of flatten(S.tree, S.collapsed)) {
    if (r.dir) {
      h += `<div class="tr dir" data-dir="${esc(r.key)}"><span class="pad"></span>${ind(r.depth)}<span class="ic">${r.open ? '▾' : '▸'}</span><span class="nm">${esc(r.name)}</span></div>`;
      continue;
    }
    const f = r.file;
    const open = f.path === S.path;
    const gone = ['deleted', 'deleted-accepted', 'moved'].includes(f.state) ? ' gone' : '';
    const off = rset && !rset.has(f.path) ? ' untouched' : '';
    const tag = S.resp ? (f.absent ? `<span class="tag rnew">${T().rNew}</span>` : f.diskChanged ? `<span class="tag warn">${T().stDiskShort}</span>` : '') : fileTag(f);
    h += `<div class="tr${open ? ' open' : ''}${gone}${off}" data-file="${esc(f.path)}" title="${esc(f.path)}"><span class="pad"></span>${ind(r.depth)}<span class="ic">${fileIcon(f)}</span><span class="nm">${esc(f.name)}</span>${f.open && !S.resp ? `<span class="cmk${f.questions ? ' q' : ''}">${f.open}</span>` : ''}<span class="fill"></span>${tag}</div>`;
    if (open && !S.resp && !S.blocksHidden && S.file && S.file.path === f.path) {
      const cur = currentBlock();
      for (const [k, b] of (S.file.blocks || []).entries()) {
        const imp = { top: ['⇈', 'i3'], high: ['↑', 'i2'], low: ['↓', 'i1'] }[b.level] || ['·', 'i1'];
        h += `<div class="tr blk${cur === k ? ' cur' : ''}" data-block="${k}"><span class="pad"></span>${ind(r.depth + 1)}<span class="imp ${imp[1]}">${imp[0]}</span><span class="nm">${esc(b.title)}</span></div>`;
      }
    }
  }
  if (!visibleFiles().length) h = `<div class="empty">${T().noFiles}</div>`;
  el.innerHTML = h;
  const open = el.querySelector('.tr.open');
  if (open && (open.offsetTop < el.scrollTop || open.offsetTop > el.scrollTop + el.clientHeight - 30)) el.scrollTop = open.offsetTop - 80;
}

function stateText(f) {
  const t = T();
  const parts = [];
  if (f.agentWorking) parts.push(`${SNAKE} ${t.stWorking}`);
  else if (f.ripple) parts.push(`${RIPPLE(true)} <span class="rsp-t">${t.rTouches}</span>`);
  else {
    const st = { new: t.stNew, opened: t.stOpened, accepted: t.stAccepted, deleted: t.stDeleted, 'deleted-accepted': `${t.stDeleted} · ${t.stAccepted}`, moved: t.stMoved }[f.state];
    parts.push(`${fileIcon(f)} ${st || ''}`);
  }
  if (f.diskChanged) parts.push(`<span class="warn">${t.stDisk}</span>`);
  return parts.join(' · ');
}

function renderWinbar() {
  const el = $('#winbar');
  const f = S.file;
  if (!S.review || !f) { el.innerHTML = ''; return; }
  const t = T();
  const rel = relPath(f.path);
  const slash = rel.lastIndexOf('/');
  const acc = isAccepted(f)
    ? `<span class="kb done">✓ ${t.accepted}</span>`
    : f.state === 'moved' ? '' : `<button class="kb" data-act="accept">${t.accept} <kbd>␣a</kbd></button>`;
  const cm = canComment() ? `<button class="kb" data-act="comment">${t.menuComment} <kbd>␣c</kbd></button>` : '';
  const name = `<span><span class="dir">${esc(rel.slice(0, slash + 1))}</span><span class="fn">${esc(rel.slice(slash + 1))}</span></span>`;
  if (S.resp) {
    const P = (x, cls = '') => `<span class="pill${cls}">${x}</span>`;
    let vs;
    if (f.response == null && !f.responseDeleted) vs = `${P(t.rCurrent)} = ${P(t.rResp, ' rs')}`;
    else if (f.absent && !f.hasBase) vs = `${P(t.rResp, ' rs')} <span class="rnew">${t.rNew}</span>`;
    else vs = `${P(f.absent ? t.beforeTask : t.rCurrent)} → ${P(t.rResp, ' rs')}`;
    const held = f.diskChanged ? `<span class="dnote">${t.diskHeld}</span>` : '';
    el.innerHTML = `${name}<span class="vs">${vs}</span>${held}<span class="sp"></span>${cm}`;
    return;
  }
  const rb = f.ripple && pendingResp() ? `<button class="kb rsp" data-act="response">${t.rHint} <kbd>␣r</kbd></button>` : '';
  const ed = canEdit() ? `<button class="kb" data-act="edit">${t.edit} <kbd>␣e</kbd></button>` : '';
  el.innerHTML = `${name}<span class="st">${stateText(f)}</span><span class="sp"></span>${rb}${ed}${cm}${acc}`;
}

function relPath(p) {
  const root = buildTree(S.review.files).root;
  return root ? p.slice(root.length + 1) : p;
}

function contentHTML() {
  const t = T();
  const f = S.file;
  const kind = fileKind(f.path);
  const lang = kind === 'code' ? langOf(f.path) : kind === 'markdown' ? 'md' : '';
  const docMode = kind === 'markdown' && S.doc;
  const words = { was: t.was, now: t.now, added: t.added, removed: t.removed, more: t.more, changed: t.changed, blankAdded: t.blankAdded, blankRemoved: t.blankRemoved };
  if (S.resp) return respContentHTML(words);
  if (f.state === 'moved') {
    const r = (S.reviews || []).find((x) => x.id === f.movedTo);
    return `<div class="notice"><a href="/r/${encodeURIComponent(f.movedTo)}" data-go="/r/${encodeURIComponent(f.movedTo)}">${esc(t.movedTo(r ? r.title : f.movedTo))}</a></div>`;
  }
  if (f.currentDeleted) {
    return `<div class="notice del">${t.fileDeleted}</div><div class="gone-body">${docMode ? renderDocPlain(f.base) : renderLines(f.base, lang)}</div>`;
  }
  if (!f.hasBase) {
    return `<div class="notice add">${t.fileNew}</div>${docMode ? renderDocPlain(f.current) : renderLines(f.current, lang)}`;
  }
  const origin = f.baseIsOriginal ? ` <b>${t.beforeTask}</b>${f.originSource ? ' · ' + esc(f.originSource) : ''}` : '';
  const labels = { base: `<span class="pill">${t.base}</span>${origin}`, current: `<span class="pill">${t.current}</span>` };
  const bar = S.cols === 1 && f.baseIsOriginal ? `<div class="basebar"><span class="pill">${t.base}</span>${origin}</div>` : '';
  if (docMode) return bar + renderDoc(S.chunks, f.base, f.current, { cols: S.cols, labels, words });
  return bar.replace('basebar', 'basebar code-bar') + renderCode(S.chunks, { cols: S.cols, lang, oldText: f.base, newText: f.current, fold: kind === 'code', expanded: S.expanded, labels, foldText: t.fold });
}

// Текст «до» и «после» в окне ответа: текущая (у файла, которого владелец ещё не видел, — база) и ответ.
const respOld = (f) => (f.absent ? (f.hasBase ? f.base : '') : f.current || '');
const respNew = (f) => (f.response == null ? f.current || '' : f.response);

function respContentHTML(words) {
  const t = T();
  const f = S.file;
  const kind = fileKind(f.path);
  const lang = kind === 'code' ? langOf(f.path) : kind === 'markdown' ? 'md' : '';
  const docMode = kind === 'markdown' && S.doc;
  const plain = (text, side) => (docMode ? renderDocPlain(text, side) : renderLines(text, lang, side));
  const old = respOld(f);
  if (f.responseDeleted) return `<div class="notice del">${t.rDeleted}</div><div class="gone-body">${plain(old, 'o')}</div>`;
  if (f.response == null) return `<div class="notice">${t.rOnlyAnswers}</div>${plain(f.current || '')}`;
  if (f.absent && !f.hasBase) return `<div class="notice add">${t.rCreated}</div>${plain(f.response)}`;
  const labels = { base: `<span class="pill">${f.absent ? t.beforeTask : t.rCurrent}</span>`, current: `<span class="pill rs">${t.rResp}</span>` };
  const bar = f.absent ? `<div class="notice">${t.rUnseen}</div>` : '';
  if (docMode) return bar + renderDoc(S.chunks, old, f.response, { cols: S.cols, labels, words });
  return bar + renderCode(S.chunks, { cols: S.cols, lang, oldText: old, newText: f.response, fold: kind === 'code', expanded: S.expanded, labels, foldText: t.fold });
}

function renderContent() {
  const sc = $('#sc');
  if (S.missing) { sc.innerHTML = `<div class="notice">${T().noReview} <a href="/" data-go="/">${T().back}</a></div>`; S.contentKey = ''; return; }
  if (!S.file) { sc.innerHTML = ''; S.contentKey = ''; $('#map').innerHTML = ''; return; }
  const f = S.file;
  const key = JSON.stringify([!!S.resp, f.path, f.base, f.current, f.currentDeleted, f.hasBase, f.response, f.responseDeleted, f.state === 'moved', f.baseIsOriginal, f.originSource, S.chunks, S.cols, S.doc, [...S.expanded], S.lang]);
  if (key === S.contentKey) return;
  S.contentKey = key;
  sc.innerHTML = contentHTML();
  S.sel = null; S.menuSel = false;
  shapeMarks(sc);
  resolveBlocks();
  renderMap();
}

// ── список ревью ──

function agentText(r) {
  const t = T();
  if (r.agentLost) return `<span class="err">${t.agentLost}</span>`;
  if (!r.agent) return `<span class="dim">${t.agentNone}</span>`;
  const kind = { claude: 'Claude Code', codex: 'Codex' }[r.agentKind] || r.agentKind || '';
  if (r.stopping) return `<span class="spin"></span> ${kind} · ${t.agentStopping}`;
  if (r.working) return `${SNAKE} ${kind} · <b>${t.agentWorks}</b>`;
  if (r.agentBusy) return `<span class="d-seen"></span> ${kind} · ${t.agentBusy}`;
  return `<span class="d-seen"></span> ${kind} · ${t.agentWaits}`;
}

function listHTML() {
  const t = T();
  const list = S.reviews || [];
  if (!list.length) return `<div class="list"><div class="lh"><b>${t.reviews}</b></div><p class="empty">${S.reviews ? t.noReviews : ''}</p></div>`;
  S.listSel = Math.min(S.listSel, list.length - 1);
  const rows = list.map((r, i) => {
    const c = counts(shown(r.files));
    const mk = r.accepted ? `<span class="mk okc"><span class="d-ok">✓</span> ${t.acceptedAll}</span>`
      : r.hasResponse ? `<span class="mk rpl">${RIPPLE(false, r.working)} ${t.unviewed}</span>` : '';
    return `<div class="lr${i === S.listSel ? ' on' : ''}" data-go="/r/${encodeURIComponent(r.id)}" data-i="${i}"><span class="cur">›</span><span class="nm">${esc(r.title || r.id)}</span><span>${esc(fmtDate(r.created, S.lang))}</span><span class="pr"><span><b>${c.done}</b>/${c.total}</span><span class="pbar"><i style="width:${c.total ? (c.done / c.total) * 100 : 0}%"></i></span></span><span class="ag">${agentText(r)}</span>${mk || '<span></span>'}</div>`;
  }).join('');
  return `<div class="list"><div class="lh"><b>${t.reviews}</b> ${list.length} · ${t.newestFirst}</div><div class="cols"><span></span><span>${t.colTitle}</span><span>${t.colCreated}</span><span>${t.colAccepted}</span><span>${t.colAgent}</span><span></span></div>${rows}</div>`;
}

// ── строка состояния ──

function renderStatus() {
  const t = T();
  const id = routeId();
  const seg = (h, cls = '') => `<span class="seg ${cls}">${h}</span>`;
  let mode = [t.modeRead, ''];
  if (!S.online) mode = [t.modeOff, 'off'];
  else if (S.overlay === 'menu') mode = ['␣ ' + t.modeMenu, 'lead'];
  else if (S.overlay === 'help') mode = ['? ' + t.modeHelp, 'help'];
  else if (!id) mode = [t.modeList, ''];
  else if (S.edit) mode = [t.modeEdit, 'edm'];
  else if (S.resp) mode = [t.modeResp, 'rspm'];
  let h = seg(mode[0], 'mode ' + mode[1]);
  if (!id) {
    h += seg(t.reviews.toLowerCase(), 'file') + '<span class="sp"></span>';
    h += seg(`<span class="k">j</span> <span class="k">k</span> · <span class="k">↵</span> · <span class="k">?</span> ${t.keys}`, 'click" data-act="help');
    $('#status').innerHTML = h;
    return;
  }
  if (S.file) {
    const rel = relPath(S.file.path);
    const sl = rel.lastIndexOf('/');
    h += seg(`<span class="dir">${esc(rel.slice(0, sl + 1))}</span>${esc(rel.slice(sl + 1))}`, 'file');
  }
  if (S.sync) h += seg(`<span class="spin"></span> ${t.syncing} <b>${S.sync.done}</b>/${S.sync.total}`);
  if (S.toast && S.toast.short) h += seg(`<span class="${S.toast.ok ? 'ok' : 'err'}">${esc(S.toast.short)}</span>`);
  h += '<span class="sp"></span>';
  if (S.review && pendingResp() && respStale()) h += `<span class="seg rsp" data-act="response">${RIPPLE(false)} ${t.rStaleShort} · ␣r</span>`;
  else if (S.review && pendingResp() && !S.resp) h += S.review.working
    ? `<span class="seg rsp quiet" data-act="response">${RIPPLE(false)} ${t.rHintQuiet} · ␣r</span>`
    : `<span class="seg rsp" data-act="response">${RIPPLE(false)} ${t.rHint} · ␣r</span>`;
  if (S.review) {
    const c = counts(shown(S.review.files));
    const uns = S.resp ? (pendingResp() || {}).unsent : S.review.unsent; // в окне — написанное в окне
    if (uns) h += `<span class="seg uns" data-act="send">${t.unsentN} <b>${uns}</b> · ␣s</span>`;
    h += seg(`${S.lang === 'ru' ? 'принято' : 'accepted'} <b>${c.done}</b>/${c.total} <span class="pbar"><i style="width:${c.total ? (c.done / c.total) * 100 : 0}%"></i></span>`);
    h += seg(agentText(S.review) + (canInterrupt() ? `<button class="kb int" data-act="interrupt">${t.interrupt} <kbd>␣u</kbd></button>` : ''));
  }
  if (S.edit) {
    h += seg(editPos(), 'pos');
    h += seg(`<span class="k">⌘↵</span> ${t.eHint} · <span class="k">esc</span> ${t.eHintEsc}`);
    $('#status').innerHTML = (S.sync ? `<span class="sync" style="width:${(S.sync.done / Math.max(1, S.sync.total)) * 100}%"></span>` : '') + h;
    return;
  }
  h += seg(position(), 'pos');
  if (S.resp) h += seg(`<span class="k">␣a</span> ${t.rAccept} · <span class="k">␣x</span> ${t.rReject} · <span class="k">esc</span> ${t.rClose}`, 'click" data-act="menu');
  else h += seg(`<span class="k">␣</span> ${t.menu} · <span class="k">?</span> ${t.keys}`, 'click" data-act="menu');
  $('#status').innerHTML = (S.sync ? `<span class="sync" style="width:${(S.sync.done / Math.max(1, S.sync.total)) * 100}%"></span>` : '') + h;
}

function position() {
  const sc = $('#sc');
  if (!S.file || !sc) return '';
  const t = T();
  const parts = [];
  const b = currentBlock();
  if (b >= 0) {
    const bl = S.file.blocks[b];
    parts.push(`${{ top: '⇈', high: '↑', low: '↓' }[bl.level] || '·'} ${esc(bl.title)}`);
  }
  const cg = [...sc.querySelectorAll('.cg')];
  if (cg.length) {
    const y = sc.scrollTop + sc.clientHeight / 3;
    const i = cg.filter((e) => top(e) <= y).length;
    parts.push(`${t.chg} ${Math.max(i, 0)}/${cg.length}`);
  }
  const max = sc.scrollHeight - sc.clientHeight;
  parts.push(`${max > 0 ? Math.round((sc.scrollTop / max) * 100) : 100}%`);
  return parts.join(' · ');
}

// ── всплывающее ──

function renderOverlays() {
  const t = T();
  let h = '';
  if (S.overlay === 'menu' && S.resp) {
    const it = (k, ds, now, later) => `<div class="it${later ? ' later' : ''}" ${later ? '' : `data-key="${k}"`}><span class="key">${k}</span><span class="ar">→</span><span class="ds">${ds}</span>${now ? `<span class="now">${now}</span>` : ''}</div>`;
    h += `<div class="wk"><div class="top"><span class="crumb">␣</span><span>${t.rTitle}${S.file ? ' · ' + esc(relPath(S.file.path)) : ''}</span><span class="sp"></span><kbd>esc</kbd> ${t.close}</div>
<div class="grid"><div><div class="grp">${t.menuResp}</div>${it('a', t.rAccept)}${it('x', t.rReject)}${it('r', t.rClose)}${it('s', t.menuSend, (pendingResp() || {}).unsent ? t.menuSendNow(pendingResp().unsent) : '', !(pendingResp() || {}).unsent)}</div>
<div><div class="grp">${t.menuFile}</div>${it('c', t.menuComment, t.menuCommentNow, !canComment())}</div></div></div>`;
  } else if (S.overlay === 'menu') {
    const it = (k, ds, now, later) => `<div class="it${later ? ' later' : ''}" ${later ? '' : `data-key="${k}"`}><span class="key">${k}</span><span class="ar">→</span><span class="ds">${ds}</span>${now ? `<span class="now">${now}</span>` : ''}</div>`;
    const file = S.file && !isAccepted(S.file) && S.file.state !== 'moved' ? it('a', t.menuAccept, t.menuAcceptNext) : '';
    const r = S.review || {};
    h += `<div class="wk"><div class="top"><span class="crumb">␣</span><span>${t.menuTitle}${S.file ? ' · ' + esc(relPath(S.file.path)) : ''}</span><span class="sp"></span><kbd>esc</kbd> ${t.close}</div>
<div class="grid" style="grid-template-columns:repeat(3,max-content)"><div><div class="grp">${t.menuFile}</div>${file || `<div class="it later"><span class="key">a</span><span class="ar">→</span><span class="ds">${t.menuAccept}</span><span class="now">${S.file && isAccepted(S.file) ? '✓ ' + t.accepted : ''}</span></div>`}${it('c', t.menuComment, t.menuCommentNow, !canComment())}${it('e', t.menuEdit, t.menuEditNow, !canEdit())}${it('b', t.menuRevert, t.menuRevertNow, !canEdit(true))}</div>
<div><div class="grp">${t.menuAgent}</div>${it('s', t.menuSend, r.unsent ? t.menuSendNow(r.unsent) : '', !r.unsent)}${it('u', t.menuInterrupt, t.menuInterruptNow, !canInterrupt())}</div>
<div><div class="grp">${t.menuResp}</div>${it('r', t.menuRespOpen, '', !pendingResp())}</div></div></div>`;
  }
  if (S.overlay === 'help') {
    const k = (keys, ds) => `<div class="k"><span class="ks">${keys.map((x) => `<kbd>${x}</kbd>`).join('')}</span><span class="ds">${ds}</span></div>`;
    h += `<div class="dimmer" data-act="close"></div><div class="keys"><div class="h"><b>${t.allKeys}</b><span class="sp"></span><kbd>esc</kbd> ${t.close}</div><div class="g">
<div><div class="grp">${t.reading}</div>${k(['j', 'k'], t.kScroll)}${k(['↓', '↑'], t.kScroll)}${k(['gg', 'G'], t.kTopBottom)}</div>
<div><div class="grp">${t.navigation}</div>${k([']c', '[c'], t.kChange)}${k([']f', '[f'], t.kFile)}${k([']b', '[b'], t.kBlock)}</div>
<div><div class="grp">${t.actions}</div>${k(['␣'], t.kMenu)}${k(['␣a'], t.kAccept)}${k(['␣e'], t.kEdit)}${k(['␣b'], t.kRevert)}${k(['?'], t.kHelp)}${k(['esc'], t.kEsc)}</div>
<div><div class="grp">${t.rHint}</div>${k(['␣r'], t.kResp)}${k(['␣a'], t.kRespAccept)}${k(['␣x'], t.kRespReject)}${k([']f', '[f'], t.kRespFile)}${k(['␣s'], t.kRespSend)}</div>
<div><div class="grp">${t.view}</div>${k(['␣v'], t.kCols)}${k(['␣m'], t.kDoc)}</div>
<div><div class="grp">${t.comments}</div>${k(['gc'], t.kComment)}${k(['␣c'], t.kCommentAny)}${k(['␣s'], t.kSend)}${k(['␣u'], t.kInterrupt)}${k([']n', '[n'], t.kNextComment)}</div>
<div><div class="grp">${t.selected}</div>${k(['↵'], t.kCardEnter)}${k(['x'], t.kCardX)}${k(['1', '2', '3'], t.kCardNum)}</div>
<div><div class="grp">${t.reviews.toLowerCase()}</div>${k(['j', 'k', '↵'], t.kList)}</div>
</div></div>`;
  }
  if (S.overlay === 'reject') {
    const p = pendingResp() || {};
    const created = (S.review.files || []).filter((f) => f.absent && (p.files || []).includes(f.path)).map((f) => `<code>${esc(f.path.split('/').pop())}</code>`);
    const mine = respMine();
    const disk = (S.review.diskChanged || []).filter((x) => (p.files || []).includes(x)).map((x) => `<code>${esc(x.split('/').pop())}</code>`);
    h += `<div class="dimmer" data-act="close"></div><div class="off-card"><div class="h"><span class="pt"></span>${t.rRejTitle}</div><p>${t.rRejText(created.join(', '), p.answers || 0)}</p>${disk.length ? `<p>${t.rRejDisk(disk.join(', '))}</p>` : ''}${mine ? `<p class="strong">${t.rRejMine(mine)}</p>` : ''}<div class="ft"><button class="btn err" data-act="reject-go">${t.rRejGo} <kbd>↵</kbd></button><span><kbd>esc</kbd> ${t.rRejNo}</span></div></div>`;
  }
  const card = (dot, title, text, go, goText, noText) => `<div class="dimmer" data-act="close"></div><div class="off-card"><div class="h"><span class="pt ${dot}"></span>${title}</div><p>${text}</p><div class="ft"><button class="btn err" data-act="${go}">${goText} <kbd>↵</kbd></button><span><kbd>esc</kbd> ${noText}</span></div></div>`;
  if (S.overlay === 'editDiscard') h += card('o', t.eDiscardTitle, t.eDiscardText, 'edit-discard-go', t.eDiscardGo, t.eDiscardNo);
  if (S.overlay === 'editMeanwhile') h += card('o', t.eMeanTitle, t.eMeanText, 'edit-mean-go', t.eMeanGo, t.eMeanNo);
  if (S.overlay === 'editWork') h += card('w', t.eWorkTitle, t.eWorkText, 'edit-work-go', t.eWorkGo, t.eWorkNo);
  if (S.overlay === 'revert' && S.file) {
    const f = S.file;
    const base = f.baseIsOriginal ? ` («${t.beforeTask}»)` : '';
    h += card('w', esc(t.revTitle(f.path.split('/').pop())), t.revText(base), 'revert-go', t.revGo, t.revNo);
  }
  if (S.overlay === 'interrupt') {
    h += `<div class="dimmer" data-act="close"></div><div class="off-card"><div class="h"><span class="pt"></span>${t.intTitle}</div><p>${t.intText}</p><div class="ft"><button class="btn err" data-act="interrupt-go">${t.intGo} <kbd>↵</kbd></button><span><kbd>esc</kbd> ${t.intNo}</span></div></div>`;
  }
  if (S.review && S.review.stopping && routeId()) {
    const late = S.stopSince && Date.now() - S.stopSince >= 10000;
    h += `<div class="toast"><span class="x w"><span class="spin"></span></span><div class="t1">${t.stopTitle}</div><div class="t2">${t.stopText}</div>${late ? `<div class="t3"><button class="btn2" data-act="restore">${t.restoreAnyway}</button></div>` : ''}</div>`;
  } else if (S.toast && S.toast.title) {
    const icon = S.toast.icon || (S.toast.ok ? '✓' : '✕');
    h += `<div class="toast${S.toast.ok ? ' ok' : ''}"><span class="x${S.toast.cls ? ' ' + S.toast.cls : ''}">${icon}</span><div class="t1">${esc(S.toast.title)}</div>${S.toast.text ? `<div class="t2">${S.toast.text}</div>` : ''}<div class="t3">${S.toast.btn || ''}<span><kbd>esc</kbd> ${t.close}</span></div></div>`;
  }
  if (!S.online) {
    h += `<div class="dimmer"></div><div class="off-card"><div class="h"><span class="pt"></span>${t.offTitle}</div><p>${t.offText}</p><div class="ft"><span class="spin"></span> ${t.offWait}</div></div>`;
  }
  $('#over').innerHTML = h;
}

// note — короткое «готово» в строке состояния; подсказку с текстом (например, о правке с диска) не перекрывает.
function note(short) {
  if (S.toast && S.toast.title) return;
  toast({ ok: true, short });
}

let toastTimer = 0;
function toast(x) {
  S.toast = x;
  clearTimeout(toastTimer);
  if (x && x.ok) toastTimer = setTimeout(() => { S.toast = null; renderOverlays(); renderStatus(); }, 6000);
  renderOverlays();
  renderStatus();
}

// ── прокрутка и навигация ──

const top = (el) => {
  const sc = $('#sc');
  return el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop;
};

function anchorOf() {
  const sc = $('#sc');
  for (const el of sc.querySelectorAll('[data-n]')) {
    const y = top(el);
    if (y + el.offsetHeight > sc.scrollTop) return { n: +el.dataset.n, off: y - sc.scrollTop };
  }
  return null;
}

function elAtLine(n) {
  let best = null;
  for (const el of $('#sc').querySelectorAll('[data-n]')) {
    if (+el.dataset.n <= n) best = el;
    else break;
  }
  return best;
}

function restoreAnchor(a) {
  const sc = $('#sc');
  const el = elAtLine(a.n);
  if (el) sc.scrollTop = top(el) - a.off;
}

function scrollToLine(n) {
  const sc = $('#sc');
  if (!n) { sc.scrollTop = 0; return; }
  const el = elAtLine(n);
  sc.scrollTop = el ? Math.max(0, top(el) - 60) : 0;
}

let posTimer = 0;
function savePos() {
  clearTimeout(posTimer);
  posTimer = setTimeout(() => {
    if (!S.review || !S.path || S.resp) return;
    const a = anchorOf();
    store.set('pos:' + S.review.id, a ? { path: S.path, n: a.n, off: a.off } : { path: S.path, n: 0 });
  }, 250);
}

function scrollBy(dy) {
  $('#sc').scrollTop += dy;
}

function jumpChange(dir) {
  const sc = $('#sc');
  const cg = [...sc.querySelectorAll('.cg')].map(top);
  const y = sc.scrollTop + 61;
  const target = dir > 0 ? cg.find((v) => v > y + 1) : [...cg].reverse().find((v) => v < y - 1);
  if (target !== undefined) sc.scrollTop = Math.max(0, target - 60);
}

function resolveBlocks() {
  const f = S.file;
  S.blockLines = [];
  if (!f || !f.blocks) return;
  const lines = splitLines(f.current || '');
  S.blockLines = f.blocks.map((b) => {
    let i = lines.indexOf(b.at);
    if (i < 0) i = lines.findIndex((l) => l.trim() === (b.at || '').trim());
    return i + 1;
  });
}

function currentBlock() {
  const sc = $('#sc');
  if (!S.blockLines.length || !sc) return -1;
  const a = anchorOf();
  const n = a ? a.n : 0;
  let cur = -1;
  S.blockLines.forEach((l, k) => { if (l > 0 && l <= n + 2) cur = k; });
  return cur;
}

function jumpBlock(dir) {
  if (!S.blockLines.length) return;
  const cur = currentBlock();
  const next = Math.min(Math.max(cur + dir, 0), S.blockLines.length - 1);
  goBlock(next);
}

function goBlock(k) {
  const n = S.blockLines[k];
  if (n > 0) scrollToLine(n);
}

function jumpFile(dir) {
  if (!S.order.length) return;
  const i = S.order.findIndex((f) => f.path === S.path);
  for (let s = 1; s <= S.order.length; s++) {
    const f = S.order[(i + dir * s + S.order.length * 2) % S.order.length];
    if (!isAccepted(f) && f.state !== 'moved' && f.path !== S.path) { openFile(f.path); return; }
  }
}

function renderMap() {
  const sc = $('#sc');
  const map = $('#map');
  const H = sc.scrollHeight;
  if (!S.file || H <= 0) { map.innerHTML = ''; return; }
  const segs = [];
  for (const el of sc.querySelectorAll('.chg.del, .chg.add, .chg.mix, .ln.del, .ln.add, .rr .tx.del, .rr .tx.add, .r .b.del, .r .b.add')) {
    const c = el.matches('.del, .del *') || el.classList.contains('del') ? 'd' : 'a';
    const y = top(el);
    const last = segs[segs.length - 1];
    if (last && last.c === c && y - (last.y + last.h) < 4) last.h = Math.max(last.h, y + el.offsetHeight - last.y);
    else segs.push({ c, y, h: el.offsetHeight });
  }
  map.innerHTML = '<span class="vp"></span>' + segs.map((s) => `<i class="${s.c}" style="top:${(s.y / H) * 100}%;height:${Math.max(0.4, (s.h / H) * 100)}%"></i>`).join('');
  mapViewport();
}

function mapViewport() {
  const sc = $('#sc');
  const vp = $('#map .vp');
  if (!vp) return;
  const H = sc.scrollHeight;
  vp.style.top = (sc.scrollTop / H) * 100 + '%';
  vp.style.height = (sc.clientHeight / H) * 100 + '%';
}

// ── действия ──

async function accept() {
  const f = S.file;
  if (!f || !S.review) return;
  if (isAccepted(f)) { jumpFile(1); return; }
  const t = T();
  try {
    await act('accept-file', { path: f.path });
  } catch (e) {
    const text = { 'unviewed-response': t.errUnviewed, 'disk-changed': t.errDisk, moved: t.errMoved }[e.code] || esc(e.message || t.errNet);
    toast({ title: t.denied, text, short: `✕ ${t.denied}`, ...(e.code === 'unviewed-response' ? openRespBtn() : {}) });
    return;
  }
  toast(null);
  await loadReview(S.review.id);
  const i = S.order.findIndex((x) => x.path === f.path);
  for (let s = 1; s < S.order.length; s++) {
    const n = S.order[(i + s) % S.order.length];
    if (!isAccepted(n) && n.state !== 'moved') { await openFile(n.path); return; }
  }
  await openFile(f.path, { keep: true });
  summary();
}

// summary — всё принято: ревью принято целиком или что ещё мешает.
function summary() {
  const t = T();
  const r = S.review;
  if (r.accepted) { toast({ ok: true, title: t.allAccepted, short: `✓ ${t.allAccepted}` }); return; }
  const left = [];
  if (r.unsent) left.push(t.leftUnsent(r.unsent));
  if (r.questions) left.push(t.leftQuestions(r.questions));
  if (r.hasResponse) left.push(t.leftResponse);
  if (r.diskChanged && r.diskChanged.length) left.push(t.leftDisk(r.diskChanged.length));
  if (r.working) left.push(t.leftWorking);
  toast({ ok: true, title: t.filesAccepted, text: esc(left.join(' · ')), short: `✓ ${t.filesAccepted}` });
}

function toggleCols() {
  S.cols = S.cols === 2 ? 1 : 2;
  store.set('cols', S.cols);
  rerenderKeep();
}

function toggleDoc() {
  if (!S.file || fileKind(S.file.path) !== 'markdown') return;
  S.doc = !S.doc;
  store.set('doc', S.doc);
  rerenderKeep();
}

function rerenderKeep() {
  const a = anchorOf();
  renderAll();
  if (a) restoreAnchor(a);
  renderStatus();
}

function closeOverlay() {
  S.overlay = '';
  renderOverlays();
  renderStatus();
}

// ── комментарии ──

function canComment() {
  const f = S.file;
  if (S.resp) return !!(S.review && f && !f.responseDeleted && (f.response != null || !f.currentDeleted));
  return !!(S.review && f && f.state !== 'moved' && !f.currentDeleted);
}

function canInterrupt() {
  const r = S.review;
  return !!(r && !r.stopping && (r.agentLost || r.files.some((f) => f.agentWorking)));
}

function openComments() {
  const f = S.file;
  const list = f && f.state !== 'moved' ? f.comments || [] : [];
  const p = pendingResp();
  return shownComments(list, { resp: !!S.resp, pending: p ? p.id : 0 });
}
const commentById = (id) => openComments().find((c) => c.id === id);

function renderBanner() {
  const el = $('#banner');
  const r = S.review;
  if (!r || !r.agentLost || r.stopping) { el.innerHTML = ''; return; }
  const t = T();
  el.innerHTML = `<span class="pt"></span><span><b>${t.lostTitle}</b> ${t.lostText}</span><span class="sp"></span><button class="btn err" data-act="interrupt">${t.intGo} <kbd>␣u</kbd></button>`;
}

// keepEditor / restoreEditor — окно комментария переживает перерисовку: текст, фокус, курсор.
function keepEditor() {
  const ta = document.querySelector('#ced textarea');
  if (!ta || !S.ed || S.ed.ask) return;
  S.ed.text = ta.value;
  S.ed.focus = document.activeElement === ta;
  S.ed.caret = [ta.selectionStart, ta.selectionEnd];
}

function restoreEditor() {
  const ta = document.querySelector('#ced textarea');
  if (!ta || !S.ed) return;
  ta.value = S.ed.text || '';
  if (S.ed.ask) return;
  if (S.ed.focus !== false) {
    ta.focus({ preventScroll: true });
    const [a, b] = S.ed.caret || [ta.value.length, ta.value.length];
    ta.setSelectionRange(a, b);
  }
  ta.oninput = () => { S.ed.text = ta.value; };
}

function renderComments() {
  const sc = $('#sc');
  if (!S.file || !routeId()) return;
  keepEditor();
  const t = T();
  const list = commentOrder(openComments());
  if (S.act && !list.some((c) => c.id === S.act)) { S.act = null; S.ask = null; }
  const items = list.map((c, i) => {
    const mine = S.ed && S.ed.id === c.id;
    let html = cardHTML(c, {
      t, n: i + 1, on: S.act === c.id, inResp: !!S.resp,
      edit: mine && S.ed.kind === 'edit' ? editorHTML(S.ed, t) : '',
      reply: mine && S.ed.kind === 'reply' ? editorHTML(S.ed, t) : '',
    });
    if (S.ask && S.ask.id === c.id) {
      const q = S.ask.kind === 'draft' ? t.cDeleteReplyQ : t.cDeleteQ;
      html = html.replace(/<div class="acts">[\s\S]*?<\/div><\/div>$/, `<div class="ced incard"><div class="ft ask"><span class="q">${q}</span><span class="sp"></span><button class="btn err" data-cact="delete-go">${t.cDelete} <kbd>↵</kbd></button><button class="btn2" data-cact="delete-no">${t.cCancel} <kbd>esc</kbd></button></div></div></div>`);
    }
    return { c, html };
  });
  const current = S.resp ? respNew(S.file) : S.file.current || '';
  placeCards(sc, items, current);
  S.ranges = anchorRanges(sc, list, current);
  paintAnchors(S.ranges, list, S.act);
  sc.querySelectorAll(':scope > .ced, :scope > .smenu').forEach((e) => e.remove());
  if (S.ed && S.ed.kind === 'new') {
    if (S.ed.whole) {
      const box = sc.querySelector('.doc, .code, .two, .code2');
      if (box) {
        const d = document.createElement('div');
        d.className = `cc-slot cc-whole ${box.classList.contains('doc') ? 'cc-doc' : 'cc-code'}`;
        d.innerHTML = editorHTML(S.ed, t);
        const hd = box.querySelector(':scope > .hd');
        const first = box.querySelector(':scope > .cc-whole');
        if (first) first.before(d); else if (hd) hd.after(d); else box.prepend(d);
      }
    } else {
      sc.insertAdjacentHTML('beforeend', editorHTML(S.ed, t));
      const e = sc.lastElementChild;
      e.style.left = S.ed.x + 'px';
      e.style.top = S.ed.y + 'px';
    }
  }
  const ls = drawSelection(sc, S.sel && S.sel.range && !S.sel.range.collapsed ? S.sel.range : null);
  if (S.sel) S.sel.lines = ls.length ? ls : S.sel.lines;
  if (S.sel && S.menuSel && !S.ed && ls.length) {
    const last = ls[ls.length - 1];
    sc.insertAdjacentHTML('beforeend', `<div class="smenu" data-act="comment"><span class="ic">✎</span>${t.cComment} <kbd>gc</kbd></div>`);
    const m = sc.lastElementChild;
    m.style.left = Math.max(8, Math.min(last.r - 60, sc.clientWidth - 200)) + 'px';
    m.style.top = last.b + 6 + 'px';
  }
  restoreEditor();
}

// Окно нового комментария: к выделенному или, без выделения, ко всему файлу.
function openNew() {
  if (!canComment() || staleDenied()) return;
  const f = S.file;
  if (S.ed) { document.querySelector('#ced textarea')?.focus(); return; }
  if (!S.sel) {
    S.ed = { kind: 'new', whole: true, text: '', images: [] };
    renderComments();
    document.querySelector('#ced')?.scrollIntoView({ block: 'nearest' });
    return;
  }
  const tg = S.resp
    ? target(S.sel.a, S.sel.b, respOld(f), respNew(f), f.response == null ? [] : S.chunks || [])
    : target(S.sel.a, S.sel.b, f.base || '', f.current || '', S.chunks || []);
  if (!tg) return;
  const sc = $('#sc');
  const ls = S.sel.lines || [];
  const last = ls[ls.length - 1] || { b: sc.scrollTop + 40 };
  const left = Math.min(...ls.map((x) => x.l), sc.clientWidth - 460);
  S.ed = { kind: 'new', target: tg, text: '', images: [], x: Math.max(16, left), y: last.b + 8 };
  S.menuSel = false;
  renderComments();
}

function openEdit(c) {
  const { fresh, draft, last } = threadState(c);
  if (!fresh && !draft) return;
  const m = fresh ? c.thread[0] : last;
  S.ed = { kind: 'edit', id: c.id, draft, text: m.text, images: [...(m.images || [])] };
  S.act = c.id;
  renderComments();
}

function openReply(c) {
  S.ed = { kind: 'reply', id: c.id, text: '', images: [] };
  S.act = c.id;
  renderComments();
}

function closeEditor() {
  S.ed = null;
  renderComments();
}

function escEditor() {
  keepEditor();
  const ed = S.ed;
  if (ed.ask) { ed.ask = null; ed.focus = true; renderComments(); return; }
  if (!(ed.text || '').trim() && !(ed.images || []).length) { if (ed.kind === 'new') S.sel = null; closeEditor(); return; }
  ed.ask = 'discard';
  renderComments();
}

async function saveEditor() {
  keepEditor();
  const ed = S.ed;
  const t = T();
  const text = (ed.text || '').trim();
  if (!text && !(ed.images || []).length) return;
  try {
    if (ed.kind === 'new') {
      const b = { path: S.file.path, text, images: ed.images, inResponse: !!S.resp };
      if (ed.target && ed.target.range) b.range = ed.target.range;
      if (ed.target && ed.target.was) { b.range = { start: ed.target.at, end: ed.target.at }; b.was = ed.target.was; }
      const r = await act('comment', b);
      S.act = r.id;
    } else if (ed.kind === 'edit') {
      await act('comment-edit', { id: ed.id, text, images: ed.images });
    } else {
      await act('reply', { id: ed.id, text, images: ed.images });
    }
  } catch (e) {
    commentError(e);
    return;
  }
  S.ed = null;
  S.sel = null;
  S.menuSel = false;
  await reloadFile();
}

async function reloadFile() {
  if (!S.review) return;
  await loadReview(S.review.id);
  if (S.path) await openFile(S.path, { keep: true });
}

function commentError(e) {
  const t = T();
  const map = {
    'unviewed-response': [t.unviewedTitle, t.errUnviewedSend],
    'no-agent': null,
    stopping: [t.sendDenied, t.errStopping],
    editing: [t.sendDenied, t.errEditing],
    'disk-changed': [t.sendDenied, t.errDiskSend],
    'comment-status': [t.errSave, t.errStatus],
  };
  const m = map[e.code] || [t.errSave, esc(e.message || t.errNet)];
  toast({ title: m[0], text: m[1], short: `✕ ${m[0]}`, ...(e.code === 'unviewed-response' && !S.resp ? openRespBtn() : {}) });
}

async function cardAction(kind, c, i) {
  if (kind !== 'delete-no' && staleDenied()) return;
  const { fresh, draft, ask } = threadState(c);
  try {
    switch (kind) {
      case 'edit': openEdit(c); return;
      case 'reply': openReply(c); return;
      case 'delete':
        if (fresh || draft) { S.ask = { id: c.id, kind: draft ? 'draft' : 'comment' }; renderComments(); }
        return;
      case 'delete-no': S.ask = null; renderComments(); return;
      case 'delete-go': S.ask = null; await act('comment-delete', { id: c.id }); break;
      case 'dismiss': if (c.status === 'question') await act('dismiss', { id: c.id }); break;
      case 'opt': {
        const opts = ask >= 0 ? c.thread[ask].options || [] : [];
        if (i >= opts.length) { openReply(c); return; }
        if (draft) await act('comment-edit', { id: c.id, text: opts[i], images: [] });
        else await act('reply', { id: c.id, text: opts[i], images: [] });
        break;
      }
      default: return;
    }
  } catch (e) {
    commentError(e);
    return;
  }
  await reloadFile();
}

// Главное действие выбранного комментария (Enter): свой неотправленный — править, иначе — ответить.
function cardPrimary(c) {
  const { fresh, draft } = threadState(c);
  if (fresh || draft) openEdit(c);
  else if (c.status === 'question' || c.status === 'answered') openReply(c);
}

function selectComment(id, scroll) {
  S.act = id;
  S.ask = null;
  renderComments();
  if (!scroll) return;
  const sc = $('#sc');
  const r = S.ranges.get(id);
  const el = r || sc.querySelector(`.cc[data-cid="${CSS.escape(id)}"]`);
  if (!el) return;
  const y = el.getBoundingClientRect().top - sc.getBoundingClientRect().top;
  if (y < 60 || y > sc.clientHeight - 160) sc.scrollTop += y - 160;
}

function jumpComment(dir) {
  const list = commentOrder(openComments());
  if (!list.length) return;
  let i = list.findIndex((c) => c.id === S.act);
  if (i < 0) i = dir > 0 ? -1 : 0;
  selectComment(list[(i + dir + list.length) % list.length].id, true);
}

async function send() {
  const t = T();
  const r = S.review;
  if (!r) return;
  if (staleDenied()) return;
  const inResp = !!S.resp;
  const n = inResp ? (pendingResp() || {}).unsent || 0 : r.unsent;
  try {
    await act('send', inResp ? { inResponse: true } : {});
  } catch (e) {
    if (e.code === 'no-agent') {
      const link = `${location.origin}/r/${encodeURIComponent(r.id)}`;
      toast({ title: t.noAgentTitle, text: `${t.noAgentText}<br><code>${esc(link)}</code>`, short: `✕ ${t.noAgentTitle}`, btn: `<button class="btn" data-act="copylink" data-link="${esc(link)}">${t.copyLink} <kbd>↵</kbd></button>`, enter: 'copylink' });
    } else if (e.code === 'nothing-to-send') {
      toast({ ok: true, short: t.nothingToSend });
    } else commentError(e);
    return;
  }
  if (inResp) {
    // Ответ остаётся непросмотренным, окно — открытым: агент работает поверх него.
    await reloadFile();
    toast({ ok: true, title: t.sent(n), text: t.sentResp, short: `✓ ${t.sent(n)}` });
    return;
  }
  await loadReview(r.id);
  toast({ ok: true, title: t.sent(n), text: S.review.agentBusy || S.review.working ? t.sentWait : t.sentWork, short: `✓ ${t.sent(n)}` });
  if (S.path) await openFile(S.path, { keep: true });
}

function askInterrupt() {
  if (!canInterrupt()) return;
  S.overlay = 'interrupt';
  renderOverlays();
  renderStatus();
}

async function interrupt() {
  closeOverlay();
  try { await act('interrupt'); } catch (e) { commentError(e); return; }
  await reloadFile();
}

// ── правка владельца ──
// Окно с исходником текущей версии поверх места чтения (как окно ответа). Под исходником — его
// подсветка, сверху — прозрачная textarea: переносы строк у них одинаковые, номера строк у подсветки.

// canEdit — файл можно править (revert — вернуть к базе). Запреты ядра (ответ, агент) объясняются при нажатии.
function canEdit(revert) {
  const f = S.file;
  if (!S.review || !f || S.resp || f.state === 'moved') return false;
  if (!revert) return !f.currentDeleted;
  return !!f.hasBase && (!!f.currentDeleted || f.current !== f.base);
}

// changeAllowed — менять текущую версию сейчас можно; иначе duet объясняет или предлагает прервать агента.
function changeAllowed(title, then) {
  const t = T();
  const why = S.review.locked;
  if (why === 'unviewed-response') { toast({ title, text: t.eUnviewed, short: `✕ ${title}`, ...openRespBtn() }); return false; }
  if (why === 'stopping') { toast({ title, text: t.errStopping, short: `✕ ${title}` }); return false; }
  if (why === 'agent-working') { S.overlay = 'editWork'; S.editWork = then; renderOverlays(); renderStatus(); return false; }
  return true;
}

function editError(e, title, then) {
  const t = T();
  if (e.code === 'unviewed-response' || e.code === 'stopping' || e.code === 'agent-working') {
    S.review.locked = e.code;
    changeAllowed(title, then);
    return;
  }
  toast({ title, text: e.code === 'not-found' && S.file && S.file.currentDeleted ? t.eDeletedFile : esc(e.message || t.errNet), short: `✕ ${title}` });
}

async function startEdit() {
  const t = T();
  const f = S.file;
  if (!S.review || !f || S.resp || S.edit || f.state === 'moved') return;
  if (f.currentDeleted) { toast({ title: t.eDenied, text: t.eDeletedFile, short: `✕ ${t.eDenied}` }); return; }
  if (!changeAllowed(t.eDenied, 'edit')) return;
  try { await act('editing', { on: true }); } catch (e) { editError(e, t.eDenied, 'edit'); return; }
  // Пока ядро открывало правку, файл могли перечитать (событие изменения) — берём свежий, но тот же.
  if (!S.file || S.file.path !== f.path || S.resp || S.edit) { act('editing', { on: false }).catch(() => {}); return; }
  const g = S.file;
  const a = anchorOf();
  const cur = g.current || '';
  const kind = fileKind(f.path);
  S.edit = {
    key: f.path + ':' + Date.now(), path: f.path, from: cur, text: cur, kind,
    lang: kind === 'code' ? langOf(f.path) : kind === 'markdown' ? 'md' : '',
    line: a ? a.n - 1 : 0, off: a ? a.off : 0,
    sel: editSelection(S.sel, g.base, cur, S.chunks),
  };
  S.sel = null; S.menuSel = false; S.ed = null; S.act = null; S.ask = null;
  toast(null);
  renderAll();
}

// renderEdit — окно правки. Строится один раз на открытие: перерисовки экрана под ним его не трогают.
function renderEdit() {
  const w = $('#ewrap');
  const on = !!(S.edit && S.review && routeId());
  $('#app').classList.toggle('emode', on);
  if (!on) { if (w.innerHTML) { w.innerHTML = ''; w.dataset.key = ''; } return; }
  if (w.dataset.key === S.edit.key) return;
  const t = T();
  const ed = S.edit;
  const rel = relPath(ed.path);
  w.dataset.key = ed.key;
  w.innerHTML = `<div class="ewin"><div class="ehead"><span class="ttl"><span class="pt"></span>${t.eTitle} · <span class="fn">${esc(rel)}</span></span><span class="meta">${t.eMeta}</span><span class="sp"></span><button class="ract" data-act="edit-cancel">${t.eCancel} <kbd>esc</kbd></button><button class="ract sv" data-act="edit-save">${t.eSave} <kbd>⌘↵</kbd></button></div>
<div class="escroll" id="escroll"><div class="ebox"><div class="ehl" id="ehl" aria-hidden="true"></div><textarea class="eta" id="eta" spellcheck="false" autocomplete="off" autocapitalize="off" autocorrect="off"></textarea></div></div>
<div class="efoot"><span id="epos"></span><span class="sp"></span><span>${t.eUnder} <b>${esc(rel)}</b></span></div></div>`;
  const ta = $('#eta');
  ta.value = ed.text;
  paintEdit();
  let raf = 0;
  ta.addEventListener('input', () => {
    ed.text = ta.value;
    if (!raf) raf = requestAnimationFrame(() => { raf = 0; paintEdit(); });
    renderStatus();
  });
  // Место: та же строка на том же месте экрана, что была в чтении; выделение — то же.
  const sc = $('#escroll');
  const lines = $('#ehl').children;
  const at = (n) => lines[Math.max(0, Math.min(n, lines.length - 1))];
  if (ed.sel) ta.setSelectionRange(ed.sel[0], ed.sel[1]);
  else { const i = ed.text.split('\n').slice(0, ed.line).join('\n').length + (ed.line ? 1 : 0); ta.setSelectionRange(i, i); }
  if (at(ed.line)) sc.scrollTop = Math.max(0, at(ed.line).offsetTop - ed.off);
  if (ed.sel) {
    const el = at(lineOf(ed.text, ed.sel[0]));
    if (el && (el.offsetTop < sc.scrollTop || el.offsetTop > sc.scrollTop + sc.clientHeight - 60)) sc.scrollTop = Math.max(0, el.offsetTop - sc.clientHeight / 3);
  }
  ta.focus({ preventScroll: true });
  renderStatus();
}

function paintEdit() {
  const ehl = $('#ehl');
  if (ehl && S.edit) ehl.innerHTML = editLinesHTML(S.edit.text, S.edit.lang);
}

function editPos() {
  const ta = $('#eta');
  if (!ta || !S.edit) return '';
  const i = ta.selectionStart;
  const before = ta.value.slice(0, i);
  const line = lineOf(ta.value, i);
  return T().eLine(line + 1, i - before.lastIndexOf('\n'));
}
document.addEventListener('selectionchange', () => { if (S.edit && document.activeElement && document.activeElement.id === 'eta') renderStatus(); });

// editTopLine — какая строка исходника сейчас вверху окна правки и на каком она месте.
function editTopLine() {
  const sc = $('#escroll');
  const lines = $('#ehl') ? [...$('#ehl').children] : [];
  for (let k = 0; k < lines.length; k++) {
    if (lines[k].offsetTop + lines[k].offsetHeight > sc.scrollTop) return { n: k + 1, off: lines[k].offsetTop - sc.scrollTop };
  }
  return null;
}

async function ownerSave(force) {
  const t = T();
  const ed = S.edit;
  if (!ed || ed.saving) return;
  const ta = $('#eta');
  if (ta) ed.text = ta.value;
  if (ed.text === ed.from && !force) { await closeEdit(false); return; }
  ed.saving = true;
  try {
    await act('edit', { path: ed.path, content: ed.text, from: ed.from, force: !!force });
  } catch (e) {
    ed.saving = false;
    if (e.code === 'changed-meanwhile') { S.overlay = 'editMeanwhile'; renderOverlays(); renderStatus(); return; }
    toast({ title: t.eSaveErr, text: esc(e.message || t.errNet), short: `✕ ${t.eSaveErr}` });
    return;
  }
  await closeEdit(true);
}

function ownerCancel() {
  const ta = $('#eta');
  if (S.edit && ta) S.edit.text = ta.value;
  if (S.edit && S.edit.text !== S.edit.from) { S.overlay = 'editDiscard'; renderOverlays(); renderStatus(); return; }
  closeEdit(false);
}

// closeEdit — закрыть окно правки; чтение открывается на той строке, что была вверху окна.
async function closeEdit(saved) {
  const ed = S.edit;
  if (!ed) return;
  const topAt = editTopLine();
  S.edit = null;
  S.overlay = '';
  renderEdit();
  try { await act('editing', { on: false }); } catch { /* ядро перезапускается — закроется при входе */ }
  await loadReview(S.review.id);
  if (S.review && visibleFiles().some((f) => f.path === ed.path)) await openFile(ed.path, topAt ? { line: topAt.n, off: topAt.off } : { keep: true });
  else renderAll();
  if (saved) note(`✓ ${T().eSaved}`);
}

function backToEdit() {
  S.overlay = '';
  renderOverlays();
  renderStatus();
  $('#eta')?.focus({ preventScroll: true });
}

function editIndent(ta, back) {
  const unit = indentUnit(ta.value, S.edit.kind);
  const v = ta.value;
  const a = ta.selectionStart, b = ta.selectionEnd;
  const ls = v.lastIndexOf('\n', a - 1) + 1;
  if (a === b && !back) { ta.setRangeText(unit, a, b, 'end'); }
  else {
    // Несколько строк (или Shift+Tab): отступ у каждой строки выделения.
    const le = b > a && v[b - 1] === '\n' ? b - 1 : b;
    const block = v.slice(ls, le);
    const out = block.split('\n').map((l) => (back ? (l.startsWith(unit) ? l.slice(unit.length) : l.replace(/^[ \t]/, '')) : unit + l)).join('\n');
    ta.setRangeText(out, ls, le, 'select');
  }
  ta.dispatchEvent(new Event('input'));
}

async function askRevert() {
  const t = T();
  const f = S.file;
  if (!S.review || !f || S.resp || S.edit || f.state === 'moved') return;
  const deny = (text) => toast({ title: t.revDenied, text, short: `✕ ${t.revDenied}` });
  if (!f.hasBase) { deny(t.revNoBase); return; }
  if (!f.currentDeleted && f.current === f.base) { deny(t.revSame); return; }
  if (!changeAllowed(t.revDenied, 'revert')) return;
  S.overlay = 'revert';
  renderOverlays();
  renderStatus();
}

async function revertGo() {
  const t = T();
  closeOverlay();
  try { await act('revert', { path: S.file.path }); } catch (e) { editError(e, t.revDenied, 'revert'); return; }
  await reloadFile();
  note(`✓ ${t.revDone}`);
}

// «Прервать и править»: агент прерван, а когда он остановится — открыть правку или «вернуть к базе».
async function interruptToEdit() {
  S.editAfterStop = S.editWork || 'edit';
  S.editWork = null;
  closeOverlay();
  try { await act('interrupt'); } catch (e) { S.editAfterStop = null; commentError(e); return; }
  await reloadFile();
}

// Вкладку закрывают с открытой правкой — ядро не должно думать, что редактор ещё открыт.
window.addEventListener('pagehide', () => {
  if (S.edit && S.review) navigator.sendBeacon(`/api/reviews/${rid()}/editing`, new Blob([JSON.stringify({ on: false })], { type: 'application/json' }));
});

// ── окно ответа ──
// Окно — это тот же основной экран в режиме ответа: справа от дерева, поверх затемнения, со своей
// шапкой и низом. Под окном остаётся место чтения (S.resp.back), куда возвращаемся после закрытия.

function renderFrame() {
  const on = !!(S.resp && S.review);
  $('#app').classList.toggle('rmode', on);
  if (!on) { $('#rhead').innerHTML = ''; $('#rfoot').innerHTML = ''; return; }
  const t = T();
  const p = (respStale() ? S.resp.shown : pendingResp()) || { files: [] }; // устаревшее окно — как было открыто
  const kind = { claude: 'Claude Code', codex: 'Codex' }[S.review.agentKind] || S.review.agentKind || '';
  const time = p.at ? new Date(p.at).toLocaleTimeString(S.lang, { hour: '2-digit', minute: '2-digit' }) : '';
  const meta = [kind, time, t.rFiles(p.files.length), p.answers ? t.rAnswers(p.answers, p.questions) : ''].filter(Boolean).join(' · ');
  const stale = respStale();
  const state = stale
    ? `<button class="rstale" data-act="response">${t.rStale} · ${t.rReopen} <kbd>␣r</kbd></button>`
    : S.review.working ? `<span class="rwork">${SNAKE} ${t.rWorkingWin}</span>` : '';
  const off = (x) => (x ? ' off' : '');
  $('#rhead').innerHTML = `<span class="ttl"><span class="pt"></span>${t.rTitle}</span><span class="meta">${esc(meta)}</span>${state}<span class="sp"></span>
<button class="ract no${off(stale || S.review.working)}" data-act="resp-reject">${t.rReject} <kbd>␣x</kbd></button><button class="ract" data-act="resp-close">${t.rClose} <kbd>esc</kbd></button><button class="ract ok${off(stale)}" data-act="resp-accept">${t.rAccept} <kbd>␣a</kbd></button>`;
  const list = respFiles();
  const i = list.findIndex((f) => f.path === S.path) + 1;
  const back = S.resp.back.path && S.review.files.some((f) => f.path === S.resp.back.path) ? relPath(S.resp.back.path) : '';
  $('#rfoot').innerHTML = `<span>${t.rFileN(i, list.length)}</span><span><b>]f</b> <b>[f</b> ${t.rKeyFiles}</span><span><b>]c</b> <b>[c</b> ${t.rKeyChanges}</span><span><b>]n</b> <b>[n</b> ${t.rKeyComments}</span><span class="sp"></span>${back ? `<span>${t.rUnder} <b>${esc(back)}</b></span>` : ''}`;
}

function openRespBtn() {
  return { btn: `<button class="btn" data-act="response">${T().rOpen} <kbd>↵</kbd></button>`, enter: 'response' };
}

async function openResp() {
  const t = T();
  if (!S.review || S.resp) return;
  toast(null);
  if (!pendingResp()) { toast({ ok: true, short: S.review.working ? t.rWorking : t.rNone }); return; }
  S.resp = { back: { path: S.path, anchor: S.path ? anchorOf() : null }, ...respStamp() };
  S.overlay = '';
  rebuildTree();
  const list = respFiles();
  const path = list.some((f) => f.path === S.path) ? S.path : (list[0] || {}).path;
  S.path = null; // файл открывается заново: другой вид, другие отличия
  renderFrame();
  if (path) await openFile(path);
  else renderAll();
}

// closeResp — закрыть окно. path — какой файл открыть (после «принять»), иначе — то, что было под окном.
async function closeResp(path) {
  if (!S.resp) return;
  const back = S.resp.back;
  S.resp = null;
  S.overlay = '';
  if (S.review) await loadReview(S.review.id);
  if (!S.review) { renderAll(); return; }
  const has = (x) => x && visibleFiles().some((f) => f.path === x);
  S.path = null;
  if (has(path)) await openFile(path);
  else if (has(back.path)) await openFile(back.path, back.anchor ? { line: back.anchor.n, off: back.anchor.off } : {});
  else if (S.order[0]) await openFile(S.order[0].path);
  else renderAll();
}

function toggleResp() {
  if (respStale()) reopenResp();
  else if (S.resp) closeResp();
  else openResp();
}

// Какой ответ открыт в окне: если агент закончил и ответ обновился, окно не меняется само.
const respStamp = () => ({ id: (pendingResp() || {}).id, at: (pendingResp() || {}).at, shown: pendingResp() });
const respStale = () => !!(S.resp && pendingResp() && (pendingResp().id !== S.resp.id || pendingResp().at !== S.resp.at));

// Устаревшее окно: читать можно, менять — нет, сначала открыть заново.
function staleDenied() {
  if (!respStale()) return false;
  toast({ title: T().rStale, text: '', short: `✕ ${T().rStaleDenied}` });
  return true;
}

// reopenResp — открыть окно заново на том же файле (или на другом файле ответа — path).
async function reopenResp(path = S.path) {
  if (!S.resp) return;
  toast(null);
  Object.assign(S.resp, respStamp());
  rebuildTree();
  const list = respFiles();
  const p = list.some((f) => f.path === path) ? path : (list[0] || {}).path;
  S.path = null;
  renderFrame();
  if (p) await openFile(p);
  else renderAll();
}

function jumpRespFile(dir) {
  const list = respFiles();
  if (!list.length) return;
  const i = list.findIndex((f) => f.path === S.path);
  const n = list[(i + dir + list.length * 2) % list.length];
  if (respStale()) reopenResp(n.path);
  else if (n.path !== S.path) openFile(n.path);
}

// respMine — сколько написано в окне ответа: комментарии к версии из ответа и ответы в ветках.
const respMine = () => (pendingResp() || {}).mine || 0;

async function acceptResp() {
  if (!S.resp || staleDenied()) return;
  const t = T();
  const path = S.path;
  try { await act('accept-response'); } catch (e) { commentError(e); return; }
  await closeResp(path);
  note(`✓ ${t.rAccepted}`);
}

function askReject() {
  if (!S.resp || staleDenied()) return;
  if (S.review.working) { toast({ ok: true, short: T().rRejWorking }); return; }
  S.overlay = 'reject';
  renderOverlays();
  renderStatus();
}

async function rejectResp() {
  const t = T();
  S.overlay = '';
  try { await act('reject-response'); } catch (e) { commentError(e); renderOverlays(); return; }
  await closeResp();
  note(`✓ ${t.rRejected}`);
}

// Картинки: из буфера (⌘V) или перетащенные в окно.
async function addImages(files) {
  const ed = S.ed;
  if (!ed) return;
  keepEditor();
  for (const f of files) {
    if (!f.type.startsWith('image/')) continue;
    const data = await new Promise((res) => { const r = new FileReader(); r.onload = () => res(r.result); r.readAsDataURL(f); });
    try {
      const out = await api('/api/images', { data });
      ed.images = [...(ed.images || []), out.path];
    } catch (e) { commentError(e); }
  }
  if (S.ed === ed) renderComments();
}

document.addEventListener('paste', (e) => {
  if (!S.ed || !e.target.closest || !e.target.closest('#ced')) return;
  const files = [...(e.clipboardData?.files || [])].filter((f) => f.type.startsWith('image/'));
  if (files.length) { e.preventDefault(); addImages(files); }
});
document.addEventListener('dragover', (e) => { if (S.ed && e.target.closest && e.target.closest('#ced')) e.preventDefault(); });
document.addEventListener('drop', (e) => {
  if (!S.ed || !e.target.closest || !e.target.closest('#ced')) return;
  e.preventDefault();
  addImages([...(e.dataTransfer?.files || [])]);
});

// ── своё выделение ──
// Родного выделения в тексте файла нет: нажатие мыши гасится, duet сам следит за мышью и рисует
// выделение одной фигурой. Поэтому плашки браузера («Перевести» и т. п.) не появляются.

let drag = null;

function caretAt(x, y) {
  if (document.caretPositionFromPoint) {
    const p = document.caretPositionFromPoint(x, y);
    return p ? [p.offsetNode, p.offset] : null;
  }
  const r = document.caretRangeFromPoint && document.caretRangeFromPoint(x, y);
  return r ? [r.startContainer, r.startOffset] : null;
}

const inText = (n) => {
  const el = n && (n.nodeType === 3 ? n.parentElement : n);
  return !!(el && el.closest('#sc') && el.closest('[data-p]') && !el.closest('.cc, .ced, .smenu'));
};

function rangeOf(a, b) {
  const r = document.createRange();
  r.setStart(a[0], a[1]);
  r.collapse(true);
  const p = document.createRange();
  p.setStart(b[0], b[1]);
  if (r.compareBoundaryPoints(Range.START_TO_START, p) <= 0) r.setEnd(b[0], b[1]);
  else { r.setStart(b[0], b[1]); r.setEnd(a[0], a[1]); }
  return r;
}

function setSelection(range) {
  const a = pointPos(range.startContainer, range.startOffset);
  const b = pointPos(range.endContainer, range.endOffset);
  if (!a || !b || !range.toString().trim()) return false;
  S.sel = { range, a, b, lines: [] };
  S.menuSel = canComment();
  S.act = null;
  S.ask = null;
  renderComments();
  return true;
}

function clearSelection() {
  if (!S.sel) return;
  S.sel = null;
  S.menuSel = false;
  renderComments();
}

$('#sc').addEventListener('mousedown', (e) => {
  if (e.button !== 0 || e.target.closest('.cc, .ced, .smenu, .fold, a, button, .notice')) return;
  if (!e.target.closest('.doc, .two, .code, .code2')) return;
  e.preventDefault();
  if (document.activeElement && document.activeElement !== document.body) document.activeElement.blur();
  window.getSelection().removeAllRanges();
  const c = caretAt(e.clientX, e.clientY);
  drag = { a: c && inText(c[0]) ? c : null, b: null, x: e.clientX, y: e.clientY };
});

document.addEventListener('mousemove', (e) => {
  if (!drag || !drag.a) return;
  if (!drag.b && Math.abs(e.clientX - drag.x) + Math.abs(e.clientY - drag.y) < 3) return;
  const c = caretAt(e.clientX, e.clientY);
  if (!c || !inText(c[0])) return;
  drag.b = c;
  const sc = $('#sc');
  sc.querySelectorAll(':scope > .smenu').forEach((m) => m.remove());
  drawSelection(sc, rangeOf(drag.a, c));
});

document.addEventListener('mouseup', (e) => {
  if (!drag) return;
  const d = drag;
  drag = null;
  if (d.a && d.b) {
    const r = rangeOf(d.a, d.b);
    if (!r.collapsed && setSelection(r)) return;
  }
  // Просто клик: по тексту с комментарием — выбрать комментарий.
  if (d.a) {
    for (const [id, r] of S.ranges) {
      try { if (r.isPointInRange(d.a[0], d.a[1])) { S.sel = null; S.menuSel = false; selectComment(id); return; } } catch { /* другой документ */ }
    }
  }
  if (S.sel && !S.ed) clearSelection();
  else if (S.sel && S.ed && S.ed.kind !== 'new') clearSelection();
  else drawSelection($('#sc'), S.sel ? S.sel.range : null);
  if (S.act && !S.ed) { S.act = null; S.ask = null; renderComments(); }
});

$('#sc').addEventListener('dblclick', (e) => {
  const c = caretAt(e.clientX, e.clientY);
  if (!c || c[0].nodeType !== 3 || !inText(c[0])) return;
  const s = c[0].nodeValue;
  const W = /[\p{L}\p{N}_-]/u;
  let a = c[1], b = c[1];
  while (a > 0 && W.test(s[a - 1])) a--;
  while (b < s.length && W.test(s[b])) b++;
  if (b <= a) return;
  const r = document.createRange();
  r.setStart(c[0], a);
  r.setEnd(c[0], b);
  setSelection(r);
});

// Копирование: ⌘C и «Копировать» в меню правой кнопки (на это время выделение отдаём браузеру).
document.addEventListener('contextmenu', () => {
  if (!S.sel || !S.sel.range) return;
  const s = window.getSelection();
  s.removeAllRanges();
  s.addRange(S.sel.range);
});
document.addEventListener('copy', (e) => {
  if (!S.sel || !S.sel.range) return;
  const s = window.getSelection();
  if (!s.isCollapsed && !(s.anchorNode && s.anchorNode.parentElement && s.anchorNode.parentElement.closest('[data-p]'))) return;
  e.clipboardData.setData('text/plain', S.sel.range.toString());
  e.preventDefault();
});

// ── клавиши ──

document.addEventListener('keydown', (e) => {
  const k = e.key;
  // Окно правки: ⌘Enter — сохранить, Esc — отменить (с вопросом, если текст менялся), Tab — отступ.
  if (S.edit && S.overlay !== 'help') {
    if (S.overlay === 'editDiscard' || S.overlay === 'editMeanwhile') {
      e.preventDefault();
      if (k === 'Enter') { if (S.overlay === 'editDiscard') closeEdit(false); else { S.overlay = ''; renderOverlays(); ownerSave(true); } }
      else if (k === 'Escape') backToEdit();
      return;
    }
    if (k === 'Escape') { e.preventDefault(); ownerCancel(); return; }
    if (k === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); ownerSave(false); return; }
    if (k === 'Tab' && e.target.id === 'eta') { e.preventDefault(); editIndent(e.target, e.shiftKey); return; }
    return;
  }
  // Окно комментария: Esc — закрыть (с вопросом, если что-то написано), ⌘Enter — сохранить.
  if (S.ed && S.overlay !== 'help') {
    if (k === 'Escape') { e.preventDefault(); escEditor(); return; }
    if (S.ed.ask && k === 'Enter') { e.preventDefault(); if (S.ed.kind === 'new') S.sel = null; closeEditor(); return; }
    if (k === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); saveEditor(); return; }
  }
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.target.closest && e.target.closest('input, textarea, [contenteditable]')) return;
  const isList = !routeId();
  if (S.overlay === 'menu') {
    e.preventDefault();
    closeOverlay();
    if (S.resp) {
      if (k === 'a') acceptResp();
      else if (k === 'x') askReject();
      else if (k === 'r') toggleResp();
      else if (k === 'c') openNew();
      else if (k === 'v') toggleCols();
      else if (k === 'm') toggleDoc();
      else if (k === 's') send();
      return;
    }
    if (k === 'r') openResp();
    else if (k === 'a') accept();
    else if (k === 'v') toggleCols();
    else if (k === 'm') toggleDoc();
    else if (k === 'e') startEdit();
    else if (k === 'b') askRevert();
    else if (k === 'c') openNew();
    else if (k === 's') send();
    else if (k === 'u') askInterrupt();
    return;
  }
  if (S.overlay === 'reject') {
    e.preventDefault();
    if (k === 'Enter') rejectResp();
    else if (k === 'Escape') closeOverlay();
    return;
  }
  if (S.overlay === 'interrupt') {
    e.preventDefault();
    if (k === 'Enter') interrupt();
    else if (k === 'Escape') closeOverlay();
    return;
  }
  if (S.overlay === 'editWork' || S.overlay === 'revert') {
    e.preventDefault();
    if (k === 'Enter') (S.overlay === 'revert' ? revertGo : interruptToEdit)();
    else if (k === 'Escape') { S.editWork = null; closeOverlay(); }
    return;
  }
  if (S.overlay === 'help') {
    if (k === 'Escape' || k === '?' || k === 'q') { e.preventDefault(); closeOverlay(); }
    return;
  }
  if (k === 'Escape') {
    S.pending = '';
    if (S.ask) { S.ask = null; renderComments(); }
    else if (S.toast) toast(null);
    else if (S.sel) clearSelection();
    else if (S.act) { S.act = null; renderComments(); }
    else if (S.resp) closeResp();
    return;
  }
  if (k === 'Enter' && S.toast && S.toast.enter === 'copylink') { e.preventDefault(); copyLink(); return; }
  if (k === 'Enter' && S.toast && S.toast.enter === 'response') { e.preventDefault(); openResp(); return; }
  if (!S.pending && S.act && !isList) {
    const c = commentById(S.act);
    if (c) {
      if (S.ask) { if (k === 'Enter') { e.preventDefault(); cardAction('delete-go', c); } return; }
      if (k === 'Enter') { e.preventDefault(); cardPrimary(c); return; }
      if (k === 'x') {
        e.preventDefault();
        const st = threadState(c);
        cardAction(st.fresh || st.draft ? 'delete' : c.status === 'question' ? 'dismiss' : '', c);
        return;
      }
      if (/^[1-9]$/.test(k) && c.status === 'question') { e.preventDefault(); cardAction('opt', c, +k - 1); return; }
    }
  }
  if (S.pending) {
    const seq = S.pending + k;
    S.pending = '';
    e.preventDefault();
    if (isList) return;
    if (seq === 'gg') $('#sc').scrollTop = 0;
    else if (seq === ']c') jumpChange(1);
    else if (seq === '[c') jumpChange(-1);
    else if (seq === ']f') (S.resp ? jumpRespFile : jumpFile)(1);
    else if (seq === '[f') (S.resp ? jumpRespFile : jumpFile)(-1);
    else if (seq === ']b') jumpBlock(1);
    else if (seq === '[b') jumpBlock(-1);
    else if (seq === ']n') jumpComment(1);
    else if (seq === '[n') jumpComment(-1);
    else if (seq === 'gc' && S.sel) openNew();
    return;
  }
  if (k === '?') { e.preventDefault(); S.overlay = 'help'; renderOverlays(); renderStatus(); return; }
  if (isList) {
    const n = (S.reviews || []).length;
    if (k === 'j' || k === 'ArrowDown') { e.preventDefault(); S.listSel = Math.min(n - 1, S.listSel + 1); renderAll(); }
    else if (k === 'k' || k === 'ArrowUp') { e.preventDefault(); S.listSel = Math.max(0, S.listSel - 1); renderAll(); }
    else if (k === 'Enter' && n) { e.preventDefault(); go('/r/' + encodeURIComponent(S.reviews[S.listSel].id)); }
    return;
  }
  const sc = $('#sc');
  switch (k) {
    case ' ': e.preventDefault(); if (S.review) { S.overlay = 'menu'; renderOverlays(); renderStatus(); } break;
    case 'g': case ']': case '[': e.preventDefault(); S.pending = k; break;
    case 'G': e.preventDefault(); sc.scrollTop = sc.scrollHeight; break;
    case 'j': case 'ArrowDown': e.preventDefault(); scrollBy(56); break;
    case 'k': case 'ArrowUp': e.preventDefault(); scrollBy(-56); break;
    case 'PageDown': e.preventDefault(); scrollBy(sc.clientHeight * 0.9); break;
    case 'PageUp': e.preventDefault(); scrollBy(-sc.clientHeight * 0.9); break;
    case 'Home': e.preventDefault(); sc.scrollTop = 0; break;
    case 'End': e.preventDefault(); sc.scrollTop = sc.scrollHeight; break;
  }
});

// ── мышь ──

document.addEventListener('click', (e) => {
  const t = e.target;
  const goEl = t.closest('[data-go]');
  if (goEl && !e.metaKey && !e.ctrlKey) { e.preventDefault(); go(goEl.dataset.go); return; }
  const a = t.closest('[data-act]');
  if (S.edit) {
    // Пока открыта правка, другой файл в дереве — то же, что Esc; остальное под окном не нажимается.
    if (a && a.dataset.act === 'close' && S.overlay && S.overlay !== 'help') { backToEdit(); return; }
    if (a && !['edit-save', 'edit-cancel', 'edit-discard-go', 'edit-mean-go', 'lang', 'theme', 'help', 'close'].includes(a.dataset.act)) return;
    if (!a && t.closest('[data-file], [data-block], [data-go]')) { ownerCancel(); return; }
  }
  if (a) {
    switch (a.dataset.act) {
      case 'accept': accept(); break;
      case 'cols': toggleCols(); break;
      case 'doc': toggleDoc(); break;
      case 'lang': S.lang = S.lang === 'ru' ? 'en' : 'ru'; store.set('lang', S.lang); S.contentKey = ''; rerenderKeep(); break;
      case 'theme': S.theme = { system: 'dark', dark: 'light', light: 'system' }[S.theme]; store.set('theme', S.theme); renderAll(); break;
      case 'menu': S.overlay = S.overlay === 'menu' ? '' : 'menu'; renderOverlays(); renderStatus(); break;
      case 'help': S.overlay = 'help'; renderOverlays(); renderStatus(); break;
      case 'close': closeOverlay(); break;
      case 'comment': openNew(); break;
      case 'send': send(); break;
      case 'interrupt': askInterrupt(); break;
      case 'interrupt-go': interrupt(); break;
      case 'restore': act('restore-anyway').catch(commentError); break;
      case 'copylink': copyLink(); break;
      case 'response': toggleResp(); break;
      case 'resp-accept': acceptResp(); break;
      case 'resp-reject': askReject(); break;
      case 'resp-close': closeResp(); break;
      case 'reject-go': rejectResp(); break;
      case 'edit': startEdit(); break;
      case 'edit-save': ownerSave(false); break;
      case 'edit-cancel': ownerCancel(); break;
      case 'edit-discard-go': closeEdit(false); break;
      case 'edit-mean-go': S.overlay = ''; renderOverlays(); ownerSave(true); break;
      case 'edit-work-go': interruptToEdit(); break;
      case 'revert-go': revertGo(); break;
    }
    return;
  }
  const key = t.closest('.wk [data-key]');
  if (key) {
    closeOverlay();
    const k = key.dataset.key;
    if (S.resp) {
      if (k === 'a') acceptResp();
      else if (k === 'x') askReject();
      else if (k === 'r') toggleResp();
      else if (k === 'c') openNew();
      else if (k === 's') send();
      return;
    }
    if (k === 'r') openResp();
    else if (k === 'a') accept();
    else if (k === 'e') startEdit();
    else if (k === 'b') askRevert();
    else if (k === 'c') openNew();
    else if (k === 's') send();
    else if (k === 'u') askInterrupt();
    return;
  }
  const eact = t.closest('[data-eact]');
  if (eact && S.ed) {
    switch (eact.dataset.eact) {
      case 'save': saveEditor(); break;
      case 'cancel': escEditor(); break;
      case 'back': S.ed.ask = null; S.ed.focus = true; renderComments(); break;
      case 'discard': if (S.ed.kind === 'new') S.sel = null; closeEditor(); break;
      case 'unimg': keepEditor(); S.ed.images.splice(+eact.dataset.i, 1); renderComments(); break;
    }
    return;
  }
  const cact = t.closest('[data-cact]');
  if (cact) {
    const c = commentById(cact.closest('.cc')?.dataset.cid);
    if (c) { S.act = c.id; cardAction(cact.dataset.cact, c, +(cact.dataset.i || 0)); }
    return;
  }
  const card = t.closest('.cc');
  if (card && !t.closest('.ced, a')) {
    if (S.act !== card.dataset.cid) selectComment(card.dataset.cid);
    return;
  }
  const dir = t.closest('[data-dir]');
  if (dir) {
    const k = dir.dataset.dir;
    if (S.collapsed.has(k)) S.collapsed.delete(k); else S.collapsed.add(k);
    store.set('collapsed:' + S.review.id, [...S.collapsed]);
    renderTree();
    return;
  }
  const file = t.closest('[data-file]');
  if (file) {
    const p = file.dataset.file;
    if (S.resp && file.classList.contains('untouched')) return;
    if (p === S.path) { S.blocksHidden = !S.blocksHidden; renderTree(); }
    else openFile(p);
    return;
  }
  const blk = t.closest('[data-block]');
  if (blk) { goBlock(+blk.dataset.block); return; }
  const fold = t.closest('[data-fold]');
  if (fold) { S.expanded.add(+fold.dataset.fold); rerenderKeep(); return; }
  if (S.toast && !t.closest('.toast')) toast(null);
});

$('#map').addEventListener('click', (e) => {
  const r = $('#map').getBoundingClientRect();
  const sc = $('#sc');
  sc.scrollTop = ((e.clientY - r.top) / r.height) * sc.scrollHeight - sc.clientHeight / 2;
});

let scrollRaf = 0;
$('#sc').addEventListener('scroll', () => {
  if (scrollRaf) return;
  scrollRaf = requestAnimationFrame(() => {
    scrollRaf = 0;
    mapViewport();
    renderStatus();
    if (S.file && S.file.blocks && S.file.blocks.length && !S.blocksHidden) {
      const cur = currentBlock();
      document.querySelectorAll('#tree .tr.blk').forEach((el) => el.classList.toggle('cur', +el.dataset.block === cur));
    }
    savePos();
  });
});
let resizeTimer = 0;
window.addEventListener('resize', () => {
  renderMap();
  // Строки переносятся иначе: фигуры правок и выделения считаются заново.
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(() => { shapeMarks($('#sc')); renderComments(); }, 120);
});

applyTheme();
connect();
enter();
