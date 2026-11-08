// Fixed drawer action, independent list scrolling and read-only navigation.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),revision=id(2),state={count:3,seq:0,changed:false,writes:0},streams=new Set();
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');
  const send=(data,code=200)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  if(req.method!=='GET'){state.writes++;return send({status:500},500)}
  if(u.pathname==='/api/v1/engine')return send({id:engine,role:'hosted',apiVersion:'0.8.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send({items:Array.from({length:state.count},(_,i)=>({id:id(100+i),revisionId:revision,protocol:'xmpp',label:'Account '+String(i+1).padStart(2,'0')+(state.changed?' updated':''),address:'account'+i+'@fixture',createdAt:'2026-11-08T12:00:00Z'}))});
  if(u.pathname.endsWith('/connection'))return send({accountId:u.pathname.split('/')[4],state:'not_connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send({accountId:u.pathname.split('/')[4],state:'not_started',retainedMessages:0,conversations:0});
  if(u.pathname==='/api/v1/conversations'||u.pathname.endsWith('/avatars'))return send({items:[]});
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send({status:404},404);
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let stderr='';child.stderr.on('data',s=>stderr+=s);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],results=[];
 const until=async(fn,label)=>{for(let i=0;i<200;i++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error(label+' '+stderr)};
 const emit=()=>{state.seq++;for(const s of streams)s.write(`id: ${engine}:${state.seq}\nevent: account.connection_changed\ndata: {}\n\n`)};
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  for(const width of [390,818,1440])for(const mode of ['light','dark'])for(const locale of ['en','pt-BR']){
   const page=await browser.newPage({viewport:{width,height:885},locale});page.on('pageerror',e=>errors.push(e.message));
   const params=new URLSearchParams({open:'accounts',q:'preserve query',filter:'unread',theme:mode,lang:locale});
   for(const count of [3,35]){
    state.count=count;state.changed=false;await page.goto(base+'/?'+params);
    const drawer=page.getByRole('dialog',{name:locale==='en'?'Accounts':'Contas',exact:true});await drawer.waitFor({state:'visible'});
    await page.waitForFunction(()=>{const b=document.querySelector('.oie-account-drawer').getBoundingClientRect();return b.left>=-1&&b.right<=innerWidth+1});
    const list=page.locator('#account-overview'),action=page.locator('[data-account-add]');await action.waitFor({state:'visible'});
    const footer=await page.locator('.oie-account-list-footer').boundingBox(),body=await page.locator('#accounts-body').boundingBox(),button=await action.boundingBox();
    assert(Math.abs(footer.y+footer.height-body.y-body.height)<2,'footer not at body bottom');assert(button.y>=footer.y&&button.y+button.height<=885);
    assert.equal(await list.locator('[data-manage-account]').count(),count);
    if(count===35){
     assert(await list.evaluate(el=>el.scrollHeight>el.clientHeight),'fixture did not overflow');
     await list.hover();await page.mouse.wheel(0,900);await until(()=>list.evaluate(el=>el.scrollTop>0),'wheel scroll');
     await list.locator('[data-manage-account]').last().scrollIntoViewIfNeeded();
     const last=await list.locator('[data-manage-account]').last().boundingBox();assert(last.y+last.height<=footer.y+1,'last account obscured by footer');
     assert(Math.abs((await action.boundingBox()).y-button.y)<1,'action moved with list');
     const scroll=await list.evaluate(el=>el.scrollTop);await action.focus();state.changed=true;emit();
     await until(async()=>await list.locator('[data-manage-account]').last().textContent().then(s=>s.includes('updated')),'live account refresh');
     assert(Math.abs(await list.evaluate(el=>el.scrollTop)-scroll)<2,'live refresh lost list scroll');assert(await action.evaluate(el=>el===document.activeElement));
     assert.equal(await page.locator('#accounts-body').evaluate(el=>el.scrollTop),0);
    }
    await action.focus();await page.keyboard.press('Enter');await page.locator('#register-account-form').waitFor({state:'visible'});
    for(const key of ['open','q','filter'])assert.equal(new URL(page.url()).searchParams.get(key),params.get(key));
    assert.equal(await page.locator('html').getAttribute('lang'),locale);assert.equal(await page.locator('html').getAttribute('data-mode'),mode);
    await page.locator('[data-account-back]').click();await action.waitFor({state:'visible'});
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   }
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const a11y=await page.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(a11y.violations.map(v=>v.id),[]);
   results.push({width,mode,locale,accounts:35,axe:0});const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});if(width===818&&mode==='dark'&&locale==='pt-BR')await page.screenshot({path:path.join(artifacts,'account-drawer-overflow.png')});await page.close();
  }
  assert.equal(state.writes,0);assert.deepEqual(errors,[]);await fs.writeFile(path.join(__dirname,'artifacts','account-drawer-report.json'),JSON.stringify({results,mutations:state.writes,errors},null,2));
  console.log('PASS: drawer footer stays fixed with3 and35 accounts; independent scrolling, last-row clearance, live refresh, keyboard navigation and12 responsive/locale cases');
 }finally{await browser.close();child.kill('SIGTERM');for(const s of streams)s.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
