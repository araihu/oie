window.OieRuntime.register('reactions',life=>{
 'use strict';
 const picker=document.querySelector('[data-reaction-picker]'),quick=document.getElementById('oie-reaction-quick'),catalog=document.getElementById('oie-reaction-picker');if(!picker||!quick||!catalog)return;
 const more=quick.querySelector('[data-reaction-more]'),choices=[...quick.querySelectorAll('[data-reaction-quick-emoji]')];
 const body=document.body,scope={engineId:body.dataset.engineId,accountId:body.dataset.accountId,conversationId:body.dataset.conversationId},labels=picker.dataset,csrf=document.querySelector('meta[name="oie-csrf"]')?.content||'';
 const room=!!document.getElementById('room-session-status'),roomRevisions=new Map(),roomSnapshotTimes=new Map();
 let latestRoomRefresh=0,withdrawnAt=0;
 const memory=window.OieRuntime.memory,key='reaction-journals:'+Object.values(scope).join(':'),journals=memory.get(key)||new Map();memory.set(key,journals);
 const filters=new Map(),snapshots=new Map(),busy=new Set(),polls=new Map(),stored=new Set(),prefix='oie.reaction.v1:'+Object.values(scope).join(':')+':';
 const uuid=v=>typeof v==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v),sameSet=(a,b)=>Array.isArray(a)&&Array.isArray(b)&&a.length===b.length&&a.every(e=>b.includes(e));
 let preparation=null,opener=null,openerID='',preparing=null;
 const activeSnapshot=()=>preparation&&snapshots.get(preparation.messageId);
 const allowed=glyph=>{const s=activeSnapshot();return !!s&&(!s.allowedEmojis||s.allowedEmojis.includes(glyph))};
 const selected=glyph=>preparation?.emojis.includes(glyph)===true;
 const enabled=glyph=>{const s=activeSnapshot();return !!s&&(s.maxEmojis===1||selected(glyph)||preparation.emojis.length<s.maxEmojis)};
 const selector=window.OieEmojiPicker.mount(catalog,life,{
  available:()=>!quick.hidden&&activeSnapshot()?.eligible===true,allowed,selected,enabled,
  choose:glyph=>!!preparation&&!quick.hidden&&choose(preparation,glyph)
 });
 function notify(message){if(message)window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message}}))}
 function validSnapshot(s,id){return s&&s.messageId===id&&s.accountId===scope.accountId&&s.conversationId===scope.conversationId&&Array.isArray(s.items)&&Array.isArray(s.ownEmojis)&&s.ownEmojis.length<=8&&s.ownEmojis.every(e=>typeof e==='string')&&(!s.ownRevisionId||uuid(s.ownRevisionId))&&(!s.revisionId||uuid(s.revisionId))&&Number.isInteger(s.maxEmojis)&&s.maxEmojis>=1&&s.maxEmojis<=8&&s.items.every(i=>typeof i.emoji==='string'&&i.emoji&&Number.isSafeInteger(i.count)&&i.count>0&&typeof i.own==='boolean')&&(!s.allowedEmojis||Array.isArray(s.allowedEmojis)&&s.allowedEmojis.length<=256)&&validActors(s)}
 function validActors(s){
  if(s.actors===undefined)return true;if(!Array.isArray(s.actors))return false;const seen=new Set(),counts=new Map();let own=0;
  for(const a of s.actors){if(!a||typeof a.address!=='string'||!a.address||a.address.length>1023||/[\p{Cc}]/u.test(a.address)||seen.has(a.address)||typeof a.displayName!=='string'||!a.displayName.trim()||a.displayName.length>1023||/[\p{Cc}]/u.test(a.displayName)||typeof a.own!=='boolean'||!Array.isArray(a.emojis)||!a.emojis.length||a.emojis.length>8||a.emojis.some(e=>typeof e!=='string'||!e)||new Set(a.emojis).size!==a.emojis.length||a.avatarHash!==undefined&&!/^[0-9a-f]{64}$/.test(a.avatarHash))return false;seen.add(a.address);if(a.own){own++;if(!sameSet(a.emojis,s.ownEmojis))return false}for(const e of a.emojis)counts.set(e,(counts.get(e)||0)+1)}
  return own<=1&&(s.ownEmojis.length>0)===(own===1)&&counts.size===s.items.length&&s.items.every(item=>counts.get(item.emoji)===item.count);
 }
 function validIntent(v,id){return v&&Object.entries(scope).every(([k,x])=>v[k]===x)&&v.messageId===id&&uuid(v.operationKey)&&(!v.expectedOwnRevisionId||uuid(v.expectedOwnRevisionId))&&Array.isArray(v.emojis)&&v.emojis.length<=8&&v.emojis.every(e=>typeof e==='string'&&e.length<=128)}
 function matches(c,intent){return c&&c.id===intent.operationKey&&c.messageId===intent.messageId&&c.accountId===intent.accountId&&c.conversationId===intent.conversationId&&(c.expectedOwnRevisionId||'')===(intent.expectedOwnRevisionId||'')&&sameSet(c.emojis,intent.emojis)&&uuid(c.revisionId)&&['queued','dispatching','submitted','unknown','failed'].includes(c.state)}
 function save(intent){journals.set(intent.messageId,intent);stored.add(intent.messageId);try{localStorage.setItem(prefix+intent.messageId,JSON.stringify(intent))}catch{}render()}
 function clear(id){journals.delete(id);clearTimeout(polls.get(id));polls.delete(id);try{localStorage.removeItem(prefix+id)}catch{}}
 function restore(id){if(stored.has(id))return;stored.add(id);try{const saved=JSON.parse(localStorage.getItem(prefix+id)||'null');if(validIntent(saved,id))journals.set(id,saved)}catch{}}
 function commandIntent(command){return {...scope,messageId:command.messageId,operationKey:command.id,expectedOwnRevisionId:command.expectedOwnRevisionId||'',emojis:command.emojis,command}}
 const roomState=()=>document.getElementById('room-session-status')?.dataset.roomState;
 const publicActors=s=>s?.actors?.some(actor=>!actor.address.startsWith('occupant:'))===true;
 function publishRoomSnapshot(id,s,previous,valid){
  if(!valid||s?.eligible!==previous?.eligible||publicActors(s)!==publicActors(previous)){
   roomRevisions.set(id,(roomRevisions.get(id)||0)+1);
  }
  // A refresh can retain the active row or omit rows from older pages.
  const live=document.querySelector('[data-reaction-source="'+CSS.escape(id)+'"]');
  if(live)live.dataset.reactionState=JSON.stringify(s??null);
  if(s)snapshots.set(id,s);
 }
 function read(root,startedAt=performance.now(),liveRefresh=false){
  if(room&&liveRefresh){
   latestRoomRefresh=Math.max(latestRoomRefresh,startedAt);
   if(withdrawnAt&&startedAt>=withdrawnAt&&roomState()==='joined')withdrawnAt=0;
  }
  const withdrawn=room&&(withdrawnAt!==0||roomState()!=='joined');
  if(withdrawn){
   for(const [id,previous] of snapshots){
    if(previous.eligible||publicActors(previous)){
     const s={...previous,actors:publicActors(previous)?undefined:previous.actors,eligible:false};
     publishRoomSnapshot(id,s,previous,false);
    }
   }
  }
  for(const region of root.querySelectorAll('[data-reaction-source][data-reaction-state]')){
   const id=region.dataset.reactionSource;
   if(!uuid(id))continue;
   let incoming;
   try{incoming=JSON.parse(region.dataset.reactionState)}catch{}
   const valid=validSnapshot(incoming,id),previous=snapshots.get(id);
   if(!room&&!valid)continue;
   let s=valid?incoming:previous;
   if(room){
    const previousAt=roomSnapshotTimes.get(id)||0;
    const visibilityAt=Math.max(latestRoomRefresh,withdrawnAt);
    const superseded=startedAt<Math.max(visibilityAt,previousAt);
    if(superseded){
     // Late pages may mount messages, but their reaction rosters cannot
     // replace fresher data or expose previously public profiles.
     s=previous||s;
     if(s&&previousAt<visibilityAt)s={...s,actors:publicActors(s)?undefined:s.actors,eligible:false};
    }else if(valid)roomSnapshotTimes.set(id,startedAt);
    const missing=!valid&&!superseded;
    if(s&&(missing||withdrawn))s={...s,actors:missing||publicActors(s)?undefined:s.actors,eligible:false};
    publishRoomSnapshot(id,s,previous,!missing);
   }else snapshots.set(id,s);
   if(!s)continue;
   restore(id);
   if(s.pendingCommand){
    const intent=commandIntent(s.pendingCommand);
    if(validIntent(intent,id)&&matches(s.pendingCommand,intent)&&!journals.has(id))save(intent);
   }
  }
  render();
  if(preparation&&!activeSnapshot()?.eligible)close(false);else positionQuick();
 }
 function chipLabel(item){return item.emoji+' · '+item.count+(item.own?' · '+labels.remove:'')}
 function detailFilter(id,emoji,count,selected){const button=picker.querySelector('[data-reaction-filter-template]').content.firstElementChild.cloneNode(true);button.dataset.reactionFilter=emoji;button.dataset.reactionMessage=id;button.setAttribute('aria-pressed',String(selected));button.querySelector('span').textContent=(emoji||labels.all)+' · '+count;return button}
 function reactionRows(s,id,selected,waiting){
  const fragment=document.createDocumentFragment();
  if(s.actors!==undefined){
   for(const actor of s.actors){const emojis=actor.emojis.filter(emoji=>!selected||emoji===selected);if(!emojis.length)continue;const row=picker.querySelector('[data-reaction-actor-template]').content.firstElementChild.cloneNode(true);row.dataset.reactionActor=actor.address;row.dataset.initials=actor.displayName.trim().split(/\s+/).slice(0,2).map(part=>Array.from(part)[0]).join('').toLocaleUpperCase(document.documentElement.lang);row.querySelector('[data-reaction-actor-name]').textContent=actor.own?labels.you:actor.displayName;row.querySelector('[data-reaction-actor-address]').textContent=!actor.address.startsWith('occupant:')&&(actor.displayName!==actor.address||actor.own)?actor.address:'';row.querySelector('[data-src]').dataset.src=actor.avatarHash?'/ui/avatars/'+actor.avatarHash:'';
    const choices=row.querySelector('[data-reaction-actor-emojis]');for(const emoji of emojis){let glyph;if(actor.own){glyph=picker.querySelector('[data-reaction-own-emoji-template]').content.firstElementChild.cloneNode(true);glyph.dataset.reactionEmoji=emoji;glyph.dataset.reactionMessage=id;glyph.disabled=!s.eligible||waiting;glyph.setAttribute('aria-label',labels.remove+' '+emoji);glyph.title=labels.remove+' '+emoji;glyph.querySelector('[data-reaction-remove-glyph]').textContent=emoji}else{glyph=document.createElement('span');glyph.textContent=emoji}glyph.dataset.reactionDetailEmoji=emoji;choices.append(glyph)}fragment.append(row)
   }
  }else{
for(const item of s.items){if(selected&&selected!==item.emoji)continue;const row=document.createElement('div');row.className='oie-reaction-detail-row';row.dataset.reactionDetailEmoji=item.emoji;const glyph=document.createElement('span');glyph.className='oie-reaction-detail-emoji';glyph.textContent=item.emoji;const count=document.createElement('span');count.textContent=String(item.count);row.append(glyph,count);if(item.own){const own=document.createElement('small');own.textContent=labels.you;row.append(own);const remove=picker.querySelector('[data-reaction-remove-template]').content.firstElementChild.cloneNode(true);remove.dataset.reactionEmoji=item.emoji;remove.dataset.reactionMessage=id;remove.disabled=!s.eligible||waiting;remove.setAttribute('aria-label',labels.remove+' '+item.emoji);row.append(remove)}fragment.append(row)}
  }
  return fragment;
 }
 function render(){
  for(const region of document.querySelectorAll('[data-reaction-source]')){
   const id=region.dataset.reactionSource,s=snapshots.get(id);if(!s)continue;restore(id);const pending=journals.get(id),selected=s.items.some(item=>item.emoji===filters.get(id))?filters.get(id):'',active=region.contains(document.activeElement)?document.activeElement:null;
   const activeEmoji=active?.dataset.reactionEmoji,activeFilter=active?.dataset.reactionFilter;
   const signature=JSON.stringify([s.items,s.actors,s.eligible,pending?.operationKey,pending?.command?.state,busy.has(id),selected]);if(region.dataset.reactionRendered===signature)continue;
   const summary=region.querySelector('[data-reaction-summary]'),total=s.items.reduce((sum,item)=>sum+item.count,0),details=region.querySelector('[data-reaction-details]');
   const closingEmpty=total===0&&summary.getAttribute('aria-expanded')==='true';
   if(closingEmpty)summary.click();
   summary.hidden=total===0;summary.querySelector('[data-reaction-summary-emojis]').textContent=s.items.slice(0,3).map(item=>item.emoji).join(' ');summary.querySelector('[data-reaction-summary-count]').textContent=String(total);summary.setAttribute('aria-label',labels.reactSummary+' · '+total);
   details.querySelector('[data-reaction-total]').textContent=String(total);const filterRow=details.querySelector('[data-reaction-filters]'),rows=details.querySelector('[data-reaction-detail-list]');
   filterRow.replaceChildren(detailFilter(id,'',total,!selected),...s.items.map(item=>detailFilter(id,item.emoji,item.count,selected===item.emoji)));
   rows.replaceChildren(reactionRows(s,id,selected,!!pending||busy.has(id)));
   details.querySelector('[data-reaction-open]').disabled=!s.eligible||!!pending||busy.has(id);
   const pendingSlot=region.querySelector('[data-reaction-pending]');pendingSlot.replaceChildren();
   if(pending){const button=picker.querySelector('[data-reaction-status-template]').content.firstElementChild.cloneNode(true),state=pending.command?.state,waiting=busy.has(id)||['queued','dispatching'].includes(state);button.dataset.reactionMessage=id;button.disabled=busy.has(id);const actionLabel=waiting?labels.pending:!pending.command&&pending.missingReceipt?labels.retry:labels.check;button.setAttribute('aria-label',actionLabel);button.title=actionLabel;button.setAttribute('aria-busy',String(waiting));button.querySelector('[data-reaction-check-icon]').hidden=waiting;button.querySelector('[data-reaction-status-spinner]').hidden=!waiting;pendingSlot.append(button)}
   region.dataset.reactionRendered=signature;
   if(closingEmpty&&active){
    region.closest('[data-message-bubble]')?.querySelector('[data-message-menu-trigger]')?.focus({preventScroll:true});
   }else if(active&&!active.isConnected){
    const replacement=activeFilter!==undefined?[...filterRow.querySelectorAll('[data-reaction-filter]')].find(button=>button.dataset.reactionFilter===activeFilter):activeEmoji?[...rows.querySelectorAll('[data-reaction-emoji]')].find(button=>button.dataset.reactionEmoji===activeEmoji):pendingSlot.querySelector('[data-reaction-check]');
    (replacement||summary)?.focus({preventScroll:true});
   }
  }
  for(const button of document.querySelectorAll('[data-reaction-open]')){const s=snapshots.get(button.dataset.reactionOpen),pending=journals.get(button.dataset.reactionOpen),slot=button.closest('[data-reaction-shortcut-slot]');if(slot)slot.hidden=!s||(!s.eligible&&!pending);if(slot)button.setAttribute('aria-busy',String(busy.has(button.dataset.reactionOpen)))}
  for(const button of document.querySelectorAll('[id^="react-message-"]')){const id=button.id.slice(14),s=snapshots.get(id),pending=journals.get(id);button.disabled=!s||(!s.eligible&&!pending)||busy.has(id);button.setAttribute('aria-label',pending?labels.check:labels.react);button.title=pending?labels.check:labels.react;let text=button.querySelector('[data-reaction-menu-label]');if(!text){text=document.createElement('span');text.dataset.reactionMenuLabel='';const icon=button.querySelector('svg');button.replaceChildren(...(icon?[icon]:[]),text)}text.textContent=pending?labels.check:labels.react}
 }
 function refresh(){document.dispatchEvent(new CustomEvent('oie:refresh-request'))}
 function setCurrent(snapshot){snapshots.set(snapshot.messageId,snapshot);render()}
 function planPoll(intent){clearTimeout(polls.get(intent.messageId));if(['queued','dispatching'].includes(intent.command?.state))polls.set(intent.messageId,life.timeout(()=>submit(intent,true),1200))}
 function accept(result,intent){
  if(result.accepted&&matches(result.command,intent)){intent={...intent,command:result.command};if(['submitted','failed'].includes(result.command.state)){clear(intent.messageId);render();refresh();if(result.command.state==='failed')notify(labels.failed)}else{save(intent);planPoll(intent);if(result.command.state==='unknown')notify(labels.unknown)}return true}
  if(result.rejected===true){clear(intent.messageId);render();notify(result.message||labels.unavailable);refresh();return true}return false;
 }
 async function submit(intent,receiptOnly=false){
  if(!validIntent(intent,intent.messageId)||busy.has(intent.messageId))return;save(intent);busy.add(intent.messageId);render();const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
  try{const values=new URLSearchParams({...scope,messageId:intent.messageId,operationKey:intent.operationKey,expectedOwnRevisionId:intent.expectedOwnRevisionId||'',emojis:JSON.stringify(intent.emojis),csrf,lang:document.documentElement.lang});const response=await life.fetch(receiptOnly?'/ui/messages/reaction-receipt':'/ui/messages/reactions',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal}),result=await response.json();if(life.signal.aborted||journals.get(intent.messageId)?.operationKey!==intent.operationKey)return;if(!accept(result,intent)){save({...intent,unconfirmed:true,missingReceipt:receiptOnly&&response.status===404});notify(result.message||labels.unknown)}}
  catch{if(!life.signal.aborted&&journals.get(intent.messageId)?.operationKey===intent.operationKey){save({...intent,unconfirmed:true});notify(labels.unknown)}}
  finally{clearTimeout(timeout);if(!life.signal.aborted){busy.delete(intent.messageId);render()}}
 }
 async function prepare(id,element,emoji){
  if(!uuid(id)||busy.has(id)||body.dataset.mock==='true')return;if(journals.has(id)){void submit(journals.get(id),true);return}
  const opening=!emoji,controller=new AbortController();
  const roomRevision=roomRevisions.get(id),presence=roomState();
  if(opening){
   cancelPreparation();
   preparing=controller;
   opener=element;
   openerID=id;
  }
  const timeout=life.timeout(()=>controller.abort(),8000);
  busy.add(id);
  render();
  try{
   const url=new URL('/ui/message-reactions',location.origin);
   for(const[k,v]of Object.entries({...scope,messageId:id,lang:document.documentElement.lang}))url.searchParams.set(k,v);
   const response=await life.fetch(url,{credentials:'same-origin',cache:'no-store',headers:{Accept:'application/json'},signal:controller.signal}),value=await response.json();
   if(life.signal.aborted||controller.signal.aborted||room&&(roomRevision!==roomRevisions.get(id)||presence!==roomState()))return;
   if(!response.ok||value.engineId!==scope.engineId||!uuid(value.operationKey)||!validSnapshot(value.snapshot,id))throw Error('unavailable');
   if(room){
    // Preflight authorizes the action. Actor details come from the timeline
    // refresh so a delayed preflight cannot reintroduce a public profile.
    const prior=snapshots.get(id);
    if(prior){
     const {eligible,allowedEmojis,maxEmojis}=value.snapshot;
     setCurrent({...prior,eligible,allowedEmojis,maxEmojis});
     if(!sameSet(prior.ownEmojis,value.snapshot.ownEmojis))refresh();
    }
   }else setCurrent(value.snapshot);
   if(value.snapshot.pendingCommand){
    const intent=commandIntent(value.snapshot.pendingCommand);
    if(!validIntent(intent,id)||!matches(intent.command,intent))throw Error('pending');
    save(intent);
    busy.delete(id);
    void submit(intent,true);
    return;
   }
   if(!value.snapshot.eligible)throw Error('ineligible');
   const next={...scope,messageId:id,expectedOwnRevisionId:value.snapshot.ownRevisionId||'',operationKey:value.operationKey,emojis:value.snapshot.ownEmojis};
   if(opening)openQuick(next);
   else if(next.emojis.includes(emoji)){
    busy.delete(id);
    void submit({...next,emojis:next.emojis.filter(glyph=>glyph!==emoji)},false);
   }
  }catch{
   if(!life.signal.aborted&&!controller.signal.aborted)notify(labels.unavailable);
  }finally{
   clearTimeout(timeout);
   if(preparing===controller)preparing=null;
   if(!journals.has(id))busy.delete(id);
   if(!life.signal.aborted)render();
  }
 }
 function positionQuick(){
  if(quick.hidden)return;
  const bubble=document.querySelector('[data-message-bubble="'+CSS.escape(openerID)+'"]');
  const timeline=document.getElementById('message-timeline');
  if(!bubble||!timeline){close(false);return}
  const source=bubble.getBoundingClientRect(),pane=timeline.getBoundingClientRect();
  const anchor=bubble.querySelector('.oie-reaction-shortcut')?.getBoundingClientRect();
  const rect=anchor?.width?anchor:source,viewport=window.visualViewport;
  const offsetLeft=viewport?.offsetLeft||0,offsetTop=viewport?.offsetTop||0;
  const viewWidth=viewport?.width||document.documentElement.clientWidth,viewHeight=viewport?.height||innerHeight;
  const left=offsetLeft+8,right=offsetLeft+viewWidth-8;
  const catalogFocused=more.getAttribute('aria-expanded')==='true'&&catalog.contains(document.activeElement);
  const reducedViewport=viewHeight<innerHeight||viewWidth<document.documentElement.clientWidth;
  const keepCatalog=catalogFocused&&reducedViewport;
  const top=(keepCatalog?offsetTop:Math.max(pane.top,offsetTop))+6;
  const bottom=(keepCatalog?offsetTop+viewHeight:Math.min(pane.bottom,offsetTop+viewHeight))-6;
  if(!keepCatalog&&(source.bottom<=top||source.top>=bottom)){close(false);return}
  quick.style.setProperty('--oie-reaction-available-width',Math.max(0,right-left)+'px');
  const width=quick.offsetWidth,height=quick.offsetHeight,own=!!bubble.closest('.oie-message-own');
  const x=own?rect.left-6:rect.right-width+6;
  const y=rect.top-height-6>=top?rect.top-height-6:rect.bottom+6;
  quick.style.left=Math.max(left,Math.min(x,right-width))+'px';
  quick.style.top=Math.max(top,Math.min(y,bottom-height))+'px';
 }
 function openQuick(intent){
  preparation=intent;
  for(const button of choices){
   const glyph=button.dataset.reactionQuickEmoji;
   button.hidden=!allowed(glyph);
   button.disabled=!enabled(glyph);
   button.setAttribute('aria-pressed',String(selected(glyph)));
  }
  quick.hidden=false;
  quick.showPopover?.();
  positionQuick();
  if(quick.hidden)return;
  if(opener?.matches('[data-reaction-open]'))opener.setAttribute('aria-expanded','true');
  (choices.find(button=>!button.hidden&&!button.disabled)||more).focus({preventScroll:true});
 }
 function close(restoreFocus=true){
  if(quick.hidden)return;
  const source=opener,id=openerID;
  if(more.getAttribute('aria-expanded')==='true')more.click();
  selector.close();
  if(quick.isConnected)quick.hidePopover?.();
  quick.hidden=true;
  preparation=null;
  if(source?.matches('[data-reaction-open]'))source.setAttribute('aria-expanded','false');
  if(!restoreFocus)return;
  const root=document.querySelector('[data-message-bubble="'+CSS.escape(id)+'"]');
  const control=source?.matches('.oie-reaction-shortcut')&&source.isConnected&&source.getClientRects().length
   ?source:root?.querySelector('[data-message-menu-trigger]');
  control?.focus({preventScroll:true});
 }
 function choose(intent,emoji){
  const s=snapshots.get(intent.messageId);if(!s||!s.eligible||s.allowedEmojis&&!s.allowedEmojis.includes(emoji))return false;
  const emojis=intent.emojis.includes(emoji)?intent.emojis.filter(e=>e!==emoji):s.maxEmojis===1?[emoji]:[...intent.emojis,emoji];if(emojis.length>s.maxEmojis){notify(labels.unavailable);return false}
  close();void submit({...intent,emojis},false);return true;
 }
 const catalogObserver=new MutationObserver(()=>{
  if(!quick.hidden&&more.getAttribute('aria-expanded')==='true'){
   selector.open(true);
   selector.focusSearch(more);
  }else selector.close();
 });
 catalogObserver.observe(more,{attributes:true,attributeFilter:['aria-expanded']});
 life.cleanup(()=>{catalogObserver.disconnect();close(false)});
 life.on(quick,'click',event=>{
  const button=event.target.closest('[data-reaction-quick-emoji]');
  if(!button||button.disabled||button.hidden||!preparation)return;
  const glyph=button.dataset.reactionQuickEmoji;
  if(choose(preparation,glyph))selector.remember(glyph);
 });
 life.on(quick,'keydown',event=>{
  if(!['ArrowLeft','ArrowRight'].includes(event.key)||catalog.contains(event.target)&&event.target!==more)return;
  const buttons=[...choices.filter(button=>!button.hidden&&!button.disabled),more];
  const current=buttons.indexOf(event.target);if(current<0)return;
  event.preventDefault();
  buttons[(current+buttons.length+(event.key==='ArrowRight'?1:-1))%buttons.length].focus({preventScroll:true});
 });
 function cancelPreparation(){preparing?.abort();preparing=null}
 life.on(document,'keydown',event=>{
  if(event.key!=='Escape')return;
  cancelPreparation();
  if(quick.hidden||more.getAttribute('aria-expanded')==='true')return;
  event.preventDefault();
  close();
 });
 life.on(document,'pointerdown',event=>{
  if(quick.contains(event.target)||opener?.contains(event.target))return;
  cancelPreparation();
  close(false);
 },{capture:true});
 life.on(document,'focusin',event=>{
  if(!quick.hidden&&!quick.contains(event.target)&&event.target!==opener)close(false);
 });
 life.on(document,'scroll',event=>{if(!quick.contains(event.target))positionQuick()},{capture:true,passive:true});
 life.on(window,'resize',positionQuick);
 life.on(window.visualViewport,'resize',positionQuick);
 life.on(window.visualViewport,'scroll',positionQuick);
 life.on(document,'click',event=>{
  const filter=event.target.closest('[data-reaction-filter]');if(filter){event.preventDefault();filters.set(filter.dataset.reactionMessage,filter.dataset.reactionFilter);render();return}
  const direct=event.target.closest('[data-reaction-open]'),menu=event.target.closest('[id^="react-message-"]'),chip=event.target.closest('[data-reaction-emoji][data-reaction-message]'),check=event.target.closest('[data-reaction-check]');if(!direct&&!menu&&!chip&&!check)return;const button=direct||menu||chip||check;if(button.disabled)return;event.preventDefault();if(!direct||button.closest('[data-reaction-details]'))button.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));const id=direct?direct.dataset.reactionOpen:menu?menu.id.slice(14):button.dataset.reactionMessage;if(check){const intent=journals.get(id);if(intent)void submit(intent,!!intent.command||!intent.missingReceipt)}else if(!quick.hidden&&button===opener)close();else{close(false);void prepare(id,button,chip?.dataset.reactionEmoji)}
 });
 // Touch gestures compose the same scoped, read-only preparation and public
 // Dropdown trigger as keyboard/menu use. Scrolling and native controls win.
 let gesture=null,lastTap=null,suppressClick=null,delayedPrimary=null;
 const nativeTarget=target=>target.closest('a,button,input,textarea,select,video,audio,[contenteditable],[role=button],[data-message-menu-slot]');
 function cancelGesture(){if(gesture)clearTimeout(gesture.timer);gesture=null}
 function cancelPrimary(){if(delayedPrimary)clearTimeout(delayedPrimary.timer);delayedPrimary=null}
 function openMessageMenu(source){const trigger=document.querySelector('[data-message-menu-trigger="'+CSS.escape(source.dataset.messageBubble)+'"]');if(trigger&&!trigger.disabled&&trigger.getAttribute('aria-expanded')!=='true')trigger.click()}
 life.on(document,'pointerdown',event=>{
  suppressClick=null;cancelPrimary();if(!['touch','pen'].includes(event.pointerType))return;cancelGesture();const primary=event.target.closest('[data-preview-expand],[data-document-open]');if(event.isPrimary===false||event.button!==0||event.target.closest('a,input,textarea,select,video,audio,[contenteditable]')||nativeTarget(event.target)&&(!primary||primary.disabled)){lastTap=null;return}const source=event.target.closest('[data-message-bubble]');if(!source)return;
  const candidate={id:event.pointerId,source,x:event.clientX,y:event.clientY,time:performance.now(),held:false,primary};gesture=candidate;
  candidate.timer=life.timeout(()=>{if(gesture!==candidate||!source.isConnected)return;candidate.held=true;lastTap=null;suppressClick={source,until:performance.now()+900};openMessageMenu(source)},500)
 },{passive:true});
 life.on(document,'pointermove',event=>{if(gesture&&gesture.id===event.pointerId&&Math.hypot(event.clientX-gesture.x,event.clientY-gesture.y)>10){cancelGesture();lastTap=null}},{passive:true});
 life.on(document,'pointerup',event=>{
  if(!gesture||gesture.id!==event.pointerId)return;const candidate=gesture;cancelGesture();if(candidate.held||!candidate.source.isConnected)return;const now=performance.now();if(now-candidate.time>450){lastTap=null;return}
  if(lastTap&&lastTap.source===candidate.source&&now-lastTap.time<=320&&Math.hypot(candidate.x-lastTap.x,candidate.y-lastTap.y)<=24){event.preventDefault();lastTap=null;suppressClick={source:candidate.source,until:now+700};close(false);void prepare(candidate.source.dataset.messageBubble,candidate.source)}else lastTap={...candidate,time:now}
 },{passive:false});
 life.on(document,'pointercancel',()=>{cancelGesture();cancelPrimary();lastTap=null},{passive:true});
 life.on(document,'scroll',()=>{cancelGesture();cancelPrimary();lastTap=null},{capture:true,passive:true});
 life.on(document,'contextmenu',event=>{if(gesture&&gesture.source.contains(event.target)){event.preventDefault();gesture.held=true;clearTimeout(gesture.timer);lastTap=null;suppressClick={source:gesture.source,until:performance.now()+900};openMessageMenu(gesture.source)}else if(suppressClick&&performance.now()<suppressClick.until&&suppressClick.source.contains(event.target))event.preventDefault()});
 life.on(window,'click',event=>{
  if(suppressClick&&performance.now()<suppressClick.until&&suppressClick.source.contains(event.target)&&event.isTrusted){event.preventDefault();event.stopImmediatePropagation();suppressClick=null;return}
  const primary=event.target.closest('[data-preview-expand],[data-document-open]');if(event.isTrusted&&primary&&lastTap?.primary===primary&&performance.now()-lastTap.time<250){event.preventDefault();event.stopImmediatePropagation();cancelPrimary();const next={primary,timer:0};delayedPrimary=next;next.timer=life.timeout(()=>{if(delayedPrimary!==next)return;delayedPrimary=null;lastTap=null;if(primary.isConnected&&!primary.disabled)primary.click()},320)}
 },{capture:true});
 life.on(document,'visibilitychange',()=>{if(document.hidden){cancelGesture();cancelPrimary();lastTap=null}});
 life.on(document,'keydown',event=>{if(event.key==='Escape'){cancelGesture();cancelPrimary();lastTap=null}});
 life.cleanup(()=>{cancelGesture();cancelPrimary();lastTap=null;suppressClick=null});
 life.on(document,'oie:timeline-content',event=>read(event.detail.page,event.detail.startedAt));
 life.on(document,'oie:reaction-snapshots',event=>read(event.detail.page,event.detail.startedAt,true));
 life.on(document,'oie:room-reaction-visibility-withdrawn',()=>{if(room){withdrawnAt=performance.now();read(document,withdrawnAt)}});
 life.on(document,'oie:actions-refreshed',render);
 life.cleanup(()=>{cancelPreparation();for(const timer of polls.values())clearTimeout(timer)});
 read(document);for(const intent of journals.values())if(validIntent(intent,intent.messageId))life.timeout(()=>submit(intent,true),0);
});
