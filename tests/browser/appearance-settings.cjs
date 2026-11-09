// Isolated BFF settings navigation. No real account or protocol server is used.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id='019a5df0-0000-7000-8000-000000000001';
(async()=>{
 const streams=new Set();
 const api=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  if(req.method!=='GET'){res.writeHead(405);return res.end();}
  if(url.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);req.on('close',()=>streams.delete(res));return;}
  res.setHeader('Content-Type','application/json');
  if(url.pathname==='/api/v1/engine')return res.end(JSON.stringify({id,role:'hosted',apiVersion:'0.16.0',eventCursor:id+':0'}));
  if(url.pathname==='/api/v1/shared-content')return res.end(JSON.stringify({items:[],indexing:{state:'complete',indexedMessages:0,totalMessages:0}}));
  if(['/api/v1/accounts','/api/v1/conversations','/api/v1/starred-messages'].includes(url.pathname))return res.end('{"items":[]}');
  res.writeHead(404);res.end('{"status":404}');
 });api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:'ignore'});
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[];
 const until=async(fn,label)=>{for(let n=0;n<150;n++){if(await fn())return;await new Promise(r=>setTimeout(r,40));}throw Error(label);};
 try{
  await until(async()=>{try{return(await fetch(base)).ok;}catch{return false;}},'BFF starts');
  for(const width of [390,1440])for(const locale of ['en','pt-BR']){
   const context=await browser.newContext({viewport:{width,height:900},locale,reducedMotion:'reduce',colorScheme:'light'});
   const page=await context.newPage();page.setDefaultTimeout(8000);page.on('pageerror',error=>errors.push(error.message));
   await page.goto(`${base}/?lang=${locale}&open=settings`);await page.waitForFunction(()=>window.OieRuntime?.generation>0);
   const dialog=page.getByRole('dialog',{name:locale==='en'?'Settings':'Configurações',exact:true});await dialog.waitFor({state:'visible'});
   const names=locale==='en'?['System','Light','Dark']:['Sistema','Claro','Escuro'];
   const group=dialog.getByRole('radiogroup');assert.equal(await group.getByRole('radio').count(),3);await group.getByRole('radio',{name:names[0],exact:true}).waitFor();
   await page.evaluate(()=>window.appearanceDocumentToken='preserved');
   await group.getByRole('radio',{name:names[2],exact:true}).click({noWaitAfter:true});
   await page.waitForFunction(()=>document.documentElement.dataset.mode==='dark');await dialog.waitFor({state:'visible'});
   await page.waitForFunction(()=>document.activeElement?.id==='appearance-dark');
   assert.equal(await group.locator('svg').count(),3);
   assert.equal(await page.evaluate(()=>window.appearanceDocumentToken),'preserved');assert(await group.getByRole('radio',{name:names[2],exact:true}).isChecked());
   assert.equal((await context.cookies()).find(c=>c.name==='oie_theme')?.value,'dark');
   await page.keyboard.press('ArrowLeft');
   await page.waitForFunction(()=>document.documentElement.dataset.mode==='light');await dialog.waitFor({state:'visible'});
   await page.waitForFunction(()=>document.activeElement?.id==='appearance-light');assert(await group.getByRole('radio',{name:names[1],exact:true}).isChecked());
   await page.keyboard.press('ArrowRight');await page.waitForFunction(()=>document.documentElement.dataset.mode==='dark'&&document.activeElement?.id==='appearance-dark');await page.keyboard.press('ArrowLeft');await page.waitForFunction(()=>document.documentElement.dataset.mode==='light'&&document.activeElement?.id==='appearance-light');
   await page.goto(`${base}/?lang=${locale}`);await page.waitForFunction(()=>window.OieRuntime?.generation>0);if(width===390)await page.getByRole('button',{name:locale==='en'?'Open navigation':'Abrir navegação',exact:true}).click();await page.locator('[data-navigation-settings]:visible').click();await dialog.waitFor({state:'visible'});
   assert.equal(await page.evaluate(()=>document.documentElement.dataset.mode),'light');assert(await group.getByRole('radio',{name:names[1],exact:true}).isChecked());
   await group.getByRole('radio',{name:names[0],exact:true}).click({noWaitAfter:true});await page.waitForFunction(()=>document.documentElement.dataset.mode==='system');
   await page.emulateMedia({colorScheme:'dark'});await page.waitForFunction(()=>document.documentElement.classList.contains('dark'));
   await page.emulateMedia({colorScheme:'light'});await page.waitForFunction(()=>!document.documentElement.classList.contains('dark'));
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const result=await dialog.evaluate(node=>axe.run(node,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(result.violations.map(v=>v.id),[]);
   await context.close();
  }
  assert.deepEqual(errors,[]);console.log('PASS: appearance segmented radios, icons, HTMX document preservation, drawer, keyboard, cookie persistence, system theme, EN/PT, mobile/desktop and accessibility');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
