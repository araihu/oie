(() => {
  'use strict';
  const body = document.body;
  const labels = document.querySelector('#oie-live-copy')?.dataset || {};
  const text = key => labels[key] || '';
  const status = document.querySelector('[data-live-status]');
  const announce = (message, quiet = false) => { if (status) { status.textContent = message; status.hidden = quiet; } };
  const mock = body.dataset.mock === 'true';
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content || '';
  let pendingRequests = 0, refreshTimer, refreshing = false, refreshAgain = false;
  let source, stopped = false, forceBottom = false, storageDenied = false;
  let accountsVersion = [...document.querySelectorAll('#account-blocks>[data-account-id]')].map(e => e.dataset.accountId+';').join('');
  // Exact account/address tuples avoid nickname guesses and cross-account leaks.
  const avatarSources = new Map();
  const avatarKey = (account,address) => JSON.stringify([account,address]);
  function readAvatars(root) {
    const data = root.querySelector('#oie-avatar-data');
    if (!data) return;
    const loaded = new Set([...data.querySelectorAll('[data-avatar-loaded-account]')].map(el=>el.dataset.avatarLoadedAccount));
    for (const key of avatarSources.keys()) if (loaded.has(JSON.parse(key)[0])) avatarSources.delete(key);
    for (const entry of data.querySelectorAll('[data-avatar-entry]')) {
      const {accountId,address,hash} = entry.dataset;
      if (loaded.has(accountId) && /^[0-9a-f]{64}$/.test(hash)) avatarSources.set(avatarKey(accountId,address),'/ui/avatars/'+hash);
    }
  }
  function applyAvatars() {
    for (const scope of document.querySelectorAll('[data-avatar-account]')) {
      const src = avatarSources.get(avatarKey(scope.dataset.avatarAccount,scope.dataset.avatarAddress)) || '';
      scope.dataset.avatarSrc = src;
      if (window.Alpine?.$data) window.Alpine.$data(scope).oieAvatarSrc = src;
    }
  }
  readAvatars(document);
  document.addEventListener('alpine:initialized',applyAvatars);
  const inMemory = new Map();
  const composer = document.querySelector('#send-message-form');
  const input = document.querySelector('#message-composer');
  const submissionKey = composer?.elements.namedItem('operationKey');
  const draftKey = body.dataset.engineId && body.dataset.conversationId && body.dataset.accountId
    ? ['oie.draft.v1', body.dataset.engineId, body.dataset.accountId, body.dataset.conversationId].join(':') : '';
  let journal;

  function storageWarning() {
    if (!storageDenied && composer) { const note=document.createElement('p');note.className='oie-storage-warning';note.role='status';note.textContent=text('storageUnavailable');composer.append(note); }
    storageDenied = true;
    announce(text('storageUnavailable'));
  }
  function readDraft() {
    if (!draftKey) return null;
    try { const raw = localStorage.getItem(draftKey); return raw ? JSON.parse(raw) : null; }
    catch { storageWarning(); return inMemory.get(draftKey) || null; }
  }
  function saveDraft(record) {
    if (!draftKey) return;
    journal = record; inMemory.set(draftKey, record);
    try { localStorage.setItem(draftKey, JSON.stringify(record)); }
    catch { storageWarning(); }
  }
  function clearDraft(key) {
    if (!draftKey) return;
    const stored = readDraft();
    if (!stored || stored.key === key) {
      inMemory.delete(draftKey);
      try { localStorage.removeItem(draftKey); } catch { storageWarning(); }
    }
    journal = null;
  }
  function feedback(form, message, error = false) {
    const region = form.querySelector('[data-form-feedback]');
    if (region) { region.textContent = message; region.dataset.error = String(error);region.setAttribute('role',error?'alert':'status'); if(error&&form.dataset.oieAction!=='send'){region.tabIndex=-1;region.focus({preventScroll:true});} }
    announce(message);
  }
  function lockComposer(locked) {
    if (!input || !composer) return;
    input.readOnly = locked;
    input.setAttribute('aria-readonly', String(locked));
    const button = composer.querySelector('[data-send-message]');
    if (button) {
      button.setAttribute('aria-label', locked ? text('retry') : text('send'));
      button.title = locked ? text('locked') : text('send');
    }
  }
  if (input && submissionKey && draftKey && !mock) {
    const saved = readDraft();
    if (saved && typeof saved.text === 'string' && typeof saved.key === 'string') {
      journal = saved; input.value = saved.text; submissionKey.value = saved.key;
      lockComposer(saved.pending === true);
      if (saved.pending) feedback(composer, text('pending'), true);
    }
    input.addEventListener('input', () => {
      if (journal?.pending) return;
      saveDraft({text:input.value,key:submissionKey.value,pending:false});
    });
    input.addEventListener('keydown', event => {
      const mobile = matchMedia('(pointer: coarse)').matches || innerWidth <= 720;
      if (event.key === 'Enter' && !event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && !event.isComposing && event.keyCode !== 229 && !mobile) {
        event.preventDefault(); composer.requestSubmit();
      }
    });
    addEventListener('beforeunload', event => {
      if (journal?.pending || (storageDenied && input.value)) { event.preventDefault(); event.returnValue = ''; }
    });
  }

  function mutateDOM(target, html) {
    if (!target) return;
    const apply = () => {
      for (const child of target.children) window.Alpine?.destroyTree(child);
      target.innerHTML = html;
      for (const child of target.children) window.Alpine?.initTree(child);
    };
    if (window.Alpine?.mutateDom) window.Alpine.mutateDom(apply); else apply();
  }
  function localizeTimes() {
    const locale=document.documentElement.lang || undefined;
    const clock=new Intl.DateTimeFormat(locale,{hour:'2-digit',minute:'2-digit'});
    for (const time of document.querySelectorAll('time[datetime]:not([data-day])')) {
      const date=new Date(time.dateTime);if(!Number.isNaN(date.getTime()))time.textContent=time.matches('[data-viewer-date],[data-capability-date],[data-details-date],[data-shared-source-date]')?new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeStyle:'short'}).format(date):clock.format(date);
    }
    const timeline=document.querySelector('#message-timeline');if(!timeline)return;
    for(const divider of timeline.querySelectorAll('.oie-day-divider'))divider.remove();
    const key=date=>[date.getFullYear(),date.getMonth(),date.getDate()].join('-');
    const today=key(new Date());let previous='';
    for(const row of timeline.querySelectorAll(':scope>[data-message-id]')){
      const date=new Date(row.dataset.createdAt);if(Number.isNaN(date.getTime()))continue;
      const current=key(date);if(current===previous)continue;previous=current;
      const divider=document.createElement('div');divider.className='oie-day-divider';
      const time=document.createElement('time');time.dateTime=row.dataset.createdAt;time.dataset.day='true';
      time.textContent=current===today?text('today'):new Intl.DateTimeFormat(locale,{dateStyle:'medium'}).format(date);
      divider.append(time);row.before(divider);
    }
  }
  function nearBottom(timeline) { return !timeline || timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 60; }
  let highlightedMessage='',highlightTimer;
  function pinned(){return !!body.dataset.focusMessage;}
  function showJump(show){const jump=document.querySelector('[data-jump-latest]');if(jump){jump.hidden=!show;jump.textContent=pinned()?text('latest'):text('newMessages');}}
  function scrollLatest() {
    const timeline=document.querySelector('#message-timeline');if(timeline)timeline.scrollTop=timeline.scrollHeight;
    showJump(pinned());
  }
  function focusMessage(id,focus=true){
    const timeline=document.querySelector('#message-timeline'),row=timeline?.querySelector(`[data-message-id="${CSS.escape(id)}"]`);
    if(!row)return false;
    clearTimeout(highlightTimer);for(const old of timeline.querySelectorAll('.oie-reply-highlight'))old.classList.remove('oie-reply-highlight');
    highlightedMessage=id;row.classList.add('oie-reply-highlight');row.scrollIntoView({block:'center',behavior:'auto'});if(focus)row.focus({preventScroll:true});
    highlightTimer=setTimeout(()=>{row.classList.remove('oie-reply-highlight');document.querySelector(`#message-timeline [data-message-id="${CSS.escape(id)}"]`)?.classList.remove('oie-reply-highlight');highlightedMessage='';},1800);
    return true;
  }
  document.addEventListener('click',event=>{
    const reply=event.target.closest('[data-reply-target]');
    if(reply&&!event.defaultPrevented&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey&&reply.dataset.replyConversation===body.dataset.conversationId&&focusMessage(reply.dataset.replyTarget)){event.preventDefault();return;}
    if(event.target.closest('[data-jump-latest]')){
      if(pinned()||new URLSearchParams(location.search).has('message')){const url=new URL(location.href);url.searchParams.delete('message');location.assign(url.pathname+url.search);}
      else scrollLatest();
    }
  });
  document.querySelector('#message-timeline')?.addEventListener('scroll',()=>{if(nearBottom(document.querySelector('#message-timeline')))showJump(pinned());});
  localizeTimes();if(!body.dataset.focusMessage||!focusMessage(body.dataset.focusMessage))scrollLatest();else showJump(true);

  async function refresh() {
    if (stopped || mock) return;
    if (pendingRequests || refreshing) { refreshAgain = true; return; }
    refreshing = true;
    const starGeneration=Number(body.dataset.starGeneration||0);
    const controller = new AbortController(); const timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const response = await fetch('/ui/refresh'+location.search, {credentials:'same-origin',cache:'no-store',signal:controller.signal});
      if (!response.ok) throw new Error('refresh unavailable');
      const page = new DOMParser().parseFromString(await response.text(), 'text/html');
      const regions = page.querySelector('#oie-live-regions');
      if (!regions || regions.dataset.unavailable === 'true') { announce(text('liveRetry')); return; }
      if (body.dataset.engineId && regions.dataset.engineId !== body.dataset.engineId) {
        stopped = true; source?.close(); if (composer) composer.querySelector('[data-send-message]').disabled = true;
        announce(text('engineChanged')); return;
      }
      body.dataset.contextNewer=regions.dataset.contextNewer||'false';body.dataset.contextMissing=regions.dataset.contextMissing||'false';
      const contextNotice=document.querySelector('#context-notice'),newNotice=page.querySelector('#live-context-notice');if(contextNotice&&newNotice&&contextNotice.innerHTML!==newNotice.innerHTML)mutateDOM(contextNotice,newNotice.innerHTML);
      if(pinned())showJump(true);
      readAvatars(page);
      const history=document.querySelector('#history-status'),newHistory=page.querySelector('#live-history-status');if(history&&newHistory&&history.innerHTML!==newHistory.innerHTML)mutateDOM(history,newHistory.innerHTML);
      const list = document.querySelector('#conversation-list');
      const nextList = page.querySelector('#live-list');
      if (list && nextList && list.innerHTML !== nextList.innerHTML) {
        const scroll = list.scrollTop;
        const focused = list.contains(document.activeElement) ? document.activeElement?.closest('[data-conversation-id]')?.dataset.conversationId : '';
        mutateDOM(list,nextList.innerHTML); list.scrollTop = scroll;
        if (focused) list.querySelector(`[data-conversation-id="${CSS.escape(focused)}"]`)?.focus({preventScroll:true});
      }
      if (regions.dataset.conversationId === body.dataset.conversationId) {
        const timeline = document.querySelector('#message-timeline'), next = page.querySelector('#live-timeline');
        if (timeline && next && timeline.dataset.version !== next.dataset.version) {
          const follow = !pinned()&&(forceBottom || nearBottom(timeline)), top = timeline.scrollTop;
          const active=timeline.contains(document.activeElement)?document.activeElement:null;
          const activeRow=active?.closest('[data-message-id]')?.dataset.messageId,activeID=active?.id,activeReply=active?.closest('[data-reply-target]')?.dataset.replyTarget;
          const anchor = [...timeline.querySelectorAll('[data-message-id]')].find(el => el.getBoundingClientRect().bottom >= timeline.getBoundingClientRect().top);
          const anchorID = anchor?.dataset.messageId, anchorTop = anchor?.getBoundingClientRect().top;
          mutateDOM(timeline,next.innerHTML); timeline.dataset.version = next.dataset.version;localizeTimes();
          if (follow) scrollLatest();
          else {
            const same = anchorID && timeline.querySelector(`[data-message-id="${CSS.escape(anchorID)}"]`);
            timeline.scrollTop = same ? top + same.getBoundingClientRect().top - anchorTop : top;
            const jump = document.querySelector('[data-jump-latest]'); if (jump) jump.hidden = false;
          }
          if(activeRow){const row=timeline.querySelector(`[data-message-id="${CSS.escape(activeRow)}"]`);const target=(activeID&&document.getElementById(activeID))||(activeReply&&row?.querySelector(`[data-reply-target="${CSS.escape(activeReply)}"]`))||row;target?.focus({preventScroll:true});}
          if(highlightedMessage)timeline.querySelector(`[data-message-id="${CSS.escape(highlightedMessage)}"]`)?.classList.add('oie-reply-highlight');
          forceBottom = false;
        }
        const header = document.querySelector('#chat-header'), nextHeader = page.querySelector('#live-header');
        if (header && nextHeader?.firstElementChild && header.dataset.version !== nextHeader.dataset.version && !header.contains(document.activeElement)) {
          const replacement = nextHeader.firstElementChild.cloneNode(true);
          const apply = () => { window.Alpine?.destroyTree(header); header.replaceWith(replacement); window.Alpine?.initTree(replacement); };
          if (window.Alpine?.mutateDom) window.Alpine.mutateDom(apply); else apply();
        }
      }
      for (const fresh of page.querySelectorAll('#live-account-blocks [data-account-id]')) {
        if (!fresh.matches('section[data-account-id]')) continue;
        const existing = document.querySelector(`#account-blocks>section[data-account-id="${CSS.escape(fresh.dataset.accountId)}"]`);
        if (existing) {
          const oldStatus = existing.querySelector('[id^="connection-status-"]'), newStatus = fresh.querySelector('[id^="connection-status-"]');
          if (oldStatus && newStatus && oldStatus.innerHTML !== newStatus.innerHTML) mutateDOM(oldStatus,newStatus.innerHTML);
          const oldAction=existing.querySelector('[id^="disconnect-action-"]'),newAction=fresh.querySelector('[id^="disconnect-action-"]');
          if(oldAction&&newAction&&oldAction.innerHTML!==newAction.innerHTML&&!oldAction.contains(document.activeElement))mutateDOM(oldAction,newAction.innerHTML);
        }
      }
      const detailsRegion=page.querySelector('#live-conversation-details');
      if(detailsRegion?.dataset.detailsConversation===body.dataset.conversationId){
        const detailsScroll=document.getElementById('details-body')?.scrollTop;
        for(const section of ['identity','account','metadata','participants']){
          const existing=document.getElementById('conversation-details-'+section),fresh=detailsRegion.querySelector('#live-details-'+section);
          if(existing&&fresh&&existing.dataset.version!==fresh.dataset.version&&!existing.contains(document.activeElement)){
            mutateDOM(existing,fresh.innerHTML);existing.dataset.version=fresh.dataset.version;
          }
        }
        if(detailsScroll!==undefined)document.getElementById('details-body').scrollTop=detailsScroll;
      }
      const accountScroll=document.getElementById('accounts-body')?.scrollTop;
      // Read-only evidence refreshes independently from connection forms and drawer navigation.
      for(const id of ['account-capability-summary','account-capability-matrix']) {
        const existing=document.getElementById(id),fresh=page.querySelector('#live-account-capabilities #'+id);
        if(existing&&fresh&&existing.innerHTML!==fresh.innerHTML&&!existing.contains(document.activeElement)){
          const scroll=document.getElementById('accounts-body')?.scrollTop;
          mutateDOM(existing,fresh.innerHTML);
          if(scroll!==undefined)document.getElementById('accounts-body').scrollTop=scroll;
        }
      }
      const discovery=document.getElementById('account-discovery-services'),nextDiscovery=page.querySelector('#live-account-capabilities #account-discovery-services');
      if(discovery&&nextDiscovery){
        const details=[...discovery.querySelectorAll('[data-discovery-address]')],freshDetails=[...nextDiscovery.querySelectorAll('[data-discovery-address]')];
        if(details.length>0&&details.length===freshDetails.length&&details.every((detail,index)=>detail.dataset.discoveryAddress===freshDetails[index].dataset.discoveryAddress)){
          // Updating app-owned content leaves the public accordion's expansion and focus intact.
          for(let index=0;index<details.length;index++)if(details[index].innerHTML!==freshDetails[index].innerHTML&&!details[index].contains(document.activeElement))mutateDOM(details[index],freshDetails[index].innerHTML);
        }else if(discovery.innerHTML!==nextDiscovery.innerHTML&&!discovery.contains(document.activeElement))mutateDOM(discovery,nextDiscovery.innerHTML);
      }
      if(accountScroll!==undefined)document.getElementById('accounts-body').scrollTop=accountScroll;
      const overview=document.querySelector('#account-overview'),nextOverview=page.querySelector('#live-account-overview');
      if(overview&&nextOverview&&overview.innerHTML!==nextOverview.innerHTML&&!overview.contains(document.activeElement)){
        const scroll=overview.scrollTop;
        mutateDOM(overview,nextOverview.innerHTML);
        overview.scrollTop=scroll;
      }

      if (accountsVersion !== regions.dataset.accountsVersion && !document.querySelector('#new-conversation-form')?.contains(document.activeElement)) {
        mutateDOM(document.querySelector('#newchat-account-select'), page.querySelector('#live-newchat-account')?.innerHTML || '');
        accountsVersion = regions.dataset.accountsVersion;
      }
      const nextMenu = page.querySelector('#live-account-menu .oie-account-menu');
      if (nextMenu) for (const menu of document.querySelectorAll('.oie-account-menu')) {
        // Keep an open menu's focus and its lifecycle intact while refreshing counts.
        if (!menu.contains(document.activeElement)) {
          if (menu.innerHTML !== nextMenu.innerHTML) mutateDOM(menu,nextMenu.innerHTML);
        } else for (const badge of menu.querySelectorAll('[data-account-unread]')) {
          const next = nextMenu.querySelector(`[data-account-unread="${CSS.escape(badge.dataset.accountUnread)}"]`);
          if (next) {badge.textContent = next.textContent;badge.hidden = next.hidden;badge.setAttribute('aria-label',next.getAttribute('aria-label'));}
        }
        for (const status of menu.querySelectorAll('[data-account-status]')) {
          const next = nextMenu.querySelector(`[data-account-status="${CSS.escape(status.dataset.accountStatus)}"]`);
          if (next && status.dataset.connectionState !== next.dataset.connectionState) {
            status.dataset.connectionState = next.dataset.connectionState;
            status.title = next.title;
            status.setAttribute('aria-label',next.getAttribute('aria-label'));
            mutateDOM(status,next.innerHTML);
          }
        }
      }
      document.dispatchEvent(new CustomEvent("oie:shared-refresh",{detail:page}));applyAvatars();document.dispatchEvent(new CustomEvent("oie:stars-refresh",{detail:{page,generation:starGeneration}}));document.dispatchEvent(new CustomEvent("oie:previews-refresh",{detail:page}));localizeTimes();
    } catch { announce(text('liveRetry')); }
    finally {
      clearTimeout(timeout); refreshing = false;
      if (refreshAgain) { refreshAgain = false; scheduleRefresh(); }
    }
  }
  document.addEventListener('oie:refresh-request',()=>scheduleRefresh());
  function scheduleRefresh() { clearTimeout(refreshTimer); refreshTimer = setTimeout(refresh,100); }

  document.addEventListener('submit', async event => {
    const form = event.target.closest('form[data-oie-action]');
    if (!form || mock) return;
    event.preventDefault();
    if (form.dataset.busy === 'true' || stopped) return;
    const sending = form.dataset.oieAction === 'send';
    if (sending) {
      if (form.dataset.sendAllowed !== 'true') return;
      const value = journal?.pending ? journal.text : input.value;
      if (!value.trim() || Array.from(value).length > 16384) { feedback(form,text('invalidMessage'),true);input.focus();return; }
      if (journal?.pending) { input.value=journal.text; submissionKey.value=journal.key; }
      saveDraft({text:value,key:submissionKey.value,pending:true});lockComposer(true);
    }
    const values = new URLSearchParams(new FormData(form));
    const buttons = [...form.querySelectorAll('input:not([type=hidden]),textarea,select,button')];
    const disabled = buttons.map(button => button.disabled);
    form.dataset.busy = 'true'; form.setAttribute('aria-busy','true'); buttons.forEach(button => button.disabled = true);
    for (const secret of form.querySelectorAll('input[type="password"]')) secret.value = '';
    feedback(form,text('saving'));
    pendingRequests++;
    const controller = new AbortController(); const timeout = setTimeout(() => controller.abort(),15000);
    try {
      const response = await fetch(form.action,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal});
      const result = await response.json();
      if (!result.ok) {
        feedback(form,result.message || text('pending'),true);
        if(result.code==='engine_changed'){stopped=true;source?.close();}
        if (sending && result.rejected) { saveDraft({text:input.value,key:submissionKey.value,pending:false});lockComposer(false); }
        if (sending) input.focus({preventScroll:true});
        if(!sending)scheduleRefresh();
      } else {
        feedback(form,result.message || '');
        if (sending && result.accepted) {
          const submittedKey = values.get('operationKey');
          clearDraft(submittedKey);input.value='';submissionKey.value=result.nextKey;lockComposer(false);forceBottom=true;
          input.focus({preventScroll:true});
        } else if (form.dataset.oieAction === 'register' || form.dataset.oieAction === 'open') {
          if (result.redirect) { location.assign(result.redirect); return; }
        }
        const key = form.elements.namedItem('operationKey'); if (key && result.nextKey) key.value=result.nextKey;
        scheduleRefresh();
      }
    } catch {
      feedback(form,sending ? text('pending') : text('liveRetry'),true);
      if (sending) input.focus({preventScroll:true});
    } finally {
      clearTimeout(timeout);pendingRequests--;form.dataset.busy='false';form.removeAttribute('aria-busy');
      buttons.forEach((button,index)=>button.disabled=disabled[index]);
      if(stopped&&composer)composer.querySelector('[data-send-message]').disabled=true;
      if (refreshAgain) { refreshAgain=false;scheduleRefresh(); }
    }
  });
  function retryPending() { if (composer && journal?.pending && composer.dataset.busy !== 'true' && !stopped && navigator.onLine) composer.requestSubmit(); }
  function connect(cursor) {
    if (mock || stopped) return;
    source?.close();announce(text('liveConnecting'),true);
    source = new EventSource('/ui/events?cursor='+encodeURIComponent(cursor || ''));
    source.onopen = () => { announce(text('liveReady'),true);scheduleRefresh();retryPending(); };
    source.onerror = () => announce(text('liveRetry'));
    for (const type of ['message.preview_changed','profile.avatar_changed','account.registered','account.connection_changed','account.history_changed','conversation.created','conversation.updated','message.created','message.updated']) source.addEventListener(type,scheduleRefresh);
    source.addEventListener('message.star_changed',event=>{try{document.dispatchEvent(new CustomEvent('oie:star-event',{detail:JSON.parse(event.data).star}));}catch{}scheduleRefresh();});
    source.addEventListener('oie.reset', event => {
      try {
        const reset=JSON.parse(event.data);
        if (body.dataset.engineId && reset.engineId !== body.dataset.engineId) {stopped=true;source.close();announce(text('engineChanged'));return;}
        scheduleRefresh();connect(reset.cursor);
      }catch{announce(text('liveRetry'))}
    });
  }
  connect(body.dataset.eventCursor);
  // Bounded recovery polling also covers a proxy that buffers or disables SSE.
  setInterval(() => { if (!mock && !document.hidden) {scheduleRefresh();retryPending();} },15000);
  addEventListener('online', () => {scheduleRefresh();retryPending();});
  const open = new URLSearchParams(location.search).get('open') || body.dataset.openDrawer;
  if (open === 'accounts' || open === 'newchat' || open === 'details') {
    const show = () => {
      dispatchEvent(new CustomEvent('drawer:open',{detail:{id:open}}));
      if(open==='accounts')setTimeout(()=>{
        const query=new URLSearchParams(location.search);
        const target=query.get('accountPage')==='capabilities'?document.querySelector('[data-account-capabilities-title]'):query.get('accountFocus')==='capabilities'?document.querySelector('[data-account-capabilities]'):null;
        target?.focus({preventScroll:true});
      },230);
    };
    if (window.Alpine) setTimeout(show,0); else document.addEventListener('alpine:initialized',show,{once:true});
  }
  const nativeError=document.querySelector('[data-native-error]');if(nativeError)nativeError.focus();
})();
