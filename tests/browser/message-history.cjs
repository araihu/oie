// Isolated observed-content history fixture. No real account/provider traffic.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),direct=id(3),room=id(4),revision=id(5),streams=new Set();
 const state={generation:0,transport:0,fail:false,foreign:false,delay:0,writes:[],historyReads:[],otherReads:[]};
 const owner={id:account,revisionId:revision,protocol:'xmpp',label:'Fixture account',address:'alice@fixture',createdAt:'2026-11-09T12:00:00Z'};
 const chats=[{id:direct,kind:'direct',address:'bob@fixture',title:'Bob'},{id:room,kind:'room',address:'room@fixture',title:'Saved room'}].map(c=>({...c,revisionId:revision,accountId:account,lastMessage:'Retained text',updatedAt:'2026-11-09T12:19:00Z',unreadCount:0,archived:false,favorite:false}));
 const baseNumber=c=>c.id===room?200:100,targetID=c=>id(baseNumber(c)+18),revisionID=(c,n)=>id((c.id===room?4000:3000)+n);
 const queuedRoomMessage=id(baseNumber(chats[1])+16);
 const starredRoomMessages=new Set([queuedRoomMessage,targetID(chats[1])]);
 const currentNumber=()=>state.generation?26+state.generation:25;
 const sourceText=(c,n)=>n===0?'Original **literal** <script>alert(1)</script>\nCafé 🧑🏽‍💻':n===25?'Current **saved** message\nSecond line':n===26?'Alternative authored later than the selected current head':n===27?'Newest verified correction':n===28?'Newest verified correction after live refresh':n===22||n===23?'Repeated authored text':'Observed content version '+n;
 const messages=c=>Array.from({length:20},(_,n)=>{
  const messageId=id(baseNumber(c)+n),queued=messageId===queuedRoomMessage;
  return {id:messageId,revisionId:n===18?revisionID(c,currentNumber()+state.transport*10000):n===17?revisionID(c,currentNumber()+100):revision,accountId:account,conversationId:c.id,sender:queued?{id:account,displayName:'Alice'}:{id:id(8),displayName:'Bob'},originalText:n===18?sourceText(c,currentNumber()):n===17?'Other logical source version '+currentNumber():'Retained message '+n,createdAt:new Date(Date.UTC(2026,10,9,12,n)).toISOString(),isOwn:queued,historical:true,sendState:queued?'queued':'received',edited:n===18||n===17,editedAt:n===18||n===17?new Date(Date.UTC(2026,10,9,13,currentNumber())).toISOString():undefined};
 });
 const versions=(c,canonical=targetID(c))=>Array.from({length:27+state.generation},(_,n)=>({revisionId:revisionID(c,n+(canonical===targetID(c)?0:100)),sourceMessageId:n===0?canonical:id((c.id===room?6000:5000)+n+(canonical===targetID(c)?0:100)),createdAt:new Date(Date.UTC(2026,10,9,13,n)).toISOString(),originalText:canonical===targetID(c)?sourceText(c,n):'Other logical source version '+n,sender:{id:id(8),displayName:'Bob'},isCurrent:n===currentNumber(),isOriginal:n===0,parentRevisionIds:n===0?[]:[revisionID(c,(n===26?24:n-1)+(canonical===targetID(c)?0:100))]})).reverse();
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,value)=>{res.statusCode=code;res.end(JSON.stringify(value))};
  if(req.method!=='GET'){state.writes.push({method:req.method,path:u.pathname});return send(405,{status:405,code:'invalid_request'})}
  const history=u.pathname.match(/^\/api\/v1\/messages\/([^/]+)\/edit-history$/);
  if(history){
   state.historyReads.push({message:history[1],cursor:u.searchParams.get('cursor')||''});
   const canonical=history[1]===id(900)?targetID(chats[0]):history[1],c=chats.find(chat=>targetID(chat)===canonical||id(baseNumber(chat)+17)===canonical);
   if(!c)return send(404,{status:404,code:'not_found'});
   if(state.fail)return send(503,{status:503,code:'unavailable'});
   const snapshot=(state.generation>1?'e':state.generation?'c':canonical!==targetID(c)?'d':c.id===room?'b':'a').repeat(64),limit=Number(u.searchParams.get('limit')||20),cursor=u.searchParams.get('cursor')||'';
   let start=0;if(cursor){const match=cursor.match(/^history:([a-f0-9-]+):(\d+):([a-f0-9]+)$/);if(!match||match[1]!==c.id)return send(400,{status:400,code:'invalid_request'});if(match[3]!==snapshot)return send(409,{status:409,code:'history_changed'});start=Number(match[2]);}
   const all=versions(c,canonical),items=all.slice(start,start+limit),body={requestedMessageId:history[1],messageId:canonical,accountId:state.foreign?id(999):account,conversationId:c.id,currentRevisionId:revisionID(c,currentNumber()+(canonical===targetID(c)?0:100)),snapshot,items,nextCursor:start+limit<all.length?'history:'+c.id+':'+(start+limit)+':'+snapshot:undefined};
   const delay=state.delay;return delay?setTimeout(()=>send(200,body),delay):send(200,body);
  }
  state.otherReads.push(u.pathname);
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.24.0',eventCursor:engine+':0'});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[owner]});if(u.pathname===`/api/v1/accounts/${account}`)return send(200,owner);
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:40,conversations:2,updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews'))return send(200,{items:[]});
  if(u.pathname.endsWith('/capabilities'))return send(200,{accountId:account,state:'known',directText:true,features:['room_messaging','send_replies'].map(id=>({id,appSupport:'supported',protocolSupport:'supported',serverSupport:'scope_dependent',scope:'room'})),discovery:{state:'complete',services:[]}});
  if(u.pathname.endsWith('/room-session'))return send(200,{conversationId:room,accountId:account,state:'joined',active:true,updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/message-stars'))return send(200,{items:u.searchParams.getAll('messageId').map(messageId=>{
   const starred=starredRoomMessages.has(messageId);
   return {messageId,accountId:account,conversationId:chats.find(c=>u.pathname.includes(c.id))?.id||direct,starred,revisionId:starred?id(6):null,updatedAt:starred?owner.createdAt:null};
  })});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:chats});
  for(const c of chats){if(u.pathname===`/api/v1/conversations/${c.id}`)return send(200,c);if(u.pathname===`/api/v1/conversations/${c.id}/messages`)return send(200,{items:messages(c).reverse()});}
  const retained=u.pathname.match(/^\/api\/v1\/messages\/([^/]+)$/);if(retained){const m=chats.flatMap(messages).find(m=>m.id===(retained[1]===id(900)?targetID(chats[0]):retained[1]));return m?send(200,m):send(404,{status:404,code:'not_found'})}
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  return send(404,{status:404,code:'not_found'});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(resolve=>allocation.close(resolve));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let stderr='',childError='';child.stderr.on('data',c=>stderr+=c);child.on('error',e=>childError=e.message);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),findings=[],errors=[],external=[];
 const until=async(fn,label)=>{for(let n=0;n<200;n++){if(childError)throw Error(childError);if(await fn())return;await new Promise(resolve=>setTimeout(resolve,50))}throw Error('Timed out: '+label+' '+stderr.slice(-1500))};
 const route=(c,lang='en',theme='dark')=>base+'/?'+new URLSearchParams({account,chat:c.id,lang,theme});
 const observeHistoryTooltip=async(p,action)=>{
  await action.focus();const tooltipID=await action.evaluate(el=>el.closest('[data-tooltip-portal-ready]').querySelector('[role="tooltip"]').id);
  await p.waitForFunction(id=>document.getElementById(id).matches(':popover-open'),tooltipID);
  await p.evaluate(id=>{window.historyTooltipAtOpen=null;window.addEventListener('drawer:open',event=>{if(event.detail?.id==='message-history')window.historyTooltipAtOpen=document.getElementById(id).matches(':popover-open')},{once:true})},tooltipID);
  return p.locator('[id="'+tooltipID+'"]');
 };
 const assertHistoryTooltipDismissed=async(p,tooltip)=>{
  assert.equal(await p.evaluate(()=>window.historyTooltipAtOpen),false,'tooltip closes before opening history');
  assert.equal(await tooltip.evaluate(el=>el.matches(':popover-open')),false,'tooltip stays closed above history');
 };
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  const context=await browser.newContext({viewport:{width:1440,height:960},locale:'en',timezoneId:'America/Sao_Paulo'});
  await context.route('**/*',r=>{if(new URL(r.request().url()).origin!==base){external.push(r.request().url());return r.abort()}return r.continue()});
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));let documents=0;page.on('request',r=>{if(r.isNavigationRequest()&&r.frame()===page.mainFrame())documents++});
  await page.goto(route(chats[0]));await page.locator('[data-message-edited][data-open-message-history]').first().waitFor();await until(()=>streams.size>0,'SSE ready');await page.waitForTimeout(250);
  const target=page.locator('[data-message-id="'+targetID(chats[0])+'"]'),edited=target.locator('[data-message-edited][data-open-message-history]'),slot=page.locator('#message-history-content'),drawer=page.getByRole('dialog',{name:'Edit history',exact:true});
  assert.equal(await edited.locator('use').getAttribute('href'),'/assets/icons/heroicons.svg#hi-16-solid-pencil-square');
  await page.locator('#message-composer').fill('Keep this draft 😀');await edited.scrollIntoViewIfNeeded();await edited.focus();
  await page.evaluate(()=>{window.historyTimeline=document.querySelector('#message-timeline');window.historyTimelineRow=document.querySelector('[data-message-id]');window.historyScroll=historyTimeline.scrollTop;window.historyDocument=crypto.randomUUID();});const token=await page.evaluate(()=>historyDocument),beforeDocuments=documents,beforeHistoryReads=state.otherReads.length;
  const editedTooltip=await observeHistoryTooltip(page,edited);await page.keyboard.press('Enter');await drawer.waitFor({state:'visible'});await slot.locator('[data-message-history-page]').waitFor();await assertHistoryTooltipDismissed(page,editedTooltip);
  const rows=()=>slot.locator('[data-history-revision]');
  // Exercise a real BFF snapshot. Mutating only the mounted source leaves the
  // mock history/version stale, so a later recovery poll correctly invalidates it.
  const refreshTimeline=async()=>{const response=page.waitForResponse(r=>new URL(r.url()).pathname==='/ui/refresh'&&r.status()===200);await page.evaluate(()=>document.dispatchEvent(new CustomEvent('oie:refresh-request')));await response;await until(async()=>await target.getAttribute('data-message-source')===JSON.stringify(sourceText(chats[0],currentNumber()))&&await target.getAttribute('data-message-revision')===revisionID(chats[0],currentNumber()+state.transport*10000),'coherent live timeline source');await page.evaluate(()=>new Promise(requestAnimationFrame));};
  const settleDrawers=(activePage=page)=>activePage.evaluate(async()=>{await new Promise(requestAnimationFrame);await new Promise(requestAnimationFrame);await Promise.all(document.getAnimations().filter(a=>a.effect&&Number.isFinite(a.effect.getTiming().iterations)&&a.effect.getComputedTiming().endTime<1000).map(a=>a.finished.catch(()=>{})))});
  assert.deepEqual(state.otherReads.slice(beforeHistoryReads).filter(path=>path.includes('sender-profile')||path.includes('/previews')||path.includes('/avatars')||path.includes('/shared-content')||path.endsWith('/details')),[]);
  assert.equal(await rows().count(),20);assert.equal(await rows().filter({hasText:'Repeated authored text'}).count(),2);assert.equal(await rows().first().getAttribute('data-history-is-current'),'false');assert.equal(await rows().nth(1).getAttribute('data-history-is-current'),'true');assert.equal(await slot.locator('[data-history-is-current="true"]').count(),1);assert.equal(await slot.locator('[data-history-is-original="true"]').count(),0);
  assert(await slot.getByText('Current version',{exact:true}).isVisible());assert(await slot.getByText('Previous version',{exact:true}).count()>0);assert.equal(await slot.locator('script').count(),0);assert.equal(documents,beforeDocuments);assert.equal(await page.evaluate(()=>historyDocument),token);await page.waitForTimeout(250);assert.match(await slot.locator('[data-history-date]').first().textContent(),/2026/);assert.match(await slot.locator('[data-history-date]').first().textContent(),/\d{1,2}:\d{2}:\d{2}/);
  assert(await page.evaluate(()=>document.querySelector('#message-timeline')===historyTimeline&&document.querySelector('[data-message-id]')===historyTimelineRow));assert.equal(await page.locator('#message-composer').inputValue(),'Keep this draft 😀');assert(await page.evaluate(()=>Math.abs(historyTimeline.scrollTop-historyScroll)<2));
  const snapshot=await slot.locator('[data-message-history-page]').getAttribute('data-history-snapshot');
  await slot.locator('a[data-history-next]').click();await until(async()=>await rows().count()===7,'older bounded history page');assert.equal(await slot.locator('[data-history-is-current="true"]').count(),0);assert.equal(await slot.locator('[data-history-is-original="true"]').count(),1);assert(await slot.getByText('Original version',{exact:true}).isVisible());
  const original=slot.locator('[data-history-is-original="true"]');assert.equal(await original.locator('.oie-original-message').textContent(),sourceText(chats[0],0));assert.equal(await original.locator('script').count(),0);assert.equal(await slot.locator('[data-message-history-page]').getAttribute('data-history-snapshot'),snapshot);
  await slot.locator('[data-history-newer]').click();await until(async()=>await rows().count()===20,'newer bounded history page');assert.equal(await slot.locator('[data-history-newer]').isVisible(),false);
  findings.push('Edited link opens the scoped history drawer without a document reload or draft/scroll/timeline replacement; selected current is identified by its flag instead of timestamp order; older/newer replace at most 20 versions and preserve exact literal source safely');
  // A newly observed correction invalidates the old cursor and must clear its page.
  state.generation=1;await slot.locator('a[data-history-next]').click();await slot.locator('[data-history-restart]').waitFor({state:'visible'});assert.equal(await rows().count(),0);await refreshTimeline();await slot.locator('[data-history-restart]').click();await until(async()=>await rows().count()===20,'restart changed history');assert.notEqual(await slot.locator('[data-message-history-page]').getAttribute('data-history-snapshot'),snapshot);assert.equal(await rows().first().getAttribute('data-history-is-current'),'true');
  await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();await until(()=>edited.evaluate(el=>document.activeElement===el),'focus restored to Edited link');
  findings.push('Changed history returns 409, clears stale versions, restarts at the new snapshot and returns keyboard focus when closed');
  await edited.click();await until(async()=>await rows().count()===20,'reopen observed history');
  state.transport=1;await refreshTimeline();assert.equal(await rows().count(),20);
  state.generation=2;state.transport=0;await refreshTimeline();await slot.locator('[data-history-restart]').waitFor({state:'visible'});assert.equal(await rows().count(),0);await slot.locator('[data-history-restart]').click();await until(async()=>await rows().count()===20,'restart after content refresh');await refreshTimeline();assert.equal(await rows().count(),20);await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();
  findings.push('A transport-only row revision keeps the observed page, while a retained-source refresh clears it and offers a fresh scoped read');
  state.fail=true;await edited.click();await drawer.waitFor({state:'visible'});await settleDrawers();await slot.locator('[data-history-retry]').waitFor({state:'visible'});assert.equal(await rows().count(),0);state.fail=false;await slot.locator('[data-history-retry]').click();await until(async()=>await rows().count()===20,'retry saved history');await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();
  state.foreign=true;await edited.click();await slot.locator('[data-history-retry]').waitFor({state:'visible'});assert.equal(await rows().count(),0);state.foreign=false;await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();
  findings.push('Transient failures have scoped read-only retry, and a foreign account response never exposes versions');
  // The context-menu action opens the same read-only path, including on touch.
  await target.locator('[data-message-menu-trigger]').click();await page.locator('[id="history-message-'+targetID(chats[0])+'"]:visible').click();await drawer.waitFor({state:'visible'});await until(async()=>await rows().count()===20,'context menu history');await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();
  state.delay=650;await edited.click();await drawer.waitFor({state:'visible'});await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();const closed=await slot.innerHTML();await page.waitForTimeout(800);assert.equal(await slot.innerHTML(),closed);
  state.delay=650;await edited.click();await drawer.waitFor({state:'visible'});await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();state.delay=0;const other=page.locator('[data-message-id="'+id(baseNumber(chats[0])+17)+'"] [data-message-edited][data-open-message-history]');await other.click();await until(async()=>await slot.locator('[data-message-history-page]').getAttribute('data-history-message')===id(baseNumber(chats[0])+17),'different source history');await page.waitForTimeout(800);assert.equal(await slot.locator('[data-message-history-page]').getAttribute('data-history-message'),id(baseNumber(chats[0])+17));await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await settleDrawers();
  state.delay=650;await edited.click();await drawer.waitFor({state:'visible'});await page.evaluate(()=>window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id:'message-history'}})));await drawer.waitFor({state:'hidden'});await settleDrawers();await page.locator('#conversation-list a[data-conversation-id="'+room+'"]').click();await until(async()=>await page.locator('#conversation-pane').getAttribute('data-conversation-id')===room,'room fragment navigation');state.delay=0;await page.waitForTimeout(800);assert.equal(await page.locator('#message-history-content [data-message-history-page]').count(),0);assert.equal(await page.locator('#message-history-content').isVisible(),false);
  findings.push('Close, another source and conversation fragment replacement reject delayed history responses instead of reviving stale content');
  const native=await context.request.get(base+'/ui/message-history?'+new URLSearchParams({engineId:engine,accountId:account,conversationId:direct,messageId:id(900),lang:'en'}));assert(native.ok());const nativeHTML=await native.text();assert(nativeHTML.includes('data-history-requested="'+id(900)+'"'));assert(nativeHTML.includes('data-history-message="'+targetID(chats[0])+'"'));assert(nativeHTML.includes('Newest verified correction'));assert(nativeHTML.startsWith('<!doctype html>')||nativeHTML.startsWith('<!DOCTYPE html>'));
  const nativeContext=await browser.newContext({viewport:{width:900,height:800},locale:'en'});await nativeContext.route('**/*',r=>{if(new URL(r.request().url()).origin!==base){external.push(r.request().url());return r.abort()}return r.continue()});await nativeContext.addInitScript(()=>localStorage.setItem('oie.device.render-markdown.v1','false'));const nativePage=await nativeContext.newPage();nativePage.on('pageerror',e=>errors.push(e.message));const readsBeforeNative=state.otherReads.length;await nativePage.goto(native.url());await until(()=>nativePage.locator('html').getAttribute('data-message-markdown').then(value=>value==='false'),'native Markdown preference');assert.equal(await nativePage.locator('[data-message-history-native]').count(),1);assert.equal(await nativePage.locator('.oie-original-message:visible').count(),20);assert.equal(await nativePage.locator('.oie-rendered-message:visible').count(),0);assert.equal(await nativePage.locator('[data-history-is-current="true"] .oie-original-message').textContent(),sourceText(chats[0],currentNumber()));assert.equal(await nativePage.evaluate(()=>Boolean(window.OieRuntime)),false);assert(!state.otherReads.slice(readsBeforeNative).includes('/api/v1/events'));await nativeContext.close();
  const unedited=page.locator('[data-message-id="'+id(baseNumber(chats[1])+19)+'"]');assert.equal(await unedited.locator('[data-open-message-history]').count(),0);
  assert.deepEqual(await unedited.locator('.oie-message-stamp > :visible').evaluateAll(items=>items.map(item=>item.tagName)),['TIME'],'an unedited room message with no mention has only a timestamp');
  const assertStampAlignment=async(p,messageId,firstSymbol)=>{
   const stamp=p.locator('[data-message-id="'+messageId+'"] .oie-message-stamp');
   const {timeCenter,icons}=await stamp.evaluate(node=>{
    const time=node.querySelector('time').getBoundingClientRect();
    return {timeCenter:time.y+time.height/2,icons:[...node.querySelectorAll('svg')].filter(icon=>icon.getClientRects().length).map(icon=>{
     const rect=icon.getBoundingClientRect();
     return {symbol:icon.querySelector('use').getAttribute('href').split('#')[1],width:rect.width,height:rect.height,center:rect.y+rect.height/2};
    })};
   });
   assert.deepEqual(icons.map(icon=>icon.symbol),[firstSymbol,'hi-16-solid-star']);
   assert(icons.every(icon=>icon.width===12&&icon.height===12&&Math.abs(icon.center-timeCenter)<=0.1),JSON.stringify({timeCenter,icons}));
  };
  const artifacts=path.join(__dirname,'artifacts');
  for(const width of [390,1440])for(const locale of ['en','pt-BR'])for(const theme of ['light','dark']){
   const p=await context.newPage();p.on('pageerror',e=>errors.push(e.message));await p.setViewportSize({width,height:960});await p.goto(route(chats[1],locale,theme));const historyAction=p.locator('[data-message-id="'+targetID(chats[1])+'"] [data-message-edited][data-open-message-history]');
   const inset=await historyAction.evaluate(link=>link.getBoundingClientRect().left-link.closest('.oie-message-stamp').getBoundingClientRect().left);
   assert(Math.abs(inset)<=1,'the edit icon starts at the footer edge when the room message has no mention');
   await assertStampAlignment(p,targetID(chats[1]),'hi-16-solid-pencil-square');
   await assertStampAlignment(p,queuedRoomMessage,'hi-16-solid-clock');
   const historyTooltip=await observeHistoryTooltip(p,historyAction);await historyAction.click();const title=locale==='en'?'Edit history':'Histórico de edições';const d=p.getByRole('dialog',{name:title,exact:true});await d.waitFor({state:'visible'});await p.locator('[data-message-history-page]').waitFor();await settleDrawers(p);await assertHistoryTooltipDismissed(p,historyTooltip);assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));const box=await d.boundingBox();assert(box.x>=-1&&box.x+box.width<=width+1,JSON.stringify({width,locale,theme,box}));await p.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`${width} ${locale} ${theme}`);await fs.mkdir(artifacts,{recursive:true});if(theme==='dark')await p.screenshot({path:path.join(artifacts,`message-history-${width}-${locale}.png`)});await p.close();findings.push({width,locale,theme,axe:0});
  }
  assert.deepEqual(state.writes,[]);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);findings.push('Native aliased read resolves canonical history, unedited messages have no history action and all 8 responsive localized theme variants pass accessibility with zero writes or external requests');
  console.log('PASS: scoped observed edit history, bounded older/newer pages, current/original labels, literal sources, stale/retry/route guards, keyboard/draft/scroll preservation, native alias and localized accessible layouts');
  await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});await fs.writeFile(path.join(__dirname,'artifacts/message-history-report.json'),JSON.stringify({findings,mutationEffects:state.writes,pageErrors:errors,externalRequests:external},null,2));
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
