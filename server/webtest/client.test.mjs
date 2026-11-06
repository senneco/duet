// Тесты клиента без браузера: node --test server/webtest
import test from 'node:test';
import assert from 'node:assert/strict';

import { byteToIndex, ranges, fileKind } from '../web/js/util.js';
import { tokenize, langOf } from '../web/js/syntax.js';
import { parseBlocks, renderBlock } from '../web/js/markdown.js';
import { renderCode, renderDoc, docUnits } from '../web/js/view.js';
import { buildTree, flatten, fileOrder, counts } from '../web/js/tree.js';

const L = (n, text, marks) => (marks ? { n, text, marks } : { n, text });
const bytes = (s, sub) => {
  const i = s.indexOf(sub);
  return { from: Buffer.byteLength(s.slice(0, i)), to: Buffer.byteLength(s.slice(0, i + sub.length)) };
};
// Без пометок мест в исходнике (data-p): так проще сверять текст.
const plain = (html) => html.replace(/<span data-p="[^"]*">(.*?)<\/span>/gs, '$1');
const marks = (html, cls) => [...html.matchAll(new RegExp(`<mark class="${cls}">(.*?)</mark>`, 'g'))].map((m) => m[1].replace(/<[^>]+>/g, ''));

test('места подсветки из байтов UTF-8 в символы строки', () => {
  const s = 'Тема всегда светлая';
  assert.equal(byteToIndex(s, Buffer.byteLength('Тема ')), 5);
  assert.deepEqual(ranges(s, [bytes(s, 'всегда')]), [[5, 11]]);
});

test('вид файла: документ, текст, код', () => {
  assert.equal(fileKind('/a/spec.md'), 'markdown');
  assert.equal(fileKind('/a/ticket'), 'markdown');
  assert.equal(fileKind('/a/notes.txt'), 'text');
  assert.equal(fileKind('/a/tree.ts'), 'code');
  assert.equal(fileKind('/a/Makefile'), 'code');
});

test('подсветка синтаксиса: слова, строки, комментарии, вызовы, многострочный комментарий', () => {
  const lines = tokenize('const x = load("a"); // ok\n/* one\ntwo */ Foo', langOf('a.ts'));
  const cls = (i, text) => lines[i].find(([, t]) => t === text)?.[0];
  assert.equal(cls(0, 'const'), 'k');
  assert.equal(cls(0, 'load'), 'f');
  assert.equal(cls(0, '"a"'), 's');
  assert.equal(cls(0, '// ok'), 'c');
  assert.equal(cls(1, '/* one'), 'c');
  assert.equal(cls(2, 'two */'), 'c');
  assert.equal(cls(2, 'Foo'), 't');
  assert.equal(langOf('x.go'), 'go');
  assert.equal(langOf('Dockerfile'), 'sh');
});

test('markdown: блоки и их строки', () => {
  const ls = ['# Веб-клиент', '', 'Первая строка', 'вторая строка.', '', '- a', '- b', '  продолжение', '', '```ts', 'const a = 1;', '```', '', '| A | B |', '|---|---|', '| 1 | 2 |', 'Заголовок', '---'];
  const bs = parseBlocks(ls).map((b) => [b.type, b.start, b.end]);
  assert.deepEqual(bs, [
    ['heading', 0, 1], ['para', 2, 4], ['item', 5, 6], ['item', 6, 8], ['code', 9, 12], ['table', 13, 16], ['heading', 16, 18],
  ]);
});

test('markdown: подсветка правок поверх разметки, без дыр между строками абзаца', () => {
  const ls = ['Это **очень важный** текст,', 'и он продолжается.'];
  const [b] = parseBlocks(ls);
  const rs = { 0: [[6, 27]], 1: [[0, 4]] }; // «очень важный** текст,» и «и он»
  const h = renderBlock(b, ls, (i) => rs[i] || [], 'wa');
  assert.match(h, /<strong><mark class="wa">очень важный<\/mark><\/strong><mark class="wa"> текст,\nи он<\/mark> продолжается/);
});

test('markdown: ссылки только безопасные', () => {
  const ls = ['[a](https://x.y) [b](javascript:alert(1))'];
  const h = renderBlock(parseBlocks(ls)[0], ls);
  assert.match(h, /<a href="https:\/\/x.y"/);
  assert.doesNotMatch(h, /javascript:/);
});

