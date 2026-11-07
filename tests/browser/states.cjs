// Browser evidence for empty and actual transport-unavailable BFF states.
const assert=require('node:assert/strict');
const http=require('node:http');
const path=require('node:path');
const {spawn}=require('node:child_process');
const {once}=require('node:events');
const {chromium}=require('playwright');
const fs=require('node:fs/promises');
(async()=>{
 const api=http.createServer((req,res)=>{
  res.setHeader('Content-Type','application/json');
  if(req.url==='/api/v1/engine')res.end(JSON.stringify({id:'019a5df0-0000-7000-8000-000000000001',role:'hosted',apiVersion:'0.1.0',eventCursor:'019a5df0-0000-7000-8000-000000000001:0'}));
  else res.end('{"items":[]}');
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen',`127.0.0.1:${port}`,'--engine-url',`http://127.0.0.1:${api.address().port}`],{stdio:['ignore','ignore','pipe']});
 const base=`http://127.0.0.1:${port}`;let started=false;
 const browser=await chromium.launch({headless:true});const findings=[];await fs.mkdir('artifacts',{recursive:true});
 try{
  for(let attempt=0;attempt<50;attempt++){try{if((await fetch(base)).status===200){started=true;break}}catch{}await new Promise(r=>setTimeout(r,100))}
  assert(started,'isolated BFF did not start');
  for(const state of ['empty','unavailable','unavailableSelected','notFound']){
   if(state==='unavailable')await new Promise(r=>api.close(r));
   for(const width of [390,1440])for(const theme of ['goshtoso','minimal'])for(const mode of ['light','dark'])for(const locale of ['en','pt-BR']){
    const page=await browser.newPage({viewport:{width,height:900},locale});const errors=[];page.on('pageerror',e=>errors.push(e.message));
    const route=state==='notFound'?'/missing':'/';const selected=state==='unavailableSelected'?'&chat=019a5df0-0000-7000-8000-000000000102':'';const response=await page.goto(base+route+`?theme=${mode}&lang=${locale}`+selected);assert.equal(response.status(),state==='empty'?200:state==='notFound'?404:503);
    await page.waitForFunction(()=>window.Alpine&&window.htmx);await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    const text=state==='empty'?(locale==='en'?'No conversations yet':'Nenhuma conversa ainda'):state==='notFound'?(locale==='en'?'Page not found':'Página não encontrada'):(locale==='en'?'Engine unavailable':'Motor indisponível');
    const target=state==='unavailableSelected'?page.locator('.oie-chat-pane'):page;assert(await target.getByText(text,{exact:true}).isVisible());
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
    const a11y=await page.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
    assert.deepEqual(a11y.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`${state}/${width}/${theme}/${mode}/${locale}`);
    assert.deepEqual(errors,[]);
    findings.push({state,width,theme,mode,locale,status:response.status(),accessibilityViolations:a11y.violations.length});
    if(theme==='goshtoso'&&mode==='dark'&&locale==='pt-BR')await page.screenshot({path:`artifacts/${state}-${width}.png`,fullPage:true});
    await page.close();
   }
  }
  await fs.writeFile('artifacts/states-report.json',JSON.stringify({findings},null,2));console.log('PASS: 64 empty/transport-failure/selected-chat/missing-route presentation states');
 }finally{await browser.close();child.kill('SIGTERM');if(api.listening)await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
