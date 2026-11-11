const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
const wait=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn,label){for(let i=0;i<240;i++){if(await fn())return;await wait(50)}throw Error('Timed out: '+label)}
(async()=>{
 const engine=id(1),account=id(2),chat=id(3),other=id(4),revision=id(5),streams=new Set(),read=new Set(),operations=new Map(),posts=[];
 const messages=Array.from({length:40},(_,n)=>({id:id(100+n),revisionId:revision,accountId:account,conversationId:chat,sender:{id:id(9),displayName:'Bob'},originalText:'Retained message '+n,createdAt:new Date(Date.UTC(2026,10,8,12,n)).toISOString(),isOwn:false,historical:n>0&&n<37,sendState:'received'}));
 messages.push({...messages[0],id:id(200),conversationId:other,originalText:'Other chat unread'});
 Object.assign(messages[0],{originalText:'Alice, hello',mentionsYou:true});
 Object.assign(messages[39],{originalText:'Hello @Alice!',mentionsYou:true});
 let seq=0,effects=0,lost=false,hold=null;
 const unread=c=>messages.filter(m=>m.conversationId===c&&!m.historical&&!m.isOwn&&!read.has(m.id));
 const count=c=>unread(c).length;
 const state=c=>({conversationId:c,accountId:account,revisionId:effects?id(500+effects):null,updatedAt:effects?'2026-11-08T13:00:00Z':null,unreadCount:count(c)});
 function emit(){seq++;for(const stream of streams)stream.write(`id: ${engine}:${seq}\nevent: conversation.updated\ndata: {}\n\n`)}
 function incoming(text='New incoming',mentionsYou=false){const m={...messages[0],id:id(300+messages.length),originalText:text,mentionsYou,createdAt:new Date(Date.UTC(2026,10,8,14,messages.length)).toISOString()};messages.push(m);emit();return m}
 const api=http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(status,data)=>{res.statusCode=status;res.end(JSON.stringify(data))};let raw='';for await(const c of req)raw+=c;
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.17.0',eventCursor:engine+':'+seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,address:'alice@fixture',label:'Alice',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}]});
  if(u.pathname==='/api/v1/accounts/'+account)return send(200,{id:account,revisionId:revision,address:'alice@fixture',label:'Alice',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.startsWith('/api/v1/messages/')){const message=messages.find(m=>m.id===u.pathname.split('/').at(-1));return send(message?200:404,message||{status:404,code:'not_found'})}
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:messages.length,conversations:2,updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/avatars'))return send(200,{items:[]});
  const chats=[chat,other].map(c=>({id:c,revisionId:revision,accountId:account,kind:c===chat?'room':'direct',address:c===chat?'room@rooms.fixture':'carol@fixture',title:c===chat?'Room':'Carol',lastMessage:'Latest retained',updatedAt:'2026-11-08T13:00:00Z',unreadCount:count(c),unreadMention:c===chat&&unread(c).some(m=>m.mentionsYou),archived:false,favorite:false}));
  if(u.pathname==='/api/v1/conversations')return send(200,{items:chats.filter(c=>u.searchParams.get('archived')!=='true'&&(u.searchParams.get('unreadOnly')!=='true'||c.unreadCount>0))});
  for(const c of chats){
   if(u.pathname==='/api/v1/conversations/'+c.id)return send(200,c);
   if(u.pathname===`/api/v1/conversations/${c.id}/messages`){
    const retained=messages.filter(m=>m.conversationId===c.id).sort((a,b)=>b.createdAt.localeCompare(a.createdAt)||b.id.localeCompare(a.id)),cursor=u.searchParams.get('cursor'),position=retained.findIndex(m=>m.id===cursor),newer=u.searchParams.get('direction')==='newer',limit=Number(u.searchParams.get('limit')||50);
    const candidates=cursor?(newer?retained.slice(0,position):retained.slice(position+1)):retained,items=newer?candidates.slice(-limit):candidates.slice(0,limit);
    return send(200,{items,...(candidates.length>items.length?{nextCursor:newer?items[0].id:items.at(-1).id}:{})});
   }
   if(u.pathname===`/api/v1/conversations/${c.id}/previews`)return send(200,{items:[]});
   if(u.pathname===`/api/v1/conversations/${c.id}/message-stars`)return send(200,{items:[]});
   if(u.pathname===`/api/v1/conversations/${c.id}/read-state`){
    if(req.method==='GET')return send(200,state(c.id));
    assert.equal(req.method,'POST');const body=JSON.parse(raw),key=req.headers['idempotency-key'];posts.push({key,raw,conversation:c.id});let snapshot=operations.get(key);
    if(!snapshot){let changed=false;for(const mid of body.messageIds){const m=messages.find(m=>m.id===mid);assert(m&&m.conversationId===c.id);if(!m.historical&&!m.isOwn&&!read.has(mid)){read.add(mid);changed=true}}if(changed){effects++;emit()}snapshot=state(c.id);operations.set(key,snapshot)}
    if(hold){const gate=hold;hold=null;gate.accepted();await gate.release}
    if(lost){lost=false;return send(503,{status:503,code:'unavailable'})}return send(200,snapshot);
   }
  }
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404,code:'not_found'});
 });api.listen(0,'127.0.0.1');await once(api,'listening');
 const reserve=http.createServer();reserve.listen(0,'127.0.0.1');await once(reserve,'listening');const port=reserve.address().port;await new Promise(r=>reserve.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let logs='';child.stderr.on('data',b=>logs+=b);
 const browser=await chromium.launch({headless:true}),base='http://127.0.0.1:'+port,errors=[];
 let debugPage;
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF ready');
  const context=await browser.newContext({viewport:{width:1400,height:900}});
  await context.addInitScript(()=>{window.testFocus=false;window.testHidden=false;Object.defineProperty(document,'hasFocus',{value:()=>window.testFocus});Object.defineProperty(document,'hidden',{get:()=>window.testHidden});localStorage.setItem('oie.device.auto-image-downloads.v1','false')});
  const page=await context.newPage();debugPage=page;page.on('pageerror',e=>errors.push(e.message));let documents=0;page.on('request',r=>{if(r.resourceType()==='document')documents++});
  await page.goto(base+'/?chat='+chat+'&lang=en');await until(async()=>page.evaluate(()=>!!window.OieRuntime?.generation),'runtime');
  await wait(1500);assert.equal(posts.length,0,'background/focusless view was read');assert.equal(count(chat),4);
  const roomMark=page.locator('[data-conversation-id="'+chat+'"] [data-unread-mention]');
  const highlighted=page.locator('[data-message-id="'+messages[0].id+'"] [data-message-mention]');
  assert.equal(await roomMark.getAttribute('aria-label'),'Unread mention');
  assert.equal(await highlighted.getAttribute('aria-label'),'You were mentioned');
  assert.equal(await page.locator('[data-message-mention]').count(),2);
  await page.evaluate(()=>{window.testFocus=true;window.dispatchEvent(new Event('focus'))});
  await until(()=>count(chat)===1,'visible latest messages read');assert(!read.has(messages[0].id),'offscreen older message read');await until(async()=>await page.locator('a[data-conversation-id="'+chat+'"] .oie-unread-count').textContent()==='1','partial unread badge');
  assert.equal(await roomMark.count(),1,'offscreen mention keeps the room marked');
  await page.locator('#message-timeline').evaluate(n=>n.scrollTop=0);await until(()=>count(chat)===0,'older visible message read');await until(async()=>await page.locator('a[data-conversation-id="'+chat+'"] .oie-unread-count').count()===0,'badge cleared');await until(async()=>await page.locator('[data-account-unread="'+account+'"]').first().textContent()==='1','account unread aggregate');
  assert.equal(await roomMark.count(),0);
  assert.equal(await highlighted.count(),1,'reading keeps the message highlight');
  await page.evaluate(()=>window.testFocus=false);const next=incoming();await until(async()=>await page.locator('#message-timeline [data-message-id="'+next.id+'"]').count()===1,'incoming row');await wait(1200);assert.equal(count(chat),1);
  await page.locator('#message-timeline').evaluate(n=>n.scrollTop=n.scrollHeight);await page.evaluate(()=>window.testFocus=true);await until(()=>count(chat)===0,'refocused message read');
  await page.evaluate(()=>{window.testHidden=true;document.dispatchEvent(new Event('visibilitychange'))});const hidden=incoming();await until(async()=>await page.locator('#message-timeline [data-message-id="'+hidden.id+'"]').count()===1,'hidden incoming');await wait(1200);assert.equal(count(chat),1,'hidden document read');
  await page.evaluate(()=>{window.testHidden=false;document.dispatchEvent(new Event('visibilitychange'))});await until(()=>count(chat)===0,'visible document resumes');
  // A modal covers the timeline even when the document itself retains focus.
  await page.evaluate(()=>{const d=document.createElement('dialog');d.id='read-test-overlay';d.textContent='Overlay';document.body.append(d);d.showModal()});const covered=incoming();await wait(1500);assert.equal(count(chat),1,'covered conversation read');await page.evaluate(()=>{const d=document.querySelector('#read-test-overlay');d.close();d.remove()});await until(()=>count(chat)===0,'uncovered message read');
  // Lose a committed reply, then preserve one operation key while retrying.
  lost=true;const beforePosts=posts.length,priorEffects=effects;incoming('Uncertain acknowledgement');await until(()=>posts.length>=beforePosts+2&&count(chat)===0,'read retry');const attempts=posts.slice(beforePosts);assert.equal(attempts[0].key,attempts[1].key);assert.equal(attempts[0].raw,attempts[1].raw);assert.equal(effects,priorEffects+1);
  // A later arrival must survive an older accepted read acknowledgement.
  let acceptedResolve,releaseResolve;const accepted=new Promise(r=>acceptedResolve=r),release=new Promise(r=>releaseResolve=r);hold={accepted:acceptedResolve,release};incoming('Read before racing arrival');await accepted;await page.evaluate(()=>window.testFocus=false);const racing=incoming('Alice, later arrival remains unread',true);releaseResolve();await until(async()=>await page.locator('#message-timeline [data-message-id="'+racing.id+'"]').count()===1,'racing arrival rendered');await wait(1200);assert.equal(count(chat),1,'old read response cleared newer arrival');await until(async()=>await page.locator('a[data-conversation-id="'+chat+'"] .oie-unread-count').textContent()==='1','newer unread badge retained');
  assert.equal(await roomMark.count(),1,'older acknowledgement preserved the new mention');
  await page.locator('a[data-conversation-id="'+other+'"]').click();await until(async()=>await page.locator('#oie-app').getAttribute('data-conversation-id').then(v=>v===other),'other chat route');await page.evaluate(()=>window.testFocus=true);await until(()=>count(other)===0,'other conversation read');assert.equal(count(chat),1);
  assert.equal(documents,1);assert.deepEqual(errors,[]);
  await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await page.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>v.id),[]);
  for(const [width,lang] of [[1440,'en'],[390,'pt-BR']]){
   const viewport=await context.newPage();viewport.on('pageerror',e=>errors.push(e.message));await viewport.setViewportSize({width,height:900});await viewport.goto(base+'/?chat='+chat+'&lang='+lang);
   const row=viewport.locator('[data-message-id="'+racing.id+'"]');
   assert.equal(await row.locator('[data-message-mention]').getAttribute('aria-label'),lang==='pt-BR'?'Você foi mencionado':'You were mentioned');
   assert.equal(await viewport.locator('[data-unread-mention]').getAttribute('aria-label'),lang==='pt-BR'?'Menção não lida':'Unread mention');
   await until(()=>streams.size>=2,'geometry viewport event stream ready');
   await viewport.evaluate(async mid=>{
    await document.fonts.ready;
    const measure=()=>['#message-timeline','.oie-composer','[data-message-id="'+mid+'"] [data-message-bubble]'].flatMap(selector=>{
     const node=document.querySelector(selector),{x,y,width,height}=node.getBoundingClientRect();
     return [x,y,width,height,node.scrollTop];
    });
    const samples=window.mentionGeometry={baseline:measure(),frames:[]};
    const tick=()=>{samples.frames.push(measure());samples.id=requestAnimationFrame(tick)};
    samples.id=requestAnimationFrame(tick);
   },racing.id);
   await viewport.route(url=>url.pathname==='/ui/refresh',async route=>{await wait(200);await route.continue()});
   racing.mentionsYou=false;racing.revisionId=id(width);emit();
   await until(async()=>await row.locator('[data-message-mention]').count()===0,'updated mention highlight clears');
   await until(async()=>await viewport.locator('[data-unread-mention]').count()===0,'updated conversation mark clears');
   const geometry=await viewport.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>{cancelAnimationFrame(mentionGeometry.id);resolve(mentionGeometry)})));
   assert(geometry.frames.length>1);
   for(const frame of geometry.frames)assert(frame.every((value,i)=>Math.abs(value-geometry.baseline[i])<=1),JSON.stringify({width,lang,baseline:geometry.baseline,frame}));
   await viewport.close();racing.mentionsYou=true;
  }
  assert.deepEqual(errors,[]);
  console.log('PASS: visible-only read batches, incoming mentions and stable localized markers, later arrivals, same-key uncertain retries, conversation isolation, HTMX and accessibility');
 }catch(error){console.error('Fixture state',JSON.stringify({streams:streams.size,posts,effects,messages:messages.length,errors}));if(debugPage)console.error('DOM state',await debugPage.evaluate(()=>({url:location.href,version:document.querySelector('#message-timeline')?.dataset.version,ids:[...document.querySelectorAll('#message-timeline [data-message-id]')].map(n=>n.dataset.messageId),live:document.querySelector('[data-live-status]')?.textContent})));throw error;}finally{await browser.close();for(const stream of streams)stream.end();child.kill('SIGTERM');await once(child,'exit').catch(()=>{});await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
