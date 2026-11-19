const assert = require('node:assert/strict');

module.exports = async function liveMessageAppend({open, until, emit, messages, positions, conversation, state, id}) {
  const size = messages.length, original = {...conversation};
  state.appendFixture = true;
  const {page,context} = await open();
  try {
    const timeline=page.locator('#message-timeline');
    await page.waitForFunction(()=>window.OieEvents.connected && !document.querySelector('[data-timeline-busy="true"]'));
    await page.evaluate(()=>{
      window.OieTimeline.scrollLatest();
      window.appendFirst=document.querySelector('#message-timeline [data-message-id]');
    });
    const input=page.locator('#message-composer');
    await input.fill('Draft while messages arrive');
    const replies=[];
    page.on('response',response=>{
      const url=new URL(response.url());
      if(['/ui/refresh','/ui/timeline'].includes(url.pathname))replies.push(response);
    });
    const post=(text,paired=true)=>{
      const n=messages.length;
      const message={...messages[0],id:id(7900000+n),revisionId:id(7800000+n),originalText:text,
        createdAt:new Date(Date.parse(messages.at(-1).createdAt)+1000).toISOString(),sendState:'received',isOwn:false};
      messages.push(message);positions.set(message.id,n);
      Object.assign(conversation,{lastMessage:text,revisionId:id(7700000+n),updatedAt:message.createdAt,unreadCount:conversation.unreadCount+1});
      emit('message.created',{message});
      if(paired)emit('conversation.updated',{conversation});
      return message;
    };
    // Let the EventSource's initial reconciliation settle before observing a real event.
    await page.waitForTimeout(250);
    replies.length=0;
    const message=post('A new live message');
    const row=timeline.locator('[data-message-id="'+message.id+'"]');
    await row.waitFor({state:'visible'});
    assert.equal(await row.count(),1);
    const response=replies.find(r=>r.request().headers()['x-oie-timeline-message']===message.id);
    assert(response,'the incoming event requested a bounded append');
    const body=await response.text();
    assert(body.includes('id="oie-live-message"'));
    assert.equal((body.match(/data-message-source=/g)||[]).length,1);
    assert.equal(replies.filter(r=>new URL(r.url()).pathname==='/ui/timeline').length,0);
    assert.equal(await timeline.locator('[data-message-id]').count(),51);
    assert(await page.evaluate(()=>window.appendFirst===document.querySelector('#message-timeline [data-message-id]')));
    assert.equal(await input.inputValue(),'Draft while messages arrive');
    const selected=page.locator('#conversation-list [data-conversation-row="'+conversation.id+'"]');
    assert((await selected.innerText()).includes(message.originalText));
    assert.equal(await page.locator('#chat-header [data-conversation-preference-state]').getAttribute('data-revision'),conversation.revisionId);
    assert.equal(await page.locator('[data-jump-latest-badge]').textContent(),String(conversation.unreadCount));
    // A rich message at the same edge requires the ordinary decorated refresh.
    replies.length=0;
    const rich=post('https://example.test/document');
    await timeline.locator('[data-message-id="'+rich.id+'"]').waitFor({state:'attached'});
    assert((await Promise.all(replies.filter(r=>new URL(r.url()).pathname==='/ui/refresh').map(r=>r.text()))).some(body=>body.includes('id="oie-live-regions"')));
    // Two creations covered by one refresh cannot be acknowledged as a single row.
    replies.length=0;
    const first=post('First in a burst'),second=post('Second in a burst');
    for(const item of [first,second])await timeline.locator('[data-message-id="'+item.id+'"]').waitFor({state:'attached'});
    assert.equal(replies.filter(r=>r.request().headers()['x-oie-timeline-message']).length,0);
    // If the companion event arrives after the earlier response was produced,
    // discard that response and recover against the latest cursor.
    await page.evaluate(()=>{window.OieTimeline.scrollLatest();window.lateAppends=[];
      document.addEventListener('oie:live-message-appended',e=>window.lateAppends.push(e.detail.cursor));
    });
    await page.waitForFunction(()=>{
      const list=document.getElementById('conversation-list');
      return window.OieTimeline.appendEdge()&&window.OieInbox.current(list)&&
        list.dataset.inboxCursor===window.OieEvents.latest('');
    });
    let held=false;
    const release=Promise.withResolvers();
    await page.route('**/ui/refresh?*',async route=>{
      if(!route.request().headers()['x-oie-timeline-message'])return route.continue();
      const response=await route.fetch();held=true;
      await release.promise;await route.fulfill({response});
    });
    const late=post('Response crossed by a new event',false);
    let oldCursor;
    try {
      await until(()=>held,'single live append intercepted');
      oldCursor=await page.evaluate(()=>window.OieEvents.latest(''));
      emit('conversation.updated',{conversation});
      await page.waitForFunction(cursor=>window.OieEvents.latest('')!==cursor,oldCursor);
    } finally {
      release.resolve();
    }
    await timeline.locator('[data-message-id="'+late.id+'"]').waitFor({state:'attached'});
    assert(!(await page.evaluate(()=>window.lateAppends)).includes(oldCursor));
    assert.equal(await input.inputValue(),'Draft while messages arrive');
  } finally {
    await context.close();
    for(const message of messages.splice(size))positions.delete(message.id);
    Object.assign(conversation,original);
    state.appendFixture=false;
  }
};
