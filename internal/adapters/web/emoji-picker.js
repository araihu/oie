window.OieRuntime.register('emoji-picker', life => {
 'use strict';
 const root=document.getElementById('oie-emoji-picker'),input=document.getElementById('message-composer'),form=document.getElementById('send-message-form');
 if(!root||!input||!form)return;
 const content=root.querySelector('[data-emoji-content]'),trigger=root.querySelector('[data-emoji-trigger]'),search=root.querySelector('#emoji-search'),grid=root.querySelector('#emoji-grid'),scroll=root.querySelector('[data-emoji-scroll]'),status=root.querySelector('[data-emoji-status]'),more=root.querySelector('[data-emoji-more]'),retry=root.querySelector('[data-emoji-retry]'),cell=root.querySelector('[data-emoji-button]');
 const memory=window.OieRuntime.memory,cacheKey='emoji-catalog:'+content.dataset.emojiUrl,recentKey='oie.emoji-recent.v1',fold=value=>value.normalize('NFD').replace(/\p{M}/gu,'').toLocaleLowerCase(document.documentElement.lang).trim();
 let catalog=memory.get(cacheKey),loading=false,filtered=[],shown=0,category='all',selection={start:input.selectionStart||0,end:input.selectionEnd||0},recent=[];
 try{recent=JSON.parse(localStorage.getItem(recentKey)||'[]')}catch{recent=memory.get(recentKey)||[]}
 if(!Array.isArray(recent))recent=[];recent=recent.filter(value=>typeof value==='string').slice(0,24);
 const writable=()=>input.isConnected&&!input.disabled&&!input.readOnly&&form.dataset.busy!=='true'&&form.dataset.composeAllowed==='true';
 function rememberSelection(){selection={start:input.selectionStart,end:input.selectionEnd}}
 for(const name of ['select','keyup','pointerup','input'])life.on(input,name,rememberSelection);
 life.on(trigger,'pointerdown',rememberSelection);
 function append(){
  const batch=filtered.slice(shown,shown+144),fragment=document.createDocumentFragment();
  for(const item of batch){const button=cell.content.firstElementChild.cloneNode(true);button.textContent=item.glyph;button.dataset.emojiPick=item.glyph;button.setAttribute('aria-label',item.name);button.title=item.name;fragment.append(button)}
  shown+=batch.length;grid.append(fragment);more.hidden=shown>=filtered.length;
 }
 function render(){
  if(!catalog)return;
  const terms=fold(search.value).split(/\s+/).filter(Boolean),byGlyph=new Map(catalog.map(item=>[item.glyph,item]));
  const candidates=category==='recent'?recent.map(glyph=>byGlyph.get(glyph)).filter(Boolean):catalog;
  filtered=candidates.filter(item=>(category==='all'||category==='recent'||String(item.group)===category)&&terms.every(term=>item.match.includes(term)));
  shown=0;grid.replaceChildren();scroll.scrollTop=0;status.textContent=filtered.length?'':content.dataset.emojiEmpty;retry.hidden=true;append();
 }
 async function load(){
  if(catalog){render();return}if(loading)return;loading=true;status.textContent=content.dataset.emojiLoading;retry.hidden=true;
  const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
  try{
   const response=await life.fetch(content.dataset.emojiUrl,{credentials:'same-origin',redirect:'error',signal:controller.signal});if(!response.ok)throw Error('catalog_unavailable');
   const data=await response.json();if(life.signal.aborted||!root.isConnected)return;
   if(!Array.isArray(data.items)||data.items.length>10000||data.locale!==document.documentElement.lang)throw Error('invalid_catalog');
   catalog=data.items.map(item=>{if(!Array.isArray(item)||item.length!==4||item.slice(0,3).some(value=>typeof value!=='string')||!Number.isInteger(item[3])||item[3]<0||item[3]>8)throw Error('invalid_emoji');return{glyph:item[0],name:item[1],match:fold(item[0]+' '+item[1]+' '+item[2]),group:item[3]}});
   memory.set(cacheKey,catalog);render();
  }catch{if(!life.signal.aborted){status.textContent=content.dataset.emojiFailed;retry.hidden=false}}
  finally{clearTimeout(timeout);loading=false}
 }
 function sync(){const disabled=!writable();if(disabled&&trigger.getAttribute('aria-expanded')==='true')trigger.click();trigger.disabled=disabled}
 const observer=new MutationObserver(sync);observer.observe(input,{attributes:true,attributeFilter:['readonly','disabled']});observer.observe(form,{attributes:true,attributeFilter:['data-busy','data-compose-allowed']});life.cleanup(()=>observer.disconnect());sync();
 function focusSearch(attempt=0){life.raf(()=>{if(trigger.getAttribute('aria-expanded')!=='true')return;if(search.getClientRects().length){search.focus({preventScroll:true});if(document.activeElement===search)return}if(attempt<12)focusSearch(attempt+1)})}
 life.on(trigger,'click',()=>{if(!writable())return;life.raf(()=>{if(trigger.getAttribute('aria-expanded')==='true'){load();focusSearch()}})});
 life.on(trigger,'keydown',event=>{if(['Enter',' ','ArrowDown'].includes(event.key)&&writable())life.raf(()=>{load();focusSearch()})});
 life.on(search,'input',render);life.on(search,'keydown',event=>{if(event.key==='Enter'){event.preventDefault();grid.querySelector('[data-emoji-pick]')?.focus()}if(event.key==='ArrowDown'){event.preventDefault();grid.querySelector('[data-emoji-pick]')?.focus()}});
 life.on(scroll,'scroll',()=>{if(scroll.scrollTop+scroll.clientHeight>=scroll.scrollHeight-90&&shown<filtered.length)append()},{passive:true});
 life.on(root,'click',event=>{
  const tab=event.target.closest('[data-emoji-category]');if(tab){category=tab.dataset.emojiCategory;for(const node of root.querySelectorAll('[data-emoji-category]'))node.setAttribute('aria-pressed',String(node===tab));render();return}
  if(event.target.closest('[data-emoji-more]')){append();return}if(event.target.closest('[data-emoji-retry]')){load();return}
  const chosen=event.target.closest('[data-emoji-pick]');if(!chosen||!writable()||!catalog?.some(item=>item.glyph===chosen.dataset.emojiPick))return;
  event.preventDefault();const glyph=chosen.dataset.emojiPick,start=Math.min(selection.start,input.value.length),end=Math.min(selection.end,input.value.length);
  input.setRangeText(glyph,start,end,'end');input.dispatchEvent(new Event('input',{bubbles:true}));rememberSelection();
  recent=[glyph,...recent.filter(value=>value!==glyph)].slice(0,24);memory.set(recentKey,recent);try{localStorage.setItem(recentKey,JSON.stringify(recent))}catch{}
  trigger.click();input.focus({preventScroll:true});
 });
 life.on(grid,'keydown',event=>{const buttons=[...grid.querySelectorAll('[data-emoji-pick]')],index=buttons.indexOf(event.target),delta={ArrowLeft:-1,ArrowRight:1,ArrowUp:-8,ArrowDown:8}[event.key];if(index<0||delta===undefined)return;event.preventDefault();buttons[Math.max(0,Math.min(buttons.length-1,index+delta))]?.focus()});
});
