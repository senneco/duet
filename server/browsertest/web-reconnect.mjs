// node server/browsertest/web-reconnect.mjs /path/to/playwright-core/index.mjs /path/to/duet
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, rmSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { createServer } from 'node:net';
const { chromium } = await import(process.argv[2]);
const bin = resolve(process.argv[3]);
const root = realpathSync(mkdtempSync(join(tmpdir(), 'duet-web-reconnect-')));
const portServer = createServer();
await new Promise(r => portServer.listen(0, '127.0.0.1', r));
const port = portServer.address().port;
await new Promise(r => portServer.close(r));
const env = { ...process.env, DUET_HOME: join(root, 'home'), DUET_PORT: String(port), DUET_AGENT: 'claude', DUET_PID: String(process.pid), DUET_BROWSER: '/usr/bin/true', CODEX_THREAD_ID: '', CLAUDE_CODE_SESSION_ID: '' };
const run = (session, ...args) => execFileSync(bin, args, { cwd: root, env: { ...env, DUET_SESSION: session }, encoding: 'utf8' });
const stopTurn = session => execFileSync(bin, ['hook', 'claude'], { env, input: JSON.stringify({ session_id: session, cwd: root, hook_event_name: 'Stop' }) });
const path = join(root, 'spec.md');
const old = '# Проверка нового чата\n\nИсходный текст до уточнения.\n';
const next = '# Проверка нового чата\n\nУточнённый текст из нового чата.\n';
let browser;
try {
  writeFileSync(path, old);
  const submitted = run('first', 'submit', JSON.stringify({ title: 'Продолжение задачи', files: [{ path, original: { text: old, source: 'до задачи' } }] }));
  const id = submitted.match(/New review (\w+):/)[1];
  stopTurn('first');
  writeFileSync(path, next);
  const link = run('second', 'web', '--files', path).trim();
  if (!link.endsWith('/r/' + id)) throw Error(link);
  browser = await chromium.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto(link.replace('duet.localhost', '127.0.0.1'));
  await page.waitForFunction(() => document.querySelector('#sc')?.innerText.includes('Уточнённый текст из нового чата.'));
  const f = await page.evaluate(async ({ id, path }) => fetch('/api/reviews/' + id + '/file?path=' + encodeURIComponent(path)).then(r => r.json()), { id, path });
  if (f.base !== old || f.current !== next || f.state === 'accepted' || f.response) throw Error(JSON.stringify(f));
  if (readFileSync(path, 'utf8') !== next) throw Error('Изменён диск');
  if (errors.length) throw Error(errors.join('\n'));
  await page.screenshot({ path: '/tmp/duet-web-reconnect.png', fullPage: true });
  console.log('Новый чат открыл прежнее ревью; уточнение видно относительно прежней базы, файл требует принятия.');
} finally {
  if (browser) await browser.close();
  run('', 'stop');
  rmSync(root, { recursive: true, force: true });
}
