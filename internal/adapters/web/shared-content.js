window.OieRuntime.register('shared-content', life => {
 'use strict';
 const controllers=new Map(),memoryKey='shared-scroll-v1';
 if(!window.OieRuntime.memory.has(memoryKey))window.OieRuntime.memory.set(memoryKey,new Map());
 const memory=window.OieRuntime.memory.get(memoryKey),uuid=value=>/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value||'');
 const keyCompare=(a,b)=>a.ms===b.ms?(a.id>b.id?1:a.id<b.id?-1:0):(a.ms>b.ms?1:-1),sorted=items=>[...items].sort((a,b)=>-keyCompare(a,b));
 function rawItem(node){
  const copy=node.cloneNode(true);
  for(const media of copy.querySelectorAll('video,audio')){media.removeAttribute('src');media.hidden=true;media.removeAttribute('autoplay')}
  for(const source of copy.querySelectorAll('source'))source.removeAttribute('src');
  for(const card of copy.querySelectorAll('[data-video-item]')){const cover=card.querySelector('[data-video-cover]'),retry=card.querySelector('[data-video-retry]'),status=card.querySelector('[data-video-status]');if(cover)cover.hidden=false;if(retry)retry.hidden=true;if(status){status.textContent=card.dataset.videoReady||'';status.dataset.state='ready';status.setAttribute('role','status')}}
  return copy.outerHTML;
 }
 function records(root){
  const found=[],seen=new Set();
  for(const item of root.querySelectorAll('[data-shared-item]')){const id=item.dataset.sharedItem,ms=Number(item.dataset.sharedCreatedMs);if(!uuid(id)||!Number.isSafeInteger(ms)||!/^[-]?\d+$/.test(item.dataset.sharedCreatedMs||'')||seen.has(id))throw Error('invalid_items');seen.add(id);found.push({id,ms,html:rawItem(item)})}
  for(let index=1;index<found.length;index++)if(keyCompare(found[index-1],found[index])<=0)throw Error("unordered_items");return found;
 }
 function group(results){
  if(!results||results.querySelector(':scope>.oie-shared-date-group'))return;
  const items=[...results.querySelectorAll('[data-shared-item]')];if(!items.length)return;
  const fragment=document.createDocumentFragment();let previous='',grid;
  for(const item of items){const date=new Date(Number(item.dataset.sharedCreatedMs)),month=date.getFullYear()+'-'+date.getMonth();if(Number.isNaN(date.getTime()))continue;if(month!==previous){previous=month;const section=document.createElement('section'),heading=document.createElement('h3');section.className='oie-shared-date-group';section.dataset.sharedMonth=month;heading.className='oie-shared-month';heading.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{month:'long',year:'numeric'}).format(date);grid=document.createElement('div');grid.className='oie-shared-grid';section.append(heading,grid);fragment.append(section)}grid.append(item)}
  results.replaceChildren(fragment);
 }
 function replace(target,html){if(window.OieVideos?.swap(target,html))return;const apply=()=>{for(const child of target.children)window.Alpine?.destroyTree(child);target.innerHTML=html;for(const child of target.children)window.Alpine?.initTree(child)};if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply()}
 function pageData(root,info,live=false){
  const prefix=info?'info-shared':'shared-content',resultSelector=live?(info?'#live-info-content #info-shared-results':'#live-shared-results'):'#'+prefix+'-results',pagerSelector=live?(info?'#live-info-content #info-shared-pagination':'#live-shared-pagination'):'#'+prefix+'-pagination';
  const results=root.querySelector(resultSelector),pagination=root.querySelector(pagerSelector),page=pagination?.querySelector('[data-shared-page]');if(!results||!page||!['engine','scope','cursor','next','state'].every(key=>page.hasAttribute('data-shared-page-'+key)))return null;
  const link=page.querySelector('[data-shared-next]');return{results,pagination,page,engine:page.dataset.sharedPageEngine,scope:page.dataset.sharedPageScope,cursor:page.dataset.sharedPageCursor||'',next:page.dataset.sharedPageNext||'',state:page.dataset.sharedPageState,href:link?.getAttribute('href')||'',version:results.dataset.version||''};
 }
 function check(c,data,cursor){if(!data||c.engine!==document.body.dataset.engineId||data.engine!==c.engine||data.scope!==c.scope||data.cursor!==cursor||data.state!=='ready')throw Error('invalid_page');if(data.next&&data.next===cursor)throw Error('repeated_cursor');return records(data.results)}
 function requestURL(c,cursor,href){const url=new URL(href||c.templateURL,location.href);if(url.origin!==location.origin||url.pathname!=='/'||url.hash||url.username||url.password)throw Error('invalid_url');url.searchParams.set(c.cursorName,cursor);if(!cursor)url.searchParams.delete(c.cursorName);return url}
 function capturePreviews(c){
  for(const live of c.results.querySelectorAll('[data-shared-item]')){const item=c.items.get(live.dataset.sharedItem);if(!item)continue;const fragment=document.createElement('template');fragment.innerHTML=item.html;let changed=false;
   for(const preview of live.querySelectorAll('[data-preview-id]')){const original=fragment.content.querySelector(`[data-preview-id="${CSS.escape(preview.dataset.previewId)}"]`);if(!original)continue;for(const key of ['previewRevision','previewTitle','previewDescription','previewSite','previewImage','previewFile','previewState','previewDownload','previewStatus','previewMime','previewSize','previewName'])if(original.dataset[key]!==preview.dataset[key]&&preview.dataset[key]!==undefined){original.dataset[key]=preview.dataset[key];changed=true}}
   if(changed){item.html=fragment.innerHTML;c.rendered.set(item.id,item.html)}
  }
 }
 function save(c){
  if(!c.items.size)return;
  const entry={scope:c.scope,engine:c.engine,base:c.base,items:[...c.items.values()],pages:c.pages,next:c.next,href:c.href,templateURL:c.templateURL,pagerHTML:c.pagerHTML,tailRequest:c.tailRequest,edges:[...c.edges],scroll:c.top};
  const size=JSON.stringify(entry).length;if(size>2*1024*1024){memory.delete(c.cacheKey);return}memory.delete(c.cacheKey);memory.set(c.cacheKey,entry);
  while(memory.size>4||[...memory.values()].reduce((sum,value)=>sum+JSON.stringify(value).length,0)>2*1024*1024)memory.delete(memory.keys().next().value);
 }
 function feedback(c,failed=false){
  const page=c.pagination.querySelector('[data-shared-page]'),status=page?.querySelector('[data-shared-page-status]'),link=page?.querySelector('[data-shared-next]');
  if(status){status.textContent=failed?page.dataset.sharedLoadFailed:c.busy?page.dataset.sharedLoading:'';status.setAttribute('role',failed?'alert':'status')}
  if(link){link.hidden=!c.next&&!failed;link.setAttribute('aria-disabled',String(c.busy));if(failed){link.textContent=page.dataset.sharedRetryLabel||'';try{link.href=requestURL(c,c.retryCursor).pathname+requestURL(c,c.retryCursor).search}catch{}}}
 }
 function refreshPager(c,html){c.pagerHTML=html;replace(c.pagination,html);const link=c.pagination.querySelector('[data-shared-next]');if(link&&c.href)link.setAttribute('href',c.href);observe(c)}
 function render(c,page){
  const top=c.scroll.scrollTop,anchor=[...c.results.querySelectorAll('[data-shared-item]')].find(item=>item.getBoundingClientRect().bottom>=c.scroll.getBoundingClientRect().top),anchorID=anchor?.dataset.sharedItem,anchorTop=anchor?.getBoundingClientRect().top,active=c.results.contains(document.activeElement)?document.activeElement:null,activeID=active?.id;
  const activeItem=active?.closest("[data-shared-item]")?.dataset.sharedItem,activeAction=["data-preview-expand","data-preview-download","data-shared-source","data-image-menu-trigger","data-video-player","data-video-play","data-video-retry"].find(name=>active?.hasAttribute(name));
  group(c.results);const existing=new Map([...c.results.querySelectorAll('[data-shared-item]')].map(node=>[node.dataset.sharedItem,node])),months=new Map();
  for(const item of sorted(c.items.values())){const date=new Date(item.ms),key=date.getFullYear()+'-'+date.getMonth();if(!months.has(key))months.set(key,{date,items:[]});months.get(key).items.push(item)}
  const apply=()=>{
   for(const [id,node]of existing)if(!c.items.has(id)){window.OieVideos?.release(node);window.Alpine?.destroyTree(node);node.remove();c.rendered.delete(id)}
   const sections=new Map([...c.results.querySelectorAll(':scope>.oie-shared-date-group')].map(node=>[node.dataset.sharedMonth,node]));
   for(const node of [...c.results.children])if(!node.matches('.oie-shared-date-group')){window.Alpine?.destroyTree(node);node.remove()}
   for(const [key,node]of sections)if(!months.has(key)){window.OieVideos?.release(node);window.Alpine?.destroyTree(node);node.remove()}
   const keys=[...months.keys()];
   for(let index=0;index<keys.length;index++){
    const key=keys[index],month=months.get(key);let section=sections.get(key);
    if(!section){section=document.createElement('section');section.className='oie-shared-date-group';section.dataset.sharedMonth=key;const heading=document.createElement('h3');heading.className='oie-shared-month';heading.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{month:'long',year:'numeric'}).format(month.date);const grid=document.createElement('div');grid.className='oie-shared-grid';section.append(heading,grid);const before=keys.slice(index+1).map(key=>sections.get(key)).find(node=>node?.isConnected)||null;c.results.insertBefore(section,before);sections.set(key,section)}
    const grid=section.querySelector('.oie-shared-grid');
    for(let n=0;n<month.items.length;n++){
     const item=month.items[n];let node=existing.get(item.id);
     if(!node||!node.isConnected){const template=document.createElement('template');template.innerHTML=item.html;node=template.content.firstElementChild;const before=month.items.slice(n+1).map(item=>existing.get(item.id)).find(node=>node?.parentElement===grid)||null;grid.insertBefore(node,before);window.Alpine?.initTree(node);existing.set(item.id,node)}
     else if(c.rendered.get(item.id)!==item.html){const template=document.createElement('template');template.innerHTML=item.html;const fresh=template.content.firstElementChild;replace(node,fresh.innerHTML);for(const attr of fresh.attributes)node.setAttribute(attr.name,attr.value)}
     c.rendered.set(item.id,item.html);
    }
   }
  };
  if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();
  if(active&&!active.isConnected){const item=activeItem&&c.results.querySelector(`[data-shared-item="${CSS.escape(activeItem)}"]`),target=activeID?document.getElementById(activeID):activeAction?item?.querySelector('['+activeAction+']'):null;target?.focus({preventScroll:true});}
  document.dispatchEvent(new CustomEvent('oie:previews-refresh',{detail:page||document}));document.dispatchEvent(new CustomEvent('oie:shared-append',{detail:{page:page||document}}));document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));const correct=()=>{if(c.disposed||!c.results.isConnected)return;const same=anchorID&&c.results.querySelector(`[data-shared-item="${CSS.escape(anchorID)}"]`);if(same)c.scroll.scrollTop+=same.getBoundingClientRect().top-anchorTop;else c.scroll.scrollTop=top;c.top=c.scroll.scrollTop;save(c)};correct();life.raf(correct);
 }
 function mergeWindow(c,items,head=false,upper=null){
  const incoming=new Set(items.map(item=>item.id)),oldest=items.at(-1);
  for(const [id,item]of c.items)if((!oldest||keyCompare(item,oldest)>=0)&&(head||!upper||keyCompare(item,upper)<0)&&!incoming.has(id)&&!(c.headIDs.has(id)&&!head))c.items.delete(id);
  for(const item of items)if(head||!c.headIDs.has(item.id))c.items.set(item.id,item);
  if(head)c.headIDs=incoming;
 }
 function dispose(c){clearTimeout(c.cacheTimer);capturePreviews(c);save(c);c.disposed=true;c.request?.abort();c.events.abort();c.observer?.disconnect()}
 function observe(c){
  c.observer?.disconnect();if(!c.next||c.failed||c.disposed||c.closed||typeof IntersectionObserver!=='function')return;
  const sentinel=c.pagination.querySelector('[data-shared-page]');if(!sentinel)return;
  c.observer=new IntersectionObserver(entries=>{if(entries.some(entry=>entry.isIntersecting)&&c.results.isConnected&&c.scroll.clientHeight>0&&c.scroll.getClientRects().length)load(c,false)}, {root:c.scroll,rootMargin:'0px 0px 300px 0px'});c.observer.observe(sentinel);
 }
 async function load(c,manual=false,revalidate=null){
  const refreshing=revalidate!==null;if(c.busy||c.disposed||c.closed||!c.results.isConnected||(c.failed&&!manual)||(!refreshing&&!c.next))return;
  const cursor=revalidate??c.next,href=refreshing?null:c.href;let url;try{url=requestURL(c,cursor,href)}catch{c.failed=true;feedback(c,true);return}
  const generation=++c.generation;c.busy=true;if(!refreshing)c.failed=false;feedback(c);const request=new AbortController();c.request=request;const timeout=life.timeout(()=>request.abort(),12000),oldest=sorted(c.items.values()).at(-1);
  try{
   const response=await life.fetch(url.pathname+url.search,{credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'HX-Request':'true','X-Oie-Fragment':c.info?'info':'app'},signal:request.signal});if(!response.ok)throw Error('unavailable');const page=new DOMParser().parseFromString(await response.text(),'text/html');if(c.disposed||c.closed||generation!==c.generation||!c.results.isConnected||controllers.get(c.prefix)!==c)return;
   capturePreviews(c);const data=pageData(page,c.info),items=check(c,data,cursor),newItems=items.filter(item=>!c.items.has(item.id)),upper=c.edges.get(cursor);if(items.some(item=>upper&&keyCompare(item,upper)>=0)||items.some(item=>c.items.has(item.id)&&c.items.get(item.id).ms!==item.ms))throw Error('invalid_boundary');if(!items.length&&data.next)throw Error('nonadvancing_page');if(!upper&&cursor!==c.base)throw Error('unknown_boundary');
   if(!refreshing&&((items.length&&!newItems.length)||(!items.length&&data.next)||c.used.has(data.next)&&data.next))throw Error('nonadvancing_page');
   if(data.next&&items.length)c.edges.set(data.next,items.at(-1));
   if(refreshing){const overlap=items.some(item=>c.items.has(item.id));mergeWindow(c,items,false,upper);if(!overlap&&data.next&&oldest&&items.length&&keyCompare(items.at(-1),oldest)>0)c.bridge=data.next;else if(c.bridge===cursor)c.bridge='';const extendsTail=items.length&&(!oldest||keyCompare(items.at(-1),oldest)<0);if(cursor===c.tailRequest||extendsTail){c.next=data.next;c.href=data.href;c.tailRequest=cursor;refreshPager(c,data.pagination.innerHTML)}if(!c.pages.includes(cursor))c.pages.push(cursor)}
   else{for(const item of items)if(!c.headIDs.has(item.id))c.items.set(item.id,item);c.pages.push(cursor);c.tailRequest=cursor;c.used.add(cursor);c.next=data.next;c.href=data.href;refreshPager(c,data.pagination.innerHTML)}
   render(c,page);c.failed=false;c.retryMode=null;
  }catch(error){if(c.disposed||c.closed||generation!==c.generation||life.signal.aborted)return;c.failed=true;c.retryMode=refreshing?'refresh':'append';c.retryCursor=cursor;feedback(c,true);c.observer?.disconnect()}
  finally{clearTimeout(timeout);if(!c.disposed&&generation===c.generation){c.busy=false;c.request=null;feedback(c,c.failed);observe(c);save(c)}}
 }
 function bind(info){
  const prefix=info?'info-shared':'shared-content',data=pageData(document,info),scroll=document.getElementById(info?'details-body':'shared-content-scroll'),existing=controllers.get(prefix);
  if(existing&&data&&existing.results===data.results&&existing.scope===data.scope)return existing;
  if(existing){dispose(existing);controllers.delete(prefix)}if(!data||!scroll||data.state!=='ready'||!uuid(data.engine)||!/^([0-9a-f]{64})$/.test(data.scope))return null;
  let initial;try{initial=records(data.results)}catch{return null}
  const c={prefix,info,results:data.results,pagination:data.pagination,scroll,engine:data.engine,scope:data.scope,base:data.cursor,cursorName:info?'infoCursor':'contentCursor',next:data.next,href:data.href,templateURL:data.href||location.href,items:new Map(initial.map(item=>[item.id,item])),headIDs:new Set(initial.map(item=>item.id)),pages:[data.cursor],used:new Set([data.cursor]),cacheKey:data.scope+':'+data.cursor,rendered:new Map(initial.map(item=>[item.id,item.html])),pagerHTML:data.pagination.innerHTML,tailRequest:data.cursor,edges:new Map(data.next&&initial.length?[[data.next,initial.at(-1)]]:[]),bridge:'',headNext:data.next,closed:false,generation:0,eofPriority:false,events:new AbortController(),top:scroll.scrollTop,busy:false,failed:false,disposed:false,round:0,lastRefresh:0};controllers.set(prefix,c);
  const cached=memory.get(c.cacheKey);if(cached&&cached.engine===c.engine&&cached.scope===c.scope&&cached.base===c.base){c.items=new Map(cached.items.map(item=>[item.id,item]));mergeWindow(c,initial,true);c.pages=[...cached.pages];c.used=new Set(c.pages);c.next=cached.next;c.href=cached.href;c.templateURL=cached.templateURL;c.tailRequest=cached.tailRequest??c.pages.at(-1);c.edges=new Map(cached.edges||[]);if(data.next&&initial.length)c.edges.set(data.next,initial.at(-1));c.top=cached.scroll;refreshPager(c,cached.pagerHTML||data.pagination.innerHTML);render(c,document);life.raf(()=>{if(!c.disposed){c.scroll.scrollTop=cached.scroll;c.top=c.scroll.scrollTop;observe(c)}})}else{group(data.results);life.raf(()=>observe(c))}
  scroll.addEventListener('scroll',()=>{if(c.results.isConnected){c.top=scroll.scrollTop;clearTimeout(c.cacheTimer);c.cacheTimer=life.timeout(()=>{if(!c.disposed)save(c)},200)}},{passive:true,signal:c.events.signal});return c;
 }
 function update(page,info){
  const c=bind(info);if(!c||c.disposed)return;const data=pageData(page,info,true);let items;try{items=check(c,data,c.base)}catch{return}
  capturePreviews(c);const hasTail=c.pages.length>1||c.items.size>c.headIDs.size;mergeWindow(c,items,true);if(c.headNext!==data.next)c.bridge=data.next;c.headNext=data.next;if(data.next&&items.length)c.edges.set(data.next,items.at(-1));
  if(!hasTail){c.next=data.next;c.href=data.href;refreshPager(c,data.pagination.innerHTML)}
  render(c,page);const status=document.getElementById(c.prefix+'-status'),freshStatus=page.querySelector(info?'#live-info-content #info-shared-status':'#live-shared-status');if(status&&freshStatus&&!status.contains(document.activeElement)&&status.innerHTML!==freshStatus.innerHTML)replace(status,freshStatus.innerHTML);
  // One paced older window per live refresh. Include the current head boundary
  // so items pushed out of that window are eventually checked, not immortal.
  if(hasTail&&!c.busy&&!c.failed&&Date.now()-c.lastRefresh>=5000){const cursors=[...new Set([data.next,...c.pages.filter(cursor=>cursor!==c.base)].filter(Boolean))],cursor=!c.next&&c.pages.length>1&&!c.eofPriority?c.tailRequest:c.bridge||cursors[c.round++%cursors.length];c.eofPriority=!c.next&&!c.eofPriority;if(cursor){c.lastRefresh=Date.now();const generation=c.generation;life.timeout(()=>{if(!c.failed&&c.generation===generation)load(c,false,cursor)},150)}}
  if(c.failed)feedback(c,true);observe(c);
 }
 bind(false);bind(true);
 life.on(document,'click',event=>{const link=event.target.closest('a[data-shared-next]');if(!link||event.button>0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;const c=[...controllers.values()].find(c=>c.pagination.contains(link));if(!c)return;event.preventDefault();event.stopImmediatePropagation();load(c,true,c.failed&&c.retryMode==='refresh'?c.retryCursor:null)},true);
 life.on(document,'oie:info-content',()=>{bind(true)});
 life.on(document,'oie:shared-search-settled',()=>bind(false));
 life.on(document,'htmx:before:request',event=>{const id=event.detail.ctx?.target?.id;if(!['oie-app','chat-info-content','shared-search-results'].includes(id))return;for(const c of controllers.values())if(id!=='chat-info-content'||c.info){c.generation++;c.request?.abort();c.request=null;c.busy=false;c.observer?.disconnect();capturePreviews(c);save(c)}},{capture:true});
 for(const name of ['drawer:close','drawer:close-request'])life.on(window,name,event=>{if(event.detail?.id==='details')life.timeout(()=>{if(!event.defaultPrevented){const c=controllers.get('info-shared');if(c){c.closed=true;c.generation++;c.request?.abort();c.request=null;c.busy=false;c.observer?.disconnect();capturePreviews(c);feedback(c,c.failed);save(c)}}},0)});
 life.on(window,'drawer:open',event=>{if(event.detail?.id==='details'){const c=bind(true);if(c){c.closed=false;life.timeout(()=>observe(c),250)}}});
 life.on(document,'oie:shared-refresh',event=>{update(event.detail,false);update(event.detail,true)});
 const observer=new MutationObserver(()=>{for(const [key,c]of controllers)if(!c.results.isConnected){dispose(c);controllers.delete(key)}});observer.observe(document.getElementById('oie-app')||document.body,{childList:true,subtree:true});
 life.cleanup(()=>{observer.disconnect();for(const c of controllers.values())dispose(c);controllers.clear()});
});

