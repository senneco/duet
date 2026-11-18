// node server/browsertest/plugin.mjs /path/to/playwright-core/index.mjs
import assert from 'node:assert/strict';
const { chromium } = await import(process.argv[2]);
const browser = await chromium.launch({ executablePath: '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
try {
 const page = await browser.newPage({viewport:{width:1440,height:1000}});
 const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>localStorage.setItem('duet:lang',JSON.stringify('ru')));
 const url='http://127.0.0.1:4886';
 const f=await (await page.request.get(url+'/fixture')).json();
 await page.goto(url+'/r/'+f.id);
 await page.locator('#tree').getByText('document.md',{exact:true}).click();
 await page.waitForFunction(()=>document.querySelector('#sc').textContent.includes('Новый текст'));
 let v=await (await page.request.get(url+'/api/reviews/'+f.id)).json();
 assert.equal(v.completeness.workspace.provider,'example-git');assert.equal(v.completeness.complete,true);
 await page.request.get(url+'/plugin?broken=1');
 await page.locator('#winbar [data-act="accept"]').click();
 await page.locator('#banner').getByText('Полнота ревью не подтверждена').waitFor();
 v=await (await page.request.get(url+'/api/reviews/'+f.id)).json();assert.equal(v.accepted,false);
 assert(v.completeness.problems.some(p=>p.message.includes('example-git')));
 await page.getByText('Полнота ревью не подтверждена', {exact:true}).nth(1).waitFor();
 await page.screenshot({path:'/tmp/duet-7p3-plugin-error.png',fullPage:true});
 await page.request.get(url+'/plugin?broken=0');
 await page.locator('[data-act="recheck-repository"]').click();
 await page.waitForFunction(id=>fetch('/api/reviews/'+id).then(r=>r.json()).then(r=>r.accepted && r.completeness.complete),f.id);
 await page.screenshot({path:'/tmp/duet-7p3-plugin-recovered.png',fullPage:true});
 assert.deepEqual(errors,[]);
 console.log('Внешний плагин: исходник, ошибка при принятии, блокировка и восстановление — успешно.');
} finally { await browser.close(); }
