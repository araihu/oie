// Independent canonical API fixture. No original/provider URLs are fetched.
const assert=require('node:assert/strict');
const http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {createHash}=require('node:crypto'),{deflateSync}=require('node:zlib');
const {spawn}=require('node:child_process'),{once}=require('node:events');
const {chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
function thumbnail(color,width=8,height=8){
 const crc=buffer=>{let value=0xffffffff;for(const b of buffer){value^=b;for(let n=0;n<8;n++)value=(value>>>1)^((value&1)?0xedb88320:0)}return(value^0xffffffff)>>>0};
 const chunk=(type,data)=>{const name=Buffer.from(type),size=Buffer.alloc(4),sum=Buffer.alloc(4);size.writeUInt32BE(data.length);sum.writeUInt32BE(crc(Buffer.concat([name,data])));return Buffer.concat([size,name,data,sum])};
 const header=Buffer.alloc(13);header.writeUInt32BE(width,0);header.writeUInt32BE(height,4);header[8]=8;header[9]=6;
 const stride=1+width*4,pixels=Buffer.alloc(height*stride);for(let y=0;y<height;y++)for(let x=0;x<width;x++)Buffer.from([...color,255]).copy(pixels,y*stride+1+x*4);
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',header),chunk('IDAT',deflateSync(pixels)),chunk('IEND',Buffer.alloc(0))]);
}(async()=>{
 const engine=id(1),account=id(2),chat=id(3),revision=id(4),streams=new Set();
 const thumb=thumbnail([25,166,122],256,144),original=thumbnail([200,60,140],600,400);
 const thumbHash=createHash('sha256').update(thumb).digest('hex'),originalHash=createHash('sha256').update(original).digest('hex');
 const state={seq:0,downloads:0,originalReads:0,batchReads:0,room:false,failNext:false,ensureRequests:new Map()};
 const accountData={id:account,revisionId:revision,address:'alice@fixture',label:'Alice',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z',connectionState:'connected'};
 const conversation=()=>({id:chat,revisionId:revision,accountId:account,kind:state.room?'room':'direct',address:state.room?'room@fixture':'bob@fixture',title:state.room?'Saved room':'Bob',lastMessage:'Latest',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false});
 const messages=Array.from({length:24},(_,i)=>({id:id(100+i),revisionId:revision,accountId:account,conversationId:chat,sender:{id:i%2?account:id(9),displayName:i%2?'Alice':'Bob'},originalText:'Retained message '+i,createdAt:new Date(Date.UTC(2026,10,8,12,i)).toISOString(),isOwn:i%2===1,sendState:i%2?'submitted':'received'}));
 const preview=(n,kind,extra={})=>({id:id(300+n),revisionId:revision,accountId:account,conversationId:chat,messageId:messages[n].id,url:kind==='link'?'https://social.example.test/post/'+n:'https://images.example.test/photo'+n+'.png',kind,state:'ready',downloadState:'not_downloaded',updatedAt:'2026-11-08T12:00:00Z',...extra});
 const previews=[preview(16,'link',{state:'queued'}),preview(18,'image'),preview(20,'image',{imageHash:thumbHash,title:'Image with a supplied thumbnail'}),preview(22,'image'),preview(0,'image'),preview(4,'image'),preview(6,'image'),preview(8,'image'),preview(10,'image'),preview(12,'link'),preview(23,'image')];
 for(const p of previews)messages.find(m=>m.id===p.messageId).originalText=p.url;
 messages[4].originalText='Caption stays\n'+previews[5].url+'\n';
 messages[6].originalText='```text\n'+previews[6].url+'\n```';
 messages[8].originalText='`'+previews[7].url+'`';
 messages[10].originalText='Read this '+previews[8].url+' in this example';
 const emit=type=>{state.seq++;for(const stream of streams)stream.write(`id: ${engine}:${state.seq}\nevent: ${type}\ndata: {}\n\n`)};
 const finish=(p,fail=false)=>{p.revisionId=id(500+state.seq);p.downloadState=fail?'failed':'ready';if(fail)p.lastErrorCode='fetch_failed';else{p.imageHash=thumbHash;p.fileHash=originalHash;p.fileMimeType='image/png';p.fileSize=original.length;p.integrity='computed_only';delete p.lastErrorCode}emit('message.preview_changed')};
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,data)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.5.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[accountData]});
  if(u.pathname===`/api/v1/accounts/${account}/avatars`)return send(200,{items:[]});
  if(u.pathname===`/api/v1/accounts/${account}/connection`)return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname===`/api/v1/accounts/${account}/history`)return send(200,{accountId:account,state:'complete',retainedMessages:messages.length,conversations:1,updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:[conversation()]});
  if(u.pathname===`/api/v1/conversations/${chat}`)return send(200,conversation());
  if(u.pathname===`/api/v1/conversations/${chat}/messages`)return send(200,{items:[...messages].reverse()});
  if(u.pathname===`/api/v1/conversations/${chat}/previews`){state.batchReads++;assert(u.searchParams.getAll('messageId').length===messages.length);return send(200,{items:previews})}
  for(const p of previews){
   if(u.pathname===`/api/v1/previews/${p.id}`)return send(200,p);
   if(u.pathname===`/api/v1/previews/${p.id}/download`){
    assert.equal(req.method,'POST');state.ensureRequests.set(p.id,(state.ensureRequests.get(p.id)||0)+1);if(p.downloadState==='ready')return send(200,p);
    state.downloads++;p.downloadState='queued';p.revisionId=id(400+state.seq);emit('message.preview_changed');send(202,p);setTimeout(()=>finish(p,state.failNext),p===previews[2]?600:250);return;
   }
  }
  if(u.pathname.startsWith('/api/v1/media/')){const hash=u.pathname.split('/').at(-1);if(hash===originalHash)state.originalReads++;res.setHeader('Content-Type','image/png');res.end(hash===originalHash?original:thumb);return}
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie-preview-ui'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],external=[],findings=[];
 const until=async(fn,label)=>{for(let i=0;i<180;i++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label)};
 const scope=(page,p)=>page.locator(`[data-preview-id="${p.id}"]`);
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  const preferenceKey='oie.device.auto-image-downloads.v1';
  const resetImage=p=>{p.downloadState='not_downloaded';delete p.fileHash;delete p.fileMimeType;delete p.fileSize;delete p.integrity;delete p.lastErrorCode;if(p!==previews[2])delete p.imageHash;p.revisionId=id(800+state.seq);};
  const changeSetting=async(page,enabled)=>{
   await page.evaluate(()=>window.dispatchEvent(new CustomEvent('drawer:open',{detail:{id:'settings'}})));
   const control=page.locator('[data-auto-image-downloads]');await page.locator('label[for="auto-image-downloads"]').waitFor({state:'visible'});await page.waitForTimeout(250);
   if(await control.isChecked()!==enabled)await page.locator('label[for="auto-image-downloads"]').click();
   assert.equal(await control.isChecked(),enabled);await page.keyboard.press('Escape');await page.waitForTimeout(250);
  };
  const autoContext=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'}),automaticPage=await autoContext.newPage();automaticPage.on('pageerror',e=>errors.push(e.message));
  await automaticPage.goto(`${base}/?account=${account}&chat=${chat}&lang=en&theme=dark`);
  const automaticDraft='Keep this draft while nearby images load';await automaticPage.locator('#message-composer').fill(automaticDraft);await automaticPage.locator('#message-composer').focus();
  await until(()=>previews[3].downloadState==='ready','default nearby automatic image');await until(()=>previews.every(p=>!['queued','downloading'].includes(p.downloadState)),'automatic batch completes');
  assert(state.downloads>0);for(const count of state.ensureRequests.values())assert.equal(count,1);assert.equal(previews[4].downloadState,'not_downloaded');assert.equal(state.originalReads,0);assert.equal(await automaticPage.locator('#message-composer').inputValue(),automaticDraft);assert(await automaticPage.locator('#message-composer').evaluate(el=>document.activeElement===el));assert(await automaticPage.locator('[data-auto-image-downloads]').isChecked());
  findings.push('Automatic downloads default on for nearby raw images; distant old history is untouched and drafts/focus survive');
  await changeSetting(automaticPage,false);assert.equal(await automaticPage.evaluate(key=>localStorage.getItem(key),preferenceKey),'false');
  resetImage(previews[3]);emit('message.preview_changed');const beforeOptOut=state.downloads;
  await until(async()=>await scope(automaticPage,previews[3]).getByRole('button',{name:'Load image',exact:true}).isVisible(),'opt-out manual action');await automaticPage.waitForTimeout(400);assert.equal(state.downloads,beforeOptOut);
  await scope(automaticPage,previews[3]).getByRole('button',{name:'Load image',exact:true}).click();await until(()=>previews[3].downloadState==='ready','manual image with auto disabled');assert.equal(state.downloads,beforeOptOut+1);
  resetImage(previews[1]);emit('message.preview_changed');await automaticPage.reload();await automaticPage.waitForTimeout(400);assert(!(await automaticPage.locator('[data-auto-image-downloads]').isChecked()));assert.equal(state.downloads,beforeOptOut+1);
  findings.push('The device switch persists across reload; disabling automatic downloads has zero ensure effects and manual loading still works');
  await autoContext.close();
  // A denied browser store still uses the default and holds changes in memory.
  for(const p of [previews[1],previews[2]])finish(p,false);resetImage(previews[3]);state.failNext=true;
  const deniedContext=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'});
  await deniedContext.addInitScript(()=>{for(const method of ['getItem','setItem','removeItem'])Object.defineProperty(Storage.prototype,method,{configurable:true,value(){throw new DOMException('Denied','SecurityError')}})});
  const deniedPage=await deniedContext.newPage();deniedPage.on('pageerror',e=>errors.push(e.message));const beforeFailure=state.downloads;
  await deniedPage.goto(`${base}/?account=${account}&chat=${chat}&lang=en&theme=dark`);await until(()=>previews[3].downloadState==='failed','default automatic failure with denied storage');assert.equal(state.downloads,beforeFailure+1);
  for(let i=0;i<3;i++){emit('message.preview_changed');await deniedPage.waitForTimeout(150)}
  await deniedPage.evaluate(()=>{const timeline=document.querySelector('#message-timeline');timeline.scrollTop-=80;timeline.scrollTop+=80});await deniedPage.waitForTimeout(150);assert.equal(state.downloads,beforeFailure+1);
  state.failNext=false;await scope(deniedPage,previews[3]).getByRole('button',{name:'Load image',exact:true}).click();await until(()=>previews[3].downloadState==='ready','explicit failed-image retry');assert.equal(state.downloads,beforeFailure+2);
  await changeSetting(deniedPage,false);await deniedPage.evaluate(()=>window.dispatchEvent(new CustomEvent('drawer:open',{detail:{id:'settings'}})));await deniedPage.locator('[data-device-preference-warning]').waitFor({state:'visible'});await deniedPage.waitForTimeout(250);await deniedPage.keyboard.press('Escape');await deniedPage.waitForTimeout(250);resetImage(previews[3]);emit('message.preview_changed');await deniedPage.waitForTimeout(400);assert.equal(state.downloads,beforeFailure+2);
  findings.push('Denied storage keeps the default working and opt-out in memory; failed images never retry on refresh or scroll and one user action retries once');
  await deniedContext.close();
  const automaticDownloads=state.downloads;
  // Keep the established manual-mode checks as coverage of the explicit opt-out.
  for(const p of previews)if(p.kind==='image')resetImage(p);state.downloads=0;state.originalReads=0;
  const context=await browser.newContext({viewport:{width:1440,height:1000},locale:'en'});await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==base){external.push(route.request().url());route.abort()}else route.continue()});
  await context.addInitScript(key=>{localStorage.setItem(key,'false');window.copied=[];Object.defineProperty(navigator,'clipboard',{value:{writeText:async text=>copied.push(text)}})},preferenceKey);
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));await page.goto(`${base}/?account=${account}&chat=${chat}&lang=en&theme=dark`);await until(()=>streams.size>0,'SSE');
  await until(async()=>await scope(page,previews[2]).locator('img').evaluate(img=>img.naturalWidth===256),'automatic supplied thumbnail');
  assert.equal(state.downloads,0);assert.equal(state.originalReads,0);assert.equal(await scope(page,previews[1]).locator('img').getAttribute('src'),null);assert(await scope(page,previews[1]).getByRole('button',{name:'Load image',exact:true}).isVisible());
  findings.push('With the device switch off, only supplied thumbnails load; raw originals and provider browser requests stay at zero');
  const messageRow=n=>page.locator(`[data-message-id="${messages[n].id}"]`);
  assert(!(await messageRow(18).textContent()).includes(previews[1].url));assert.equal(await messageRow(18).locator('[data-original-message]').textContent(),'');assert.equal(await messageRow(18).locator('.oie-bubble [data-preview-id]').count(),1);assert((await messageRow(18).locator('.oie-bubble').textContent()).includes('Bob'));
  assert((await messageRow(4).locator('[data-rendered-message]').textContent()).includes('Caption stays'));assert(!(await messageRow(4).locator('[data-rendered-message]').textContent()).includes(previews[5].url));
  for(const n of [6,8,10,12])assert((await messageRow(n).locator('[data-rendered-message]').textContent()).includes(messages[n].originalText.match(/https?:\/\/[^`\s]+/)[0]));
  assert.equal(await messageRow(23).locator('.oie-bubble').getAttribute('data-mine'),'true');assert.equal(await messageRow(23).locator('.oie-bubble [data-preview-id]').count(),1);assert(!(await messageRow(23).textContent()).includes(messages[23].originalText));
  await messageRow(4).locator('[data-copy-original]').click();await until(async()=>await page.evaluate(()=>copied.length)===1,'caption original copied');assert.equal(await page.evaluate(()=>copied[0]),messages[4].originalText);
  assert.equal(await page.locator('[data-preview-id]').getByRole('button',{name:'Image info',exact:true}).count(),0);
  const late=preview(14,'image');messages[14].originalText=late.url;await page.reload();await until(()=>streams.size>0,'reloaded live stream');
  const originalBody=messageRow(14).locator('[data-rendered-message]');assert((await originalBody.textContent()).includes(late.url));await page.locator('#message-composer').fill('Keep this image metadata draft');await page.locator('#message-composer').focus();await page.evaluate(()=>window.imageRow=document.querySelector('[data-message-id="019a5df0-0000-7000-8000-000000000114"]'));
  previews.push(late);emit('message.preview_changed');await until(async()=>!(await originalBody.textContent()).includes(late.url),'late image projection hides duplicate');assert(await page.evaluate(()=>document.querySelector('[data-message-id="019a5df0-0000-7000-8000-000000000114"]')===window.imageRow));assert(await page.locator('#message-composer').evaluate(el=>document.activeElement===el));assert.equal(await page.locator('#message-composer').inputValue(),'Keep this image metadata draft');
  findings.push('Image-only URL lines become image content in the same sender/own bubble; captions, inline/code examples and article links remain, Copy preserves the exact source, late metadata keeps row/focus/draft, and image details stay inside the expanded viewer');

  const draft='Draft survives link enrichment and image downloads';await page.locator('#message-composer').fill(draft);await page.locator('#message-composer').focus();
  await page.evaluate(()=>{window.previewRow=document.querySelector('[data-message-id]');const timeline=document.querySelector('#message-timeline');timeline.scrollTop=150;window.previewAnchor=[...timeline.querySelectorAll('[data-message-id]')].find(row=>row.getBoundingClientRect().bottom>=timeline.getBoundingClientRect().top);window.previewAnchorTop=window.previewAnchor.getBoundingClientRect().top});
  Object.assign(previews[0],{state:'ready',title:'<svg onload="bad()"> Social post',description:'A quoted <script> stays plain text.',siteName:'Example social',imageHash:thumbHash,revisionId:id(700)});emit('message.preview_changed');
  await until(async()=>await scope(page,previews[0]).locator('.oie-preview-title').textContent()==='<svg onload="bad()"> Social post','metadata enrichment');await page.waitForTimeout(80);
  assert(await page.locator('#message-composer').evaluate(el=>document.activeElement===el));assert.equal(await page.locator('#message-composer').inputValue(),draft);assert(await page.evaluate(()=>document.querySelector('[data-message-id]')===window.previewRow));assert(await page.evaluate(()=>Math.abs(window.previewAnchor.getBoundingClientRect().top-window.previewAnchorTop)<2));
  const card=scope(page,previews[0]).locator('[data-preview-page]');assert.equal(await card.getAttribute('href'),previews[0].url);assert.equal(await card.getAttribute('target'),'_blank');assert((await card.getAttribute('rel')).includes('noopener'));assert.equal(await scope(page,previews[0]).locator('script,svg').count(),0);
  findings.push('Structured social title/description render as plain text in a safe external card; enrichment preserves focus, draft, message nodes and scroll anchor');
  const verifyOwn=async()=>{
   const styles=await page.locator('.oie-bubble').evaluateAll(rows=>rows.slice(-2).map(row=>{const bubble=row.querySelector('[data-message-presentation]').parentElement,bounds=bubble.getBoundingClientRect();return {mine:row.dataset.mine,left:bounds.left,right:bounds.right,color:getComputedStyle(bubble).backgroundColor}}));
   const incoming=styles.find(s=>s.mine==='false'),own=styles.find(s=>s.mine==='true');assert(incoming&&own);assert(own.right>incoming.right+50,JSON.stringify(styles));assert.notEqual(own.color,incoming.color);
  };
  await verifyOwn();
  const raw=scope(page,previews[1]);await raw.getByRole('button',{name:'Load image',exact:true}).click();await until(()=>previews[1].downloadState==='ready','image retained');await until(async()=>await raw.locator('img').evaluate(img=>img.naturalWidth===256),'requested thumbnail');assert.equal(state.downloads,1);assert.equal(state.originalReads,0);assert(!(await page.locator('#oie-image-viewer').evaluate(el=>el.open)));
  await raw.locator('[data-preview-expand]').first().click();await until(async()=>await page.locator('#oie-image-viewer .oie-expanded-image').evaluate(img=>img.naturalWidth===600),'retained original expands');assert.equal(state.originalReads,1);assert(await page.locator('#oie-image-viewer').evaluate(el=>el.open));assert.equal(await page.locator('.oie-image-viewer-footer [data-viewer-info]').count(),1);assert.equal((await page.locator('[data-viewer-info]').textContent()).trim(),'');await page.locator('[data-viewer-info]').click();await page.locator('#oie-image-info').waitFor({state:'visible'});assert.equal(await page.locator('[data-image-source]').getAttribute('href'),previews[1].url);assert.equal(await page.locator('[data-image-type]').textContent(),'image/png');assert.equal((await page.locator('[data-image-size]').textContent()).replace(/\D/g,''),String(original.length));
  messages.push({...messages[0],id:id(900),revisionId:id(901),originalText:'A new actual message projection',createdAt:'2026-11-08T13:00:00Z'});emit('message.created');await until(async()=>await page.locator('[data-message-id="'+id(900)+'"]').count()===1,'new message while viewer open');assert(await page.locator('#oie-image-viewer').evaluate(el=>el.open));await page.keyboard.press('Escape');await page.locator('#oie-image-info').waitFor({state:'hidden'});assert(await page.locator('#oie-image-viewer').evaluate(el=>el.open));assert(await page.locator('[data-viewer-info]').evaluate(el=>document.activeElement===el));await page.keyboard.press('Escape');await until(async()=>!(await page.locator('#oie-image-viewer').evaluate(el=>el.open)),'viewer closes');await until(async()=>await raw.locator('[data-preview-expand]').first().evaluate(el=>document.activeElement===el),'viewer restores surviving opener');assert.equal(await page.locator('#message-composer').inputValue(),draft);
  findings.push('Load image retains only after an explicit request; expansion fetches the cached original and survives a new message, restoring focus on Escape');
  const supplied=scope(page,previews[2]);await supplied.locator('[data-preview-expand]').first().click();assert(await page.locator('#oie-image-viewer').evaluate(el=>el.open));await page.waitForTimeout(100);await page.locator('[data-viewer-info]').click();await page.locator('#oie-image-info').waitFor({state:'visible'});await page.locator('[data-image-source]').focus();assert(await page.locator('[data-image-source]').evaluate(el=>document.activeElement===el));await until(()=>previews[2].downloadState==='ready','supplied thumbnail explicit original');await until(async()=>await page.locator('#oie-image-viewer .oie-expanded-image').getAttribute('src')==='/ui/media/'+originalHash,'supplied image expansion');assert(await page.locator('[data-image-source]').evaluate(el=>document.activeElement===el));await page.locator('#oie-image-info').waitFor({state:'visible'});assert.equal(await page.locator('[data-image-type]').textContent(),'image/png');await page.keyboard.press('Escape');await page.locator('#oie-image-info').waitFor({state:'hidden'});assert(await page.locator('#oie-image-viewer').evaluate(el=>el.open));await page.keyboard.press('Escape');assert.equal(state.downloads,2);
  findings.push('Expanding an automatic small thumbnail explicitly requests the missing original');
  state.failNext=true;const failed=scope(page,previews[3]);await failed.getByRole('button',{name:'Load image',exact:true}).click();await until(async()=>await failed.locator('.oie-preview-status').textContent()==='The image could not be downloaded. Try again.','failed download visible');assert(await failed.getByRole('button',{name:'Load image',exact:true}).isEnabled());assert.equal(state.downloads,3);state.failNext=false;await failed.getByRole('button',{name:'Load image',exact:true}).click();await until(()=>previews[3].downloadState==='ready','failed image retry');assert.equal(state.downloads,4);
  findings.push('A failed original download reports a localized error and supports a safe retry');
  state.room=true;await page.goto(`${base}/?account=${account}&chat=${chat}&lang=en&theme=dark`);await verifyOwn();assert(await page.locator('#message-composer').isDisabled());
  findings.push('Canonical isOwn=true messages align right with primary color in both direct and read-only room views');
  await page.setViewportSize({width:390,height:844});await page.goto(`${base}/?account=${account}&chat=${chat}&lang=pt-BR&theme=dark`);await scope(page,previews[1]).locator('[data-preview-expand]').first().click();await page.locator('#oie-image-viewer').waitFor({state:'visible'});await page.waitForTimeout(100);
  assert.equal(await page.locator('#oie-image-viewerTitle').textContent(),'Imagem');assert.equal(await page.locator('.oie-image-viewer-footer [data-viewer-info]').count(),1);assert.equal((await page.locator('[data-viewer-info]').textContent()).trim(),'');await page.locator('[data-viewer-info]').click();await page.locator('#oie-image-info').waitFor({state:'visible'});assert.equal(await page.locator('[data-image-source]').getAttribute('href'),previews[1].url);await page.locator('#oie-image-info').waitFor({state:'visible'});await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const accessibility=await page.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(accessibility.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[]);assert.deepEqual(external,[]);assert.deepEqual(errors,[]);
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await page.screenshot({path:path.join(artifacts,'message-previews-mobile.png')});await page.screenshot({path:path.join(artifacts,'image-info-mobile.png')});await page.keyboard.press('Escape');await page.locator('#oie-image-info').waitFor({state:'hidden'});await page.keyboard.press('Escape');await page.setViewportSize({width:1440,height:1000});await scope(page,previews[0]).scrollIntoViewIfNeeded();await page.waitForTimeout(100);await page.screenshot({path:path.join(artifacts,'message-previews-desktop.png')});await messageRow(18).scrollIntoViewIfNeeded();await page.screenshot({path:path.join(artifacts,'image-message-desktop.png')});await fs.writeFile(path.join(artifacts,'message-previews-report.json'),JSON.stringify({findings,automaticDownloads,downloads:state.downloads,originalReads:state.originalReads,externalBrowserRequests:external,pageErrors:errors},null,2));console.log('PASS: lazy automatic/device-controlled image downloads, structured cards, expansion, safe retries, focus/drafts, no provider browser requests and own alignment');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(error=>{console.error(error);process.exitCode=1});
