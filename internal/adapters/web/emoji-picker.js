// One catalog, category list, recent history and tone picker for both entry points.
window.OieEmojiPicker={mount(root,life,options){
 'use strict';
 options=options||{};
 const content=root.querySelector('[data-emoji-content]');if(!content)return null;
 const search=content.querySelector('[data-emoji-search]'),grid=content.querySelector('[data-emoji-grid]'),scroll=content.querySelector('[data-emoji-scroll]'),status=content.querySelector('[data-emoji-status]'),retry=content.querySelector('[data-emoji-retry]'),cell=content.querySelector('[data-emoji-button]'),choice=content.querySelector('[data-emoji-choice]'),sectionTemplate=content.querySelector('[data-emoji-section-template]'),jumps=[...content.querySelectorAll('[data-emoji-jump]')];
 const memory=window.OieRuntime.memory,cacheKey='emoji-catalog-v2:'+content.dataset.emojiUrl,recentKey='oie.emoji-recent.v1',fold=value=>value.normalize('NFD').replace(/\p{M}/gu,'').toLocaleLowerCase(document.documentElement.lang).trim();
 let catalog=memory.get(cacheKey),loading=false,sections=new Map(),scrollFrame=false,variantGeometry=null,recent=[];
 function readRecent(){try{recent=JSON.parse(localStorage.getItem(recentKey)||'[]')}catch{recent=memory.get(recentKey)||[]}if(!Array.isArray(recent))recent=[];recent=recent.filter(value=>typeof value==='string').slice(0,24)}
 function remember(glyph){readRecent();recent=[glyph,...recent.filter(value=>value!==glyph)].slice(0,24);memory.set(recentKey,recent);try{localStorage.setItem(recentKey,JSON.stringify(recent))}catch{}}
 const writable=()=>root.isConnected&&(options.available?.()??true),allowed=glyph=>options.allowed?.(glyph)??true,enabled=glyph=>writable()&&(options.enabled?.(glyph)??true);
 const mutate=fn=>window.Alpine?.mutateDom?window.Alpine.mutateDom(fn):fn();
 function clear(node){for(const child of node.children)window.Alpine?.destroyTree(child);node.replaceChildren()}
 // Bitmap emoji can leave unequal transparent margins inside the same line box.
 // Measure visible glyphs once and move only their artwork, keeping the hit area fixed.
 const inkOffsets=new Map(),sampleSize=48,sampleScale=2;
 const canvas=document.createElement('canvas');
 canvas.width=canvas.height=sampleSize*sampleScale;
 const ink=canvas.getContext('2d',{willReadFrequently:true});
 let inkFont='';
 function inkOffset(emoji){
  const midpoint=sampleSize/2,metrics=ink.measureText(emoji);
  const baseline=midpoint+(metrics.fontBoundingBoxAscent-metrics.fontBoundingBoxDescent)/2;
  if(!Number.isFinite(baseline)||metrics.width>=sampleSize)return '0px';
  ink.clearRect(0,0,sampleSize,sampleSize);
  ink.fillText(emoji,midpoint,baseline);
  const size=canvas.width,alpha=ink.getImageData(0,0,size,size).data;
  let left=size,right=-1,top=size,bottom=-1;
  for(let y=0;y<size;y++)for(let x=0;x<size;x++){
   if(alpha[(y*size+x)*4+3]<32)continue;
   left=Math.min(left,x);right=Math.max(right,x);
   top=Math.min(top,y);bottom=Math.max(bottom,y);
  }
  if(right<0||left===0||top===0||right===size-1||bottom===size-1)return '0px';
  const shift=(start,end)=>Math.round((midpoint-(start+end+1)/(2*sampleScale))*4)/4;
  return shift(left,right)+'px '+shift(top,bottom)+'px';
 }
 function centerGlyph(button){
  const glyph=button?.firstElementChild;
  if(!ink||!glyph||glyph.style.translate)return;
  if(!inkFont){
   const style=getComputedStyle(button);
   inkFont=[style.fontWeight,style.fontSize,style.fontFamily].join(' ');
   ink.scale(sampleScale,sampleScale);
   ink.font=inkFont;
   ink.textAlign='center';
  }
  const emoji=button.dataset.emojiPick;
  if(!inkOffsets.has(emoji)){
   let offset='0px';
   try{offset=inkOffset(emoji)}catch{}
   inkOffsets.set(emoji,offset);
  }
  glyph.style.translate=inkOffsets.get(emoji);
 }
 const visibleGlyphs=ink&&typeof IntersectionObserver==='function'?new IntersectionObserver(entries=>{
  for(const {target,isIntersecting} of entries)if(isIntersecting){
   centerGlyph(target);visibleGlyphs.unobserve(target);
  }
 },{root:scroll,rootMargin:'80px 0px'}):null;
 life.cleanup(()=>visibleGlyphs?.disconnect());
 for(const event of ['pointerover','focusin'])life.on(root,event,e=>centerGlyph(e.target.closest('[data-emoji-pick]')));
 function setButton(button,item){
  const glyph=document.createElement('span');
  glyph.className='oie-emoji-glyph';
  glyph.textContent=item.glyph;
  if(inkOffsets.has(item.glyph))glyph.style.translate=inkOffsets.get(item.glyph);
  button.append(glyph);
  button.dataset.emojiPick=item.glyph;
  button.setAttribute('aria-label',item.name);
  button.title=item.name;
  button.disabled=!enabled(item.glyph);
  if(options.selected)button.setAttribute('aria-pressed',String(options.selected(item.glyph)));
 }
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
  // A cached open can finish after ArrowDown already entered the results.
  // Preserve the selected app-owned emoji when rebuilding that same grid.
  const active=grid.contains(document.activeElement)?document.activeElement:null,glyph=active?.dataset.emojiPick,section=active?.closest('[data-emoji-section]')?.dataset.emojiSection;
  const terms=fold(search.value).split(/\s+/).filter(Boolean),exact=catalog.items.get(search.value.trim()),preferred=new Map();
  for(const glyph of recent){const item=catalog.items.get(glyph);if(item&&allowed(item.glyph)&&!preferred.has(item.family))preferred.set(item.family,item)}
  const available=[...catalog.families.values()].map(family=>({...family,variants:family.variants.filter(item=>allowed(item.glyph))})).filter(family=>family.variants.length);
  const candidates=available.filter(family=>family.variants.some(item=>terms.every(term=>item.match.includes(term))));
  const display=family=>({family,display:exact?.family===family.base.glyph&&allowed(exact.glyph)?exact:preferred.get(family.base.glyph)||family.variants.find(item=>item.glyph===family.base.glyph)||family.variants[0]});
  const groups=new Map();
  if(!terms.length)groups.set('recent',available.filter(family=>preferred.has(family.base.glyph)).sort((a,b)=>[...preferred.keys()].indexOf(a.base.glyph)-[...preferred.keys()].indexOf(b.base.glyph)).map(display));
  for(let group=0;group<9;group++){const items=candidates.filter(family=>family.base.group===group).map(display);if(items.length)groups.set(String(group),items)}
  const fragment=document.createDocumentFragment();sections=new Map();
  for(const [key,items]of groups){
   const section=sectionTemplate.content.firstElementChild.cloneNode(true);section.dataset.emojiSection=key;section.id=content.dataset.emojiPrefix+'-section-'+key;
   section.querySelector('h3').textContent=jumps.find(button=>button.dataset.emojiJump===key).getAttribute('aria-label');
   const cells=section.querySelector('.oie-emoji-grid');for(const item of items)cells.append(createChoice(item));
   if(!items.length){const empty=document.createElement('p');empty.textContent=content.dataset.emojiNoRecent;section.append(empty)}
   sections.set(key,section);fragment.append(section);
  }
  visibleGlyphs?.disconnect();
  stopVariantGeometry();mutate(()=>{clear(grid);grid.append(fragment);window.Alpine?.initTree(grid)});
  if(visibleGlyphs)for(const button of grid.querySelectorAll('[data-emoji-pick]')){
   if(!button.firstElementChild.style.translate)visibleGlyphs.observe(button);
  }
  scroll.scrollTop=0;status.textContent=candidates.length?'':content.dataset.emojiEmpty;retry.hidden=true;
  if(glyph){const replacement=[...grid.querySelectorAll('[data-emoji-pick]')].find(button=>button.dataset.emojiPick===glyph&&button.closest('[data-emoji-section]')?.dataset.emojiSection===section);(replacement||search).focus()}
  updateFooter();
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
 // The public popover owns positioning and collision handling. Center its
 // app-owned strip with CSS translation and keep the decorative tip on the
 // originating emoji. Its content reserves space for the tip inside overflow.
 function stopVariantGeometry(){if(variantGeometry){variantGeometry.observer.disconnect();variantGeometry=null}}
 function positionVariant(){
  const value=variantGeometry;if(!value||!value.panel.isConnected||value.toggle.getAttribute('aria-expanded')!=='true')return;
  const {panel,toggle}=value,anchor=toggle.closest('[data-emoji-family]').querySelector(':scope>[data-emoji-pick]').getBoundingClientRect(),rect=panel.getBoundingClientRect();if(!rect.width)return;
  const viewport=window.visualViewport,left=(viewport?.offsetLeft||0)+8,right=(viewport?.offsetLeft||0)+(viewport?.width||document.documentElement.clientWidth)-8;
  const previous=parseFloat(panel.style.getPropertyValue('--oie-emoji-shift-x'))||0,width=panel.offsetWidth,desired=Math.max(left,Math.min(anchor.left+anchor.width/2-width/2,right-width)),shift=Math.round((desired-(rect.left-previous))*10)/10;
  if(Math.abs(shift-previous)>.2)panel.style.setProperty('--oie-emoji-shift-x',shift+'px');
  const previousY=parseFloat(panel.style.getPropertyValue('--oie-emoji-shift-y'))||0,baseTop=rect.top-previousY,height=panel.offsetHeight,side=baseTop+height/2<anchor.top+anchor.height/2?'top':'bottom',top=(viewport?.offsetTop||0)+8,bottom=(viewport?.offsetTop||0)+(viewport?.height||innerHeight)-8;
  const desiredY=Math.max(top,Math.min(side==='top'?anchor.top-height-2:anchor.bottom+2,bottom-height)),shiftY=Math.round((desiredY-baseTop)*10)/10;
  if(Math.abs(shiftY-previousY)>.2)panel.style.setProperty('--oie-emoji-shift-y',shiftY+'px');if(panel.dataset.emojiSide!==side)panel.dataset.emojiSide=side;
  const bubble=panel.querySelector('.oie-emoji-variants-bubble').getBoundingClientRect(),tip=Math.max(13,Math.min(anchor.left+anchor.width/2-bubble.left,bubble.width-13));
  const next=Math.round(tip*10)/10+'px';if(panel.style.getPropertyValue('--oie-emoji-tip-x')!==next)panel.style.setProperty('--oie-emoji-tip-x',next);
 }
 function watchVariantGeometry(toggle){
  stopVariantGeometry();const panel=toggle.closest('[data-emoji-family]').querySelector('.oie-emoji-variants-panel');
  const observer=new MutationObserver(()=>{if(toggle.getAttribute('aria-expanded')!=='true'){stopVariantGeometry();return}life.raf(positionVariant)});
  variantGeometry={toggle,panel,observer};observer.observe(panel,{attributes:true,attributeFilter:['style','class']});observer.observe(toggle,{attributes:true,attributeFilter:['aria-expanded']});life.raf(positionVariant);
 }
 life.cleanup(stopVariantGeometry);
 function closeVariants(){for(const button of root.querySelectorAll('[data-emoji-variants-trigger][aria-expanded="true"]'))button.click();stopVariantGeometry()}

 function focusWhenOpen(toggle,target,attempt=0){life.raf(()=>{if(toggle.getAttribute('aria-expanded')!=='true'||document.activeElement===target)return;const active=document.activeElement;if(active!==toggle&&active!==document.body&&!(target===search&&active===options.input))return;if(target?.getClientRects().length){target.focus({preventScroll:true});if(document.activeElement===target)return}if(attempt<12)focusWhenOpen(toggle,target,attempt+1)})}
 function populateVariants(toggle){
  const wrapper=toggle.closest('[data-emoji-family]'),variants=wrapper?.querySelector('[data-emoji-variants]'),family=catalog?.families.get(wrapper?.dataset.emojiFamily);if(!variants||!family)return;
  if(!variants.childElementCount){const fragment=document.createDocumentFragment();for(const item of family.variants.filter(item=>allowed(item.glyph))){const button=cell.content.firstElementChild.cloneNode(true);setButton(button,item);button.dataset.emojiVariant='';button.setAttribute('aria-pressed',String(options.selected?options.selected(item.glyph):item.glyph===wrapper.querySelector(':scope>[data-emoji-pick]').dataset.emojiPick));fragment.append(button)}mutate(()=>{variants.append(fragment);for(const node of variants.children)window.Alpine?.initTree(node)})}
  for(const button of variants.children)centerGlyph(button);
  if(toggle.getAttribute('aria-expanded')==='true')watchVariantGeometry(toggle);
  focusWhenOpen(toggle,variants.querySelector('[data-emoji-pick]'));
 }
 life.on(search,'input',render);life.on(search,'keydown',event=>{if(['Enter','ArrowDown'].includes(event.key)){event.preventDefault();grid.querySelector('[data-emoji-pick]:not(:disabled)')?.focus()}});
 life.on(scroll,'scroll',()=>{if(!scrollFrame){scrollFrame=true;life.raf(()=>{scrollFrame=false;updateFooter()})}},{passive:true});
 life.on(root,'click',event=>{
  const jump=event.target.closest('[data-emoji-jump]');if(jump){jumpTo(jump.dataset.emojiJump);return}if(event.target.closest('[data-emoji-retry]')){load();return}
  const toggle=event.target.closest('[data-emoji-variants-trigger]');if(toggle){populateVariants(toggle);return}
  const chosen=event.target.closest('[data-emoji-pick]');if(!chosen||chosen.disabled||!writable()||!catalog?.items.has(chosen.dataset.emojiPick))return;
  event.preventDefault();const glyph=chosen.dataset.emojiPick;if(!allowed(glyph)||!enabled(glyph)||options.choose?.(glyph)===false)return;
  remember(glyph);
  closeVariants();options.afterChoose?.();
 });
 life.on(root,'keydown',event=>{
  const toggle=event.target.closest('[data-emoji-variants-trigger]');if(toggle&&['Enter',' ','ArrowDown'].includes(event.key)){populateVariants(toggle);return}
  const region=event.target.closest('[data-emoji-variants],.oie-emoji-grid');if(!region)return;
  const buttons=[...region.querySelectorAll('[data-emoji-pick]')].filter(button=>!button.disabled&&button.closest('[data-emoji-variants],.oie-emoji-grid')===region),index=buttons.indexOf(event.target),columns=region.matches('[data-emoji-variants]')?1:8,delta={ArrowLeft:-1,ArrowRight:1,ArrowUp:-columns,ArrowDown:columns}[event.key];
  if(index<0||delta===undefined)return;event.preventDefault();buttons[Math.max(0,Math.min(buttons.length-1,index+delta))]?.focus();
 });
 return {search,remember,focusSearch:trigger=>focusWhenOpen(trigger,search),open(resetSearch=false){readRecent();if(resetSearch)search.value='';void load()},close:closeVariants};
}};
window.OieRuntime.register('emoji-picker',life=>{
 const root=document.getElementById('oie-emoji-picker'),input=document.getElementById('message-composer'),form=document.getElementById('send-message-form');if(!root||!input||!form)return;
 const trigger=root.querySelector('[data-emoji-trigger]');let selection={start:input.selectionStart||0,end:input.selectionEnd||0};
 const writable=()=>input.isConnected&&!input.disabled&&!input.readOnly&&form.dataset.busy!=='true'&&form.dataset.composeAllowed==='true';
 function rememberSelection(){selection={start:input.selectionStart,end:input.selectionEnd}}
 for(const name of ['select','keyup','pointerup','input'])life.on(input,name,rememberSelection);life.on(trigger,'pointerdown',rememberSelection);
 const selector=window.OieEmojiPicker.mount(root,life,{input,available:writable,choose(glyph){const start=Math.min(selection.start,input.value.length),end=Math.min(selection.end,input.value.length);input.setRangeText(glyph,start,end,'end');input.dispatchEvent(new Event('input',{bubbles:true}));rememberSelection()},afterChoose(){if(trigger.getAttribute('aria-expanded')==='true')trigger.click();input.focus({preventScroll:true})}});
 function sync(){const disabled=!writable();if(disabled){selector.close();if(trigger.getAttribute('aria-expanded')==='true')trigger.click()}trigger.disabled=disabled}
 const observer=new MutationObserver(sync);observer.observe(input,{attributes:true,attributeFilter:['readonly','disabled']});observer.observe(form,{attributes:true,attributeFilter:['data-busy','data-compose-allowed']});life.cleanup(()=>observer.disconnect());sync();
 function focusSearch(attempt=0){life.raf(()=>{if(trigger.getAttribute('aria-expanded')!=='true'||document.activeElement===selector.search)return;const active=document.activeElement;if(active!==trigger&&active!==document.body&&active!==input)return;selector.search.focus({preventScroll:true});if(document.activeElement!==selector.search&&attempt<12)focusSearch(attempt+1)})}
 life.on(trigger,'click',()=>{if(!writable())return;life.raf(()=>{if(trigger.getAttribute('aria-expanded')==='true'){selector.open();focusSearch()}})});
 life.on(trigger,'keydown',event=>{if(['Enter',' ','ArrowDown'].includes(event.key)&&writable())life.raf(()=>{selector.open();focusSearch()})});
});
