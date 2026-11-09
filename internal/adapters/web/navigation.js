(() => {
 'use strict';
 const runtime=window.OieRuntime;
 let booted=false,epoch=0,currentURL=location.href,infoReturnScroll=0,infoRequestGeneration=0,inboxScroll=0;
 const positions=new Map(),app=()=>document.getElementById('oie-app');
 const scrollIDs=['conversation-list','message-timeline','shared-content-scroll','details-body','accounts-body'];
 const routeFields=['openDrawer','engineId','eventCursor','conversationId','accountId','focusMessage','contextNewer','contextMissing','mock'];
 const kinds=['oie-app','chat-info-content','conversation-pane','shared-content-scroll'];
 const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');
 function remember(){const state={};for(const id of scrollIDs){const node=document.getElementById(id);if(node){state[id]=node.scrollTop;if(id==='conversation-list'&&node.clientHeight>0)inboxScroll=node.scrollTop}}positions.set(currentURL,state);if(positions.size>100)positions.delete(positions.keys().next().value)}
 function routeState(pane){
  const root=app();if(!root)return;
  if(pane){for(const name of [...routeFields,'routeLocale','routeTheme','csrf'])root.dataset[name]=pane.dataset[name]||'';document.getElementById('workspace')?.classList.toggle('oie-chat-open',pane.dataset.openChat==='true');root.querySelector('[data-open-chat]')?.setAttribute('data-open-chat',pane.dataset.openChat||'false');const list=document.getElementById('conversation-list');if(list){if(list.clientHeight>0)list.scrollTop=inboxScroll;}for(const row of root.querySelectorAll('#conversation-list a[data-conversation-id]'))row.setAttribute('aria-current',String(pane.dataset.openChat==='true'&&row.dataset.conversationId===pane.dataset.conversationId));}
  for(const name of routeFields)document.body.dataset[name]=root.dataset[name]||'';
  document.body.dataset.starGeneration='0';document.documentElement.lang=root.dataset.routeLocale;document.documentElement.dataset.mode=root.dataset.routeTheme;document.documentElement.classList.toggle('dark',root.dataset.routeTheme==='dark'||root.dataset.routeTheme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);document.querySelector('meta[name="oie-csrf"]')?.setAttribute('content',root.dataset.csrf||'');
 }
 function shellKey(url){
  const q=new URLSearchParams(url.search);
  for(const key of ['chat','message','open','infoView','infoType','infoCursor','infoQuery','infoFocus','searchQuery','searchAccount','searchConversation','searchCursor','searchEngine','includeEditHistory','searchMode','chatSearch','chatQuery','chatIncludeHistory'])q.delete(key);
  for(const key of [...q.keys()])if(!q.get(key))q.delete(key);
  q.set('section',q.get('section')||'chats');q.set('filter',q.get('filter')||'all');q.set('lang',q.get('lang')||document.documentElement.lang);q.set('theme',q.get('theme')||app()?.dataset.routeTheme||'system');q.sort();return q.toString();
 }
 function conversationRoute(url){return !!document.getElementById('conversation-pane')&&url.origin===location.origin&&url.pathname==='/'&&shellKey(url)===shellKey(new URL(currentURL));}
 function sharedRoute(url){
  if(!document.getElementById('shared-content-scroll')||url.origin!==location.origin||url.pathname!=='/')return false;
  const current=new URL(currentURL);if((url.searchParams.get('section')||'chats')!=='media'||current.searchParams.get('section')!=='media')return false;
  const key=value=>{const q=new URLSearchParams(shellKey(value));q.delete('contentType');q.delete('contentCursor');return q.toString()};
  return key(url)===key(current);
 }
 function sharedFields(){const form=document.getElementById('shared-content-search');return form?JSON.stringify(['q','account'].map(name=>new FormData(form).get(name)||'')):''}
 function swapFor(kind){return kind==='shared-content-scroll'?'innerHTML swap:'+(reducedMotion.matches?'0ms':'75ms')+' settle:0ms show:none':'outerHTML show:none'}
 function selectFor(kind){return '#'+kind+(kind==='shared-content-scroll'?' > *':'')}
 function fragment(kind){return kind==='conversation-pane'?'conversation':kind==='chat-info-content'?'info':'app';}
 function showError(){const region=document.querySelector('[data-navigation-status]');if(region){region.textContent=app()?.dataset.routeError||'';region.hidden=false}}
 function upgrade(root=document){if(!root)return;for(const node of root.querySelectorAll('a[href],form')){
  if(node.matches('[data-global-search-form],[data-open-sender],a[download],a[target]:not([target="_self"])')||node.hasAttribute('hx-get')||node.hasAttribute('hx-post'))continue;
  const form=node.tagName==='FORM';if(form&&(node.method||'get').toLowerCase()!=='get')continue;
  let url;try{url=new URL(form?node.action:node.href,location.href)}catch{continue}
  if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||(!form&&(node.getAttribute('href')||'').startsWith('#')))continue;
  const info=!!node.closest('#chat-info-content')&&(node.matches('form[data-info-search]')||url.searchParams.get('open')==='details'&&url.searchParams.get('chat')===document.body.dataset.conversationId);
  const pane=!form&&node.matches('#conversation-list a[data-conversation-id],.oie-back-button')&&conversationRoute(url);
  const shared=!form&&node.matches('[data-shared-tab]')&&sharedRoute(url);
  const kind=info?'chat-info-content':pane?'conversation-pane':shared?'shared-content-scroll':'oie-app';
  node.setAttribute('hx-boost','true');node.setAttribute('hx-target','#'+kind);node.setAttribute('hx-select',selectFor(kind));node.setAttribute('hx-swap',swapFor(kind));node.setAttribute('hx-push-url','true');node.setAttribute('hx-headers',JSON.stringify({'X-Oie-Fragment':fragment(kind)}));
 }window.htmx?.process(root);}
 // The dependency loader can expose HTMX before Alpine finishes loading.
 // Process the clicked sidebar link before the event reaches its target,
 // without starting application scopes before the remaining dependencies.
 document.addEventListener('click',event=>{
  if(booted||!window.htmx||event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  const link=event.target.closest?.('.oie-navigation a[hx-boost="true"]');
  if(link)window.htmx.process(link);
 },true);
 function infoReady(starGeneration){const root=document.getElementById('chat-info-content');if(!root)return;upgrade(root);document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root,generation:starGeneration}}));const body=document.getElementById('details-body');if(root.dataset.infoView){if(body)body.scrollTop=0;root.querySelector('[data-info-heading]')?.focus({preventScroll:true})}else{if(body)body.scrollTop=infoReturnScroll;const kind=new URL(location.href).searchParams.get('infoFocus');root.querySelector(`[data-info-entry="${CSS.escape(kind||'media')}"]`)?.focus({preventScroll:true})}}
 function closeOverlays(){for(const id of ['oie-image-viewer','oie-document-viewer']){window.dispatchEvent(new CustomEvent('modal:close',{detail:{id}}));const dialog=document.getElementById(id);if(dialog?.open)dialog.close()}for(const id of ['details','accounts','settings','newchat','sender'])window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id}}));}
 document.addEventListener('htmx:before:request',event=>{
  const ctx=event.detail.ctx;if(ctx?.target?.id==='shared-search-results'){epoch++;document.getElementById('shared-content-scroll')?.removeAttribute('aria-busy')}if(!ctx||!kinds.includes(ctx.target?.id))return;
  ctx.oieEpoch=++epoch;ctx.oieKind=ctx.target.id;
  if(ctx.oieKind==='shared-content-scroll'){ctx.oieSharedFields=sharedFields();ctx.target.setAttribute('aria-busy','true')}ctx.oieStarGeneration=Number(document.body.dataset.starGeneration||0);
  if(ctx.oieKind==='conversation-pane'){ctx.oieScope=document.getElementById('conversation-pane')?.dataset.conversationScope;const url=new URL(ctx.request.action,location.href);ctx.oieChat=url.searchParams.get('chat')||'';}
  const region=document.querySelector('[data-navigation-status]');if(region){region.textContent=app()?.dataset.routeLoading||'';region.hidden=false}
  if(ctx.oieKind==='chat-info-content'&&!document.getElementById('chat-info-content')?.dataset.infoView)infoReturnScroll=document.getElementById('details-body')?.scrollTop||0;
 });
 document.addEventListener('htmx:after:request',event=>{
  const ctx=event.detail.ctx;if(!ctx?.oieKind)return;if(ctx.oieEpoch!==epoch){event.preventDefault();return}
  const parsed=new DOMParser().parseFromString(ctx.text||'','text/html'),next=parsed.querySelector('#'+ctx.oieKind);
  let valid=!!next;
  if(ctx.oieKind==='shared-content-scroll'){valid=valid&&ctx.response?.status>=200&&ctx.response.status<300&&parsed.querySelector('#oie-app')?.dataset.engineId===document.body.dataset.engineId&&sharedFields()===ctx.oieSharedFields;ctx.oieSharedPage=parsed}
  if(ctx.oieKind==='chat-info-content')valid=valid&&next.dataset.detailsConversation===document.body.dataset.conversationId;
  if(ctx.oieKind==='conversation-pane')valid=valid&&ctx.response?.status>=200&&ctx.response.status<300&&next.dataset.conversationScope===ctx.oieScope&&next.dataset.engineId===document.body.dataset.engineId&&(ctx.oieChat?next.dataset.conversationId===ctx.oieChat:next.dataset.openChat==='false');
  if(!valid){event.preventDefault();ctx.target?.removeAttribute('aria-busy');showError();}
 });
 document.addEventListener('htmx:before:swap',event=>{
  const ctx=event.detail.ctx;if(!ctx?.oieKind)return;
  if(ctx.oieEpoch!==epoch||!document.getElementById(ctx.oieKind)||ctx.oieKind==='shared-content-scroll'&&sharedFields()!==ctx.oieSharedFields){event.preventDefault();return}
  remember();ctx.target=document.getElementById(ctx.oieKind);
  if(ctx.oieKind==='shared-content-scroll'){closeOverlays();window.OieVideos?.release(ctx.target);ctx.target.dataset.sharedChanging='true';ctx.target.oieSharedPage=ctx.oieSharedPage;}
  if(ctx.oieKind==='oie-app'||ctx.oieKind==='conversation-pane'){if(ctx.oieKind==='conversation-pane')closeOverlays();runtime.stop();}else infoRequestGeneration=ctx.oieStarGeneration;
 });
 document.addEventListener('htmx:after:settle',event=>{
  const root=event.detail.task?.target;if(!root||!root.isConnected||root!==document.getElementById(root.id)||!kinds.includes(root.id))return;
  if(root.id==='shared-content-scroll'){
   const page=root.oieSharedPage;delete root.oieSharedPage;delete root.dataset.sharedChanging;root.removeAttribute('aria-busy');
   const type=page?.querySelector('#shared-content-results')?.dataset.contentType;
   if(type){const field=document.querySelector('#shared-content-search input[name=contentType]');if(field)field.value=type;for(const tab of document.querySelectorAll('#shared-search-results [data-shared-tab]'))tab.setAttribute('aria-current',String(tab.dataset.sharedTab===type));}
   const region=document.getElementById('shared-search-results'),fresh=page?.querySelector('#shared-search-results');if(region&&fresh)region.dataset.searchQuery=fresh.dataset.searchQuery||'';
   document.dispatchEvent(new CustomEvent('oie:shared-search-settled',{detail:{root:region,page}}));
   document.dispatchEvent(new CustomEvent('oie:previews-refresh',{detail:page}));document.dispatchEvent(new CustomEvent('oie:stars-refresh',{detail:{page,generation:Number(document.body.dataset.starGeneration||0)}}));upgrade(app());
  }
  else if(root.id==='chat-info-content')infoReady(infoRequestGeneration);
  else{
   if(root.id==='oie-app')inboxScroll=0;routeState(root.id==='conversation-pane'?root:null);upgrade(app());if(booted)runtime.start();const saved=positions.get(location.href);
   requestAnimationFrame(()=>{if(!root.isConnected||root!==document.getElementById(root.id))return;if(saved)for(const [id,top]of Object.entries(saved)){if(root.id==='conversation-pane'&&id!=='message-timeline')continue;if(id==='message-timeline'&&new URL(location.href).searchParams.has('message'))continue;const node=document.getElementById(id);if(node)node.scrollTop=top}const info=document.getElementById('chat-info-content');if(info)document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root:info,generation:Number(document.body.dataset.starGeneration||0)}}));});
  }
  currentURL=location.href;const status=document.querySelector('[data-navigation-status]');if(status)status.hidden=true;
 });
 document.addEventListener('htmx:error',event=>{if(event.detail.ctx?.oieKind&&event.detail.ctx.oieEpoch===epoch){event.detail.ctx.target?.removeAttribute('aria-busy');showError()}});
 function go(value,history=false){
  const url=new URL(value,location.href);if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||!window.htmx){location.assign(url.href);return}
  const kind=sharedRoute(url)?'shared-content-scroll':conversationRoute(url)?'conversation-pane':'oie-app';
  return window.htmx.ajax('GET',url.pathname+url.search,{source:app(),target:document.getElementById(kind),swap:swapFor(kind),select:selectFor(kind),push:history?'false':url.pathname+url.search,headers:{'X-Oie-Fragment':fragment(kind),...(history?{'HX-History-Restore-Request':'true'}:{})}});
 }
 // Turn an implicitly selected desktop conversation into an addressable one
 // before a command can remove its row from the current list. This is the same
 // view and history entry, so keep its mounted nodes, scroll and runtime.
 function pinConversation(id){
  const pane=document.getElementById('conversation-pane'),root=app(),url=new URL(location.href);
  if(!id||url.pathname!=='/'||!pane||pane.dataset.conversationId!==id||document.body.dataset.conversationId!==id)return false;
  const selected=url.searchParams.get('chat');if(selected)return selected===id;
  remember();const saved=positions.get(currentURL);positions.delete(currentURL);
  url.searchParams.set('chat',id);history.replaceState(history.state,'',url.pathname+url.search+url.hash);currentURL=location.href;if(saved)positions.set(currentURL,saved);
  pane.dataset.openChat='true';document.getElementById('workspace')?.classList.add('oie-chat-open');
  if(root)for(const row of root.querySelectorAll('#conversation-list a[data-conversation-id]'))row.setAttribute('aria-current',String(row.dataset.conversationId===id));
  return true;
 }
 document.addEventListener('htmx:before:history:restore',event=>{event.preventDefault();go(event.detail.path,true);});
 function addressSearch(url,push=false){remember();history[push?'pushState':'replaceState'](history.state,'',url.pathname+url.search+url.hash);currentURL=location.href;}
 window.OieNavigation={go,pinConversation,addressSearch};
 document.addEventListener('oie:navigation-upgrade',()=>upgrade(app()));
 document.addEventListener('oie:shared-search-settled',()=>{currentURL=location.href;});
 reducedMotion.addEventListener('change',()=>upgrade(app()));
 async function ready(){await window.goshtosoDependencies?.ready;booted=true;routeState();upgrade(app());runtime.start();}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ready,{once:true});else ready();
})();
