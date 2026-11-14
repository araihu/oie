window.OieRuntime.register('conversation-search',life=>{
 'use strict';
 const root=document.querySelector('[data-conversation-search]');if(!root)return;
 const form=root.querySelector('form'),query=root.querySelector('#conversation-search-query'),history=root.querySelector('#conversation-search-history'),older=root.querySelector('[data-conversation-search-older]'),newer=root.querySelector('[data-conversation-search-newer]'),feedback=root.querySelector('[data-conversation-search-status]'),excerpt=root.querySelector('[data-conversation-search-excerpt]'),retry=root.querySelector('[data-conversation-search-retry]'),coverage=root.querySelector('[data-conversation-search-coverage]');
 const engine=root.dataset.searchEngine,account=root.dataset.searchAccount,conversation=root.dataset.searchConversation;
 const uuid=value=>/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value||'');
 let opened=root.dataset.searchOpen==='true',epoch=0,selectionEpoch=0,timer=0,controller=null,busy=false,composing=false,hits=[],index=-1,next='',complete=false,lastCursor='',failure='',selected='',returnFocus=null;
 let lastSearchFocus=true;
 let archiveTimer=0, archiveController, contextController, archiveCursor='', archivePending=false, archiveFailed=false, localCoverage='';
 const state=()=>({q:query.value.trim(),includeEditHistory:String(history.checked),engineId:engine,accountId:account,conversationId:conversation});
 const valid=()=>!life.signal.aborted&&root.isConnected&&document.body.dataset.engineId===engine&&document.body.dataset.accountId===account&&document.body.dataset.conversationId===conversation;
 const same=(request)=>{const current=state();return Object.keys(current).every(key=>current[key]===request[key])};
 function status(value){feedback.textContent=value||'';retry.hidden=!failure;}
 function controls(){older.disabled=busy||!opened||!hits.length||index+1>=hits.length&&!next&&!archiveCursor;newer.disabled=busy||!opened||index<=0;root.dataset.searchSelected=selected;root.dataset.searchIndex=String(index);root.dataset.searchLoaded=String(hits.length);root.dataset.searchComplete=String(complete);root.setAttribute('aria-busy',String(busy));if(!busy&&returnFocus){const previous=returnFocus;returnFocus=null;if(document.activeElement===document.body||document.activeElement===previous){const target=!previous.disabled&&!previous.hidden?previous:!newer.disabled?newer:query;target.focus({preventScroll:true})}}}
 const highlightName='oie-room-search';
 function clearHighlights(){
  CSS.highlights?.delete(highlightName);
  for(const row of document.querySelectorAll('#message-timeline .oie-conversation-search-selected'))row.classList.remove('oie-conversation-search-selected');
 }
 function visibleText(presentation){
  const walker=document.createTreeWalker(presentation,NodeFilter.SHOW_TEXT),nodes=[];
  let text='',node;
  while((node=walker.nextNode())){
   const parent=node.parentElement;
   if(parent.closest('script,style,button,textarea,input,blockquote,[hidden],[aria-hidden="true"]'))continue;
   if(!parent.getClientRects().length||getComputedStyle(parent).visibility!=='visible')continue;
   nodes.push({node,start:text.length});
   text+=node.data;
  }
  return {text,nodes};
 }
 function projectedRanges(source,visible,ranges){
  if(!ranges.length)return [];
  if(source===visible)return ranges;
  // Accept simple inline Markdown only when dropping its delimiters
  // reproduces the entire visible text, up to surrounding whitespace.
  let reduced='',last=0;
  const removed=[];
  const markup=/(\*\*|__|~~|\*|_|`)([^*_`~\n]+?)\1/gu;
  for(const token of source.matchAll(markup)){
   const start=token.index,end=start+token[0].length,width=token[1].length;
   reduced+=source.slice(last,start)+token[2];
   removed.push([start,start+width],[end-width,end]);
   last=end;
  }
  reduced+=source.slice(last);
  if(reduced.trim()!==visible.trim())return [];
  const leading=visible.length-visible.trimStart().length-(reduced.length-reduced.trimStart().length);
  const projected=[];
  let i=0,shift=leading;
  for(const [start,end] of ranges){
   while(i<removed.length&&removed[i][1]<=start){
    shift-=removed[i][1]-removed[i][0];
    i++;
   }
   if(i===removed.length||removed[i][0]>=end)projected.push([start+shift,end+shift]);
  }
  return projected;
 }
 function authoredRanges(source,display,ranges){
  const boundary=offset=>offset>=0&&offset<=source.length&&!(source.charCodeAt(offset)>=0xdc00&&source.charCodeAt(offset)<=0xdfff);
  if(ranges.some(([start,end])=>!boundary(start)||!boundary(end)))return [];
  if(source===display)return ranges;
  // Reply projection may remove a leading fallback quote. Accept only
  // an identifiable authored suffix and rebase ranges wholly within it.
  if(display&&source.endsWith(display)){
   const start=source.length-display.length;
   if(source.slice(0,start).trimStart().startsWith('>')&&source[start-1]==='\n'){
    return ranges.filter(([from])=>from>=start).map(([from,to])=>[from-start,to-start]);
   }
  }
  return [];
 }
 function literalRanges(nodes){
  const terms=[...new Set(query.value.trim().split(/\s+/u).filter(Boolean))].sort((a,b)=>b.length-a.length);
  if(!terms.length)return [];
  const pattern=new RegExp(terms.map(term=>term.replace(/[.*+?^${}()|[\]\\]/g,'\\$&')).join('|'),'giu');
  const ranges=[];
  for(const {node,start} of nodes){
   for(const match of node.data.matchAll(pattern)){
    ranges.push([start+match.index,start+match.index+match[0].length]);
    if(ranges.length===256)return ranges;
   }
  }
  return ranges;
 }
 function sourceRanges(row,hit){
  if(!hit.remote&&(!hit.ranges||row.dataset.messageRevision!==hit.contentRevision))return [];
  if(hit.remote&&row.dataset.messageRevision!==hit.revision)return [];
  const presentation=row.querySelector('[data-message-presentation]');
  if(!presentation||!hit.remote&&presentation.dataset.searchSourceDigest!==hit.sourceDigest)return [];
  let source,display;
  try{
   source=JSON.parse(row.dataset.messageSource);
   display=JSON.parse(presentation.dataset.presentationVersion);
  }catch{return []}
  if(typeof source!=='string'||typeof display!=='string')return [];
  const authored=hit.remote?[]:authoredRanges(source,display,hit.ranges);
  const result=[];
  for(const body of presentation.querySelectorAll('[data-rendered-message],[data-original-message]')){
   if(!body.getClientRects().length||body.textContent.length>65536)continue;
   const {text,nodes}=visibleText(body);
   const ranges=hit.remote?literalRanges(nodes):projectedRanges(display,text,authored);
   let i=0;
   for(const [start,end] of ranges){
    if(start<0||end>text.length||start>=end)continue;
    while(i<nodes.length&&nodes[i].start+nodes[i].node.length<=start)i++;
    for(let j=i;j<nodes.length&&nodes[j].start<end;j++){
     const {node,start:offset}=nodes[j],range=document.createRange();
     range.setStart(node,Math.max(0,start-offset));
     range.setEnd(node,Math.min(node.length,end-offset));
     result.push(range);
     if(result.length===256)return result;
    }
   }
  }
  return result;
 }
 function highlight(){
  clearHighlights();
  if(!opened||!selected)return;
  const row=document.querySelector('#message-timeline [data-message-id="'+CSS.escape(selected)+'"]');
  if(!row)return;
  row.classList.add('oie-conversation-search-selected');
  const hit=hits[index];
  if(!hit||hit.historical||hit.kind!=='text'||!CSS.highlights||typeof Highlight!=='function')return;
  const ranges=sourceRanges(row,hit);
  if(ranges.length)CSS.highlights.set(highlightName,new Highlight(...ranges));
 }
 function parseRanges(row){
  const {searchRanges,searchContentRevision,searchSourceDigest}=row.dataset;
  if(!searchRanges||searchRanges.length>4096||!uuid(searchContentRevision)||!/^[a-f0-9]{64}$/.test(searchSourceDigest||''))return {};
  try{
   const ranges=JSON.parse(searchRanges);
   let previous=0;
   if(!Array.isArray(ranges)||ranges.length>256)return {};
   for(const range of ranges){
    if(!Array.isArray(range)||range.length!==2)return {};
    const [start,end]=range;
    if(!Number.isInteger(start)||!Number.isInteger(end)||start<previous||end<=start||end>65536)return {};
    previous=end;
   }
   return {ranges,contentRevision:searchContentRevision,sourceDigest:searchSourceDigest};
  }catch{return {}}
 }
 function showExcerpt(hit){
  excerpt.hidden=!(hit.historical||hit.kind==='metadata')||!hit.excerpt?.textContent;
  excerpt.replaceChildren();
  if(excerpt.hidden)return;
  if(hit.historical)excerpt.append(root.dataset.searchHistoricalLabel+': ');
  for(const part of hit.excerpt.childNodes){
   if(part.nodeType===Node.TEXT_NODE)excerpt.append(part.textContent);
   else if(part.nodeName==='MARK'){
    const mark=document.createElement('mark');mark.className='oie-search-highlight';mark.textContent=part.textContent;excerpt.append(mark);
   }
  }
 }
 function address(clearRevision=false){const url=new URL(location.href);if(opened){url.searchParams.set('chatSearch','1');if(query.value)url.searchParams.set('chatQuery',query.value);else url.searchParams.delete('chatQuery');if(history.checked)url.searchParams.set('chatIncludeHistory','true');else url.searchParams.delete('chatIncludeHistory')}else for(const key of ['chatSearch','chatQuery','chatIncludeHistory'])url.searchParams.delete(key);if(clearRevision||!opened)url.searchParams.delete('chatSearchRevision');window.OieNavigation?.addressSearch(url);}
 function abort(){epoch++;selectionEpoch++;clearTimeout(timer);clearTimeout(archiveTimer);controller?.abort();archiveController?.abort();contextController?.abort();controller=null;busy=false;window.OieTimeline?.cancelFocus?.();}
 function reset(){hits=[];index=-1;selected='';next='';archiveCursor='';archivePending=false;archiveFailed=false;localCoverage='';complete=false;failure='';lastCursor='';excerpt.hidden=true;excerpt.textContent='';coverage.hidden=true;coverage.textContent='';clearHighlights();status('');controls();}
 function parse(html,request,cursor){
  const document=new DOMParser().parseFromString(html,'text/html'),envelope=document.querySelector('[data-message-search-response]');
  if(!envelope||envelope.dataset.searchEngine!==engine||envelope.dataset.searchAccount!==account||envelope.dataset.searchConversation!==conversation||envelope.dataset.searchQuery!==request.q||envelope.dataset.searchHistory!==request.includeEditHistory||envelope.dataset.searchCursor!==cursor||envelope.dataset.searchState!=='ready')throw Error('scope');
  const nextCursor=envelope.dataset.searchNext||'';if(nextCursor.length>2048||nextCursor&&nextCursor===cursor)throw Error('cursor');
  const rows=[...envelope.querySelectorAll('[data-message-search-hit]')];if(rows.length>50||!rows.length&&nextCursor)throw Error('bounds');
  const known=new Set(),items=[];
  for(const row of rows){const id=row.dataset.searchMessage,revision=row.dataset.searchRevision,historical=row.dataset.searchHistorical,kind=row.dataset.searchMatchKind,link=row.querySelector('[data-message-search-source]'),url=link&&new URL(link.getAttribute('href'),location.href);if(!uuid(id)||!uuid(revision)||!['true','false'].includes(historical)||historical==='true'&&!history.checked||!['text','metadata'].includes(kind)||!url||url.origin!==location.origin||url.pathname!=='/'||url.searchParams.get('chat')!==conversation||url.searchParams.get('account')!==account||url.searchParams.get('message')!==id)throw Error('hit');
   const key=historical==='true'?id+':'+revision:id;if(known.has(key))throw Error('duplicate');known.add(key);const summary=row.querySelector('[data-search-result-excerpt]')?.content;if(Array.from(summary?.textContent||'').length>512)throw Error('excerpt');items.push({key,id,revision,historical:historical==='true',kind,excerpt:summary,...parseRanges(row)})}
  return {items,next:nextCursor,coverage:envelope.querySelector('[data-message-search-indexing]')?.textContent?.slice(0,512)||''};
 }
 async function select(position){
  if(!valid()||!opened||position<0||position>=hits.length)return;const generation=epoch,selection=++selectionEpoch,hit=hits[position];index=position;selected='';failure='';busy=true;clearHighlights();excerpt.hidden=true;status(root.dataset.searchLoading);controls();
  try{
   if(hit.remote){
    contextController?.abort();
    const context=new AbortController();
    contextController=context;
    await window.OieRoomArchive.request(life,{action:'context',messageId:hit.id},context.signal).catch(()=>{});
    if(!valid()||!opened||generation!==epoch||selection!==selectionEpoch)return;
   }
   const found=await window.OieTimeline?.focus?.(hit.id,{focus:false});if(!valid()||!opened||generation!==epoch||selection!==selectionEpoch)return;
   busy=false;if(!found){failure='focus';status(root.dataset.searchMissing);controls();return}
   selected=hit.id;const url=new URL(location.href);url.searchParams.set('message',hit.id);if(hit.historical)url.searchParams.set('chatSearchRevision',hit.revision);else url.searchParams.delete('chatSearchRevision');url.searchParams.delete('timelineCursor');url.searchParams.delete('timelineDirection');window.OieNavigation?.addressSearch(url);status(root.dataset.searchResultLabel+' '+(index+1)+(complete?' / '+hits.length:''));
   showExcerpt(hit);
   highlight();controls();document.dispatchEvent(new CustomEvent('oie:conversation-search-selected',{detail:{id:hit.id,index}}));
  }catch{if(valid()&&generation===epoch&&selection===selectionEpoch){busy=false;failure='focus';status(root.dataset.searchFailed);controls()}}
 }
 async function request(cursor='',position=0,focusResult=true){
  if(!valid()||!opened||busy||composing)return;const request=state(),generation=epoch;if(!request.q){reset();return}if(Array.from(request.q).length>200){failure='';status(root.dataset.searchInvalid);controls();return}
  busy=true;failure='';lastSearchFocus=focusResult;status(root.dataset.searchLoading);controls();controller=new AbortController();const ownController=controller,timeout=life.timeout(()=>ownController.abort(),12000);
  try{
   const params=new URLSearchParams({...request,limit:'50',presentation:'conversation',lang:document.documentElement.lang});
   const address=new URL(location.href).searchParams;
   const target=focusResult?'':address.get('message');
   const revision=target&&history.checked?address.get('chatSearchRevision'):'';
   const restoreRevision=uuid(target)&&uuid(revision);
   const visited=new Set();
   while(true){
    if(visited.has(cursor))throw Error('cursor');
    visited.add(cursor);
    lastCursor=cursor;
    if(cursor)params.set('cursor',cursor);else params.delete('cursor');
    const response=await life.fetch('/ui/message-search?'+params,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:ownController.signal});if(!response.ok||!response.headers.get('Content-Type')?.startsWith('text/html'))throw Error(response.status===409?'engine_changed':response.status===400?'invalid':'response');const html=await response.text();if(html.length>2*1024*1024)throw Error('large');if(!valid()||!opened||generation!==epoch||!same(request))return;
    const page=parse(html,request,cursor);localCoverage=page.coverage;
    addHits(page.items);next=page.next;updateCoverage();
    if(focusResult||!hits.length)break;
    index=hits.findIndex(hit=>hit.id===target&&(!restoreRevision||hit.historical&&hit.revision===revision));
    if(index>=0){
     selected=target;showExcerpt(hits[index]);highlight();
     status(root.dataset.searchResultLabel+' '+(index+1)+(complete?' / '+hits.length:''));
     break;
    }
    if(!restoreRevision||!next){
     status(restoreRevision?root.dataset.searchMissing:'');
     break;
    }
    // Follow the same bounded local pages under the original request timeout.
    cursor=next;
   }
   busy=false;
   if(!hits.length)status(root.dataset.searchEmpty);
   else if(focusResult){await select(position);return;}
   controls();
  }catch(error){if(valid()&&opened&&generation===epoch&&same(request)){busy=false;failure='search';status(error.message==='engine_changed'?root.dataset.searchEngineChanged:error.message==='invalid'?root.dataset.searchInvalid:root.dataset.searchFailed);controls()}}
  finally{clearTimeout(timeout);if(controller===ownController)controller=null;}
 }
 function addHits(items){
  const known=new Set(hits.map(hit=>hit.key));
  for(const hit of items){if(!known.has(hit.key)){known.add(hit.key);hits.push(hit)}}
 }
 function updateCoverage(){
  const archive=window.OieRoomArchive;
  const localOnly=archive?.partial()&&(archiveFailed||!archive.search());
  coverage.textContent=localOnly?root.dataset.archiveLocalResults:localCoverage;
  coverage.hidden=!coverage.textContent;
  complete=!next&&!archiveCursor&&!archivePending;
 }
 async function searchArchive(cursor='', focusResult=true){
  if(!valid()||!opened||composing)return;
  const generation=epoch,request=state();
  if(!window.OieRoomArchive?.search()||!request.q||Array.from(request.q).length>200){archivePending=false;updateCoverage();controls();return}
  archivePending=true;
  const controller=new AbortController();
  archiveController=controller;
  const timeout=life.timeout(()=>controller.abort(),65000);
  try{
   const result=await window.OieRoomArchive.request(life,{action:'search',query:request.q,...(cursor?{cursor}:{})},controller.signal);
   if(!valid()||!opened||generation!==epoch||!same(request))return;
   archivePending=false;
   if(!result||!['stored','complete'].includes(result.outcome)){archiveFailed=true;archiveCursor='';updateCoverage();controls();return}
   archiveFailed=false;
   const previous=hits.length;
   addHits(result.items.map(message=>({
    key:message.id,id:message.id,revision:message.revisionId,remote:true,historical:false,kind:'text',excerpt:'',
   })));
   archiveCursor=result.nextCursor||'';
   updateCoverage();
   if(!busy&&focusResult&&(index<0&&hits.length||cursor&&hits.length>previous)){
    await select(index<0?0:previous);
   }else{
    if(!busy&&selected)status(root.dataset.searchResultLabel+' '+(index+1)+(complete?' / '+hits.length:''));
    controls();
   }
  }catch{
   if(valid()&&opened&&generation===epoch&&same(request)){
    archiveFailed=true;archivePending=false;archiveCursor='';updateCoverage();controls();
   }
  }finally{
   clearTimeout(timeout);
   if(archiveController===controller)archiveController=null;
  }
 }
 function changed(delay=100){
  if(!opened)return;
  returnFocus=null;abort();reset();address(true);
  if(!composing&&state().q){
   archivePending=window.OieRoomArchive?.search()===true;
   timer=life.timeout(()=>request(),delay);
   if(archivePending)archiveTimer=life.timeout(()=>searchArchive(),350);
  }
 }
 function open(){if(!valid())return;const menu=document.getElementById('chat-options'),item=menu?.contains(document.activeElement)?document.activeElement:menu?.querySelector('[role=menuitem]');item?.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));opened=true;root.hidden=false;root.dataset.searchOpen='true';address();query.focus({preventScroll:true});if(state().q&&index<0)changed(0);}
 function close(){opened=false;root.hidden=true;root.dataset.searchOpen='false';abort();clearHighlights();address();controls();document.getElementById('chat-search-messages')?.focus({preventScroll:true});}
 life.on(document,'oie:conversation-search-open',open);
 life.on(document,'click',event=>{const link=event.target.closest?.('[data-open-conversation-search]');if(link&&event.button===0&&!event.metaKey&&!event.ctrlKey&&!event.shiftKey&&!event.altKey&&(!link.dataset.searchConversation||link.dataset.searchConversation===conversation)){event.preventDefault();event.stopImmediatePropagation();window.dispatchEvent(new CustomEvent('drawer:close',{detail:{id:'details'}}));open()}},true);
 life.on(form,'submit',event=>{event.preventDefault();changed(0)});
 life.on(query,'compositionstart',()=>{composing=true;abort()});life.on(query,'compositionend',()=>{composing=false;changed()});life.on(query,'input',event=>{if(!event.isComposing&&!composing)changed()});life.on(history,'change',()=>changed(0));
 life.on(older,'click',()=>{if(busy)return;returnFocus=older;if(index+1<hits.length)select(index+1);else if(next)request(next,index+1);else if(archiveCursor&&!archivePending)searchArchive(archiveCursor)});
 life.on(newer,'click',()=>{if(!busy&&index>0){returnFocus=newer;select(index-1)}});
 life.on(root.querySelector('[data-conversation-search-close]'),'click',event=>{event.preventDefault();event.stopPropagation();close()});
 life.on(retry,'click',()=>{returnFocus=retry;if(failure==='focus')select(index);else request(lastCursor,index<0?0:index+1,lastSearchFocus)});
 life.on(root,'keydown',event=>{
  if(!opened||event.defaultPrevented||event.isComposing||event.keyCode===229||composing||event.ctrlKey||event.metaKey||event.altKey)return;
  if(event.key==='Escape'){event.preventDefault();close();return;}
  if(event.target!==query)return;
  const direction=event.key==='Enter'?(event.shiftKey?newer:older):event.shiftKey?null:event.key==='ArrowDown'?older:event.key==='ArrowUp'?newer:null;
  if(direction){event.preventDefault();if(!direction.disabled)direction.click();}
 });
 life.on(document,'oie:timeline-content',()=>{if(opened&&selected)highlight()});life.on(document,'oie:previews-refresh',()=>{if(opened&&selected)life.raf(highlight)});life.on(document,'change',event=>{if(event.target.matches('[data-render-markdown]')&&opened&&selected)life.raf(highlight)});life.on(window,'storage',event=>{if(event.key==='oie.device.render-markdown.v1'&&opened&&selected)life.raf(highlight)});
 life.cleanup(()=>{abort();clearHighlights()});controls();if(opened&&state().q){address();const focusResult=!new URL(location.href).searchParams.has('message');archivePending=window.OieRoomArchive?.search()===true;request('',0,focusResult);if(archivePending)archiveTimer=life.timeout(()=>searchArchive('',focusResult),350);}
});
