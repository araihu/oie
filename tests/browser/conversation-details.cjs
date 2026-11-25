// Read-only details ledger: explicit scope/status, unknown != offline, no inferred
// member roster, scoped GET retry, live section preservation and zero writes.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const engine=id(1),account=id(2),direct=id(3),room=id(5),revision=id(4),streams=new Set(),state={seq:0,engineVersion:'0.9.0',mode:'known',peer:'unknown',roster:'available',rosterReads:0,delayRoster:false,connection:'connected',reads:0,writes:0};
 const conversations=[{id:direct,revisionId:revision,accountId:account,kind:'direct',address:'bob@fixture',title:'Bob',lastMessage:'Hello',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false},{id:room,revisionId:revision,accountId:account,kind:'room',address:'room@fixture',title:'Saved room',lastMessage:'Archived room message',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false}];
 const presence=(value,status)=>({state:value,statusText:status,observedAt:value==='unknown'?undefined:'2026-11-08T12:00:00Z'});
 const details=chat=>({conversationId:chat.id,accountId:state.mode==='foreign'?id(9):account,address:chat.address,kind:chat.kind,accountConnection:{accountId:account,state:state.connection,updatedAt:'2026-11-08T12:00:00Z'},state:['pending','partial','forbidden','unavailable','not_connected','unsupported','unknown'].includes(state.mode)?state.mode:'known',checkedAt:state.mode==='pending'?undefined:'2026-11-08T12:00:00Z',name:chat.kind==='room'?'Room <script>plain name</script>':undefined,description:chat.kind==='room'?'A public room with retained history':undefined,topic:chat.kind==='room'?'Topic <img src=x onerror="bad()">\nSecond line':undefined,roomSession:chat.kind==='room'?'not_joined':undefined,occupantCount:chat.kind==='room'&&!['pending','unknown'].includes(state.mode)?42:undefined,features:chat.kind==='room'?['public','persistent','moderated','members_only','anonymous']:[],participants:{scope:chat.kind==='room'?'occupants':'peer',state:state.mode==='forbidden'?'forbidden':state.mode==='pending'?'pending':'partial',truncated:chat.kind==='room'&&!['forbidden','pending','unknown'].includes(state.mode),checkedAt:'2026-11-08T12:00:00Z',items:chat.kind==='room'&&!['forbidden','pending','unknown'].includes(state.mode)?Array.from({length:18},(_,n)=>({id:'room/nick-'+n,displayName:n===0?'Nickname <img src=x>':'Participant '+n,source:n===0?'discovery':'presence',presence:presence(n===1?'away':n===2?'busy':'unknown')})):[]},peerPresence:chat.kind==='direct'?presence(state.peer,state.peer==='unknown'?undefined:'Working <img src=x onerror="bad()">'):undefined});
 const roster=[
  {nickname:'Zélia',role:'moderator',affiliation:'owner',self:true,avatarHash:'a'.repeat(64)},
  {nickname:'Amanda',role:'moderator',affiliation:'admin',self:false},
  {nickname:'Caio',role:'moderator',affiliation:'member',self:false},
  {nickname:'Teo',role:'visitor',affiliation:'none',self:false},
  ...Array.from({length:34},(_,n)=>({nickname:n===0?'Cecília <img src=x>':'Participant '+n,role:'participant',affiliation:'none',self:false})),
 ];
 const api=http.createServer((req,res)=>{const u=new URL(req.url,'http://fixture');res.setHeader('Content-Type','application/json');const send=(code,data)=>{res.statusCode=code;res.end(JSON.stringify(data))};
  if(req.method!=='GET'){state.writes++;return send(500,{status:500})}
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:state.engineVersion,eventCursor:engine+':'+state.seq});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,label:'Alice',address:'alice@fixture',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}]});
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:2,conversations:2,updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews')||u.pathname.endsWith('/message-stars'))return send(200,{items:[]});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:conversations});
  for(const chat of conversations){
   if(u.pathname===`/api/v1/conversations/${chat.id}`)return send(200,chat);
   if(u.pathname===`/api/v1/conversations/${chat.id}/details`){state.reads++;if(state.mode==='http_error')return send(503,{status:503});if(state.mode==='old_api')return send(404,{status:404});return send(200,details(chat))}
   if(u.pathname===`/api/v1/conversations/${chat.id}/room-participants` && chat.kind==='room'){
    state.rosterReads++;
    const respond=()=>state.roster==='error'?send(503,{status:503}):send(200,{state:state.roster,participants:state.roster==='available'?roster:[],truncated:false});
    return state.delayRoster?setTimeout(respond,850):respond();
   }
   if(u.pathname===`/api/v1/conversations/${chat.id}/messages`)return send(200,{items:[{id:id(chat.kind==='room'?101:100),revisionId:revision,accountId:account,conversationId:chat.id,sender:{id:id(9),displayName:'Bob'},originalText:'Retained message',createdAt:'2026-11-08T12:00:00Z',isOwn:false,sendState:'received'}]});
  }
  if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  return send(404,{status:404});
 });api.listen(0,'127.0.0.1');await once(api,'listening');const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie-conversation-details-ui'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let stderr='';child.stderr.on('data',chunk=>stderr+=chunk);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),findings=[],errors=[],external=[];
 const until=async(fn,label)=>{for(let n=0;n<240;n++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label+' '+stderr.slice(-2000))};
 const emit=()=>{state.seq++;for(const stream of streams)stream.write(`id: ${engine}:${state.seq}\nevent: account.connection_changed\ndata: {}\n\n`)};
 const opened=async page=>{await page.getByRole('dialog',{name:'Conversation details',exact:true}).waitFor({state:'visible'})};
 const openFromMenu=async(page,label,keyboard=false)=>{
  const trigger=page.locator('#chat-options [aria-controls="chat-options-panel"]');
  const menu=page.locator('#chat-options-panel');
  if(keyboard)await trigger.press('Enter');else await trigger.click();
  const item=menu.getByRole('menuitem',{name:label,exact:true});
  await item.waitFor({state:'visible'});
  await menu.evaluate(panel=>window.addEventListener('drawer:open',()=>{
   window.detailsMenuWasVisible=panel.getClientRects().length!==0;
  },{once:true}));
  if(keyboard)await item.press('Enter');else await item.click();
  const drawer=page.getByRole('dialog',{name:label,exact:true});
  await drawer.waitFor({state:'visible'});
  assert.equal(await page.evaluate(()=>window.detailsMenuWasVisible),false,'menu closes before the drawer opens');
  assert.equal(await trigger.getAttribute('aria-expanded'),'false');
  return {trigger,drawer};
 };
 try{
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
  const context=await browser.newContext({viewport:{width:1440,height:1000},locale:'en',timezoneId:'America/Sao_Paulo'});await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==base){external.push(route.request().url());route.abort()}else route.continue()});const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));
  const params=new URLSearchParams({chat:direct,account,q:'Keep 😀',filter:'unread',lang:'en',theme:'dark'});await page.goto(base+'/?'+params);const draft='Keep my conversation draft 😀';await page.locator('#message-composer').fill(draft);const trigger=page.locator('#chat-header .oie-chat-avatar-trigger');assert.equal(await trigger.getAttribute('aria-label'),'Conversation details');await trigger.click();await opened(page);
  const connection=page.locator('[data-info-entry=connection]');
  const back=page.locator('#chat-info-content [data-info-back]');
  const presence=page.locator('#conversation-details-identity [data-presence-state]');
  assert.equal(await presence.getAttribute('data-presence-state'),'unknown');
  assert.equal(await page.locator('[data-info-entry=participants]').count(),0);
  await connection.click();
  assert.equal(await page.locator('[data-details-account-state]').getAttribute('data-details-account-state'),'connected');
  assert.equal(await page.locator('[data-details-date]').first().textContent(),'Nov 8, 2026, 9:00 AM');
  assert((await page.locator('#conversation-details-metadata').textContent()).includes('No presence observation is available for this contact.'));
  await back.click();await connection.focus();
  state.peer='away';state.connection='not_connected';emit();
  await until(async()=>await presence.getAttribute('data-presence-state')==='away','observed peer presence');
  assert(await connection.evaluate(el=>document.activeElement===el));
  assert((await presence.textContent()).includes('Working <img'));
  assert.equal(await presence.locator('img,script').count(),0);
  await connection.click();
  assert.equal(await page.locator('[data-details-account-state]').getAttribute('data-details-account-state'),'not_connected');
  assert.equal(await page.locator('#conversation-details-metadata').getByText('No presence observation is available for this contact.',{exact:true}).count(),0);
  await back.click();
  assert.equal(await page.locator('#message-composer').inputValue(),draft);
  state.peer='unavailable';emit();await until(async()=>await presence.getAttribute('data-presence-state')==='unavailable','explicit peer unavailable');
  await page.keyboard.press('Escape');await page.getByRole('dialog',{name:'Conversation details',exact:true}).waitFor({state:'hidden'});await until(()=>trigger.evaluate(el=>document.activeElement===el),'drawer restores details trigger');
  findings.push('The header avatar opens details with pointer and keyboard; the drawer shows own account status independently; missing peer presence stays unknown, explicit authorized presence and account disconnect refresh safely without losing draft/focus; Escape restores trigger');
  const fromMenu=await openFromMenu(page,'Conversation details',true);
  await page.keyboard.press('Escape');
  await fromMenu.drawer.waitFor({state:'hidden'});
  await until(()=>fromMenu.trigger.evaluate(el=>document.activeElement===el),'drawer restores menu trigger');
  assert.equal(await page.locator('#message-composer').inputValue(),draft);
  state.connection='connected';
  const roomParams=new URLSearchParams(params);roomParams.set('chat',room);
  await page.goto(base+'/?'+roomParams);
  await openFromMenu(page,'Conversation details');
  await connection.click();
  assert.equal(await page.locator('[data-room-session]').textContent(),'Not joined');
  assert.equal(await page.locator('[data-occupant-count]').textContent(),'42');
  await back.click();
  assert.equal(await page.locator('#conversation-details-description script,#conversation-details-description img').count(),0);
  assert((await page.locator('#conversation-details-description').textContent()).includes('Topic <img'));
  const entry=page.locator('[data-info-entry="participants"]');
  assert.deepEqual(await page.locator('[data-info-entry]').evaluateAll(rows=>rows.map(row=>row.dataset.infoEntry)),['media','links','documents','stars','participants','preferences','connection']);
  assert.equal(await page.locator('#conversation-details-identity [data-info-entry="participants"]').count(),0);
  assert.equal(state.rosterReads,0);

  const roomRead=page.locator('[data-info-room-participants]');
  const search=page.locator('#info-room-participants-search');
  const rows=roomRead.locator('[data-room-list-match]');
  state.delayRoster=true;
  await entry.click();
  await page.locator('.oie-info-participants-loading').waitFor({state:'visible'});
  await page.keyboard.press('Escape');
  await page.getByRole('dialog',{name:'Conversation details',exact:true}).waitFor({state:'hidden'});
  await page.waitForTimeout(950);
  assert.equal(await roomRead.count(),0);
  assert.equal(await page.locator('.oie-info-participants-loading').count(),0);
  state.delayRoster=false;
  await page.locator('#chat-header .oie-chat-avatar-trigger').press('Enter');
  await opened(page);
  await entry.click();
  await roomRead.waitFor();
  assert.deepEqual(await roomRead.locator('[data-roster-list] section h3').allTextContents(),['Owners','Administrators','Moderators','Participants','Visitors']);
  assert.equal(await rows.count(),38);
  assert.equal(await rows.locator('img').count(),1);
  assert.equal(await rows.locator('a, form').count(),0);
  const firstPerson=rows.first().getByRole('button');
  await firstPerson.click();
  assert(await roomRead.locator('[data-roster-name]').isVisible());
  assert.equal(await roomRead.locator('[data-roster-detail] form').count(),0);
  await roomRead.locator('[data-roster-back]').click();
  assert(await firstPerson.evaluate(el=>el===document.activeElement));
  await search.fill('cecilia');
  assert.equal(await rows.filter({visible:true}).count(),1);
  assert.equal(await rows.filter({visible:true}).getAttribute('data-room-list-match'),'Cecília <img src=x>');
  assert.equal(await rows.filter({visible:true}).locator('img').count(),0);
  await search.fill('nothing-matches');
  assert(await roomRead.locator('[data-room-list-no-results]').isVisible());
  const loaded=state.rosterReads;
  const eventRefresh=page.waitForResponse(response=>new URL(response.url()).pathname==='/ui/refresh');
  emit();await eventRefresh;
  assert.equal(state.rosterReads,loaded);
  assert.equal(await search.inputValue(),'nothing-matches');
  await search.press('Escape');
  assert.equal(await rows.filter({visible:true}).count(),38);
  await roomRead.locator('[data-info-back]').click();
  await entry.waitFor();
  await until(()=>entry.evaluate(el=>document.activeElement===el),'return focus on Participants');
  assert.equal(state.rosterReads,loaded);

  state.roster='error';await entry.click();
  const retry=roomRead.getByRole('link',{name:'Try again',exact:true});
  await retry.waitFor();
  state.roster='not_joined';await retry.click();
  await roomRead.getByText('Join the room to see its participants.').waitFor();
  assert.equal(await rows.count(),0);
  await roomRead.locator('[data-info-back]').click();
  state.roster='available';
  findings.push('Room participants load only on entry and retry. Closing cancels a delayed response. Search, permission groups and avatars are read-only and survive live conversation refreshes.');

  await connection.click();
  for(const mode of ['pending','forbidden','unavailable','not_connected','unsupported','unknown']){
   state.mode=mode;await back.click();await connection.click();
   await until(async()=>await page.locator('[data-details-state]').getAttribute('data-details-state')===mode,'refreshed detail state '+mode);
   if(['pending','unknown'].includes(mode))assert.equal(await page.locator('[data-occupant-count]').textContent(),'Unknown');
  }
  for(const mode of ['http_error','foreign','old_api']){
   state.mode=mode;await back.click();await connection.click();
   await until(async()=>(await page.locator('#conversation-details-metadata').textContent()).includes('Additional details could not be read'),'failed detail read '+mode);
   assert.equal(await page.locator('[data-details-account-state]').getAttribute('data-details-account-state'),'connected');
   state.mode='known';await back.click();await connection.click();
   await until(async()=>await page.locator('[data-details-state]').getAttribute('data-details-state')==='known','recovered details');
  }
  await page.goto(base+'/?'+params);assert.equal(await page.locator('#message-composer').inputValue(),draft);for(const [key,value]of params)assert.equal(new URL(page.url()).searchParams.get(key),value);
  findings.push('Room metadata states and GET retry remain independent of Participants. The direct draft and filters survive.');
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});
  state.engineVersion='0.39.0';
  for(const width of [390,1440])for(const theme of ['goshtoso','minimal'])for(const mode of ['light','dark'])for(const locale of ['en','pt-BR']){
   state.mode='known';const p=await browser.newPage({viewport:{width,height:1000},locale,timezoneId:'America/Sao_Paulo',hasTouch:width===390});p.on('pageerror',e=>errors.push(e.message));const q=new URLSearchParams(roomParams);q.set('lang',locale);q.set('theme',mode);await p.goto(base+'/?'+q);const {drawer}=await openFromMenu(p,locale==='en'?'Conversation details':'Detalhes da conversa');await p.waitForTimeout(230);await p.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));const box=await drawer.boundingBox();assert(box.x>=-1&&box.x+box.width<=width+1);assert.equal(await p.locator('[data-info-room-participants]').count(),0);
   assert.equal(await drawer.locator('[data-info-entry="stars"]').textContent(),locale==='en'?'Starred messages':'Mensagens favoritadas');
   const actions=drawer.getByRole('navigation',{name:locale==='en'?'Conversation actions':'Ações da conversa'});
   const infoEntries=await actions.locator('[data-info-entry]').evaluateAll(entries=>entries.map(entry=>entry.dataset.infoEntry));
   assert.deepEqual(infoEntries.slice(-3),['participants','preferences','connection']);
   await p.addScriptTag({path:require.resolve('./fixtures/axe-core/axe.min.js')});const axe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`${width} ${theme} ${mode} ${locale}`);
   if(theme==='goshtoso')await p.screenshot({path:path.join(artifacts,`conversation-details-${width}-${mode}-${locale}.png`)});
   await p.locator('[data-info-entry="participants"]').click();await p.locator('[data-info-room-participants]').waitFor();
   assert.equal(await p.locator('[data-info-room-participants] [data-room-list-match]').count(),38);
   const participantAxe=await p.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
   assert.deepEqual(participantAxe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`Participants ${width} ${theme} ${mode} ${locale}`);
   if(theme==='goshtoso')await p.screenshot({path:path.join(artifacts,`conversation-participants-${width}-${mode}-${locale}.png`)});findings.push({width,theme,mode,locale,axe:0});await p.close();
  }
  assert.equal(state.writes,0);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);await fs.writeFile(path.join(artifacts,'conversation-details-report.json'),JSON.stringify({findings,cachedReadCount:state.reads,mutationEffects:state.writes,pageErrors:errors,externalBrowserRequests:external},null,2));console.log('PASS: scoped conversation details, room/peer status, lazy read-only participant roster, safe identity/status metadata, live focus/scroll/drafts,16 responsive variants and zero mutations');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(error=>{console.error(error);process.exitCode=1});
