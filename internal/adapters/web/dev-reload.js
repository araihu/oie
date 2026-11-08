(() => {
 'use strict';
 const build=document.currentScript?.dataset.buildId;
 if(!build)return;
 let timer=null,request=null,stopped=false,reloading=false;
 function schedule(){clearTimeout(timer);if(!stopped&&!reloading&&!document.hidden)timer=setTimeout(poll,2000)}
 async function poll(){
  if(stopped||reloading||document.hidden||request)return;
  const controller=new AbortController();request=controller;
  const timeout=setTimeout(()=>controller.abort(),3000);
  try{
   const response=await fetch('/ui/dev/build',{cache:'no-store',credentials:'same-origin',redirect:'error',signal:controller.signal});
   if(!response.ok)return;
   const value=await response.json();
   if(!stopped&&!controller.signal.aborted&&typeof value.buildId==='string'&&value.buildId.length>0&&value.buildId.length<=128&&value.buildId!==build){reloading=true;location.reload()}
  }catch{}finally{clearTimeout(timeout);request=null;schedule()}
 }
 document.addEventListener('visibilitychange',()=>{clearTimeout(timer);if(document.hidden)request?.abort();else if(!stopped)poll()});
 window.addEventListener('pagehide',()=>{stopped=true;clearTimeout(timer);request?.abort()});
 window.addEventListener('pageshow',event=>{if(event.persisted){stopped=false;poll()}});
 schedule();
})();
