(() => {
  'use strict';
  const labels=document.querySelector('#oie-preview-copy')?.dataset || {};
  const viewer=document.querySelector('[data-image-viewer]');
  const dialog=document.querySelector('#oie-image-viewer');
  const csrf=document.querySelector('meta[name="oie-csrf"]')?.content || '';
  const downloading=new Set();
  let viewing='';
  const source=hash=>typeof hash==='string' && /^[0-9a-f]{64}$/.test(hash)?'/ui/media/'+hash:'';
  const find=id=>document.querySelector(`[data-preview-id="${CSS.escape(id)}"]`);
  const state=element=>window.Alpine?.$data(element);
  const fields=['previewTitle','previewDescription','previewSite','previewImage','previewFile','previewState','previewDownload','previewStatus','previewRevision'];
  function apply(scope,values) {
    const data=state(scope);
    for(const field of fields) if(Object.hasOwn(values,field)) {
      scope.dataset[field]=values[field];if(data)data[field]=values[field];
    }
    if(data)data.previewBusy=downloading.has(scope.dataset.previewId);
    if(viewing===scope.dataset.previewId)updateViewer(scope);
  }
  function updateViewer(scope) {
    if(!viewer)return;const data=state(viewer);if(!data)return;
    const next=scope.dataset.previewFile || '';
    if(data.viewerSrc!==next){data.viewerFailed=false;data.viewerSrc=next;}
    data.viewerLabel=scope.dataset.previewTitle || labels.image || '';
    data.viewerStatus=next?'':scope.dataset.previewStatus || labels.loading || '';
  }
  function expand(scope) {
    if(!dialog||!viewer)return;
    viewing=scope.dataset.previewId;updateViewer(scope);
    window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'oie-image-viewer'}}));
  }
  dialog?.addEventListener('close',()=>{
    const previous=viewing;viewing='';
    // A new message may replace the timeline while the dialog is open. Restore
    // focus to the equivalent surviving action instead of a detached opener.
    setTimeout(()=>{const scope=find(previous);const target=scope&&[...scope.querySelectorAll('[data-preview-expand],[data-preview-download]')].find(el=>el.getClientRects().length);target?.focus({preventScroll:true});},0);
  });
  function synchronize(root) {
    const timeline=document.querySelector('#message-timeline');
    const follow=timeline&&timeline.scrollHeight-timeline.scrollTop-timeline.clientHeight<60;
    const anchor=timeline&&[...timeline.querySelectorAll('[data-message-id]')].find(el=>el.getBoundingClientRect().bottom>=timeline.getBoundingClientRect().top);
    const anchorTop=anchor?.getBoundingClientRect().top;
    for(const fresh of root.querySelectorAll('#live-timeline [data-preview-id]')) {
      let current=find(fresh.dataset.previewId);
      if(!current) {
        const message=fresh.closest('[data-message-previews]')?.dataset.messagePreviews;
        const container=message&&document.querySelector(`[data-message-previews="${CSS.escape(message)}"]`);
        if(!container)continue;
        current=fresh.cloneNode(true);
        const insert=()=>{container.append(current);window.Alpine?.initTree(current);};
        if(window.Alpine?.mutateDom)window.Alpine.mutateDom(insert);else insert();
      }
      apply(current,fresh.dataset);
    }
    // Alpine applies text/visibility changes in its next microtask. Preserve the
    // visible message after those updates, including newly appended link cards.
    requestAnimationFrame(()=>{
      if(!timeline)return;
      if(follow)timeline.scrollTop=timeline.scrollHeight;
      else if(anchor?.isConnected)timeline.scrollTop+=anchor.getBoundingClientRect().top-anchorTop;
    });
  }
  document.addEventListener('oie:previews-refresh',event=>synchronize(event.detail));
  async function download(scope) {
    const id=scope.dataset.previewId;if(downloading.has(id))return;
    downloading.add(id);if(state(scope))state(scope).previewBusy=true;
    const feedback=scope.querySelector('[data-preview-feedback]');if(feedback)feedback.textContent=labels.loading || '';
    const controller=new AbortController(),timeout=setTimeout(()=>controller.abort(),12000);
    let failure=labels.failed || '';
    try {
      const values=new URLSearchParams({csrf,engineId:document.body.dataset.engineId,previewId:id,returnTo:location.pathname+location.search});
      const response=await fetch('/ui/previews/download',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json'},body:values,signal:controller.signal});
      const result=await response.json();
      if(!response.ok||!result.ok){failure=result.message || failure;throw new Error('preview_rejected');}
      const p=result.preview;
      if(p&&p.id===id)apply(find(id)||scope,{previewTitle:p.title||'',previewDescription:p.description||'',previewSite:p.siteName||'',previewImage:source(p.imageHash),previewFile:p.downloadState==='ready'?source(p.fileHash):'',previewState:p.state,previewDownload:p.downloadState,previewRevision:p.revisionId,previewStatus:result.message||''});
      if(feedback)feedback.textContent='';
    } catch(error) {
      const current=find(id)||scope,feedback=current.querySelector('[data-preview-feedback]');
      const message=failure;
      if(feedback)feedback.textContent=message || '';
      if(viewing===id&&state(viewer))state(viewer).viewerStatus=message || '';
    } finally {
      clearTimeout(timeout);downloading.delete(id);const current=find(id);if(current&&state(current))state(current).previewBusy=false;
    }
  }
  document.addEventListener('click',event=>{
    const button=event.target.closest('[data-preview-download],[data-preview-expand]');if(!button)return;
    const scope=button.closest('[data-preview-id]');if(!scope||scope.dataset.previewKind!=='image')return;
    event.preventDefault();
    if(button.hasAttribute('data-preview-expand')) {
      expand(scope);if(!scope.dataset.previewFile)download(scope);
    } else download(scope);
  });
})();
