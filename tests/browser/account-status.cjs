// Isolated HTTP fixture. No live engines, real accounts, or protocol traffic.
const assert=require('node:assert/strict');
const http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {createHash}=require('node:crypto'),{deflateSync}=require('node:zlib');
const {spawn}=require('node:child_process'),{once}=require('node:events');
const {chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
function image(){
 const crc=b=>{let n=0xffffffff;for(const v of b){n^=v;for(let i=0;i<8;i++)n=(n>>>1)^((n&1)?0xedb88320:0)}return(n^0xffffffff)>>>0};
 const chunk=(type,body)=>{const name=Buffer.from(type),size=Buffer.alloc(4),sum=Buffer.alloc(4);size.writeUInt32BE(body.length);sum.writeUInt32BE(crc(Buffer.concat([name,body])));return Buffer.concat([size,name,body,sum])};
 const header=Buffer.alloc(13);header.writeUInt32BE(1);header.writeUInt32BE(1,4);header[8]=8;header[9]=6;
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',header),chunk('IDAT',deflateSync(Buffer.from([0,12,185,127,255]))),chunk('IEND',Buffer.alloc(0))]);
}
(async()=>{
 const engine=id(1),alice=id(2),bob=id(3),revision=id(4),stamp='2026-11-08T12:00:00Z';
 const png=image(),hash=createHash('sha256').update(png).digest('hex'),missing='f'.repeat(64);
 const accounts=[{id:alice,label:'Alice initials',address:'alice@fixture'},{id:bob,label:'Bob photo',address:'bob@fixture'}].map(a=>({...a,revisionId:revision,protocol:'xmpp',createdAt:stamp,connectionState:'connected'}));
 const state={seq:0,connection:'connected',avatars:new Map([[bob,hash]])},streams=new Set(),errors=[],findings=[];
 const emit=type=>{state.seq++;for(const stream of streams)stream.write(`id: ${engine}:${state.seq}\nevent: ${type}\ndata: {}\n\n`)};
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(status,body)=>{res.statusCode=status;res.end(JSON.stringify(body))};
  assert.equal(req.method,'GET','status fixture must not mutate any account');
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.12.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:accounts});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:[]});
  for(const account of accounts){
   if(u.pathname===`/api/v1/accounts/${account.id}/connection`)return send(200,{accountId:account.id,state:state.connection,updatedAt:stamp});
   if(u.pathname===`/api/v1/accounts/${account.id}/history`)return send(200,{accountId:account.id,state:'complete',retainedMessages:0,conversations:0,updatedAt:stamp});
   if(u.pathname===`/api/v1/accounts/${account.id}/avatars`)return send(200,{items:state.avatars.has(account.id)?[{id:id(20),revisionId:revision,accountId:account.id,address:account.address,imageHash:state.avatars.get(account.id),updatedAt:stamp}]:[]});
  }
  if(u.pathname===`/api/v1/avatars/${hash}`){res.setHeader('Content-Type','image/png');return res.end(png)}
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(resolve=>allocation.close(resolve));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});
 let log='';child.stderr.on('data',chunk=>log+=chunk);const browser=await chromium.launch({headless:true}),base='http://127.0.0.1:'+port;
 const until=async(fn,label)=>{for(let n=0;n<160;n++){if(await fn())return;await new Promise(resolve=>setTimeout(resolve,50))}throw Error('Timed out: '+label+'\n'+log)};
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  for(const config of [{width:1440,locale:'en',mode:'dark'},{width:390,locale:'pt-BR',mode:'light'}])for(const theme of ['goshtoso','minimal']){
   state.connection='connected';state.avatars=new Map([[bob,hash]]);
   const page=await browser.newPage({viewport:{width:config.width,height:900},locale:config.locale});page.on('pageerror',error=>errors.push(error.message));
   await page.goto(`${base}/?lang=${config.locale}&theme=${config.mode}`);await page.evaluate(value=>document.documentElement.dataset.theme=value,theme);
   await until(()=>streams.size>0,'live event stream');await page.locator('[data-account-switcher]:visible').click();
   const menu=page.getByRole('dialog',{name:config.locale==='en'?'Switch account':'Trocar de conta',exact:true});await menu.waitFor({state:'visible'});
   const choice=account=>menu.locator(`[data-switch-account="${account}"]`),status=account=>choice(account).locator(`[data-account-status="${account}"]`);
   const placement=async account=>{
    const marker=status(account);assert.equal(await marker.count(),1);assert.equal(await choice(account).locator('.oie-account-indicators [data-account-status]').count(),0);
    let geometry;await until(async()=>{geometry=await marker.evaluate(root=>{
     const avatar=root.querySelector('[data-avatar-account]'),box=avatar?.getBoundingClientRect();
     const dots=[...root.querySelectorAll('span[aria-hidden="true"]')].map(node=>({node,rect:node.getBoundingClientRect(),style:getComputedStyle(node)})).filter(({rect,style})=>rect.width>0&&rect.width<20&&rect.height>0&&rect.height<20&&style.position==='absolute'&&style.backgroundColor!=='rgba(0, 0, 0, 0)'&&style.visibility!=='hidden'&&style.display!=='none');
     return{avatar:box?{left:box.left,right:box.right,top:box.top,bottom:box.bottom}:null,dots:dots.map(({rect,style})=>({left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,color:style.backgroundColor}))};
    });return geometry.dots.length===1},'avatar indicator mounted '+config.width+' '+theme+' '+account);
    assert(geometry.avatar,'connection status must wrap the account avatar');
    const box=geometry.avatar,dot=geometry.dots[0];assert(dot.left>=box.left-1&&dot.right<=box.right+1&&dot.top>=box.top-1&&dot.bottom<=box.bottom+1,JSON.stringify(geometry));
    assert(dot.left>(box.left+box.right)/2&&dot.top>(box.top+box.bottom)/2,'indicator is attached to the avatar lower corner');return dot.color;
   };
   await until(async()=>await choice(bob).locator('img').evaluate(img=>img.complete&&img.naturalWidth===1),'profile image ready');
   assert.equal(await choice(alice).locator('img').count(),0);await placement(alice);await placement(bob);
   const labels=config.locale==='en'?{connected:'Connected',connecting:'Connecting…',error:'Connection error',not_connected:'Not connected'}:{connected:'Conectado',connecting:'Conectando…',error:'Erro de conexão',not_connected:'Não conectado'};
   await choice(bob).focus();const colors=new Set();
   for(const value of ['connecting','error','not_connected','connected']){
    state.connection=value;emit('account.connection_changed');
    await until(async()=>await status(bob).getAttribute('data-connection-state')===value&&await status(alice).getAttribute('data-connection-state')===value,'live avatar status '+value);
    for(const account of [alice,bob]){assert.equal(await status(account).getAttribute('title'),labels[value]);assert.equal(await status(account).getAttribute('aria-label'),labels[value]);assert.equal(await status(account).getAttribute('role'),'img');await placement(account)}
    colors.add(await placement(bob));assert(await menu.isVisible());assert(await choice(bob).evaluate(node=>document.activeElement===node));assert.equal(await choice(bob).locator('.oie-profile-identity small').count(),1);
   }
   assert.equal(colors.size,4,'connection states retain distinct semantic colors');
   state.avatars.set(bob,missing);emit('profile.avatar_changed');
   await until(async()=>await choice(bob).locator('img').evaluate(img=>img.complete&&img.naturalWidth===0&&getComputedStyle(img).display==='none'),'broken image falls back to initials');await placement(bob);
   assert(await choice(bob).evaluate(node=>document.activeElement===node));
   state.avatars.set(bob,'');emit('profile.avatar_changed');await until(async()=>await choice(bob).locator('img').count()===0,'explicit avatar removal');await placement(bob);
   await page.keyboard.press('Escape');await menu.waitFor({state:'hidden'});assert(await page.locator('[data-account-switcher]:visible').evaluate(node=>document.activeElement===node));
   findings.push({...config,theme,statuses:4,initials:true,image:true,failedImageFallback:true,focusedLiveUpdates:true});await page.close();
  }
  assert.deepEqual(errors,[]);const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await fs.writeFile(path.join(artifacts,'account-status-report.json'),JSON.stringify({findings,pageErrors:errors},null,2));
  console.log('PASS: avatar-attached account status, localized accessibility, semantic colors, live focus and image fallbacks');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
