const assert=require('node:assert/strict'),http=require('node:http'),path=require('node:path'),fs=require('node:fs/promises');
const {spawn}=require('node:child_process'),{once}=require('node:events'),{chromium}=require('playwright');
(async()=>{
 const engine='019a5df0-0000-7000-8000-000000000001';
 const api=http.createServer((req,res)=>{res.setHeader('Content-Type','application/json');res.setHeader('X-Oie-Mock','true');
  const p=new URL(req.url,'http://fixture').pathname;
  if(p==='/api/v1/engine')return res.end(JSON.stringify({id:engine,role:'hosted',apiVersion:'0.13.0',eventCursor:engine+':0'}));
  if(p==='/api/v1/accounts'||p==='/api/v1/conversations')return res.end('{"items":[]}');
  res.statusCode=404;res.end('{"status":404}');
 });api.listen(0,'127.0.0.1');await once(api,'listening');
 const probe=http.createServer();probe.listen(0,'127.0.0.1');await once(probe,'listening');const port=probe.address().port;await new Promise(r=>probe.close(r));
 const address='http://127.0.0.1:'+port,binary=process.env.OIE_WEB_BINARY||path.resolve(__dirname,'../../bin/oie');
 const child=spawn(binary,['--role','bff','--listen','127.0.0.1:'+port,'--engine-url','http://127.0.0.1:'+api.address().port],{stdio:['ignore','ignore','pipe']});let log='';child.stderr.on('data',data=>log+=data);
 const browser=await chromium.launch({headless:true}),findings=[];
 try{
  for(let i=0;i<100;i++){try{if((await fetch(address)).ok)break}catch{}if(i===99)throw Error('BFF not ready '+log);await new Promise(r=>setTimeout(r,100))}
  const artifacts=path.join(__dirname,'artifacts');await fs.mkdir(artifacts,{recursive:true});
  for(const width of [390,1440])for(const theme of ['goshtoso','minimal'])for(const mode of ['light','dark'])for(const lang of ['en','pt-BR']){
   const ctx=await browser.newContext({viewport:{width,height:980},locale:lang,colorScheme:mode==='dark'?'light':'dark'}),page=await ctx.newPage(),errors=[];
   page.on('pageerror',e=>errors.push(e.message));await page.goto(address+'/?lang='+lang+'&theme='+mode);
   await page.waitForFunction(()=>window.OieRuntime?.generation>0);await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);assert.equal(await page.title(),'Oiê');
   if(width===390){await page.getByRole('button',{name:lang==='en'?'Open navigation':'Abrir navegação',exact:true}).click()}
   const logo=page.locator('.oie-brand-logo:visible');await logo.first().waitFor({state:'visible'});assert.equal(await logo.count(),1);await logo.evaluate(img=>img.decode());
   assert.equal(await logo.getAttribute('src').then(s=>s.includes('logo-'+mode+'.svg')),true);
   assert.equal(await page.getByRole('link',{name:'Oiê',exact:true}).count(),1);
   assert(await logo.evaluate(img=>img.naturalWidth>0&&img.naturalHeight>0));
   const manifestURL=await page.locator('link[rel=manifest]').getAttribute('href'),response=await page.request.get(new URL(manifestURL,address).href),manifest=await response.json();
   assert.equal(response.status(),200);assert.equal(manifest.name,'Oiê');assert.equal(manifest.short_name,'Oiê');assert.equal(manifest.lang,lang);assert.equal(manifest.display,'standalone');assert.equal(manifest.start_url,'/');assert.equal(manifest.scope,'/');assert.equal(manifest.icons.length,4);
   for(const icon of manifest.icons){const r=await page.request.get(new URL(icon.src,address).href),bytes=await r.body();assert.equal(r.status(),200);assert.equal(r.headers()['content-type'],'image/png');const size=Number(icon.sizes.split('x')[0]);assert.equal(bytes.readUInt32BE(16),size);assert.equal(bytes.readUInt32BE(20),size)}
   const favicon=await page.request.get(new URL(await page.locator('link[rel=icon][type="image/svg+xml"]').getAttribute('href'),address).href);assert.equal(favicon.status(),200);assert.equal(favicon.headers()['content-type'],'image/svg+xml');
   await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});const axe=await page.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));assert.deepEqual(axe.violations.map(v=>v.id),[]);assert.deepEqual(errors,[]);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
   await page.screenshot({path:path.join(artifacts,`branding-${width}-${theme}-${mode}-${lang}.png`)});findings.push({width,theme,mode,lang,logo:true,manifest:true,axe:0});await ctx.close();
  }
  await fs.writeFile(path.join(artifacts,'branding-report.json'),JSON.stringify({findings},null,2));console.log('PASS: Oiê shared logos, theme override, localized web manifest, generated favicon/PWA PNGs and 16 accessible layouts');
 }finally{await browser.close();child.kill('SIGTERM');api.closeAllConnections();await new Promise(r=>api.close(r))}
})().catch(e=>{console.error(e);process.exit(1)});
