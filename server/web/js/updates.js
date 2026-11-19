import { esc } from './util.js';

// Каждая вкладка подтверждает готовность отдельно, включая вкладки с общим SSE.
export function updateClient({ dirty, preserve, language, repaint }) {
  const id = crypto.randomUUID();
  let state = { phase: 'disabled' }, token = '', frozen = false, timer, left = false;
  let dismissed = '', reloadPending = false, starting = false, polling = false;
  const notice = document.createElement('div');
  notice.className = 'toast update-notice';
  notice.setAttribute('role', 'status');
  notice.setAttribute('aria-live', 'polite');
  document.body.append(notice);
  const ru = () => language() === 'ru';
  const words = (r, e) => ru() ? r : e;
  const active = () => reloadPending || starting || (['checking', 'error'].includes(state.phase) ? state.requested : !['disabled', 'idle', 'current'].includes(state.phase));
  const busy = () => starting || ['checking', 'downloading', 'building', 'applying'].includes(state.phase);
  const key = () => `${state.version || ''}:${state.phase}:${state.message || ''}`;
  async function post(path, body) {
    const r = await fetch('/api/update/' + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal: AbortSignal.timeout(7000) });
    if (!r.ok) throw Error('update unavailable');
    return r.json();
  }
  function text() {
    if (reloadPending) return words('Сохраните текст, затем откройте новую версию.', 'Save your draft, then open the new version.');
    const messages = {
      available: words('Доступна новая версия duet.', 'A new version of duet is available.'),
      checking: words('Проверяю обновление…', 'Checking for updates…'),
      downloading: words('Скачиваю обновление…', 'Downloading update…'),
      building: words('Собираю новую версию… Можно продолжать чтение.', 'Building the new version… You can keep reading.'),
      ready: state.message ? words('Обновление готово. Завершите работу агента, сохраните текст и закройте окно ответа. Если открыты другие вкладки duet, проверьте их тоже.', 'The update is ready. Finish agent work, save drafts and close the response view. Check the other duet tabs as well.') : words('Обновление готово к применению.', 'The update is ready to apply.'),
      error: words('Не удалось обновить duet. Текущая версия продолжает работать.', 'Could not update duet. Your current version is still available.'),
    };
    return messages[state.phase] || '';
  }
  function action() { return reloadPending ? words('Открыть новую версию', 'Open new version') : state.phase === 'ready' ? words('Применить', 'Apply') : state.phase === 'error' ? words('Повторить', 'Try again') : words('Обновить', 'Update'); }
  function render() {
    const hidden = !active() || dismissed === key() || !!document.querySelector('#over .toast');
    notice.hidden = hidden;
    notice.innerHTML = `<span class="x i">↑</span><div class="t1">${words('Обновление duet', 'Duet update')}</div><div class="t2">${esc(text())}</div><div class="t3"><button class="btn" data-update="start" ${busy() || frozen ? 'disabled' : ''}>${busy() ? words('Подождите…', 'Please wait…') : action()}</button><button class="btn2" data-update="dismiss">${words('Позже', 'Later')}</button></div>`;
  }
  function freeze(on) {
    frozen = on;
    document.querySelector('#app').inert = on;
    document.body.classList.toggle('update-applying', on);
  }
  async function heartbeat() {
    const response = await post('client', { id, ack: token, ready: !dirty() });
    if (response.token && response.token !== token) {
      token = response.token;
      const ready = !dirty();
      if (ready) { preserve(); freeze(true); }
      await post('client', { id, ack: token, ready });
    } else if (!response.token) { token = ''; freeze(false); }
  }
  async function poll() {
    if (left || polling) return;
    polling = true; clearTimeout(timer);
    try {
      await heartbeat();
      const r = await fetch('/api/update/status', { cache: 'no-store', signal: AbortSignal.timeout(3000) });
      if (r.ok) state = await r.json();
    } catch { /* переподключение обрабатывает основной клиент; черновики остаются в памяти */ }
    render(); repaint();
    polling = false;
    timer = setTimeout(poll, 1000);
  }
  async function start() {
    dismissed = '';
    if (reloadPending) { if (!dirty()) { preserve(); location.reload(); } else render(); return; }
    if (busy() || frozen) return;
    starting = true; render();
    try { await post('start', {}); state = { ...state, phase: 'checking', requested: true }; }
    catch { state = { ...state, phase: 'error', requested: true }; }
    finally { starting = false; render(); repaint(); }
  }
  document.addEventListener('click', e => {
    const button = e.target.closest('[data-update]');
    if (!button) return;
    e.preventDefault(); e.stopImmediatePropagation();
    if (button.dataset.update === 'start') start();
    else if (button.dataset.update === 'show') { dismissed = ''; render(); }
    else { dismissed = key(); render(); }
  }, true);
  addEventListener('pagehide', () => {
    left = true; clearTimeout(timer);
    navigator.sendBeacon('/api/update/client', new Blob([JSON.stringify({ id, leave: true })], { type: 'application/json' }));
  });
  addEventListener('pageshow', e => { if (e.persisted) { left = false; poll(); } });
  addEventListener('visibilitychange', () => { if (!document.hidden && !left) { clearTimeout(timer); poll(); } });
  return {
    async start() { try { await heartbeat(); } catch {} poll(); },
    header() { return active() ? `<button class="tb update-button" data-update="show">↑ ${words('Обновление', 'Update')}</button>` : ''; },
    mayReload() {
      if (dirty()) { reloadPending = true; freeze(false); dismissed = ''; render(); return false; }
      preserve(); return true;
    },
  };
}
