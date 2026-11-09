window.OieRuntime.register('video-thumbnails', life => {
 'use strict';
 const preference='oie.device.auto-video-previews.v1',active=new Map(),requested=new Set();
 const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 const hash=value=>typeof value==='string'&&/^[0-9a-f]{64}$/.test(value);
 const cards=()=>[...document.querySelectorAll('[data-video-item][data-video-thumbnail-state]:not([data-video-thumbnail-state=""])')];
 const same=item=>cards().filter(card=>card.dataset.videoItem===item);
 const engine=document.body.dataset.engineId,csrf=document.querySelector('meta[name=oie-csrf]')?.content||'';
 async function jsonRequest(url,options={}){
  const controller=new AbortController(),timer=life.timeout(()=>controller.abort(),12000);
  try{const response=await life.fetch(url,{...options,signal:controller.signal});const text=await response.text();if(text.length>32768)throw Error('thumbnail_invalid');return {ok:response.ok,data:JSON.parse(text)}}finally{clearTimeout(timer)}
 }
 let automatic=true;try{automatic=localStorage.getItem(preference)!=='false'}catch{automatic=window.OieRuntime.memory.get(preference)!==false}
 function controls(card,failed=false){const container=card.querySelector('[data-video-thumbnail-controls]');if(container&&card.hasAttribute('data-video-compact'))container.hidden=!!card.dataset.videoPoster||automatic&&card.dataset.videoThumbnailState!=='unavailable'&&!failed}
 function preferences(){for(const control of document.querySelectorAll('[data-auto-video-thumbnails]'))control.checked=automatic;for(const card of cards())controls(card)}
 function actionLabel(card,failed){const button=card.querySelector('[data-video-thumbnail-load]');if(!button)return;const label=failed?button.dataset.thumbnailRetryLabel:button.dataset.thumbnailLoadLabel;const text=button.querySelector('[data-video-thumbnail-action-label]');if(text)text.textContent=label;else button.textContent=label;button.setAttribute('aria-label',label);button.title=failed?card.dataset.videoThumbnailFailed+' '+label:label;}
 function message(card,value){const region=card.querySelector('[data-video-thumbnail-status]');if(region){region.textContent=value;region.hidden=!value}}
 function validate(p,card){return p&&p.kind==='video'&&p.sharedContentId===card.dataset.videoItem&&[p.id,p.revisionId,p.sharedContentId,p.accountId,p.conversationId,p.messageId].every(uuid)&&p.accountId===card.dataset.videoAccount&&p.conversationId===card.dataset.videoConversation&&p.messageId===card.dataset.videoMessage&&['not_requested','queued','ready','unavailable'].includes(p.state)&&(!p.imageHash||hash(p.imageHash))}
 function apply(p){for(const card of same(p.sharedContentId)){
  if(!validate(p,card))continue;
  card.dataset.videoThumbnailState=p.state;
  const poster=p.state==='ready'&&hash(p.imageHash)?'/ui/media/'+p.imageHash:'';
  card.dataset.videoPoster=poster;
  const image=card.querySelector('[data-video-poster-image]');if(image){if(poster){image.src=poster;image.hidden=false}else{image.removeAttribute('src');image.hidden=true}}
  const player=card.querySelector('[data-video-player]');if(player&&poster)player.poster=poster;
  const button=card.querySelector('[data-video-thumbnail-load]');if(button){button.hidden=!!poster||p.state==='queued';button.disabled=p.state==='queued'}
  actionLabel(card,p.state==='unavailable');controls(card);message(card,p.state==='queued'?card.dataset.videoThumbnailLoading:p.state==='unavailable'?card.dataset.videoThumbnailFailed:'');
 }}
 function fail(item){for(const card of same(item)){actionLabel(card,true);controls(card,true);message(card,card.dataset.videoThumbnailFailed);const button=card.querySelector('[data-video-thumbnail-load]');if(button){button.hidden=false;button.disabled=false}}}
 async function request(card,manual=false){
  const item=card.dataset.videoItem;if(!uuid(item)||card.dataset.videoEngine!==engine||active.has(item)||!manual&&(requested.has(item)||card.dataset.videoThumbnailState==='unavailable')||card.dataset.videoPoster)return;
  requested.add(item);const token={};active.set(item,token);message(card,card.dataset.videoThumbnailLoading);
  try{
   const body=new URLSearchParams({csrf,engineId:engine,itemId:item,returnTo:location.pathname+location.search});
   const response=await jsonRequest('/ui/videos/thumbnail',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json'},body});
   const result=response.data;if(life.signal.aborted||active.get(item)!==token)return;
   if(!response.ok||!result.ok||!validate(result.thumbnail,card))throw Error('thumbnail_unavailable');
   apply(result.thumbnail);
   if(result.thumbnail.state==='queued'){poll(item,token,card,0);return}
  }catch{if(!life.signal.aborted)fail(item)}
  if(active.get(item)===token)active.delete(item);
 }
 async function poll(item,token,origin,attempt){
  if(life.signal.aborted||active.get(item)!==token)return;
  if(!same(item).length||attempt>=45){active.delete(item);if(attempt>=45)fail(item);return}
  try{
   const response=await jsonRequest('/api/v1/shared-content/'+item+'/thumbnail',{credentials:'same-origin',cache:'no-store',headers:{'X-Oie-Engine-ID':engine}});
   const p=response.data;if(life.signal.aborted||active.get(item)!==token)return;
   if(!response.ok||!validate(p,origin))throw Error('thumbnail_unavailable');apply(p);
   if(p.state!=='queued'){active.delete(item);return}
  }catch{if(!life.signal.aborted){active.delete(item);fail(item)}return}
  life.timeout(()=>poll(item,token,origin,attempt+1),1000);
 }
 const observed=new WeakSet(),near=new Set();
 const observer=new IntersectionObserver(entries=>{for(const entry of entries){if(entry.isIntersecting){near.add(entry.target);const card=entry.target,item=card.dataset.videoItem;if(card.dataset.videoThumbnailState==='queued'&&!active.has(item)){const token={};active.set(item,token);poll(item,token,card,0)}else if(automatic)request(card)}else near.delete(entry.target)}},{rootMargin:'100px'});
 function observe(){for(const card of cards())if(!observed.has(card)){observed.add(card);const failed=card.dataset.videoThumbnailState==='unavailable';actionLabel(card,failed);if(failed)message(card,card.dataset.videoThumbnailFailed);controls(card);observer.observe(card);const p=card.dataset.videoPoster;if(/^\/ui\/media\/[0-9a-f]{64}$/.test(p||''))card.querySelector('[data-video-player]')?.setAttribute('poster',p)}}
 const changes=new MutationObserver(observe);changes.observe(document.querySelector('#oie-app')||document.body,{childList:true,subtree:true});
 life.on(document,'click',event=>{const button=event.target.closest('[data-video-thumbnail-load]');if(!button)return;event.preventDefault();request(button.closest('[data-video-item]'),true)});
 life.on(document,'change',event=>{if(!event.target.matches('[data-auto-video-thumbnails]'))return;automatic=event.target.checked;window.OieRuntime.memory.set(preference,automatic);try{localStorage.setItem(preference,String(automatic))}catch{}preferences();if(automatic)for(const card of near)if(card.isConnected)request(card)});
 life.on(window,'storage',event=>{if(event.key!==preference)return;automatic=event.newValue!=='false';preferences();if(automatic)for(const card of near)if(card.isConnected)request(card)});
 life.cleanup(()=>{observer.disconnect();changes.disconnect();near.clear();active.clear()});preferences();observe();
});
