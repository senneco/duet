// Веб-клиент duet: список ревью и ревью (дерево, отличия, принятие), клавиши, тема и язык,
// живые обновления. Всё, что решает поведение, делает ядро; клиент показывает и передаёт действия.

import { esc, fileKind, splitLines } from './util.js';
import { langOf } from './syntax.js';
import { dict, defaultLang, fmtDate } from './i18n.js';
import { buildTree, flatten, fileOrder, isAccepted, counts } from './tree.js';
import { renderCode, renderDoc, renderDocPlain, renderLines } from './view.js';

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
  S.review = null; S.missing = false; S.path = null; S.file = null; S.chunks = null; S.contentKey = '';
  S.overlay = ''; S.toast = null; S.sync = null; S.expanded = new Set();
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
  const { tree } = buildTree(S.review.files);
  S.tree = tree;
  S.order = fileOrder(tree);
}

// openReview — открыть ревью: сверить с диском, прочитать, открыть файл.
async function openReview(id) {
  act0(id, 'sync');
  await loadReview(id);
  if (!S.review) { renderAll(); return; }
  const saved = store.get('pos:' + id, null);
  const has = (p) => S.review.files.some((f) => f.path === p);
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
  if (changed) { S.expanded = new Set(); S.blocksHidden = false; if (S.toast && !S.toast.ok) { S.toast = null; renderOverlays(); } }
  S.path = path;
  const anchor = keep ? anchorOf() : null;
  let file, chunks;
  try {
    [file, chunks] = await Promise.all([
      api(`/api/reviews/${rid()}/file?path=${encodeURIComponent(path)}`),
      api(`/api/reviews/${rid()}/diff?path=${encodeURIComponent(path)}`).catch(() => []),
    ]);
  } catch {
    return;
  }
  if (S.path !== path) return; // пока грузили, открыли другой
  S.file = file;
  S.chunks = chunks;
  if (file.state === 'moved' && !S.reviews) await loadList();
  if (file.state === 'new') act('opened', { path }).catch(() => {});
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
    if (S.path && S.review.files.some((f) => f.path === S.path)) await openFile(S.path, { keep: true });
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
    $('#map').innerHTML = '';
    $('#sc').innerHTML = listHTML();
    S.contentKey = '';
    document.title = 'duet';
  } else {
    renderTree();
    renderWinbar();
    renderContent();
    document.title = S.review ? `${S.review.title || S.review.id} — duet` : 'duet';
  }
  renderStatus();
  renderOverlays();
}

const SNAKE = '<span class="dots"><i></i><i></i><i></i><i></i><i></i><i></i></span>';
const RIPPLE = (seen) => `<span class="rip${seen ? ' seen' : ''}"><i></i><i></i><i></i><b></b></span>`;

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
  for (const r of flatten(S.tree, S.collapsed)) {
    if (r.dir) {
      h += `<div class="tr dir" data-dir="${esc(r.key)}"><span class="pad"></span>${ind(r.depth)}<span class="ic">${r.open ? '▾' : '▸'}</span><span class="nm">${esc(r.name)}</span></div>`;
      continue;
    }
    const f = r.file;
    const open = f.path === S.path;
    const gone = ['deleted', 'deleted-accepted', 'moved'].includes(f.state) ? ' gone' : '';
    h += `<div class="tr${open ? ' open' : ''}${gone}" data-file="${esc(f.path)}" title="${esc(f.path)}"><span class="pad"></span>${ind(r.depth)}<span class="ic">${fileIcon(f)}</span><span class="nm">${esc(f.name)}</span><span class="fill"></span>${fileTag(f)}</div>`;
    if (open && !S.blocksHidden && S.file && S.file.path === f.path) {
      const cur = currentBlock();
      for (const [k, b] of (S.file.blocks || []).entries()) {
        const imp = { top: ['⇈', 'i3'], high: ['↑', 'i2'], low: ['↓', 'i1'] }[b.level] || ['·', 'i1'];
        h += `<div class="tr blk${cur === k ? ' cur' : ''}" data-block="${k}"><span class="pad"></span>${ind(r.depth + 1)}<span class="imp ${imp[1]}">${imp[0]}</span><span class="nm">${esc(b.title)}</span></div>`;
      }
    }
  }
  if (!S.review.files.length) h = `<div class="empty">${T().noFiles}</div>`;
  el.innerHTML = h;
  const open = el.querySelector('.tr.open');
  if (open && (open.offsetTop < el.scrollTop || open.offsetTop > el.scrollTop + el.clientHeight - 30)) el.scrollTop = open.offsetTop - 80;
}

