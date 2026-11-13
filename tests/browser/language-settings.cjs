// Device language preferences through an isolated, read-only HTTP fixture.
const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
const id=n=>'019a5df0-0000-7000-8000-'+String(n).padStart(12,'0');
(async()=>{
 const streams=new Set();let writes=0;
 const api=http.createServer((req,res)=>{
  const url=new URL(req.url,'http://fixture');
  if(req.method!=='GET'){writes++;res.writeHead(405);return res.end();}
  if(url.pathname==='/api/v1/events'){res.setHeader('Content-Type','text/event-stream');res.write(': ready\n\n');streams.add(res);req.on('close',()=>streams.delete(res));return;}
  res.setHeader('Content-Type','application/json');
  if(url.pathname==='/api/v1/engine')return res.end(JSON.stringify({id:id(1),role:'hosted',apiVersion:'0.17.0',eventCursor:id(1)+':0'}));
  if(url.pathname==='/api/v1/shared-content')return res.end(JSON.stringify({items:[],indexing:{state:'complete',indexedMessages:0,totalMessages:0}}));
  if(['/api/v1/accounts','/api/v1/conversations','/api/v1/starred-messages'].includes(url.pathname))return res.end('{"items":[]}');
  res.writeHead(404);res.end('{"status":404}');
 });api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocation=http.createServer();allocation.listen(0,'127.0.0.1');await once(allocation,'listening');const port=allocation.address().port;await new Promise(r=>allocation.close(r));
 const child=spawn(process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie'),['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:'ignore'});
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],external=[];
 const until=async(fn,label)=>{for(let n=0;n<200;n++){if(await fn())return;await new Promise(r=>setTimeout(r,40));}throw Error(label);};
 const flag={en:'🇺🇸','pt-BR':'🇧🇷'},name={en:'English','pt-BR':'Português do Brasil'};
 let debugPage;
 try{
  await until(async()=>{try{return(await fetch(base)).ok;}catch{return false;}},'BFF starts');
  for(const width of [390,1440])for(const initial of ['en','pt-BR']){
   const context=await browser.newContext({viewport:{width,height:950},locale:initial,hasTouch:width===390,reducedMotion:'reduce'});
   await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():(external.push(route.request().url()),route.abort()));
   const page=await context.newPage();debugPage=page;let documents=0,fragments=0;page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(request.resourceType()==='document')documents++;if(request.headers()['hx-request']==='true')fragments++;});
   const parameters={section:width===390?'chats':'media',account:id(2),filter:'unread',q:'Preserve café 😀',theme:'dark'};
   await page.goto(base+'/?'+new URLSearchParams(parameters));await page.waitForFunction(()=>window.OieRuntime?.generation>0);
   assert.equal(await page.locator('html').getAttribute('lang'),initial,'browser negotiation');assert.equal((await context.cookies()).find(cookie=>cookie.name==='oie_lang'),undefined,'initial negotiation must not become explicit override');
   const openSettings=async()=>{if(width===390)await page.getByRole('button',{name:(await page.locator('html').getAttribute('lang'))==='en'?'Open navigation':'Abrir navegação',exact:true}).click();await page.locator('[data-navigation-settings]:visible').click();await page.getByRole('dialog',{name:(await page.locator('html').getAttribute('lang'))==='en'?'Settings':'Configurações',exact:true}).waitFor({state:'visible'});};
   await openSettings();const trigger=page.locator('[data-language-trigger]'),value=page.locator('#settings-language input[name="lang"]');
   await trigger.waitFor({state:'visible'});assert.equal(await value.inputValue(),initial);
   const verifyLeading=async(node,locale)=>{
    const label=node.locator('[data-combobox-option-label],[data-combobox-trigger-label-outer]');
    assert.equal(await label.textContent(),name[locale]);
    const content=await label.evaluate(el=>getComputedStyle(el,'::before').content);
    assert(content.includes(flag[locale])&&content.endsWith('/ ""'),content);
   };
   await verifyLeading(trigger,initial);await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});await trigger.focus();await trigger.press('ArrowDown');
   for(const locale of ['en','pt-BR']){const option=page.getByRole('option',{name:name[locale],exact:true});await option.waitFor({state:'visible'});await verifyLeading(option,locale);assert.equal(await option.getAttribute('aria-selected'),String(locale===initial));}
   const openAxe=await page.getByRole('dialog',{name:initial==='en'?'Settings':'Configurações',exact:true}).evaluate(node=>axe.run(node,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(openAxe.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[],'open language list is accessible');
   const search=page.getByRole('textbox',{name:initial==='en'?'Search languages':'Buscar idiomas',exact:true});
   await search.fill('no matching language');await page.locator('#settings-language').getByText(initial==='en'?'No languages found':'Nenhum idioma encontrado',{exact:true}).waitFor();
   await search.fill('');await search.press('Escape');
   await until(async()=>await trigger.getAttribute('aria-expanded')==='false'&&await trigger.evaluate(node=>node===document.activeElement),'Escape closes only the picker and returns focus');
   await trigger.press('ArrowDown');
   const beforeSame=fragments;await page.getByRole('option',{name:name[initial],exact:true}).focus();await page.keyboard.press('Enter');await page.waitForTimeout(180);assert.equal(fragments,beforeSame,'same-value choice must not navigate');assert.equal((await context.cookies()).find(cookie=>cookie.name==='oie_lang'),undefined);
   await page.evaluate(()=>window.languageDocumentToken=Math.random());const token=await page.evaluate(()=>window.languageDocumentToken),next=initial==='en'?'pt-BR':'en',beforeChange=fragments;
   let release,requestStarted;
   const held=new Promise(resolve=>{requestStarted=resolve;}),response=new Promise(resolve=>{release=resolve;});
   const languageRoute=url=>url.searchParams.get('settingsFocus')==='language';
   await page.route(languageRoute,async route=>{
    requestStarted();
    if(await response==='fail')await route.abort();
    else await route.continue();
   });
   const selectNext=async()=>{
    await trigger.focus();await trigger.press('ArrowDown');await search.fill(name[next]);
    assert.equal(await page.locator('#settings-language').getByRole('option').count(),1);
    await search.press('ArrowDown');
    await until(()=>page.getByRole('option',{name:name[next],exact:true}).evaluate(node=>node===document.activeElement),'filtered option receives keyboard focus');
    await page.keyboard.press('Enter');
   };
   await selectNext();
   await held;
   await page.evaluate(()=>{
    const nodes=['oie-app','settings-language-trigger','settings-time-zone-trigger'].map(id=>document.getElementById(id));
    const geometry=()=>nodes.flatMap(node=>{const {x,y,width,height}=node.getBoundingClientRect();return [x,y,width,height]});
    const before=geometry();
    window.languageDrift=new Promise(resolve=>{
     let frames=0,max=0;
     const sample=()=>{
      max=Math.max(max,...geometry().map((value,index)=>Math.abs(value-before[index])));
      if(++frames===6)resolve(max);else requestAnimationFrame(sample);
     };
     requestAnimationFrame(sample);
    });
   });
   assert.equal(await page.evaluate(()=>window.languageDrift),0,'delayed language fragment preserves settings geometry');
   assert.equal(await value.inputValue(),initial,'pending selection preserves the applied language');
   const failed=page.waitForEvent('requestfailed',request=>languageRoute(new URL(request.url())));
   release('fail');
   await failed;
   assert.equal(await page.locator('html').getAttribute('lang'),initial);
   assert.equal(await value.inputValue(),initial);
   await page.unroute(languageRoute);
   await selectNext();
   await until(async()=>await page.locator('html').getAttribute('lang')===next&&await value.inputValue()===next,'language fragment applied');
   await page.getByRole('dialog',{name:next==='en'?'Settings':'Configurações',exact:true}).waitFor({state:'visible'});await until(async()=>page.locator('[data-language-trigger]').evaluate(node=>document.activeElement===node),'language control regains focus');
   assert.equal(await page.evaluate(()=>window.languageDocumentToken),token);assert.equal(documents,1,'locale change preserves document');assert.equal(fragments,beforeChange+2,'one fragment per language attempt');
   for(const [key,expected]of Object.entries(parameters))assert.equal(new URL(page.url()).searchParams.get(key),expected,'preserve '+key);
   assert.equal(new URL(page.url()).searchParams.get('lang'),next);assert.equal(new URL(page.url()).searchParams.get('open'),'settings');assert.equal((await context.cookies()).find(cookie=>cookie.name==='oie_lang')?.value,next);await verifyLeading(page.locator('[data-language-trigger]'),next);
   assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));const result=await page.getByRole('dialog',{name:next==='en'?'Settings':'Configurações',exact:true}).evaluate(node=>axe.run(node,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(result.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[]);
   await page.goto(base+'/?section=chats');await page.waitForFunction(()=>window.OieRuntime?.generation>0);assert.equal(await page.locator('html').getAttribute('lang'),next,'explicit device cookie overrides browser language after reload');await openSettings();assert.equal(await value.inputValue(),next);await context.close();
  }
  assert.equal(writes,0);assert.deepEqual(errors,[]);assert.deepEqual(external,[]);console.log('PASS: language search, keyboard and Escape, flags, delayed and failed requests, HTMX route and focus preservation, cookie persistence and EN/PT accessibility');
 }catch(error){if(debugPage&&!debugPage.isClosed())console.error('Language selector evidence',await debugPage.evaluate(()=>({url:location.href,lang:document.documentElement.lang,focus:document.activeElement?.outerHTML.slice(0,250),language:document.querySelector('#settings-language')?.value,trigger:document.querySelector('[data-language-trigger]')?.outerHTML.slice(0,1200)})).catch(()=>({unavailable:true})));throw error;}finally{await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(r=>api.close(r));}
})().catch(error=>{console.error(error);process.exitCode=1;});
