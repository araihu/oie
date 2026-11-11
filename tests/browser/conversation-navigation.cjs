// Isolated HTTP engines only; no retained user accounts or protocol traffic.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),other=id(3),revision=id(4),streams=new Set(),reads=[],state={hold:null,release:null,writes:0,seq:0};
 const accounts=[{id:account,address:'personal@fixture',label:'Personal'},{id:other,address:'work@fixture',label:'Work'}].map(value=>({...value,revisionId:revision,protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}));
 const chats=Array.from({length:24},(_,index)=>({id:id(100+index),revisionId:revision,accountId:index%2?other:account,address:'chat'+index+'@fixture',title:'Conversation '+index,kind:'direct',updatedAt:'2026-11-08T12:00:00Z',lastMessage:'Fixture',unreadCount:0,archived:false,favorite:false}));
 const [a,b,c,room]=[chats[0],chats[8],chats[9],chats[23]];
 room.kind='room';c.favorite=true;
 const photoURL='https://images.fixture/photo.png',photoPath='/ui/media/'+'a'.repeat(64),photoMessage=id(2055);
 const photo={id:id(2100),revisionId:revision,accountId:c.accountId,conversationId:c.id,messageId:photoMessage,url:photoURL,kind:'image',state:'ready',downloadState:'ready',imageHash:'a'.repeat(64),updatedAt:c.updatedAt};
 const message=chat=>({id:id(1000+chats.indexOf(chat)),revisionId:revision,conversationId:chat.id,accountId:chat.accountId,sender:{id:id(5),displayName:'Peer'},originalText:'Text for '+chat.title,createdAt:'2026-11-08T12:00:00Z',isOwn:false,sendState:'received'});
 const api=http.createServer(async(req,res)=>{
  const url=new URL(req.url,'http://fixture');reads.push(url.pathname+url.search);res.setHeader('Content-Type','application/json');const send=(status,value)=>{res.statusCode=status;res.end(JSON.stringify(value))};
  if(req.method!=='GET'){state.writes++;return send(405,{status:405})}
  if(url.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.15.0',eventCursor:engine+':'+state.seq});
  if(url.pathname==='/api/v1/accounts')return send(200,{items:accounts});
  if(url.pathname.endsWith('/connection'))return send(200,{accountId:url.pathname.includes(other)?other:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(url.pathname.endsWith('/history'))return send(200,{accountId:account,state:'partial',retainedMessages:24,conversations:24,updatedAt:'2026-11-08T12:00:00Z'});
  if(url.pathname.endsWith('/previews'))return send(200,{items:url.searchParams.getAll('messageId').includes(photoMessage)?[photo]:[]});
  if(url.pathname.endsWith('/avatars')||url.pathname==='/api/v1/shared-content'||url.pathname.endsWith('/message-stars'))return send(200,{items:[]});
  if(url.pathname==='/api/v1/contacts')return send(200,{items:[],sources:accounts.filter(value=>!url.searchParams.get('accountId')||value.id===url.searchParams.get('accountId')).map(value=>({accountId:value.id,state:'known'}))});
  if(url.pathname==='/api/v1/messages/search'){
   const q=(url.searchParams.get('q')||'').trim(),accountId=url.searchParams.get('accountId')||'',conversationId=url.searchParams.get('conversationId')||'',includeEditHistory=url.searchParams.get('includeEditHistory')==='true',limit=Number(url.searchParams.get('limit')||50);
   const matched=chats.map(message).filter(value=>(!accountId||value.accountId===accountId)&&(!conversationId||value.conversationId===conversationId)&&value.originalText.toLowerCase().includes(q.toLowerCase())).sort((a,b)=>Date.parse(b.createdAt)-Date.parse(a.createdAt)||b.id.localeCompare(a.id));
   const cursor=url.searchParams.get('cursor'),start=cursor?matched.findIndex(value=>value.id===cursor)+1:0;if(cursor&&start===0)return send(400,{status:400,code:'invalid_cursor'});
   return send(200,{query:q,accountId:accountId||undefined,conversationId:conversationId||undefined,includeEditHistory,items:matched.slice(start,start+limit).map(value=>({message:value,accountLabel:accounts.find(a=>a.id===value.accountId).label,conversationTitle:chats.find(c=>c.id===value.conversationId).title,matchedRevisionId:value.revisionId,matchedAt:value.createdAt,historical:false,matchKind:'text',excerpt:value.originalText})),nextCursor:matched.length>start+limit?matched[start+limit-1].id:undefined,metadataIndexing:{state:'complete',indexedMessages:24,totalMessages:24}});
  }
  if(url.pathname==='/api/v1/conversations'){const filtered=chats.filter(chat=>(!url.searchParams.get('accountId')||chat.accountId===url.searchParams.get('accountId'))&&chat.archived===(url.searchParams.get('archived')==='true')&&chat.title.toLowerCase().includes((url.searchParams.get('q')||'').toLowerCase())&&(!url.searchParams.has('kind')||chat.kind===url.searchParams.get('kind'))&&(!url.searchParams.has('favoritesOnly')||chat.favorite)&&(!url.searchParams.has('unreadOnly')||chat.unreadCount>0));const limit=Number(url.searchParams.get('limit')||100);return send(200,{items:filtered.slice(0,limit),nextCursor:filtered.length>limit?filtered[limit-1].id:undefined});}
  for(const chat of chats){
   const prefix='/api/v1/conversations/'+chat.id;
   if(url.pathname===prefix)return send(200,chat);
   if(url.pathname===prefix+'/messages'){
    if(state.hold?.chat===chat){
     const hold=state.hold;
     hold.requested=true;
     res.on('close',()=>{if(!res.writableEnded)hold.aborted=true});
     await hold.gate;
    }
    const all=chat===c?Array.from({length:60},(_,index)=>({...message(chat),id:id(2000+index),originalText:index===55?photoURL:'Long conversation message '+index})):[message(chat)];
    const limit=Number(url.searchParams.get('limit')||50),direction=url.searchParams.get('direction')||'older',cursor=url.searchParams.get('cursor'),index=cursor?all.findIndex(item=>item.id===cursor):all.length;
    if(!Number.isInteger(limit)||limit<1||limit>100||!['older','newer'].includes(direction))return send(400,{status:400});
    if(index<0)return send(409,{status:409,code:'invalid_cursor'});
    const start=direction==='newer'?index+1:Math.max(0,index-limit),end=direction==='newer'?Math.min(all.length,start+limit):index,items=all.slice(start,end).reverse();
    const nextCursor=direction==='newer'?(end<all.length?items[0]?.id:undefined):(start>0?items.at(-1)?.id:undefined);
    return send(200,{items,nextCursor});
   }
   if(url.pathname===prefix+'/details')return send(200,{conversationId:chat.id,accountId:chat.accountId,kind:'direct',address:chat.address,state:'known',checkedAt:chat.updatedAt,accountConnection:{accountId:chat.accountId,state:'connected',updatedAt:chat.updatedAt},participants:{scope:'peer',state:'unknown',items:[]},features:[]});
  }
  if(url.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');const allocator=http.createServer();allocator.listen(0,'127.0.0.1');await once(allocator,'listening');const port=allocator.address().port;await new Promise(resolve=>allocator.close(resolve));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let log='';child.stderr.on('data',chunk=>log+=chunk);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],requests=[];let page;
 const until=async(check,label)=>{for(let n=0;n<200;n++){if(await check())return;await new Promise(resolve=>setTimeout(resolve,50))}throw Error('Timed out: '+label+'\n'+log)};
 const fresh=async(options={})=>{const context=await browser.newContext({viewport:{width:1440,height:950},locale:'en',...options});await context.addInitScript(()=>{window.documentToken=Math.random();localStorage.setItem('oie.device.auto-image-downloads.v1','false')});const page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>requests.push({kind:request.resourceType(),headers:request.headers(),url:request.url()}));return{context,page}};
 const layout=async()=>{const size=await page.evaluate(()=>{const rect=selector=>{const r=document.querySelector(selector).getBoundingClientRect();return{top:r.top,bottom:r.bottom}};const timeline=document.querySelector('#message-timeline');return{viewport:innerHeight,workspace:rect('#workspace'),pane:rect('.oie-chat-pane'),composer:rect('.oie-composer'),footer:document.querySelector('.oie-list-account').getBoundingClientRect().height?rect('.oie-list-account'):null,pageHeight:document.documentElement.scrollHeight,timelineHeight:timeline.clientHeight,timelineContent:timeline.scrollHeight}});assert(Math.abs(size.workspace.bottom-size.viewport)<1,JSON.stringify(size));assert(Math.abs(size.pane.bottom-size.workspace.bottom)<1,JSON.stringify(size));assert(Math.abs(size.composer.bottom-size.workspace.bottom)<1,JSON.stringify(size));if(size.footer)assert(Math.abs(size.footer.bottom-size.workspace.bottom)<1,JSON.stringify(size));assert(size.timelineHeight>0&&size.pageHeight<=size.viewport+1,JSON.stringify(size));return size};
 const ready=async(chat)=>{await until(async()=>await page.locator('#message-composer').count()===1&&await page.evaluate(()=>document.body.dataset.conversationId)===chat.id,'selected '+chat.title);await until(()=>streams.size===1,'one event stream');await layout()};
 const skeleton=()=>page.locator('[data-conversation-loading]');
 const recordTimelineFrames=async(chat)=>{
  await page.evaluate(id=>{
   const samples=[];
   const sample=()=>{
    const pane=document.getElementById('conversation-pane'),timeline=document.getElementById('message-timeline');
    if(pane?.dataset.conversationId===id&&timeline?.getClientRects().length)samples.push({
     ready:document.body.dataset.conversationId===id,
     scroll:timeline.scrollTop,
     end:timeline.scrollHeight-timeline.clientHeight,
    });
    window.timelineFrame=requestAnimationFrame(sample);
   };
   window.timelineFrames=samples;
   sample();
  },chat.id);
 };
 const timelineFrames=async()=>{
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const samples=await page.evaluate(()=>{cancelAnimationFrame(window.timelineFrame);return window.timelineFrames});
  assert(samples.length>0);
  return samples;
 };
 const sampleOpening=async(chat,selectors)=>{
  const row=page.locator('#conversation-list a[data-conversation-id="'+chat.id+'"]');
  await row.scrollIntoViewIfNeeded();
  await page.evaluate(selectors=>{
   const measure=()=>selectors.map(selector=>{const node=document.querySelector(selector),rect=node.getBoundingClientRect();return [rect.x,rect.y,rect.width,rect.height,node.scrollTop]});
   const state={before:measure(),frames:[],id:0};
   const tick=()=>{state.frames.push(measure());state.id=requestAnimationFrame(tick)};
   window.openingGeometry=state;tick();
  },selectors);
  const gate=new Promise(resolve=>state.release=resolve);
  state.hold={chat,gate,requested:false};
  await row.click();
  await until(()=>state.hold.requested,'conversation request held');
  await skeleton().waitFor({state:'visible'});
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const {maxShift,frames}=await page.evaluate(()=>{
   const {before,frames,id}=window.openingGeometry;cancelAnimationFrame(id);
   const maxShift=Math.max(...frames.flatMap(frame=>frame.flatMap((rect,i)=>rect.map((value,j)=>Math.abs(value-before[i][j])))));
   return {maxShift,frames:frames.length};
  });
  assert(frames>=2);
  assert(maxShift<=1,'opening a conversation shifted the current view by '+maxShift+'px');
  const alignment=await skeleton().evaluate(node=>{
   const rect=node.getBoundingClientRect(),pane=node.parentElement.getBoundingClientRect();
   return [Math.abs(rect.top-pane.top),Math.abs(rect.bottom-pane.bottom),Math.abs(rect.left-pane.left),Math.abs(rect.right-pane.right)];
  });
  assert(Math.max(...alignment)<=1,JSON.stringify(alignment));
  assert.equal(await page.locator('#history-status,[data-navigation-status]').count(),0);
  assert.equal(await page.locator('[data-toast-container]').getByText(/Opening…|Abrindo…/).count(),0);
 };
 const click=async(chat)=>{const row=page.locator('#conversation-list a[data-conversation-id="'+chat.id+'"]');await row.scrollIntoViewIfNeeded();const top=await page.locator('#conversation-list').evaluate(node=>node.scrollTop);const generation=await page.evaluate(()=>window.OieRuntime.generation);await row.click();await until(async()=>await page.evaluate(()=>window.OieRuntime.generation)>generation,'conversation settled');await ready(chat);if(await page.locator('#conversation-list').isVisible())assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),top);assert(await page.evaluate(()=>window.savedList===document.getElementById('conversation-list')&&window.savedApp===document.getElementById('oie-app')&&window.savedSidebar===document.querySelector('.oie-sidebar')));assert.equal(await row.getAttribute('aria-current'),'true');assert.equal(await page.locator('#oie-avatar-data').count(),1);assert.equal(await page.locator('#oie-star-data').count(),1);assert.equal(await page.locator('#chat-info-content').getAttribute('data-details-conversation'),chat.id);return top};
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF ready');
  let opened=await fresh();page=opened.page;await page.goto(base+'/?chat='+a.id+'&lang=en&theme=dark');await ready(a);await page.setViewportSize({width:1530,height:1220});assert.equal((await layout()).timelineContent,(await layout()).timelineHeight);await page.setViewportSize({width:900,height:950});await layout();await page.setViewportSize({width:1440,height:950});const token=await page.evaluate(()=>window.documentToken);const startDocuments=requests.filter(request=>request.kind==='document').length;
  await page.evaluate(()=>{window.savedList=document.getElementById('conversation-list');window.savedApp=document.getElementById('oie-app');window.savedSidebar=document.querySelector('.oie-sidebar')});
  await page.locator('#message-composer').fill('Draft A');
  const originalQuery=await page.locator('#conversation-search').inputValue(),originalTitleParameter=new URL(page.url()).searchParams.get('q');
  await page.locator('#conversation-list').evaluate(node=>node.scrollTop=180);
  const searchGeneration=await page.evaluate(()=>{window.savedGlobalInput=document.getElementById('conversation-search');window.savedNavigationRows=[...document.querySelectorAll('#conversation-list a[data-conversation-id]')];return window.OieRuntime.generation}),searchTop=await page.locator('#conversation-list').evaluate(node=>node.scrollTop);
  await page.locator('#conversation-search').fill('unfinished filter');
  await until(async()=>await page.locator('[data-message-search-root]').getAttribute('data-search-query')==='unfinished filter'&&await page.locator('[data-message-search-root]').getAttribute('data-search-state')==='ready','unified global query settled');
  assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'unfinished filter');assert.equal(new URL(page.url()).searchParams.get('q'),originalTitleParameter);assert.equal(await page.locator('#conversation-search').inputValue(),'unfinished filter');assert(await page.locator('#conversation-search').evaluate(node=>document.activeElement===node));assert(await page.locator('#conversation-list-default').isHidden());assert(await page.locator('#conversation-list a[data-conversation-id="'+b.id+'"]').isHidden());assert.equal(await page.locator('[data-message-search-conversation]').count(),0);assert.equal(await page.locator('[data-message-search-hit]').count(),0);assert.equal(await page.locator('#message-composer').inputValue(),'Draft A');assert.equal(await page.evaluate(()=>window.OieRuntime.generation),searchGeneration);assert(await page.evaluate(()=>window.savedList===document.getElementById('conversation-list')&&window.savedGlobalInput===document.getElementById('conversation-search')&&window.savedNavigationRows.every((node,index)=>node.isConnected&&node===document.querySelectorAll('#conversation-list a[data-conversation-id]')[index])));
  await page.locator('[data-message-search-close]').click();await page.locator('#conversation-list-default').waitFor({state:'visible'});assert.equal(await page.locator('#conversation-search').inputValue(),originalQuery);assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),searchTop);assert(await page.locator('#conversation-search').evaluate(node=>document.activeElement===node));assert.equal(new URL(page.url()).searchParams.get('searchMode'),null);
  await click(b);assert.equal(await page.locator('#conversation-search').inputValue(),originalQuery);assert.equal(await page.locator('#message-composer').inputValue(),'');await page.locator('#message-composer').fill('Draft B');await page.locator('[data-open-details]').click();await page.getByRole('dialog',{name:'Conversation details',exact:true}).waitFor({state:'visible'});assert((await page.locator('.oie-details-identity').textContent()).includes(b.title));await page.keyboard.press('Escape');await page.getByRole('dialog',{name:'Conversation details',exact:true}).waitFor({state:'hidden'});await click(a);await until(async()=>await page.locator('#message-composer').inputValue()==='Draft A','A draft');
  const top=await page.locator('#conversation-list').evaluate(node=>node.scrollTop);let generation=await page.evaluate(()=>window.OieRuntime.generation);await page.goBack();await until(async()=>await page.evaluate(()=>window.OieRuntime.generation)>generation,'Back settled');await ready(b);assert.equal(await page.locator('#message-composer').inputValue(),'Draft B');assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),top);assert(await page.evaluate(()=>window.savedList===document.getElementById('conversation-list')));
  generation=await page.evaluate(()=>window.OieRuntime.generation);await page.goForward();await until(async()=>await page.evaluate(()=>window.OieRuntime.generation)>generation,'Forward settled');await ready(a);assert.equal(await page.locator('#message-composer').inputValue(),'Draft A');
  await click(b);await page.locator('[data-sidebar-toggle]').click();await page.locator('.oie-list-account').waitFor();
  await sampleOpening(a,['#conversation-list','.oie-list-account','#message-timeline','.oie-composer']);
  assert.equal(await page.locator('#message-composer').inputValue(),'Draft B');
  const settleDelay=await page.evaluate(()=>{const delay=window.htmx.config.defaultSettleDelay;window.htmx.config.defaultSettleDelay=180;return delay});
  let releasePhoto;
  const photoGate=new Promise(resolve=>releasePhoto=resolve);
  await page.route(url=>url.pathname===photoPath,async route=>{
   await photoGate;
   await route.fulfill({contentType:'image/png',body:Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2iEAAAAASUVORK5CYII=','base64')});
  });
  await recordTimelineFrames(c);
  const superseded=state.hold;
  await click(c);await skeleton().waitFor({state:'hidden'});
  await until(()=>superseded.aborted,'superseded conversation request aborted at engine');
  const image=page.locator('[data-message-id="'+photoMessage+'"] .oie-preview-thumbnail');
  const beforeDecode=await image.boundingBox();
  releasePhoto();
  assert(beforeDecode?.height>=100,JSON.stringify(beforeDecode));
  await image.evaluate(node=>node.decode());
  const afterDecode=await image.boundingBox();
  assert(Math.abs(afterDecode.height-beforeDecode.height)<=1&&Math.abs(afterDecode.width-beforeDecode.width)<=1,JSON.stringify({beforeDecode,afterDecode}));
  for(const sample of await timelineFrames())assert(sample.ready&&sample.end>0&&Math.abs(sample.scroll-sample.end)<=1,JSON.stringify(sample));
  const longLayout=await layout();assert(longLayout.timelineContent>longLayout.timelineHeight);await page.setViewportSize({width:1280,height:650});await layout();await page.setViewportSize({width:1440,height:950});state.hold=null;state.release();state.release=null;await new Promise(resolve=>setTimeout(resolve,200));assert.equal(await page.evaluate(()=>document.body.dataset.conversationId),c.id);
  const failureURL=url=>url.searchParams.get('chat')===a.id;
  let releaseFailure;
  const failureGate=new Promise(resolve=>releaseFailure=resolve);
  await page.route(failureURL,async route=>{
   await failureGate;
   await route.fulfill({status:503,body:'unavailable'});
  },{times:1});
  const beforeFailure=await page.evaluate(()=>{window.failedOpenPane=document.getElementById('conversation-pane');return location.href});
  await page.locator('#conversation-list a[data-conversation-id="'+a.id+'"]').click();
  try{
   await skeleton().waitFor({state:'visible'});
   assert(await page.locator('#message-timeline').evaluate(node=>node.inert));
   assert(await skeleton().locator('.oie-chat-skeleton-header').isHidden());
   assert(await skeleton().locator('.oie-chat-skeleton-composer').isHidden());
   assert(await page.locator('#chat-header').isVisible());
   assert(await page.locator('#message-composer').isVisible());
  }finally{releaseFailure();}
  await page.locator('[data-toast-container]').getByText('Could not open this view. Try again.',{exact:true}).waitFor();
  await skeleton().waitFor({state:'hidden'});
  assert(await page.evaluate(()=>window.failedOpenPane===document.getElementById('conversation-pane')&&window.savedList===document.getElementById('conversation-list')&&!document.getElementById('message-timeline').inert));
  assert.equal(page.url(),beforeFailure);
  const savedChatScroll=await page.locator('#message-timeline').evaluate(node=>{
   node.scrollTop=Math.floor((node.scrollHeight-node.clientHeight)/2);
   return node.scrollTop;
  });
  await click(b);
  await recordTimelineFrames(c);
  await page.goBack();await ready(c);
  for(const sample of await timelineFrames())assert(sample.ready&&Math.abs(sample.scroll-savedChatScroll)<=1,JSON.stringify(sample));
  await page.evaluate(delay=>{window.htmx.config.defaultSettleDelay=delay;},settleDelay);
  const liveTop=await page.locator('#conversation-list').evaluate(node=>node.scrollTop);chats[8].lastMessage='Updated live preview';state.seq++;for(const stream of streams)stream.write('id: '+engine+':'+state.seq+'\nevent: conversation.updated\ndata: {}\n\n');await until(async()=>await page.locator('#conversation-list').textContent().then(text=>text.includes('Updated live preview')),'changed inbox data updates');assert(await page.evaluate(()=>window.savedList===document.getElementById('conversation-list')));assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),liveTop);assert.equal(await page.evaluate(()=>window.documentToken),token);assert.equal(requests.filter(request=>request.kind==='document').length,startDocuments);assert(requests.some(request=>request.headers['x-oie-fragment']==='conversation'));
  await opened.context.close();await until(()=>streams.size===0,'desktop stream closed');
  opened=await fresh({viewport:{width:390,height:844}});page=opened.page;await page.goto(base+'/?lang=en&theme=dark');await until(async()=>await page.locator('#conversation-list').isVisible(),'mobile inbox');await page.evaluate(()=>{window.savedList=document.getElementById('conversation-list');window.savedApp=document.getElementById('oie-app');window.savedSidebar=document.querySelector('.oie-sidebar')});const mobileTop=await click(b);assert.equal(await page.locator('.oie-list-pane').isVisible(),false);await page.locator('.oie-back-button').click();await until(async()=>await page.locator('#conversation-list').isVisible(),'mobile back');assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),mobileTop);assert(await page.evaluate(()=>window.savedList===document.getElementById('conversation-list')));await opened.context.close();await until(()=>streams.size===0,'mobile stream closed');
  opened=await fresh({viewport:{width:390,height:844},locale:'pt-BR'});page=opened.page;
  await page.goto(base+'/?lang=pt-BR&theme=dark');
  await page.waitForFunction(()=>window.OieRuntime?.generation>0);
  // Open a different chat from the one the initial background refresh can read.
  await sampleOpening(b,['#conversation-list','.oie-list-account']);
  state.hold=null;state.release();state.release=null;
  await page.locator('#message-composer').waitFor();await ready(b);await opened.context.close();
  opened=await fresh();page=opened.page;
  await page.goto(base+'/?chat='+c.id+'&lang=en&theme=dark');
  await ready(c);await page.waitForFunction(()=>window.OieRuntime?.generation>0);
  const filter=name=>page.locator('a[data-filter="'+name+'"]');
  const activeFilter=async name=>await until(async()=>await filter(name).getAttribute('aria-current')==='true','active filter '+name);
  await page.locator('#message-composer').fill('Draft kept by filters');
  await page.evaluate(()=>{
   window.keptSelectors=['#oie-app','.oie-sidebar','.oie-list-heading','#conversation-search','.oie-list-account','#conversation-pane','#message-timeline','#message-composer'];
   window.keptNodes=window.keptSelectors.map(selector=>document.querySelector(selector));
   const timeline=document.getElementById('message-timeline');timeline.scrollTop=150;
   window.keptScroll=timeline.scrollTop;window.keptGeneration=window.OieRuntime.generation;
  });
  const verifyKept=async()=>{
   assert(await page.evaluate(()=>window.keptNodes.every((node,index)=>node===document.querySelector(window.keptSelectors[index]))&&window.keptGeneration===window.OieRuntime.generation&&window.keptScroll===document.getElementById('message-timeline').scrollTop));
   assert.equal(await page.locator('#message-composer').inputValue(),'Draft kept by filters');
   assert.equal(new URL(page.url()).searchParams.get('chat'),c.id);
   assert(await skeleton().isHidden());
  };

  await filter('favorites').click();await activeFilter('favorites');await verifyKept();
  assert.equal(await page.locator('#conversation-list [aria-current="true"]').getAttribute('data-conversation-id'),c.id);
  let releaseFilter,requestFilter;
  const filterGate=new Promise(resolve=>releaseFilter=resolve);
  const filterStarted=new Promise(resolve=>requestFilter=resolve);
  await page.route(url=>url.searchParams.get('filter')==='groups',async route=>{
   requestFilter();
   await filterGate;
   await route.continue();
  },{times:1});
  try{
   await filter('groups').focus();await filter('groups').press('Enter');await filterStarted;
   await activeFilter('favorites');await verifyKept();
  }finally{releaseFilter();}
  await activeFilter('groups');await verifyKept();
  assert.equal(await page.locator('#conversation-list [data-conversation-id="'+c.id+'"]').count(),0);
  assert.equal(await page.locator('#conversation-list [data-conversation-id="'+room.id+'"]').count(),1);
  assert(await filter('groups').evaluate(node=>node===document.activeElement));
  assert.equal(await page.locator('#conversation-filter-form input[name=filter]').inputValue(),'groups');
  await filter('all').click();await activeFilter('all');await verifyKept();
  await page.goBack();await activeFilter('groups');await verifyKept();
  await page.goForward();await activeFilter('all');await verifyKept();
  await page.route(url=>url.searchParams.get('filter')==='groups',route=>route.fulfill({status:503,body:'unavailable'}),{times:1});
  await filter('groups').click();
  await page.locator('[data-toast-container]').getByText('Could not open this view. Try again.',{exact:true}).waitFor();
  await activeFilter('all');await verifyKept();
  const beforeSelection=await page.evaluate(()=>window.OieRuntime.generation);
  await page.locator('#conversation-list a[data-conversation-id="'+b.id+'"]').click();await ready(b);
  assert(await skeleton().isHidden());
  assert.equal(await page.evaluate(()=>window.OieRuntime.generation),beforeSelection+1);
  await page.locator('#conversation-list a[data-conversation-id="'+b.id+'"]').click();
  assert.equal(await page.evaluate(()=>window.OieRuntime.generation),beforeSelection+1);
  assert(requests.some(request=>request.headers['x-oie-fragment']==='inbox'));
  await opened.context.close();await until(()=>streams.size===0,'filter stream closed');

  opened=await fresh({viewport:{width:390,height:844},locale:'pt-BR',reducedMotion:'reduce'});page=opened.page;
  await page.goto(base+'/?lang=pt-BR&theme=dark');
  await page.waitForFunction(()=>window.OieRuntime?.generation>0);
  await filter('groups').click();await activeFilter('groups');
  assert(await page.locator('#conversation-list').isVisible());
  assert.equal(await page.evaluate(()=>document.body.dataset.conversationId),a.id);
  assert.equal(new URL(page.url()).searchParams.get('inbox'),'1');
  await page.reload();await activeFilter('groups');
  assert(await page.locator('#conversation-list').isVisible());
  assert.equal(await page.evaluate(()=>document.body.dataset.conversationId),a.id);
  await filter('all').click();await activeFilter('all');
  await sampleOpening(b,['#conversation-list','.oie-list-account']);
  assert.equal(await skeleton().getByRole('status').textContent(),'Carregando mensagens…');
  assert(await skeleton().locator('[data-skeleton-item]').evaluateAll(nodes=>nodes.length>0&&nodes.every(node=>getComputedStyle(node).animationName==='none')));
  await skeleton().getByRole('button',{name:'Voltar'}).click();
  await skeleton().waitFor({state:'hidden'});
  assert(await page.locator('#conversation-list').isVisible());
  assert.equal(await page.evaluate(()=>document.body.dataset.conversationId),a.id);
  assert.equal(new URL(page.url()).searchParams.get('inbox'),'1');
  const row=page.locator('#conversation-list a[data-conversation-id="'+b.id+'"]');
  assert(await row.evaluate(node=>node===document.activeElement));
  state.hold=null;state.release();state.release=null;
  await row.click();await ready(b);
  assert(await skeleton().isHidden());
  await opened.context.close();
  opened=await fresh({javaScriptEnabled:false});page=opened.page;await page.goto(base+'/?chat='+a.id);await filter('groups').click();assert.equal(new URL(page.url()).searchParams.get('chat'),a.id);assert.equal(await page.locator('#chat-header').textContent().then(text=>text.includes(a.title)),true);await filter('all').click();await page.locator('#conversation-list a[data-conversation-id="'+b.id+'"]').click();assert.equal(new URL(page.url()).searchParams.get('chat'),b.id);assert.equal(await page.locator('#chat-header').textContent().then(text=>text.includes(b.title)),true);await opened.context.close();
  assert.deepEqual(errors,[]);assert.equal(state.writes,0);await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});await fs.writeFile(path.join(__dirname,'artifacts/conversation-navigation-report.json'),JSON.stringify({pageErrors:errors,mutations:state.writes,conversationFragmentRequests:requests.filter(request=>request.headers['x-oie-fragment']==='conversation').length},null,2));console.log('PASS: conversation-only swaps preserve inbox nodes/scroll/filter, isolated drafts/details, history, latest intent, one stream, mobile Back and native fallback');
 }catch(error){if(page&&!page.isClosed())console.error(JSON.stringify(await page.evaluate(()=>({url:location.href,body:{...document.body.dataset},pane:document.querySelector('#conversation-pane')?.outerHTML.slice(0,1000),links:[...document.querySelectorAll('#conversation-list a')].slice(0,2).map(node=>({href:node.href,target:node.getAttribute('hx-target')}))})).catch(()=>({}))));throw error}finally{state.release?.();await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
