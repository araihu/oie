window.OieRuntime.register('reactions',life=>{
 'use strict';
 const picker=document.querySelector('[data-reaction-picker]'),dialog=document.getElementById('oie-reaction-picker');if(!picker||!dialog)return;
 const body=document.body,scope={engineId:body.dataset.engineId,accountId:body.dataset.accountId,conversationId:body.dataset.conversationId},labels=picker.dataset,csrf=document.querySelector('meta[name="oie-csrf"]')?.content||'',search=picker.querySelector('#reaction-emoji-search'),grid=picker.querySelector('#reaction-emoji-grid'),status=picker.querySelector('[data-reaction-picker-status]'),retry=picker.querySelector('[data-reaction-catalog-retry]');
 const memory=window.OieRuntime.memory,key='reaction-journals:'+Object.values(scope).join(':'),journals=memory.get(key)||new Map();memory.set(key,journals);
 const snapshots=new Map(),busy=new Set(),polls=new Map(),stored=new Set(),prefix='oie.reaction.v1:'+Object.values(scope).join(':')+':';
 const uuid=v=>typeof v==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v),sameSet=(a,b)=>Array.isArray(a)&&Array.isArray(b)&&a.length===b.length&&a.every(e=>b.includes(e));
 const fold=v=>v.normalize('NFD').replace(/\p{M}/gu,'').toLocaleLowerCase(document.documentElement.lang).trim();
 let preparation=null,opener=null,openerID='',prepareEpoch=0,preparing=null,catalog=null,loading=false;
 function notify(message){if(message)window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message}}))}
 function validSnapshot(s,id){return s&&s.messageId===id&&s.accountId===scope.accountId&&s.conversationId===scope.conversationId&&Array.isArray(s.items)&&Array.isArray(s.ownEmojis)&&s.ownEmojis.length<=8&&s.ownEmojis.every(e=>typeof e==='string')&&(!s.ownRevisionId||uuid(s.ownRevisionId))&&(!s.revisionId||uuid(s.revisionId))&&Number.isInteger(s.maxEmojis)&&s.maxEmojis>=1&&s.maxEmojis<=8&&s.items.every(i=>typeof i.emoji==='string'&&i.emoji&&Number.isSafeInteger(i.count)&&i.count>0&&typeof i.own==='boolean')&&(!s.allowedEmojis||Array.isArray(s.allowedEmojis)&&s.allowedEmojis.length<=256)}
 function validIntent(v,id){return v&&Object.entries(scope).every(([k,x])=>v[k]===x)&&v.messageId===id&&uuid(v.operationKey)&&(!v.expectedOwnRevisionId||uuid(v.expectedOwnRevisionId))&&Array.isArray(v.emojis)&&v.emojis.length<=8&&v.emojis.every(e=>typeof e==='string'&&e.length<=128)}
 function matches(c,intent){return c&&c.id===intent.operationKey&&c.messageId===intent.messageId&&c.accountId===intent.accountId&&c.conversationId===intent.conversationId&&(c.expectedOwnRevisionId||'')===(intent.expectedOwnRevisionId||'')&&sameSet(c.emojis,intent.emojis)&&uuid(c.revisionId)&&['queued','dispatching','submitted','unknown','failed'].includes(c.state)}
 function save(intent){journals.set(intent.messageId,intent);stored.add(intent.messageId);try{localStorage.setItem(prefix+intent.messageId,JSON.stringify(intent))}catch{}render()}
 function clear(id){journals.delete(id);clearTimeout(polls.get(id));polls.delete(id);try{localStorage.removeItem(prefix+id)}catch{}}
 function restore(id){if(stored.has(id))return;stored.add(id);try{const saved=JSON.parse(localStorage.getItem(prefix+id)||'null');if(validIntent(saved,id))journals.set(id,saved)}catch{}}
 function commandIntent(command){return {...scope,messageId:command.messageId,operationKey:command.id,expectedOwnRevisionId:command.expectedOwnRevisionId||'',emojis:command.emojis,command}}
 function read(root){
  for(const region of root.querySelectorAll('[data-reaction-source][data-reaction-state]')){const id=region.dataset.reactionSource;if(!uuid(id))continue;let s;try{s=JSON.parse(region.dataset.reactionState)}catch{continue}if(!validSnapshot(s,id))continue;snapshots.set(id,s);restore(id);if(s.pendingCommand){const intent=commandIntent(s.pendingCommand);if(validIntent(intent,id)&&matches(s.pendingCommand,intent)&&!journals.has(id))save(intent)}}
  render();
 }
 function chipLabel(item){return item.emoji+' · '+item.count+(item.own?' · '+labels.remove:'')}
 function render(){
  for(const region of document.querySelectorAll('[data-reaction-source]')){
   const id=region.dataset.reactionSource,s=snapshots.get(id);if(!s)continue;restore(id);const pending=journals.get(id),active=region.contains(document.activeElement)?document.activeElement:null,activeEmoji=active?.dataset.reactionEmoji;
   const signature=JSON.stringify([s.items,s.eligible,pending?.operationKey,pending?.command?.state,busy.has(id)]);if(region.dataset.reactionRendered===signature)continue;
   const fragment=document.createDocumentFragment();for(const item of s.items){const button=picker.querySelector('[data-reaction-chip-template]').content.firstElementChild.cloneNode(true);button.dataset.reactionEmoji=item.emoji;button.dataset.reactionMessage=id;button.querySelector('[data-reaction-glyph]').textContent=item.emoji;button.querySelector('[data-reaction-count]').textContent=item.count;button.setAttribute('aria-pressed',String(item.own));button.disabled=!s.eligible||!!pending||busy.has(id);button.setAttribute('aria-label',chipLabel(item));button.title=chipLabel(item);fragment.append(button)}
   if(pending){const button=picker.querySelector('[data-reaction-status-template]').content.firstElementChild.cloneNode(true),state=pending.command?.state,waiting=busy.has(id)||['queued','dispatching'].includes(state);button.dataset.reactionMessage=id;button.disabled=busy.has(id);const actionLabel=waiting?labels.pending:!pending.command&&pending.missingReceipt?labels.retry:labels.check;button.setAttribute('aria-label',actionLabel);button.title=actionLabel;button.setAttribute('aria-busy',String(waiting));button.querySelector('[data-reaction-check-icon]').hidden=waiting;button.querySelector('[data-reaction-status-spinner]').hidden=!waiting;fragment.append(button)}
   region.replaceChildren(fragment);region.dataset.reactionRendered=signature;if(active){const replacement=[...region.querySelectorAll('[data-reaction-emoji]')].find(b=>b.dataset.reactionEmoji===activeEmoji)||region.querySelector('[data-reaction-check]');replacement?.focus({preventScroll:true})}
  }
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
  const epoch=++prepareEpoch;preparing?.abort();const controller=new AbortController();preparing=controller;openerID=id;const timeout=life.timeout(()=>controller.abort(),8000);opener=element;busy.add(id);render();
  try{const url=new URL('/ui/message-reactions',location.origin);for(const[k,v]of Object.entries({...scope,messageId:id,lang:document.documentElement.lang}))url.searchParams.set(k,v);const response=await life.fetch(url,{credentials:'same-origin',cache:'no-store',headers:{Accept:'application/json'},signal:controller.signal}),value=await response.json();if(life.signal.aborted||epoch!==prepareEpoch)return;
   if(!response.ok||value.engineId!==scope.engineId||!uuid(value.operationKey)||!validSnapshot(value.snapshot,id))throw Error('unavailable');setCurrent(value.snapshot);
   if(value.snapshot.pendingCommand){const intent=commandIntent(value.snapshot.pendingCommand);if(!validIntent(intent,id)||!matches(intent.command,intent))throw Error('pending');save(intent);busy.delete(id);void submit(intent,true);return}
   if(!value.snapshot.eligible)throw Error('ineligible');const next={...scope,messageId:id,expectedOwnRevisionId:value.snapshot.ownRevisionId||'',operationKey:value.operationKey,emojis:value.snapshot.ownEmojis};
   if(emoji){busy.delete(id);choose(next,emoji);return}
   preparation=next;search.value='';window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:dialog.id}}));void loadCatalog();life.timeout(()=>search.focus({preventScroll:true}),0);
  }catch{if(!life.signal.aborted&&epoch===prepareEpoch&&!controller.signal.aborted)notify(labels.unavailable)}
  finally{clearTimeout(timeout);if(epoch===prepareEpoch)preparing=null;if(!journals.has(id))busy.delete(id);if(!life.signal.aborted)render()}
 }
 function close(){window.dispatchEvent(new CustomEvent('modal:close',{detail:{id:dialog.id}}))}
 function choose(intent,emoji){
  const s=snapshots.get(intent.messageId);if(!s||!s.eligible||s.allowedEmojis&&!s.allowedEmojis.includes(emoji))return;
  const emojis=intent.emojis.includes(emoji)?intent.emojis.filter(e=>e!==emoji):s.maxEmojis===1?[emoji]:[...intent.emojis,emoji];if(emojis.length>s.maxEmojis){notify(labels.unavailable);return}
  close();void submit({...intent,emojis},false);
 }
 function renderCatalog(){
  if(!catalog||!preparation)return;const s=snapshots.get(preparation.messageId),terms=fold(search.value).split(/\s+/).filter(Boolean),allowed=s?.allowedEmojis;
  const quick=['👍','❤️','😂','😮','😢','🙏'],items=catalog.filter(item=>(!allowed||allowed.includes(item.glyph))&&terms.every(term=>item.match.includes(term))).sort((a,b)=>{if(terms.length)return 0;const ai=quick.indexOf(a.glyph),bi=quick.indexOf(b.glyph);return(ai<0?99:ai)-(bi<0?99:bi)});
  const fragment=document.createDocumentFragment();for(const item of items.slice(0,256)){const button=picker.querySelector('[data-reaction-emoji-template]').content.firstElementChild.cloneNode(true);button.dataset.reactionChoice=item.glyph;button.textContent=item.glyph;button.setAttribute('aria-label',item.name);button.title=item.name;button.setAttribute('aria-pressed',String(preparation.emojis.includes(item.glyph)));fragment.append(button)}grid.replaceChildren(fragment);grid.scrollTop=0;status.textContent=items.length?'':labels.empty;retry.hidden=true;
 }
 async function loadCatalog(){
  if(catalog){renderCatalog();return}if(loading)return;loading=true;status.textContent=labels.loading;retry.hidden=true;
  const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),10000);
  try{let data=memory.get('reaction-catalog:'+labels.emojiUrl);if(!data){const response=await life.fetch(labels.emojiUrl,{credentials:'same-origin',redirect:'error',signal:controller.signal});if(!response.ok)throw Error('catalog');data=await response.json()}
   if(life.signal.aborted)return;if(data.locale!==document.documentElement.lang||!Array.isArray(data.items)||data.items.length>10000||data.items.some(i=>!Array.isArray(i)||i.length!==4||i.slice(0,3).some(x=>typeof x!=='string'||!x)))throw Error('catalog');memory.set('reaction-catalog:'+labels.emojiUrl,data);catalog=data.items.map(i=>({glyph:i[0],name:i[1],match:fold(i.slice(0,3).join(' '))}));renderCatalog();
  }catch{if(!life.signal.aborted){status.textContent=labels.loadFailed;retry.hidden=false}}finally{clearTimeout(timeout);loading=false}
 }
 life.on(search,'input',renderCatalog);life.on(picker,'click',event=>{if(event.target.closest('[data-reaction-close]')){close();return}if(event.target.closest('[data-reaction-catalog-retry]')){void loadCatalog();return}const chosen=event.target.closest('[data-reaction-choice]');if(chosen&&preparation)choose({...preparation,emojis:[...preparation.emojis]},chosen.dataset.reactionChoice)});
 life.on(grid,'keydown',event=>{const buttons=[...grid.querySelectorAll('[data-reaction-choice]')],index=buttons.indexOf(event.target),delta={ArrowLeft:-1,ArrowRight:1,ArrowUp:-8,ArrowDown:8}[event.key];if(index>=0&&delta!==undefined){event.preventDefault();buttons[Math.max(0,Math.min(buttons.length-1,index+delta))]?.focus()}});
 life.on(search,'keydown',event=>{if(event.key==='ArrowDown'){event.preventDefault();grid.querySelector('button')?.focus()}});
 life.on(document,'keydown',event=>{if(event.key==='Escape'&&preparing){prepareEpoch++;preparing.abort();preparing=null}});
 life.on(dialog,'close',()=>{preparation=null;const id=openerID;life.timeout(()=>{if(life.signal.aborted)return;const target=opener?.isConnected&&opener.getClientRects().length?opener:id?document.querySelector('[data-message-menu-trigger="'+CSS.escape(id)+'"]'):null;target?.focus({preventScroll:true})},0)});
 life.on(document,'click',event=>{const menu=event.target.closest('[id^="react-message-"]'),chip=event.target.closest('[data-reaction-emoji][data-reaction-message]'),check=event.target.closest('[data-reaction-check]');if(!menu&&!chip&&!check)return;const button=menu||chip||check;if(button.disabled)return;event.preventDefault();button.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));const id=menu?menu.id.slice(14):button.dataset.reactionMessage;if(check){const intent=journals.get(id);if(intent)void submit(intent,!!intent.command||!intent.missingReceipt)}else void prepare(id,button,chip?.dataset.reactionEmoji)});
 life.on(document,'oie:timeline-content',event=>read(event.detail.page));life.on(document,'oie:shared-refresh',event=>read(event.detail));life.on(document,'oie:actions-refreshed',render);
 life.cleanup(()=>{prepareEpoch++;preparing?.abort();for(const timer of polls.values())clearTimeout(timer)});
 read(document);for(const intent of journals.values())if(validIntent(intent,intent.messageId))life.timeout(()=>submit(intent,true),0);
});
