// Isolated protocol-free HTTP engine fixture. SQLite/CAS durability is checked
// independently by conversation_preferences_test.go against the real engine.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),foreign=id(3),streams=new Set(),receipts=new Map(),errors=[],findings=[];
 const state={sequence:0,revision:8000,effects:0,providerEffects:0,patches:[],hold:false,release:null,beforePatch:null,engine};
 const accounts=[{id:account,address:'alice@fixture',label:'Personal'},{id:foreign,address:'work@fixture',label:'Work'}].map(value=>({...value,revisionId:id(4),protocol:'xmpp',connectionState:'connected',createdAt:'2026-11-09T12:00:00Z'}));
 const chats=Array.from({length:26},(_,n)=>({id:id(100+n),revisionId:id(300+n),accountId:n===25?foreign:account,kind:'direct',address:'peer'+n+'@fixture',title:'Conversation '+n,lastMessage:'Retained text '+n,updatedAt:new Date(Date.UTC(2026,10,9,12,n)).toISOString(),unreadCount:n===0?3:0,archived:n>=20&&n<25,favorite:n===19}));
 const [chat,sibling,nativeChat]=[chats[0],chats[6],chats[2]];
 const messages=new Map(chats.map((conversation,n)=>[conversation.id,Array.from({length:36},(_,index)=>({id:id(1000+n*100+index),revisionId:id(5),accountId:conversation.accountId,conversationId:conversation.id,sender:{id:id(8),displayName:'Peer'},originalText:'Retained message '+index+'\nPreserve the reading position and exact draft.',createdAt:new Date(Date.UTC(2026,10,9,12,index)).toISOString(),isOwn:false,sendState:'received',historical:true}))]));
 const emit=conversation=>{state.sequence++;for(const stream of streams)stream.write(`id: ${state.engine}:${state.sequence}\nevent: conversation.updated\ndata: ${JSON.stringify({type:'conversation.updated',conversation})}\n\n`)};
 const revise=(conversation,flags)=>{Object.assign(conversation,flags,{revisionId:id(++state.revision)});return conversation};
 const api=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,value)=>{res.statusCode=code;res.end(JSON.stringify(value))};
  if(req.method==='PATCH'&&/^\/api\/v1\/conversations\/[^/]+$/.test(url.pathname)){
   let raw='';for await(const chunk of req)raw+=chunk;let body;try{body=JSON.parse(raw)}catch{return send(400,{code:'invalid_request'})}
   const conversation=chats.find(value=>value.id===url.pathname.split('/').at(-1)),key=req.headers['idempotency-key'];state.patches.push({key,path:url.pathname,body:structuredClone(body)});
   if(state.hold)await new Promise(resolve=>state.release=resolve);
   const beforePatch=state.beforePatch;state.beforePatch=null;beforePatch?.();
   const fingerprint=JSON.stringify([url.pathname,body.accountId,body.expectedRevisionId,Object.hasOwn(body,'archived')?body.archived:null,Object.hasOwn(body,'favorite')?body.favorite:null]);
   if(receipts.has(key)){const receipt=receipts.get(key);if(receipt.fingerprint!==fingerprint)return send(409,{code:'idempotency_conflict'});res.setHeader('Idempotency-Replayed','true');return send(200,receipt.conversation)}
   if(!conversation)return send(404,{code:'not_found'});
   if(conversation.accountId!==body.accountId)return send(400,{code:'invalid_request'});
   if(conversation.revisionId!==body.expectedRevisionId)return send(409,{code:'state_conflict',currentConversation:conversation});
   const fields=['archived','favorite'].filter(field=>Object.hasOwn(body,field));if(!key||!fields.length||fields.some(field=>typeof body[field]!=='boolean'))return send(400,{code:'invalid_request'});
   const changed=fields.some(field=>conversation[field]!==body[field]);if(changed){revise(conversation,Object.fromEntries(fields.map(field=>[field,body[field]])));state.effects++;emit(conversation)}
   receipts.set(key,{fingerprint,conversation:structuredClone(conversation)});res.setHeader('Idempotency-Replayed','false');return send(200,conversation);
  }
  if(req.method!=='GET'){state.providerEffects++;return send(405,{code:'unexpected_protocol_write'})}
  if(url.pathname==='/api/v1/engine')return send(200,{id:state.engine,role:'hosted',apiVersion:'0.16.0',eventCursor:state.engine+':'+state.sequence});
  if(url.pathname==='/api/v1/accounts')return send(200,{items:accounts});
  const knownAccount=accounts.find(value=>url.pathname==='/api/v1/accounts/'+value.id);if(knownAccount)return send(200,knownAccount);
  if(url.pathname.endsWith('/connection'))return send(200,{accountId:url.pathname.includes(foreign)?foreign:account,state:'connected',updatedAt:chat.updatedAt});
  if(url.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:36,conversations:chats.length,updatedAt:chat.updatedAt});
  if(url.pathname.endsWith('/avatars')||url.pathname.endsWith('/previews')||url.pathname.endsWith('/message-stars')||url.pathname==='/api/v1/shared-content')return send(200,{items:[]});
  if(url.pathname==='/api/v1/conversations'){
   let selected=chats.filter(value=>value.archived===(url.searchParams.get('archived')==='true'));
   if(url.searchParams.get('accountId'))selected=selected.filter(value=>value.accountId===url.searchParams.get('accountId'));
   if(url.searchParams.get('favoritesOnly')==='true')selected=selected.filter(value=>value.favorite);
   if(url.searchParams.get('unreadOnly')==='true')selected=selected.filter(value=>value.unreadCount>0);
   if(url.searchParams.get('q'))selected=selected.filter(value=>value.title.toLowerCase().includes(url.searchParams.get('q').toLowerCase()));
   return send(200,{items:selected});
  }
  for(const conversation of chats){
   const prefix='/api/v1/conversations/'+conversation.id,all=messages.get(conversation.id);
   if(url.pathname===prefix)return send(200,conversation);
   if(url.pathname===prefix+'/messages'){
    const cursor=url.searchParams.get('cursor'),direction=url.searchParams.get('direction')||'older',limit=Number(url.searchParams.get('limit')||50),index=cursor?all.findIndex(value=>value.id===cursor):all.length;
    if(index<0)return send(404,{code:'not_found'});const start=direction==='newer'?index+1:Math.max(0,index-limit),end=direction==='newer'?Math.min(all.length,start+limit):index,items=all.slice(start,end).reverse();
    return send(200,{items,nextCursor:direction==='newer'?(end<all.length?items[0]?.id:undefined):(start>0?items.at(-1)?.id:undefined)});
   }
   if(url.pathname===prefix+'/details')return send(200,{conversationId:conversation.id,accountId:conversation.accountId,kind:'direct',address:conversation.address,state:'known',checkedAt:chat.updatedAt,accountConnection:{accountId:conversation.accountId,state:'connected',updatedAt:chat.updatedAt},participants:{scope:'peer',state:'unknown',items:[]},features:[]});
  }
  if(url.pathname.startsWith('/api/v1/messages/')){const message=[...messages.values()].flat().find(value=>value.id===url.pathname.split('/').at(-1));return message?send(200,message):send(404,{code:'not_found'})}
  if(url.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{code:'not_found'});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');const allocator=http.createServer();allocator.listen(0,'127.0.0.1');await once(allocator,'listening');const port=allocator.address().port;await new Promise(resolve=>allocator.close(resolve));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let stderr='';child.stderr.on('data',chunk=>stderr+=chunk);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true});let page;const contexts=[];
 const until=async(fn,label)=>{for(let n=0;n<200;n++){if(await fn())return;await new Promise(resolve=>setTimeout(resolve,50))}throw Error('Timed out: '+label+'\n'+stderr)};
 const fresh=async(options={})=>{const context=await browser.newContext({viewport:{width:1440,height:950},locale:'en',...options});contexts.push(context);const page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));return page};
 const url=(conversation=chat,extra='')=>base+'/?chat='+conversation.id+'&account='+conversation.accountId+'&lang=en&theme=dark'+extra;
 const form=(page,field)=>page.locator('#conversation-preferences-'+chat.id+'-'+(field==='archived'?'archive':'favorite'));
 const action=(page,field)=>page.locator((field==='archived'?['archive','unarchive','retry-archive']:['favorite','unfavorite','retry-favorite']).map(prefix=>'#'+prefix+'-conversation-'+chat.id+':visible').join(','));
 const openMenu=async page=>{await page.locator('#chat-options button').first().click();await action(page,'archived').waitFor({state:'visible'})};
 const clickAction=async(page,field)=>{await openMenu(page);await action(page,field).click()};
 const keyboardAction=async(page,field)=>{
  await page.locator('#chat-options button').first().focus();await page.keyboard.press('Enter');
  await until(async()=>await page.locator('#chat-options [role=menuitem]:visible').first().evaluate(node=>document.activeElement===node),'keyboard focuses the first available menu item');
  for(const next of field==='favorite'?['archived','favorite']:['archived']){await page.keyboard.press('ArrowDown');await until(async()=>await action(page,next).evaluate(node=>document.activeElement===node),'arrow navigation skips hidden preference variants');}
  await page.keyboard.press('Enter');
 };
 const currentFlag=(page,field)=>page.locator('[data-conversation-preference-state]').getAttribute('data-'+field);
 const settled=async(page,field,value)=>until(async()=>await currentFlag(page,field)===String(value)&&await form(page,field).locator('[name="expectedRevisionId"]').inputValue()===chat.revisionId,'canonical '+field+'='+value);
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  page=await fresh();await page.goto(url());await page.locator('#message-composer').waitFor({state:'visible'});
  const observer=await fresh();await observer.goto(url(chat,'&section=archived'));await observer.locator('#conversation-list').waitFor({state:'attached'});assert.equal(await observer.locator('[data-conversation-row="'+chat.id+'"]').count(),0);
  const draft='Keep this exact draft while preferences change 😀';await page.locator('#message-composer').fill(draft);
  const geometry=await page.evaluate(sibling=>{
   const timeline=document.getElementById('message-timeline'),list=document.getElementById('conversation-list');timeline.scrollTop=(timeline.scrollHeight-timeline.clientHeight)/2;list.scrollTop=180;
   const row=[...timeline.querySelectorAll('[data-message-id]')].find(value=>value.getBoundingClientRect().bottom>timeline.getBoundingClientRect().top);
   window.preferenceNodes={app:document.getElementById('oie-app'),pane:document.getElementById('conversation-pane'),timeline,list,composer:document.getElementById('message-composer'),sidebar:document.querySelector('.oie-sidebar'),sibling:document.querySelector('[data-conversation-row="'+sibling+'"]'),row};
   return {rowId:row.dataset.messageId,top:row.getBoundingClientRect().top-timeline.getBoundingClientRect().top,listTop:list.scrollTop};
  },sibling.id);
  await keyboardAction(page,'favorite');await until(()=>chat.favorite,'favorite commits');await settled(page,'favorite',true);
  await until(async()=>await page.locator('[data-conversation-row="'+chat.id+'"] .oie-pin').count()===1,'favorite badge appears beside unread count');
  assert.equal(await page.locator('[data-conversation-row="'+chat.id+'"] .oie-unread-count').textContent(),'3');
  assert.equal(await page.locator('#message-composer').inputValue(),draft);
  assert(await page.evaluate(sibling=>{const n=window.preferenceNodes;return n.app===document.getElementById('oie-app')&&n.pane===document.getElementById('conversation-pane')&&n.timeline===document.getElementById('message-timeline')&&n.composer===document.getElementById('message-composer')&&n.sidebar===document.querySelector('.oie-sidebar')&&n.list===document.getElementById('conversation-list')&&n.sibling===document.querySelector('[data-conversation-row="'+sibling+'"]')},sibling.id),'preference changes preserve sibling and workspace identities');
  const after=await page.evaluate(rowId=>({top:document.querySelector('[data-message-id="'+rowId+'"]').getBoundingClientRect().top-document.getElementById('message-timeline').getBoundingClientRect().top,listTop:document.getElementById('conversation-list').scrollTop}),geometry.rowId);
  assert(Math.abs(after.top-geometry.top)<3,'preference metadata preserves the visible reading anchor');assert.equal(after.listTop,geometry.listTop);
  findings.push('App-owned favorite preserves the source conversation, unread badge, draft, sibling DOM, timeline and list/reading position');

  await clickAction(page,'archived');await settled(page,'archived',true);
  await until(async()=>await page.locator('[data-conversation-row="'+chat.id+'"]').count()===0,'archived conversation leaves the ordinary inbox');
  await until(async()=>await observer.locator('[data-conversation-row="'+chat.id+'"]').count()===1,'archived view receives source-linked SSE');
  assert.equal(await page.locator('#message-composer').inputValue(),draft);assert.equal(await currentFlag(page,'favorite'),'true');
  await clickAction(page,'archived');await settled(page,'archived',false);await until(async()=>await observer.locator('[data-conversation-row="'+chat.id+'"]').count()===0,'unarchive leaves archived filter');
  await page.goto(url(chat,'&filter=favorites'));await page.locator('[data-conversation-row="'+chat.id+'"]').waitFor({state:'visible'});
  await clickAction(page,'favorite');await settled(page,'favorite',false);await until(async()=>await page.locator('[data-conversation-row="'+chat.id+'"]').count()===0,'unfavorite leaves favorites filter');
  findings.push('Archive/unarchive and favorite/unfavorite update their own filters through SSE while keeping the selected conversation available');

  await page.goto(url());const beforeDouble=state.patches.length;state.hold=true;await openMenu(page);await action(page,'favorite').evaluate(button=>{button.click();button.click()});await until(()=>!!state.release,'one held preference write');
  assert.equal(state.patches.length,beforeDouble+1,'double activation starts one API command');state.hold=false;state.release();state.release=null;await settled(page,'favorite',true);
  const staleKey=await form(page,'favorite').locator('[name="operationKey"]').inputValue(),beforeStale=state.effects;
  state.beforePatch=()=>revise(chat,{favorite:false});await clickAction(page,'favorite');await until(async()=>await page.locator('#conversation-preference-status').isVisible(),'stale preference feedback');
  await settled(page,'favorite',false);assert.equal(state.effects,beforeStale,'409 never automatically applies an opposite or fresh intent');
  assert.notEqual(await form(page,'favorite').locator('[name="operationKey"]').inputValue(),staleKey,'a rejected precondition is replaced with a fresh intent key');
  const staleRequests=state.patches.length;await page.waitForTimeout(200);assert.equal(state.patches.length,staleRequests,'a rejected preference is not retried automatically');
  findings.push('Double activation has one effect; stale revisions project current truth and require a fresh explicit intent');

  let lost=false;const loseReply=async route=>{const response=await route.fetch();assert.equal(response.status(),200);assert((await response.json()).ok);lost=true;await route.abort('failed')};
  await page.route('**/ui/conversations/preferences',loseReply);const beforeLoss=state.effects;await clickAction(page,'favorite');await until(()=>lost&&state.effects===beforeLoss+1,'drop a real accepted preference reply');
  await page.locator('#conversation-preference-status').waitFor({state:'visible'});const original=structuredClone(state.patches.at(-1));
  await page.unroute('**/ui/conversations/preferences',loseReply);await page.reload();await page.locator('#message-composer').waitFor({state:'visible'});
  revise(chat,{favorite:false});emit(chat);await until(async()=>await currentFlag(page,'favorite')==='false','newer live projection remains visible while an older intent is pending');await openMenu(page);assert(await action(page,'archived').isDisabled(),'another field cannot overwrite an uncertain intent');
  assert.match(await action(page,'favorite').textContent(),/Retry conversation change/);const retryKey=await form(page,'favorite').locator('[name="operationKey"]').inputValue();await action(page,'favorite').click();
  await until(async()=>await form(page,'favorite').locator('[name="operationKey"]').inputValue()!==retryKey&&await form(page,'favorite').locator('[name="expectedRevisionId"]').inputValue()===chat.revisionId,'exact accepted receipt clears pending intent');
  assert.deepEqual(state.patches.at(-1),original,'retry retains exact field, desired value, key and original precondition');assert.equal(state.effects,beforeLoss+1,'receipt replay causes no duplicate effect');
  assert.equal(chat.favorite,false,'retry never revives an obsolete accepted snapshot');await settled(page,'favorite',false);
  findings.push('An accepted reply lost in transit survives reload, freezes the exact command, blocks unrelated changes and replays once without reviving an obsolete snapshot');

  const beforeGuards=state.patches.length;
  for(const fields of [{csrf:'invalid'},{engineId:id(999)},{accountId:foreign},{favorite:'invalid'},{archived:'true',favorite:'true'}]){
   const rejected=await page.evaluate(async fields=>{const form=document.querySelector('[data-conversation-preference="favorite"]'),body=new URLSearchParams(new FormData(form));for(const [key,value]of Object.entries(fields))body.set(key,value);const response=await fetch(form.action,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':fields.csrf||document.querySelector('meta[name="oie-csrf"]').content},body});return {status:response.status,body:await response.json()}},fields);
   assert(rejected.status>=400&&!rejected.body.ok,JSON.stringify(rejected));
  }
  assert.equal(state.patches.length,beforeGuards,'CSRF, engine, account and malformed field guards run before preference delivery');
  findings.push('Cross-engine/account requests, CSRF mismatch and malformed field combinations are rejected before the API command');

  const native=await fresh({javaScriptEnabled:false});await native.goto(url(nativeChat));const nativeBefore=state.effects;
  await native.locator('form[data-conversation-preference="archived"] button[type="submit"]').click();assert(nativeChat.archived&&!nativeChat.favorite);assert.equal(state.effects,nativeBefore+1);
  await native.locator('form[data-conversation-preference="favorite"] button[type="submit"]').click();assert(nativeChat.archived&&nativeChat.favorite);assert.equal(state.effects,nativeBefore+2);
  findings.push('The same guarded archive and favorite commands work through native forms without scripting');

  const mobile=await fresh({viewport:{width:390,height:844},locale:'pt-BR',hasTouch:true});await mobile.goto(base+'/?chat='+chat.id+'&account='+account+'&lang=pt-BR&theme=light');
  await openMenu(mobile);assert.equal((await action(mobile,'archived').textContent()).trim(),'Arquivar conversa');assert.equal((await action(mobile,'favorite').textContent()).trim(),'Adicionar às favoritas');await mobile.keyboard.press('Escape');
  await keyboardAction(mobile,'favorite');await settled(mobile,'favorite',true);
  await mobile.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const audit=await mobile.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
  assert.deepEqual(audit.violations.map(value=>({id:value.id,nodes:value.nodes.map(node=>node.target)})),[]);assert(await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  assert.equal(state.providerEffects,0,'preference actions never send protocol messages or other mutations');assert.deepEqual(errors,[]);
  await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});await fs.writeFile(path.join(__dirname,'artifacts/conversation-preferences-report.json'),JSON.stringify({findings,preferenceEffects:state.effects,requests:state.patches,providerEffects:state.providerEffects,pageErrors:errors,mobileAxe:audit.violations.length},null,2));
  console.log('PASS: scoped conversation archive/favorite, filters/SSE, preserved draft/DOM/scroll, exact uncertain replay, CAS conflicts, native forms and localized keyboard/mobile accessibility');
 }catch(error){if(page&&!page.isClosed())console.error('Preference failure evidence',JSON.stringify(await page.evaluate(()=>({url:location.href,status:document.querySelector('#conversation-preference-status')?.textContent,forms:[...document.querySelectorAll('[data-conversation-preference]')].map(form=>({field:form.dataset.conversationPreference,values:Object.fromEntries(new FormData(form))})),current:document.querySelector('[data-conversation-preference-state]')?.outerHTML})).catch(()=>({}))),JSON.stringify({patches:state.patches,effects:state.effects}));throw error}
 finally{state.release?.();await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
