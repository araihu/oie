// Isolated catalog actions fixture. No real engine or provider is contacted.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{createHash}=require('node:crypto'),{deflateSync}=require('node:zlib'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
function png(){const crc=b=>{let n=0xffffffff;for(const v of b){n^=v;for(let j=0;j<8;j++)n=(n>>>1)^((n&1)?0xedb88320:0)}return(n^0xffffffff)>>>0};const chunk=(name,data)=>{const size=Buffer.alloc(4),sum=Buffer.alloc(4),kind=Buffer.from(name);size.writeUInt32BE(data.length);sum.writeUInt32BE(crc(Buffer.concat([kind,data])));return Buffer.concat([size,kind,data,sum])};const h=Buffer.alloc(13);h.writeUInt32BE(32,0);h.writeUInt32BE(32,4);h[8]=8;h[9]=6;const pixels=Buffer.alloc(32*129);for(let y=0;y<32;y++)for(let x=0;x<32;x++)Buffer.from([30,130,180,255]).copy(pixels,y*129+1+x*4);return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',h),chunk('IDAT',deflateSync(pixels)),chunk('IEND',Buffer.alloc(0))])}
(async()=>{
 const engine=id(1),account=id(2),chat=id(3),revision=id(4),bytes=png(),hash=createHash('sha256').update(bytes).digest('hex'),streams=new Set();
 const state={ensures:0,registrations:0,polls:0,media:0,seq:0,ready:false};
 let acknowledgeRegistration;
 const kinds=['image','image','video','document','audio','link','document'];
 const items=kinds.map((kind,n)=>{
  const ext=kind==='image'?'.png':kind==='video'?'.webm':kind==='audio'?'.mp3':kind==='document'?n===6?'.docx':'.txt':'';
  const url='https://files.example/item'+n+ext+'?source=chat&encoded=%F0%9F%91%8B';
  const message={id:id(100+n),revisionId:revision,accountId:account,conversationId:chat,originalText:url,sender:{id:id(9),displayName:'Bob'},createdAt:new Date(Date.UTC(2026,10,8,12,60-n)).toISOString(),isOwn:false,sendState:'received'};
  const item={id:id(200+n),revisionId:revision,kind,url,fileName:'item'+n+ext,accountLabel:'Personal',conversationTitle:'Fixture chat',message};
  if(kind==='image')item.preview={id:id(300+n),revisionId:revision,accountId:account,conversationId:chat,messageId:message.id,url,kind:'image',state:'ready',downloadState:n===0?'ready':'not_downloaded',fileHash:n===0?hash:undefined,imageHash:hash,updatedAt:'2026-11-08T12:00:00Z'};
  return item;
 });
 items[1].message=items[0].message;
 items[1].preview.messageId=items[0].message.id;
 const messages=[...new Map(items.map(item=>[item.message.id,item.message])).values()];
 const stars=new Map([[items[2].message.id,{starred:true,revisionId:id(405)}]]);
 const starFor=messageId=>({messageId,accountId:account,conversationId:chat,starred:false,...stars.get(messageId)});
 const emit=(event='message.preview_changed',data={})=>{
  state.seq++;
  for(const stream of streams)stream.write(`id: ${engine}:${state.seq}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
 };
 const starWrites=[];
 const conversation={id:chat,revisionId:revision,accountId:account,kind:'direct',address:'bob@fixture',title:'Fixture chat',lastMessage:'Fixture',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false};
 const api=http.createServer(async (req,res)=>{
  const u=new URL(req.url,'http://fixture'),send=(status,value)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))};
  if(req.method==='POST'&&u.pathname===`/api/v1/previews/${items[0].preview.id}/download`){
   assert.match(req.headers['x-oie-download-device'],/^[0-9a-f-]{36}$/);
   state.registrations++;
   if(state.registrationPending)await state.registrationPending;
   return send(200,items[0].preview);
  }
  if(req.method==='POST'&&u.pathname===`/api/v1/previews/${items[1].preview.id}/download`){state.ensures++;items[1].preview.downloadState='queued';return send(202,items[1].preview)}
  const starSource=messages.find(message=>u.pathname===`/api/v1/messages/${message.id}/star`);
  if(starSource){
   const current=starFor(starSource.id);
   if(req.method==='GET')return send(200,current);
   if(req.method==='PUT'){
    let body='';for await(const chunk of req)body+=chunk;
    const intent=JSON.parse(body);starWrites.push({messageId:current.messageId,...intent});
    if((intent.expectedRevisionId??null)!==(current.revisionId??null))return send(409,{status:409,code:'state_conflict'});
    stars.set(current.messageId,{starred:intent.starred,revisionId:id(410+starWrites.length)});
    emit('message.star_changed',{star:starFor(current.messageId)});
    return send(200,starFor(current.messageId));
   }
  }
  if(req.method!=='GET')return send(405,{status:405,code:'method_not_allowed'});
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.15.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,label:'Personal',address:'alice@fixture',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}]});
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:7,conversations:1});
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/message-stars'))return send(200,{items:[]});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:[conversation]});if(u.pathname===`/api/v1/conversations/${chat}`)return send(200,conversation);
  if(u.pathname.endsWith('/source-context'))return send(200,{
   engine:{id:engine,role:'hosted',apiVersion:'0.32.0',eventCursor:engine+':'+state.seq},conversation,
   context:{items:messages,targetId:items[0].message.id,hasOlder:false,hasNewer:false},
   previews:items.filter(i=>i.preview).map(i=>i.preview),videos:items.filter(i=>i.kind==='video'),
  });
  if(u.pathname.endsWith('/messages')||u.pathname.endsWith('/context'))return send(200,{items:messages,targetId:items[0].message.id,hasOlder:false,hasNewer:false});
  const message=items.find(i=>u.pathname==='/api/v1/messages/'+i.message.id);if(message)return send(200,message.message);
  if(u.pathname.endsWith('/previews'))return send(200,{items:items.filter(i=>i.preview).map(i=>i.preview)});
  if(u.pathname==='/api/v1/shared-content'){
   const type=u.searchParams.get('type'),includeStars=u.searchParams.get('includeMessageStars')==='true';
   const page=items.filter(i=>type==='links'?i.kind==='link':type==='documents'?i.kind==='document':['image','video','audio'].includes(i.kind));
   page.sort((a,b)=>b.message.createdAt.localeCompare(a.message.createdAt)||b.id.localeCompare(a.id));
   return send(200,{items:page.map(item=>({...item,star:includeStars?starFor(item.message.id):undefined})),indexing:{state:'complete',indexedMessages:7,totalMessages:7}});
  }
  if(u.pathname===`/api/v1/previews/${items[1].preview.id}`){state.polls++;if(state.ready){items[1].preview.downloadState='ready';items[1].preview.fileHash=hash;}return send(200,items[1].preview)}
  if(u.pathname===`/api/v1/media/${hash}`){state.media++;res.setHeader('Content-Type','image/png');return res.end(bytes)}
  const file=items.find(i=>u.pathname===`/api/v1/shared-content/${i.id}/content`);
  if(file){res.setHeader('Content-Type',file.kind==='video'?'video/webm':'text/plain; charset=utf-8');res.setHeader('Content-Disposition',`inline; filename="${file.fileName}"`);return res.end(file.kind==='video'?Buffer.from([26,69,223,163,1,2,3,4]):Buffer.from('Document fixture\n'))}
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);req.on('close',()=>streams.delete(res));return}
  return send(404,{status:404,title:'Not found',code:'not_found'});
 });api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:'ignore'}),base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true});
 const until=async(fn,label)=>{for(let n=0;n<200;n++){if(await fn())return;await new Promise(r=>setTimeout(r,40))}throw Error(label)};
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF ready');
  const c=await browser.newContext({viewport:{width:1440,height:1000},locale:'en',reducedMotion:'reduce'}),page=await c.newPage(),errors=[],documents=[];
  await c.addInitScript(()=>{localStorage.setItem('oie.device.auto-image-downloads.v1','false');window.copied=[];Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>window.copied.push(value)}});window.documentToken=Math.random()});
  await c.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(r.resourceType()==='document')documents.push(r.url())});
  await page.goto(base+'/?section=media&lang=en&theme=dark');await page.waitForFunction(()=>window.OieRuntime?.generation>0);
  assert.equal(await page.locator('.oie-shared-source a').count(),0);
  const card=n=>page.locator(`[data-shared-item="${items[n].id}"]`),menu=n=>card(n).locator('[role=menu]'),open=async n=>{await card(n).hover();await card(n).locator('[data-shared-menu-trigger]').click();await menu(n).getByRole('menuitem',{name:'Copy link',exact:true}).waitFor({state:'visible'})};
  const star=n=>card(n).locator('[data-shared-star]');
  const opacity=locator=>locator.evaluate(node=>getComputedStyle(node).opacity);
  const pressed=(locator,value)=>until(async()=>await locator.getAttribute('aria-pressed')===String(value),'star state');
  assert.equal(await opacity(star(0)),'0');
  assert.equal(await opacity(star(2)),'1');
  await pressed(star(2),true);
  await card(0).hover();
  assert.equal(await opacity(star(0)),'1');
  assert.equal(await opacity(star(1)),'0');
  await star(0).hover();
  const positions=await card(0).evaluate(node=>{
   const card=node.getBoundingClientRect();
   const star=node.querySelector('[data-shared-star]').getBoundingClientRect();
   const menu=node.querySelector('[data-shared-menu-trigger]').getBoundingClientRect();
   const center=document.elementFromPoint(card.x+card.width/2,star.y+star.height/2);
   return {left:star.left-card.left,right:card.right-menu.right,actionsCoverCenter:!!center?.closest('[data-shared-actions]')};
  });
  assert(positions.left>0&&positions.left<=8,'the star sits at the left edge');
  assert(positions.right>0&&positions.right<=8,'the menu sits at the right edge');
  assert.equal(positions.actionsCoverCenter,false);
  for(const action of [star(0),star(2)]){
   const surface=await action.evaluate(node=>{
    const {backgroundColor,borderTopWidth,boxShadow}=getComputedStyle(node);
    return {backgroundColor,borderTopWidth,boxShadow};
   });
   assert.deepEqual(surface,{backgroundColor:'rgba(0, 0, 0, 0)',borderTopWidth:'0px',boxShadow:'none'});
  }
  assert.notEqual(await star(0).evaluate(node=>getComputedStyle(node).color),await star(2).evaluate(node=>getComputedStyle(node).color));
  await page.mouse.move(1,1);
  await star(0).focus();
  assert.equal(await opacity(star(0)),'1');

  let finishStar;
  const starGate=new Promise(resolve=>{finishStar=resolve});
  await page.route('**/ui/messages/star',async route=>{await starGate;await route.continue()},{times:1});
  const firstStarRect=await card(0).boundingBox(),secondRect=await card(1).boundingBox();
  const request=page.waitForRequest('**/ui/messages/star');
  await star(0).focus();
  await page.keyboard.press('Space');
  await request;
  await until(async()=>await star(1).getAttribute('aria-disabled')==='true','same source disabled while saving');
  const drift=await page.evaluate(async ({ids,initial})=>{
   let max=0;
   for(let frame=0;frame<12;frame++){
    await new Promise(requestAnimationFrame);
    for(const [index,id] of ids.entries()){
     const rect=document.querySelector(`[data-shared-item="${id}"]`).getBoundingClientRect();
     for(const key of ['x','y','width','height'])max=Math.max(max,Math.abs(rect[key]-initial[index][key]));
    }
   }
   return max;
  },{ids:[items[0].id,items[1].id],initial:[firstStarRect,secondRect]});
  assert(drift<=1,'star request keeps both card rectangles in place');
  assert.equal(starWrites.length,0);
  finishStar();
  await pressed(star(0),true);
  await pressed(star(1),true);
  assert.equal(await star(0).evaluate(node=>document.activeElement===node),true);
  assert.deepEqual(starWrites,[{messageId:items[0].message.id,expectedRevisionId:null,starred:true}]);

  // A stale revision recovers to the current star and permits another click.
  stars.set(items[0].message.id,{starred:true,revisionId:id(419)});
  await star(0).click();
  await until(async()=>starWrites.length===2&&await star(0).getAttribute('aria-disabled')==='false','conflict recovered');
  await pressed(star(0),true);
  await star(0).click();
  await pressed(star(0),false);
  await pressed(star(1),false);
  assert.equal(starWrites[2].expectedRevisionId,id(419));
  assert.equal(starWrites[2].starred,false);
  await page.locator('[data-shared-tab=links]').click();
  await pressed(star(5),false);
  await card(5).hover();
  await star(5).click();
  await pressed(star(5),true);
  await page.locator('[data-shared-tab=media]').click();
  await pressed(star(0),false);

  const touch=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,locale:'pt-BR'});
  await touch.addInitScript(()=>localStorage.setItem("oie.device.auto-image-downloads.v1","false"));
  try{
   await touch.route('**/*',r=>new URL(r.request().url()).origin===base?r.continue():r.abort());
   const mobile=await touch.newPage();
   await mobile.goto(base+'/?section=media&lang=pt-BR&theme=light');
   await mobile.waitForFunction(()=>window.OieRuntime?.generation>0);
   const action=mobile.locator(`[data-shared-item="${items[0].id}"] [data-shared-star]`);
   assert.equal(await action.getAttribute('aria-label'),'Favoritar');
   assert.equal(await opacity(action),'1');
   const leftInset=await action.evaluate(node=>node.getBoundingClientRect().left-node.closest('[data-shared-item]').getBoundingClientRect().left);
   assert(leftInset>0&&leftInset<=8,'the touch action sits at the left edge');
   await action.tap();
   await pressed(action,true);
   await action.tap();
   await pressed(action,false);
   await mobile.evaluate(()=>Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{window.lastCopied=value}}}));
   const tile=mobile.locator(`[data-shared-item="${items[0].id}"]`);
   await tile.locator('[data-shared-menu-trigger]').tap();
   await tile.getByRole('menuitem',{name:'Copiar link',exact:true}).tap();
   const toast=mobile.getByRole('alert').filter({hasText:'Link copiado.'});
   await toast.getByRole('button',{name:'Fechar'}).tap();
   await toast.waitFor({state:'detached'});
   assert.equal(await mobile.evaluate(()=>window.lastCopied),items[0].url);
  }finally{await touch.close()}

  const copyLink=async n=>{
   await open(n);
   await menu(n).getByRole('menuitem',{name:'Copy link',exact:true}).click();
  };
  const copiedToast=page.locator('[data-toast-container]').getByRole('alert').filter({hasText:'Link copied.'});
  const before=state.media, beforeCopy=await card(0).boundingBox();
  await page.evaluate(()=>{
   const clipboard=navigator.clipboard,write=clipboard.writeText;
   clipboard.writeText=value=>new Promise(resolve=>{
    window.finishCopy=()=>{clipboard.writeText=write;resolve(write(value));};
   });
  });
  await copyLink(0);
  assert.equal(await copiedToast.count(),0);
  assert.deepEqual(await card(0).boundingBox(),beforeCopy);
  await page.evaluate(()=>window.finishCopy());
  await copiedToast.waitFor();
  assert.equal(await page.evaluate(()=>copied[0]),items[0].url);
  assert.equal(state.media,before);
  assert.equal(state.ensures,0);
  await until(()=>card(0).locator('[data-shared-menu-trigger]').evaluate(n=>n===document.activeElement),'menu focus restored');
  assert.deepEqual(await card(0).boundingBox(),beforeCopy);
  await copyLink(1);
  await copiedToast.waitFor();
  assert.equal(await copiedToast.count(),1);
  assert.deepEqual(await page.evaluate(()=>copied),[items[0].url,items[1].url]);
  await copiedToast.waitFor({state:'detached',timeout:6000});
  await copyLink(0);
  await copiedToast.getByRole('button',{name:'Close'}).click();
  await copiedToast.waitFor({state:'detached'});
  await page.evaluate(()=>{
   const clipboard=navigator.clipboard,write=clipboard.writeText;
   clipboard.writeText=async()=>{clipboard.writeText=write;throw Error('clipboard denied');};
  });
  await copyLink(1);
  const failedToast=page.getByRole('alert').filter({hasText:'This action could not be completed.'});
  await failedToast.getByRole('button',{name:'Close'}).click();
  await failedToast.waitFor({state:'detached'});
  await open(0);
  state.registrationPending=new Promise(resolve=>{acknowledgeRegistration=resolve});
  const saved=page.waitForEvent('download'),beforeSave=state.media;
  await menu(0).getByRole('menuitem',{name:'Download',exact:true}).click();
  await until(()=>state.registrations===1,'ready image registration');
  assert.equal(state.media,beforeSave,'saving waits until the device registration succeeds');
  acknowledgeRegistration();state.registrationPending=null;
  const download=await saved;
  assert.deepEqual(await fs.readFile(await download.path()),bytes);assert.equal(state.ensures,0);
  await open(1);const queued=page.waitForEvent('download');await menu(1).getByRole('menuitem',{name:'Download',exact:true}).click();await until(()=>state.ensures===1&&state.polls>0,'queued image polling');state.ready=true;const original=await queued;assert.deepEqual(await fs.readFile(await original.path()),bytes);assert.equal(state.ensures,1);
  await open(4);assert.equal(await menu(4).getByRole('menuitem',{name:'Download',exact:true}).count(),0);await page.keyboard.press('Escape');
  await open(2);const video=page.waitForEvent('download');await menu(2).getByRole('menuitem',{name:'Download',exact:true}).click();assert.equal((await video).suggestedFilename(),items[2].fileName);
  await page.locator('[data-shared-tab=documents]').click();await card(3).waitFor();await pressed(star(3),false);await open(3);const doc=page.waitForEvent('download');await menu(3).getByRole('menuitem',{name:'Download',exact:true}).click();assert.equal(await fs.readFile(await (await doc).path(),'utf8'),'Document fixture\n');
  await open(6);assert.equal(await menu(6).getByRole('menuitem',{name:'Download',exact:true}).count(),0);await page.keyboard.press('Escape');
  // Native file selection is asynchronous. Cancel must stay quiet, a failed
  // save must be reported, and neither case may fall back to an anchor download.
  await page.evaluate(()=>{window.nativeSaves=[];window.OieDesktop={saveFile:(url,name)=>{nativeSaves.push({url,name});return new Promise((resolve,reject)=>{window.finishNativeSave=resolve;window.failNativeSave=reject;});}};});
  let nativeAnchorDownloads=0;const nativeDownload=()=>nativeAnchorDownloads++;page.on('download',nativeDownload);
  await open(3);await menu(3).getByRole('menuitem',{name:'Download',exact:true}).click();await until(async()=>await page.evaluate(()=>nativeSaves.length)===1,'native document save requested');
  assert.equal((await page.evaluate(()=>nativeSaves[0])).url,`/ui/shared-content/${items[3].id}/content?engineId=${engine}&download=1`);
  await page.evaluate(()=>finishNativeSave(false));await until(async()=>await card(3).locator('[data-shared-action-status]').isHidden(),'cancelled document save stays quiet');
  await open(3);await menu(3).getByRole('menuitem',{name:'Download',exact:true}).click();await until(async()=>await page.evaluate(()=>nativeSaves.length)===2,'second native document save');
  await page.evaluate(()=>failNativeSave(Error('native_write_failed')));await until(async()=>await card(3).locator('[data-shared-action-status]').isVisible(),'failed document save feedback');
  await page.locator('[data-shared-tab=media]').click();await card(0).waitFor();await open(0);await menu(0).getByRole('menuitem',{name:'Download',exact:true}).click();await until(async()=>await page.evaluate(()=>nativeSaves.length)===3,'native cached image save requested');
  assert.equal((await page.evaluate(()=>nativeSaves[2])).url,`/ui/media/${hash}?download=${encodeURIComponent(items[0].fileName)}`);
  await page.evaluate(()=>finishNativeSave(false));await until(async()=>await card(0).locator('[data-shared-action-status]').isHidden(),'cancelled image save stays quiet');
  assert.equal(nativeAnchorDownloads,0);page.off('download',nativeDownload);await page.evaluate(()=>{delete window.OieDesktop;});
  await page.locator('[data-shared-tab=media]').click();await card(0).waitFor();await open(0);
  // A failed foreground jump must leave the catalog in place and resume live refreshes.
  const refusedURL=url=>url.searchParams.get('message')===items[0].message.id&&url.pathname==='/';
  let finishRefusal;
  const refusalGate=new Promise(resolve=>{finishRefusal=resolve});
  const refuseJump=async route=>{await refusalGate;await route.fulfill({status:503,body:''});};
  await page.route(refusedURL,refuseJump,{times:1});
  const refusalStarted=page.waitForRequest(r=>refusedURL(new URL(r.url())));
  const catalogRect=()=>card(0).locator('[data-shared-menu-trigger]').boundingBox();
  const rect=await catalogRect();
  const sourceMenu=()=>menu(0).getByRole('menuitem',{name:'Go to message',exact:true});
  await sourceMenu().click();await refusalStarted;
  const refreshesDuringRefusal=[];
  const watchRefresh=r=>{if(new URL(r.url()).pathname==='/ui/refresh')refreshesDuringRefusal.push(r.url());};
  page.on('request',watchRefresh);
  await until(()=>streams.size===1,'SSE stays connected while the source request is pending');
  emit();
  const movement=await page.evaluate(async ({ref,rect})=>{
   const trigger=document.querySelector('[data-shared-menu-trigger="'+ref+'"]');
   let max=0;const until=performance.now()+320;
   while(performance.now()<until){
    await new Promise(requestAnimationFrame);
    const next=trigger.getBoundingClientRect();
    for(const key of ['x','y','width','height'])max=Math.max(max,Math.abs(next[key]-rect[key]));
   }
   return max;
  },{ref:items[0].id,rect});
  assert(movement<=1,'pending source keeps the catalog geometry');
  assert.equal(refreshesDuringRefusal.length,0,'foreground source defers the HTML refresh');
  const resumed=page.waitForResponse(r=>new URL(r.url()).pathname==='/ui/refresh');
  finishRefusal();await resumed;
  page.off('request',watchRefresh);
  assert.equal(new URL(page.url()).searchParams.get('section'),'media');
  if(!await sourceMenu().isVisible())await open(0);
  // An invalid success envelope must not commit history or replace the catalog.
  await page.route(refusedURL,route=>route.fulfill({status:200,contentType:'text/html',body:'<div id="oie-app" data-source-route><div id="oie-route"></div></div>'}),{times:1});
  const malformed=page.waitForResponse(r=>refusedURL(new URL(r.url())));
  await page.evaluate(()=>{window.sourceFinally=false;document.addEventListener('htmx:finally:request',function finish(event){
   if(event.detail.ctx?.request.headers['X-Oie-Fragment']!=='source')return;
   document.removeEventListener('htmx:finally:request',finish);window.sourceFinally=true;
  });});
  const resumedAfterMalformed=page.waitForResponse(r=>new URL(r.url()).pathname==='/ui/refresh');
  await sourceMenu().click();await malformed;await resumedAfterMalformed;
  await page.waitForFunction(()=>window.sourceFinally);
  assert.equal(new URL(page.url()).searchParams.get('section'),'media');
  if(!await sourceMenu().isVisible())await open(0);
  // A snapshot announces shared content before synchronizing message-menu slots.
  // Capture the surviving node only after that synchronization completes.
  const watchSnapshot=async()=>page.evaluate(({engine,account,chat,source})=>{
   const controller=new AbortController(),state={previousGeneration:window.OieRuntime.generation,routeGeneration:null,swapGeneration:null,snapshotGeneration:null,slot:null};window.catalogActionSettlement=state;
   const current=()=>document.body.dataset.engineId===engine&&document.body.dataset.accountId===account&&document.body.dataset.conversationId===chat;
   document.addEventListener('oie:route-ready',event=>{if(current())state.routeGeneration=event.detail.generation},{signal:controller.signal});
   document.addEventListener('htmx:after:settle',event=>{const target=event.detail.task?.target;if(current()&&target?.id==='oie-route'&&target===document.getElementById('oie-route'))state.swapGeneration=window.OieRuntime.generation},{signal:controller.signal});
   document.addEventListener('oie:shared-refresh',event=>{const regions=event.detail?.querySelector('#oie-live-regions');if(!current()||regions?.dataset.engineId!==engine||regions.dataset.accountId!==account||regions.dataset.conversationId!==chat)return;state.snapshotGeneration=window.OieRuntime.generation},{signal:controller.signal});
   document.addEventListener('oie:actions-refreshed',()=>{if(current()&&state.snapshotGeneration===window.OieRuntime.generation){state.slot=document.querySelector('[data-message-menu-slot="'+source+'"]');controller.abort()}},{signal:controller.signal});
  },{engine,account,chat,source:items[0].message.id});
  const snapshotApplied=()=>page.evaluate(()=>{const s=window.catalogActionSettlement;return s.snapshotGeneration===window.OieRuntime.generation&&s.slot?.isConnected});
  const shell=await page.locator('#oie-app').elementHandle();
  const settings=await page.locator('[data-route-settings]').elementHandle();
  const viewers=await page.locator('[data-route-viewers]').elementHandle();
  await page.evaluate(source=>{
   window.sourceFirstFrame=null;
   const frame=()=>{
    const timeline=document.getElementById('message-timeline');
    if(!timeline){requestAnimationFrame(frame);return;}
    const target=timeline.querySelector(`[data-message-id="${CSS.escape(source)}"]`);
    const row=target?.getBoundingClientRect(),viewport=timeline.getBoundingClientRect();
    window.sourceFirstFrame={focused:document.activeElement===target,visible:!!row&&row.bottom>viewport.top&&row.top<viewport.bottom};
   };
   requestAnimationFrame(frame);
  },items[0].message.id);
  await watchSnapshot();
  const sourceAction=menu(0).getByRole('menuitem',{name:'Go to message',exact:true});
  assert.equal(await sourceAction.locator('use[href$="#heroicons-navigation-16-solid-arrow-turn-right-down"]').count(),1);
  const jumpResponse=page.waitForResponse(r=>r.request().headers()['x-oie-fragment']==='source');
  await sourceAction.click();await page.waitForFunction(()=>document.querySelector('#message-timeline'));assert.equal(documents.length,1);assert.equal(new URL(page.url()).searchParams.get('message'),items[0].message.id);
  const response=await jumpResponse;
  assert.equal(await response.headerValue('content-encoding'),'gzip');
  assert(!(await response.text()).includes('data-route-settings'));
  await page.waitForFunction(()=>window.sourceFirstFrame);
  assert.deepEqual(await page.evaluate(()=>window.sourceFirstFrame),{focused:true,visible:true});
  await until(()=>streams.size===1,'live stream retained across source navigation');
  emit();
  // Network completion is earlier than application consumption. Wait for the
  // actual source-route settlement and its scoped SSE snapshot to be applied.
  await until(async()=>await snapshotApplied()&&await page.evaluate(()=>{const s=window.catalogActionSettlement,generation=window.OieRuntime.generation;return s.routeGeneration===generation&&s.swapGeneration===generation&&generation===s.previousGeneration+1&&document.querySelector('#message-timeline')?.dataset.timelineBusy==='false'}),'source route and initial live snapshot applied');
  assert(await shell.evaluate(node=>node===document.getElementById('oie-app')),'source keeps the app shell mounted');
  assert(await settings.evaluate(node=>node===document.querySelector('[data-route-settings]')),'source keeps the mounted settings');
  assert(await viewers.evaluate(node=>node===document.querySelector('[data-route-viewers]')),'source keeps the mounted viewers');
  await shell.dispose();
  await settings.dispose();
  await viewers.dispose();
  assert(await page.evaluate(()=>window.catalogActionSettlement.slot.isConnected),'menu measurement starts from the applied live DOM');
  const deferredMenu=page.locator(`[data-message-menu-slot="${items[0].message.id}"]`);
  assert.equal(await deferredMenu.locator('[role=menu]').count(),0);
  const deferredTrigger=deferredMenu.locator('[data-message-menu-trigger]');
  await deferredTrigger.focus();await page.keyboard.press('ArrowDown');
  await deferredMenu.getByRole('menu').waitFor({state:'visible'});
  await page.keyboard.press('Escape');
  await deferredMenu.locator('[role=menu]').waitFor({state:'detached'});
  await until(async()=>await deferredTrigger.evaluate(el=>el===document.activeElement),'first keyboard open restores focus on Escape');
  // Exercise the actual message action menu with the consumer theme overrides.
  const painted=el=>{
   const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d');
   ctx.fillStyle=getComputedStyle(el.closest('[role=menu]')).backgroundColor;ctx.fillRect(0,0,1,1);
   ctx.fillStyle=getComputedStyle(el).backgroundColor;ctx.fillRect(0,0,1,1);
   return Array.from(ctx.getImageData(0,0,1,1).data).slice(0,3);
  };
  for(const width of [390,1440])for(const mode of ['light','dark']){
   await page.setViewportSize({width,height:1000});
   await page.evaluate(mode=>document.documentElement.classList.toggle('dark',mode==='dark'),mode);
   const trigger=page.locator(`[data-message-menu-trigger="${items[0].message.id}"]`),copy=page.locator('#copy-message-'+items[0].message.id);
   await trigger.locator('xpath=ancestor::*[@data-message-id][1]').hover();await trigger.click();await copy.waitFor({state:'visible'});
   await until(()=>streams.size===1,'live event stream connected');await watchSnapshot();emit();
   await until(snapshotApplied,'live event snapshot applied while the menu is open');await copy.waitFor({state:'visible'});await until(async()=>await copy.evaluate(el=>getComputedStyle(el).cursor==='pointer'),'message menu pointer cursor');
   await page.mouse.move(0,0);const normal=await copy.evaluate(painted);await copy.hover();const hover=await copy.evaluate(painted);
   const cursorState=await copy.evaluate(el=>({cursor:getComputedStyle(el).cursor,connected:el.isConnected,root:el.getRootNode().nodeName,parent:el.parentElement?.outerHTML.slice(0,150),className:el.className}));assert.equal(cursorState.cursor,'pointer',JSON.stringify({width,mode,...cursorState}));
   assert(normal.reduce((sum,v,i)=>sum+Math.abs(v-hover[i]),0)>20,`${width}/${mode}: visible painted hover`);
   await page.mouse.move(0,0);await trigger.focus();await page.keyboard.press('Tab');
   assert.equal(await copy.evaluate(el=>document.activeElement===el&&el.matches(':focus-visible')),true);
   const focus=await copy.evaluate(painted);assert(normal.reduce((sum,v,i)=>sum+Math.abs(v-focus[i]),0)>20,`${width}/${mode}: visible keyboard focus`);
   await page.keyboard.press('Escape');await copy.waitFor({state:'hidden'});await until(async()=>await trigger.evaluate(el=>el===document.activeElement),'message menu restores trigger focus');
  }
  assert.deepEqual(errors,[]);await c.close();
  for(const locale of ['en','pt-BR']){const m=await browser.newContext({viewport:{width:390,height:844},locale,hasTouch:true,reducedMotion:'reduce'});await m.addInitScript(()=>localStorage.setItem('oie.device.auto-image-downloads.v1','false'));const p=await m.newPage();p.on('pageerror',e=>errors.push(e.message));await p.goto(base+'/?section=media&lang='+locale);await p.waitForFunction(()=>window.OieRuntime?.generation>0);const first=p.locator('[data-shared-item]').first();await first.locator('[data-shared-menu-trigger]').tap();await first.getByRole('menuitem',{name:locale==='en'?'Copy link':'Copiar link',exact:true}).waitFor({state:'visible'});await until(async()=>{const b=await first.locator('[role=menu]').boundingBox();return b&&b.x>=0&&b.x+b.width<=390},'mobile menu placement settles inside viewport');await p.addScriptTag({path:require.resolve('./fixtures/axe-core/axe.min.js')});const axe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>v.id),[]);
   await first.getByRole('menuitem',{name:locale==='en'?'Go to message':'Ir para a mensagem',exact:true}).tap();
   const focused=p.locator(`#message-timeline [data-message-id="${items[0].message.id}"]:focus`);
   await focused.waitFor({state:'visible'});
   await p.goBack();await p.locator('#shared-content-results').waitFor({state:'visible'});
   await p.goForward();await focused.waitFor({state:'visible'});
   await m.close();
  }
  assert.deepEqual(errors,[]);console.log('PASS: catalog stars and chevron, exact URL copy, cached/queued image downloads, video/document save, supported actions, fragment source jump and EN/PT touch accessibility');
 }finally{await browser.close();child.kill('SIGTERM');for(const s of streams)s.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
