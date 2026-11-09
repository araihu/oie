// Isolated thumbnail API/BFF fixtures. Only the local sample media is read.
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const path = require('node:path');
const {createHash} = require('node:crypto');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require('playwright');
const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(fn, label) { for(let n=0;n<180;n++){if(await fn())return;await sleep(50)}throw Error('Timed out: '+label) }
(async()=>{
 const png=await fs.readFile(path.join(__dirname,'fixtures/video-poster.png'));
 const video=await fs.readFile(path.join(__dirname,'fixtures/tiny-video.webm'));
 const hash=createHash('sha256').update(png).digest('hex');
 const engine=id(1),account=id(2),chat=id(3),revision=id(4),itemID=id(20),messageID=id(30),previewID=id(40);
 const message={id:messageID,revisionId:revision,accountId:account,conversationId:chat,sender:{id:id(9),displayName:'Bob'},originalText:'Video source https://files.example.test/clip.webm https://files.example.test/photo.png',createdAt:'2026-11-08T12:00:00Z',isOwn:false,sendState:'received'};
 const conversation={id:chat,revisionId:revision,accountId:account,kind:'direct',address:'bob@fixture',title:'Bob',lastMessage:'Video source',updatedAt:message.createdAt,unreadCount:0,archived:false,favorite:false};
 const state={status:'not_requested',ensures:0,media:0,posters:0,fail:false,seq:0,holdThumbnail:false,releaseThumbnail:null};
 const streams=new Set(),errors=[];
 const preview=()=>({id:previewID,revisionId:id(50+state.seq),sharedContentId:itemID,accountId:account,conversationId:chat,messageId:messageID,url:'https://files.example.test/clip.webm',kind:'video',state:state.status,updatedAt:message.createdAt,...(state.status==='ready'?{imageHash:hash}:{})});
 const item=()=>({id:itemID,revisionId:revision,kind:'video',url:preview().url,fileName:'Preview sample.webm',accountLabel:'Fixture account',conversationTitle:'Bob',message,thumbnail:preview()});
 const imageItem={id:id(21),revisionId:revision,kind:'image',url:'https://files.example.test/photo.png',fileName:'Photo.png',accountLabel:'Fixture account',conversationTitle:'Bob',message,preview:{id:id(41),revisionId:revision,accountId:account,conversationId:chat,messageId:messageID,url:'https://files.example.test/photo.png',kind:'image',state:'ready',downloadState:'ready',imageHash:hash,fileHash:hash,updatedAt:message.createdAt}};
 const api=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');
  const send=(status,body)=>{res.statusCode=status;res.end(JSON.stringify(body))};
  if(url.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.16.0',eventCursor:engine+':0'});
  if(url.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,protocol:'xmpp',label:'Fixture account',address:'alice@fixture',createdAt:message.createdAt,connectionState:'connected'}]});
  if(url.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:message.createdAt});
  if(url.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:1,conversations:1,updatedAt:message.createdAt});
  if(url.pathname.endsWith('/avatars')||url.pathname.endsWith('/previews')||url.pathname.endsWith('/message-stars'))return send(200,{items:[]});
  if(url.pathname==='/api/v1/conversations')return send(200,{items:[conversation]});
  if(url.pathname==='/api/v1/conversations/'+chat)return send(200,conversation);
  if(url.pathname.endsWith('/messages'))return send(200,{items:[message]});
  if(url.pathname==='/api/v1/shared-content')return send(200,{items:[item(),imageItem],indexing:{state:'complete',indexedMessages:1,totalMessages:1}});
  if(url.pathname==='/api/v1/shared-content/'+itemID+'/thumbnail'){
   if(req.method==='POST'){state.ensures++;state.seq++;state.status='queued';send(202,preview());const finish=()=>{state.status=state.fail?'unavailable':'ready';state.seq++};if(state.holdThumbnail)state.releaseThumbnail=finish;else setTimeout(finish,100);return}
   return send(200,preview());
  }
  if(url.pathname==='/api/v1/media/'+hash){state.posters++;res.setHeader('Content-Type','image/png');res.end(png);return}
  if(url.pathname==='/api/v1/shared-content/'+itemID+'/content'){
   state.media++;let start=0,end=video.length-1,code=200;const match=/^bytes=(\d+)-(\d*)$/.exec(req.headers.range||'');
   if(match){start=+match[1];if(match[2])end=Math.min(+match[2],end);code=206;res.setHeader('Content-Range',`bytes ${start}-${end}/${video.length}`)}
   res.setHeader('Content-Type','video/webm');res.setHeader('Accept-Ranges','bytes');res.setHeader('Content-Length',end-start+1);res.writeHead(code);res.end(video.subarray(start,end+1));return;
  }
  if(url.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(resolve=>allocation.close(resolve));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let log='';child.stderr.on('data',data=>log+=data);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true});
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF ready');
  const context=await browser.newContext({viewport:{width:1100,height:850},locale:'en'});
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==base){errors.push('external request');return route.abort()}return route.continue()});
  const page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));
  await page.goto(base+'/?section=media&lang=en&theme=dark');
  const card=page.locator('[data-video-item="'+itemID+'"]');
  await until(()=>card.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64&&!image.hidden),'cached poster displayed');
  assert.equal(await card.locator('[data-video-thumbnail-load]').isVisible(),false,'default previews must not need a Load action');assert.equal(state.ensures,1);assert.equal(state.media,0);assert.equal(await card.locator('video').getAttribute('src'),null);assert((await card.locator('video').getAttribute('poster')).endsWith('/ui/media/'+hash));
  await page.screenshot({path:path.join(__dirname,'artifacts/video-thumbnail.png')});
  await page.reload();await until(()=>card.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64),'poster reused after reload');assert.equal(state.ensures,1);assert.equal(state.media,0);
  for(const width of [390,1211,1440])for(const theme of ['light','dark']){
   await page.setViewportSize({width,height:900});await page.goto(base+'/?section=media&lang=en&theme='+theme);
   await until(()=>page.locator('.oie-shared-item .oie-preview-thumbnail').evaluate(image=>image.naturalWidth===64),'image preview ready');
   const sizes=await page.evaluate(()=>{const rect=selector=>{const r=document.querySelector(selector).getBoundingClientRect();return{width:r.width,height:r.height}};return{image:rect('.oie-shared-item .oie-image-preview'),video:rect('.oie-shared-item .oie-video-stage'),overflow:document.documentElement.scrollWidth>innerWidth}});
   assert(Math.abs(sizes.image.width-sizes.video.width)<1,JSON.stringify({width,theme,sizes}));assert(Math.abs(sizes.image.height-sizes.video.height)<1,JSON.stringify({width,theme,sizes}));assert(Math.abs(sizes.image.width/sizes.image.height-4/3)<.01,JSON.stringify(sizes));assert.equal(sizes.overflow,false);assert.equal(state.ensures,1,'cached catalog posters must not queue again');assert.equal(state.media,0,'uniform previews must not load the original video');
   const frame=await card.locator('.oie-video-stage').boundingBox();await card.locator('[data-video-play]').click();await until(()=>card.locator('video').evaluate(video=>video.videoWidth===64&&video.currentTime>0.05),'matrix native playback');const playing=await card.locator('.oie-video-stage').boundingBox();assert(Math.abs(frame.height-playing.height)<1,'playback must retain the gallery frame');state.media=0;
  }
  await page.setViewportSize({width:1100,height:850});await page.goto(base+'/?section=media&lang=en&theme=dark');
  await card.locator('[data-video-play]').click();await until(()=>card.locator('video').evaluate(video=>video.videoWidth===64&&video.currentTime>0.05),'explicit native playback');assert(state.media>0);
  await context.close();
  state.status='not_requested';state.ensures=0;state.media=0;
  const manual=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR'});
  await manual.addInitScript(()=>localStorage.setItem('oie.device.auto-video-previews.v1','false'));
  const phone=await manual.newPage();phone.on('pageerror',error=>errors.push(error.message));await phone.goto(base+'/?section=media&lang=pt-BR&theme=light');await sleep(400);assert.equal(state.ensures,0);assert.equal(state.media,0);
  const mobileCard=phone.locator('[data-video-item="'+itemID+'"]');assert.equal(await mobileCard.locator('[data-video-thumbnail-load]').count(),0,'gallery previews have no standalone retry control');const mobileTile=phone.locator('[data-shared-item="'+itemID+'"]');await mobileTile.locator('[data-shared-menu-trigger]').click();const manualAction=mobileTile.locator('[id^="shared-thumbnail-"]');assert.equal((await manualAction.textContent()).trim(),'Carregar prévia do vídeo');
  state.holdThumbnail=true;await manualAction.focus();await phone.keyboard.press('Enter');await until(()=>typeof state.releaseThumbnail==='function','manual thumbnail queued');await until(()=>manualAction.isDisabled(),'queued thumbnail action disabled');assert.equal(await mobileTile.locator('[data-shared-menu-trigger]').getAttribute('aria-expanded'),'false','thumbnail action closes its menu');assert.equal(typeof state.releaseThumbnail,'function');state.holdThumbnail=false;state.releaseThumbnail();state.releaseThumbnail=null;await until(()=>mobileCard.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64),'manual keyboard preview');await mobileTile.locator('[data-shared-menu-trigger]').click();assert.equal(await manualAction.isVisible(),false,'successful preview hides retry menu action');await phone.keyboard.press('Escape');await until(()=>mobileTile.locator('[role=menu]').isHidden(),'menu closed after checking cached preview');assert.equal(state.ensures,1);assert.equal(state.media,0);
  await phone.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await phone.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>({target:n.target,summary:n.failureSummary}))})),[]);
  await manual.close();
  state.status='unavailable';state.ensures=0;
  const failed=await browser.newContext({viewport:{width:1100,height:850}}),failurePage=await failed.newPage();await failurePage.goto(base+'/?section=media&lang=en');await sleep(400);assert.equal(state.ensures,0,'failed previews must not retry just because a route opens');assert.equal(await failurePage.locator('[data-video-thumbnail-load]').count(),0);const failedTile=failurePage.locator('[data-shared-item="'+itemID+'"]');await failedTile.locator('[data-shared-menu-trigger]').click();const retryAction=failedTile.locator('[id^="shared-thumbnail-"]');assert.equal((await retryAction.textContent()).trim(),'Try again');assert.match(await failurePage.locator('[data-video-thumbnail-status]').textContent(),/unavailable/);const failedStage=await failurePage.locator('.oie-video-stage').boundingBox(),failedImage=await failurePage.locator('.oie-image-preview').boundingBox();assert(Math.abs(failedStage.height-failedImage.height)<1,'failure must retain the common preview frame');
  state.fail=true;await retryAction.click();await until(async()=>state.ensures===1&&!await retryAction.isDisabled(),'failed retry becomes available again');assert.equal((await retryAction.textContent()).trim(),'Try again');await sleep(350);assert.equal(state.ensures,1,'failed retry must not loop automatically');state.fail=false;await failedTile.locator('[data-shared-menu-trigger]').click();await retryAction.click();await until(()=>failurePage.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64),'explicit failed-preview retry');assert.equal(state.ensures,2);await failed.close();
  assert.deepEqual(errors,[]);console.log('PASS: default automatic previews, uniform 4:3 image/video frames, stable playback, cache reuse, manual opt-out, failed-preview retry and mobile accessibility');
 }catch(error){console.error(log);throw error}finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
