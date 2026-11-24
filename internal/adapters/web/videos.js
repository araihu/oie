window.OieRuntime.register('videos', life => {
 'use strict';
 const loaded=new Set(),sessions=new WeakMap(),releasing=new WeakSet(),failed=new WeakSet();
 const selector='[data-video-item]',playerSelector='video[data-video-player]';
 const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 const identity=card=>JSON.stringify(['videoEngine','videoAccount','videoConversation','videoMessage','videoItem'].map(key=>card.dataset[key]||''));
 const cards=root=>[...(root.matches?.(selector)?[root]:[]),...root.querySelectorAll(selector)];
 const isLoaded=card=>loaded.has(card.querySelector(playerSelector));
 function status(card,key,state){card.querySelector('[data-video-download]').hidden=state!=='failed';const region=card.querySelector('[data-video-status]'),compact=card.hasAttribute('data-video-compact'),silent=state==='ready'||state==='failed'||compact;if(region){region.textContent=silent?'':card.dataset[key]||'';region.hidden=silent;region.dataset.state=state;region.setAttribute('role','status')}const metadata=card.querySelector('[data-video-metadata]');if(metadata&&compact)metadata.hidden=state!=='failed'}
 let fallbackVideo = null;
 function primeAudio(){
  try {
   const audio = new AudioContext();
   void audio.resume().catch(()=>{});
   return audio;
  } catch { return null; }
 }
 function releaseAudio(session){
  if(session.audio) void session.audio.close().catch(()=>{});
  session.audio=null;
 }
 function stopSession(video){
  const session = sessions.get(video);
  if(!session) return;
  sessions.delete(video);
  if(fallbackVideo===video) fallbackVideo=null;
  session.abort.abort();
  releaseAudio(session);
 }
 function release(video){
  if(!video)return;stopSession(video);releasing.add(video);loaded.delete(video);failed.delete(video);
  video.pause();video.removeAttribute('src');video.load();video.hidden=true;
  const card=video.closest(selector);if(card){card.querySelector('[data-video-cover]').hidden=false;card.querySelector('[data-video-retry]').hidden=true;status(card,'videoReady','ready')}
  releasing.delete(video);
 }
 function releaseTree(root){if(root.matches?.(playerSelector))release(root);for(const video of root.querySelectorAll?.(playerSelector)||[])release(video)}
 function failure(card,unsupported=false){const video=card.querySelector(playerSelector);if(failed.has(video))return;stopSession(video);failed.add(video);const key=unsupported?'videoUnsupported':'videoUnavailable';status(card,key,'failed');card.querySelector('[data-video-retry]').hidden=false;video.hidden=true;card.querySelector('[data-video-cover]').hidden=false;window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message:card.dataset[key]||''}}))}
 function start(card){
  const video=card.querySelector(playerSelector);let source;
  try{source=new URL(card.dataset.videoSource,location.href)}catch{failure(card);return}
  const {videoEngine,videoAccount,videoConversation,videoMessage,videoItem}=card.dataset;
  if(![videoEngine,videoAccount,videoConversation,videoMessage,videoItem].every(uuid)||videoEngine!==document.body.dataset.engineId||source.origin!==location.origin||source.pathname!=='/ui/shared-content/'+videoItem+'/content'||source.searchParams.get('engineId')!==videoEngine||[...source.searchParams.keys()].length!==1){failure(card);return}
  if(fallbackVideo && fallbackVideo!==video) release(fallbackVideo);
  stopSession(video);
  const session = {abort:new AbortController(),audio:primeAudio(),fallback:false};
  sessions.set(video,session);
  const signal = session.abort.signal;
  loaded.add(video);failed.delete(video);
  card.querySelector('[data-video-cover]').hidden=true;card.querySelector('[data-video-retry]').hidden=true;video.hidden=false;status(card,'videoLoading','loading');
  const hasFocus = !!document.activeElement?.closest('[data-video-play],[data-video-retry]');
  let rendered = false;
  function confirmFrame(){
   if(signal.aborted || session.fallback) return;
   rendered = true;
   releaseAudio(session);
  }
  if(video.requestVideoFrameCallback){
   const callback = video.requestVideoFrameCallback(confirmFrame);
   signal.addEventListener('abort',()=>video.cancelVideoFrameCallback(callback),{once:true});
  }
  const extensions = {'video/x-matroska':'mkv','video/matroska':'mkv','video/x-msvideo':'avi','video/mp4':'mp4','video/webm':'webm','video/quicktime':'mov','video/ogg':'ogg'};
  session.startFallback = async () => {
   if (signal.aborted || session.fallback) return;
   if(fallbackVideo && fallbackVideo!==video) release(fallbackVideo);
   fallbackVideo=video;
   session.fallback = true;
   const focus = document.activeElement === video;
   video.pause();video.removeAttribute('src');video.load();video.hidden=true;
   status(card,'videoLoading','loading');
   try {
    const response = await fetch(source, {headers:{Range:'bytes=0-0'},signal,redirect:'error'});
    const ext = extensions[response.headers.get('Content-Type')?.split(';')[0]];
    void response.body?.cancel().catch(()=>{});
    signal.throwIfAborted();
    if (!response.ok) {failure(card);return;}
    if (!ext) {failure(card,true);return;}
    const {playFallback} = await import('/ui/video-fallback.js');
    signal.throwIfAborted();
    await playFallback({card,source:source.href,ext,signal,focus,takeAudio:()=>{const audio=session.audio;session.audio=null;return audio;},
     onState:(key,state)=>status(card,key,state),onFailure:()=>failure(card,true)});
   } catch {if(!signal.aborted) failure(card,true);}
  };
  function checkFrame(){
   if (rendered || session.fallback || document.hidden || video.seeking) return;
   if (!video.requestVideoFrameCallback && video.videoWidth && video.readyState >= 2) {confirmFrame();return;}
   if ((video.currentTime > 0.8 && !video.paused || video.ended) && (!video.videoWidth || video.getVideoPlaybackQuality?.().totalVideoFrames === 0)) void session.startFallback();
  }
  for (const event of ['timeupdate','ended']) video.addEventListener(event,checkFrame,{signal});
  video.src=source.pathname+source.search;
  const play=video.play();if(hasFocus)video.focus({preventScroll:true});
  play?.catch(error=>{
   if(signal.aborted||session.fallback||!video.isConnected||error.name==='AbortError')return;
   if(error.name==='NotAllowedError'){status(card,'videoReady','ready');return}
   if(error.name==='NotSupportedError')void session.startFallback();else failure(card);
  });
 }
 life.on(document,'click',event=>{const button=event.target.closest('[data-video-play],[data-video-retry]');if(!button)return;const card=button.closest(selector);if(!card)return;event.preventDefault();start(card)});
 for(const [event,key,state]of [['loadstart','videoLoading','loading'],['waiting','videoLoading','loading'],['playing','videoPlaying','playing'],['pause','videoPaused','paused'],['ended','videoReady','ready']])life.on(document,event,e=>{const video=e.target;if(!video.matches?.(playerSelector)||!loaded.has(video)||sessions.get(video)?.fallback||releasing.has(video)||failed.has(video))return;status(video.closest(selector),key,state)},true);
 life.on(document,'error',event=>{
  const video=event.target, session=sessions.get(video);
  if(!session||session.fallback||releasing.has(video)||!video.error)return;
  if([3,4].includes(video.error.code)) void session.startFallback();
  else failure(video.closest(selector));
 },true);
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
