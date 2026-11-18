// Запустить direct-fixture.go, затем node server/browsertest/many-tabs.mjs /path/to/playwright-core/index.mjs
import assert from 'node:assert/strict';

const { chromium } = await import(process.argv[2]);
const base = 'http://127.0.0.1:4884';
const { id, second } = await fetch(base + '/fixture?new').then(r => r.json());
const browser = await chromium.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' });
try {
  const context = await browser.newContext();
  const pages = [];
  for (let i = 0; i < 8; i++) {
    const page = await context.newPage();
    pages.push(page);
    await page.goto(base + '/r/' + id, { timeout: 5000 });
    await page.waitForSelector('#sc .page', { timeout: 5000 });
  }
  await pages[0].close();
  const last = pages.at(-1);
  await last.locator('[data-file]').filter({ hasText: 'second.md' }).click();
  await last.waitForFunction(() => document.querySelector('#sc').innerText.includes('Второй текст.'), null, { timeout: 5000 });
  assert.equal(await last.locator('[data-file].open').getAttribute('data-file'), second);
  const accepted = await context.request.post(base + '/api/reviews/' + id + '/accept-file', { data: { path: second } });
  assert.ok(accepted.ok());
  for (const page of pages.slice(1)) {
    await page.locator('[data-file]').filter({ hasText: 'second.md' }).locator('.d-ok').waitFor({ timeout: 5000 });
  }
  console.log('В восьмой вкладке открылся файл, и принятие отразилось во всех открытых вкладках.');
} finally {
  await browser.close();
}
