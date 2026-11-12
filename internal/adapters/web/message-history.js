(() => {
 const dates=page=>window.OieTime.localize(page);
 if(!window.OieRuntime){
  if(document.querySelector('[data-message-history-native]')){let enabled=true;try{enabled=localStorage.getItem('oie.device.render-markdown.v1')!=='false'}catch{}document.documentElement.dataset.messageMarkdown=String(enabled);dates(document);document.addEventListener('oie:time-zone',()=>dates(document));}
  return;
 }
 window.OieRuntime.register('message-history', life => {
 'use strict';
 const root=document.getElementById('message-history-content');if(!root)return;
 const results=root.querySelector('[data-history-results]'),loading=root.querySelector('[data-history-loading]'),error=root.querySelector('[data-history-error]'),retry=root.querySelector('[data-history-retry]'),restart=root.querySelector('[data-history-restart]');
 let generation=0,request=null,scope=null,snapshot='',currentRevision='',canonical='',pages=[],position=-1,lastRequest=null,active=false,opener=null,openerKind='edited',observed=null;
 const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 const digest=value=>typeof value==='string'&&/^[0-9a-f]{64}$/.test(value);
 function scopedURL(value,wanted=null){
  let url;try{url=new URL(value,location.origin)}catch{return null}
  const q=url.searchParams;if(url.origin!==location.origin||url.pathname!=='/ui/message-history'||!['engineId','accountId','conversationId','messageId'].every(key=>uuid(q.get(key)))||(q.get('cursor')||'').length>2048)return null;
  if(q.get('engineId')!==document.body.dataset.engineId||q.get('accountId')!==document.body.dataset.accountId||q.get('conversationId')!==document.body.dataset.conversationId)return null;
  if(wanted&&['engineId','accountId','conversationId','messageId'].some(key=>q.get(key)!==wanted.url.searchParams.get(key)))return null;
  return url;
 }
 function sourceVersion(id){const row=id&&document.querySelector(`#message-timeline [data-message-id="${CSS.escape(id)}"][data-message-source]`);return row?JSON.stringify([row.dataset.messageSource,row.querySelector('[data-message-edited]')?.dataset.editedAt||'']):null;}
 function cancel(){generation++;request?.abort();request=null;loading.hidden=true;root.removeAttribute('aria-busy');}
 function currentOpener(){
  if(opener?.isConnected)return opener;
  const id=canonical||scope?.url.searchParams.get('messageId');if(!uuid(id))return null;
  const row=document.querySelector(`#message-timeline [data-message-id="${CSS.escape(id)}"]`);if(!row)return null;
  const trigger=row.querySelector('[data-message-menu-trigger]');
  return openerKind==='menu'?trigger:row.querySelector('[data-open-message-history][data-message-edited]')||trigger;
 }
 function close(restore=true){
  const wasActive=active;active=false;cancel();results.replaceChildren();
  if(restore&&wasActive)life.timeout(()=>{if(!life.signal.aborted)currentOpener()?.focus({preventScroll:true})},0);
 }
 life.cleanup(()=>close(false));
 life.on(window,'drawer:close',event=>{if(event.detail?.id==='message-history')close()});
 life.on(window,'drawer:close-request',event=>{if(event.detail?.id==='message-history'&&!event.defaultPrevented)close()});

 function matches(page,url,wanted){
  const d=page?.dataset,q=url.searchParams;
  if(!d||d.historyEngine!==q.get('engineId')||d.historyAccount!==q.get('accountId')||d.historyConversation!==q.get('conversationId')||d.historyRequested!==q.get('messageId')||d.historyCursor!==(q.get('cursor')||'')||!uuid(d.historyMessage)||!uuid(d.historyCurrentRevision)||!digest(d.historySnapshot))return false;
  if(wanted!==scope||snapshot&&(d.historySnapshot!==snapshot||d.historyCurrentRevision!==currentRevision||d.historyMessage!==canonical))return false;
  const rows=[...page.querySelectorAll('[data-history-revision]')];if(rows.length>20||new Set(rows.map(row=>row.dataset.historyRevision)).size!==rows.length)return false;
  return rows.every(row=>uuid(row.dataset.historyRevision)&&uuid(row.dataset.historySource)&&['true','false'].includes(row.dataset.historyIsCurrent)&&['true','false'].includes(row.dataset.historyIsOriginal)&&(row.dataset.historyIsCurrent==='true')===(row.dataset.historyRevision===d.historyCurrentRevision));
 }
 function fail(changed,focus){
  results.replaceChildren();error.textContent=changed?root.dataset.historyChanged:root.dataset.historyUnavailable;error.hidden=false;retry.hidden=changed;restart.hidden=!changed;
  if(changed){snapshot='';currentRevision='';canonical='';pages=[];position=-1;observed=null;}
  if(focus)(changed?restart:retry).focus({preventScroll:true});
 }
 async function load(url,stack,index,focus=false){
  const wanted=scope;if(!wanted||!scopedURL(url,wanted))return;cancel();const epoch=generation;request=new AbortController();const controller=request;
  lastRequest={url:url.href,stack,index};results.replaceChildren();error.hidden=true;retry.hidden=true;restart.hidden=true;loading.hidden=false;root.setAttribute('aria-busy','true');
  const timeout=life.timeout(()=>controller.abort(),10000);
  try{
   const response=await life.fetch(url,{credentials:'same-origin',cache:'no-store',headers:{Accept:'text/html','X-Oie-Fragment':'message-history'},signal:controller.signal});
   if(life.signal.aborted||epoch!==generation||!active||scope!==wanted)return;
   if(!response.ok){fail(response.status===409||response.status===400,focus);return}
   const text=await response.text();if(life.signal.aborted||epoch!==generation||!active||scope!==wanted)return;
   const doc=new DOMParser().parseFromString(text,'text/html'),page=doc.querySelector('[data-message-history-page]');
   if(!matches(page,url,wanted))throw new Error('scope');
   if(page.dataset.historyNext&&!scopedURL(page.dataset.historyNext,wanted))throw new Error('next_scope');
   snapshot=page.dataset.historySnapshot;currentRevision=page.dataset.historyCurrentRevision;canonical=page.dataset.historyMessage;pages=stack;position=index;if(observed===null)observed=sourceVersion(canonical);
   const node=document.importNode(page,true);dates(node);node.querySelector('[data-history-newer]').hidden=position<=0;results.replaceChildren(node);
   const scroll=document.getElementById('message-history-body');if(scroll)scroll.scrollTop=0;
   if(focus)node.focus({preventScroll:true});
  }catch{if(!life.signal.aborted&&epoch===generation&&active&&scope===wanted)fail(false,focus)}
  finally{clearTimeout(timeout);if(!life.signal.aborted&&epoch===generation){loading.hidden=true;root.removeAttribute('aria-busy');}}
 }
 // Content changes invalidate the snapshot. Transport-state revisions alone
 // leave the retained history, focused controls and page position untouched.
 life.on(document,'oie:shared-refresh',()=>{
  if(!active||observed===null||!scope)return;
  const current=sourceVersion(canonical||scope.url.searchParams.get('messageId'));
  if(current!==null&&current!==observed){const focus=results.contains(document.activeElement);cancel();fail(true,focus);}
 });
 life.on(document,'click',event=>{
  const action=event.target.closest('[data-open-message-history],[id^="history-message-"]');
  if(action&&!event.defaultPrevented&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey){
   const url=scopedURL(action.href);if(!url)return;
   event.preventDefault();event.stopPropagation();close(false);active=true;scope={url};snapshot='';currentRevision='';canonical='';pages=[];position=-1;observed=sourceVersion(url.searchParams.get('messageId'));
   const menuID=action.id?.startsWith('history-message-')?action.id.slice(16):'';openerKind=menuID?'menu':'edited';opener=menuID?document.getElementById('message-menu-'+menuID)?.querySelector('button')||action:action;opener?.focus({preventScroll:true});
   window.dispatchEvent(new CustomEvent('drawer:open',{detail:{id:'message-history'}}));load(url,[url.href],0);return;
  }
  if(!active)return;
  const next=event.target.closest('a[data-history-next]');if(next&&root.contains(next)&&!event.defaultPrevented&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey){event.preventDefault();const url=scopedURL(next.href,scope);if(url)load(url,[...pages.slice(0,position+1),url.href],position+1,true);return}
  const newer=event.target.closest('[data-history-newer]');if(newer&&root.contains(newer)){event.preventDefault();if(position>0)load(new URL(pages[position-1]),pages,position-1,true);return}
  const again=event.target.closest('[data-history-retry]');if(again&&root.contains(again)){event.preventDefault();if(lastRequest)load(new URL(lastRequest.url),lastRequest.stack,lastRequest.index,true);return}
  const fresh=event.target.closest('[data-history-restart]');if(fresh&&root.contains(fresh)){event.preventDefault();const url=new URL(scope.url);url.searchParams.delete('cursor');observed=sourceVersion(canonical||url.searchParams.get('messageId'));snapshot='';currentRevision='';canonical='';load(url,[url.href],0,true);}
 });
});

})();
