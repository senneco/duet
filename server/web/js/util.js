// Мелочи, общие для всех частей клиента.

const ESC = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };
export const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ESC[c]);

// Ядро отдаёт места подсветки в байтах UTF-8, строки в JS — в UTF-16. Переводим.
export function byteToIndex(s, b) {
  let bytes = 0;
  for (let i = 0; i < s.length; i++) {
    if (bytes >= b) return i;
    const c = s.codePointAt(i);
    bytes += c < 0x80 ? 1 : c < 0x800 ? 2 : c < 0x10000 ? 3 : 4;
    if (c > 0xffff) i++;
  }
  return s.length;
}

// marks ядра → [[from, to)] в индексах строки.
export function ranges(text, marks) {
  return (marks || []).map((m) => [byteToIndex(text, m.from), byteToIndex(text, m.to)]).filter(([a, b]) => b > a);
}

// Строка целиком новая или удалённая: подсвечен весь текст, кроме отступа.
export function wholeRange(text) {
  const m = /^\s*/.exec(text)[0].length;
  return m < text.length ? [[m, text.length]] : [];
}

export const inRanges = (rs, i) => rs.some(([a, b]) => i >= a && i < b);

// paint — строка кода: токены подсветки синтаксиса и места правок. Подсветка правок цельная:
// <mark> не пересекает границы токенов, а соседние куски одной правки выглядят как одна подложка.
export function paint(text, tokens, rs, cls) {
  if (!text) return '';
  const toks = tokens && tokens.length ? tokens : [['', text]];
  let out = '';
  let pos = 0;
  let open = false;
  for (const [c, t] of toks) {
    let i = 0;
    while (i < t.length) {
      const marked = inRanges(rs, pos + i);
      let j = i + 1;
      while (j < t.length && inRanges(rs, pos + j) === marked) j++;
      if (marked && !open) { out += `<mark class="${cls}">`; open = true; }
      if (!marked && open) { out += '</mark>'; open = false; }
      const s = esc(t.slice(i, j));
      out += c ? `<span class="t-${c}">${s}</span>` : s;
      i = j;
    }
    pos += t.length;
  }
  if (open) out += '</mark>';
  return out;
}

export function ext(path) {
  const base = path.slice(path.lastIndexOf('/') + 1);
  const dot = base.lastIndexOf('.');
  return dot > 0 ? base.slice(dot + 1).toLowerCase() : '';
}

// Вид файла: документ (markdown), текст или код. Как ядро решает «текст или код» — core.IsProse.
export function fileKind(path) {
  const base = path.slice(path.lastIndexOf('/') + 1);
  const e = ext(path);
  if (['md', 'markdown', 'mdx'].includes(e)) return 'markdown';
  if (['txt', 'text', 'rst', 'adoc', 'asciidoc', 'org', 'wiki'].includes(e)) return 'text';
  if (!e) {
    if (['Makefile', 'Dockerfile', 'Jenkinsfile', 'Gemfile', 'Rakefile', 'Procfile', 'BUILD', 'WORKSPACE'].includes(base)) return 'code';
    // Страницы Вики и тикеты обычно без расширения и в разметке markdown.
    return base.startsWith('.') ? 'code' : 'markdown';
  }
  return 'code';
}

export function splitLines(s) {
  if (s == null || s === '') return [];
  const ls = s.split('\n');
  if (ls[ls.length - 1] === '') ls.pop();
  return ls;
}
