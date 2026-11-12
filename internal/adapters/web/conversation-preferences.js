window.OieRuntime.register('conversation-preferences',life=>{
 'use strict';
 const body=document.body,forms=[...document.querySelectorAll('form[data-conversation-preference]')],copy=document.querySelector('[data-conversation-preference-copy]')?.dataset;
 if(!forms.length||!copy)return;
 const engine=body.dataset.engineId,account=body.dataset.accountId,conversation=body.dataset.conversationId,csrf=document.querySelector('meta[name="oie-csrf"]')?.content||'';
 const uuid=()=>{const b=crypto.getRandomValues(new Uint8Array(16));let t=BigInt(Date.now());for(let i=5;i>=0;i--){b[i]=Number(t&255n);t>>=8n}b[6]=(b[6]&15)|112;b[8]=(b[8]&63)|128;const h=[...b].map(n=>n.toString(16).padStart(2,'0')).join('');return h.slice(0,8)+'-'+h.slice(8,12)+'-'+h.slice(12,16)+'-'+h.slice(16,20)+'-'+h.slice(20)};
 const canonical=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 const memory=window.OieRuntime.memory;
 let tab=memory.get('conversation-preference-tab')||uuid(),denied=false;
 try{tab=sessionStorage.getItem('oie.conversation-preference.tab.v1')||tab;sessionStorage.setItem('oie.conversation-preference.tab.v1',tab)}catch{denied=true}
 memory.set('conversation-preference-tab',tab);
 const key=['oie.conversation-preference.v1',engine,tab,account,conversation].join(':');
 let intent=memory.get(key)||null,current=null,busy=false,blocked=body.dataset.mock==='true';
 const requestPanel=!!document.querySelector('[data-request-preferences]');
 const field=value=>value==='archived'||value==='favorite'||value==='acceptRequest';
 const desired=name=>name==='acceptRequest'||requestPanel||!current[name];
 const valid=value=>value&&value.engineId===engine&&value.accountId===account&&value.conversationId===conversation&&canonical(value.operationKey)&&canonical(value.expectedRevisionId)&&field(value.field)&&typeof value.desired==='boolean';
 try{if(!intent)intent=JSON.parse(localStorage.getItem(key)||'null')}catch{denied=true}
 if(!valid(intent))intent=null;
 const warning=()=>{const node=document.getElementById('conversation-preference-storage-warning');if(node)node.hidden=!denied};
 function save(value){intent=value;memory.set(key,value);try{localStorage.setItem(key,JSON.stringify(value))}catch{denied=true}warning()}
 function clear(){intent=null;memory.delete(key);try{localStorage.removeItem(key)}catch{denied=true}for(const form of forms)form.elements.operationKey.value=uuid();warning()}
 const announce=message=>{const node=document.getElementById('conversation-preference-status');if(node){node.textContent=message||'';node.hidden=!message}};
 const bump=()=>{body.dataset.conversationPreferenceGeneration=String(Number(body.dataset.conversationPreferenceGeneration||0)+1)};
 const matches=value=>value&&value.id===conversation&&value.accountId===account&&canonical(value.revisionId)&&typeof value.archived==='boolean'&&typeof value.favorite==='boolean';
 function apply(value){if(!matches(value))return false;current=value;const state=document.querySelector('#chat-header [data-conversation-preference-state]');if(state){state.dataset.revision=value.revisionId;state.dataset.archived=String(value.archived);state.dataset.favorite=String(value.favorite)}return true}
 function render(){
  for(const form of forms){
   const name=form.dataset.conversationPreference,pending=intent?.field===name;
   form.dataset.busy=String(busy);form.setAttribute('aria-busy',String(busy));
   if(current){form.elements.expectedRevisionId.value=pending?intent.expectedRevisionId:current.revisionId;form.elements[name].value=String(pending?intent.desired:desired(name));if(pending)form.elements.operationKey.value=intent.operationKey}
   const requestButton=form.querySelector('[data-request-action]');
   if(requestButton){
    if(!requestButton.dataset.label)requestButton.dataset.label=requestButton.textContent.trim();
    requestButton.textContent=pending&&!busy?copy.retry:requestButton.dataset.label;
    requestButton.disabled=blocked||busy||!current||!!intent&&!pending;
    form.hidden=name==='archived'&&!!current?.archived&&!pending;
   }
   if(name==='acceptRequest')continue;
   for(const [prefix,visible]of name==='archived'?[['archive',!current?.archived],['unarchive',!!current?.archived],['retry-archive',false]]:[['favorite',!current?.favorite],['unfavorite',!!current?.favorite],['retry-favorite',false]]){
    const item=document.getElementById(prefix+'-conversation-'+conversation);if(!item)continue;
    const hidden=pending?!prefix.startsWith('retry-'):!visible;
    if(hidden&&item===document.activeElement)item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
    item.hidden=hidden;
    item.disabled=blocked||busy||!current||!!intent&&!pending;
   }
  }
  warning();
 }
 function read(event){
  const state=event?.detail?.page?.querySelector('#live-header [data-conversation-preference-state]')||document.querySelector('#chat-header [data-conversation-preference-state]');
  if(state?.dataset.conversation===conversation&&state.dataset.account===account&&['true','false'].includes(state.dataset.archived)&&['true','false'].includes(state.dataset.favorite))apply({id:conversation,accountId:account,revisionId:state.dataset.revision,archived:state.dataset.archived==='true',favorite:state.dataset.favorite==='true'});
  render();
 }
 read();if(intent)announce(copy.uncertain);
 life.on(document,'oie:conversation-preferences-refreshed',read);
 life.on(document,'oie:conversation-preference',event=>{
  const name=event.detail?.field,form=forms.find(form=>form.dataset.conversationPreference===name),button=[...document.querySelectorAll('#chat-options [role=menuitem]')].find(item=>!item.hidden&&item.id.endsWith('-conversation-'+conversation)&&((name==='archived'&&/^(archive|unarchive|retry-archive)-/.test(item.id))||(name==='favorite'&&/^(favorite|unfavorite|retry-favorite)-/.test(item.id))));
  if(!field(name)||!form||!button||button.disabled)return;
  button.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));form.requestSubmit();
 });
 life.on(document,'submit',async event=>{
  const form=event.target;if(!forms.includes(form))return;event.preventDefault();
  if(busy||blocked||!current||body.dataset.engineId!==engine||body.dataset.accountId!==account||body.dataset.conversationId!==conversation)return;
  const name=form.dataset.conversationPreference;if(intent&&intent.field!==name)return;
  if(!window.OieNavigation?.pinConversation(conversation))return;
  if(!intent)save({engineId:engine,accountId:account,conversationId:conversation,operationKey:form.elements.operationKey.value,expectedRevisionId:current.revisionId,field:name,desired:desired(name)});
  if(!valid(intent)){clear();announce(copy.unavailable);return}
  const revision=current.revisionId,values=new URLSearchParams(new FormData(form));
  for(const name of ['engineId','accountId','conversationId','operationKey','expectedRevisionId'])values.set(name,intent[name]);
  values.delete('archived');values.delete('favorite');values.delete('acceptRequest');values.set(intent.field,String(intent.desired));values.set('csrf',csrf);
  busy=true;bump();render();
  const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),14000);
  try{
   const response=await life.fetch(form.action,{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values,signal:controller.signal}),result=await response.json();
   if(life.signal.aborted)return;
   if(typeof result.ok!=='boolean')throw Error('invalid_result');
   // A newer live projection may have arrived while this command was pending.
   if(current.revisionId===revision||current.revisionId===result.conversation?.revisionId)apply(result.conversation);
   if(response.ok&&result.ok||result.rejected===true){clear();if(!matches(result.conversation)&&!result.ok)current=null}
   if(result.code==='engine_changed')blocked=true;
   announce(result.message||copy.uncertain);
  }catch{if(!life.signal.aborted)announce(copy.uncertain)}
  finally{clearTimeout(timeout);if(!life.signal.aborted){busy=false;bump();render();document.dispatchEvent(new CustomEvent('oie:refresh-request'))}}
 });
});
