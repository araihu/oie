// Isolated engine fixture. Emoji selection must never send a message.
const assert = require('node:assert/strict');
const http = require('node:http');
const path = require('node:path');
const fs = require('node:fs/promises');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require('playwright');
const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
(async () => {
 const engine=id(1), account=id(2), chat=id(3), other=id(4), revision=id(5), streams=new Set();
 const conversations=[{id:chat,title:'Bob'},{id:other,title:'Carol'}].map(item=>({...item,revisionId:revision,accountId:account,address:item.title.toLowerCase()+'@fixture',kind:'direct',lastMessage:'Hello',updatedAt:'2026-11-08T12:00:00Z',unreadCount:0,archived:false,favorite:false}));
 let writes=0;
 const api=http.createServer((req,res)=>{
  const u=new URL(req.url,'http://fixture');
  const send=(code,data)=>{res.writeHead(code,{'Content-Type':'application/json'});res.end(JSON.stringify(data))};
  if(req.method!=='GET'){writes++;return send(405,{status:405})}
  if(u.pathname==='/api/v1/engine')return send(200,{id:engine,role:'hosted',apiVersion:'0.14.0',eventCursor:engine+':0'});
  if(u.pathname==='/api/v1/accounts')return send(200,{items:[{id:account,revisionId:revision,label:'Alice',address:'alice@fixture',protocol:'xmpp',createdAt:'2026-11-08T12:00:00Z'}]});
  if(u.pathname.endsWith('/connection'))return send(200,{accountId:account,state:'connected',updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/history'))return send(200,{accountId:account,state:'complete',retainedMessages:0,conversations:2,updatedAt:'2026-11-08T12:00:00Z'});
  if(u.pathname.endsWith('/capabilities'))return send(200,{accountId:account,state:'available',directText:true,features:[],discovery:{state:'complete',services:[]}});
  if(u.pathname.endsWith('/avatars')||u.pathname.endsWith('/previews')||u.pathname.endsWith('/message-stars')||u.pathname.endsWith('/messages')||u.pathname==='/api/v1/shared-content')return send(200,{items:[]});
  if(u.pathname==='/api/v1/conversations')return send(200,{items:conversations});
  const selected=conversations.find(item=>u.pathname==='/api/v1/conversations/'+item.id);if(selected)return send(200,selected);
  if(u.pathname==='/api/v1/events'){res.writeHead(200,{'Content-Type':'text/event-stream'});res.write(': ready\n\n');streams.add(res);res.on('close',()=>streams.delete(res));return}
  send(404,{status:404});
 });
 api.listen(0,'127.0.0.1');await once(api,'listening');
 const allocator=http.createServer();allocator.listen(0,'127.0.0.1');await once(allocator,'listening');const port=allocator.address().port;await new Promise(resolve=>allocator.close(resolve));
 const binary=process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie');
 const child=spawn(binary,['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});
 let log='';child.stderr.on('data',chunk=>log+=chunk);
 const base='http://127.0.0.1:'+port,browser=await chromium.launch({headless:true}),errors=[],findings=[];
 const until=async(fn,label)=>{for(let n=0;n<180;n++){if(await fn())return;await new Promise(resolve=>setTimeout(resolve,50))}throw Error('Timed out: '+label+'\n'+log)};
 const route=(conversation=chat,locale='en',theme='dark')=>`${base}/?chat=${conversation}&lang=${locale}&theme=${theme}`;
 let active;
 try {
  await until(async()=>{try{return(await fetch(base)).ok}catch{return false}},'BFF ready');
  const context=await browser.newContext({viewport:{width:1180,height:850},locale:'en'}),page=await context.newPage();active=page;
  page.on('pageerror',error=>errors.push(error.message));let reads=0;
  page.on('request',request=>{if(request.url().includes('/ui/emoji/en.json'))reads++});
  await page.goto(route());const input=page.locator('#message-composer'),trigger=page.locator('[data-emoji-trigger]'),search=page.locator('#emoji-search');
  await input.fill('A😀B');await input.evaluate(node=>{node.focus();node.setSelectionRange(1,3);node.dispatchEvent(new Event('select'))});
  const originalKey=await page.locator('[name=operationKey]').first().inputValue(),originalURL=page.url();assert.equal(reads,0,'catalog should load lazily');
  await trigger.click();await search.waitFor({state:'visible'});await until(async()=>await search.evaluate(node=>node===document.activeElement),'search focus');
  const picker=page.getByRole('dialog',{name:'Choose emoji',exact:true});await page.locator('#emoji-grid [data-emoji-pick]').first().waitFor();await picker.evaluate(node=>Promise.all(node.getAnimations().map(animation=>animation.finished.catch(()=>{}))));const fullHeight=await picker.evaluate(node=>node.offsetHeight);
  await page.getByText('No recent emojis yet.',{exact:true}).waitFor();assert(Math.abs(await picker.evaluate(node=>node.offsetHeight)-fullHeight)<2,'empty recent section changed picker height');assert.equal(await page.locator('[data-emoji-category],[role=tablist]').count(),0,'picker uses section navigation rather than filtering tabs');assert.equal(await page.locator('[data-emoji-section]').count(),10);const browseArtifacts=path.join(__dirname,'artifacts');await fs.mkdir(browseArtifacts,{recursive:true});await page.screenshot({path:path.join(browseArtifacts,'emoji-picker-browse-desktop.png')});
  const totalChoices=await page.locator('[data-emoji-section]>.oie-emoji-grid>[data-emoji-pick],[data-emoji-section]>.oie-emoji-grid>[data-emoji-family]').count();
  await page.locator('[data-emoji-jump="8"]').click();await until(async()=>await page.locator('[data-emoji-scroll]').evaluate(node=>node.scrollTop>100),'footer jumps down the long list');assert.equal(await page.locator('[data-emoji-section]>.oie-emoji-grid>[data-emoji-pick],[data-emoji-section]>.oie-emoji-grid>[data-emoji-family]').count(),totalChoices,'footer jump must not filter emojis');assert.equal(await search.inputValue(),'');
  await search.fill('heart');await page.locator('[data-emoji-pick="❤️"]').click();
  assert.equal(await input.inputValue(),'A❤️B');assert(await input.evaluate(node=>node===document.activeElement&&node.selectionStart===3));assert.equal(page.url(),originalURL);assert.equal(await page.locator('[name=operationKey]').first().inputValue(),originalKey);assert.equal(writes,0);assert.equal(reads,1);
  await page.reload();await until(async()=>await input.inputValue()==='A❤️B','emoji draft survives reload');await trigger.focus();await page.keyboard.press('Enter');await search.waitFor({state:'visible'});
  const results=()=>page.locator('#emoji-grid').evaluate(node=>[...node.querySelectorAll('[data-emoji-section]>.oie-emoji-grid')].flatMap(group=>[...group.children].map(child=>child.dataset.emojiFamily||child.dataset.emojiPick)));
  await search.fill('thumbs up');await page.locator('[data-emoji-family="👍"]').waitFor();const englishResults=await results();
  await search.fill('joinha');await page.locator('[data-emoji-family="👍"]').waitFor();assert.deepEqual(await results(),englishResults,'English and Portuguese aliases must resolve to the same families');assert.equal(await page.locator('#emoji-grid .oie-emoji-grid > [data-emoji-family="👍"]').count(),1);assert.equal(await page.locator('#emoji-grid [data-emoji-pick]').count(),1,'tone variants should not repeat in the main grid');
  await page.locator('[data-emoji-jump="1"]').hover();assert(!(await page.locator('[data-emoji-jump="1"]').evaluate(node=>getComputedStyle(node).textDecorationLine)).includes('underline'));
  const primaryThumb=page.locator('[data-emoji-family="👍"] > [data-emoji-pick]');await primaryThumb.hover();const hoverStyle=await primaryThumb.evaluate(node=>({radius:parseFloat(getComputedStyle(node).borderTopLeftRadius),background:getComputedStyle(node).backgroundColor,decoration:getComputedStyle(node).textDecorationLine}));assert(hoverStyle.radius>=6&&hoverStyle.background!=='rgba(0, 0, 0, 0)'&&!hoverStyle.decoration.includes('underline'),'emoji hover uses a rounded background');
  const variantsToggle=page.locator('[data-emoji-family="👍"] [data-emoji-variants-trigger]');await variantsToggle.focus();await page.keyboard.press('Enter');
  const variants=page.getByRole('dialog',{name:'Emoji variants',exact:true});await variants.waitFor({state:'visible'});await variants.evaluate(node=>Promise.all(node.getAnimations().map(animation=>animation.finished.catch(()=>{}))));assert.equal(await variants.locator('[data-emoji-variant]').count(),6);assert.equal(await input.inputValue(),'A❤️B','opening variants must not alter the draft');
  await variants.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const strip=await variants.evaluate(panel=>{const buttons=[...panel.querySelectorAll('[data-emoji-variant]')],origin=panel.closest('[data-emoji-family]').querySelector(':scope>[data-emoji-pick]').getBoundingClientRect(),box=panel.getBoundingClientRect();return{height:box.height,width:box.width,center:box.left+box.width/2,originCenter:origin.left+origin.width/2,above:box.bottom<=origin.top+1,rows:buttons.map(button=>button.getBoundingClientRect().top),pointer:getComputedStyle(panel.querySelector('.oie-emoji-variants-bubble'),'::after').content}});assert(strip.height<80&&strip.width<280&&strip.above&&Math.abs(strip.center-strip.originCenter)<2&&Math.max(...strip.rows)-Math.min(...strip.rows)<1&&strip.pointer!=='none','compact variant strip stays centered above its emoji: '+JSON.stringify(strip));assert.equal(await variants.locator('strong').count(),0,'variant strip has no empty heading');
  const themeStyles=await variants.evaluate(panel=>{const bubble=panel.querySelector('.oie-emoji-variants-bubble'),probe=document.createElement('span');probe.style.backgroundColor='var(--oie-raised)';probe.style.borderRadius='var(--radius-radius)';probe.className='shadow-md';document.body.append(probe);const actual=getComputedStyle(bubble),expected=getComputedStyle(probe),result={background:actual.backgroundColor===expected.backgroundColor,radius:actual.borderRadius===expected.borderRadius,shadow:actual.boxShadow===expected.boxShadow};probe.remove();return result});assert.deepEqual(themeStyles,{background:true,radius:true,shadow:true},'variant strip keeps the active theme tokens');
  const variantArtifacts=path.join(__dirname,'artifacts');await fs.mkdir(variantArtifacts,{recursive:true});await page.screenshot({path:path.join(variantArtifacts,'emoji-variants-desktop.png')});
  await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const variantAxe=await page.evaluate(()=>window.axe.run(document.querySelector('#oie-emoji-picker-panel'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(variantAxe.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[]);
  await page.keyboard.press('Escape');await variants.waitFor({state:'hidden'});assert(await search.isVisible(),'Escape must close only the subpicker');await until(async()=>await variantsToggle.evaluate(node=>node===document.activeElement),'subpicker restores its trigger focus');
  await variantsToggle.click();await variants.locator('[data-emoji-pick="👍🏽"]').hover();assert(!(await variants.locator('[data-emoji-pick="👍🏽"]').evaluate(node=>getComputedStyle(node).textDecorationLine)).includes('underline'));await variants.locator('[data-emoji-pick="👍🏽"]').click();assert((await input.inputValue()).includes('👍🏽'));assert(await input.evaluate(node=>node===document.activeElement));assert.equal(writes,0);
  // Hold the cached opener's deferred work while the user searches and arrows
  // into a result. Releasing that work must preserve the user's focus intent.
  await page.evaluate(()=>{
   const raf=window.requestAnimationFrame,cancel=window.cancelAnimationFrame,pending=new Map();let next=-1;
   window.requestAnimationFrame=callback=>{const id=next--;pending.set(id,callback);return id};
   window.cancelAnimationFrame=id=>pending.delete(id)||cancel(id);
   window.releaseEmojiOpenFrames=()=>{window.requestAnimationFrame=raf;window.cancelAnimationFrame=cancel;const callbacks=[...pending.values()];pending.clear();for(const callback of callbacks)callback(performance.now());return new Promise(resolve=>raf(()=>raf(resolve)))};
  });
  await trigger.click();await search.fill('👍🏽');await search.press('ArrowDown');
  const keyboardChoice=page.locator('#emoji-grid .oie-emoji-grid > [data-emoji-family] > [data-emoji-pick]').first();
  assert(await keyboardChoice.evaluate(node=>node===document.activeElement),'ArrowDown enters the filtered emoji result');
  await page.evaluate(()=>window.releaseEmojiOpenFrames());
  assert(await keyboardChoice.evaluate(node=>node.isConnected&&node===document.activeElement),'cached opener rendering preserves the focused emoji result');
  await page.keyboard.press('Enter');assert((await input.inputValue()).includes('👍🏽'));
  await trigger.click();await search.fill('');await page.locator('[data-emoji-jump=recent]').click();assert(await page.locator('#emoji-section-recent [data-emoji-pick="👍🏽"]').isVisible());await page.locator('#emoji-section-recent [data-emoji-pick="👍🏽"]').hover();await picker.screenshot({path:path.join(__dirname,'artifacts','emoji-recent-hover.png')});await page.keyboard.press('Escape');await until(async()=>await trigger.evaluate(node=>node===document.activeElement),'Escape restores trigger focus');
  await trigger.click();await search.fill('no-such-emoji-qwerty');await page.getByText('No emojis found.',{exact:true}).waitFor({state:'visible'});assert(Math.abs(await picker.evaluate(node=>node.offsetHeight)-fullHeight)<2,'empty search collapsed the picker');await search.fill('');
  await input.evaluate(node=>{node.readOnly=true});await until(async()=>await trigger.isDisabled(),'uncertain send locks picker');await search.waitFor({state:'hidden'});await input.evaluate(node=>{node.readOnly=false});await until(async()=>await trigger.isEnabled(),'unlock');
  await trigger.click();await search.fill('rocket');await page.locator('[data-emoji-pick="🚀"]').waitFor();await page.keyboard.press('Escape');
  const beforeNavigation=await input.inputValue();await page.locator(`a[data-conversation-id="${other}"]`).first().click();await until(async()=>new URL(page.url()).searchParams.get('chat')===other,'HTMX route');assert.equal(await input.inputValue(),'');
  await trigger.click();await search.fill('cat');await page.locator('[data-emoji-pick="🐈"]').click();assert.equal(await input.inputValue(),'🐈');await page.locator(`a[data-conversation-id="${chat}"]`).first().click();await until(async()=>await input.inputValue()===beforeNavigation,'original scoped draft restored');assert.equal(writes,0);
  findings.push('Continuous categorized list, footer scroll-only navigation, global bilingual search, grouped tone variants, no underline, accessible keyboard and scoped drafts without writes');
  await context.close();
  const mobileContext=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR',isMobile:true,hasTouch:true});
  await mobileContext.addInitScript(()=>{for(const name of ['getItem','setItem'])Object.defineProperty(Storage.prototype,name,{configurable:true,value(){throw new DOMException('Denied','SecurityError')}})});
  const mobile=await mobileContext.newPage();active=mobile;mobile.on('pageerror',error=>errors.push(error.message));
  await mobile.goto(route(chat,'pt-BR','light'));await mobile.locator('[data-emoji-trigger]').click();await mobile.locator('#emoji-grid [data-emoji-pick]').first().waitFor();const mobilePicker=mobile.getByRole('dialog',{name:'Escolher emoji',exact:true}),mobileHeight=await mobilePicker.evaluate(async node=>{await Promise.all(node.getAnimations().map(animation=>animation.finished.catch(()=>{})));return node.offsetHeight});
  await mobile.getByText('Nenhum emoji recente ainda.',{exact:true}).waitFor();await mobile.screenshot({path:path.join(__dirname,'artifacts','emoji-picker-browse-mobile.png')});assert(Math.abs(await mobilePicker.evaluate(node=>node.offsetHeight)-mobileHeight)<2,'empty mobile recents collapsed the picker');await mobile.locator('[data-emoji-jump="8"]').tap();assert.equal(await mobile.locator('[data-emoji-section]').count(),10);
  await mobile.locator('#emoji-search').fill('thumbs up');await mobile.locator('[data-emoji-family="👍"]').waitFor();const mobileEnglish=await mobile.locator('#emoji-grid').evaluate(node=>[...node.querySelectorAll('[data-emoji-section]>.oie-emoji-grid')].flatMap(group=>[...group.children].map(child=>child.dataset.emojiFamily||child.dataset.emojiPick)));
  await mobile.locator('#emoji-search').fill('joinha');await mobile.locator('[data-emoji-family="👍"]').waitFor();assert.deepEqual(await mobile.locator('#emoji-grid').evaluate(node=>[...node.querySelectorAll('[data-emoji-section]>.oie-emoji-grid')].flatMap(group=>[...group.children].map(child=>child.dataset.emojiFamily||child.dataset.emojiPick))),mobileEnglish);assert.equal(await mobile.locator('[data-emoji-family="👍"] > [data-emoji-pick]').getAttribute('aria-label'),'polegar para cima');
  await mobile.locator('[data-emoji-family="👍"] [data-emoji-variants-trigger]').tap();const mobileVariants=mobile.getByRole('dialog',{name:'Variações do emoji',exact:true});await mobileVariants.waitFor({state:'visible'});const variantBox=await mobileVariants.boundingBox();assert(variantBox.x>=-1&&variantBox.x+variantBox.width<=391,'subpicker fits mobile viewport');assert(await mobileVariants.evaluate(panel=>{const probe=document.createElement('span');probe.style.backgroundColor='var(--oie-raised)';document.body.append(probe);const same=getComputedStyle(panel.querySelector('.oie-emoji-variants-bubble')).backgroundColor===getComputedStyle(probe).backgroundColor;probe.remove();return same}),'variant strip keeps light theme colors');await mobileVariants.locator('[data-emoji-pick="👍🏻"]').tap();assert.equal(await mobile.locator('#message-composer').inputValue(),'👍🏻');await mobile.locator('#message-composer').fill('');
  await mobile.locator('[data-emoji-trigger]').tap();await mobile.locator('#emoji-search').fill('coracao');await mobile.locator('[data-emoji-pick="❤️"]').waitFor();
  const panel=mobile.getByRole('dialog',{name:'Escolher emoji',exact:true});await panel.waitFor({state:'visible'});const box=await panel.boundingBox();assert(box.x>=-1&&box.x+box.width<=391,'picker fits mobile viewport');
  await mobile.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await mobile.evaluate(()=>window.axe.run(document.querySelector('#oie-emoji-picker-panel'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(axe.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[]);
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await mobile.screenshot({path:path.join(artifacts,'emoji-picker-mobile.png')});
  await mobile.locator('[data-emoji-pick="❤️"]').click();assert.equal(await mobile.locator('#message-composer').inputValue(),'❤️');assert.equal(writes,0);assert.deepEqual(errors,[]);
  findings.push('pt-BR accent-insensitive search, light mobile layout, denied-storage insertion, bilingual aliases, touch variant choice and zero picker WCAG A/AA violations');
  await mobileContext.close();
  const failure=await browser.newPage({viewport:{width:1180,height:850}});active=failure;failure.on('pageerror',error=>errors.push(error.message));let fail=true;
  await failure.route('**/ui/emoji/en.json',route=>fail?route.abort('failed'):route.continue());await failure.goto(route());await failure.locator('[data-emoji-trigger]').click();await failure.getByText('Could not load emojis. Try again.',{exact:true}).waitFor();fail=false;await failure.locator('[data-emoji-retry]').click();await failure.locator('#emoji-search').fill('flag brazil');await failure.locator('[data-emoji-pick="🇧🇷"]').waitFor();assert.equal(writes,0);
  findings.push('Failed local catalog can retry without changing or sending the draft');
  assert.deepEqual(errors,[]);
  await fs.writeFile(path.join(artifacts,'emoji-picker-report.json'),JSON.stringify({findings,unexpectedMutations:writes,pageErrors:errors},null,2));
  console.log('PASS: searchable localized emoji picker, Unicode cursor insertion, drafts, keyboard/mobile accessibility, lifecycle and retry');
 } catch(error) {
  if(active&&!active.isClosed())console.error(await active.evaluate(()=>({url:location.href,status:document.querySelector('[data-emoji-status]')?.textContent,trigger:document.querySelector('[data-emoji-trigger]')?.outerHTML,search:document.querySelector('#emoji-search')?.outerHTML,focus:document.activeElement?.outerHTML.slice(0,300)})));
  throw error;
 } finally {
  await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve));
 }
})().catch(error=>{console.error(error);process.exitCode=1});