// Сценарий 140: строка, новая целиком, среди изменённых подсвечена целиком.
test('код: новая строка среди изменённых подсвечена целиком', () => {
  const o1 = '  row.textContent = f.path;';
  const n1 = '  row.textContent = node.name;';
  const chunks = [
    { kind: 'changed', old: [L(1, o1, [bytes(o1, 'f.path')])], new: [L(1, n1, [bytes(n1, 'node.name')])] },
    { kind: 'added', new: [L(2, '  row.prepend(icon(node.file));')] },
  ];
  for (const cols of [1, 2]) {
    const h = renderCode(chunks, { cols, lang: 'js', oldText: o1 + '\n', newText: `${n1}\n  row.prepend(icon(node.file));\n` });
    assert.deepEqual(marks(h, 'wa'), ['node.name', 'row.prepend(icon(node.file));'], `cols ${cols}`);
    assert.deepEqual(marks(h, 'wd'), ['f.path']);
  }
});

test('код: длинные неизменённые куски свёрнуты, без правок — файл целиком', () => {
  const same = (o0, n0, len) => ({ kind: 'same', old: Array.from({ length: len }, (_, k) => L(o0 + k, 'x')), new: Array.from({ length: len }, (_, k) => L(n0 + k, 'x')) });
  const chunks = [same(1, 1, 20), { kind: 'added', new: [L(21, 'y')] }, same(21, 22, 20)];
  const h = renderCode(chunks, { lang: 'js', oldText: '', newText: '' });
  assert.equal((h.match(/class="fold"/g) || []).length, 2);
  assert.match(h, /data-fold="0"[^>]*>.*<span class="fl">17</);
  const all = renderCode([same(1, 1, 40)], { lang: 'js' });
  assert.doesNotMatch(all, /class="fold"/);
  const open = renderCode(chunks, { lang: 'js', expanded: new Set([0]) });
  assert.equal((open.match(/class="fold"/g) || []).length, 1);
});

test('документ: абзац переписан — было и стало, весь текст подсвечен; новый пункт — новое', () => {
  const oldText = '# T\n\nВременная страница.\n\n- a\n';
  const newText = '# T\n\nКлиент показывает ревью.\n\n- a\n- b\n';
  const chunks = [
    { kind: 'same', old: [L(1, '# T'), L(2, '')], new: [L(1, '# T'), L(2, '')] },
    { kind: 'deleted', old: [L(3, 'Временная страница.')] },
    { kind: 'added', new: [L(3, 'Клиент показывает ревью.')] },
    { kind: 'same', old: [L(4, ''), L(5, '- a')], new: [L(4, ''), L(5, '- a')] },
    { kind: 'added', new: [L(6, '- b')] },
  ];
  const words = { was: 'было', now: 'стало', added: 'новое' };
  const h = renderDoc(chunks, oldText, newText, { words });
  assert.deepEqual(marks(h, 'wd'), ['Временная страница.']);
  assert.deepEqual(marks(h, 'wa'), ['Клиент показывает ревью.', 'b']);
  assert.match(h, /data-s="было"/);
  assert.match(h, /data-s="стало"/);
  assert.match(h, /data-s="новое"[^>]*><div class="u" data-n="6"><div class="li"/);
  const two = renderDoc(chunks, oldText, newText, { cols: 2, words });
  assert.match(two, /<div class="b hatch"><\/div><\/div><div class="c"><div class="b add">/);
});

test('документ: изменение внутри абзаца из нескольких строк показывает весь абзац', () => {
  const oldText = 'один два\nтри четыре\n';
  const newText = 'один два\nтри пять\n';
  const chunks = [
    { kind: 'same', old: [L(1, 'один два')], new: [L(1, 'один два')] },
    { kind: 'changed', old: [L(2, 'три четыре', [bytes('три четыре', 'четыре')])], new: [L(2, 'три пять', [bytes('три пять', 'пять')])] },
  ];
  const { units } = docUnits(chunks, oldText, newText);
  assert.equal(units.length, 1);
  assert.ok(units[0].dirty);
  const h = renderDoc(chunks, oldText, newText, {});
  assert.match(plain(h), /<p>один два\nтри <mark class="wa">пять<\/mark><\/p>/);
});

test('дерево: общий путь убран, папки сначала и по алфавиту, цепочка папок одной строкой', () => {
  const files = ['/p/duet/server/web/tree.ts', '/p/duet/README.md', '/p/duet/docs/specs/web.md', '/p/duet/docs/roadmap.md', '/p/duet/server/web/app.ts']
    .map((path, i) => ({ path, state: i === 1 ? 'accepted' : 'new' }));
  const { root, tree } = buildTree(files);
  assert.equal(root, '/p/duet');
  const rows = flatten(tree).map((r) => (r.dir ? r.name : r.file.name));
  assert.deepEqual(rows, ['docs/', 'specs/', 'web.md', 'roadmap.md', 'server/web/', 'app.ts', 'tree.ts', 'README.md']);
  assert.deepEqual(flatten(tree, new Set(['docs/'])).map((r) => (r.dir ? r.name : r.file.name)), ['docs/', 'server/web/', 'app.ts', 'tree.ts', 'README.md']);
  assert.equal(fileOrder(tree)[0].name, 'web.md');
  assert.deepEqual(counts(files), { done: 1, total: 5 });
});

