const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
const {chromium}=require('playwright');
(async()=>{
 const base=process.env.OIE_BROWSER_URL||'http://127.0.0.1:8080';
 const browser=await chromium.launch({headless:true});
 const findings=[];const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});
 try {
  for(const width of [390,1440])for(const theme of ['goshtoso','minimal'])for(const mode of ['light','dark'])for(const locale of ['en','pt-BR']){
   const page=await browser.newPage({viewport:{width,height:900},locale});const errors=[];
   page.on('pageerror',e=>errors.push(e.message));page.on('console',m=>{if(m.type()==='error')errors.push(m.text())});
   await page.goto(base+'/?lang='+locale+'&theme='+mode+'&q=unmatched&filter=unread');
   await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
   const trigger=page.locator('[data-account-switcher]:visible');assert.equal(await trigger.count(),1);
   await trigger.focus();await page.keyboard.press('Enter');
   const label=locale==='en'?'Switch account':'Trocar de conta';const panel=page.getByRole('dialog',{name:label,exact:true});
   await panel.waitFor({state:'visible'});await page.waitForTimeout(170);
   assert.equal(await trigger.getAttribute('aria-expanded'),'true');
   const box=await panel.boundingBox();assert(box.x>=0&&box.y>=0&&box.x+box.width<=width+1&&box.y+box.height<=900);
   assert.equal(await panel.locator('[data-switch-account][aria-current=true]').count(),1);
   const account=panel.locator('[data-switch-account]').filter({hasText:'Personal'});const id=await account.getAttribute('data-switch-account');
   assert.equal(await account.locator('[data-account-unread]').textContent(),'6','totals must ignore current empty search');
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
   const axe=await page.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
   assert.deepEqual(axe.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],`${width}-${theme}-${mode}-${locale}`);
   await page.screenshot({path:path.join(artifacts,`account-switcher-${width}-${theme}-${mode}-${locale}.png`)});
   await page.keyboard.press('Escape');await panel.waitFor({state:'hidden'});assert(await trigger.evaluate(e=>e===document.activeElement));
   await trigger.click();await account.click();await page.waitForURL(u=>u.searchParams.get('account')===id);
   assert.equal(new URL(page.url()).searchParams.get('q'),'unmatched');assert.equal(new URL(page.url()).searchParams.get('chat'),null);
   assert((await page.locator('[data-account-switcher]:visible').textContent()).includes('Personal'));
   await page.locator('[data-account-switcher]:visible').click();
   const activePanel=page.getByRole('dialog',{name:label,exact:true});assert.equal(await activePanel.locator('[data-switch-account][aria-current=true]').getAttribute('data-switch-account'),id);
   const checkScreen=async screen=>{
    await page.evaluate(t=>document.documentElement.dataset.theme=t,theme);
    const drawer=page.getByRole('dialog',{name:locale==='en'?'Accounts':'Contas',exact:true});await drawer.waitFor({state:'visible'});await page.waitForFunction(()=>{const box=document.querySelector('.oie-account-drawer').getBoundingClientRect();return box.x>=-1&&box.right<=innerWidth+1});
    const box=await drawer.boundingBox();assert(box.x>=-1&&box.x+box.width<=width+1);
    await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
    const result=await page.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
    assert.deepEqual(result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})),[],screen+' '+width+' '+mode+' '+locale);
    await page.screenshot({path:path.join(artifacts,`accounts-${screen}-${width}-${theme}-${mode}-${locale}.png`)});
   };
   await activePanel.locator('[data-open-accounts]').click();await page.locator('#account-overview').waitFor({state:'visible'});assert.equal(await page.locator('[data-connect-account]').count(),0);await checkScreen('list');
   await page.locator('[data-manage-account="'+id+'"]').click();await page.locator('[data-connect-account]').waitFor({state:'visible'});assert.equal(await page.locator('[data-connect-account]').count(),1);await checkScreen('detail');
   const endpoint=page.locator('[data-connect-account] [name=endpoint]');assert(!(await endpoint.isVisible()));await page.getByRole('button',{name:locale==='en'?'Advanced connection settings':'Configurações avançadas',exact:true}).click();await endpoint.waitFor({state:'visible'});
   await page.locator('[data-account-back]').click();await page.locator('#account-overview').waitFor({state:'visible'});
   await page.keyboard.press('Escape');
   await page.locator('[data-account-switcher]:visible').click();await page.locator('[data-add-account]:visible').click();await page.locator('#register-account-form').waitFor({state:'visible'});assert.equal(await page.locator('[data-connect-account]').count(),0);await checkScreen('add');
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));assert.deepEqual(errors,[]);
   findings.push({width,theme,mode,locale,unread:6,accessibilityViolations:0});await page.close();
  }
  await fs.writeFile(path.join(artifacts,'account-switcher-report.json'),JSON.stringify({findings,keyboard:'Escape restores trigger focus',navigation:'account URL, profile and selected row agree; filters retained',consoleErrors:[]},null,2));
  console.log('PASS: account profile popover16 variants, bounds, keyboard, unread badges, navigation and account actions');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
