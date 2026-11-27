(() => {
 'use strict';
 const modules=new Map(),scopes=new Map(),memory=new Map();let active=false,generation=0;
 async function request(input, options) {
  const response = await fetch(input, options);
  window.OieHostedSession?.observe(response);
  return response;
 }
 async function saveFile(path, name, opener) {
  if (window.OieDesktop?.saveFile) return window.OieDesktop.saveFile(path, name);
  const link = document.createElement('a');
  link.href = path;
  link.download = name || '';
  link.oieDownloadOpener = opener;
  document.body.append(link);
  try { link.click(); } finally { link.remove(); }
  return true;
 }
 // Server-rendered attachments can receive taps before deferred scripts start.
 function iosFileLinks() {
  const ios = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
   (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  if (!ios) return;
  let openFile, pending;
  document.addEventListener('click', event => {
   const link = event.target.closest?.('a[download], a[id^="download-video-"]');
   if (!link || link.hasAttribute('data-file-save') || event.defaultPrevented || event.button !== 0 ||
       event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
   const url = new URL(link.href, location.href);
   if (url.origin !== location.origin || !(url.pathname === '/ui/scheduling/file' ||
       /^\/ui\/(?:omemo-attachments\/[^/]+\/\d+|shared-content\/[^/]+\/content|media\/[a-f0-9]{64})$/.test(url.pathname))) return;
   event.preventDefault();
   if (openFile) openFile(link);
   else pending = {link, opener:link.oieDownloadOpener || link, route:location.href, href:link.href};
  }, true);
  window.addEventListener('pagehide', () => { pending = null; });
  return (life, handle) => {
   openFile = handle;
   life.cleanup(() => { openFile = null; pending = null; });
   const queued = pending;
   pending = null;
   if (queued?.opener.isConnected && queued.route === location.href && queued.href === queued.link.href) {
    openFile(queued.link);
   }
  };
 }
 const onIOSFileOpen = iosFileLinks();
 function scope(){
  const controller=new AbortController(),cleanups=[],timeouts=new Set(),intervals=new Set(),frames=new Set();
  const life={signal:controller.signal,on(target,type,handler,options={}){if(!target)return;const opts=typeof options==='boolean'?{capture:options}:options;target.addEventListener(type,handler,{...opts,signal:controller.signal});},cleanup(fn){cleanups.push(fn);},raf(fn){if(controller.signal.aborted)return 0;const id=requestAnimationFrame(time=>{frames.delete(id);if(!controller.signal.aborted)fn(time)});frames.add(id);return id;},timeout(fn,ms,...args){if(controller.signal.aborted)return 0;const id=setTimeout(()=>{timeouts.delete(id);if(!controller.signal.aborted)fn(...args)},ms);timeouts.add(id);return id;},interval(fn,ms){if(controller.signal.aborted)return 0;const id=setInterval(()=>{if(!controller.signal.aborted)fn()},ms);intervals.add(id);return id;},fetch(input,options={}){if(typeof AbortSignal.any==='function')return request(input,{...options,signal:AbortSignal.any([controller.signal,...(options.signal?[options.signal]:[])])});const linked=new AbortController(),abort=()=>linked.abort();controller.signal.addEventListener('abort',abort,{once:true});options.signal?.addEventListener('abort',abort,{once:true});cleanups.push(()=>{controller.signal.removeEventListener('abort',abort);options.signal?.removeEventListener('abort',abort)});if(controller.signal.aborted||options.signal?.aborted)linked.abort();return request(input,{...options,signal:linked.signal})},dispose(){controller.abort();for(const id of timeouts)clearTimeout(id);for(const id of intervals)clearInterval(id);for(const id of frames)cancelAnimationFrame(id);for(const fn of cleanups.reverse())fn();}};
  return life;
 }
 function start(options){if(active)return;active=true;generation++;for(const [name,init]of modules){const life=scope();scopes.set(name,life);init(life,options)}document.dispatchEvent(new CustomEvent('oie:route-ready',{detail:{generation}}));}
 function stop(){active=false;for(const life of scopes.values())life.dispose();scopes.clear();}
 // Component IDs, ARIA references and Alpine bindings all refer to the
 // same message. Populate only a server-rendered template and a safe DOM ID.
 function messageControls(name,id){
  const template=document.querySelector('template[data-message-template="'+name+'"]');
  if(!template||!/^[-a-zA-Z0-9_]+$/.test(id))return;
  const copy=document.createElement('template');
  copy.innerHTML=template.innerHTML.replaceAll('oie-message-placeholder',id);
  return copy.content;
 }
 window.OieRuntime={memory,messageControls,onIOSFileOpen,request,saveFile,register(name,init){modules.set(name,init);if(active){scopes.get(name)?.dispose();const life=scope();scopes.set(name,life);init(life)}},start,stop,get generation(){return generation}};
})();
