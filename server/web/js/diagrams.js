// Mermaid поставляется локально; загрузка и рендеринг только при наличии схем.
let library;
let queue = Promise.resolve();
let serial = 0;
function load() {
  return library ||= new Promise((resolve, reject) => {
    const script = document.createElement('script');
    script.src = '/web/vendor/mermaid/mermaid.min.js';
    script.onload = () => resolve(window.mermaid);
    script.onerror = () => { library = null; script.remove(); reject(Error('Mermaid')); };
    document.head.append(script);
  });
}
const ru = () => (document.documentElement.lang || navigator.language).startsWith('ru');
function expand(svg) {
  const dialog = document.createElement('dialog'); dialog.className = 'diagram-dialog';
  dialog.innerHTML = `<header><strong>${ru() ? 'Схема' : 'Diagram'}</strong><label>− <input type="range" min="50" max="250" value="100" aria-label="Zoom"> +</label><button type="button">${ru() ? 'Закрыть' : 'Close'} · Esc</button></header><div class="diagram-pan"></div>`;
  const clone = svg.cloneNode(true);
  // Mermaid привязывает CSS к id корневого SVG. При копировании переносим
  // все id вместе со ссылками на стили и маркеры, сохраняя независимость окон.
  const ids = new Map();
  for (const node of [clone, ...clone.querySelectorAll('[id]')]) {
    if (node.id) ids.set(node.id, node.id + '-expanded-' + (++serial));
  }
  const references = text => text.replace(/#([\w:-]+)/g, (match, id) => ids.has(id) ? '#' + ids.get(id) : match);
  for (const node of [clone, ...clone.querySelectorAll('*')]) {
    for (const attr of [...node.attributes]) {
      if (attr.name === 'id') node.setAttribute('id', ids.get(attr.value));
      else if (attr.value.includes('#')) node.setAttribute(attr.name, references(attr.value));
    }
    if (node.tagName.toLowerCase() === 'style') node.textContent = references(node.textContent);
  }
  clone.style.maxWidth = 'none'; clone.style.width = '100%'; clone.style.height = 'auto';
  dialog.querySelector('.diagram-pan').append(clone);
  dialog.querySelector('input').oninput = e => clone.style.width = e.target.value + '%';
  dialog.querySelector('button').onclick = () => dialog.close();
  dialog.addEventListener('close', () => dialog.remove());
  dialog.addEventListener('keydown', e => e.stopPropagation());
  document.body.append(dialog); dialog.showModal();
}
export function renderDiagrams(root) {
  const dark = document.body.classList.contains('dark');
  for (const el of root.querySelectorAll('.diagram')) {
    const theme = dark ? 'dark' : 'light';
    if (el.dataset.renderTheme === theme) continue;
    el.dataset.renderTheme = theme;
    const source = el.dataset.source;
    el.querySelector('summary').textContent = ru() ? 'Исходник и правки' : 'Source and changes';
    el.querySelector('button').setAttribute('aria-label', ru() ? 'Увеличить схему' : 'Expand diagram');
    queue = queue.catch(() => {}).then(async () => {
      if (!el.isConnected || el.dataset.renderTheme !== theme) return;
      const canvas = el.querySelector('.diagram-canvas');
      try {
        const mermaid = await load();
        const css = getComputedStyle(document.body);
        mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', suppressErrorRendering: true,
          theme: 'base', look: 'classic', layout: 'elk', fontFamily: css.getPropertyValue('--sans').trim() || 'system-ui',
          secure: ['securityLevel', 'startOnLoad', 'themeCSS', 'fontFamily', 'themeVariables', 'maxTextSize', 'maxEdges'],
          themeVariables: { darkMode: dark, background: dark ? '#191722' : '#faf9f6', primaryColor: dark ? '#302d48' : '#edf0fc', primaryTextColor: dark ? '#ede9f6' : '#292638', primaryBorderColor: dark ? '#8c83bf' : '#9c94c5', lineColor: dark ? '#ada6cd' : '#827a9a', secondaryColor: dark ? '#223c3a' : '#e8f3ed', tertiaryColor: dark ? '#3c3340' : '#f5ede5', fontSize: '15px' },
          flowchart: { htmlLabels: false, curve: 'basis', padding: 18, nodeSpacing: 32, rankSpacing: 48 },
          sequence: { useMaxWidth: true, actorMargin: 35, messageMargin: 28 } });
        const result = await mermaid.render('duetDiagram' + (++serial), source);
        if (!el.isConnected || el.dataset.renderTheme !== theme) return;
        canvas.innerHTML = result.svg;
        const svg = canvas.querySelector('svg');
        svg.setAttribute('role', 'img'); svg.setAttribute('aria-label', ru() ? 'Схема Mermaid' : 'Mermaid diagram');
        const button = el.querySelector('.diagram-expand'); button.hidden = false; button.onclick = () => expand(svg);
      } catch {
        canvas.textContent = ru() ? 'Не удалось построить схему. Проверьте синтаксис Mermaid в исходнике ниже.' : 'Cannot render diagram. Check the Mermaid source below.';
        canvas.classList.add('diagram-error'); el.querySelector('details').open = true;
      }
    });
  }
}
