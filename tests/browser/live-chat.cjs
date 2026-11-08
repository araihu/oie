// Adversarial browser checks against an independent stateful HTTP API fixture.
// Real XMPP delivery is verified separately by engine-browser.cjs.
const assert=require('node:assert/strict');
const http=require('node:http');const path=require('node:path');const fs=require('node:fs/promises');
const {spawn}=require('node:child_process');const {once}=require('node:events');const {chromium}=require('playwright');
const artifacts=path.join(__dirname,'artifacts');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
const engine=id(1),account=id(2),chat=id(3);
(async()=>{
 const state={messages:[],operations:new Map(),effects:0,cancelEffects:0,seq:0,connection:'not_connected',error:null};const streams=new Set();
 const conversation=()=>({id:chat,revisionId:id(4),accountId:account,kind:state.showRoom?'room':'direct',address:state.showRoom?'saved@conference.localhost':'bob@localhost',title:state.showRoom?'Saved room':'Bob',lastMessage:state.messages.at(-1)?.originalText||'',updatedAt:'2026-11-07T20:00:00Z',unreadCount:0,archived:false,favorite:false});
 const accountData={id:account,revisionId:id(5),address:'alice@localhost',protocol:'xmpp',label:'Alice test',createdAt:'2026-11-07T18:00:00Z',connectionState:'not_connected'};
 const emit=(type,message)=>{state.seq++;const event={id:engine+':'+state.seq,type,occurredAt:new Date().toISOString(),message};for(const stream of streams)stream.write(`id: ${event.id}\nevent: ${type}\ndata: ${JSON.stringify(event)}\n\n`)};
 const api=http.createServer(async(req,res)=>{
  res.setHeader('Content-Type','application/json');const u=new URL(req.url,'http://fixture');
  const write=(code,data)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  const problem=(code,type,currentMessage)=>write(code,{type:'urn:oie:problem:'+type,title:'Problem',code:type,status:code,currentMessage});
  let raw='';for await(const chunk of req)raw+=chunk;let input={};try{if(raw)input=JSON.parse(raw)}catch{return problem(400,'invalid_request')}
  if(u.pathname==='/api/v1/engine')return write(200,{id:engine,role:'hosted',apiVersion:'0.2.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return write(200,{items:[accountData]});
  if(u.pathname==='/api/v1/accounts/'+account+'/history')return write(200,{accountId:account,state:state.showRoom?'partial':'syncing',retainedMessages:state.messages.length,conversations:1,updatedAt:new Date().toISOString()});
  if(u.pathname==='/api/v1/accounts/'+account+'/connection'){
   if(req.method==='PUT'){state.connection='error';state.error='authentication_failed';emit('account.connection_changed');return write(202,{accountId:account,state:'error',lastErrorCode:state.error,updatedAt:new Date().toISOString()})}
   return write(200,{accountId:account,state:state.connection,lastErrorCode:state.error||undefined,updatedAt:new Date().toISOString()});
  }
  if(u.pathname==='/api/v1/conversations')return write(200,{items:[conversation()]});
  if(u.pathname==='/api/v1/conversations/'+chat)return write(200,conversation());
  if(u.pathname==='/api/v1/conversations/'+chat+'/messages'){
   if(req.method==='GET')return write(200,{items:[...state.messages].reverse()});
   const key=req.headers['idempotency-key'];const known=state.operations.get(key);
   if(known){const message=state.messages.find(m=>m.id===known.resourceId);return message.originalText===input.originalText?write(200,message):problem(409,'idempotency_conflict')}
   const message={id:id(100+state.effects),revisionId:id(200+state.effects),accountId:account,conversationId:chat,sender:{id:account,displayName:'Alice'},originalText:input.originalText,createdAt:new Date().toISOString(),isOwn:true,sendState:'queued'};
   state.effects++;state.messages.push(message);state.operations.set(key,{id:key,kind:'send_message',resourceId:message.id,status:'succeeded',createdAt:message.createdAt});emit('message.created',message);return write(202,message);
  }
  if(u.pathname.startsWith('/api/v1/operations/')){const operation=state.operations.get(u.pathname.split('/').at(-1));return operation?write(200,operation):problem(404,'not_found')}
  if(u.pathname.startsWith('/api/v1/messages/')){
   const message=state.messages.find(m=>m.id===u.pathname.split('/')[4]);if(!message)return problem(404,'not_found');
   if(req.method==='POST'){
    if(message.sendState!=='queued'||message.revisionId!==input.expectedRevisionId)return problem(409,'state_conflict',message);
    message.sendState='cancelled';state.cancelEffects++;emit('message.updated',message);return write(200,message);
   }
   return write(200,message);
  }
  if(u.pathname==='/api/v1/events'){
   res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);req.on('close',()=>streams.delete(res));return;
  }
  problem(404,'not_found');
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});
 const base='http://127.0.0.1:'+port;const browser=await chromium.launch({headless:true});const findings=[];
 const until=async(fn,name)=>{for(let i=0;i<160;i++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+name)};
 try{
  for(let i=0;i<50;i++){try{if((await fetch(base)).ok)break}catch{}await new Promise(r=>setTimeout(r,100))}
  const context=await browser.newContext({viewport:{width:1440,height:900},locale:'en',timezoneId:'America/Sao_Paulo'});const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base+'/?chat='+chat+'&lang=en&theme=dark');await page.locator('#message-composer').waitFor();
  await page.locator('[data-account-switcher]:visible').click();
  const switcher=page.getByRole('dialog',{name:'Switch account',exact:true});
  const accountLink=switcher.locator('[data-switch-account="'+account+'"]');await accountLink.focus();
  for(const [value,label]of [['connecting','Connecting…'],['connected','Connected'],['error','Connection error'],['not_connected','Not connected']]){
   state.connection=value;emit('account.connection_changed');
   const dot=accountLink.locator('[data-account-status]');
   await until(async()=>await dot.getAttribute('data-connection-state')===value,'focused account status '+value);
   assert.equal(await dot.getAttribute('title'),label);assert.equal(await dot.getAttribute('aria-label'),label);
   assert(await accountLink.evaluate(el=>document.activeElement===el));
   assert.equal(await accountLink.locator('.oie-profile-identity small').count(),1);
  }
  await page.keyboard.press('Escape');
  const draft='Keep this device draft 👋';await page.locator('#message-composer').fill(draft);await page.reload();await until(async()=>await page.locator('#message-composer').inputValue()===draft,'draft reload');
  const forged=await page.evaluate(async()=>{const form=document.querySelector('#send-message-form');const values=new URLSearchParams(new FormData(form));values.set('originalText','forged');values.set('csrf','forged');return(await fetch(form.action,{method:'POST',body:values,headers:{Accept:'application/json'}})).status});assert.equal(forged,403);assert.equal(state.effects,0);
  const guards=await page.evaluate(async()=>{const form=document.querySelector('#send-message-form');const values=new URLSearchParams(new FormData(form));values.set('originalText','blocked');values.set('engineId','019a5df0-0000-7000-8000-000000009999');const rejected=await fetch(form.action,{method:'POST',headers:{Accept:'application/json'},body:values});return {engine:rejected.status,method:(await fetch(form.action)).status}});assert.equal(guards.engine,409);assert.equal(guards.method,405);assert.equal(state.effects,0);
  findings.push('Device draft survives reload; forged CSRF/engine identity and wrong method have zero API effects');
  await until(async()=>await page.locator('[data-live-status]').textContent().then(t=>t.includes('Live updates connected')),'live stream ready before transport fault');assert(await page.locator('[data-live-status]').isHidden());
  let dropped=false,release;const gate=new Promise(resolve=>release=resolve);
  await page.route('**/ui/messages/send',async route=>{if(!dropped){dropped=true;await route.fetch();await gate;await route.abort('connectionreset')}else await route.continue()});
  await page.locator('[data-send-message]').click();await until(()=>state.effects===1,'one in-flight effect');assert(await page.locator('[data-send-message]').isDisabled());await page.evaluate(()=>document.querySelector('#send-message-form').requestSubmit());await new Promise(r=>setTimeout(r,100));assert.equal(state.effects,1);release();await until(async()=>await page.locator('[data-form-feedback]').filter({hasText:'Acceptance could not be confirmed'}).count()>0,'visible lost-response recovery');
  assert.equal(state.effects,1);assert.equal(await page.locator('#message-composer').inputValue(),draft);assert(await page.locator('#message-composer').evaluate(e=>e.readOnly));
  const pending=await page.evaluate(()=>Object.keys(localStorage).filter(k=>k.startsWith('oie.draft.v1')).map(k=>JSON.parse(localStorage.getItem(k))).find(v=>v.pending));assert(pending&&pending.text===draft);
  await page.locator('[data-send-message]').click();await until(async()=>await page.locator('#message-composer').inputValue()==='','receipt reconciliation');assert.equal(state.effects,1);
  findings.push('Lost BFF acceptance response preserves key/body, locks editing, and reconciles to one effect');
  await until(async()=>await page.locator('[data-cancel-message]').count()===1,'queued cancellation control');
  state.messages[0].sendState='submitted';state.messages[0].revisionId=id(999);
  await page.locator('[data-cancel-message] button[type=submit]').click();await until(async()=>await page.locator('[data-live-status]').textContent().then(t=>t.includes('Cancellation was not applied')),'cancel conflict announced');assert(await page.locator('[data-live-status]').isVisible());assert.equal(state.cancelEffects,0);
  await until(async()=>await page.locator('[data-message-id="'+state.messages[0].id+'"][data-send-state=submitted]').count()===1,'actual submitted projection');
  findings.push('Stale queued cancel is rejected, announces submitted state, and makes zero cancellation effects');
  state.connection='not_connected';emit('account.connection_changed');await page.locator('[data-account-switcher]:visible').click();await page.locator('[data-open-accounts]:visible').click();await page.locator('[data-manage-account="'+account+'"]').click();const connect=page.locator('[data-connect-account]');await connect.locator('[name=password]').fill('never-journal-this-secret');await connect.locator('button[type=submit]').click();
  await until(async()=>await page.getByText('Authentication failed. Check the password.',{exact:true}).count()>0,'actual authentication error');assert.equal(await connect.locator('[name=password]').inputValue(),'');
  assert(!(await page.content()).includes('never-journal-this-secret'));assert(!(await page.evaluate(()=>JSON.stringify(localStorage))).includes('never-journal-this-secret'));
  findings.push('Authentication error remains visible; passwords are cleared and absent from markup/journals');
  state.showRoom=true;await page.goto(base+'/?chat='+chat+'&account='+account+'&lang=en');
  assert(await page.locator('#message-composer').isDisabled());assert.equal(await page.locator('[data-room-readonly]').count(),0);
  assert((await page.locator('#history-status').textContent()).includes('Some history is unavailable'));
  const guarded=await page.evaluate(async()=>{const form=document.querySelector('#send-message-form');const values=new URLSearchParams(new FormData(form));values.set('originalText','forged room send');const response=await fetch(form.action,{method:'POST',headers:{Accept:'application/json'},body:values});return {status:response.status,body:await response.json()}});
  assert.equal(guarded.status,409);assert.equal(guarded.body.code,'read_only');assert.equal(state.effects,1);
  findings.push('Recovered room is read-only even for a forged valid-CSRF send; partial history is visible');state.showRoom=false;
  const denied=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR',timezoneId:'America/Sao_Paulo'});
  await denied.addInitScript(()=>{for(const name of ['getItem','setItem','removeItem'])Object.defineProperty(Storage.prototype,name,{configurable:true,value(){throw new DOMException('Denied','SecurityError')}})});
  const mobile=await denied.newPage();mobile.on('pageerror',e=>errors.push(e.message));await mobile.goto(base+'/?chat='+chat+'&lang=pt-BR&theme=dark');await mobile.locator('#message-composer').fill('Envio com armazenamento indisponível');await mobile.locator('[data-send-message]').click();await until(async()=>await mobile.locator('#message-composer').inputValue()==='','denied-storage send');assert.equal(state.effects,2);assert(await mobile.locator('.oie-storage-warning').isVisible());
  findings.push('Denied storage is an explicit warning; an accepted send still works without a script crash');
  await mobile.setViewportSize({width:1076,height:1109});await mobile.goto(base+'/?section=media&lang=pt-BR&theme=dark');
  const circles=await mobile.locator('.oie-icon-button:visible').evaluateAll(nodes=>nodes.map(node=>{const b=node.getBoundingClientRect();return {name:node.getAttribute('aria-label'),width:b.width,height:b.height}}));assert(circles.length>0);for(const circle of circles)assert(Math.abs(circle.width-circle.height)<1,JSON.stringify(circle));
  findings.push('Circular controls have square actual bounds at the reported1076x1109 viewport');
  assert.deepEqual(errors,[]);await fs.mkdir(artifacts,{recursive:true});await fs.writeFile(path.join(artifacts,'live-adversarial-report.json'),JSON.stringify({backend:'independent HTTP fixture; actual XMPP is a separate observer',findings,pageErrors:errors,effects:state.effects,cancelEffects:state.cancelEffects},null,2));console.log('PASS: live UI CSRF, reload drafts, lost-response reconciliation, stale cancel, password hygiene, and denied storage');
 }finally{await browser.close();child.kill('SIGTERM');api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exitCode=1});
