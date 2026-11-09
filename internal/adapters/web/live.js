window.OieRuntime.register('live', life => {
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
  life.cleanup(()=>{stopped=true;source?.close();});
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
  life.on(document,'alpine:initialized',applyAvatars);life.raf(applyAvatars);
  const memoryKey='device-drafts';
  if(!window.OieRuntime.memory.has(memoryKey))window.OieRuntime.memory.set(memoryKey,new Map());
  const inMemory=window.OieRuntime.memory.get(memoryKey);
  const composer = document.querySelector('#send-message-form');
  const input = document.querySelector('#message-composer');
  const submissionKey = composer?.elements.namedItem('operationKey');
  const draftKey = body.dataset.engineId && body.dataset.conversationId && body.dataset.accountId
    ? ['oie.draft.v1', body.dataset.engineId, body.dataset.accountId, body.dataset.conversationId].join(':') : '';
  let journal,preparingReply=false,replyEpoch=0;
  const replyField=composer?.elements.namedItem('replyToMessageId');
  const replyBox=composer?.querySelector('[data-composer-reply]');
  const canonicalID=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
  function renderReply(){
    const reply=journal?.reply,locked=!!journal?.pending;
    if(replyField)replyField.value=reply?.messageId||'';
    if(replyBox){replyBox.hidden=!reply;replyBox.querySelector('[data-composer-reply-sender]').textContent=typeof reply?.senderName==='string'?reply.senderName:'';replyBox.querySelector('[data-composer-reply-excerpt]').textContent=typeof reply?.excerpt==='string'?reply.excerpt:'';const cancel=replyBox.querySelector('[data-cancel-reply]');if(cancel)cancel.disabled=locked||preparingReply;}
    for(const button of document.querySelectorAll('[id^="reply-message-"]'))button.disabled=!composer||composer.dataset.replyAllowed!=='true'||locked||preparingReply;
    const send=composer?.querySelector('[data-send-message]');if(send)send.disabled=stopped||preparingReply||composer.dataset.busy==='true'||(composer.dataset.sendAllowed!=='true'&&!locked);
  }
  function draftRecord(pending=false){return {text:input?.value||'',key:submissionKey?.value||'',pending,reply:journal?.reply||null};}


  function storageWarning() {
    if (!storageDenied && composer) { const note=document.createElement('p');note.className='oie-storage-warning';note.role='status';note.textContent=text('storageUnavailable');composer.append(note); }
    storageDenied = true;
    announce(text('storageUnavailable'));
  }
  function readDraft() {
    if (!draftKey) return null;
    try { const raw = localStorage.getItem(draftKey); return raw ? JSON.parse(raw) : inMemory.get(draftKey)||null; }
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
    if (saved && typeof saved.text === 'string' && (canonicalID(saved.key)||(saved.key===''&&!saved.pending)) && (!saved.reply || (canonicalID(saved.reply.messageId) && saved.reply.engineId===body.dataset.engineId && saved.reply.accountId===body.dataset.accountId && saved.reply.conversationId===body.dataset.conversationId && typeof saved.reply.senderName==='string' && typeof saved.reply.excerpt==='string' && Array.from(saved.reply.excerpt).length<=512))) {
      journal = {...saved,key:canonicalID(saved.key)?saved.key:submissionKey.value}; input.value = saved.text; submissionKey.value = journal.key;
      lockComposer(saved.pending === true);
      if (saved.pending) feedback(composer, text('pending'), true);
    }
    life.on(input,'input', () => {
      if (journal?.pending) return;
      saveDraft(draftRecord(false));
    });
    life.on(input,'keydown', event => {
      const mobile = matchMedia('(pointer: coarse)').matches || innerWidth <= 720;
      if (event.key === 'Enter' && !event.shiftKey && !event.ctrlKey && !event.altKey && !event.metaKey && !event.isComposing && event.keyCode !== 229 && !mobile) {
        event.preventDefault(); composer.requestSubmit();
      }
    });
    life.on(window,'beforeunload', event => {
      if (journal?.pending || (storageDenied && input.value)) { event.preventDefault(); event.returnValue = ''; }
    });
  }


  renderReply();
  life.on(document,'oie:actions-refreshed',renderReply);
  life.on(document,'click',async event=>{
    const cancel=event.target.closest('[data-cancel-reply]');
    if(cancel){event.preventDefault();if(journal?.pending){feedback(composer,text('replyLocked'),true);return}replyEpoch++;preparingReply=false;saveDraft({...draftRecord(false),reply:null});renderReply();input?.focus({preventScroll:true});return;}
    const action=event.target.closest('[id^="reply-message-"]');if(!action||action.disabled||!composer)return;
    event.preventDefault();if(journal?.pending){feedback(composer,text('replyLocked'),true);return}if(composer.dataset.replyAllowed!=='true')return;
    const messageId=action.id.slice(14);if(!canonicalID(messageId))return;
    const epoch=++replyEpoch;preparingReply=true;renderReply();feedback(composer,text('replyPreparing'));
    const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
    try{
      const query=new URLSearchParams({engineId:body.dataset.engineId,conversationId:body.dataset.conversationId,accountId:body.dataset.accountId,messageId});
      const response=await life.fetch('/ui/reply-source?'+query,{credentials:'same-origin',cache:'no-store',signal:controller.signal}),reply=await response.json();
      if(life.signal.aborted||epoch!==replyEpoch)return;
      if(!response.ok||reply.engineId!==body.dataset.engineId||reply.accountId!==body.dataset.accountId||reply.conversationId!==body.dataset.conversationId||reply.requestedMessageId!==messageId||!canonicalID(reply.messageId)||typeof reply.senderName!=='string'||typeof reply.excerpt!=='string'||Array.from(reply.excerpt).length>512)throw Error('reply_unavailable');
      saveDraft({...draftRecord(false),reply});feedback(composer,'');input?.focus({preventScroll:true});
    }catch{if(!life.signal.aborted&&epoch===replyEpoch)feedback(composer,text('replyUnavailable'),true)}
    finally{clearTimeout(timeout);if(!life.signal.aborted&&epoch===replyEpoch){preparingReply=false;renderReply()}}
  });

  const roomDraftKey=draftKey?draftKey.replace('oie.draft.v1','oie.room-operation.v1'):'';
  let roomOperation=null;
  if(roomDraftKey){try{roomOperation=JSON.parse(localStorage.getItem(roomDraftKey)||'null')||inMemory.get(roomDraftKey)||null}catch{roomOperation=inMemory.get(roomDraftKey)||null}}
  if(roomOperation&&(!canonicalID(roomOperation.operationKey)||!['join','leave'].includes(roomOperation.command)||roomOperation.conversationId!==body.dataset.conversationId||roomOperation.accountId!==body.dataset.accountId))roomOperation=null;
  function keepRoomOperation(value){if(!value&&roomOperation){const region=document.querySelector('#room-session-status');if(region)region.dataset.version='';}roomOperation=value;if(!roomDraftKey)return;if(value)inMemory.set(roomDraftKey,value);else inMemory.delete(roomDraftKey);try{if(value)localStorage.setItem(roomDraftKey,JSON.stringify(value));else localStorage.removeItem(roomDraftKey)}catch{storageWarning()}}
  function restoreRoomOperation(){
    if(!roomOperation)return;
    const form=document.querySelector('[data-room-command]');if(!form)return;
    form.action='/ui/rooms/'+roomOperation.command;form.dataset.oieAction=roomOperation.command+'_room';form.dataset.roomCommand=roomOperation.command;
    for(const key of ['operationKey','expectedRevisionId','nickname']){const field=form.elements.namedItem(key);if(field)field.value=roomOperation[key]||'';}
    for(const field of form.querySelectorAll('input:not([type=hidden])'))field.readOnly=true;
    const button=form.querySelector('[data-room-action]');if(button){button.dataset.roomAction=roomOperation.command;button.textContent=text('retry');button.title=text('retry');}
    const region=form.querySelector('[data-form-feedback]');if(region){region.textContent=text('roomCommandUnknown');region.dataset.error='true';region.setAttribute('role','alert');}
  }
  function syncRoomLeave(){
    const item=document.getElementById('chat-leave-room'),form=document.querySelector('[data-room-command]'),room=document.getElementById('room-session-status');
    if(item)item.disabled=stopped||!form||form.dataset.roomCommand!=='leave'||room?.dataset.roomActive!=='true'||form.dataset.busy==='true'||!!(roomOperation&&roomOperation.command!=='leave');
  }
  life.on(window,'oie:leave-room',()=>{
    const form=document.querySelector('[data-room-command]'),room=document.getElementById('room-session-status');
    if(mock||stopped||!form||form.dataset.busy==='true')return;
    if(roomOperation&&roomOperation.command!=='leave'){window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'room-session-dialog'}}));return}
    if(form.dataset.roomCommand!=='leave'||room?.dataset.roomActive!=='true')return;
    document.getElementById('chat-leave-room')?.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
    form.dataset.roomMenuSubmit='true';try{form.requestSubmit()}finally{delete form.dataset.roomMenuSubmit}
  });
  restoreRoomOperation();syncRoomLeave();
  function mutateDOM(target, html) {
    if (!target) return;
    if(window.OieVideos?.swap(target,html))return;
    const apply = () => {
      for (const child of target.children) window.Alpine?.destroyTree(child);
      target.innerHTML = html;
      for (const child of target.children) window.Alpine?.initTree(child);
    };
    if (window.Alpine?.mutateDom) window.Alpine.mutateDom(apply); else apply();
  }
  function refreshAccountStatuses(root, nextRoot) {
    for (const status of root.querySelectorAll('[data-account-status]')) {
      const next = nextRoot.querySelector(`[data-account-status="${CSS.escape(status.dataset.accountStatus)}"]`);
      if (next && status.dataset.connectionState !== next.dataset.connectionState) {
        status.dataset.connectionState = next.dataset.connectionState;
        status.title = next.title;
        status.setAttribute('aria-label',next.getAttribute('aria-label'));
        mutateDOM(status,next.innerHTML);
      }
    }
  }
  function localizeTimes() {
    const locale=document.documentElement.lang || undefined;
    const clock=new Intl.DateTimeFormat(locale,{hour:'2-digit',minute:'2-digit'});
    for (const time of document.querySelectorAll('time[datetime]:not([data-day])')) {
      const date=new Date(time.dateTime);if(!Number.isNaN(date.getTime()))time.textContent=time.matches('[data-viewer-date],[data-capability-date],[data-details-date],[data-shared-source-date]')?new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeStyle:'short'}).format(date):clock.format(date);
    }
    for(const label of document.querySelectorAll('[data-message-edited][data-edited-at]')){const at=new Date(label.dataset.editedAt);if(!Number.isNaN(at.getTime()))label.title=new Intl.DateTimeFormat(locale,{dateStyle:'medium',timeStyle:'short'}).format(at);}
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
    highlightTimer=life.timeout(()=>{row.classList.remove('oie-reply-highlight');document.querySelector(`#message-timeline [data-message-id="${CSS.escape(id)}"]`)?.classList.remove('oie-reply-highlight');highlightedMessage='';},1800);
    return true;
  }
  life.on(document,'click',event=>{
    const reply=event.target.closest('[data-reply-target]');
    if(!reply||event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey||!body.dataset.accountId||reply.dataset.replyConversation!==body.dataset.conversationId)return;
    let target;try{target=new URL(reply.href,location.href)}catch{return}
    const account=target.searchParams.get('account');
    if(target.origin!==location.origin||target.pathname!=='/'||target.searchParams.get('chat')!==body.dataset.conversationId||(account&&account!==body.dataset.accountId))return;
    if(focusMessage(reply.dataset.replyTarget)){event.preventDefault();event.stopPropagation();}
  },true);
  life.on(document,'click',event=>{
    if(event.target.closest('[data-jump-latest]')){
      if(pinned()||new URLSearchParams(location.search).has('message')){const url=new URL(location.href);url.searchParams.delete('message');window.OieNavigation?.go(url.pathname+url.search) ?? location.assign(url.pathname+url.search);}
      else scrollLatest();
    }
  });
  life.on(document.querySelector('#message-timeline'),'scroll',()=>{if(nearBottom(document.querySelector('#message-timeline')))showJump(pinned());});
  localizeTimes();if(!body.dataset.focusMessage||!focusMessage(body.dataset.focusMessage))scrollLatest();else showJump(true);

  async function refresh() {
    if (stopped || mock) return;
    if (pendingRequests || refreshing) { refreshAgain = true; return; }
    refreshing = true;
    const starGeneration=Number(body.dataset.starGeneration||0);
    const requestedSearch=location.search;
    const controller = new AbortController(); const timeout = life.timeout(() => controller.abort(), 12000);
    try {
      const response = await life.fetch('/ui/refresh'+requestedSearch, {credentials:'same-origin',cache:'no-store',signal:controller.signal});
      if(life.signal.aborted)return;
      if (!response.ok) throw new Error('refresh unavailable');
      const page = new DOMParser().parseFromString(await response.text(), 'text/html');
      if(life.signal.aborted)return;
      if(requestedSearch!==location.search){refreshAgain=true;return;}
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
      const room=document.querySelector('#room-session-status'),nextRoom=page.querySelector('#live-room-session #room-session-status');
      if(room&&nextRoom&&composer){
        for(const key of ['sendAllowed','composeAllowed','replyAllowed'])composer.dataset[key]=nextRoom.dataset[key]||'false';
        input.disabled=composer.dataset.composeAllowed!=='true'&&!journal?.pending;
        if(room.dataset.version!==nextRoom.dataset.version&&!room.querySelector('input[name="nickname"]')?.matches(':focus')){
          const previousFocus=room.contains(document.activeElement),previousFeedback=room.querySelector('[data-form-feedback]'),previousCommand=room.querySelector('[data-room-command]')?.dataset.roomCommand;
          const savedFeedback=previousFeedback?.textContent,savedError=previousFeedback?.dataset.error;
          mutateDOM(room,nextRoom.innerHTML);room.dataset.version=nextRoom.dataset.version;room.dataset.roomState=nextRoom.dataset.roomState;room.dataset.roomActive=nextRoom.dataset.roomActive;for(const key of ['sendAllowed','composeAllowed','replyAllowed'])room.dataset[key]=nextRoom.dataset[key];
          const currentFeedback=room.querySelector('[data-form-feedback]');if(savedFeedback&&currentFeedback&&previousCommand===room.querySelector('[data-room-command]')?.dataset.roomCommand){currentFeedback.textContent=savedFeedback;currentFeedback.dataset.error=savedError||'false';}
          if(previousFocus){const target=currentFeedback?.textContent?currentFeedback:room.querySelector('[data-room-status-label]');if(target){target.tabIndex=-1;target.focus({preventScroll:true})}}
        }
        restoreRoomOperation();renderReply();syncRoomLeave();
      }
      const history=document.querySelector('#history-status'),newHistory=page.querySelector('#live-history-status');if(history&&newHistory&&history.innerHTML!==newHistory.innerHTML)mutateDOM(history,newHistory.innerHTML);
      const list = document.querySelector('#conversation-list');
      const nextList = page.querySelector('#live-list');
      if (list && nextList && list.dataset.version !== nextList.dataset.version) {
        const scroll = list.scrollTop;
        const focused = list.contains(document.activeElement) ? document.activeElement?.closest('[data-conversation-id]')?.dataset.conversationId : '';
        mutateDOM(list,nextList.innerHTML); list.dataset.version=nextList.dataset.version; list.scrollTop = scroll;
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
          if(activeRow&&!active?.isConnected){const row=timeline.querySelector(`[data-message-id="${CSS.escape(activeRow)}"]`);const target=(activeID&&document.getElementById(activeID))||(activeReply&&row?.querySelector(`[data-reply-target="${CSS.escape(activeReply)}"]`))||row;target?.focus({preventScroll:true});}
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
          refreshAccountStatuses(existing,fresh);
          const oldError=existing.querySelector('[id^="connection-error-"]'),newError=fresh.querySelector('[id^="connection-error-"]');
          if(oldError&&newError&&oldError.innerHTML!==newError.innerHTML)mutateDOM(oldError,newError.innerHTML);
          const controls=existing.querySelector('[id^="connection-controls-"]'),nextControls=fresh.querySelector('[id^="connection-controls-"]');
          // State transitions replace obsolete actions. Same-state refreshes keep
          // password/endpoint edits, accordion state and pending feedback intact.
          if(controls&&nextControls&&controls.dataset.connectionState!==nextControls.dataset.connectionState){
            const restoreFocus=controls.contains(document.activeElement);
            mutateDOM(controls,nextControls.innerHTML);controls.dataset.connectionState=nextControls.dataset.connectionState;
            if(restoreFocus)controls.querySelector('button[type="submit"]')?.focus({preventScroll:true});
          }
        }
      }
      const detailsRegion=page.querySelector('#live-conversation-details');
      if(detailsRegion?.dataset.detailsConversation===body.dataset.conversationId){
        const detailsScroll=document.getElementById('details-body')?.scrollTop;
        for(const section of ['identity','description','account','metadata','participants']){
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
        refreshAccountStatuses(menu,nextMenu);
      }
      document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));document.dispatchEvent(new CustomEvent("oie:shared-refresh",{detail:page}));applyAvatars();document.dispatchEvent(new CustomEvent("oie:stars-refresh",{detail:{page,generation:starGeneration}}));document.dispatchEvent(new CustomEvent("oie:previews-refresh",{detail:page}));localizeTimes();syncRoomLeave();
    } catch { if(!life.signal.aborted)announce(text('liveRetry')); }
    finally {
      clearTimeout(timeout);if(life.signal.aborted)return; refreshing = false;
      if (refreshAgain) { refreshAgain = false; scheduleRefresh(); }
    }
  }
  life.on(document,'oie:info-content',()=>{applyAvatars();localizeTimes();});
  life.on(document,'oie:shared-append',event=>{if(event.detail?.page)readAvatars(event.detail.page);applyAvatars();localizeTimes();});
  for(const name of ['oie:shared-search-settled','oie:info-shared-search-settled'])life.on(document,name,event=>{if(event.detail?.page)readAvatars(event.detail.page);applyAvatars();localizeTimes();});
  life.on(document,'oie:refresh-request',()=>scheduleRefresh());
  function scheduleRefresh() { if(stopped||life.signal.aborted)return;clearTimeout(refreshTimer); refreshTimer = life.timeout(refresh,100); }

  life.on(document,'submit', async event => {
    const form = event.target.closest('form[data-oie-action]');
    if (!form || mock) return;
    event.preventDefault();
    if (form.dataset.busy === 'true' || stopped) return;
    const sending = form.dataset.oieAction === 'send';
    if (sending) {
      if(preparingReply){feedback(form,text('replyPreparing'));return}
      if (form.dataset.sendAllowed !== 'true'&&!journal?.pending) return;
      if(!journal?.pending&&journal?.reply){
        const requested=journal.reply.messageId;preparingReply=true;renderReply();
        const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
        try{
          const query=new URLSearchParams({engineId:body.dataset.engineId,conversationId:body.dataset.conversationId,accountId:body.dataset.accountId,messageId:requested});
          const response=await life.fetch('/ui/reply-source?'+query,{credentials:'same-origin',cache:'no-store',signal:controller.signal}),reply=await response.json();
          if(life.signal.aborted)return;
          if(!response.ok||reply.engineId!==body.dataset.engineId||reply.accountId!==body.dataset.accountId||reply.conversationId!==body.dataset.conversationId||reply.requestedMessageId!==requested||!canonicalID(reply.messageId)||typeof reply.senderName!=='string'||typeof reply.excerpt!=='string'||Array.from(reply.excerpt).length>512)throw Error('reply_unavailable');
          saveDraft({...draftRecord(false),reply});
        }catch{if(!life.signal.aborted)feedback(form,text('replyUnavailable'),true);return}
        finally{clearTimeout(timeout);if(!life.signal.aborted){preparingReply=false;renderReply()}}
      }
      const value = journal?.pending ? journal.text : input.value;
      if (!value.trim() || Array.from(value).length > 16384) { feedback(form,text('invalidMessage'),true);input.focus();return; }
      if (journal?.pending) { input.value=journal.text; submissionKey.value=journal.key; }
      saveDraft({text:value,key:submissionKey.value,pending:true,reply:journal?.reply||null});lockComposer(true);renderReply();
    }
    const values = new URLSearchParams(new FormData(form));
    const roomCommand=form.dataset.roomCommand;
    const roomMenuSubmit=roomCommand==='leave'&&form.dataset.roomMenuSubmit==='true';
    if(roomCommand){if(roomOperation){for(const key of ['operationKey','expectedRevisionId','nickname','conversationId','accountId'])values.set(key,roomOperation[key]||'')}else keepRoomOperation({command:roomCommand,operationKey:values.get('operationKey'),expectedRevisionId:values.get('expectedRevisionId'),nickname:values.get('nickname')||'',conversationId:values.get('conversationId'),accountId:values.get('accountId')})}

    if(sending){values.set('originalText',journal.text);values.set('replyToMessageId',journal.reply?.messageId||'');}
    const buttons = [...form.querySelectorAll('input:not([type=hidden]),textarea,select,button')];
    const disabled = buttons.map(button => button.disabled);
    form.dataset.busy = 'true'; form.setAttribute('aria-busy','true'); buttons.forEach(button => button.disabled = true);syncRoomLeave();
    for (const secret of form.querySelectorAll('input[type="password"]')) secret.value = '';
    feedback(form,text('saving'));
    pendingRequests++;
    const controller = new AbortController(); const timeout = life.timeout(() => controller.abort(),15000);
    try {
      const response = await life.fetch(form.action,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal});
      const result = await response.json();
      if(life.signal.aborted)return;
      if (!result.ok) {
        feedback(form,result.message || text('pending'),true);
        if(result.code==='engine_changed'){stopped=true;source?.close();}
        if (sending && result.rejected) { saveDraft(draftRecord(false));lockComposer(false); }
        if (sending) input.focus({preventScroll:true});
        if(roomCommand&&result.rejected){keepRoomOperation(null);for(const field of form.querySelectorAll('input:not([type=hidden])'))field.readOnly=false;}
        if(!sending)scheduleRefresh();
      } else {
        if(roomCommand)keepRoomOperation(null);
        feedback(form,sending ? '' : result.message || '');
        if (sending && result.accepted) {
          const submittedKey = values.get('operationKey');
          clearDraft(submittedKey);input.value='';submissionKey.value=result.nextKey;lockComposer(false);forceBottom=true;
          input.focus({preventScroll:true});renderReply();
        } else if (form.dataset.oieAction === 'register' || form.dataset.oieAction === 'open') {
          if (result.redirect) { if(window.OieNavigation)window.OieNavigation.go(result.redirect);else location.assign(result.redirect); return; }
        }
        const key = form.elements.namedItem('operationKey'); if (key && result.nextKey) key.value=result.nextKey;
        scheduleRefresh();
      }
    } catch {
      if(life.signal.aborted)return;
      feedback(form,sending ? text('pending') : text('liveRetry'),true);
      if (sending) input.focus({preventScroll:true});
    } finally {
      clearTimeout(timeout);if(life.signal.aborted)return;pendingRequests--;form.dataset.busy='false';form.removeAttribute('aria-busy');
      buttons.forEach((button,index)=>button.disabled=disabled[index]);renderReply();syncRoomLeave();
      if(roomMenuSubmit&&form.querySelector('[data-form-feedback]')?.dataset.error==='true'){
        window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'room-session-dialog'}}));
        life.raf(()=>form.querySelector('[data-form-feedback]')?.focus({preventScroll:true}));
      }
      if(stopped&&composer)composer.querySelector('[data-send-message]').disabled=true;
      if (refreshAgain) { refreshAgain=false;scheduleRefresh(); }
    }
  });
  function retryPending() { if (composer && journal?.pending && composer.dataset.busy !== 'true' && !stopped && navigator.onLine) composer.requestSubmit(); }
  function connect(cursor) {
    if (mock || stopped) return;
    source?.close();announce(text('liveConnecting'),true);
    source = new EventSource('/ui/events?cursor='+encodeURIComponent(cursor || ''));
    source.onopen = () => { if(stopped)return;announce(text('liveReady'),true);scheduleRefresh();retryPending(); };
    source.onerror = () => {if(!stopped)announce(text('liveRetry'));};
    for (const type of ['message.preview_changed','profile.avatar_changed','account.registered','account.connection_changed','account.history_changed','conversation.created','conversation.updated','conversation.room_session_changed','message.created','message.updated']) life.on(source,type,scheduleRefresh);
    life.on(source,'message.star_changed',event=>{try{document.dispatchEvent(new CustomEvent('oie:star-event',{detail:JSON.parse(event.data).star}));}catch{}scheduleRefresh();});
    life.on(source,'oie.reset', event => {
      try {
        const reset=JSON.parse(event.data);
        if (body.dataset.engineId && reset.engineId !== body.dataset.engineId) {stopped=true;source.close();announce(text('engineChanged'));return;}
        scheduleRefresh();connect(reset.cursor);
      }catch{announce(text('liveRetry'))}
    });
  }
  connect(body.dataset.eventCursor);
  // Bounded recovery polling also covers a proxy that buffers or disables SSE.
  life.interval(() => { if (!mock && !document.hidden) {scheduleRefresh();retryPending();} },15000);
  life.on(window,'online', () => {scheduleRefresh();retryPending();});
  const open = new URLSearchParams(location.search).get('open') || body.dataset.openDrawer;
  if (open === 'accounts' || open === 'newchat' || open === 'details') {
    const show = () => {
      dispatchEvent(new CustomEvent('drawer:open',{detail:{id:open}}));
      if(open==='accounts')life.timeout(()=>{
        const query=new URLSearchParams(location.search);
        const target=query.get('accountPage')==='capabilities'?document.querySelector('[data-account-capabilities-title]'):query.get('accountFocus')==='capabilities'?document.querySelector('[data-account-capabilities]'):null;
        target?.focus({preventScroll:true});
      },230);
    };
    if (window.Alpine) life.timeout(show,0); else life.on(document,'alpine:initialized',show,{once:true});
  }
  const nativeError=document.querySelector('[data-native-error]');if(nativeError)nativeError.focus();
});
