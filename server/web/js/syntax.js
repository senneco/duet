// Подсветка синтаксиса. Простой разбор по словам: ключевые слова, строки, комментарии, числа,
// вызовы функций и типы. Состояние (многострочные комментарии и строки) переносится между строками,
// поэтому разбирается весь текст сразу. Результат — строки из токенов [класс, текст].
// Классы: k — ключевое слово, s — строка, c — комментарий, f — функция, t — тип, n — число и литерал,
// p — ключ (JSON, YAML) и служебные знаки разметки.

const W = (s) => new Set(s.split(/\s+/).filter(Boolean));

const KW = {
  js: W(`await break case catch class const continue debugger default delete do else export extends finally for from function
    get if import in instanceof let new of return set static super switch this throw try typeof var void while with yield async
    as type interface enum implements declare namespace abstract private protected public readonly keyof infer is satisfies`),
  go: W(`break case chan const continue default defer else fallthrough for func go goto if import interface map package range
    return select struct switch type var`),
  py: W(`and as assert async await break class continue def del elif else except finally for from global if import in is
    lambda nonlocal not or pass raise return try while with yield match case`),
  sh: W(`if then else elif fi case esac for while until do done in function select return local export readonly declare
    set unset shift exit source alias`),
  rs: W(`as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref
    return self Self static struct super trait type unsafe use where while`),
  c: W(`auto break case char const continue default do double else enum extern float for goto if inline int long register
    return short signed sizeof static struct switch typedef union unsigned void volatile while class namespace template
    typename public private protected virtual override new delete using try catch throw bool`),
  java: W(`abstract assert boolean break byte case catch char class const continue default do double else enum extends final
    finally float for if implements import instanceof int interface long native new package private protected public return
    short static super switch synchronized this throw throws transient try void volatile while var val fun object when is in
    override open data sealed companion lateinit let guard func struct protocol extension init self import`),
  rb: W(`alias and begin break case class def defined do else elsif end ensure for if in module next not or redo rescue
    retry return self super then undef unless until when while yield require`),
  sql: W(`select from where and or not insert into values update set delete create table index view drop alter add column
    primary key foreign references join left right inner outer on as group by order having limit offset union all distinct
    case when then else end is null like in between exists with returning default`),
  css: W(`@media @import @supports @keyframes @font-face @layer @container !important`),
};
const LIT = W('true false null nil undefined None True False NaN Infinity iota');

const LANG = {
  js: 'js', mjs: 'js', cjs: 'js', jsx: 'js', ts: 'js', tsx: 'js', mts: 'js', cts: 'js', vue: 'js', svelte: 'js',
  go: 'go', py: 'py', pyi: 'py', sh: 'sh', bash: 'sh', zsh: 'sh', fish: 'sh', rs: 'rs',
  c: 'c', h: 'c', cc: 'c', cpp: 'c', cxx: 'c', hpp: 'c', hh: 'c', m: 'c', mm: 'c', cs: 'c',
  java: 'java', kt: 'java', kts: 'java', scala: 'java', swift: 'java', dart: 'java', groovy: 'java', gradle: 'java',
  rb: 'rb', sql: 'sql', css: 'css', scss: 'css', less: 'css', json: 'json', jsonc: 'json', json5: 'json',
  yaml: 'yaml', yml: 'yaml', toml: 'yaml', ini: 'yaml', conf: 'yaml', env: 'yaml', properties: 'yaml',
  html: 'html', htm: 'html', xml: 'html', svg: 'html', plist: 'html',
  md: 'md', markdown: 'md', mdx: 'md', proto: 'c', php: 'c', lua: 'sql', tf: 'yaml', hcl: 'yaml',
};
const NAMES = { Makefile: 'sh', Dockerfile: 'sh', Jenkinsfile: 'java', Gemfile: 'rb', Rakefile: 'rb', Procfile: 'yaml', BUILD: 'py', WORKSPACE: 'py' };

