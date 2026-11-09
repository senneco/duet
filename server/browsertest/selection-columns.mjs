const {default: puppeteer}=await import(process.argv[2]);
const b=await puppeteer.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
try{const p=await b.newPage();await p.goto('http://127.0.0.1:4882');
console.log(await p.evaluate(async()=>{
 const {selectionRanges,selectionText}=await import('/web/js/selection.js');
 const {drawSelection}=await import('/web/js/comments.js');
 const sc=document.querySelector('#sc');sc.innerHTML='<div class="code2">'+[0,1,2].map(i=>`<div class="rr"><span class="no">${i}</span><span class="tx" data-p="o${i}.0">left${i}</span><span class="no r2">${i}</span><span class="tx" data-p="n${i}.0">right${i}</span></div>`).join('')+'</div>';
 for(const [side,label] of [['o','left'],['n','right']]){
  const nodes=[...sc.querySelectorAll(`[data-p^="${side}"]`)];const r=document.createRange();r.setStart(nodes[0].firstChild,1);r.setEnd(nodes[2].firstChild,3);
  const expected=label.slice(1)+'0\n'+label+'1\n'+label.slice(0,3);
  if(selectionText(r)!==expected)throw Error(JSON.stringify({side,text:selectionText(r),expected}));
  const rect=nodes[0].getBoundingClientRect();
  const ranges=selectionRanges(r);if(ranges.length!==3)throw Error('missing text');
  for(const part of ranges)for(const box of part.getClientRects())if(box.left<rect.left-1||box.right>rect.right+1)throw Error('opposite column highlighted');
  drawSelection(sc,r);
  if(!sc.querySelector('svg.selsvg path'))throw Error('no highlight');
 }
 return 'OK: обе колонки, многострочное выделение и копируемый текст';
}));await p.screenshot({path:'/tmp/duet-columns.png'});}finally{await b.close()}
