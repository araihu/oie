// Isolated sent-correction fixture. No provider or real-account traffic.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),chat=id(3),room=id(4),revision=id(5),own=id(10),incoming=id(11),commands=new Map(),streams=new Set();
 const state={effects:0,reads:0,latestReads:0,seq:0,eligible:true,foreign:false,conflict:false,commandState:'submitted',delay:0,submitDelay:0,posted:[],receiptReads:0};
 const conversations=[{id:chat,kind:'direct',title:'Bob',address:'bob@fixture'},{id:room,kind:'room',title:'Room',address:'room@fixture'}].map(c=>({...c,accountId:account,revisionId:revision,updatedAt:'2026-11-09T12:00:00Z',unreadCount:0,archived:false,favorite:false,lastMessage:'Retained text'}));
 const messages=[{id:incoming,originalText:'A retained incoming message',isOwn:false,sender:{id:id(8),displayName:'Bob'}},{id:own,originalText:'Own **plain** message 😀',isOwn:true,sender:{id:account,displayName:'Alice'}}].map((m,n)=>({...m,revisionId:revision,accountId:account,conversationId:chat,createdAt:`2026-11-09T12:0${n}:00Z`,sendState:m.isOwn?'submitted':'received',historical:true}));
 const emit=()=>{state.seq++;for(const stream of streams)stream.write(`id: ${engine}:${state.seq}\nevent: message.updated\ndata: {}\n\n`)};
 const api=http.createServer(async(req,res)=>{
  const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,value)=>{res.statusCode=code;res.end(JSON.stringify(value))};
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.25.0',eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts'||u.pathname==='/api/v1/accounts/'+account){const value={id:account,revisionId:revision,protocol:'xmpp',address:'alice@fixture',label:'Alice',createdAt:'2026-11-09T12:00:00Z'};return send(200,u.pathname.endsWith('/accounts')?{items:[value]}:value)}
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:2,conversations:2,updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews'))return send(200,{items:[]});
  if(u.pathname.endsWith('/capabilities'))return send(200,{accountId:account,state:'known',directText:true,features:['send_corrections','send_replies','room_messaging'].map(id=>({id,appSupport:'partial',protocolSupport:'supported',serverSupport:'scope_dependent',scope:'peer'})),discovery:{state:'complete',services:[]}});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:conversations});
  for(const c of conversations){if(u.pathname==='/api/v1/conversations/'+c.id)return send(200,c);if(u.pathname==='/api/v1/conversations/'+c.id+'/messages')return send(200,{items:c.id===chat?[...messages].reverse():[]});}
  if(u.pathname.endsWith('/message-stars'))return send(200,{items:u.searchParams.getAll('messageId').map(messageId=>({messageId,accountId:account,conversationId:chat,starred:false,revisionId:null,updatedAt:null}))});
  if(u.pathname.endsWith('/room-session'))return send(200,{accountId:account,conversationId:room,state:'joined',active:true,updatedAt:'2026-11-09T12:00:00Z'});
  if(u.pathname==='/api/v1/conversations/'+chat+'/messages/'+incoming+'/context')return send(200,{targetId:incoming,items:[messages[0]],hasOlder:false,hasNewer:true});
  if(u.pathname==='/api/v1/conversations/'+chat+'/messages/'+own+'/context')return send(200,{targetId:own,items:[...messages].reverse(),hasOlder:false,hasNewer:false});
  if(u.pathname.endsWith('/edit-target')){
   state.reads++;if(u.pathname.includes('/conversations/'))state.latestReads++;
   if(u.pathname.includes(room))return send(404,{code:'not_found'});
   const value={messageId:own,accountId:state.foreign?id(999):account,conversationId:chat,currentRevisionId:messages[1].revisionId,originalText:messages[1].originalText,eligible:state.eligible,reason:state.eligible?undefined:'not_latest'};
   return state.delay?setTimeout(()=>send(200,value),state.delay):send(200,value);
  }
  if(u.pathname.startsWith('/api/v1/message-edits/')){state.receiptReads++;const value=commands.get(u.pathname.split('/').at(-1));return value?send(200,value):send(404,{code:'not_found'})}
  if(u.pathname==='/api/v1/messages/'+own+'/edits'&&req.method==='POST'){
   let raw='';for await(const chunk of req)raw+=chunk;const input=JSON.parse(raw),key=req.headers['idempotency-key'];state.posted.push({key,input});
   if(commands.has(key))return send(200,commands.get(key));
   if(state.conflict)return send(409,{code:'state_conflict'});
   const command={id:key,revisionId:id(100+state.effects),messageId:own,accountId:account,conversationId:chat,expectedRevisionId:input.expectedRevisionId,originalText:input.originalText,state:state.commandState,createdAt:'2026-11-09T13:00:00Z',updatedAt:'2026-11-09T13:00:00Z'};commands.set(key,command);state.effects++;
   if(command.state==='submitted'){messages[1].originalText=input.originalText;messages[1].revisionId=command.revisionId;messages[1].edited=true;messages[1].editedAt=command.updatedAt;}
   return state.submitDelay?setTimeout(()=>send(202,command),state.submitDelay):send(202,command);
  }
  const source=messages.find(m=>u.pathname==='/api/v1/messages/'+m.id);if(source)return send(200,source);
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  return send(404,{code:'not_found'});
 });api.listen(0,'127.0.0.1');await once(api,'listening');const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(resolve=>allocation.close(resolve));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie-edit-ui'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let stderr='';child.stderr.on('data',c=>stderr+=c);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],findings=[];
 const until=async(fn,label)=>{for(let n=0;n<180;n++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out '+label+' '+stderr.slice(-1200))};
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'startup');
  const context=await browser.newContext({viewport:{width:1440,height:960},locale:'en'}),page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));let preparationRequests=0;page.on('request',request=>{if(new URL(request.url()).pathname==='/ui/message-edit-target')preparationRequests++;});
  const route=(params={})=>base+'/?'+new URLSearchParams({account,chat,lang:'en',theme:'dark',...params});
  const input=page.locator('#message-composer'),banner=page.locator('[data-composer-edit]'),editing=page.locator('[data-editing-message]'),save=page.locator('button[data-send-message]'),cancel=page.locator('[data-edit-cancel]');
  const assertPendingIcon=async p=>{
   const symbol=p.locator('[data-edit-pending] [data-edit-state="pending"] [role="img"]').first();await symbol.waitFor({state:'visible'});
   assert.equal(await symbol.getAttribute('aria-label'),'Saving edit');assert.equal(await symbol.evaluate(n=>n.closest('a,button')!==null),false);assert.equal(await symbol.locator('svg.oie-edit-spinner').count(),1);
   assert.equal(await p.locator('[data-edit-pending] [data-edit-state="editing"]:visible').count(),0);
  };
  const menuEdit=async()=>{await page.locator('[data-message-menu-trigger="'+own+'"]').click();await page.locator('#edit-message-'+own).click();await editing.waitFor({state:'visible'});assert(await banner.isHidden());assert(await page.locator('[data-live-status]').isHidden());await until(async()=>await input.inputValue()===messages[1].originalText,'edit loaded');};
  await page.goto(route());await until(async()=>await page.evaluate(()=>document.body.dataset.oieLiveReady==='true'),'runtime');
  await input.fill('Preserve the normal draft 😀');await menuEdit();assert.equal(await input.inputValue(),'Own **plain** message 😀');await input.fill('Unsubmitted edit');messages[1].sender.displayName='Alice refreshed';emit();await page.waitForTimeout(400);assert.equal(await editing.count(),1);assert.equal(await input.inputValue(),'Unsubmitted edit');assert(await banner.isHidden());await cancel.click();assert.equal(await editing.count(),0);assert.equal(await input.inputValue(),'Preserve the normal draft 😀');assert.equal(state.effects,0);
  await page.goto(route({message:incoming}));await until(async()=>await page.locator('#message-timeline [data-message-id="'+incoming+'"]').count()===1,'old bounded page');assert.equal(await page.locator('#message-timeline [data-message-id="'+own+'"]').count(),0);await input.fill('');const reads=state.latestReads;await input.press('ArrowUp');await until(async()=>await input.inputValue()===messages[1].originalText,'engine latest outside DOM');await editing.waitFor({state:'visible'});assert(state.latestReads>reads);assert(await banner.isHidden());await input.press('Escape');await banner.waitFor({state:'hidden'});assert.equal(await input.inputValue(),'');
  await input.fill(' ');let count=preparationRequests;await input.press('ArrowUp');await page.waitForTimeout(150);assert.equal(preparationRequests,count);await input.fill('');await input.dispatchEvent('keydown',{key:'ArrowUp',isComposing:true,keyCode:229});await page.waitForTimeout(150);assert.equal(preparationRequests,count);
  state.eligible=false;await input.press('ArrowUp');await until(()=>preparationRequests>count,'ineligible latest lookup');await page.waitForTimeout(150);assert(await banner.isHidden());assert.equal(state.effects,0);state.eligible=true;
  findings.push('Menu edit preserves the normal draft; ArrowUp queries the engine latest own source outside the bounded DOM, with whitespace/IME and ineligible-latest guards');
  await page.goto(route());await input.fill('Draft survives successful correction');await menuEdit();await input.fill('Corrected literal @nickname 😀');state.submitDelay=1500;await input.press('Enter');await page.locator('[data-edit-pending=true]').waitFor();await assertPendingIcon(page);assert.equal(await page.locator('[data-edit-pending] [data-message-presentation]').evaluate(el=>getComputedStyle(el.closest('.oie-bubble')).opacity),'0.7');assert(await banner.isHidden());state.submitDelay=0;await until(async()=>await editing.count()===0,'accepted submitted edit');assert.equal(await input.inputValue(),'Draft survives successful correction');assert.equal(state.effects,1);assert.equal(state.posted[0].input.originalText,'Corrected literal @nickname 😀');assert.deepEqual(Object.keys(state.posted[0].input).sort(),['expectedRevisionId','originalText']);
  const last=[...commands.values()].at(-1);last.state='failed';last.lastErrorCode='forbidden';emit();await page.locator('[data-edit-last-status]').waitFor({state:'visible'});assert.equal(await input.inputValue(),'Draft survives successful correction');assert((await page.locator('[data-edit-last-copy]').textContent()).includes('could not'));last.state='submitted';emit();await page.locator('[data-edit-last-status]').waitFor({state:'hidden'});
  state.conflict=true;await menuEdit();await input.fill('Rejected correction');await save.click();await until(async()=>await cancel.isEnabled()&&await banner.locator('[data-edit-feedback]').textContent()!=='','known conflict');assert(await save.isDisabled());await cancel.click();state.conflict=false;assert.equal(await input.inputValue(),'Draft survives successful correction');
  let dropped=false;await page.route('**/ui/messages/edit',async route=>{if(!dropped){dropped=true;await route.fetch();await route.abort('connectionreset')}else await route.continue()});await menuEdit();await input.fill('One immutable correction after a lost response');await save.click();await until(async()=>await banner.locator('[data-edit-check]').isEnabled()&&await input.evaluate(node=>node.readOnly),'uncertain acceptance');assert.equal(state.effects,2);await page.reload();await until(async()=>await editing.count()===0&&await input.inputValue()==='Draft survives successful correction','receipt on reload');assert.equal(state.effects,2);await page.unroute('**/ui/messages/edit');
  findings.push('Accepted corrections preserve the normal composer; late provider errors display separately; changed revision rejects clearly; lost acceptance survives reload and reconciles without a second effect');
  state.commandState='unknown';await menuEdit();await input.fill('Known command with uncertain provider outcome');await save.click();await until(async()=>await banner.locator('[data-edit-dismiss]').isVisible(),'known unknown command');assert(await cancel.isDisabled());assert(await banner.locator('[data-edit-retry]').isHidden());await banner.locator('[data-edit-dismiss]').click();assert.equal(await input.inputValue(),'Draft survives successful correction');assert(await banner.isHidden());await page.locator('[data-edit-last-status]').waitFor({state:'visible'});await input.fill('Normal typing remains available');assert.equal(state.effects,3);await page.locator('[data-edit-last-check]').click();await page.waitForTimeout(100);assert.equal(state.effects,3);
  const uncertain=[...commands.values()].at(-1);uncertain.state='submitted';emit();await page.locator('[data-edit-last-status]').waitFor({state:'hidden'});
  state.commandState='queued';await menuEdit();await input.fill('Queued correction without a status box');await save.click();await until(async()=>await editing.count()===0,'queued edit restores composer');await page.locator('[data-edit-pending]').waitFor();await assertPendingIcon(page);assert(await banner.isHidden());assert(await page.locator('[data-edit-last-status]').isHidden());assert.equal(await input.inputValue(),'Normal typing remains available');const queued=[...commands.values()].at(-1);queued.state='submitted';messages[1].originalText=queued.originalText;messages[1].revisionId=queued.revisionId;emit();await until(async()=>await page.locator('[data-edit-pending]').count()===0,'queued edit resolved');
  state.commandState='submitted';state.foreign=true;const foreign=await context.request.get(base+'/ui/message-edit-target?'+new URLSearchParams({engineId:engine,accountId:account,conversationId:chat,messageId:own}),{headers:{Accept:'application/json'}});assert.equal(foreign.status(),503);assert.equal((await foreign.json()).target,undefined);state.foreign=false;
  findings.push('An accepted unknown command can return to normal typing without claiming cancellation or retransmitting; its immutable receipt remains available separately');
  // Sample every painted frame. CLS alone omits shifts after a key press.
  const startGeometry=async p=>p.evaluate(async()=>{
   await document.fonts.ready;await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
   const read=()=>{const field=document.querySelector('#message-composer').getBoundingClientRect(),row=document.querySelector('.oie-composer').getBoundingClientRect(),timeline=document.querySelector('#message-timeline'),t=timeline.getBoundingClientRect(),send=document.querySelector('[data-send-message]').getBoundingClientRect();return {x:field.x,width:field.width,bottom:field.bottom,rowBottom:row.bottom,rowChrome:row.height-field.height,timelineAnchor:t.bottom+field.height,sendX:send.x,scroll:timeline.scrollTop,documentScroll:scrollY,editor:document.querySelector('[data-editing-message]')!==null};};
   window.editGeometry={frames:[read()],active:true};const tick=()=>{if(!window.editGeometry.active)return;window.editGeometry.frames.push(read());requestAnimationFrame(tick);};requestAnimationFrame(tick);
  });
  const stableGeometry=async(p,label,noEditor=false)=>{
   const frames=await p.evaluate(()=>{window.editGeometry.active=false;return window.editGeometry.frames});assert(frames.length>10,label+' sampled painted frames');
   for(const sample of frames)for(const key of ['x','width','bottom','rowBottom','rowChrome','timelineAnchor','sendX','scroll','documentScroll'])assert(Math.abs(sample[key]-frames[0][key])<=1,`${label} ${key} shifted ${sample[key]-frames[0][key]}px`);
   if(noEditor)assert(frames.every(frame=>!frame.editor),label+' flashed the editor');
   findings.push({geometry:label,paintedFrames:frames.length,maxUnintendedMovement:Math.max(...frames.flatMap(sample=>['x','width','bottom','rowBottom','rowChrome','timelineAnchor','sendX','scroll','documentScroll'].map(key=>Math.abs(sample[key]-frames[0][key]))))});
  };
  const assertEditIndicator=async(p,kind,label)=>{
   const marker=p.locator('[data-'+(kind==='editing'?'editing-message':'edit-pending')+']').first(),symbol=marker.locator('[data-edit-state="'+kind+'"] [role="img"]');
   await symbol.waitFor({state:'visible'});assert.equal(await symbol.getAttribute('aria-label'),label);assert.equal(await symbol.evaluate(n=>n.closest('a,button')!==null),false,'transient edit indicator has no action');
   const history=marker.locator('[data-message-edited]');if(await history.count())assert(await history.isHidden(),'history action is hidden during editing/pending');
   const before=await marker.boundingBox();await symbol.hover();const tip=p.locator('[role="tooltip"]').filter({hasText:label});
   await until(async()=>await tip.evaluateAll(nodes=>nodes.some(n=>Number(getComputedStyle(n).opacity)>.95&&n.getBoundingClientRect().width>0)),'localized edit tooltip');
   assert.deepEqual(await marker.boundingBox(),before,'tooltip does not change bubble geometry');await p.mouse.move(0,0);
   assert.equal(await symbol.locator('svg').count(),1);
  };
  const assertRepeatedArrowUpIgnored=async p=>{
   const field=p.locator('#message-composer'),original=await field.inputValue();
   for(const value of ['First line\nKeep this edited line','']){
    await field.fill(value);await field.evaluate(node=>node.setSelectionRange(Math.max(0,node.value.length-4),node.value.length));
    await p.evaluate(()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r))));
    const snapshot=()=>field.evaluate(node=>({text:node.value,start:node.selectionStart,end:node.selectionEnd,focus:document.activeElement===node,target:document.querySelector('[data-editing-message]').closest('[data-message-id]').dataset.messageId,scroll:document.querySelector('#message-timeline').scrollTop}));
    const before=await snapshot(),reads=state.reads,effects=state.effects;
    await field.press('ArrowUp');await field.press('ArrowUp');await field.press('ArrowUp');await p.waitForTimeout(150);
    assert.deepEqual(await snapshot(),before,'ArrowUp during editing preserves text, selection, focus, target and scroll');
    assert.equal(state.reads,reads,'ArrowUp during editing does not prepare another edit');assert.equal(state.effects,effects,'ArrowUp during editing does not submit');
   }
   await field.fill(original);
  };
  for(const width of [390,1440])for(const lang of ['en','pt-BR']){
   const p=await context.newPage();p.on('pageerror',error=>errors.push(error.message));await p.setViewportSize({width,height:960});await p.goto(route({lang}));await until(async()=>await p.evaluate(()=>document.body.dataset.oieLiveReady==='true'),'localized runtime');await p.locator('#message-composer').fill('');
   state.delay=450;state.eligible=false;await startGeometry(p);const before=state.reads;await p.locator('#message-composer').press('ArrowUp');await until(()=>state.reads>before,'delayed eligibility');await p.waitForTimeout(650);await stableGeometry(p,`${width} ${lang} refusal`,true);assert.equal(await p.locator('[data-form-feedback]').first().textContent(),'');assert.equal(await p.locator('#message-composer').inputValue(),'');
   const toast=p.locator('[role="alert"]').filter({hasText:lang==='en'?'cannot be edited':'não pode ser editada'});await toast.first().waitFor({state:'visible'});if(width===390&&lang==='en')await toast.first().waitFor({state:'hidden',timeout:11000});
   state.eligible=true;await p.locator('#message-composer').press('ArrowUp');await p.waitForTimeout(100);await p.locator('#message-composer').fill('Typing during eligibility must survive');await p.waitForTimeout(600);assert.equal(await p.locator('#message-composer').inputValue(),'Typing during eligibility must survive');assert(await p.locator('[data-composer-edit]').isHidden());
   await p.locator('#message-composer').fill('');await startGeometry(p);await p.locator('#message-composer').press('ArrowUp');await p.waitForTimeout(650);await stableGeometry(p,`${width} ${lang} entering edit`);state.delay=0;await p.locator('[data-editing-message]').waitFor({state:'visible'});await until(async()=>await p.locator('#message-composer').inputValue()===messages[1].originalText,'localized edit '+width+' '+lang+' wanted '+messages[1].originalText);await assertEditIndicator(p,'editing',lang==='en'?'Editing message':'Editando mensagem');await assertRepeatedArrowUpIgnored(p);await p.locator('#message-composer').press('Escape');assert(await p.locator('[data-composer-edit]').isHidden());assert.equal(await p.locator('#message-composer').inputValue(),'');await p.locator('#message-composer').press('ArrowUp');await p.locator('[data-editing-message]').waitFor({state:'visible'});assert(await p.locator('[data-send-message]').isVisible());assert(await p.locator('[data-send-message]').isEnabled());await p.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`${width} ${lang}`);assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});await p.screenshot({path:path.join(__dirname,`artifacts/sent-corrections-${width}-${lang}.png`)});await p.locator('[data-edit-cancel]').click();assert.equal(await p.locator('[data-editing-message],[data-edit-pending]').count(),0);await p.close();findings.push({width,lang,axe:0});
  }
  assert.deepEqual(errors,[]);await fs.writeFile(path.join(__dirname,'artifacts/sent-corrections-report.json'),JSON.stringify({findings,effects:state.effects,commands:state.posted,pageErrors:errors},null,2));console.log('PASS: scoped sent corrections, engine latest keyboard target, draft restore, unknown receipt journal, rejection and responsive accessibility');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve))}
})().catch(error=>{console.error(error);process.exitCode=1});
