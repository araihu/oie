// Isolated read-only engine fixture. Does not access real accounts or providers.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{createHash}=require('node:crypto'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),direct=id(3),room=id(4),revision=id(5),streams=new Set();
 const photo=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=','base64'),hash=createHash('sha256').update(photo).digest('hex');
 const state={fail:false,delay:0,writes:[],reads:[],seq:0,roomProfile:"not_disclosed",foreignProfile:false,oldProfileEngine:false};
 const owner={id:account,revisionId:revision,protocol:'xmpp',label:'Fixture account',address:'alice@fixture',createdAt:'2026-11-08T12:00:00Z'};
 const chats=[{id:direct,kind:'direct',address:'bob@fixture',title:'Bob'},{id:room,kind:'room',address:'room@fixture',title:'Saved room'}].map(c=>({...c,revisionId:revision,accountId:account,lastMessage:'Retained text',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false}));
 const messages=c=>Array.from({length:24},(_,n)=>({id:id((c.kind==='room'?200:100)+n),revisionId:revision,accountId:account,conversationId:c.id,sender:{id:id(20+n),displayName:c.kind==='room'?'bob@fixture':'Bob'},originalText:'Retained message '+n,createdAt:new Date(Date.UTC(2026,10,8,12,n)).toISOString(),isOwn:false,historical:true,sendState:'received'}));
 const details=c=>({conversationId:c.id,accountId:account,address:c.address,kind:c.kind,accountConnection:{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'},state:'known',name:c.title,participants:{scope:c.kind==='room'?'occupants':'peer',state:'partial',truncated:false,items:c.kind==='room'?[{id:'room@fixture/bob@fixture',displayName:'bob@fixture',source:'presence',presence:{state:'available',statusText:'A current occupant with the same display name'}}]:[]},peerPresence:c.kind==='direct'?{state:'away',statusText:'At lunch <script>plain</script>',observedAt:'2026-11-08T12:00:00Z'}:undefined});
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,body)=>{res.statusCode=code;res.end(JSON.stringify(body))};
  if(req.method!=='GET'){state.writes.push({method:req.method,path:u.pathname});return send(405,{status:405})}
  state.reads.push(u.pathname);
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.22.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[owner]});
  if(u.pathname===`/api/v1/accounts/${account}`)return send(200,owner);
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/capabilities'))return send(200,{accountId:account,state:'available',directText:true,features:[{id:'room_messaging',appSupport:'supported',protocolSupport:'supported',serverSupport:'scope_dependent',scope:'room'}],discovery:{state:'complete',services:[]}});
  if(u.pathname.endsWith('/room-session'))return send(200,{conversationId:room,accountId:account,state:'joined',active:true,updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:48,conversations:2,updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/avatars'))return send(200,{items:['bob@fixture','actual-bob@fixture'].map((address,n)=>({id:id(70+n),revisionId:revision,accountId:account,address,imageHash:hash,updatedAt:'2026-11-08T12:00:00Z'}))});
  if(u.pathname===`/api/v1/avatars/${hash}`){res.setHeader('Content-Type','image/png');return res.end(photo)}
  if(u.pathname.endsWith('/previews')||u.pathname.endsWith('/message-stars'))return send(200,{items:[]});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:chats});
  for(const c of chats){
   if(u.pathname===`/api/v1/conversations/${c.id}`)return send(200,c);
   if(u.pathname===`/api/v1/conversations/${c.id}/details`)return send(200,details(c));
   if(u.pathname===`/api/v1/conversations/${c.id}/messages`)return send(200,{items:messages(c).reverse()});
  }
  if(u.pathname.endsWith('/sender-profile')){
   if(state.oldProfileEngine)return send(404,{status:404});
   const message=chats.flatMap(messages).find(m=>m.id===u.pathname.split('/').at(-2));if(!message)return send(404,{status:404});
   const isRoom=message.conversationId===room,known=!isRoom||state.roomProfile==='known';
   return send(200,{accountId:state.foreignProfile?id(999):account,conversationId:message.conversationId,messageId:message.id,state:known?'known':'not_disclosed',...(known?{fullName:'Robert <Published> Name',personalAddress:isRoom?'actual-bob@fixture':'bob@fixture'}:{})});
  }
  if(u.pathname.startsWith('/api/v1/messages/')){
   const message=chats.flatMap(messages).find(m=>m.id===u.pathname.split('/').at(-1));
   if(state.fail)return send(503,{status:503});
   const delay=state.delay;if(delay)return setTimeout(()=>send(message?200:404,message||{status:404}),delay);
   return send(message?200:404,message||{status:404});
  }
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  return send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let stderr='',childError='';child.stderr.on('data',chunk=>stderr+=chunk);child.on('error',e=>childError=e.message);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),findings=[],errors=[],external=[];
 const until=async(fn,label)=>{for(let n=0;n<200;n++){if(childError)throw Error(childError);if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label+' '+stderr.slice(-2000))};
 const route=(chat,locale='en')=>base+'/?'+new URLSearchParams({account,chat,lang:locale,theme:'dark'});
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  const context=await browser.newContext({viewport:{width:1440,height:960},locale:'en'});await context.route('**/*',r=>{if(new URL(r.request().url()).origin!==base){external.push(r.request().url());return r.abort()}return r.continue()});
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));let documents=0;page.on('request',r=>{if(r.isNavigationRequest()&&r.frame()===page.mainFrame())documents++});
  await page.goto(route(room));await page.locator('[data-open-sender]:visible').first().waitFor();await until(()=>streams.size>0,'initial SSE ready');await page.waitForTimeout(350);
  const avatar=page.locator('[data-open-sender]:visible').last(),draft='Preserve my draft 😀';await page.locator('#message-composer').fill(draft);await avatar.scrollIntoViewIfNeeded();await avatar.focus();
  await page.evaluate(()=>{window.senderTimeline=document.querySelector('#message-timeline');window.senderRow=document.querySelector('[data-message-id]');window.senderScroll=senderTimeline.scrollTop;window.senderDocument=crypto.randomUUID()});
  const marker=await page.evaluate(()=>senderDocument),loads=documents,readStart=state.reads.length;
  await page.keyboard.press('Enter');const drawer=page.getByRole('dialog',{name:'User information',exact:true});await drawer.waitFor({state:'visible'});await page.locator('[data-sender-info]').waitFor();
  assert.equal(await page.locator('[data-sender-info] h2').textContent(),'bob@fixture');assert((await page.locator('[data-sender-info]').textContent()).includes('Saved room'));assert((await page.locator('[data-sender-info]').textContent()).includes('Fixture account'));
  assert.equal(await page.locator('[data-sender-info] [data-sender-address],[data-sender-info] [data-presence-state],[data-sender-info] img').count(),0);
  assert(!state.reads.slice(readStart).some(p=>p.includes('avatars')||p.endsWith('/details')),'room drawer must not query personal profile or current participant evidence');
  assert.equal(documents,loads);assert.equal(await page.evaluate(()=>senderDocument),marker);assert(await page.evaluate(()=>senderTimeline===document.querySelector('#message-timeline')&&senderRow===document.querySelector('[data-message-id]')));assert.equal(await page.locator('#message-composer').inputValue(),draft);assert(await page.evaluate(()=>Math.abs(senderTimeline.scrollTop-senderScroll)<2));
  await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});assert(await avatar.evaluate(el=>document.activeElement===el));
  findings.push('Room avatar opens by keyboard with retained name, room and account only; matching personal-address nickname gains no image or presence, no inferred profile, reload, draft/scroll/DOM change; Escape restores avatar focus');
  state.fail=true;await avatar.click();await drawer.waitFor({state:'visible'});await page.locator('.oie-sender-retry').waitFor();assert.equal(await page.locator('[data-sender-info]').count(),0);state.fail=false;await page.locator('.oie-sender-retry').click();await page.locator('[data-sender-info]').waitFor();await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});
  findings.push('Failed saved-info read clears previous data and offers a scoped retry that recovers without a full reload');
  state.delay=650;await avatar.click();await drawer.waitFor({state:'visible'});await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});await page.waitForTimeout(800);assert.equal(await page.locator('[data-sender-info]').count(),0);state.delay=0;
  findings.push('Closing a drawer aborts its pending request and a late response cannot restore its content');
  await page.goto(route(direct));const directAvatar=page.locator('[data-open-sender]:visible').last();await directAvatar.click();await drawer.waitFor({state:'visible'});await page.locator('[data-sender-info]').waitFor();assert.equal(await page.locator('[data-sender-name]').textContent(),'Robert <Published> Name');assert.equal(await page.locator('[data-sender-nickname]').textContent(),'Bob');assert.equal(await page.locator('[data-sender-address]').textContent(),'bob@fixture');assert.equal(await page.locator('[data-sender-info] [data-presence-state]').getAttribute('data-presence-state'),'away');assert((await page.locator('[data-sender-info]').textContent()).includes('At lunch <script>plain</script>'));assert.equal(await page.locator('[data-sender-info] script').count(),0);await until(async()=>await page.locator('[data-sender-info] img').evaluate(img=>img.naturalWidth===1),'cached direct peer photo');
  findings.push('Direct sender uses the canonical conversation address, retained verified photo and escaped observed away/status data');
  await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});state.delay=650;await directAvatar.click();await drawer.waitFor({state:'visible'});await page.evaluate(()=>window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id:'sender'}})));await drawer.waitFor({state:'hidden'});await page.locator(`#conversation-list a[data-conversation-id="${room}"]`).click();await until(async()=>await page.locator('#conversation-pane').getAttribute('data-conversation-id')===room,'conversation fragment navigation');state.delay=0;await page.waitForTimeout(800);assert.equal(await page.locator('[data-sender-info]').count(),0);assert.equal(await page.locator('#sender-content').isVisible(),false);
  findings.push('Conversation fragment replacement disposes sender requests and late direct-peer results cannot populate another room drawer');
  state.roomProfile='known';await page.locator('[data-open-sender]:visible').last().click();await drawer.waitFor({state:'visible'});await page.locator('[data-sender-info]').waitFor();
  assert.equal(await page.locator('[data-sender-name]').textContent(),'Robert <Published> Name');assert.equal(await page.locator('[data-sender-nickname]').textContent(),'bob@fixture');assert.equal(await page.locator('[data-sender-address]').textContent(),'actual-bob@fixture');assert.equal(await page.locator('[data-sender-info] [data-presence-state]').count(),0);assert.equal(await page.locator('[data-sender-info] script').count(),0);await until(async()=>await page.locator('[data-sender-info] [data-avatar-address="actual-bob@fixture"] img').evaluate(img=>img.naturalWidth===1),'verified room author photo');await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});
  findings.push('A scoped engine profile shows published full name, distinct retained nickname and disclosed user@host; the room photo uses only that verified address and does not infer current room presence');
  state.foreignProfile=true;await page.locator('[data-open-sender]:visible').last().click();await drawer.waitFor({state:'visible'});await page.locator('[data-sender-info]').waitFor();assert.equal(await page.locator('[data-sender-name]').textContent(),'bob@fixture');assert.equal(await page.locator('[data-sender-address],[data-sender-nickname],[data-sender-info] img').count(),0);await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});state.foreignProfile=false;
  findings.push('A foreign-account profile cannot replace the sender name, expose an address or grant a room avatar');
  state.oldProfileEngine=true;await page.locator('[data-open-sender]:visible').last().click();await drawer.waitFor({state:'visible'});await page.locator('[data-sender-info]').waitFor();assert.equal(await page.locator('[data-sender-name]').textContent(),'bob@fixture');assert.equal(await page.locator('[data-sender-address]').count(),0);await page.keyboard.press('Escape');await drawer.waitFor({state:'hidden'});state.oldProfileEngine=false;
  findings.push('Older engines without the optional profile endpoint keep the retained sender name and room context');
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});
  for(const width of [390,1440])for(const locale of ['en','pt-BR']){
   const p=await context.newPage();p.on('pageerror',e=>errors.push(e.message));await p.setViewportSize({width,height:960});await p.goto(route(room,locale));await p.locator('[data-open-sender]:visible').last().click();const title=locale==='en'?'User information':'Informações do usuário';await p.getByRole('dialog',{name:title,exact:true}).waitFor({state:'visible'});await p.locator('[data-sender-info]').waitFor();await p.waitForTimeout(230);assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));const box=await p.getByRole('dialog',{name:title,exact:true}).boundingBox();assert(box.x>=-1&&box.x+box.width<=width+1);await p.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`${width} ${locale}`);await p.screenshot({path:path.join(artifacts,`sender-info-${width}-${locale}.png`)});await p.close();findings.push({width,locale,axe:0});
  }
  const native=await context.newPage();await native.goto(route(room)+'&open=sender&senderMessage='+id(223));await native.getByRole('dialog',{name:'User information',exact:true}).waitFor({state:'visible'});await native.locator('[data-sender-info]').waitFor();assert.equal(await native.locator('[data-sender-info]').getAttribute('data-sender-message'),id(223));await native.close();findings.push('Addressable sender URL renders and opens the same scoped drawer');
  assert.deepEqual(state.writes,[]);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);await fs.writeFile(path.join(artifacts,'sender-info-report.json'),JSON.stringify({findings,mutationEffects:state.writes,pageErrors:errors,externalBrowserRequests:external},null,2));console.log('PASS: sender drawer scoped identity, direct cached profile, room privacy, keyboard/focus, fragment/draft/scroll preservation, retry/stale guards, responsive accessibility and zero writes');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(error=>{console.error(error);process.exitCode=1});
