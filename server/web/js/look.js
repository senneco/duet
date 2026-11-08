// Настройки вида (шаг 7a): что можно выбрать, значения по умолчанию, цвета и классы для страницы,
// карточка «Вид». Здесь нет обращений к странице — только данные, поэтому всё проверяется тестами.

// Цвета правок: «новое», «убранное» и акцент — для светлой и тёмной схемы.
export const PALETTES = {
  sian: { light: { a: '#2d62c9', d: '#c4364a', acc: '#7b6cf6', af: '#5b4ce0' }, dark: { a: '#86aef5', d: '#f08a98', acc: '#8b7cf8', af: '#a99dfb' } },
  sea: { light: { a: '#0c7c8c', d: '#d0573b', acc: '#0f8a9c', af: '#0b6f7e' }, dark: { a: '#62cfdb', d: '#f59b82', acc: '#3fb8c8', af: '#7fd8e2' } },
  forest: { light: { a: '#2c8650', d: '#c43a6c', acc: '#37905c', af: '#2a7449' }, dark: { a: '#7fd69c', d: '#f08bb2', acc: '#57c084', af: '#8fdcaa' } },
  sunset: { light: { a: '#b36b06', d: '#b8327a', acc: '#e0702a', af: '#b8561a' }, dark: { a: '#f2b45a', d: '#f084bb', acc: '#ef8a45', af: '#f6aa74' } },
  gh: { light: { a: '#1a7f37', d: '#cf222e', acc: '#0969da', af: '#0757b8' }, dark: { a: '#3fb950', d: '#f85149', acc: '#4493f8', af: '#79b4fa' } },
};

// Характер: доля цвета правки в подложке блока и в подложке изменённых слов.
export const CHARACTERS = {
  quiet: { light: [4, 17], dark: [12, 32] },
  normal: { light: [8, 27], dark: [20, 48] },
  juicy: { light: [12, 36], dark: [26, 58] },
  bright: { light: [16, 46], dark: [32, 68] },
};

// Порядок строк в карточке, клавиши и значения (первое — по умолчанию, если не сказано иначе).
export const OPTIONS = [
  { name: 'char', key: 'h', values: ['quiet', 'normal', 'juicy', 'bright'], def: 'normal' },
  { name: 'pal', key: 'c', values: ['sian', 'sea', 'forest', 'sunset', 'gh'], def: 'gh' },
  { name: 'theme', key: 't', values: ['system', 'dark', 'light'] },
  { name: 'font', key: 'f', values: ['serif', 'sans'] },
  { name: 'size', key: 's', values: ['s', 'm', 'l'], def: 'm' },
  { name: 'edge', key: 'e', values: ['none', 'leaf', 'frame'] },
  { name: 'focus', key: 'b', values: ['off', 'on'] },
  { name: 'keys', key: 'p', values: ['on', 'off'] },
  { name: 'lang', key: 'l', values: ['ru', 'en'] },
];

const byName = Object.fromEntries(OPTIONS.map((o) => [o.name, o]));
const def = (o) => o.def || o.values[0];

// loadLook — настройки из хранилища; неизвестное значение — как по умолчанию. lang по умолчанию — язык системы.
export function loadLook(get, lang) {
  const look = {};
  for (const o of OPTIONS) {
    const d = o.name === 'lang' ? lang : def(o);
    const v = get(o.name, d);
    look[o.name] = o.values.includes(v) ? v : d;
  }
  return look;
}

// next — следующее значение настройки по кругу.
export function next(look, name) {
  const vs = byName[name].values;
  return vs[(vs.indexOf(look[name]) + 1) % vs.length];
}

export function optionByKey(key) {
  return OPTIONS.find((o) => o.key === key);
}

export function isOption(name, value) {
  return !!byName[name] && byName[name].values.includes(value);
}

// paint — цвета (свойства CSS) и классы страницы для настроек и схемы (light | dark).
export function paint(look, scheme) {
  const p = PALETTES[look.pal][scheme];
  const [blk, word] = CHARACTERS[look.char][scheme];
  const mix = (c, n) => `color-mix(in srgb, ${c} ${n}%, var(--bg))`;
  const props = {
    '--add-fg': p.a, '--del-fg': p.d,
    '--add-bg': mix(p.a, blk), '--del-bg': mix(p.d, blk),
    '--add-w': mix(p.a, word), '--del-w': mix(p.d, word),
    '--add-w2': mix(p.a, word + 20), '--del-w2': mix(p.d, word + 20),
    '--acc': p.acc, '--acc-fg': p.af,
    '--acc-soft': mix(p.acc, scheme === 'dark' ? 22 : 14),
    '--sel': mix(p.acc, scheme === 'dark' ? 13 : 9),
  };
  const classes = [`ch-${look.char}`, `sz-${look.size}`, `edge-${look.edge}`];
  if (look.font === 'sans') classes.push('sans');
  if (look.keys === 'off') classes.push('nokeys');
  if (look.focus === 'on') classes.push('focus');
  return { props, classes };
}

// Все классы, которые ставит paint: их снимают перед тем, как поставить новые.
export const LOOK_CLASSES = [
  ...OPTIONS[0].values.map((v) => `ch-${v}`), ...byName.size.values.map((v) => `sz-${v}`),
  ...byName.edge.values.map((v) => `edge-${v}`), 'sans', 'nokeys', 'focus',
];

// swatch — образец палитры: половина «новое», половина «убранное».
export function swatch(pal, scheme) {
  const p = PALETTES[pal][scheme];
  return `background:linear-gradient(90deg, ${p.a} 0 50%, ${p.d} 50% 100%)`;
}

// panelHTML — карточка «Вид». t — тексты (i18n), scheme — схема, которая сейчас на экране.
export function panelHTML(look, t, scheme) {
  const L = t.look;
  const seg = (name) => `<span class="sg">${byName[name].values.map((v) => `<button data-look="${name}=${v}" class="${look[name] === v ? 'on' : ''}">${L.v[name][v]}</button>`).join('')}</span>`;
  const row = (name) => `<div class="vr"><span class="lk">${byName[name].key}</span><span class="vl">${L.n[name]}</span>${seg(name)}</div>`;
  const ci = byName.char.values.indexOf(look.char);
  const last = byName.char.values.length - 1;
  const char = `<div class="ch"><div class="cn"><span class="lk">h</span><span class="cl">${L.n.char}</span></div>
<div class="cv">${L.v.char[look.char]}</div><div class="cd">${L.charText[look.char]}</div>
<div class="trk">${byName.char.values.map((v, i) => `<button style="left:${(i * 100) / last}%" data-look="char=${v}" class="${i === ci ? 'on' : ''}" title="${L.v.char[v]}"></button>`).join('')}</div>
<div class="tl">${byName.char.values.map((v, i) => `<span class="${i === ci ? 'on' : ''}">${L.v.char[v]}</span>`).join('')}</div></div>`;
  const pal = `<div class="vr pr"><span class="lk">c</span><span class="vl">${L.n.pal}</span></div><div class="pal">${byName.pal.values.map((k) => `<button data-look="pal=${k}" class="${look.pal === k ? 'on' : ''}"><i style="${swatch(k, scheme)}"></i>${L.v.pal[k]}</button>`).join('')}</div>`;
  return `<div class="vp"><div class="vh">${L.title}<span class="sp"></span><span class="x" data-act="look">${L.close}</span></div>
${char}${pal}${['theme', 'font', 'size', 'edge', 'focus', 'keys', 'lang'].map(row).join('')}
<div class="vf">${L.foot}</div></div>`;
}
