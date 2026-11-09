// Reuses the isolated HTTP fixture from live-chat.cjs. No protocol traffic.
const assert=require('node:assert/strict');
module.exports=async({browser,base,chat,state,emit,until,id})=>{
 const context=await browser.newContext({locale:'pt-BR',timezoneId:'America/Sao_Paulo'}),page=await context.newPage();
 const errors=[];page.on('pageerror',error=>errors.push(error.message));
 const texts=['askdkadsk','Uma mensagem mais longa que pode ocupar mais de uma linha no celular. '.repeat(3),'Primeira linha\nSegunda linha\nTerceira linha'];
 const bounds=locator=>locator.evaluate(row=>{
  const bubble=row.querySelector('.oie-message-body').parentElement.getBoundingClientRect(),outer=row.getBoundingClientRect();
  return {x:bubble.x,y:bubble.y,width:bubble.width,height:bubble.height,rowHeight:outer.height};
 });
 try{
  for(const width of [390,1440])for(const mode of ['light','dark'])for(const libraryTheme of ['goshtoso','minimal'])for(const originalText of texts){
   await page.setViewportSize({width,height:900});await page.goto(base+'/?chat='+chat+'&lang=pt-BR&theme='+mode);await page.evaluate(theme=>document.documentElement.dataset.theme=theme,libraryTheme);await page.evaluate(()=>document.fonts.ready);
   const checkAccessibility=async(row,phase)=>{
    if(originalText!==texts[0])return;
    const violations=await row.evaluate(async row=>{const result=await axe.run(row,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}});return result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>({target:n.target,checks:n.any.map(c=>c.data)}))}))});
    assert.deepEqual(violations,[],JSON.stringify({width,mode,libraryTheme,phase,violations}));
   };
   if(originalText===texts[0])await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
   let release;const gate=new Promise(resolve=>release=resolve);
   await page.route('**/ui/messages/send',async route=>{await gate;await route.continue()});
   await page.locator('#message-composer').fill(originalText);await page.locator('[data-send-message]').click();
   await until(async()=>await page.locator('[data-pending-send]').count()===1,'local pending bubble');
   const initial=await bounds(page.locator('[data-pending-send]'));
   assert(await page.locator('[data-pending-send-time]').textContent());
   await checkAccessibility(page.locator('[data-pending-send]'),'local pending');
   await page.evaluate(()=>{
    const old=new Set([...document.querySelectorAll('#message-timeline>[data-message-id]')].map(row=>row.dataset.messageId));
    window.sendGeometryFrames=[];
    const sample=()=>{
     const rows=[...document.querySelectorAll('#message-timeline>[data-pending-send],#message-timeline>[data-message-id]')].filter(row=>!old.has(row.dataset.messageId));
     window.sendGeometryFrames.push(rows.map(row=>{
      const box=row.querySelector('.oie-message-body').parentElement.getBoundingClientRect();
      return {x:box.x,y:box.y,width:box.width,height:box.height,rowHeight:row.getBoundingClientRect().height};
     }));
     window.sendGeometryFrame=requestAnimationFrame(sample);
    };sample();
   });
   release();await until(()=>state.messages.at(-1)?.originalText===originalText,'engine acceptance');
   const current=state.messages.at(-1),row=page.locator('[data-message-id="'+current.id+'"]');
   await until(async()=>await row.count()===1&&await page.locator('[data-pending-send]').count()===0,'canonical identity replaces pending');
   for(const phase of ['queued','dispatching','submitted']){
    if(phase!=='queued'){
     current.sendState=phase;current.revisionId=id(10000+state.seq);emit('message.updated',current);
     await until(async()=>await row.getAttribute('data-send-state')===phase,phase);
    }
    const actual=await bounds(row);
    for(const key of Object.keys(initial))assert(Math.abs(actual[key]-initial[key])<1,JSON.stringify({width,mode,libraryTheme,originalText,phase,key,initial,actual}));
    assert.equal(await row.locator('.oie-message-content').evaluate(node=>getComputedStyle(node).opacity),phase==='submitted'?'1':'0.8');
    await checkAccessibility(row,phase);
   }
   const frames=await page.evaluate(()=>{cancelAnimationFrame(window.sendGeometryFrame);return window.sendGeometryFrames});
   for(const frame of frames){
    assert.equal(frame.length,1,'identity handoff never paints a duplicate or missing bubble');
    for(const key of Object.keys(initial))assert(Math.abs(frame[0][key]-initial[key])<1,JSON.stringify({width,phase:'animation frame',key,initial,actual:frame[0]}));
   }
   await page.unroute('**/ui/messages/send');
  }
  assert.deepEqual(errors,[]);
  console.log('PASS: pending, queued, dispatching and submitted preserve bubble position, dimensions and row height at 390px and 1440px, in both modes and library themes, with readable text and timestamps');
 }finally{await context.close()}
};
