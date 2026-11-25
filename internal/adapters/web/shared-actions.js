window.OieRuntime.register('shared-actions', life => {
 'use strict';
 const copyTemplate = document.querySelector('template[data-shared-action-copy]');
 const labels = copyTemplate?.dataset || {};
 const pending=new Set();
 function feedback(scope,message){const node=scope.closest('[data-shared-item]')?.querySelector('[data-shared-action-status]');if(node){node.textContent=message||'';node.hidden=!message;}}
 let copyAttempt = 0;
 let copyToast;
 let copyToastTimer;
 function clearCopyToast() {
  clearTimeout(copyToastTimer);
  const toast = copyToast;
  copyToast = null;
  toast?.remove();
 }
 life.cleanup(clearCopyToast);
 function showCopyFeedback(scope, copied) {
  const drawer = scope.closest('.oie-conversation-details-drawer');
  const outlet = drawer || document.getElementById('toast-container-oob');
  const result = copied ? 'success' : 'failure';
  const template = copyTemplate?.content.querySelector(`[data-copy-result="${result}"]`);
  if (!outlet || !template) return;
  const card = scope.closest('[data-shared-item]');
  const toast = template.content.firstElementChild.cloneNode(true);
  if (drawer) toast.classList.add('oie-drawer-copy-toast');
  const scheduleDismissal = () => {
   if (copyToast !== toast) return;
   clearTimeout(copyToastTimer);
   copyToastTimer = setTimeout(() => {
    if (copyToast === toast && toast.isConnected) {
     window.dispatchEvent(new CustomEvent('toast-dismiss', {detail: {id: toast.dataset.toastId}}));
    }
   }, copied ? 2500 : 8000);
  };
  toast.addEventListener('focusin', () => clearTimeout(copyToastTimer));
  toast.addEventListener('focusout', scheduleDismissal);
  toast.querySelector('button').addEventListener('click', () => {
   if (toast.contains(document.activeElement) && card?.isConnected) {
    card.querySelector('[data-shared-menu-trigger]')?.focus({preventScroll:true});
   }
   clearTimeout(copyToastTimer);
  });
  clearCopyToast();
  copyToast = toast;
  outlet.append(toast);
  scheduleDismissal();
 }
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
  event.preventDefault();
  const key=scope.dataset.sharedActions;if(pending.has(key))return;
  const address=scope.dataset.sharedUrl;let url;try{url=new URL(address);}catch{return;}
  if(!['https:','http:'].includes(url.protocol)||url.username||url.password)return;
  if(item.id.startsWith('shared-refresh-')){
   item.disabled=true;document.dispatchEvent(new CustomEvent('oie:preview-refresh-request',{detail:{id:scope.dataset.sharedPreview}}));return;
  }
  if(item.id.startsWith('shared-copy-')){
   const attempt = ++copyAttempt;
   clearCopyToast();
   pending.add(key);
   let copied = false;
   try {
    await copy(address);
    copied = true;
   } catch {
    // A denied clipboard write leaves the failure toast as feedback.
   } finally {
    pending.delete(key);
   }
   if (attempt === copyAttempt && !life.signal.aborted && scope.isConnected) showCopyFeedback(scope, copied);
   return;
  }
  if(scope.dataset.sharedEngine!==document.body.dataset.engineId){feedback(scope,labels.failed);return;}
  if(scope.dataset.sharedKind==='image'){
   pending.add(key);feedback(scope,labels.downloading);
   document.dispatchEvent(new CustomEvent('oie:preview-save-request',{detail:{id:scope.dataset.sharedPreview,engineId:scope.dataset.sharedEngine,accountId:scope.dataset.sharedAccount,conversationId:scope.dataset.sharedConversation,messageId:scope.dataset.sharedMessage,url:address,alive:()=>!life.signal.aborted&&scope.isConnected,onState:state=>{if(state!=='loading')pending.delete(key);if(life.signal.aborted||!scope.isConnected)return;feedback(scope,state==='failed'?labels.failed:state==='loading'?labels.downloading:'');}}}));return;
  }
  const path=scope.dataset.sharedDownload,expected='/ui/shared-content/'+encodeURIComponent(key)+'/content';let target;try{target=new URL(path,location.origin);}catch{return;}
  if(!['video','audio','document'].includes(scope.dataset.sharedKind)||target.origin!==location.origin||target.pathname!==expected||target.searchParams.get('engineId')!==document.body.dataset.engineId){feedback(scope,labels.failed);return;}
  target.searchParams.set('download','1');pending.add(key);
  try{await save(target.pathname+target.search,scope.dataset.sharedName);if(!life.signal.aborted&&scope.isConnected)feedback(scope,'');}
  catch{if(!life.signal.aborted&&scope.isConnected)feedback(scope,labels.failed);}
  finally{pending.delete(key);}
 });
});
