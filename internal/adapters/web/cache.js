(() => {
 'use strict';
 // Shared result cache. The LRU owns RAM expiry/eviction. Storage adapters own
 // persistence; decorated loaders own data and never retain DOM or PDF workers.
 const library=import('/ui/cache/lru-cache.min.mjs'),ready=library.then(({LRUCache})=>new LRUCache({max:128,maxSize:16*1024*1024,maxEntrySize:2*1024*1024,ttl:30*60*1000,updateAgeOnGet:false,ttlAutopurge:false}));
 ready.catch(()=>{});
 const pending=new Map(),jobs=new Set(),stores=new Map(),aborted=()=>new DOMException('Aborted','AbortError');
 function dataOnly(value,depth=0){
  if(depth>12)return false;if(value===null||['string','number','boolean','undefined'].includes(typeof value))return true;
  if(value instanceof Blob)return true;if(typeof value!=='object')return false;
  const prototype=Object.getPrototypeOf(value);if(!Array.isArray(value)&&prototype!==Object.prototype&&prototype!==null)return false;
  return Object.values(value).every(child=>dataOnly(child,depth+1));
 }
 function decorate({namespace,key,load,size,ttl=30*60*1000,failureTTL=30000,storage, persist=()=>true,validate=()=>true}){
  if(!namespace||typeof key!=='function'||typeof load!=='function'||typeof size!=='function')throw TypeError('cache configuration');
  return async(input,{signal,refresh=false,onHit,onStored}={})=>{
   if(signal?.aborted)throw aborted();
   const cache=await ready.catch(()=>null),identity=namespace+':'+JSON.stringify(key(input));
   if(signal?.aborted)throw aborted();
   if(refresh){cache?.delete(identity);await storage?.delete(identity)}
   if(signal?.aborted)throw aborted();
   const hit=cache?.get(identity);
   if(hit){if(hit.failed)throw Error('cached_preview_failure');onHit?.();if(hit.persisted)onStored?.();return hit.value}
   let job=pending.get(identity);if(job?.controller.signal.aborted){pending.delete(identity);job=null}
   if(!job){
    if(jobs.size>=16)throw Error('cache_capacity');
    job={controller:new AbortController(),users:0,persisted:false,cached:false};pending.set(identity,job);jobs.add(job);
    job.promise=Promise.resolve().then(async()=>{
     const stored=storage&&persist(input)?await storage.get(identity):null;
     if(job.controller.signal.aborted)throw aborted();
     if(stored&&await validate(stored.value,input)){job.persisted=true;job.cached=true;return stored.value}
     if(stored)await storage.delete(identity);
     return load(input,{signal:job.controller.signal});
    }).then(async value=>{
     if(job.controller.signal.aborted)throw aborted();
     const weight=size(value)+identity.length*2+128;
     if(!job.controller.signal.aborted&&dataOnly(value)&&Number.isSafeInteger(weight)&&weight>0&&weight<=2*1024*1024){
      if(storage&&persist(input)&&!job.persisted)job.persisted=await storage.set(identity,value,weight);
      if(!job.controller.signal.aborted)cache?.set(identity,{value,persisted:job.persisted},{size:weight,ttl:typeof ttl==='function'?ttl(value,input):ttl});
     }
     return value;
    },error=>{if(!job.controller.signal.aborted&&failureTTL>0)cache?.set(identity,{failed:true},{size:identity.length*2+128,ttl:failureTTL});throw error}).finally(()=>{jobs.delete(job);if(pending.get(identity)===job)pending.delete(identity)});
    job.promise.catch(()=>{});
   }
   job.users++;
   return new Promise((resolve,reject)=>{
    let done=false;
    const detach=()=>{if(done)return;done=true;signal?.removeEventListener('abort',cancel);job.users--;if(job.users===0)job.controller.abort()};
    const cancel=()=>{detach();reject(aborted())};signal?.addEventListener('abort',cancel,{once:true});if(signal?.aborted){cancel();return}
    job.promise.then(value=>{if(done)return;detach();try{if(job.cached)onHit?.();if(job.persisted)onStored?.();resolve(value)}catch(error){reject(error)}},error=>{if(done)return;detach();reject(error)});
   });
  };
 }
 function indexedDBStore({name,store='entries',ttl=7*24*60*60*1000,maxEntries=64,maxSize=16*1024*1024}){
  if(typeof name!=='string'||!name||name.length>128||!Number.isSafeInteger(maxEntries)||maxEntries<1||maxEntries>128||!Number.isSafeInteger(maxSize)||maxSize<1||maxSize>16*1024*1024||!Number.isFinite(ttl)||ttl<=0)throw TypeError('storage configuration');
  const identity=JSON.stringify([name,store,ttl,maxEntries,maxSize]);if(stores.has(identity))return stores.get(identity);if(stores.size>=8)throw Error('storage_capacity');
  let database=null;
  async function open(){
   if(!globalThis.indexedDB)return null;
   if(!database)database=new Promise(resolve=>{let settled=false;const done=db=>{if(settled){db?.close();return}settled=true;resolve(db)};try{const request=indexedDB.open(name,1);request.onupgradeneeded=()=>{if(!request.result.objectStoreNames.contains(store))request.result.createObjectStore(store,{keyPath:'key'})};request.onerror=()=>done(null);request.onblocked=()=>done(null);request.onsuccess=()=>{const db=request.result;db.onversionchange=()=>{db.close();database=null};done(db)}}catch{done(null)}});
   return database;
  }
  const valid=entry=>entry&&Number.isSafeInteger(entry.bytes)&&entry.bytes>0&&entry.bytes<=2*1024*1024&&Number.isFinite(entry.createdAt)&&entry.createdAt<=Date.now()&&Date.now()-entry.createdAt<ttl&&Number.isFinite(entry.usedAt)&&dataOnly(entry.value);
  async function remove(key){const db=await open();if(!db)return;await new Promise(resolve=>{try{const tx=db.transaction(store,'readwrite');tx.objectStore(store).delete(key);tx.oncomplete=resolve;tx.onabort=resolve;tx.onerror=()=>{}}catch{resolve()}})}
  async function put(entry){
   const implementation=await library.catch(()=>null),db=await open();if(!db||!implementation)return false;
   return new Promise(resolve=>{try{const tx=db.transaction(store,'readwrite'),table=tx.objectStore(store);table.put(entry);const all=table.getAll();all.onsuccess=()=>{
    // The same library chooses disk survivors; the transaction only applies its
    // decision. Blob values are references, not decoded PDF/image resources.
    const retained=new implementation.LRUCache({max:maxEntries,maxSize,sizeCalculation:value=>value.bytes,ttlAutopurge:false});
    for(const value of all.result.sort((a,b)=>a.usedAt-b.usedAt))if(valid(value))retained.set(value.key,value,{ttl:Math.max(1,ttl-(Date.now()-value.createdAt))});
    const keys=new Set(retained.keys());for(const value of all.result)if(!keys.has(value.key))table.delete(value.key);
   };tx.oncomplete=()=>resolve(true);tx.onabort=()=>resolve(false);tx.onerror=()=>{}}catch{resolve(false)}});
  }
  const adapter={
   async get(key){const db=await open();if(!db)return null;const entry=await new Promise(resolve=>{try{const tx=db.transaction(store,'readonly'),request=tx.objectStore(store).get(key);request.onsuccess=()=>resolve(request.result);request.onerror=()=>resolve(null);tx.onabort=()=>resolve(null)}catch{resolve(null)}});if(!valid(entry)){if(entry)await remove(key);return null}entry.usedAt=Date.now();await put(entry);return entry},
   async set(key,value,bytes){if(!dataOnly(value))return false;return put({key,value,bytes,createdAt:Date.now(),usedAt:Date.now()})},
   delete:remove,
   async close(){const active=database;database=null;(await active)?.close()}
  };
  stores.set(identity,adapter);return adapter;
 }
 async function clear(){const cache=await ready.catch(()=>null);cache?.clear();for(const job of jobs)job.controller.abort()}
 window.OieCache={decorate,indexedDBStore,clear,async stats(){const cache=await ready.catch(()=>null);return{entries:cache?.size||0,bytes:cache?.calculatedSize||0,pending:jobs.size}}};
 window.addEventListener('pagehide',()=>{void clear();for(const store of stores.values())void store.close()});
})();
