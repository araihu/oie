window.OieRuntime.register('shared-actions', life => {
 'use strict';
 const labels=document.querySelector('[data-shared-action-copy]')?.dataset||{};
 const pending=new Set();
 function feedback(scope,message){const node=scope.closest('[data-shared-item]')?.querySelector('[data-shared-action-status]');if(node){node.textContent=message||'';node.hidden=!message;}}
 function close(item){item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));}
 async function save(url,name){if(window.OieDesktop?.saveFile)return window.OieDesktop.saveFile(url,name||'');const anchor=document.createElement('a');anchor.href=url;anchor.download=name||'';document.body.append(anchor);anchor.click();anchor.remove();return true;}
 async function copy(value){
  if(navigator.clipboard?.writeText){await navigator.clipboard.writeText(value);return;}
  const previous=document.activeElement,field=document.createElement('textarea');field.value=value;field.readOnly=true;field.style.cssText='position:fixed;left:-10000px;top:0';document.body.append(field);field.select();
  try{if(!document.execCommand('copy'))throw Error('clipboard_unavailable');}finally{field.remove();previous?.focus({preventScroll:true});}
 }
 function syncMenus(){for(const scope of document.querySelectorAll('[data-shared-actions]')){const preview=scope.closest('[data-shared-item]')?.querySelector('[data-preview-id]'),button=scope.querySelector('[id^="shared-refresh-"]');if(button&&preview)button.disabled=preview.dataset.previewState==='queued'||preview.dataset.previewRefreshing==='true';}}
 life.on(document,'oie:actions-refreshed',syncMenus);life.on(document,'oie:info-content',syncMenus);life.raf(syncMenus);
 life.on(document,'click',async event=>{
  const item=event.target.closest('[id^="shared-copy-"],[id^="shared-download-"],[id^="shared-refresh-"]');
  if(!item||item.disabled)return;
  const scope=item.closest('[data-shared-actions]');if(!scope)return;
  event.preventDefault();close(item);
  const key=scope.dataset.sharedActions;if(pending.has(key))return;
  const address=scope.dataset.sharedUrl;let url;try{url=new URL(address);}catch{return;}
  if(!['https:','http:'].includes(url.protocol)||url.username||url.password)return;
  if(item.id.startsWith('shared-refresh-')){
   item.disabled=true;document.dispatchEvent(new CustomEvent('oie:preview-refresh-request',{detail:{id:scope.dataset.sharedPreview}}));return;
  }
  if(item.id.startsWith('shared-copy-')){
   pending.add(key);try{await copy(address);if(!life.signal.aborted&&scope.isConnected)feedback(scope,labels.copied);}catch{if(!life.signal.aborted&&scope.isConnected)feedback(scope,labels.failed);}finally{pending.delete(key);}return;
  }
  if(scope.dataset.sharedEngine!==document.body.dataset.engineId){feedback(scope,labels.failed);return;}
  if(scope.dataset.sharedKind==='image'){
   pending.add(key);feedback(scope,labels.downloading);
   document.dispatchEvent(new CustomEvent('oie:preview-save-request',{detail:{id:scope.dataset.sharedPreview,engineId:scope.dataset.sharedEngine,accountId:scope.dataset.sharedAccount,conversationId:scope.dataset.sharedConversation,messageId:scope.dataset.sharedMessage,url:address,alive:()=>!life.signal.aborted&&scope.isConnected,onState:state=>{if(state!=='loading')pending.delete(key);if(life.signal.aborted||!scope.isConnected)return;feedback(scope,state==='failed'?labels.failed:state==='loading'?labels.downloading:'');}}}));return;
  }
  const path=scope.dataset.sharedDownload,expected='/ui/shared-content/'+encodeURIComponent(key)+'/content';let target;try{target=new URL(path,location.origin);}catch{return;}
  if(!['video','document'].includes(scope.dataset.sharedKind)||target.origin!==location.origin||target.pathname!==expected||target.searchParams.get('engineId')!==document.body.dataset.engineId){feedback(scope,labels.failed);return;}
  target.searchParams.set('download','1');pending.add(key);
  try{await save(target.pathname+target.search,scope.dataset.sharedName);if(!life.signal.aborted&&scope.isConnected)feedback(scope,'');}
  catch{if(!life.signal.aborted&&scope.isConnected)feedback(scope,labels.failed);}
  finally{pending.delete(key);}
 });
});
