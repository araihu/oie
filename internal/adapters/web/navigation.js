(() => {
 'use strict';
 const runtime=window.OieRuntime;let booted=false,epoch=0,currentURL=location.href,infoReturnScroll=0,infoRequestGeneration=0;const positions=new Map();
 const app=()=>document.getElementById('oie-app');
 const scrollIDs=['conversation-list','message-timeline','shared-content-scroll','details-body','accounts-body'];
 function remember(){const state={};for(const id of scrollIDs){const node=document.getElementById(id);if(node)state[id]=node.scrollTop}positions.set(currentURL,state);if(positions.size>100)positions.delete(positions.keys().next().value)}
 function routeState(){const root=app();if(!root)return;for(const name of ['openDrawer','engineId','eventCursor','conversationId','accountId','focusMessage','contextNewer','contextMissing','mock'])document.body.dataset[name]=root.dataset[name]||'';document.body.dataset.starGeneration='0';document.documentElement.lang=root.dataset.routeLocale;document.documentElement.dataset.mode=root.dataset.routeTheme;document.documentElement.classList.toggle('dark',root.dataset.routeTheme==='dark'||root.dataset.routeTheme==='system'&&matchMedia('(prefers-color-scheme: dark)').matches);document.querySelector('meta[name="oie-csrf"]')?.setAttribute('content',root.dataset.csrf||'');}
 function showError(){const region=document.querySelector('[data-navigation-status]');if(region){region.textContent=app()?.dataset.routeError||'';region.hidden=false}}
 function upgrade(root=document){if(!root)return;for(const node of root.querySelectorAll('a[href],form')){
  if(node.matches('a[download],a[target]:not([target="_self"])')||node.hasAttribute('hx-get')||node.hasAttribute('hx-post'))continue;
  const form=node.tagName==='FORM';if(form&&(node.method||'get').toLowerCase()!=='get')continue;
  let url;try{url=new URL(form?node.action:node.href,location.href)}catch{continue}
  if(url.origin!==location.origin||url.pathname!=='/'||(!form&&(node.getAttribute('href')||'').startsWith('#')))continue;
  const info=!!node.closest('#chat-info-content')&&(node.matches('form[data-info-search]')||url.searchParams.get('open')==='details'&&url.searchParams.get('chat')===document.body.dataset.conversationId);
  node.setAttribute('hx-boost','true');node.setAttribute('hx-target',info?'#chat-info-content':'#oie-app');node.setAttribute('hx-select',info?'#chat-info-content':'#oie-app');node.setAttribute('hx-swap','outerHTML show:none');node.setAttribute('hx-push-url','true');node.setAttribute('hx-headers',JSON.stringify({'X-Oie-Fragment':info?'info':'app'}));
 }window.htmx?.process(root);}
 // The dependency loader can expose HTMX before Alpine finishes loading.
 // Process the clicked sidebar link before the event reaches its target,
 // without starting application scopes before the remaining dependencies.
 document.addEventListener('click',event=>{
  if(booted||!window.htmx||event.defaultPrevented||event.button!==0||event.metaKey||event.ctrlKey||event.shiftKey||event.altKey)return;
  const link=event.target.closest?.('.oie-navigation a[hx-boost="true"]');
  if(link)window.htmx.process(link);
 },true);
 function infoReady(starGeneration){const root=document.getElementById('chat-info-content');if(!root)return;upgrade(root);document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root,generation:starGeneration}}));const body=document.getElementById('details-body');if(root.dataset.infoView){if(body)body.scrollTop=0;root.querySelector('[data-info-heading]')?.focus({preventScroll:true})}else{if(body)body.scrollTop=infoReturnScroll;const kind=new URL(location.href).searchParams.get('infoFocus');root.querySelector(`[data-info-entry="${CSS.escape(kind||'media')}"]`)?.focus({preventScroll:true})}}
 document.addEventListener('htmx:before:request',event=>{const ctx=event.detail.ctx;if(!ctx||!['oie-app','chat-info-content'].includes(ctx.target?.id))return;ctx.oieEpoch=++epoch;ctx.oieKind=ctx.target.id;ctx.oieStarGeneration=Number(document.body.dataset.starGeneration||0);const region=document.querySelector('[data-navigation-status]');if(region){region.textContent=app()?.dataset.routeLoading||'';region.hidden=false}if(ctx.oieKind==='chat-info-content'&&!document.getElementById('chat-info-content')?.dataset.infoView)infoReturnScroll=document.getElementById('details-body')?.scrollTop||0;});
 document.addEventListener('htmx:after:request',event=>{const ctx=event.detail.ctx;if(!ctx?.oieKind)return;if(ctx.oieEpoch!==epoch){event.preventDefault();return}const parsed=new DOMParser().parseFromString(ctx.text||'','text/html'),selector='#'+ctx.oieKind,next=parsed.querySelector(selector);if(!next||(ctx.oieKind==='chat-info-content'&&next.dataset.detailsConversation!==document.body.dataset.conversationId)){event.preventDefault();showError();return}});
 document.addEventListener('htmx:before:swap',event=>{const ctx=event.detail.ctx;if(ctx?.oieKind&&ctx.oieEpoch===epoch){remember();ctx.target=document.getElementById(ctx.oieKind);if(ctx.oieKind==='oie-app')runtime.stop();else infoRequestGeneration=ctx.oieStarGeneration;}});
 document.addEventListener('htmx:after:settle',event=>{const root=event.detail.task?.target;if(!root||!root.isConnected||root!==document.getElementById(root.id)||!['oie-app','chat-info-content'].includes(root.id))return;if(root.id==='oie-app'){routeState();upgrade(app());if(booted)runtime.start();const saved=positions.get(location.href);requestAnimationFrame(()=>{if(root!==app())return;if(saved)for(const [id,top]of Object.entries(saved)){if(id==='message-timeline'&&new URL(location.href).searchParams.has('message'))continue;const node=document.getElementById(id);if(node)node.scrollTop=top}const info=document.getElementById('chat-info-content');if(info)document.dispatchEvent(new CustomEvent('oie:info-content',{detail:{root:info,generation:Number(document.body.dataset.starGeneration||0)}}));});}else infoReady(infoRequestGeneration);currentURL=location.href;const status=document.querySelector('[data-navigation-status]');if(status)status.hidden=true;});
 document.addEventListener('htmx:error',event=>{if(event.detail.ctx?.oieKind&&event.detail.ctx.oieEpoch===epoch)showError()});
 document.addEventListener('htmx:before:history:restore',event=>{event.preventDefault();window.htmx.ajax('GET',event.detail.path,{source:app(),target:app(),swap:'outerHTML show:none',select:'#oie-app',headers:{'X-Oie-Fragment':'app','HX-History-Restore-Request':'true'},push:'false'});});
 window.OieNavigation={go(value){const url=new URL(value,location.href);if(url.origin!==location.origin||url.pathname!=='/'||!window.htmx){location.assign(url.href);return}return window.htmx.ajax('GET',url.pathname+url.search,{source:app(),target:app(),swap:'outerHTML show:none',select:'#oie-app',push:url.pathname+url.search,headers:{'X-Oie-Fragment':'app'}})}};
 document.addEventListener('oie:navigation-upgrade',()=>upgrade(app()));
 async function ready(){await window.goshtosoDependencies?.ready;booted=true;routeState();upgrade(app());runtime.start();}
 if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',ready,{once:true});else ready();
})();
