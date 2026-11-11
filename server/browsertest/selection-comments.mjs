const {default: puppeteer}=await import(process.argv[2]);
const browser=await puppeteer.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
try{const page=await browser.newPage();await page.goto('http://127.0.0.1:4882');
console.log(await page.evaluate(async()=>{
 const {renderDoc}=await import('/web/js/view.js');const {placeCards,drawSelection,pointPos}=await import('/web/js/comments.js');const {target}=await import('/web/js/anchors.js');
 const text='Верхний абзац.\n\nАбзац с комментарием.\n\nНижний абзац для нового комментария.\n\nЕщё ниже текст.\n';
 const root=document.createElement('div');root.style='position:fixed;inset:0;z-index:100;overflow:auto';document.body.append(root);
 const c={id:'test',whole:false,range:{start:BufferLength('Верхний абзац.\n\n'),end:BufferLength('Верхний абзац.\n\nАбзац с комментарием.')}};
 function BufferLength(s){return new TextEncoder().encode(s).length}
 const list=[{c,html:'<div class="cc">Комментарий</div>'}];
 for(const cols of [1,2])for(const below of [false,true]){
  root.innerHTML=renderDoc([{kind:'added',new:text.split('\n').map((text,i)=>({n:i+1,text}))}],'',text,{cols,words:{added:'новое'}});
  placeCards(root,list,text);
  const span=[...root.querySelectorAll('[data-p]')].find(e=>e.textContent.startsWith(below?'Нижний':'Верхний'));
  const node=document.createTreeWalker(span,NodeFilter.SHOW_TEXT).nextNode();const r=document.createRange();r.setStart(node,1);r.setEnd(node,10);const expected=r.toString();
  for(let i=0;i<4;i++){
   placeCards(root,list,text,r);
   if(r.toString()!==expected)throw Error('selection lost '+JSON.stringify({cols,below}));
   const a=pointPos(r.startContainer,r.startOffset),b=pointPos(r.endContainer,r.endOffset);
   if(!target(a,b,'',text,[{kind:'added',new:text.split('\n').map((text,i)=>({n:i+1,text}))}])?.range)throw Error('cannot comment');
   if(!drawSelection(root,r).length)throw Error('highlight disappeared');
  }
 }
 return 'OK: выделение выше и ниже карточки сохраняется, место нового комментария доступно, обе колонки';
}));}finally{await browser.close()}
