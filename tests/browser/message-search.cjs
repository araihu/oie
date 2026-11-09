// Independent read-only engine fixture. Real SQLite search is checked separately
// by message_search_test.go and the development-XMPP browser workflow.
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require('playwright');
const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
(async () => {
  const account = id(2), work = id(3), streams = new Set();
  const state = {engine:id(1), writes:0, searchReads:[], titleReads:[], contactReads:[], contexts:[], mode:'normal'};
  const accounts = [
    {id:account,address:'alice@fixture',label:'Personal'},
    {id:work,address:'work@fixture',label:'Work'},
  ].map(a => ({...a,revisionId:id(4),protocol:'xmpp',connectionState:'connected',createdAt:'2026-11-09T12:00:00Z'}));
  const chats = [
    {id:id(10),accountId:account,title:'Family'},
    {id:id(11),accountId:work,title:'Team'},
    {id:id(12),accountId:account,title:'Other conversation'},
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
  const all = [...corpus,...fillers];
  const chronological = c => all.filter(m => m.conversationId===c.id).sort((a,b)=>a.createdAt.localeCompare(b.createdAt)||a.id.localeCompare(b.id));
  const fold = value => value.normalize('NFD').replace(/\p{M}/gu,'').toLowerCase();
  const api = http.createServer(async (req,res) => {
    const u = new URL(req.url,'http://fixture');
    res.setHeader('Content-Type','application/json');
    const send = (status, body) => {res.statusCode=status;res.end(JSON.stringify(body))};
    if (req.method !== 'GET') {state.writes++;return send(405,{code:'unexpected_write'})}
    if (u.pathname === '/api/v1/engine') return send(200,{id:state.engine,role:'hosted',apiVersion:'0.21.0',eventCursor:state.engine+':0'});
    if (u.pathname === '/api/v1/accounts') return send(200,{items:accounts});
    const a = accounts.find(a=>u.pathname==='/api/v1/accounts/'+a.id);
    if (a) return send(200,a);
    if (u.pathname.endsWith('/connection')) return send(200,{accountId:u.pathname.includes(work)?work:account,state:'connected',updatedAt:chat.updatedAt});
    if (u.pathname.endsWith('/history')) return send(200,{accountId:account,state:'complete',retainedMessages:all.length,conversations:3,updatedAt:chat.updatedAt});
    if (u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews')||u.pathname.endsWith('/message-stars')||u.pathname==='/api/v1/shared-content') return send(200,{items:[]});
    if (u.pathname === '/api/v1/messages/search') {
      state.searchReads.push(Object.fromEntries(u.searchParams));
      if (state.mode==='unavailable') return send(503,{code:'unavailable'});
      const q=u.searchParams.get('q')?.trim(), selectedAccount=u.searchParams.get('accountId')||'', conversation=u.searchParams.get('conversationId')||'', history=u.searchParams.get('includeEditHistory')==='true';
      const fingerprint=JSON.stringify([state.engine,q,selectedAccount,conversation,history]);
      let offset=0;
      if (u.searchParams.has('cursor')) {try {const cursor=JSON.parse(Buffer.from(u.searchParams.get('cursor'),'base64url'));if(cursor.scope!==fingerprint)throw Error('scope');offset=cursor.offset}catch{return send(400,{code:'invalid_cursor'})}}
      let hits=corpus.filter(m=>(!selectedAccount||m.accountId===selectedAccount)&&(!conversation||m.conversationId===conversation)).flatMap(m=>{
        const c=chats.find(c=>c.id===m.conversationId),base={message:m,accountLabel:accounts.find(a=>a.id===m.accountId).label,conversationTitle:c.title,matchedAt:m.createdAt,matchKind:'text'};
        const rows=fold(m.originalText).includes(fold(q))?[{...base,matchedRevisionId:m===corpus[1]?id(9001):m.revisionId,historical:false,excerpt:m.originalText}]:[];
        if(history&&m===corpus[0]&&fold('Pineapple original').includes(fold(q)))rows.push(...[2,1].map(n=>({...base,matchedRevisionId:id(8000+n),matchedAt:`2026-11-09T12:00:0${n}Z`,historical:true,excerpt:'Pineapple prior version '+n})));
        return rows;
      }).sort((a,b)=>new Date(b.message.createdAt).getTime()-new Date(a.message.createdAt).getTime()||b.message.id.localeCompare(a.message.id)||Number(a.historical)-Number(b.historical)||b.matchedAt.localeCompare(a.matchedAt));
      const limit=conversation===chat.id?Math.min(7,Number(u.searchParams.get('limit')||50)):Number(u.searchParams.get('limit')||50),items=hits.slice(offset,offset+limit);
      return send(200,{query:q,...(selectedAccount?{accountId:selectedAccount}:{}),...(conversation?{conversationId:conversation}:{}),includeEditHistory:history,items,nextCursor:offset+limit<hits.length?Buffer.from(JSON.stringify({scope:fingerprint,offset:offset+limit})).toString('base64url'):undefined,metadataIndexing:{state:all.filter(m=>(!selectedAccount||m.accountId===selectedAccount)&&(!conversation||m.conversationId===conversation)).length===corpus.filter(m=>(!selectedAccount||m.accountId===selectedAccount)&&(!conversation||m.conversationId===conversation)).length?'complete':'pending',indexedMessages:corpus.filter(m=>(!selectedAccount||m.accountId===selectedAccount)&&(!conversation||m.conversationId===conversation)).length,totalMessages:all.filter(m=>(!selectedAccount||m.accountId===selectedAccount)&&(!conversation||m.conversationId===conversation)).length}});
    }
    if(u.pathname.endsWith('/contacts')){state.contactReads.push(Object.fromEntries(u.searchParams));return send(404,{code:'unsupported'})}
    if(u.pathname==='/api/v1/conversations'&&u.searchParams.get('q'))state.titleReads.push(Object.fromEntries(u.searchParams));
    if (u.pathname === '/api/v1/conversations') return send(200,{items:chats.filter(c=>(!u.searchParams.get('accountId')||c.accountId===u.searchParams.get('accountId'))&&c.archived===(u.searchParams.get('archived')==='true')&&(!u.searchParams.get('q')||fold(c.title).includes(fold(u.searchParams.get('q'))))).slice(0,Number(u.searchParams.get('limit')||100))});
    for (const c of chats) {
      const prefix='/api/v1/conversations/'+c.id, messages=chronological(c),limit=Number(u.searchParams.get('limit')||50);
      if (u.pathname===prefix) return send(200,c);
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
  const fresh=async(options={})=>{const context=await browser.newContext({viewport:{width:1440,height:950},locale:'en',...options}),page=await context.newPage();await page.addInitScript(()=>{document.addEventListener('oie:message-search-settled',()=>{const form=document.getElementById('message-search-form');window.searchSettled={q:form.elements.q.value.trim(),account:form.querySelector('input[name=accountId]')?.value||'',conversation:form.elements.conversationId.value,history:form.elements.includeEditHistory.checked};});});page.on('pageerror',e=>errors.push(e.message));return page};
  const initial=()=>base+'/?chat='+chat.id+'&account='+account+'&lang=en&theme=dark';
  const hits=page=>page.locator('#message-search-results [data-message-search-hit]');
  const query=page=>page.locator('#conversation-search:visible,#message-search-query:visible');
  const form=page=>page.locator('#message-search-form');
  const settled=async(page,q)=>until(async()=>await page.evaluate(q=>{const last=window.searchSettled,form=document.getElementById('message-search-form');return last?.q===q&&last.account===(form.querySelector('input[name=accountId]')?.value||'')&&last.conversation===form.elements.conversationId.value&&last.history===form.elements.includeEditHistory.checked},q),'ready query '+q);
  const openGlobal=async page=>{await query(page).waitFor({state:'visible'});await query(page).focus()};
  const chooseAccount=async(page,label)=>{await page.locator('#message-search-account-trigger').click();await page.getByRole('option').filter({hasText:label}).click()};
  let page;
  try {
    await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF startup');
    page=await fresh();await page.goto(initial());await page.locator('#message-composer').waitFor({state:'visible'});
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
    const blankReads=state.searchReads.length,blankTitles=state.titleReads.length,blankContacts=state.contactReads.length;await openGlobal(page);assert.equal(state.searchReads.length,blankReads,'blank search does not query saved messages');assert.equal(state.titleReads.length,blankTitles,'blank search does not query conversation titles');assert.equal(state.contactReads.length,blankContacts,'blank search does not query protocol contacts');
    await query(page).fill('Telescope');await settled(page,'Telescope');assert.equal(await query(page).evaluate(node=>document.activeElement===node),true,'results retain typing focus');await chooseAccount(page,'Personal');await settled(page,'Telescope');assert.equal(await hits(page).count(),30);assert.equal(await page.locator('[data-message-search-contacts]').count(),0,'an unsupported contacts endpoint does not produce a fabricated contacts group');
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

    await page.locator('[data-message-search-close]:visible').click();await page.locator('#conversation-list-default').waitFor({state:'visible'});
    assert(await page.evaluate(()=>window.searchNodes.list===document.getElementById('conversation-list')),'closing restores the same conversation-list node');assert.equal(await page.locator('#conversation-list').evaluate(node=>node.scrollTop),before.listScroll);assert.equal(await query(page).inputValue(),'','closing restores the original title filter');assert.equal(await page.locator('#message-composer').inputValue(),draft);
    await query(page).fill('Telescope');await settled(page,'Telescope');
    await chooseAccount(page,'All accounts');await settled(page,'Telescope');await until(async()=>await hits(page).count()===30,'global first page');
    await page.locator('#message-search-scroll').evaluate(node=>node.scrollTop=node.scrollHeight);await until(async()=>await hits(page).count()===57,'all-account second page');
    assert(await page.locator('[data-search-message="'+corpus[1].id+'"] [data-message-search-source]').count(),'current cancelled source with prior content revision renders normally');
    assert.equal(await page.locator('[data-search-message="'+corpus[1].id+'"]').getAttribute('data-search-historical'),'false');
    await query(page).fill('Family');await settled(page,'Family');assert.equal(await hits(page).count(),0);assert.equal(await page.locator('[data-message-search-conversation="'+chat.id+'"]').count(),1,'conversation titles form a distinct group');await query(page).fill('Telescope');await settled(page,'Telescope');
    await chooseAccount(page,'Work');await settled(page,'Telescope');await until(async()=>await hits(page).count()===19,'account scope before pagination');
    assert((await hits(page).evaluateAll(nodes=>nodes.map(n=>n.textContent))).every(text=>text.includes('Work')));
    await chooseAccount(page,'All accounts');await query(page).fill('CAFÉ 😀');await settled(page,'CAFÉ 😀');assert.equal(await hits(page).count(),1);assert.equal(await page.evaluate(()=>window.searchInjection),undefined);assert.equal(await hits(page).locator('img').count(),0);
    const readCount=state.searchReads.length;await query(page).fill('');await until(async()=>await hits(page).count()===0,'blank clears results');assert.equal(state.searchReads.length,readCount,'clearing does not send an invalid blank API query');
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
    await page.goBack();await page.waitForURL(historicalReturnURL);await query(page).waitFor({state:'visible'});await until(async()=>await hits(page).count()===2,'restored historical search');assert.equal(await query(page).inputValue(),'Pineapple');assert(await form(page).locator('[name="includeEditHistory"]').isChecked());assert.equal(await hits(page).count(),2);
    findings.push('Edit history is explicit and labeled; prior-content hits navigate to the bounded canonical current source and Back restores the search context');

    // The global list search persists outside conversation swaps. Source navigation must not
    // permanently suppress subsequent close cleanup or leave a hidden paginator.
    const secondSource=hits(page).first().locator('[data-message-search-source]'),beforeSecondSource=await page.evaluate(()=>window.OieRuntime.generation);
    await secondSource.click();await page.waitForURL(url=>url.searchParams.get('message')===corpus[0].id);await page.waitForFunction(generation=>window.OieRuntime.generation>generation,beforeSecondSource);
    await openGlobal(page);
    let closeRelease,closeHeld=false,closedDone;const closedResponse=new Promise(resolve=>closedDone=resolve);
    await page.route('**/ui/message-search?**',async route=>{if(new URL(route.request().url()).searchParams.get('q')==='Telescope'){const response=await route.fetch();closeHeld=true;await new Promise(resolve=>closeRelease=resolve);await route.fulfill({response}).catch(()=>{});closedDone();return}await route.continue()});
    await query(page).fill('Telescope');await until(()=>closeHeld,'pending query after reopening source global list search');await page.locator('[data-message-search-close]:visible').click();await page.locator('#message-search-slot').waitFor({state:'hidden'});
    await until(async()=>new URL(page.url()).searchParams.get('searchMode')===null,'closing a reopened search restores the conversation-list route');const readsAtClose=state.searchReads.length;
    closeRelease();await closedResponse;await page.unroute('**/ui/message-search?**');await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    assert.equal(state.searchReads.length,readsAtClose,'closed global list search cannot continue background pagination');assert.equal(await hits(page).count(),0,'closed pending response cannot resurrect results');
    await openGlobal(page);await query(page).fill('CAFÉ 😀');await settled(page,'CAFÉ 😀');assert.equal(await hits(page).count(),1);
    findings.push('After a source jump, reopening and closing cancels the pending read, clears the overlay route, rejects the late response and prevents hidden pagination; reopening works again');

    let sourceHeld=false,sourceRelease,sourceReleased;const pendingSource=new Promise(resolve=>sourceReleased=resolve);
    await page.route('**/*',async route=>{const url=new URL(route.request().url());if(url.origin===base&&url.pathname==='/'&&url.searchParams.get('message')===corpus[2].id){const response=await route.fetch();sourceHeld=true;await new Promise(resolve=>sourceRelease=resolve);await route.fulfill({response}).catch(()=>{});sourceReleased();return}await route.continue()});
    await hits(page).first().locator('[data-message-search-source]').click();await until(()=>sourceHeld,'held canonical source fragment');await openGlobal(page);await query(page).fill('Telescope');await chooseAccount(page,'Work');await settled(page,'Telescope');assert.equal(await hits(page).count(),19);
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

    const globalSearchNodes=await page.evaluate(()=>{window.concurrentSearchNodes={input:document.getElementById('conversation-search'),results:document.getElementById('message-search-results'),composer:document.getElementById('message-composer')};return{query:document.getElementById('conversation-search').value,account:document.querySelector('#message-search-form input[name=accountId]').value}});
    await page.locator('#chat-options button').first().click();await page.locator('#chat-search-messages:visible').click();
    const chatSearch=page.locator('[data-conversation-search]'),chatQuery=page.locator('#conversation-search-query');await chatQuery.waitFor({state:'visible'});await page.locator('#chat-options [role=menu]').waitFor({state:'hidden'});
    const orderedMatches=corpus.filter(message=>message.conversationId===chat.id).sort((a,b)=>new Date(b.createdAt).getTime()-new Date(a.createdAt).getTime()||b.id.localeCompare(a.id));
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
    await chatSearch.locator('[data-conversation-search-close]').click();await chatSearch.waitFor({state:'hidden'});assert.equal(await page.locator('body').getAttribute('data-focus-message'),corpus[0].id,'closing chat search keeps its selected source');assert.equal(await query(page).inputValue(),globalSearchNodes.query);assert.equal(await hits(page).count(),1);
    findings.push('Global and per-chat queries coexist independently; next/previous traverse scoped pages without wrapping, preserve draft/global DOM/focus, highlight only current literal content and keep timeline bounded to200 rows');

    const native=await fresh({javaScriptEnabled:false});await native.goto(base+'/search?q=Telescope&accountId='+work+'&lang=en&theme=light');assert.equal(await hits(native).count(),19);assert.equal(await native.locator('form[role="search"] input[name="q"]').inputValue(),'Telescope');await native.context().close();
    await fs.mkdir(path.join(__dirname,'artifacts'),{recursive:true});
    for(const width of [390,1440])for(const design of ['goshtoso','minimal'])for(const theme of ['light','dark']){
      const variant=await fresh({viewport:{width,height:900},locale:width===390?'pt-BR':'en'}),lang=width===390?'pt-BR':'en';
      await variant.goto(base+'/?account='+work+'&lang='+lang+'&theme='+theme);await variant.waitForFunction(()=>window.OieRuntime?.generation>0);await openGlobal(variant);await query(variant).fill('Telescope');await settled(variant,'Telescope');await chooseAccount(variant,'Work');await settled(variant,'Telescope');
      await hits(variant).first().waitFor({state:'visible'});await variant.evaluate(design=>document.documentElement.dataset.theme=design,design);assert.equal(await variant.locator('html').getAttribute('data-theme'),design);
      if(width===1440){await variant.locator('#chat-options button').first().click();await variant.locator('#chat-search-messages:visible').click();await variant.locator('#chat-options [role=menu]').waitFor({state:'hidden'});await variant.locator('#conversation-search-query').fill(corpus[55].originalText);await until(async()=>await variant.locator('[data-conversation-search]').getAttribute('data-search-selected')===corpus[55].id,'independent desktop queries in '+design+'/'+theme);assert.equal(await query(variant).inputValue(),'Telescope');assert.equal(await hits(variant).count(),19)}
      await query(variant).focus();await variant.keyboard.press('Tab');assert.notEqual(await variant.evaluate(()=>document.activeElement.tagName),'BODY');
      assert(await variant.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'search has no horizontal page overflow '+width+'/'+design+'/'+theme);
      await variant.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const audit=await variant.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(audit.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[]);
      const screenshot='message-search-'+width+'-'+design+'-'+theme+'.png';await variant.screenshot({path:path.join(__dirname,'artifacts',screenshot),fullPage:true});screenshots.push({width,design,theme,lang,screenshot,axe:audit.violations.length});await variant.context().close();
    }
    state.engine=id(99);await query(page).fill('switch');await until(async()=>await page.locator('#message-search-feedback').textContent().then(text=>text.includes('engine')||text.includes('Engine')),'engine change is visible');assert.equal(await hits(page).count(),0);assert.equal(await query(page).inputValue(),'switch','engine rejection preserves the latest typed search');assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'switch');const readsBeforeRebind=state.searchReads.length;await page.locator('button[data-message-search-restart]:visible').click();await page.waitForURL(url=>url.pathname==='/search');await until(async()=>await form(page).locator('[name=engineId]').inputValue()===id(99),'explicit restart binds the replacement engine');assert.equal(await query(page).inputValue(),'switch');assert(state.searchReads.length>readsBeforeRebind,'only explicit recovery queries the replacement engine');state.engine=id(1);
    findings.push('Engine replacement clears old results and requires explicit restart before rebinding');

    assert.equal(state.writes,0,'message search never writes read state or causes protocol effects');assert.deepEqual(errors,[]);
    await fs.writeFile(path.join(__dirname,'artifacts/message-search-report.json'),JSON.stringify({findings,screenshots,searchRequests:state.searchReads,titleRequests:state.titleReads,contactRequests:state.contactReads,writes:state.writes,contexts:state.contexts,pageErrors:errors},null,2));
    console.log('PASS: retained message search scopes/history/pagination, bounded canonical jump, stale-response and failure recovery, preserved workspace, native and localized accessible layouts');
  } catch(error) {if(page&&!page.isClosed())console.error('Search failure evidence',JSON.stringify(await page.evaluate(()=>({url:location.href,feedback:document.querySelector('#message-search-feedback')?.textContent,search:document.querySelector('[data-message-search-root]')?.innerText?.slice(0,3000),chatSearch:document.querySelector('[data-conversation-search]')?.dataset,chatStatus:document.querySelector('[data-conversation-search-status]')?.textContent,active:document.activeElement?.outerHTML?.slice(0,500)})).catch(()=>({}))));throw error}
  finally {await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(error=>{console.error(error);process.exitCode=1});
