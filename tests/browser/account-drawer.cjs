// Fixed drawer action, independent list scrolling and read-only navigation.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),revision=id(2),state={count:3,seq:0,changed:false,writes:0},streams=new Set();
 const names=new Map(), renames=[];
 const accounts=()=>Array.from({length:state.count},(_,i)=>{
  const accountId=id(100+i);
  return {id:accountId,revisionId:revision,protocol:'xmpp',label:'Account '+String(i+1).padStart(2,'0')+(state.changed?' updated':''),address:'account'+i+'@fixture',createdAt:'2026-11-08T12:00:00Z',...names.get(accountId)};
 });
 const api=http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');
  const send=(data,code=200)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  const rename=u.pathname.match(/^\/api\/v1\/accounts\/([^/]+)\/label$/);
  if(req.method==='PUT'&&rename){
   const account=accounts().find(item=>item.id===rename[1]);
   let body='';
   for await(const chunk of req)body+=chunk;
   const input=JSON.parse(body);
   renames.push({accountId:rename[1],...input});
   await new Promise(resolve=>setTimeout(resolve,600));
   if(!account)return send({status:404},404);
   if(account.revisionId!==input.expectedRevisionId)return send({status:409},409);
   const updated={...account,label:input.label.trim(),revisionId:id(800+renames.length)};
   names.set(account.id,updated);
   emit('account.updated');
   return send(updated);
  }
  if(req.method!=='GET'){state.writes++;return send({status:500},500)}
  if(u.pathname==='/api/v1/engine')return send({id:engine,role:'hosted',apiVersion:'0.8.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send({items:accounts()});
  const account=accounts().find(item=>u.pathname==='/api/v1/accounts/'+item.id);
  if(account)return send(account);
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
 const emit=(event='account.connection_changed')=>{state.seq++;for(const s of streams)s.write(`id: ${engine}:${state.seq}\nevent: ${event}\ndata: {}\n\n`)};
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
     await list.evaluate(el=>{
      const rows=el.querySelectorAll('[data-manage-account]');
      rows[rows.length-1].scrollIntoView({block:'nearest',behavior:'instant'});
     });
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
   await page.addScriptTag({path:require.resolve('./fixtures/axe-core/axe.min.js')});const a11y=await page.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(a11y.violations.map(v=>v.id),[]);
   results.push({width,mode,locale,accounts:35,axe:0});const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});if(width===818&&mode==='dark'&&locale==='pt-BR')await page.screenshot({path:path.join(artifacts,'account-drawer-overflow.png')});await page.close();
  }
  const phoneContext=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true,deviceScaleFactor:2,locale:'pt-BR',reducedMotion:'reduce'});
  const phone=await phoneContext.newPage();
  for (const theme of ['light', 'dark']) {
   await phone.goto(base+'/?lang=pt-BR&theme='+theme);
   const switcher = phone.locator('.oie-mobile-nav .oie-profile-trigger');
   await switcher.waitFor();
   const safeArea = await switcher.evaluate(trigger => {
    document.documentElement.style.setProperty('--oie-safe-bottom', '34px');
    const shell = document.querySelector('.oie-shell');
    const pane = document.querySelector('.oie-list-pane');
    const paneBottom = pane.getBoundingClientRect().bottom;
    return {
     shellBackground: getComputedStyle(shell).backgroundColor,
     paneBackground: getComputedStyle(pane).backgroundColor,
     inset: shell.getBoundingClientRect().bottom - paneBottom,
     triggerBottom: trigger.getBoundingClientRect().bottom,
     paneBottom,
    };
   });
   assert.equal(safeArea.shellBackground, safeArea.paneBackground, theme+' safe area continues the panel background');
   assert.equal(safeArea.inset, 34, 'the system gesture area is reserved');
   assert(safeArea.triggerBottom <= safeArea.paneBottom, 'the account switcher stays above the gesture area');
  }
  await phone.goto(base+'/?open=accounts&accountPage=add&lang=pt-BR');
  const addDrawer=phone.getByRole('dialog',{name:'Contas',exact:true});
  const close=addDrawer.getByRole('button',{name:'Fechar',exact:true});
  const field=addDrawer.locator('#register-address');
  await close.waitFor();
  assert.equal(await field.evaluate(el=>getComputedStyle(el).fontSize),'16px');
  await field.focus();
  const keyboardGeometry=await phone.evaluate(async()=>{
   const viewport=window.visualViewport;
   const innerHeightDescriptor=Object.getOwnPropertyDescriptor(window,'innerHeight');
   const nextPaint=()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));
   try {
    // iOS can report equal viewport heights after panning the layout to a focused field.
    Object.defineProperty(window,'innerHeight',{configurable:true,value:416});
    Object.defineProperties(viewport,{
     height:{configurable:true,value:416},
     offsetTop:{configurable:true,value:428},
    });
    viewport.dispatchEvent(new Event('resize'));
    await nextPaint();
    const shell=document.querySelector('.oie-shell');
    const {top,bottom}=shell.getBoundingClientRect();
    return {top,bottom,fixed:getComputedStyle(shell).position==='fixed'};
   } finally {
    if(innerHeightDescriptor)Object.defineProperty(window,'innerHeight',innerHeightDescriptor);
    else delete window.innerHeight;
    delete viewport.height;
    delete viewport.offsetTop;
    document.activeElement.blur();
    viewport.dispatchEvent(new Event('resize'));
    await nextPaint();
   }
  });
  assert.deepEqual(keyboardGeometry,{top:428,bottom:844,fixed:true});
  assert.equal(await phone.locator('html').getAttribute('data-oie-keyboard'),null);
  await phone.evaluate(()=>{
   document.documentElement.style.setProperty('--oie-safe-top','47px');
   document.documentElement.style.setProperty('--oie-safe-right','14px');
   document.documentElement.style.setProperty('--oie-safe-bottom','34px');
  });
  const safe=await addDrawer.boundingBox();
  assert(safe.y>=47&&safe.x+safe.width<=376&&safe.y+safe.height<=810);
  const viewport=await phoneContext.newCDPSession(phone);
  await viewport.send('Emulation.setPageScaleFactor',{pageScaleFactor:1.5});
  await phone.waitForFunction(()=>visualViewport.scale===1.5);
  await until(()=>close.evaluate(button=>{
   const box=button.getBoundingClientRect(),vv=visualViewport;
   return box.width>=44&&box.height>=44&&box.left>=vv.offsetLeft&&box.top>=vv.offsetTop&&
    box.right<=vv.offsetLeft+vv.width-13&&box.bottom<=vv.offsetTop+vv.height-33;
  }),'zoomed drawer close stays in the visual viewport');
  await close.tap();
  await addDrawer.waitFor({state:'hidden'});
  await phone.setViewportSize({width:874,height:402});
  await phone.goto(base+'/?open=accounts&accountPage=add&lang=pt-BR');
  await field.focus();
  await phone.evaluate(()=>{
   const viewport=window.visualViewport;
   Object.defineProperty(viewport,'height',{configurable:true,value:110});
   viewport.dispatchEvent(new Event('resize'));
  });
  await phone.waitForFunction(()=>document.documentElement.hasAttribute('data-oie-keyboard'));
  assert(await addDrawer.evaluate(panel=>{
   const body=panel.querySelector('#accounts-body');
   const save=panel.querySelector('#register-account-form button[type=submit]');
   body.scrollTop=body.scrollHeight;
   const visible=body.getBoundingClientRect(),button=save.getBoundingClientRect();
   return button.top>=visible.top&&button.bottom<=visible.bottom;
  }),'the entire register action scrolls above the landscape keyboard');
  await phoneContext.close();
  state.count=3;
  state.changed=false;
  const accountId=id(100);
  for(const width of [390,1440])for(const locale of ['en','pt-BR']){
   names.clear();
   const page=await browser.newPage({viewport:{width,height:885},locale,reducedMotion:'reduce'});
   page.on('pageerror',error=>errors.push(error.message));
   const pt=locale==='pt-BR';
   const params=new URLSearchParams({open:'accounts',managedAccount:accountId,account:accountId,lang:locale});
   await page.goto(base+'/?'+params);
   const openEditor=()=>page.getByRole('button',{name:pt?'Editar nome':'Edit name',exact:true}).click();
   const heading=page.locator('#account-profile-name');
   const editor=page.locator('#account-name-dialog');
   const form=page.locator('#account-name-form');
   const field=form.locator('input[name="label"]');
   const save=form.getByRole('button',{name:pt?'Salvar nome':'Save name',exact:true});
   const initialRequests=renames.length;
   await page.getByRole('dialog',{name:pt?'Perfil':'Profile',exact:true}).waitFor();
   assert(await editor.isHidden());
   await openEditor();
   await field.fill('Unsaved');
   await page.keyboard.press('Escape');
   await editor.waitFor({state:'hidden'});
   await openEditor();
   assert.equal(await field.inputValue(),'Account 01');
   assert.equal(renames.length,initialRequests);

   const savedName='Nome novo '+ 'longo'.repeat(16);
   await field.fill(savedName);
   const currentRevision=id(300+width);
   names.set(accountId,{label:'Outro nome',revisionId:currentRevision});
   emit('account.updated');
   await until(async()=>await heading.textContent()==='Outro nome','rename from another device appears');
   assert.equal(await field.inputValue(),savedName);
   const feedback=form.locator('[data-form-feedback]');
   await save.focus();
   await page.waitForFunction(()=>document.querySelector('.oie-account-drawer').getBoundingClientRect().right<=innerWidth+1);
   const startGeometry=()=>page.evaluate(()=>{
    const selectors=['#account-name-input','#account-name-form button','#account-detail-scroll'];
    const geometry=()=>selectors.flatMap(selector=>{
     const node=document.querySelector(selector),rect=node.getBoundingClientRect();
     return [rect.x,rect.y,rect.width,rect.height,node.scrollTop];
    });
    const origin=geometry(),sample={running:true,frames:0,max:0};
    const frame=()=>{
     if(!sample.running)return;
     geometry().forEach((value,index)=>sample.max=Math.max(sample.max,Math.abs(value-origin[index])));
     sample.frames++;
     requestAnimationFrame(frame);
    };
    window.accountNameGeometry=sample;
    requestAnimationFrame(frame);
   });
   const stopGeometry=async()=>{
    const geometry=await page.evaluate(()=>{
     window.accountNameGeometry.running=false;
     return window.accountNameGeometry;
    });
    assert(geometry.frames>=5&&geometry.max<=1,JSON.stringify(geometry));
   };
   await startGeometry();
   await page.keyboard.press('Enter');
   await feedback.filter({hasText:pt?'O nome mudou em outro lugar. Confira e salve novamente.':'The name changed elsewhere. Review it and save again.'}).waitFor();
   await stopGeometry();
   assert(await save.evaluate(button=>button===document.activeElement));
   assert.equal(await field.inputValue(),savedName);
   assert.equal(await heading.textContent(),'Outro nome');
   assert.equal(renames.at(-1).expectedRevisionId,revision);

   await startGeometry();
   await page.keyboard.press('Enter');
   await until(()=>renames.length===initialRequests+2,'rename retry reaches engine');
   await field.fill('Rascunho novo');
   await feedback.filter({hasText:pt?'Nome da conta atualizado.':'Account name saved.'}).waitFor();
   await until(async()=>await heading.textContent()===savedName,'saved name appears in details');
   await stopGeometry();
   assert.equal(await field.inputValue(),'Rascunho novo');
   assert.equal(renames.at(-1).expectedRevisionId,currentRevision);
   assert.equal(renames.at(-1).accountId,accountId);
   assert.equal(await page.locator('[data-account-switcher]').first().getAttribute('data-active-account-name'),savedName);
   assert.equal(accounts()[1].label,'Account 02');
   await page.close();
  }
  assert.equal(state.writes,0);assert.deepEqual(errors,[]);await fs.writeFile(path.join(__dirname,'artifacts','account-drawer-report.json'),JSON.stringify({results,mutations:state.writes,errors},null,2));
  console.log('PASS: localized offline account rename preserves keyboard focus, delayed geometry and drafts with revision conflict and retry');
  console.log('PASS: drawer footer stays fixed with3 and35 accounts; independent scrolling, last-row clearance, live refresh, keyboard navigation and12 responsive/locale cases');
 }finally{await browser.close();child.kill('SIGTERM');for(const s of streams)s.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
