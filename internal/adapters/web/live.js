window.OieRuntime.register('live', life => {
  'use strict';
  const body = document.body;
  body.dataset.oieLiveReady='true';
  const labels = document.querySelector('#oie-live-copy')?.dataset || {};
  const text = key => labels[key] || '';
  const status = document.querySelector('[data-live-status]');
  const announce = (message, quiet = false) => { if (status) { status.textContent = message; status.hidden = quiet || !message; } };
  const errorToast = message => { if (message) window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message}})); };
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
  function applyAvatars(root = document) {
    for (const scope of root.querySelectorAll('[data-avatar-account]')) {
      const src = avatarSources.get(avatarKey(scope.dataset.avatarAccount,scope.dataset.avatarAddress)) || '';
      scope.dataset.avatarSrc = src;
      const data = window.Alpine?.$data?.(scope);
      if (data && 'oieAvatarSrc' in data) data.oieAvatarSrc = src;
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
  if(composerSend) composerSend.dataset.sendMessage='';
  let editingSchedule = composer?.hasAttribute('data-editing-schedule');
  const readScheduledMessage = () => JSON.parse(document.getElementById('schedule-edit-data')?.textContent || 'null');
  let scheduledMessage = readScheduledMessage();
  const scheduleRetry = composer?.querySelector('[data-schedule-retry]');
  const scheduleReviewChanged = document.querySelector('[data-schedule-dialog]')?.dataset.reviewChanged;
  const ordinaryDraftKey = body.dataset.engineId && body.dataset.conversationId && body.dataset.accountId
    ? ['oie.draft.v1', body.dataset.engineId, body.dataset.accountId, body.dataset.conversationId].join(':') : '';
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
  function renderEditMarkers(){
    for(const node of document.querySelectorAll('[data-editing-message],[data-edit-pending]')){node.removeAttribute('data-editing-message');node.removeAttribute('data-edit-pending');node.removeAttribute('aria-busy');}
    const fallback=composer?.querySelector('.oie-composer-field');
    const place=(node,id)=>{const bubble=editMessageBody(id),target=bubble||fallback;if(node&&target&&node.parentElement!==target)target.append(node);if(node){const top=bubble&&!node.hidden?document.querySelector('#message-timeline').getBoundingClientRect().top:null;node.toggleAttribute('data-edit-below',top!==null&&bubble.getBoundingClientRect().top-top<100);}return bubble;};
    const bubble=place(editBox,editSession?.messageId);
    const state=editSession?.command?.state;
    if(bubble&&editSession){bubble.dataset.editingMessage='true';if(editBusy||editSession.submitted&&(!state||state==='queued'||state==='dispatching')){bubble.dataset.editPending='true';bubble.setAttribute('aria-busy','true');}}
    const previous=place(lastEditStatus,lastEditReceipt?.messageId);
    if(lastEditStatus)lastEditStatus.hidden=!lastEditReceipt?.command?.state||['queued','dispatching','submitted'].includes(lastEditReceipt.command.state)||editSession?.messageId===lastEditReceipt.messageId;
    if(previous&&['queued','dispatching'].includes(lastEditReceipt?.command?.state)&&!previous.hasAttribute('data-editing-message')){previous.dataset.editPending='true';previous.setAttribute('aria-busy','true');}
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
    input.value=journal?.pending?'':journal?.text||'';if(journal?.key)submissionKey.value=journal.key;restoreMentions(journal?.mentions);lockComposer(!!journal?.pending);renderReply();renderEdit();input.focus({preventScroll:true});
  }
  function leaveEdit(){if(editBusy||editSession?.submitted)return;if(editPreparing)window.OieTimeline?.cancelFocus();editEpoch++;editPreparing=false;clearTimeout(editPoll);keepEdit(null);restoreOrdinaryDraft();}
  function finishEdit(){keepLastEdit(editSession?{...editSession}:null);editEpoch++;clearTimeout(editPoll);editBusy=false;editPreparing=false;keepEdit(null);restoreOrdinaryDraft();scheduleRefresh();}
  function editMenuOpen(){return input.getAttribute('aria-expanded')==='true'||[...document.querySelectorAll('[role="dialog"],[role="menu"],[data-emoji-trigger][aria-expanded="true"]')].some(node=>node.getClientRects().length&&getComputedStyle(node).visibility!=='hidden');}
  async function beginEdit(messageId=''){
    if(!composer||!editBox||composer.hasAttribute('data-attachments-pending')||editingSchedule||composer.dataset.editAllowed!=='true'||journal?.pending||preparingReply||editBusy||editPreparing||editSession||stopped)return;
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
      input.value=target.originalText;restoreMentions([]);announceEdit(editText('mode'));input.focus({preventScroll:true});input.setSelectionRange(input.value.length,input.value.length);const bubble=editMessageBody(target.messageId),viewport=document.querySelector('#message-timeline')?.getBoundingClientRect(),bounds=bubble?.getBoundingClientRect();if(bounds&&viewport&&(bounds.bottom<=viewport.top||bounds.top>=viewport.bottom))bubble.scrollIntoView({block:'nearest',behavior:'auto'});
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

  function renderReply(){
    const reply=journal?.reply,locked=!!journal?.pending,editing=!!editSession||editPreparing;
    if(replyField)replyField.value=reply?.messageId||'';
    if(replyBox){replyBox.hidden=!reply||(locked&&!journal?.scheduledAt&&!editingSchedule)||!!editSession;replyBox.querySelector('[data-composer-reply-sender]').textContent=typeof reply?.senderName==='string'?reply.senderName:'';replyBox.querySelector('[data-composer-reply-excerpt]').textContent=typeof reply?.excerpt==='string'?reply.excerpt:'';const cancel=replyBox.querySelector('[data-cancel-reply]');if(cancel)cancel.disabled=locked||preparingReply;}
    for(const button of document.querySelectorAll('[id^="reply-message-"]'))button.disabled=!composer||composer.dataset.replyAllowed!=='true'||locked||preparingReply||editing;
    if(composerSend) composerSend.disabled=stopped||locked||preparingReply||editing||composer.dataset.busy==='true'||
      composer.hasAttribute('data-attachments-pending')||(composer.dataset.sendAllowed!=='true'&&!editingSchedule&&!locked);
    if(scheduleRetry){scheduleRetry.hidden=!locked||!journal.scheduledAt&&!editingSchedule;scheduleRetry.disabled=stopped||composer.dataset.busy==='true';}
    composer?.dispatchEvent(new Event('oie:composer-preview-change'));
    composer?.dispatchEvent(new CustomEvent('oie:composer-attachments',{detail:{files:journal?.attachments||[],locked:locked||editing||preparingReply||stopped||composer.dataset.busy==='true'}}));
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
  function readDraft() {
    if (!draftKey) return null;
    try { const raw = localStorage.getItem(draftKey); return raw ? JSON.parse(raw) : inMemory.get(draftKey)||null; }
    catch { storageWarning(); return inMemory.get(draftKey) || null; }
  }
  function saveDraft(record) {
    if (!draftKey) return;
    record = {...record, conversationKind: record.conversationKind || conversationKind};
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
    delete composer.dataset.privateMapLinks;
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
      timeline.scrollTop=timeline.scrollHeight;
    }
    pendingBubble.dataset.pendingState=state;
    // Keep an unresolved send readable while the retry action is available.
    pendingBubble.querySelector('[data-pending-send-content]').style.opacity=state==='uncertain'?'1':'';
    for(const node of pendingBubble.querySelectorAll('[data-pending-state]'))node.hidden=node.dataset.pendingState!==state;
    const disabled=state!=='uncertain'||stopped||composer.dataset.busy==='true';
    for(const action of pendingBubble.querySelectorAll('[data-pending-send-retry],#retry-pending-send')){action.disabled=disabled;action.setAttribute('aria-disabled',String(disabled));}
  }
  function acceptPendingSend(id){
    if(!pendingBubble)return;
    renderPendingSend('accepted');
    // Accepted placeholders have no retry action. The engine now owns delivery.
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
    const action=event.target.closest('[data-pending-send-retry],#retry-pending-send');
    if(!action||!pendingBubble?.contains(action)||action.disabled||!journal?.pending)return;
    event.preventDefault();composer.requestSubmit();
  });
  function feedback(form, message, error = false) {
    if(form===composer&&(journal?.scheduledAt||editingSchedule)){
      if(error)errorToast(message);
      return;
    }
    if(error&&form.matches('[data-cancel-message]')){errorToast(message);return;}
    if(form.dataset.oieAction==='schedule-row'){if(error)errorToast(message);return;}
    const region = form.querySelector('[data-form-feedback]');
    if(form===composer&&journal?.pending&&!journal.scheduledAt&&!editingSchedule){if(region)region.textContent='';renderPendingSend(error?'uncertain':'sending');announce(message,true);return;}
    if (region) { region.textContent = message; region.dataset.error = String(error);region.setAttribute('role',error?'alert':'status'); if(error&&form.dataset.oieAction!=='send'){region.tabIndex=-1;region.focus({preventScroll:true});} }
    if (error && !region) errorToast(message);
    else announce(message);
  }
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
  function restoreDraft(nextKey = submissionKey?.value, initialText = '') {
    journal = null;
    delete composer.dataset.privateMapLinks;
    input.value = initialText;
    submissionKey.value = nextKey;
    let saved = readDraft();
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
      if (journal?.pending || editSession?.submitted || (storageDenied && input.value)) { event.preventDefault(); event.returnValue = ''; }
    });
  }


  if(lastEditKey&&!mock){let saved;try{saved=JSON.parse(localStorage.getItem(lastEditKey)||'null')||inMemory.get(lastEditKey)}catch{saved=inMemory.get(lastEditKey)}if(validEditSession(saved)&&matchingEditCommand(saved.command,saved)){keepLastEdit(saved);life.timeout(()=>checkCompletedEdit(),0);}}
  if(editKey&&!mock&&!editingSchedule){let saved;try{saved=JSON.parse(localStorage.getItem(editKey)||'null')||inMemory.get(editKey)}catch{saved=inMemory.get(editKey)}if(validEditSession(saved)&&!journal?.pending){editSession=saved;input.value=saved.text;restoreMentions([]);if(saved.submitted)life.timeout(()=>submitEdit(true),0);}}
  renderReply();renderEdit();
  if(editSession&&!editMessageBody(editSession.messageId)){const sourceID=editSession.messageId;life.timeout(async()=>{if(editSession?.messageId!==sourceID)return;await window.OieTimeline?.focus(sourceID,{focus:false});if(!life.signal.aborted)renderEditMarkers();},0);}
  life.on(document,'oie:actions-refreshed',()=>{renderReply();renderEdit();});
  const validExcerptDigest = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value);
  async function fetchReplySource(messageId, requireReview = false) {
    const controller = new AbortController(), timeout = life.timeout(() => controller.abort(), 12000);
    try {
      const query = new URLSearchParams({engineId:body.dataset.engineId, conversationId:body.dataset.conversationId, accountId:body.dataset.accountId, messageId});
      const response = await life.fetch('/ui/reply-source?' + query, {credentials:'same-origin', cache:'no-store', signal:controller.signal});
      const reply = await response.json();
      if (!response.ok || reply.engineId !== body.dataset.engineId || reply.accountId !== body.dataset.accountId ||
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
  function keepRoomOperation(value){if(!value&&roomOperation){const region=document.querySelector('#room-session-status');if(region)region.dataset.version='';}roomOperation=value;if(!roomDraftKey)return;if(value)inMemory.set(roomDraftKey,value);else inMemory.delete(roomDraftKey);try{if(value)localStorage.setItem(roomDraftKey,JSON.stringify(value));else localStorage.removeItem(roomDraftKey)}catch{storageWarning()}}
  function restoreRoomOperation(){
    if(!roomOperation)return;
    const form=document.querySelector('[data-room-command]');if(!form)return;
    form.action='/ui/rooms/'+roomOperation.command;form.dataset.oieAction=roomOperation.command+'_room';form.dataset.roomCommand=roomOperation.command;
    for(const key of ['operationKey','expectedRevisionId','expectedConversationRevisionId','nickname']){const field=form.elements.namedItem(key);if(field)field.value=roomOperation[key]||'';}
    for(const field of form.querySelectorAll('input:not([type=hidden])'))field.readOnly=true;
    const button=form.querySelector('[data-room-action]');if(button){button.dataset.roomAction=roomOperation.command;button.textContent=text('retry');button.title=text('retry');}
    const region=form.querySelector('[data-form-feedback]');if(region){region.textContent=text('roomCommandUnknown');region.dataset.error='true';region.setAttribute('role','alert');}
  }
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
  function roomOperationKey() {
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
      form.elements.namedItem('operationKey').value = roomOperationKey();
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
  life.on(document, 'oie:conversation-drawer-open', ({target, detail: id}) => {
    const menu = target.closest('#chat-options-panel');
    const trigger = document.querySelector('#chat-options [aria-controls="chat-options-panel"]');
    if (!menu || trigger?.getAttribute('aria-expanded') !== 'true') return;

    trigger.click();
    // The drawer's focus trap must start after the dropdown leaves the top layer.
    const openAfterMenu = () => {
      if (!menu.isConnected || trigger.getAttribute('aria-expanded') === 'true') return;
      if (menu.getClientRects().length) {
        life.raf(openAfterMenu);
        return;
      }
      trigger.focus({preventScroll: true});
      window.dispatchEvent(new CustomEvent('drawer:open', {detail: {id}}));
    };
    life.raf(openAfterMenu);
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
  function localizeTimes(root=document) {
    window.OieTime.localize(root);
    const timeline=root.querySelector('#message-timeline');if(!timeline)return;
    for(const divider of timeline.querySelectorAll('.oie-day-divider'))divider.remove();
    const key=date=>window.OieTime.dateKey(date,'day');
    const today=key(new Date());let previous='';
    for(const row of timeline.querySelectorAll(':scope>[data-message-id],:scope>[data-pending-send]')){
      const date=new Date(row.dataset.createdAt);if(Number.isNaN(date.getTime()))continue;
      const current=key(date);if(current===previous)continue;previous=current;
      const divider=document.createElement('div');divider.className='oie-day-divider';
      const time=document.createElement('time');time.dateTime=row.dataset.createdAt;time.dataset.day='true';
      time.textContent=current===today?text('today'):window.OieTime.format(date,{dateStyle:'medium'});
      divider.append(time);row.before(divider);
    }
  }
  function nearBottom(timeline) { return !timeline || timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 60; }
  life.on(document,'oie:time-zone',()=>{
    const timeline=document.querySelector('#message-timeline');
    const follow=timeline&&nearBottom(timeline)&&!pinned();
    const viewport=timeline?.getBoundingClientRect();
    const row=viewport&&[...timeline.querySelectorAll(':scope>[data-message-id],:scope>[data-pending-send]')]
      .find(node=>node.getBoundingClientRect().bottom>viewport.top);
    const top=row?.getBoundingClientRect().top;
    localizeTimes();
    if(follow)timeline.scrollTop=timeline.scrollHeight;
    else if(row)timeline.scrollTop+=row.getBoundingClientRect().top-top;
  },{capture:true});
  let highlightedMessage='',highlightTimer;
  function pinned(){const timeline=window.OieTimeline;return !!body.dataset.focusMessage||!!(timeline?.hasNewer&&!timeline.liveAttached);}
  function showJump(show){const jump=document.querySelector('[data-jump-latest]');if(jump){jump.hidden=!show;jump.textContent=pinned()?text('latest'):text('newMessages');}}
  function scrollLatest() {
    if(window.OieTimeline)window.OieTimeline.scrollLatest();
    else{const timeline=document.querySelector('#message-timeline');if(timeline)timeline.scrollTop=timeline.scrollHeight;}
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
      if(pinned()||new URLSearchParams(location.search).has('message')||new URLSearchParams(location.search).has('timelineCursor')){const url=new URL(location.href);url.searchParams.delete('message');url.searchParams.delete('timelineCursor');url.searchParams.delete('timelineDirection');window.OieNavigation.go(url);}
      else scrollLatest();
    }
  });
  life.on(document.querySelector('#message-timeline'),'scroll',()=>{if(nearBottom(document.querySelector('#message-timeline')))showJump(pinned());});
  localizeTimes();if(!body.dataset.focusMessage||!focusMessage(body.dataset.focusMessage))scrollLatest();else showJump(true);

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
    saveDraft({...draftRecord(false),attachments:detail.files});
    detail.applied = true;
    renderReply();
  });
  life.on(composer,'oie:location-draft',event=>{
    if(journal?.pending||editSession||editPreparing||stopped||composer.dataset.busy==='true'||input.disabled||input.readOnly)return;
    const {text:position} = event.detail;
    if (typeof position !== 'string') return;
    const separator = !input.value || /\s$/.test(input.value) ? '' : '\n';
    const draft = input.value + separator + position;
    if (Array.from(draft).length > 16384) return;
    input.value = draft;
    input.dispatchEvent(new Event('input', {bubbles:true}));
    event.detail.applied = true;
  });
  life.on(composer,'oie:attachment-staging',renderReply);
  life.on(composer,'oie:schedule-snapshot',event=>{
    const draft = draftRecord();
    const editBlocked=!!journal?.pending||!!editSession||editPreparing||preparingReply||stopped||composer.dataset.busy==='true'||composer.hasAttribute('data-attachments-pending');
    Object.assign(event.detail,{text:draft.text,reply:draft.reply,files:draft.attachments,review:scheduleReview(draft),
      editBlocked:editBlocked||!composer.hasAttribute('data-schedule-composer'),
      blocked:editBlocked||(composer.dataset.sendAllowed!=='true'&&!editingSchedule)});
  });
  function completeSubmission(result){
    if(!result.accepted||!canonicalID(result.messageId)||!canonicalID(result.nextKey))return false;
    const key=journal?.key;if(!journal?.pending)return false;
    const scheduled=journal.scheduledAt||editingSchedule;
    if(!scheduled)acceptPendingSend(result.messageId);
    clearDraft(key);input.value='';restoreMentions([]);submissionKey.value=result.nextKey;lockComposer(false);forceBottom=!scheduled;renderReply();
    if(scheduled&&result.message)window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',message:result.message}}));
    if(editingSchedule&&result.redirect)finishScheduleEdit(result.redirect,result.nextKey);
    return true;
  }
  async function reconcilePending(signal){
    if(editingSchedule||!journal?.pending||composer.dataset.busy==='true')return;
    const key=journal.key,values=new URLSearchParams(new FormData(composer));
    values.set('operationKey',key);values.set('originalText',journal.text);values.set('replyToMessageId',journal.reply?.messageId||'');values.set('mentions',JSON.stringify(journal.mentions||[]));setScheduledValues(values);
    try{
      const response=await life.fetch('/ui/messages/reconcile',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal});
      if(response.ok){const result=await response.json();if(!life.signal.aborted&&journal?.key===key&&composer.dataset.busy!=='true'&&result.ok)completeSubmission(result);}
    }catch{/* Keep the frozen submission and explicit retry on missing evidence. */}
  }
  function reconcileConversationList(list,fresh){
    const scroll=list.scrollTop,rect=list.getBoundingClientRect(),rows=[...list.querySelectorAll('[data-conversation-row]')];
    const anchors=rows.filter(row=>row.getBoundingClientRect().bottom>=rect.top&&row.getBoundingClientRect().top<rect.bottom).map(row=>({id:row.dataset.conversationRow,top:row.getBoundingClientRect().top}));
    const focused=list.contains(document.activeElement)?document.activeElement.closest('[data-conversation-id]')?.dataset.conversationId:'';
    const existing=list.querySelector('ul.oie-conversations'),incoming=fresh.querySelector('ul.oie-conversations');
    if(existing&&incoming){
      const known=new Map(rows.map(row=>[row.dataset.conversationRow,row])),wanted=new Set();
      for(const next of incoming.children){
        const id=next.dataset.conversationRow;if(!id)continue;wanted.add(id);let row=known.get(id);
        if(!row){row=next.cloneNode(true);mutateDOMNode(()=>{existing.append(row);window.Alpine?.initTree(row)})}
        else if(row.dataset.version!==next.dataset.version){mutateDOM(row,next.innerHTML);row.dataset.version=next.dataset.version}
        // Appending an unchanged node moves it without replacing its identity.
        if(existing.children[wanted.size-1]!==row)mutateDOMNode(()=>existing.insertBefore(row,existing.children[wanted.size-1]||null));
      }
      for(const row of rows)if(!wanted.has(row.dataset.conversationRow))mutateDOMNode(()=>{window.Alpine?.destroyTree(row);row.remove()});
    }else mutateDOM(list,fresh.innerHTML);
    const anchor=anchors.find(anchor=>list.querySelector(`[data-conversation-row="${CSS.escape(anchor.id)}"]`)),surviving=anchor&&list.querySelector(`[data-conversation-row="${CSS.escape(anchor.id)}"]`);
    list.scrollTop=surviving?scroll+surviving.getBoundingClientRect().top-anchor.top:scroll;
    if(focused){const target=list.querySelector(`[data-conversation-id="${CSS.escape(focused)}"]`);if(target)target.focus({preventScroll:true});else document.querySelector('#chat-options button')?.focus({preventScroll:true})}
  }
  function mutateDOMNode(fn){if(window.Alpine?.mutateDom)window.Alpine.mutateDom(fn);else fn()}
  async function refresh() {
    if (stopped || mock) return;
    if (pendingRequests || refreshing) { refreshAgain = true; return; }
    refreshing = true;
    const starGeneration=Number(body.dataset.starGeneration||0);
    const preferenceGeneration=Number(body.dataset.conversationPreferenceGeneration||0);
    const requestedSearch=location.search;
    const refreshQuery=new URLSearchParams(requestedSearch);
    if(!refreshQuery.has('chat')&&document.querySelector('#conversation-pane[data-open-chat="false"]')){
      if(body.dataset.conversationId)refreshQuery.set('chat',body.dataset.conversationId);
      refreshQuery.set('inbox','1');
    }
    const controller = new AbortController(); const timeout = life.timeout(() => controller.abort(), 12000);
    try {
      await reconcilePending(controller.signal);
      const reactionStartedAt=performance.now();
      const response = await life.fetch('/ui/refresh?'+refreshQuery, {credentials:'same-origin',cache:'no-store',signal:controller.signal});
      if(life.signal.aborted)return;
      if (!response.ok) throw new Error('refresh unavailable');
      const page = new DOMParser().parseFromString(await response.text(), 'text/html');
      if(life.signal.aborted)return;
      if(requestedSearch!==location.search){refreshAgain=true;return;}
      const regions = page.querySelector('#oie-live-regions');
      if (!regions || regions.dataset.unavailable === 'true') { announce(text('liveRetry')); return; }
      if (body.dataset.engineId && regions.dataset.engineId !== body.dataset.engineId) {
        stopped = true; source?.close(); renderReply();
        errorToast(text('engineChanged')); return;
      }
      body.dataset.contextNewer=regions.dataset.contextNewer||'false';body.dataset.contextMissing=regions.dataset.contextMissing||'false';
      const contextNotice=document.querySelector('#context-notice'),newNotice=page.querySelector('#live-context-notice');if(contextNotice&&newNotice&&contextNotice.innerHTML!==newNotice.innerHTML)mutateDOM(contextNotice,newNotice.innerHTML);
      if(pinned())showJump(true);
      readAvatars(page);
      const room=document.querySelector('#room-session-status'),nextRoom=page.querySelector('#live-room-session #room-session-status');
      if(room&&nextRoom){
        if(composer){
          for(const key of ['sendAllowed','composeAllowed','replyAllowed'])composer.dataset[key]=nextRoom.dataset[key]||'false';
          input.disabled=composer.dataset.composeAllowed!=='true'&&!journal?.pending;
        }
        if(room.dataset.version!==nextRoom.dataset.version&&!room.querySelector('input[name="nickname"]')?.matches(':focus')){
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
      const list = document.querySelector('#conversation-list'),nextList=page.querySelector('#live-list');
      if(preferencesCurrent&&list&&nextList&&list.dataset.version!==nextList.dataset.version){
        reconcileConversationList(list,nextList);list.dataset.version=nextList.dataset.version;
      }else if(!preferencesCurrent)refreshAgain=true;
      if (regions.dataset.conversationId === body.dataset.conversationId) {
        if (composer) {
          const allowed = regions.dataset.attachmentAllowed;
          if (allowed && allowed !== composer.dataset.attachmentAllowed) {
            composer.dataset.attachmentAllowed = allowed;
            renderReply();
          }
        }
        const pane=document.getElementById('conversation-pane');
        if(pane&&(pane.dataset.conversationRequest||'')!==(regions.dataset.conversationRequest||'')){
          const focus=pane.querySelector('[data-conversation-request-panel]')?.contains(document.activeElement);
          const route=location.pathname+location.search;
          if(!window.OieNavigation){location.reload();return;}
          await window.OieNavigation.go(route,true);
          if(focus)document.querySelector('#conversation-request-title,#message-composer')?.focus({preventScroll:true});
          return;
        }
        const timeline = document.querySelector('#message-timeline'), next = page.querySelector('#live-timeline');
        if (timeline && next && window.OieTimeline) {window.OieTimeline.refresh(page,forceBottom,reactionStartedAt);forceBottom=false;}
        const header = document.querySelector('#chat-header'), nextHeader = page.querySelector('#live-header');
        if (preferencesCurrent && header && nextHeader?.firstElementChild && header.dataset.version !== nextHeader.dataset.version && !header.contains(document.activeElement)) {
          const replacement = nextHeader.firstElementChild.cloneNode(true);
          const apply = () => { window.Alpine?.destroyTree(header); header.replaceWith(replacement); window.Alpine?.initTree(replacement); };
          if (window.Alpine?.mutateDom) window.Alpine.mutateDom(apply); else apply();
        }
      }
      if(preferencesCurrent)document.dispatchEvent(new CustomEvent('oie:conversation-preferences-refreshed',{detail:{page}}));
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
            const footer=existing.querySelector('.oie-account-detail-footer'),nextFooter=fresh.querySelector('.oie-account-detail-footer');
            const restoreFocus=controls.contains(document.activeElement)||footer?.contains(document.activeElement);
            mutateDOM(controls,nextControls.innerHTML);controls.dataset.connectionState=nextControls.dataset.connectionState;
            if(footer&&nextFooter)mutateDOM(footer,nextFooter.innerHTML);
            if(restoreFocus)(controls.querySelector('button[type="submit"]')||footer?.querySelector('button[type="submit"]')||existing.closest('#accounts-body')?.querySelector('[data-account-back]'))?.focus({preventScroll:true});
          }
        }
      }
      const schedules=document.getElementById('scheduled-results'),newSchedules=page.querySelector('#scheduled-results');
      if(schedules&&newSchedules&&!schedules.inert&&schedules.dataset.version!==newSchedules.dataset.version&&!schedules.contains(document.activeElement)&&!document.querySelector('[data-schedule-dialog][open]')){
        mutateDOM(schedules,newSchedules.innerHTML);schedules.dataset.version=newSchedules.dataset.version;
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
      if(composer&&regions.dataset.conversationId===body.dataset.conversationId){composer.dataset.latestEditMessage=regions.dataset.editMessage||'';for(const action of document.querySelectorAll('[id^="edit-message-"]'))action.hidden=action.id!=='edit-message-'+composer.dataset.latestEditMessage;renderEdit();}
      document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));document.dispatchEvent(new CustomEvent("oie:shared-refresh",{detail:page}));document.dispatchEvent(new CustomEvent('oie:reaction-snapshots',{detail:{page,startedAt:reactionStartedAt}}));applyAvatars();document.dispatchEvent(new CustomEvent("oie:stars-refresh",{detail:{page,generation:starGeneration}}));document.dispatchEvent(new CustomEvent("oie:previews-refresh",{detail:page}));localizeTimes();syncRoomLeave();
    } catch { if(!life.signal.aborted)announce(text('liveRetry')); }
    finally {
      clearTimeout(timeout);if(life.signal.aborted)return; refreshing = false;
      if (refreshAgain) { refreshAgain = false; scheduleRefresh(); }
    }
  }
  life.on(document,'oie:timeline-content',event=>{renderEditMarkers();readAvatars(event.detail.page);applyAvatars();localizeTimes();if(highlightedMessage)document.querySelector(`#message-timeline [data-message-id="${CSS.escape(highlightedMessage)}"]`)?.classList.add('oie-reply-highlight');});
  life.on(document,'oie:timeline-latest',()=>{
    clearTimeout(highlightTimer);highlightedMessage='';
    const timeline=document.getElementById('message-timeline');
    for(const node of [...acceptedBubbles,pendingBubble])if(node&&!node.isConnected){
      timeline.insertBefore(node,timeline.querySelector('[data-timeline-edge="newer"]'));window.Alpine?.initTree(node);
    }
    retireAcceptedBubbles();scrollLatest();timeline.focus({preventScroll:true});
  });
  life.on(document,'oie:timeline-focus',event=>{focusMessage(event.detail.id,event.detail.focus!==false);showJump(true);});
  life.on(document,'oie:timeline-alias',event=>{if(highlightedMessage===event.detail.requested)highlightedMessage=event.detail.canonical;});
  life.on(document,'oie:info-content',()=>{applyAvatars();localizeTimes();});
  life.on(document,'oie:accounts-content',()=>{applyAvatars();localizeTimes();});
  life.on(document,'oie:shared-account',event=>{
    const {page}=event.detail;
    const trigger=page.querySelector('[data-account-switcher]'),menu=page.querySelector('.oie-account-menu');
    if(trigger)for(const node of document.querySelectorAll('[data-account-switcher]'))mutateDOM(node,trigger.innerHTML);
    if(menu)for(const node of document.querySelectorAll('.oie-account-menu'))mutateDOM(node,menu.innerHTML);
    readAvatars(page);applyAvatars();
  });
  life.on(document,'oie:inbox-filtered',event=>localizeTimes(event.detail.root));
  life.on(document,'oie:shared-append',event=>{if(event.detail?.page)readAvatars(event.detail.page);applyAvatars();localizeTimes();});
  for(const name of ['oie:shared-search-settled','oie:info-shared-search-settled'])life.on(document,name,event=>{if(event.detail?.page)readAvatars(event.detail.page);applyAvatars();localizeTimes();});
  life.on(document,'oie:refresh-request',()=>scheduleRefresh());
  function scheduleRefresh() { if(stopped||life.signal.aborted)return;clearTimeout(refreshTimer); refreshTimer = life.timeout(refresh,100); }

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
  life.on(document,'submit', async event => {
    const form = event.target.closest('form[data-oie-action]');
    if (!form || mock) return;
    event.preventDefault();
    if (form.dataset.busy === 'true' || stopped) return;
    const sending = form.dataset.oieAction === 'send';
    const scheduleDrawer = form.dataset.oieAction === 'schedule-row' && form.closest('[data-conversation-schedules]');
    if(sending&&(editSession||editPreparing)){if(editSession&&!editSession.submitted)void submitEdit(false);return;}
    if (sending) {
      if(preparingReply){feedback(form,text('replyPreparing'));return}
      if (composer.hasAttribute('data-attachments-pending')||(form.dataset.sendAllowed !== 'true'&&!editingSchedule&&!journal?.pending)) return;
      const scheduledAt=journal?.pending?journal.scheduledAt:form.elements.namedItem('scheduledAt')?.value;
      if(!journal?.pending&&needsReplyRefresh(journal?.reply)){
        const reviewed = scheduledAt || editingSchedule ? scheduleReview(draftRecord()) : '';
        const requested = journal.reply.messageId, epoch = ++replyEpoch;
        preparingReply = true;
        renderReply();
        try{
          const reply = await fetchReplySource(requested, Boolean(reviewed));
          if (life.signal.aborted || epoch !== replyEpoch) return;
          const current = draftRecord();
          if (reviewed && reviewed !== scheduleReview(current)) {
            errorToast(scheduleReviewChanged);
            return;
          }
          const refreshed = {...current, reply};
          saveDraft(refreshed);
          if (reviewed && reviewed !== scheduleReview(refreshed)) {
            errorToast(scheduleReviewChanged);
            return;
          }
        } catch {
          if (!life.signal.aborted && epoch === replyEpoch) {
            if (reviewed) errorToast(text('replyUnavailable'));
            else feedback(form, text('replyUnavailable'), true);
          }
          return;
        }
        finally{finishReplyPreparation(epoch)}
      }
      const value = journal?.pending ? journal.text : input.value;
      if ((!value.trim() && !journal?.attachments?.length) || Array.from(value).length > 16384) { feedback(form,text('invalidMessage'),true);input.focus();return; }
      if (journal?.pending) submissionKey.value=journal.key;
      const mentions=journal?.pending?(journal.mentions||[]):readMentions();
      saveDraft({text:value,key:submissionKey.value,pending:true,reply:journal?.reply||null,mentions,attachments:journal?.attachments||[],privateMapLinks:privateMapLinks(),scheduledAt});if(!scheduledAt&&!editingSchedule)input.value='';restoreMentions(mentions);lockComposer(true);renderReply();
    }
    const values = new URLSearchParams(new FormData(form));
    const roomEntry = !!form.dataset.roomEntry;
    if (roomEntry) {
      const saved = pendingRoomEntry(form);
      if (saved) {
        for (const name of roomEntryFields) values.set(name, saved[name] || '');
      } else if (canonicalID(values.get('accountId')) && values.get('address')) {
        saveRoomEntry(form, Object.fromEntries(roomEntryFields.map(name => [name, values.get(name) || ''])));
      }
    }
    const roomCommand=form.dataset.roomCommand;
    const roomMenuSubmit=roomCommand==='leave'&&form.dataset.roomMenuSubmit==='true';
    if(roomCommand){if(roomOperation){for(const key of ['operationKey','expectedRevisionId','expectedConversationRevisionId','nickname','conversationId','accountId'])values.set(key,roomOperation[key]||'')}else keepRoomOperation({command:roomCommand,operationKey:values.get('operationKey'),expectedRevisionId:values.get('expectedRevisionId'),expectedConversationRevisionId:values.get('expectedConversationRevisionId')||'',nickname:values.get('nickname')||'',conversationId:values.get('conversationId'),accountId:values.get('accountId')})}

    if(sending){values.set('originalText',journal.text);values.set('replyToMessageId',journal.reply?.messageId||'');values.set('mentions',JSON.stringify(journal.mentions||[]));setScheduledValues(values);}
    const buttons = [...form.querySelectorAll('input:not([type=hidden]),textarea,select,button')];
    const disabled = buttons.map(button => button.disabled);
    form.dataset.busy = 'true'; form.setAttribute('aria-busy','true'); buttons.forEach(button => button.disabled = true);syncRoomLeave();
    for (const secret of form.querySelectorAll('input[type="password"]')) secret.value = '';
    feedback(form,sending?'':text('saving'));
    pendingRequests++;
    const controller = new AbortController(); const timeout = life.timeout(() => controller.abort(),15000);
    try {
      const response = await life.fetch(form.action,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal});
      const result = await response.json();
      if(life.signal.aborted)return;
      if (!result.ok) {
        if(result.code==='engine_changed'){stopped=true;source?.close();}
        if (sending && result.rejected) {
          if (pendingBubble) {
            window.Alpine?.destroyTree(pendingBubble);
            pendingBubble.remove();
            pendingBubble = null;
          }
          const mentions=result.code==='mention_unavailable'?[]:journal.mentions;
          if(result.code==='mention_unavailable')form.dispatchEvent(new Event('oie:mentions-invalidated'));
          input.value=journal.text;restoreMentions(mentions);saveDraft({...journal,mentions,pending:false});lockComposer(false);
          const message = result.code === 'reply_source_changed' ? scheduleReviewChanged : result.message || text('invalidMessage');
          if (['media_too_large','reply_source_changed'].includes(result.code)) errorToast(message);
          else feedback(form, message, true);
          if (result.code === 'reply_source_changed') void refreshRejectedQuote();
        } else {
          feedback(form, result.message || text('pending'), true);
        }
        if (sending) input.focus({preventScroll:true});
        if (roomEntry && (result.rejected || result.code === 'idempotency_conflict')) {
          saveRoomEntry(form, null);
          if (result.nextKey) form.elements.namedItem('operationKey').value = result.nextKey;
        }
        if(roomCommand&&result.rejected){keepRoomOperation(null);for(const field of form.querySelectorAll('input:not([type=hidden])'))field.readOnly=false;}
        if(scheduleDrawer&&['state_conflict','revision_conflict','not_found'].includes(result.code)){
          scheduleDrawer.dispatchEvent(new Event('oie:schedule-refresh'));
        }
        if(!sending)scheduleRefresh();
      } else {
        if (roomEntry) saveRoomEntry(form, null);
        if(roomCommand){
          keepRoomOperation(null);
          if(roomCommand==='leave')document.dispatchEvent(new CustomEvent('oie:room-reaction-visibility-withdrawn'));
        }
        feedback(form,sending ? '' : result.message || '');
        if (sending && result.accepted) {
          if(!completeSubmission(result))feedback(form,text('pending'),true);
          input.focus({preventScroll:true});renderReply();
        } else if(form.dataset.oieAction==='schedule-row'){
          if(scheduleDrawer) scheduleDrawer.dispatchEvent(new Event('oie:schedule-refresh'));
          else {
            document.dispatchEvent(new CustomEvent('oie:agenda-refresh',{detail:{form}}));
            return;
          }
        } else if (form.dataset.oieAction === 'register' || form.dataset.oieAction === 'open') {
          if (result.redirect) { if(window.OieNavigation)window.OieNavigation.go(result.redirect);else location.assign(result.redirect); return; }
        }
        const key = form.elements.namedItem('operationKey'); if (key && result.nextKey && (!sending || !journal?.pending)) key.value=result.nextKey;
        scheduleRefresh();
      }
    } catch {
      if(life.signal.aborted)return;
      feedback(form,sending ? text('pending') : text('liveRetry'),true);
      if (sending) input.focus({preventScroll:true});
    } finally {
      clearTimeout(timeout);if(life.signal.aborted)return;pendingRequests--;form.dataset.busy='false';form.removeAttribute('aria-busy');
      buttons.forEach((button,index)=>button.disabled=disabled[index]);renderReply();syncRoomLeave();
      if (roomEntry) restoreRoomEntry(form, pendingRoomEntry(form));
      if(journal?.pending)renderPendingSend(pendingBubble?.dataset.pendingState||'uncertain');
      if(sending)input.focus({preventScroll:true});
      if(roomMenuSubmit&&form.querySelector('[data-form-feedback]')?.dataset.error==='true'){
        window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'room-session-dialog'}}));
        life.raf(()=>form.querySelector('[data-form-feedback]')?.focus({preventScroll:true}));
      }
      if(stopped&&composer)composerSend.disabled=true;
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
    for (const type of ['message.preview_changed','profile.avatar_changed','account.registered','account.connection_changed','account.history_changed','conversation.created','conversation.updated','message.created']) life.on(source,type,scheduleRefresh);
    life.on(source,'message.updated',event=>{
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
    });
    life.on(source,'conversation.room_session_changed',event=>{
      try{
        const room=JSON.parse(event.data).roomSession;
        if(room?.accountId===body.dataset.accountId&&room.conversationId===body.dataset.conversationId&&room.state!=='joined'){
          document.dispatchEvent(new CustomEvent('oie:room-reaction-visibility-withdrawn'));
        }
      }catch{}
      scheduleRefresh();
    });
    life.on(source,'message.star_changed',event=>{try{document.dispatchEvent(new CustomEvent('oie:star-event',{detail:JSON.parse(event.data).star}));}catch{}scheduleRefresh();});
    life.on(source,'oie.reset', event => {
      try {
        const reset=JSON.parse(event.data);
        if (body.dataset.engineId && reset.engineId !== body.dataset.engineId) {stopped=true;source.close();errorToast(text('engineChanged'));return;}
        scheduleRefresh();connect(reset.cursor);
      }catch{announce(text('liveRetry'))}
    });
  }
  connect(body.dataset.eventCursor);
  // Bounded recovery polling also covers a proxy that buffers or disables SSE.
  life.interval(() => { if (!mock && !document.hidden) {scheduleRefresh();retryPending();} },15000);
  life.on(window,'online', () => {scheduleRefresh();retryPending();});
  const open = new URLSearchParams(location.search).get('open') || body.dataset.openDrawer;
  if (open === 'sender' || open === 'accounts' || open === 'newchat' || open === 'details' || open === 'settings') {
    const show = () => {
      dispatchEvent(new CustomEvent('drawer:open',{detail:{id:open}}));
      if(open==='settings'&&new URLSearchParams(location.search).get('settingsFocus')!=='language')life.timeout(()=>document.querySelector('#settings-body input[name="theme"]:checked')?.focus({preventScroll:true}),230);
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
