const { chromium } = await import(process.argv[2]);
const browser = await chromium.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
try {
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('http://127.0.0.1:4884');
  const f = await page.evaluate(() => fetch('/fixture?new').then(r => r.json()));
  await page.goto('http://127.0.0.1:4884/r/' + f.id);
  for (const [path, text] of [[f.first, 'Первый текст.'], [f.second, 'Второй текст.']]) {
    await page.locator('#tree').getByText(path.split('/').pop(), { exact: true }).click();
    await page.waitForFunction(text => document.body.innerText.includes(text), text);
    const state = await page.evaluate(async ({id, path}) => {
      const review = await fetch('/api/reviews/' + id).then(r => r.json());
      const file = await fetch('/api/reviews/' + id + '/file?path=' + encodeURIComponent(path)).then(r => r.json());
      return { pending: review.hasResponse, modal: document.querySelector('#app').classList.contains('rmode'), base: file.hasBase, absent: file.absent, state: file.state };
    }, {id: f.id, path});
    if (state.pending || state.modal || state.base || state.absent || state.state === 'accepted') throw Error(JSON.stringify(state));
  }
  await page.screenshot({ path: '/tmp/duet-new-files-direct.png', fullPage: true });
  if (errors.length) throw Error(errors.join('\n'));
  console.log('Два новых файла видны в основном просмотре, без базы и принятия.');
} finally { await browser.close(); }
