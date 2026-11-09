window.OieRuntime.register('sender-info', life => {
 'use strict';
 const slot=document.getElementById('sender-content');if(!slot)return;
 let generation=0,request=null;
 const close=()=>{generation++;request?.abort();slot.removeAttribute('aria-busy');};
 life.cleanup(close);
 life.on(window,'drawer:close',event=>{if(event.detail?.id==='sender')close()});
 life.on(window,'drawer:close-request',event=>{if(event.detail?.id==='sender'&&!event.defaultPrevented)close()});
 life.on(document,'click',async event=>{
  const trigger=event.target.closest('[data-open-sender]');
  if(!trigger||event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  event.preventDefault();event.stopPropagation();
  close();const epoch=generation;request=new AbortController();const controller=request;
  slot.textContent=slot.dataset.senderLoading;slot.setAttribute('aria-busy','true');
  window.dispatchEvent(new CustomEvent('drawer:open',{detail:{id:'sender'}}));
  const url=new URL(trigger.dataset.senderFragment,location.origin);
  const timeout=life.timeout(()=>controller.abort(),10000);
  try{
   const response=await life.fetch(url,{signal:controller.signal,headers:{'Accept':'text/html'}});
   const text=await response.text();if(life.signal.aborted||epoch!==generation)return;
   if(!response.ok)throw new Error('unavailable');
   const doc=new DOMParser().parseFromString(text,'text/html'),content=doc.querySelector('[data-sender-info]');
   if(!content||content.dataset.senderEngine!==url.searchParams.get('engineId')||content.dataset.senderAccount!==url.searchParams.get('accountId')||content.dataset.senderConversation!==url.searchParams.get('conversationId')||content.dataset.senderMessage!==url.searchParams.get('messageId'))throw new Error('scope');
   slot.replaceChildren(document.importNode(content,true));
  }catch{
   if(!life.signal.aborted&&epoch===generation){slot.textContent=slot.dataset.senderError;const retry=document.createElement('a');retry.href=trigger.href;retry.textContent=trigger.getAttribute('aria-label');retry.dataset.openSender='';retry.dataset.senderFragment=url.href;retry.className='oie-sender-retry';slot.append(retry);}
  }finally{clearTimeout(timeout);if(!life.signal.aborted&&epoch===generation)slot.removeAttribute('aria-busy');}
 });
});
