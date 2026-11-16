import assert from 'node:assert/strict';
const { default: puppeteer } = await import(process.argv[2]);
const browser = await puppeteer.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
try {
  const page = await browser.newPage();
  await page.setViewport({ width: 1200, height: 800 });
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('http://127.0.0.1:4885');
  const fixture = await page.evaluate(() => fetch('/fixture').then(r => r.json()));
  await page.goto(`http://127.0.0.1:4885/r/${fixture.id}`);
  await page.waitForSelector('.cmk');
  assert.equal(await page.$eval('.cmk', el => el.textContent), '1');
  assert.equal(await page.$$eval('.cc', els => els.length), 1);
  await page.click('.cmk');
  await page.waitForSelector('.cc.on');
  const question = await page.$eval('.cc.on', el => el.dataset.cid);
  assert.ok(await page.$eval('#sc', el => el.scrollTop > 0));
  // Two fresh comments above the question, added through the owner's API.
  await page.evaluate(async f => {
    const file = await fetch(`/api/reviews/${f.id}/file?path=${encodeURIComponent(f.path)}`).then(r => r.json());
    for (const n of [2, 20]) {
      const char = file.current.indexOf(`## Раздел ${n}\n`);
      const start = new TextEncoder().encode(file.current.slice(0, char)).length;
      const res = await fetch(`/api/reviews/${f.id}/comment`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ path: f.path, range: { start, end: start + 10 }, text: `Новый ${n}` }) });
      if (!res.ok) throw Error(await res.text());
    }
  }, fixture);
  await page.waitForFunction(() => document.querySelector('.cmk')?.textContent === '3');
  const ids = [];
  for (let i = 0; i < 3; i++) {
    await page.click('.cmk');
    ids.push(await page.$eval('.cc.on', el => el.dataset.cid));
  }
  assert.equal(new Set(ids).size, 3);
  assert.equal(ids[2], question);
  await page.$eval('#sc', el => { el.scrollTop = el.scrollHeight; });
  await page.click('.cmk');
  assert.equal(await page.$eval('.cc.on', el => el.dataset.cid), ids[0]);
  await page.screenshot({ path: '/tmp/duet-comment-count.png' });
  assert.deepEqual(errors, []);
  console.log('Счётчик 1; переходы по трём веткам; возврат с конца к первой — OK');
} finally { await browser.close(); }
