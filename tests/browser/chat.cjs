const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const {chromium}=require('playwright');
const base=process.env.OIE_BROWSER_URL||'http://127.0.0.1:8080';
const casa='019a5df0-0000-7000-8000-000000000102';
(async()=>{
 const browser=await chromium.launch({headless:true});
 const findings=[];
 await fs.mkdir('artifacts',{recursive:true});
 try {
  for(const width of [390,1440]) for(const theme of ['goshtoso','minimal']) for(const mode of ['light','dark']) for(const locale of ['en','pt-BR']) {
   const page=await browser.newPage({viewport:{width,height:960},locale});
   const errors=[];page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text())});
   await page.goto(base+'/?chat='+casa+'&theme='+mode+'&lang='+locale);
   await page.waitForFunction(()=>window.Alpine&&window.htmx);
   await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   assert.equal(await page.locator('main').count(),1);
   assert.equal(await page.locator('h1').count(),1);
   assert.equal(await page.locator('.oie-chat-header h2').textContent(),locale==='en'?'Home':'Casa');
   assert(await page.locator('.oie-preview-note').isVisible());
   assert(await page.locator('#message-composer').isDisabled());
   assert(await page.locator('.oie-composer button').last().isDisabled());
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   const composer=await page.locator('.oie-composer').boundingBox();assert(composer.y+composer.height<=961);
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
   const a11y=await page.evaluate(()=>axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
   assert.deepEqual(a11y.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],[width,theme,mode,locale].join('-'));
   await page.screenshot({path:`artifacts/workspace-${width}-${theme}-${mode}-${locale}.png`,fullPage:true});
   findings.push({width,theme,mode,locale,accessibilityViolations:a11y.violations.length});
   assert.deepEqual(errors,[]);await page.close();
  }
  for(const width of [390,1440]) {
   const page=await browser.newPage({viewport:{width,height:900},locale:'en'});
   await page.goto(base+'/?lang=en&theme=dark');await page.waitForFunction(()=>window.Alpine);
   if(width===390)assert(!(await page.locator('.oie-chat-pane').isVisible()));
   await page.locator('a[data-conversation-id="'+casa+'"]').click();
   await page.waitForURL(new RegExp('chat='+casa));
   await page.locator('.oie-chat-header').waitFor({state:'visible'});
   if(width===390){await page.getByRole('link',{name:'Back to conversations'}).click();await page.locator('.oie-list-pane').waitFor({state:'visible'})}
   await page.goto(base+'/?lang=en&theme=dark');
   await page.waitForFunction(()=>window.OieRuntime?.generation>0);
   const search=page.locator('#conversation-search'),searchGeneration=await page.evaluate(()=>{window.chatSearchBefore={app:document.getElementById('oie-app'),list:document.getElementById('conversation-list'),input:document.getElementById('conversation-search'),composer:document.getElementById('message-composer')};return window.OieRuntime.generation});
   assert.equal(await search.getAttribute('aria-label'),'Search');
   const searched=async value=>page.waitForFunction(value=>{const root=document.querySelector('[data-message-search-root]');return root?.dataset.searchState==='ready'&&root.dataset.searchQuery===value},value);
   await search.fill('Nina Martins');await search.press('Enter');await searched('Nina Martins');
   assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'Nina Martins');assert.equal(new URL(page.url()).searchParams.get('q'),null);
   assert.equal(await page.locator('[data-message-search-conversation]').count(),1);
   assert(await page.locator('[data-message-search-conversations]').getByText('Nina Martins',{exact:true}).isVisible());
   assert(await page.locator('#conversation-list-default').isHidden());
   await search.fill('zz-no-match');await search.press('Enter');await searched('zz-no-match');
   assert.equal(new URL(page.url()).searchParams.get('searchQuery'),'zz-no-match');assert.equal(await page.locator('[data-message-search-conversation]').count(),0);assert.equal(await page.locator('[data-message-search-hit]').count(),0);
   assert(await page.getByText('No matching messages',{exact:true}).isVisible());
   assert(await search.evaluate(node=>document.activeElement===node));
   assert.equal(await page.evaluate(()=>window.OieRuntime.generation),searchGeneration);
   assert(await page.evaluate(()=>window.chatSearchBefore.app===document.getElementById('oie-app')&&window.chatSearchBefore.list===document.getElementById('conversation-list')&&window.chatSearchBefore.input===document.getElementById('conversation-search')&&window.chatSearchBefore.composer===document.getElementById('message-composer')));
   await page.locator('[data-message-search-close]').click();await page.locator('#conversation-list-default').waitFor({state:'visible'});
   assert.equal(await search.inputValue(),'');assert.equal(new URL(page.url()).searchParams.get('searchMode'),null);assert(await search.evaluate(node=>document.activeElement===node));
   await page.goto(base+'/?lang=en&theme=dark');
   await page.locator('[data-filter="unread"]').click();await page.waitForURL(/filter=unread/);assert.equal(await page.locator('.oie-conversations>li').count(),3);
   await page.locator('[data-filter="groups"]').click();await page.waitForURL(/filter=groups/);assert.equal(await page.locator('.oie-conversations>li').count(),5);
   if(width===390)await page.getByRole('button',{name:'Open navigation',exact:true}).click();
   await page.locator('[data-navigation-settings]:visible').click();
   await page.getByRole('dialog',{name:'Settings',exact:true}).waitFor({state:'visible'});
   await page.keyboard.press('Escape');await page.getByRole('dialog',{name:'Settings',exact:true}).waitFor({state:'hidden'});
   if(width===390){
    const trigger=page.getByRole('button',{name:'Open navigation',exact:true});await trigger.click();
    const panel=page.locator('#mobile-navigation-panel');await panel.waitFor({state:'visible'});const box=await panel.boundingBox();assert(box.width>0&&box.height>0&&box.y<900);
    await page.keyboard.press('Escape');await panel.waitFor({state:'hidden'});
   }
   await page.close();
  }
  await fs.writeFile('artifacts/workspace-report.json',JSON.stringify({findings,selection:'passed',search:'passed',filters:'passed',mobileBack:'passed',settings:'passed',mobileNavigation:'passed'},null,2));
  console.log('PASS: mock workspace matrix, selection/search/filter, mobile back, settings, and navigation');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
