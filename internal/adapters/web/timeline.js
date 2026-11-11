window.OieRuntime.register('timeline',life=>{
 'use strict';
 const root=document.getElementById('message-timeline');if(!root)return;
 const uuid=value=>/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value||''),tuple=(a,b)=>a.ms===b.ms?(a.id>b.id?1:a.id<b.id?-1:0):a.ms>b.ms?1:-1;
 const rows=()=>[...root.querySelectorAll(':scope>[data-message-id][data-message-source]')];
 const identity={engine:document.body.dataset.engineId,account:document.body.dataset.accountId,conversation:document.body.dataset.conversationId};
 let disposed=false,busy=false,epoch=0,controller,observer,resizer,anchor,restoring=false,follow=false,failed='',retryDirection='',refreshAgain=false,refreshPage,refreshStartedAt=0,liveAttached=false,older=false,newer=false,retryMode='page',retryTarget='',forceFollow=false,focusEpoch=0,focusPending=false;
 let exhaustedArchiveEdge='';
 const remoteOlder=()=>{
  const first=rows()[0]?.dataset.messageId;
  return !!first&&first!==exhaustedArchiveEdge&&window.OieRoomArchive?.older()===true;
 };
 const edge=direction=>root.querySelector(`[data-timeline-edge="${direction}"]`),nearBottom=()=>root.scrollHeight-root.scrollTop-root.clientHeight<60;
 function parse(scope,limit=200){
  const envelope=scope.querySelector('template[data-timeline-page]');if(!envelope)return null;const data=envelope.dataset;
  for(const key of ['engine','account','conversation','mode','direction','cursor','canonicalCursor','requestOldest','requestNewest','oldest','newest','oldestMs','newestMs','hasOlder','hasNewer','complete','state'])if(!Object.hasOwn(data,key))throw Error('envelope');
  if(data.engine!==identity.engine||data.account!==identity.account||data.conversation!==identity.conversation||data.state!=='ready'||!['true','false'].includes(data.hasOlder)||!['true','false'].includes(data.hasNewer)||!['true','false'].includes(data.complete))throw Error('scope');
  const entries=[],seen=new Set();for(const node of scope.querySelectorAll('[data-message-id][data-message-source]')){const id=node.dataset.messageId,ms=Number(node.dataset.createdMs);if(!uuid(id)||!uuid(node.dataset.messageRevision)||node.dataset.messageAccount!==identity.account||node.dataset.messageConversation!==identity.conversation||!/^[-]?\d+$/.test(node.dataset.createdMs||'')||!Number.isSafeInteger(ms)||seen.has(id)||!node.dataset.messageVersion)throw Error('row');const item={id,ms,node,version:node.dataset.messageVersion};if(entries.length&&tuple(entries.at(-1),item)>=0)throw Error('order');seen.add(id);entries.push(item)}
  if(entries.length>limit||entries.length&&(data.oldest!==entries[0].id||data.newest!==entries.at(-1).id||data.oldestMs!==String(entries[0].ms)||data.newestMs!==String(entries.at(-1).ms))||!entries.length&&(data.oldest||data.newest))throw Error('bounds');
  return {data,entries,scope};
 }
 function current(){return rows().map(node=>({id:node.dataset.messageId,ms:Number(node.dataset.createdMs),version:node.dataset.messageVersion,node}))}
 function avatarGroups(){
  const entries=rows();for(let i=0;i<entries.length;i++){const node=entries[i],next=entries[i+1];
   const continues=next&&node.dataset.messageGroupable==='true'&&next.dataset.messageGroupable==='true'&&['messageAccount','messageConversation','messageSender','messageSenderName'].every(key=>node.dataset[key]===next.dataset[key])&&node.classList.contains('oie-message-own')===next.classList.contains('oie-message-own');
   node.classList.toggle('oie-message-group-end',!continues);
  }
 }
 function interactionProtected(node){const selection=getSelection(),active=document.activeElement;return active!==node&&!active?.matches('[data-form-feedback]')&&node.contains(active)||!!node.querySelector('[aria-expanded="true"]')||[...node.querySelectorAll('video,audio')].some(media=>!media.paused&&!media.ended)||!!selection&&!selection.isCollapsed&&(node.contains(selection.anchorNode)||node.contains(selection.focusNode))}
 // A loaded video's reconciler preserves matching native players and releases
 // removed source references. That inner refresh is distinct from row eviction.
 function canReconcileVideo(node){const active=document.activeElement,selection=getSelection();return !!window.OieVideos&&!!node.querySelector('video[data-video-player][src]')&&!(active!==node&&node.contains(active)&&!active.matches('[data-form-feedback],video[data-video-player]'))&&!node.querySelector('[aria-expanded="true"]')&&![...node.querySelectorAll('audio')].some(media=>!media.paused&&!media.ended)&&!(selection&&!selection.isCollapsed&&(node.contains(selection.anchorNode)||node.contains(selection.focusNode)))}
 function protectedRow(node){const viewport=root.getBoundingClientRect(),box=node.getBoundingClientRect();return box.bottom>viewport.top&&box.top<viewport.bottom||node.contains(document.activeElement)||interactionProtected(node)}
 function capacity(direction,count=50){const existing=current(),need=Math.max(0,existing.length+count-200),candidates=direction==='older'?existing.slice(-need).reverse():existing.slice(0,need);return !need||candidates.every(item=>!protectedRow(item.node))}
 function setFeedback(direction,state=''){
  const control=edge(direction);if(!control)return;control.dataset.state=state;const status=control.querySelector('[data-timeline-status]'),button=control.querySelector('[data-timeline-load]');status.textContent=state==='loading'?control.dataset.loading:state==='failed'?control.dataset.failed:state==='paused'?control.dataset.paused:'';status.setAttribute('role',state==='failed'?'alert':'status');button.hidden=!(direction==='older'?older||remoteOlder():newer)&&!state;button.setAttribute('aria-disabled',String(busy));const boundary=direction==='older'?current()[0]:current().at(-1);if(boundary){const href=new URL(button.href,location.href);if(href.origin===location.origin&&href.pathname==='/'){href.searchParams.set('timelineCursor',boundary.id);href.searchParams.set('timelineDirection',direction);button.href=href.pathname+href.search}}if(state==='failed'||state==='paused')button.textContent=control.dataset.retry;
 }
 function controls(){capture();setFeedback('older',failed&&retryDirection==='older'?failed:busy&&retryDirection==='older'?'loading':'');setFeedback('newer',failed&&retryDirection==='newer'?failed:busy&&retryDirection==='newer'?'loading':'');root.dataset.timelineBusy=String(busy);root.dataset.timelineOlder=String(older);root.dataset.timelineNewer=String(newer);const jump=document.querySelector('[data-jump-latest]');if(jump&&(newer||document.body.dataset.focusMessage))jump.hidden=false;correct()}
 function capture(){const viewport=root.getBoundingClientRect(),item=rows().find(row=>row.getBoundingClientRect().bottom>viewport.top);anchor=item?{id:item.dataset.messageId,top:item.getBoundingClientRect().top}:null;follow=liveAttached&&nearBottom()}
 function correct(){if(disposed||!root.isConnected)return;restoring=true;if(follow)root.scrollTop=root.scrollHeight;else if(anchor){const node=rows().find(row=>row.dataset.messageId===anchor.id);if(node)root.scrollTop+=node.getBoundingClientRect().top-anchor.top}
  // A completed tail page can leave scrollTop unchanged. Clear its transient
  // notice here instead of waiting for a scroll event that may never fire.
  const jump=document.querySelector('[data-jump-latest]');if(jump){if(newer||document.body.dataset.focusMessage)jump.hidden=false;else if(nearBottom())jump.hidden=true}
  life.raf(()=>{restoring=false})}
 function hooks(page,generation,startedAt){document.dispatchEvent(new CustomEvent('oie:timeline-content',{detail:{page,startedAt}}));document.dispatchEvent(new CustomEvent('oie:stars-refresh',{detail:{page,generation}}));document.dispatchEvent(new CustomEvent('oie:previews-refresh',{detail:page}));document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));document.dispatchEvent(new CustomEvent('oie:actions-refreshed'))}
 function merge(page,{direction='update',authoritative=false,followEnd=false,starGeneration=Number(document.body.dataset.starGeneration||0),aliases=[],reset=false}={}){
  const old=current(),logical=normalizedRows(old,aliases);for(const plan of aliases){const original=old.find(item=>item.id===plan.previous);if(original&&interactionProtected(original.node)){failed='paused';retryDirection=direction==='older'?'older':'newer';controls();return false}}for(const item of page.entries){const previous=logical.find(value=>value.id===item.id);if(previous&&previous.ms!==item.ms)throw Error('timestamp_drift')}const items=new Map((reset?[]:logical).map(item=>[item.id,item])),incoming=new Set(page.entries.map(item=>item.id));
  if(authoritative&&page.entries.length){const low=page.entries[0],high=page.entries.at(-1);for(const item of logical)if(tuple(item,low)>=0&&tuple(item,high)<=0&&!incoming.has(item.id)&&!interactionProtected(item.node))items.delete(item.id)}
  for(const item of page.entries)items.set(item.id,item);
  let ordered=[...items.values()].sort(tuple),trimmed=[];
  while(ordered.length>200){const index=direction==='older'?ordered.length-1:0,item=ordered[index];if(item.node.isConnected&&protectedRow(item.node)){failed='paused';retryDirection=direction==='older'?'older':'newer';controls();return false}trimmed.push(item);ordered.splice(index,1)}
  capture();if(followEnd)follow=true;const keep=new Set(ordered.map(item=>item.id)),existing=new Map(old.map(item=>[item.id,item.node]));
  const apply=()=>{
   for(const item of old)if(!keep.has(item.id)){window.OieVideos?.release(item.node);window.Alpine?.destroyTree(item.node);item.node.remove()}
   const after=edge('newer');for(const item of ordered){let node=existing.get(item.id);if(node?.isConnected){if(node.dataset.messageVersion!==item.version&&(!interactionProtected(node)||canReconcileVideo(node))&&!(item.node.dataset.messagePreviewsKnown!=='true'&&node.dataset.messageRevision===item.node.dataset.messageRevision)){const active=node.contains(document.activeElement)?document.activeElement:null,activeID=active?.id,protectedUpdate=interactionProtected(node),swapped=window.OieVideos?.swap(node,item.node.innerHTML);if(!swapped&&!protectedUpdate){for(const child of node.children)window.Alpine?.destroyTree(child);node.innerHTML=item.node.innerHTML;for(const child of node.children)window.Alpine?.initTree(child)}if(swapped||!protectedUpdate){for(const attribute of item.node.attributes)node.setAttribute(attribute.name,attribute.value);if(active&&!active.isConnected)(activeID&&node.querySelector('#'+CSS.escape(activeID))||node).focus({preventScroll:true})}}}else{node=item.node.cloneNode(true);const next=ordered.slice(ordered.indexOf(item)+1).map(value=>existing.get(value.id)).find(value=>value?.isConnected)||after;root.insertBefore(node,next);window.Alpine?.initTree(node);existing.set(item.id,node)}}
  };
  if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();avatarGroups();
  if(trimmed.length){if(direction==='older'){newer=true;liveAttached=false}else older=true}
  for(const plan of aliases){if(anchor?.id===plan.previous)anchor.id=plan.record.id;if(document.body.dataset.focusMessage===plan.previous)document.body.dataset.focusMessage=plan.record.id;document.dispatchEvent(new CustomEvent('oie:timeline-alias',{detail:{requested:plan.previous,canonical:plan.record.id}}))}hooks(page.scope,starGeneration,page.startedAt);correct();life.raf(correct);observe();return true;
 }
 function observe(){observer?.disconnect();resizer?.disconnect();if(resizer)for(const row of rows())resizer.observe(row);if(failed||busy||disposed||focusPending)return;if(observer){if(older)observer.observe(edge('older'));if(newer)observer.observe(edge('newer'))}}
 function aliasPlans(pageDocument,requested){
  const plans=[],seen=new Set();for(const template of pageDocument.querySelectorAll('template[data-timeline-alias]')){const previous=template.dataset.timelineAlias,id=template.dataset.canonical,node=template.content.firstElementChild,ms=Number(node?.dataset.createdMs);if(!requested.includes(previous)||seen.has(previous)||!uuid(id)||id===previous||!node||node.dataset.messageId!==id||node.dataset.messageAccount!==identity.account||node.dataset.messageConversation!==identity.conversation||!uuid(node.dataset.messageRevision)||!/^[-]?\d+$/.test(node.dataset.createdMs||'')||!Number.isSafeInteger(ms)||!node.dataset.messageVersion)throw Error('alias');seen.add(previous);plans.push({previous,record:{id,ms,node,version:node.dataset.messageVersion}})}return plans;
 }
 function normalizedRows(before,plans){const items=new Map(before.map(item=>[item.id,item]));for(const plan of plans){if(!items.has(plan.previous))continue;items.delete(plan.previous);const existing=items.get(plan.record.id);if(existing&&existing.ms!==plan.record.ms)throw Error('alias_timestamp');if(!existing)items.set(plan.record.id,plan.record)}return [...items.values()].sort(tuple)}
 function endpoint(mode,direction='',cursor='',bounds){const url=new URL('/ui/timeline',location.origin);for(const [key,value]of Object.entries({engineId:identity.engine,accountId:identity.account,conversationId:identity.conversation,mode,direction,cursor,lang:document.documentElement.lang,theme:document.documentElement.dataset.mode||'system',...(bounds||{})}))if(value)url.searchParams.set(key,value);return url}
 async function request(mode,direction,manual=false,target='',focusOptions={}){let nextRequest;
  if(disposed||busy||!manual&&failed)return;if(mode==='page'&&!(direction==='older'?older||remoteOlder():newer))return;
  retryMode=mode;
  if(mode==='page'&&!capacity(direction)){failed='paused';retryDirection=direction;controls();observe();return}
  const before=current();if(!before.length&&mode!=='context')return false;const bound=direction==='older'?before[0]:before.at(-1),bounds={oldest:before[0]?.id||'',newest:before.at(-1)?.id||''},url=mode==='window'?endpoint(mode,'','',bounds):mode==='context'?endpoint(mode,'',target):endpoint(mode,direction,bound.id),generation=++epoch,starGeneration=Number(document.body.dataset.starGeneration||0),wasBottom=nearBottom()||forceFollow;forceFollow=false;
  busy=true;retryDirection=direction;retryMode=mode;retryTarget=target;failed='';controls();observe();controller=new AbortController();
  const oldestCovered=Number(root.dataset.archiveCovered||0);
  const loadArchive=mode==='page'&&direction==='older'&&remoteOlder()&&(!older||oldestCovered>0&&bound.ms<=oldestCovered);
  const timer=life.timeout(()=>controller.abort(),loadArchive?65000:12000);
  try{
   if(loadArchive){
    const result=await window.OieRoomArchive.request(life,{action:'older',messageId:bound.id},controller.signal);
    if(disposed||generation!==epoch)return;
    if(!older&&result?.outcome==='unavailable')throw Error('archive');
    if(result?.outcome==='complete')exhaustedArchiveEdge=bound.id;
   }
   const startedAt=performance.now();
   const response=await life.fetch(url,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:controller.signal});if(!response.ok||!response.headers.get('Content-Type')?.startsWith('text/html'))throw Error('response');const html=await response.text();if(html.length>12*1024*1024)throw Error('large');if(disposed||generation!==epoch)return;
   const pageDocument=new DOMParser().parseFromString(html,'text/html'),page=parse(pageDocument,mode==='window'?200:50);if(!page||page.data.mode!==mode||(mode==='page'||mode==='context')&&page.data.cursor!==(mode==='context'?target:bound.id)||mode==='page'&&page.data.direction!==direction||mode==='window'&&(page.data.requestOldest!==bounds.oldest||page.data.requestNewest!==bounds.newest))throw Error('request');
   page.startedAt=startedAt;
   const aliases=aliasPlans(pageDocument,mode==='page'?[bound.id]:mode==='context'?[target]:[bounds.oldest,bounds.newest]),normalized=normalizedRows(current(),aliases),lower=normalized[0],upper=normalized.at(-1),canonicalBound=mode==='page'?normalized.find(item=>item.id===(page.data.canonicalCursor||bound.id)):null;
   if((mode==='page'||mode==='context')&&(!uuid(page.data.canonicalCursor)||page.data.canonicalCursor!==(mode==='context'?target:bound.id)&&!aliases.some(plan=>plan.previous===(mode==='context'?target:bound.id)&&plan.record.id===page.data.canonicalCursor)))throw Error('cursor_proof');
   if(mode==='page'&&page.entries.some(item=>direction==='older'?tuple(item,canonicalBound||bound)>=0:tuple(item,canonicalBound||bound)<=0))throw Error('boundary');
   if(mode==='window'&&page.entries.some(item=>tuple(item,lower)<0||tuple(item,upper)>0))throw Error('window');
   for(const item of page.entries){const existing=normalized.find(value=>value.id===item.id);if(existing&&existing.ms!==item.ms)throw Error('timestamp_drift')}
   if(mode==='context'){
    if(!page.entries.some(item=>item.id===page.data.canonicalCursor))throw Error('context_target');
    const selection=getSelection(),locked=rows().some(row=>!!row.querySelector('[aria-expanded="true"]')||[...row.querySelectorAll('video,audio')].some(media=>!media.paused&&!media.ended)||selection&&!selection.isCollapsed&&(row.contains(selection.anchorNode)||row.contains(selection.focusNode)));if(locked){failed='paused';return}
    if(merge(page,{direction:'context',reset:true,starGeneration})){older=page.data.hasOlder==='true';newer=page.data.hasNewer==='true';liveAttached=false;document.body.dataset.focusMessage=page.data.canonicalCursor;document.dispatchEvent(new CustomEvent('oie:timeline-focus',{detail:{id:page.data.canonicalCursor,focus:focusOptions.focus!==false}}));capture();return true}return false;
   }
   const far=aliases.filter(plan=>!before.some(item=>item.id===plan.record.id)&&(tuple(plan.record,before[0])<0||tuple(plan.record,before.at(-1))>0));
   if(far.length){
    const focused=far.find(plan=>document.body.dataset.focusMessage===plan.previous||before.find(item=>item.id===plan.previous)?.node.contains(document.activeElement));
    if(focused||before.length===far.length){nextRequest={mode:'context',direction, target:(focused||far[0]).previous};return}
    if(far.some(plan=>{const item=before.find(value=>value.id===plan.previous);return item&&interactionProtected(item.node)})){failed='paused';return}
    capture();const retired=new Set(far.map(plan=>plan.previous));if(anchor&&retired.has(anchor.id)){const survivor=before.find(item=>!retired.has(item.id));anchor=survivor?{id:survivor.id,top:survivor.node.getBoundingClientRect().top}:null}
    const apply=()=>{for(const item of before)if(retired.has(item.id)){window.OieVideos?.release(item.node);window.Alpine?.destroyTree(item.node);item.node.remove()}};if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();avatarGroups();
    const removedOldest=retired.has(before[0].id);if(removedOldest)older=true;else{newer=true;liveAttached=false}correct();nextRequest={mode:'page',direction:removedOldest?'older':'newer'};return;
   }
   if(merge(page,{direction:mode==='page'?direction:'update',authoritative:mode==='window',followEnd:mode==='page'&&direction==='newer'&&liveAttached&&wasBottom,starGeneration,aliases})){
    if(mode==='page'){if(direction==='older')older=page.data.hasOlder==='true';else{newer=page.data.hasNewer==='true';if(!newer&&!window.document.body.dataset.focusMessage)liveAttached=true}}
   }
  }catch(error){if(!disposed&&!life.signal.aborted&&generation===epoch){failed='failed';retryDirection=direction||'older'}}
  finally{clearTimeout(timer);if(!disposed){busy=false;controls();observe();if(nextRequest&&!focusPending){life.timeout(()=>request(nextRequest.mode,nextRequest.direction,true,nextRequest.target||''),0)}else retryRefresh()}}
 }
 function retryRefresh(){
  if(refreshAgain&&!failed&&!focusPending){refreshAgain=false;life.timeout(()=>refresh(refreshPage,false,refreshStartedAt),100)}
 }
 function refresh(page,force=false,startedAt=performance.now()){forceFollow=forceFollow||force;
  if(disposed)return;let snapshot;try{snapshot=parse(page.querySelector('#live-timeline')||page)}catch{return}if(!snapshot)return;snapshot.startedAt=startedAt;refreshPage=page;refreshStartedAt=startedAt;if(busy){refreshAgain=true;return}
  const before=current();if(!before.length){if(snapshot.entries.length){older=snapshot.data.hasOlder==='true';newer=snapshot.data.hasNewer==='true';merge(snapshot,{direction:'newer',followEnd:liveAttached});controls()}return}
  // A snapshot may update overlapping rows, but never bridge a historical gap.
  const ids=new Set(before.map(item=>item.id)),overlap=snapshot.entries.filter(item=>ids.has(item.id));if(overlap.length)merge({...snapshot,entries:overlap},{direction:'update'});
  const completeCoverage=before.every(item=>snapshot.entries.some(value=>value.id===item.id));
  if(liveAttached&&!document.body.dataset.focusMessage){const newest=snapshot.entries.at(-1);if(newest&&tuple(newest,before.at(-1))>0){newer=true;request('page','newer');return}}
  if(!completeCoverage&&!failed)request('window','older');controls();
 }
 let initial;try{initial=parse(root,50)}catch{return}if(!initial)return;older=initial.data.hasOlder==='true';newer=initial.data.hasNewer==='true';liveAttached=!newer&&!document.body.dataset.focusMessage;
 // Search uses the same bounded context parser/merge path as reply navigation.
 // A newer target invalidates an unfinished request before it can mount rows.
 function cancelFocus(){focusEpoch++;if(focusPending){epoch++;controller?.abort()}focusPending=false}
 async function focus(id,options={}){
  const generation=++focusEpoch;focusPending=true;
  if(disposed||!uuid(id)||document.body.dataset.engineId!==identity.engine||document.body.dataset.accountId!==identity.account||document.body.dataset.conversationId!==identity.conversation){focusPending=false;return false}
  if(busy){epoch++;controller?.abort()}
  while(busy&&!disposed&&generation===focusEpoch)await new Promise(resolve=>setTimeout(resolve,16));
  if(disposed||generation!==focusEpoch)return false;
  const row=rows().find(item=>item.dataset.messageId===id);
  let found=false;
  if(row){document.body.dataset.focusMessage=id;liveAttached=false;document.dispatchEvent(new CustomEvent('oie:timeline-focus',{detail:{id,focus:options.focus!==false}}));controls();found=true}
  else found=await request('context','',true,id,options)===true;
  if(generation!==focusEpoch||disposed)return false;
  focusPending=false;controls();observe();retryRefresh();return found;
 }
 const api={refresh,focus,cancelFocus,get hasNewer(){return newer},get liveAttached(){return liveAttached},get size(){return rows().length}};window.OieTimeline=api;
 if(typeof IntersectionObserver==='function')observer=new IntersectionObserver(entries=>{for(const entry of entries)if(entry.isIntersecting){const direction=entry.target.dataset.timelineEdge;if(direction==='older'&&older||direction==='newer'&&newer){request('page',direction);break}}},{root,rootMargin:'70px 0px'});
 if(typeof ResizeObserver==='function')resizer=new ResizeObserver(()=>correct());
 life.on(document,'click',event=>{const button=event.target.closest('[data-timeline-load]');if(button&&root.contains(button)&&!event.defaultPrevented&&event.button===0&&!event.ctrlKey&&!event.metaKey&&!event.shiftKey&&!event.altKey){event.preventDefault();event.stopPropagation();request(failed?retryMode:'page',button.dataset.timelineLoad,true,failed?retryTarget:'')}},true);
 function loadArchiveAtTop(){
  if(root.scrollTop<=70&&!older&&remoteOlder()&&current().length)request('page','older');
 }
 let lastTop=root.scrollTop;
 life.on(root,'scroll',()=>{
  const movedUp=root.scrollTop<lastTop;
  lastTop=root.scrollTop;
  if(!restoring){capture();if(movedUp)loadArchiveAtTop()}
 });
 life.on(root,'wheel',event=>{if(event.deltaY<0)loadArchiveAtTop()},{passive:true});
 life.on(root,'touchend',loadArchiveAtTop,{passive:true});
 life.on(root,'keydown',event=>{if(event.target===root&&['ArrowUp','PageUp','Home'].includes(event.key))loadArchiveAtTop()});
 life.on(document,'oie:timeline-snapshot',event=>refresh(event.detail.page,false,event.detail.startedAt));
 life.on(document,'oie:actions-refreshed',()=>{if(!busy&&!failed)observe()});
 life.on(document,'keydown',event=>{if(event.key==='Escape'&&failed==='paused')life.timeout(()=>{failed='';controls();observe()},200)});
 life.cleanup(()=>{disposed=true;focusEpoch++;epoch++;controller?.abort();observer?.disconnect();resizer?.disconnect();if(window.OieTimeline===api)delete window.OieTimeline});
 avatarGroups();controls();life.raf(()=>{capture();observe()});
});
