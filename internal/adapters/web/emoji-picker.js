window.OieEmojiPicker=(()=>{
 'use strict';
 const memory=window.OieRuntime.memory,recentKey='oie.emoji-recent.v1';
 let unsavedRecent=false;
 const fold=value=>value.normalize('NFD').replace(/\p{M}/gu,'').toLowerCase().trim();
 function readRecent(){
  let recent=memory.get(recentKey);
  if(!unsavedRecent){
   try{recent=JSON.parse(localStorage.getItem(recentKey)||'[]')}catch{}
  }
  return Array.isArray(recent)?recent.filter(value=>typeof value==='string').slice(0,24):[];
 }
 function remember(glyph){
  const recent=[glyph,...readRecent().filter(value=>value!==glyph)].slice(0,24);
  memory.set(recentKey,recent);
  try{localStorage.setItem(recentKey,JSON.stringify(recent));unsavedRecent=false}catch{unsavedRecent=true}
  return recent;
 }
 function insert(input,glyph,start,end){
  input.setRangeText(glyph,start,end,'end');
  input.dispatchEvent(new Event('input',{bubbles:true}));
 }
 const catalogKey=url=>'emoji-catalog-v3:'+url;
 async function loadCatalog(url,locale,life){
  const key=catalogKey(url),cached=memory.get(key);
  if(cached)return cached;
  const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
  try{
   const response=await life.fetch(url,{credentials:'same-origin',redirect:'error',signal:controller.signal});
   if(!response.ok)throw Error('catalog_unavailable');
   const data=await response.json();
   if(life.signal.aborted)throw Error('catalog_aborted');
   const catalog=prepare(data,locale);
   memory.set(key,catalog);
   return catalog;
  }finally{clearTimeout(timeout)}
 }
 function prepare(data,locale){
  if(!Array.isArray(data.items)||data.items.length>10000||data.locale!==locale||!data.families||typeof data.families!=='object'||Array.isArray(data.families))throw Error('invalid_catalog');
  const shortcuts=new Map();
  for(const [glyph,terms] of Object.entries(data.shortcuts??{})){
   if(Array.isArray(terms)&&terms.length===2&&terms.every(term=>typeof term==='string'&&term))shortcuts.set(glyph,{en:terms[0],'pt-BR':terms[1]});
  }
  const items=new Map(),families=new Map();
  for(const item of data.items){
   if(!Array.isArray(item)||item.length!==4)throw Error('invalid_emoji');
   const [glyph,name,search,group]=item;
   if(typeof glyph!=='string'||!glyph||typeof name!=='string'||!name||typeof search!=='string'||!search||!Number.isInteger(group)||group<0||group>8||items.has(glyph))throw Error('invalid_emoji');
   const family=Object.hasOwn(data.families,glyph)?data.families[glyph]:glyph;
   if(typeof family!=='string')throw Error('invalid_family');
   items.set(glyph,{glyph,name,match:fold(glyph+' '+search),group,family});
  }
  for(const item of items.values()){
   const base=items.get(item.family);
   if(!base||base.family!==base.glyph||base.group!==item.group)throw Error('invalid_family');
   if(!families.has(base.glyph))families.set(base.glyph,{base,variants:[base]});
   if(item.glyph!==base.glyph)families.get(base.glyph).variants.push(item);
  }
  return{items,families,shortcuts};
 }
 // Use the painted bounds of bitmap emoji when available. Cache the
 // measurement by font so composer and reaction pickers can share it.
 const glyphLayouts=new Map(),sampleSize=48,samplesPerRow=4,batchSize=samplesPerRow**2;
 let ink,canReadInk=true;
 function getGlyphLayout(content,template){
  const probe=template.content.firstElementChild.cloneNode(true);
  probe.hidden=true;
  content.append(probe);
  const style=getComputedStyle(probe);
  const font=[style.fontWeight,style.fontSize,style.fontFamily].join(' '),fontSize=parseFloat(style.fontSize);
  probe.remove();
  if(!glyphLayouts.has(font))glyphLayouts.set(font,{font,fontSize,glyphs:new Map()});
  return glyphLayouts.get(font);
 }
 function getInk(){
  if(ink!==undefined)return ink;
  const canvas=document.createElement('canvas');
  canvas.width=canvas.height=sampleSize*samplesPerRow;
  try{ink=canvas.getContext('2d',{willReadFrequently:true})}catch{ink=null}
  return ink;
 }
 function measureGlyphBatch(batch,{font,fontSize,glyphs}){
  for(const glyph of batch)glyphs.set(glyph,null);
  if(!getInk())return;
  const midpoint=sampleSize/2,size=ink.canvas.width;
  const em=distance=>distance/fontSize+'em';
  ink.font=font;
  ink.textAlign='center';
  if(canReadInk){
   ink.setTransform(1,0,0,1,0,0);
   ink.clearRect(0,0,size,size);
  }
  const samples=batch.map((glyph,index)=>{
   const metrics=ink.measureText(glyph);
   const {actualBoundingBoxLeft:left,actualBoundingBoxRight:right,actualBoundingBoxAscent:ascent,actualBoundingBoxDescent:descent}=metrics;
   const baseline=(metrics.fontBoundingBoxAscent-metrics.fontBoundingBoxDescent)/2;
   // Text metrics still give split glyphs a usable width if readback is blocked.
   glyphs.set(glyph,{
    left:em(Number.isFinite(left+right)?(left-right)/2:0),
    top:em(Number.isFinite(ascent+descent+baseline)?(ascent-descent)/2-baseline:0),
    width:Math.max(metrics.width,left+right||0)
   });
   if(!canReadInk||!Number.isFinite(baseline))return null;
   const x=index%samplesPerRow*sampleSize,y=Math.floor(index/samplesPerRow)*sampleSize;
   const extent=Math.max(metrics.width/2,left||0,right||0,1);
   const scale=Math.min(1,(midpoint-2)/extent);
   // Some fallback fonts paint outside their metrics. Keep adjacent samples isolated.
   ink.save();
   ink.setTransform(1,0,0,1,0,0);
   ink.beginPath();
   ink.rect(x,y,sampleSize,sampleSize);
   ink.clip();
   ink.setTransform(scale,0,0,scale,x+midpoint,y+midpoint);
   ink.fillText(glyph,0,baseline);
   ink.restore();
   return{glyph,x,y,scale};
  });
  if(!canReadInk)return;
  let pixels;
  try{pixels=ink.getImageData(0,0,size,Math.ceil(samples.length/samplesPerRow)*sampleSize).data}
  catch{canReadInk=false;return}
  for(const sample of samples){
   if(!sample)continue;
   const {glyph,x,y,scale}=sample;
   let left=sampleSize,right=-1,top=sampleSize,bottom=-1;
   for(let row=0;row<sampleSize;row++)for(let column=0;column<sampleSize;column++){
    if(pixels[((y+row)*size+x+column)*4+3]<32)continue;
    left=Math.min(left,column);right=Math.max(right,column);
    top=Math.min(top,row);bottom=Math.max(bottom,row);
   }
   if(right<0||left===0||top===0||right===sampleSize-1||bottom===sampleSize-1)continue;
   const shift=(first,last)=>em((midpoint-(first+last+1)/2)/scale);
   glyphs.set(glyph,{left:shift(left,right),top:shift(top,bottom),width:(right-left+1)/scale});
  }
 }
 async function measureGlyphs(missing,layout,current){
  for(let start=0;start<missing.length;){
   // A fresh catalog is large. Give the browser a chance to paint and
   // accept input between groups, then publish an already aligned grid.
   await new Promise(resolve=>setTimeout(resolve,0));
   if(!current())return;
   const deadline=performance.now()+8;
   do{
    const batch=missing.slice(start,start+batchSize).filter(glyph=>!layout.glyphs.has(glyph));
    if(batch.length)measureGlyphBatch(batch,layout);
    start+=batchSize;
   }while(start<missing.length&&performance.now()<deadline);
  }
 }
 function mount(root,life,options){
 'use strict';
 options=options||{};
 const content=root.querySelector('[data-emoji-content]');if(!content)return null;
 const search=content.querySelector('[data-emoji-search]'),grid=content.querySelector('[data-emoji-grid]'),scroll=content.querySelector('[data-emoji-scroll]'),status=content.querySelector('[data-emoji-status]'),retry=content.querySelector('[data-emoji-retry]'),cell=content.querySelector('[data-emoji-button]'),choice=content.querySelector('[data-emoji-choice]'),sectionTemplate=content.querySelector('[data-emoji-section-template]'),jumps=[...content.querySelectorAll('[data-emoji-jump]')];
 const url=content.dataset.emojiUrl,locale=document.documentElement.lang;
 let catalog=memory.get(catalogKey(url)),loading=false,sections=new Map(),browseSections=new Map(),scrollFrame=false,variantGeometry=null,recent=[],layout,renderVersion=0;
 const opened=()=>root.isConnected&&!life.signal.aborted&&options.opened();
 const writable=()=>root.isConnected&&(options.available?.()??true),allowed=glyph=>options.allowed?.(glyph)??true,enabled=glyph=>writable()&&(options.enabled?.(glyph)??true);
 const mutate=fn=>window.Alpine?.mutateDom?window.Alpine.mutateDom(fn):fn();
 function clear(node){for(const child of node.children)window.Alpine?.destroyTree(child);node.replaceChildren()}
 function updateButtonState(button){
  const glyph=button.dataset.emojiPick,disabled=!enabled(glyph);
  if(button.disabled!==disabled)button.disabled=disabled;
  if(options.selected){
   const pressed=String(options.selected(glyph));
   if(button.getAttribute('aria-pressed')!==pressed)button.setAttribute('aria-pressed',pressed);
  }
 }
 function setButton(button,item){
  const glyph=document.createElement('span');
  glyph.className='oie-emoji-glyph';
  glyph.textContent=item.glyph;
  const bounds=layout.glyphs.get(item.glyph);
  if(bounds){
   glyph.style.setProperty('--oie-emoji-left',bounds.left);
   glyph.style.setProperty('--oie-emoji-top',bounds.top);
   if(bounds.width>layout.fontSize){
    glyph.dataset.emojiWide='';
    glyph.style.setProperty('--oie-emoji-width',bounds.width+'px');
    glyph.style.setProperty('--oie-emoji-fit',layout.fontSize/bounds.width);
   }
  }
  button.append(glyph);
  button.dataset.emojiPick=item.glyph;
  button.setAttribute('aria-label',item.name);
  button.title=item.name;
  updateButtonState(button);
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
  for(const [key,{element}]of sections)if(element.getBoundingClientRect().top<=top+16)current=key;
  for(const button of jumps){button.disabled=!sections.has(button.dataset.emojiJump);if(button.dataset.emojiJump===current)button.setAttribute('aria-current','location');else button.removeAttribute('aria-current')}
 }
 function jumpTo(key){const section=sections.get(key)?.element;if(!section)return;scroll.scrollTop+=section.getBoundingClientRect().top-scroll.getBoundingClientRect().top;section.querySelector('h3').focus({preventScroll:true});updateFooter()}
 async function render(){
  if(!catalog||!opened())return;
  layout??=getGlyphLayout(content,cell);
  const version=++renderVersion,current=()=>version===renderVersion&&opened();
  const terms=fold(search.value).split(/\s+/).filter(Boolean),exact=catalog.items.get(search.value.trim()),preferred=new Map();
  for(const glyph of recent){const item=catalog.items.get(glyph);if(item&&allowed(item.glyph)&&!preferred.has(item.family))preferred.set(item.family,item)}
  const available=[...catalog.families.values()].map(family=>({...family,variants:family.variants.filter(item=>allowed(item.glyph))})).filter(family=>family.variants.length);
  const candidates=available.filter(family=>family.variants.some(item=>terms.every(term=>item.match.includes(term))));
  const display=family=>({family,display:exact?.family===family.base.glyph&&allowed(exact.glyph)?exact:preferred.get(family.base.glyph)||family.variants.find(item=>item.glyph===family.base.glyph)||family.variants[0]});
  const groups=new Map();
  if(!terms.length)groups.set('recent',available.filter(family=>preferred.has(family.base.glyph)).sort((a,b)=>[...preferred.keys()].indexOf(a.base.glyph)-[...preferred.keys()].indexOf(b.base.glyph)).map(display));
  for(let group=0;group<9;group++){const items=candidates.filter(family=>family.base.group===group).map(display);if(items.length)groups.set(String(group),items)}
  const missing=[...new Set([...groups.values()].flatMap(items=>items.map(item=>item.display.glyph)))].filter(glyph=>!layout.glyphs.has(glyph));
  if(missing.length>batchSize){
   stopVariantGeometry();
   if(grid.contains(document.activeElement))search.focus({preventScroll:true});
   mutate(()=>clear(grid));
   sections=new Map();
   updateFooter();
   status.textContent=content.dataset.emojiLoading;
   await measureGlyphs(missing,layout,current);
   if(!current())return;
  }else if(missing.length)measureGlyphBatch(missing,layout);
  // Preserve keyboard selection when a cached open rebuilds the same grid.
  const active=grid.contains(document.activeElement)?document.activeElement:null,glyph=active?.dataset.emojiPick,section=active?.closest('[data-emoji-section]')?.dataset.emojiSection;
  const nextSections=new Map();
  for(const [key,items]of groups){
   const previous=(terms.length?sections:browseSections).get(key);
   const sameChoices=previous?.items.length===items.length&&previous.items.every((item,index)=>{
    const next=items[index],variants=item.family.variants;
    return item.display===next.display&&variants.length===next.family.variants.length&&
     variants.every((variant,i)=>variant===next.family.variants[i]);
   });
   if(sameChoices){
    for(const button of previous.element.querySelectorAll('[data-emoji-pick]'))updateButtonState(button);
    nextSections.set(key,previous);
    continue;
   }
   const element=sectionTemplate.content.firstElementChild.cloneNode(true);
   element.dataset.emojiSection=key;
   element.id=content.dataset.emojiPrefix+'-section-'+key;
   element.querySelector('h3').textContent=jumps.find(button=>button.dataset.emojiJump===key).getAttribute('aria-label');
   const cells=element.querySelector('.oie-emoji-grid');
   for(const item of items)cells.append(createChoice(item));
   if(!items.length){
    const empty=document.createElement('p');
    empty.textContent=content.dataset.emojiNoRecent;
    element.append(empty);
   }
   nextSections.set(key,{element,items});
  }
  if(variantGeometry)closeVariants();
  mutate(()=>{
   for(const [key,previous]of sections){
    if(nextSections.get(key)===previous)continue;
    window.Alpine?.destroyTree(previous.element);
    previous.element.remove();
   }
   let position=grid.firstElementChild;
   for(const {element}of nextSections.values()){
    const mounted=element.parentElement===grid;
    if(element!==position)grid.insertBefore(element,position);
    if(!mounted)window.Alpine?.initTree(element);
    position=element.nextElementSibling;
   }
  });
  sections=nextSections;
  if(!terms.length)browseSections=nextSections;
  scroll.scrollTop=0;status.textContent=candidates.length?'':content.dataset.emojiEmpty;retry.hidden=true;
  if(glyph&&!active.isConnected){
   const replacement=[...grid.querySelectorAll('[data-emoji-pick]')].find(button=>button.dataset.emojiPick===glyph&&button.closest('[data-emoji-section]')?.dataset.emojiSection===section);
   (replacement||search).focus();
  }
  updateFooter();
 }
 async function load(){
  if(catalog){await render();return}if(loading)return;loading=true;status.textContent=content.dataset.emojiLoading;retry.hidden=true;
  try{
   catalog=await loadCatalog(url,locale,life);
   await render();
  }catch{if(opened()){status.textContent=content.dataset.emojiFailed;retry.hidden=false}}
  finally{loading=false}
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
  if(!variants.childElementCount){
   const fragment=document.createDocumentFragment(),items=family.variants.filter(item=>allowed(item.glyph));
   const missing=items.map(item=>item.glyph).filter(glyph=>!layout.glyphs.has(glyph));
   for(let start=0;start<missing.length;start+=batchSize)measureGlyphBatch(missing.slice(start,start+batchSize),layout);
   for(const item of items){
    const button=cell.content.firstElementChild.cloneNode(true);
    setButton(button,item);
    button.dataset.emojiVariant='';
    button.setAttribute('aria-pressed',String(options.selected?options.selected(item.glyph):item.glyph===wrapper.querySelector(':scope>[data-emoji-pick]').dataset.emojiPick));
    fragment.append(button);
   }
   mutate(()=>{
    variants.append(fragment);
    for(const node of variants.children)window.Alpine?.initTree(node);
   });
  }
  if(toggle.getAttribute('aria-expanded')==='true')watchVariantGeometry(toggle);
  focusWhenOpen(toggle,variants.querySelector('[data-emoji-pick]'));
 }
 life.on(search,'input',render);life.on(search,'keydown',event=>{if(['Enter','ArrowDown'].includes(event.key)){event.preventDefault();grid.querySelector('[data-emoji-pick]:not(:disabled)')?.focus()}});
 life.on(scroll,'scroll',()=>{
  if(scrollFrame)return;
  scrollFrame=true;
  life.raf(()=>{
   scrollFrame=false;
   updateFooter();
   if(!variantGeometry)return;
   const {toggle,panel}=variantGeometry,bounds=scroll.getBoundingClientRect();
   const anchor=toggle.closest('[data-emoji-family]').getBoundingClientRect();
   if(anchor.bottom>bounds.top&&anchor.top<bounds.bottom)return;
   const restoreFocus=panel.contains(document.activeElement)||document.activeElement===toggle;
   closeVariants();
   if(restoreFocus)search.focus({preventScroll:true});
  });
 },{passive:true});
 life.on(root,'click',event=>{
  const jump=event.target.closest('[data-emoji-jump]');if(jump){jumpTo(jump.dataset.emojiJump);return}if(event.target.closest('[data-emoji-retry]')){load();return}
  const toggle=event.target.closest('[data-emoji-variants-trigger]');if(toggle){populateVariants(toggle);return}
  const chosen=event.target.closest('[data-emoji-pick]');if(!chosen||chosen.disabled||!writable()||!catalog?.items.has(chosen.dataset.emojiPick))return;
  event.preventDefault();const glyph=chosen.dataset.emojiPick;if(!allowed(glyph)||!enabled(glyph)||options.choose?.(glyph)===false)return;
  recent=remember(glyph);
  closeVariants();options.afterChoose?.();
 });
 life.on(root,'keydown',event=>{
  const toggle=event.target.closest('[data-emoji-variants-trigger]');if(toggle&&['Enter',' ','ArrowDown'].includes(event.key)){populateVariants(toggle);return}
  const region=event.target.closest('[data-emoji-variants],.oie-emoji-grid');if(!region)return;
  const buttons=[...region.querySelectorAll('[data-emoji-pick]')].filter(button=>!button.disabled&&button.closest('[data-emoji-variants],.oie-emoji-grid')===region),index=buttons.indexOf(event.target),columns=region.matches('[data-emoji-variants]')?1:8,delta={ArrowLeft:-1,ArrowRight:1,ArrowUp:-columns,ArrowDown:columns}[event.key];
  if(index<0||delta===undefined)return;event.preventDefault();buttons[Math.max(0,Math.min(buttons.length-1,index+delta))]?.focus();
 });
 return {search,focusSearch:trigger=>focusWhenOpen(trigger,search),open(resetSearch=false){recent=readRecent();if(resetSearch)search.value='';void load()},close:closeVariants,escape(){const toggle=root.querySelector('[data-emoji-variants-trigger][aria-expanded="true"]');if(!toggle)return false;closeVariants();toggle.focus({preventScroll:true});return true}};
 }
 return {mount,loadCatalog,fold,insert,remember};
})();
window.OieRuntime.register('emoji-picker',life=>{
 const root=document.getElementById('oie-emoji-picker'),input=document.getElementById('message-composer'),form=document.getElementById('send-message-form');if(!root||!input||!form)return;
 const trigger=root.querySelector('[data-emoji-trigger]');let selection={start:input.selectionStart||0,end:input.selectionEnd||0};
 const opened=()=>root.isConnected&&!life.signal.aborted&&options.opened();
 const writable=()=>input.isConnected&&!input.disabled&&!input.readOnly&&form.dataset.busy!=='true'&&form.dataset.composeAllowed==='true';
 function rememberSelection(){selection={start:input.selectionStart,end:input.selectionEnd}}
 for(const name of ['select','keyup','pointerup','input'])life.on(input,name,rememberSelection);life.on(trigger,'pointerdown',rememberSelection);
 const selector=window.OieEmojiPicker.mount(root,life,{input,opened:()=>trigger.getAttribute('aria-expanded')==='true',available:writable,choose(glyph){const start=Math.min(selection.start,input.value.length),end=Math.min(selection.end,input.value.length);window.OieEmojiPicker.insert(input,glyph,start,end);rememberSelection()},afterChoose(){if(trigger.getAttribute('aria-expanded')==='true')trigger.click();input.focus({preventScroll:true})}});
 function sync(){const disabled=!writable();if(disabled){selector.close();if(trigger.getAttribute('aria-expanded')==='true')trigger.click()}trigger.disabled=disabled}
 const observer=new MutationObserver(sync);observer.observe(input,{attributes:true,attributeFilter:['readonly','disabled']});observer.observe(form,{attributes:true,attributeFilter:['data-busy','data-compose-allowed']});life.cleanup(()=>observer.disconnect());sync();
 let expanded=false;
 const pickerObserver=new MutationObserver(()=>{
  const open=trigger.getAttribute('aria-expanded')==='true';
  if(open===expanded)return;
  expanded=open;
  if(!open){selector.close();return}
  if(writable()){
   selector.open();
   selector.focusSearch(trigger);
  }
 });
 pickerObserver.observe(trigger,{attributes:true,attributeFilter:['aria-expanded']});
 life.cleanup(()=>pickerObserver.disconnect());
});