test('документ: правка абзаца и новая таблица следом — отдельные блоки «было / стало» и «новое»', () => {
  const o = 'Ревью списком. Видно название.\n';
  const n = 'Ревью списком. Видно всё.\n\n| A | B |\n|---|---|\n| 1 | 2 |\n';
  const chunks = [
    { kind: 'changed', old: [L(1, 'Ревью списком. Видно название.', [bytes('Ревью списком. Видно название.', 'название')])], new: [L(1, 'Ревью списком. Видно всё.', [bytes('Ревью списком. Видно всё.', 'всё')])] },
    { kind: 'added', new: [L(2, ''), L(3, '| A | B |'), L(4, '|---|---|'), L(5, '| 1 | 2 |')] },
  ];
  const h = renderDoc(chunks, o, n, { words: { was: 'было', now: 'стало', added: 'новое' } });
  assert.match(h, /data-s="было"[\s\S]*data-s="стало"[\s\S]*data-s="новое"[^>]*><div class="u" data-n="3"><table>/);
});

// ── шаг 6b: места комментариев ──

import { target, parseP, oldToNew, byteToChar, utf8, commentOrder } from '../web/js/anchors.js';

// Пометки мест в отрисованном HTML: [текст, место].
const spans = (html) => [...html.matchAll(/data-p="([^"]+)">(?:<mark[^>]*>)?([^<]*)/g)].map((m) => [m[2], m[1]]);

test('документ: каждый кусок текста помечен местом в исходнике, разметка не мешает', () => {
  const lines = ['Это **важно** и `код` тут', 'вторая строка'];
  const h = renderBlock({ type: 'para', start: 0, end: 2 }, lines, () => [], '', 'n');
  const sp = spans(h);
  const at = (t) => parseP(sp.find(([s]) => s.includes(t))[1]);
  assert.deepEqual(at('важно'), { side: 'n', line: 0, col: 6 });
  assert.deepEqual(at('код'), { side: 'n', line: 0, col: 17 });
  assert.deepEqual(at('вторая'), { side: 'n', line: 1, col: 0 });
});

test('выделение → место в текущей версии в байтах', () => {
  const cur = 'один\nдва три\n';
  const r = target({ side: 'n', line: 1, col: 4 }, { side: 'n', line: 1, col: 7 }, cur, cur, [{ kind: 'same', old: [L(1, 'один'), L(2, 'два три')], new: [L(1, 'один'), L(2, 'два три')] }]);
  const b = Buffer.from(cur);
  assert.equal(b.subarray(r.range.start, r.range.end).toString(), 'три');
});

test('выделение удалённого текста → точка, где он был, и цитата «было»', () => {
  const base = 'один\nстарое\nтри\n';
  const cur = 'один\nтри\n';
  const chunks = [
    { kind: 'same', old: [L(1, 'один')], new: [L(1, 'один')] },
    { kind: 'deleted', old: [L(2, 'старое')] },
    { kind: 'same', old: [L(3, 'три')], new: [L(2, 'три')] },
  ];
  const r = target({ side: 'o', line: 1, col: 0 }, { side: 'o', line: 1, col: 6 }, base, cur, chunks);
  assert.equal(r.was, 'старое');
  assert.equal(r.at, utf8('один\n'));
  // Неизменённый текст слева в двух колонках — обычный комментарий к тому же тексту справа.
  const s = target({ side: 'o', line: 2, col: 0 }, { side: 'o', line: 2, col: 3 }, base, cur, chunks);
  assert.equal(Buffer.from(cur).subarray(s.range.start, s.range.end).toString(), 'три');
  // Удалено в конце файла — точка в конце текста.
  assert.equal(oldToNew([{ kind: 'same', old: [L(1, 'а')], new: [L(1, 'а')] }, { kind: 'deleted', old: [L(2, 'б')] }], 1, 'а\n').at, 2);
  // Пустое выделение — нечего комментировать.
  assert.equal(target({ side: 'n', line: 0, col: 2 }, { side: 'n', line: 0, col: 2 }, cur, cur, chunks), null);
});

test('байты ↔ символы, порядок комментариев', () => {
  assert.equal(byteToChar('ёжик', 4), 2);
  assert.equal(utf8('ё😀'), 6);
  const o = commentOrder([{ id: 'b', range: { start: 9, end: 10 } }, { id: 'w', whole: true, range: {} }, { id: 'a', range: { start: 1, end: 5 } }]);
  assert.deepEqual(o.map((x) => x.id), ['w', 'a', 'b']);
});
