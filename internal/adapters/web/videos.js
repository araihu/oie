window.OieRuntime.register('videos', life => {
 'use strict';
 const loaded=new Set(),generations=new WeakMap(),releasing=new WeakSet(),failed=new WeakSet();
 const selector='[data-video-item]',playerSelector='video[data-video-player]';
 const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 const identity=card=>JSON.stringify(['videoEngine','videoAccount','videoConversation','videoMessage','videoItem'].map(key=>card.dataset[key]||''));
 const cards=root=>[...(root.matches?.(selector)?[root]:[]),...root.querySelectorAll(selector)];
 const isLoaded=card=>!!card.querySelector(playerSelector)?.getAttribute('src');
 function status(card,key,state){const region=card.querySelector('[data-video-status]'),compact=card.hasAttribute('data-video-compact'),silent=state==='ready'||compact&&state!=='failed';if(region){region.textContent=silent?'':card.dataset[key]||'';region.hidden=silent;region.dataset.state=state;region.setAttribute('role',state==='failed'?'alert':'status')}const metadata=card.querySelector('[data-video-metadata]');if(metadata&&compact)metadata.hidden=state!=='failed'}
 function release(video){
  if(!video)return;generations.set(video,(generations.get(video)||0)+1);releasing.add(video);loaded.delete(video);failed.delete(video);
  video.pause();video.removeAttribute('src');video.load();video.hidden=true;
  const card=video.closest(selector);if(card){card.querySelector('[data-video-cover]').hidden=false;card.querySelector('[data-video-retry]').hidden=true;status(card,'videoReady','ready')}
  releasing.delete(video);
 }
 function releaseTree(root){if(root.matches?.(playerSelector))release(root);for(const video of root.querySelectorAll?.(playerSelector)||[])release(video)}
 function failure(card,unsupported=false){failed.add(card.querySelector(playerSelector));status(card,unsupported?'videoUnsupported':'videoUnavailable','failed');card.querySelector('[data-video-retry]').hidden=false;if(card.hasAttribute('data-video-compact')){card.querySelector(playerSelector).hidden=true;card.querySelector('[data-video-cover]').hidden=false;}}
 function start(card){
  const video=card.querySelector(playerSelector);let source;
  try{source=new URL(card.dataset.videoSource,location.href)}catch{failure(card);return}
  const {videoEngine,videoAccount,videoConversation,videoMessage,videoItem}=card.dataset;
  if(![videoEngine,videoAccount,videoConversation,videoMessage,videoItem].every(uuid)||videoEngine!==document.body.dataset.engineId||source.origin!==location.origin||source.pathname!=='/ui/shared-content/'+videoItem+'/content'||source.searchParams.get('engineId')!==videoEngine||[...source.searchParams.keys()].length!==1){failure(card);return}
  const generation=(generations.get(video)||0)+1;generations.set(video,generation);loaded.add(video);failed.delete(video);
  card.querySelector('[data-video-cover]').hidden=true;card.querySelector('[data-video-retry]').hidden=true;video.hidden=false;status(card,'videoLoading','loading');
  video.src=source.pathname+source.search;
  const play=video.play();if(document.activeElement?.closest('[data-video-play],[data-video-retry]'))video.focus({preventScroll:true});
  play?.catch(error=>{if(life.signal.aborted||generations.get(video)!==generation||!video.isConnected)return;if(error.name==='AbortError')return;if(error.name==='NotAllowedError'){status(card,'videoReady','ready');return}failure(card,error.name==='NotSupportedError')});
 }
 life.on(document,'click',event=>{const button=event.target.closest('[data-video-play],[data-video-retry]');if(!button)return;const card=button.closest(selector);if(!card)return;event.preventDefault();start(card)});
 for(const [event,key,state]of [['loadstart','videoLoading','loading'],['waiting','videoLoading','loading'],['playing','videoPlaying','playing'],['pause','videoPaused','paused'],['ended','videoReady','ready']])life.on(document,event,e=>{const video=e.target;if(!video.matches?.(playerSelector)||!loaded.has(video)||releasing.has(video)||failed.has(video))return;status(video.closest(selector),key,state)},true);
 life.on(document,'error',event=>{const video=event.target;if(video.matches?.(playerSelector)&&loaded.has(video)&&!releasing.has(video)&&video.error)failure(video.closest(selector),[3,4].includes(video.error?.code))},true);
 life.on(window,'pagehide',()=>{for(const video of [...loaded])release(video)});
 const observer=new MutationObserver(()=>{for(const video of loaded)if(!video.isConnected)release(video)});observer.observe(document.querySelector('#oie-app')||document.body,{childList:true,subtree:true});
 for(const name of ['drawer:close','drawer:close-request'])life.on(window,name,event=>{if(event.detail?.id!=='details')return;life.timeout(()=>{if(!event.defaultPrevented){const root=document.getElementById('chat-info-content');if(root)releaseTree(root)}},0)});

 function destroy(node){releaseTree(node);if(node.nodeType===Node.ELEMENT_NODE)window.Alpine?.destroyTree(node);node.remove()}
 function insert(parent,node,before){parent.insertBefore(node,before);if(node.nodeType===Node.ELEMENT_NODE)window.Alpine?.initTree(node)}
 function copyAttributes(current,fresh){if(current.nodeType!==Node.ELEMENT_NODE||fresh.nodeType!==Node.ELEMENT_NODE)return;for(const attr of [...current.attributes])if(!fresh.hasAttribute(attr.name))current.removeAttribute(attr.name);for(const attr of fresh.attributes)if(current.getAttribute(attr.name)!==attr.value)current.setAttribute(attr.name,attr.value)}
 function reconcile(current,fresh){
  // Preserve the native media element and its controller. Metadata is plain
  // app-owned content; no remote URL or source is copied into a loaded player.
  if(current.matches?.(selector)&&isLoaded(current)&&fresh.matches?.(selector)&&identity(current)===identity(fresh)){
   const old=current.querySelector('[data-video-label]'),next=fresh.querySelector('[data-video-label]');if(old&&next)old.textContent=next.textContent;return;
  }
  const nextChildren=[...fresh.childNodes],keep=[];
  for(const child of [...current.childNodes]){
   if(child.nodeType!==Node.ELEMENT_NODE)continue;
   const active=cards(child).filter(isLoaded);if(!active.length)continue;
   const index=nextChildren.findIndex(next=>next.nodeType===Node.ELEMENT_NODE&&next.nodeName===child.nodeName&&active.every(card=>cards(next).some(candidate=>identity(candidate)===identity(card))));
   if(index>=0&&!keep.some(entry=>entry.index===index))keep.push({child,next:nextChildren[index],index});
  }
  // Canonical retained source order is stable. If a source genuinely changes
  // scope/order, release it rather than preserve a player against another item.
  if(keep.some((entry,index)=>index>0&&entry.index<keep[index-1].index))keep.length=0;
  const retained=new Set(keep.map(entry=>entry.child));
  for(const child of [...current.childNodes])if(!retained.has(child))destroy(child);
  for(let index=0;index<nextChildren.length;index++){
   const entry=keep.find(value=>value.index===index);
   if(entry){copyAttributes(entry.child,entry.next);reconcile(entry.child,entry.next)}
   else{const before=keep.find(value=>value.index>index)?.child||null;insert(current,nextChildren[index].cloneNode(true),before)}
  }
 }
 const api={swap(target,html){
  if(!target||!cards(target).some(isLoaded))return false;
  const template=document.createElement('template');template.innerHTML=html;
  const apply=()=>reconcile(target,template.content);if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();return true;
 },release:releaseTree};
 window.OieVideos=api;
 life.cleanup(()=>{observer.disconnect();for(const video of [...loaded])release(video);if(window.OieVideos===api)delete window.OieVideos});
});
