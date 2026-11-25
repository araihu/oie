window.OieRuntime.register('previews', life => {
  'use strict';
  const labels=document.querySelector('#oie-preview-copy')?.dataset || {};
  const viewer=document.querySelector('[data-image-viewer]');
  const dialog=document.querySelector('#oie-image-viewer');
  const csrf=document.querySelector('meta[name="oie-csrf"]')?.content || '';
  async function saveFile(path,name){
    if(window.OieDesktop?.saveFile)return window.OieDesktop.saveFile(path,name);
    const anchor=document.createElement('a');anchor.href=path;anchor.download=name;document.body.append(anchor);anchor.click();anchor.remove();return true;
  }
  const downloading=new Map(),refreshing=new Set();
  const preferenceKey='oie.device.auto-image-downloads.v1';
  let automatic=true;
  try { automatic=localStorage.getItem(preferenceKey)!=='false'; }
  catch { automatic=window.OieRuntime.memory.get(preferenceKey)!==false;preferenceWarning(); }
  function preferenceWarning() {
    const warning=document.querySelector('[data-device-preference-warning]');if(warning)warning.hidden=false;
  }
  function showPreference() {
    for(const control of document.querySelectorAll('[data-auto-image-downloads]'))control.checked=automatic;
  }
  showPreference();
  life.on(document,'change',event=>{
    if(!event.target.matches('[data-auto-image-downloads]'))return;
    automatic=event.target.checked;window.OieRuntime.memory.set(preferenceKey,automatic);
    try {localStorage.setItem(preferenceKey,String(automatic));}catch{preferenceWarning();}
    showPreference();scheduleAutomatic();
  });
  life.on(window,'storage',event=>{
    if(event.key!==preferenceKey)return;
    automatic=event.newValue!=='false';window.OieRuntime.memory.set(preferenceKey,automatic);showPreference();scheduleAutomatic();
  });
  const near=new Set(),observed=new Set(),attempted=new Set(),observers=new Map(),scopeRoots=new Map();
  let activeAutomatic=0,automaticFrame;
  life.cleanup(()=>{for(const observer of observers.values())observer.disconnect();near.clear();observed.clear();});
  function previewRoot(scope){
    if(scope.closest('#info-shared-results,[data-info-stars]'))return document.querySelector('#details-body');
    return scope.closest('#forward-message-dialog,#favorites-scroll,#shared-content-scroll,#message-timeline');
  }
  function observerFor(root){
    if(!root||typeof IntersectionObserver!=='function')return null;
    if(!observers.has(root))observers.set(root,new IntersectionObserver(entries=>{for(const entry of entries){if(entry.isIntersecting&&entry.target.isConnected)near.add(entry.target);else near.delete(entry.target);}scheduleAutomatic();},{root,rootMargin:'240px 0px',threshold:0.01}));
    return observers.get(root);
  }
  function trackImages(){
    for(const [root,observer] of observers)if(!root.isConnected){observer.disconnect();observers.delete(root);}
    for(const scope of observed)if(!scope.isConnected||scopeRoots.get(scope)!==previewRoot(scope)){observers.get(scopeRoots.get(scope))?.unobserve(scope);scopeRoots.delete(scope);observed.delete(scope);near.delete(scope);}
    for(const scope of document.querySelectorAll('#message-timeline [data-preview-kind="image"],#shared-content-scroll [data-preview-kind="image"],#info-shared-results [data-preview-kind="image"],[data-favorite-source] [data-preview-kind="image"],[data-forward-preview] [data-preview-kind="image"]')){
      if(!observed.has(scope)){const root=previewRoot(scope);observed.add(scope);scopeRoots.set(scope,root);observerFor(root)?.observe(scope);}
    }
    if(typeof IntersectionObserver!=='function')measureImages();scheduleAutomatic();
  }
  function measureImages(){
    for(const scope of observed){const root=scopeRoots.get(scope);if(!root)continue;const bounds=root.getBoundingClientRect(),box=scope.getBoundingClientRect();if(bounds.height>0&&box.height>0&&box.bottom>=bounds.top-240&&box.top<=bounds.bottom+240)near.add(scope);else near.delete(scope);}
  }
  function scheduleAutomatic(){if(life.signal.aborted||automaticFrame)return;automaticFrame=life.raf(()=>{automaticFrame=0;drainAutomatic();});}
  function drainAutomatic(){
    if(!automatic||document.body.dataset.mock==='true'||document.visibilityState==='hidden')return;
    for(const scope of near){if(activeAutomatic>=2)break;const id=scope.dataset.previewId;if(!scope.isConnected||!scope.getClientRects().length||scope.dataset.previewFile||scope.dataset.previewDownload!=='not_downloaded'||attempted.has(id)||downloading.has(id))continue;attempted.add(id);activeAutomatic++;download(scope).finally(()=>{activeAutomatic--;scheduleAutomatic();});}
  }
  if(typeof IntersectionObserver!=='function'){life.on(document,'scroll',()=>{measureImages();scheduleAutomatic();},{capture:true,passive:true});life.on(window,'resize',()=>{measureImages();scheduleAutomatic();});}
  life.on(document,'visibilitychange',scheduleAutomatic);
  life.raf(()=>{trackImages();for(const menu of document.querySelectorAll('[id^="save-preview-"]')){const scope=menu.closest('[data-preview-id]')||document.querySelector(`[data-preview-id="${CSS.escape(menu.id.slice(13))}"]`);menu.hidden=!scope?.dataset.previewFile;menu.disabled=!scope?.dataset.previewFile;}});
  const loadActions = '[id^="load-preview-"],[id^="shared-load-image-"]';
  function imageForLoadAction(action) {
    const shared = action.closest('[data-shared-actions]');
    const id = shared ? shared.dataset.sharedPreview : action.id.slice('load-preview-'.length);
    const container = shared ? shared.closest('[data-shared-item]') : action.closest('[data-message-id]');
    return container?.querySelector('[data-preview-kind="image"][data-preview-id="' + CSS.escape(id) + '"]');
  }
  function syncLoadAction(action, scope) {
    action.hidden = !scope || !!scope.dataset.previewImage && !state(scope)?.previewImageFailed;
    action.disabled = action.hidden || downloading.has(scope.dataset.previewId) ||
      ['queued', 'downloading'].includes(scope.dataset.previewDownload);
  }
  function syncImageLoad(scope) {
    const shared = scope.closest('[data-shared-item]');
    const action = shared ? shared.querySelector('[id^="shared-load-image-"]') :
      scope.closest('[data-message-id]')?.querySelector('#load-preview-' + CSS.escape(scope.dataset.previewId));
    if (action) syncLoadAction(action, scope);
  }
  function syncLoadActions() {
    for (const action of document.querySelectorAll(loadActions)) syncLoadAction(action, imageForLoadAction(action));
  }
  life.raf(syncLoadActions);
  life.on(document, 'oie:image-preview-error', event => {
    const scope = event.target.closest('[data-preview-kind="image"]');
    if (scope) syncImageLoad(scope);
  });
  life.on(document,'oie:actions-refreshed',()=>{
    syncLoadActions();
    for(const save of document.querySelectorAll('[data-message-menu-slot] [id^="save-preview-"]')){
      const scope=document.querySelector('[data-preview-id="'+CSS.escape(save.id.slice(13))+'"]');
      save.hidden=save.disabled=!scope?.dataset.previewFile;
    }
  });
  let viewing='',menuOpener=false,viewerOrigin=null;
  const source=hash=>typeof hash==='string' && /^[0-9a-f]{64}$/.test(hash)?'/ui/media/'+hash:'';
  const contexts='#forward-message-preview,#info-shared-results,#shared-content-results,#favorites-results,[data-info-stars],#message-timeline';
  const contextOf=scope=>scope.closest(contexts);
  const findAll=id=>document.querySelectorAll(`[data-preview-id="${CSS.escape(id)}"]`);
  const find=id=>[...findAll(id)].find(scope=>contextOf(scope)===viewerOrigin)||findAll(id)[0];
  const state=element=>window.Alpine?.$data(element);
  const fields=['previewUrl','previewName','previewMime','previewSize','previewTitle','previewDescription','previewSite','previewImage','previewFavicon','previewHost','previewPath','previewFile','previewState','previewDownload','previewStatus','previewRevision'];
  function apply(scope,values) {
    const previousRevision=scope.dataset.previewRevision;
    const data=state(scope);
    for(const field of fields) if(Object.hasOwn(values,field)) {
      scope.dataset[field]=values[field];if(data)data[field]=values[field];
    }
    if(data)data.previewBusy=downloading.has(scope.dataset.previewId)||refreshing.has(scope.dataset.previewId);
    const feedback=scope.querySelector('[data-link-refresh-feedback]');
    if(feedback&&scope.dataset.previewState!=='queued'&&(scope.dataset.previewRefreshing==='true'||scope.dataset.previewState==='ready'&&scope.dataset.previewRevision!==previousRevision)){feedback.textContent=scope.dataset.previewState==='unavailable'?(labels.refreshFailed||''):'';scope.dataset.previewRefreshing='false'}
    const refresh=scope.querySelector('[id^="refresh-preview-"]');if(refresh)refresh.disabled=scope.dataset.previewState==='queued'||refreshing.has(scope.dataset.previewId);
    const save=scope.querySelector('[id^="save-preview-"]')||document.getElementById('save-preview-'+scope.dataset.previewId);
    if(save){save.disabled=!scope.dataset.previewFile;save.hidden=!scope.dataset.previewFile;}
    syncImageLoad(scope);
    if(viewing===scope.dataset.previewId&&contextOf(scope)===viewerOrigin)updateViewer(scope);
  }
  function updateViewer(scope) {
    if(!viewer)return;const data=state(viewer);if(!data)return;
    const file=scope.dataset.previewFile;
    const next=file?file+'?original=1':'';
    if(data.viewerSrc!==next){data.viewerFailed=false;data.viewerSrc=next;}
    data.viewerLabel=scope.dataset.previewTitle || labels.image || '';
    data.viewerStatus=next?'':scope.dataset.previewStatus || labels.notDownloaded || '';
    viewer.dataset.viewerMessageId=scope.dataset.previewMessage || '';
    document.dispatchEvent(new CustomEvent('oie:viewer-source',{detail:{messageId:viewer.dataset.viewerMessageId}}));
    const identity=viewer.querySelector('[data-viewer-identity]');
    const sourceId=(contextOf(scope)?.id||'details')+':'+viewer.dataset.viewerMessageId;
    if(identity&&identity.dataset.sourceId!==sourceId){
      const row=document.querySelector(`[data-message-id="${CSS.escape(viewer.dataset.viewerMessageId)}"]`);
      const template=(scope.closest('[data-shared-item],[data-favorite-source],[data-forward-preview]')||row)?.querySelector('[data-viewer-source]');
      if(template){
        const replace=()=>{for(const child of identity.children)window.Alpine?.destroyTree(child);identity.replaceChildren(template.content.cloneNode(true));for(const child of identity.children)window.Alpine?.initTree(child);};
        if(window.Alpine?.mutateDom)window.Alpine.mutateDom(replace);else replace();
        identity.dataset.sourceId=sourceId;
        window.OieTime.localize(identity);
      }
    }
    data.viewerUrl=scope.dataset.previewUrl || '';
    data.viewerFilename=scope.dataset.previewName || '';
    data.viewerMime=scope.dataset.previewMime || '';
    const size=Number(scope.dataset.previewSize);
    data.viewerSize=scope.dataset.previewSize && Number.isFinite(size)?new Intl.NumberFormat(document.documentElement.lang,{style:'unit',unit:'byte',unitDisplay:'long'}).format(size):'';
  }
  function expand(scope,fromMenu=false) {
    if(!dialog||!viewer)return;
    viewing=scope.dataset.previewId;menuOpener=fromMenu;viewerOrigin=contextOf(scope);state(viewer).viewerActive=true;updateViewer(scope);
    window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'oie-image-viewer'}}));
  }
  // A disabled focused action can leave focus on body while this native modal
  // remains open. Consume that Escape before the underlying Drawer's window
  // listener; an inner popover already handles and stops its own Escape.
  life.on(document,'keydown',event=>{
    if(event.key!=='Escape'||!dialog?.open)return;
    event.preventDefault();event.stopPropagation();dialog.close();
  });
  life.on(dialog,'close',()=>{
    const previous=viewing,origin=viewerOrigin,message=viewer.dataset.viewerMessageId;viewing='';
    if(state(viewer))state(viewer).viewerActive=false;
    // A new message may replace the timeline while the dialog is open. Restore
    // focus to the equivalent surviving action instead of a detached opener.
    life.timeout(()=>{
      if (dialog.open) return;
      const scope=[...findAll(previous)].find(item=>contextOf(item)===origin);
      const target=scope&&(menuOpener?(scope.querySelector('[data-image-menu-trigger]')||scope.closest('[data-message-id]')?.querySelector('[data-message-menu-trigger]')):[...scope.querySelectorAll('[data-preview-expand],[data-preview-download]')].find(el=>el.getClientRects().length));
      const row=origin?.querySelector('[data-message-id="' + CSS.escape(message) + '"]');
      (target||row)?.focus({preventScroll:true});
    },0);
  });
  function synchronize(root) {
    if(!root||life.signal.aborted)return;
    for(const fresh of root.querySelectorAll('#live-timeline [data-message-presentation]')) {
      const id=fresh.closest('[data-message-id]')?.dataset.messageId;
      const current=id&&document.querySelector(`[data-message-id="${CSS.escape(id)}"] [data-message-presentation]`);
      // No provider markup is inserted: this is our safely rendered BFF fragment.
      // Keep a focused source link intact until a later refresh after blur.
      if(current&&fresh.dataset.presentationKnown==='true'&&current.dataset.presentationVersion!==fresh.dataset.presentationVersion&&!current.contains(document.activeElement)){
        current.innerHTML=fresh.innerHTML;current.dataset.presentationVersion=fresh.dataset.presentationVersion;current.dataset.presentationKnown='true';
      }
    }
    const timeline = document.getElementById('message-timeline');
    const liveTimeline = root.id === 'live-timeline' ? root : root.querySelector('#live-timeline');
    if (liveTimeline && timeline) {
      // A preview can disappear without changing the message text or revision.
      // Only remove an image after a successful preview read for this row.
      for (const current of timeline.querySelectorAll('[data-message-previews] > template[data-viewer-source]')) {
        const container = current.parentElement;
        const id = container.dataset.messagePreviews;
        const freshRow = liveTimeline.querySelector('[data-message-id="' + CSS.escape(id) + '"]');
        if (freshRow?.querySelector('[data-message-presentation]')?.dataset.presentationKnown !== 'true') continue;
        if (!freshRow.querySelector('template[data-viewer-source]')) current.remove();
        for (const image of container.querySelectorAll('[data-preview-kind="image"]')) {
          const selector = '[data-preview-id="' + CSS.escape(image.dataset.previewId) + '"][data-preview-kind="image"]';
          if (!freshRow.querySelector(selector)) {
            window.Alpine?.destroyTree(image);
            image.remove();
          }
        }
      }
      if (dialog?.open && viewerOrigin === timeline) {
        const id = viewer.dataset.viewerMessageId;
        const freshRow = liveTimeline.querySelector('[data-message-id="' + CSS.escape(id) + '"]');
        const withdrawn = freshRow?.querySelector('[data-message-retracted],[data-message-moderated]');
        const known = freshRow?.querySelector('[data-message-presentation]')?.dataset.presentationKnown === 'true';
        if (withdrawn || (known && !freshRow.querySelector('[data-preview-id="' + CSS.escape(viewing) + '"][data-preview-kind="image"]'))) {
          dialog.close();
        }
      }
    }
    const openMenu=document.querySelector('[data-menu-url] [data-message-menu-trigger][aria-expanded="true"]')?.closest('[data-menu-url]');
    if(openMenu && root.querySelector('#live-timeline [data-message-menu-slot="'+CSS.escape(openMenu.dataset.messageMenuSlot)+'"]')){
      openMenu.dispatchEvent(new CustomEvent('oie:message-menu-updated',{bubbles:true}));
    }
    for(const fresh of root.querySelectorAll('#live-timeline [data-preview-id],#live-shared-results [data-preview-id],#info-shared-results [data-preview-id]')) {
      const existing=findAll(fresh.dataset.previewId);
      const message=fresh.closest('#live-timeline [data-message-previews]')?.dataset.messagePreviews;
      const container=message&&document.querySelector(`#message-timeline [data-message-previews="${CSS.escape(message)}"]`);
      if(container&&![...existing].some(scope=>container.contains(scope))) {
        const current=fresh.cloneNode(true);
        const insert=()=>{
          if (fresh.dataset.previewKind === 'image' && !container.querySelector('template[data-viewer-source]')) {
            const source = fresh.parentElement.querySelector('template[data-viewer-source]');
            if (source) container.prepend(source.cloneNode(true));
          }
          container.append(current);
          window.Alpine?.initTree(current);
        };
        if(window.Alpine?.mutateDom)window.Alpine.mutateDom(insert);else insert();
      }
      for(const scope of findAll(fresh.dataset.previewId))apply(scope,fresh.dataset);
    }
    document.dispatchEvent(new CustomEvent('oie:actions-refreshed'));
  }
  life.on(document,'oie:previews-refresh',event=>{synchronize(event.detail);trackImages();});
  life.on(document,'oie:forward-content',trackImages);
  life.on(document,'oie:info-content',event=>{synchronize(event.detail.root);trackImages();});
  life.on(document,'oie:starred-content',event=>{
    const {current,fresh}=event.detail;
    const rows=new Map([...current.querySelectorAll('[data-favorite-source]')].map(row=>[row.dataset.infoStarMessage,row]));
    const forward=document.querySelector('#forward-message-dialog[open] [data-forward-preview]');
    for(const next of fresh.querySelectorAll('[data-favorite-source]')){
      const row=rows.get(next.dataset.infoStarMessage);
      const refreshRow=row&&row.dataset.sourceRevision===next.dataset.sourceRevision&&row.dataset.version!==next.dataset.version;
      const refreshForward=forward?.dataset.sourceRevision===next.dataset.sourceRevision;
      if(!refreshRow&&!refreshForward)continue;
      for(const preview of next.querySelectorAll('[data-preview-id]')){
        const selector=`[data-preview-id="${CSS.escape(preview.dataset.previewId)}"]`;
        const existing=refreshRow&&row.querySelector(selector);
        if(existing)apply(existing,preview.dataset);
        const forwarded=refreshForward&&forward.querySelector(selector);
        if(forwarded)apply(forwarded,preview.dataset);
      }
    }
    trackImages();
    const scope=viewing&&find(viewing);
    if(scope&&contextOf(scope)===viewerOrigin)updateViewer(scope);
  });
  // Catalog Save shares the image ensure pipeline, including queued originals.
  // Targeted status reads also cover items beyond the refreshed gallery page.
  const saving=new Set();
  const pause=ms=>new Promise(resolve=>{let timer;const done=()=>{clearTimeout(timer);life.signal.removeEventListener('abort',done);resolve();};timer=life.timeout(done,ms);life.signal.addEventListener('abort',done,{once:true});});
  life.on(document,'oie:preview-save-request',async event=>{
    const request=event.detail||{},id=request.id;
    const matches=p=>p&&p.id===id&&p.kind==='image'&&p.accountId===request.accountId&&p.conversationId===request.conversationId&&p.messageId===request.messageId&&p.url===request.url;
    const active=()=>!life.signal.aborted&&request.engineId===document.body.dataset.engineId&&request.alive?.();
    const notify=status=>{if(active())request.onState?.(status);};
    const scope=findAll(id||'')[0];
    if(!active())return;
    if(!scope||scope.dataset.previewKind!=='image'||scope.dataset.previewMessage!==request.messageId||scope.dataset.previewUrl!==request.url){notify('failed');return;}
    if(saving.has(id)){request.onState?.('cancelled');return;}saving.add(id);notify('loading');
    const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),60000);
    try{
      if(!await download(scope))throw Error('ensure_failed');
      let delay=300;
      while(active()&&!controller.signal.aborted){
        const current=findAll(id)[0];if(!current)throw Error('preview_missing');
        const file=current.dataset.previewFile;
        if(/^\/ui\/media\/[0-9a-f]{64}$/.test(file||'')){
          const name=current.dataset.previewName||'image';
          const saved=await saveFile(file+'?download='+encodeURIComponent(name),name);notify(saved?'saved':'cancelled');return;
        }
        if(current.dataset.previewDownload==='failed')throw Error('download_failed');
        const response=await life.fetch('/ui/api/v1/previews/'+encodeURIComponent(id),{credentials:'same-origin',cache:'no-store',headers:{'X-Oie-Engine-ID':request.engineId},signal:controller.signal});
        const preview=await response.json();if(!active())return;
        if(!response.ok||!matches(preview))throw Error('invalid_preview');
        for(const matching of findAll(id))apply(matching,{previewFile:preview.downloadState==='ready'?source(preview.fileHash):'',previewImage:source(preview.imageHash),previewDownload:preview.downloadState,previewRevision:preview.revisionId});
        if(preview.downloadState==='failed')throw Error('download_failed');
        if(preview.downloadState==='ready'&&source(preview.fileHash))continue;
        await pause(delay);delay=Math.min(2000,Math.round(delay*1.5));
      }
      if(active())throw Error('download_timeout');
    }catch{notify('failed');}finally{clearTimeout(timeout);saving.delete(id);if(!active())request.onState?.('cancelled');}
  });
  life.on(document,'oie:preview-refresh-request',event=>{const scope=findAll(event.detail?.id||'')[0];if(scope?.dataset.previewKind==='link')refreshLink(scope);});
  const scopedOriginals=new Set();
  function registerOriginal(scope){
    const id=scope.dataset.previewId,revision=scope.dataset.previewRevision;
    const key=id+':'+revision;
    if(scopedOriginals.has(key))return;
    scopedOriginals.add(key);
    const body=new URLSearchParams({previewId:id,engineId:document.body.dataset.engineId,csrf,lang:document.documentElement.lang});
    life.fetch('/ui/previews/download',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json'},body})
      .then(response=>{if(!response.ok)scopedOriginals.delete(key);return response.body?.cancel();})
      .catch(()=>scopedOriginals.delete(key));
  }
  function download(scope) {
    const id=scope.dataset.previewId;
    if(downloading.has(id))return downloading.get(id);
    const pending=requestDownload(scope).finally(()=>{
      downloading.delete(id);
      if(!life.signal.aborted)for(const matching of findAll(id)){
        if(state(matching))state(matching).previewBusy=false;
        syncImageLoad(matching);
      }
    });
    downloading.set(id,pending);
    for(const matching of findAll(id)){
      if(state(matching))state(matching).previewBusy=true;
      syncImageLoad(matching);
    }
    return pending;
  }
  async function requestDownload(scope) {
    const id=scope.dataset.previewId;
    const feedback=scope.querySelector('[data-preview-feedback]');if(feedback)feedback.textContent=labels.loading || '';
    const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),12000);
    let failure=labels.failed || '';
    try {
      const values=new URLSearchParams({csrf,engineId:document.body.dataset.engineId,previewId:id,returnTo:location.pathname+location.search});
      const response=await life.fetch('/ui/previews/download',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json'},body:values,signal:controller.signal});
      const result=await response.json();
      if(life.signal.aborted)return;
      if(!response.ok||!result.ok){failure=result.message || failure;throw new Error('preview_rejected');}
      const p=result.preview;
      if(p&&p.id===id)for(const matching of findAll(id))apply(matching,{previewUrl:p.url||'',previewName:p.title||scope.dataset.previewName||'',previewMime:p.fileMimeType||'',previewSize:p.fileSize===undefined?'':String(p.fileSize),previewTitle:p.title||'',previewDescription:p.description||'',previewSite:p.siteName||'',previewImage:source(p.imageHash),previewFile:p.downloadState==='ready'?source(p.fileHash):'',previewState:p.state,previewDownload:p.downloadState,previewRevision:p.revisionId,previewStatus:result.message||''});
      if(feedback)feedback.textContent='';
      return true;
    } catch(error) {
      if(life.signal.aborted)return;
      const current=find(id)||scope,feedback=current.querySelector('[data-preview-feedback]');
      const message=failure;
      if(feedback)feedback.textContent=message || '';
      if(viewing===id&&state(viewer))state(viewer).viewerStatus=message || '';
      return false;
    } finally {
      clearTimeout(timeout);
    }
  }
  async function refreshLink(scope){
    const id=scope.dataset.previewId;if(refreshing.has(id)||scope.dataset.previewState==='queued')return;
    refreshing.add(id);scope.dataset.previewRefreshing='true';const feedback=scope.querySelector('[data-link-refresh-feedback]');
    if(feedback)feedback.textContent=labels.refreshLoading||'';
    const button=scope.querySelector('[id^="refresh-preview-"]');if(button)button.disabled=true;
    try{
      const values=new URLSearchParams({engineId:document.body.dataset.engineId,previewId:id});
      const response=await life.fetch('/ui/previews/refresh',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json','X-CSRF-Token':csrf},body:values});
      const result=await response.json();if(life.signal.aborted)return;
      if(!response.ok||!result.ok||result.preview?.id!==id)throw Error('refresh_failed');
      const p=result.preview;
      for(const matching of findAll(id))apply(matching,{previewTitle:p.title||'',previewDescription:p.description||'',previewSite:p.siteName||'',previewImage:source(p.imageHash),previewFavicon:source(p.faviconHash),previewState:p.state,previewRevision:p.revisionId,previewStatus:result.message||''});
      if(feedback)feedback.textContent=result.message||'';document.dispatchEvent(new CustomEvent('oie:refresh-request'));
    }catch{scope.dataset.previewRefreshing='false';if(!life.signal.aborted&&feedback)feedback.textContent=labels.refreshFailed||'';}
    finally{refreshing.delete(id);if(button)button.disabled=scope.dataset.previewState==='queued';if(!life.signal.aborted)document.dispatchEvent(new CustomEvent('oie:actions-refreshed'));}
  }
  life.on(document,'click',event=>{const button=event.target.closest('[id^="refresh-preview-"]');if(!button)return;const scope=button.closest('[data-preview-id]');if(!scope||scope.dataset.previewKind!=='link')return;event.preventDefault();refreshLink(scope)});
  life.on(document,'click',async event=>{
    const load = event.target.closest(loadActions);
    if (load) {
      const scope = imageForLoadAction(load);
      if (scope && !load.disabled) await download(scope);
      return;
    }
    const menu=event.target.closest('[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"]');
    const button=menu||event.target.closest('button[data-preview-download],button[data-preview-expand]');if(!button)return;
    const id=menu?menu.id.slice(menu.id.indexOf('-preview-')+9):'';
    const scope=button.closest('[data-preview-id]')||(menu&&find(id));if(!scope||scope.dataset.previewKind!=='image')return;
    event.preventDefault();
    if(menu?.id.startsWith('save-preview-')){
      if(!await download(scope))return;
      const path=scope.dataset.previewFile;if(!/^\/ui\/media\/[0-9a-f]{64}$/.test(path||''))return;
      const name=scope.dataset.previewName||'image';
      try{await saveFile(path+'?download='+encodeURIComponent(name),name);}catch{if(!life.signal.aborted)window.dispatchEvent(new CustomEvent('notify',{detail:{kind:'toast',tone:'danger',message:labels.failed||''}}));}return;
    }
    if(menu?.id.startsWith('info-preview-')){
      if(scope.dataset.previewFile)registerOriginal(scope);
      expand(scope,true);life.timeout(()=>{viewer.querySelector('[data-viewer-info]')?.click();},40);return;
    }
    if(menu||button.hasAttribute('data-preview-expand')){
      expand(scope,!!menu);
      if(scope.dataset.previewFile||['queued','downloading'].includes(scope.dataset.previewDownload))registerOriginal(scope);
      else download(scope);
    }else download(scope);
  });
});
