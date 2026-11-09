window.OieRuntime.register('read-state',life=>{
 'use strict';
 const root=document.getElementById('oie-app'),timeline=document.getElementById('message-timeline');
 const engine=root?.dataset.engineId,account=root?.dataset.accountId,conversation=root?.dataset.conversationId;
 const uuid=value=>/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value||'');
 if(!timeline||root.dataset.mock==='true'||![engine,account,conversation].every(uuid))return;
 const path='/api/v1/conversations/'+conversation+'/read-state',csrf=document.querySelector('meta[name="oie-csrf"]')?.content||'';
 const headers={'X-Oie-Engine-ID':engine,'X-Oie-Account-ID':account};
 const seen=new Map(),acknowledged=new Set();let supported=false,busy=false,pending=null,nextKey='',retryAt=0,delay=1000;
 const status=root.querySelector('[data-read-status]');
 function feedback(failed){if(status){status.textContent=failed?status.dataset.readFailed:'';status.hidden=!failed;}}
 function focused(){return !document.querySelector('[data-message-search-root][data-search-open="true"],[data-conversation-search][data-search-open="true"]')&&!document.hidden&&document.hasFocus()&&document.getElementById('oie-app')===root&&document.body.dataset.conversationId===conversation&&timeline.getClientRects().length>0&&![...document.querySelectorAll('dialog[open],[role="dialog"][aria-modal="true"]')].some(node=>node.getClientRects().length>0);}
 function valid(state){return state?.conversationId===conversation&&state.accountId===account&&Number.isInteger(state.unreadCount)&&state.unreadCount>=0;}
 async function initialize(){
  try{const response=await life.fetch(path,{credentials:'same-origin',cache:'no-store',headers});if(life.signal.aborted)return;if(response.status===404||response.status===405)return;if(!response.ok||!valid(await response.json()))throw Error('read_state_unavailable');nextKey=response.headers.get('X-Oie-Next-Operation-Key')||'';supported=uuid(nextKey);}
  catch{if(!life.signal.aborted)life.timeout(initialize,5000);}
 }
 async function submit(){
  if(!pending||busy||!focused()||Date.now()<retryAt)return;
  busy=true;
  try{
   const response=await life.fetch(path,{method:'POST',credentials:'same-origin',headers:{...headers,'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':pending.key},body:JSON.stringify({messageIds:pending.ids})});
   if(life.signal.aborted)return;
   if([400,403,404,409].includes(response.status)){supported=false;pending=null;feedback(true);return;}
   if(!response.ok||!valid(await response.json()))throw Error('read_failed');
   for(const id of pending.ids)acknowledged.add(id);while(acknowledged.size>2000)acknowledged.delete(acknowledged.values().next().value);pending=null;nextKey=response.headers.get('X-Oie-Next-Operation-Key')||'';if(!uuid(nextKey))initialize();delay=1000;retryAt=0;feedback(false);
   // An operation replay can contain an older count. Refresh engine truth rather
   // than replacing a newer badge with the receipt's snapshot.
   document.dispatchEvent(new CustomEvent('oie:refresh-request'));
  }catch{if(!life.signal.aborted){feedback(true);retryAt=Date.now()+delay;delay=Math.min(15000,delay*2);}}
  finally{busy=false;}
 }
 function scan(){
  if(!supported||life.signal.aborted)return;
  if(!focused()){seen.clear();return;}
  const now=performance.now(),viewport=timeline.getBoundingClientRect(),current=new Set(),ready=[];
  for(const row of timeline.querySelectorAll('[data-message-id][data-read-eligible="true"]')){
   const id=row.dataset.messageId;if(!uuid(id)||acknowledged.has(id))continue;
   const box=row.getBoundingClientRect(),visible=Math.max(0,Math.min(box.bottom,viewport.bottom,innerHeight)-Math.max(box.top,viewport.top,0));
   const width=Math.max(0,Math.min(box.right,viewport.right,innerWidth)-Math.max(box.left,viewport.left,0));
   if(box.height<=0||width<=0||visible<Math.min(box.height,viewport.height,innerHeight)*0.6)continue;
   current.add(id);if(!seen.has(id))seen.set(id,now);if(now-seen.get(id)>=500)ready.push(id);
  }
  for(const id of seen.keys())if(!current.has(id))seen.delete(id);
  if(!pending&&ready.length&&uuid(nextKey)){pending={key:nextKey,ids:ready.slice(0,100)};nextKey='';}
  submit();
 }
 life.on(window,'blur',()=>seen.clear());life.on(document,'visibilitychange',()=>{if(document.hidden)seen.clear();});
 life.interval(scan,250);initialize();
});
