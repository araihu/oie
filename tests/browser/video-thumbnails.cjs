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
 const message={id:messageID,revisionId:revision,accountId:account,conversationId:chat,sender:{id:id(9),displayName:'Bob'},originalText:'Video source https://files.example.test/clip.webm',createdAt:'2026-11-08T12:00:00Z',isOwn:false,sendState:'received'};
 const conversation={id:chat,revisionId:revision,accountId:account,kind:'direct',address:'bob@fixture',title:'Bob',lastMessage:'Video source',updatedAt:message.createdAt,unreadCount:0,archived:false,favorite:false};
 const state={status:'not_requested',ensures:0,media:0,posters:0,fail:false,seq:0};
 const streams=new Set(),errors=[];
 const preview=()=>({id:previewID,revisionId:id(50+state.seq),sharedContentId:itemID,accountId:account,conversationId:chat,messageId:messageID,url:'https://files.example.test/clip.webm',kind:'video',state:state.status,updatedAt:message.createdAt,...(state.status==='ready'?{imageHash:hash}:{})});
 const item=()=>({id:itemID,revisionId:revision,kind:'video',url:preview().url,fileName:'Preview sample.webm',accountLabel:'Fixture account',conversationTitle:'Bob',message,thumbnail:preview()});
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
  if(url.pathname==='/api/v1/shared-content')return send(200,{items:[item()],indexing:{state:'complete',indexedMessages:1,totalMessages:1}});
  if(url.pathname==='/api/v1/shared-content/'+itemID+'/thumbnail'){
   if(req.method==='POST'){state.ensures++;state.seq++;state.status='queued';send(202,preview());setTimeout(()=>{state.status=state.fail?'unavailable':'ready';state.seq++},100);return}
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
  assert.equal(state.ensures,1);assert.equal(state.media,0);assert.equal(await card.locator('video').getAttribute('src'),null);assert((await card.locator('video').getAttribute('poster')).endsWith('/ui/media/'+hash));
  await page.screenshot({path:path.join(__dirname,'artifacts/video-thumbnail.png')});
  await page.reload();await until(()=>card.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64),'poster reused after reload');assert.equal(state.ensures,1);assert.equal(state.media,0);
  await card.locator('[data-video-play]').click();await until(()=>card.locator('video').evaluate(video=>video.videoWidth===64&&video.currentTime>0.05),'explicit native playback');assert(state.media>0);
  await context.close();
  state.status='not_requested';state.ensures=0;state.media=0;
  const manual=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR'});
  await manual.addInitScript(()=>localStorage.setItem('oie.device.auto-video-previews.v1','false'));
  const phone=await manual.newPage();phone.on('pageerror',error=>errors.push(error.message));await phone.goto(base+'/?section=media&lang=pt-BR&theme=light');await sleep(400);assert.equal(state.ensures,0);assert.equal(state.media,0);
  const mobileCard=phone.locator('[data-video-item="'+itemID+'"]');assert.equal((await mobileCard.locator('[data-video-thumbnail-load]').textContent()).trim(),'Carregar prévia do vídeo');
  await mobileCard.locator('[data-video-thumbnail-load]').focus();await phone.keyboard.press('Enter');await until(()=>mobileCard.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64),'manual keyboard preview');assert.equal(state.ensures,1);assert.equal(state.media,0);
  await phone.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await phone.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>v.id),[]);
  await manual.close();
  state.status='unavailable';state.ensures=0;
  const failed=await browser.newContext({viewport:{width:1100,height:850}}),failurePage=await failed.newPage();await failurePage.goto(base+'/?section=media&lang=en');await sleep(400);assert.equal(state.ensures,0,'failed previews must not retry just because a route opens');
  await failurePage.locator('[data-video-thumbnail-load]').click();await until(()=>failurePage.locator('[data-video-poster-image]').evaluate(image=>image.naturalWidth===64),'explicit failed-preview retry');assert.equal(state.ensures,1);await failed.close();
  assert.deepEqual(errors,[]);console.log('PASS: demand-bounded posters, cached reload, explicit playback, per-device opt-out/manual keyboard load, failed-preview retry and mobile accessibility');
 }catch(error){console.error(log);throw error}finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
