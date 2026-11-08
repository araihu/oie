window.OieRuntime.register('shared-content', life => {
 'use strict';
 const grouped=new WeakMap();
 function group(results){
  if(!results||grouped.get(results)===results.dataset.version)return;
  const items=[...results.querySelectorAll('[data-shared-item]')];if(!items.length){grouped.set(results,results.dataset.version);return;}
  const apply=()=>{const fragment=document.createDocumentFragment();let key='',grid;
   for(const item of items){
    const at=new Date(item.dataset.sharedCreatedAt);if(Number.isNaN(at.getTime()))continue;
    const next=at.getFullYear()+'-'+at.getMonth();
    if(next!==key){key=next;const section=document.createElement('section'),heading=document.createElement('h3');section.className='oie-shared-date-group';heading.className='oie-shared-month';heading.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{month:'long',year:'numeric'}).format(at);grid=document.createElement('div');grid.className='oie-shared-grid';section.append(heading,grid);fragment.append(section);}
    grid.append(item);
   }
   results.replaceChildren(fragment);
  };
  if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();grouped.set(results,results.dataset.version);
 }
 function groupAll(){group(document.querySelector('#shared-content-results'));group(document.querySelector('#info-shared-results'));}
 function replace(target,html){const apply=()=>{for(const child of target.children)window.Alpine?.destroyTree(child);target.innerHTML=html;for(const child of target.children)window.Alpine?.initTree(child);};if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();}
 function update(page,info){
  const prefix=info?'info-shared':'shared-content',results=document.getElementById(prefix+'-results'),scroll=document.getElementById(info?'details-body':'shared-content-scroll');
  const fresh=page.querySelector(info?'#live-info-content #info-shared-results':'#live-shared-results');if(!results||!scroll||!fresh)return;
  const top=scroll.scrollTop,follow=top<20,anchor=[...results.querySelectorAll('[data-shared-item]')].find(item=>item.getBoundingClientRect().bottom>=scroll.getBoundingClientRect().top),anchorID=anchor?.dataset.sharedItem,anchorTop=anchor?.getBoundingClientRect().top;
  const active=results.contains(document.activeElement)?document.activeElement:null,id=active?.id,itemID=active?.closest('[data-shared-item]')?.dataset.sharedItem,previewAction=active?.matches('[data-preview-expand]')?'data-preview-expand':active?.matches('[data-preview-download]')?'data-preview-download':'';
  if(results.dataset.version!==fresh.dataset.version){replace(results,fresh.innerHTML);results.dataset.version=fresh.dataset.version;group(results);const item=itemID&&results.querySelector(`[data-shared-item="${CSS.escape(itemID)}"]`);const target=(id&&document.getElementById(id))||(previewAction&&item?.querySelector('['+previewAction+']'));target?.focus({preventScroll:true});}
  for(const name of ['status','pagination']){const current=document.getElementById(prefix+'-'+name),next=page.querySelector(info?'#live-info-content #info-shared-'+name:'#live-shared-'+name);if(current&&next&&current.innerHTML!==next.innerHTML&&!current.contains(document.activeElement))replace(current,next.innerHTML);}
  life.raf(()=>{const same=anchorID&&results.querySelector(`[data-shared-item="${CSS.escape(anchorID)}"]`);scroll.scrollTop=follow?0:same?top+same.getBoundingClientRect().top-anchorTop:top;});
 }
 groupAll();
 life.on(document,'oie:info-content',groupAll);
 life.on(document,'oie:shared-refresh',event=>{update(event.detail,false);update(event.detail,true);document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));});
});
