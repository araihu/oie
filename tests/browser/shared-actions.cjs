// Isolated catalog actions fixture. No real engine or provider is contacted.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{createHash}=require('node:crypto'),{deflateSync}=require('node:zlib'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
function png(){const crc=b=>{let n=0xffffffff;for(const v of b){n^=v;for(let j=0;j<8;j++)n=(n>>>1)^((n&1)?0xedb88320:0)}return(n^0xffffffff)>>>0};const chunk=(name,data)=>{const size=Buffer.alloc(4),sum=Buffer.alloc(4),kind=Buffer.from(name);size.writeUInt32BE(data.length);sum.writeUInt32BE(crc(Buffer.concat([kind,data])));return Buffer.concat([size,kind,data,sum])};const h=Buffer.alloc(13);h.writeUInt32BE(32,0);h.writeUInt32BE(32,4);h[8]=8;h[9]=6;const pixels=Buffer.alloc(32*129);for(let y=0;y<32;y++)for(let x=0;x<32;x++)Buffer.from([30,130,180,255]).copy(pixels,y*129+1+x*4);return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',h),chunk('IDAT',deflateSync(pixels)),chunk('IEND',Buffer.alloc(0))])}
(async()=>{
 const engine=id(1),account=id(2),chat=id(3),revision=id(4),bytes=png(),hash=createHash('sha256').update(bytes).digest('hex'),streams=new Set();
 const state={ensures:0,polls:0,media:0,seq:0,ready:false};
 const kinds=['image','image','video','document','audio','link','document'];
 const items=kinds.map((kind,n)=>{
  const ext=kind==='image'?'.png':kind==='video'?'.webm':kind==='audio'?'.mp3':kind==='document'?n===6?'.docx':'.txt':'';
  const url='https://files.example/item'+n+ext+'?source=chat&encoded=%F0%9F%91%8B';
  const message={id:id(100+n),revisionId:revision,accountId:account,conversationId:chat,originalText:url,sender:{id:id(9),displayName:'Bob'},createdAt:new Date(Date.UTC(2026,10,8,12,60-n)).toISOString(),isOwn:false,sendState:'received'};
  const item={id:id(200+n),revisionId:revision,kind,url,fileName:'item'+n+ext,accountLabel:'Personal',conversationTitle:'Fixture chat',message};
  if(kind==='image')item.preview={id:id(300+n),revisionId:revision,accountId:account,conversationId:chat,messageId:message.id,url,kind:'image',state:'ready',downloadState:n===0?'ready':'not_downloaded',fileHash:n===0?hash:undefined,imageHash:hash,updatedAt:'2026-11-08T12:00:00Z'};
  return item;
 });
 const emit=()=>{state.seq++;for(const s of streams)s.write(`id: ${engine}:${state.seq}\nevent: message.preview_changed\ndata: {}\n\n`)};
 const conversation={id:chat,revisionId:revision,accountId:account,kind:'direct',address:'bob@fixture',title:'Fixture chat',lastMessage:'Fixture',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false};
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture'),send=(status,value)=>{res.statusCode=status;res.setHeader('Content-Type','application/json');res.end(JSON.stringify(value))};
  if(req.method==='POST'&&u.pathname===`/api/v1/previews/${items[1].preview.id}/download`){state.ensures++;items[1].preview.downloadState='queued';return send(202,items[1].preview)}
  if(req.method!=='GET')return send(405,{status:405,code:'method_not_allowed'});
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.15.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,label:'Personal',address:'alice@fixture',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}]});
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:7,conversations:1});
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/message-stars'))return send(200,{items:[]});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:[conversation]});if(u.pathname===`/api/v1/conversations/${chat}`)return send(200,conversation);
  if(u.pathname.endsWith('/messages')||u.pathname.endsWith('/context'))return send(200,{items:items.map(i=>i.message),targetId:items[0].message.id,hasOlder:false,hasNewer:false});
  const message=items.find(i=>u.pathname==='/api/v1/messages/'+i.message.id);if(message)return send(200,message.message);
  if(u.pathname.endsWith('/previews'))return send(200,{items:items.filter(i=>i.preview).map(i=>i.preview)});
  if(u.pathname==='/api/v1/shared-content'){const type=u.searchParams.get('type');return send(200,{items:items.filter(i=>type==='links'?i.kind==='link':type==='documents'?i.kind==='document':['image','video','audio'].includes(i.kind)),indexing:{state:'complete',indexedMessages:7,totalMessages:7}})}
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
  const card=n=>page.locator(`[data-shared-item="${items[n].id}"]`),menu=n=>card(n).locator('[role=menu]'),open=async n=>{await card(n).hover();await card(n).locator('[data-shared-menu-trigger]').click();await menu(n).getByRole('menuitem',{name:'Copy link',exact:true}).waitFor({state:'visible'})};
  await open(0);assert.equal(state.ensures,0);const before=state.media;await menu(0).getByRole('menuitem',{name:'Copy link',exact:true}).click();await until(async()=>await page.evaluate(()=>copied.length)===1,'URL copied');assert.equal(await page.evaluate(()=>copied[0]),items[0].url);assert.equal(state.media,before);await until(async()=>card(0).locator('[data-shared-menu-trigger]').evaluate(n=>n===document.activeElement),'menu focus restored');
  await open(0);const saved=page.waitForEvent('download');await menu(0).getByRole('menuitem',{name:'Download',exact:true}).click();const download=await saved;assert.deepEqual(await fs.readFile(await download.path()),bytes);assert.equal(state.ensures,0);
  await open(1);const queued=page.waitForEvent('download');await menu(1).getByRole('menuitem',{name:'Download',exact:true}).click();await until(()=>state.ensures===1&&state.polls>0,'queued image polling');state.ready=true;const original=await queued;assert.deepEqual(await fs.readFile(await original.path()),bytes);assert.equal(state.ensures,1);
  await open(4);assert.equal(await menu(4).getByRole('menuitem',{name:'Download',exact:true}).count(),0);await page.keyboard.press('Escape');
  await open(2);const video=page.waitForEvent('download');await menu(2).getByRole('menuitem',{name:'Download',exact:true}).click();assert.equal((await video).suggestedFilename(),items[2].fileName);
  await page.locator('[data-shared-tab=documents]').click();await card(3).waitFor();await open(3);const doc=page.waitForEvent('download');await menu(3).getByRole('menuitem',{name:'Download',exact:true}).click();assert.equal(await fs.readFile(await (await doc).path(),'utf8'),'Document fixture\n');
  await open(6);assert.equal(await menu(6).getByRole('menuitem',{name:'Download',exact:true}).count(),0);await page.keyboard.press('Escape');
  await page.locator('[data-shared-tab=media]').click();await card(0).waitFor();await open(0);await menu(0).getByRole('menuitem',{name:'Go to message',exact:true}).click();await page.waitForFunction(()=>document.querySelector('#message-timeline'));assert.equal(documents.length,1);assert.equal(new URL(page.url()).searchParams.get('message'),items[0].message.id);assert.deepEqual(errors,[]);await c.close();
  for(const locale of ['en','pt-BR']){const m=await browser.newContext({viewport:{width:390,height:844},locale,hasTouch:true,reducedMotion:'reduce'});await m.addInitScript(()=>localStorage.setItem('oie.device.auto-image-downloads.v1','false'));const p=await m.newPage();p.on('pageerror',e=>errors.push(e.message));await p.goto(base+'/?section=media&lang='+locale);await p.waitForFunction(()=>window.OieRuntime?.generation>0);const first=p.locator('[data-shared-item]').first();await first.locator('[data-shared-menu-trigger]').tap();await first.getByRole('menuitem',{name:locale==='en'?'Copy link':'Copiar link',exact:true}).waitFor({state:'visible'});const b=await first.locator('[role=menu]').boundingBox();assert(b.x>=0&&b.x+b.width<=390);await p.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>v.id),[]);await m.close()}
  assert.deepEqual(errors,[]);console.log('PASS: catalog chevron, exact URL copy, cached/queued image downloads, video/document save, supported actions, fragment source jump and EN/PT touch accessibility');
 }finally{await browser.close();child.kill('SIGTERM');for(const s of streams)s.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
