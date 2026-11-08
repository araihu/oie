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
  await search.fill('heart');await page.locator('[data-emoji-pick="❤️"]').click();
  assert.equal(await input.inputValue(),'A❤️B');assert(await input.evaluate(node=>node===document.activeElement&&node.selectionStart===3));assert.equal(page.url(),originalURL);assert.equal(await page.locator('[name=operationKey]').first().inputValue(),originalKey);assert.equal(writes,0);assert.equal(reads,1);
  await page.reload();await until(async()=>await input.inputValue()==='A❤️B','emoji draft survives reload');await trigger.focus();await page.keyboard.press('Enter');await search.waitFor({state:'visible'});await search.fill('thumbs up medium skin tone');await page.locator('[data-emoji-pick="👍🏽"]').waitFor();
  await search.fill('👍🏽');await search.press('ArrowDown');assert(await page.locator('#emoji-grid [data-emoji-pick]').first().evaluate(node=>node===document.activeElement));await page.keyboard.press('Enter');assert((await input.inputValue()).includes('👍🏽'));assert.equal(writes,0);
  await trigger.click();await page.locator('[data-emoji-category=recent]').click();assert(await page.locator('[data-emoji-pick="👍🏽"]').isVisible());await page.keyboard.press('Escape');await until(async()=>await trigger.evaluate(node=>node===document.activeElement),'Escape restores trigger focus');
  await trigger.click();await page.locator('[data-emoji-category=all]').click();await search.fill('no-such-emoji-qwerty');await page.getByText('No emojis found.',{exact:true}).waitFor({state:'visible'});await search.fill('');
  await input.evaluate(node=>{node.readOnly=true});await until(async()=>await trigger.isDisabled(),'uncertain send locks picker');await search.waitFor({state:'hidden'});await input.evaluate(node=>{node.readOnly=false});await until(async()=>await trigger.isEnabled(),'unlock');
  await trigger.click();await search.fill('rocket');await page.locator('[data-emoji-pick="🚀"]').waitFor();await page.keyboard.press('Escape');
  const beforeNavigation=await input.inputValue();await page.locator(`a[data-conversation-id="${other}"]`).first().click();await until(async()=>new URL(page.url()).searchParams.get('chat')===other,'HTMX route');assert.equal(await input.inputValue(),'');
  await trigger.click();await search.fill('cat');await page.locator('[data-emoji-pick="🐈"]').click();assert.equal(await input.inputValue(),'🐈');await page.locator(`a[data-conversation-id="${chat}"]`).first().click();await until(async()=>await input.inputValue()===beforeNavigation,'original scoped draft restored');assert.equal(writes,0);
  findings.push('Lazy local catalog, cursor replacement, skin-tone sequence, keyboard selection/Escape, scoped drafts, recent emojis, lock protection and HTMX lifecycle without writes');
  await context.close();
  const mobileContext=await browser.newContext({viewport:{width:390,height:844},locale:'pt-BR',isMobile:true,hasTouch:true});
  await mobileContext.addInitScript(()=>{for(const name of ['getItem','setItem'])Object.defineProperty(Storage.prototype,name,{configurable:true,value(){throw new DOMException('Denied','SecurityError')}})});
  const mobile=await mobileContext.newPage();active=mobile;mobile.on('pageerror',error=>errors.push(error.message));
  await mobile.goto(route(chat,'pt-BR','light'));await mobile.locator('[data-emoji-trigger]').click();await mobile.locator('#emoji-search').fill('coracao');await mobile.locator('[data-emoji-pick="❤️"]').waitFor();
  const panel=mobile.getByRole('dialog',{name:'Escolher emoji',exact:true});await panel.waitFor({state:'visible'});const box=await panel.boundingBox();assert(box.x>=-1&&box.x+box.width<=391,'picker fits mobile viewport');
  await mobile.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await mobile.evaluate(()=>window.axe.run(document.querySelector('#oie-emoji-picker-panel'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa']}}));assert.deepEqual(axe.violations.map(item=>({id:item.id,nodes:item.nodes.map(node=>node.target)})),[]);
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});await mobile.screenshot({path:path.join(artifacts,'emoji-picker-mobile.png')});
  await mobile.locator('[data-emoji-pick="❤️"]').click();assert.equal(await mobile.locator('#message-composer').inputValue(),'❤️');assert.equal(writes,0);assert.deepEqual(errors,[]);
  findings.push('pt-BR accent-insensitive search, light mobile layout, denied-storage insertion, zero picker WCAG A/AA violations');
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