// langOf — язык по имени файла или по слову после ``` в markdown.
export function langOf(nameOrTag) {
  if (!nameOrTag) return '';
  const base = nameOrTag.slice(nameOrTag.lastIndexOf('/') + 1);
  if (NAMES[base]) return NAMES[base];
  const dot = base.lastIndexOf('.');
  const e = (dot >= 0 ? base.slice(dot + 1) : base).toLowerCase();
  const alias = { javascript: 'js', typescript: 'js', golang: 'go', python: 'py', shell: 'sh', console: 'sh', rust: 'rs', kotlin: 'kt', ruby: 'rb', yml: 'yaml', jsonl: 'json', cpp: 'c' };
  return LANG[alias[e] || e] || LANG[e] || '';
}

const HASH = new Set(['py', 'sh', 'rb', 'yaml']);
const SLASH = new Set(['js', 'go', 'rs', 'c', 'java', 'css', 'json']);

export function tokenize(text, lang) {
  if (!lang) return null;
  const toks = lang === 'html' ? markup(text) : lang === 'md' ? markdownSrc(text) : code(text, lang);
  // Разрезаем токены по переводам строк.
  const lines = [[]];
  for (const [c, t] of toks) {
    const parts = t.split('\n');
    parts.forEach((p, i) => {
      if (i > 0) lines.push([]);
      if (p) lines[lines.length - 1].push([c, p]);
    });
  }
  return lines;
}

function code(s, lang) {
  const out = [];
  const kw = KW[lang] || new Set();
  const push = (c, t) => {
    const last = out[out.length - 1];
    if (last && last[0] === c) last[1] += t;
    else out.push([c, t]);
  };
  let i = 0;
  const n = s.length;
  let lineStart = true;
  while (i < n) {
    const ch = s[i];
    const rest2 = s.slice(i, i + 2);
    // Комментарии.
    if ((SLASH.has(lang) && rest2 === '//' && lang !== 'css' && lang !== 'json') || (lang === 'json' && rest2 === '//')) {
      const e = s.indexOf('\n', i);
      push('c', s.slice(i, e < 0 ? n : e));
      i = e < 0 ? n : e;
      continue;
    }
    if (SLASH.has(lang) && rest2 === '/*') {
      const e = s.indexOf('*/', i + 2);
      const end = e < 0 ? n : e + 2;
      push('c', s.slice(i, end));
      i = end;
      continue;
    }
    if (HASH.has(lang) && ch === '#' && (i === 0 || /\s/.test(s[i - 1]))) {
      const e = s.indexOf('\n', i);
      push('c', s.slice(i, e < 0 ? n : e));
      i = e < 0 ? n : e;
      continue;
    }
    if (lang === 'sql' && rest2 === '--') {
      const e = s.indexOf('\n', i);
      push('c', s.slice(i, e < 0 ? n : e));
      i = e < 0 ? n : e;
      continue;
    }
    // Строки.
    if (ch === '"' || ch === "'" || ch === '`') {
      if (ch === "'" && (lang === 'rs' || lang === 'java') && /\w/.test(s[i - 1] || '')) { push('', ch); i++; continue; }
      const triple = lang === 'py' && s.slice(i, i + 3) === ch.repeat(3);
      const q = triple ? ch.repeat(3) : ch;
      const multi = triple || (ch === '`' && (lang === 'js' || lang === 'go'));
      let j = i + q.length;
      while (j < n) {
        if (s[j] === '\\' && ch !== '`') { j += 2; continue; }
        if (s.startsWith(q, j)) { j += q.length; break; }
        if (s[j] === '\n' && !multi) break;
        j++;
      }
      const str = s.slice(i, Math.min(j, n));
      // Ключ JSON и YAML: строка, за которой двоеточие.
      const after = /^\s*:/.test(s.slice(j, j + 40));
      push((lang === 'json' || lang === 'yaml') && after ? 'p' : 's', str);
      i = Math.min(j, n);
      continue;
    }
    // Числа.
    if (/[0-9]/.test(ch) && !/[\w$]/.test(s[i - 1] || '')) {
      const m = /^(0[xXbBoO][0-9a-fA-F_]+|\d[\d_]*(\.\d+)?([eE][+-]?\d+)?)[a-zA-Z%]*/.exec(s.slice(i, i + 40));
      push('n', m[0]);
      i += m[0].length;
      continue;
    }
    if (lang === 'css' && ch === '#' && /[0-9a-fA-F]/.test(s[i + 1] || '')) {
      const m = /^#[0-9a-fA-F]{3,8}/.exec(s.slice(i, i + 10));
      if (m) { push('n', m[0]); i += m[0].length; continue; }
    }
    // Слова.
    if (/[A-Za-z_$@]/.test(ch) || (lang === 'css' && ch === '-' && /[a-z]/.test(s[i + 1] || ''))) {
      const m = /^[@$\w-]*[\w$]|^[@$\w]/.exec(s.slice(i, i + 80));
      let w = m[0];
      if (lang !== 'css' && lang !== 'yaml') w = /^[@$\w]+/.exec(w)[0];
      const next = s.slice(i + w.length).match(/^\s*(.)/);
      const nc = next ? next[1] : '';
      let c = '';
      if (lang === 'yaml') {
        if (lineStart && nc === ':') c = 'p';
        else if (LIT.has(w)) c = 'n';
      } else if (lang === 'css') {
        if (kw.has(w)) c = 'k';
        else if (nc === ':' && !/^[@]/.test(w)) c = 'p';
        else if (nc === '(') c = 'f';
      } else if (lang === 'sql' ? kw.has(w.toLowerCase()) : kw.has(w)) c = 'k';
      else if (LIT.has(w)) c = 'n';
      else if (nc === '(') c = 'f';
      else if (/^[A-Z][a-z0-9]/.test(w) && lang !== 'sh' && lang !== 'sql') c = 't';
      push(c, w);
      i += w.length;
      lineStart = false;
      continue;
    }
    if (ch === '\n') lineStart = true;
    else if (!/\s/.test(ch) && !(lang === 'yaml' && ch === '-')) lineStart = false;
    push('', ch);
    i++;
  }
  return out;
}

