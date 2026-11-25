window.OieRuntime.register('live', (life, route) => {
  'use strict';
  const body = document.body;
  body.dataset.oieLiveReady='true';
  const labels = document.querySelector('#oie-live-copy')?.dataset || {};
  const offlineCopy = document.querySelector('[data-pending-send-template]')?.dataset || {};
  const text = key => labels[key] || '';
  const status = document.querySelector('[data-live-status]');
  const announce = (message, quiet = false) => { if (status) { status.textContent = message; status.hidden = quiet || !message; } };
  const errorToast = message => { if (message) window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message}})); };
  const mock = body.dataset.mock === 'true';
  const recoveringView = document.getElementById('oie-route')?.hasAttribute('data-engine-unavailable');
  const events = window.OieEvents, inbox = window.OieInbox;
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content || '';
  let accountRenameEpoch = 0;
  let pendingRequests = 0, refreshTimer, refreshing = false, refreshAgain = false;
  const initialList = document.getElementById('conversation-list');
  let appliedCursor = initialList?.dataset.inboxCursor === body.dataset.eventCursor && inbox.current(initialList) ? body.dataset.eventCursor : '';
  let inboxEventCursor = '', changedRow = '', fullRefresh = false;
  let pendingAppend;
  let lastRefreshStart = -Infinity;
  let sourceNavigation, refreshController;
  let stopped = false, storageDenied = false;
  life.cleanup(()=>{stopped=true;});
  let accountsVersion = [...document.querySelectorAll('#account-blocks>[data-account-id]')].map(e => e.dataset.accountId+';').join('');
  // Exact account/address tuples avoid nickname guesses and cross-account leaks.
  let avatarCache = window.OieRuntime.memory.get('avatar-cache');
  if (!avatarCache || avatarCache.engine !== body.dataset.engineId) {
    avatarCache = {engine: body.dataset.engineId, sources: new Map(), accounts: new Set()};
    window.OieRuntime.memory.set('avatar-cache', avatarCache);
  }
  const avatarSources = avatarCache.sources, loadedAvatarAccounts = avatarCache.accounts;
  const avatarKey = (account,address) => JSON.stringify([account,address]);
  function readAvatars(root) {
    const data = root.querySelector('#oie-avatar-data');
    if (!data) return;
    const loaded = new Set([...data.querySelectorAll('[data-avatar-loaded-account]')].map(el=>el.dataset.avatarLoadedAccount));
    for (const account of loaded) loadedAvatarAccounts.add(account);
    for (const key of avatarSources.keys()) if (loaded.has(JSON.parse(key)[0])) avatarSources.delete(key);
    for (const entry of data.querySelectorAll('[data-avatar-entry]')) {
      const {accountId,address,hash} = entry.dataset;
      if (/^[0-9a-f]{64}$/.test(hash)) avatarSources.set(avatarKey(accountId,address),'/ui/avatars/'+hash);
      else if (!hash) avatarSources.delete(avatarKey(accountId,address));
    }
  }
  function applyAvatars(root = document) {
    for (const scope of root.querySelectorAll('[data-avatar-account]')) {
      const key = avatarKey(scope.dataset.avatarAccount,scope.dataset.avatarAddress);
      if (!loadedAvatarAccounts.has(scope.dataset.avatarAccount) && !avatarSources.has(key)) continue;
      const src = avatarSources.get(key) || '';
      if(scope.hasAttribute('data-inbox-avatar-deferred')){
        if(!src)continue;
        scope.dataset.avatarSrc=src;
        if(!window.Alpine)continue;
        window.Alpine.mutateDom(()=>{
          window.Alpine.destroyTree(scope);
          scope.removeAttribute('x-ignore');
          scope.removeAttribute('data-inbox-avatar-deferred');
          window.Alpine.initTree(scope);
        });
        continue;
      }
      if(scope.dataset.avatarSrc!==src)scope.dataset.avatarSrc=src;
      const data = window.Alpine?.$data?.(scope);
      if (data && 'oieAvatarSrc' in data && data.oieAvatarSrc!==src) data.oieAvatarSrc = src;
    }
  }
  readAvatars(document);
  life.on(document,'alpine:initialized',()=>applyAvatars());life.raf(()=>applyAvatars());
  life.on(document,'oie:message-search-settled',event=>{
    applyAvatars(event.detail.root);
    localizeTimes(event.detail.root);
  });
  const memoryKey='device-drafts';
  if(!window.OieRuntime.memory.has(memoryKey))window.OieRuntime.memory.set(memoryKey,new Map());
  const inMemory=window.OieRuntime.memory.get(memoryKey);
  const composer = document.querySelector('#send-message-form');
  const input = document.querySelector('#message-composer');
  const submissionKey = composer?.elements.namedItem('operationKey');
  const composerSend = composer?.querySelector('[data-send-message], #composer-send');
  const draftSwitch = composer?.querySelector('[data-draft-switch]');
  if(composerSend) composerSend.dataset.sendMessage='';
  let editingSchedule = composer?.hasAttribute('data-editing-schedule');
  const readScheduledMessage = () => JSON.parse(document.getElementById('schedule-edit-data')?.textContent || 'null');
  let scheduledMessage = readScheduledMessage();
  const scheduleRetry = composer?.querySelector('[data-schedule-retry]');
  const scheduleReviewChanged = document.querySelector('[data-schedule-dialog]')?.dataset.reviewChanged;
  const ordinaryDraftKey = body.dataset.engineId && body.dataset.conversationId && body.dataset.accountId
    ? ['oie.draft.v1', body.dataset.engineId, body.dataset.accountId, body.dataset.conversationId].join(':') : '';
  const offlineEngine = body.dataset.engineId;
  const offlinePrefix = 'oie.offline-send.v1:' + offlineEngine + ':';
  const acceptedSendPrefix = 'oie.send-accepted.v1:' + offlineEngine + ':';
  const offlineKey = ordinaryDraftKey.replace('oie.draft.v1', 'oie.offline-send.v1');
  const recordingUploadClaimKey = offlineKey.replace('oie.offline-send.v1', 'oie.voice-upload-claim.v1');
  const resettingDevice = () => window.OieRuntime.memory.has('device-reset');
  const recoveredDraftPrefix = ordinaryDraftKey.replace('oie.draft.v1', 'oie.recovered-draft.v1') + ':';
  let selectedRecoveredDraft = '';
  try { selectedRecoveredDraft = sessionStorage.getItem(recoveredDraftPrefix) || ''; } catch {}
  const scheduleDraftKey = () => ordinaryDraftKey + ':scheduled:' + scheduledMessage.id + ':' + scheduledMessage.revisionId;
  let draftKey = editingSchedule ? scheduleDraftKey() : ordinaryDraftKey;
  const conversationKind = composer?.elements.namedItem('expectedConversationKind')?.value;
  let journal,preparingReply=false,replyEpoch=0;
  const replyField=composer?.elements.namedItem('replyToMessageId');
  const mentionField=composer?.elements.namedItem('mentions');
  function readMentions(){try{const records=JSON.parse(mentionField?.value||'[]');return Array.isArray(records)&&records.length<=32?records:[]}catch{return []}}
  function restoreMentions(records){if(!mentionField)return;mentionField.value=JSON.stringify(Array.isArray(records)?records:[]);composer.dispatchEvent(new CustomEvent('oie:mentions-restore'));}
  const replyBox=composer?.querySelector('[data-composer-reply]');
  const canonicalID=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
  // Sent corrections have a separate journal. They never overwrite the ordinary
  // message draft, its reply target, mention selections or original send key.
  const editBox=composer?.querySelector('[data-composer-edit]'),editFeedback=editBox?.querySelector('[data-edit-feedback]'),editRecovery=editBox?.querySelector('[data-edit-recovery]'),lastEditStatus=composer?.querySelector('[data-edit-last-status]');
  const editKey=ordinaryDraftKey.replace('oie.draft.v1','oie.correction.v1');
  const lastEditKey=editKey?editKey.replace('oie.correction.v1','oie.correction-receipt.v1'):'';
  const announceEdit=message=>{const region=composer?.querySelector('[data-edit-announcement]');if(region)region.textContent=message;};
  const editText=key=>editBox?.dataset['edit'+key[0].toUpperCase()+key.slice(1)]||'';
  let editSession=null,editPreparing=false,editBusy=false,editEpoch=0,editPoll=0,editComposing=false,editStorageDenied=false,lastEditReceipt=null,lastEditChecking=false,lastEditPoll=0;
  const editScope=value=>value&&value.engineId===body.dataset.engineId&&value.accountId===body.dataset.accountId&&value.conversationId===body.dataset.conversationId;
  function validEditSession(value){return editScope(value)&&['messageId','expectedRevisionId','operationKey'].every(key=>canonicalID(value[key]))&&typeof value.text==='string'&&Array.from(value.text).length<=16384&&typeof value.submitted==='boolean';}
  function keepEdit(value){
    editSession=value;if(!editKey)return;
    if(value)inMemory.set(editKey,value);else inMemory.delete(editKey);
    try{if(value)localStorage.setItem(editKey,JSON.stringify(value));else localStorage.removeItem(editKey)}catch{editStorageDenied=true;storageWarning();}
  }
  function editMessageBody(id){return id&&document.querySelector(`#message-timeline [data-message-id="${CSS.escape(id)}"] [data-message-bubble]`);}
  function mountEditIndicators(bubble){
    const slot=bubble?.querySelector('[data-edit-indicators]');
    if(!slot)return;
    const controls=window.OieRuntime.messageControls('edit',bubble.dataset.messageBubble);
    if(controls)slot.replaceWith(controls);
  }
  function renderEditMarkers(){
    for(const node of document.querySelectorAll('[data-editing-message],[data-edit-pending]')){node.removeAttribute('data-editing-message');node.removeAttribute('data-edit-pending');node.removeAttribute('aria-busy');}
    const fallback=composer?.querySelector('.oie-composer-field');
    const place=(node,id)=>{const bubble=editMessageBody(id),target=bubble||fallback;if(node&&target&&node.parentElement!==target)target.append(node);if(node){const top=bubble&&!node.hidden?document.querySelector('#message-timeline').getBoundingClientRect().top:null;node.toggleAttribute('data-edit-below',top!==null&&bubble.getBoundingClientRect().top-top<100);}return bubble;};
    const bubble=place(editBox,editSession?.messageId);
    const state=editSession?.command?.state;
    if(bubble&&editSession){mountEditIndicators(bubble);bubble.dataset.editingMessage='true';if(editBusy||editSession.submitted&&(!state||state==='queued'||state==='dispatching')){bubble.dataset.editPending='true';bubble.setAttribute('aria-busy','true');}}
    const previous=place(lastEditStatus,lastEditReceipt?.messageId);
    if(lastEditStatus)lastEditStatus.hidden=!lastEditReceipt?.command?.state||['queued','dispatching','submitted'].includes(lastEditReceipt.command.state)||editSession?.messageId===lastEditReceipt.messageId;
    if(previous&&['queued','dispatching'].includes(lastEditReceipt?.command?.state)&&!previous.hasAttribute('data-editing-message')){mountEditIndicators(previous);previous.dataset.editPending='true';previous.setAttribute('aria-busy','true');}
  }
  function renderEdit(){
    if(!editBox)return;
    composer.dataset.editing=String(!!editSession||editPreparing);
    editBox.hidden=!editSession||editBusy||['queued','dispatching'].includes(editSession.command?.state)||!(editSession.error||editSession.submitted||editSession.rejected||editStorageDenied);
    composer.setAttribute('aria-busy',String(editPreparing||editBusy));
    if(editFeedback){const state=editSession?.command?.state;editFeedback.textContent=editPreparing?editText('preparing'):editSession?.error||(state?editText(state):editSession?.submitted?editText('acceptanceUnknown'):editStorageDenied?editText('storage'):'');}
    const cancel=composer.querySelector('[data-edit-cancel]'),save=composerSend,check=editBox.querySelector('[data-edit-check]'),retry=editBox.querySelector('[data-edit-retry]');
    if(cancel){cancel.hidden=!editSession;cancel.disabled=editBusy||!!editSession?.submitted;}
    const dismiss=editBox.querySelector('[data-edit-dismiss]');if(dismiss){dismiss.hidden=!editSession?.command;dismiss.disabled=editBusy;}
    if(save){save.toggleAttribute('data-edit-save',!!editSession);if(editSession){save.disabled=editBusy||editSession.submitted||editSession.rejected===true||!input.value.trim()||Array.from(input.value).length>16384;}save.setAttribute('aria-label',editSession?editText('saveLabel'):(editingSchedule?(save.querySelector('[data-schedule-save-label]')?.textContent||text('send')):text('send')));save.title=save.getAttribute('aria-label');}
    if(editRecovery)editRecovery.hidden=!editSession?.submitted||editBusy;
    if(check)check.disabled=editBusy;
    if(retry)retry.hidden=!!editSession?.command||editSession?.rejected===true;
    if(editSession){input.readOnly=editBusy||editSession.submitted||editSession.rejected===true;input.setAttribute('aria-readonly',String(input.readOnly));}
    renderEditMarkers();
    for(const action of document.querySelectorAll('[id^="edit-message-"]')){
      action.hidden=editingSchedule||action.id!=='edit-message-'+(composer.dataset.latestEditMessage||'');
      action.setAttribute('aria-disabled',String(editBusy||editPreparing||!!editSession||!!journal?.pending));
    }
  }
  function restoreOrdinaryDraft(){
    input.value=journal?.pending?'':journal?.text||'';if(journal?.key)submissionKey.value=journal.key;restoreMentions(journal?.mentions);lockComposer(!!journal?.pending);renderReply();renderEdit();
    const target = composer.querySelector('[data-record-audio]:not([hidden]) [data-record-audio-remove]') || input;
    target.focus({preventScroll:true});
  }
  function leaveEdit(){if(editBusy||editSession?.submitted)return;if(editPreparing)window.OieTimeline?.cancelFocus();editEpoch++;editPreparing=false;clearTimeout(editPoll);keepEdit(null);restoreOrdinaryDraft();}
  function finishEdit(){keepLastEdit(editSession?{...editSession}:null);editEpoch++;clearTimeout(editPoll);editBusy=false;editPreparing=false;keepEdit(null);restoreOrdinaryDraft();scheduleRefresh();}
  function editMenuOpen(){return input.getAttribute('aria-expanded')==='true'||[...document.querySelectorAll('[role="dialog"],[role="menu"],[data-emoji-trigger][aria-expanded="true"]')].some(node=>node.getClientRects().length&&getComputedStyle(node).visibility!=='hidden');}
  async function beginEdit(messageId=''){
    if(!composer||!editBox||composer.hasAttribute('data-attachments-pending')||composer.hasAttribute('data-capturing-audio')||editingSchedule||composer.dataset.editAllowed!=='true'||journal?.pending||preparingReply||editBusy||editPreparing||editSession||stopped)return;
    const epoch=++editEpoch,draftAtRequest=input.value;editPreparing=true;feedback(composer,'');announceEdit(editText('preparing'));renderReply();renderEdit();
    const url=new URL(composer.dataset.editTargetUrl,location.origin);if(messageId)url.searchParams.set('messageId',messageId);else url.searchParams.delete('messageId');
    const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),10000);
    try{
      const response=await life.fetch(url,{credentials:'same-origin',cache:'no-store',headers:{Accept:'application/json'},signal:controller.signal}),value=await response.json();
      if(life.signal.aborted||epoch!==editEpoch)return;
      if(input.value!==draftAtRequest)return;
      const target=value.target;
      if(!response.ok||value.engineId!==body.dataset.engineId||!target||target.accountId!==body.dataset.accountId||target.conversationId!==body.dataset.conversationId||!canonicalID(target.messageId)||messageId&&target.messageId!==messageId||!canonicalID(target.currentRevisionId)||!canonicalID(value.operationKey)||!target.eligible||typeof target.originalText!=='string'||Array.from(target.originalText).length>16384)throw Error('edit_unavailable');
      if(!editMessageBody(target.messageId)&&window.OieTimeline){const found=await window.OieTimeline.focus(target.messageId,{focus:false});if(life.signal.aborted||epoch!==editEpoch||input.value!==draftAtRequest)return;if(!found)throw Error('edit_source_unavailable');}
      saveDraft(draftRecord(false));
      keepEdit({engineId:body.dataset.engineId,accountId:body.dataset.accountId,conversationId:body.dataset.conversationId,messageId:target.messageId,expectedRevisionId:target.currentRevisionId,operationKey:value.operationKey,text:target.originalText,submitted:false});
      input.value=target.originalText;restoreMentions([]);announceEdit(editText('mode'));
      renderReply();input.focus({preventScroll:true});input.setSelectionRange(input.value.length,input.value.length);
      const bubble=editMessageBody(target.messageId),viewport=document.querySelector('#message-timeline')?.getBoundingClientRect(),bounds=bubble?.getBoundingClientRect();if(bounds&&viewport&&(bounds.bottom<=viewport.top||bounds.top>=viewport.bottom))bubble.scrollIntoView({block:'nearest',behavior:'auto'});
    }catch{if(!life.signal.aborted&&epoch===editEpoch){errorToast(editText('unavailable'));}}
    finally{clearTimeout(timeout);if(!life.signal.aborted&&epoch===editEpoch){editPreparing=false;renderReply();renderEdit();}}
  }
  function editValues(intent=editSession){return new URLSearchParams({engineId:intent.engineId,accountId:intent.accountId,conversationId:intent.conversationId,messageId:intent.messageId,expectedRevisionId:intent.expectedRevisionId,operationKey:intent.operationKey,originalText:intent.text,csrf,lang:document.documentElement.lang});}
  function matchingEditCommand(command,intent=editSession){return command&&intent&&command.id===intent.operationKey&&command.accountId===intent.accountId&&command.conversationId===intent.conversationId&&command.messageId===intent.messageId&&command.expectedRevisionId===intent.expectedRevisionId&&command.originalText===intent.text&&canonicalID(command.revisionId)&&['queued','dispatching','submitted','unknown','failed'].includes(command.state);}
  async function submitEdit(receiptOnly=false){
    if(!editSession||editBusy||editPreparing||stopped)return;
    if(!editSession.submitted){if(receiptOnly||editSession.rejected)return;const value=input.value;if(!value.trim()||Array.from(value).length>16384)return;keepEdit({...editSession,text:value,submitted:true,error:'',rejected:false});}
    const epoch=++editEpoch;editBusy=true;clearTimeout(editPoll);renderReply();renderEdit();
    const values=editValues(),controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
    try{
      const response=await life.fetch(receiptOnly?'/ui/messages/edit-receipt':'/ui/messages/edit',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal}),result=await response.json();
      if(life.signal.aborted||epoch!==editEpoch)return;
      if(response.ok&&result.ok&&result.accepted&&matchingEditCommand(result.command)){
        keepEdit({...editSession,command:result.command,error:''});
        if(['queued','dispatching','submitted'].includes(result.command.state)){finishEdit();return;}
        if(result.command.state==='failed')keepEdit({...editSession,submitted:false,rejected:true});
      }else if(result.rejected===true){keepEdit({...editSession,submitted:false,rejected:true,error:result.message||editText('changed')});}
      else keepEdit({...editSession,error:result.message||editText('acceptanceUnknown')});
    }catch{if(!life.signal.aborted&&epoch===editEpoch)keepEdit({...editSession,error:editText('acceptanceUnknown')});}
    finally{
      clearTimeout(timeout);if(!life.signal.aborted&&epoch===editEpoch){editBusy=false;renderReply();renderEdit();const state=editSession?.command?.state;if(state==='queued'||state==='dispatching')editPoll=life.timeout(()=>submitEdit(true),1200);}
    }
  }
  function renderLastEdit(){const status=lastEditStatus;if(!status)return;const state=lastEditReceipt?.command?.state;status.hidden=!state||state==='submitted';status.querySelector('[data-edit-last-copy]').textContent=status.hidden?'':editText(state);const button=status.querySelector('[data-edit-last-check]');if(button)button.disabled=lastEditChecking;renderEditMarkers();}
  function keepLastEdit(value){lastEditReceipt=value;clearTimeout(lastEditPoll);if(lastEditKey){if(value)inMemory.set(lastEditKey,value);else inMemory.delete(lastEditKey);try{if(value)localStorage.setItem(lastEditKey,JSON.stringify(value));else localStorage.removeItem(lastEditKey)}catch{storageWarning();}}renderLastEdit();if(value&&['queued','dispatching'].includes(value.command?.state))lastEditPoll=life.timeout(()=>checkCompletedEdit(),1200);}
  async function checkCompletedEdit(){
    const intent=lastEditReceipt;if(!intent||lastEditChecking||!editScope(intent))return;lastEditChecking=true;renderLastEdit();
    const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),10000);
    try{const response=await life.fetch('/ui/messages/edit-receipt',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:editValues(intent),signal:controller.signal}),result=await response.json();if(life.signal.aborted||lastEditReceipt!==intent||!response.ok||!matchingEditCommand(result.command,intent))return;keepLastEdit({...intent,command:result.command});}
    catch{/* A missing refresh receipt does not change the verified last outcome. */}
    finally{clearTimeout(timeout);lastEditChecking=false;if(!life.signal.aborted)renderLastEdit();}
  }
  life.on(document,'oie:shared-refresh',()=>{void checkCompletedEdit()});
  life.cleanup(()=>{editEpoch++;clearTimeout(editPoll);clearTimeout(lastEditPoll)});
  life.on(document,'click',event=>{
    const action=event.target.closest('[id^="edit-message-"]');
    if(action&&!event.defaultPrevented&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey){event.preventDefault();event.stopPropagation();if(action.getAttribute('aria-disabled')!=='true'){action.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));void beginEdit(action.id.slice(13));}return;}
    if(event.target.closest('[data-edit-cancel]')){event.preventDefault();leaveEdit();}
    if(event.target.closest('[data-edit-dismiss]')){event.preventDefault();if(editSession?.command&&!editBusy)finishEdit();}
    if(event.target.closest('[data-edit-last-check]')){event.preventDefault();void checkCompletedEdit();}
    if(event.target.closest('[data-edit-save]')){event.preventDefault();void submitEdit(false);}
    if(event.target.closest('[data-edit-check]')){event.preventDefault();void submitEdit(true);}
    if(event.target.closest('[data-edit-retry]')){event.preventDefault();if(editSession&&!editSession.command)void submitEdit(false);}
  });
  life.on(input,'compositionstart',()=>{editComposing=true});life.on(input,'compositionend',()=>{editComposing=false});
  life.on(input,'keydown',event=>{
    if(event.defaultPrevented||event.isComposing||event.keyCode===229||editComposing||event.ctrlKey||event.altKey||event.metaKey||event.shiftKey)return;
    if(event.key==='Escape'&&(editSession||editPreparing)&&!editMenuOpen()){event.preventDefault();event.stopImmediatePropagation();leaveEdit();return;}
    if(event.key==='ArrowUp'&&(editSession||editPreparing)&&!editMenuOpen()){event.preventDefault();event.stopImmediatePropagation();return;}
    if(event.key==='ArrowUp'&&input.value===''&&!input.readOnly&&!input.disabled&&!editSession&&!editPreparing&&!journal?.pending&&!preparingReply&&!editMenuOpen()&&composer.dataset.editAllowed==='true'){event.preventDefault();void beginEdit();}
  });

  function compositionLocked(){
    return !!journal?.pending||!!editSession||editPreparing||preparingReply||stopped||composer?.dataset.busy==='true';
  }
  function scheduleBlocked(locked){
    return locked||!composer?.hasAttribute('data-schedule-composer')||composer.hasAttribute('data-attachments-pending')||composer.hasAttribute('data-capturing-audio')||
      (composer.dataset.sendAllowed!=='true'&&!editingSchedule);
  }

  let waitingForRecordingUpload = false;
  const recordingUploadPending = () => waitingForRecordingUpload || composer?.hasAttribute('data-waiting-recording-upload');

  function canQueueRecordingUpload() {
    if (!composer.hasAttribute('data-recording-upload') || composer.dataset.attachmentAllowed !== 'true') return false;
    // A queued recording that leaves this route may need the conversation's offline slot.
    try { return !hasDraft(parseDraft(localStorage.getItem(offlineKey))) && !readRecordingUploadClaim(); }
    catch { return true; }
  }

  function renderReply(){
    const reply=journal?.reply,locked=!!journal?.pending,editing=!!editSession||editPreparing;
    const waitingForAudio = recordingUploadPending();
    if(replyField)replyField.value=reply?.messageId||'';
    if(replyBox){replyBox.hidden=!reply||(locked&&!journal?.scheduledAt&&!editingSchedule)||!!editSession;replyBox.querySelector('[data-composer-reply-sender]').textContent=typeof reply?.senderName==='string'?reply.senderName:'';replyBox.querySelector('[data-composer-reply-excerpt]').textContent=typeof reply?.excerpt==='string'?reply.excerpt:'';const cancel=replyBox.querySelector('[data-cancel-reply]');if(cancel)cancel.disabled=locked||preparingReply||waitingForAudio;}
    for(const button of document.querySelectorAll('[id^="reply-message-"]'))button.disabled=!composer||composer.dataset.replyAllowed!=='true'||locked||preparingReply||editing||waitingForAudio;
    if(composerSend) composerSend.disabled=stopped||locked||preparingReply||editing||composer.dataset.busy==='true'||
      (composer.hasAttribute('data-attachments-pending')&&!waitingForAudio&&!canQueueRecordingUpload())||
      (composer.dataset.sendAllowed!=='true'&&!editingSchedule&&!locked);
    if(scheduleRetry){scheduleRetry.hidden=!locked||!journal.scheduledAt&&!editingSchedule;scheduleRetry.disabled=stopped||composer.dataset.busy==='true';}
    const composingLocked=compositionLocked();
    const scheduleDisabled=scheduleBlocked(composingLocked);
    for(const action of composer?.querySelectorAll('#composer-schedule-new, #composer-schedule-reschedule')||[])action.disabled=scheduleDisabled;
    composer?.dispatchEvent(new Event('oie:composer-preview-change'));
    composer?.dispatchEvent(new CustomEvent('oie:composer-attachments',{detail:{files:journal?.attachments||[],locked:composingLocked,editingSent:!!editSession}}));
    renderDraftSwitch();
  }
  function privateMapLinks() {
    try { return JSON.parse(composer?.dataset.privateMapLinks || '[]'); } catch { return []; }
  }
  function draftRecord(pending=false){return {text:input?.value||'',key:submissionKey?.value||'',pending,reply:journal?.reply||null,mentions:readMentions(),attachments:journal?.attachments||[],privateMapLinks:privateMapLinks().filter(url=>input?.value.includes(url))};}

  function scheduleReview(draft) {
    const {text, reply, mentions, attachments} = draft;
    return JSON.stringify([text, reply?.messageId, reply?.senderName, reply?.excerpt, mentions, attachments.map(file => file.id)]);
  }

  function storageWarning() {
    if (!storageDenied && composer) { const note=document.createElement('p');note.className='oie-storage-warning';note.role='status';note.textContent=text('storageUnavailable');composer.append(note); }
    storageDenied = true;
    announce(text('storageUnavailable'));
  }
  function readStoredDraft(key) {
    if (!key) return null;
    if (window.OieHostedSession) {
      try {
        const saved = sessionStorage.getItem(key);
        if (saved) return JSON.parse(saved);
      } catch {}
    }
    try { const raw = localStorage.getItem(key); return raw ? JSON.parse(raw) : inMemory.get(key)||null; }
    catch { storageWarning(); return inMemory.get(key) || null; }
  }
  function hasDraft(record) {
    return Boolean(record?.pending || record?.text || record?.reply || record?.attachments?.length);
  }
  function isOfflineIntent(record) {
    return record?.pending && ['waiting', 'attempted'].includes(record.offline);
  }
  function isVoiceSend(record) {
    return Array.isArray(record?.attachments) && record.attachments.some(file => file?.recordedAudio === true);
  }
  function readDraft() {
    const regular = readStoredDraft(draftKey);
    if (editingSchedule) return regular;
    if (selectedRecoveredDraft === offlineKey || selectedRecoveredDraft.startsWith(recoveredDraftPrefix)) {
      const recovered = readStoredDraft(selectedRecoveredDraft);
      if (hasDraft(recovered) && !isOfflineIntent(recovered)) return recovered;
    }
    selectRecoveredDraft('');
    if (hasDraft(regular) && !regular.offline) return regular;
    try {
      const pending = parseDraft(localStorage.getItem(offlineKey));
      if (hasDraft(pending)) {
        if (!isOfflineIntent(pending)) selectRecoveredDraft(offlineKey);
        return pending;
      }
      for (const key of Object.keys(localStorage)) {
        if (!key.startsWith(recoveredDraftPrefix)) continue;
        const recovered = readStoredDraft(key);
        if (hasDraft(recovered)) {
          selectRecoveredDraft(key);
          return recovered;
        }
      }
    } catch {}
    return regular;
  }
  function selectRecoveredDraft(key) {
    selectedRecoveredDraft = key;
    try {
      if (key) sessionStorage.setItem(recoveredDraftPrefix, key);
      else sessionStorage.removeItem(recoveredDraftPrefix);
    } catch {}
  }
  function draftStorageKey() {
    return !editingSchedule && selectedRecoveredDraft ? selectedRecoveredDraft : draftKey;
  }
  function nextDraftChoice() {
    if (!draftSwitch || mock || editingSchedule) return;
    try {
      const current = journal?.offline ? offlineKey : draftStorageKey();
      const keys = [offlineKey, ordinaryDraftKey,
        ...Object.keys(localStorage).filter(key => key.startsWith(recoveredDraftPrefix)).sort()];
      const after = keys.indexOf(current) + 1;
      for (const key of [...keys.slice(after), ...keys.slice(0, after)]) {
        if (key === current) continue;
        const record = readStoredDraft(key);
        if (hasDraft(record) && (key === offlineKey || !record.offline)) return {key, record};
      }
    } catch {}
  }
  function renderDraftSwitch() {
    if (!draftSwitch) return;
    const choice = nextDraftChoice();
    if (!choice && document.activeElement === draftSwitch) {
      (pendingBubble || input).focus({preventScroll:true});
    }
    draftSwitch.hidden = !choice;
    draftSwitch.disabled = stopped || preparingReply || !!editSession || editPreparing ||
      composer.dataset.busy === 'true' || (journal?.pending && !journal.offline) ||
      composer.hasAttribute('data-capturing-audio') || composer.hasAttribute('data-attachments-pending');
    if (choice) draftSwitch.textContent = isOfflineIntent(choice.record) ? draftSwitch.dataset.pendingLabel : draftSwitch.dataset.draftLabel;
  }
  life.on(draftSwitch, 'click', () => {
    renderDraftSwitch();
    if (draftSwitch.disabled) return;
    const choice = nextDraftChoice();
    if (!choice) return;
    composer.dispatchEvent(new Event('oie:attachment-draft-changing'));
    if (pendingBubble) {
      window.Alpine?.destroyTree(pendingBubble);
      pendingBubble.remove();
      pendingBubble = null;
    }
    selectRecoveredDraft(choice.key !== ordinaryDraftKey && !isOfflineIntent(choice.record) ? choice.key : '');
    restoreDraft(submissionKey.value, '', choice.record);
    renderReply();
    renderEdit();
    (pendingBubble || input).focus({preventScroll:true});
  });
  function saveDraft(record) {
    const key = draftStorageKey();
    if (!key) return;
    record = {...record, conversationKind: record.conversationKind || conversationKind};
    journal = record; inMemory.set(key, record);
    const serialized = JSON.stringify(record);
    try { localStorage.setItem(key, serialized); }
    catch {
      if (window.OieHostedSession) {
        try { sessionStorage.setItem(key, serialized); return; } catch {}
      }
      storageWarning();
      return;
    }
    clearSessionDraft(key);
  }
  function saveQueuedRecordingDraft(record, previous) {
    const stored = readStoredDraft(draftStorageKey());
    if (hasDraft(stored) && !sameComposition(stored, previous)) {
      const key = localOperationKey();
      selectRecoveredDraft(recoveredKey(offlineKey, key));
      submissionKey.value = key;
      record = {...record, key};
    }
    saveDraft(record);
  }
  function parseDraft(raw) {
    try { return JSON.parse(raw); } catch { return null; }
  }
  function sameComposition(draft, record) {
    return draft?.key === record.key && draft.text === record.text &&
      compositionIdentity(draft) === compositionIdentity(record);
  }
  function compositionIdentity(record) {
    return JSON.stringify([record.text, record.reply?.messageId || '', record.mentions || [],
      (record.attachments || []).map(file => file.id), record.scheduledAt || '',
      record.scheduledAt ? record.reply?.revisionId || '' : '']);
  }
  async function sendIdentity(record) {
    const bytes = new TextEncoder().encode(compositionIdentity(record));
    if (!crypto.subtle) {
      // FNV-1a fingerprints the composition on origins without WebCrypto.
      let hash = 0xcbf29ce484222325n;
      for (const byte of bytes) hash = BigInt.asUintN(64, (hash ^ BigInt(byte)) * 0x100000001b3n);
      return 'fnv1a64:' + hash.toString(16).padStart(16, '0');
    }
    const digest = await crypto.subtle.digest('SHA-256', bytes);
    return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
  }
  function readRecordingUploadClaim() {
    const claim = parseDraft(localStorage.getItem(recordingUploadClaimKey));
    return claim?.until > Date.now() ? claim : null;
  }
  async function claimRecordingUpload(token) {
    try {
      return await withOfflineLock(offlineKey, () => {
        if (resettingDevice() || readRecordingUploadClaim() || hasDraft(parseDraft(localStorage.getItem(offlineKey)))) return false;
        // Leave time for the 90 s upload and the final send preparation.
        localStorage.setItem(recordingUploadClaimKey, JSON.stringify({token, until:Date.now() + 120000}));
        return true;
      });
    } catch { return !resettingDevice(); }
  }
  function releaseRecordingUpload(token) {
    void withOfflineLock(offlineKey, () => {
      const claim = parseDraft(localStorage.getItem(recordingUploadClaimKey));
      if (claim?.token === token) localStorage.removeItem(recordingUploadClaimKey);
    }).catch(() => {});
  }
  async function saveOfflineSend(record, reservation, source) {
    const key = source?.key ?? draftStorageKey();
    const pending = {...record, pending: true, offline: 'waiting', conversationKind,
      submittedAt: record.submittedAt || new Date().toISOString()};
    const matchesDraft = draft => sameComposition(draft, record) || (source && sameComposition(draft, source.draft));
    const retireSourceDraft = () => {
      if (key === offlineKey) return;
      try {
        if (matchesDraft(parseDraft(localStorage.getItem(key)))) localStorage.removeItem(key);
      } catch {}
      if (!source) return;
      if (matchesDraft(inMemory.get(key))) inMemory.delete(key);
      if (window.OieHostedSession) {
        try {
          if (matchesDraft(parseDraft(sessionStorage.getItem(key)))) clearSessionDraft(key);
        } catch {}
      }
    };
    try {
      const identity = reservation?.identity ?? await sendIdentity(record);
      const saved = await withOfflineLock(offlineKey, () => {
        if (resettingDevice()) return false;
        if (!source && (life.signal.aborted || stopped || key !== draftStorageKey())) return false;
        const claim = readRecordingUploadClaim();
        if (claim && claim.token !== source?.token) return false;
        const queued = parseDraft(localStorage.getItem(offlineKey));
        const operation = reservation ? sendOperationState(record.key, queued) : selectSendOperation(record, identity, queued);
        if (!operation) return false;
        if (operation.accepted?.identity === identity) {
          if (source) retireSourceDraft();
          return operation.accepted;
        }
        if (hasDraft(queued) && (isOfflineIntent(queued) || key !== offlineKey || !sameComposition(queued, record))) return false;
        if (reservation ? operation.attempt?.token !== reservation.token : operation.attempt) return false;
        pending.key = operation.key;
        // Keep another window's draft when it diverged from this composition.
        localStorage.setItem(offlineKey, JSON.stringify(pending));
        retireSourceDraft();
        return true;
      });
      if (!saved) return false;
      if (saved.accepted) {
        if (!life.signal.aborted) consumeAcceptedDraft(record, saved);
        return true;
      }
    } catch { return false; }
    if (life.signal.aborted) {
      if (source) {
        document.dispatchEvent(new CustomEvent('oie:offline-send-changed', {
          detail: {key:offlineKey, operation:pending.key},
        }));
        document.dispatchEvent(new Event('oie:resume-offline-sends'));
      }
      return true;
    }
    clearSessionDraft(key);
    if (inMemory.get(key)?.key === record.key) inMemory.delete(key);
    selectRecoveredDraft('');
    if (pendingBubble?.dataset.operationKey === record.key) pendingBubble.dataset.operationKey = pending.key;
    journal = pending;
    submissionKey.value = pending.key;
    input.value = '';
    restoreMentions(pending.mentions);
    lockComposer(true);
    renderReply();
    renderPendingSend('waiting');
    return true;
  }
  function clearSessionDraft(key = draftStorageKey()) {
    if (window.OieHostedSession) {
      try { sessionStorage.removeItem(key); } catch {}
    }
  }
  function clearDraft(key, composition = journal) {
    if (!draftKey) return;
    const storageKey = draftStorageKey();
    const stored = readStoredDraft(storageKey);
    if (!stored || stored.key === key && composition && sameComposition(stored, composition)) {
      inMemory.delete(storageKey);
      try { localStorage.removeItem(storageKey); } catch { storageWarning(); }
      clearSessionDraft(storageKey);
    }
    if (composition && sameComposition(inMemory.get(storageKey), composition)) inMemory.delete(storageKey);
    if (storageKey === selectedRecoveredDraft) selectRecoveredDraft('');
    journal = null;
    delete composer.dataset.privateMapLinks;
  }
  function consumeAcceptedDraft(record, receipt) {
    if (journal?.pending && journal.key === record.key && completeSubmission(receipt)) {
      scheduleRefresh();
      return;
    }
    clearDraft(record.key, record);
    input.value = '';
    restoreMentions([]);
    submissionKey.value = receipt.nextKey;
    lockComposer(false);
    renderReply();
    scheduleRefresh();
  }
  let pendingBubble;
  const acceptedBubbles=new Set();
  function retireAcceptedBubbles(){
    const timeline=document.querySelector('#message-timeline');
    for(const node of acceptedBubbles)if(timeline?.querySelector(`[data-message-id="${CSS.escape(node.dataset.acceptedMessage)}"]`)){
      if(node.contains(document.activeElement))input?.focus({preventScroll:true});
      window.Alpine?.destroyTree(node);node.remove();acceptedBubbles.delete(node);
    }
  }
  function renderPendingAttachments(bubble, files) {
    const list = bubble.querySelector('[data-pending-send-attachments]');
    const template = bubble.querySelector('[data-pending-send-file]');
    for (const file of files || []) {
      if (!canonicalID(file?.id) || typeof file.fileName !== 'string') continue;
      const query = new URLSearchParams({engineId: body.dataset.engineId, conversationId: body.dataset.conversationId, attachmentId: file.id});
      const previewURL = '/ui/scheduling/file?' + query;
      const link = template.content.firstElementChild.cloneNode(true);
      query.set('download', '1');
      link.href = '/ui/scheduling/file?' + query;
      link.download = file.fileName;
      link.querySelector('span').textContent = file.fileName;
      const thumbnail = link.querySelector('img');
      if (['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'image/avif'].includes(file.mediaType)) {
        thumbnail.src = previewURL;
      } else {
        thumbnail.remove();
      }
      list.append(link);
    }
    list.hidden = !list.childElementCount;
  }

  function renderPendingSend(state='sending'){
    if(!journal?.pending || journal.scheduledAt || editingSchedule)return;
    if (journal.offline && state !== 'accepted') state = offlinePendingState(journal);
    const timeline=document.querySelector('#message-timeline'),template=composer?.querySelector('[data-pending-send-template]');
    if(!timeline||!template)return;
    if(!pendingBubble){
      if(!journal.submittedAt||Number.isNaN(Date.parse(journal.submittedAt)))saveDraft({...journal,submittedAt:new Date().toISOString()});
      pendingBubble=template.content.firstElementChild.cloneNode(true);
      pendingBubble.dataset.operationKey=journal.key;
      pendingBubble.dataset.createdAt=journal.submittedAt;
      const pendingText = pendingBubble.querySelector('[data-pending-send-text]');
      pendingText.textContent = journal.text;
      pendingText.hidden = !journal.text;
      renderPendingAttachments(pendingBubble, journal.attachments);
      const quote=pendingBubble.querySelector('[data-pending-send-reply]');quote.hidden=!journal.reply;
      quote.querySelector('h3').textContent=journal.reply?.senderName||'';
      pendingBubble.querySelector('[data-pending-send-excerpt]').textContent=journal.reply?.excerpt||'';
      pendingBubble.querySelector('[data-pending-send-time]').dateTime=journal.submittedAt;
      timeline.insertBefore(pendingBubble,timeline.querySelector('[data-timeline-edge="newer"]'));window.Alpine?.initTree(pendingBubble);
      localizeTimes();
      // Submitting is an explicit intent to view this outgoing message.
      scrollLatest();
    }
    const focused = document.activeElement;
    pendingBubble.dataset.pendingState=state;
    // Keep an unresolved send readable while the retry action is available.
    pendingBubble.querySelector('[data-pending-send-content]').style.opacity=state==='uncertain'?'1':'';
    const actions = pendingBubble.querySelector('[data-offline-send-actions]');
    const cancelMenu = pendingBubble.querySelector('#cancel-offline-send');
    const retryMenu = pendingBubble.querySelector('#retry-pending-send');
    actions.hidden = !journal.offline;
    const retryActions = pendingBubble.querySelectorAll('[data-pending-send-retry],#retry-pending-send');
    const disabled=state!=='uncertain'||stopped||composer.dataset.busy==='true';
    if ((state !== 'waiting' && (actions.contains(focused) || cancelMenu === focused)) ||
        (disabled && [...retryActions].includes(focused))) {
      focused.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape', bubbles:true}));
      pendingBubble.focus({preventScroll:true});
    }
    for(const node of pendingBubble.querySelectorAll('[data-pending-state]'))node.hidden=node.dataset.pendingState!==state;
    for(const action of retryActions){action.disabled=disabled;action.setAttribute('aria-disabled',String(disabled));}
    cancelMenu.hidden = cancelMenu.disabled = state !== 'waiting';
    actions.querySelector('button').disabled = state !== 'waiting';
    retryMenu.hidden = state === 'waiting';
  }
  function acceptPendingSend(id){
    if(!pendingBubble)return;
    const hadFocus = pendingBubble.contains(document.activeElement);
    renderPendingSend('accepted');
    // Accepted placeholders have no retry action. The engine now owns delivery.
    if(hadFocus)pendingBubble.focus({preventScroll:true});
    const menu=pendingBubble.querySelector('.oie-message-menu-slot');window.Alpine?.destroyTree(menu);menu.remove();
    pendingBubble.dataset.acceptedMessage=id;acceptedBubbles.add(pendingBubble);pendingBubble=null;retireAcceptedBubbles();
  }
  life.cleanup(()=>{for(const node of [...acceptedBubbles,pendingBubble])if(node){window.Alpine?.destroyTree(node);node.remove();}});
  life.on(document,'oie:timeline-content',retireAcceptedBubbles);
  life.on(document,'oie:cancel-message',event=>{
    const id=event.detail?.id;if(!canonicalID(id))return;
    const form=document.getElementById('cancel-'+id);
    if(form?.matches('[data-cancel-message]')&&form.dataset.busy!=='true'){
      event.target.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
      life.raf(()=>form.closest('[data-message-id]')?.focus({preventScroll:true}));
      form.requestSubmit();
    }
  });
  life.on(document,'click',event=>{
    const cancel = event.target.closest('[data-cancel-offline-send],#cancel-offline-send');
    if (cancel && pendingBubble?.contains(cancel) && !cancel.disabled && journal?.offline === 'waiting') {
      event.preventDefault();
      void cancelOfflineSend();
      return;
    }
    const action=event.target.closest('[data-pending-send-retry],#retry-pending-send');
    if(!action||!pendingBubble?.contains(action)||action.disabled||!journal?.pending)return;
    event.preventDefault();
    if (journal.offline) retryOfflineSend();
    else composer.requestSubmit();
  });
  function feedback(form, message, error = false) {
    if(form===composer&&(journal?.scheduledAt||editingSchedule)){
      if(error)errorToast(message);
      return;
    }
    if (['account-avatar','account-label'].includes(form.dataset.oieAction)) {
      const editor = form.closest('dialog');
      const region = form.querySelector('[data-form-feedback]');
      if (editor?.open && region) {
        region.dataset.error = String(error);
        region.textContent = message;
      } else if (message && form.isConnected) {
        if (error) errorToast(message); else announce(message, true);
      }
      return;
    }
    if(error&&form.matches('[data-cancel-message]')){errorToast(message);return;}
    if(form.dataset.oieAction==='schedule-row'){if(error)errorToast(message);return;}
    const region = form.querySelector('[data-form-feedback]');
    if(form===composer&&journal?.pending&&!journal.scheduledAt&&!editingSchedule){if(region)region.textContent='';renderPendingSend(journal.offline==='waiting'?'waiting':error?'uncertain':'sending');announce(message,true);return;}
    if (region) { region.textContent = message; region.dataset.error = String(error);region.setAttribute('role',error?'alert':'status'); if(error&&form.dataset.oieAction!=='send'){region.tabIndex=-1;region.focus({preventScroll:true});} }
    if (error && !region) errorToast(message);
    else announce(message);
  }
  life.on(document, 'change', async event => {
    const input = event.target;
    if (input.id !== 'account-photo-image') return;
    const preview = input.form.querySelector('[data-account-photo-preview]');
    preview.hidden = true;
    preview.removeAttribute('src');
    const file = input.files[0];
    if (!file?.size || file.size > Number(preview.dataset.maxBytes) ||
        (file.type && !input.accept.split(',').includes(file.type))) return;

    const source = URL.createObjectURL(file);
    preview.src = source;
    try {
      await preview.decode();
      if (preview.getAttribute('src') === source) preview.hidden = false;
    } catch {
      // Leave the current photo visible when the selected file cannot be decoded.
    } finally {
      URL.revokeObjectURL(source);
    }
  });
  life.on(document, 'close', event => {
    if (!(event.target instanceof HTMLDialogElement)) return;
    const form = event.target.querySelector('form[data-oie-action="account-label"],form[data-oie-action="account-avatar"]');
    if (!form) return;
    form.reset();
    form.querySelector('input[type="file"]')?.dispatchEvent(new Event('change', {bubbles:true}));
    const feedback = form.querySelector('[data-form-feedback]');
    if (feedback) feedback.textContent = '';
    scheduleRefresh();
  }, {capture:true});
  function lockComposer(locked) {
    if (!input || !composer) return;
    input.readOnly = locked;
    input.setAttribute('aria-readonly', String(locked));
    const button = composerSend;
    if (button) {
      button.setAttribute('aria-label', text('send'));
      button.title = locked ? text('locked') : text('send');
    }
  }
  function restoreDraft(nextKey = submissionKey?.value, initialText = '', chosenDraft) {
    journal = null;
    delete composer.dataset.privateMapLinks;
    input.value = initialText;
    submissionKey.value = nextKey;
    let saved = chosenDraft ?? readDraft();
    if(!saved&&scheduledMessage){
      const m=scheduledMessage, composition=m.composition;
      saved={key:submissionKey.value,text:composition.originalText,mentions:composition.mentions,attachments:m.attachments||[],
        reply:composition.replyToMessageId?{messageId:composition.replyToMessageId,engineId:body.dataset.engineId,accountId:m.accountId,conversationId:m.conversationId,
          senderName:m.reply?.senderName||'',excerpt:m.reply?.excerpt||''}:null};
    }
    restoreMentions([]);
    lockComposer(false);
    if (saved && saved.conversationKind && saved.conversationKind !== conversationKind) saved = null;
    if (saved && typeof saved.text === 'string' && (canonicalID(saved.key)||(saved.key===''&&!saved.pending)) && (!saved.reply || (canonicalID(saved.reply.messageId) && saved.reply.engineId===body.dataset.engineId && saved.reply.accountId===body.dataset.accountId && saved.reply.conversationId===body.dataset.conversationId && typeof saved.reply.senderName==='string' && typeof saved.reply.excerpt==='string' && Array.from(saved.reply.excerpt).length<=512))) {
      composer.dataset.privateMapLinks = JSON.stringify(Array.isArray(saved.privateMapLinks) ? saved.privateMapLinks : []);
      journal = {...saved,key:canonicalID(saved.key)?saved.key:submissionKey.value}; input.value = saved.pending&&!saved.scheduledAt&&!editingSchedule?'':saved.text; submissionKey.value = journal.key; restoreMentions(saved.mentions);
      lockComposer(saved.pending === true);
      if (saved.pending) feedback(composer, text('pending'), true);
      if (saved.offlineReturned) {
        delete journal.offlineReturned;
        saveDraft(journal);
        life.timeout(() => errorToast(offlineCopy.returned), 0);
      }
    }
  }
  if (input && submissionKey && draftKey && !mock) {
    restoreDraft(submissionKey.value,input.value);
    life.on(input,'input', () => {
      if(editSession){if(!editSession.submitted){keepEdit({...editSession,text:input.value});renderEdit();}return;}
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
      if (window.OieHostedSession?.leaving) return;
      if ((journal?.pending && !journal.offline) || editSession?.submitted || (storageDenied && input.value)) { event.preventDefault(); event.returnValue = ''; }
    });
  }


  if(lastEditKey&&!mock){let saved;try{saved=JSON.parse(localStorage.getItem(lastEditKey)||'null')||inMemory.get(lastEditKey)}catch{saved=inMemory.get(lastEditKey)}if(validEditSession(saved)&&matchingEditCommand(saved.command,saved)){keepLastEdit(saved);life.timeout(()=>checkCompletedEdit(),0);}}
  if(editKey&&!mock&&!editingSchedule){let saved;try{saved=JSON.parse(localStorage.getItem(editKey)||'null')||inMemory.get(editKey)}catch{saved=inMemory.get(editKey)}if(validEditSession(saved)&&!journal?.pending){editSession=saved;input.value=saved.text;restoreMentions([]);if(saved.submitted)life.timeout(()=>submitEdit(true),0);}}
  renderReply();renderEdit();
  if(editSession&&!editMessageBody(editSession.messageId)){const sourceID=editSession.messageId;life.timeout(async()=>{if(editSession?.messageId!==sourceID)return;await window.OieTimeline?.focus(sourceID,{focus:false});if(!life.signal.aborted)renderEditMarkers();},0);}
  let composerActionsReadAt=0;
  function applyComposerActions(state,startedAt){
    if(!composer||startedAt<composerActionsReadAt)return;
    composerActionsReadAt=startedAt;
    composer.dataset.replyAllowed=state.replyAllowed;
    composer.dataset.editAllowed=state.editAllowed;
    if(state.editMessage!==undefined)composer.dataset.latestEditMessage=state.editMessage;
  }
  life.on(document,'oie:actions-refreshed',event=>{
    const state=event.detail?.menuState;
    if(state)applyComposerActions(state,Number(state.startedAt));
    renderReply();renderEdit();
  });
  const validExcerptDigest = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
  class ReplySourceUnavailable extends Error {}
  async function fetchReplySource(messageId, requireReview = false) {
    const controller = new AbortController(), timeout = life.timeout(() => controller.abort(), 12000);
    try {
      const query = new URLSearchParams({engineId:body.dataset.engineId, conversationId:body.dataset.conversationId, accountId:body.dataset.accountId, messageId});
      let response;
      try {
        response = await life.fetch('/ui/reply-source?' + query, {credentials:'same-origin', cache:'no-store', signal:controller.signal});
      } catch (error) {
        if (controller.signal.aborted || error instanceof TypeError) throw new ReplySourceUnavailable();
        throw error;
      }
      const reply = await response.json().catch(error => {
        if (response.ok && error instanceof TypeError) throw new ReplySourceUnavailable();
        return null;
      });
      const unavailable = [502, 503, 504].includes(response.status) && (!reply || reply.code === 'engine_unavailable');
      if (controller.signal.aborted || unavailable) throw new ReplySourceUnavailable();
      if (!response.ok || !reply || reply.engineId !== body.dataset.engineId || reply.accountId !== body.dataset.accountId ||
          reply.conversationId !== body.dataset.conversationId || reply.requestedMessageId !== messageId || !canonicalID(reply.messageId) ||
          typeof reply.senderName !== 'string' || typeof reply.excerpt !== 'string' || Array.from(reply.excerpt).length > 512 ||
          (requireReview && (!canonicalID(reply.revisionId) || !validExcerptDigest(reply.excerptSha256)))) throw Error('reply_unavailable');
      return reply;
    } finally {
      clearTimeout(timeout);
    }
  }
  function finishReplyPreparation(epoch) {
    if (life.signal.aborted || epoch !== replyEpoch) return;
    preparingReply = false;
    renderReply();
  }
  async function refreshRejectedQuote() {
    const messageId = journal?.reply?.messageId;
    if (!messageId) return;
    const epoch = ++replyEpoch;
    preparingReply = true;
    renderReply();
    try {
      const reply = await fetchReplySource(messageId, true);
      if (!life.signal.aborted && epoch === replyEpoch) saveDraft({...draftRecord(), reply});
    } catch {
      // Keep the draft available if the quote cannot be refreshed yet.
    } finally {
      finishReplyPreparation(epoch);
    }
  }
  life.on(document,'click',async event=>{
    const cancel=event.target.closest('[data-cancel-reply]');
    if(cancel){event.preventDefault();if(editSession||editPreparing)return;if(journal?.pending){feedback(composer,text('replyLocked'),true);return}replyEpoch++;preparingReply=false;saveDraft({...draftRecord(false),reply:null});renderReply();input?.focus({preventScroll:true});return;}
    const action=event.target.closest('[id^="reply-message-"]');if(!action||action.disabled||!composer)return;
    event.preventDefault();if(editSession||editPreparing)return;if(journal?.pending){feedback(composer,text('replyLocked'),true);return}if(composer.dataset.replyAllowed!=='true')return;
    const messageId=action.id.slice(14);if(!canonicalID(messageId))return;
    const epoch=++replyEpoch;preparingReply=true;renderReply();feedback(composer,text('replyPreparing'));
    try{
      const reply = await fetchReplySource(messageId);
      if(life.signal.aborted||epoch!==replyEpoch)return;
      saveDraft({...draftRecord(false),reply});feedback(composer,'');input?.focus({preventScroll:true});
    }catch{if(!life.signal.aborted&&epoch===replyEpoch)feedback(composer,text('replyUnavailable'),true)}
    finally{finishReplyPreparation(epoch)}
  });

  const roomDraftKey=ordinaryDraftKey?ordinaryDraftKey.replace('oie.draft.v1','oie.room-operation.v1'):'';
  let roomOperation=null;
  if(roomDraftKey){try{roomOperation=JSON.parse(localStorage.getItem(roomDraftKey)||'null')||inMemory.get(roomDraftKey)||null}catch{roomOperation=inMemory.get(roomDraftKey)||null}}
  if(roomOperation&&(!canonicalID(roomOperation.operationKey)||!['join','leave'].includes(roomOperation.command)||roomOperation.conversationId!==body.dataset.conversationId||roomOperation.accountId!==body.dataset.accountId))roomOperation=null;
  let roomCommandPassword = "";
  let roomPasswordRequested = false;
  let roomCommandGeneration = 0;
  function keepRoomOperation(value){if(!value){roomCommandPassword="";roomPasswordRequested=false;}if(!value&&roomOperation){const region=document.querySelector('#room-session-status');if(region)region.dataset.version='';}roomOperation=value;if(!roomDraftKey)return;if(value)inMemory.set(roomDraftKey,value);else inMemory.delete(roomDraftKey);try{if(value)localStorage.setItem(roomDraftKey,JSON.stringify(value));else localStorage.removeItem(roomDraftKey)}catch{storageWarning()}}
  function restoreRoomOperation(){
    if(!roomOperation)return;
    const form=document.querySelector('[data-room-command]');if(!form)return;
    form.hidden=false;
    form.action='/ui/rooms/'+roomOperation.command;form.dataset.oieAction=roomOperation.command+'_room';form.dataset.roomCommand=roomOperation.command;
    for(const key of ['operationKey','expectedRevisionId','expectedConversationRevisionId','nickname']){const field=form.elements.namedItem(key);if(field)field.value=roomOperation[key]||'';}
    for(const field of form.querySelectorAll('input:not([type=hidden])'))field.readOnly=true;
    const passwordField=form.querySelector('[data-room-password-field]');
    if (roomPasswordRequested && passwordField) {
      passwordField.hidden=false;
      passwordField.querySelector('label').textContent=passwordField.dataset.originalPasswordLabel;
      const input=passwordField.querySelector('input');
      input.disabled=false;
      input.readOnly=false;
      input.required=roomOperation.passwordRequired===true;
    }
    const button=form.querySelector('[data-room-action]');if(button){button.dataset.roomAction=roomOperation.command;button.textContent=text('retry');button.title=text('retry');}
    const region=form.querySelector('[data-form-feedback]');if(region){region.textContent=roomPasswordRequested&&passwordField ? passwordField.dataset.passwordRetry : text('roomCommandUnknown');region.dataset.error='true';region.setAttribute('role','alert');}
  }
  const roomEntryPasswords = new WeakMap();
  const roomEntryFields = ['operationKey','accountId','address','name','nickname'];
  function roomEntryStorage(form, account, address) {
    const scope = ['oie.room-entry.v1', body.dataset.engineId, form.dataset.roomEntry].join(':');
    return account && address ? scope + ':' + JSON.stringify([account,address]) : scope;
  }
  function pendingRoomEntry(form, account, address) {
    const scope = roomEntryStorage(form, account, address);
    let saved = inMemory.get(scope);
    if (!saved) {
      try { saved = JSON.parse(sessionStorage.getItem(scope)); } catch {}
    }
    return saved && canonicalID(saved.operationKey) && canonicalID(saved.accountId)
      && typeof saved.address === 'string' && saved.address.length <= 2047 ? saved : null;
  }
  function restoreRoomEntry(form, saved) {
    form.dataset.roomEntryPending = String(!!saved);
    for (const name of roomEntryFields) {
      const field = form.elements.namedItem(name);
      if (saved && field) {
        field.value = saved[name] || '';
        if (name === 'accountId') field.dispatchEvent(new Event('input', {bubbles:true}));
      }
      if (name !== 'operationKey' && field) field.readOnly = !!saved;
    }
    const account = form.querySelector('[role=combobox]');
    if (account) account.disabled = !!saved;
    const submit = form.querySelector('[type=submit]');
    if (submit) submit.textContent = saved ? text('retry') : submit.dataset.roomEntryLabel;
  }
  function saveRoomEntry(form, saved) {
    if (!saved) roomEntryPasswords.delete(form);
    const prior = pendingRoomEntry(form);
    const command = saved || prior;
    const scopes = [roomEntryStorage(form)];
    if (command) scopes.push(roomEntryStorage(form, command.accountId, command.address));
    for (const scope of scopes) {
      if (saved) inMemory.set(scope, saved); else inMemory.delete(scope);
      try {
        if (saved) sessionStorage.setItem(scope, JSON.stringify(saved));
        else sessionStorage.removeItem(scope);
      } catch {}
    }
    restoreRoomEntry(form, saved);
  }
  function localOperationKey() {
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    let timestamp = Date.now();
    for (let i = 5; i >= 0; i--) {
      bytes[i] = timestamp % 256;
      timestamp = Math.floor(timestamp / 256);
    }
    bytes[6] = (bytes[6] & 15) | 112;
    bytes[8] = (bytes[8] & 63) | 128;
    const hex = Array.from(bytes, value => value.toString(16).padStart(2,'0')).join('');
    return [hex.slice(0,8),hex.slice(8,12),hex.slice(12,16),hex.slice(16,20),hex.slice(20)].join('-');
  }
  life.on(document, 'oie:room-entry-target', event => {
    const {form, account, address} = event.detail;
    if (form.dataset.busy === 'true') return;
    const scope = roomEntryStorage(form);
    const saved = pendingRoomEntry(form, account, address);
    if (saved) {
      saveRoomEntry(form, saved);
      feedback(form, text('roomCommandUnknown'), true);
    } else {
      inMemory.delete(scope);
      try { sessionStorage.removeItem(scope); } catch {}
      restoreRoomEntry(form, null);
      form.elements.namedItem('operationKey').value = localOperationKey();
      feedback(form, '');
    }
  });
  for (const form of document.querySelectorAll('form[data-room-entry]')) {
    const saved = pendingRoomEntry(form);
    if (saved) {
      restoreRoomEntry(form, saved);
      const status = form.querySelector('[data-form-feedback]');
      if (status) status.textContent = text('roomCommandUnknown');
    }
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
  function openRoomSession() {
    window.dispatchEvent(new CustomEvent('modal:open', {detail: {id: 'room-session-dialog'}}));
  }
  life.on(window, 'oie:join-saved-room', () => {
    if (mock || stopped || !document.querySelector('[data-room-command=join]')) return;
    const trigger = document.querySelector('#chat-options [aria-controls="chat-options-panel"]');
    const menu = document.getElementById('chat-options-panel');
    if (trigger?.getAttribute('aria-expanded') === 'true') trigger.click();
    const openAfterMenu = () => {
      if (menu?.isConnected && menu.getClientRects().length) {
        life.raf(openAfterMenu);
        return;
      }
      trigger?.focus({preventScroll: true});
      openRoomSession();
    };
    life.raf(openAfterMenu);
  });
  function afterConversationMenu(target, open) {
    const menu = target.closest('#chat-options-panel');
    if (!menu) {
      target.focus({preventScroll: true});
      open();
      return;
    }
    const trigger = document.querySelector('#chat-options [aria-controls="chat-options-panel"]');
    if (trigger?.getAttribute('aria-expanded') !== 'true') return;

    trigger.click();
    // The drawer's focus trap must start after the dropdown leaves the top layer.
    const openAfterMenu = () => {
      if (!menu.isConnected || trigger.getAttribute('aria-expanded') === 'true') return;
      if (menu.getClientRects().length) {
        life.raf(openAfterMenu);
        return;
      }
      trigger.focus({preventScroll: true});
      open();
    };
    life.raf(openAfterMenu);
  }
  life.on(document, 'oie:conversation-drawer-open', ({target, detail: id}) => {
    afterConversationMenu(target, () => window.dispatchEvent(new CustomEvent('drawer:open', {detail: {id}})));
  });
  let removalSnapshot;
  function rememberConversationForRemoval(conversation) {
    if (conversation && conversation.id === body.dataset.conversationId && conversation.accountId === body.dataset.accountId &&
        canonicalID(conversation.revisionId) && typeof conversation.title === 'string') {
      removalSnapshot = conversation;
    }
  }
  life.on(document, 'oie:conversation-remove-open', ({target}) => {
    afterConversationMenu(target, () => {
      const dialog = document.getElementById('conversation-removal');
      const form = dialog?.querySelector('form');
      if (!form || dialog.open || form.dataset.busy === 'true') return;
      if (form.dataset.removalRetry !== 'true') {
        if (removalSnapshot && !removalSnapshot.removedFromChats &&
            removalSnapshot.id === form.elements.conversationId.value &&
            removalSnapshot.accountId === form.elements.accountId.value) {
          form.elements.expectedRevisionId.value = removalSnapshot.revisionId;
          form.querySelector('[data-removal-title]').textContent = removalSnapshot.title;
        }
        feedback(form, '');
      }
      window.dispatchEvent(new CustomEvent('modal:open', {detail: {id: dialog.id}}));
    });
  });
  restoreRoomOperation();syncRoomLeave();
  if (roomOperation && document.getElementById('room-session-dialog')) {
    if (window.Alpine) life.timeout(openRoomSession, 0);
    else life.on(document, 'alpine:initialized', openRoomSession, {once: true});
  }
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
      if (next && (status.dataset.connectionState !== next.dataset.connectionState ||
          status.dataset.accountAvatarName !== next.dataset.accountAvatarName ||
          status.dataset.accountAvatarBorder !== next.dataset.accountAvatarBorder)) {
        status.dataset.accountAvatarBorder = next.dataset.accountAvatarBorder;
        status.dataset.accountAvatarName = next.dataset.accountAvatarName;
        status.dataset.connectionState = next.dataset.connectionState;
        status.title = next.title;
        status.setAttribute('aria-label',next.getAttribute('aria-label'));
        mutateDOM(status,next.innerHTML);
      }
    }
  }
  function refreshAccountNames(root, nextRoot) {
    for (const element of root.querySelectorAll('[data-account-name]')) {
      const next = nextRoot.querySelector(`[data-account-name="${CSS.escape(element.dataset.accountName)}"]`);
      if (next && element.textContent !== next.textContent) {
        element.textContent = next.textContent;
        if (next.hasAttribute('title')) element.title = next.title;
      }
    }
  }
  function syncAccountNameInput(form, label, revision, preserveDraft) {
    const field = form.elements.namedItem('label');
    const draft = field.value;
    field.defaultValue = label;
    field.value = preserveDraft ? draft : label;
    form.elements.namedItem('expectedRevisionId').value = revision;
  }
  function localizeTimes(root=document) {
    window.OieTime.localize(root);
    const timeline=root.querySelector('#message-timeline');if(!timeline)return;
    const obsolete=new Set(timeline.querySelectorAll(':scope > .oie-day-divider'));
    const key=date=>window.OieTime.dateKey(date,'day');
    const today=key(new Date());let previous='';
    for(const row of timeline.querySelectorAll(':scope>[data-message-id],:scope>[data-pending-send]')){
      const date=new Date(row.dataset.createdAt);if(Number.isNaN(date.getTime()))continue;
      const current=key(date);if(current===previous)continue;previous=current;
      let divider=row.previousElementSibling;
      const mounted=obsolete.delete(divider);
      if(!mounted){
        divider=document.createElement('div');
        divider.className='oie-day-divider';
      }
      let time=divider.querySelector('time');
      if(!time){
        time=document.createElement('time');
        time.dataset.day='true';
        divider.replaceChildren(time);
      }
      if(time.dateTime!==row.dataset.createdAt)time.dateTime=row.dataset.createdAt;
      const label=current===today?text('today'):window.OieTime.format(date,{dateStyle:'medium'});
      if(time.textContent!==label)time.textContent=label;
      if(!mounted)row.before(divider);
    }
    for(const divider of obsolete)divider.remove();
  }
  life.on(document,'oie:time-zone',()=>{
    const timeline=document.querySelector('#message-timeline');
    const follow=window.OieTimeline?.atBottom()&&window.OieTimeline.liveAttached;
    const viewport=timeline?.getBoundingClientRect();
    const row=viewport&&[...timeline.querySelectorAll(':scope>[data-message-id],:scope>[data-pending-send]')]
      .find(node=>node.getBoundingClientRect().bottom>viewport.top);
    const top=row?.getBoundingClientRect().top;
    localizeTimes();
    if(follow)timeline.scrollTop=timeline.scrollHeight;
    else if(row)timeline.scrollTop+=row.getBoundingClientRect().top-top;
  },{capture:true});
  let highlightedMessage='',highlightTimer;
  function scrollLatest() {
    if(window.OieTimeline)window.OieTimeline.scrollLatest();
    else{const timeline=document.querySelector('#message-timeline');if(timeline)timeline.scrollTop=timeline.scrollHeight;}
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
    const jump=event.target.closest('[data-jump-latest]');
    if(jump){
      const url=new URL(location.href);
      if(window.OieTimeline?.needsLatestPage()){
        for(const key of ['message','timelineCursor','timelineDirection'])url.searchParams.delete(key);
        window.OieNavigation.go(url,{latest:true});
      }else{
        const restoreFocus=jump===document.activeElement;
        scrollLatest();
        if(restoreFocus&&jump.hidden)document.getElementById('message-timeline')?.focus({preventScroll:true});
      }
    }
  });
  localizeTimes();
  const timelineOnOpen=document.getElementById('message-timeline');
  if(timelineOnOpen){
    const firstUnread=body.dataset.firstUnread&&timelineOnOpen.querySelector(`[data-message-id="${CSS.escape(body.dataset.firstUnread)}"]`);
    if(route?.timelineTop!==undefined)timelineOnOpen.scrollTop=route.timelineTop;
    else if(firstUnread)firstUnread.scrollIntoView({block:'start',behavior:'instant'});
    else if(!body.dataset.focusMessage||!focusMessage(body.dataset.focusMessage))scrollLatest();
    window.OieTimeline?.syncJump();
  }

  life.on(scheduleRetry,'click',()=>composer.requestSubmit());
  function applyScheduleEditor(editor, nextKey = editor.dataset.operationKey) {
    const wasEditing = editingSchedule;
    const nextSchedule = readScheduledMessage();
    if (!wasEditing && !nextSchedule) return;
    composer.dispatchEvent(new Event('oie:attachment-draft-changing'));
    if (!wasEditing) saveDraft(draftRecord(false));
    scheduledMessage = nextSchedule;
    editingSchedule = !!scheduledMessage;
    draftKey = editingSchedule ? scheduleDraftKey() : ordinaryDraftKey;
    composer.toggleAttribute('data-editing-schedule', editingSchedule);
    composer.action = editingSchedule ? '/ui/scheduling/update' : '/ui/messages/send';
    composer.elements.namedItem('returnTo').value = editor.dataset.returnTo;
    composer.elements.namedItem('scheduledAt').value = '';
    restoreDraft(nextKey);
    renderReply();
    renderEdit();
    const drawer = editingSchedule && document.querySelector('[data-conversation-schedules]')?.closest('[role="dialog"]');
    // The drawer's focus trap restores its opener as it closes.
    const focusComposer = () => {
      if (!editor.isConnected || scheduledMessage !== nextSchedule) return;
      if (drawer?.getClientRects().length) life.raf(focusComposer);
      else input.focus({preventScroll:true});
    };
    focusComposer();
  }
  function finishScheduleEdit(destination, nextKey) {
    const editor = document.getElementById('schedule-editor');
    const url = new URL(destination, location.href);
    editor.replaceChildren();
    url.searchParams.set('theme',document.documentElement.dataset.mode);
    editor.dataset.returnTo = url.pathname + url.search;
    window.OieNavigation.addressSearch(url);
    applyScheduleEditor(editor, nextKey);
  }
  life.on(document,'oie:schedule-editor',event=>applyScheduleEditor(event.detail.root));
  life.on(composer,'click',event=>{
    const cancel = event.target.closest('[data-cancel-schedule-edit]');
    if (!cancel || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    event.stopPropagation();
    if (journal?.pending) return;
    clearDraft(journal?.key);
    finishScheduleEdit(cancel.href, submissionKey.value);
  },true);
  life.on(input,'keydown',event=>{
    if(editingSchedule&&event.key==='Escape'&&!event.defaultPrevented&&!editMenuOpen()){event.preventDefault();composer.querySelector('[data-cancel-schedule-edit]').click();}
  });
  life.on(composer,'oie:attachments-change',event=>{
    if(journal?.pending||editSession||editPreparing||stopped||composer.dataset.busy==='true')return;
    const detail = event.detail;
    const previous = draftRecord(false);
    const next = {...previous, attachments:detail.files};
    if (detail.queuedRecording) saveQueuedRecordingDraft(next, previous);
    else saveDraft(next);
    detail.applied = true;
    renderReply();
  });
  life.on(composer,'oie:attachment-staging',renderReply);
  life.on(composer,'oie:schedule-snapshot',event=>{
    const draft = draftRecord();
    const locked=compositionLocked();
    Object.assign(event.detail,{text:draft.text,reply:draft.reply,files:draft.attachments,review:scheduleReview(draft),
      locked,editingSent:!!editSession,
      editBlocked:locked||!composer.hasAttribute('data-schedule-composer')||composer.hasAttribute('data-attachments-pending'),
      blocked:scheduleBlocked(locked)});
  });
  function completeSubmission(result){
    if(!result.accepted||!canonicalID(result.messageId)||!canonicalID(result.nextKey))return false;
    const key=journal?.key;if(!journal?.pending)return false;
    const scheduled=journal.scheduledAt||editingSchedule;
    if(result.state==='expired'&&pendingBubble){
      window.Alpine?.destroyTree(pendingBubble);pendingBubble.remove();pendingBubble=null;
    }else if(!scheduled&&result.state!=='expired')acceptPendingSend(result.messageId);
    if (journal.offline) {
      restoreDraft(result.nextKey);
    } else {
      clearDraft(key);
      input.value='';
      restoreMentions([]);
      submissionKey.value=result.nextKey;
      lockComposer(false);
    }
    renderReply();
    if(scheduled&&result.message)window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',message:result.message}}));
    if(editingSchedule&&result.redirect)finishScheduleEdit(result.redirect,result.nextKey);
    return true;
  }
  async function reconcilePending(signal){
    if(editingSchedule||!journal?.pending||journal.offline||composer.dataset.busy==='true')return;
    const key=journal.key,values=new URLSearchParams(new FormData(composer));
    values.set('operationKey',key);values.set('originalText',journal.text);values.set('replyToMessageId',journal.reply?.messageId||'');values.set('mentions',JSON.stringify(journal.mentions||[]));setScheduledValues(values);
    try{
      const response=await life.fetch('/ui/messages/reconcile',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal});
      if (response.ok) {
        const result = await response.json();
        if (life.signal.aborted || journal?.key !== key || composer.dataset.busy === 'true' || !result.ok) return;
        const record = journal;
        if (completeSubmission(result)) {
          void sendIdentity(record).then(identity =>
            withOfflineLock(offlineKey, () => rememberSendAcceptance(key, result, identity))).catch(() => {});
        }
      }
    }catch{/* Keep the frozen submission and explicit retry on missing evidence. */}
  }
  let resumingOfflineSends = false;
  let cancellingOfflineSend = false;
  function withOfflineLock(key, write) {
    if (navigator.locks) return navigator.locks.request(key, write);
    // IndexedDB read-write transactions serialize local transitions on origins
    // without Web Locks. The journal itself stays in the device draft store.
    return new Promise((resolve, reject) => {
      const open = indexedDB.open('oie.offline-sends', 1);
      let blocked = false;
      open.onupgradeneeded = () => open.result.createObjectStore('draft-lock');
      open.onerror = () => reject(open.error);
      open.onblocked = () => {
        blocked = true;
        reject(new Error('offline storage unavailable'));
      };
      open.onsuccess = () => {
        const db = open.result;
        try {
          if (blocked) return;
          const tx = db.transaction('draft-lock', 'readwrite');
          const permit = tx.objectStore('draft-lock').get(key);
          permit.onsuccess = () => {
            try { resolve(write()); } catch (error) { reject(error); }
          };
          tx.onabort = () => reject(tx.error);
        } catch (error) { reject(error); }
        finally { db.close(); }
      };
    });
  }
  function offlinePendingState(record) {
    if (record.offline === 'waiting') return 'waiting';
    return record.attemptUntil > Date.now() ? 'sending' : 'uncertain';
  }
  function normalAttemptKey(operation) {
    return offlineKey.replace('oie.offline-send.v1', 'oie.send-attempt.v1') + ':' + operation;
  }
  function splitSendOperation(key, identity) {
    // Keep the UUIDv7 timestamp and variant. Tabs with the same original draft
    // and edited composition must derive the same operation after a split.
    const prefix = key.slice(0, 20);
    const random = key.slice(20).replace('-', '');
    const mask = BigInt(`0x${identity.slice(-15)}`) || 1n;
    const suffix = (BigInt(`0x${random}`) ^ mask).toString(16).padStart(15, '0');
    return `${prefix}${suffix.slice(0, 3)}-${suffix.slice(3)}`;
  }
  function sendOperationState(key, queued) {
    const attemptKey = normalAttemptKey(key);
    return {key, attemptKey, accepted:readSendAcceptance(key),
      attempt:parseDraft(localStorage.getItem(attemptKey)),
      offline:isOfflineIntent(queued) && queued.key === key ? queued : null};
  }
  function selectSendOperation(record, identity, queued) {
    const matches = operation => operation.accepted?.identity === identity ||
      operation.attempt?.identity === identity || sameComposition(operation.offline, {...record, key:operation.key});
    const occupied = operation => operation.accepted || operation.attempt || operation.offline;
    const original = sendOperationState(record.key, queued);
    if (matches(original)) return original;
    const split = sendOperationState(splitSendOperation(record.key, identity), queued);
    if (matches(split)) return split;
    if (!occupied(original)) return original;
    if (!occupied(split)) return split;
    return null;
  }
  async function reserveNormalSend(record) {
    try {
      const identity = await sendIdentity(record);
      return await withOfflineLock(offlineKey, () => {
        if (life.signal.aborted || stopped) return false;
        const queued = parseDraft(localStorage.getItem(offlineKey));
        const operation = selectSendOperation(record, identity, queued);
        if (!operation) return false;
        if (operation.accepted?.identity === identity) return operation.accepted;
        if (operation.offline || operation.attempt?.until > Date.now()) return false;
        const token = localOperationKey();
        localStorage.setItem(operation.attemptKey, JSON.stringify({token, identity, until:Date.now() + 30000}));
        record.key = operation.key;
        return {key:operation.attemptKey, token, identity, operation:operation.key, firstAttempt:!operation.attempt};
      });
    } catch {
      // Sending online still works when the device cannot coordinate retries.
      return null;
    }
  }
  function settleNormalSend(reservation, release, accepted) {
    return withOfflineLock(offlineKey, () => {
      const attempt = parseDraft(localStorage.getItem(reservation.key));
      if (attempt?.token !== reservation.token) return;
      if (accepted) rememberSendAcceptance(reservation.operation, accepted, reservation.identity);
      else if (release) localStorage.removeItem(reservation.key);
      else localStorage.setItem(reservation.key, JSON.stringify({...attempt, until:0}));
    }).catch(() => {});
  }
  function readSendAcceptance(operation) {
    const key = acceptedSendPrefix + operation;
    let receipt = parseDraft(localStorage.getItem(key));
    if (!receipt) {
      try { receipt = parseDraft(sessionStorage.getItem(key)); } catch {}
    }
    return receipt?.accepted && canonicalID(receipt.messageId) && canonicalID(receipt.nextKey) ? receipt : null;
  }
  function rememberSendAcceptance(operation, result, identity, scope = offlineKey) {
    if (!canonicalID(result.messageId) || !canonicalID(result.nextKey)) return;
    try {
      const records = Object.keys(localStorage).filter(key => key.startsWith(acceptedSendPrefix))
        .map(key => ({key, at:parseDraft(localStorage.getItem(key))?.at || 0}))
        .sort((a, b) => b.at - a.at);
      for (const {key} of records.slice(63)) localStorage.removeItem(key);
      localStorage.setItem(acceptedSendPrefix + operation, JSON.stringify({
        accepted:true, messageId:result.messageId, nextKey:result.nextKey, identity, at:Date.now(),
      }));
      localStorage.removeItem(scope.replace('oie.offline-send.v1', 'oie.send-attempt.v1') + ':' + operation);
    } catch {}
  }
  function recoveredKey(key, operation) {
    return key.replace('oie.offline-send.v1', 'oie.recovered-draft.v1') + ':' + operation;
  }
  async function sendStagedRecording(source, metadata, submittedAt) {
    const record = {...source.draft, attachments:[metadata], submittedAt, conversationKind};
    if (await saveOfflineSend(record, undefined, source)) return;
    recoverStagedRecording(source, record);
  }
  function recoverStagedRecording(source, record) {
    if (resettingDevice()) return;
    const original = readStoredDraft(source.key);
    const key = !hasDraft(original) || sameComposition(original, source.draft) || sameComposition(original, record)
      ? source.key : recoveredKey(offlineKey, record.key);
    inMemory.set(key, record);
    try { localStorage.setItem(key, JSON.stringify(record)); } catch {}
    errorToast(offlineCopy.storageFailed);
  }
  async function updateOfflineDraft(key, raw, next, result, dispatched = false) {
    const previous = parseDraft(raw);
    const changed = await withOfflineLock(key, () => {
      if ((!dispatched && (life.signal.aborted || stopped)) || localStorage.getItem(key) !== raw) return false;
      if (next) localStorage.setItem(key, JSON.stringify(next));
      else {
        if (result?.returned) {
          const {offline, attempt, attemptUntil, submittedAt, ...draft} = previous;
          const returned = {
            ...draft, key: localOperationKey(), pending: false, offlineReturned: result.rejected,
            mentions: result.code === 'mention_unavailable' ? [] : draft.mentions,
          };
          const serialized = JSON.stringify(returned);
          try {
            localStorage.setItem(recoveredKey(key, previous.key), serialized);
          } catch {
            // At quota, replacing the intent is smaller than copying it.
            localStorage.setItem(key, serialized);
            return true;
          }
        }
        localStorage.removeItem(key);
        if (result?.accepted) rememberSendAcceptance(previous.key, result, result.identity, key);
      }
      return true;
    });
    if (changed) {
      const latest = parseDraft(localStorage.getItem(key));
      if (!life.signal.aborted) applyOfflineUpdate(key, latest, journal?.key === previous?.key ? result : null);
      else document.dispatchEvent(new CustomEvent('oie:offline-send-changed', {
        detail: {key, operation:previous.key, result},
      }));
    }
    return changed;
  }
  async function cancelOfflineSend() {
    if (cancellingOfflineSend) return;
    cancellingOfflineSend = true;
    try {
      await migrateOfflineDraft(ordinaryDraftKey);
      const raw = localStorage.getItem(offlineKey);
      const previous = parseDraft(raw);
      if (previous?.key === journal?.key && previous?.offline === 'waiting') {
        if (await updateOfflineDraft(offlineKey, raw, null, {returned:true})) return;
      }
    } catch { /* Offer cancellation again if the send is still waiting. */ }
    finally { cancellingOfflineSend = false; }
    if (!life.signal.aborted) errorToast(offlineCopy.cancelFailed);
  }
  function applyOfflineUpdate(key, next, result) {
    if (key !== offlineKey || !composer || editingSchedule) return;
    if (!journal?.offline) {
      if (isOfflineIntent(next) && isVoiceSend(next) &&
          !compositionLocked() && !hasDraft(draftRecord()) &&
          !composer.hasAttribute('data-attachments-pending') && !composer.hasAttribute('data-capturing-audio')) {
        restoreDraft(submissionKey.value, '', next);
      }
      renderReply();
      return;
    }
    if (isOfflineIntent(next) && next.key === journal.key) {
      journal = next;
      renderPendingSend();
      return;
    }
    if (result && completeSubmission(result)) return;
    const hadFocus = pendingBubble?.contains(document.activeElement);
    if (pendingBubble) {
      window.Alpine?.destroyTree(pendingBubble);
      pendingBubble.remove();
      pendingBubble = null;
    }
    const recovered = recoveredKey(key, journal.key);
    if (hasDraft(next) && !isOfflineIntent(next)) selectRecoveredDraft(offlineKey);
    else if (hasDraft(readStoredDraft(recovered))) selectRecoveredDraft(recovered);
    restoreDraft();
    renderReply();
    if (hadFocus) input.focus({preventScroll:true});
    scheduleRefresh();
  }
  life.on(window, 'storage', event => {
    if (event.storageArea !== localStorage) return;
    if (event.key === recordingUploadClaimKey) {
      renderReply();
      return;
    }
    if (event.key?.startsWith(acceptedSendPrefix) && event.newValue) {
      const operation = event.key.slice(acceptedSendPrefix.length);
      const identity = parseDraft(event.newValue)?.identity;
      const matches = draft => draft?.key === operation ||
        (canonicalID(draft?.key) && typeof identity === 'string' && /[0-9a-f]{15}$/.test(identity) &&
          splitSendOperation(draft.key, identity) === operation);
      if (matches(journal) || [...inMemory.values()].some(matches)) {
        // Keep the receipt with the stale tab even after the shared cache turns over.
        try { sessionStorage.setItem(event.key, event.newValue); } catch {}
      }
      return;
    }
    if (!event.key?.startsWith(offlinePrefix)) {
      if (event.key === ordinaryDraftKey || event.key?.startsWith(recoveredDraftPrefix)) renderDraftSwitch();
      return;
    }
    const next = parseDraft(localStorage.getItem(event.key));
    applyOfflineUpdate(event.key, next);
    if (next?.offline === 'waiting') void resumeOfflineSends();
  });
  life.on(document, 'oie:offline-send-changed', ({detail: {key, operation, result}}) => {
    if (!key.startsWith(offlinePrefix)) return;
    applyOfflineUpdate(key, parseDraft(localStorage.getItem(key)), journal?.key === operation ? result : null);
    if (result?.accepted) {
      inbox.invalidate();
      scheduleRefresh();
    }
  });
  async function offlineRequest(action, values, dispatched = false) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 12000);
    const request = dispatched ? window.OieRuntime.request : life.fetch;
    try {
      const response = await request('/ui/messages/' + action, {method: 'POST',
        credentials: 'same-origin', headers: {Accept: 'application/json', 'X-CSRF-Token': csrf},
        body: values, signal: controller.signal});
      const result = await response.json();
      if ((dispatched || !life.signal.aborted) && !controller.signal.aborted) return result;
    } catch {
      // Preserve the intent. A write without a response needs a receipt check.
    } finally { clearTimeout(timeout); }
  }
  async function resumeOfflineSend(key, manual = false) {
    const [accountId, conversationId, extra] = key.slice(offlinePrefix.length).split(':');
    if (extra !== undefined || !canonicalID(accountId) || !canonicalID(conversationId)) return;
    let raw = localStorage.getItem(key);
    let record = parseDraft(raw);
    if (!record?.pending || !['waiting', 'attempted'].includes(record.offline) ||
        !canonicalID(record.key) || typeof record.text !== 'string') return;
    if (key === offlineKey && journal?.key === record.key) renderPendingSend();
    if (!navigator.onLine || record.attemptUntil > Date.now() ||
        (manual && record.offline !== 'attempted')) return;
    const values = new URLSearchParams({
      engineId: offlineEngine, accountId, conversationId, operationKey: record.key,
      originalText: record.text, expectedConversationKind: record.conversationKind || '',
      replyToMessageId: record.reply?.messageId || '', mentions: JSON.stringify(record.mentions || []),
      attachmentIds: JSON.stringify((Array.isArray(record.attachments) ? record.attachments : []).map(file => file?.id)),
      offlineCapable: '1',
    });
    let dispatched = false;
    async function advance(next) {
      if (!await updateOfflineDraft(key, raw, next, undefined, dispatched)) return false;
      raw = JSON.stringify(next);
      record = next;
      return true;
    }
    let result = await offlineRequest('reconcile', values);
    const waiting = record.offline === 'waiting';
    if (waiting && result?.code === 'idempotency_conflict') {
      await updateOfflineDraft(key, raw, null, {...result, rejected:true, returned:true});
      return;
    }
    if (!result?.accepted && !result?.rejected && navigator.onLine &&
        ((waiting && result?.code === 'not_found') || manual)) {
      // Claim every network attempt, including explicit retries in other tabs.
      // An expired claim can be retried with the same idempotency key only.
      if (!await advance({...record, offline:'attempted', attempt:localOperationKey(), attemptUntil:Date.now() + 30000})) return;
      // Once claimed, a recording's send and receipt must survive another route swap.
      dispatched = isVoiceSend(record);
      if (!dispatched && life.signal.aborted) return;
      if (localStorage.getItem(key) !== raw) return;
      result = await offlineRequest('send', values, dispatched);
      const {attempt, attemptUntil, ...settled} = record;
      if (waiting && result?.code === 'engine_unavailable') {
        await advance({...settled, offline:'waiting'});
        return;
      }
      if (!result?.accepted && !result?.rejected) {
        await advance(settled);
        return;
      }
    }
    if (result?.accepted && canonicalID(result.messageId) && canonicalID(result.nextKey)) {
      const identity = await sendIdentity(record);
      if (await updateOfflineDraft(key, raw, null, {...result, identity}, dispatched) && !life.signal.aborted) {
        inbox.invalidate();
        scheduleRefresh();
      }
    } else if (result?.rejected) {
      await updateOfflineDraft(key, raw, null, {...result, returned:true}, dispatched);
    }
  }
  function retryOfflineSend() {
    resumeOfflineSend(offlineKey, true).catch(() => {
      if (!life.signal.aborted) errorToast(text('pending'));
    });
  }
  async function migrateOfflineDraft(oldKey) {
    if (!oldKey) return;
    const raw = localStorage.getItem(oldKey);
    const record = parseDraft(raw);
    if (!record?.pending || !['waiting', 'attempted'].includes(record.offline)) return;
    const key = oldKey.replace('oie.draft.v1', 'oie.offline-send.v1');
    await withOfflineLock(key, () => {
      if (life.signal.aborted || localStorage.getItem(oldKey) !== raw || localStorage.getItem(key)) return;
      localStorage.setItem(key, raw);
      localStorage.removeItem(oldKey);
    });
  }
  let rescanOfflineSends = false;
  function wakeOfflineSends() {
    if (resumingOfflineSends) rescanOfflineSends = true;
    else void resumeOfflineSends();
  }
  life.on(document, 'oie:resume-offline-sends', wakeOfflineSends);
  async function resumeOfflineSends() {
    if (mock || stopped || !canonicalID(offlineEngine) || resumingOfflineSends) return;
    resumingOfflineSends = true;
    try {
      const legacyPrefix = offlinePrefix.replace('oie.offline-send.v1', 'oie.draft.v1');
      for (const key of Object.keys(localStorage).filter(key => key.startsWith(legacyPrefix))) {
        await migrateOfflineDraft(key);
      }
      const keys = Object.keys(localStorage).filter(key => key.startsWith(offlinePrefix));
      for (const key of keys) {
        if (life.signal.aborted || stopped || !navigator.onLine) break;
        await resumeOfflineSend(key);
      }
    } catch { /* The saved operation can be recovered when storage is available again. */ }
    finally {
      resumingOfflineSends = false;
      if (rescanOfflineSends) {
        rescanOfflineSends = false;
        void resumeOfflineSends();
      }
    }
  }
  // A conditional read is valid only for the exact view we have applied.
  // Any missing event, explicit mutation or failed reconciliation takes the full path.
  const rowEvents = new Set(['message.created', 'message.updated', 'conversation.created', 'conversation.updated']);
  function inboxEvent(event, type) {
    if (fullRefresh || !appliedCursor || !rowEvents.has(type) || !events.follows(inboxEventCursor || appliedCursor, event.lastEventId)) return false;
    let value;
    try { value = JSON.parse(event.data); } catch { return false; }
    const id = value?.message?.conversationId || value?.conversation?.id;
    if (!canonicalID(id)) return false;
    if(!inboxEventCursor)pendingAppend=undefined;
    if(id===body.dataset.conversationId){
      const m=value.message;
      if(type==='message.created'&&!inboxEventCursor&&m?.sendState==='received'&&!m.isOwn&&!m.historical&&
         m.accountId===body.dataset.accountId&&canonicalID(m.id)&&typeof m.originalText==='string'&&m.originalText&&!m.reply&&!m.retraction&&!m.moderation&&
         !m.attachments?.length&&!m.audioAttachmentUrls?.length&&!m.videoAttachmentUrls?.length&&
         !m.originalText.includes('://')&&window.OieTimeline?.appendEdge()){
        pendingAppend={id:m.id,cursor:event.lastEventId,paired:false};
      }else if(type==='conversation.updated'&&pendingAppend?.cursor===inboxEventCursor&&!pendingAppend.paired&&
               value.conversation?.accountId===body.dataset.accountId){
        // A retained incoming message and its conversation summary commit together.
        pendingAppend={...pendingAppend,cursor:event.lastEventId,paired:true};
      }else return false;
    }else if(pendingAppend)return false;
    changedRow = !inboxEventCursor || changedRow === id ? id : '';
    inboxEventCursor = event.lastEventId;
    scheduleRefresh(true);
    return true;
  }

  function applyInboxUpdate(region, list, generation, append) {
    const fresh = region.querySelector('#live-list');
    if (!list?.isConnected || !fresh) return false;
    const rows = new Map([...list.querySelectorAll('[data-conversation-row]')].map(row => [row.dataset.conversationRow, row]));
    for (const stub of fresh.querySelectorAll('[data-conversation-stub]')) {
      const existing = rows.get(stub.dataset.conversationStub);
      if (!existing || existing.dataset.version !== stub.dataset.version) return false;
      stub.dataset.conversationRow = existing.dataset.conversationRow;
    }
    if(append&&!append())return false;
    inbox.reconcile(list, fresh);
    list.dataset.version = fresh.dataset.version;
    inbox.track(list, generation, region.dataset.eventCursor);
    for (const badge of region.querySelectorAll('[data-account-unread]')) {
      for (const mounted of document.querySelectorAll('[data-account-unread="' + CSS.escape(badge.dataset.accountUnread) + '"]')) {
        mounted.textContent = badge.textContent;
        mounted.hidden = badge.hidden;
        mounted.setAttribute('aria-label', badge.getAttribute('aria-label'));
      }
    }
    applyAvatars(list);
    localizeTimes(list);
    window.htmx?.process(list);
    return true;
  }
  async function refresh() {
    if (stopped || mock) return;
    if (pendingRequests || refreshing || sourceNavigation) { refreshAgain = true; return; }
    refreshing = true;
    lastRefreshStart = performance.now();
    const renameEpoch = accountRenameEpoch;
    const roomGeneration = roomCommandGeneration;
    const starGeneration=Number(body.dataset.starGeneration||0);
    const preferenceGeneration=Number(body.dataset.conversationPreferenceGeneration||0);
    const list = document.getElementById('conversation-list');
    const inboxGeneration = inbox.generation;
    const requestedSearch=location.search;
    const refreshQuery=new URLSearchParams(requestedSearch);
    // Keep a source link addressable after scrolling to the live timeline.
    if(window.OieTimeline?.liveAttached&&body.dataset.contextMissing!=='true'){
      // A retired source clears the mounted identity, so revalidate its URL.
      if(body.dataset.focusMessage)refreshQuery.delete('message');
      for(const key of ['timelineCursor','timelineDirection'])refreshQuery.delete(key);
    }
    if(!refreshQuery.has('chat')&&list){
      if(body.dataset.conversationId)refreshQuery.set('chat',body.dataset.conversationId);
      if(!body.dataset.conversationId||document.querySelector('#conversation-pane[data-open-chat="false"]'))refreshQuery.set('inbox','1');
    }
    const headers = {};
    if(list && inbox.current(list) && events.connected){
      headers['X-Oie-Inbox-Scope'] = list.dataset.inboxCacheScope;
      headers['X-Oie-Inbox-Cursor'] = events.latest(list.dataset.inboxCursor);
    }
    const conditional = !fullRefresh && appliedCursor && list && events.connected &&
      !journal?.pending && !sourceNavigation && !recoveringView && !refreshQuery.has('open') &&
      !refreshQuery.has('searchMode') && !refreshQuery.has('chatSearch') &&
      ![...document.querySelectorAll('dialog[open],[role="dialog"][aria-modal="true"]:not([hidden])')].some(dialog => dialog.getClientRects().length);
    const append=pendingAppend?.cursor===inboxEventCursor?pendingAppend:undefined;
    if(conditional && inboxEventCursor){
      const edge=append&&window.OieTimeline?.appendEdge();
      if(!append||edge){
        headers['X-Oie-Inbox-Event']=inboxEventCursor;
        headers['X-Oie-Inbox-Row']=changedRow;
        if(append){
          headers['X-Oie-Timeline-Message']=append.id;
          headers['X-Oie-Timeline-After']=edge.id;
          headers['X-Oie-Timeline-Scope']=edge.scope;
        }
      }
    }else if(conditional && inbox.current(list)){
      headers['X-Oie-Refresh-Cursor']=appliedCursor;
    }
    fullRefresh=false;
    const controller = new AbortController(); const timeout = life.timeout(() => controller.abort(), 12000);
    refreshController=controller;
    try {
      await reconcilePending(controller.signal);
      const reactionStartedAt=performance.now();
      const response = await life.fetch('/ui/refresh?'+refreshQuery, {credentials:'same-origin',cache:'no-store',headers,signal:controller.signal});
      if(life.signal.aborted||controller.signal.aborted)return;
      if (response.status === 204 && headers['X-Oie-Refresh-Cursor']) {
        if (requestedSearch !== location.search || !list.isConnected) refreshAgain = true;
        return;
      }
      if (!response.ok) throw new Error('refresh unavailable');
      const markup = await response.text();
      if(life.signal.aborted||controller.signal.aborted)return;
      // A room refresh started before Join or Leave can carry the opposite command.
      if(requestedSearch!==location.search || renameEpoch!==accountRenameEpoch || roomGeneration!==roomCommandGeneration){refreshAgain=true;return;}
      const page = new DOMParser().parseFromString(markup, 'text/html');
      const message=page.getElementById('oie-live-message');
      if(message){
        if(message.dataset.engineId!==body.dataset.engineId){engineChanged();return;}
        const cursor=message.dataset.eventCursor;
        if(!append||fullRefresh||pendingRequests||journal?.pending||cursor!==append.cursor||cursor!==inboxEventCursor||inboxGeneration!==inbox.generation||
           preferenceGeneration!==Number(body.dataset.conversationPreferenceGeneration||0)||
           !applyInboxUpdate(message,list,inboxGeneration,()=>window.OieTimeline?.appendMessage(message,headers['X-Oie-Timeline-After'],append.id,reactionStartedAt,starGeneration))){
          refreshAgain=true;return;
        }
        const state=message.querySelector('#live-header [data-conversation-preference-state]');
        if(state)rememberConversationForRemoval({id:state.dataset.conversation,accountId:state.dataset.account,revisionId:state.dataset.revision,title:document.querySelector('#chat-header h2')?.textContent});
        document.dispatchEvent(new CustomEvent('oie:conversation-preferences-refreshed',{detail:{page}}));
        document.dispatchEvent(new CustomEvent('oie:live-message-appended',{detail:{cursor}}));
        appliedCursor=cursor;inboxEventCursor='';pendingAppend=undefined;
        return;
      }
      const inboxOnly = page.getElementById('oie-live-inbox');
      if(inboxOnly){
        if(inboxOnly.dataset.engineId!==body.dataset.engineId){engineChanged();return;}
        if(preferenceGeneration!==Number(body.dataset.conversationPreferenceGeneration||0)||
           !applyInboxUpdate(inboxOnly,list,inboxGeneration)){refreshAgain=true;return;}
        appliedCursor=inboxOnly.dataset.eventCursor;
        if(inboxEventCursor===appliedCursor)inboxEventCursor='';
        return;
      }
      const regions = page.querySelector('#oie-live-regions');
      if (!regions || regions.dataset.unavailable === 'true') { appliedCursor=''; announce(text('liveRetry')); return; }
      if (body.dataset.engineId && regions.dataset.engineId !== body.dataset.engineId) {
        engineChanged(); return;
      }
      if (recoveringView && regions.dataset.engineId) {
        await window.OieNavigation.go(location.href,{history:true,workspace:'oie-app',noInboxReuse:true});
        return;
      }
      body.dataset.contextNewer=regions.dataset.contextNewer||'false';body.dataset.contextMissing=regions.dataset.contextMissing||'false';
      if(refreshQuery.has('message')&&body.dataset.contextMissing!=='true'&&!body.dataset.focusMessage&&canonicalID(regions.dataset.focusMessage)){
        body.dataset.focusMessage=regions.dataset.focusMessage;
        if(window.OieTimeline?.liveAttached)refreshAgain=true;
      }
      const contextNotice=document.querySelector('#context-notice'),newNotice=page.querySelector('#live-context-notice');if(contextNotice&&newNotice&&contextNotice.innerHTML!==newNotice.innerHTML)mutateDOM(contextNotice,newNotice.innerHTML);
      readAvatars(page);
      const room=document.querySelector('#room-session-status'),nextRoom=page.querySelector('#live-room-session #room-session-status');
      if(room&&nextRoom){
        if(composer){
          for(const key of ['sendAllowed','composeAllowed'])composer.dataset[key]=nextRoom.dataset[key]||'false';
          input.disabled=composer.dataset.composeAllowed!=='true'&&!journal?.pending;
        }
        if(room.dataset.version!==nextRoom.dataset.version&&!room.querySelector('input[name="nickname"]:focus,input[name="password"]:not([readonly]):focus')){
          const previousFocus=room.contains(document.activeElement),previousFeedback=room.querySelector('[data-form-feedback]'),previousCommand=room.querySelector('[data-room-command]')?.dataset.roomCommand;
          const savedFeedback=previousFeedback?.textContent,savedError=previousFeedback?.dataset.error;
          mutateDOM(room,nextRoom.innerHTML);room.dataset.version=nextRoom.dataset.version;room.dataset.roomState=nextRoom.dataset.roomState;room.dataset.roomActive=nextRoom.dataset.roomActive;for(const key of ['sendAllowed','composeAllowed','replyAllowed'])room.dataset[key]=nextRoom.dataset[key];
          document.dispatchEvent(new CustomEvent('oie:room-status-updated'));
          const currentFeedback=room.querySelector('[data-form-feedback]');if(savedFeedback&&currentFeedback&&previousCommand===room.querySelector('[data-room-command]')?.dataset.roomCommand){currentFeedback.textContent=savedFeedback;currentFeedback.dataset.error=savedError||'false';}
          if(previousFocus){const target=currentFeedback?.textContent?currentFeedback:room.querySelector('[data-room-status-label]');if(target){target.tabIndex=-1;target.focus({preventScroll:true})}}
        }
        restoreRoomOperation();renderReply();syncRoomLeave();
      }
      const preferencesCurrent=preferenceGeneration===Number(body.dataset.conversationPreferenceGeneration||0);
      const nextList=page.querySelector('#live-list');
      if(preferencesCurrent&&list?.isConnected&&nextList){
        if(list.dataset.version!==nextList.dataset.version){
          inbox.reconcile(list,nextList);list.dataset.version=nextList.dataset.version;
        }
        inbox.track(list,inboxGeneration,regions.dataset.eventCursor);
      }else if(!preferencesCurrent)refreshAgain=true;
      if (regions.dataset.conversationId === body.dataset.conversationId) {
        if (composer) {
          const mediaChoice = composer.querySelector('[data-media-choice]');
          if (mediaChoice) mediaChoice.dataset.uploadLimit = regions.dataset.uploadLimit || '';
          const allowed = regions.dataset.attachmentAllowed;
          if (allowed && allowed !== composer.dataset.attachmentAllowed) {
            composer.dataset.attachmentAllowed = allowed;
            renderReply();
          }
        }
        const pane=document.getElementById('conversation-pane');
        const removed=regions.dataset.conversationRemoved==='true';
        if (pane && removed !== (pane.dataset.conversationRemoved==='true')) {
          // Retire the old composer immediately, including while navigation is pending.
          if (removed && composer) {
            composer.dataset.sendAllowed='false';
            composer.inert=true;
          }
          const restoreFocus=pane.contains(document.activeElement);
          const url=new URL(location.href);
          if (!window.OieNavigation) { location.reload(); return; }
          await window.OieNavigation.go(url.href, {
            history:true, workspace:url.searchParams.has('chat')?'conversation-pane':'oie-app', noInboxReuse:true,
          });
          if (restoreFocus && document.activeElement === document.body) {
            document.getElementById('message-timeline')?.focus({preventScroll:true});
          }
          return;
        }
        if(pane&&(pane.dataset.conversationRequest||'')!==(regions.dataset.conversationRequest||'')){
          const focus=pane.querySelector('[data-conversation-request-panel]')?.contains(document.activeElement);
          const route=location.pathname+location.search;
          if(!window.OieNavigation){location.reload();return;}
          await window.OieNavigation.go(route,{history:true});
          if(focus)document.querySelector('#conversation-request-title,#message-composer')?.focus({preventScroll:true});
          return;
        }
        const timeline = document.querySelector('#message-timeline'), next = page.querySelector('#live-timeline');
        if (timeline && next && window.OieTimeline) window.OieTimeline.refresh(page,reactionStartedAt);
        const header = document.querySelector('#chat-header'), nextHeader = page.querySelector('#live-header');
        if (preferencesCurrent) {
          const state=nextHeader?.querySelector('[data-conversation-preference-state]');
          if (state) rememberConversationForRemoval({
            id:state.dataset.conversation, accountId:state.dataset.account, revisionId:state.dataset.revision,
            title:nextHeader.querySelector('h2')?.textContent, removedFromChats:removed,
          });
        }
        // A native confirmation returns focus to the current menu trigger.
        const confirmingRemoval = document.getElementById('conversation-removal')?.open;
        if (preferencesCurrent && header && nextHeader?.firstElementChild && header.dataset.version !== nextHeader.dataset.version &&
            !confirmingRemoval && !header.contains(document.activeElement)) {
          const replacement = nextHeader.firstElementChild.cloneNode(true);
          const apply = () => { window.Alpine?.destroyTree(header); header.replaceWith(replacement); window.Alpine?.initTree(replacement); };
          if (window.Alpine?.mutateDom) window.Alpine.mutateDom(apply); else apply();
          syncRoomLeave();
        }
      }
      if(preferencesCurrent)document.dispatchEvent(new CustomEvent('oie:conversation-preferences-refreshed',{detail:{page}}));
      for (const fresh of page.querySelectorAll('#live-account-blocks [data-account-id]')) {
        if (!fresh.matches('section[data-account-id]')) continue;
        const existing = document.querySelector(`#account-blocks>section[data-account-id="${CSS.escape(fresh.dataset.accountId)}"]`);
        if (existing) {
          refreshAccountStatuses(existing,fresh);
          refreshAccountNames(existing,fresh);
          const nameForm = existing.querySelector('#account-name-form');
          const nextNameForm = fresh.querySelector('#account-name-form');
          if (nameForm && nextNameForm && nameForm.dataset.busy !== 'true') {
            const name = nameForm.elements.namedItem('label');
            const revision = nextNameForm.elements.namedItem('expectedRevisionId').value;
            if (name.value === name.defaultValue && nameForm.elements.namedItem('expectedRevisionId').value !== revision) {
              syncAccountNameInput(nameForm, nextNameForm.elements.namedItem('label').value, revision, false);
            }
          }
          const photoFields=existing.querySelector('[data-account-photo-fields]');
          const nextPhotoFields=fresh.querySelector('[data-account-photo-fields]');
          if(photoFields&&nextPhotoFields)photoFields.disabled=nextPhotoFields.disabled;
          const photoAction=existing.querySelector('[data-edit-account-photo]');
          if (photoAction && nextPhotoFields) photoAction.disabled=nextPhotoFields.disabled;
          const oldError=existing.querySelector('[id^="connection-error-"]'),newError=fresh.querySelector('[id^="connection-error-"]');
          if(oldError&&newError&&oldError.innerHTML!==newError.innerHTML)mutateDOM(oldError,newError.innerHTML);
          const controls=existing.querySelector('[id^="connection-controls-"]'),nextControls=fresh.querySelector('[id^="connection-controls-"]');
          // State transitions replace obsolete actions. Same-state refreshes keep
          // password/endpoint edits, accordion state and pending feedback intact.
          if(controls&&nextControls&&controls.dataset.connectionState!==nextControls.dataset.connectionState){
            const disconnect=existing.querySelector('[data-account-disconnect-actions]');
            const nextDisconnect=fresh.querySelector('[data-account-disconnect-actions]');
            const restoreFocus=controls.contains(document.activeElement)||disconnect?.contains(document.activeElement);
            mutateDOM(controls,nextControls.innerHTML);controls.dataset.connectionState=nextControls.dataset.connectionState;
            if(disconnect&&nextDisconnect)mutateDOM(disconnect,nextDisconnect.innerHTML);
            if(restoreFocus)(controls.querySelector('button[type="submit"]')||disconnect?.querySelector('button[type="submit"]')||existing.closest('#accounts-body')?.querySelector('[data-account-back]'))?.focus({preventScroll:true});
          }
        }
      }
      const schedules=document.getElementById('scheduled-results'),newSchedules=page.querySelector('#scheduled-results');
      const destinations=page.querySelector('template[data-live-schedule-destinations]');
      if(schedules&&destinations)document.dispatchEvent(new CustomEvent('oie:agenda-options',{detail:destinations.content}));
      if(schedules&&newSchedules&&!schedules.inert&&schedules.dataset.version!==newSchedules.dataset.version&&!schedules.contains(document.activeElement)&&!document.querySelector('[data-schedule-dialog][open]')){
        mutateDOM(schedules,newSchedules.innerHTML);schedules.dataset.version=newSchedules.dataset.version;
      }
      const detailsRegion=page.querySelector('#live-conversation-details');
      if(detailsRegion?.dataset.detailsConversation===body.dataset.conversationId){
        const detailsScroll=document.getElementById('details-body')?.scrollTop;
        for(const section of ['identity','description','account','metadata']){
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
        refreshAccountNames(menu,nextMenu);
      }
      const nextTrigger = page.querySelector('#live-account-trigger [data-account-switcher]');
      if (nextTrigger) for (const trigger of document.querySelectorAll('[data-account-switcher]')) {
        if (trigger.dataset.activeAccountName !== nextTrigger.dataset.activeAccountName ||
            trigger.dataset.accountAvatarBorder !== nextTrigger.dataset.accountAvatarBorder) {
          trigger.dataset.accountAvatarBorder = nextTrigger.dataset.accountAvatarBorder;
          trigger.dataset.activeAccountName = nextTrigger.dataset.activeAccountName;
          mutateDOM(trigger, nextTrigger.innerHTML);
        }
      }
      if(composer&&regions.dataset.conversationId===body.dataset.conversationId){
        applyComposerActions(regions.dataset,reactionStartedAt);
        renderReply();renderEdit();
      }
      document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));document.dispatchEvent(new CustomEvent("oie:shared-refresh",{detail:page}));document.dispatchEvent(new CustomEvent('oie:reaction-snapshots',{detail:{page,startedAt:reactionStartedAt}}));applyAvatars();document.dispatchEvent(new CustomEvent("oie:stars-refresh",{detail:{page,generation:starGeneration,startedAt:reactionStartedAt}}));document.dispatchEvent(new CustomEvent("oie:previews-refresh",{detail:page}));localizeTimes();syncRoomLeave();
      appliedCursor=regions.dataset.eventCursor;
      if(inboxEventCursor===appliedCursor)inboxEventCursor='';
    } catch { appliedCursor=''; if(!life.signal.aborted&&!sourceNavigation)announce(text('liveRetry')); }
    finally {
      clearTimeout(timeout);if(life.signal.aborted)return; refreshing = false;refreshController=undefined;
      if (refreshAgain) { refreshAgain = false; scheduleRefresh(); }
    }
  }
  life.on(document,'htmx:before:request',event=>{
    const ctx=event.detail.ctx,fragment=ctx?.request?.headers?.['X-Oie-Fragment'];
    if(fragment!=='source'&&fragment!=='source-pane')return;
    sourceNavigation=ctx;
    clearTimeout(refreshTimer);
    refreshTimer = undefined;
    refreshController?.abort();
  });
  life.on(document,'htmx:finally:request',event=>{
    if(!sourceNavigation||event.detail.ctx!==sourceNavigation)return;
    sourceNavigation=undefined;
    if(life.signal.aborted)return;
    scheduleRefresh();
  });
  life.on(document,'oie:timeline-content',event=>{renderEditMarkers();readAvatars(event.detail.page);applyAvatars();localizeTimes();if(highlightedMessage)document.querySelector(`#message-timeline [data-message-id="${CSS.escape(highlightedMessage)}"]`)?.classList.add('oie-reply-highlight');});
  life.on(document,'oie:timeline-latest',()=>{
    clearTimeout(highlightTimer);highlightedMessage='';
    const timeline=document.getElementById('message-timeline');
    const active=document.activeElement;
    for(const node of [...acceptedBubbles,pendingBubble])if(node&&!node.isConnected){
      timeline.insertBefore(node,timeline.querySelector('[data-timeline-edge="newer"]'));window.Alpine?.initTree(node);
    }
    retireAcceptedBubbles();scrollLatest();
    if(active===body||active?.matches('[data-jump-latest]'))timeline.focus({preventScroll:true});
  });
  life.on(document,'oie:timeline-focus',event=>focusMessage(event.detail.id,event.detail.focus!==false));
  life.on(document,'oie:timeline-alias',event=>{if(highlightedMessage===event.detail.requested)highlightedMessage=event.detail.canonical;});
  life.on(document,'oie:room-management-content',event=>applyAvatars(event.detail.root));
  life.on(document,'oie:info-content',()=>{applyAvatars();localizeTimes();});
  life.on(document,'oie:accounts-content',()=>{applyAvatars();localizeTimes();});
  life.on(document,'oie:shared-account',event=>{
    const {page}=event.detail;
    const trigger=page.querySelector('[data-account-switcher]'),menu=page.querySelector('.oie-account-menu');
    if(trigger)for(const node of document.querySelectorAll('[data-account-switcher]'))mutateDOM(node,trigger.innerHTML);
    if(menu)for(const node of document.querySelectorAll('.oie-account-menu'))mutateDOM(node,menu.innerHTML);
    readAvatars(page);applyAvatars();
  });
  life.on(document,'oie:inbox-filtered',event=>{
    const root=event.detail.root;
    applyAvatars(root);localizeTimes(root);
    if(!inbox.current(root.querySelector('#conversation-list')))scheduleRefresh();
  });
  life.on(document,'oie:shared-append',event=>{if(event.detail?.page)readAvatars(event.detail.page);applyAvatars();localizeTimes();});
  for(const name of ['oie:shared-search-settled','oie:info-shared-search-settled'])life.on(document,name,event=>{if(event.detail?.page)readAvatars(event.detail.page);applyAvatars();localizeTimes();});
  life.on(document,'oie:refresh-request',()=>scheduleRefresh());
  function scheduleRefresh(conditional = false) {
    if (stopped || life.signal.aborted) return;
    if (conditional !== true) { fullRefresh = true; inboxEventCursor = ''; }
    if (sourceNavigation) { refreshAgain = true; return; }
    if (refreshTimer !== undefined) return;
    // Give an isolated event one short coalescing window. Under sustained
    // traffic, keep the same deadline and start at most four reads per second.
    const delay = Math.max(16, 250 - (performance.now() - lastRefreshStart));
    refreshTimer = life.timeout(() => {
      refreshTimer = undefined;
      void refresh();
    }, delay);
  }

  function needsReplyRefresh(reply) {
    return reply && (!editingSchedule || reply.messageId !== scheduledMessage?.composition?.replyToMessageId || canonicalID(reply.revisionId));
  }
  function setScheduledValues(values) {
    if(!composer.hasAttribute('data-schedule-composer'))return;
    values.set('scheduledAt',journal.scheduledAt||'');
    values.set('attachmentIds',JSON.stringify((journal.attachments||[]).map(file=>file.id)));
    if ((journal.scheduledAt || editingSchedule) && needsReplyRefresh(journal.reply) && canonicalID(journal.reply.revisionId)) {
      values.set('expectedReplyRevisionId', journal.reply.revisionId);
      if (validExcerptDigest(journal.reply.excerptSha256)) values.set('expectedReplyExcerptSha256', journal.reply.excerptSha256);
    }
  }
  function focusAfterSend() {
    if (journal?.offline && pendingBubble) {
      pendingBubble.focus({preventScroll:true});
      return;
    }
    let target = input;
    const voiceDraft = composer.querySelector('[data-record-audio]:not([hidden])');
    if (voiceDraft) {
      const retry = pendingBubble?.querySelector('[data-pending-state]:not([hidden]) [data-pending-send-retry]:not(:disabled)');
      const retrySchedule = scheduleRetry?.matches(':not([hidden]):not(:disabled)') ? scheduleRetry : null;
      target = retry || retrySchedule ||
        (composerSend.disabled ? voiceDraft.querySelector('[data-record-audio-remove]:not(:disabled)') : composerSend);
    }
    target?.focus({preventScroll:true});
  }
  life.on(document,'submit', async event => {
    const form = event.target.closest('form[data-oie-action]');
    if (!form || mock) return;
    event.preventDefault();
    if (form.dataset.busy === 'true' || stopped) return;
    const sending = form.dataset.oieAction === 'send';
    const sendFocus = sending && form.contains(document.activeElement) ? document.activeElement : null;
    const retainsSendFocus = () => sendFocus && [sendFocus, body].includes(document.activeElement);
    const retryingSend = sending && journal?.pending === true;
    let reservation;
    let releaseReservation = false;
    let normalAcceptance;
    let savedOffline = false;
    const accountAvatar = form.dataset.oieAction === 'account-avatar';
    const removingAccount = form.dataset.oieAction === 'remove-account';
    const accountLabel = form.dataset.oieAction === 'account-label';
    const accountEditor = accountAvatar || accountLabel;
    const scheduleDrawer = form.dataset.oieAction === 'schedule-row' && form.closest('[data-conversation-schedules]');
    if(sending&&(editSession||editPreparing)){if(editSession&&!editSession.submitted)void submitEdit(false);return;}
    if (sending) {
      let voiceSource;
      const followRecordingDraft = () => {
        voiceSource.key = draftStorageKey();
        voiceSource.draft = {...voiceSource.draft,key:journal.key};
      };
      try {
        if(preparingReply){feedback(form,text('replyPreparing'));return}
        if (recordingUploadPending()||composer.hasAttribute('data-capturing-audio')||(form.dataset.sendAllowed !== 'true'&&!editingSchedule&&!journal?.pending)) return;
        let voiceSubmittedAt, finishVoiceSendAfterNavigation;
        if (composer.hasAttribute('data-attachments-pending')) {
          if (!canQueueRecordingUpload() || editingSchedule || journal?.pending) return;
          const upload = {};
          composer.dispatchEvent(new CustomEvent('oie:wait-for-recording-upload', {detail: upload}));
          if (!upload.ready) return;
          voiceSource = {key: draftStorageKey(), draft: draftRecord(), token: localOperationKey()};
          const claim = claimRecordingUpload(voiceSource.token);
          voiceSubmittedAt = new Date().toISOString();
          waitingForRecordingUpload = true;
          renderReply();
          const claimed = await claim;
          if (!claimed && !resettingDevice() && upload.abandon()) {
            waitingForRecordingUpload = false;
            renderReply();
            errorToast(offlineCopy.storageFailed);
            return;
          }
          const metadata = await upload.ready;
          waitingForRecordingUpload = false;
          if (resettingDevice()) return;
          if (metadata && journal?.attachments?.some(file => file.id === metadata.id)) followRecordingDraft();
          if (!claimed) {
            if (metadata && life.signal.aborted) {
              recoverStagedRecording(voiceSource, {...voiceSource.draft, attachments:[metadata], submittedAt:voiceSubmittedAt, conversationKind});
            } else if (!life.signal.aborted) {
              renderReply();
              if (metadata) errorToast(offlineCopy.storageFailed);
            }
            return;
          }
          if (metadata) finishVoiceSendAfterNavigation = () => sendStagedRecording(voiceSource, metadata, voiceSubmittedAt);
          if (life.signal.aborted) {
            await finishVoiceSendAfterNavigation?.();
            return;
          }
          renderReply();
          if (!metadata || stopped || editingSchedule || editSession || editPreparing || journal?.pending ||
              form.dataset.busy === 'true' || form.dataset.sendAllowed !== 'true' ||
              composer.hasAttribute('data-attachments-pending') || !journal?.attachments?.some(file => file.id === metadata.id)) return;
        }
        if (journal?.offline) {
          if (offlinePendingState(journal) === 'uncertain') retryOfflineSend();
          return;
        }
        const scheduledAt=journal?.pending?journal.scheduledAt:form.elements.namedItem('scheduledAt')?.value;
        let offline = !journal?.pending && !scheduledAt && !editingSchedule && !navigator.onLine;
        if(!offline&&!journal?.pending&&needsReplyRefresh(journal?.reply)){
          const reviewed = scheduledAt || editingSchedule ? scheduleReview(draftRecord()) : '';
          const requested = journal.reply.messageId, epoch = ++replyEpoch;
          preparingReply = true;
          renderReply();
          try{
            const reply = await fetchReplySource(requested, Boolean(reviewed));
            if (life.signal.aborted) { await finishVoiceSendAfterNavigation?.(); return; }
            if (epoch !== replyEpoch) return;
            const current = draftRecord();
            if (reviewed && reviewed !== scheduleReview(current)) {
              errorToast(scheduleReviewChanged);
              return;
            }
            const refreshed = {...current, reply};
            if (voiceSource) {
              saveQueuedRecordingDraft(refreshed, current);
              followRecordingDraft();
            } else saveDraft(refreshed);
            if (reviewed && reviewed !== scheduleReview(refreshed)) {
              errorToast(scheduleReviewChanged);
              return;
            }
          } catch (error) {
            if (life.signal.aborted) { await finishVoiceSendAfterNavigation?.(); return; }
            if (epoch !== replyEpoch) return;
            if (!reviewed && error instanceof ReplySourceUnavailable) {
              // The failed lookup was a GET. Keep the selected quote for replay.
              offline = true;
            } else {
              if (reviewed) errorToast(text('replyUnavailable'));
              else feedback(form, text('replyUnavailable'), true);
              return;
            }
          }
          finally{finishReplyPreparation(epoch)}
        }
        const value = journal?.pending ? journal.text : input.value;
        if ((!value.trim() && !journal?.attachments?.length) || Array.from(value).length > 16384) { feedback(form,text('invalidMessage'),true);input.focus();return; }
        if (journal?.pending) submissionKey.value=journal.key;
        const mentions=journal?.pending?(journal.mentions||[]):readMentions();
        const record = {...draftRecord(true), text: value, mentions, scheduledAt,
          privateMapLinks: journal?.pending ? journal.privateMapLinks : privateMapLinks(),
          submittedAt: journal?.submittedAt || voiceSubmittedAt};
        if (offline || voiceSource) {
          form.dataset.busy = 'true';
          lockComposer(true);
          renderReply();
          const saved = await saveOfflineSend(record, undefined, voiceSource);
          if (life.signal.aborted) {
            if (!saved) await finishVoiceSendAfterNavigation?.();
            return;
          }
          form.dataset.busy = 'false';
          lockComposer(Boolean(journal?.pending));
          renderReply();
          if (saved || offline) {
            if (retainsSendFocus()) focusAfterSend();
            if (!saved) errorToast(offlineCopy.storageFailed);
            if (saved && !offline) wakeOfflineSends();
            return;
          }
        }
        if (!editingSchedule && draftKey === ordinaryDraftKey) {
          form.dataset.busy = 'true';
          lockComposer(true);
          renderReply();
          reservation = await reserveNormalSend(record);
          if (life.signal.aborted) {
            if (reservation?.token) await settleNormalSend(reservation, true);
            await finishVoiceSendAfterNavigation?.();
            return;
          }
          form.dataset.busy = 'false';
          if (reservation === false) {
            lockComposer(Boolean(journal?.pending));
            renderReply();
            if (retainsSendFocus()) focusAfterSend();
            return;
          }
          if (reservation?.accepted) {
            consumeAcceptedDraft(record, reservation);
            if (retainsSendFocus()) focusAfterSend();
            return;
          }
          submissionKey.value = record.key;
        }
        saveDraft(record);if(!scheduledAt&&!editingSchedule)input.value='';restoreMentions(mentions);lockComposer(true);renderReply();
      } finally {
        if (voiceSource) releaseRecordingUpload(voiceSource.token);
      }
    }
    const fields = new FormData(form);
    const photoInput = accountAvatar ? form.elements.namedItem('image') : null;
    const submittedPhoto = photoInput?.files[0];
    const nameInput = accountLabel ? form.elements.namedItem('label') : null;
    const editorSave = accountEditor ? form.querySelector('button[type="submit"]') : null;
    let values = form.enctype === 'multipart/form-data' ? fields : new URLSearchParams(fields);
    const removingConversation = form.dataset.oieAction === 'remove-conversation';
    if (removingConversation) form.dataset.removalRetry = 'true';
    const roomManagement = form.dataset.oieAction === 'room-management';
    const managementKey = roomManagement ? values.get('operationKey') : '';
    if (managementKey) {
      const key = 'oie.room-management:' + managementKey;
      const previous = inMemory.get(key);
      if (previous) values = previous;
      else inMemory.set(key, values);
    }
    const roomEntry = !!form.dataset.roomEntry;
    if (roomEntry) {
      const saved = pendingRoomEntry(form);
      if (saved) {
        for (const name of roomEntryFields) values.set(name, saved[name] || '');
        if (!values.get('password') && roomEntryPasswords.has(form)) values.set('password', roomEntryPasswords.get(form));
      } else if (canonicalID(values.get('accountId')) && values.get('address')) {
        saveRoomEntry(form, Object.fromEntries(roomEntryFields.map(name => [name, values.get(name) || ''])));
      }
    }
    if (roomEntry && values.get('password')) roomEntryPasswords.set(form, values.get('password'));
    const roomCommand=form.dataset.roomCommand;
    const roomMenuSubmit=roomCommand==='leave'&&form.dataset.roomMenuSubmit==='true';
    if (roomCommand) roomCommandGeneration++;
    if(roomCommand==='join'&&values.get('password'))roomCommandPassword=values.get('password');
    if(roomCommand==='join'&&roomCommandPassword)values.set('password',roomCommandPassword);
    // Old journals have no password marker. Check their receipt before replaying too.
    if(roomCommand==='join'&&roomOperation&&roomOperation.passwordRequired!==false&&!roomCommandPassword&&!roomPasswordRequested){
      values.set('checkJoinReceipt','1');
    }
    if(roomCommand){if(roomOperation){for(const key of ['operationKey','expectedRevisionId','expectedConversationRevisionId','nickname','conversationId','accountId'])values.set(key,roomOperation[key]||'')}else keepRoomOperation({command:roomCommand,passwordRequired:!!roomCommandPassword,operationKey:values.get('operationKey'),expectedRevisionId:values.get('expectedRevisionId'),expectedConversationRevisionId:values.get('expectedConversationRevisionId')||'',nickname:values.get('nickname')||'',conversationId:values.get('conversationId'),accountId:values.get('accountId')})}

    if(sending){
      values.set('originalText',journal.text);values.set('replyToMessageId',journal.reply?.messageId||'');values.set('mentions',JSON.stringify(journal.mentions||[]));setScheduledValues(values);
      if (!retryingSend && !editingSchedule && !journal.scheduledAt &&
          (reservation?.firstAttempt || reservation === null)) values.set('offlineCapable', '1');
    }
    const buttons = [...form.querySelectorAll('input:not([type=hidden]),textarea,select,button')];
    const disabled = buttons.map(button => button.disabled);
    form.dataset.busy = 'true'; form.setAttribute('aria-busy','true');
    const removeButton = removingAccount ? form.querySelector('button[type=submit]') : null;
    const cancelRemove = removingAccount ? form.querySelector('[data-remove-cancel]') : null;
    if (accountLabel) accountRenameEpoch++;
    if (accountEditor) editorSave?.setAttribute('aria-disabled','true');
    else if (removeButton) {
      removeButton.setAttribute('aria-disabled','true');
      if (cancelRemove) cancelRemove.inert = true;
    }
    else buttons.forEach(button => button.disabled = true);
    syncRoomLeave();
    for (const secret of form.querySelectorAll('input[type="password"]')) secret.value = '';
    if (roomEntry) {
      const submit = form.querySelector('[data-room-entry-label]');
      if (submit) submit.textContent = submit.dataset.roomEntryLabel;
    }
    feedback(form,sending?'':removingAccount?form.dataset.removing:form.dataset.roomEntryChecking||text('saving'));
    pendingRequests++;
    const controller = new AbortController(); const timeout = life.timeout(() => controller.abort(),15000);
    try {
      const response = await life.fetch(form.action,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal});
      const signedOut = window.OieHostedSession?.requiresSignIn(response);
      if (signedOut && (!sending || retryingSend)) return;
      const result = signedOut ? {rejected: true} : await response.json();
      if(life.signal.aborted)return;
      const keyConflict = sending && !retryingSend && !editingSchedule && result.code === 'idempotency_conflict';
      if (keyConflict) {
        result.rejected = true;
        result.message = offlineCopy.returned;
      }
      releaseReservation = result.accepted || reservation?.firstAttempt && result.rejected;
      if (result.accepted) normalAcceptance = result;
      if (sending && result.code === 'engine_unavailable' && values.get('offlineCapable') === '1') {
        releaseReservation = true;
        if (reservation) savedOffline = await saveOfflineSend(journal, reservation);
        if (savedOffline || life.signal.aborted) return;
        result.rejected = true;
        result.message = offlineCopy.storageFailed;
      }
      if (form.dataset.oieAction === 'account-communication') {
        if (result.ok && result.accountCommunication) {
          form.elements.expectedRevisionId.value = result.accountCommunication.revisionId || '';
        }
        const refresh = form.querySelector('[data-communication-refresh]');
        if (refresh && result.code === 'revision_conflict') refresh.hidden = false;
      }
      if (removingConversation && result.rejected) {
        delete form.dataset.removalRetry;
        form.elements.operationKey.value = localOperationKey();
        rememberConversationForRemoval(result.conversation);
      }
      if (roomManagement) {
        if (result.ok || result.rejected) {
          inMemory.delete('oie.room-management:' + managementKey);
          form.elements.operationKey.value = localOperationKey();
        }
        document.dispatchEvent(new CustomEvent('oie:room-management-result', {detail: {form, result}}));
      }
      const draftChanged = nameInput ? nameInput.value !== fields.get('label') :
        Boolean(photoInput && photoInput.files[0] !== submittedPhoto);
      if (accountLabel && form.isConnected && result.account?.id === fields.get('accountId') &&
          canonicalID(result.account.revisionId) && typeof result.account.label === 'string') {
        syncAccountNameInput(form, result.account.label, result.account.revisionId,
          !result.ok || draftChanged);
      }
      if (!result.ok) {
        if(result.code==='engine_changed')engineChanged(false);
        if (sending && result.rejected) {
          if (pendingBubble) {
            window.Alpine?.destroyTree(pendingBubble);
            pendingBubble.remove();
            pendingBubble = null;
          }
          const mentions=result.code==='mention_unavailable'?[]:journal.mentions;
          if(result.code==='mention_unavailable')form.dispatchEvent(new Event('oie:mentions-invalidated'));
          const {submittedAt, ...draft} = journal;
          if (keyConflict) {
            draft.key = localOperationKey();
            submissionKey.value = draft.key;
          }
          input.value=journal.text;restoreMentions(mentions);saveDraft({...draft,mentions,pending:false});lockComposer(false);
          const message = result.code === 'reply_source_changed' ? scheduleReviewChanged : result.message || text('invalidMessage');
          if (keyConflict || ['media_too_large','reply_source_changed','engine_unavailable'].includes(result.code)) errorToast(message);
          else feedback(form, message, true);
          if (result.code === 'reply_source_changed') void refreshRejectedQuote();
        } else if (removingAccount) {
          feedback(form, '');
          errorToast(result.message || form.dataset.removeFailed);
        } else {
          feedback(form, result.message || text('pending'), true);
        }
        if (roomEntry && (result.rejected || result.code === 'idempotency_conflict')) {
          saveRoomEntry(form, null);
          if (result.nextKey) form.elements.namedItem('operationKey').value = result.nextKey;
        }
        if(roomCommand==='join'&&(result.code==='room_password_requested'||(roomPasswordRequested&&result.code==='idempotency_conflict'))){
          roomPasswordRequested=true;
          roomCommandPassword='';
          const password=form.elements.namedItem('password');
          if(password)password.value='';
        }
        if(roomCommand&&result.rejected){keepRoomOperation(null);for(const field of form.querySelectorAll('input:not([type=hidden])'))field.readOnly=false;}
        if(scheduleDrawer&&['state_conflict','revision_conflict','not_found'].includes(result.code)){
          scheduleDrawer.dispatchEvent(new Event('oie:schedule-refresh'));
        }
        if(!sending)scheduleRefresh();
      } else {
        if (removingAccount && result.redirect) {
          inbox.invalidate();
          if (result.message) window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'success',message:result.message}}));
          if (window.OieNavigation) window.OieNavigation.go(result.redirect);
          else location.replace(result.redirect);
          return;
        }
        if (roomEntry) saveRoomEntry(form, null);
        if(roomCommand){
          keepRoomOperation(null);
          if(roomCommand==='leave')document.dispatchEvent(new CustomEvent('oie:room-reaction-visibility-withdrawn'));
        }
        if (accountEditor && form.isConnected) {
          if (photoInput && photoInput.files[0] === submittedPhoto) {
            photoInput.value = '';
            photoInput.dispatchEvent(new Event('change', {bubbles:true}));
          }
          const editor = form.closest('dialog');
          if (editor?.open && draftChanged) {
            feedback(form, result.message);
          } else {
            if (editor?.open) window.dispatchEvent(new CustomEvent('modal:close',{detail:{id:editor.id}}));
            if (result.message) window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'success',message:result.message}}));
          }
        } else if (roomManagement) {
          feedback(form, '');
          if (result.message) window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'success',message:result.message}}));
        } else {
          feedback(form,sending ? '' : result.message || '');
        }
        if (sending && result.accepted) {
          if(!completeSubmission(result))feedback(form,text('pending'),true);
          renderReply();
        } else if(form.dataset.oieAction==='schedule-row'){
          if(scheduleDrawer) scheduleDrawer.dispatchEvent(new Event('oie:schedule-refresh'));
          else {
            document.dispatchEvent(new CustomEvent('oie:agenda-refresh',{detail:{form}}));
            return;
          }
        } else if ((removingConversation || roomManagement) && result.redirect) {
          inbox.invalidate();
          if (window.OieNavigation) window.OieNavigation.go(result.redirect);
          else location.replace(result.redirect);
          return;
        } else if (form.dataset.oieAction === 'register' || form.dataset.oieAction === 'open') {
          if (result.redirect) { if(window.OieNavigation)window.OieNavigation.go(result.redirect);else location.assign(result.redirect); return; }
        }
        const key = form.elements.namedItem('operationKey'); if (key && result.nextKey && (!sending || !journal?.pending)) key.value=result.nextKey;
        scheduleRefresh();
      }
    } catch {
      if(life.signal.aborted)return;
      if (roomManagement) {
        document.dispatchEvent(new CustomEvent('oie:room-management-result', {detail: {form, result: {ok:false}}}));
      }
      if (removingAccount) {
        feedback(form, '');
        errorToast(form.dataset.removeFailed);
      } else feedback(form,sending ? text('pending') : text('liveRetry'),true);
    } finally {
      if (reservation) await settleNormalSend(reservation, releaseReservation, normalAcceptance);
      clearTimeout(timeout);
      if(!life.signal.aborted||form.isConnected){
        form.dataset.busy='false';form.removeAttribute('aria-busy');
        if (accountEditor) editorSave?.removeAttribute('aria-disabled');
        else if (removeButton) {
          removeButton.removeAttribute('aria-disabled');
          if (cancelRemove) cancelRemove.inert = false;
        }
        else buttons.forEach((button,index)=>button.disabled=disabled[index]);
        if(life.signal.aborted)feedback(form,text('liveRetry'),true);
      }
      if(life.signal.aborted)return;
      pendingRequests--;renderReply();syncRoomLeave();
      if (roomCommand && roomOperation) restoreRoomOperation();
      if (roomEntry) restoreRoomEntry(form, pendingRoomEntry(form));
      if(journal?.pending)renderPendingSend(pendingBubble?.dataset.pendingState||'uncertain');
      if (sending && (!savedOffline || retainsSendFocus())) focusAfterSend();
      if(roomMenuSubmit&&form.querySelector('[data-form-feedback]')?.dataset.error==='true'){
        window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'room-session-dialog'}}));
        life.raf(()=>form.querySelector('[data-form-feedback]')?.focus({preventScroll:true}));
      }
      if(stopped&&composer)composerSend.disabled=true;
      if (refreshAgain) { refreshAgain=false;scheduleRefresh(); }
    }
  });
  function engineChanged(notify = true) {
    if (stopped) return;
    stopped = true; events.markReplaced();
    renderReply();
    if (notify) errorToast(text('engineChanged'));
  }
  function messageUpdated(event) {
    try{
      const message=JSON.parse(event.data).message;
      if(message&&message.accountId===body.dataset.accountId&&message.conversationId===body.dataset.conversationId){
        const hiddenSchedule = message.scheduledAt && !message.timelineAt && ['queued','dispatching','cancelled'].includes(message.sendState);
        if (hiddenSchedule || (message.timelineAt && message.sendState === 'cancelled')) {
          window.OieTimeline?.hideScheduled?.(message.id);
        }
        document.dispatchEvent(new CustomEvent('oie:message-updated',{detail:{id:message.id}}));
      }
    }catch{}
    scheduleRefresh();
  }
  function roomSessionChanged(event) {
    try{
      const room=JSON.parse(event.data).roomSession;
      if(room?.accountId===body.dataset.accountId&&room.conversationId===body.dataset.conversationId&&room.state!=='joined'){
        document.dispatchEvent(new CustomEvent('oie:room-reaction-visibility-withdrawn'));
      }
    }catch{}
    scheduleRefresh();
  }
  function starChanged(event) {
    try{document.dispatchEvent(new CustomEvent('oie:star-event',{detail:JSON.parse(event.data).star}));}catch{}
    scheduleRefresh();
  }
  function accountRemoved(event) {
    let id;
    try { id = JSON.parse(event.data).removedAccountId; } catch {}
    if (!canonicalID(id) || document.querySelector('#remove-account-form[aria-busy="true"]')) {
      scheduleRefresh();
      return;
    }
    const url = new URL(location.href), q = url.searchParams;
    const filterRemoved = q.get('account') === id;
    const chatRemoved = body.dataset.accountId === id;
    const managedRemoved = q.get('managedAccount') === id && q.get('open') === 'accounts';
    if (!filterRemoved && !chatRemoved && !managedRemoved) {
      scheduleRefresh();
      return;
    }
    if (filterRemoved) q.delete('account');
    if (chatRemoved || filterRemoved) {
      for (const key of ['chat','message','infoView','infoType','infoCursor','infoQuery','chatSearch','chatQuery','chatIncludeHistory','chatIncludeRetracted','chatSearchRevision','contentConversation','contentCursor','favoritesCursor','scheduleCursor']) q.delete(key);
      if (['sender','details'].includes(q.get('open'))) q.delete('open');
      q.set('inbox','1');
    }
    if (managedRemoved) for (const key of ['managedAccount','accountPage','accountFocus']) q.delete(key);
    if (window.OieNavigation) window.OieNavigation.go(url,{workspace:chatRemoved||filterRemoved?'oie-app':undefined});
    else location.replace(url.href);
  }
  if (!mock) {
    const handlers = {
      'account.removed': accountRemoved,
      'message.updated': messageUpdated,
      'conversation.history_pruned': event => {
        try {
          const history = JSON.parse(event.data).historyPruned;
          if (Array.isArray(history?.messageIds)) {
            if (history.conversationId === body.dataset.conversationId) window.OieTimeline?.expire?.(history.messageIds);
            document.dispatchEvent(new CustomEvent('oie:history-pruned',{detail:history}));
          }
        } catch {}
        scheduleRefresh();
      },
      'profile.avatar_changed': event => {
        try {
          const avatar = JSON.parse(event.data).avatar;
          if (avatar?.accountId) document.dispatchEvent(new CustomEvent('oie:room-participant-avatar-changed',{detail:avatar}));
        } catch {}
        scheduleRefresh();
      },
      'conversation.room_session_changed': roomSessionChanged,
      'message.star_changed': starChanged,
    };
    for (const type of events.eventTypes) {
      life.on(events.target,type,event=>{if(!stopped&&!inboxEvent(event.detail,type))(handlers[type]||scheduleRefresh)(event.detail);});
    }
    life.on(events.target,'open',()=>{if(!stopped){announce(text('liveReady'),true);scheduleRefresh(true);void resumeOfflineSends();}});
    life.on(events.target,'error',()=>{fullRefresh=true;inboxEventCursor='';if(!stopped)announce(text('liveRetry'));});
    life.on(events.target,'engine-changed',()=>engineChanged());
    events.sync(body.dataset.engineId,body.dataset.eventCursor);
    const mountedList=document.getElementById('conversation-list');
    if(events.replaced)engineChanged();
    else if(events.connected){
      // A view response may predate events that arrived while it was in flight.
      if((mountedList&&(!inbox.current(mountedList)||
        (body.dataset.eventCursor&&mountedList.dataset.inboxCursor!==body.dataset.eventCursor&&
          events.latest(mountedList.dataset.inboxCursor)!==body.dataset.eventCursor)))||
        events.latest(body.dataset.eventCursor)!==body.dataset.eventCursor||journal?.pending)scheduleRefresh();
    }else announce(text('liveConnecting'),true);
  }
  // Bounded recovery polling also covers a proxy that buffers or disables SSE.
  function recoverLive(){
    if(mock||stopped)return;
    events.sync(body.dataset.engineId,body.dataset.eventCursor);
    scheduleRefresh(true);
    void resumeOfflineSends();
  }
  life.interval(() => { if (!document.hidden) recoverLive(); },15000);
  function wakeLive(online){
    if(mock||stopped)return;
    inbox.invalidate();events.wake(online);recoverLive();
  }
  life.on(window,'online',()=>wakeLive(true));
  life.on(document,'visibilitychange',()=>{if(!document.hidden)wakeLive();});
  life.timeout(resumeOfflineSends, 0);
  if(recoveringView) scheduleRefresh();
  const open = new URLSearchParams(location.search).get('open') || body.dataset.openDrawer;
  if (open === 'sender' || open === 'accounts' || open === 'newchat' || open === 'details' || open === 'settings') {
    const show = () => {
      dispatchEvent(new CustomEvent('drawer:open',{detail:{id:open}}));
      if(open==='settings'&&!new URLSearchParams(location.search).has('settingsTab')&&new URLSearchParams(location.search).get('settingsFocus')!=='language')life.timeout(()=>document.querySelector('#settings-body input[name="theme"]:checked')?.focus({preventScroll:true}),230);
      if(open==='accounts')life.timeout(()=>{
        const query=new URLSearchParams(location.search);
        const target=query.get('accountPage')==='capabilities'?document.querySelector('[data-account-capabilities-title]'):query.get('accountFocus')==='capabilities'?document.querySelector('[data-account-capabilities]'):null;
        const removalTarget=query.get('accountPage')==='remove'?document.querySelector('[data-account-remove-title]'):query.get('accountFocus')==='remove'?document.querySelector('[data-account-remove]'):null;
        (target||removalTarget)?.focus({preventScroll:true});
      },230);
    };
    if (window.Alpine) life.timeout(show,0); else life.on(document,'alpine:initialized',show,{once:true});
  }
  const nativeError=document.querySelector('[data-native-error]');if(nativeError)nativeError.focus();
});
