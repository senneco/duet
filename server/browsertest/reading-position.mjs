// Тестовое ядро с /fixture должно возвращать {id, path, other} и ответ со вставкой выше текста.
const {default: puppeteer} = await import(process.argv[2]);
const url=process.argv[3] || 'http://127.0.0.1:4882';
const b=await puppeteer.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true,defaultViewport:{width:1440,height:900}});
try {
for(const action of ['accept','accept-same','accept-delete','accept-untouched','close','reject']) {
 const p=await b.newPage();const errors=[];p.on('pageerror',e=>errors.push(e.message));
 await p.goto(url);const f=await p.evaluate(action=>fetch('/fixture'+(action==='accept-delete'?'?delete=1':action==='accept-untouched'?'?untouched=1':'')).then(r=>r.json()),action);
 await p.goto(url+'/r/'+f.id);await new Promise(r=>setTimeout(r,500));
 await p.evaluate(path=>document.querySelector(`[data-file="${path}"]`).click(),f.path);await new Promise(r=>setTimeout(r,300));
 const before=await p.evaluate(()=>{const sc=document.querySelector('#sc');const h=[...sc.querySelectorAll('h2')].find(e=>e.textContent==='Раздел 50');sc.scrollTop+=h.getBoundingClientRect().top-sc.getBoundingClientRect().top-35;return h.getBoundingClientRect().top});
 await p.keyboard.press('Space');await p.keyboard.press('r');await new Promise(r=>setTimeout(r,300));
 await p.evaluate(path=>document.querySelector(`[data-file="${path}"]`).click(),action==='accept-same'?f.path:f.other);await new Promise(r=>setTimeout(r,300));
 if(action.startsWith('accept'))await p.click('[data-act="resp-accept"]');
 else if(action==='close')await p.keyboard.press('Escape');
 else {await p.click('[data-act="resp-reject"]');await new Promise(r=>setTimeout(r,100));await p.keyboard.press('Enter')}
 await new Promise(r=>setTimeout(r,600));
 const after=await p.evaluate(()=>({top:[...document.querySelectorAll('#sc h2')].find(e=>e.textContent==='Раздел 50')?.getBoundingClientRect().top,resp:document.querySelector('#app').classList.contains('rmode'),path:document.querySelector('.tree .open')?.dataset.file}));
 console.log(JSON.stringify({action,before,after,errors}));
 if(after.resp||Math.abs(after.top-before)>3||errors.length)throw Error('position not preserved');
 await p.screenshot({path:'/tmp/duet-position-'+action+'.png'});await p.close();
}
} finally{await b.close()}
