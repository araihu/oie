(() => {
 'use strict';
 const modules=new Map(),scopes=new Map(),memory=new Map();let active=false,generation=0;
 function scope(){
  const controller=new AbortController(),cleanups=[],timeouts=new Set(),intervals=new Set(),frames=new Set();
  const life={signal:controller.signal,on(target,type,handler,options={}){if(!target)return;const opts=typeof options==='boolean'?{capture:options}:options;target.addEventListener(type,handler,{...opts,signal:controller.signal});},cleanup(fn){cleanups.push(fn);},raf(fn){if(controller.signal.aborted)return 0;const id=requestAnimationFrame(time=>{frames.delete(id);if(!controller.signal.aborted)fn(time)});frames.add(id);return id;},timeout(fn,ms,...args){if(controller.signal.aborted)return 0;const id=setTimeout(()=>{timeouts.delete(id);if(!controller.signal.aborted)fn(...args)},ms);timeouts.add(id);return id;},interval(fn,ms){if(controller.signal.aborted)return 0;const id=setInterval(()=>{if(!controller.signal.aborted)fn()},ms);intervals.add(id);return id;},fetch(input,options={}){if(typeof AbortSignal.any==='function')return fetch(input,{...options,signal:AbortSignal.any([controller.signal,...(options.signal?[options.signal]:[])])});const linked=new AbortController(),abort=()=>linked.abort();controller.signal.addEventListener('abort',abort,{once:true});options.signal?.addEventListener('abort',abort,{once:true});cleanups.push(()=>{controller.signal.removeEventListener('abort',abort);options.signal?.removeEventListener('abort',abort)});if(controller.signal.aborted||options.signal?.aborted)linked.abort();return fetch(input,{...options,signal:linked.signal})},dispose(){controller.abort();for(const id of timeouts)clearTimeout(id);for(const id of intervals)clearInterval(id);for(const id of frames)cancelAnimationFrame(id);for(const fn of cleanups.reverse())fn();}};
  return life;
 }
 function start(){active=true;generation++;for(const [name,init]of modules){const life=scope();scopes.set(name,life);init(life)}document.dispatchEvent(new CustomEvent('oie:route-ready',{detail:{generation}}));}
 function stop(){active=false;for(const life of scopes.values())life.dispose();scopes.clear();}
 window.OieRuntime={memory,register(name,init){modules.set(name,init);if(active){scopes.get(name)?.dispose();const life=scope();scopes.set(name,life);init(life)}},start,stop,get generation(){return generation}};
})();
