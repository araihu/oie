(() => {
 'use strict';
 const runtime=window.OieRuntime;
 let booted=false,epoch=0,currentURL=location.href,infoReturnScroll=0,infoRequestGeneration=0,inboxScroll=0;
 let restoreFilterFocus=false,filterScroll=0;
 let pendingConversation, settling, historyRequest;
 const positions=new Map(),app=()=>document.getElementById('oie-app');
 const scrollIDs=['conversation-list','message-timeline','shared-content-scroll','details-body','accounts-body','account-detail-scroll','workspace'];
 const routeFields=['openDrawer','engineId','eventCursor','conversationId','accountId','focusMessage','contextNewer','contextMissing','mock'];
 const inboxID='conversation-list-default';
 const kinds=['oie-app','chat-info-content','conversation-pane','shared-content-scroll','shared-search-results','account-workspace','appearance-settings','scheduled-results','schedule-editor','message-timeline',inboxID];
 const chatQueryKeys=['message','timelineCursor','timelineDirection','chatSearch','chatQuery','chatIncludeHistory'];
 const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');
 function stopConversationLoading(){
  if(!pendingConversation)return;
  const {timer,pane,overlay,covered,focus}=pendingConversation;
  pendingConversation=null;
  clearTimeout(timer);
  for(const node of covered??[])node.inert=false;
  const restoreFocus=focus?.isConnected&&(document.activeElement===document.body||overlay.contains(document.activeElement));
  overlay.hidden=true;
  if(overlay.parentElement!==pane)pane.append(overlay);
  if(restoreFocus)focus.focus({preventScroll:true});
 }
 function startConversationLoading(ctx){
  const pane=ctx.target,overlay=pane.querySelector('[data-conversation-loading]');
  if(!overlay)return;
  const pending={ctx,pane,overlay};
  pendingConversation=pending;
  pending.timer=setTimeout(()=>{
   if(!overlay.isConnected||ctx.oieEpoch!==epoch)return;
   const chat=pane.querySelector('.oie-chat-pane');
   const visible=chat.getClientRects().length>0;
   const timeline=visible&&chat.querySelector('.oie-timeline-frame');
   const covered=visible?chat:document.querySelector('#workspace > .oie-list-pane');
   if(covered?.contains(document.activeElement))pending.focus=document.activeElement;
   if(timeline){
    // Keep the real header and composer in place. The skeleton must be
    // outside the inert content so its status stays available.
    timeline.append(overlay);
    pending.covered=[...chat.children].flatMap(node=>node===timeline?[timeline.firstElementChild]:[node]);
    for(const node of pending.covered)node.inert=true;
   }else if(covered){covered.inert=true;pending.covered=[covered];}
   overlay.hidden=false;
   if(!timeline&&pending.focus)overlay.querySelector('[data-cancel-conversation-loading]').focus({preventScroll:true});
  },250);
 }
 function cancelConversationLoading(){
  if(!pendingConversation)return;
  const {ctx}=pendingConversation;
  epoch++;
  stopConversationLoading();
  ctx.request.abort();
 }
 function remember(){const state={};for(const id of scrollIDs){const node=document.getElementById(id);if(node){state[id]=node.scrollTop;if(id==='conversation-list'&&node.clientHeight>0)inboxScroll=node.scrollTop}}positions.set(currentURL,state);if(positions.size>100)positions.delete(positions.keys().next().value)}
 function routeState(pane){
  const root=app();if(!root)return;
  if(pane){for(const name of [...routeFields,'routeLocale','routeTheme','csrf'])root.dataset[name]=pane.dataset[name]||'';document.getElementById('workspace')?.classList.toggle('oie-chat-open',pane.dataset.openChat==='true');root.querySelector('[data-open-chat]')?.setAttribute('data-open-chat',pane.dataset.openChat||'false');const list=document.getElementById('conversation-list');if(list){if(list.clientHeight>0)list.scrollTop=inboxScroll;}for(const row of root.querySelectorAll('#conversation-list a[data-conversation-id]'))row.setAttribute('aria-current',String(pane.dataset.openChat==='true'&&row.dataset.conversationId===pane.dataset.conversationId));}
  for(const name of routeFields)document.body.dataset[name]=root.dataset[name]||'';
  document.body.dataset.starGeneration='0';document.documentElement.lang=root.dataset.routeLocale;document.documentElement.dataset.mode=root.dataset.routeTheme;document.documentElement.classList.toggle('dark',root.dataset.routeTheme==='dark'||root.dataset.routeTheme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);document.querySelector('meta[name="oie-csrf"]')?.setAttribute('content',root.dataset.csrf||'');
 }
 function shellKey(url, ignored=[]){
  const q=new URLSearchParams(url.search);
  for(const key of ['chat','inbox','message','open','infoView','infoType','infoCursor','infoQuery','infoFocus','searchQuery','searchAccount','searchType','searchConversation','searchCursor','searchEngine','includeEditHistory','searchMode','chatSearch','chatQuery','chatIncludeHistory',...ignored])q.delete(key);
  for(const key of [...q.keys()])if(!q.get(key))q.delete(key);
  q.set('section',q.get('section')||'chats');q.set('filter',q.get('filter')||'all');q.set('lang',q.get('lang')||document.documentElement.lang);q.set('theme',q.get('theme')||app()?.dataset.routeTheme||'system');q.sort();return q.toString();
 }
 function conversationRoute(url){return !!document.getElementById('conversation-pane')&&url.origin===location.origin&&url.pathname==='/'&&shellKey(url)===shellKey(new URL(currentURL));}
 function inboxURL(url){
  const result=new URL(url),pane=document.getElementById('conversation-pane');
  if(!result.searchParams.has('chat')&&!result.searchParams.has('inbox')&&pane?.dataset.openChat==='false'){
   if(pane.dataset.conversationId)result.searchParams.set('chat',pane.dataset.conversationId);
   result.searchParams.set('inbox','1');
  }
  return result;
 }
 function inboxRoute(url){
  if(!document.getElementById(inboxID)||url.origin!==location.origin||url.pathname!=='/')return false;
  const key=value=>{
   const q=inboxURL(value).searchParams;
   q.delete('filter');
   for(const name of [...q.keys()])if(!q.get(name))q.delete(name);
   q.set('section',q.get('section')||'chats');
   q.set('lang',q.get('lang')||document.documentElement.lang);
   q.set('theme',q.get('theme')||app()?.dataset.routeTheme||'system');
   q.sort();
   return q.toString();
  };
  return key(url)===key(new URL(currentURL));
 }
 function syncFilterLinks(){
  const pane=document.getElementById('conversation-pane'),current=new URL(location.href);
  if(!pane)return;
  for(const link of document.querySelectorAll('#'+inboxID+' a[data-filter]')){
   const url=new URL(link.href);
   if(pane.dataset.conversationId)url.searchParams.set('chat',pane.dataset.conversationId);
   else url.searchParams.delete('chat');
   if(pane.dataset.openChat==='true')url.searchParams.delete('inbox');
   else url.searchParams.set('inbox','1');
   for(const name of chatQueryKeys){
    const value=current.searchParams.get(name);
    if(value)url.searchParams.set(name,value);
    else url.searchParams.delete(name);
   }
   link.href=url.pathname+url.search;
  }
 }
 function syncInboxScope(root,previousFilter){
  const filter=root.dataset.inboxFilter, url=new URL(location.href);
  const field=document.querySelector('#conversation-filter-form input[name="filter"]');
  if(field)field.value=filter;
  const search=document.querySelector('[data-message-search-root]');
  if(search)search.dataset.inboxFilter=filter;
  for(const node of document.querySelectorAll('#conversation-pane a[href],#conversation-pane input[name="returnTo"]')){
   const link=node.tagName==='A',href=link?node.getAttribute('href'):node.value;
   if(!href?.startsWith('/'))continue;
   const target=new URL(href,location.href),params=target.searchParams;
   if(target.origin!==location.origin||target.pathname!=='/'||(params.get('filter')||'all')!==previousFilter)continue;
   if(['section','account','q'].some(name=>(params.get(name)||'')!==(url.searchParams.get(name)||'')))continue;
   if(filter==='all')params.delete('filter');else params.set('filter',filter);
   if(link)node.href=target.pathname+target.search+target.hash;
   else node.value=target.pathname+target.search+target.hash;
  }
  syncFilterLinks();
 }
 function sameRoute(url, ignored) {
  const key=value=>{
   const q=new URLSearchParams(value.search);
   for(const name of ignored)q.delete(name);
   for(const name of [...q.keys()])if(!q.get(name))q.delete(name);
   q.set('section',q.get('section')||'chats');q.set('filter',q.get('filter')||'all');
   q.set('lang',q.get('lang')||document.documentElement.lang);
   q.set('theme',q.get('theme')||app()?.dataset.routeTheme||'system');
   q.sort();return q.toString();
  };
  return url.pathname===new URL(currentURL).pathname&&key(url)===key(new URL(currentURL));
 }
 function sharedRoute(url, ignored=[]) {
  return !!document.getElementById('shared-content-scroll')&&sameRoute(url,['contentType','contentCursor',...ignored]);
 }
 function scopedKind(url, node) {
  const params=url.searchParams,current=new URL(currentURL).searchParams;
  const pane=document.getElementById('conversation-pane');
  const chat=params.get('chat')||(!node&&!current.has('chat')?pane?.dataset.conversationId:'');
  const sameChat=!!pane?.dataset.conversationId&&chat===pane.dataset.conversationId&&!params.has('inbox');
  const localInfo=node?.closest('#chat-info-content');
  const detailsHistory=!node&&(params.get('open')==='details'||current.get('open')==='details')&&sameRoute(url,['open','infoView','infoType','infoQuery','infoCursor','infoFocus','chat']);
  if(sameChat&&(params.get('open')==='details'&&localInfo||detailsHistory))return 'chat-info-content';
  if(document.getElementById('account-workspace')&&(params.get('open')==='accounts'||!node&&current.get('open')==='accounts'&&!params.has('open'))&&sameRoute(url,['open','accountPage','managedAccount','accountFocus']))return 'account-workspace';
  if(sameChat&&document.getElementById('schedule-editor')&&params.get('editSchedule')!==current.get('editSchedule')&&sameRoute(url,['editSchedule','open']))return 'schedule-editor';
  if(document.getElementById('scheduled-results')&&params.get('section')==='scheduled'&&(!node||node.matches('[data-agenda-next]'))&&sameRoute(url,['scheduleCursor','scheduleConversation']))return 'scheduled-results';
  if(sharedRoute(url,['q','account','contentAccount','contentConversation'])&&(!node||node.id==='shared-content-search'||node.closest('#shared-search-results'))){
   return sharedRoute(url,node?.matches('[data-shared-tab]')?['q']:[])?'shared-content-scroll':'shared-search-results';
  }
  if(!node&&sameChat&&document.getElementById('message-timeline')&&!params.has('message')&&!params.has('timelineCursor')&&(current.has('message')||current.has('timelineCursor')||document.body.dataset.focusMessage)&&sameRoute(url,['message','timelineCursor','timelineDirection']))return 'message-timeline';
 }
 function syncSharedAccount(params){
  const picker=document.getElementById('shared-account');
  const values=picker?.dataset.comboboxName==='contentAccount'?params.getAll('contentAccount'):[params.get('account')||''];
  picker?.dispatchEvent(new CustomEvent('combobox:load',{
   bubbles:true,detail:{id:'shared-account',values}
  }));
 }
 function reconcileSharedAccount(page,params,restoreSelection){
  const picker=document.getElementById('shared-account'),next=page.getElementById('shared-account');
  if(picker&&next&&picker.dataset.comboboxName!==next.dataset.comboboxName){
   const restoreFocus=picker.contains(document.activeElement);
   window.Alpine?.destroyTree(picker);
   picker.replaceWith(next);
   window.Alpine?.initTree(next);
   if(restoreFocus)next.querySelector('[role="combobox"]')?.focus({preventScroll:true});
  }else if(restoreSelection)syncSharedAccount(params);
 }
 function sharedFields(){
  const form=document.getElementById('shared-content-search');if(!form)return '';
  const fields=new FormData(form);return JSON.stringify([fields.get('q'),fields.get('account'),fields.get('contentConversation'),fields.getAll('contentAccount').sort()]);
 }
 function swapFor(kind){
  if(kind==='shared-content-scroll')return 'innerHTML swap:'+(reducedMotion.matches?'0ms':'75ms')+' settle:0ms show:none';
  if(kind==='message-timeline')return 'innerHTML settle:0ms show:none';
  if(kind==='conversation-pane'||kind==='schedule-editor')return 'outerHTML settle:0ms show:none';
  return 'outerHTML show:none';
 }
 function selectFor(kind){return '#'+kind+(['shared-content-scroll','message-timeline'].includes(kind)?' > *':'')}
 const fragments={
  [inboxID]:'inbox','conversation-pane':'conversation','chat-info-content':'info',
  'shared-content-scroll':'shared','shared-search-results':'shared','account-workspace':'accounts',
  'appearance-settings':'appearance','scheduled-results':'scheduled','schedule-editor':'schedule-editor','message-timeline':'timeline',
 };
 function fragment(kind){return fragments[kind]||'app'}
 function applyAppearance(mode, history=false){
  const root=document.documentElement;
  root.dataset.mode=mode;
  root.classList.toggle('dark',mode==='dark'||mode==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);
  app().dataset.routeTheme=mode;
  const pane=document.getElementById('conversation-pane');
  if(pane)pane.dataset.routeTheme=mode;
  const update=value=>{
   const url=new URL(value,location.href);
   if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||!url.searchParams.has('theme'))return value;
   url.searchParams.set('theme',mode);return url.pathname+url.search+url.hash;
  };
  for(const node of app().querySelectorAll('a[href*="theme="]'))node.setAttribute('href',update(node.getAttribute('href')));
  for(const field of app().querySelectorAll('input[type=hidden][name="theme"]'))field.value=mode;
  for(const field of app().querySelectorAll('input[name="returnTo"]'))field.value=update(field.value);
  const editor=document.getElementById('schedule-editor');
  if(editor)editor.dataset.returnTo=update(editor.dataset.returnTo);
  document.dispatchEvent(new Event('oie:appearance-changed'));
  if(!history){const url=new URL(location.href);url.searchParams.set('theme',mode);url.searchParams.set('open','settings');addressSearch(url,true);}
  else currentURL=location.href;
  const open=new URL(location.href).searchParams.get('open')==='settings';
  window.dispatchEvent(new CustomEvent(open?'drawer:open':'drawer:close',{detail:{id:'settings'}}));
  if(open)document.querySelector('#appearance-settings input:checked')?.focus({preventScroll:true});
 }
 function accountReady(root, previousURL) {
  const panel=root.closest('[role=dialog]');
  const previous=panel?.dataset.oieAccountPanelClass;
  const classes=root.dataset.panelClass.split(' ').filter(Boolean);
  if(previous)panel.classList.remove(...previous.split(' ').filter(Boolean));
  else panel?.classList.remove('oie-account-list-drawer','oie-account-detail-drawer','oie-capabilities-drawer');
  panel?.classList.add(...classes);
  if(panel)panel.dataset.oieAccountPanelClass=root.dataset.panelClass;
  const query=new URLSearchParams(location.search),body=document.getElementById('accounts-body');
  if(query.get('open')!=='accounts'){
   window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id:'accounts'}}));
   return;
  }
  if(new URL(previousURL).searchParams.get('open')!=='accounts'||!panel?.getClientRects().length)window.dispatchEvent(new CustomEvent('drawer:open',{detail:{id:'accounts'}}));
  const focus=query.get('accountPage')==='capabilities'?'[data-account-capabilities-title]':query.get('accountFocus')==='capabilities'?'[data-account-capabilities]':'[data-account-back],[data-account-add]';
  const saved=positions.get(location.href);
  if(body)body.scrollTop=saved?.['accounts-body']||0;
  const detail=document.getElementById('account-detail-scroll');
  if(detail)detail.scrollTop=saved?.['account-detail-scroll']||0;
  const target=root.querySelector(focus);
  target?.focus({preventScroll:true});
  document.dispatchEvent(new CustomEvent('oie:accounts-content',{detail:{root}}));
 }
 function conversationResponse(ctx){
  if(!(ctx.response?.status>=200&&ctx.response.status<300))return false;
  // Validate the fragment root before HTMX updates history. HTMX parses the
  // timeline itself when it swaps, so only read the opening tag here.
  const markup=ctx.text?.trimStart()||'',end=markup.indexOf('>');
  if(end<0)return false;
  const next=new DOMParser().parseFromString(markup.slice(0,end+1),'text/html').body.firstElementChild;
  return next?.id==='conversation-pane'&&next.dataset.conversationScope===ctx.oieScope&&next.dataset.engineId===document.body.dataset.engineId&&(ctx.oieChat?next.dataset.conversationId===ctx.oieChat:next.dataset.openChat==='false');
 }
 async function showError(ctx){
  if(ctx.oieErrorShown)return;
  ctx.oieErrorShown=true;
  const root=app(),failedEpoch=epoch;
  if(!booted)await window.goshtosoDependencies?.ready;
  if(root!==app()||failedEpoch!==epoch||!root?.dataset.routeError)return;
  window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message:root.dataset.routeError}}));
 }
 function upgrade(root=document){if(!root)return;for(const node of root.querySelectorAll('a[href],form')){
  if(node.closest('[data-quick-search]')||node.matches('[data-global-search-form],[data-message-search-filter],[data-conversation-search-close],[data-open-sender],[data-room-invitation],[data-room-entry-open],a[download],a[target]:not([target="_self"])')||node.hasAttribute('hx-get')||node.hasAttribute('hx-post'))continue;
  const form=node.tagName==='FORM';if(form&&(node.method||'get').toLowerCase()!=='get')continue;
  let url;try{url=new URL(form?node.action:node.href,location.href)}catch{continue}
  if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||(!form&&(node.getAttribute('href')||'').startsWith('#')))continue;
  const pane=!form&&node.matches('#conversation-list a[data-conversation-id],.oie-back-button')&&conversationRoute(url);
  const scoped=node.id==='shared-content-search'?'shared-search-results':form&&node.matches('[data-info-search]')?'chat-info-content':scopedKind(url,node);
  const kind=scoped||(pane?'conversation-pane':!form&&node.matches('a[data-filter]')?inboxID:'oie-app');
  node.setAttribute('hx-boost','true');node.setAttribute('hx-target','#'+kind);node.setAttribute('hx-select',selectFor(kind));node.setAttribute('hx-swap',swapFor(kind));node.setAttribute('hx-push-url','true');node.setAttribute('hx-headers',JSON.stringify({'X-Oie-Fragment':fragment(kind)}));
 }window.htmx?.process(root);}
 // The dependency loader can expose HTMX before Alpine finishes loading.
 // Process the clicked navigation link before the event reaches its target,
 // without starting application scopes before the remaining dependencies.
 document.addEventListener('click',event=>{
  if(event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  const row=event.target.closest?.('#conversation-list a[data-conversation-id]');
  if(event.target.closest?.('[data-cancel-conversation-loading]')){
   cancelConversationLoading();return;
  }
  const activeFilter=event.target.closest?.('#'+inboxID+' a[data-filter][aria-current="true"]');
  const pane=document.getElementById('conversation-pane');
  if(activeFilter||row&&pane?.dataset.openChat==='true'&&pane.dataset.conversationId===row.dataset.conversationId){
   if(row)cancelConversationLoading();
   event.preventDefault();event.stopImmediatePropagation();return;
  }
  const accountLink=event.target.closest?.('[data-open-accounts], [data-add-account]');
  if(booted&&accountLink?.getAttribute('hx-target')==='#account-workspace'){
   accountLink.closest('[data-popover-panel]')?.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
  }
  if(booted)return;
  const link=event.target.closest?.('a[data-oie-navigation-item][hx-boost="true"], #conversation-list a[data-conversation-id][hx-boost="true"]');
  if(!link)return;
  if(window.htmx){window.htmx.process(link);return;}
  const ready=window.goshtosoDependencies?.ready;
  if(!ready)return;
  const epochBefore=epoch;
  event.preventDefault();
  ready.then(()=>{if(link.isConnected&&epochBefore===epoch)link.click();});

 },true);
 document.addEventListener('keydown',event=>{
  if(event.key!=='Escape'||!pendingConversation||pendingConversation.overlay.hidden)return;
  event.preventDefault();event.stopImmediatePropagation();
  cancelConversationLoading();
 },true);
 function infoReady(starGeneration){const root=document.getElementById('chat-info-content');if(!root)return;upgrade(root);document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root,generation:starGeneration}}));const body=document.getElementById('details-body');if(root.dataset.infoView){if(body)body.scrollTop=0;root.querySelector('[data-info-heading]')?.focus({preventScroll:true})}else{if(body)body.scrollTop=infoReturnScroll;const kind=new URL(location.href).searchParams.get('infoFocus');root.querySelector(`[data-info-entry="${CSS.escape(kind||'media')}"]`)?.focus({preventScroll:true})}}
 function closeMediaViewers(){for(const id of ['oie-image-viewer','oie-document-viewer']){window.dispatchEvent(new CustomEvent('modal:close',{detail:{id}}));const dialog=document.getElementById(id);if(dialog?.open)dialog.close()}}
 function closeOverlays(){closeMediaViewers();for(const id of ['details','accounts','settings','about','newchat','join-room','create-room','sender','message-history'])window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id}}));}
 document.addEventListener('htmx:before:request',event=>{
  const ctx=event.detail.ctx;if(ctx?.target?.id==='shared-search-results')document.getElementById('shared-content-scroll')?.removeAttribute('aria-busy');if(!ctx||!kinds.includes(ctx.target?.id))return;
  const previousConversation=pendingConversation?.ctx;
  stopConversationLoading();
  ctx.oieEpoch=++epoch;ctx.oieKind=ctx.target.id;ctx.oieHistory=historyRequest!==undefined&&new URL(ctx.request.action,location.href).href===historyRequest;
  if(ctx.oieHistory)historyRequest=undefined;
  previousConversation?.request.abort();
  if(['shared-content-scroll','shared-search-results'].includes(ctx.oieKind)){ctx.oieSharedFields=sharedFields();ctx.target.setAttribute('aria-busy','true')}ctx.oieStarGeneration=Number(document.body.dataset.starGeneration||0);
  if(ctx.oieKind==='conversation-pane'){
   ctx.oieScope=ctx.target.dataset.conversationScope;
   const url=new URL(ctx.request.action,location.href);
   ctx.oieChat=url.searchParams.get('chat')||'';
   if(ctx.oieChat&&(ctx.oieChat!==ctx.target.dataset.conversationId||!ctx.target.getClientRects().length))startConversationLoading(ctx);
  }
  if(ctx.oieKind==='schedule-editor'&&!ctx.sourceElement?.matches('[data-conversation-schedule-edit]')){
   const snapshot={};
   document.querySelector('#send-message-form')?.dispatchEvent(new CustomEvent('oie:schedule-snapshot',{detail:snapshot}));
   if(snapshot.editBlocked!==false){event.preventDefault();showError(ctx);return;}
  }
  if(ctx.oieKind==='chat-info-content'&&!document.getElementById('chat-info-content')?.dataset.infoView)infoReturnScroll=document.getElementById('details-body')?.scrollTop||0;
 });
 document.addEventListener('htmx:after:request',event=>{
  const ctx=event.detail.ctx;if(!ctx?.oieKind)return;if(ctx.oieEpoch!==epoch){event.preventDefault();return}
  if(pendingConversation?.ctx===ctx)clearTimeout(pendingConversation.timer);
  if(ctx.oieKind==='conversation-pane'){
   if(!conversationResponse(ctx)){event.preventDefault();showError(ctx);}
   return;
  }
  const parsed=new DOMParser().parseFromString(ctx.text||'','text/html'),next=parsed.querySelector('#'+ctx.oieKind);
  let valid=!!next;
  if(['shared-content-scroll','shared-search-results'].includes(ctx.oieKind)){
   valid=valid&&ctx.response?.status===200&&parsed.querySelector('[data-shared-fragment]')?.dataset.engineId===document.body.dataset.engineId&&sharedFields()===ctx.oieSharedFields;
   ctx.oieSharedPage=parsed;
  }
  if(['account-workspace','scheduled-results','schedule-editor','message-timeline'].includes(ctx.oieKind))valid=valid&&ctx.response?.status===200&&next.dataset.engineId===document.body.dataset.engineId;
  if(ctx.oieKind==='message-timeline'){valid=valid&&next?.dataset.conversationId===document.body.dataset.conversationId;ctx.oieTimelinePage=parsed;}
  if(ctx.oieKind==='schedule-editor'){
   const snapshot={};
   document.querySelector('#send-message-form')?.dispatchEvent(new CustomEvent('oie:schedule-snapshot',{detail:snapshot}));
   const id=new URL(ctx.request.action,location.href).searchParams.get('editSchedule');
   valid=valid&&snapshot.editBlocked===false&&next.dataset.conversationId===document.body.dataset.conversationId&&next.dataset.accountId===document.body.dataset.accountId&&(!id||next.querySelector('input[name=messageId]')?.value===id);
  }
  if(ctx.oieKind==='chat-info-content')valid=valid&&ctx.response?.status===200&&next.dataset.detailsConversation===document.body.dataset.conversationId;
  if(ctx.oieKind===inboxID){
   const url=new URL(ctx.request.action,location.href);
   valid=valid&&ctx.response?.status===200&&next.dataset.inboxScope===ctx.target.dataset.inboxScope&&next.dataset.inboxFilter===(url.searchParams.get('filter')||'all');
  }
  if(!valid){event.preventDefault();ctx.target?.removeAttribute('aria-busy');if(ctx.sourceElement?.id==='shared-content-search')syncSharedAccount(new URL(currentURL).searchParams);if(!ctx.sourceElement?.matches('[data-conversation-schedule-edit]'))showError(ctx);}
 });
 document.addEventListener('htmx:finally:request',event=>{
  if(pendingConversation?.ctx===event.detail.ctx)stopConversationLoading();
 });
 document.addEventListener('htmx:before:swap',event=>{
  const ctx=event.detail.ctx;if(!ctx?.oieKind)return;
  if(ctx.oieEpoch!==epoch||!document.getElementById(ctx.oieKind)||ctx.oieSharedFields!==undefined&&sharedFields()!==ctx.oieSharedFields){
   event.preventDefault();return;
  }
  remember();ctx.target=document.getElementById(ctx.oieKind);
  if(ctx.oieKind===inboxID){
   const filters=ctx.target.querySelector('.oie-filters');
   restoreFilterFocus=filters.contains(document.activeElement);
   filterScroll=filters.scrollLeft;
  }
  if(ctx.oieSharedPage){closeMediaViewers();window.OieVideos?.release(ctx.target);ctx.target.dataset.sharedChanging='true';}
  settling=ctx;
  if(ctx.oieKind==='message-timeline'){
   document.dispatchEvent(new Event('oie:timeline-replacing'));
   window.OieVideos?.release(ctx.target);
  }

  if(ctx.oieKind==='oie-app'||ctx.oieKind==='conversation-pane'){if(ctx.oieKind==='conversation-pane')closeOverlays();runtime.stop();}else infoRequestGeneration=ctx.oieStarGeneration;
 });
 document.addEventListener('htmx:after:settle',event=>{
  const root=event.detail.task?.target;if(!root||!root.isConnected||root!==document.getElementById(root.id)||!kinds.includes(root.id))return;
  const ctx=settling?.oieKind===root.id?settling:null;if(ctx)settling=null;
  const previousURL=currentURL;
  currentURL=location.href;
  if(root.id===inboxID){
   const previousFilter=new URL(previousURL).searchParams.get('filter')||'all';
   syncInboxScope(root,previousFilter);
   upgrade(app());
   const scroll=positions.get(location.href)?.['conversation-list']||0;
   root.querySelector('#conversation-list').scrollTop=scroll;
   root.querySelector('.oie-filters').scrollLeft=filterScroll;
   if(restoreFilterFocus)root.querySelector('a[data-filter][aria-current="true"]')?.focus({preventScroll:true});
   document.dispatchEvent(new CustomEvent('oie:inbox-filtered',{detail:{root}}));
  }
  else if(root.id==='shared-content-scroll'||root.id==='shared-search-results'){
   const page=ctx?.oieSharedPage;delete root.dataset.sharedChanging;root.removeAttribute('aria-busy');
   if(!page)return;
   window.Alpine?.initTree(root);
   const form=document.getElementById('shared-content-search'),url=new URL(location.href);
   if(ctx.oieHistory)document.getElementById('shared-search').value=url.searchParams.get('q')||'';
   form.elements.contentConversation.value=url.searchParams.get('contentConversation')||'';
   const account=page.querySelector('[data-shared-fragment]').dataset.accountFilter;
   reconcileSharedAccount(page,url.searchParams,ctx.oieHistory||account!==(app().dataset.sharedAccount||''));
   if(account!==(app().dataset.sharedAccount||'')){
    app().dataset.sharedAccount=account;
    document.dispatchEvent(new CustomEvent('oie:shared-account',{detail:{page}}));
   }
   const type=page?.querySelector('#shared-content-results')?.dataset.contentType;
   if(type){const field=document.querySelector('#shared-content-search input[name=contentType]');if(field)field.value=type;for(const tab of document.querySelectorAll('#shared-search-results [data-shared-tab]'))tab.setAttribute('aria-current',String(tab.dataset.sharedTab===type));}
   const region=document.getElementById('shared-search-results'),fresh=page?.querySelector('#shared-search-results');if(region&&fresh)region.dataset.searchQuery=fresh.dataset.searchQuery||'';
   document.dispatchEvent(new CustomEvent('oie:shared-search-settled',{detail:{root:region,page}}));
   document.dispatchEvent(new CustomEvent('oie:previews-refresh',{detail:page}));document.dispatchEvent(new CustomEvent('oie:stars-refresh',{detail:{page,generation:ctx.oieStarGeneration}}));upgrade(app());
  }
  else if(root.id==='chat-info-content'){
   window.Alpine?.initTree(root);
   if(ctx?.oieHistory){
    const open=new URL(location.href).searchParams.get('open')==='details';
    window.dispatchEvent(new CustomEvent(open?'drawer:open':'drawer:close',{detail:{id:'details'}}));
   }
   infoReady(infoRequestGeneration);
   const saved=ctx?.oieHistory?positions.get(location.href)?.['details-body']:undefined;
   if(saved!==undefined)document.getElementById('details-body').scrollTop=saved;
  }
  else if(root.id==='account-workspace'){window.Alpine?.initTree(root);upgrade(root);accountReady(root,previousURL);}
  else if(root.id==='appearance-settings'){applyAppearance(root.dataset.mode,ctx?.oieHistory);}
  else if(root.id==='scheduled-results'){
   upgrade(root);
   const saved=ctx?.oieHistory?positions.get(location.href)?.workspace:undefined, workspace=document.getElementById('workspace');
   if(saved!==undefined)workspace.scrollTop=saved;
   document.dispatchEvent(new CustomEvent('oie:agenda-settled',{detail:{root,source:ctx?.sourceElement}}));
  }
  else if(root.id==='schedule-editor'){
   window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id:'conversation-schedules'}}));
   document.dispatchEvent(new CustomEvent('oie:schedule-editor',{detail:{root}}));
   upgrade(root);
  }
  else if(root.id==='message-timeline'){
   // The timeline itself stays mounted. Initialize its new children before
   // the live hooks read their Alpine state.
   for(const child of root.children)window.Alpine?.initTree(child);
   const next=ctx?.oieTimelinePage?.getElementById(root.id);
   if(next){
    for(const key of ['version','roomArchive','archiveTextSearch','archiveInitial','archiveComplete','archiveCovered']){
     if(next.dataset[key]===undefined)delete root.dataset[key];else root.dataset[key]=next.dataset[key];
    }
   }
   for(const node of [app(),document.body,document.getElementById('conversation-pane')])for(const key of ['focusMessage','contextNewer','contextMissing'])if(node)node.dataset[key]='';
   document.getElementById('context-notice')?.replaceChildren();
   document.dispatchEvent(new CustomEvent('oie:timeline-latest',{detail:{root,page:ctx?.oieTimelinePage,generation:ctx?.oieStarGeneration}}));
   upgrade(root);
  }
  else{
   if(pendingConversation&&!pendingConversation.overlay.isConnected)stopConversationLoading();
   const pane=root.id==='conversation-pane'?root:null;
   if(!pane)inboxScroll=0;
   routeState(pane);syncFilterLinks();upgrade(pane||app());
   // Alpine's HTMX hook flushes after settle. Initialize the new pane now
   // so the first scroll calculation already includes revealed previews.
   if(pane)window.Alpine?.initTree(pane);
   if(booted)runtime.start();
   const saved=positions.get(location.href);
   const scheduleEditor=root.querySelector('[data-editing-schedule] #message-composer');
   const savedTimelineTop=saved?.['message-timeline']??(scheduleEditor?positions.get(previousURL)?.['message-timeline']:undefined);
   if(savedTimelineTop!==undefined&&!new URL(location.href).searchParams.has('message')){
    const timeline=document.getElementById('message-timeline');
    if(timeline)timeline.scrollTop=savedTimelineTop;
   }
   if(scheduleEditor)scheduleEditor.focus({preventScroll:true});
   requestAnimationFrame(()=>{if(!root.isConnected||root!==document.getElementById(root.id))return;if(saved&&root.id==='oie-app')for(const [id,top]of Object.entries(saved)){if(id==='message-timeline')continue;const node=document.getElementById(id);if(node)node.scrollTop=top}const info=document.getElementById('chat-info-content');if(info)document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root:info,generation:Number(document.body.dataset.starGeneration||0)}}));});
  }
 });
 document.addEventListener('htmx:error',event=>{const ctx=event.detail.ctx;if(ctx?.oieKind&&ctx.oieEpoch===epoch){ctx.target?.removeAttribute('aria-busy');showError(ctx)}});
 function go(value,history=false){
  const url=new URL(value,location.href);if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||!window.htmx){location.assign(url.href);return}
  const filterChanged=(url.searchParams.get('filter')||'all')!==(new URL(currentURL).searchParams.get('filter')||'all');
  const themeChanged=history&&sameRoute(url,['theme','open'])&&url.searchParams.get('theme')!==document.documentElement.dataset.mode&&url.searchParams.has('theme');
  const kind=(themeChanged?'appearance-settings':scopedKind(url))||(filterChanged&&inboxRoute(url)?inboxID:conversationRoute(url)?'conversation-pane':'oie-app');
  const requestURL=kind===inboxID?inboxURL(url):new URL(url);
  if(kind==='chat-info-content'&&!requestURL.searchParams.has('chat'))requestURL.searchParams.set('chat',document.body.dataset.conversationId);
  if(history)historyRequest=requestURL.href;
  return window.htmx.ajax('GET',requestURL.pathname+requestURL.search,{source:app(),target:document.getElementById(kind),swap:swapFor(kind),select:selectFor(kind),push:history?'false':url.pathname+url.search,headers:{'X-Oie-Fragment':fragment(kind),...(history?{'HX-History-Restore-Request':'true'}:{})}});
 }
 // Turn an implicitly selected desktop conversation into an addressable one
 // before a command can remove its row from the current list. This is the same
 // view and history entry, so keep its mounted nodes, scroll and runtime.
 function pinConversation(id){
  const pane=document.getElementById('conversation-pane'),root=app(),url=new URL(location.href);
  if(!id||url.pathname!=='/'||!pane||pane.dataset.conversationId!==id||document.body.dataset.conversationId!==id)return false;
  const selected=url.searchParams.get('chat');
  if(selected&&selected!==id)return false;
  if(selected&&!url.searchParams.has('inbox'))return true;
  remember();const saved=positions.get(currentURL);positions.delete(currentURL);
  url.searchParams.set('chat',id);url.searchParams.delete('inbox');history.replaceState(history.state,'',url.pathname+url.search+url.hash);currentURL=location.href;if(saved)positions.set(currentURL,saved);
  pane.dataset.openChat='true';document.getElementById('workspace')?.classList.add('oie-chat-open');
  syncFilterLinks();
  if(root)for(const row of root.querySelectorAll('#conversation-list a[data-conversation-id]'))row.setAttribute('aria-current',String(row.dataset.conversationId===id));
  return true;
 }
 document.addEventListener('htmx:before:history:restore',event=>{event.preventDefault();go(event.detail.path,true);});
 function addressSearch(url,push=false){remember();history[push?'pushState':'replaceState'](history.state,'',url.pathname+url.search+url.hash);currentURL=location.href;syncFilterLinks();}
 window.OieNavigation={go,pinConversation,addressSearch,showError};
 document.addEventListener('oie:navigation-upgrade',()=>upgrade(app()));
 document.addEventListener('oie:shared-search-settled',()=>{currentURL=location.href;});
 reducedMotion.addEventListener('change',()=>upgrade(app()));
 async function ready(){await window.goshtosoDependencies?.ready;booted=true;routeState();syncFilterLinks();upgrade(app());runtime.start();}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ready,{once:true});else ready();
})();
