// Isolated BFF settings navigation. No real account or protocol server is used.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id='019a5df0-0000-7000-8000-000000000001';
(async()=>{
 const streams=new Set();
 let engineAvailable = true;
 const api=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  if (!engineAvailable) {
    res.writeHead(503);
    return res.end();
  }
  if(req.method!=='GET'){res.writeHead(405);return res.end();}
  if(url.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);req.on('close',()=>streams.delete(res));return;}
  res.setHeader('Content-Type','application/json');
  if(url.pathname==='/api/v1/engine')return res.end(JSON.stringify({id,role:'hosted',apiVersion:'0.16.0',eventCursor:id+':0'}));
  if(url.pathname==='/api/v1/shared-content')return res.end(JSON.stringify({items:[],indexing:{state:'complete',indexedMessages:0,totalMessages:0}}));
  if(['/api/v1/accounts','/api/v1/conversations','/api/v1/starred-messages'].includes(url.pathname))return res.end('{"items":[]}');
  res.writeHead(404);res.end('{"status":404}');
 });api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:'ignore'});
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[];
 const until=async(fn,label)=>{for(let n=0;n<150;n++){if(await fn())return;await new Promise(r=>setTimeout(r,40));}throw Error(label);};
 try{
  await until(async()=>{try{return(await fetch(base)).ok;}catch{return false;}},'BFF starts');
  for(const width of [390,1440])for(const locale of ['en','pt-BR'])for(const section of ['chats','media','archived','favorites']){
   const context=await browser.newContext({viewport:{width,height:900},locale,hasTouch:width===390,reducedMotion:'reduce'});
   const page=await context.newPage();page.on('pageerror',error=>errors.push(error.message));
   const response=await page.goto(`${base}/?section=${section}&lang=${locale}`);
   if(section==='chats'){
    const renderedListbox=(await response.text()).match(/<ul\b[^>]*\bid="emoji-shortcut-languages-options"[^>]*>/)?.[0] ?? '';
    assert.match(renderedListbox,/\baria-multiselectable="true"/,'the HTML response exposes multi-select before JavaScript runs');
   }
   await page.waitForFunction(()=>window.OieRuntime?.generation>0);
   assert.equal(await page.locator('[data-shared-settings],[data-footer-settings]').count(),0,'no duplicate settings shortcuts');
   if(width===390){assert.equal(await page.locator('[data-navigation-settings]:visible').count(),0);await page.getByRole('button',{name:locale==='en'?'Open navigation':'Abrir navegação',exact:true}).click();}
   const entry=page.locator('[data-navigation-settings]:visible');await entry.waitFor({state:'visible'});assert.equal(await entry.count(),1);
   const position=await entry.evaluate(node=>{const footer=node.closest('[data-navigation-footer]'),a=node.getBoundingClientRect(),b=footer.getBoundingClientRect();return{hasAccount:!!footer.querySelector('[data-account-switcher]'),inside:a.left>=b.left&&a.right<=b.right+1&&a.top>=b.top&&a.bottom<=b.bottom+1};});
   assert.deepEqual(position,{hasAccount:true,inside:true});
   await page.evaluate(()=>window.settingsDocumentToken={});const token=await page.evaluateHandle(()=>window.settingsDocumentToken);
   await entry.focus();await entry.press(width===390?'Space':'Enter');
   const dialog=page.locator('[role=dialog]').filter({has:page.locator('#settings-body')});await dialog.waitFor({state:'visible'});
   assert(await page.evaluate(token=>token===window.settingsDocumentToken,token),'opening settings preserves document');
   if(width===390)await page.locator('#mobile-navigation-panel').waitFor({state:'hidden'});
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.addScriptTag({path:require.resolve('./fixtures/axe-core/axe.min.js')});
   const audit=()=>dialog.evaluate(async node=>(await axe.run(node,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}})).violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)})));
   if(section==='chats'){
    const pt=locale==='pt-BR';
    const device=dialog.locator('[data-settings-target=device]:visible');
    const general=dialog.locator('[data-settings-page=general]');
    const groups=general.getByRole('region');
    assert.deepEqual(await groups.getByRole('heading',{level:3}).allTextContents(),pt?
      ['Geral','Dispositivo e motor','Segurança e suporte']:['General','Device and engine','Security and support']);
    assert.deepEqual(await groups.evaluateAll(nodes=>nodes.map(node=>getComputedStyle(node).borderBlockStartWidth)),['0px','1px','1px']);
    assert(await groups.nth(1).locator('[data-settings-target=device]').isVisible());
    assert(await groups.nth(2).locator('[data-drawer-child=about]').isVisible());
    const languages=dialog.getByRole('combobox',{name:pt?'Idiomas dos atalhos de emoji':'Emoji shortcut languages'});
    await languages.click();
    const languageOptions=dialog.getByRole('listbox');
    await languageOptions.waitFor({state:'visible'});
    assert.equal(await languageOptions.getAttribute('aria-multiselectable'),'true');
    assert.equal(await languageOptions.locator('input[type=checkbox]').count(),2);
    assert.equal(await languageOptions.getByRole('checkbox').count(),0);
    const search=dialog.getByRole('textbox',{name:pt?'Buscar idiomas':'Search languages'});
    await search.fill('Portugu');
    assert.deepEqual(await audit(),[]);
    assert.equal(await dialog.getByRole('option').count(),1);
    assert.equal(await dialog.getByRole('option',{name:'Português do Brasil'}).getAttribute('aria-selected'),'true');
    const box=await search.boundingBox();
    assert(box.x>=0&&box.x+box.width<=width+1);
    await search.press('Escape');
    await search.waitFor({state:'hidden'});
    assert(await dialog.isVisible());
    await device.press('Enter');
    await dialog.locator('[data-settings-target=display]:visible').click();
    await dialog.locator('label[for="auto-image-downloads"]').waitFor({state:'visible'});
    const deviceBack=dialog.getByRole('button',{name:pt?'Voltar às configurações deste dispositivo':'Back to device settings',exact:true});
    await deviceBack.press('Enter');
    assert(await dialog.locator('[data-settings-target=display]:visible').evaluate(node=>node===document.activeElement));
    await dialog.getByRole('button',{name:pt?'Voltar às configurações':'Back to settings',exact:true}).press('Enter');
    assert(await device.evaluate(node=>node===document.activeElement));
    await dialog.locator('[data-drawer-back]').waitFor({state:'hidden'});

   }
   assert.deepEqual(await audit(),[]);
   await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'});await context.close();
  }
  engineAvailable = false;
  const offline = await browser.newPage();
  await offline.goto(base + '/settings/retention?lang=pt-BR');
  const settings = offline.getByRole('dialog', {name: 'Configurações', exact: true});
  await settings.locator('[data-settings-page=general]').waitFor();
  assert.equal(await settings.locator('[data-settings-target=engine]').count(), 0);
  await offline.close();
  assert.deepEqual(errors,[]);console.log('PASS: settings navigation, keyboard, accessibility and unavailable engine fallback');
 }finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
