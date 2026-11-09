// Isolated canonical HTTP fixture. Never accesses live user engines or providers.
const assert=require('node:assert/strict');
const http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {createHash}=require('node:crypto'),{deflateSync}=require('node:zlib');
const {spawn}=require('node:child_process'),{once}=require('node:events');
const {chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
function thumbnail(color){
 const crc=buffer=>{let value=0xffffffff;for(const b of buffer){value^=b;for(let n=0;n<8;n++)value=(value>>>1)^((value&1)?0xedb88320:0)}return(value^0xffffffff)>>>0};
 const chunk=(type,data)=>{const name=Buffer.from(type),size=Buffer.alloc(4),sum=Buffer.alloc(4);size.writeUInt32BE(data.length);sum.writeUInt32BE(crc(Buffer.concat([name,data])));return Buffer.concat([size,name,data,sum])};
 const header=Buffer.alloc(13);header.writeUInt32BE(8,0);header.writeUInt32BE(8,4);header[8]=8;header[9]=6;
 const pixels=Buffer.alloc(8*(1+8*4));for(let y=0;y<8;y++)for(let x=0;x<8;x++)Buffer.from([...color,255]).copy(pixels,y*33+1+x*4);
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',header),chunk('IDAT',deflateSync(pixels)),chunk('IEND',Buffer.alloc(0))]);
}
(async()=>{
 const engine=id(1),account=id(2),other=id(3),direct=id(4),room=id(5),revision=id(6),bad='f'.repeat(64);
 const images=[thumbnail([12,185,127]),thumbnail([210,80,110])],hashes=images.map(body=>createHash('sha256').update(body).digest('hex'));
 const streams=new Set(),state={seq:0,avatars:new Map(),fail:false,connection:'connected',messageCount:80};
 const accounts=[{id:account,address:'alice@fixture',label:'Alice'},{id:other,address:'alice@fixture',label:'Other Alice'}].map(a=>({...a,revisionId:revision,protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z',connectionState:'connected'}));
 const conversations=[{id:direct,kind:'direct',address:'bob@fixture',title:'Bob'},{id:room,kind:'room',address:'room@fixture',title:'Saved room'}].map(c=>({...c,accountId:account,revisionId:revision,lastMessage:'Retained text',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false}));
 const messagesFor=c=>Array.from({length:state.messageCount},(_,i)=>({id:id((c.id===room?1000:100)+i),revisionId:revision,accountId:account,conversationId:c.id,sender:{id:id(500+Math.floor(i/3)%2),displayName:c.kind==='room'?'room@fixture':'Bob'},originalText:'Retained message '+i,createdAt:new Date(Date.UTC(2026,10,8,12,i)).toISOString(),isOwn:false,sendState:'received'}));
 const emit=(type='profile.avatar_changed')=>{state.seq++;for(const s of streams)s.write(`id: ${engine}:${state.seq}\nevent: ${type}\ndata: {}\n\n`)};
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,data)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.4.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:accounts});
  for(const a of accounts){
   if(u.pathname===`/api/v1/accounts/${a.id}`)return send(200,a);
   if(u.pathname===`/api/v1/accounts/${a.id}/connection`)return send(200,{accountId:a.id,state:state.connection,updatedAt:'2026-11-08T12:00:00Z'});
   if(u.pathname===`/api/v1/accounts/${a.id}/history`)return send(200,{accountId:a.id,state:'complete',retainedMessages:state.messageCount,conversations:2,updatedAt:'2026-11-08T12:00:00Z'});
   if(u.pathname===`/api/v1/accounts/${a.id}/avatars`){
    if(state.fail)return send(503,{code:'unavailable',status:503});
    return send(200,{items:[...state.avatars].filter(([key])=>JSON.parse(key)[0]===a.id).map(([key,imageHash],i)=>({id:id(400+i),revisionId:revision,accountId:a.id,address:JSON.parse(key)[1],imageHash,updatedAt:'2026-11-08T12:00:00Z'}))});
   }
  }
  if(u.pathname==='/api/v1/conversations')return send(200,{items:conversations});
  for(const c of conversations){
   if(u.pathname===`/api/v1/conversations/${c.id}`)return send(200,c);
   if(u.pathname===`/api/v1/conversations/${c.id}/messages`){
    const all=messagesFor(c);
    const limit=Number(u.searchParams.get('limit')||50),direction=u.searchParams.get('direction')||'older',cursor=u.searchParams.get('cursor'),index=cursor?all.findIndex(message=>message.id===cursor):all.length;
    if(!Number.isInteger(limit)||limit<1||limit>100||!['older','newer'].includes(direction))return send(400,{status:400});
    if(index<0)return send(409,{status:409,code:'invalid_cursor'});
    const start=direction==='newer'?index+1:Math.max(0,index-limit),end=direction==='newer'?Math.min(all.length,start+limit):index,items=all.slice(start,end).reverse();
    const nextCursor=direction==='newer'?(end<all.length?items[0]?.id:undefined):(start>0?items.at(-1)?.id:undefined);
    return send(200,{items,nextCursor});
   }
  }
  if(u.pathname.startsWith('/api/v1/messages/')){for(const c of conversations){const message=messagesFor(c).find(m=>m.id===u.pathname.split('/').at(-1));if(message)return send(200,message)}return send(404,{status:404})}
  if(u.pathname.startsWith('/api/v1/avatars/')){const hash=u.pathname.split('/').at(-1),n=hashes.indexOf(hash);if(n<0)return send(404,{status:404});res.setHeader('Content-Type','image/png');res.end(images[n]);return}
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const socket=http.createServer();socket.listen(0,'127.0.0.1');await once(socket,'listening');const port=socket.address().port;await new Promise(r=>socket.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie-avatar-ui'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});
 let childLog='',childExit=null,childError=null,startupProbe=null;
 child.stderr.on('data',chunk=>{childLog=(childLog+chunk.toString()).slice(-65536)});
 child.on('error',error=>{childError=error.message});child.on('exit',(code,signal)=>{childExit={code,signal}});
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true});const findings=[],errors=[];
 const until=async(fn,label)=>{for(let i=0;i<160;i++){if(childError||childExit)throw Error('BFF exited: '+JSON.stringify({childError,childExit,startupProbe})+'\n'+childLog);if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label+' '+JSON.stringify({startupProbe,childExit,childError})+'\n'+childLog)};
 const set=(a,address,hash)=>state.avatars.set(JSON.stringify([a,address]),hash);
 try{
  await until(async()=>{try{const response=await fetch(base),body=await response.text();startupProbe={status:response.status,body:body.slice(0,300)};return response.ok}catch(error){startupProbe={error:error.message};return false}},'BFF startup');
  const noScripts=await browser.newContext({javaScriptEnabled:false,viewport:{width:1440,height:900}});const initial=await noScripts.newPage();await initial.goto(`${base}/?account=${account}&chat=${direct}&lang=en`);assert.equal(await initial.locator('[data-avatar-account] img').count(),0);assert(await initial.locator('#chat-header [data-avatar-initials]').isVisible());await noScripts.close();findings.push('Before Alpine, initials-only avatars contain no rendered image element or broken-image outline');
  const context=await browser.newContext({viewport:{width:1440,height:900},locale:'en'}),page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  await page.goto(`${base}/?account=${account}&chat=${direct}&lang=en&theme=dark`);
  await until(()=>streams.size>0,'SSE connection');
  const own=`[data-avatar-account="${account}"][data-avatar-address="alice@fixture"]`,peer=`#chat-header [data-avatar-address="bob@fixture"] img`;
  assert.equal(await page.locator(peer).count(),0);assert.equal(await page.locator('[data-avatar-account] img:visible').count(),0);
  const draft='Keep this draft while profile photos arrive';await page.locator('#message-composer').fill(draft);
  await page.locator('[data-account-switcher]:visible').click();const choice=page.locator(`[data-switch-account="${other}"]:visible`);await choice.focus();
  await page.evaluate(()=>{window.avatarFocus=document.activeElement;window.avatarRow=document.querySelector('[data-message-id]');const timeline=document.querySelector('#message-timeline');timeline.scrollTop=200;window.avatarScroll=timeline.scrollTop});
  set(account,'alice@fixture',hashes[0]);set(account,'bob@fixture',hashes[0]);set(account,'room@fixture',hashes[1]);set(other,'alice@fixture',hashes[1]);emit();
  await until(async()=>await page.locator(peer).getAttribute('src')==='/ui/avatars/'+hashes[0],'late direct avatar');
  await until(async()=>await page.locator(`${own}:visible img`).first().evaluate(img=>img.naturalWidth>0),'late own avatar');
  assert(await page.evaluate(()=>document.activeElement===window.avatarFocus));assert(await page.evaluate(()=>document.querySelector('[data-message-id]')===window.avatarRow));assert(await page.evaluate(()=>Math.abs(document.querySelector('#message-timeline').scrollTop-window.avatarScroll)<2));
  assert.equal(await page.locator('#message-composer').inputValue(),draft);
  assert.equal(await page.locator(`[data-switch-account="${other}"]:visible img`).getAttribute('src'),'/ui/avatars/'+hashes[1]);
  assert.equal(await page.locator('#message-timeline [data-avatar-address="bob@fixture"] img').first().getAttribute('src'),'/ui/avatars/'+hashes[0]);
  findings.push('Late metadata updates own profile, open account menu, list/header and direct-message photos; menu focus, row identity, scroll and draft survive');
  state.fail=true;emit();await new Promise(r=>setTimeout(r,500));assert.equal(await page.locator(peer).getAttribute('src'),'/ui/avatars/'+hashes[0]);state.fail=false;
  findings.push('A failed metadata request retains the current browser photo');
  set(account,'bob@fixture','');emit();await until(async()=>await page.locator(peer).count()===0,'explicit photo removal');assert(await page.locator('#chat-header [data-avatar-initials]').isVisible());
  assert(await page.evaluate(()=>document.activeElement===window.avatarFocus));assert.equal(await page.locator('#message-composer').inputValue(),draft);
  findings.push('An explicit empty image hash removes the image node and restores clean initials without closing the focused popover');
  set(account,'bob@fixture',bad);emit();await until(async()=>await page.locator(peer).evaluate(img=>img.getAttribute('src')?.endsWith('f'.repeat(64))&&img.complete&&img.naturalWidth===0),'missing photo request');
  await until(async()=>await page.locator(peer).evaluate(img=>getComputedStyle(img).display==='none'),'failed-image initials fallback');
  findings.push('Unavailable image falls back to initials');
  set(account,'bob@fixture',hashes[1]);emit();await until(async()=>await page.locator(peer).evaluate(img=>img.naturalWidth===8&&getComputedStyle(img).display!=='none'),'photo recovery');
  state.connection='not_connected';await page.keyboard.press('Escape');await page.locator('[data-account-switcher]:visible').click();await page.locator('[data-open-accounts]:visible').click();await page.locator(`[data-manage-account="${account}"]`).click();
  const password=page.locator('[data-connect-account] input[name=password]');await password.fill('unsent-form-value');await password.focus();set(account,'alice@fixture',hashes[1]);emit();
  await until(async()=>await page.locator(`.oie-account-detail ${own} img`).getAttribute('src')==='/ui/avatars/'+hashes[1],'account-detail photo');assert.equal(await password.inputValue(),'unsent-form-value');assert(await password.evaluate(el=>document.activeElement===el));
  findings.push('Account-detail photo updates preserve the focused connection form');
  state.connection='connected';await page.goto(`${base}/?account=${account}&chat=${room}&lang=pt-BR&theme=dark`);
  await until(async()=>await page.locator('#chat-header img').evaluate(img=>img.naturalWidth===8),'room photo');
  assert.equal(await page.locator('#chat-header img').getAttribute('src'),'/ui/avatars/'+hashes[1]);
  const participantSources=await page.locator('#message-timeline [data-avatar-account] img').evaluateAll(images=>images.map(img=>img.getAttribute('src')));assert.equal(participantSources.length,0);const initials=page.locator('#message-timeline [data-avatar-initials]');assert(await initials.count()>0);assert(await page.locator('#message-timeline [data-avatar-initials]:visible').count()>0);
  await page.locator('#chat-header button[title="Detalhes da conversa"]').click().catch(async()=>{await page.locator('#chat-header .oie-icon-button').first().click()});
  await until(async()=>await page.locator('#conversation-details-identity img').getAttribute('src')==='/ui/avatars/'+hashes[1],'details photo');await page.locator('.oie-conversation-details').waitFor({state:'visible'});await page.waitForTimeout(250);
  findings.push('Room photo appears in list, header and details; a sender matching the room address still receives initials');
  await page.keyboard.press('Escape');
  const avatarFor=n=>page.locator(`[data-message-id="${id(1000+n)}"] > .oie-message-content > [data-avatar-account]`);
  const groupedAvatars=async(target=page)=>target.locator('#message-timeline > [data-message-id]').evaluateAll(rows=>rows.map((row,index)=>{const next=rows[index+1],continues=next&&['messageAccount','messageConversation','messageSender','messageSenderName'].every(key=>row.dataset[key]===next.dataset[key]);const avatar=row.querySelector(':scope > .oie-message-content > [data-avatar-account]');return !!avatar&&getComputedStyle(avatar).visibility===(continues?'hidden':'visible')})).then(values=>values.length>0&&values.every(Boolean));
  assert(await groupedAvatars(),'only the last message of each consecutive speaker shows the avatar');
  assert(await avatarFor(79).isVisible());assert(await avatarFor(78).isHidden());
  await until(()=>streams.size>0,'room SSE ready');state.messageCount=81;emit('message.created');
  await until(async()=>await avatarFor(80).isVisible()&&await avatarFor(79).isHidden(),'live arrival moves avatar to the new group end').catch(async error=>{console.error('Avatar group diagnostic',JSON.stringify(await page.evaluate(()=>({timeline:{size:window.OieTimeline?.size,liveAttached:window.OieTimeline?.liveAttached,hasNewer:window.OieTimeline?.hasNewer},rows:[...document.querySelectorAll('#message-timeline > [data-message-id]')].slice(-4).map(row=>({id:row.dataset.messageId,classes:row.className,sender:row.dataset.messageSender,avatarVisibility:getComputedStyle(row.querySelector(':scope > .oie-message-content > [data-avatar-account]')).visibility}))}))));throw error});
  await page.locator('#message-timeline').evaluate(node=>node.scrollTop=0);
  await until(async()=>await page.locator('#message-timeline > [data-message-id]').count()===81,'older history page merged');
  assert(await groupedAvatars(),'page boundaries do not duplicate avatars within a sender sequence');
  findings.push('Consecutive sender runs show one bottom-aligned avatar on their last message, including live append and older-page joins; identical display names with different sender IDs stay separate');
  const nativePage=await browser.newPage({viewport:{width:390,height:844},javaScriptEnabled:false});
  await nativePage.goto(`${base}/?account=${account}&chat=${room}&lang=en&theme=dark`);
  assert(await groupedAvatars(nativePage),'native HTML also places avatars at the last message without JavaScript');
  await nativePage.close();


  assert.deepEqual(errors,[]);const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await page.screenshot({path:path.join(artifacts,'avatars-room.png')});await page.keyboard.press('Escape');await page.setViewportSize({width:390,height:844});await page.goto(`${base}/?account=${account}&chat=${room}&lang=pt-BR&theme=dark`);await until(async()=>await page.locator('#chat-header img').evaluate(img=>img.naturalWidth===8),'mobile room photo');assert.deepEqual(errors,[]);await page.screenshot({path:path.join(artifacts,'avatars-mobile.png')});await fs.writeFile(path.join(artifacts,'avatars-report.json'),JSON.stringify({findings,pageErrors:errors},null,2));console.log('PASS: reactive avatar identity, late/clear/failure/recovery, focus/draft/scroll preservation and room participant privacy');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(error=>{console.error(error);process.exitCode=1});
