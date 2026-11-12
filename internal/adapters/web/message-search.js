window.OieRuntime.register('message-search',life=>{
 'use strict';
 const root=document.querySelector('[data-message-search-root]');if(!root)return;
 const native=root.dataset.native==='true',form=root.querySelector('#message-search-form'),query=native?form.elements.q:document.getElementById('conversation-search');if(!query)return;
 for(const fieldset of root.querySelectorAll('[data-message-search-native-accounts]'))fieldset.disabled=true;
 const slot=document.getElementById('message-search-slot'),original=document.getElementById('conversation-list-default'),filterForm=document.getElementById('conversation-filter-form'),close=document.querySelector('[data-message-search-close]'),originalQuery=root.dataset.titleQuery||'';
 const account=()=>form.querySelector('input[name="accountId"]'),historyField=form.elements.includeEditHistory,conversation=form.elements.conversationId;
 const results=root.querySelector('#message-search-results'),pagination=root.querySelector('#message-search-pagination'),buffer=root.querySelector('#message-search-page-buffer'),scroll=root.querySelector('#message-search-scroll'),feedback=root.querySelector('#message-search-feedback'),loading=root.querySelector('[data-message-search-loading]');
 const engine=form.elements.engineId.value,memory=window.OieRuntime.memory,searchKeys=['searchMode','searchQuery','searchAccount','searchConversation','searchCursor','searchEngine','includeEditHistory'];
 let epoch=0,timer=0,active=null,busy=false,next='',lastCursor='',opened=native,observer=null,engineChanged=false;
 const value=()=>({q:query.value.trim(),accountId:account()?.value||'',conversationId:conversation.value,includeEditHistory:String(historyField.checked),engineId:engine});
 const key=s=>JSON.stringify([s.engineId,s.q,s.accountId,s.conversationId,s.includeEditHistory]);
 const valid=()=>!life.signal.aborted&&root.isConnected&&document.body.dataset.engineId===engine;
 function abort(){observer?.disconnect();epoch++;if(timer)clearTimeout(timer);timer=0;active?.request?.abort?.();active=null;busy=false;loading.hidden=true;results.removeAttribute('aria-busy');}
 function status(text,restart=false){feedback.textContent=text||'';feedback.hidden=!text;for(const node of root.querySelectorAll('button[data-message-search-retry]'))node.hidden=!text||restart;for(const node of root.querySelectorAll('button[data-message-search-restart]'))node.hidden=!text||!restart;}
 function applyURL(url,state){for(const name of searchKeys)url.searchParams.delete(name);url.searchParams.set('searchMode','messages');for(const [name,val]of [['searchQuery',state.q],['searchAccount',state.accountId],['searchEngine',state.engineId],['includeEditHistory',state.includeEditHistory==='true'?'true':'']])if(val)url.searchParams.set(name,val);}
 function address(push=false){const state=value(),url=new URL(location.href);if(native){url.search='';for(const [name,val]of Object.entries(state))if(val&&val!=='false')url.searchParams.set(name,val);url.searchParams.set('lang',form.elements.lang.value);url.searchParams.set('theme',form.elements.theme.value)}else applyURL(url,state);window.OieNavigation?.addressSearch(url,push);}
 function show(active){opened=active;root.dataset.searchOpen=String(active);if(slot)slot.hidden=!active;if(original)original.hidden=active;if(close)close.hidden=!active;filterForm?.classList.toggle('oie-global-search-active',active);}
 function rememberPending(){if(memory.get('message-search-source-pending')===engine)memory.set('message-search-pending-open',{engine,state:value()});}
 function dates(){for(const node of root.querySelectorAll('[data-message-search-time]')){const date=new Date(node.dateTime);if(Number.isFinite(date.getTime()))node.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{dateStyle:'medium',timeStyle:'short'}).format(date);}}
 function watch(){observer?.disconnect();next=pagination.querySelector('[data-message-search-next]')?.dataset.searchCursor||'';if(!next||!opened)return;observer=new IntersectionObserver(entries=>{if(opened&&entries.some(e=>e.isIntersecting)&&!busy&&!feedback.textContent)request(next)},{root:scroll,rootMargin:'200px'});observer.observe(pagination);}
 function consume(response,append=false){const items=response.querySelector('[data-message-search-items]'),pager=response.querySelector('[data-message-search-pagination]');if(!items||!pager)return false;for(const name of ['searchQuery','searchAccount','searchConversation','searchHistory','searchState'])root.dataset[name]=response.dataset[name]||'';const seen=new Set([...results.querySelectorAll('[data-message-search-hit]')].map(n=>n.dataset.messageSearchHit));if(append){for(const node of items.children){const id=node.dataset.messageSearchHit;if(id&&seen.has(id))continue;results.append(node.cloneNode(true));if(id)seen.add(id)}}else{results.replaceChildren(...[...items.childNodes].map(n=>n.cloneNode(true)));scroll.scrollTop=0;}pagination.replaceChildren(...[...pager.childNodes].map(n=>n.cloneNode(true)));dates();watch();return true;}
 function blank(){consume(root.querySelector('template[data-message-search-blank]').content.querySelector('[data-message-search-response]'));pagination.replaceChildren();next='';status('');}
 function request(cursor=''){if(!valid()||busy||!opened)return;const state=value();if(!state.q){blank();return}if(Array.from(state.q).length>200){status(root.dataset.searchInvalid);for(const node of root.querySelectorAll('button[data-message-search-retry]'))node.hidden=true;return;}lastCursor=cursor;status('');form.elements.q.value=query.value;const params=new URLSearchParams({...state,limit:form.elements.limit.value,lang:form.elements.lang.value});if(cursor)params.set('cursor',cursor);window.htmx.ajax('GET','/ui/message-search?'+params,{source:form,target:buffer,select:'[data-message-search-response]',swap:'innerHTML show:none'});}
 function categories(){for(const link of root.querySelectorAll('[data-message-search-category]')){const url=new URL(link.href,location.href);for(const name of [...searchKeys,'chatSearch','chatQuery','chatIncludeHistory','chat','message'])url.searchParams.delete(name);url.searchParams.set('q',query.value.trim());url.searchParams.set('account',account()?.value||'');link.href=url.pathname+url.search;}}
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
 life.on(document,'htmx:before:request',event=>{const ctx=event.detail.ctx;if(ctx?.target!==buffer)return;if(!valid()||!opened){event.preventDefault();return}ctx.searchEpoch=epoch;ctx.searchKey=key(value());ctx.searchCursor=new URL(ctx.request.action,location.href).searchParams.get('cursor')||'';active=ctx;busy=true;loading.hidden=false;results.setAttribute('aria-busy','true');});
 life.on(document,'htmx:after:request',event=>{const ctx=event.detail.ctx;if(ctx?.target!==buffer)return;if(!valid()||!opened||ctx.searchEpoch!==epoch||ctx.searchKey!==key(value())){event.preventDefault();return}const parsed=new DOMParser().parseFromString(ctx.text||'','text/html'),response=parsed.querySelector('[data-message-search-response]'),s=value();const matches=response&&response.dataset.searchEngine===engine&&response.dataset.searchQuery===s.q&&response.dataset.searchAccount===s.accountId&&response.dataset.searchConversation===s.conversationId&&response.dataset.searchHistory===s.includeEditHistory&&response.dataset.searchCursor===ctx.searchCursor;if(!matches||ctx.response?.status<200||ctx.response?.status>=300){event.preventDefault();busy=false;loading.hidden=true;results.removeAttribute('aria-busy');const code=ctx.response?.status;engineChanged=code===409;if(engineChanged){results.replaceChildren();pagination.replaceChildren();next='';observer?.disconnect();root.dataset.searchState='engine_changed';}status(code===409?root.dataset.searchEngineChanged:code===400?root.dataset.searchInvalid:root.dataset.searchFailed,code===400||code===409);return;}engineChanged=false;ctx.searchResponse=response;});
 life.on(document,'htmx:before:swap',event=>{const ctx=event.detail.ctx;if(ctx?.target===buffer&&(!valid()||!opened||ctx.searchEpoch!==epoch||ctx.searchKey!==key(value())||!ctx.searchResponse))event.preventDefault();});
 life.on(document,'htmx:after:settle',event=>{if(event.detail.task?.target!==buffer||!active?.searchResponse||active.searchEpoch!==epoch||!valid()||!opened)return;const ctx=active;busy=false;active=null;loading.hidden=true;results.removeAttribute('aria-busy');consume(ctx.searchResponse,!!ctx.searchCursor);buffer.replaceChildren();document.dispatchEvent(new CustomEvent('oie:message-search-settled',{detail:{root}}));});
 life.on(document,'htmx:error',event=>{const ctx=event.detail.ctx;if(ctx?.target===buffer&&ctx.searchEpoch===epoch&&valid()&&opened){busy=false;active=null;loading.hidden=true;results.removeAttribute('aria-busy');status(root.dataset.searchFailed);}});
 life.on(query,'compositionstart',()=>abort());life.on(query,'input',event=>{if(!event.isComposing)changed(300)});life.on(query,'compositionend',()=>changed(300));
 for(const node of [form,filterForm])life.on(node,'submit',event=>{event.preventDefault();changed()});life.on(historyField,'change',()=>changed());
 life.on(document,'combobox:change',event=>{if(event.detail?.id!=='message-search-account')return;conversation.value='';changed();});
 function setScope(state){query.value=state.q||'';form.elements.q.value=query.value;historyField.checked=state.includeEditHistory==='true';conversation.value=native?(state.conversationId||''):'';document.dispatchEvent(new CustomEvent('combobox:load',{detail:{id:'message-search-account',values:[state.accountId||'']}}));categories();}
 function restoreTitleQuery(){query.value=originalQuery;form.elements.q.value=originalQuery;categories();}
 function clearAddress(){
  const url=new URL(location.href);
  for(const name of searchKeys)url.searchParams.delete(name);
  window.OieNavigation?.addressSearch(url);
 }
 function exit(){
  abort();show(false);blank();restoreTitleQuery();clearAddress();
  if(memory.get('message-search-source-pending')===engine)memory.set('message-search-pending-open',{engine,state:null});
  query.focus({preventScroll:true});
 }
 life.on(document,'keydown',event=>{if(!native&&opened&&event.key==='Escape'&&event.target===query){event.preventDefault();event.stopImmediatePropagation();exit();}},true);
 life.on(document,'click',event=>{const node=event.target.closest?.('[data-message-search-close],[data-message-search-source],[data-message-search-conversation],[data-message-search-contact],[data-message-search-next],[data-message-search-retry],[data-message-search-restart],[data-message-search-clear-scope]');if(!node||!root.contains(node)&&node!==close||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;event.preventDefault();event.stopImmediatePropagation();if(node===close){exit();return}if(node.hasAttribute('data-message-search-source')||node.hasAttribute('data-message-search-conversation')||node.hasAttribute('data-message-search-contact')){const url=new URL(node.href,location.href);if(!native){applyURL(url,value());if(originalQuery)url.searchParams.set('q',originalQuery);else url.searchParams.delete('q');url.searchParams.set('filter',root.dataset.inboxFilter||'all');url.searchParams.set('section',root.dataset.inboxSection||'chats');}if(url.searchParams.get('chat')!==document.body.dataset.conversationId)for(const name of ['chatSearch','chatQuery','chatIncludeHistory'])url.searchParams.delete(name);memory.set('message-search-source-pending',engine);rememberPending();const finished=()=>{if(life.signal.aborted)return;memory.delete('message-search-source-pending');memory.delete('message-search-pending-open');if(opened)address();};Promise.resolve(window.OieNavigation?.go(url)).then(finished,finished);return}if(node.hasAttribute('data-message-search-clear-scope')){conversation.value='';root.querySelector('[data-message-search-scope]').hidden=true;changed();return}if(node.hasAttribute('data-message-search-next'))request(node.dataset.searchCursor);else{abort();if(node.hasAttribute('data-message-search-restart')&&engineChanged){const params=new URLSearchParams({...value(),lang:form.elements.lang.value,theme:form.elements.theme.value});params.delete('engineId');window.OieNavigation?.go('/search?'+params);return;}request(node.hasAttribute('data-message-search-restart')?'':lastCursor);}},true);
 const url=new URL(location.href),pending=memory.get('message-search-pending-open');memory.delete('message-search-pending-open');memory.delete('message-search-source-pending');
 const initial=results.querySelector('[data-message-search-response]');if(initial)consume(initial);
 if(native){categories();show(true);dates();}
 else{
  query.placeholder=root.dataset.searchEverything;
  query.setAttribute('aria-label',root.dataset.searchEverything);
  const fromURL=url.searchParams.get('searchMode')==='messages',fromPending=pending?.engine===engine;
  const state=fromPending?pending.state:{q:(url.searchParams.get('searchQuery')||'').trim(),accountId:url.searchParams.get('searchAccount')||'',includeEditHistory:url.searchParams.get('includeEditHistory')||'false'};
  const wanted=(fromURL||fromPending)&&!!state?.q;
  show(wanted);
  if(wanted){
   const same=key(value())===key({...state,engineId:engine,conversationId:''});
   setScope(state);
   if(fromPending)address();
   if(!same||root.dataset.searchQuery!==state.q||root.dataset.searchAccount!==state.accountId||root.dataset.searchHistory!==state.includeEditHistory){abort();request();}else watch();
  }else{
   restoreTitleQuery();
   if(fromURL)clearAddress();
  }
 }
 life.cleanup(()=>{abort();observer?.disconnect()});
});
