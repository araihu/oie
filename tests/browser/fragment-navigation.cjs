// Navigation exercises real HTMX against an independent API, without a browser
// protocol client or any connection to the user's retained accounts.
const assert = require('node:assert/strict');
const http = require('node:http');
const path = require('node:path');
const fs = require('node:fs/promises');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require('playwright');
const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, label) {
  for (let n = 0; n < 600; n++) {if (await check()) return; await wait(50);}
  throw Error('Timed out: ' + label);
}
async function assertMobileControlOwnership(page) {
  const controls=await page.evaluate(()=>({
    navigation:document.querySelector('button[aria-label="Open navigation"]')?.getAttribute('aria-controls'),
    account:document.querySelector('[data-navigation-footer="mobile-navigation"] [data-account-switcher]')?.getAttribute('aria-controls'),
  }));
  assert.deepEqual(controls,{navigation:'mobile-navigation-panel',account:'account-switcher-mobile-navigation-panel'},'nested account popover must not replace sidebar trigger ownership');
}
(async () => {
  const engine=id(1), alpha=id(2), beta=id(3), chatA=id(4), chatB=id(5), revision=id(6);
  const accounts=[{id:alpha,label:'Personal',address:'alice@fixture'},{id:beta,label:'Work',address:'work@fixture'}].map(a=>({...a,revisionId:revision,protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}));
  const chats=[{id:chatA,accountId:alpha,title:'Family',address:'family@fixture'},{id:chatB,accountId:beta,title:'Team',address:'team@fixture'}].map(c=>({...c,revisionId:revision,kind:'direct',updatedAt:'2026-11-08T12:00:00Z',unreadCount:1,archived:false,favorite:true,lastMessage:'Retained fixture'}));
  const messages=chats.map((c,n)=>({id:id(100+n),revisionId:revision,accountId:c.accountId,conversationId:c.id,sender:{id:id(20+n),displayName:n?'Sam':'Bob'},originalText:'Retained source '+n+' https://example.test/item.png',createdAt:'2026-11-08T12:00:00Z',isOwn:false,sendState:'received'}));
  const catalog=Array.from({length:57},(_,n)=>{const kind=n<53?'image':n<55?'link':'document',m=messages[n%2];return {id:id(500+n),revisionId:revision,kind,url:'https://example.test/'+n+(kind==='image'?'.png':kind==='document'?'.pdf':''),fileName:kind==='document'?n+'.pdf':'',accountLabel:accounts[n%2].label,conversationTitle:chats[n%2].title,message:m};});
  const streams=new Set(),operations=new Map(),state={effects:0,seq:0,reads:[],searchReads:[]};let releaseDelayedSearch;
  const emit=()=>{state.seq++;for(const stream of streams)stream.write(`id: ${engine}:${state.seq}\nevent: message.created\ndata: {}\n\n`);};
  const api=http.createServer(async (req,res)=>{
    const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');
    const send=(status,data)=>{res.statusCode=status;res.end(JSON.stringify(data));};
    let text='';for await(const chunk of req)text+=chunk;const body=text?JSON.parse(text):{};
    if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.10.0',eventCursor:engine+':'+state.seq});
    if(u.pathname==='/api/v1/accounts')return send(200,{items:accounts});
    if(u.pathname.endsWith('/connection'))return send(200,{accountId:u.pathname.includes(beta)?beta:alpha,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
    if(u.pathname.endsWith('/history'))return send(200,{accountId:u.pathname.includes(beta)?beta:alpha,state:'complete',retainedMessages:2,conversations:1,updatedAt:'2026-11-08T12:00:00Z'});
    if(u.pathname.endsWith('/avatars'))return send(200,{items:[]});
    if(u.pathname==='/api/v1/conversations'){
      const matched=chats.filter(c=>(!u.searchParams.get('accountId')||c.accountId===u.searchParams.get('accountId'))&&c.archived===(u.searchParams.get('archived')==='true')&&(!u.searchParams.get('q')||c.title.toLowerCase().includes(u.searchParams.get('q').toLowerCase()))),limit=Number(u.searchParams.get('limit')||100);return send(200,{items:matched.slice(0,limit),nextCursor:matched.length>limit?matched[limit-1].id:undefined});
    }
    if(u.pathname==='/api/v1/contacts')return send(200,{items:[],sources:accounts.filter(a=>!u.searchParams.get('accountId')||a.id===u.searchParams.get('accountId')).map(a=>({accountId:a.id,state:'known'}))});
    if(u.pathname==='/api/v1/messages/search'){
      state.searchReads.push(Object.fromEntries(u.searchParams));
      const query=(u.searchParams.get('q')||'').trim(),accountId=u.searchParams.get('accountId')||'',conversationId=u.searchParams.get('conversationId')||'',includeEditHistory=u.searchParams.get('includeEditHistory')==='true',limit=Number(u.searchParams.get('limit')||50);
      const matched=messages.filter(m=>(!accountId||m.accountId===accountId)&&(!conversationId||m.conversationId===conversationId)&&m.originalText.toLowerCase().includes(query.toLowerCase())).sort((a,b)=>Date.parse(b.createdAt)-Date.parse(a.createdAt)||b.id.localeCompare(a.id)),cursor=u.searchParams.get('cursor'),start=cursor?matched.findIndex(m=>m.id===cursor)+1:0;
      if(cursor&&start===0)return send(400,{status:400,code:'invalid_cursor'});
      return send(200,{query,accountId:accountId||undefined,conversationId:conversationId||undefined,includeEditHistory,items:matched.slice(start,start+limit).map(m=>({message:m,accountLabel:accounts.find(a=>a.id===m.accountId).label,conversationTitle:chats.find(c=>c.id===m.conversationId).title,matchedRevisionId:m.revisionId,matchedAt:m.createdAt,historical:false,matchKind:'text',excerpt:m.originalText})),nextCursor:matched.length>start+limit?matched[start+limit-1].id:undefined,metadataIndexing:{state:'complete',indexedMessages:messages.length,totalMessages:messages.length}});
    }
    if(u.pathname==='/api/v1/shared-content'){
      state.reads.push(Object.fromEntries(u.searchParams));
      let items=catalog.filter(item=>u.searchParams.get('type')==='links'?item.kind==='link':u.searchParams.get('type')==='documents'?item.kind==='document':item.kind==='image');
      if(u.searchParams.get('accountId'))items=items.filter(i=>i.message.accountId===u.searchParams.get('accountId'));
      if(u.searchParams.get('conversationId'))items=items.filter(i=>i.message.conversationId===u.searchParams.get('conversationId'));
      const start=u.searchParams.get('cursor')?50:0;
      return send(200,{items:items.slice(start,start+50),nextCursor:items.length>start+50?'next-page':undefined,indexing:{state:'complete',indexedMessages:2,totalMessages:2}});
    }
    if(u.pathname==='/api/v1/starred-messages')return send(200,{items:[]});
    for(const c of chats){
      if(u.pathname===`/api/v1/conversations/${c.id}`)return send(200,c);
      if(u.pathname===`/api/v1/conversations/${c.id}/messages`){
        if(req.method==='POST'){
          const key=req.headers['idempotency-key'];let m=operations.get(key);
          if(!m){m={...messages[0],id:id(1000+state.effects),accountId:c.accountId,conversationId:c.id,originalText:body.originalText,isOwn:true,sendState:'queued'};operations.set(key,structuredClone(m));state.effects++;messages.push(m);emit();}
          return send(202,m);
        }
        const compare=(a,b)=>Date.parse(b.createdAt)-Date.parse(a.createdAt)||b.id.localeCompare(a.id),ordered=messages.filter(m=>m.conversationId===c.id).sort(compare),cursor=u.searchParams.get('cursor'),limit=Number(u.searchParams.get('limit')||50),direction=u.searchParams.get('direction')||'older',anchor=cursor?ordered.find(message=>message.id===cursor):null;
        if(cursor&&!anchor)return send(404,{status:404});
        const candidates=anchor?ordered.filter(message=>direction==='newer'?compare(message,anchor)<0:compare(message,anchor)>0):ordered;
        const selected=direction==='newer'?candidates.slice(-limit):candidates.slice(0,limit);
        return send(200,{items:selected,nextCursor:candidates.length>limit?selected[direction==='newer'?0:selected.length-1].id:undefined});
      }
      if(u.pathname===`/api/v1/conversations/${c.id}/message-stars`)return send(200,{items:u.searchParams.getAll('messageId').map(messageId=>({messageId,accountId:c.accountId,conversationId:c.id,starred:false,revisionId:null,updatedAt:null}))});
      if(u.pathname===`/api/v1/conversations/${c.id}/previews`)return send(200,{items:[]});
      const context=u.pathname.match(new RegExp('^/api/v1/conversations/'+c.id+'/messages/([^/]+)/context$'));
      if(context){const m=messages.find(m=>m.id===context[1]&&m.conversationId===c.id);return m?send(200,{items:[m],targetId:m.id,hasOlder:false,hasNewer:false}):send(404,{status:404});}
    }
    const retained=u.pathname.match(/^\/api\/v1\/messages\/([^/]+)$/);if(retained){const m=messages.find(message=>message.id===retained[1]);return m?send(200,m):send(404,{status:404});}
    if(u.pathname.startsWith('/api/v1/operations/')){const id=u.pathname.split('/').at(-1),m=operations.get(id);return m?send(200,{id,kind:'send_message',resourceId:m.id,status:'succeeded',createdAt:m.createdAt,...(u.searchParams.get('includeAcceptedMessage')==='true'?{acceptedMessage:m}:{})}):send(404,{status:404});}
    if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return;}
    send(404,{status:404});
  });
  api.listen(0,'127.0.0.1');await once(api,'listening');
  const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(resolve=>allocation.close(resolve));
  const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let log='';child.stderr.on('data',chunk=>log+=chunk);
  // Full Chromium's new headless mode includes the native background-tab lifecycle.
  // The lighter headless shell can load a tab without reporting Playwright's Page.
  const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true,channel:'chromium'}),findings=[];let debuggingPage;
  try {
    await until(async()=>{try{return (await fetch(base)).ok;}catch{return false;}},'isolated BFF ready '+log);
    // HTMX becomes ready before the final Alpine dependency. Navigation must
    // use fragments throughout that interval, then start one application scope.
    const early=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'}),earlyPage=await early.newPage(),earlyDocuments=[],earlyHX=[],earlyErrors=[];let alpineHeld=false,releaseAlpine;const alpineGate=new Promise(resolve=>releaseAlpine=resolve);
    debuggingPage=earlyPage;await early.addInitScript(()=>{window.documentToken=Math.random();window.earlySettles=0;document.addEventListener('htmx:after:settle',()=>window.earlySettles++)});earlyPage.on('pageerror',error=>earlyErrors.push(error.message));earlyPage.on('request',request=>{if(request.resourceType()==='document')earlyDocuments.push(request.url());if(request.headers()['hx-request']==='true')earlyHX.push(request.url())});await earlyPage.route('**/assets/js/runtime/alpinejs/*/alpine.min.js',async route=>{alpineHeld=true;await alpineGate;try{await route.continue()}catch{}});
    try{await earlyPage.goto(base+'/?section=chats&lang=en&theme=dark',{waitUntil:'commit'});await until(async()=>alpineHeld&&await earlyPage.evaluate(()=>!!window.htmx&&!window.OieRuntime?.generation),'HTMX ready before Alpine');const earlyToken=await earlyPage.evaluate(()=>{window.htmx.config.defaultSettleDelay=500;return window.documentToken});console.log('Early init diagnostic',JSON.stringify(await earlyPage.evaluate(()=>{const link=[...document.querySelectorAll('.oie-sidebar a')].find(link=>link.textContent.includes('Media & links'));return{ready:document.readyState,navigation:typeof window.OieNavigation,linkMatches:link.matches('.oie-navigation a[hx-boost=\"true\"]'),processed:link.getAttribute('data-htmx-powered'),process:typeof window.htmx.process}})));await earlyPage.locator('.oie-sidebar a').filter({hasText:'Media & links'}).click({noWaitAfter:true});await until(async()=>new URL(earlyPage.url()).searchParams.get('section')==='media'&&await earlyPage.locator('#shared-content-results').count()===1,'initial sidebar route before Alpine');console.log('Early sidebar evidence',JSON.stringify({documents:earlyDocuments.length,fragments:earlyHX.length,sameDocument:await earlyPage.evaluate(token=>window.documentToken===token,earlyToken)}));assert.equal(earlyDocuments.length,1,'sidebar reloaded after HTMX was ready but before Alpine');assert.equal(await earlyPage.evaluate(()=>window.documentToken),earlyToken);assert.equal(earlyHX.length,1);assert.equal(await earlyPage.evaluate(()=>window.OieRuntime.generation),0);assert.equal(streams.size,0);releaseAlpine();await until(async()=>await earlyPage.evaluate(()=>window.OieRuntime?.generation>0),'application starts after dependencies');await until(()=>streams.size===1,'one stream after delayed dependency');await until(async()=>await earlyPage.evaluate(()=>window.earlySettles)===1,'delayed fragment settles after dependency release');await wait(200);assert.equal(await earlyPage.evaluate(()=>window.OieRuntime.generation),1);assert.deepEqual(earlyErrors,[]);assert.equal(streams.size,1);findings.push('Sidebar navigation uses HTMX while Alpine is delayed and starts one application stream after dependency readiness')}finally{releaseAlpine();await early.close();await until(()=>streams.size===0,'early stream disposed')}
    const startup=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR'});
    const startupPage=await startup.newPage(),startupRequests=[];
    let releaseStartup, startupHeld=false;
    const startupGate=new Promise(resolve=>releaseStartup=resolve);
    await startupPage.addInitScript(()=>{window.documentToken=Math.random()});
    startupPage.on('request',request=>startupRequests.push([request.resourceType(),request.headers()['x-oie-fragment']]));
    await startupPage.route('**/assets/js/runtime/alpinejs/*/alpine.min.js',async route=>{
      startupHeld=true;await startupGate;await route.continue().catch(()=>{});
    });
    try {
      await startupPage.goto(base+'/?lang=pt-BR&theme=light',{waitUntil:'commit'});
      await until(()=>startupPage.evaluate(()=>!!window.htmx&&!window.OieRuntime?.generation).catch(()=>false).then(ready=>ready&&startupHeld),'chat list before Alpine');
      const token=await startupPage.evaluate(()=>window.documentToken);
      await startupPage.locator('#conversation-list a[data-conversation-id="'+chatB+'"]').click({noWaitAfter:true});
      await until(()=>startupPage.evaluate(chat=>document.querySelector('#conversation-pane')?.dataset.conversationId===chat,chatB),'chat fragment before Alpine');
      assert.equal(await startupPage.evaluate(()=>window.documentToken),token);
      assert.equal(startupRequests.filter(([type])=>type==='document').length,1);
      assert.equal(startupRequests.filter(([,kind])=>kind==='conversation').length,1);
      releaseStartup();
      await startupPage.waitForFunction(()=>window.OieRuntime.generation>0,{},{timeout:90000});
      await until(()=>streams.size===1,'one stream after starting in the selected chat');
    } finally {
      releaseStartup();await startup.close();await until(()=>streams.size===0,'startup stream disposed');
    }
    {
    const retainedCount=messages.length;
    for(let index=0;index<24;index++)messages.push({
      ...messages[0],id:id(200+index),createdAt:new Date(Date.parse('2026-11-08T12:00:00Z')+(index+1)*60000).toISOString(),
      originalText:'A retained message to make the history scroll · '+index
    });
    const scopedContext=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'});
    const scoped=await scopedContext.newPage(), documents=[], scopedErrors=[];
    debuggingPage=scoped;scoped.on('pageerror',error=>scopedErrors.push(error.message));
    let subscriptions=0;
    scoped.on('request',request=>{
      if(request.resourceType()==='eventsource')subscriptions++;
      if(request.resourceType()==='document')documents.push(request.url());
    });
    await scoped.goto(base+'/?chat='+chatA+'&lang=en&theme=dark',{waitUntil:'domcontentloaded'});
    await until(()=>streams.size===1,'initial scoped stream connected');
    const streamBefore=[...streams][0],subscriptionsBefore=subscriptions;
    await scoped.locator('#message-composer').fill('Draft kept through scoped swaps');
    const chatBefore=await scoped.evaluate(()=>{
      const selectors=['#oie-app','#message-composer','#message-timeline','#conversation-list'];
      window.chatNodes=selectors.map(selector=>document.querySelector(selector));
      window.chatNodes[2].scrollTop=150;
      return {generation:window.OieRuntime.generation,scroll:window.chatNodes[2].scrollTop};
    });
    assert(chatBefore.scroll>0,'fixture timeline overflows');
    const keepChat=async()=>{
      assert(await scoped.evaluate(({generation,scroll})=>window.OieRuntime.generation===generation&&
        window.chatNodes.every(node=>node.isConnected)&&window.chatNodes[2].scrollTop===scroll,chatBefore));
      assert.equal(await scoped.locator('#message-composer').inputValue(),'Draft kept through scoped swaps');
      assert.equal(subscriptions,subscriptionsBefore);
      assert.deepEqual([...streams],[streamBefore]);
      assert.equal(documents.length,1);
    };
    const accountMenu=scoped.locator('[data-account-switcher]:visible').first();
    await accountMenu.click();
    await scoped.locator('[data-open-accounts]:visible').click();
    await scoped.locator('[data-manage-account]:visible').first().click();
    await scoped.locator('#account-blocks:visible').waitFor();await keepChat();
    await scoped.keyboard.press('Escape');
    await scoped.locator('#accounts-body').waitFor({state:'hidden'});
    await scoped.locator('[data-open-details]').click();
    await scoped.locator('[data-info-entry=media]:visible').click();
    await scoped.locator('#chat-info-content[data-info-view=shared]').waitFor();
    await scoped.locator('#chat-info-content [data-shared-tab=links]').click();
    await scoped.locator('#chat-info-content [data-shared-tab=links][aria-current=true]').waitFor();
    await scoped.goBack();
    await scoped.locator('#chat-info-content [data-shared-tab=media][aria-current=true]').waitFor();
    await keepChat();
    await scoped.goForward();
    await scoped.locator('#chat-info-content [data-shared-tab=links][aria-current=true]').waitFor();
    await keepChat();
    await scoped.keyboard.press('Escape');
    await scoped.locator('#details-body').waitFor({state:'hidden'});
    await scoped.locator('[data-navigation-settings]:visible').click();
    await scoped.locator('#appearance-light').check();
    await scoped.waitForFunction(()=>document.documentElement.dataset.mode==='light');
    await keepChat();
    await scoped.keyboard.press('Escape');
    await scoped.locator('#settings-body').waitFor({state:'hidden'});
    assert.deepEqual(scopedErrors,[]);
    await scopedContext.close();await until(()=>streams.size===0,'scoped stream disposed');
    messages.splice(retainedCount);
    findings.push('Account details, details history and appearance preserve the mounted chat and original event connection');
    }
    const context=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'}),page=await context.newPage(),errors=[],documents=[],hx=[],fragmentChecks=[];
    debuggingPage=page;
    // These document identity and request probes belong only to the original page.
    await page.addInitScript(()=>{if(location.origin==='null')return;window.documentToken=Math.random();window.navTrace=[];for(const name of ['htmx:before:request','htmx:after:request','htmx:after:settle'])document.addEventListener(name,event=>{const ctx=event.detail.ctx;window.navTrace.push({event:name,action:ctx?.request?.action,target:ctx?.target?.id,location:location.href,generation:window.OieRuntime?.generation});});localStorage.setItem('oie.device.auto-image-downloads.v1','false');});
    page.on('pageerror',error=>errors.push(error.message));
    page.on('request',request=>{if(request.resourceType()==='document')documents.push(request.url());if(request.headers()['hx-request']==='true')hx.push(request.url());});page.on('response',response=>{if(response.request().headers()['x-oie-fragment']==='app'&&response.status()===200)fragmentChecks.push(response.text().then(body=>({doctype:/<!doctype|<html[ >]|<head[ >]/i.test(body),app:body.includes('id="oie-app"')})).catch(()=>null))});
    await page.goto(base+'/?chat='+chatA+'&lang=en&theme=dark',{waitUntil:'domcontentloaded'});
    await until(async()=>page.evaluate(()=>!!window.OieRuntime?.generation&&!!window.htmx),'runtime ready');
    const token=await page.evaluate(()=>window.documentToken);
    let settledGeneration=await page.evaluate(()=>window.OieRuntime.generation);
    const route=async check=>{await until(async()=>await page.evaluate(()=>window.OieRuntime.generation)>settledGeneration&&await check(),'settled fragment route');settledGeneration=await page.evaluate(()=>window.OieRuntime.generation);await until(()=>streams.size===1,'one live stream after route');assert.equal(await page.evaluate(()=>window.documentToken),token);assert.equal(documents.length,1);};
    const sharedTab=async(kind,count)=>{
      const previous=await page.evaluate(()=>{
        window.sharedTabBefore={app:document.getElementById('oie-app'),sidebar:document.querySelector('.oie-sidebar'),search:document.getElementById('shared-content-search'),scroll:document.getElementById('shared-content-scroll')};window.sharedTabSettles=0;
        const target=window.sharedTabBefore.scroll,settled=event=>{if(event.detail.task?.target===target){window.sharedTabSettles++;document.removeEventListener('htmx:after:settle',settled)}};document.addEventListener('htmx:after:settle',settled);
        const params=new URL(location.href).searchParams;return{generation:window.OieRuntime.generation,context:Object.fromEntries(['account','q','lang','theme'].map(key=>[key,params.get(key)||'']))};
      }),beforeRequests=hx.length;
      await page.locator('[data-shared-tab="'+kind+'"]').click();
      await until(async()=>await page.evaluate(({kind,count})=>window.sharedTabSettles===1&&new URL(location.href).searchParams.get('contentType')===kind&&document.querySelector('#shared-content-results')?.dataset.contentType===kind&&document.querySelectorAll('[data-shared-item]').length===count&&document.querySelector('[data-shared-tab="'+kind+'"]')?.getAttribute('aria-current')==='true'&&!document.querySelector('#shared-content-scroll')?.hasAttribute('aria-busy'),{kind,count}),'settled shared-content tab partial');
      const current=await page.evaluate(()=>{const params=new URL(location.href).searchParams;return{generation:window.OieRuntime.generation,context:Object.fromEntries(['account','q','lang','theme'].map(key=>[key,params.get(key)||''])),retained:window.sharedTabBefore.app===document.getElementById('oie-app')&&window.sharedTabBefore.sidebar===document.querySelector('.oie-sidebar')&&window.sharedTabBefore.search===document.getElementById('shared-content-search')&&window.sharedTabBefore.scroll===document.getElementById('shared-content-scroll'),type:document.querySelector('#shared-content-search input[name=contentType]')?.value}});
      assert.equal(current.generation,previous.generation,'shared tabs must retain the application runtime');assert.equal(current.generation,settledGeneration);assert.deepEqual(current.context,previous.context);assert.equal(current.retained,true);assert.equal(current.type,kind);assert.equal(hx.length,beforeRequests+1,'one HTMX request per tab change');await until(()=>streams.size===1,'one live stream after tab partial');assert.equal(await page.evaluate(()=>window.documentToken),token);assert.equal(documents.length,1);
    };
    const choose=async(account,label)=>{await page.locator('[data-account-switcher]:visible').first().click();await page.locator('[data-switch-account="'+account+'"]:visible').click();await route(async()=>(new URL(page.url()).searchParams.get('account')||'')===account&&await page.locator('[data-account-switcher]:visible').first().textContent().then(t=>t.includes(label)));};
    const sidebarDestinations=[['media','Media & links'],['archived','Archived'],['favorites','Favorites'],['chats','Chats']];
    for(const [section,label]of sidebarDestinations){await page.locator('.oie-sidebar a').filter({hasText:label}).click();await route(async()=>new URL(page.url()).searchParams.get('section')===section);assert.equal(await page.locator('.oie-sidebar a[aria-current=page]').count(),1);assert((await page.locator('.oie-sidebar a[aria-current=page]').textContent()).includes(label))}
    await page.goBack();await route(async()=>new URL(page.url()).searchParams.get('section')==='favorites');assert((await page.locator('.oie-sidebar a[aria-current=page]').textContent()).includes('Favorites'));await page.goForward();await route(async()=>new URL(page.url()).searchParams.get('section')==='chats');
    for(const gesture of [{modifiers:['Control']},{button:'middle'}]){const original=page.url(),requests=hx.length,popupEvent=context.waitForEvent('page');await page.locator('.oie-sidebar a').filter({hasText:'Media & links'}).click(gesture);const popup=await popupEvent;await popup.waitForURL(url=>url.searchParams.get('section')==='media',{waitUntil:'domcontentloaded'});assert.equal(new URL(popup.url()).searchParams.get('section'),'media');assert.equal(await popup.locator('#oie-app').count(),1);assert.equal(page.url(),original);assert.equal(hx.length,requests);assert.equal(await page.evaluate(()=>window.documentToken),token);await popup.close();await until(()=>streams.size===1,'modifier popup stream disposed')}
    await page.locator('a[data-conversation-id="'+chatA+'"]').click();await route(async()=>await page.locator('#message-composer').count()===1);findings.push('All four desktop sidebar destinations and Back/Forward use same-document fragments with the active link updated; Control and middle clicks retain native new-tab behavior');
    const draft='Unsaved local draft 👋';await page.locator('#message-composer').fill(draft);
    await choose(beta,'Work');assert.equal(await page.locator('#message-composer').inputValue(),'');
    await choose(alpha,'Personal');await until(async()=>await page.locator('#message-composer').inputValue()===draft,'account draft restored');
    await page.locator('.oie-sidebar a').filter({hasText:'Media & links'}).click();await route(async()=>await page.locator('#shared-content-results').count()===1);assert.equal(await page.locator('[data-shared-item]').count(),27);
    await choose('','All accounts');assert.equal(await page.locator('[data-shared-item]').count(),50);
    const beforePage=await page.evaluate(()=>window.OieRuntime.generation);
    await page.locator('[data-shared-next]').click();
    await until(async()=>await page.locator('[data-shared-item]').count()>=53||
      new URL(page.url()).searchParams.get('contentCursor')==='next-page'&&await page.locator('[data-shared-item]').count()===3,'catalog continuation');
    assert.equal(await page.evaluate(()=>window.OieRuntime.generation),beforePage);
    await sharedTab('links',2);assert.equal(new URL(page.url()).searchParams.get('contentCursor'),null);
    await sharedTab('documents',2);
    const sourceCard=page.locator('[data-shared-conversation="'+chatA+'"]').first();await sourceCard.locator('[data-shared-menu-trigger]').click();await sourceCard.getByRole('menuitem',{name:'Go to message',exact:true}).click();await route(async()=>await page.locator('[data-message-id="'+messages[0].id+'"]').count()===1&&new URL(page.url()).searchParams.get('message')===messages[0].id);await until(async()=>page.locator('[data-message-id="'+messages[0].id+'"]').evaluate(node=>document.activeElement===node),'source message focused');assert.equal(await page.locator('#message-composer').inputValue(),draft);
    await page.locator('.oie-sidebar a').filter({hasText:'Chats'}).click();await route(async()=>new URL(page.url()).searchParams.get('section')!=='media'&&!new URL(page.url()).searchParams.has('message'));
    const searchBefore=await page.evaluate(()=>{window.searchBefore={app:document.getElementById('oie-app'),input:document.getElementById('conversation-search'),list:document.getElementById('conversation-list'),pane:document.getElementById('conversation-pane'),composer:document.getElementById('message-composer')};return{generation:window.OieRuntime.generation,q:new URL(location.href).searchParams.get('q')}});
    const searchReady=async query=>until(async()=>await page.evaluate(query=>{const root=document.querySelector('[data-message-search-root]');return root?.dataset.searchState==='ready'&&root.dataset.searchQuery===query},query),'settled global query '+query);
    await page.locator('#conversation-search').fill('Family');await page.locator('#conversation-filter-form').evaluate(form=>form.requestSubmit());await searchReady('Family');
    assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'Family');assert.equal(new URL(page.url()).searchParams.get('q'),searchBefore.q);assert.equal(await page.locator('[data-message-search-conversation]').count(),1);assert(await page.locator('[data-message-search-conversations]').getByText('Family',{exact:true}).isVisible());
    await until(async()=>page.locator('#conversation-search').evaluate(node=>document.activeElement===node),'search focus preserved');assert.equal(await page.locator('#message-composer').inputValue(),draft);assert.equal(await page.evaluate(()=>window.OieRuntime.generation),searchBefore.generation);assert(await page.evaluate(()=>window.searchBefore.app===document.getElementById('oie-app')&&window.searchBefore.input===document.getElementById('conversation-search')&&window.searchBefore.list===document.getElementById('conversation-list')&&window.searchBefore.pane===document.getElementById('conversation-pane')&&window.searchBefore.composer===document.getElementById('message-composer')));assert.equal(await page.evaluate(()=>window.documentToken),token);assert.equal(documents.length,1);assert.equal(streams.size,1);
    await page.goBack();await route(async()=>new URL(page.url()).searchParams.get('searchMode')!=='messages'&&await page.locator('#conversation-list-default').isVisible());assert.equal(await page.locator('#conversation-search').inputValue(),searchBefore.q||'');assert((await page.locator('[data-message-search-category]').evaluateAll(nodes=>nodes.map(node=>new URL(node.href).searchParams.get('q')))).every(value=>value===(searchBefore.q||'')),'Back restores category queries with the original title filter');
    await page.goForward();await route(async()=>new URL(page.url()).searchParams.get('searchQuery')==='Family');await searchReady('Family');assert.equal(await page.locator('#conversation-search').inputValue(),'Family');assert((await page.locator('[data-message-search-category]').evaluateAll(nodes=>nodes.map(node=>new URL(node.href).searchParams.get('q')))).every(value=>value==='Family'),'Forward restores category queries with global search');assert.equal(await page.locator('#message-composer').inputValue(),draft);
    findings.push('Account switching, global navigation, media tabs/pagination, source jump, unified search results and Back/Forward keep one document, one stream, original inbox context, focus and account-scoped draft');
    // Hold an actual completed old search response until the newer media route
    // has settled. Releasing it must not restore its obsolete search context.
    let searchHeld=false,finishSearch;const searchReleased=new Promise(resolve=>finishSearch=resolve);
    await page.route('**/ui/message-search?**',async intercepted=>{if(new URL(intercepted.request().url()).searchParams.get('q')!=='slow')return intercepted.continue();const response=await intercepted.fetch();searchHeld=true;await new Promise(resolve=>releaseDelayedSearch=resolve);try{await intercepted.fulfill({response})}catch{}finishSearch();});
    await page.locator('#conversation-search').fill('slow');await page.locator('#conversation-filter-form').evaluate(form=>form.requestSubmit());await until(()=>searchHeld,'old search response held');
    await page.locator('.oie-sidebar a').filter({hasText:'Media & links'}).click();await route(async()=>await page.locator('#shared-content-results').count()===1);releaseDelayedSearch();await searchReleased;releaseDelayedSearch=null;await page.unroute('**/ui/message-search?**');await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));assert.equal(new URL(page.url()).searchParams.get('section'),'media');assert.equal(await page.locator('#shared-content-results').count(),1);
    findings.push('Delayed old search response cannot overwrite a newer media route');
    // Transport failure keeps the current view usable and does not reload it.
    await page.route('**/?*section=archived*',route=>route.abort('failed'));await page.locator('.oie-sidebar a').filter({hasText:'Archived'}).click();await page.locator('[data-toast-container]').getByText('Could not open this view. Try again.',{exact:true}).waitFor();assert.equal(await page.locator('#shared-content-results').count(),1);assert.equal(documents.length,1);await page.unroute('**/?*section=archived*');
    await page.locator('.oie-sidebar a').filter({hasText:'Chats'}).click();await route(async()=>await page.locator('#message-composer').count()===1);
    await page.locator('#message-composer').fill('One send after many route swaps');await page.locator('[data-send-message]').click();await until(()=>state.effects===1,'exactly one engine effect');await until(async()=>await page.locator('#message-composer').inputValue()==='','accepted send clears once');await wait(300);assert.equal(state.effects,1);
    // Leave after acceptance but before the browser receives it. The saved
    // operation key must reconcile on return instead of creating another send.
    let committedResolve,releaseReply,held=false;
    const committed=new Promise(resolve=>committedResolve=resolve),replyGate=new Promise(resolve=>releaseReply=resolve);
    await page.route('**/ui/messages/send',async intercepted=>{
      if(held)return intercepted.continue();held=true;
      const response=await intercepted.fetch();committedResolve();await replyGate;
      try{await intercepted.fulfill({response});}catch{} // Route disposal can abort this response.
    });
    const uncertainText='Accepted while changing views';await page.locator('#message-composer').fill(uncertainText);
    const uncertainKey=await page.locator('#send-message-form [name="operationKey"]').inputValue();
    await page.locator('[data-send-message]').click();await committed;assert.equal(state.effects,2);
    await page.locator('.oie-sidebar a').filter({hasText:'Media & links'}).click();await route(async()=>await page.locator('#shared-content-results').count()===1);
    const pending=await page.evaluate(()=>Object.keys(localStorage).filter(key=>key.startsWith('oie.draft.v1')).map(key=>JSON.parse(localStorage.getItem(key))).find(value=>value.pending));
    assert.equal(pending?.key,uncertainKey);assert.equal(pending?.text,uncertainText);
    releaseReply();await page.locator('.oie-sidebar a').filter({hasText:'Chats'}).click();await route(async()=>await page.locator('#message-composer').count()===1);
    await until(async()=>await page.locator('#message-composer').inputValue()==='','accepted operation reconciles after route return');
    assert.equal(state.effects,2);assert.equal(messages.filter(message=>message.originalText===uncertainText).length,1);await page.unroute('**/ui/messages/send');
    findings.push('Navigation after committed send and before its reply preserves original operation key and reconciles on return with one API acceptance');
    const duplicate=await page.evaluate(()=>{const ids=[...document.querySelectorAll('[id]')].map(node=>node.id);return ids.filter((id,index)=>ids.indexOf(id)!==index);});assert.deepEqual(duplicate,[]);assert.deepEqual(errors,[]);assert(hx.length>=10);assert.equal(documents.length,1);
    findings.push('Failed navigation remains recoverable; remounts do not duplicate mutation handlers, IDs or page errors');
    const completedFragments=(await Promise.all(fragmentChecks)).filter(Boolean);assert(completedFragments.length>=8);assert(completedFragments.every(fragment=>fragment.app&&!fragment.doctype));await context.close();
    const denied=await browser.newContext({viewport:{width:390,height:844},locale:'en'});await denied.addInitScript(()=>{if(location.origin==='null')return;window.documentToken=Math.random();window.navTrace=[];for(const name of ['htmx:before:request','htmx:after:request','htmx:before:swap','htmx:after:settle','htmx:error'])document.addEventListener(name,event=>{const ctx=event.detail.ctx;window.navTrace.push({event:name,action:ctx?.request?.action,target:ctx?.target?.id,kind:ctx?.oieKind,response:ctx?.response?.status,location:location.href,generation:window.OieRuntime?.generation,pane:document.querySelector('#conversation-pane')?.dataset.openChat,workspace:document.querySelector('#workspace')?.className});});for(const name of ['getItem','setItem','removeItem'])Object.defineProperty(Storage.prototype,name,{configurable:true,value(){throw new DOMException('Denied','SecurityError');}});});const mobile=await denied.newPage(),mobileDocuments=[],mobileHX=[],mobileErrors=[];debuggingPage=mobile;mobile.on('pageerror',error=>mobileErrors.push(error.message));mobile.on('request',request=>{if(request.resourceType()==='document')mobileDocuments.push(request.url());if(request.headers()['hx-request']==='true')mobileHX.push(request.url())});await mobile.goto(base+'/?chat='+chatA+'&lang=en&theme=light');await until(async()=>mobile.evaluate(()=>!!window.OieRuntime?.generation),'mobile runtime');await assertMobileControlOwnership(mobile);
    await mobile.locator('#message-composer').fill('Session fallback draft');await mobile.locator('.oie-back-button').click();await until(async()=>await mobile.locator('[data-account-switcher]:visible').count()>0,'mobile conversation list');await assertMobileControlOwnership(mobile);await mobile.locator('[data-account-switcher]:visible').first().click();await mobile.locator('[data-switch-account="'+beta+'"]:visible').click();await until(async()=>mobile.locator('[data-account-switcher]:visible').textContent().then(t=>t.includes('Work')),'mobile Work');await mobile.locator('[data-account-switcher]:visible').first().click();await mobile.locator('[data-switch-account="'+alpha+'"]:visible').click();await mobile.locator('a[data-conversation-id="'+chatA+'"]').click();await until(async()=>await mobile.locator('#message-composer').inputValue()==='Session fallback draft','denied-storage draft survives route');const mobileToken=await mobile.evaluate(()=>window.documentToken);let mobileGeneration=await mobile.evaluate(()=>window.OieRuntime.generation);await mobile.locator('.oie-back-button').click();
    const mobileRoute=async section=>{await until(async()=>new URL(mobile.url()).searchParams.get('section')===section&&await mobile.evaluate(()=>window.OieRuntime.generation)>mobileGeneration,'mobile sidebar '+section);await assertMobileControlOwnership(mobile);mobileGeneration=await mobile.evaluate(()=>window.OieRuntime.generation);assert.equal(await mobile.evaluate(()=>window.documentToken),mobileToken);assert.equal(mobileDocuments.length,1);await until(()=>streams.size===1,'one mobile stream')};
    for(const [section,label]of sidebarDestinations){await mobile.locator('[aria-controls=mobile-navigation-panel]:visible').click();await mobile.locator('#mobile-navigation-panel').waitFor({state:'visible'});await mobile.locator('#mobile-navigation-panel a').filter({hasText:label}).click();await mobileRoute(section);await mobile.locator('#mobile-navigation-panel').waitFor({state:'hidden'});assert((await mobile.locator('#mobile-navigation-panel a[aria-current=page]').textContent()).includes(label))}
    await mobile.goBack();await mobileRoute('favorites');await mobile.goForward();await mobileRoute('chats');assert(mobileHX.length>=6);assert.deepEqual(mobileErrors,[]);findings.push('Mobile overlay routes all four destinations, closes after each fragment, updates active state and preserves the document through Back/Forward');assert(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await mobile.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await mobile.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>v.id),[]);await denied.close();findings.push('Mobile light layout preserves denied-storage session draft and has no horizontal overflow or WCAG A/AA violations');
    const native=await browser.newContext({javaScriptEnabled:false,viewport:{width:1440,height:1000}}),fallback=await native.newPage();await fallback.goto(base+'/?chat='+chatA+'&lang=en');for(const [section,label]of sidebarDestinations){await fallback.locator('.oie-sidebar a').filter({hasText:label}).click();assert.equal(new URL(fallback.url()).searchParams.get('section'),section);assert.equal(await fallback.locator('#oie-app').count(),1);assert((await fallback.locator('.oie-sidebar a[aria-current=page]').textContent()).includes(label));if(section==='media')assert.equal(await fallback.locator('[data-shared-item]').count(),50)}await native.close();findings.push('Direct native links remain usable when JavaScript is disabled');
    const artifact=path.join(__dirname,'artifacts');await fs.mkdir(artifact,{recursive:true});await fs.writeFile(path.join(artifact,'fragment-navigation-report.json'),JSON.stringify({findings,fragmentRequests:hx.length,documentRequests:documents.length,completedFragmentShapes:completedFragments.length,mobileDocumentRequests:mobileDocuments.length,mobileFragmentRequests:mobileHX.length,earlyDocumentRequests:earlyDocuments.length,intents:2,mutations:state.effects,pageErrors:errors},null,2));console.log('PASS: HTMX fragment navigation, scoped drafts, Back/Forward, stale-response/failure recovery, one stream and one effect per intent, mobile accessibility and native fallback');
  } catch(error) {
    if(debuggingPage&&!debuggingPage.isClosed())console.error('Fragment failure evidence',JSON.stringify(await debuggingPage.evaluate(()=>({url:location.href,ready:document.readyState,generation:window.OieRuntime?.generation,account:document.body.dataset.accountId,chat:document.body.dataset.conversationId,status:document.querySelector('[data-navigation-status]')?.textContent,labels:[...document.querySelectorAll('[data-account-switcher]')].map(e=>e.textContent),boosted:document.querySelectorAll('[hx-boost]').length,form:document.querySelector('form.oie-search')?.outerHTML,trace:window.navTrace?.slice(-30),workspace:document.querySelector('#workspace')?.className,paneOpen:document.querySelector('#conversation-pane')?.dataset.openChat,navigationControls:[...document.querySelectorAll('button[aria-label="Open navigation"]')].map(node=>({controls:node.getAttribute('aria-controls'),expanded:node.getAttribute('aria-expanded')}))})).catch(()=>({unavailable:true}))), 'streams',streams.size, 'BFF log',log);
    throw error;
  } finally {releaseDelayedSearch?.();await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve));}
})().catch(error=>{console.error(error);process.exitCode=1;});
