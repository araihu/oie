(() => {
 'use strict';
 const results=document.querySelector('#shared-content-results'),scroll=document.querySelector('#shared-content-scroll');
 if(!results||!scroll)return;
 function group(){
  const items=[...results.querySelectorAll('[data-shared-item]')];if(!items.length)return;
  const apply=()=>{const fragment=document.createDocumentFragment();let key='',grid;
  for(const item of items){
   const at=new Date(item.dataset.sharedCreatedAt),next=at.getFullYear()+'-'+at.getMonth();
   if(next!==key){key=next;const section=document.createElement('section'),heading=document.createElement('h3');section.className='oie-shared-date-group';heading.className='oie-shared-month';heading.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{month:'long',year:'numeric'}).format(at);grid=document.createElement('div');grid.className='oie-shared-grid';section.append(heading,grid);fragment.append(section);}
   grid.append(item);
  }
  results.replaceChildren(fragment);};if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();
 }
 group();
 function replace(target,html){const apply=()=>{for(const child of target.children)window.Alpine?.destroyTree(child);target.innerHTML=html;for(const child of target.children)window.Alpine?.initTree(child);};if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();}
 document.addEventListener('oie:shared-refresh',event=>{
  const fresh=event.detail.querySelector('#live-shared-results');if(!fresh)return;
  const top=scroll.scrollTop,follow=top<20,anchor=[...results.querySelectorAll('[data-shared-item]')].find(item=>item.getBoundingClientRect().bottom>=scroll.getBoundingClientRect().top),anchorID=anchor?.dataset.sharedItem,anchorTop=anchor?.getBoundingClientRect().top;
  const active=results.contains(document.activeElement)?document.activeElement:null,id=active?.id,itemID=active?.closest('[data-shared-item]')?.dataset.sharedItem,previewAction=active?.matches('[data-preview-expand]')?'data-preview-expand':active?.matches('[data-preview-download]')?'data-preview-download':'';
  if(results.dataset.version!==fresh.dataset.version){replace(results,fresh.innerHTML);results.dataset.version=fresh.dataset.version;group();const item=itemID&&results.querySelector(`[data-shared-item="${CSS.escape(itemID)}"]`);const target=(id&&document.getElementById(id))||(previewAction&&item?.querySelector('['+previewAction+']'));target?.focus({preventScroll:true});}
  for(const name of ['status','pagination']){const current=document.querySelector('#shared-content-'+name),next=event.detail.querySelector('#live-shared-'+name);if(current&&next&&current.innerHTML!==next.innerHTML&&!current.contains(document.activeElement))replace(current,next.innerHTML);}
  requestAnimationFrame(()=>{const same=anchorID&&results.querySelector(`[data-shared-item="${CSS.escape(anchorID)}"]`);scroll.scrollTop=follow?0:same?top+same.getBoundingClientRect().top-anchorTop:top;});
 });
})();
