window.OieRuntime.register('documents', life => {
 'use strict';
 const dialog=document.getElementById('oie-document-viewer');if(!dialog)return;
 const labels=document.querySelector('[data-document-copy]')?.dataset||{},status=dialog.querySelector('[data-document-status]'),text=dialog.querySelector('[data-document-text]'),markdown=dialog.querySelector('[data-document-markdown]'),canvas=dialog.querySelector('[data-document-canvas]'),layer=dialog.querySelector('[data-document-text-layer]'),sheet=dialog.querySelector('[data-document-pdf-page]'),controls=dialog.querySelector('[data-document-pdf-controls]'),scroll=dialog.querySelector('[data-document-scroll]'),counter=dialog.querySelector('[data-document-page]'),retry=dialog.querySelector('[data-document-retry]');
 const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 let current=null,opener=null,origin='',generation=0,renderGeneration=0,request,loadingTask,pdf,renderTask,textLayer,page=1,zoom=1,pdfjs;
 function feedback(key,state){status.textContent=labels[key]||'';status.dataset.state=state;status.setAttribute('role',state==='failed'?'alert':'status')}
 function reset(){
  generation++;renderGeneration++;request?.abort();request=null;renderTask?.cancel();renderTask=null;textLayer?.cancel();textLayer=null;
  const task=loadingTask,document=pdf;loadingTask=null;pdf=null;if(task)task.destroy().catch(()=>{});else document?.destroy().catch(()=>{});
  text.textContent='';text.hidden=true;markdown.replaceChildren();markdown.hidden=true;layer.replaceChildren();canvas.width=0;canvas.height=0;canvas.style.width='';canvas.style.height='';sheet.hidden=true;sheet.style.width='';sheet.style.height='';controls.hidden=true;counter.textContent='';delete counter.dataset.current;delete counter.dataset.total;retry.hidden=true;page=1;zoom=1;scroll.scrollTop=0;scroll.scrollLeft=0;
 }
 function validSource(card){
  const data=card.dataset;if(![data.documentItem,data.documentEngine,data.documentAccount,data.documentConversation,data.documentMessage].every(uuid)||data.documentEngine!==document.body.dataset.engineId)return null;
  let source;try{source=new URL(data.documentSource,location.href)}catch{return null}
  if(source.origin!==location.origin||source.pathname!=='/ui/shared-content/'+data.documentItem+'/content'||source.username||source.password||source.hash||source.searchParams.get('engineId')!==data.documentEngine||[...source.searchParams.keys()].length!==1)return null;
  let original='';try{const url=new URL(data.documentUrl);if(['http:','https:'].includes(url.protocol)&&!url.username&&!url.password)original=url.href}catch{}
  return {id:data.documentItem,engine:data.documentEngine,account:data.documentAccount,conversation:data.documentConversation,message:data.documentMessage,revision:data.documentRevision,messageRevision:data.documentMessageRevision,format:data.documentFormat,filename:data.documentFilename||labels.page||'',source:source.pathname+source.search,original};
 }
 function failure(unsupported=false){text.hidden=true;markdown.hidden=true;sheet.hidden=true;controls.hidden=true;feedback(unsupported?'unsupported':'failed','failed');retry.hidden=!current||current.format==='unsupported'}
 function decode(bytes){
  if(bytes.length>=4&&((bytes[0]===0&&bytes[1]===0&&bytes[2]===254&&bytes[3]===255)||(bytes[0]===255&&bytes[1]===254&&bytes[2]===0&&bytes[3]===0)))throw Error('encoding');
  let encoding='utf-8',offset=0;if(bytes[0]===255&&bytes[1]===254){encoding='utf-16le';offset=2}else if(bytes[0]===254&&bytes[1]===255){encoding='utf-16be';offset=2}
  let result;try{result=new TextDecoder(encoding,{fatal:true}).decode(bytes.subarray(offset))}catch{throw Error('encoding')}if(result.includes('\u0000'))throw Error('encoding');return result;
 }
 async function readText(response){const limit=2*1024*1024;if(Number(response.headers.get('Content-Length'))>limit)throw Error('large');const reader=response.body?.getReader();if(!reader){const body=new Uint8Array(await response.arrayBuffer());if(body.length>limit)throw Error('large');return decode(body)}const chunks=[];let size=0;try{while(true){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>limit){await reader.cancel();throw Error('large')}chunks.push(value)}}finally{reader.releaseLock()}const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length}return decode(bytes)}
 async function renderMarkdown(source,signal,preview=false){
  const response=await life.fetch('/ui/document-markdown',{method:'POST',credentials:'same-origin',cache:'no-store',redirect:'error',headers:{'Content-Type':'application/json'},body:JSON.stringify({source}),signal});
  if(!response.ok)throw Error('markdown');const result=await response.json();if(typeof result.html!=='string'||result.html.length>8*1024*1024)throw Error('markdown');
  // This fragment is Margo output filtered by the BFF into static host HTML.
  // Never initialize Alpine or document runtimes for attachment contents.
  const template=document.createElement('template');template.innerHTML=result.html;
  if(preview)for(const link of template.content.querySelectorAll('a'))link.replaceWith(...link.childNodes);
  return template.content;
 }
 async function pdfStyles(){
  const key='document-pdf-style';if(window.OieRuntime.memory.has(key))return window.OieRuntime.memory.get(key);
  const promise=new Promise((resolve,reject)=>{let link=document.querySelector('link[data-document-pdf-style]');if(link?.sheet){resolve();return}if(!link){link=document.createElement('link');link.rel='stylesheet';link.href='/ui/pdfjs/web/pdf_viewer.css';link.dataset.documentPdfStyle=''}link.addEventListener('load',resolve,{once:true});link.addEventListener('error',()=>{link.remove();window.OieRuntime.memory.delete(key);reject(Error('style'))},{once:true});if(!link.isConnected)document.head.append(link)});window.OieRuntime.memory.set(key,promise);return promise;
 }
 async function renderPage(token){
  if(!pdf||token!==generation)return;const serial=++renderGeneration;renderTask?.cancel();textLayer?.cancel();layer.replaceChildren();feedback('loading','loading');
  for(const button of controls.querySelectorAll('button'))button.disabled=true;
  try{
   const active=await pdf.getPage(page);if(token!==generation||serial!==renderGeneration)return;
   const base=active.getViewport({scale:1}),fit=Math.max(1,scroll.clientWidth-24)/base.width,scale=Math.min(fit*zoom,8192/Math.max(base.width,base.height));const viewport=active.getViewport({scale}),output=Math.min(devicePixelRatio||1,2,Math.sqrt(16000000/(viewport.width*viewport.height)));
   canvas.width=Math.max(1,Math.floor(viewport.width*output));canvas.height=Math.max(1,Math.floor(viewport.height*output));canvas.style.width=viewport.width+'px';canvas.style.height=viewport.height+'px';sheet.style.width=viewport.width+'px';sheet.style.height=viewport.height+'px';sheet.style.setProperty('--scale-factor',String(scale));sheet.style.setProperty('--total-scale-factor',String(scale*(viewport.userUnit||1)));sheet.style.setProperty('--user-unit',String(viewport.userUnit||1));sheet.hidden=false;
   renderTask=active.render({canvasContext:canvas.getContext('2d'),viewport,transform:output===1?null:[output,0,0,output,0,0],annotationMode:pdfjs.AnnotationMode.DISABLE});await renderTask.promise;if(token!==generation||serial!==renderGeneration)return;
   if(typeof pdfjs.TextLayer==='function'){const content=await active.getTextContent();if(token!==generation||serial!==renderGeneration)return;textLayer=new pdfjs.TextLayer({textContentSource:content,container:layer,viewport});await textLayer.render()}
   if(token!==generation||serial!==renderGeneration)return;controls.hidden=false;counter.dataset.current=String(page);counter.dataset.total=String(pdf.numPages);counter.textContent=(labels.page||'')+' '+page+' / '+pdf.numPages;status.textContent='';status.dataset.state='ready';scroll.scrollTop=0;
  }catch(error){if(token!==generation||serial!==renderGeneration||error.name==='RenderingCancelledException'||error.name==='AbortException')return;failure()}
  finally{if(token===generation&&serial===renderGeneration&&pdf){for(const button of controls.querySelectorAll('button'))button.disabled=false;controls.querySelector('[data-document-previous]').disabled=page<=1;controls.querySelector('[data-document-next]').disabled=page>=pdf.numPages;controls.querySelector('[data-document-zoom-out]').disabled=zoom<=0.5;controls.querySelector('[data-document-zoom-in]').disabled=zoom>=3}}
 }
 async function load(){
  reset();const token=generation;if(!current)return;feedback('loading','loading');
  if(current.format==='unsupported'){failure(true);return}
  if(current.format==='pdf'){
   try{await pdfStyles();pdfjs=await import('/ui/pdfjs/build/pdf.mjs');if(token!==generation)return;pdfjs.GlobalWorkerOptions.workerSrc='/ui/pdfjs/build/pdf.worker.mjs';loadingTask=pdfjs.getDocument({url:current.source,cMapUrl:'/ui/pdfjs/cmaps/',cMapPacked:true,standardFontDataUrl:'/ui/pdfjs/standard_fonts/',wasmUrl:'/ui/pdfjs/wasm/',iccUrl:'/ui/pdfjs/iccs/',isEvalSupported:false,enableXfa:false,useSystemFonts:true,canvasMaxAreaInBytes:67108864});const document=await loadingTask.promise;if(token!==generation){document.destroy().catch(()=>{});return}pdf=document;await renderPage(token)}catch(error){if(token===generation&&!life.signal.aborted&&error.name!=='AbortException')failure()}
  }else{
   request=new AbortController();try{const response=await life.fetch(current.source,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:request.signal});if(!response.ok)throw Error(response.status===413?'large':'unavailable');if(response.headers.get('Content-Type')?.split(';')[0].trim()!=='text/plain')throw Error('encoding');const body=await readText(response);if(token!==generation)return;if(current.format==='markdown'){
    try{const fragment=await renderMarkdown(body,request.signal);if(token!==generation)return;markdown.replaceChildren(fragment);markdown.hidden=false;feedback(body?'':'empty','ready');return}catch(error){if(token!==generation||request.signal.aborted)return}
   }text.textContent=body;text.hidden=false;feedback(body?'':'empty','ready')}catch(error){if(token===generation&&!life.signal.aborted&&error.name!=='AbortError')failure(['encoding','large'].includes(error.message))}
  }
 }
 // Keep only generated PNG thumbnails, never original PDF bytes. Runtime memory
 // survives fragment navigation; IndexedDB also survives document reloads.
 const cacheName='oie.pdf-previews.v1',cacheStore='thumbnails',cacheTTL=7*24*60*60*1000;
 const cache=window.OieRuntime.memory.get(cacheName)||{entries:new Map(),bytes:0,database:null};window.OieRuntime.memory.set(cacheName,cache);
 function validThumbnail(entry){return entry&&entry.blob instanceof Blob&&entry.blob.type==='image/png'&&entry.blob.size>0&&entry.blob.size<=1024*1024&&Number.isFinite(entry.createdAt)&&entry.createdAt<=Date.now()&&Date.now()-entry.createdAt<cacheTTL&&Number.isFinite(entry.usedAt)&&Number.isFinite(entry.width)&&entry.width>0&&entry.width<=400.01&&Number.isFinite(entry.height)&&entry.height>0&&entry.height<=180.01&&Number.isInteger(entry.pixelsWide)&&entry.pixelsWide>0&&entry.pixelsWide<=801&&Number.isInteger(entry.pixelsHigh)&&entry.pixelsHigh>0&&entry.pixelsHigh<=361}
 async function thumbnailKey(record){
  // Include ownership, source/revision and output density. A changed message,
  // catalog entry or renderer can never reuse another source's thumbnail.
  const identity=JSON.stringify(['pdfjs-6.5.438-400x180-v1',record.engine,record.account,record.conversation,record.message,record.id,record.revision,record.messageRevision,record.original,Math.min(devicePixelRatio||1,2)]);
  if(!globalThis.crypto?.subtle)return identity;
  const digest=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(identity));return Array.from(new Uint8Array(digest),byte=>byte.toString(16).padStart(2,'0')).join('');
 }
 function rememberThumbnail(entry){
  const old=cache.entries.get(entry.key);if(old){cache.bytes-=old.blob.size;cache.entries.delete(entry.key)}
  cache.entries.set(entry.key,entry);cache.bytes+=entry.blob.size;
  while(cache.entries.size>16||cache.bytes>4*1024*1024){const key=cache.entries.keys().next().value;cache.bytes-=cache.entries.get(key).blob.size;cache.entries.delete(key)}
 }
 async function thumbnailDatabase(){
  if(!globalThis.indexedDB)return null;
  if(!cache.database)cache.database=new Promise(resolve=>{
   let settled=false;const done=value=>{if(settled){value?.close();return}settled=true;resolve(value)};
   try{const request=indexedDB.open(cacheName,1);request.onupgradeneeded=()=>{if(!request.result.objectStoreNames.contains(cacheStore))request.result.createObjectStore(cacheStore,{keyPath:'key'})};request.onsuccess=()=>{const db=request.result;db.onversionchange=()=>{db.close();cache.database=null};done(db)};request.onerror=()=>done(null);request.onblocked=()=>done(null)}catch{done(null)}
  });return cache.database;
 }
 async function readThumbnail(key){
  const hot=cache.entries.get(key);if(validThumbnail(hot)){hot.usedAt=Date.now();rememberThumbnail(hot);void persistThumbnail(hot);return hot}
  const db=await thumbnailDatabase();if(!db)return null;
  const entry=await new Promise(resolve=>{try{const tx=db.transaction(cacheStore,'readonly'),request=tx.objectStore(cacheStore).get(key);request.onsuccess=()=>resolve(request.result);request.onerror=()=>resolve(null);tx.onabort=()=>resolve(null)}catch{resolve(null)}});
  if(!validThumbnail(entry)){if(entry)void forgetThumbnail(key);return null}entry.usedAt=Date.now();rememberThumbnail(entry);void persistThumbnail(entry);return entry;
 }
 async function persistThumbnail(entry){
  const db=await thumbnailDatabase();if(!db)return;
  await new Promise(resolve=>{try{const tx=db.transaction(cacheStore,'readwrite'),store=tx.objectStore(cacheStore);store.put(entry);const all=store.getAll();all.onsuccess=()=>{let bytes=0,count=0;for(const item of all.result.sort((a,b)=>b.usedAt-a.usedAt)){if(!validThumbnail(item)||++count>64||(bytes+=item.blob.size)>16*1024*1024)store.delete(item.key)}};tx.oncomplete=resolve;tx.onabort=resolve;tx.onerror=()=>{}}catch{resolve()}});
 }
 async function forgetThumbnail(key){
  const entry=cache.entries.get(key);if(entry){cache.bytes-=entry.blob.size;cache.entries.delete(key)}
  const db=await thumbnailDatabase();if(db)try{const tx=db.transaction(cacheStore,'readwrite');tx.objectStore(cacheStore).delete(key);tx.onerror=()=>{}}catch{}
 }
 async function paintThumbnail(card,target,entry,controller){
  let bitmap,url;
  try{
   if(typeof createImageBitmap==='function')bitmap=await createImageBitmap(entry.blob);
   else{url=URL.createObjectURL(entry.blob);bitmap=await new Promise((resolve,reject)=>{const image=new Image();image.onload=()=>resolve(image);image.onerror=reject;image.src=url})}
   if(controller.signal.aborted||!card.isConnected)return;
   if(bitmap.width!==entry.pixelsWide||bitmap.height!==entry.pixelsHigh)throw Error('thumbnail');
   const node=document.createElement('canvas');node.width=entry.pixelsWide;node.height=entry.pixelsHigh;node.style.width=entry.width+'px';node.style.height=entry.height+'px';node.getContext('2d').drawImage(bitmap,0,0);target.replaceChildren(node);card.dataset.documentPreviewState='ready';card.dataset.documentPreviewCache='hit';
  }finally{bitmap?.close?.();if(url)URL.revokeObjectURL(url)}
 }
 // Card previews only read bounded bytes for visible documents. Originals still
 // use the existing explicit viewer path, independently of preview failures.
 const previewChunk=64*1024,previewBudget=2*1024*1024,previewQueue=[],previewSeen=new WeakSet(),previewRequests=new Set();let previewActive=0;
 async function previewRange(record,start,end,controller,partial=false){
  const response=await life.fetch(record.source,{credentials:'same-origin',cache:'no-store',redirect:'error',headers:{Range:`bytes=${start}-${end-1}`},signal:controller.signal});
  if(!response.ok)throw Error('unavailable');
  const kind=response.headers.get('Content-Type')?.split(';')[0].trim();if(kind!==(record.format==='pdf'?'application/pdf':'text/plain'))throw Error('type');
  const header=response.headers.get('Content-Range'),range=header&&/^bytes (\d+)-(\d+)\/(\d+)$/.exec(header),length=Number(response.headers.get('Content-Length'));
  if(response.status===206&&(!range||Number(range[1])!==start||Number(range[2])>=end||Number(range[3])<=Number(range[2])))throw Error('range');
  if(response.status!==206&&(start!==0||(!partial&&length>end))) {await response.body?.cancel();throw Error('range')}
  const reader=response.body?.getReader();if(!reader)throw Error('stream');const chunks=[];let size=0;
  try{while(size<end-start){const {done,value}=await reader.read();if(done)break;const chunk=value.subarray(0,end-start-size);chunks.push(chunk);size+=chunk.length;if(chunk.length!==value.length&&!partial)throw Error('large')}if(partial)await reader.cancel();else{const rest=await reader.read();if(!rest.done)throw Error('large')}}finally{await reader.cancel().catch(()=>{});reader.releaseLock()}
  const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length}
  if(range&&size!==Number(range[2])-start+1)throw Error('range');
  return {bytes,total:range?Number(range[3]):size,range:!!range};
 }
 async function paintPreview(card,record,controller){
  const target=card.querySelector('[data-document-preview]');if(!target)return;
  let cacheKey;
  if(record.format==='pdf'){
   cacheKey=await thumbnailKey(record);const entry=await readThumbnail(cacheKey);if(controller.signal.aborted||!card.isConnected)return;
   if(entry){try{await paintThumbnail(card,target,entry,controller);return}catch{await forgetThumbnail(cacheKey);if(controller.signal.aborted||!card.isConnected)return}}
  }
  const initial=await previewRange(record,0,previewChunk,controller,record.format!=='pdf');if(controller.signal.aborted||!card.isConnected)return;
  if(record.format!=='pdf'){
   // A range can end inside a UTF-8 sequence. Decode incrementally without
   // flushing that final incomplete code point into a replacement character.
   let bytes=initial.bytes,encoding='utf-8';if(bytes[0]===255&&bytes[1]===254){encoding='utf-16le';bytes=bytes.subarray(2)}else if(bytes[0]===254&&bytes[1]===255){encoding='utf-16be';bytes=bytes.subarray(2)}
   const excerpt=new TextDecoder(encoding,{fatal:true}).decode(bytes,{stream:initial.total>initial.bytes.length});if(excerpt.includes('\u0000'))throw Error('encoding');
   const node=document.createElement('span');
   if(record.format==='markdown'){
    try{const fragment=await renderMarkdown(excerpt,controller.signal,true);if(controller.signal.aborted||!card.isConnected)return;node.dataset.documentMarkdownPreview='';node.className='oie-document-markdown';node.append(fragment)}catch(error){if(controller.signal.aborted)return;node.dataset.documentExcerpt='';node.textContent=excerpt.slice(0,1800)||labels.empty||''}
   }else{node.dataset.documentExcerpt='';node.textContent=excerpt.slice(0,1800)||labels.empty||''}
   target.replaceChildren(node);card.dataset.documentPreviewState='ready';return;
  }
  if(!Number.isSafeInteger(initial.total)||initial.total>128*1024*1024)throw Error('large');
  const library=await import('/ui/pdfjs/build/pdf.mjs');if(controller.signal.aborted)return;library.GlobalWorkerOptions.workerSrc='/ui/pdfjs/build/pdf.worker.mjs';
  let spent=initial.bytes.length,task,failed,rangeQueue=Promise.resolve();const failure=new Promise((_,reject)=>failed=reject);controller.signal.addEventListener('abort',()=>failed(Error('aborted')),{once:true});
  class PreviewTransport extends library.PDFDataRangeTransport{
   requestDataRange(begin,end){
    if(controller.signal.aborted)return;
    if(!Number.isSafeInteger(begin)||!Number.isSafeInteger(end)||begin<0||end<=begin||end>initial.total||(spent+=end-begin)>previewBudget){failed(Error('large'));return}
    // PDF.js can request several disjoint objects at once. Serialize each
    // document's ranges so two preview jobs still use at most two HTTP readers.
    rangeQueue=rangeQueue.then(async()=>{if(controller.signal.aborted)return;const part=await previewRange(record,begin,end,controller);if(!controller.signal.aborted){if(part.total!==initial.total)throw Error('changed');this.onDataRange(begin,part.bytes)}}).catch(error=>{if(!controller.signal.aborted)failed(error)});
   }
   abort(){controller.abort()}
  }
  try{
   const range=new PreviewTransport(initial.total,initial.bytes,initial.bytes.length===initial.total);
   task=library.getDocument({range,rangeChunkSize:previewChunk,disableAutoFetch:true,disableStream:true,cMapUrl:'/ui/pdfjs/cmaps/',cMapPacked:true,standardFontDataUrl:'/ui/pdfjs/standard_fonts/',wasmUrl:'/ui/pdfjs/wasm/',iccUrl:'/ui/pdfjs/iccs/',isEvalSupported:false,enableXfa:false,useSystemFonts:true,canvasMaxAreaInBytes:4194304});
   await Promise.race([(async()=>{const pdf=await task.promise,first=await pdf.getPage(1);if(controller.signal.aborted||!card.isConnected)return;const base=first.getViewport({scale:1}),scale=Math.min(400/base.width,180/base.height),viewport=first.getViewport({scale}),output=Math.min(devicePixelRatio||1,2);const node=document.createElement('canvas');node.width=Math.ceil(viewport.width*output);node.height=Math.ceil(viewport.height*output);node.style.width=viewport.width+'px';node.style.height=viewport.height+'px';await first.render({canvasContext:node.getContext('2d'),viewport,transform:output===1?null:[output,0,0,output,0,0],annotationMode:library.AnnotationMode.DISABLE}).promise;if(!controller.signal.aborted&&card.isConnected){target.replaceChildren(node);card.dataset.documentPreviewState='ready';card.dataset.documentPreviewCache='miss';
    const blob=await new Promise(resolve=>node.toBlob(resolve,'image/png'));if(blob&&cacheKey&&!controller.signal.aborted&&card.isConnected){const entry={key:cacheKey,blob,width:viewport.width,height:viewport.height,pixelsWide:node.width,pixelsHigh:node.height,createdAt:Date.now(),usedAt:Date.now()};if(validThumbnail(entry)){rememberThumbnail(entry);await persistThumbnail(entry);if(card.isConnected)card.dataset.documentPreviewStored='true'}}
   }})(),failure]);
  }finally{if(task)await task.destroy().catch(()=>{})}
 }
 async function drainPreviews(){
  while(previewActive<2&&previewQueue.length&&!life.signal.aborted){const card=previewQueue.shift(),record=validSource(card);if(!record||!card.isConnected)continue;previewActive++;const controller=new AbortController();previewRequests.add(controller);const timeout=life.timeout(()=>controller.abort(),12000);card.dataset.documentPreviewState='loading';paintPreview(card,record,controller).catch(()=>{if(card.isConnected)card.dataset.documentPreviewState='unavailable'}).finally(()=>{clearTimeout(timeout);controller.abort();previewRequests.delete(controller);if(card.isConnected&&card.dataset.documentPreviewState==='loading')card.dataset.documentPreviewState='unavailable';previewActive--;drainPreviews()})}
 }
 const previewObserver=typeof IntersectionObserver==='function'?new IntersectionObserver(entries=>{for(const entry of entries)if(entry.isIntersecting){previewObserver.unobserve(entry.target);previewQueue.push(entry.target)}drainPreviews()},{rootMargin:'120px'}):null;
 function observePreviews(){for(const card of document.querySelectorAll('[data-document-item]'))if(!previewSeen.has(card)&&['pdf','text','markdown'].includes(card.dataset.documentFormat)){previewSeen.add(card);previewObserver?.observe(card)}}
 observePreviews();life.on(document,'oie:previews-refresh',observePreviews);life.on(document,'oie:shared-append',observePreviews);life.on(document,'oie:info-shared-search-settled',observePreviews);
 life.cleanup(()=>{previewObserver?.disconnect();previewQueue.length=0;for(const controller of previewRequests)controller.abort();previewRequests.clear()});
 function close(){window.dispatchEvent(new CustomEvent('modal:close',{detail:{id:'oie-document-viewer'}}))}
 life.on(document,'click',event=>{
  const open=event.target.closest('[data-document-open]');if(open){const card=open.closest('[data-document-item]'),record=card&&validSource(card);if(!record)return;event.preventDefault();opener=open;origin=card.closest('#info-shared-results')?'info':'global';current=record;dialog.querySelector('[data-document-title]').textContent=record.filename;dialog.setAttribute('aria-labelledby','oie-document-title');const download=dialog.querySelector('[data-document-download]'),original=dialog.querySelector('[data-document-original]');download.href=record.source+'&download=1';download.hidden=record.format==='unsupported';original.href=record.original||'#';original.hidden=!record.original;window.dispatchEvent(new CustomEvent('modal:open',{detail:{id:'oie-document-viewer'}}));load();return}
  if(event.target.closest('[data-document-close]')){event.preventDefault();close();return}
  if(event.target.closest('[data-document-retry]')){event.preventDefault();load();return}
  const button=event.target.closest('[data-document-previous],[data-document-next],[data-document-fit],[data-document-zoom-in],[data-document-zoom-out]');if(!button||button.disabled||!pdf)return;event.preventDefault();if(button.hasAttribute('data-document-previous'))page=Math.max(1,page-1);if(button.hasAttribute('data-document-next'))page=Math.min(pdf.numPages,page+1);if(button.hasAttribute('data-document-fit'))zoom=1;if(button.hasAttribute('data-document-zoom-in'))zoom=Math.min(3,zoom*1.25);if(button.hasAttribute('data-document-zoom-out'))zoom=Math.max(0.5,zoom/1.25);renderPage(generation);
 });
 life.on(dialog,'close',()=>{const id=current?.id;reset();current=null;status.textContent='';const target=opener?.isConnected?opener:document.querySelector((origin==='info'?'#info-shared-results':'#shared-content-results')+` [data-document-item="${CSS.escape(id||'')}"] [data-document-open]`);life.raf(()=>target?.focus({preventScroll:true}))});
 life.on(window,'pagehide',()=>reset());
 life.cleanup(()=>{reset();current=null;close();if(dialog.open)dialog.close()});
});
