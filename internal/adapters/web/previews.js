window.OieRuntime.register('previews', life => {
  'use strict';
  const labels=document.querySelector('#oie-preview-copy')?.dataset || {};
  const viewer=document.querySelector('[data-image-viewer]');
  const dialog=document.querySelector('#oie-image-viewer');
  const csrf=document.querySelector('meta[name="oie-csrf"]')?.content || '';
  const downloading=new Set(),refreshing=new Set();
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
  function previewRoot(scope){return scope.closest('#info-shared-results')?document.querySelector('#details-body'):scope.closest('#shared-content-scroll')||scope.closest('#message-timeline');}
  function observerFor(root){
    if(!root||typeof IntersectionObserver!=='function')return null;
    if(!observers.has(root))observers.set(root,new IntersectionObserver(entries=>{for(const entry of entries){if(entry.isIntersecting)near.add(entry.target);else near.delete(entry.target);}scheduleAutomatic();},{root,rootMargin:'240px 0px',threshold:0.01}));
    return observers.get(root);
  }
  function trackImages(){
    for(const scope of observed)if(!scope.isConnected){observers.get(scopeRoots.get(scope))?.unobserve(scope);scopeRoots.delete(scope);observed.delete(scope);near.delete(scope);}
    for(const scope of document.querySelectorAll('#message-timeline [data-preview-kind="image"],#shared-content-scroll [data-preview-kind="image"],#info-shared-results [data-preview-kind="image"]')){
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
  life.raf(()=>{trackImages();for(const menu of document.querySelectorAll('[id^="save-preview-"]')){const scope=document.querySelector(`[data-preview-id="${CSS.escape(menu.id.slice(13))}"]`);menu.hidden=!scope?.dataset.previewFile;menu.disabled=!scope?.dataset.previewFile;}});
  let viewing='',menuOpener=false,viewerOrigin='timeline';
  const source=hash=>typeof hash==='string' && /^[0-9a-f]{64}$/.test(hash)?'/ui/media/'+hash:'';
  const contextOf=scope=>scope.closest('#info-shared-results')?'info':scope.closest('#shared-content-results')?'shared':'timeline';
  const findAll=id=>document.querySelectorAll(`[data-preview-id="${CSS.escape(id)}"]`);
  const find=id=>{const selector=viewerOrigin==='info'?'#info-shared-results':viewerOrigin==='shared'?'#shared-content-results':'#message-timeline';return document.querySelector(`${selector} [data-preview-id="${CSS.escape(id)}"]`)||document.querySelector(`[data-preview-id="${CSS.escape(id)}"]`);};
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
    const save=document.getElementById('save-preview-'+scope.dataset.previewId);if(save){save.disabled=!scope.dataset.previewFile;save.hidden=!scope.dataset.previewFile;}
    if(viewing===scope.dataset.previewId&&contextOf(scope)===viewerOrigin)updateViewer(scope);
  }
  function updateViewer(scope) {
    if(!viewer)return;const data=state(viewer);if(!data)return;
    const next=scope.dataset.previewFile || '';
    if(data.viewerSrc!==next){data.viewerFailed=false;data.viewerSrc=next;}
    data.viewerLabel=scope.dataset.previewTitle || labels.image || '';
    data.viewerStatus=next?'':scope.dataset.previewStatus || labels.notDownloaded || '';
    viewer.dataset.viewerMessageId=scope.dataset.previewMessage || '';
    document.dispatchEvent(new CustomEvent('oie:viewer-source',{detail:{messageId:viewer.dataset.viewerMessageId}}));
    const identity=viewer.querySelector('[data-viewer-identity]');
    if(identity&&identity.dataset.sourceId!==viewer.dataset.viewerMessageId){
      const row=document.querySelector(`[data-message-id="${CSS.escape(viewer.dataset.viewerMessageId)}"]`);
      const template=(scope.closest('[data-shared-item]')||row)?.querySelector('[data-viewer-source]');
      if(template){
        const replace=()=>{for(const child of identity.children)window.Alpine?.destroyTree(child);identity.replaceChildren(template.content.cloneNode(true));for(const child of identity.children)window.Alpine?.initTree(child);};
        if(window.Alpine?.mutateDom)window.Alpine.mutateDom(replace);else replace();
        identity.dataset.sourceId=viewer.dataset.viewerMessageId;
        const time=identity.querySelector('time');if(time)time.textContent=new Intl.DateTimeFormat(document.documentElement.lang,{dateStyle:'medium',timeStyle:'short'}).format(new Date(time.dateTime));
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
    const previous=viewing;viewing='';
    if(state(viewer))state(viewer).viewerActive=false;
    // A new message may replace the timeline while the dialog is open. Restore
    // focus to the equivalent surviving action instead of a detached opener.
    life.timeout(()=>{const scope=find(previous);const target=scope&&(menuOpener?(scope.querySelector('[data-image-menu-trigger]')||scope.closest('[data-message-id]')?.querySelector('[data-message-menu-trigger]')):[...scope.querySelectorAll('[data-preview-expand],[data-preview-download]')].find(el=>el.getClientRects().length));target?.focus({preventScroll:true});},0);
  });
  function synchronize(root) {
    if(!root||life.signal.aborted)return;
    const timeline=document.querySelector('#message-timeline');
    const follow=timeline&&!document.body.dataset.focusMessage&&timeline.scrollHeight-timeline.scrollTop-timeline.clientHeight<60;
    const anchor=timeline&&[...timeline.querySelectorAll('[data-message-id]')].find(el=>el.getBoundingClientRect().bottom>=timeline.getBoundingClientRect().top);
    const anchorTop=anchor?.getBoundingClientRect().top;
    for(const fresh of root.querySelectorAll('#live-timeline [data-message-presentation]')) {
      const id=fresh.closest('[data-message-id]')?.dataset.messageId;
      const current=id&&document.querySelector(`[data-message-id="${CSS.escape(id)}"] [data-message-presentation]`);
      // No provider markup is inserted: this is our safely rendered BFF fragment.
      // Keep a focused source link intact until a later refresh after blur.
      if(current&&fresh.dataset.presentationKnown==='true'&&current.dataset.presentationVersion!==fresh.dataset.presentationVersion&&!current.contains(document.activeElement)){
        current.innerHTML=fresh.innerHTML;current.dataset.presentationVersion=fresh.dataset.presentationVersion;current.dataset.presentationKnown='true';
      }
    }
    for(const fresh of root.querySelectorAll('#live-timeline [data-message-menu-slot]')) {
      const id=fresh.dataset.messageMenuSlot;
      const current=document.querySelector(`[data-message-menu-slot="${CSS.escape(id)}"]`);
      const panel=current?.querySelector('[role="menu"]'),next=fresh.querySelector('[role="menu"]');
      if(panel&&next&&!panel.contains(document.activeElement)&&panel.innerHTML!==next.innerHTML){
        const replace=()=>{for(const child of panel.children)window.Alpine?.destroyTree(child);panel.innerHTML=next.innerHTML;for(const child of panel.children)window.Alpine?.initTree(child);};
        if(window.Alpine?.mutateDom)window.Alpine.mutateDom(replace);else replace();
      }
    }
    for(const fresh of root.querySelectorAll('#live-timeline [data-preview-id],#live-shared-results [data-preview-id],#info-shared-results [data-preview-id]')) {
      let current=find(fresh.dataset.previewId);
      if(!current) {
        const message=fresh.closest('[data-message-previews]')?.dataset.messagePreviews;
        const container=message&&document.querySelector(`[data-message-previews="${CSS.escape(message)}"]`);
        if(!container)continue;
        current=fresh.cloneNode(true);
        const insert=()=>{container.append(current);window.Alpine?.initTree(current);};
        if(window.Alpine?.mutateDom)window.Alpine.mutateDom(insert);else insert();
      }
      for(const scope of findAll(fresh.dataset.previewId))apply(scope,fresh.dataset);
    }
    document.dispatchEvent(new CustomEvent('oie:actions-refreshed'));
    // Alpine applies text/visibility changes in its next microtask. Preserve the
    // visible message after those updates, including newly appended link cards.
    life.raf(()=>{
      if(!timeline)return;
      if(follow)timeline.scrollTop=timeline.scrollHeight;
      else if(anchor?.isConnected)timeline.scrollTop+=anchor.getBoundingClientRect().top-anchorTop;
    });
  }
  life.on(document,'oie:previews-refresh',event=>{synchronize(event.detail);trackImages();});
  life.on(document,'oie:info-content',event=>{synchronize(event.detail.root);trackImages();});
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
      if(!scope.dataset.previewFile&&!['queued','downloading'].includes(scope.dataset.previewDownload)&&!await download(scope))throw Error('ensure_failed');
      let delay=300;
      while(active()&&!controller.signal.aborted){
        const current=findAll(id)[0];if(!current)throw Error('preview_missing');
        const file=current.dataset.previewFile;
        if(/^\/ui\/media\/[0-9a-f]{64}$/.test(file||'')){
          saving.delete(id);
          const anchor=document.createElement('a');anchor.href=file+'?download='+encodeURIComponent(current.dataset.previewName||'image');anchor.download=current.dataset.previewName||'image';document.body.append(anchor);anchor.click();anchor.remove();notify('saved');return;
        }
        if(current.dataset.previewDownload==='failed')throw Error('download_failed');
        const response=await life.fetch('/api/v1/previews/'+encodeURIComponent(id),{credentials:'same-origin',cache:'no-store',headers:{'X-Oie-Engine-ID':request.engineId},signal:controller.signal});
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
  async function download(scope) {
    const id=scope.dataset.previewId;if(downloading.has(id))return true;
    downloading.add(id);for(const matching of findAll(id))if(state(matching))state(matching).previewBusy=true;
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
      clearTimeout(timeout);if(life.signal.aborted)return;downloading.delete(id);for(const matching of findAll(id))if(state(matching))state(matching).previewBusy=false;
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
  life.on(document,'click',event=>{
    const menu=event.target.closest('[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"]');
    const button=menu||event.target.closest('button[data-preview-download],button[data-preview-expand]');if(!button)return;
    const id=menu?menu.id.slice(menu.id.indexOf('-preview-')+9):'';
    const scope=menu?find(id):button.closest('[data-preview-id]');if(!scope||scope.dataset.previewKind!=='image')return;
    event.preventDefault();
    if(menu?.id.startsWith('save-preview-')){
      const path=scope.dataset.previewFile;if(!/^\/ui\/media\/[0-9a-f]{64}$/.test(path||''))return;
      const link=document.createElement('a');link.href=path+'?download='+encodeURIComponent(scope.dataset.previewName||'image');link.download=scope.dataset.previewName||'image';document.body.append(link);link.click();link.remove();return;
    }
    if(menu?.id.startsWith('info-preview-')){
      expand(scope,true);life.timeout(()=>{viewer.querySelector('[data-viewer-info]')?.click();},40);return;
    }
    if(menu||button.hasAttribute('data-preview-expand')){
      expand(scope,!!menu);if(!scope.dataset.previewFile&&!['queued','downloading'].includes(scope.dataset.previewDownload))download(scope);
    }else download(scope);
  });
});
