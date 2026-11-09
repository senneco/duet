// Запуск: node server/browsertest/selection.mjs /путь/к/puppeteer-core/lib/esm/puppeteer/puppeteer-core.js
const {default: puppeteer} = await import(process.argv[2]);
const b=await puppeteer.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true,defaultViewport:{width:1440,height:900}});
try {
await b.defaultBrowserContext().overridePermissions('http://duet.localhost:4848', ['clipboard-read', 'clipboard-write', 'clipboard-sanitized-write']);
const p=await b.newPage(); await p.goto('http://duet.localhost:4848/');
const id=await p.evaluate(async()=>{const rs=await fetch('/api/reviews').then(r=>r.json());return rs.find(r=>r.title?.includes('3419'))?.id||rs[0]?.id});
await p.goto('http://duet.localhost:4848/r/'+id); await new Promise(r=>setTimeout(r,1000));
await p.evaluate(() => {
 const sc=document.querySelector('#sc'); sc.innerHTML='<div class="doc"><p><span data-p="n0.0">Первое вто<strong>рое</strong> третье четвёртое пятое шестое седьмое восьмое девятое десятое</span></p></div>';
 sc.querySelector('p').style.width='260px';

});
const points=await p.evaluate(()=>{
 const root=document.querySelector('#sc p'); const walker=document.createTreeWalker(root,NodeFilter.SHOW_TEXT);const nodes=[];let n,txt='';while(n=walker.nextNode()){nodes.push([n,txt.length]);txt+=n.textContent}
 return [...txt.matchAll(/[\p{L}]+/gu)].map(m=>{ const i=m.index+Math.floor(m[0].length/2);const [n,start]=nodes.find(([n,s])=>i>=s&&i<s+n.length);const r=document.createRange();r.setStart(n,i-start);r.setEnd(n,i-start+1);const b=r.getBoundingClientRect();return {x:b.x+b.width/2,y:b.y+b.height/2,word:m[0]}})
});
const copy=async(mod)=>{await p.keyboard.down(mod);await p.keyboard.press('c');await p.keyboard.up(mod);await new Promise(r=>setTimeout(r,50));return p.evaluate(()=>navigator.clipboard.readText())};
const gesture=async(a,b,back)=>{
 await p.keyboard.press('Escape');await p.mouse.click(points[a].x,points[a].y);await p.mouse.down({clickCount:2});
 if(b!==undefined)await p.mouse.move(points[b].x,points[b].y,{steps:8});
 if(back!==undefined)await p.mouse.move(points[back].x,points[back].y,{steps:8});
 await p.mouse.up({clickCount:2});
};
const assert=(actual,expected)=>{if(actual!==expected)throw Error(JSON.stringify({actual,expected}));console.log('OK',expected)};
await gesture(1); assert(await copy('Control'),'второе');
await gesture(1,5);assert(await copy('Control'),'второе третье четвёртое пятое шестое');
assert(await copy('Meta'),'второе третье четвёртое пятое шестое');
await gesture(5,1);assert(await copy('Control'),'второе третье четвёртое пятое шестое');
await gesture(3,7,1);assert(await copy('Control'),'второе третье четвёртое');
// Обычное протягивание остаётся по символам.
await p.mouse.move(points[0].x,points[0].y);await p.mouse.down({clickCount:1});await p.mouse.move(points[1].x,points[1].y,{steps:5});await p.mouse.up({clickCount:1});
const chars=await copy('Control');if(chars==='Первое второе'||!chars.length)throw Error('character drag failed');console.log('OK character selection',chars);
// Копирование в поле ввода не перехватывается выделением документа.
await p.evaluate(()=>{const t=document.createElement('textarea');t.value='Текст поля';document.body.append(t);t.focus();t.select();window.fieldCopy=null;document.addEventListener('copy',e=>{window.fieldCopy=e.defaultPrevented},{once:true});document.execCommand('copy')});
assert(await p.evaluate(()=>window.fieldCopy),false);
await p.screenshot({path:'/tmp/duet-selection-after.png'});
} finally{await b.close()}
