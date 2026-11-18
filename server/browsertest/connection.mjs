const {default: puppeteer}=await import(process.argv[2]);
const b=await puppeteer.launch({executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',headless:true});
try{const p=await b.newPage();await p.evaluateOnNewDocument(()=>{
 window.healthMode='ok';const original=window.fetch;window.fetch=(url,opts)=>url==='/api/health'?(window.healthMode==='fail'?Promise.reject(Error('offline')):window.healthMode==='slow'?new Promise(resolve=>window.finishHealth=resolve):Promise.resolve(new Response(JSON.stringify({build:'test'})))):original(url,opts);
 window.SharedWorker=undefined;
 window.EventSource=class {constructor(){window.stream=this;this.listeners={}}addEventListener(type,fn){this.listeners[type]=fn}close(){}};
});await p.goto('http://127.0.0.1:4882');
const text=()=>p.evaluate(()=>document.querySelector('#over').innerText);
await p.evaluate(()=>window.stream.listeners.error());await new Promise(r=>setTimeout(r,100));
let t=await text();if(!t.includes('Reconnecting')||t.includes('has stopped'))throw Error(t);console.log('OK stream lost, server responds');
await p.evaluate(()=>{window.healthMode='fail';window.stream.listeners.error()});await new Promise(r=>setTimeout(r,100));t=await text();if(!t.includes('server unavailable'))throw Error(t);console.log('OK server unavailable');
await p.evaluate(()=>window.healthMode='ok');await new Promise(r=>setTimeout(r,3300));if(!(await text()).includes('Reconnecting'))throw Error('probe did not recover');console.log('OK health recovers before stream');
await p.evaluate(()=>{window.healthMode='slow';window.stream.listeners.error();window.stream.listeners.hello({data:JSON.stringify({build:'test'})});window.finishHealth(new Response(JSON.stringify({build:'test'})))});await new Promise(r=>setTimeout(r,100));if((await text()).includes('Reconnecting'))throw Error('stale probe replaced connection');console.log('OK hello wins over stale health check');
}finally{await b.close()}
