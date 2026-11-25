window.OieRuntime.register('stars', life => {
  'use strict';
  const body=document.body,engine=body.dataset.engineId;
  const labels=document.querySelector('#oie-star-copy')?.dataset || {};
  const csrf=document.querySelector('meta[name="oie-csrf"]')?.content || '';
  const states=new Map(),busy=new Set();
  const memoryKey='star-journals:'+engine;if(!window.OieRuntime.memory.has(memoryKey))window.OieRuntime.memory.set(memoryKey,new Map());const journals=window.OieRuntime.memory.get(memoryKey);
  let generation=0,denied=false,viewerMessage='';body.dataset.starGeneration='0';
  function uuid(){const b=crypto.getRandomValues(new Uint8Array(16));let t=BigInt(Date.now());for(let i=5;i>=0;i--){b[i]=Number(t&255n);t>>=8n}b[6]=(b[6]&15)|112;b[8]=(b[8]&63)|128;const h=[...b].map(n=>n.toString(16).padStart(2,'0')).join('');return h.slice(0,8)+'-'+h.slice(8,12)+'-'+h.slice(12,16)+'-'+h.slice(16,20)+'-'+h.slice(20)}
  let tab=window.OieRuntime.memory.get('star-tab')||uuid();window.OieRuntime.memory.set('star-tab',tab);try{const saved=sessionStorage.getItem('oie.star.tab.v1');if(saved)tab=saved;else sessionStorage.setItem('oie.star.tab.v1',tab);}catch{denied=true;}
  window.OieRuntime.memory.set('star-tab',tab);
  const key=id=>['oie.star.operation.v1',engine,tab,id].join(':');
  function warning(){const note=document.querySelector('#oie-star-storage-warning');if(note)note.hidden=false;}
  function journal(id){
    if(journals.has(id))return journals.get(id);
    let value=null;try{value=JSON.parse(localStorage.getItem(key(id))||'null');}catch{denied=true;}
    if(value&&value.engine===engine&&value.messageId===id&&typeof value.desired==='boolean'&&typeof value.key==='string'&&(value.expected===null||typeof value.expected==='string'))journals.set(id,value);else value=null;
    return value;
  }
  function save(value){journals.set(value.messageId,value);try{localStorage.setItem(key(value.messageId),JSON.stringify(value));}catch{denied=true;}if(denied)warning();}
  function clear(id){journals.delete(id);try{localStorage.removeItem(key(id));}catch{denied=true;warning();}}
  function announce(message){const node=document.querySelector('#oie-star-status');if(node)node.textContent=message || '';}
  function render(){
    for(const row of document.querySelectorAll('[data-message-id][data-message-source]')){
      const value=states.get(row.dataset.messageId),starred=!!value?.known&&!!value.starred;row.dataset.starred=String(starred);
      const indicator=row.querySelector('[data-message-star-indicator]');if(indicator&&!indicator.matches('button[data-star-message]'))indicator.hidden=!starred;
    }
    for(const button of document.querySelectorAll('[data-star-message],[id^="star-message-"]')){
      if(button.id?.startsWith('star-message-'))button.dataset.starMessage=button.id.slice(13);
      const id=button.dataset.starMessage,shared=button.hasAttribute('data-shared-star');
      if(shared&&!states.has(id))states.set(id,sharedState(button));
      const current=states.get(id),pending=id&&journal(id),known=!!current?.known;
      button.hidden=!known;
      if(shared||button.closest('#favorites-results')){
        // Keep keyboard focus on the action while it awaits confirmation.
        button.disabled=!known;
        button.setAttribute('aria-disabled',String(!known||busy.has(id)));
      }else button.disabled=!known||busy.has(id);
      const label=pending?labels.retry:current?.starred?labels.unstar:labels.star;
      button.setAttribute('aria-label',label||'');button.title=label||'';
      if(button.id?.startsWith('star-message-')){button.setAttribute('role','menuitem');button.removeAttribute('aria-pressed');
        let text=button.querySelector('[data-star-label]');
        if(!text){text=document.createElement('span');text.dataset.starLabel='';const icon=button.querySelector('svg');button.replaceChildren(...(icon?[icon]:[]),text);}
        text.textContent=label||'';}else button.setAttribute('aria-pressed',known?String(current.starred):'mixed');
      button.dataset.starred=String(!!current?.starred);button.dataset.starPending=String(!!pending);
      const text=button.querySelector('[data-star-label]');if(text)text.textContent=label||'';
    }
  }
  const readTimes=new Map();
  function applySnapshot(id,state,startedAt){
    if(startedAt<(readTimes.get(id)||0))return;
    readTimes.set(id,startedAt);
    states.set(id,state);
  }
  function read(root,startedAt=performance.now()){
    const data=root.querySelector('#oie-star-data');if(!data)return;
    if(data.dataset.known!=='true'){for(const [id,value] of states)applySnapshot(id,{...value,known:false},startedAt);return;}
    for(const row of data.querySelectorAll('[data-star-state]'))applySnapshot(row.dataset.starSource,{known:true,starred:row.dataset.starred==='true',revision:row.dataset.revision||null},startedAt);
  }
  function sharedState(button){
    return {known:true,starred:button.dataset.sharedStar==='true',revision:button.dataset.revision||null};
  }
  function readShared(root,startedAt=performance.now()){
    for(const button of root.querySelectorAll('[data-shared-star]')){
      applySnapshot(button.dataset.starMessage,sharedState(button),startedAt);
    }
  }
  read(document);
  readShared(document);
  function readInfo(root,startedAt=performance.now()){
    if(!root)return;
    for(const row of root.querySelectorAll('[data-info-star-states][data-known="true"] [data-star-state]')){
      applySnapshot(row.dataset.starSource,{known:true,starred:row.dataset.starred==='true',revision:row.dataset.revision||null},startedAt);
    }
    render();
  }
  readInfo(document);

  window.OieMasonry?.layout(document.getElementById('favorites-results'));
  life.on(document,'oie:info-content',event=>{
    const {root, generation: requestedGeneration} = event.detail;
    if (root.id === 'favorites-results') window.OieMasonry?.layout(root);
    if(requestedGeneration===generation){readShared(root);readInfo(root);}
    else{render();document.dispatchEvent(new CustomEvent('oie:refresh-request'));}
  });
  life.on(document,'oie:shared-append',event=>{
    const {page,starGeneration}=event.detail;
    if(starGeneration===generation)readShared(page);
    render();
  });

  life.on(document,'oie:actions-refreshed',event=>{
    const state=event.detail?.menuState;
    if(state && event.detail.starGeneration===generation){
      applySnapshot(state.menuState,{known:state.starKnown==='true',starred:state.starred==='true',revision:state.revision||null},Number(state.startedAt));
    }
    render();
  });
  function refreshStarredPage(current,fresh,scroll,heading){
    if(!current||!fresh||current.dataset.version===fresh.dataset.version)return;
    const top=scroll?.scrollTop;
    const active=current.contains(document.activeElement)?document.activeElement:null;
    const message=active?.closest('[data-info-star-message]')?.dataset.infoStarMessage;
    const action=active?.matches('[data-star-message]')?'[data-star-message]':'[data-favorite-menu-trigger]';
    const visibleTop=scroll?.getBoundingClientRect().top;
    const anchors=scroll?[...current.querySelectorAll('[data-info-star-message]')]
      .filter(row=>row.getBoundingClientRect().bottom>visibleTop)
      .map(row=>[row.dataset.infoStarMessage,row.getBoundingClientRect().top]):[];
    const findRow=id=>current.querySelector(`[data-info-star-message="${CSS.escape(id)}"]`);
    const rows='[data-info-star-message]';
    const apply=()=>{
      let cursor=current.firstElementChild;
      for(const next of fresh.children){
        const id=next.dataset.infoStarMessage;
        let child=id?findRow(id):null;
        if(!child&&cursor&&!cursor.matches(rows)&&!next.matches(rows)){
          // The incoming pagination has no client-computed position.
          const top=cursor.style.getPropertyValue('--masonry-top');
          if(top)next.style.setProperty('--masonry-top',top);
          if(cursor.isEqualNode(next))child=cursor;
        }
        if(!child){
          child=next.cloneNode(true);
          current.insertBefore(child,cursor);
          window.Alpine?.initTree(child);
        }else{
          if(child!==cursor)current.insertBefore(child,cursor);
          if(id&&child.dataset.version!==next.dataset.version&&!child.querySelector('[role="menu"]:focus-within') && !child.querySelector('.oie-message-body :focus')){
            if(!window.OieVideos?.swap(child,next.innerHTML)){
              for(const element of child.children)window.Alpine?.destroyTree(element);
              child.innerHTML=next.innerHTML;
              for(const element of child.children)window.Alpine?.initTree(element);
            }
            for(const name of ['data-version','data-source-revision','data-message-source'])child.setAttribute(name,next.getAttribute(name));
          }
        }
        cursor=child.nextElementSibling;
      }
      while(cursor){
        const next=cursor.nextElementSibling;
        window.Alpine?.destroyTree(cursor);
        cursor.remove();
        cursor=next;
      }
      current.dataset.version=[...fresh.querySelectorAll(rows)].every(row=>findRow(row.dataset.infoStarMessage)?.dataset.version===row.dataset.version)?fresh.dataset.version:'';
    };
    if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();
    window.OieTime?.localize(current);
    if(current.id==='favorites-results')window.OieMasonry?.layout(current);
    if(active&&!active.isConnected){
      const row=message&&findRow(message);
      (row?.querySelector(action)||current.querySelector(rows+' '+action)||heading())?.focus({preventScroll:true});
    }
    if(scroll&&top!==undefined){
      const anchor=anchors.find(([id])=>findRow(id));
      scroll.scrollTop=anchor?top+findRow(anchor[0]).getBoundingClientRect().top-anchor[1]:top;
    }
    document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));
    document.dispatchEvent(new CustomEvent('oie:starred-content',{detail:{current,fresh}}));
  }
  life.on(document,'oie:stars-refresh',event=>{
    if(event.detail.generation!==generation){render();return;}
    const page=event.detail.page;
    const info=document.querySelector('[data-info-stars]');
    refreshStarredPage(info,page.querySelector('#live-info-content [data-info-stars]'),
      document.getElementById('details-body'),()=>info.querySelector('[data-info-heading]'));
    refreshStarredPage(document.getElementById('favorites-results'),page.querySelector('#favorites-results'),
      document.getElementById('favorites-scroll'),()=>document.getElementById('favorites-heading'));
    read(page,event.detail.startedAt);readShared(page,event.detail.startedAt);readInfo(page,event.detail.startedAt);
  });
  let viewerRead=false,viewerAgain=false;
  async function refreshViewer(){
    if(!viewerMessage)return;if(viewerRead){viewerAgain=true;return;}viewerRead=true;
    const id=viewerMessage,epoch=generation,controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),8000);
    try{const response=await life.fetch('/ui/message-star?messageId='+encodeURIComponent(id),{credentials:'same-origin',cache:'no-store',signal:controller.signal});const star=await response.json();if(life.signal.aborted)return;if(response.ok&&star.messageId===id&&epoch===generation)states.set(id,{known:true,starred:star.starred,revision:star.revisionId});}
    catch{if(!life.signal.aborted&&epoch===generation)states.set(id,{...states.get(id),known:false});}finally{clearTimeout(timeout);if(life.signal.aborted)return;viewerRead=false;render();if(viewerAgain){viewerAgain=false;refreshViewer();}}
  }
  life.on(document,'oie:star-event',event=>{
    // Durable events can arrive after a newer HTTP read. Their state is never
    // applied directly; invalidate earlier reads and fetch current state.
    generation++;body.dataset.starGeneration=String(generation);
    if(event.detail?.messageId===viewerMessage)refreshViewer();
    document.dispatchEvent(new CustomEvent('oie:refresh-request'));
  });
  life.on(document,'oie:viewer-source',event=>{
    const next=event.detail.messageId||'',changed=next!==viewerMessage;viewerMessage=next;
    for(const button of document.querySelectorAll('[data-image-viewer] [data-star-message]'))button.dataset.starMessage=viewerMessage;
    render();if(changed||!states.get(next)?.known)refreshViewer();
  });
  life.on(document,'click',async event=>{
    const button=event.target.closest('[data-star-message],[id^="star-message-"]');if(!button)return;
    const id=button.dataset.starMessage||button.id.slice(13),current=states.get(id);
    if(!id||!current?.known||busy.has(id)||body.dataset.mock==='true')return;
    event.preventDefault();
    const intent=journal(id)||{engine,messageId:id,key:uuid(),desired:!current.starred,expected:current.revision};
    save(intent);busy.add(id);generation++;const operationGeneration=generation;body.dataset.starGeneration=String(generation);render();
    const controller=new AbortController(),timeout=life.timeout(()=>controller.abort(),14000);
    try{
      const values=new URLSearchParams({csrf,engineId:engine,messageId:id,operationKey:intent.key,starred:String(intent.desired),expectedRevisionId:intent.expected||'',returnTo:location.pathname+location.search});
      const response=await life.fetch('/ui/messages/star',{method:'POST',credentials:'same-origin',headers:{Accept:'application/json'},body:values,signal:controller.signal});
      const result=await response.json();
      if(life.signal.aborted)return;
      if(generation===operationGeneration&&result.star?.messageId===id)states.set(id,{known:true,starred:result.star.starred,revision:result.star.revisionId});
      else if(generation===operationGeneration&&!result.ok&&!button.hasAttribute('data-shared-star'))states.set(id,{...current,known:false});
      if(result.ok||result.rejected)clear(id);
      announce(result.message||labels.uncertain);
    }catch{if(!life.signal.aborted)announce(labels.uncertain);}
    finally{
      clearTimeout(timeout);if(life.signal.aborted)return;busy.delete(id);generation++;body.dataset.starGeneration=String(generation);render();
      if(id===viewerMessage)refreshViewer();
      document.dispatchEvent(new CustomEvent('oie:refresh-request'));
    }
  });
});
