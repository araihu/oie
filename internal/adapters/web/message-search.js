(() => {
'use strict';

const resultSelector='a:is([data-message-search-source],[data-message-search-conversation],[data-message-search-contact])';
const moreSelector='a[data-message-search-next]';
const searchKeys=['searchMode','searchQuery','searchAccount','searchType','searchConversation','searchCursor','searchEngine','includeEditHistory'];
const chatSearchKeys=['chatSearch','chatQuery','chatIncludeHistory'];

function searchResponseMatches(response,state,cursor){
 const data=response?.dataset;
 return data&&data.searchEngine===state.engineId&&data.searchQuery===state.q&&data.searchAccount===state.accounts.join(',')&&data.searchConversation===state.conversationId&&data.searchHistory===state.includeEditHistory&&data.searchType===state.searchType&&data.searchCursor===cursor;
}

function searchResultKeys(life,input,scroll,enabled){
 life.on(document,'keydown',event=>{
  if(!enabled()||event.defaultPrevented||event.isComposing||event.keyCode===229||event.altKey||event.ctrlKey||event.metaKey||event.shiftKey)return;
  const link=event.target.closest?.(resultSelector+','+moreSelector),fromInput=event.target===input;
  if(!fromInput&&!scroll.contains(link))return;
  if(event.key==='Enter'&&fromInput){
   const first=scroll.querySelector(resultSelector);
   if(first){event.preventDefault();first.click();}
   return;
  }
  if(event.key!=='ArrowDown'&&event.key!=='ArrowUp')return;
  const links=[...scroll.querySelectorAll(resultSelector+','+moreSelector)];
  if(!links.length)return;
  event.preventDefault();
  const index=links.indexOf(link);
  const target=event.key==='ArrowDown'?links[index+1]:index===-1?links.at(-1):links[index-1]||input;
  if(target===input)input.focus({preventScroll:true});
  else if(target){
   target.focus({preventScroll:true});
   target.scrollIntoView({block:'nearest',inline:'nearest'});
  }
 },true);
}

window.OieRuntime.register('message-search',life=>{
 'use strict';
 const root=document.querySelector('[data-message-search-root]');if(!root)return;
 const native=root.dataset.native==='true',form=root.querySelector('#message-search-form'),query=native?form.elements.q:document.getElementById('conversation-search');if(!query)return;
 for(const fieldset of root.querySelectorAll('[data-message-search-native-accounts]'))fieldset.disabled=true;
 const slot=document.getElementById('message-search-slot'),original=document.getElementById('conversation-list-default'),filterForm=document.getElementById('conversation-filter-form'),close=document.querySelector('[data-message-search-close]'),originalQuery=root.dataset.titleQuery||'';
 const historyField=form.elements.includeEditHistory,conversation=form.elements.conversationId,category=form.elements.searchType;
 const categoryNav=root.querySelector('[data-message-search-categories]');
 const historyHelp=form.querySelector('.oie-search-history-info'),historyTip=form.querySelector('#message-search-history-help');
 const results=root.querySelector('#message-search-results'),pagination=root.querySelector('#message-search-pagination'),buffer=root.querySelector('#message-search-page-buffer'),scroll=root.querySelector('#message-search-scroll'),feedback=root.querySelector('#message-search-feedback'),loading=root.querySelector('[data-message-search-loading]');
 const initial=results.querySelector('[data-message-search-response]');
 const engine=form.elements.engineId.value,memory=window.OieRuntime.memory;
 let epoch=0,timer=0,active=null,busy=false,next='',lastCursor='',opened=native,observer=null,engineChanged=false;
 const accountSet=values=>[...new Set(values.filter(Boolean))].sort();
 let selectedAccounts=accountSet((initial?.dataset.searchAccount||'').split(','));
 const value=()=>({q:query.value.trim(),accounts:selectedAccounts,conversationId:conversation.value,includeEditHistory:String(historyField.checked),searchType:category.value,engineId:engine});
 const key=s=>JSON.stringify([s.engineId,s.q,s.accounts,s.conversationId,s.includeEditHistory,s.searchType]);
 function requestParams(state){
  const {accounts,...fields}=state;
  const params=new URLSearchParams(fields);
  if(accounts.length===1)params.set('accountId',accounts[0]);
  else for(const id of accounts)params.append('accountIds',id);
  return params;
 }
 const valid=()=>!life.signal.aborted&&root.isConnected&&document.body.dataset.engineId===engine;
 function abort(){observer?.disconnect();epoch++;if(timer)clearTimeout(timer);timer=0;active?.request?.abort?.();active=null;busy=false;loading.hidden=true;results.removeAttribute('aria-busy');}
 function status(text,restart=false){feedback.textContent=text||'';feedback.hidden=!text;for(const node of root.querySelectorAll('button[data-message-search-retry]'))node.hidden=!text||restart;for(const node of root.querySelectorAll('button[data-message-search-restart]'))node.hidden=!text||!restart;}
 function clearSearchParams(url){
  for(const name of searchKeys)url.searchParams.delete(name);
 }
 function applyURL(url,state){
  clearSearchParams(url);
  url.searchParams.set('searchMode','messages');
  for(const [name,val]of [['searchQuery',state.q],['searchEngine',state.engineId],['searchType',state.searchType],['includeEditHistory',state.includeEditHistory==='true'?'true':'']])if(val)url.searchParams.set(name,val);
  for(const id of state.accounts)url.searchParams.append('searchAccount',id);
 }
 function nativeAddressParams(state){
  const params=new URLSearchParams();
  for(const [name,val]of requestParams(state))if(val&&val!=='false')params.append(name,val);
  params.set('lang',form.elements.lang.value);
  params.set('theme',form.elements.theme.value);
  return params;
 }
 function address(push=false){const state=value(),url=new URL(location.href);if(native)url.search=nativeAddressParams(state);else applyURL(url,state);window.OieNavigation?.addressSearch(url,push);}
 function show(active){opened=active;root.dataset.searchOpen=String(active);if(slot)slot.hidden=!active;if(original)original.hidden=active;if(close)close.hidden=!active;filterForm?.classList.toggle('oie-global-search-active',active);}
 function rememberPending(){if(memory.get('message-search-source-pending')===engine)memory.set('message-search-pending-open',{engine,state:value()});}
 function watch(){observer?.disconnect();next=pagination.querySelector('[data-message-search-next]')?.dataset.searchCursor||'';if(!next||!opened)return;observer=new IntersectionObserver(entries=>{if(opened&&entries.some(e=>e.isIntersecting)&&!busy&&!feedback.textContent)request(next)},{root:scroll,rootMargin:'200px'});observer.observe(pagination);}
 function consume(response,append=false){const items=response.querySelector('[data-message-search-items]'),pager=response.querySelector('[data-message-search-pagination]');if(!items||!pager)return false;for(const name of ['searchQuery','searchAccount','searchConversation','searchHistory','searchType','searchState'])root.dataset[name]=response.dataset[name]||'';const seen=new Set([...results.querySelectorAll('[data-message-search-hit]')].map(n=>n.dataset.messageSearchHit));if(append){for(const node of items.children){const id=node.dataset.messageSearchHit;if(id&&seen.has(id))continue;results.append(node.cloneNode(true));if(id)seen.add(id)}}else{results.replaceChildren(...[...items.childNodes].map(n=>n.cloneNode(true)));scroll.scrollTop=0;}pagination.replaceChildren(...[...pager.childNodes].map(n=>n.cloneNode(true)));watch();return true;}
 function blank(){consume(root.querySelector('template[data-message-search-blank]').content.querySelector('[data-message-search-response]'));pagination.replaceChildren();next='';status('');}
 function request(cursor=''){if(!valid()||busy||!opened)return;const state=value();if(!state.q){blank();return}if(Array.from(state.q).length>200){status(root.dataset.searchInvalid);for(const node of root.querySelectorAll('button[data-message-search-retry]'))node.hidden=true;return;}lastCursor=cursor;status('');form.elements.q.value=query.value;const params=requestParams(state);params.set('limit',form.elements.limit.value);params.set('lang',form.elements.lang.value);if(cursor)params.set('cursor',cursor);window.htmx.ajax('GET','/ui/message-search?'+params,{source:form,target:buffer,select:'[data-message-search-response]',swap:'innerHTML show:none'});}
 function categories(){
  categoryNav.hidden=native&&!!conversation.value;
  for(const link of categoryNav.querySelectorAll('[data-message-search-filter]')){
   const searchType=link.dataset.messageSearchFilter,state={...value(),searchType};
   const url=new URL(location.href);
   if(native)url.search=nativeAddressParams(state);else applyURL(url,state);
   link.href=url.pathname+url.search+url.hash;
   link.setAttribute('aria-current',String(searchType===category.value));
  }
  for(const link of categoryNav.querySelectorAll('[data-message-search-category]')){
   const url=new URL(link.href,location.href);
   for(const name of [...searchKeys,'chatSearch','chatQuery','chatIncludeHistory','chat','message'])url.searchParams.delete(name);
   url.searchParams.set('q',query.value.trim());
   url.searchParams.set('account',selectedAccounts.length===1?selectedAccounts[0]:'');
   link.href=url.pathname+url.search;
  }
 }
 function changed(delay=0){
  const hasQuery=!!value().q;
  if(!native&&!hasQuery){
   if(opened)exit();
   return;
  }
  const opening=!opened;
  if(opening)show(true);
  categories();rememberPending();abort();status('');address(opening);next='';
  if(!hasQuery){blank();return;}
  results.replaceChildren();pagination.replaceChildren();timer=life.timeout(()=>request(),delay);
 }
 // The URL carries the canonical account scope. The combobox also contributes
 // hidden inputs to the form, which must not turn a set into a mixed filter.
 life.on(form,'htmx:config:request',event=>{
  const ctx=event.detail.ctx;
  if(ctx?.target===buffer)ctx.request.body.delete('accountId');
 });
 life.on(document,'htmx:before:request',event=>{const ctx=event.detail.ctx;if(ctx?.target!==buffer)return;if(!valid()||!opened){event.preventDefault();return}ctx.searchEpoch=epoch;ctx.searchKey=key(value());ctx.searchCursor=new URL(ctx.request.action,location.href).searchParams.get('cursor')||'';active=ctx;busy=true;loading.hidden=false;results.setAttribute('aria-busy','true');});
 life.on(document,'htmx:after:request',event=>{const ctx=event.detail.ctx;if(ctx?.target!==buffer)return;if(!valid()||!opened||ctx.searchEpoch!==epoch||ctx.searchKey!==key(value())){event.preventDefault();return}const parsed=new DOMParser().parseFromString(ctx.text||'','text/html'),response=parsed.querySelector('[data-message-search-response]');if(!searchResponseMatches(response,value(),ctx.searchCursor)||ctx.response?.status<200||ctx.response?.status>=300){event.preventDefault();busy=false;loading.hidden=true;results.removeAttribute('aria-busy');const code=ctx.response?.status;engineChanged=code===409;if(engineChanged){results.replaceChildren();pagination.replaceChildren();next='';observer?.disconnect();root.dataset.searchState='engine_changed';}status(code===409?root.dataset.searchEngineChanged:code===400?root.dataset.searchInvalid:root.dataset.searchFailed,code===400||code===409);return;}engineChanged=false;ctx.searchResponse=response;});
 life.on(document,'htmx:before:swap',event=>{const ctx=event.detail.ctx;if(ctx?.target===buffer&&(!valid()||!opened||ctx.searchEpoch!==epoch||ctx.searchKey!==key(value())||!ctx.searchResponse))event.preventDefault();});
 life.on(document,'htmx:after:settle',event=>{if(event.detail.task?.target!==buffer||!active?.searchResponse||active.searchEpoch!==epoch||!valid()||!opened)return;const ctx=active;busy=false;active=null;loading.hidden=true;results.removeAttribute('aria-busy');consume(ctx.searchResponse,!!ctx.searchCursor);buffer.replaceChildren();document.dispatchEvent(new CustomEvent('oie:message-search-settled',{detail:{root}}));});
 life.on(document,'htmx:error',event=>{const ctx=event.detail.ctx;if(ctx?.target===buffer&&ctx.searchEpoch===epoch&&valid()&&opened){busy=false;active=null;loading.hidden=true;results.removeAttribute('aria-busy');status(root.dataset.searchFailed);}});
 life.on(query,'compositionstart',()=>abort());life.on(query,'input',event=>{if(!event.isComposing)changed(300)});life.on(query,'compositionend',()=>changed(300));
 for(const node of [form,filterForm])life.on(node,'submit',event=>{event.preventDefault();changed()});life.on(historyField,'change',()=>changed());
 life.on(document,'combobox:change',event=>{
  if(event.detail?.id!=='message-search-account')return;
  selectedAccounts=accountSet(event.detail.values);
  conversation.value='';
  root.querySelector('[data-message-search-scope]').hidden=true;
  changed();
 });
 function setScope(state){
  selectedAccounts=state.accounts;
  category.value=state.searchType;
  query.value=state.q||'';form.elements.q.value=query.value;historyField.checked=state.includeEditHistory==='true';conversation.value=native?(state.conversationId||''):'';
  document.dispatchEvent(new CustomEvent('combobox:load',{detail:{id:'message-search-account',values:selectedAccounts}}));
  categories();
 }
 function restoreTitleQuery(){query.value=originalQuery;form.elements.q.value=originalQuery;categories();}
 function clearAddress(){
  const url=new URL(location.href);
  clearSearchParams(url);
  window.OieNavigation?.addressSearch(url);
  // The mounted inbox and workspace can have links rendered with the search
  // that was active when their fragment loaded.
  for(const node of document.querySelectorAll('#oie-app a[href^="/?"][href*="searchMode="],#oie-app input[name="returnTo"][value^="/?"][value*="searchMode="]')){
   const attr=node.tagName==='A'?'href':'value';
   const target=new URL(node.getAttribute(attr),url);
   if(target.searchParams.get('searchMode')!=='messages')continue;
   clearSearchParams(target);
   node.setAttribute(attr,target.pathname+target.search+target.hash);
  }
 }
 function dismissHistoryHelp(){if(historyHelp?.getAttribute('aria-expanded')==='true')historyHelp.click();}
 // Some app actions consume the click before the tooltip's outside listener.
 life.on(document,'pointerdown',event=>{
  if(event.button===0&&!historyHelp?.contains(event.target)&&!historyTip?.contains(event.target))dismissHistoryHelp();
 },true);
 function exit(){
  dismissHistoryHelp();abort();show(false);blank();restoreTitleQuery();clearAddress();
  if(memory.get('message-search-source-pending')===engine)memory.set('message-search-pending-open',{engine,state:null});
  query.focus({preventScroll:true});
 }
 const sidebarSearchActive=()=>opened&&!document.querySelector('dialog[open]');
 life.on(document,'keydown',event=>{
  if(native||event.key!=='Escape'||event.defaultPrevented||event.isComposing||event.keyCode===229||!sidebarSearchActive())return;
  if(!filterForm?.contains(event.target)&&!root.contains(event.target))return;
  if(event.target.closest('#message-search-account')?.querySelector('[role="combobox"][aria-expanded="true"]'))return;
  if(historyHelp?.contains(event.target)&&historyHelp.getAttribute('aria-expanded')==='true')return;
  event.preventDefault();event.stopImmediatePropagation();exit();
 },true);
 searchResultKeys(life,query,scroll,sidebarSearchActive);
 life.on(document,'click',event=>{const node=event.target.closest?.('[data-message-search-close],[data-message-search-filter],[data-message-search-source],[data-message-search-conversation],[data-message-search-contact],[data-message-search-next],[data-message-search-retry],[data-message-search-restart],[data-message-search-clear-scope]');if(!node||!root.contains(node)&&node!==close||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;event.preventDefault();event.stopImmediatePropagation();if(node===close){exit();return}dismissHistoryHelp();
  if(node.hasAttribute('data-message-search-filter')){
   const selected=node.dataset.messageSearchFilter;
   if(category.value!==selected){category.value=selected;changed();}
   return;
  }
  if(node.hasAttribute('data-message-search-source')||node.hasAttribute('data-message-search-conversation')||node.hasAttribute('data-message-search-contact')){const url=new URL(node.href,location.href);if(!native){applyURL(url,value());if(originalQuery)url.searchParams.set('q',originalQuery);else url.searchParams.delete('q');url.searchParams.set('filter',root.dataset.inboxFilter||'all');url.searchParams.set('section',root.dataset.inboxSection||'chats');}if(url.searchParams.get('chat')!==document.body.dataset.conversationId)for(const name of ['chatSearch','chatQuery','chatIncludeHistory'])url.searchParams.delete(name);memory.set('message-search-source-pending',engine);rememberPending();const finished=()=>{if(life.signal.aborted)return;memory.delete('message-search-source-pending');memory.delete('message-search-pending-open');if(opened)address();};Promise.resolve(window.OieNavigation?.go(url)).then(finished,finished);return}if(node.hasAttribute('data-message-search-clear-scope')){conversation.value='';root.querySelector('[data-message-search-scope]').hidden=true;changed();return}if(node.hasAttribute('data-message-search-next'))request(node.dataset.searchCursor);else{abort();if(node.hasAttribute('data-message-search-restart')&&engineChanged){const params=requestParams(value());params.set('lang',form.elements.lang.value);params.set('theme',form.elements.theme.value);params.delete('engineId');window.OieNavigation?.go('/search?'+params);return;}request(node.hasAttribute('data-message-search-restart')?'':lastCursor);}},true);
 const url=new URL(location.href),pending=memory.get('message-search-pending-open');memory.delete('message-search-pending-open');memory.delete('message-search-source-pending');
 if(initial)consume(initial);
 if(native){categories();show(true);}
 else{
  query.placeholder=root.dataset.searchEverything;
  query.setAttribute('aria-label',root.dataset.searchEverything);
  const fromURL=url.searchParams.get('searchMode')==='messages',fromPending=pending?.engine===engine;
  const state=fromPending?pending.state:{q:(url.searchParams.get('searchQuery')||'').trim(),accounts:accountSet(url.searchParams.getAll('searchAccount')),includeEditHistory:url.searchParams.get('includeEditHistory')||'false',searchType:url.searchParams.get('searchType')||''};
  const wanted=(fromURL||fromPending)&&!!state?.q;
  show(wanted);
  if(wanted){
   const same=key(value())===key({...state,engineId:engine,conversationId:''});
   setScope(state);
   if(fromPending)address();
   if(!same||root.dataset.searchQuery!==state.q||root.dataset.searchAccount!==state.accounts.join(',')||root.dataset.searchHistory!==state.includeEditHistory||root.dataset.searchType!==state.searchType){abort();request();}else watch();
  }else{
   restoreTitleQuery();
   if(fromURL)clearAddress();
  }
 }
 life.cleanup(()=>{abort();observer?.disconnect()});
});

window.OieRuntime.register('quick-message-search',life=>{
 const dialog=document.getElementById('oie-quick-search');
 const root=dialog?.querySelector('[data-quick-search]'),app=document.getElementById('oie-app');
 const engine=app?.dataset.engineId;
 if(!root||!engine)return;
 const form=root.querySelector('form'),input=form.elements.q;
 const scroll=root.querySelector('#oie-quick-search-results'),results=root.querySelector('[data-quick-search-items]'),pager=root.querySelector('[data-quick-search-pagination]');
 const status=root.querySelector('[data-quick-search-status]'),retry=root.querySelector('[data-quick-search-retry]');
 let timer,active,lastCursor='',destination;

 function feedback(message='',canRetry=false){
  status.textContent=message;
  status.hidden=!message;
  retry.hidden=!canRetry;
 }
 function abort(){
  clearTimeout(timer);active?.abort();active=null;
  scroll.setAttribute('aria-busy','false');
 }
 function reset(){
  abort();input.value='';lastCursor='';results.replaceChildren();pager.replaceChildren();feedback();
 }
 function modal(action){
  window.dispatchEvent(new CustomEvent('modal:'+action,{detail:{id:dialog.id}}));
 }
 function consume(response,cursor){
  const items=response.querySelector('[data-message-search-items]'),pagination=response.querySelector('[data-message-search-pagination]');
  if(!items||!pagination)throw Error('search response');
  const wasOnMore=pager.contains(document.activeElement);
  let first;
  if(cursor){
   const existing=new Set([...results.querySelectorAll('[data-message-search-hit]')].map(row=>row.dataset.messageSearchHit));
   for(const row of items.children){
    const id=row.dataset.messageSearchHit;
    if(!id||existing.has(id))continue;
    first??=row.querySelector(resultSelector);results.append(row);existing.add(id);
   }
  }else{
   results.replaceChildren(...items.childNodes);
   scroll.scrollTop=0;
  }
  pager.replaceChildren(...pagination.childNodes);
  if(wasOnMore)(first||pager.querySelector(moreSelector)||input).focus();
  document.dispatchEvent(new CustomEvent('oie:message-search-settled',{detail:{root}}));
 }
 async function request(cursor=''){
  if(active||!dialog.open)return;
  const q=input.value.trim();
  if(!q)return;
  if(Array.from(q).length>200){feedback(root.dataset.invalid);return;}
  const state={q,engineId:engine,accounts:[],conversationId:'',includeEditHistory:'false',searchType:''};
  const params=new URLSearchParams({q,engineId:engine,lang:app.dataset.routeLocale,theme:app.dataset.routeTheme,presentation:'compact'});
  if(cursor)params.set('cursor',cursor);
  const controller=new AbortController();
  active=controller;lastCursor=cursor;
  const current=()=>dialog.open&&!life.signal.aborted&&active===controller&&input.value.trim()===q&&document.body.dataset.engineId===engine;
  feedback(root.dataset.loading);scroll.setAttribute('aria-busy','true');
  try{
   const response=await life.fetch('/ui/message-search?'+params,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:controller.signal});
   if(!current())return;
   if(!response.ok){
    if(response.status===409){results.replaceChildren();pager.replaceChildren();feedback(root.dataset.engineChanged);return;}
    if(response.status===400){feedback(root.dataset.invalid);return;}
    throw Error('search unavailable');
   }
   const html=await response.text();
   if(!current())return;
   const result=new DOMParser().parseFromString(html,'text/html').querySelector('[data-message-search-response]');
   if(!searchResponseMatches(result,state,cursor)||result.dataset.searchState!=='ready')throw Error('search scope');
   consume(result,cursor);feedback();
  }catch{
   if(current())feedback(root.dataset.failed,true);
  }finally{
   if(active===controller){active=null;scroll.setAttribute('aria-busy','false');}
  }
 }
 function changed(){
  abort();lastCursor='';results.replaceChildren();pager.replaceChildren();feedback();
  if(input.value.trim())timer=life.timeout(request,300);
 }
 function destinationURL(link){
  const url=new URL(link.href,location.href);
  const current=new URL(location.href);
  const sidebar=document.getElementById('conversation-filter-form');
  const sameChat=url.searchParams.get('chat')===document.body.dataset.conversationId;
  const keys=[...(sidebar?['section','filter','q',...searchKeys]:[]),...(sameChat?chatSearchKeys:[])];
  for(const key of keys){
   url.searchParams.delete(key);
   for(const value of current.searchParams.getAll(key))url.searchParams.append(key,value);
  }
  url.searchParams.set('lang',app.dataset.routeLocale);
  url.searchParams.set('theme',app.dataset.routeTheme);
  return url;
 }
 life.on(document,'keydown',event=>{
  if(event.defaultPrevented||event.isComposing||event.keyCode===229||event.altKey||event.shiftKey||event.ctrlKey===event.metaKey||event.key.toLowerCase()!=='k')return;
  const open=document.querySelector('dialog[open]');
  if(open&&open!==dialog)return;
  event.preventDefault();event.stopPropagation();
  if(dialog.open){input.focus({preventScroll:true});return;}
  if(!event.repeat){reset();modal('open');}
 },true);
 life.on(dialog,'close',()=>{
  if(dialog.open)return;
  const url=destination;destination=null;reset();
  if(url)window.OieNavigation.go(url);
 });
 life.on(dialog,'keydown',event=>{
  if(event.key!=='Escape'||event.isComposing||event.keyCode===229)return;
  event.preventDefault();event.stopPropagation();modal('close');
 },true);
 life.on(window,'popstate',()=>{if(dialog.open)modal('close');});
 life.on(form,'submit',event=>{event.preventDefault();clearTimeout(timer);request();});
 life.on(input,'compositionstart',abort);
 life.on(input,'input',event=>{if(!event.isComposing)changed();});
 life.on(input,'compositionend',changed);
 life.on(root.querySelector('[data-quick-search-close]'),'click',()=>modal('close'));
 life.on(retry,'click',()=>{input.focus({preventScroll:true});request(lastCursor);});
 life.on(dialog,'click',event=>{
  const link=event.target.closest?.(resultSelector+','+moreSelector+',[data-message-search-retry]');
  if(!link||!scroll.contains(link)||event.button!==0||event.ctrlKey||event.metaKey||event.altKey||event.shiftKey)return;
  const url=new URL(link.href,location.href);
  if(url.origin!==location.origin)return;
  event.preventDefault();
  if(link.matches(moreSelector)){request(link.dataset.searchCursor);return;}
  if(link.hasAttribute('data-message-search-retry')){input.focus({preventScroll:true});request();return;}
  if(url.pathname!=='/')return;
  destination=destinationURL(link);modal('close');
 });
 searchResultKeys(life,input,scroll,()=>dialog.open);
 life.cleanup(()=>{abort();if(dialog.open){modal('close');dialog.close();}});
});
})();
