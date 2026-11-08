window.OieRuntime.register('emoji-picker', life => {
 'use strict';
 const root=document.getElementById('oie-emoji-picker'),input=document.getElementById('message-composer'),form=document.getElementById('send-message-form');
 if(!root||!input||!form)return;
 const content=root.querySelector('[data-emoji-content]'),trigger=root.querySelector('[data-emoji-trigger]'),search=root.querySelector('#emoji-search'),grid=root.querySelector('#emoji-grid'),scroll=root.querySelector('[data-emoji-scroll]'),status=root.querySelector('[data-emoji-status]'),retry=root.querySelector('[data-emoji-retry]'),cell=root.querySelector('[data-emoji-button]'),choice=root.querySelector('[data-emoji-choice]'),sectionTemplate=root.querySelector('[data-emoji-section-template]'),jumps=[...root.querySelectorAll('[data-emoji-jump]')];
 const memory=window.OieRuntime.memory,cacheKey='emoji-catalog-v2:'+content.dataset.emojiUrl,recentKey='oie.emoji-recent.v1',fold=value=>value.normalize('NFD').replace(/\p{M}/gu,'').toLocaleLowerCase(document.documentElement.lang).trim();
 let catalog=memory.get(cacheKey),loading=false,sections=new Map(),scrollFrame=false,selection={start:input.selectionStart||0,end:input.selectionEnd||0},recent=[];
 try{recent=JSON.parse(localStorage.getItem(recentKey)||'[]')}catch{recent=memory.get(recentKey)||[]}
 if(!Array.isArray(recent))recent=[];recent=recent.filter(value=>typeof value==='string').slice(0,24);
 const writable=()=>input.isConnected&&!input.disabled&&!input.readOnly&&form.dataset.busy!=='true'&&form.dataset.composeAllowed==='true';
 const mutate=fn=>window.Alpine?.mutateDom?window.Alpine.mutateDom(fn):fn();
 function clear(node){for(const child of node.children)window.Alpine?.destroyTree(child);node.replaceChildren()}
 function rememberSelection(){selection={start:input.selectionStart,end:input.selectionEnd}}
 for(const name of ['select','keyup','pointerup','input'])life.on(input,name,rememberSelection);
 life.on(trigger,'pointerdown',rememberSelection);
 function setButton(button,item){button.textContent=item.glyph;button.dataset.emojiPick=item.glyph;button.setAttribute('aria-label',item.name);button.title=item.name}
 function createChoice(item){
  const node=(item.family.variants.length>1?choice:cell).content.firstElementChild.cloneNode(true);
  if(item.family.variants.length>1){
   node.dataset.emojiFamily=item.family.base.glyph;setButton(node.querySelector('[data-emoji-pick]'),item.display);
   const toggle=node.querySelector('[data-emoji-variants-trigger]'),label=toggle.dataset.emojiVariantsLabel.replace('{emoji}',item.family.base.name);
   toggle.setAttribute('aria-label',label);toggle.title=label;
  }else setButton(node,item.display);
  return node;
 }
 function updateFooter(){
  const top=scroll.getBoundingClientRect().top;let current=sections.keys().next().value;
  for(const [key,section]of sections)if(section.getBoundingClientRect().top<=top+16)current=key;
  for(const button of jumps){button.disabled=!sections.has(button.dataset.emojiJump);if(button.dataset.emojiJump===current)button.setAttribute('aria-current','location');else button.removeAttribute('aria-current')}
 }
 function jumpTo(key){const section=sections.get(key);if(!section)return;scroll.scrollTop+=section.getBoundingClientRect().top-scroll.getBoundingClientRect().top;section.querySelector('h3').focus({preventScroll:true});updateFooter()}
 function render(){
  if(!catalog)return;
  const terms=fold(search.value).split(/\s+/).filter(Boolean),exact=catalog.items.get(search.value.trim()),preferred=new Map();
  for(const glyph of recent){const item=catalog.items.get(glyph);if(item&&!preferred.has(item.family))preferred.set(item.family,item)}
  const candidates=[...catalog.families.values()].filter(family=>family.variants.some(item=>terms.every(term=>item.match.includes(term))));
  const display=family=>({family,display:exact?.family===family.base.glyph?exact:preferred.get(family.base.glyph)||family.base});
  const groups=new Map();
  if(!terms.length)groups.set('recent',[...preferred.keys()].map(key=>display(catalog.families.get(key))));
  for(let group=0;group<9;group++){const items=candidates.filter(family=>family.base.group===group).map(display);if(items.length)groups.set(String(group),items)}
  const fragment=document.createDocumentFragment();sections=new Map();
  for(const [key,items]of groups){
   const section=sectionTemplate.content.firstElementChild.cloneNode(true);section.dataset.emojiSection=key;section.id='emoji-section-'+key;
   section.querySelector('h3').textContent=jumps.find(button=>button.dataset.emojiJump===key).getAttribute('aria-label');
   const cells=section.querySelector('.oie-emoji-grid');for(const item of items)cells.append(createChoice(item));
   if(!items.length){const empty=document.createElement('p');empty.textContent=content.dataset.emojiNoRecent;section.append(empty)}
   sections.set(key,section);fragment.append(section);
  }
  mutate(()=>{clear(grid);grid.append(fragment);window.Alpine?.initTree(grid)});
  scroll.scrollTop=0;status.textContent=candidates.length?'':content.dataset.emojiEmpty;retry.hidden=true;updateFooter();
 }
 function prepare(data){
  if(!Array.isArray(data.items)||data.items.length>10000||data.locale!==document.documentElement.lang||!data.families||typeof data.families!=='object'||Array.isArray(data.families))throw Error('invalid_catalog');
  const items=new Map(),families=new Map();
  for(const item of data.items){
   if(!Array.isArray(item)||item.length!==4||item.slice(0,3).some(value=>typeof value!=='string'||!value)||!Number.isInteger(item[3])||item[3]<0||item[3]>8||items.has(item[0]))throw Error('invalid_emoji');
   const family=Object.hasOwn(data.families,item[0])?data.families[item[0]]:item[0];if(typeof family!=='string')throw Error('invalid_family');
   items.set(item[0],{glyph:item[0],name:item[1],match:fold(item[0]+' '+item[1]+' '+item[2]),group:item[3],family});
  }
  for(const item of items.values()){
   const base=items.get(item.family);if(!base||base.family!==base.glyph||base.group!==item.group)throw Error('invalid_family');
   if(!families.has(base.glyph))families.set(base.glyph,{base,variants:[base]});
   if(item.glyph!==base.glyph)families.get(base.glyph).variants.push(item);
  }
  return{items,families};
 }
 async function load(){
  if(catalog){render();return}if(loading)return;loading=true;status.textContent=content.dataset.emojiLoading;retry.hidden=true;
  const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
  try{
   const response=await life.fetch(content.dataset.emojiUrl,{credentials:'same-origin',redirect:'error',signal:controller.signal});if(!response.ok)throw Error('catalog_unavailable');
   const data=await response.json();if(life.signal.aborted||!root.isConnected)return;
   catalog=prepare(data);memory.set(cacheKey,catalog);render();
  }catch{if(!life.signal.aborted){status.textContent=content.dataset.emojiFailed;retry.hidden=false}}
  finally{clearTimeout(timeout);loading=false}
 }
 function closeVariants(){for(const button of root.querySelectorAll('[data-emoji-variants-trigger][aria-expanded="true"]'))button.click()}
 function sync(){const disabled=!writable();if(disabled){closeVariants();if(trigger.getAttribute('aria-expanded')==='true')trigger.click()}trigger.disabled=disabled}
 const observer=new MutationObserver(sync);observer.observe(input,{attributes:true,attributeFilter:['readonly','disabled']});observer.observe(form,{attributes:true,attributeFilter:['data-busy','data-compose-allowed']});life.cleanup(()=>observer.disconnect());sync();
 function focusWhenOpen(toggle,target,attempt=0){life.raf(()=>{if(toggle.getAttribute('aria-expanded')!=='true'||document.activeElement===target)return;const active=document.activeElement;if(active!==toggle&&active!==document.body&&!(target===search&&active===input))return;if(target?.getClientRects().length){target.focus({preventScroll:true});if(document.activeElement===target)return}if(attempt<12)focusWhenOpen(toggle,target,attempt+1)})}
 function populateVariants(toggle){
  const wrapper=toggle.closest('[data-emoji-family]'),variants=wrapper?.querySelector('[data-emoji-variants]'),family=catalog?.families.get(wrapper?.dataset.emojiFamily);if(!variants||!family)return;
  if(!variants.childElementCount){const fragment=document.createDocumentFragment();for(const item of family.variants){const button=cell.content.firstElementChild.cloneNode(true);setButton(button,item);button.dataset.emojiVariant='';fragment.append(button)}mutate(()=>{variants.append(fragment);for(const node of variants.children)window.Alpine?.initTree(node)})}
  focusWhenOpen(toggle,variants.querySelector('[data-emoji-pick]'));
 }
 life.on(trigger,'click',()=>{if(!writable())return;life.raf(()=>{if(trigger.getAttribute('aria-expanded')==='true'){load();focusWhenOpen(trigger,search)}})});
 life.on(trigger,'keydown',event=>{if(['Enter',' ','ArrowDown'].includes(event.key)&&writable())life.raf(()=>{load();focusWhenOpen(trigger,search)})});
 life.on(search,'input',render);life.on(search,'keydown',event=>{if(['Enter','ArrowDown'].includes(event.key)){event.preventDefault();grid.querySelector('[data-emoji-pick]')?.focus()}});
 life.on(scroll,'scroll',()=>{if(!scrollFrame){scrollFrame=true;life.raf(()=>{scrollFrame=false;updateFooter()})}},{passive:true});
 life.on(root,'click',event=>{
  const jump=event.target.closest('[data-emoji-jump]');if(jump){jumpTo(jump.dataset.emojiJump);return}if(event.target.closest('[data-emoji-retry]')){load();return}
  const toggle=event.target.closest('[data-emoji-variants-trigger]');if(toggle){populateVariants(toggle);return}
  const chosen=event.target.closest('[data-emoji-pick]');if(!chosen||!writable()||!catalog?.items.has(chosen.dataset.emojiPick))return;
  event.preventDefault();const glyph=chosen.dataset.emojiPick,start=Math.min(selection.start,input.value.length),end=Math.min(selection.end,input.value.length);
  input.setRangeText(glyph,start,end,'end');input.dispatchEvent(new Event('input',{bubbles:true}));rememberSelection();
  recent=[glyph,...recent.filter(value=>value!==glyph)].slice(0,24);memory.set(recentKey,recent);try{localStorage.setItem(recentKey,JSON.stringify(recent))}catch{}
  closeVariants();if(trigger.getAttribute('aria-expanded')==='true')trigger.click();input.focus({preventScroll:true});
 });
 life.on(root,'keydown',event=>{
  const toggle=event.target.closest('[data-emoji-variants-trigger]');if(toggle&&['Enter',' ','ArrowDown'].includes(event.key)){populateVariants(toggle);return}
  const region=event.target.closest('[data-emoji-variants],.oie-emoji-grid');if(!region)return;
  const buttons=[...region.querySelectorAll('[data-emoji-pick]')].filter(button=>button.closest('[data-emoji-variants],.oie-emoji-grid')===region),index=buttons.indexOf(event.target),columns=region.matches('[data-emoji-variants]')?6:8,delta={ArrowLeft:-1,ArrowRight:1,ArrowUp:-columns,ArrowDown:columns}[event.key];
  if(index<0||delta===undefined)return;event.preventDefault();buttons[Math.max(0,Math.min(buttons.length-1,index+delta))]?.focus();
 });
});