// Only results are replaced while typing. The form remains the native GET
// fallback, and explicit submissions/account changes use ordinary app routing.
window.OieRuntime.register('shared-search', life => {
 'use strict';
 const form=document.getElementById('shared-content-search'),input=document.getElementById('shared-search');
 if(!form||!input)return;
 let accepted=null,pending=0,composing=false;const active=new Set();
 const status=()=>document.querySelector('[data-navigation-status]');
 function announce(error=false){const node=status();if(!node)return;node.textContent=document.getElementById('oie-app')?.dataset[error?'routeError':'routeLoading']||'';node.hidden=false;}
 function scope(){return new URLSearchParams(new FormData(form)).toString();}
 function alignTabs(){const values=new FormData(form);for(const link of document.querySelectorAll('[data-shared-tab]')){const url=new URL(link.href);for(const key of ['q','account']){const value=values.get(key)||'';if(value)url.searchParams.set(key,value);else url.searchParams.delete(key);}link.href=url.pathname+url.search;}}
 function owned(ctx){return ctx?.target?.id==='shared-search-results'&&ctx.sourceElement===input;}
 function schedule(){clearTimeout(pending);pending=setTimeout(()=>{if(!life.signal.aborted&&!composing&&input.isConnected&&input.value!==document.getElementById('shared-search-results')?.dataset.searchQuery)input.dispatchEvent(new CustomEvent('oie:shared-search',{bubbles:true}));},300);}
 life.on(input,'input',event=>{if(!event.isComposing&&!composing){alignTabs();schedule();}});
 life.on(input,'compositionstart',()=>{composing=true;clearTimeout(pending);});
 life.on(input,'compositionend',()=>{composing=false;alignTabs();schedule();});
 life.on(form,'submit',()=>clearTimeout(pending),{capture:true});
 life.on(form,'combobox:change',alignTabs);
 life.on(document,'htmx:before:request',event=>{
  const ctx=event.detail.ctx;
  // Abort before navigation begins, rather than during its DOM swap, so the
  // old request cannot settle while Alpine initializes the destination screen.
  if(ctx?.target?.id==='oie-app'){for(const old of active)old.request.abort();active.clear();}
  if(!owned(ctx))return;
  active.add(ctx);
  ctx.oieSearch={form,scope:scope(),generation:Number(document.body.dataset.starGeneration||0)};
  accepted=null;document.getElementById('shared-search-results')?.setAttribute('aria-busy','true');announce();
 },{capture:true});
 life.on(document,'htmx:finally:request',event=>active.delete(event.detail.ctx),{capture:true});
 life.cleanup(()=>{clearTimeout(pending);for(const ctx of active)ctx.request.abort();active.clear();});
 life.on(document,'htmx:before:swap',event=>{
  const ctx=event.detail.ctx;if(!owned(ctx))return;
  const request=ctx.oieSearch;
  if(!request||!form.isConnected||document.getElementById('shared-content-search')!==form||request.scope!==scope()){
   event.preventDefault();return;
  }
  const page=new DOMParser().parseFromString(ctx.text||'','text/html');
  if(!page.querySelector('#shared-search-results')){event.preventDefault();announce(true);return;}
  accepted={...request,page};
 },{capture:true});
 life.on(document,'htmx:after:settle',event=>{
  const root=event.detail.task?.target;
  if(root?.id!=='shared-search-results'||!root.isConnected||root!==document.getElementById(root.id)||!accepted)return;
  const fresh=accepted;accepted=null;root.removeAttribute('aria-busy');
  document.dispatchEvent(new CustomEvent('oie:shared-search-settled',{detail:{root,page:fresh.page}}));
  document.dispatchEvent(new CustomEvent('oie:stars-refresh',{detail:{page:fresh.page,generation:fresh.generation}}));
  document.dispatchEvent(new CustomEvent('oie:previews-refresh',{detail:fresh.page}));
  document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));
  const node=status();if(node)node.hidden=true;
 });
 life.on(document,'htmx:error',event=>{if(owned(event.detail.ctx)){document.getElementById('shared-search-results')?.removeAttribute('aria-busy');announce(true);}});
});