function stateText(f) {
  const t = T();
  const parts = [];
  if (f.agentWorking) parts.push(`${SNAKE} ${t.stWorking}`);
  else if (f.ripple) parts.push(`${RIPPLE(true)} ${t.stRipple}`);
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
  el.innerHTML = `<span><span class="dir">${esc(rel.slice(0, slash + 1))}</span><span class="fn">${esc(rel.slice(slash + 1))}</span></span><span class="st">${stateText(f)}</span><span class="sp"></span>${acc}`;
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

function renderContent() {
  const sc = $('#sc');
  if (S.missing) { sc.innerHTML = `<div class="notice">${T().noReview} <a href="/" data-go="/">${T().back}</a></div>`; S.contentKey = ''; return; }
  if (!S.file) { sc.innerHTML = ''; S.contentKey = ''; $('#map').innerHTML = ''; return; }
  const f = S.file;
  const key = JSON.stringify([f.path, f.base, f.current, f.currentDeleted, f.hasBase, f.state === 'moved', f.baseIsOriginal, f.originSource, S.chunks, S.cols, S.doc, [...S.expanded], S.lang]);
  if (key === S.contentKey) return;
  S.contentKey = key;
  sc.innerHTML = contentHTML();
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
    const c = counts(r.files);
    const mk = r.accepted ? `<span class="mk okc"><span class="d-ok">✓</span> ${t.acceptedAll}</span>`
      : r.hasResponse ? `<span class="mk rpl">${RIPPLE(false)} ${t.unviewed}</span>` : '';
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
  if (S.review) {
    const c = counts(S.review.files);
    h += seg(`${S.lang === 'ru' ? 'принято' : 'accepted'} <b>${c.done}</b>/${c.total} <span class="pbar"><i style="width:${c.total ? (c.done / c.total) * 100 : 0}%"></i></span>`);
    h += seg(agentText(S.review));
  }
  h += seg(position(), 'pos');
  h += seg(`<span class="k">␣</span> ${t.menu} · <span class="k">?</span> ${t.keys}`, 'click" data-act="menu');
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
  if (S.overlay === 'menu') {
    const it = (k, ds, now, later) => `<div class="it${later ? ' later' : ''}" ${later ? '' : `data-key="${k}"`}><span class="key">${k}</span><span class="ar">→</span><span class="ds">${ds}</span>${now ? `<span class="now">${now}</span>` : ''}</div>`;
    const file = S.file && !isAccepted(S.file) && S.file.state !== 'moved' ? it('a', t.menuAccept, t.menuAcceptNext) : '';
    h += `<div class="wk"><div class="top"><span class="crumb">␣</span><span>${t.menuTitle}${S.file ? ' · ' + esc(relPath(S.file.path)) : ''}</span><span class="sp"></span><kbd>esc</kbd> ${t.close}</div>
<div class="grid"><div><div class="grp">${t.menuFile}</div>${file || `<div class="it later"><span class="key">a</span><span class="ar">→</span><span class="ds">${t.menuAccept}</span><span class="now">${S.file && isAccepted(S.file) ? '✓ ' + t.accepted : ''}</span></div>`}</div>
<div><div class="grp">${t.menuLater}</div>${it('c', t.menuComment, '', 1)}${it('s', t.menuSend, '', 1)}${it('r', t.menuResponse, '', 1)}${it('e', t.menuEdit, '', 1)}</div></div></div>`;
  }
  if (S.overlay === 'help') {
    const k = (keys, ds) => `<div class="k"><span class="ks">${keys.map((x) => `<kbd>${x}</kbd>`).join('')}</span><span class="ds">${ds}</span></div>`;
    h += `<div class="dimmer" data-act="close"></div><div class="keys"><div class="h"><b>${t.allKeys}</b><span class="sp"></span><kbd>esc</kbd> ${t.close}</div><div class="g">
<div><div class="grp">${t.reading}</div>${k(['j', 'k'], t.kScroll)}${k(['↓', '↑'], t.kScroll)}${k(['gg', 'G'], t.kTopBottom)}</div>
<div><div class="grp">${t.navigation}</div>${k([']c', '[c'], t.kChange)}${k([']f', '[f'], t.kFile)}${k([']b', '[b'], t.kBlock)}</div>
<div><div class="grp">${t.actions}</div>${k(['␣'], t.kMenu)}${k(['␣a'], t.kAccept)}${k(['?'], t.kHelp)}${k(['esc'], t.kEsc)}</div>
<div><div class="grp">${t.view}</div>${k(['␣v'], t.kCols)}${k(['␣m'], t.kDoc)}</div>
<div><div class="grp">${t.reviews.toLowerCase()}</div>${k(['j', 'k', '↵'], t.kList)}</div>
</div></div>`;
  }
  if (S.toast && S.toast.title) {
    h += `<div class="toast${S.toast.ok ? ' ok' : ''}"><span class="x">${S.toast.ok ? '✓' : '✕'}</span><div class="t1">${esc(S.toast.title)}</div>${S.toast.text ? `<div class="t2">${S.toast.text}</div>` : ''}<div class="t3"><span><kbd>esc</kbd> ${t.close}</span></div></div>`;
  }
  if (!S.online) {
    h += `<div class="dimmer"></div><div class="off-card"><div class="h"><span class="pt"></span>${t.offTitle}</div><p>${t.offText}</p><div class="ft"><span class="spin"></span> ${t.offWait}</div></div>`;
  }
  $('#over').innerHTML = h;
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
    if (!S.review || !S.path) return;
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
    toast({ title: t.denied, text, short: `✕ ${t.denied}` });
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

// ── клавиши ──

document.addEventListener('keydown', (e) => {
  if (e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.target.closest && e.target.closest('input, textarea, [contenteditable]')) return;
  const k = e.key;
  const isList = !routeId();
  if (S.overlay === 'menu') {
    e.preventDefault();
    closeOverlay();
    if (k === 'a') accept();
    else if (k === 'v') toggleCols();
    else if (k === 'm') toggleDoc();
    return;
  }
  if (S.overlay === 'help') {
    if (k === 'Escape' || k === '?' || k === 'q') { e.preventDefault(); closeOverlay(); }
    return;
  }
  if (k === 'Escape') { S.pending = ''; if (S.toast) toast(null); return; }
  if (S.pending) {
    const seq = S.pending + k;
    S.pending = '';
    e.preventDefault();
    if (isList) return;
    if (seq === 'gg') $('#sc').scrollTop = 0;
    else if (seq === ']c') jumpChange(1);
    else if (seq === '[c') jumpChange(-1);
    else if (seq === ']f') jumpFile(1);
    else if (seq === '[f') jumpFile(-1);
    else if (seq === ']b') jumpBlock(1);
    else if (seq === '[b') jumpBlock(-1);
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
    }
    return;
  }
  const key = t.closest('.wk [data-key]');
  if (key) {
    closeOverlay();
    if (key.dataset.key === 'a') accept();
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
window.addEventListener('resize', () => renderMap());

applyTheme();
connect();
enter();
