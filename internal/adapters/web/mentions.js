window.OieRuntime.register('mentions',life=>{
 'use strict';
 const form=document.getElementById('send-message-form'),input=document.getElementById('message-composer'),root=document.getElementById('oie-mention-picker');
 if(!form||!input||!root)return;
 const field=form.elements.namedItem('mentions'),list=root.querySelector('#oie-mention-options'),status=root.querySelector('[data-mention-status]'),retry=root.querySelector('[data-mention-retry]'),partial=root.querySelector('[data-mention-partial]'),template=form.querySelector('[data-mention-option-template]');
 if(!field||!list||!template)return;
 const scope={engineId:root.dataset.engineId,accountId:root.dataset.accountId,conversationId:root.dataset.conversationId};
 const fold=value=>value.normalize('NFD').replace(/\p{M}/gu,'').toLocaleLowerCase(document.documentElement.lang),points=value=>Array.from(value),writable=()=>input.isConnected&&!input.disabled&&!input.readOnly&&form.dataset.busy!=='true'&&form.dataset.composeAllowed==='true';
 let previous=input.value,tokens=[],catalog=null,loadedAt=0,loading=false,failed=false,epoch=0,controller=null,visible=[],active=-1,composing=false,dismissed='',geometryFrame=false;
 function sync(){field.value=JSON.stringify(tokens.map(({participantId,begin,end})=>({participantId,begin,end})));}
 function restore(){
  let value=[];try{value=JSON.parse(field.value||'[]')}catch{}
  const text=points(input.value);tokens=[];
  if(Array.isArray(value)&&value.length<=32){
   for(const item of value){
    if(!item||typeof item.participantId!=='string'||!item.participantId||item.participantId.length>1024||!Number.isInteger(item.begin)||!Number.isInteger(item.end)||item.begin<0||item.end<=item.begin||item.end>text.length||tokens.some(token=>item.begin<token.end&&item.end>token.begin))continue;
    const word=text.slice(item.begin,item.end).join('');if(!word.startsWith('@')||/[\r\n\u0000]/u.test(word))continue;
    tokens.push({participantId:item.participantId,begin:item.begin,end:item.end,text:word});
   }
  }
  tokens.sort((a,b)=>a.begin-b.begin);previous=input.value;sync();
 }
 function track(){
  const before=points(previous),after=points(input.value);let start=0,suffix=0;
  while(start<before.length&&start<after.length&&before[start]===after[start])start++;
  while(suffix<before.length-start&&suffix<after.length-start&&before[before.length-1-suffix]===after[after.length-1-suffix])suffix++;
  const end=before.length-suffix,shift=after.length-before.length;
  tokens=tokens.flatMap(token=>{
   if(token.end<=start)return[token];
   if(token.begin>=end)return[{...token,begin:token.begin+shift,end:token.end+shift}];
   return[];
  }).filter(token=>after.slice(token.begin,token.end).join('')===token.text);
  previous=input.value;sync();
 }
 function trigger(){
  if(!writable()||composing||tokens.length>=32||input.selectionStart!==input.selectionEnd||(document.activeElement!==input&&!root.contains(document.activeElement)))return null;
  const caret=input.selectionStart,prefix=input.value.slice(0,caret),start=prefix.lastIndexOf('@');
  if(start<0||(start>0&&!/[\s([{]/u.test(prefix[start-1])))return null;
  const query=prefix.slice(start+1),begin=points(prefix.slice(0,start)).length;
  if(points(query).length>80||/[\r\n@\u0000]/u.test(query)||query.split(/\s+/u).length>4||tokens.some(token=>token.begin===begin))return null;
  if(dismissed===start+':'+prefix)return null;
  const suffix=input.value.slice(caret).match(/^[\p{L}\p{M}\p{N}_'-]*/u)[0];
  return{start,caret,end:caret+suffix.length,begin,query};
 }
 function clearOptions(){
  for(const child of list.children)window.Alpine?.destroyTree(child);
  list.replaceChildren();visible=[];active=-1;input.removeAttribute('aria-activedescendant');
 }
 function hide(){root.hidden=true;input.removeAttribute('aria-activedescendant');active=-1;}
 function dismiss(){const current=trigger();if(current)dismissed=current.start+':'+input.value.slice(0,current.caret);hide();}
 function position(){
  geometryFrame=false;if(root.hidden)return;
  const viewport=window.visualViewport,top=form.querySelector('.oie-composer').getBoundingClientRect().top;
  root.style.setProperty('--oie-mention-max-height',Math.max(56,top-(viewport?.offsetTop||0)-12)+'px');
 }
 function schedulePosition(){if(!geometryFrame){geometryFrame=true;life.raf(position)}}
 function setActive(index,scroll=false){
  active=visible.length?(index+visible.length)%visible.length:-1;
  for(const [n,node]of [...list.children].entries())node.setAttribute('aria-selected',String(n===active));
  const selected=list.children[active];
  if(selected){input.setAttribute('aria-activedescendant',selected.id);if(scroll)selected.scrollIntoView({block:'nearest'});}else input.removeAttribute('aria-activedescendant');
 }
 function show(){root.hidden=false;schedulePosition();}
 function render(){
  const current=trigger();if(!current){hide();return;}
  const terms=fold(current.query).trim().split(/\s+/u).filter(Boolean),oldId=visible[active]?.id;
  clearOptions();show();retry.hidden=!failed;partial.hidden=true;
  if(loading){status.textContent=root.dataset.mentionLoading;return;}
  if(failed){status.textContent=root.dataset.mentionFailed;return;}
  if(!catalog){status.textContent='';return;}
  const matches=catalog.items.filter(item=>terms.every(term=>item.match.includes(term)));
  visible=matches.slice(0,50);status.textContent=visible.length?'':root.dataset.mentionEmpty;
  partial.hidden=!(catalog.truncated||catalog.state==='partial'||matches.length>50);
  const fragment=document.createDocumentFragment();
  for(const [index,item]of visible.entries()){
   const node=template.content.firstElementChild.cloneNode(true);node.id='oie-mention-option-'+index;node.dataset.mentionOption=String(index);
   node.querySelector('[data-mention-name]').textContent=item.displayName;
   const nick=node.querySelector('[data-mention-nick]');nick.textContent=item.insertText;nick.hidden=item.displayName===item.insertText||'@'+item.displayName===item.insertText;
   node.querySelector('[data-mention-initials]').textContent=points(item.displayName.trim()).filter(letter=>!/^\s$/u.test(letter)).slice(0,2).join('').toLocaleUpperCase(document.documentElement.lang);
   fragment.append(node);
  }
  const mutate=()=>{list.append(fragment);window.Alpine?.initTree(list)};
  if(window.Alpine?.mutateDom)window.Alpine.mutateDom(mutate);else mutate();
  setActive(Math.max(0,visible.findIndex(item=>item.id===oldId)));
 }
 function validate(data){
  if(!data||Object.entries(scope).some(([key,value])=>data[key]!==value)||!['known','partial','unavailable','not_connected'].includes(data.state)||!Array.isArray(data.items)||data.items.length>200||typeof data.truncated!=='boolean')throw Error('invalid_candidates');
  const ids=new Set(),items=[];
  for(const item of data.items){
   if(!item||typeof item.id!=='string'||!item.id||item.id.length>1024||ids.has(item.id)||typeof item.displayName!=='string'||!item.displayName||points(item.displayName).length>300||typeof item.insertText!=='string'||!item.insertText.startsWith('@')||points(item.insertText).length>301||/[\r\n\u0000]/u.test(item.insertText)||!['text','structured'].includes(item.referenceMode))throw Error('invalid_candidate');
   ids.add(item.id);items.push({...item,match:fold(item.displayName+' '+item.insertText)});
  }
  if(['unavailable','not_connected'].includes(data.state))throw Error('unavailable_candidates');
  return{state:data.state,truncated:data.truncated,items};
 }
 async function load(force=false){
  if(loading||!trigger())return;
  if(!force&&catalog&&Date.now()-loadedAt<30000){render();return;}
  loading=true;failed=false;render();const ticket=++epoch;controller?.abort();controller=new AbortController();const timeout=life.timeout(()=>controller?.abort(),10000);
  try{
   const response=await life.fetch(root.dataset.mentionUrl,{credentials:'same-origin',redirect:'error',signal:controller.signal});
   if(!response.ok)throw Error('candidate_request');const data=validate(await response.json());
   if(ticket!==epoch||life.signal.aborted||!root.isConnected)return;
   catalog=data;loadedAt=Date.now();
  }catch{if(ticket===epoch&&!life.signal.aborted)failed=true;}
  finally{clearTimeout(timeout);if(ticket===epoch){loading=false;if(!life.signal.aborted)render();}}
 }
 function update(){
  const current=trigger();if(!current){hide();return;}
  if(!loading&&!failed&&(!catalog||(root.hidden&&Date.now()-loadedAt>=30000))){void load();return;}
  render();
 }
 function choose(index){
  const item=visible[index],current=trigger();if(!item||!current||!writable()||tokens.length>=32)return;
  const following=input.value.slice(current.end),separator=!following||!/^[\s.,;:!?)\]}]/u.test(following)?' ':'';
  const replacement=item.insertText+separator,old=input.value;input.setRangeText(replacement,current.start,current.end,'end');track();
  const mention={participantId:item.id,begin:current.begin,end:current.begin+points(item.insertText).length,text:item.insertText};
  tokens=tokens.filter(token=>mention.begin>=token.end||mention.end<=token.begin);tokens.push(mention);tokens.sort((a,b)=>a.begin-b.begin);sync();previous=input.value;dismissed='';hide();
  input.focus({preventScroll:true});input.dispatchEvent(new Event('input',{bubbles:true}));
  if(old===input.value)hide();
 }
 restore();input.setAttribute('aria-autocomplete','list');input.setAttribute('aria-controls',list.id);input.setAttribute('aria-haspopup','listbox');
 life.on(input,'input',event=>{dismissed='';track();composing=event.isComposing===true;update();},{capture:true});
 life.on(input,'compositionstart',()=>{composing=true;hide()});life.on(input,'compositionend',()=>{composing=false;update()});
 life.on(input,'keydown',event=>{
  if(event.isComposing||event.keyCode===229||composing||root.hidden)return;
  if(['ArrowDown','ArrowUp','Enter','Escape'].includes(event.key)){
   event.preventDefault();event.stopImmediatePropagation();
   if(event.key==='ArrowDown'||event.key==='ArrowUp')setActive(active+(event.key==='ArrowDown'?1:-1),true);
   else if(event.key==='Enter'){if(active>=0)choose(active);else dismiss();}
   else dismiss();
  }else if(event.key==='Tab')hide();
 },{capture:true});
 life.on(input,'focus',update);
 for(const name of ['keyup','click','select'])life.on(input,name,event=>{if(event.key&&['Escape','Tab','Enter','ArrowDown','ArrowUp'].includes(event.key))return;if(name==='click')dismissed='';update()});
 life.on(list,'pointerdown',event=>{if(event.pointerType!=='touch')event.preventDefault()});
 life.on(list,'click',event=>{const option=event.target.closest('[data-mention-option]');if(option){event.preventDefault();choose(Number(option.dataset.mentionOption));}});
 life.on(root,'keydown',event=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();dismiss();input.focus({preventScroll:true});}});
 life.on(root,'focusout',event=>{if(event.relatedTarget!==input&&!root.contains(event.relatedTarget))hide()});
 life.on(retry,'click',()=>{input.focus({preventScroll:true});void load(true)});
 life.on(document,'pointerdown',event=>{if(event.target!==input&&!root.contains(event.target))dismiss()},{capture:true});
 life.on(form,'oie:mentions-restore',()=>{restore();hide()});
 life.on(input,'blur',event=>{if(!root.contains(event.relatedTarget))hide()});
 life.on(window,'resize',schedulePosition);life.on(window,'scroll',schedulePosition,{capture:true,passive:true});
 if(window.visualViewport){life.on(window.visualViewport,'resize',schedulePosition);life.on(window.visualViewport,'scroll',schedulePosition)}
 const observer=new MutationObserver(()=>{if(!writable())hide()});observer.observe(input,{attributes:true,attributeFilter:['disabled','readonly']});observer.observe(form,{attributes:true,attributeFilter:['data-busy','data-compose-allowed']});
 const resize=new ResizeObserver(schedulePosition);resize.observe(form.querySelector('.oie-composer'));
 life.cleanup(()=>{epoch++;controller?.abort();observer.disconnect();resize.disconnect();hide();for(const attr of ['aria-autocomplete','aria-controls','aria-haspopup','aria-expanded','aria-activedescendant'])input.removeAttribute(attr);});
});
