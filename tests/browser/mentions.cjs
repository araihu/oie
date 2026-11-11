// The browser talks only to an isolated HTTP engine; no real room is joined.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {once}=require('node:events'),{spawn}=require('node:child_process'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),room=id(3),direct=id(4),other=id(5),revision=id(6),streams=new Set(),receipts=new Map(),messages=[];
 const chats=[{id:room,kind:'room',title:'Room'},{id:direct,kind:'direct',title:'Direct'},{id:other,kind:'room',title:'Other room'}].map(c=>({...c,accountId:account,revisionId:revision,address:c.id+'@fixture',updatedAt:'2026-11-09T12:00:00Z',unreadCount:0,archived:false,favorite:false,lastMessage:''}));
 const candidates=[{id:'occupant/ana',displayName:'Ána',insertText:'@ana',referenceMode:'text'},{id:'occupant/zoe',displayName:'Zoë 🐻',insertText:'@zoë',referenceMode:'structured'},{id:'occupant/caio',displayName:'<b>Caio</b>',insertText:'@caio',referenceMode:'text'},...Array.from({length:65},(_,n)=>({id:'occupant/'+n,displayName:'User '+n,insertText:'@user'+n,referenceMode:'text'}))];
 candidates.push(
  {id:'occupant/abruno',displayName:'Abruno',insertText:'@abruno',referenceMode:'text'},
  {id:'occupant/bruno',displayName:'Bruno',insertText:'@bruno',referenceMode:'text'},
  {id:'occupant/bruna',displayName:'Bruna',insertText:'@bruna',referenceMode:'text'},
  {id:'occupant/lia',displayName:'Lia Braga',insertText:'@lia',referenceMode:'text'}
 );
 const state={reads:0,posts:0,effects:0,fail:false,delay:0,partial:false};
 const api=http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://fixture'),parts=u.pathname.split('/');res.setHeader('Content-Type','application/json');const send=(code,data)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.19.0',eventCursor:engine+':0'});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,address:'me@fixture',label:'Me',protocol:'xmpp',createdAt:'2026-11-09T12:00:00Z'}]});
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:messages.length,conversations:3,updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/capabilities'))return send(200,{accountId:account,state:'available',directText:true,features:['room_messaging','send_replies'].map(feature=>({id:feature,appSupport:'supported',protocolSupport:'supported',serverSupport:'scope_dependent',scope:'room'})),discovery:{state:'complete',services:[]}});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:chats});
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  if(u.pathname.includes('/operations/')){const item=receipts.get(parts.at(-1));return item?send(200,{id:parts.at(-1),kind:'send_message',resourceId:item.message.id,createdAt:item.message.createdAt}):send(404,{status:404})}
  if(u.pathname.startsWith('/api/v1/messages/')){const message=messages.find(m=>m.id===parts[4]);return message?send(200,message):send(404,{status:404})}
  const chat=chats.find(c=>c.id===parts[4]);
  if(chat&&parts.length===5)return send(200,chat);
  if(chat&&parts[5]==='mention-candidates'){state.reads++;if(state.delay)await new Promise(r=>setTimeout(r,state.delay));return state.fail?send(503,{status:503}):send(200,{conversationId:chat.id,accountId:account,state:state.partial?'partial':'known',truncated:state.partial,items:candidates})}
  if(chat&&parts[5]==='room-session')return send(200,{conversationId:chat.id,accountId:account,state:'joined',nickname:'me',active:true,intentId:id(8),intentRevisionId:id(9),updatedAt:'2026-11-09T12:00:00Z'});
  if(chat&&parts[5]==='messages'){
   if(req.method==='POST'){state.posts++;let raw='';for await(const b of req)raw+=b;const body=JSON.parse(raw),key=req.headers['idempotency-key'];if(receipts.has(key)){assert.deepEqual(body,receipts.get(key).body);return send(200,receipts.get(key).message)}
    state.effects++;const message={id:id(100+state.effects),revisionId:revision,conversationId:chat.id,accountId:account,sender:{id:account,displayName:'Me'},originalText:body.originalText,mentions:body.mentions?.map(m=>({...m,referenceMode:candidates.find(c=>c.id===m.participantId)?.referenceMode||'text'})),createdAt:'2026-11-09T12:00:00Z',isOwn:true,sendState:'queued'};messages.push(message);receipts.set(key,{body,message});return send(202,message)}
   return send(200,{items:messages.filter(m=>m.conversationId===chat.id).slice().reverse()});
  }
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews')||u.pathname.endsWith('/message-stars')||u.pathname==='/api/v1/shared-content')return send(200,{items:[]});
  if(req.method!=='GET')throw Error('unexpected write '+req.method+' '+u.pathname);
  return send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');const allocator=http.createServer();allocator.listen(0,'127.0.0.1');await once(allocator,'listening');const port=allocator.address().port;await new Promise(r=>allocator.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let log='';child.stderr.on('data',c=>log+=c);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],findings=[];
 const until=async(check,label)=>{for(let n=0;n<220;n++){if(await check())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label+'\n'+log)};
 const url=(chat=room,lang='en',theme='dark')=>`${base}/?chat=${chat}&lang=${lang}&theme=${theme}`;
 let page;
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF ready');
  const delayed=await browser.newContext({viewport:{width:390,height:900},locale:'en'}),delayedPage=await delayed.newPage();delayedPage.on('pageerror',e=>errors.push(e.message));
  state.delay=250;await delayedPage.goto(url());const delayedInput=delayedPage.locator('#message-composer'),delayedMenu=delayedPage.locator('#oie-mention-picker');let reads=state.reads;
  await delayedInput.fill('@an');await until(()=>state.reads>reads,'delayed mention lookup started');await delayedInput.press('Tab');await new Promise(r=>setTimeout(r,400));assert(await delayedMenu.isHidden(),'a delayed lookup must not reopen the menu after Tab');assert(await delayedInput.evaluate(e=>document.activeElement!==e));
  await delayedPage.reload();reads=state.reads;await delayedInput.fill('@an');await until(()=>state.reads>reads,'second delayed lookup started');await delayedPage.locator('#chat-header').click({position:{x:5,y:5}});await new Promise(r=>setTimeout(r,400));assert(await delayedMenu.isHidden(),'a delayed lookup must not reopen the menu after an outside click');
  await delayedInput.click();await until(async()=>await delayedPage.locator('[data-mention-option]').count()===1&&await delayedMenu.isVisible(),'explicit return to the composer restores suggestions');await delayedInput.press('Escape');assert(await delayedMenu.isHidden());await delayed.close();state.delay=0;
  findings.push('Delayed candidate responses respect Tab, outside dismissal and explicit return to the composer');
  const context=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'});page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));await page.goto(url());await until(async()=>await page.locator('#message-composer').isEnabled(),'room composer ready');
  const input=page.locator('#message-composer'),menu=page.locator('#oie-mention-picker'),options=page.locator('[data-mention-option]'),metadata=()=>page.locator('input[name=mentions]').inputValue().then(JSON.parse);
  await input.fill('@ana, depois');await input.evaluate(e=>{e.setSelectionRange(3,3);e.dispatchEvent(new Event('select'));});await until(async()=>await options.count()===1&&await menu.isVisible(),'completion in the middle of a nickname');await input.press('Enter');assert.equal(await input.inputValue(),'@ana, depois','completion replaces the whole nickname and preserves punctuation');assert.deepEqual(await metadata(),[{participantId:'occupant/ana',begin:0,end:4}]);
  await input.fill('😀 @zoë tail');await input.evaluate(e=>{e.setSelectionRange(6,6);e.dispatchEvent(new Event('select'));});await until(async()=>await options.count()===1&&await menu.isVisible(),'Unicode middle completion');await input.press('Enter');assert.equal(await input.inputValue(),'😀 @zoë tail');assert.deepEqual(await metadata(),[{participantId:'occupant/zoe',begin:2,end:6}]);
  const emailReads=state.reads;await input.fill('email@example.org');assert(await menu.isHidden());assert.equal(state.reads,emailReads);
  await input.fill('😀 @an');await menu.waitFor({state:'visible'});await until(async()=>await options.count()===1,'accent-folded match');assert.match(await options.first().textContent(),/Ána/);
  await input.press('Enter');assert.equal(await input.inputValue(),'😀 @ana ');assert(await menu.isHidden());assert.deepEqual(await metadata(),[{participantId:'occupant/ana',begin:2,end:6}]);assert.equal(state.effects,0);assert(await input.evaluate(e=>document.activeElement===e));
  await input.evaluate(e=>e.setSelectionRange(0,0));await page.keyboard.type('é ');assert.deepEqual(await metadata(),[{participantId:'occupant/ana',begin:4,end:8}]);
  await page.reload();await until(async()=>await input.inputValue()==='é 😀 @ana ','draft restored');assert.deepEqual(await metadata(),[{participantId:'occupant/ana',begin:4,end:8}]);
  await input.focus();await input.evaluate(e=>e.setSelectionRange(6,7));await page.keyboard.type('X');assert.deepEqual(await metadata(),[],'editing selected mention must discard only metadata');
  await input.fill('@');await menu.waitFor({state:'visible'});await until(async()=>await options.count()===50,'bounded suggestions');await input.press('ArrowDown');assert(await input.getAttribute('aria-activedescendant'));await input.press('Escape');assert(await menu.isHidden());assert.equal(await input.inputValue(),'@');
  await input.click();await menu.waitFor({state:'visible'});await input.press('Tab');assert.equal(await input.inputValue(),'@');assert(await input.evaluate(e=>document.activeElement!==e));
  await input.fill('@');await menu.waitFor({state:'visible'});
  await input.pressSequentially('br');
  assert.deepEqual(await options.locator('[data-mention-name]').allTextContents(),['Bruna','Bruno','Abruno','Lia Braga']);
  assert.equal(await options.first().getAttribute('aria-selected'),'true');
  await input.press('Shift+Tab');assert.equal(await input.inputValue(),'@br');assert(await input.evaluate(e=>document.activeElement!==e));assert(await menu.isHidden());
  await input.focus();await menu.waitFor({state:'visible'});await input.press('ArrowDown');await input.press('Tab');
  assert.equal(await input.inputValue(),'@bruno ');assert.deepEqual(await metadata(),[{participantId:'occupant/bruno',begin:0,end:6}]);assert(await input.evaluate(e=>document.activeElement===e));assert(await menu.isHidden());assert.equal(state.effects,0);
  await input.fill('@nomatch');await menu.waitFor({state:'visible'});assert.equal(await options.count(),0);await input.press('Tab');assert.equal(await input.inputValue(),'@nomatch');assert(await input.evaluate(e=>document.activeElement!==e));
  await input.fill('@');await menu.waitFor({state:'visible'});
  await input.click();await menu.waitFor({state:'visible'});await page.locator('.oie-mention-panel').focus();await page.keyboard.press('Escape');assert(await menu.isHidden(),'Escape from the scrollable panel must stay dismissed after returning focus');assert(await input.evaluate(e=>document.activeElement===e));
  findings.push('Nickname prefixes rank first, Tab confirms a filtered selection without sending, Shift+Tab moves focus, and Escape dismisses the focused panel');
  await input.fill('');await input.evaluate(e=>{e.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));e.value='@an';e.dispatchEvent(new InputEvent('input',{bubbles:true,isComposing:true}));e.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true,isComposing:true}));});assert(await menu.isHidden());assert.equal(state.effects,0);await input.evaluate(e=>e.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));await menu.waitFor({state:'visible'});await input.press('Escape');
  await input.fill('@ca');await until(async()=>await options.count()===1,'escaped name');assert.equal(await options.locator('b').count(),0);assert.match(await options.textContent(),/<b>Caio<\/b>/);
  await input.press('Escape');await input.fill('@zo');await until(async()=>await options.count()===1,'unicode nickname');await input.press('Enter');assert.deepEqual(await metadata(),[{participantId:'occupant/zoe',begin:0,end:4}]);
  await page.locator('[data-send-message]').click();await until(()=>state.effects===1,'one accepted mention');await until(async()=>!await input.evaluate(e=>e.readOnly),'accepted draft cleared');assert.equal(state.posts,1);assert.deepEqual(messages[0].mentions,[{participantId:'occupant/zoe',begin:0,end:4,referenceMode:'structured'}]);assert.deepEqual(await metadata(),[]);
  findings.push('Room-only completion, accent filtering, Unicode offsets, edits, draft restore, escaped labels and one explicit send');
  await input.fill('@an');await until(async()=>await options.count()===1,'pending mention option');await input.press('Enter');
  await page.route('**/ui/messages/reconcile',route=>route.abort('connectionreset'));
  let dropped=false;await page.route('**/ui/messages/send',async route=>{if(!dropped){dropped=true;await route.fetch();await route.abort('connectionreset')}else await route.continue()});
  await page.locator('[data-send-message]').click();await until(()=>state.effects===2,'accepted message with lost response');await until(async()=>await page.locator('#send-message-form').getAttribute('data-busy')==='false','uncertain response remains retryable');assert(await input.evaluate(e=>e.readOnly));assert.equal(await input.inputValue(),'');
  const pending=await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('oie.draft.v1')).map(k=>JSON.parse(localStorage.getItem(k))).find(v=>v.pending));assert(pending.pending);assert.deepEqual(pending.mentions,[{participantId:'occupant/ana',begin:0,end:4}]);
  await page.locator('input[name=mentions]').evaluate(e=>e.value='[{"participantId":"forged","begin":0,"end":1}]');const readsBeforeRetry=state.reads;state.fail=true;await page.locator('[data-pending-send-retry]').click();await until(async()=>!await input.evaluate(e=>e.readOnly),'immutable accepted receipt recovers pending send');state.fail=false;assert.equal(state.effects,2);assert.equal(state.posts,2);assert.equal(state.reads,readsBeforeRetry);await page.unroute('**/ui/messages/send');
  findings.push('A lost acceptance response retains immutable mention metadata; retry uses the same receipt without participant reads or a duplicate send');

  await page.goto(url(direct));await input.fill('@ana');assert.equal(await menu.count(),0);assert.equal(state.effects,2);await input.fill('');
  await page.goto(url());state.fail=true;await input.fill('@');await menu.waitFor({state:'visible'});await until(async()=>await page.locator('[data-mention-retry]').isVisible(),'failed load retry');assert.equal(await input.inputValue(),'@');state.fail=false;state.partial=true;await page.locator('[data-mention-retry]').click();await until(async()=>await options.count()===50,'retry candidates');assert(await page.locator('[data-mention-partial]').isVisible());await input.press('Escape');
  state.delay=300;await page.goto(url());await input.fill('@');await menu.waitFor({state:'visible'});await page.goto(url(other));state.delay=0;await new Promise(r=>setTimeout(r,400));assert.equal(await input.inputValue(),'');assert(await menu.isHidden());assert.deepEqual(await metadata(),[]);
  await context.close();findings.push('Direct chats unchanged, failed/partial lists remain editable, retry and delayed cross-room response isolation');
  for(const width of [390,1440])for(const lang of ['en','pt-BR'])for(const theme of ['light','dark'])for(const libraryTheme of ['goshtoso','minimal']){
   const ctx=await browser.newContext({viewport:{width,height:900},locale:lang});page=await ctx.newPage();page.on('pageerror',e=>errors.push(e.message));await page.goto(url(room,lang,theme));await page.evaluate(value=>document.documentElement.dataset.theme=value,libraryTheme);const field=page.locator('#message-composer');
   await field.focus();
   await page.evaluate(()=>{
    const nodes=['.oie-composer','#message-timeline','[data-send-message]'].map(selector=>document.querySelector(selector));
    const measure=()=>nodes.flatMap(node=>{const {x,y,width,height}=node.getBoundingClientRect();return [x,y,width,height,node.scrollTop]});
    const frames=window.mentionFrames={baseline:measure(),samples:[],id:0};
    const sample=()=>{frames.samples.push(measure());frames.id=requestAnimationFrame(sample)};
    frames.id=requestAnimationFrame(sample);
   });
   state.delay=200;
   await field.fill('@br');await until(async()=>await page.locator('[data-mention-option]').count()===4,'matrix option');
   await field.press('Escape');assert(await page.locator('#oie-mention-picker').isHidden());
   await field.press('u');await field.press('Tab');assert.equal(await field.inputValue(),'@bruna ');assert(await field.evaluate(e=>document.activeElement===e));
   const frames=await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>{cancelAnimationFrame(mentionFrames.id);resolve(mentionFrames)})));
   assert(frames.samples.length>1);
   for(const sample of frames.samples)assert(sample.every((value,index)=>Math.abs(value-frames.baseline[index])<=1),JSON.stringify({width,lang,theme,baseline:frames.baseline,sample}));
   state.delay=0;
   await field.fill('@an');await until(async()=>await page.locator('[data-mention-option]').count()===1,'matrix click option');await page.locator('[data-mention-option]').click();assert.equal(await field.inputValue(),'@ana ');assert(await field.evaluate(e=>document.activeElement===e));await field.fill('@');await until(async()=>await page.locator('[data-mention-option]').count()===50,'matrix list');
   const geometry=await page.locator('#oie-mention-picker').boundingBox();assert(geometry&&geometry.x>=0&&geometry.y>=0&&geometry.x+geometry.width<=width+1&&geometry.height>0,JSON.stringify({width,geometry}));
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const a11y=await page.evaluate(async()=>{const result=await axe.run(document.querySelector('#send-message-form'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}});return result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.html)}))});assert.deepEqual(a11y,[],JSON.stringify({width,lang,theme,a11y}));
   if(width===390&&lang==='pt-BR'&&theme==='dark'){await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});await page.screenshot({path:path.join(__dirname,'artifacts/mentions-mobile.png')})}
   await ctx.close();
  }
  assert.deepEqual(errors,[]);assert.equal(state.effects,2);await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});await fs.writeFile(path.join(__dirname,'artifacts/mentions-report.json'),JSON.stringify({findings,errors,reads:state.reads,effects:state.effects},null,2));console.log('PASS: room mention completion, filtering, Unicode ranges, safe drafts, scoped loading/retry, explicit send and EN/PT mobile/desktop accessibility');
 }catch(error){if(page&&!page.isClosed())console.error('Mention evidence',await page.evaluate(()=>({text:document.querySelector('#message-composer')?.value,metadata:document.querySelector('[name=mentions]')?.value,menu:document.querySelector('#oie-mention-picker')?.outerHTML.slice(0,1500)})));throw error}
 finally{await browser.close();child.kill('SIGTERM');for(const s of streams)s.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
