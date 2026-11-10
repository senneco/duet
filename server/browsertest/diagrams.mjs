const {default: puppeteer}=await import(process.argv[2]);
const browser=await puppeteer.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true,defaultViewport:{width:1440,height:1000}});
try{const page=await browser.newPage();const external=[];page.on('request',r=>{if(!r.url().startsWith('http://127.0.0.1:4882')&&!r.url().startsWith('data:'))external.push(r.url())});
page.on('pageerror',e=>console.log('PAGE',e.message));await page.goto('http://127.0.0.1:4882');
for(const [dark,cols] of [[false,1],[true,1],[false,2],[true,2]]){
 await page.evaluate(async ({dark,cols})=>{
  const {renderDoc}=await import('/web/js/view.js');const {renderDiagrams}=await import('/web/js/diagrams.js');
  const text=['# Схемы','```mermaid','flowchart LR','A[Запрос] --> B{Проверка}','B -->|Да| C[Готово]','B -->|Нет| D[Повторить]','```','```mermaid','stateDiagram-v2','[*] --> free','free: Свободен','busy: Занят','free --> busy: запрос','busy --> free: ответ','```','```mermaid','sequenceDiagram','participant U as Пользователь','participant S as Сервер','U->>S: Запрос','S-->>U: Ответ','```','```mermaid','flowchart LR','bad [ syntax {{{','```'].join('\n');
  document.body.classList.toggle('dark',dark);document.body.classList.toggle('light',!dark);let root=document.querySelector('#diagram-test');if(!root){root=document.createElement('div');root.id='diagram-test';root.style='position:fixed;inset:0;overflow:auto;z-index:100;background:var(--bg)';document.body.append(root)}root.innerHTML='<div class="page">'+renderDoc([{kind:'added',new:text.split('\n').map((text,i)=>({n:i+1,text}))}], '', text, {cols,words:{added:'новое'}})+'</div>';renderDiagrams(root);
 },{dark,cols});
 await page.waitForFunction(()=>document.querySelectorAll('.diagram-canvas svg').length===3&&document.querySelectorAll('.diagram-error').length===1,{timeout:10000});
 const result=await page.evaluate(()=>({svgs:document.querySelectorAll('.diagram-canvas svg').length,source:document.querySelectorAll('.diagram details pre [data-p]').length,error:document.querySelector('.diagram-error').textContent}));
 console.log({dark,cols,...result});
 const originalStyle=await page.$eval('.diagram-canvas svg',svg=>[...svg.querySelectorAll('.node rect, .edgePaths path')].map(n=>({fill:getComputedStyle(n).fill,stroke:getComputedStyle(n).stroke})));
 await page.click('.diagram-expand');await page.waitForSelector('dialog[open] svg');
 const centered=await page.$eval('dialog',d=>{const r=d.getBoundingClientRect();return Math.abs(r.left-(innerWidth-r.width)/2)<2&&Math.abs(r.top-(innerHeight-r.height)/2)<2});if(!centered)throw Error('dialog not centered');
 const enlargedStyle=await page.$eval('dialog svg',svg=>[...svg.querySelectorAll('.node rect, .edgePaths path')].map(n=>({fill:getComputedStyle(n).fill,stroke:getComputedStyle(n).stroke})));
 if(JSON.stringify(originalStyle)!==JSON.stringify(enlargedStyle))throw Error('expanded SVG styles changed');
 const duplicates=await page.evaluate(()=>{const ids=[...document.querySelectorAll('svg [id],svg[id]')].map(n=>n.id);return ids.length!==new Set(ids).size});if(duplicates)throw Error('duplicate SVG ids');
 await page.screenshot({path:'/tmp/duet-expanded-'+(dark?'dark':'light')+'.png'});await page.evaluate(()=>{const r=document.querySelector('dialog input');r.value='150';r.dispatchEvent(new Event('input'))});
 if(await page.$eval('dialog svg',e=>e.style.width)!=='150%')throw Error('zoom failed');await page.keyboard.press('Escape');await page.waitForFunction(()=>!document.querySelector('dialog'));
 await page.screenshot({path:'/tmp/duet-diagrams-'+(dark?'dark':'light')+'.png'});
}
if(external.length)throw Error('External requests: '+external.join(','));
}finally{await browser.close()}
