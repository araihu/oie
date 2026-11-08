window.OieRuntime.register('documents', life => {
 'use strict';
 const dialog=document.getElementById('oie-document-viewer');if(!dialog)return;
 const labels=document.querySelector('[data-document-copy]')?.dataset||{},status=dialog.querySelector('[data-document-status]'),text=dialog.querySelector('[data-document-text]'),canvas=dialog.querySelector('[data-document-canvas]'),layer=dialog.querySelector('[data-document-text-layer]'),sheet=dialog.querySelector('[data-document-pdf-page]'),controls=dialog.querySelector('[data-document-pdf-controls]'),scroll=dialog.querySelector('[data-document-scroll]'),counter=dialog.querySelector('[data-document-page]'),retry=dialog.querySelector('[data-document-retry]');
 const uuid=value=>typeof value==='string'&&/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
 let current=null,opener=null,origin='',generation=0,renderGeneration=0,request,loadingTask,pdf,renderTask,textLayer,page=1,zoom=1,pdfjs;
 function feedback(key,state){status.textContent=labels[key]||'';status.dataset.state=state;status.setAttribute('role',state==='failed'?'alert':'status')}
 function reset(){
  generation++;renderGeneration++;request?.abort();request=null;renderTask?.cancel();renderTask=null;textLayer?.cancel();textLayer=null;
  const task=loadingTask,document=pdf;loadingTask=null;pdf=null;if(task)task.destroy().catch(()=>{});else document?.destroy().catch(()=>{});
  text.textContent='';text.hidden=true;layer.replaceChildren();canvas.width=0;canvas.height=0;canvas.style.width='';canvas.style.height='';sheet.hidden=true;sheet.style.width='';sheet.style.height='';controls.hidden=true;counter.textContent='';delete counter.dataset.current;delete counter.dataset.total;retry.hidden=true;page=1;zoom=1;scroll.scrollTop=0;scroll.scrollLeft=0;
 }
 function validSource(card){
  const data=card.dataset;if(![data.documentItem,data.documentEngine,data.documentAccount,data.documentConversation,data.documentMessage].every(uuid)||data.documentEngine!==document.body.dataset.engineId)return null;
  let source;try{source=new URL(data.documentSource,location.href)}catch{return null}
  if(source.origin!==location.origin||source.pathname!=='/ui/shared-content/'+data.documentItem+'/content'||source.username||source.password||source.hash||source.searchParams.get('engineId')!==data.documentEngine||[...source.searchParams.keys()].length!==1)return null;
  let original='';try{const url=new URL(data.documentUrl);if(['http:','https:'].includes(url.protocol)&&!url.username&&!url.password)original=url.href}catch{}
  return {id:data.documentItem,engine:data.documentEngine,format:data.documentFormat,filename:data.documentFilename||labels.page||'',source:source.pathname+source.search,original};
 }
 function failure(unsupported=false){text.hidden=true;sheet.hidden=true;controls.hidden=true;feedback(unsupported?'unsupported':'failed','failed');retry.hidden=!current||current.format==='unsupported'}
 function decode(bytes){
  if(bytes.length>=4&&((bytes[0]===0&&bytes[1]===0&&bytes[2]===254&&bytes[3]===255)||(bytes[0]===255&&bytes[1]===254&&bytes[2]===0&&bytes[3]===0)))throw Error('encoding');
  let encoding='utf-8',offset=0;if(bytes[0]===255&&bytes[1]===254){encoding='utf-16le';offset=2}else if(bytes[0]===254&&bytes[1]===255){encoding='utf-16be';offset=2}
  let result;try{result=new TextDecoder(encoding,{fatal:true}).decode(bytes.subarray(offset))}catch{throw Error('encoding')}if(result.includes('\u0000'))throw Error('encoding');return result;
 }
 async function readText(response){const limit=2*1024*1024;if(Number(response.headers.get('Content-Length'))>limit)throw Error('large');const reader=response.body?.getReader();if(!reader){const body=new Uint8Array(await response.arrayBuffer());if(body.length>limit)throw Error('large');return decode(body)}const chunks=[];let size=0;try{while(true){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>limit){await reader.cancel();throw Error('large')}chunks.push(value)}}finally{reader.releaseLock()}const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length}return decode(bytes)}
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
   request=new AbortController();try{const response=await life.fetch(current.source,{credentials:'same-origin',cache:'no-store',redirect:'error',signal:request.signal});if(!response.ok)throw Error(response.status===413?'large':'unavailable');if(response.headers.get('Content-Type')?.split(';')[0].trim()!=='text/plain')throw Error('encoding');const body=await readText(response);if(token!==generation)return;text.textContent=body;text.hidden=false;feedback(body?'':'empty','ready')}catch(error){if(token===generation&&!life.signal.aborted&&error.name!=='AbortError')failure(['encoding','large'].includes(error.message))}
  }
 }
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
