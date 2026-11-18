// node server/browsertest/repository.mjs /path/to/playwright-core/index.mjs
import assert from 'node:assert/strict';
const { chromium } = await import(process.argv[2]);
const browser = await chromium.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
const url = 'http://127.0.0.1:4885';
try {
 const page = await browser.newPage({viewport:{width:1440,height:1000}});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(url+'/fixture?mode=problems');
 let f=JSON.parse(await page.locator('body').innerText());
 await page.addInitScript(()=>localStorage.setItem('duet:lang',JSON.stringify('ru')));
 await page.goto(url+'/r/'+f.id);
 await page.locator('#banner').getByText('Полнота ревью не подтверждена').waitFor();
 await page.locator('#tree').getByText('image.bin',{exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#sc').textContent.includes('бинарный'));
 assert.equal(await page.locator('#winbar [data-act="accept"]').count(),0);
 await page.screenshot({path:'/tmp/duet-7p2-binary.png',fullPage:true});
 await page.locator('#tree').getByText('another-review.md',{exact:true}).click();
 await page.locator('#sc a[href="/r/'+f.otherID+'"]').waitFor();
 await page.screenshot({path:'/tmp/duet-7p2-conflict.png',fullPage:true});
 await page.locator('#tree').getByText('document.md',{exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#sc').textContent.includes('старый формат'));
 await page.screenshot({path:'/tmp/duet-7p2-unknown.png',fullPage:true});
 await page.locator('#winbar [data-act="accept"]').click();
 let v=await page.evaluate(id=>fetch('/api/reviews/'+id).then(r=>r.json()),f.id);assert.equal(v.accepted,false);
 await page.evaluate(()=>fetch('/advance?mode=fix'));
 await page.locator('[data-act="recheck-repository"]').click();
 await page.waitForFunction(id=>fetch('/api/reviews/'+id).then(r=>r.json()).then(r=>r.completeness.complete && r.accepted),f.id);
 await page.waitForFunction(()=>!document.querySelector('#banner').textContent.includes('Полнота'));
 // Поздний файл обнаруживается прямо перед новым сообщением о полном принятии.
 await page.goto(url+'/fixture');f=JSON.parse(await page.locator('body').innerText());
 await page.goto(url+'/r/'+f.id);
 await page.locator('#tree').getByText('document.md',{exact:true}).click();
 await page.evaluate(()=>fetch('/advance?mode=late'));
 await page.locator('#winbar [data-act="accept"]').click();
 await page.locator('#tree').getByText('late.md',{exact:true}).waitFor();
 v=await page.evaluate(id=>fetch('/api/reviews/'+id).then(r=>r.json()),f.id);assert.equal(v.accepted,false);
 await page.screenshot({path:'/tmp/duet-7p2-late.png',fullPage:true});
 // Открытое окно сохраняет показанную версию при следующем сборе.
 await page.goto(url+'/fixture');f=JSON.parse(await page.locator('body').innerText());
 await page.goto(url+'/r/'+f.id);await page.locator('#tree').getByText('document.md',{exact:true}).click();
 await page.evaluate(()=>fetch('/advance?mode=response'));
 await page.locator('#winbar [data-act="response"]').click();
 await page.waitForFunction(()=>document.querySelector('#sc').textContent.includes('Первый ответ'));
 const before=await page.locator('#sc').innerText();
 await page.evaluate(()=>fetch('/advance?mode=continue'));
 await page.waitForFunction(()=>document.body.textContent.includes('открыть заново') || document.body.textContent.includes('Открыть заново'));
 assert.equal(await page.locator('#sc').innerText(),before);
 await page.screenshot({path:'/tmp/duet-7p2-stale.png',fullPage:true});
 // Ошибка определения репозитория остаётся общей причиной, не режимом вне репозитория.
 await page.goto(url+'/fixture');f=JSON.parse(await page.locator('body').innerText());
 await page.goto(url+'/r/'+f.id);
 await page.evaluate(()=>fetch('/advance?mode=failure'));
 await page.locator('#banner').getByText('Полнота ревью не подтверждена').waitFor();
 v=await page.evaluate(id=>fetch('/api/reviews/'+id).then(r=>r.json()),f.id);
 assert.equal(v.completeness.complete,false);
 assert(v.completeness.problems.some(p=>p.code==='detect' && !p.path));
 await page.screenshot({path:'/tmp/duet-7p2-provider-error.png',fullPage:true});
 assert.deepEqual(errors,[]);
 console.log('Полнота: бинарный файл, конфликт, старая база, повторная проверка, поздний файл и стабильное окно ответа — успешно.');
} finally {await browser.close();}
