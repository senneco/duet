// node server/browsertest/updates.mjs /path/to/playwright-core/index.mjs
import assert from 'node:assert/strict';
const {chromium}=await import(process.argv[2]);
const browser=await chromium.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
try{
 const ctx=await browser.newContext({viewport:{width:1440,height:1000}});
 await ctx.addInitScript(()=>localStorage.setItem('duet:lang',JSON.stringify('ru')));
 const p=await ctx.newPage(),errors=[];p.on('pageerror',e=>errors.push(e.message));
 const base='http://127.0.0.1:4896';const f=await(await p.request.get(base+'/fixture')).json();
 await p.goto(base+'/r/'+f.id);await p.locator('#tree').getByText('document.md',{exact:true}).click();
 await p.locator('.update-notice').getByText('Доступна новая версия duet.').waitFor();
 await p.screenshot({path:'/private/tmp/duet-update-notice.png',fullPage:true});
 await p.waitForTimeout(6500);assert(await p.locator('.update-notice').isVisible(),'notification vanished by timeout');
 await p.locator('.update-notice').getByRole('button',{name:'Позже',exact:true}).click();assert(!(await p.locator('.update-notice').isVisible()));
 await p.locator('.update-button').click();await p.locator('.update-notice').getByRole('button',{name:'Обновить',exact:true}).click();
 await p.locator('.update-notice').getByText('Собираю новую версию… Можно продолжать чтение.').waitFor();
 assert.equal(await p.locator('#app').evaluate(e=>e.inert),false,'build blocked review');
 await p.request.get(base+'/fixture?phase=ready');
 const other=await ctx.newPage();other.on('pageerror',e=>errors.push(e.message));await other.goto(base+'/r/'+f.id);
 await other.locator('#tree').getByText('document.md',{exact:true}).click();await other.keyboard.press('Space');await other.keyboard.press('c');
 await other.locator('textarea').waitFor();await other.locator('textarea').fill('Черновик в другой вкладке — не терять');
 const blocked=await p.request.post(base+'/api/update/prepare',{data:{}});assert.equal(blocked.status(),409);
 assert.equal(await other.locator('textarea').inputValue(),'Черновик в другой вкладке — не терять');
 await p.waitForFunction(()=>!document.querySelector('#app').inert);
 await other.screenshot({path:'/private/tmp/duet-update-draft.png',fullPage:true});
 await other.close();await p.waitForTimeout(500);
 const ready=await p.request.post(base+'/api/update/prepare',{data:{}});assert.equal(ready.status(),200,await ready.text());
 const {token}=await ready.json();assert.equal(await p.locator('#app').evaluate(e=>e.inert),true);
 await p.request.post(base+'/api/update/cancel',{data:{token}});await p.waitForFunction(()=>!document.querySelector('#app').inert);
 assert.deepEqual(errors,[]);console.log('Уведомление, отсутствие таймера, закрытие/возврат, сборка без блокировки, черновик другой вкладки, применение и отмена — успешно.');
}finally{await browser.close()}
