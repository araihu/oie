// Real isolated engine/SQLite + BFF. No protocol account is ever connected.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises'),os=require('node:os');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const typeBurst=(input,value)=>input.evaluate((node,text)=>{
 for(const character of text){
  node.setRangeText(character,node.value.length,node.value.length,'end');
  node.dispatchEvent(new InputEvent('input',{bubbles:true,data:character,inputType:'insertText'}));
 }
},value);
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const temp=await fs.mkdtemp(path.join(os.tmpdir(),'oie-shared-search-')),children=[],errors=[],findings=[];let browser,lastPage,sequence=0;
 const until=async(fn,label)=>{for(let i=0;i<200;i++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label)};
 const port=async()=>{const server=http.createServer();server.listen(0,'127.0.0.1');await once(server,'listening');const value=server.address().port;await new Promise(r=>server.close(r));return value};
 const start=args=>{const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),args,{stdio:['ignore','ignore','pipe']});let stderr='';child.stderr.on('data',v=>stderr+=v);children.push(child);child.stderrText=()=>stderr;return child};
 try{
  const engine='http://127.0.0.1:'+await port(),base='http://127.0.0.1:'+await port();
  start(['--role','hosted','--listen',new URL(engine).host,'--data-dir',path.join(temp,'engine')]);
  await until(async()=>{try{return(await fetch(engine+'/api/v1/engine')).ok}catch{return false}},'engine startup');
  const post=async(url,body)=>{const res=await fetch(engine+url,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':id(++sequence)},body:JSON.stringify(body)});const value=await res.json();assert(res.ok,JSON.stringify(value));return value};
  const alpha=await post('/api/v1/accounts',{protocol:'xmpp',address:'alpha@fixture.invalid',label:'Personal'}),beta=await post('/api/v1/accounts',{protocol:'xmpp',address:'beta@fixture.invalid',label:'Work'});
  const conversation=await post('/api/v1/conversations',{accountId:alpha.id,address:'peer@fixture.invalid'}),other=await post('/api/v1/conversations',{accountId:beta.id,address:'other@fixture.invalid'});
  for(const name of ['report','summary'])await post(`/api/v1/conversations/${conversation.id}/messages`,{originalText:`Relatório de orçamento e faturamento https://files.invalid/${name}.pdf`});
  await post(`/api/v1/conversations/${other.id}/messages`,{originalText:'Relatório de orçamento e faturamento https://files.invalid/private.pdf'});
  for(let i=0;i<60;i++)await post(`/api/v1/conversations/${conversation.id}/messages`,{originalText:'Unrelated newer source https://files.invalid/unrelated-'+i+'.pdf'});
  await until(async()=>{const page=await(await fetch(engine+'/api/v1/shared-content?type=documents')).json();return page.indexing?.state==='complete'&&page.indexing.totalMessages===63},'catalog indexed');
  const web=start(['--role','bff','--listen',new URL(base).host,'--engine-url',engine]);
  await until(async()=>{try{return(await fetch(base+'/?section=media')).ok}catch{return false}},'BFF startup '+web.stderrText());
  browser=await chromium.launch({headless:true});
  for(const config of [{width:1440,locale:'en',theme:'light'},{width:390,locale:'pt-BR',theme:'dark'}]){
   const context=await browser.newContext({viewport:{width:config.width,height:900},locale:config.locale});const page=await context.newPage(),requests=[],infoRequests=[];lastPage=page;let writes=0,documents=0;
   page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{const url=new URL(request.url());if(request.isNavigationRequest()&&request.resourceType()==='document')documents++;if(request.method()!=='GET'&&request.method()!=='HEAD')writes++;if(url.origin===base&&url.pathname==='/'&&url.searchParams.has('q'))requests.push(url.searchParams.get('q'));if(url.origin===base&&url.pathname==='/'&&url.searchParams.has('infoQuery'))infoRequests.push({query:url.searchParams.get('infoQuery'),cursor:url.searchParams.get('infoCursor'),inbox:url.searchParams.get('q'),chat:url.searchParams.get('chat'),account:url.searchParams.get('account'),hx:request.headers()['hx-request'],target:request.headers()['hx-target']})});
   await page.goto(base+'/?'+new URLSearchParams({section:'media',contentType:'documents',account:alpha.id,lang:config.locale,theme:config.theme}));
   await page.waitForFunction(()=>window.OieRuntime?.generation>0);const input=page.locator('#shared-search');await input.focus();await input.evaluate(node=>window.searchInput=node);
   assert.equal(await page.locator('[data-shared-item]').count(),50,'only first page initially loaded');assert.equal(await page.locator('#shared-content-search button[type=submit]').count(),0);
   const generation=await page.evaluate(()=>window.OieRuntime.generation),history=await page.evaluate(()=>history.length);
   await typeBurst(input,'orcamento');
   await until(async()=>new URL(page.url()).searchParams.get('q')==='orcamento'&&await page.locator('[data-shared-item]').count()===2,'accent-insensitive older-page matches');
   assert.deepEqual(requests,['orcamento'],'typing is debounced');assert.equal(await page.evaluate(()=>window.history.length),history);
   assert(await input.evaluate(node=>node===window.searchInput&&document.activeElement===node&&node.selectionStart===9));assert.equal(await page.evaluate(()=>window.OieRuntime.generation),generation);
   await input.fill('faturamneto');await until(async()=>new URL(page.url()).searchParams.get('q')==='faturamneto'&&await page.locator('[data-shared-item]').count()===2,'transposition typo');
   assert.equal(await input.inputValue(),'faturamneto');assert(await input.evaluate(node=>document.activeElement===node));
   // A response arriving between a newer keystroke and its debounce must be discarded.
   let release,held=false;const gate=new Promise(resolve=>release=resolve);
   await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('q')==='orcamento'){const response=await route.fetch();held=true;await gate;try{await route.fulfill({response})}catch{}return}await route.continue()});
   await input.fill('orcamento');await until(()=>held,'old response held');await input.fill('orcamxnto');release();
   await until(async()=>new URL(page.url()).searchParams.get('q')==='orcamxnto'&&await page.locator('[data-shared-item]').count()===2,'newer query wins');assert.equal(await input.inputValue(),'orcamxnto');
   await page.unroute('**/*');
   const beforeComposition=requests.length;
   await input.evaluate(node=>{node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));node.value='faturamneto';node.dispatchEvent(new InputEvent('input',{bubbles:true,isComposing:true,data:'faturamneto'}))});
   await page.waitForTimeout(450);assert.equal(requests.length,beforeComposition,'IME composition does not fetch');
   await input.evaluate(node=>node.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));await until(()=>new URL(page.url()).searchParams.get('q')==='faturamneto','composition completion searches');
   // An SSE refresh started for an earlier query cannot restore its old cards.
   let refreshHeld=false,releaseRefresh;const refreshGate=new Promise(resolve=>releaseRefresh=resolve);
   await page.route('**/ui/refresh?**',async route=>{if(new URL(route.request().url()).searchParams.get('q')==='faturamneto'){const response=await route.fetch();refreshHeld=true;await refreshGate;try{await route.fulfill({response})}catch{}return}await route.continue()});
   await page.evaluate(()=>document.dispatchEvent(new CustomEvent('oie:refresh-request')));await until(()=>refreshHeld,'old SSE snapshot held');
   await input.fill('zzzzzzzzzz');await until(async()=>new URL(page.url()).searchParams.get('q')==='zzzzzzzzzz'&&await page.locator('[data-shared-item]').count()===0,'new no-match query');releaseRefresh();await page.waitForTimeout(400);assert.equal(await page.locator('[data-shared-item]').count(),0);await page.unroute('**/ui/refresh?**');
   // Switching category carries the current typed query and disposes its old request.
   let categoryHeld=false,releaseCategory;const categoryGate=new Promise(resolve=>releaseCategory=resolve);
   await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('q')==='faturamneto'&&url.searchParams.get('contentType')==='documents'){const response=await route.fetch();categoryHeld=true;await categoryGate;try{await route.fulfill({response})}catch{}return}await route.continue()});
   await input.fill('faturamneto');await until(()=>categoryHeld,'old category request held');await page.locator('[data-shared-tab=links]').click();await until(()=>new URL(page.url()).searchParams.get('contentType')==='links','new category');releaseCategory();await page.waitForTimeout(400);assert.equal(new URL(page.url()).searchParams.get('contentType'),'links');assert.equal(await input.inputValue(),'faturamneto');await page.unroute('**/*');
   await page.locator('[data-shared-tab=documents]').click();await until(async()=>new URL(page.url()).searchParams.get('contentType')==='documents'&&await page.locator('[data-shared-item]').count()===2,'return to matching documents');
   // Clearing also updates results without Enter and retains account/category context.
   await input.fill('');await until(async()=>!new URL(page.url()).searchParams.get('q')&&await page.locator('[data-shared-item]').count()===50,'clear search');
   assert.equal(new URL(page.url()).searchParams.get('account'),alpha.id);assert.equal(new URL(page.url()).searchParams.get('contentType'),'documents');assert.equal(documents,1);assert.equal(writes,0);
   // Picker typeahead stays local; committing an account applies it immediately.
   await input.fill('faturamneto');await until(()=>new URL(page.url()).searchParams.get('q')==='faturamneto','query before scope change');
   await page.evaluate(()=>{window.catalogForm=document.getElementById('shared-content-search')});
   let accountStreams=0;
   page.on('request',request=>{if(request.resourceType()==='eventsource')accountStreams++;});
   const pickerRequests=requests.length,pickerURL=page.url();await page.getByRole('combobox',{name:config.locale==='en'?'Account':'Conta',exact:true}).click();await page.locator('#shared-account [data-combobox-search]').fill('Work');await page.waitForTimeout(350);assert.equal(requests.length,pickerRequests,'account typeahead must not search the catalog');assert.equal(page.url(),pickerURL);await page.getByRole('option',{name:'Work · beta@fixture.invalid',exact:true}).click();
   await until(async()=>new URL(page.url()).searchParams.get('account')===beta.id&&await page.locator('[data-shared-item]').count()===1,'account scope applied');
   assert(await page.evaluate(()=>window.catalogForm===document.getElementById('shared-content-search')));
   assert.equal(await page.evaluate(()=>window.OieRuntime.generation),generation);
   assert.equal(accountStreams,0);
   assert.equal(new URL(page.url()).searchParams.get('q'),'faturamneto');assert.equal(new URL(page.url()).searchParams.get('contentType'),'documents');assert.equal(new URL(page.url()).searchParams.get('contentCursor'),null);assert.equal(requests.length,pickerRequests+1,'one account commit produces one fragment request');assert.equal(documents,1);assert.equal(writes,0);assert((await page.locator('[data-account-switcher]').first().textContent()).includes('Work'));assert.equal(await page.locator('body').getAttribute('data-account-id'),'');
   await page.locator('#shared-account [role=listbox]').waitFor({state:'hidden'});
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
   const axe=await page.evaluate(async()=>{
    await document.fonts.ready;
    await new Promise(requestAnimationFrame);
    await new Promise(requestAnimationFrame);
    await Promise.all(document.getAnimations().filter(animation=>animation.effect?.getComputedTiming().endTime<1000).map(animation=>animation.finished.catch(()=>{})));
    return window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}});
   });
   assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(node=>({target:node.target,summary:node.failureSummary}))})),[]);
   const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await page.screenshot({path:path.join(artifacts,`shared-search-${config.width}.png`)});
   // The conversation drawer searches only its shared catalog and keeps the
   // form, input and composer alive while swapping its results region.
   const inbox='Main inbox filter',drawerParams=new URLSearchParams({chat:conversation.id,account:alpha.id,q:inbox,filter:'unread',open:'details',infoView:'shared',infoType:'documents',infoCursor:id(999),lang:config.locale,theme:config.theme});
   await page.goto(base+'/?'+drawerParams);let drawerInput=page.locator('#info-shared-search');await drawerInput.waitFor({state:'visible'});assert.equal(await page.locator('#info-shared-search-form button[type=submit]').count(),0);assert.equal(await page.locator('#info-shared-search-results').count(),1);
   await drawerInput.focus();await page.evaluate(()=>{window.drawerSearchInput=document.querySelector('#info-shared-search');window.drawerSearchForm=document.querySelector('#info-shared-search-form');window.drawerComposer=document.querySelector('#message-composer')});const drawerGeneration=await page.evaluate(()=>window.OieRuntime.generation),drawerHistory=await page.evaluate(()=>history.length),drawerDocuments=documents;infoRequests.length=0;
   await typeBurst(drawerInput,'orcamento');await until(async()=>new URL(page.url()).searchParams.get('infoQuery')==='orcamento'&&await page.locator('#info-shared-results [data-shared-item]').count()===2,'drawer older-page fuzzy matches while typing');assert.equal(infoRequests.length,1,'drawer typing is debounced');const {target,...requestScope}=infoRequests[0];assert.deepEqual(requestScope,{query:'orcamento',cursor:null,inbox,chat:conversation.id,account:alpha.id,hx:'true'});assert.equal(await page.locator(target).getAttribute('id'),'info-shared-search-results');assert.equal(await page.evaluate(()=>history.length),drawerHistory);assert.equal(await page.evaluate(()=>window.OieRuntime.generation),drawerGeneration);assert(await drawerInput.evaluate(node=>node===window.drawerSearchInput&&document.activeElement===node&&node.selectionStart===9));assert(await page.evaluate(()=>window.drawerSearchForm===document.querySelector('#info-shared-search-form')&&window.drawerComposer===document.querySelector('#message-composer')));
   await drawerInput.fill('faturamneto');await until(async()=>new URL(page.url()).searchParams.get('infoQuery')==='faturamneto'&&await page.locator('#info-shared-results [data-shared-item]').count()===2,'drawer transposition typo');assert.equal(new URL(page.url()).searchParams.get('q'),inbox);assert.equal(new URL(page.url()).searchParams.get('filter'),'unread');assert.equal(new URL(page.url()).searchParams.get('infoCursor'),null);
   let oldDrawerHeld=false,releaseDrawer;const drawerGate=new Promise(resolve=>releaseDrawer=resolve);await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('infoQuery')==='orcamento'){const response=await route.fetch();oldDrawerHeld=true;await drawerGate;try{await route.fulfill({response})}catch{}return}await route.continue()});await drawerInput.fill('orcamento');await until(()=>oldDrawerHeld,'drawer old response held');await drawerInput.fill('orcamxnto');releaseDrawer();await until(async()=>new URL(page.url()).searchParams.get('infoQuery')==='orcamxnto'&&await page.locator('#info-shared-results [data-shared-item]').count()===2,'newer drawer input wins');assert(await drawerInput.evaluate(node=>node===window.drawerSearchInput&&document.activeElement===node));await page.unroute('**/*');
   const infoBeforeIME=infoRequests.length;await drawerInput.evaluate(node=>{node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));node.value='faturamneto';node.dispatchEvent(new InputEvent('input',{bubbles:true,isComposing:true,data:'faturamneto'}))});await page.waitForTimeout(450);assert.equal(infoRequests.length,infoBeforeIME);await drawerInput.evaluate(node=>node.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));await until(()=>new URL(page.url()).searchParams.get('infoQuery')==='faturamneto','drawer IME completion');
   let drawerRefreshHeld=false,releaseDrawerRefresh;const drawerRefreshGate=new Promise(resolve=>releaseDrawerRefresh=resolve);await page.route('**/ui/refresh?**',async route=>{if(new URL(route.request().url()).searchParams.get('infoQuery')==='faturamneto'){const response=await route.fetch();drawerRefreshHeld=true;await drawerRefreshGate;try{await route.fulfill({response})}catch{}return}await route.continue()});await page.evaluate(()=>document.dispatchEvent(new CustomEvent('oie:refresh-request')));await until(()=>drawerRefreshHeld,'old drawer SSE held');await drawerInput.fill('zzzzzzzzzz');await until(async()=>new URL(page.url()).searchParams.get('infoQuery')==='zzzzzzzzzz'&&await page.locator('#info-shared-results [data-shared-item]').count()===0,'drawer no-match query');releaseDrawerRefresh();await page.waitForTimeout(400);assert.equal(await page.locator('#info-shared-results [data-shared-item]').count(),0);await page.unroute('**/ui/refresh?**');
   let drawerTabHeld=false,releaseDrawerTab;const drawerTabGate=new Promise(resolve=>releaseDrawerTab=resolve);await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('infoQuery')==='faturamneto'&&url.searchParams.get('infoType')==='documents'){const response=await route.fetch();drawerTabHeld=true;await drawerTabGate;try{await route.fulfill({response})}catch{}return}await route.continue()});await drawerInput.fill('faturamneto');await until(()=>drawerTabHeld,'old drawer tab query held');await page.locator('[data-shared-tab=links]').click();await until(()=>new URL(page.url()).searchParams.get('infoType')==='links','drawer links tab');releaseDrawerTab();await page.waitForTimeout(400);drawerInput=page.locator('#info-shared-search');assert.equal(await drawerInput.inputValue(),'faturamneto');assert.equal(new URL(page.url()).searchParams.get('infoQuery'),'faturamneto');assert.equal(new URL(page.url()).searchParams.get('q'),inbox);assert.equal(await page.locator('#info-shared-results').getAttribute('data-content-type'),'links');assert.equal(await page.locator('#info-shared-results [data-shared-item]').count(),0);await page.unroute('**/*');
   await page.locator('[data-shared-tab=documents]').click();await until(async()=>new URL(page.url()).searchParams.get('infoType')==='documents'&&await page.locator('#info-shared-results [data-shared-item]').count()===2,'drawer returns to matching documents');await drawerInput.fill('');await until(async()=>!new URL(page.url()).searchParams.get('infoQuery')&&await page.locator('#info-shared-results [data-shared-item]').count()===50,'drawer clear without Enter');assert.equal(new URL(page.url()).searchParams.get('infoCursor'),null);
   let drawerNavHeld=false,releaseDrawerNav;const drawerNavGate=new Promise(resolve=>releaseDrawerNav=resolve);await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('infoQuery')==='orcamento'){const response=await route.fetch();drawerNavHeld=true;await drawerNavGate;try{await route.fulfill({response})}catch{}return}await route.continue()});await drawerInput.fill('orcamento');await until(()=>drawerNavHeld,'drawer request held before Back');await page.locator('[data-info-back]').click();await until(async()=>await page.locator('[data-info-entry=documents]').count()===1,'drawer Back restores overview');releaseDrawerNav();await page.waitForTimeout(400);assert.equal(await page.locator('#info-shared-search').count(),0);assert.equal(new URL(page.url()).searchParams.get('q'),inbox);assert.equal(new URL(page.url()).searchParams.get('infoView'),null);assert.equal(documents,drawerDocuments);assert.equal(writes,0);assert(await page.evaluate(()=>window.drawerComposer===document.querySelector('#message-composer')));await page.unroute('**/*');
   findings.push({...config,realSQLite:true,documents,writes,debounced:true,focusPreserved:true,staleResponseRejected:true,ime:true,fuzzyOlderPages:true,drawer:{noSubmitButton:true,stableInputAndComposer:true,cursorReset:true,independentInboxQuery:true,debounce:true,ime:true,staleQueryAndSSE:true,tabCarry:true,navigationAbort:true}});await context.close();
  }
  assert.deepEqual(errors,[]);await fs.writeFile(path.join(__dirname,'artifacts','shared-search-report.json'),JSON.stringify({findings,pageErrors:errors},null,2));console.log('PASS: native SQLite fuzzy search, debounce, stable input/caret, stale responses, IME, scope and partial swaps');
 }catch(error){
  if(lastPage&&!lastPage.isClosed()){
   console.error('Failed URL',lastPage.url());await lastPage.screenshot({path:'/tmp/oie-fuzzy-browser-failure.png'}).catch(()=>{});console.error('Page errors',errors);
  }
  throw error;
 }finally{
  await browser?.close();for(const child of children.reverse()){if(child.exitCode===null){child.kill('SIGTERM');await Promise.race([once(child,'exit'),new Promise(resolve=>setTimeout(resolve,3000))]);if(child.exitCode===null)child.kill('SIGKILL')}}await fs.rm(temp,{recursive:true,force:true});
 }
})().catch(error=>{console.error(error);process.exitCode=1});
