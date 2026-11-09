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
      const id=button.dataset.starMessage,current=states.get(id),pending=id&&journal(id),known=!!current?.known;
      button.hidden=!known;button.disabled=!known||busy.has(id);
      const label=pending?labels.retry:current?.starred?labels.unstar:labels.star;
      button.setAttribute('aria-label',label||'');button.title=label||'';
      if(button.id?.startsWith('star-message-')){button.setAttribute('role','menuitem');button.removeAttribute('aria-pressed');button.textContent=label||'';}else button.setAttribute('aria-pressed',known?String(current.starred):'mixed');
      button.dataset.starred=String(!!current?.starred);button.dataset.starPending=String(!!pending);
      const text=button.querySelector('[data-star-label]');if(text)text.textContent=label||'';
    }
  }
  function read(root){
    const data=root.querySelector('#oie-star-data');if(!data)return;
    if(data.dataset.known!=='true'){for(const value of states.values())value.known=false;render();return;}
    for(const row of data.querySelectorAll('[data-star-state]'))states.set(row.dataset.starSource,{known:true,starred:row.dataset.starred==='true',revision:row.dataset.revision||null});
    render();
  }
  read(document);
  function readInfo(root){if(!root)return;for(const data of root.querySelectorAll('[data-info-star-states]'))if(data.dataset.known==='true'){for(const row of data.querySelectorAll('[data-star-state]'))states.set(row.dataset.starSource,{known:true,starred:row.dataset.starred==='true',revision:row.dataset.revision||null});}render();}
  readInfo(document);
  life.on(document,'oie:info-content',event=>{if(event.detail.generation===generation)readInfo(event.detail.root);else{render();document.dispatchEvent(new CustomEvent('oie:refresh-request'));}});

  life.on(document,'oie:actions-refreshed',render);
  function refreshInfo(page){
    const current=document.querySelector('[data-info-stars]'),fresh=page.querySelector('#live-info-content [data-info-stars]');
    if(!current||!fresh||current.dataset.version===fresh.dataset.version)return;
    const scroll=document.getElementById('details-body'),top=scroll?.scrollTop;
    const active=current.contains(document.activeElement)?document.activeElement:null,message=active?.closest('[data-info-star-message]')?.dataset.infoStarMessage,wasStar=active?.matches('[data-star-message]');
    const apply=()=>{for(const child of current.children)window.Alpine?.destroyTree(child);current.innerHTML=fresh.innerHTML;current.dataset.version=fresh.dataset.version;for(const child of current.children)window.Alpine?.initTree(child);};
    if(window.Alpine?.mutateDom)window.Alpine.mutateDom(apply);else apply();
    if(active){const row=message&&current.querySelector(`[data-info-star-message="${CSS.escape(message)}"]`);const target=wasStar?row?.querySelector('[data-star-message]'):row?.querySelector('[data-info-source]');(target||current.querySelector('[data-info-star-message] [data-star-message]')||current.querySelector('[data-info-heading]'))?.focus({preventScroll:true});}
    if(scroll&&top!==undefined)scroll.scrollTop=top;
    document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));
  }
  life.on(document,'oie:stars-refresh',event=>{if(event.detail.generation===generation){refreshInfo(event.detail.page);read(event.detail.page);readInfo(event.detail.page);}});
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
      else if(generation===operationGeneration&&!result.ok)states.set(id,{...current,known:false});
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