function markup(s) {
  const out = [];
  const re = /<!--[\s\S]*?(-->|$)|<\/?[A-Za-z][\w:-]*|\/?>|"[^"]*"|'[^']*'|[\w:-]+(?==)|[^<>"'\w]+|[\s\S]/g;
  let inTag = false;
  for (const m of s.matchAll(re)) {
    const t = m[0];
    let c = '';
    if (t.startsWith('<!--')) c = 'c';
    else if (/^<\/?[A-Za-z]/.test(t)) { c = 'k'; inTag = true; }
    else if (t === '>' || t === '/>') { c = 'k'; inTag = false; }
    else if (inTag && /^["']/.test(t)) c = 's';
    else if (inTag && /^[\w:-]+$/.test(t)) c = 'p';
    out.push([c, t]);
  }
  return out;
}

// Исходник markdown: заголовки, метки списков, код, цитаты.
function markdownSrc(s) {
  const out = [];
  let fence = false;
  for (const [i, line] of s.split('\n').entries()) {
    if (i > 0) out.push(['', '\n']);
    if (/^\s*(```|~~~)/.test(line)) { fence = !fence; out.push(['c', line]); continue; }
    if (fence) { out.push(['s', line]); continue; }
    let m;
    if ((m = /^#{1,6}\s/.exec(line))) { out.push(['k', line]); continue; }
    if ((m = /^(\s*)([-*+]|\d+[.)])(\s+)/.exec(line))) { out.push(['', m[1]], ['p', m[2]], ['', m[3]]); inline(line.slice(m[0].length), out); continue; }
    if ((m = /^\s*>/.exec(line))) { out.push(['c', line]); continue; }
    inline(line, out);
  }
  return out;
}

function inline(s, out) {
  for (const m of s.matchAll(/`[^`]*`|\*\*[^*]+\*\*|\[[^\]]*\]\([^)]*\)|[^`*[]+|[\s\S]/g)) {
    const t = m[0];
    out.push([t.startsWith('`') ? 's' : t.startsWith('**') ? 'k' : t.startsWith('[') && t.includes('](') ? 'f' : '', t]);
  }
}
