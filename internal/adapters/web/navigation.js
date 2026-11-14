(() => {
 'use strict';
 const runtime=window.OieRuntime;
 const inbox=window.OieInbox;
 let booted=false,epoch=0,currentURL=location.href,infoReturnScroll=0,infoRequestGeneration=0,inboxScroll=0;
 let restoreFilterFocus=false,filterScroll=0;
 let pendingConversation, pendingShared, settling, historyRequest;
 const sharedPageCounts=new Map();
 let sharedCountEngine;
 const positions=new Map(),app=()=>document.getElementById('oie-app');
 const scrollIDs=['conversation-list','message-timeline','shared-content-scroll','details-body','accounts-body','account-detail-scroll','workspace'];
 const routeFields=['openDrawer','engineId','eventCursor','conversationId','accountId','focusMessage','contextNewer','contextMissing','mock'];
 const inboxID='conversation-list-default';
 const routeID='oie-route';
 const replacesWorkspace=kind=>kind==='oie-app'||kind===routeID;
 const kinds=['oie-app',routeID,'chat-info-content','conversation-pane','shared-content-scroll','shared-search-results','account-workspace','appearance-settings','scheduled-results','schedule-editor','message-timeline',inboxID];
 const chatQueryKeys=['message','timelineCursor','timelineDirection','chatSearch','chatQuery','chatIncludeHistory'];
 const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)');
 function pageCount(value){
  return /^[0-9]{1,2}$/.test(value)&&Number(value)<=50?Number(value):undefined;
 }
 function visibleRows(viewport, container, item, boundary){
  const style=getComputedStyle(viewport),gap=parseFloat(getComputedStyle(container).rowGap)||0;
  let height=viewport.clientHeight;
  if(boundary){
   const area=viewport.getBoundingClientRect(),bounds=boundary.getBoundingClientRect();
   height=Math.min(area.bottom,bounds.bottom)-Math.max(area.top,bounds.top);
  }
  height-=parseFloat(style.paddingTop)+parseFloat(style.paddingBottom);
  const row=item.getBoundingClientRect().height;
  return row>0?Math.max(0,Math.floor((height+gap)/(row+gap))):0;
 }
 function renderPlaceholders(container, template, count, viewport=container, boundary){
  container.replaceChildren();
  if(count===0)return;
  container.append(template.content.cloneNode(true));
  const first=container.firstElementChild;
  const conversation=container.hasAttribute('data-conversation-placeholders');
  const columns=conversation?1:getComputedStyle(container).gridTemplateColumns.trim().split(/\s+/).length;
  const visible=Math.min(count,visibleRows(viewport,container,first,boundary)*columns);
  if(!visible){first.remove();return;}
  for(let index=1;index<visible;index++){
   const item=first.cloneNode(true);
   if(conversation&&(index%5===1||index%5===4))item.classList.add('oie-chat-skeleton-outgoing');
   container.append(item);
  }
 }
 function stopConversationLoading(){
  if(!pendingConversation)return;
  const {timer,pane,overlay,covered,focus}=pendingConversation;
  pendingConversation=null;
  clearTimeout(timer);
  for(const node of covered??[])node.inert=false;
  const restoreFocus=focus?.isConnected&&(document.activeElement===document.body||overlay.contains(document.activeElement));
  overlay.hidden=true;
  overlay.querySelector('[data-conversation-placeholders]').replaceChildren();
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
   const params=new URL(ctx.request.action,location.href).searchParams;
   const row=document.querySelector(`#conversation-list [data-conversation-row="${CSS.escape(ctx.oieChat)}"]`);
   const estimate=params.get('message')||params.get('timelineCursor')?undefined:pageCount(row?.dataset.latestMessagePageCount);
   overlay.hidden=false;
   const placeholders=overlay.querySelector('[data-conversation-placeholders]');
   renderPlaceholders(placeholders,overlay.querySelector('[data-conversation-placeholder]'),estimate??5);
   overlay.querySelector('[role=status]').classList.toggle('sr-only',placeholders.childElementCount>0);
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
 const sharedTabs=['media','links','documents'];
 function sharedTabType(type){return sharedTabs.includes(type)?type:'media';}
 function sharedPageKey(query,type,info){
  const engine=document.body.dataset.engineId;
  if(engine!==sharedCountEngine){sharedPageCounts.clear();sharedCountEngine=engine;}
  const term=[...(query.get(info?'infoQuery':'q')||'')].slice(0,200).join('');
  const locale=query.get('lang')||app()?.dataset.routeLocale;
  if(info)return JSON.stringify([engine,'details',type,document.body.dataset.accountId,
   query.get('chat')||document.body.dataset.conversationId,term,locale]);
  return JSON.stringify([engine,type,query.get('account')||'',
   query.getAll('contentAccount').sort(),query.get('contentConversation')||'',
   term,locale]);
 }
 function rememberSharedPage(page,info=false){
  if(page?.dataset.sharedPageState!=='ready'||page.dataset.sharedPageEngine!==document.body.dataset.engineId)return;
  const query=new URLSearchParams(location.search),active=sharedTabType(query.get(info?'infoType':'contentType'));
  for(const type of sharedTabs){
   const count=pageCount(page.getAttribute('data-shared-first-page-'+type))
    ??(type===active&&!page.dataset.sharedPageCursor?pageCount(page.dataset.sharedPageCount):undefined);
   if(count===undefined)continue;
   const key=sharedPageKey(query,type,info);
   sharedPageCounts.delete(key);
   sharedPageCounts.set(key,count);
  }
  while(sharedPageCounts.size>50)sharedPageCounts.delete(sharedPageCounts.keys().next().value);
 }
 function stopSharedLoading(){
  if(!pendingShared)return;
  const {ctx,timer,info,content,overlay,covered}=pendingShared;
  pendingShared=null;
  clearTimeout(timer);
  (info?content:ctx.target).removeAttribute('aria-busy');
  if(covered)content.inert=false;
  if(overlay){overlay.hidden=true;overlay.querySelector('[data-shared-placeholders]').replaceChildren();}
 }
 function cancelInfoSharedLoading(){
  if(!pendingShared?.info)return;
  const {ctx}=pendingShared;
  ctx.oieInfoCancelled=true;
  stopSharedLoading();
  ctx.request.abort();
 }
 function startSharedLoading(ctx,info=false){
  const catalog=document.getElementById(info?'info-shared-page-content':'shared-content-scroll');
  const content=catalog||(!info&&document.getElementById('workspace'));
  const overlay=catalog?.parentElement.querySelector(':scope > [data-shared-skeleton]')
   ||(!info&&!catalog&&document.querySelector('[data-shared-entry-skeleton]'));
  const query=new URL(ctx.request.action,location.href).searchParams;
  const type=sharedTabType(query.get(info?'infoType':'contentType'));
  const canShow=content&&overlay&&!query.get(info?'infoCursor':'contentCursor');
  if(info&&!canShow)return;
  const pending={ctx,info,content,overlay};
  pendingShared=pending;
  if(!canShow)return;
  if(info)content.setAttribute('aria-busy','true');
  pending.timer=setTimeout(()=>{
   if(pendingShared!==pending||!overlay.isConnected||!overlay.parentElement.getClientRects().length)return;
   if(ctx.oieEpoch!==undefined&&ctx.oieEpoch!==epoch)return;
   if(ctx.oieSharedFields!==undefined&&sharedFields()!==ctx.oieSharedFields)return;
   const focusInResults=content.contains(document.activeElement);
   if(catalog&&focusInResults)return;
   overlay.dataset.contentType=type;
   overlay.hidden=false;
   if(!content.inert&&!focusInResults){content.inert=true;pending.covered=true;}
   const placeholders=overlay.querySelector('[data-shared-placeholders]');
   renderPlaceholders(placeholders,overlay.querySelector('[data-shared-placeholder]'),
    sharedPageCounts.get(sharedPageKey(query,type,info))??(type==='media'?6:3),overlay,
    info?document.getElementById('details-body'):undefined);
   overlay.querySelector('[role=status]').classList.toggle('sr-only',placeholders.childElementCount>0);
  },250);
 }
 function remember(){const state={};for(const id of scrollIDs){const node=document.getElementById(id);if(node){state[id]=node.scrollTop;if(id==='conversation-list'&&node.clientHeight>0)inboxScroll=node.scrollTop}}positions.set(currentURL,state);if(positions.size>100)positions.delete(positions.keys().next().value)}
 function routeState(pane){
  const root=app();if(!root)return;
  if(pane){for(const name of [...routeFields,'routeLocale','routeTheme','csrf'])root.dataset[name]=pane.dataset[name]||'';document.getElementById('workspace')?.classList.toggle('oie-chat-open',pane.dataset.openChat==='true');root.querySelector('[data-open-chat]')?.setAttribute('data-open-chat',pane.dataset.openChat||'false');const list=document.getElementById('conversation-list');if(list){if(list.clientHeight>0)list.scrollTop=inboxScroll;}for(const row of root.querySelectorAll('#conversation-list a[data-conversation-id]'))row.setAttribute('aria-current',String(pane.dataset.openChat==='true'&&row.dataset.conversationId===pane.dataset.conversationId));}
  for(const name of routeFields)document.body.dataset[name]=root.dataset[name]||'';
  document.body.dataset.starGeneration='0';document.documentElement.lang=root.dataset.routeLocale;document.documentElement.dataset.mode=root.dataset.routeTheme;document.documentElement.classList.toggle('dark',root.dataset.routeTheme==='dark'||root.dataset.routeTheme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);document.querySelector('meta[name="oie-csrf"]')?.setAttribute('content',root.dataset.csrf||'');
 }
 function updateSidebar(sidebar, incoming, accountChanged){
  const logo=sidebar.querySelector('.oie-brand')?.closest('a');
  const nextLogo=incoming.querySelector('.oie-brand')?.closest('a');
  if(logo&&nextLogo)logo.setAttribute('href',nextLogo.getAttribute('href'));
  const links='a[data-oie-navigation-item]';
  const destinations=new Map([...incoming.querySelectorAll(links)].map(link=>[new URL(link.href,location.href).searchParams.get('section'),link]));
  for(const link of sidebar.querySelectorAll(links)){
   const next=destinations.get(new URL(link.href,location.href).searchParams.get('section'));
   if(!next)continue;
   link.setAttribute('href',next.getAttribute('href'));
   const active=next.hasAttribute('aria-current');
   if(active)link.setAttribute('aria-current','page');else link.removeAttribute('aria-current');
   const label=link.querySelector('.sr-only');
   if(label)label.hidden=!active;
  }
  const profile=sidebar.querySelector('.oie-profile'), nextProfile=incoming.querySelector('.oie-profile');
  if(accountChanged){
   const replace=()=>{window.Alpine?.destroyTree(profile);profile.replaceWith(nextProfile);window.Alpine?.initTree(nextProfile);};
   if(window.Alpine)window.Alpine.mutateDom(replace);else replace();
   return;
  }
  for(const selector of ['[data-switch-account]','[data-open-accounts]','[data-add-account]','input[name="returnTo"]']){
   const current=profile.querySelectorAll(selector),next=nextProfile.querySelectorAll(selector);
   if(current.length!==next.length)continue;
   for(const [index,node] of current.entries()){
    if(node.dataset.switchAccount!==next[index].dataset.switchAccount)continue;
    if(node.tagName==='INPUT')node.value=next[index].value;else node.setAttribute('href',next[index].getAttribute('href'));
   }
  }
 }
 function keepMobileNavigation(ctx){
  const mobile=ctx.target.querySelector('.oie-mobile-nav'),incoming=(ctx.oieIncomingRoute||ctx.oieRouteApp).querySelector('.oie-mobile-nav');
  if(!mobile||!incoming)return;
  ctx.oieMobileNavigation=mobile;
  ctx.oieIncomingMobile=incoming;
  ctx.oieMobileFocus=mobile.contains(document.activeElement);
  if(window.Alpine)window.Alpine.mutateDom(()=>mobile.remove());else mobile.remove();
 }
 const sourceRetainedRegions=['data-route-shared-copy','data-route-settings','data-route-viewers'];
 function keepSourceRegions(ctx){
  if(!ctx.oieRouteApp?.hasAttribute('data-source-route'))return;
  ctx.oieSourceRegions=sourceRetainedRegions.map(name=>ctx.target.querySelector('['+name+']')).filter(Boolean);
  const remove=()=>{for(const node of ctx.oieSourceRegions)node.remove()};
  if(window.Alpine)window.Alpine.mutateDom(remove);else remove();
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
 function scopedKind(url, node, latest=false) {
  const params=url.searchParams,current=new URL(currentURL).searchParams;
  const pane=document.getElementById('conversation-pane');
  const chat=params.get('chat')||(!node&&!current.has('chat')?pane?.dataset.conversationId:'');
  const sameChat=!!pane?.dataset.conversationId&&chat===pane.dataset.conversationId&&!params.has('inbox');
  if(latest&&sameChat&&document.getElementById('message-timeline'))return 'message-timeline';
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
  if(kind===routeID||kind==='conversation-pane'||kind==='schedule-editor')return 'outerHTML settle:0ms show:none';
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
 function upgrade(root=document,process=true){if(!root)return;for(const node of root.querySelectorAll('a[href],form')){
  if(node.closest('[data-quick-search]')||node.matches('[data-global-search-form],[data-message-search-filter],[data-conversation-search-close],[data-open-sender],[data-room-invitation],[data-room-entry-open],a[download],a[target]:not([target="_self"])')||node.hasAttribute('hx-get')||node.hasAttribute('hx-post'))continue;
  const form=node.tagName==='FORM';if(form&&(node.method||'get').toLowerCase()!=='get')continue;
  let url;try{url=new URL(form?node.action:node.href,location.href)}catch{continue}
  if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||(!form&&(node.getAttribute('href')||'').startsWith('#')))continue;
  const pane=!form&&node.matches('#conversation-list a[data-conversation-id],.oie-back-button')&&conversationRoute(url);
  const scoped=node.id==='shared-content-search'?'shared-search-results':form&&node.matches('[data-info-search]')?'chat-info-content':scopedKind(url,node);
  const source=!form&&node.matches('[data-shared-actions] a[id^="shared-source-"],[data-info-source]')&&
   url.searchParams.has('message')&&url.searchParams.has('chat');
  const sourceInPane=source&&conversationRoute(url);
  const kind=sourceInPane?'conversation-pane':node.matches('a[data-oie-navigation-item]')||source?routeID:scoped||(pane?'conversation-pane':!form&&node.matches('a[data-filter]')?inboxID:'oie-app');
  node.setAttribute('hx-boost','true');node.setAttribute('hx-target','#'+kind);node.setAttribute('hx-select',selectFor(kind));node.setAttribute('hx-swap',swapFor(kind));node.setAttribute('hx-push-url','true');node.setAttribute('hx-headers',JSON.stringify({'X-Oie-Fragment':sourceInPane?'source-pane':source?'source':fragment(kind)}));
 }if(process)window.htmx?.process(root);}
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
 function infoReady(starGeneration){const root=document.getElementById('chat-info-content');if(!root)return;rememberInfoSharedPage(root);upgrade(root);document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root,generation:starGeneration}}));const body=document.getElementById('details-body');if(root.dataset.infoView){if(body)body.scrollTop=0;root.querySelector('[data-info-heading]')?.focus({preventScroll:true})}else{if(body)body.scrollTop=infoReturnScroll;const kind=new URL(location.href).searchParams.get('infoFocus');root.querySelector(`[data-info-entry="${CSS.escape(kind||'media')}"]`)?.focus({preventScroll:true})}}
 function closeMediaViewers(){for(const id of ['oie-image-viewer','oie-document-viewer']){window.dispatchEvent(new CustomEvent('modal:close',{detail:{id}}));const dialog=document.getElementById(id);if(dialog?.open)dialog.close()}}
 function closeOverlays(){closeMediaViewers();for(const id of ['details','accounts','settings','about','newchat','join-room','create-room','sender','message-history'])window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id}}));}
 function workspaceResponse(ctx,nextApp,next){
  if(ctx.oieKind===routeID){
   if(!next||!nextApp||ctx.response?.status!==200)return false;
   const mounted=app().dataset,incoming=nextApp.dataset;
   if(incoming.engineId!==mounted.engineId||incoming.routeLocale!==mounted.routeLocale||incoming.mock!==mounted.mock){
    ctx.oieRetry=true;go(ctx.request.action,{history:ctx.oieHistory,workspace:'oie-app'});return false;
   }
   ctx.oieRouteApp=nextApp;
  }
  const list=next?.querySelector('#conversation-list'),entry=ctx.oieInbox;
  const matches=list&&entry&&inbox.holds(entry)&&list.dataset.inboxCacheScope===entry.list.dataset.inboxCacheScope&&nextApp?.dataset.engineId===app()?.dataset.engineId;
  ctx.oieInboxReuse=list?.hasAttribute('data-inbox-reuse');
  if(ctx.oieInboxReuse&&matches){
   const params=new URL(ctx.request.action,location.href).searchParams;
   const cursor=ctx.request.headers['X-Oie-Inbox-Cursor'];
   if(!params.get('chat')&&params.get('inbox')!=='1'&&(!inbox.current(entry.list)||window.OieEvents.latest(cursor)!==cursor)){
    ctx.oieRetry=true;go(ctx.request.action,{history:ctx.oieHistory,workspace:ctx.oieKind,noInboxReuse:true});return false;
   }
  }
  if(!matches)ctx.oieInbox=undefined;
  return !!next&&(!ctx.oieInboxReuse||!!matches);
 }
 document.addEventListener('htmx:before:request',event=>{
  const ctx=event.detail.ctx;if(!ctx)return;
  if(ctx.target?.id==='info-shared-search-results'){
   if(ctx.oieSearch&&!pendingConversation){cancelInfoSharedLoading();startSharedLoading(ctx,true);}
   return;
  }
  if(!kinds.includes(ctx.target?.id))return;
  const previousConversation=pendingConversation?.ctx;
  stopConversationLoading();
  cancelInfoSharedLoading();
  stopSharedLoading();
  ctx.oieEpoch=++epoch;ctx.oieKind=ctx.target.id;ctx.oieHistory=historyRequest!==undefined&&new URL(ctx.request.action,location.href).href===historyRequest;
  if(ctx.oieHistory)historyRequest=undefined;
  if(replacesWorkspace(ctx.oieKind)||ctx.oieKind===inboxID){
   ctx.oieInboxGeneration=inbox.generation;
   if(booted&&replacesWorkspace(ctx.oieKind)&&ctx.request.method.toUpperCase()==='GET'){
    const entry=inbox.match(new URL(ctx.request.action,location.href));
    if(entry){
     ctx.oieInbox=entry;
     ctx.request.headers['X-Oie-Inbox-Scope']=entry.list.dataset.inboxCacheScope;
     if(ctx.request.headers['X-Oie-Inbox-Cursor']===undefined&&inbox.current(entry.list)&&window.OieEvents.connected){
      ctx.request.headers['X-Oie-Inbox-Cursor']=window.OieEvents.latest(entry.list.dataset.inboxCursor);
     }
     const first=entry.list.querySelector('a[data-conversation-id]')?.dataset.conversationId;
     if(first)ctx.request.headers['X-Oie-Inbox-First']=first;
    }
   }
  }
  previousConversation?.request.abort();
  if(['shared-content-scroll','shared-search-results'].includes(ctx.oieKind)){
   ctx.oieSharedFields=sharedFields();ctx.target.setAttribute('aria-busy','true');startSharedLoading(ctx);
  }else if(replacesWorkspace(ctx.oieKind)&&new URL(ctx.request.action,location.href).searchParams.get('section')==='media'){
   startSharedLoading(ctx);
  }
  ctx.oieStarGeneration=Number(document.body.dataset.starGeneration||0);
  if(ctx.oieKind==='conversation-pane'){
   ctx.oieScope=ctx.target.dataset.conversationScope;
   const url=new URL(ctx.request.action,location.href);
   ctx.oieChat=url.searchParams.get('chat')||'';
   if(ctx.oieChat&&(ctx.oieChat!==ctx.target.dataset.conversationId||url.searchParams.has('message')||!ctx.target.getClientRects().length))startConversationLoading(ctx);
  }
  if(ctx.oieKind==='schedule-editor'&&!ctx.sourceElement?.matches('[data-conversation-schedule-edit]')){
   const snapshot={};
   document.querySelector('#send-message-form')?.dispatchEvent(new CustomEvent('oie:schedule-snapshot',{detail:snapshot}));
   if(snapshot.editBlocked!==false){event.preventDefault();showError(ctx);return;}
  }
  if(ctx.oieKind==='chat-info-content'){
   if(!ctx.target.dataset.infoView)infoReturnScroll=document.getElementById('details-body')?.scrollTop||0;
   const query=new URL(ctx.request.action,location.href).searchParams;
   if(query.get('infoView')==='shared')startSharedLoading(ctx,true);
  }
 });
 document.addEventListener('htmx:after:request',event=>{
  const ctx=event.detail.ctx;if(!ctx)return;
  if(pendingShared?.ctx===ctx)clearTimeout(pendingShared.timer);
  if(ctx.oieInfoCancelled){event.preventDefault();return;}
  if(!ctx.oieKind)return;if(ctx.oieEpoch!==epoch){event.preventDefault();return}
  if(pendingConversation?.ctx===ctx)clearTimeout(pendingConversation.timer);
  if(ctx.oieKind==='conversation-pane'){
   if(!conversationResponse(ctx)){event.preventDefault();showError(ctx);}
   return;
  }
  if(ctx.oieKind===routeID&&ctx.request.headers['X-Oie-Fragment']==='source'&&ctx.response?.status===200&&
     /^<div\s+id="oie-app"\s+data-source-route(?:\s|>)/.test(ctx.text?.trimStart()||'')){
   // Validate the envelope from HTMX's parsed fragment before committing
   // history. Its default history update runs before it parses the response.
   ctx.oieCompactSource=true;
   ctx.select='#oie-app';
   ctx.push='false';
   return;
  }
  const parsed=new DOMParser().parseFromString(ctx.text||'','text/html'),next=parsed.querySelector('#'+ctx.oieKind);
  let valid=!!next;
  if(replacesWorkspace(ctx.oieKind)){
   valid=workspaceResponse(ctx,parsed.getElementById('oie-app'),next);
  }
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
  if(!valid){event.preventDefault();ctx.target?.removeAttribute('aria-busy');if(ctx.sourceElement?.id==='shared-content-search')syncSharedAccount(new URL(currentURL).searchParams);if(!ctx.oieRetry&&!ctx.sourceElement?.matches('[data-conversation-schedule-edit]'))showError(ctx);}
 });
 document.addEventListener('htmx:finally:request',event=>{
  if(pendingConversation?.ctx===event.detail.ctx)stopConversationLoading();
  if(pendingShared?.ctx===event.detail.ctx)stopSharedLoading();
 });
 document.addEventListener('htmx:before:swap',event=>{
  const ctx=event.detail.ctx;if(!ctx?.oieKind)return;
  if(ctx.oieInfoCancelled||ctx.oieEpoch!==epoch||!document.getElementById(ctx.oieKind)||ctx.oieSharedFields!==undefined&&sharedFields()!==ctx.oieSharedFields){
   event.preventDefault();return;
  }
  if(ctx.oieCompactSource){
   const task=event.detail.tasks.find(task=>task.type==='main');
   const fresh=task?.fragment.firstElementChild,route=fresh?.querySelector(':scope > #oie-route');
   if(!fresh?.hasAttribute('data-source-route')||!route||!fresh.querySelector('#desktop-navigation')||
      !workspaceResponse(ctx,fresh,route)){
    event.preventDefault();if(!ctx.oieRetry)showError(ctx);return;
   }
   const url=new URL(ctx.response.raw.url||ctx.request.action,location.href);
   if(url.origin!==location.origin){event.preventDefault();showError(ctx);return;}
   ctx.oieSourceURL=url;
   task.fragment.replaceChildren(route);
   ctx.oieIncomingRoute=route;
  }
  remember();ctx.target=document.getElementById(ctx.oieKind);
  if(ctx.oieSourceURL&&window.htmx.config.history&&!ctx.oieHistory){
   const {pathname,search,hash}=ctx.oieSourceURL;
   if(!history.state)history.replaceState({htmx:true},'',location.href);
   history.pushState({htmx:true},'',pathname+search+hash);
  }
  if(ctx.oieKind===routeID)keepMobileNavigation(ctx);
  if(replacesWorkspace(ctx.oieKind)&&booted&&!ctx.oieInbox)inbox.park(new URL(currentURL),inboxScroll);
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

  if(replacesWorkspace(ctx.oieKind)||ctx.oieKind==='conversation-pane'){
   if(ctx.oieKind!=='oie-app')closeOverlays();
   runtime.stop();
   if(ctx.oieKind===routeID)keepSourceRegions(ctx);
  }else infoRequestGeneration=ctx.oieStarGeneration;
 });
 document.addEventListener('htmx:before:settle',event=>{
  const root=event.detail.task?.target,ctx=settling;
  if(!root?.isConnected||ctx?.oieKind!==root.id||(!replacesWorkspace(root.id)&&root.id!==inboxID))return;
  if(root.id===routeID){
   if(ctx.oieSourceRegions){
    const restore=()=>root.append(...ctx.oieSourceRegions);
    if(window.Alpine)window.Alpine.mutateDom(restore);else restore();
   }
   const fresh=ctx.oieRouteApp;
   if(ctx.oieSourceRegions){
    const previous=document.getElementById('desktop-navigation');
    const badges=new Map([...previous.querySelectorAll('[data-account-unread]')].map(node=>[node.dataset.accountUnread,node]));
    for(const container of [fresh,root])for(const badge of container.querySelectorAll('[data-account-unread]')){
     const saved=badges.get(badge.dataset.accountUnread);
     if(saved){badge.textContent=saved.textContent;badge.hidden=saved.hidden;badge.setAttribute('aria-label',saved.getAttribute('aria-label'));}
    }
   }
   const accountChanged=app().dataset.sharedAccount!==fresh.dataset.sharedAccount;
   updateSidebar(document.getElementById('desktop-navigation'),fresh.querySelector('#desktop-navigation'),accountChanged);
   if(ctx.oieMobileNavigation){
    const mobile=ctx.oieMobileNavigation;
    updateSidebar(mobile,ctx.oieIncomingMobile,accountChanged);
    const placeholder=root.querySelector('.oie-mobile-nav');
    if(window.Alpine)window.Alpine.mutateDom(()=>placeholder.replaceWith(mobile));else placeholder.replaceWith(mobile);
    const trigger=mobile.querySelector('button[aria-controls="mobile-navigation-panel"]');
    if(trigger?.getAttribute('aria-expanded')==='true'){
     mobile.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));
    }
    if(ctx.oieMobileFocus&&trigger?.getClientRects().length)trigger.focus({preventScroll:true});
   }
   for(const name of [...routeFields,'routeLocale','routeTheme','routeError','sharedAccount','csrf'])app().dataset[name]=fresh.dataset[name]||'';
   app().querySelector('.oie-shell').dataset.openChat=root.dataset.openChat;
  }
  const list=root.querySelector('#conversation-list');
  if(ctx.oieInbox&&list){
   const restored=inbox.restore(ctx.oieInbox,list,app().dataset.conversationId,!ctx.oieInboxReuse);
   if(!ctx.oieInboxReuse)inbox.track(restored,ctx.oieInboxGeneration,list.dataset.inboxCursor);
   ctx.oieInboxTop=restored.clientHeight?restored.scrollTop:ctx.oieInbox.top;
  }else if(list){
   inbox.track(list,ctx.oieInboxGeneration);
  }
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
    for(const link of app().querySelectorAll('a[data-oie-navigation-item],.oie-mobile-nav a:has(.oie-brand)')){
     const destination=new URL(link.href,location.href);
     if(destination.searchParams.get('section')==='scheduled')continue;
     if(account)destination.searchParams.set('account',account);
     else destination.searchParams.delete('account');
     link.href=destination.pathname+destination.search;
    }
    document.dispatchEvent(new CustomEvent('oie:shared-account',{detail:{page}}));
   }
   const type=page?.querySelector('#shared-content-results')?.dataset.contentType;
   if(type){const field=document.querySelector('#shared-content-search input[name=contentType]');if(field)field.value=type;for(const tab of document.querySelectorAll('#shared-search-results [data-shared-tab]'))tab.setAttribute('aria-current',String(tab.dataset.sharedTab===type));}
   const region=document.getElementById('shared-search-results'),fresh=page?.querySelector('#shared-search-results');if(region&&fresh)region.dataset.searchQuery=fresh.dataset.searchQuery||'';
   rememberSharedPage(fresh?.querySelector('[data-shared-page]'));
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
   if(!pane)inboxScroll=ctx?.oieInboxTop||0;
   routeState(pane);syncFilterLinks();
   if(ctx?.oieCompactSource)upgrade(root,false);else upgrade(pane||app());
   if(!pane)rememberSharedPage(root.querySelector('#shared-content-pagination [data-shared-page]'));
   rememberInfoSharedPage(root);
   // Alpine's HTMX hook flushes after settle. Initialize the new pane now
   // so the first scroll calculation already includes revealed previews.
   if(pane||root.id===routeID)window.Alpine?.initTree(root);
   if(booted)runtime.start();
   const saved=positions.get(location.href);
   const scheduleEditor=root.querySelector('[data-editing-schedule] #message-composer');
   const savedTimelineTop=saved?.['message-timeline']??(scheduleEditor?positions.get(previousURL)?.['message-timeline']:undefined);
   if(savedTimelineTop!==undefined&&!new URL(location.href).searchParams.has('message')){
    const timeline=document.getElementById('message-timeline');
    if(timeline)timeline.scrollTop=savedTimelineTop;
   }
   if(scheduleEditor)scheduleEditor.focus({preventScroll:true});
   requestAnimationFrame(()=>{if(!root.isConnected||root!==document.getElementById(root.id))return;if(saved&&replacesWorkspace(root.id))for(const [id,top]of Object.entries(saved)){if(id==='message-timeline'||id==='conversation-list'&&ctx?.oieInbox)continue;const node=document.getElementById(id);if(node)node.scrollTop=top}const info=document.getElementById('chat-info-content');if(info)document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root:info,generation:Number(document.body.dataset.starGeneration||0)}}));});
  }
 });
 document.addEventListener('htmx:error',event=>{const ctx=event.detail.ctx;if(ctx?.oieKind&&!ctx.oieInfoCancelled&&ctx.oieEpoch===epoch){ctx.target?.removeAttribute('aria-busy');showError(ctx)}});
 function go(value,{history=false,latest=false,workspace,noInboxReuse=false}={}){
  const url=new URL(value,location.href);if(url.origin!==location.origin||!['/','/search'].includes(url.pathname)||!window.htmx){location.assign(url.href);return}
  const filterChanged=(url.searchParams.get('filter')||'all')!==(new URL(currentURL).searchParams.get('filter')||'all');
  const themeChanged=history&&sameRoute(url,['theme','open'])&&url.searchParams.get('theme')!==document.documentElement.dataset.mode&&url.searchParams.has('theme');
  const current=new URL(currentURL),section=route=>route.searchParams.get('section')||'chats';
  const sectionChanged=url.pathname==='/'&&current.pathname==='/'&&section(url)!==section(current);
  const kind=workspace||(themeChanged?'appearance-settings':scopedKind(url,null,latest))||
   (filterChanged&&inboxRoute(url)?inboxID:conversationRoute(url)?'conversation-pane':sectionChanged?routeID:'oie-app');
  const requestURL=kind===inboxID?inboxURL(url):new URL(url);
  if(['chat-info-content','message-timeline'].includes(kind)&&!requestURL.searchParams.has('chat'))requestURL.searchParams.set('chat',document.body.dataset.conversationId);
  if(history)historyRequest=requestURL.href;
  return window.htmx.ajax('GET',requestURL.pathname+requestURL.search,{source:app(),target:document.getElementById(kind),swap:swapFor(kind),select:selectFor(kind),push:history||latest&&url.href===currentURL?'false':url.pathname+url.search,headers:{'X-Oie-Fragment':fragment(kind),...(noInboxReuse?{'X-Oie-Inbox-Cursor':''}:{}),...(history?{'HX-History-Restore-Request':'true'}:{})}});
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
 document.addEventListener('htmx:before:history:restore',event=>{event.preventDefault();go(event.detail.path,{history:true});});
 function addressSearch(url,push=false){remember();history[push?'pushState':'replaceState'](history.state,'',url.pathname+url.search+url.hash);currentURL=location.href;syncFilterLinks();}
 window.OieNavigation={go,pinConversation,addressSearch,showError};
 runtime.register('settings-comboboxes',life=>{
  const language=document.getElementById('settings-language');
  const settings=document.getElementById('settings-body');
  life.on(language,'combobox:change',({detail})=>{
   if(detail.id!==language.id)return;
   const value=detail.values[0],current=document.documentElement.lang;
   if(!['en','pt-BR'].includes(value)||value===current)return;
   // The fragment owns the selected language. Keep the current value if navigation fails.
   language.dispatchEvent(new CustomEvent('combobox:load',{bubbles:true,detail:{id:language.id,values:[current]}}));
   const url=new URL(location.href);
   url.searchParams.set('lang',value);
   url.searchParams.set('open','settings');
   url.searchParams.set('settingsFocus','language');
   go(url);
  });
  let dismissingPicker=false;
  life.on(settings,'keydown',event=>{
   if(event.key!=='Escape'||!event.target.closest('[data-combobox]')?.querySelector('[role=combobox][aria-expanded=true]'))return;
   dismissingPicker=true;
   life.timeout(()=>{dismissingPicker=false;},0);
  });
  life.on(document,'drawer:close-request',event=>{
   if(dismissingPicker&&event.detail?.id==='settings')event.preventDefault();
  });
 });
 document.addEventListener('oie:navigation-upgrade',()=>upgrade(app()));
 document.addEventListener('oie:shared-search-settled',()=>{currentURL=location.href;});
 function rememberInfoSharedPage(root){
  rememberSharedPage(root.querySelector('#info-shared-pagination [data-shared-page]'),true);
 }
 document.addEventListener('oie:info-shared-search-settled',event=>{currentURL=location.href;rememberInfoSharedPage(event.detail.page);});
 document.addEventListener('input',event=>{
  if(event.target.id==='shared-search'&&pendingShared&&!pendingShared.info&&sharedFields()!==pendingShared.ctx.oieSharedFields)stopSharedLoading();
  if(event.target.id==='info-shared-search'&&pendingShared?.info){
   const requested=new URL(pendingShared.ctx.request.action,location.href).searchParams.get('infoQuery')||'';
   if(event.target.value!==requested)cancelInfoSharedLoading();
  }
 });
 for(const name of ['drawer:close','drawer:close-request'])window.addEventListener(name,event=>{
  if(event.detail?.id==='details')queueMicrotask(()=>{if(!event.defaultPrevented)cancelInfoSharedLoading();});
 });
 document.addEventListener('oie:shared-refresh',event=>{
  const fresh=event.detail.querySelector('#live-shared-pagination [data-shared-page]');
  const mounted=document.querySelector('#shared-content-pagination [data-shared-page]');
  if(fresh&&fresh.dataset.sharedPageScope===mounted?.dataset.sharedPageScope)rememberSharedPage(fresh);
  const info=event.detail.querySelector('#live-info-content #info-shared-pagination [data-shared-page]');
  const mountedInfo=document.querySelector('#chat-info-content #info-shared-pagination [data-shared-page]');
  if(info&&info.dataset.sharedPageScope===mountedInfo?.dataset.sharedPageScope&&
     event.detail.querySelector('#oie-live-regions')?.dataset.conversationId===document.body.dataset.conversationId)rememberSharedPage(info,true);
 });
 reducedMotion.addEventListener('change',()=>upgrade(app()));
 async function ready(){inbox.seed(document.getElementById('conversation-list'));await window.goshtosoDependencies?.ready;booted=true;routeState();rememberSharedPage(document.querySelector('#shared-content-pagination [data-shared-page]'));rememberInfoSharedPage(document);syncFilterLinks();upgrade(app());runtime.start();}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ready,{once:true});else ready();
})();
