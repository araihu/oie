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
  await page.goto(route());const input=page.locator('#message-composer'),trigger=page.locator('#oie-emoji-picker [data-emoji-trigger]'),search=page.locator('#emoji-search');
  await input.fill('A😀B');await input.evaluate(node=>{node.focus();node.setSelectionRange(1,3);node.dispatchEvent(new Event('select'))});
  const originalKey=await page.locator('#send-message-form [name=operationKey]').inputValue(),originalURL=page.url();assert.equal(reads,0,'catalog should load lazily');
  await trigger.click();await search.waitFor({state:'visible'});await until(async()=>await search.evaluate(node=>node===document.activeElement),'search focus');
  const picker=page.getByRole('dialog',{name:'Choose emoji',exact:true});await page.locator('#emoji-grid [data-emoji-pick]').first().waitFor();await picker.evaluate(node=>Promise.all(node.getAnimations().map(animation=>animation.finished.catch(()=>{}))));const fullHeight=await picker.evaluate(node=>node.offsetHeight);
  await page.getByText('No recent emojis yet.',{exact:true}).waitFor();assert(Math.abs(await picker.evaluate(node=>node.offsetHeight)-fullHeight)<2,'empty recent section changed picker height');assert.equal(await page.locator('#oie-emoji-picker [data-emoji-category],#oie-emoji-picker [role=tablist]').count(),0,'picker uses section navigation rather than filtering tabs');assert.equal(await page.locator('#oie-emoji-picker [data-emoji-section]').count(),10);const browseArtifacts=path.join(__dirname,'artifacts');await fs.mkdir(browseArtifacts,{recursive:true});await page.screenshot({path:path.join(browseArtifacts,'emoji-picker-browse-desktop.png')});
  const footer=picker.locator('.oie-emoji-footer');
  const symbols=footer.locator('[data-emoji-jump="7"]');
  await symbols.hover();
  const footerGeometry=await footer.locator('[data-emoji-jump]').evaluateAll(buttons=>buttons.map(button=>{
   const bounds=button.getBoundingClientRect(),glyph=button.firstElementChild,glyphBounds=glyph.getBoundingClientRect();
   return {category:button.dataset.emojiJump,width:bounds.width,height:bounds.height,radius:getComputedStyle(button).borderTopLeftRadius,
    x:glyphBounds.x+glyphBounds.width/2-bounds.x-bounds.width/2,y:glyphBounds.y+glyphBounds.height/2-bounds.y-bounds.height/2};
  }));
  assert.equal(footerGeometry.length,10);
  for(const icon of footerGeometry){
   assert.equal(icon.width,28);assert.equal(icon.height,28);assert.equal(icon.radius,'50%');
   assert(Math.abs(icon.x)<.1,'category glyph is horizontally centered');
   assert(Math.abs(icon.y-(icon.category==='4'?-3.4:0))<.1,'category glyph has the expected optical alignment');
  }
  for(const emoji of ['😀','🚗']){
   await search.fill(emoji);
   const button=picker.locator('[data-emoji-pick='+JSON.stringify(emoji)+']').first();
   await button.waitFor({state:'visible'});
   const glyph=button.locator('.oie-emoji-glyph');
   await until(()=>glyph.evaluate(node=>node.style.translate!==''),'visible glyph alignment');
   const bounds=await button.boundingBox();
   await button.hover();
   assert.deepEqual(await button.boundingBox(),bounds,'emoji hover preserves the cell geometry');
   const screenshot=await button.screenshot();
   const offset=await page.evaluate(async base64=>{
    const bytes=Uint8Array.from(atob(base64),char=>char.charCodeAt(0));
    const picture=await createImageBitmap(new Blob([bytes],{type:'image/png'}));
    const canvas=document.createElement('canvas');
    const width=canvas.width=picture.width,height=canvas.height=picture.height;
    const paint=canvas.getContext('2d');paint.drawImage(picture,0,0);
    const pixels=paint.getImageData(0,0,width,height).data;
    let left=width,right=-1,top=height,bottom=-1;
    for(let y=0;y<height;y++)for(let x=0;x<width;x++){
     const i=(y*width+x)*4;
     if(Math.max(pixels[i],pixels[i+1],pixels[i+2])<75)continue;
     left=Math.min(left,x);right=Math.max(right,x);
     top=Math.min(top,y);bottom=Math.max(bottom,y);
    }
    return {x:(left+right+1-width)/2,y:(top+bottom+1-height)/2,painted:right>=0};
   },screenshot.toString('base64'));
   assert(offset.painted&&Math.abs(offset.x)<=1.5&&Math.abs(offset.y)<=1.5,
    emoji+' is centered in the hover highlight: '+JSON.stringify(offset));
  }
  await search.fill('');
  const totalChoices=await page.locator('#oie-emoji-picker [data-emoji-section]>.oie-emoji-grid>[data-emoji-pick],[data-emoji-section]>.oie-emoji-grid>[data-emoji-family]').count();
  await page.locator('#oie-emoji-picker [data-emoji-jump="8"]').click();await until(async()=>await page.locator('#oie-emoji-picker [data-emoji-scroll]').evaluate(node=>node.scrollTop>100),'footer jumps down the long list');assert.equal(await page.locator('#oie-emoji-picker [data-emoji-section]>.oie-emoji-grid>[data-emoji-pick],[data-emoji-section]>.oie-emoji-grid>[data-emoji-family]').count(),totalChoices,'footer jump must not filter emojis');assert.equal(await search.inputValue(),'');
  await search.fill('heart');await page.locator('#oie-emoji-picker [data-emoji-pick="❤️"]').click();
  assert.equal(await input.inputValue(),'A❤️B');assert(await input.evaluate(node=>node===document.activeElement&&node.selectionStart===3));assert.equal(page.url(),originalURL);assert.equal(await page.locator('#send-message-form [name=operationKey]').inputValue(),originalKey);assert.equal(writes,0);assert.equal(reads,1);
  await page.reload();await until(async()=>await input.inputValue()==='A❤️B','emoji draft survives reload');await trigger.focus();await page.keyboard.press('Enter');await search.waitFor({state:'visible'});
  const results=()=>page.locator('#emoji-grid').evaluate(node=>[...node.querySelectorAll('[data-emoji-section]>.oie-emoji-grid')].flatMap(group=>[...group.children].map(child=>child.dataset.emojiFamily||child.dataset.emojiPick)));
  await search.fill('thumbs up');await page.locator('#oie-emoji-picker [data-emoji-family="👍"]').waitFor();const englishResults=await results();
  await search.fill('joinha');await page.locator('#oie-emoji-picker [data-emoji-family="👍"]').waitFor();assert.deepEqual(await results(),englishResults,'English and Portuguese aliases must resolve to the same families');assert.equal(await page.locator('#emoji-grid .oie-emoji-grid > [data-emoji-family="👍"]').count(),1);assert.equal(await page.locator('#emoji-grid [data-emoji-pick]').count(),1,'tone variants should not repeat in the main grid');
  await page.locator('#oie-emoji-picker [data-emoji-jump="1"]').hover();assert(!(await page.locator('#oie-emoji-picker [data-emoji-jump="1"]').evaluate(node=>getComputedStyle(node).textDecorationLine)).includes('underline'));
  const primaryThumb=page.locator('#oie-emoji-picker [data-emoji-family="👍"] > [data-emoji-pick]');await primaryThumb.hover();const hoverStyle=await primaryThumb.evaluate(node=>({radius:parseFloat(getComputedStyle(node).borderTopLeftRadius),background:getComputedStyle(node).backgroundColor,decoration:getComputedStyle(node).textDecorationLine}));assert(hoverStyle.radius>=6&&hoverStyle.background!=='rgba(0, 0, 0, 0)'&&!hoverStyle.decoration.includes('underline'),'emoji hover uses a rounded background');
  const variantsToggle=page.locator('#oie-emoji-picker [data-emoji-family="👍"] [data-emoji-variants-trigger]');await variantsToggle.focus();await page.keyboard.press('Enter');
  const variants=page.getByRole('dialog',{name:'Emoji variants',exact:true});await variants.waitFor({state:'visible'});await variants.evaluate(node=>Promise.all(node.getAnimations().map(animation=>animation.finished.catch(()=>{}))));assert.equal(await variants.locator('[data-emoji-variant]').count(),6);assert.equal(await input.inputValue(),'A❤️B','opening variants must not alter the draft');
  await variants.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  const strip=await variants.evaluate(panel=>{const buttons=[...panel.querySelectorAll('[data-emoji-variant]')],origin=panel.closest('[data-emoji-family]').querySelector(':scope>[data-emoji-pick]').getBoundingClientRect(),box=panel.getBoundingClientRect();return{height:box.height,width:box.width,center:box.left+box.width/2,originCenter:origin.left+origin.width/2,above:box.bottom<=origin.top+1,rows:buttons.map(button=>button.getBoundingClientRect().top),pointer:getComputedStyle(panel.querySelector('.oie-emoji-variants-bubble'),'::after').content}});assert(strip.height<80&&strip.width<280&&strip.above&&Math.abs(strip.center-strip.originCenter)<2&&Math.max(...strip.rows)-Math.min(...strip.rows)<1&&strip.pointer!=='none','compact variant strip stays centered above its emoji: '+JSON.stringify(strip));assert.equal(await variants.locator('strong').count(),0,'variant strip has no empty heading');
  const themeStyles=await variants.evaluate(panel=>{const bubble=panel.querySelector('.oie-emoji-variants-bubble'),probe=document.createElement('span');probe.style.backgroundColor='var(--oie-raised)';probe.style.borderRadius='var(--radius-radius)';probe.className='shadow-md';document.body.append(probe);const actual=getComputedStyle(bubble),expected=getComputedStyle(probe),result={background:actual.backgroundColor===expected.backgroundColor,radius:actual.borderRadius===expected.borderRadius,shadow:actual.boxShadow===expected.boxShadow};probe.remove();return result});assert.deepEqual(themeStyles,{background:true,radius:true,shadow:true},'variant strip keeps the active theme tokens');
  const variantArtifacts=path.join(__dirname,'artifacts');await fs.mkdir(variantArtifacts,{recursive:true});await page.screenshot({path:path.join(variantArtifacts,'emoji-variants-desktop.png')});
  await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const variantAxe=await page.evaluate(()=>window.axe.run(document.querySelector('#oie-emoji-picker-panel'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(variantAxe.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[]);
  await page.keyboard.press('Escape');await variants.waitFor({state:'hidden'});assert(await search.isVisible(),'Escape must close only the subpicker');await until(async()=>await variantsToggle.evaluate(node=>node===document.activeElement),'subpicker restores its trigger focus');
  await variantsToggle.click();await variants.locator('[data-emoji-pick="👍🏽"]').hover();assert(!(await variants.locator('[data-emoji-pick="👍🏽"]').evaluate(node=>getComputedStyle(node).textDecorationLine)).includes('underline'));await variants.locator('[data-emoji-pick="👍🏽"]').click();assert((await input.inputValue()).includes('👍🏽'));assert(await input.evaluate(node=>node===document.activeElement));assert.equal(writes,0);
  // Hold the cached opener's deferred work after the public popover has
  // positioned. Freezing the library's opening frames would keep it hidden.
  await trigger.click();await search.waitFor({state:'visible'});
  await page.evaluate(()=>{
   const raf=window.requestAnimationFrame,cancel=window.cancelAnimationFrame,pending=new Map();let next=-1;
   window.requestAnimationFrame=callback=>{const id=next--;pending.set(id,callback);return id};
   window.cancelAnimationFrame=id=>pending.delete(id)||cancel(id);
   window.releaseEmojiOpenFrames=()=>{window.requestAnimationFrame=raf;window.cancelAnimationFrame=cancel;const callbacks=[...pending.values()];pending.clear();for(const callback of callbacks)callback(performance.now());return new Promise(resolve=>raf(()=>raf(resolve)))};
  });
  await trigger.press('ArrowDown');await search.fill('👍🏽');await search.press('ArrowDown');
  const keyboardChoice=page.locator('#emoji-grid .oie-emoji-grid > [data-emoji-family] > [data-emoji-pick]').first();
  assert(await keyboardChoice.evaluate(node=>node===document.activeElement),'ArrowDown enters the filtered emoji result');
  await page.evaluate(()=>window.releaseEmojiOpenFrames());
  assert(await keyboardChoice.evaluate(node=>node.isConnected&&node===document.activeElement),'cached opener rendering preserves the focused emoji result');
  await page.keyboard.press('Enter');assert((await input.inputValue()).includes('👍🏽'));
  await trigger.click();await search.fill('');await page.locator('#oie-emoji-picker [data-emoji-jump=recent]').click();assert(await page.locator('#emoji-section-recent [data-emoji-pick="👍🏽"]').isVisible());await page.locator('#emoji-section-recent [data-emoji-pick="👍🏽"]').hover();await picker.screenshot({path:path.join(__dirname,'artifacts','emoji-recent-hover.png')});await page.keyboard.press('Escape');await until(async()=>await trigger.evaluate(node=>node===document.activeElement),'Escape restores trigger focus');
  await trigger.click();await search.fill('no-such-emoji-qwerty');await page.getByText('No emojis found.',{exact:true}).waitFor({state:'visible'});assert(Math.abs(await picker.evaluate(node=>node.offsetHeight)-fullHeight)<2,'empty search collapsed the picker');await search.fill('');
  await input.evaluate(node=>{node.readOnly=true});await until(async()=>await trigger.isDisabled(),'uncertain send locks picker');await search.waitFor({state:'hidden'});await input.evaluate(node=>{node.readOnly=false});await until(async()=>await trigger.isEnabled(),'unlock');
  await trigger.click();await search.fill('rocket');await page.locator('#oie-emoji-picker [data-emoji-pick="🚀"]').waitFor();await page.keyboard.press('Escape');
  const beforeNavigation=await input.inputValue();await page.locator(`a[data-conversation-id="${other}"]`).first().click();await until(async()=>new URL(page.url()).searchParams.get('chat')===other,'HTMX route');assert.equal(await input.inputValue(),'');
  await trigger.click();await search.fill('cat');await page.locator('#oie-emoji-picker [data-emoji-pick="🐈"]').click();assert.equal(await input.inputValue(),'🐈');await page.locator(`a[data-conversation-id="${chat}"]`).first().click();await until(async()=>await input.inputValue()===beforeNavigation,'original scoped draft restored');assert.equal(writes,0);
  findings.push('Continuous categorized list, footer scroll-only navigation, global bilingual search, grouped tone variants, no underline, accessible keyboard and scoped drafts without writes');
  await context.close();
  const mobileContext=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR',isMobile:true,hasTouch:true});
  await mobileContext.addInitScript(()=>{for(const name of ['getItem','setItem'])Object.defineProperty(Storage.prototype,name,{configurable:true,value(){throw new DOMException('Denied','SecurityError')}})});
  const mobile=await mobileContext.newPage();active=mobile;mobile.on('pageerror',error=>errors.push(error.message));
  await mobile.goto(route(chat,'pt-BR','light'));await mobile.locator('#oie-emoji-picker [data-emoji-trigger]').click();await mobile.locator('#emoji-grid [data-emoji-pick]').first().waitFor();const mobilePicker=mobile.getByRole('dialog',{name:'Escolher emoji',exact:true}),mobileHeight=await mobilePicker.evaluate(async node=>{await Promise.all(node.getAnimations().map(animation=>animation.finished.catch(()=>{})));return node.offsetHeight});
  await mobile.getByText('Nenhum emoji recente ainda.',{exact:true}).waitFor();await mobile.screenshot({path:path.join(__dirname,'artifacts','emoji-picker-browse-mobile.png')});assert(Math.abs(await mobilePicker.evaluate(node=>node.offsetHeight)-mobileHeight)<2,'empty mobile recents collapsed the picker');await mobile.locator('#oie-emoji-picker [data-emoji-jump="8"]').tap();assert.equal(await mobile.locator('#oie-emoji-picker [data-emoji-section]').count(),10);
  await mobile.locator('#emoji-search').fill('thumbs up');await mobile.locator('#oie-emoji-picker [data-emoji-family="👍"]').waitFor();const mobileEnglish=await mobile.locator('#emoji-grid').evaluate(node=>[...node.querySelectorAll('[data-emoji-section]>.oie-emoji-grid')].flatMap(group=>[...group.children].map(child=>child.dataset.emojiFamily||child.dataset.emojiPick)));
  await mobile.locator('#emoji-search').fill('joinha');await mobile.locator('#oie-emoji-picker [data-emoji-family="👍"]').waitFor();assert.deepEqual(await mobile.locator('#emoji-grid').evaluate(node=>[...node.querySelectorAll('[data-emoji-section]>.oie-emoji-grid')].flatMap(group=>[...group.children].map(child=>child.dataset.emojiFamily||child.dataset.emojiPick))),mobileEnglish);assert.equal(await mobile.locator('#oie-emoji-picker [data-emoji-family="👍"] > [data-emoji-pick]').getAttribute('aria-label'),'polegar para cima');
  await mobile.locator('#oie-emoji-picker [data-emoji-family="👍"] [data-emoji-variants-trigger]').tap();const mobileVariants=mobile.getByRole('dialog',{name:'Variações do emoji',exact:true});await mobileVariants.waitFor({state:'visible'});const variantBox=await mobileVariants.boundingBox();assert(variantBox.x>=-1&&variantBox.x+variantBox.width<=391,'subpicker fits mobile viewport');assert(await mobileVariants.evaluate(panel=>{const probe=document.createElement('span');probe.style.backgroundColor='var(--oie-raised)';document.body.append(probe);const same=getComputedStyle(panel.querySelector('.oie-emoji-variants-bubble')).backgroundColor===getComputedStyle(probe).backgroundColor;probe.remove();return same}),'variant strip keeps light theme colors');await mobileVariants.locator('[data-emoji-pick="👍🏻"]').tap();assert.equal(await mobile.locator('#message-composer').inputValue(),'👍🏻');await mobile.locator('#message-composer').fill('');
  await mobile.locator('#oie-emoji-picker [data-emoji-trigger]').tap();await mobile.locator('#emoji-search').fill('coracao');await mobile.locator('#oie-emoji-picker [data-emoji-pick="❤️"]').waitFor();
  const panel=mobile.getByRole('dialog',{name:'Escolher emoji',exact:true});await panel.waitFor({state:'visible'});const box=await panel.boundingBox();assert(box.x>=-1&&box.x+box.width<=391,'picker fits mobile viewport');
  await mobile.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await mobile.evaluate(()=>window.axe.run(document.querySelector('#oie-emoji-picker-panel'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(axe.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[]);
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await mobile.screenshot({path:path.join(artifacts,'emoji-picker-mobile.png')});
  await mobile.locator('#oie-emoji-picker [data-emoji-pick="❤️"]').click();assert.equal(await mobile.locator('#message-composer').inputValue(),'❤️');assert.equal(writes,0);assert.deepEqual(errors,[]);
  await mobile.locator('#message-composer').fill('Valeu :joinha!');
  await mobile.locator('#message-composer').evaluate(node=>node.setSelectionRange(13,13));
  await mobile.locator('[data-emoji-option]').first().tap();
  assert.equal(await mobile.locator('#message-composer').inputValue(),'Valeu 👍!');
  findings.push('pt-BR accent-insensitive search, light mobile layout, denied-storage insertion, bilingual aliases, touch variant choice and zero picker WCAG A/AA violations');
  await mobileContext.close();
  const failure=await browser.newPage({viewport:{width:1180,height:850}});active=failure;failure.on('pageerror',error=>errors.push(error.message));let fail=true;
  await failure.route('**/ui/emoji/en.json',route=>fail?route.abort('failed'):route.continue());await failure.goto(route());
  const failedRequest=failure.waitForEvent('requestfailed',{predicate:request=>request.url().endsWith('/ui/emoji/en.json')});
  const literal=failure.locator('#message-composer');
  await literal.fill(':heart');
  await failedRequest;
  await failure.locator('#oie-emoji-completion').waitFor({state:'hidden'});
  assert.equal(await literal.inputValue(),':heart');
  await failure.locator('#oie-emoji-picker [data-emoji-trigger]').click();await failure.getByText('Could not load emojis. Try again.',{exact:true}).waitFor();fail=false;await failure.locator('#oie-emoji-picker [data-emoji-retry]').click();await failure.locator('#emoji-search').fill('flag brazil');await failure.locator('#oie-emoji-picker [data-emoji-pick="🇧🇷"]').waitFor();assert.equal(writes,0);
  findings.push('Failed local catalog can retry without changing or sending the draft');
  await failure.close();

  const inlineContext=await browser.newContext({viewport:{width:1440,height:900},locale:'en'});
  const inline=await inlineContext.newPage();active=inline;inline.on('pageerror',error=>errors.push(error.message));
  let releaseCatalog;
  const holdCatalog=new Promise(resolve=>{releaseCatalog=resolve});
  await inline.route('**/ui/emoji/en.json',async request=>{await holdCatalog;await request.continue()});
  await inline.goto(route());
  const field=inline.locator('#message-composer'),completion=inline.locator('#oie-emoji-completion');
  const suggestions=completion.getByRole('option');
  await field.fill('😀 :coracao:!');
  await field.evaluate(node=>node.setSelectionRange(7,7));
  await completion.getByText('Loading emojis…').waitFor();
  await field.press('Enter');
  assert.equal(writes,0,'Enter must not send while suggestions are loading');
  await field.press('Tab');
  await completion.waitFor({state:'hidden'});
  const response=inline.waitForResponse('**/ui/emoji/en.json');
  releaseCatalog();
  await (await response).finished();
  await inline.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
  assert(await completion.isHidden(),'the catalog response must respect Tab dismissal');
  await field.fill('😀 :coracao:!');
  await field.evaluate(node=>node.setSelectionRange(7,7));
  await suggestions.first().waitFor();
  await field.press('Escape');
  assert(await completion.isHidden());
  assert.equal(await field.inputValue(),'😀 :coracao:!');
  await field.fill('😀 :coracao:!');
  await field.evaluate(node=>node.setSelectionRange(12,12));
  await suggestions.filter({hasText:':coração:'}).first().waitFor();
  await field.press('Enter');
  assert.equal(await field.inputValue(),'😀 ❤️!');
  assert(await field.evaluate(node=>node===document.activeElement&&node.selectionStart===5));
  assert.equal(writes,0);
  await inline.reload();
  await until(async()=>await field.inputValue()==='😀 ❤️!','shortcut persists in the draft');
  await field.fill('12:30 https://oi.example/:heart');
  assert(await completion.isHidden(),'embedded colons remain literal');
  await field.evaluate(node=>{
    node.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));
    node.value=':thumbs_up:';
    node.setSelectionRange(node.value.length,node.value.length);
    node.dispatchEvent(new InputEvent('input',{bubbles:true,isComposing:true}));
    node.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,key:'Enter',isComposing:true}));
  });
  assert(await completion.isHidden());
  await field.evaluate(node=>node.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})));
  await suggestions.first().waitFor();
  await field.press('Enter');
  assert.equal(await field.inputValue(),'👍');
  assert.equal(writes,0);
  const settings=inline.getByRole('dialog',{name:'Settings',exact:true});
  const openSettings=async()=>{
    await inline.locator('[data-navigation-settings]:visible').click();
    await settings.getByRole('tab',{name:'Composition',exact:true}).click();
  };
  const closeSettings=()=>settings.getByRole('button',{name:'Close',exact:true}).click();
  await openSettings();
  const languages=settings.getByRole('tabpanel',{name:'Composition'});
  const languageTrigger=languages.getByRole('combobox',{name:'Emoji shortcut languages'});
  const english=languages.getByRole('option',{name:'English'}),portuguese=languages.getByRole('option',{name:'Português do Brasil'});
  const searchLanguages=languages.getByRole('textbox',{name:'Search languages'});
  assert.match(await languageTrigger.textContent(),/2 languages selected/);
  await languageTrigger.click();
  assert.equal(await english.getAttribute('aria-selected'),'true');
  assert.equal(await portuguese.getAttribute('aria-selected'),'true');
  await languages.screenshot({path:path.join(artifacts,'emoji-composition-settings.png')});
  await searchLanguages.fill('english');
  assert(await portuguese.isHidden());
  await searchLanguages.press('ArrowDown');
  await until(()=>english.evaluate(option=>document.activeElement===option),'keyboard focus moves from the language search to the filtered option');
  await inline.keyboard.press('Space');
  assert.equal(await english.getAttribute('aria-selected'),'false');
  await inline.keyboard.press('Escape');
  await searchLanguages.waitFor({state:'hidden'});
  assert(await settings.isVisible());
  await closeSettings();
  assert.equal(await inline.locator('html').getAttribute('lang'),'en');
  await field.fill(':thumbs_up');
  assert(await completion.isHidden());
  await field.fill(':joinha');
  await suggestions.first().click();
  assert.equal(await field.inputValue(),'👍');
  await inline.reload();
  await openSettings();
  assert.match(await languageTrigger.textContent(),/Português do Brasil/);
  await languageTrigger.click();
  assert.equal(await english.getAttribute('aria-selected'),'false');
  assert.equal(await portuguese.getAttribute('aria-selected'),'true');
  await portuguese.click();
  assert.match(await languageTrigger.textContent(),/Shortcuts off/);
  await closeSettings();
  await field.fill(':joinha');
  assert(await completion.isHidden(),'clearing both languages disables shortcuts');
  await inlineContext.close();
  findings.push('Inline completion replaces the full shortcode, preserves the draft, respects IME and focus, and saves the language selection independently');

  for(const width of [390,1440])for(const lang of ['en','pt-BR'])for(const mode of ['light','dark'])for(const libraryTheme of ['goshtoso','minimal']){
   const matrix=await browser.newContext({viewport:{width,height:900},locale:lang});
   const view=await matrix.newPage();active=view;view.on('pageerror',error=>errors.push(error.message));
   let release;
   const hold=new Promise(resolve=>{release=resolve});
   await view.route('**/ui/emoji/*.json',async request=>{await hold;await request.continue()});
   await view.goto(route(chat,lang,mode));
   await view.evaluate(theme=>{
     document.documentElement.dataset.theme=theme;
     const sample=()=>['.oie-composer','#message-timeline','[data-send-message]'].flatMap(selector=>{
       const node=document.querySelector(selector),rect=node.getBoundingClientRect();
       return [rect.x,rect.y,rect.width,rect.height,node.scrollTop];
     });
     const base=sample();
     window.completionGeometry={maxDelta:0,running:true};
     function frame(){
       const state=window.completionGeometry;
       if(!state.running)return;
       state.maxDelta=Math.max(state.maxDelta,...sample().map((value,index)=>Math.abs(value-base[index])));
       requestAnimationFrame(frame);
     }
     requestAnimationFrame(frame);
   },libraryTheme);
   const input=view.locator('#message-composer'),popup=view.locator('#oie-emoji-completion');
   await input.fill(':jo');
   await popup.getByRole('status').waitFor();
   await input.fill(':thumbs_up');
   release();
   await popup.getByRole('option').first().waitFor();
   assert.match(await popup.getByRole('option').first().textContent(),/👍.*:thumbs-up:/);
   const box=await popup.boundingBox();
   assert(box&&box.x>=0&&box.y>=0&&box.x+box.width<=width+1,JSON.stringify({width,box}));
   await view.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
   const violations=await view.evaluate(async()=>{
     const result=await axe.run(document.querySelector('#send-message-form'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}});
     return result.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)}));
   });
   assert.deepEqual(violations,[]);
   if(width===390&&lang==='pt-BR'&&mode==='dark'&&libraryTheme==='goshtoso'){
     await view.screenshot({path:path.join(artifacts,'emoji-completion-mobile.png')});
   }
   await input.press('ArrowDown');await input.press('ArrowUp');await input.press('Enter');
   assert.equal(await input.inputValue(),'👍');
   const delta=await view.evaluate(async()=>{
     await new Promise(resolve=>requestAnimationFrame(resolve));
     window.completionGeometry.running=false;
     return window.completionGeometry.maxDelta;
   });
   assert(delta<=1,JSON.stringify({width,lang,mode,libraryTheme,delta}));
   await matrix.close();
  }
  assert.equal(writes,0);
  findings.push('Delayed results use the latest query without moving the composer or timeline across EN/PT, 390/1440 px, light/dark and Goshtoso/Minimal');
  assert.deepEqual(errors,[]);
  await fs.writeFile(path.join(artifacts,'emoji-picker-report.json'),JSON.stringify({findings,unexpectedMutations:writes,pageErrors:errors},null,2));
  console.log('PASS: localized emoji picker and composer shortcuts, drafts, language preferences, keyboard/touch, geometry and accessibility');
 } catch(error) {
  if(active&&!active.isClosed())console.error(await active.evaluate(()=>({url:location.href,status:document.querySelector('[data-emoji-status]')?.textContent,trigger:document.querySelector('[data-emoji-trigger]')?.outerHTML,search:document.querySelector('#emoji-search')?.outerHTML,focus:document.activeElement?.outerHTML.slice(0,300)})));
  throw error;
 } finally {
  await browser.close();child.kill('SIGTERM');for(const stream of streams)stream.end();api.closeAllConnections();await new Promise(resolve=>api.close(resolve));
 }
})().catch(error=>{console.error(error);process.exitCode=1});
