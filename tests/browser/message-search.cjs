// Independent read-only engine fixture. Real SQLite search is checked separately
// by message_search_test.go and the development-XMPP browser workflow.
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {createHash} = require('node:crypto');
const {chromium} = require('playwright');
const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
(async () => {
  const conversationPhoto=await fs.readFile(path.join(__dirname,'fixtures/video-poster.png'));
  const senderPhoto=Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=','base64');
  const photoHash=photo=>createHash('sha256').update(photo).digest('hex');
  const conversationHash=photoHash(conversationPhoto),senderHash=photoHash(senderPhoto);
  const account = id(2), work = id(3), other = id(8), streams = new Set();
  const state = {engine:id(1), writes:0, searchReads:[], titleReads:[], contactReads:[], contexts:[], mode:'normal'};
  const accounts = [
    {id:account,address:'alice@fixture',label:'Personal'},
    {id:work,address:'work@fixture',label:'Work'},
    {id:other,address:'other@fixture',label:'Other account'},
  ].map(a => ({...a,revisionId:id(4),protocol:'xmpp',connectionState:'connected',createdAt:'2026-11-09T12:00:00Z'}));
  const chats = [
    {id:id(10),accountId:account,title:'Family'},
    {id:id(11),accountId:work,title:'Team'},
    {id:id(12),accountId:account,title:'Other conversation'},
    {id:id(14),accountId:other,title:'Other account conversation'},
  ].map(c => ({...c,revisionId:id(15),kind:'direct',address:'peer@fixture',lastMessage:'Retained text',updatedAt:'2026-11-09T14:00:00Z',unreadCount:0,archived:false,favorite:false}));
  const [chat] = chats;
  for(let n=0;n<26;n++)chats.push({...chat,id:id(100+n),title:'Spare conversation '+n,address:'spare'+n+'@fixture'});
  const corpus = Array.from({length:57}, (_, n) => {
    const c = chats[n % 3];
    return {id:id(1000+n),revisionId:id(2000+n),accountId:c.accountId,conversationId:c.id,sender:{id:id(9),displayName:'Peer'},originalText:'Telescope retained message '+n,createdAt:new Date(Date.UTC(2026,10,9,12,n)).toISOString(),isOwn:false,sendState:'received',historical:true};
  });
  // A legitimate current-content result can refer to an earlier content revision
  // after transport state transitions. Revision inequality does not imply an edit.
  corpus[54].createdAt='2026-11-09T12:54:00.000900Z';corpus[55].createdAt='2026-11-09T12:54:00.000100Z';
  corpus[1].sendState = 'cancelled'; corpus[1].isOwn = true;
  corpus[2].conversationId=chat.id;corpus[2].originalText += ' Café 😀 <img src=x onerror="window.searchInjection=true">';
  const fillers = Array.from({length:465}, (_, n) => ({...corpus[0],id:id(4000+n),revisionId:id(5000+n),originalText:'Unrelated recent message '+n,createdAt:new Date(Date.UTC(2026,10,9,14,n)).toISOString()}));
  const archiveRoom={...chat,id:id(13),title:'Archive fixture room',address:'room@archive.fixture',kind:'room'};
  const savedMatch={...corpus[0],id:id(6010),revisionId:id(6110),conversationId:archiveRoom.id,originalText:'Archive keyword in saved history'};
  const remoteMatch={...savedMatch,id:id(6011),revisionId:id(6111),originalText:'Archive keyword in server history',createdAt:'2026-11-02T12:00:00Z'};
  const olderRoomMessage={...remoteMatch,id:id(6012),revisionId:id(6112),originalText:'Older room message',createdAt:'2026-11-01T12:00:00Z'};
  const archive={conversationId:archiveRoom.id,mode:'on_demand',availability:'supported',textSearch:'supported',initialComplete:true,olderComplete:false};
  const archiveRequests=[];
  let releaseSearch, finishArchiveMode;
  const archiveModes=[];
  corpus.push(savedMatch);
  const scopeMessages=[chats[0],chats[1],chats[3]].map((c,index)=>({
    ...corpus[0],id:id(6050+index),revisionId:id(6150+index),accountId:c.accountId,conversationId:c.id,
    originalText:'Account scope marker '+index,createdAt:new Date(Date.UTC(2026,10,9,16,index)).toISOString()
  }));
  corpus.push(...scopeMessages);
  const all = [...corpus,...fillers];
  const chronological = c => all.filter(m => m.conversationId===c.id).sort((a,b)=>a.createdAt.localeCompare(b.createdAt)||a.id.localeCompare(b.id));
  const fold = value => value.normalize('NFD').replace(/\p{M}/gu,'').toLowerCase();
  const api = http.createServer(async (req,res) => {
    const u = new URL(req.url,'http://fixture');
    res.setHeader('Content-Type','application/json');
    const send = (status, body) => {res.statusCode=status;res.end(JSON.stringify(body))};
    if (u.pathname==='/api/v1/conversations/'+archiveRoom.id+'/archive' && req.method==='PUT') {
      let raw='';
      for await (const chunk of req) raw+=chunk;
      const {mode}=JSON.parse(raw);
      archiveModes.push(mode);
      const accepted=await new Promise(resolve=>finishArchiveMode=resolve);
      if(!accepted)return send(503,{code:'unavailable'});
      archive.mode=mode;
      return send(200,archive);
    }
    if (u.pathname==='/api/v1/conversations/'+archiveRoom.id+'/archive/requests' && req.method==='POST') {
      let raw='';
      for await (const chunk of req) raw+=chunk;
      const request=JSON.parse(raw);
      archiveRequests.push(request);
      if(request.action==='older'){
        await new Promise(resolve=>setTimeout(resolve,300));
        if(!all.includes(olderRoomMessage))all.push(olderRoomMessage);
        return send(200,{archive:{...archive,olderComplete:true},outcome:'stored',items:[]});
      }
      if(request.action==='context')return send(200,{archive,outcome:'stored',items:[]});
      if(request.query!=='Archive keyword')return send(200,{archive,outcome:'complete',items:[]});
      await new Promise(resolve=>releaseSearch=resolve);
      if(!all.includes(remoteMatch))all.push(remoteMatch);
      return send(200,{archive,outcome:'stored',items:[savedMatch,remoteMatch]});
    }
    if (req.method !== 'GET') {state.writes++;return send(405,{code:'unexpected_write'})}
    if (u.pathname === '/api/v1/engine') return send(200,{id:state.engine,role:'hosted',apiVersion:'0.21.0',eventCursor:state.engine+':0'});
    if (u.pathname === '/api/v1/accounts') return send(200,{items:accounts});
    const a = accounts.find(a=>u.pathname==='/api/v1/accounts/'+a.id);
    if (a) return send(200,a);
    if (u.pathname.endsWith('/connection')) return send(200,{accountId:u.pathname.includes(work)?work:account,state:'connected',updatedAt:chat.updatedAt});
    if (u.pathname.endsWith('/history')) return send(200,{accountId:account,state:'complete',retainedMessages:all.length,conversations:3,updatedAt:chat.updatedAt});
    if(u.pathname==='/api/v1/accounts/'+account+'/avatars')return send(200,{items:[{id:id(62),revisionId:id(63),accountId:account,address:chat.address,imageHash:conversationHash,updatedAt:chat.updatedAt}]});
    if(u.pathname.startsWith('/api/v1/avatars/')){
      const hash=u.pathname.split('/').at(-1),photo=hash===conversationHash?conversationPhoto:hash===senderHash?senderPhoto:null;
      if(!photo)return send(404,{code:'not_found'});
      res.setHeader('Content-Type','image/png');return res.end(photo);
    }
    if (u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews')||u.pathname.endsWith('/message-stars')||u.pathname==='/api/v1/shared-content') return send(200,{items:[]});
    if (u.pathname === '/api/v1/messages/search') {
      const accountIds=u.searchParams.getAll('accountIds').sort();
      state.searchReads.push({...Object.fromEntries(u.searchParams),accountIds});
      if (state.mode==='unavailable') return send(503,{code:'unavailable'});
      const q=u.searchParams.get('q')?.trim(), selectedAccount=u.searchParams.get('accountId')||'', conversation=u.searchParams.get('conversationId')||'', history=u.searchParams.get('includeEditHistory')==='true';
      const matchesScope=m=>(!selectedAccount||m.accountId===selectedAccount)&&(!accountIds.length||accountIds.includes(m.accountId))&&(!conversation||m.conversationId===conversation);
      const fingerprint=JSON.stringify([state.engine,q,selectedAccount,accountIds,conversation,history]);
      let offset=0;
      if (u.searchParams.has('cursor')) {try {const cursor=JSON.parse(Buffer.from(u.searchParams.get('cursor'),'base64url'));if(cursor.scope!==fingerprint)throw Error('scope');offset=cursor.offset}catch{return send(400,{code:'invalid_cursor'})}}
      let hits=corpus.filter(matchesScope).flatMap(m=>{
        const c=[...chats,archiveRoom].find(c=>c.id===m.conversationId),base={message:m,accountLabel:accounts.find(a=>a.id===m.accountId).label,conversationTitle:c.title,matchedAt:m.createdAt,matchKind:'text',...(m===corpus[54]?{senderAvatarHash:senderHash}:{})};
        const rows=fold(m.originalText).includes(fold(q))?[{...base,matchedRevisionId:m===corpus[1]?id(9001):m.revisionId,historical:false,excerpt:m.originalText}]:[];
        if(history&&m===corpus[0]&&fold('Pineapple original').includes(fold(q)))rows.push(...[2,1].map(n=>({...base,matchedRevisionId:id(8000+n),matchedAt:`2026-11-09T12:00:0${n}Z`,historical:true,excerpt:'Pineapple prior version '+n})));
        return rows;
      }).sort((a,b)=>new Date(b.message.createdAt).getTime()-new Date(a.message.createdAt).getTime()||b.message.id.localeCompare(a.message.id)||Number(a.historical)-Number(b.historical)||b.matchedAt.localeCompare(a.matchedAt));
      const limit=conversation===chat.id?Math.min(7,Number(u.searchParams.get('limit')||50)):Number(u.searchParams.get('limit')||50),items=hits.slice(offset,offset+limit);
      const indexedMessages=corpus.filter(matchesScope).length,totalMessages=all.filter(matchesScope).length;
      return send(200,{query:q,...(selectedAccount?{accountId:selectedAccount}:{}),...(accountIds.length?{accountIds}:{}),...(conversation?{conversationId:conversation}:{}),includeEditHistory:history,items,nextCursor:offset+limit<hits.length?Buffer.from(JSON.stringify({scope:fingerprint,offset:offset+limit})).toString('base64url'):undefined,metadataIndexing:{state:totalMessages===indexedMessages?'complete':'pending',indexedMessages,totalMessages}});
    }
    if(u.pathname.endsWith('/contacts')){state.contactReads.push(Object.fromEntries(u.searchParams));return send(404,{code:'unsupported'})}
    if(u.pathname==='/api/v1/conversations'&&u.searchParams.get('q'))state.titleReads.push(Object.fromEntries(u.searchParams));
    if (u.pathname === '/api/v1/conversations') {
      const accountIds=u.searchParams.getAll('accountIds');
      return send(200,{items:chats.filter(c=>(!u.searchParams.get('accountId')||c.accountId===u.searchParams.get('accountId'))&&(!accountIds.length||accountIds.includes(c.accountId))&&c.archived===(u.searchParams.get('archived')==='true')&&(!u.searchParams.get('q')||fold(c.title).includes(fold(u.searchParams.get('q'))))).slice(0,Number(u.searchParams.get('limit')||100))});
    }
    for (const c of [...chats,archiveRoom]) {
      const prefix='/api/v1/conversations/'+c.id, messages=chronological(c),limit=Number(u.searchParams.get('limit')||50);
      if (u.pathname===prefix) return send(200,c);
      if (c===archiveRoom&&u.pathname===prefix+'/archive')return send(200,archive);
      if (u.pathname===prefix+'/messages') {
        const cursor=u.searchParams.get('cursor'),index=cursor?messages.findIndex(m=>m.id===cursor):messages.length,direction=u.searchParams.get('direction')||'older';
        if(index<0)return send(404,{code:'not_found'});
        const start=direction==='newer'?index+1:Math.max(0,index-limit),end=direction==='newer'?Math.min(messages.length,start+limit):index,items=messages.slice(start,end).reverse();
        return send(200,{items,nextCursor:direction==='newer'?(end<messages.length?items[0]?.id:undefined):(start>0?items.at(-1)?.id:undefined)});
      }
      const context=/^\/api\/v1\/conversations\/[^/]+\/messages\/([^/]+)\/context$/.exec(u.pathname);
      if(context&&u.pathname.startsWith(prefix+'/')) {
        state.contexts.push({conversation:c.id,message:context[1],limit});
        const index=messages.findIndex(m=>m.id===context[1]);if(index<0)return send(404,{code:'not_found'});
        const start=Math.max(0,index-Math.floor(limit/2)),end=Math.min(messages.length,start+limit);
        return send(200,{targetId:messages[index].id,items:messages.slice(start,end).reverse(),hasOlder:start>0,hasNewer:end<messages.length});
      }
      if(u.pathname===prefix+'/details') return send(200,{conversationId:c.id,accountId:c.accountId,kind:'direct',address:c.address,state:'known',checkedAt:chat.updatedAt,accountConnection:{accountId:c.accountId,state:'connected',updatedAt:chat.updatedAt},participants:{scope:'peer',state:'unknown',items:[]},features:[]});
    }
    if(u.pathname.startsWith('/api/v1/messages/')){const m=all.find(m=>m.id===u.pathname.split('/').at(-1));return m?send(200,m):send(404,{code:'not_found'})}
    if(u.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
    send(404,{code:'not_found'});
  });
  api.listen(0,'127.0.0.1');await once(api,'listening');
  const allocator=http.createServer();allocator.listen(0,'127.0.0.1');await once(allocator,'listening');const port=allocator.address().port;await new Promise(r=>allocator.close(r));
  const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});
  let stderr='';child.stderr.on('data',c=>stderr+=c);
  const browser=await chromium.launch({headless:true}),base='http://127.0.0.1:'+port,errors=[],findings=[],screenshots=[];
  const until=async(fn,label)=>{for(let n=0;n<240;n++){if(await fn())return;await new Promise(r=>setTimeout(r,50))}throw Error('Timed out: '+label+'\n'+stderr)};
  const fresh=async(options={})=>{const context=await browser.newContext({viewport:{width:1440,height:950},locale:'en',...options}),page=await context.newPage();await page.addInitScript(()=>{document.addEventListener('oie:message-search-settled',event=>{const form=document.getElementById('message-search-form');window.searchSettled={q:form.elements.q.value.trim(),account:event.detail.root.dataset.searchAccount,conversation:form.elements.conversationId.value,history:form.elements.includeEditHistory.checked};});});page.on('pageerror',e=>errors.push(e.message));return page};
  const initial=()=>base+'/?chat='+chat.id+'&account='+account+'&lang=en&theme=dark';
  const hits=page=>page.locator('#message-search-results [data-message-search-hit]');
  const query=page=>page.locator('#conversation-search:visible,#message-search-query:visible');
  const form=page=>page.locator('#message-search-form');
  const settled=async(page,q)=>until(async()=>await page.evaluate(q=>{const last=window.searchSettled,form=document.getElementById('message-search-form');const selected=[...form.querySelectorAll('input[name=accountId]')].map(input=>input.value).sort().join(',');return last?.q===q&&last.account===selected&&last.conversation===form.elements.conversationId.value&&last.history===form.elements.includeEditHistory.checked},q),'ready query '+q);
  const openGlobal=async page=>{await query(page).waitFor({state:'visible'});await query(page).focus()};
  const chooseAccount=async(page,label)=>{
    const picker=page.locator('#message-search-account'),trigger=picker.getByRole('combobox'),clear=picker.locator('[data-combobox-clear-all]');
    await trigger.click();
    await picker.getByRole('listbox').waitFor({state:'visible'});
    if(await picker.locator('input[name=accountId]').count())await clear.click();
    if(label!=='All accounts')await picker.getByRole('option').filter({hasText:label}).click();
    await trigger.press('Escape');
    await picker.getByRole('listbox').waitFor({state:'hidden'});
  };
  let page;
  try {
    await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
    page=await fresh();await page.goto(initial()+'&searchMode=messages&searchQuery=%20&searchEngine='+state.engine);await page.locator('#message-composer').waitFor({state:'visible'});
    await page.waitForFunction(()=>window.OieRuntime?.generation>0);
    await page.locator('#conversation-list-default').waitFor({state:'visible'});
    assert.equal(new URL(page.url()).searchParams.has('searchMode'),false,'an old blank search address restores the inbox');
    // Full app fragments are parsed with scripting disabled. Native fallback
    // controls must not override the enhanced search's selected account after it.
    const beforeSectionSwap=await page.evaluate(()=>window.OieRuntime?.generation||0);
    await page.locator('nav a[href*="section=archived"]:visible').click();await page.waitForFunction(generation=>window.OieRuntime.generation>generation,beforeSectionSwap);
    const beforeInboxSwap=await page.evaluate(()=>window.OieRuntime.generation);
    await page.locator('nav').getByRole('link',{name:'Chats',exact:true}).click();await page.waitForFunction(generation=>window.OieRuntime.generation>generation,beforeInboxSwap);
    await page.locator('#message-composer').waitFor({state:'visible'});
    const draft='Retain this exact device draft 😀';await page.locator('#message-composer').fill(draft);
    await page.waitForFunction(()=>window.OieRuntime?.generation>0);await until(async()=>await page.locator('#message-timeline').evaluate(n=>n.scrollHeight-n.scrollTop-n.clientHeight<80),'initial timeline reaches latest before reading');
    const before=await page.evaluate(()=>{const timeline=document.getElementById('message-timeline');timeline.scrollTop=(timeline.scrollHeight-timeline.clientHeight)/2;document.getElementById('conversation-list').scrollTop=240;window.searchNodes={pane:document.getElementById('conversation-pane'),list:document.getElementById('conversation-list'),timeline,composer:document.getElementById('message-composer')};window.searchDocument=Math.random();return{token:window.searchDocument,generation:window.OieRuntime.generation,scroll:timeline.scrollTop,history:history.length,listScroll:document.getElementById('conversation-list').scrollTop,chat:new URL(location.href).searchParams.get('chat')}});
    const blankReads=state.searchReads.length,blankTitles=state.titleReads.length,blankContacts=state.contactReads.length;
    const field=query(page),list=page.locator('#conversation-list-default'),slot=page.locator('#message-search-slot');
    const searchBox=await field.boundingBox(),composerBox=await page.locator('#message-composer').boundingBox();
    await openGlobal(page);
    assert(await list.isVisible(),'keyboard focus keeps the conversation list visible');
    await field.click();
    assert(await list.isVisible(),'clicking the field keeps the conversation list visible');
    await field.fill('   ');await field.press('Enter');
    assert(await list.isVisible(),'submitting whitespace keeps the conversation list visible');
    assert(await slot.isHidden());
    assert.equal(new URL(page.url()).searchParams.has('searchMode'),false);
    assert.equal(await page.evaluate(()=>history.length),before.history);
    assert.deepEqual(await field.boundingBox(),searchBox);
    assert.deepEqual(await page.locator('#message-composer').boundingBox(),composerBox);
    assert.equal(state.searchReads.length,blankReads,'blank search does not query saved messages');
    assert.equal(state.titleReads.length,blankTitles,'blank search does not query conversation titles');
    assert.equal(state.contactReads.length,blankContacts,'blank search does not query protocol contacts');
    let releaseAvatar;
    const avatarPath='**/ui/avatars/'+senderHash;
    await page.route(avatarPath,async route=>{
      const response=await route.fetch();
      await new Promise(resolve=>releaseAvatar=resolve);
      await route.fulfill({response});
    });
    await query(page).fill('Telescope');await settled(page,'Telescope');
    const senderRow=page.locator('[data-search-message="'+corpus[54].id+'"]');
    const senderAvatar=senderRow.locator('.oie-search-avatar');
    await until(()=>releaseAvatar,'cached sender avatar request');
    await senderRow.evaluate(row=>{
      const nodes=[row,row.querySelector('.oie-search-avatar'),document.getElementById('message-composer')];
      const sample=()=>nodes.flatMap(node=>{const {x,y,width,height}=node.getBoundingClientRect();return [x,y,width,height]});
      const baseline=sample(),frames=[];
      let handle=requestAnimationFrame(function paint(){frames.push(sample());handle=requestAnimationFrame(paint)});
      window.finishAvatarGeometry=()=>{cancelAnimationFrame(handle);return {baseline,frames:[...frames,sample()]}};
    });
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    releaseAvatar();
    await until(()=>senderAvatar.locator('img').evaluate(image=>image.complete&&image.naturalWidth>0),'sender avatar renders');
    await senderAvatar.locator('img').waitFor({state:'visible'});
    await page.unroute(avatarPath);
    const avatarGeometry=await page.evaluate(()=>window.finishAvatarGeometry());
    assert(avatarGeometry.frames.length>1);
    assert(avatarGeometry.frames.every(frame=>frame.every((value,index)=>Math.abs(value-avatarGeometry.baseline[index])<=1)),'sender photo preserves result and composer geometry on every painted frame');
    assert.equal(await query(page).evaluate(node=>document.activeElement===node),true,'results retain typing focus');
    await chooseAccount(page,'Personal');await settled(page,'Telescope');assert.equal(await hits(page).count(),30);assert.equal(await page.locator('[data-message-search-contacts]').count(),0,'an unsupported contacts endpoint does not produce a fabricated contacts group');
    assert.equal(await page.locator('#message-search-pagination [data-message-search-indexing]').count(),1,'metadata coverage is explicit while indexing is incomplete');
    const inbox=new URL(page.url());assert.equal(inbox.searchParams.get('account'),account);assert.equal(inbox.searchParams.get('chat'),before.chat);assert.equal(inbox.searchParams.get('q'),null);assert.equal(inbox.searchParams.get('searchQuery'),'Telescope');
    assert.equal(await page.evaluate(()=>history.length),before.history+1,'typing replaces the single search history entry');
    assert.equal(await page.evaluate(()=>window.searchDocument),before.token);
    assert.equal(await page.evaluate(()=>window.OieRuntime.generation),before.generation);
    assert(await page.evaluate(()=>{const n=window.searchNodes;return n.pane===document.getElementById('conversation-pane')&&n.list===document.getElementById('conversation-list')&&n.timeline===document.getElementById('message-timeline')&&n.composer===document.getElementById('message-composer')}),'opening and searching preserve inbox and composer nodes');
    assert.equal(await page.locator('#message-composer').inputValue(),draft);assert.equal(await page.locator('#message-timeline').evaluate(n=>n.scrollTop),before.scroll);
    await page.locator('#message-search-scroll').evaluate(node=>node.scrollTop=node.scrollHeight);await until(async()=>await hits(page).count()===38,'second scoped page');
    assert.equal(new Set(await hits(page).evaluateAll(nodes=>nodes.map(n=>n.dataset.searchMessage))).size,38,'current results are not duplicated across pages');
    findings.push('Live search owns a separate URL/history scope, loads scoped pages, and preserves draft, inbox/timeline nodes, focus, stream runtime and scroll');

    const cancelSearch=page.getByRole('button',{name:'Cancel search',exact:true});
    const historyHelp=page.getByRole('button',{name:'Include edit history',exact:true});
    const historyTip=page.locator('#message-search-history-help');
    await cancelSearch.click();await page.locator('#conversation-list-default').waitFor({state:'visible'});
    assert(await page.evaluate(()=>window.searchNodes.list===document.getElementById('conversation-list')),'closing restores the same conversation-list node');assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),before.listScroll);assert.equal(await query(page).inputValue(),'','closing restores the original title filter');assert.equal(await page.locator('#message-composer').inputValue(),draft);
    await query(page).fill('Telescope');await settled(page,'Telescope');
    await chooseAccount(page,'All accounts');await settled(page,'Telescope');await until(async()=>await hits(page).count()===30,'global first page');
    await page.locator('#message-search-scroll').evaluate(node=>node.scrollTop=node.scrollHeight);await until(async()=>await hits(page).count()===57,'all-account second page');
    assert(await page.locator('[data-search-message="'+corpus[1].id+'"] [data-message-search-source]').count(),'current cancelled source with prior content revision renders normally');
    assert.equal(await page.locator('[data-search-message="'+corpus[1].id+'"]').getAttribute('data-search-historical'),'false');
    await query(page).fill('Family');await settled(page,'Family');assert.equal(await hits(page).count(),0);
    const conversationResult=page.locator('[data-message-search-conversation="'+chat.id+'"]');
    assert.equal(await conversationResult.count(),1,'conversation titles form a distinct group');
    const conversationAvatar=conversationResult.locator('img[src="/ui/avatars/'+conversationHash+'"]');
    await conversationAvatar.waitFor({state:'attached'});
    await until(()=>conversationAvatar.evaluate(image=>image.complete&&image.naturalWidth>0),'conversation thumbnail renders from the owning account');
    await conversationResult.locator('[data-avatar-ready="true"]').waitFor({state:'visible'});
    await query(page).fill('Telescope');await settled(page,'Telescope');
    await chooseAccount(page,'Work');await settled(page,'Telescope');await until(async()=>await hits(page).count()===19,'account scope before pagination');
    assert((await hits(page).evaluateAll(nodes=>nodes.map(n=>n.textContent))).every(text=>text.includes('Work')));
    await chooseAccount(page,'All accounts');await query(page).fill('CAFÉ 😀');await settled(page,'CAFÉ 😀');assert.equal(await hits(page).count(),1);assert.equal(await page.evaluate(()=>window.searchInjection),undefined);assert.equal(await hits(page).locator('img').count(),0);
    assert.equal((await hits(page).locator('.oie-search-avatar').textContent()).trim(),'P','a sender without a retained photo has initials');
    const readCount=state.searchReads.length;await field.fill('');
    assert(await list.isVisible(),'clearing the query restores the conversation list');
    assert(await slot.isHidden());
    assert.equal(await hits(page).count(),0);
    assert.equal(await field.evaluate(node=>node===document.activeElement),true,'clearing retains search focus');
    assert.equal(new URL(page.url()).searchParams.has('searchMode'),false);
    assert.equal(state.searchReads.length,readCount,'clearing does not send an invalid blank API query');
    findings.push('Global/account filters apply before pagination, distinct transport revisions stay current, Unicode matches and hostile excerpts remain inert text');

    await query(page).fill('Pineapple');await settled(page,'Pineapple');assert.equal(await hits(page).count(),0);
    await form(page).locator('[name="includeEditHistory"]').check();await settled(page,'Pineapple');await until(async()=>await hits(page).count()===2,'retained content revisions');
    assert.equal(await hits(page).filter({hasText:'Prior version'}).count(),2);
    assert((await hits(page).evaluateAll(nodes=>nodes.map(n=>n.dataset.searchMessage))).every(value=>value===corpus[0].id));
    const source=hits(page).first().locator('[data-message-search-source]'),sourceURL=new URL(await source.getAttribute('href'),base);assert.equal(sourceURL.searchParams.get('message'),corpus[0].id);
    const historicalReturnURL=page.url();await source.click();await page.locator('[data-message-id="'+corpus[0].id+'"]').waitFor({state:'attached'});
    assert.equal(new URL(page.url()).searchParams.get('message'),corpus[0].id);assert((await page.locator('[data-message-id="'+corpus[0].id+'"]').textContent()).includes(corpus[0].originalText));
    assert(state.contexts.some(c=>c.message===corpus[0].id&&c.limit<=50),'historical match opens only the bounded current canonical source context');
    assert((await page.locator('#message-timeline [data-message-id]').count())<=50);assert.equal(await page.locator('#message-composer').inputValue(),draft);
    const beforeSearchRestore=await page.evaluate(()=>window.OieRuntime.generation);
    await page.goBack();await page.waitForURL(historicalReturnURL);
    await page.waitForFunction(generation=>window.OieRuntime.generation>generation&&!document.body.dataset.focusMessage,beforeSearchRestore);
    await query(page).waitFor({state:'visible'});await until(async()=>await hits(page).count()===2,'restored historical search');assert.equal(await query(page).inputValue(),'Pineapple');assert(await form(page).locator('[name="includeEditHistory"]').isChecked());assert.equal(await hits(page).count(),2);
    findings.push('Edit history is explicit and labeled; prior-content hits navigate to the bounded canonical current source and Back restores the search context');

    // The global list search persists outside conversation swaps. Source navigation must not
    // permanently suppress subsequent close cleanup or leave a hidden paginator.
    const secondSource=hits(page).first().locator('[data-message-search-source]'),beforeSecondSource=await page.evaluate(()=>window.OieRuntime.generation);
    await secondSource.click();await page.waitForURL(url=>url.searchParams.get('message')===corpus[0].id);await page.waitForFunction(generation=>window.OieRuntime.generation>generation,beforeSecondSource);
    await openGlobal(page);
    let closeRelease,closeHeld=false,closedDone;const closedResponse=new Promise(resolve=>closedDone=resolve);
    await page.route('**/ui/message-search?**',async route=>{if(new URL(route.request().url()).searchParams.get('q')==='Telescope'){const response=await route.fetch();closeHeld=true;await new Promise(resolve=>closeRelease=resolve);await route.fulfill({response}).catch(()=>{});closedDone();return}await route.continue()});
    await query(page).fill('Telescope');await until(()=>closeHeld,'pending query after reopening source global list search');
    await historyHelp.click();await historyTip.waitFor({state:'visible'});
    await page.evaluate(()=>{
      const nodes=['conversation-search','message-composer','message-timeline'].map(id=>document.getElementById(id));
      const sample=()=>nodes.flatMap(node=>{const {x,y,width,height}=node.getBoundingClientRect();return [x,y,width,height,node.scrollTop]});
      const baseline=sample(),frames=[];
      let handle=requestAnimationFrame(function paint(){frames.push(sample());handle=requestAnimationFrame(paint)});
      window.finishCancelGeometry=()=>{cancelAnimationFrame(handle);return {baseline,frames:[...frames,sample()]}};
    });
    await cancelSearch.click();await page.locator('#message-search-slot').waitFor({state:'hidden'});
    await page.waitForFunction(()=>!document.getElementById('message-search-history-help').matches(':popover-open'));
    await until(async()=>new URL(page.url()).searchParams.get('searchMode')===null,'closing a reopened search restores the conversation-list route');const readsAtClose=state.searchReads.length;
    closeRelease();await closedResponse;await page.unroute('**/ui/message-search?**');await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    const {baseline,frames}=await page.evaluate(()=>window.finishCancelGeometry());
    assert(frames.length>1,'cancel geometry includes painted frames');
    assert(frames.every(frame=>frame.every((value,index)=>Math.abs(value-baseline[index])<=1)),'cancel and a late response preserve field, composer and timeline geometry');
    assert(await field.evaluate(node=>node===document.activeElement),'cancelling restores focus to the search field');
    assert.equal(state.searchReads.length,readsAtClose,'closed global list search cannot continue background pagination');assert.equal(await hits(page).count(),0,'closed pending response cannot resurrect results');
    await openGlobal(page);await query(page).fill('CAFÉ 😀');await settled(page,'CAFÉ 😀');assert.equal(await hits(page).count(),1);
    findings.push('After a source jump, reopening and closing cancels the pending read, clears the overlay route, rejects the late response and prevents hidden pagination; reopening works again');

    let sourceHeld=false,sourceRelease,sourceReleased;const pendingSource=new Promise(resolve=>sourceReleased=resolve);
    await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('message')===corpus[2].id){const response=await route.fetch();sourceHeld=true;await new Promise(resolve=>sourceRelease=resolve);await route.fulfill({response}).catch(()=>{});sourceReleased();return}await route.continue()});
    await historyHelp.click();await historyTip.waitFor({state:'visible'});
    await hits(page).first().locator('[data-message-search-source]').press('Enter');await until(()=>sourceHeld,'held canonical source fragment');
    await historyTip.waitFor({state:'hidden'});
    await openGlobal(page);await query(page).fill('Telescope');await chooseAccount(page,'Work');await settled(page,'Telescope');assert.equal(await hits(page).count(),19);
    sourceRelease();await pendingSource;await page.unroute('**/*');await until(async()=>await query(page).isVisible()&&await query(page).inputValue()==='Telescope'&&await form(page).locator('[name=accountId]').inputValue()===work&&await hits(page).count()===19,'latest search intent survives the older source fragment');
    assert.equal(await page.locator('#message-composer').inputValue(),draft);assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'Telescope');assert.equal(new URL(page.url()).searchParams.get('searchAccount'),work);
    findings.push('A held source fragment cannot erase a later reopened search query/account intent or its device draft');

    await chooseAccount(page,'All accounts');await settled(page,'Telescope');
    // A genuine failed response must expose an explicit in-shell recovery.
    state.mode='unavailable';await query(page).fill('failure');await page.locator('[data-message-search-retry]:visible').first().waitFor({state:'visible'});assert.equal(await query(page).inputValue(),'failure');
    state.mode='normal';await page.locator('[data-message-search-retry]:visible').first().click();await settled(page,'failure');assert.equal(await hits(page).count(),0);
    await page.route('**/ui/message-search?**',route=>new URL(route.request().url()).searchParams.get('q')==='transport'?route.abort('failed'):route.continue());
    await query(page).fill('transport');await page.locator('button[data-message-search-retry]:visible').waitFor({state:'visible'});assert.equal(await query(page).inputValue(),'transport');
    await page.unroute('**/ui/message-search?**');await page.locator('button[data-message-search-retry]:visible').click();await settled(page,'transport');assert.equal(await hits(page).count(),0);
    let release,held=false,released;const releaseDone=new Promise(resolve=>released=resolve);
    await page.route('**/ui/message-search?**',async route=>{if(new URL(route.request().url()).searchParams.get('q')==='Telescope'){const response=await route.fetch();held=true;await new Promise(r=>release=r);await route.fulfill({response}).catch(()=>{});released();return}await route.continue()});
    await query(page).fill('Telescope');await until(()=>held,'held older response');await query(page).fill('CAFÉ 😀');await settled(page,'CAFÉ 😀');release();await releaseDone;await page.unroute('**/ui/message-search?**');
    assert.equal(await hits(page).count(),1);assert.equal(await page.evaluate(()=>window.searchSettled?.q),'CAFÉ 😀');
    findings.push('HTTP and transport failures recover through visible retry actions; an older held response cannot overwrite a newer query');

    let releaseClearedSource;
    await page.route('**/*',async route=>{
      const url=new URL(route.request().url());
      if(url.origin!==base||url.pathname!=='/'||url.searchParams.get('message')!==corpus[2].id)return route.continue();
      const response=await route.fetch();
      await new Promise(resolve=>releaseClearedSource=resolve);
      await route.fulfill({response});
    });
    const generationBeforeClear=await page.evaluate(()=>window.OieRuntime.generation);
    await hits(page).first().locator('[data-message-search-source]').click();
    await until(()=>releaseClearedSource,'held source before clearing global search');
    await field.fill('');
    releaseClearedSource();
    await page.waitForFunction(generation=>window.OieRuntime.generation>generation,generationBeforeClear);
    await page.unroute('**/*');
    assert(await list.isVisible(),'an older source fragment cannot reopen a cleared search');
    assert.equal(new URL(page.url()).searchParams.has('searchMode'),false);
    await field.fill('CAFÉ 😀');await settled(page,'CAFÉ 😀');await until(async()=>await hits(page).count()===1,'reopen search after clearing during source navigation');

    const globalSearchNodes=await page.evaluate(()=>{window.concurrentSearchNodes={input:document.getElementById('conversation-search'),results:document.getElementById('message-search-results'),composer:document.getElementById('message-composer')};return{query:document.getElementById('conversation-search').value}});
    await page.locator('#chat-options button').first().click();await page.locator('#chat-search-messages:visible').click();
    const chatSearch=page.locator('[data-conversation-search]'),chatQuery=page.locator('#conversation-search-query');await chatQuery.waitFor({state:'visible'});await page.locator('#chat-options [role=menu]').waitFor({state:'hidden'});
    const orderedMatches=corpus.filter(message=>message.conversationId===chat.id&&message.originalText.includes('Telescope')).sort((a,b)=>new Date(b.createdAt).getTime()-new Date(a.createdAt).getTime()||b.id.localeCompare(a.id));
    const selected=()=>chatSearch.getAttribute('data-search-selected');
    await chatQuery.fill('Telescope');await until(async()=>await selected()===orderedMatches[0].id,'per-chat search starts at newest matching source');
    assert.equal(await chatQuery.evaluate(node=>document.activeElement===node),true,'initial chat search preserves typing focus');
    assert.equal(await query(page).inputValue(),globalSearchNodes.query);assert.equal(await hits(page).count(),1,'global results remain independently scoped');
    assert(await page.evaluate(()=>window.concurrentSearchNodes.input===document.getElementById('conversation-search')&&window.concurrentSearchNodes.results===document.getElementById('message-search-results')&&window.concurrentSearchNodes.composer===document.getElementById('message-composer')),'chat search preserves global results and composer nodes');
    assert(await chatSearch.locator('[data-conversation-search-newer]').isDisabled(),'newest matching source has no newer match');
    assert.equal(await chatSearch.getAttribute('data-search-complete'),'false');assert.equal(await chatSearch.getAttribute('data-search-loaded'),'7');assert(!/\/\s*7\b/.test(await chatSearch.locator('[data-conversation-search-status]').textContent()),'a loaded page count is not presented as the exact total');
    const readsBeforeOlder=state.searchReads.length;
    for(let index=0;index<orderedMatches.length;index++){
      if(index){if(index===1){await chatSearch.locator('[data-conversation-search-older]').focus();await page.keyboard.press('Enter')}else await chatSearch.locator('[data-conversation-search-older]').click();await until(async()=>await selected()===orderedMatches[index].id,'older matching source '+index);if(index===1)assert(await chatSearch.evaluate(node=>node.contains(document.activeElement)),'keyboard result navigation retains focus in the search toolbar')}
      assert.equal(await chatSearch.getAttribute('data-search-index'),String(index));assert.equal(new URL(page.url()).searchParams.get('message'),orderedMatches[index].id);
      await page.locator('[data-message-id="'+orderedMatches[index].id+'"].oie-conversation-search-selected').waitFor({state:'attached'});
      assert((await page.locator('#message-timeline [data-message-id]').count())<=200,'search navigation keeps the bounded timeline DOM');
      assert.equal(await query(page).inputValue(),globalSearchNodes.query);assert.equal(await page.locator('#message-composer').inputValue(),draft);
    }
    assert.equal(await chatSearch.getAttribute('data-search-complete'),'true');assert.equal(await chatSearch.getAttribute('data-search-loaded'),String(orderedMatches.length));assert(await chatSearch.locator('[data-conversation-search-older]').isDisabled(),'oldest matching source does not wrap');
    assert(state.searchReads.slice(readsBeforeOlder).filter(read=>read.conversationId===chat.id&&read.cursor).length>=2,'older navigation loads subsequent short scoped search pages');
    await chatSearch.locator('[data-conversation-search-newer]').click();await until(async()=>await selected()===orderedMatches.at(-2).id,'newer matching source moves back one hit');
    assert((await page.locator('mark[data-conversation-search-highlight]:visible').count())>0,'current literal matching text is highlighted');
    await chatQuery.fill('Pineapple');await chatSearch.locator('#conversation-search-history').check();await until(async()=>await selected()===corpus[0].id&&await chatSearch.getAttribute('data-search-loaded')==='2','chat edit-history search');
    assert((await chatSearch.locator('[data-conversation-search-excerpt]').textContent()).includes('Pineapple'));assert.equal(await page.locator('[data-message-id="'+corpus[0].id+'"] mark[data-conversation-search-highlight]').count(),0,'prior content is not invented as a highlight in the current bubble');
    let chatHeld=false,chatRelease,chatReleased;const chatStaleResponse=new Promise(resolve=>chatReleased=resolve);
    await page.route('**/ui/message-search?**',async route=>{const url=new URL(route.request().url());if(url.searchParams.get('q')==='Telescope'&&url.searchParams.get('conversationId')===chat.id){const response=await route.fetch();chatHeld=true;await new Promise(resolve=>chatRelease=resolve);await route.fulfill({response}).catch(()=>{});chatReleased();return}await route.continue()});
    const sourceBeforeStale=await page.locator('body').getAttribute('data-focus-message');await chatQuery.fill('Telescope');await until(()=>chatHeld,'held old chat-search query');await chatQuery.fill('No matching source');await until(async()=>await chatSearch.getAttribute('data-search-complete')==='true'&&await chatSearch.getAttribute('data-search-loaded')==='0','new empty chat query');chatRelease();await chatStaleResponse;await page.unroute('**/ui/message-search?**');assert.equal(await chatQuery.inputValue(),'No matching source');assert.equal(await chatSearch.getAttribute('data-search-loaded'),'0');assert.equal(await page.locator('body').getAttribute('data-focus-message'),sourceBeforeStale,'old chat query cannot refocus its source');
    state.mode='unavailable';await chatQuery.fill('Chat transport failure');await chatSearch.locator('[data-conversation-search-retry]').waitFor({state:'visible'});assert.equal(await query(page).inputValue(),globalSearchNodes.query);state.mode='normal';await chatSearch.locator('[data-conversation-search-retry]').click();await until(async()=>await chatSearch.getAttribute('data-search-complete')==='true'&&await chatSearch.getAttribute('data-search-loaded')==='0','chat search retry recovers without a synthetic match');
    await chatSearch.locator('[data-conversation-search-close]').click();await chatSearch.waitFor({state:'hidden'});
    await until(async()=>await query(page).inputValue()===globalSearchNodes.query&&await hits(page).count()===1,'closing chat search restores the independently scoped global results');
    assert.equal(await page.locator('body').getAttribute('data-focus-message'),corpus[0].id,'closing chat search keeps its selected source');
    findings.push('Global and per-chat queries coexist independently; next/previous traverse scoped pages without wrapping, preserve draft/global DOM/focus, highlight only current literal content and keep timeline bounded to200 rows');

    const native=await fresh({javaScriptEnabled:false});await native.goto(base+'/search?q=Telescope&accountId='+work+'&lang=en&theme=light');assert.equal(await hits(native).count(),19);assert.equal(await native.locator('form[role="search"] input[name="q"]').inputValue(),'Telescope');
    await native.goto(initial()+'&searchMode=messages&searchQuery=%20');
    assert(await native.locator('#conversation-list-default').isVisible(),'server renders the inbox for a blank global search');
    assert(await native.locator('#message-search-slot').isHidden());
    await native.goto(base+'/search?'+new URLSearchParams([['q','Account scope marker'],['accountIds',account],['accountIds',work],['lang','en']]));
    assert.equal(await hits(native).count(),2);
    await native.locator('#message-search-native-'+account).uncheck();
    await native.locator('#message-search-native-'+work).uncheck();
    await native.getByRole('button',{name:'Search messages',exact:true}).click();
    await native.waitForURL(url=>url.searchParams.has('nativeAccountFilter'));
    assert.equal(await hits(native).count(),3,'clearing the native checkboxes searches all accounts');
    await native.context().close();

    const multi=await fresh();
    await multi.goto(initial());
    await multi.waitForFunction(()=>window.OieRuntime?.generation>0);
    await query(multi).fill('Account scope marker');await settled(multi,'Account scope marker');
    const picker=multi.locator('#message-search-account'),trigger=picker.getByRole('combobox');
    const option=id=>picker.locator('[data-combobox-option][data-value="'+id+'"]');
    await trigger.click();
    let pickerSearchReads=0;
    multi.on('request',request=>{if(request.url().includes('/ui/message-search?'))pickerSearchReads++});
    const pickerSearch=picker.locator('[data-combobox-search]');
    await pickerSearch.fill('work@fixture');
    assert(await option(work).isVisible());
    assert(await option(account).isHidden());
    assert.equal(pickerSearchReads,0,'typing in the combobox filters its options only');
    await option(work).click();await settled(multi,'Account scope marker');
    assert(await option(work).locator('input[type=checkbox]').isChecked());
    assert.equal(await trigger.getAttribute('aria-expanded'),'true','selecting one account keeps the picker open');
    await pickerSearch.fill('Personal');
    assert(await option(account).isVisible());
    const fieldBounds=await query(multi).boundingBox(),composerBounds=await multi.locator('#message-composer').boundingBox();
    await option(account).click();await settled(multi,'Account scope marker');
    assert(await option(account).locator('input[type=checkbox]').isChecked());
    assert(await option(work).locator('input[type=checkbox]').isChecked());
    assert.equal(await trigger.innerText(),'2 accounts selected');
    assert.deepEqual(new URL(multi.url()).searchParams.getAll('searchAccount'),[account,work]);
    assert.deepEqual(await hits(multi).evaluateAll(nodes=>nodes.map(node=>node.dataset.searchMessage)),[scopeMessages[1].id,scopeMessages[0].id]);
    assert.deepEqual(await query(multi).boundingBox(),fieldBounds);
    assert.deepEqual(await multi.locator('#message-composer').boundingBox(),composerBounds);
    await trigger.press('Escape');await picker.getByRole('listbox').waitFor({state:'hidden'});
    const selectedURL=multi.url(),sourceId=scopeMessages[1].id;
    await hits(multi).first().locator('[data-message-search-source]').click();
    await multi.waitForURL(url=>url.searchParams.get('message')===sourceId);
    await multi.goBack();await multi.waitForURL(selectedURL);
    await until(async()=>await hits(multi).count()===2,'Back restores both selected accounts');
    assert.deepEqual(await form(multi).locator('input[name=accountId]').evaluateAll(inputs=>inputs.map(input=>input.value).sort()),[account,work]);
    await chooseAccount(multi,'All accounts');await settled(multi,'Account scope marker');
    assert.equal(await hits(multi).count(),3);
    assert.equal(await trigger.innerText(),'All accounts');
    await multi.context().close();
    findings.push('Searchable account checkboxes preserve selected accounts and query, apply before pagination, restore via Back, and clear to all accounts');

    await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});
    for(const width of [390,1440])for(const design of ['goshtoso','minimal'])for(const theme of ['light','dark']){
      const variant=await fresh({viewport:{width,height:900},locale:width===390?'pt-BR':'en',hasTouch:width===390}),lang=width===390?'pt-BR':'en';
      await variant.goto(base+'/?account='+work+'&lang='+lang+'&theme='+theme);await variant.waitForFunction(()=>window.OieRuntime?.generation>0);
      await variant.evaluate(design=>document.documentElement.dataset.theme=design,design);
      if(width===390)await query(variant).tap();
      else await query(variant).click();
      assert(await variant.locator('#conversation-list-default').isVisible());
      const cancel=variant.getByRole('button',{name:lang==='pt-BR'?'Cancelar pesquisa':'Cancel search',exact:true});
      assert(await cancel.isHidden(),'cancel appears only for an active search');
      const idleField=await query(variant).boundingBox();
      await query(variant).fill('Telescope');await settled(variant,'Telescope');await chooseAccount(variant,'Work');await settled(variant,'Telescope');
      const help=variant.getByRole('button',{name:lang==='pt-BR'?'Incluir histórico de edições':'Include edit history',exact:true});
      const tip=variant.locator('#message-search-history-help'),outside=variant.locator('.oie-list-heading h2');
      assert(await tip.isHidden());
      if(width===390)await help.tap();
      else await help.click();
      await tip.waitFor({state:'visible'});
      assert.equal(await help.getAttribute('aria-expanded'),'true');
      if(width===390)await outside.tap();
      else await outside.click();
      await tip.waitFor({state:'hidden'});
      assert.equal(await help.getAttribute('aria-expanded'),'false');
      if(width===1440){
        await help.press('Enter');await tip.waitFor({state:'visible'});
        await help.press('Escape');await tip.waitFor({state:'hidden'});
        assert(await help.evaluate(node=>node===document.activeElement));
      }
      assert.equal(await query(variant).inputValue(),'Telescope');
      assert.equal(await variant.locator('#message-search-history').isChecked(),false);
      await hits(variant).first().waitFor({state:'visible'});await variant.evaluate(design=>document.documentElement.dataset.theme=design,design);assert.equal(await variant.locator('html').getAttribute('data-theme'),design);
      if(width===1440){await variant.locator('#chat-options button').first().click();await variant.locator('#chat-search-messages:visible').click();await variant.locator('#chat-options [role=menu]').waitFor({state:'hidden'});await variant.locator('#conversation-search-query').fill(corpus[55].originalText);await until(async()=>await variant.locator('[data-conversation-search]').getAttribute('data-search-selected')===corpus[55].id,'independent desktop queries in '+design+'/'+theme);assert.equal(await query(variant).inputValue(),'Telescope');assert.equal(await hits(variant).count(),19)}
      const activeField=await query(variant).boundingBox(),cancelBox=await cancel.boundingBox();
      assert.deepEqual(activeField,idleField,'opening search does not move or resize the field');
      assert(cancelBox.x>activeField.x+activeField.width/2&&cancelBox.x+cancelBox.width<=activeField.x+activeField.width&&cancelBox.y>=activeField.y&&cancelBox.y+cancelBox.height<=activeField.y+activeField.height,'cancel is inside the right side of the field');
      assert(cancelBox.width>=24&&cancelBox.height>=24,'cancel has a touch target of at least 24px');
      await query(variant).focus();await variant.keyboard.press('Tab');assert(await cancel.evaluate(node=>node===document.activeElement),'cancel is the next keyboard stop');
      assert(await variant.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'search has no horizontal page overflow '+width+'/'+design+'/'+theme);
      await variant.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const audit=await variant.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(audit.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[]);
      const screenshot='message-search-'+width+'-'+design+'-'+theme+'.png';await variant.screenshot({path:path.join(__dirname,'artifacts',screenshot),fullPage:true});screenshots.push({width,design,theme,lang,screenshot,axe:audit.violations.length});
      if(width===390)await cancel.tap();
      else await variant.keyboard.press('Enter');
      assert(await variant.locator('#conversation-list-default').isVisible());
      assert(await cancel.isHidden());
      assert(await query(variant).evaluate(node=>node===document.activeElement));
      await variant.context().close();
    }
    for(const width of [390,1440]){
      const lang=width===390?'pt-BR':'en',roomPage=await fresh({viewport:{width,height:900},locale:lang,reducedMotion:'reduce'});
      await roomPage.goto(base+'/?chat='+archiveRoom.id+'&account='+account+'&lang='+lang+'&theme=dark&chatSearch=1');
      const roomSearch=roomPage.locator('[data-conversation-search]'),field=roomSearch.locator('#conversation-search-query');
      await field.waitFor({state:'visible'});
      await roomPage.waitForFunction(()=>window.OieRuntime?.generation>0);
      releaseSearch=null;
      await field.fill('Archive keyword');
      await until(async()=>await roomSearch.getAttribute('data-search-selected')===savedMatch.id,'local room search before remote completion');
      await until(()=>typeof releaseSearch==='function','debounced remote room search');
      await roomPage.evaluate(()=>{
        const timeline=document.getElementById('message-timeline'),composer=document.getElementById('send-message-form');
        const measure=()=>[timeline,composer].filter(Boolean).flatMap(node=>{const r=node.getBoundingClientRect();return [r.top,r.height]});
        window.archiveFrames={start:measure(),delta:0,active:true};
        const frame=()=>{const sample=window.archiveFrames;if(!sample.active)return;sample.delta=Math.max(sample.delta,...measure().map((v,i)=>Math.abs(v-sample.start[i])));requestAnimationFrame(frame)};
        requestAnimationFrame(frame);
      });
      releaseSearch();
      await until(async()=>await roomSearch.getAttribute('data-search-loaded')==='2','remote merge deduplicates the existing local hit');
      assert.equal(await roomSearch.getAttribute('data-search-selected'),savedMatch.id);
      assert.equal(await field.evaluate(input=>document.activeElement===input),true);
      await roomPage.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
      const delta=await roomPage.evaluate(()=>{window.archiveFrames.active=false;return window.archiveFrames.delta});
      assert(delta<=1,'late remote search response moves layout by '+delta+'px at '+width);
      await roomSearch.locator('[data-conversation-search-older]').click();
      await until(async()=>await roomSearch.getAttribute('data-search-selected')===remoteMatch.id,'remote result opens its context');
      assert(archiveRequests.some(r=>r.action==='context'&&r.messageId===remoteMatch.id));
      const previous=archiveRequests.length;
      await field.fill('Discarded term 1');
      await field.fill('Discarded term 2');
      await field.fill('Latest room term');
      await until(()=>archiveRequests.slice(previous).some(r=>r.query==='Latest room term'),'latest query reaches remote archive');
      assert.deepEqual(archiveRequests.slice(previous).map(r=>r.query),['Latest room term']);
      await roomPage.locator('[data-open-details]').click();
      const toggle=roomPage.getByRole('switch',{name:lang==='en'?'Download older history automatically':'Baixar histórico antigo automaticamente'});
      const help=roomPage.getByRole('button',{name:lang==='en'?'Older history':'Histórico antigo',exact:true});
      const initial=archive.mode==='gradual',changes=archiveModes.length;
      assert.equal(await toggle.isChecked(),initial);
      await help.click();
      const tip=roomPage.getByRole('tooltip').filter({hasText:lang==='en'?'older history downloads gradually':'o histórico antigo é baixado aos poucos'});
      await tip.waitFor({state:'visible'});
      const tipBounds=await tip.boundingBox();
      assert(tipBounds&&tipBounds.x>=0&&tipBounds.x+tipBounds.width<=width+1,'archive help fits the viewport');
      await help.click();
      await tip.waitFor({state:'hidden'});
      assert.equal(await toggle.isChecked(),initial);
      assert.equal(archiveModes.length,changes);
      await toggle.focus();
      await roomPage.evaluate(()=>{
        const row=document.querySelector('.oie-room-archive-setting');
        const measure=()=>{const {x,y,width,height}=row.getBoundingClientRect();return [x,y,width,height]};
        const before=measure(),sample=window.archiveModeFrames={delta:0};
        const frame=()=>{sample.delta=Math.max(sample.delta,...measure().map((value,i)=>Math.abs(value-before[i])));sample.id=requestAnimationFrame(frame)};
        sample.id=requestAnimationFrame(frame);
      });
      await toggle.press('Space');
      await until(()=>archiveModes.length===changes+1&&typeof finishArchiveMode==='function','pending archive mode');
      assert.equal(archiveModes.at(-1),initial?'on_demand':'gradual');
      assert.equal(await toggle.getAttribute('aria-disabled'),'true');
      await toggle.press('Space');
      assert.equal(await toggle.isChecked(),!initial);
      assert.equal(await toggle.evaluate(node=>node===document.activeElement),true);
      assert.equal(archiveModes.length,changes+1);
      finishArchiveMode(true);finishArchiveMode=null;
      await until(async()=>await toggle.getAttribute('aria-disabled')===null,'archive mode confirmed');
      await toggle.press('Space');
      await until(()=>archiveModes.length===changes+2&&typeof finishArchiveMode==='function','pending archive mode refusal');
      finishArchiveMode(false);finishArchiveMode=null;
      await until(async()=>await toggle.getAttribute('aria-disabled')===null&&await toggle.isChecked()===!initial,'archive mode rolls back on refusal');
      const toast=roomPage.locator('[data-toast-container]').getByText(lang==='en'?'Could not save the history preference. Try again.':'Não foi possível salvar a preferência de histórico. Tente novamente.',{exact:true});
      await toast.waitFor({state:'visible'});
      const switchShift=await roomPage.evaluate(()=>{cancelAnimationFrame(window.archiveModeFrames.id);return window.archiveModeFrames.delta});
      assert(switchShift<=1,'archive switch moved by '+switchShift+'px at '+width);
      const reopen=new URL(roomPage.url());reopen.searchParams.set('open','details');
      await roomPage.goto(reopen.href);
      await toggle.waitFor({state:'attached'});
      assert.equal(await toggle.isChecked(),!initial);
      await roomPage.context().close();
    }
    const historyPage=await fresh();
    await historyPage.goto(base+'/?chat='+archiveRoom.id+'&account='+account+'&lang=en');
    await historyPage.waitForFunction(()=>window.OieTimeline?.size>0);
    const roomTimeline=historyPage.locator('#message-timeline');
    await roomTimeline.evaluate(node=>{node.scrollTop=0;node.dispatchEvent(new WheelEvent('wheel',{deltaY:-80,bubbles:true}))});
    await roomTimeline.locator('[data-message-id="'+olderRoomMessage.id+'"]').waitFor({state:'attached'});
    assert(archiveRequests.some(r=>r.action==='older'&&r.messageId===remoteMatch.id));
    assert((await roomTimeline.locator('[data-message-id]').count())<=200);
    await historyPage.context().close();
    findings.push('Room search returns local hits before an outstanding server request and merges matching sources without moving the timeline or composer');
    findings.push('Room archive switch preserves focus and geometry, saves the selected mode and restores it on refusal. Its help opens independently');
    state.engine=id(99);await query(page).fill('switch');await until(async()=>await page.locator('#message-search-feedback').textContent().then(text=>text.includes('engine')||text.includes('Engine')),'engine change is visible');assert.equal(await hits(page).count(),0);assert.equal(await query(page).inputValue(),'switch','engine rejection preserves the latest typed search');assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'switch');const readsBeforeRebind=state.searchReads.length;await page.locator('button[data-message-search-restart]:visible').click();await page.waitForURL(url=>url.pathname==='/search');await until(async()=>await form(page).locator('[name=engineId]').inputValue()===id(99),'explicit restart binds the replacement engine');assert.equal(await query(page).inputValue(),'switch');assert(state.searchReads.length>readsBeforeRebind,'only explicit recovery queries the replacement engine');state.engine=id(1);
    findings.push('Engine replacement clears old results and requires explicit restart before rebinding');

    assert.equal(state.writes,0,'message search never writes read state or causes protocol effects');assert.deepEqual(errors,[]);
    await fs.writeFile(path.join(__dirname,'artifacts/message-search-report.json'),JSON.stringify({findings,screenshots,searchRequests:state.searchReads,titleRequests:state.titleReads,contactRequests:state.contactReads,writes:state.writes,contexts:state.contexts,pageErrors:errors},null,2));
    console.log('PASS: retained message search scopes/history/pagination, bounded canonical jump, stale-response and failure recovery, preserved workspace, native and localized accessible layouts');
  } catch(error) {if(page&&!page.isClosed())console.error('Search failure evidence',JSON.stringify(await page.evaluate(()=>({url:location.href,feedback:document.querySelector('#message-search-feedback')?.textContent,search:document.querySelector('[data-message-search-root]')?.innerText?.slice(0,3000),chatSearch:document.querySelector('[data-conversation-search]')?.dataset,chatStatus:document.querySelector('[data-conversation-search-status]')?.textContent,active:document.activeElement?.outerHTML?.slice(0,500)})).catch(()=>({}))));throw error}
  finally {await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(error=>{console.error(error);process.exitCode=1});
