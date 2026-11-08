window.OieRuntime.register('shared-content', life => {
 'use strict';
 const grouped=new WeakMap();
 function group(results){
  if(!results||grouped.get(results)===results.dataset.version)return;
  if(results.querySelector(':scope>.oie-shared-date-group')){grouped.set(results,results.dataset.version);return;}
  const items=[...results.querySelectorAll('[data-shared-item]')];if(!items.length){grouped.set(results,results.dataset.version);return;}
  const apply=()=>{const fragment=document.createDocumentFragment();let key='',grid;
   for(const item of items){
    const at=new Date(item.dataset.sharedCreatedAt);if(Number.isNaN(at.getTime()))continue;
    const next=at.getFullYear()+'-'+at.getMonth();
    if(next!==key){key=next;const section=document.createElement('section'),heading=document.createElement('h3');section.className='oie-shared-date-group';section.dataset.sharedMonth=next;heading.className='oie-shared-month';heading.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{month:'long',year:'numeric'}).format(at);grid=document.createElement('div');grid.className='oie-shared-grid';section.append(heading,grid);fragment.append(section);}
    grid.append(item);
   }
   results.replaceChildren(fragment);
  };
  if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();grouped.set(results,results.dataset.version);
 }
 function groupAll(){group(document.querySelector('#shared-content-results'));group(document.querySelector('#info-shared-results'));}
 function replace(target,html){if(window.OieVideos?.swap(target,html))return;const apply=()=>{for(const child of target.children)window.Alpine?.destroyTree(child);target.innerHTML=html;for(const child of target.children)window.Alpine?.initTree(child);};if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();}
 function update(page,info){
  const prefix=info?'info-shared':'shared-content',results=document.getElementById(prefix+'-results'),scroll=document.getElementById(info?'details-body':'shared-content-scroll');
  const fresh=page.querySelector(info?'#live-info-content #info-shared-results':'#live-shared-results');if(!results||!scroll||!fresh)return;
  const top=scroll.scrollTop,follow=top<20,anchor=[...results.querySelectorAll('[data-shared-item]')].find(item=>item.getBoundingClientRect().bottom>=scroll.getBoundingClientRect().top),anchorID=anchor?.dataset.sharedItem,anchorTop=anchor?.getBoundingClientRect().top;
  const active=results.contains(document.activeElement)?document.activeElement:null,id=active?.id,itemID=active?.closest('[data-shared-item]')?.dataset.sharedItem,previewAction=active?.matches('[data-preview-expand]')?'data-preview-expand':active?.matches('[data-preview-download]')?'data-preview-download':'';
  if(results.dataset.version!==fresh.dataset.version){group(fresh);replace(results,fresh.innerHTML);results.dataset.version=fresh.dataset.version;group(results);const item=itemID&&results.querySelector(`[data-shared-item="${CSS.escape(itemID)}"]`);const target=(id&&document.getElementById(id))||(previewAction&&item?.querySelector('['+previewAction+']'));if(target instanceof HTMLElement)target.focus({preventScroll:true});}
  for(const name of ['status','pagination']){const current=document.getElementById(prefix+'-'+name),next=page.querySelector(info?'#live-info-content #info-shared-'+name:'#live-shared-'+name);if(current&&next&&current.innerHTML!==next.innerHTML&&!current.contains(document.activeElement))replace(current,next.innerHTML);}
  life.raf(()=>{const same=anchorID&&results.querySelector(`[data-shared-item="${CSS.escape(anchorID)}"]`);scroll.scrollTop=follow?0:same?top+same.getBoundingClientRect().top-anchorTop:top;});
 }
 groupAll();
 life.on(document,'oie:info-content',groupAll);
 life.on(document,'oie:shared-search-settled',groupAll);
 life.on(document,'oie:shared-refresh',event=>{update(event.detail,false);update(event.detail,true);document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));});
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
