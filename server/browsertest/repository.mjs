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
 await page.locator('#banner').getByText('Проверка изменений',{exact:true}).waitFor();
 assert.match(await page.locator('#banner').textContent(),/image\.bin/);
 await page.locator('#tree').getByText('image.bin',{exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#sc').textContent.includes('бинарный'));
 assert.equal(await page.locator('#winbar [data-act="accept"]').count(),0);
 await page.screenshot({path:'/tmp/duet-7p2-binary.png',fullPage:true});
 await page.locator('#tree').getByText('another-review.md',{exact:true}).click();
 await page.locator('#sc a[href="/r/'+f.otherID+'"]').waitFor();
 await page.screenshot({path:'/tmp/duet-7p2-conflict.png',fullPage:true});
 await page.locator('#tree').getByText('document.md',{exact:true}).click();
 await page.locator('#winbar').getByText('document.md',{exact:true}).waitFor();
 await page.locator('#sc .repository-warning').waitFor({state:'detached'});
 await page.screenshot({path:'/tmp/duet-7p2-unknown.png',fullPage:true});
 await page.locator('#winbar [data-act="accept"]').click();
 let v=await page.evaluate(id=>fetch('/api/reviews/'+id).then(r=>r.json()),f.id);assert.equal(v.accepted,false);
 await page.evaluate(()=>fetch('/advance?mode=fix'));
 await page.locator('[data-act="recheck-repository"]').click();
 await page.waitForFunction(id=>fetch('/api/reviews/'+id).then(r=>r.json()).then(r=>r.completeness.complete && !r.accepted),f.id);
 await page.waitForFunction(()=>!document.querySelector('#banner').textContent.trim());
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
 await page.locator('#banner').getByText('Проверка изменений',{exact:true}).waitFor();
 v=await page.evaluate(id=>fetch('/api/reviews/'+id).then(r=>r.json()),f.id);
 assert.equal(v.completeness.complete,false);
 assert(v.completeness.problems.some(p=>p.code==='detect' && !p.path));
 await page.screenshot({path:'/tmp/duet-7p2-provider-error.png',fullPage:true});
 // Перенос из предыдущего ревью сохраняет комментарий и остаётся в текущей вкладке.
 await page.goto(url+'/fixture?mode=recover');f=JSON.parse(await page.locator('body').innerText());
 await page.goto(url+'/r/'+f.id);
 assert.equal(await page.locator('#banner').textContent(),'');
 await page.waitForFunction(id=>fetch('/api/reviews/'+id).then(r=>r.json()).then(r=>r.completeness.complete),f.id);
 await page.locator('#tree').getByText('another-review.md',{exact:true}).click();
 const recovered = await page.evaluate(({ id, other })=>fetch('/api/reviews/'+id+'/file?path='+encodeURIComponent(other)).then(r=>r.json()),f);
 assert(recovered.comments.some(c=>JSON.stringify(c).includes('Сохранить комментарий')));
 assert.equal(recovered.response, 'Исправленный материал');
 await page.locator('#winbar [data-act="response"]').click();
 await page.locator('#sc .cc .m.ag').filter({hasText:'Исправил по замечанию'}).waitFor();
 await page.locator('[data-act="resp-accept"]').click();
 await page.locator('#winbar [data-act="accept"]').waitFor();
 await page.locator('#winbar [data-act="comment"]').click();
 await page.locator('#ced textarea').fill('Один комментарий');
 let requests = 0;
 let release;
 const pending = new Promise(resolve=>{release=resolve;});
 await page.route('**/api/reviews/*/comment', async route => {
   requests++;
   await pending;
   await route.continue();
 });
 await page.locator('[data-eact="save"]').evaluate(button=>{button.click();button.click();});
 await page.locator('#ced [role="status"]').getByText('Сохраняем…').waitFor();
 release();
 await page.locator('#ced').waitFor({state:'detached'});
 assert.equal(requests, 1);

 // Откат скрывает файл сразу, но окно ответа и ветка остаются доступны.
 await page.goto(url+'/fixture');
 f=JSON.parse(await page.locator('body').innerText());
 await page.goto(url+'/r/'+f.id);
 await page.locator('#winbar').getByText('В итоговом diff',{exact:true}).waitFor();
 await page.evaluate(()=>fetch('/advance?mode=rollback'));
 const outside = page.locator('#tree [data-outside]');
 await outside.waitFor();
 assert.equal(await outside.getAttribute('aria-expanded'), 'false');
 assert.equal(await page.locator('#tree [data-file]').count(), 0);
 await page.locator('#tabline [data-act="response"]').click();
 await page.locator('#sc').getByText('Исходный текст.',{exact:true}).waitFor();
 await page.locator('[data-act="resp-accept"]').click();
 await outside.click();
 await page.locator('#tree [data-file]').click();
 await page.locator('#winbar').getByText('Вне итогового diff',{exact:true}).waitFor();
 await page.locator('#sc .cc .m.ag').filter({hasText:'Вернул исходный текст'}).waitFor();
 assert.deepEqual(errors,[]);
 console.log('Полнота: бинарный файл, конфликт, старая база, повторная проверка, поздний файл и стабильное окно ответа — успешно.');
} finally {await browser.close();}
