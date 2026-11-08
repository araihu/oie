(() => {
  'use strict';
  const body = document.body;
  const labels = document.querySelector('#oie-live-copy')?.dataset || {};
  const text = key => labels[key] || '';
  const status = document.querySelector('[data-live-status]');
  const announce = message => { if (status) status.textContent = message; };
  const mock = body.dataset.mock === 'true';
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content || '';
  let pendingRequests = 0, refreshTimer, refreshing = false, refreshAgain = false;
  let source, stopped = false, forceBottom = false, storageDenied = false;
  let accountsVersion = [...document.querySelectorAll('#account-blocks>[data-account-id]')].map(e => e.dataset.accountId+';').join('');
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
      const date=new Date(time.dateTime);if(!Number.isNaN(date.getTime()))time.textContent=clock.format(date);
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
  function scrollLatest() {
    const timeline = document.querySelector('#message-timeline');
    if (timeline) timeline.scrollTop = timeline.scrollHeight;
    const jump = document.querySelector('[data-jump-latest]'); if (jump) jump.hidden = true;
  }
  document.addEventListener('click', event => { if (event.target.closest('[data-jump-latest]')) scrollLatest(); });
  document.querySelector('#message-timeline')?.addEventListener('scroll', () => {
    if (nearBottom(document.querySelector('#message-timeline'))) {
      const jump = document.querySelector('[data-jump-latest]'); if (jump) jump.hidden = true;
    }
  });
  localizeTimes();scrollLatest();

  async function refresh() {
    if (stopped || mock) return;
    if (pendingRequests || refreshing) { refreshAgain = true; return; }
    refreshing = true;
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
      const history=document.querySelector('#history-status'),newHistory=page.querySelector('#live-history-status');if(history&&newHistory&&history.innerHTML!==newHistory.innerHTML)mutateDOM(history,newHistory.innerHTML);
      const list = document.querySelector('#conversation-list');
      const nextList = page.querySelector('#live-list');
      if (list && nextList && list.innerHTML !== nextList.innerHTML) {
        const scroll = list.scrollTop;
        const focused = document.activeElement?.closest('[data-conversation-id]')?.dataset.conversationId;
        mutateDOM(list,nextList.innerHTML); list.scrollTop = scroll;
        if (focused) list.querySelector(`[data-conversation-id="${CSS.escape(focused)}"]`)?.focus({preventScroll:true});
      }
      if (regions.dataset.conversationId === body.dataset.conversationId) {
        const timeline = document.querySelector('#message-timeline'), next = page.querySelector('#live-timeline');
        if (timeline && next && timeline.dataset.version !== next.dataset.version) {
          const follow = forceBottom || nearBottom(timeline), top = timeline.scrollTop;
          const anchor = [...timeline.querySelectorAll('[data-message-id]')].find(el => el.getBoundingClientRect().bottom >= timeline.getBoundingClientRect().top);
          const anchorID = anchor?.dataset.messageId, anchorTop = anchor?.getBoundingClientRect().top;
          mutateDOM(timeline,next.innerHTML); timeline.dataset.version = next.dataset.version;localizeTimes();
          if (follow) scrollLatest();
          else {
            const same = anchorID && timeline.querySelector(`[data-message-id="${CSS.escape(anchorID)}"]`);
            timeline.scrollTop = same ? top + same.getBoundingClientRect().top - anchorTop : top;
            const jump = document.querySelector('[data-jump-latest]'); if (jump) jump.hidden = false;
          }
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
      const overview=document.querySelector('#account-overview'),nextOverview=page.querySelector('#live-account-overview');
      if(overview&&nextOverview&&overview.innerHTML!==nextOverview.innerHTML&&!overview.contains(document.activeElement))mutateDOM(overview,nextOverview.innerHTML);

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
      }
      localizeTimes();
    } catch { announce(text('liveRetry')); }
    finally {
      clearTimeout(timeout); refreshing = false;
      if (refreshAgain) { refreshAgain = false; scheduleRefresh(); }
    }
  }
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
    source?.close();announce(text('liveConnecting'));
    source = new EventSource('/ui/events?cursor='+encodeURIComponent(cursor || ''));
    source.onopen = () => { announce(text('liveReady'));scheduleRefresh();retryPending(); };
    source.onerror = () => announce(text('liveRetry'));
    for (const type of ['account.registered','account.connection_changed','account.history_changed','conversation.created','conversation.updated','message.created','message.updated']) source.addEventListener(type,scheduleRefresh);
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
  if (open === 'accounts' || open === 'newchat') {
    const show = () => dispatchEvent(new CustomEvent('drawer:open',{detail:{id:open}}));
    if (window.Alpine) setTimeout(show,0); else document.addEventListener('alpine:initialized',show,{once:true});
  }
  const nativeError=document.querySelector('[data-native-error]');if(nativeError)nativeError.focus();
})();
