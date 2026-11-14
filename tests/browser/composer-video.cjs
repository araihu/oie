// Isolated engine fixture for local staging and received DM/room video cards.
const assert = require('node:assert/strict');
const {createHash} = require('node:crypto');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const fs = require('node:fs/promises');
const http = require('node:http');
const path = require('node:path');
const {chromium, webkit} = require('playwright');

const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const engine = id(1), account = id(2), direct = id(3), room = id(4), revision = id(5);
const now = '2026-11-08T12:00:00Z';
const conversations = [direct, room].map((chat, index) => ({
  id:chat, revisionId:revision, accountId:account, kind:index ? 'room' : 'direct',
  address:index ? 'team@rooms.fixture' : 'bob@fixture', title:index ? 'Estúdio' : 'Bob',
  lastMessage:'Video from Bob', updatedAt:now, unreadCount:0, archived:false, favorite:false,
}));
const clips = conversations.map((conversation, index) => {
  const url = 'https://files.example.test/clip-' + index + '.webm';
  const message = {
    id:id(10 + index), revisionId:revision, accountId:account, conversationId:conversation.id,
    sender:{id:id(6), displayName:'Bob'}, isOwn:false, originalText:'Video from Bob\n' + url, createdAt:now, sendState:'received',
  };
  return {id:id(20 + index), revisionId:revision, kind:'video', url, fileName:'Clip.webm', message,
    accountLabel:'Fixture', conversationTitle:conversation.title};
});
const features = ['schedules', 'file_upload', 'room_messaging'].map(id => ({
  id, appSupport:'supported', protocolSupport:'supported', serverSupport:'supported', scope:'service',
}));

async function launch() {
  const bytes = await fs.readFile(path.join(__dirname, 'fixtures/tiny-video.webm'));
  const files = new Map();
  const streams = new Set();
  const reads = [], posts = [];
  const state = {stageStatus:201, oversize:false, stagedReads:0};
  const api = http.createServer((request, response) => {
    const url = new URL(request.url, 'http://fixture');
    const send = (status, value) => response.writeHead(status, {'Content-Type':'application/json'}).end(JSON.stringify(value));
    const chat = conversations.find(item => item.id === url.pathname.split('/')[4]);
    const name = url.pathname.split('/').at(-1);
    if (url.pathname === '/api/v1/engine') return send(200,{id:engine,role:'hosted',apiVersion:'0.30.1',eventCursor:engine + ':0'});
    if (url.pathname === '/api/v1/accounts') return send(200,{items:[{id:account,revisionId:revision,protocol:'xmpp',label:'Fixture',address:'alice@fixture',createdAt:now,connectionState:'connected'}]});
    if (name === 'connection') return send(200,{accountId:account,state:'connected',updatedAt:now});
    if (name === 'history') return send(200,{accountId:account,state:'complete',retainedMessages:2,conversations:2,updatedAt:now});
    if (name === 'capabilities') return send(200,{accountId:account,features});
    if (['avatars','previews','message-stars'].includes(name)) return send(200,{items:[]});
    if (url.pathname === '/api/v1/conversations') return send(200,{items:conversations});
    if (chat && name === chat.id) return send(200,chat);
    if (chat && name === 'room-session') return send(200,{accountId:account,conversationId:room,active:true,state:'joined',nickname:'Alice',updatedAt:now});
    if (chat && name === 'messages') return send(200,{items:clips.filter(clip => clip.message.conversationId === chat.id).map(clip => clip.message)});
    if (name === 'shared-content') {
      const chatID = url.searchParams.get('conversationId');
      return send(200,{items:clips.filter(clip => !chatID || clip.message.conversationId === chatID),indexing:{state:'complete',indexedMessages:2,totalMessages:2}});
    }
    if (name === 'thumbnail') return send(503,{status:503});
    if (name === 'content') {
      const item = url.pathname.split('/').at(-2);
      const range = request.headers.range;
      reads.push({item,range});
      if (state.oversize && range === 'bytes=0-5242879') {
        response.writeHead(200,{'Content-Type':'video/webm','Content-Length':6 * 1024 * 1024});
        response.flushHeaders();
        return;
      }
      let content = bytes;
      if (range) {
        const [, from, to] = /^bytes=(\d+)-(\d*)$/.exec(range) || [];
        const start = Number(from);
        const end = to ? Math.min(Number(to), bytes.length - 1) : bytes.length - 1;
        if (!Number.isInteger(start) || start > end) {
          response.setHeader('Content-Range', 'bytes */' + bytes.length);
          return send(416,{status:416});
        }
        response.setHeader('Content-Range', 'bytes ' + start + '-' + end + '/' + bytes.length);
        content = bytes.subarray(start,end + 1);
      }
      return response.writeHead(range ? 206 : 200,{'Content-Type':'video/webm','Content-Length':content.length,'Accept-Ranges':'bytes'}).end(content);
    }
    if (chat && url.pathname.includes('/staged-attachments/')) {
      const key = chat.id + '/' + name;
      if (request.method === 'GET') {
        state.stagedReads++;
        const file = files.get(key);
        if (!file) return send(404,{status:404});
        return response.writeHead(200,{'Content-Type':'application/octet-stream','Content-Length':file.length}).end(file);
      }
      posts.push(key);
      const chunks = [];
      request.on('data', chunk => chunks.push(chunk));
      request.on('end', () => {
        if (state.stageStatus !== 201) return send(state.stageStatus,{status:state.stageStatus});
        const multipart = Buffer.concat(chunks);
        const endOfHeaders = multipart.indexOf('\r\n\r\n');
        const fileName = /filename="([^"]+)"/.exec(multipart.subarray(0,endOfHeaders).toString())[1];
        const limit = multipart.lastIndexOf('\r\n--');
        const file = multipart.subarray(endOfHeaders + 4,limit);
        files.set(key,file);
        send(201,{id:name,fileName,mediaType:'video/webm',size:file.length,sha256:createHash('sha256').update(file).digest('hex')});
      });
      return;
    }
    if (name === 'events') {
      response.writeHead(200,{'Content-Type':'text/event-stream'}).write(': ready\n\n');
      streams.add(response);
      response.on('close', () => streams.delete(response));
      return;
    }
    send(404,{status:404});
  });

  const allocation = http.createServer().listen(0,'127.0.0.1');
  const backend = api.listen(0,'127.0.0.1');
  await Promise.all([once(allocation,'listening'), once(backend,'listening')]);
  const port = allocation.address().port;
  await new Promise(resolve => allocation.close(resolve));
  const server = spawn(process.env.OIE_WEB_BINARY || path.join(__dirname,'../../bin/oie'),
    ['--role','bff','--listen','127.0.0.1:' + port,'--engine-url','http://127.0.0.1:' + api.address().port]);
  let log = '';
  server.stderr.on('data', data => { log += data; });
  const base = 'http://127.0.0.1:' + port;
  const browser = await (process.argv.includes('--webkit') ? webkit : chromium).launch();
  try {
    for (let attempt = 0; attempt < 100; attempt++) {
      if (await fetch(base).then(response => response.ok, () => false)) break;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    for (const [index, locale, width] of [[0,'en',1280], [1,'pt-BR',390]]) {
      const page = await browser.newPage({locale, viewport:{width,height:844}});
      const errors = [];
      page.on('pageerror', error => errors.push(error.message));
      const chat = conversations[index].id;
      const href = base + '/?chat=' + chat + '&lang=' + locale;
      await page.goto(href);
      const videoCard = page.locator('[data-video-item="' + clips[index].id + '"]');
      const player = videoCard.locator('video');
      const draft = page.locator('[data-composer-attachments]');
      const picker = page.locator('[data-attachment-picker]');
      const composer = page.locator('#message-composer');
      const row = draft.locator('[data-draft-attachment]');
      const still = row.locator('[data-draft-thumbnail]');
      const dialog = page.locator('[data-draft-video-dialog]');
      const waitFrame = locator => locator.evaluate(image => image.decode());
      const receive = videoCard.locator('[data-video-poster-image]');
      await receive.waitFor({state:'visible'});
      await waitFrame(receive);
      assert.equal(await player.getAttribute('src'),null);
      assert(reads.some(read => read.item === clips[index].id && read.range === 'bytes=0-5242879'));
      assert(reads.every(read => read.range === 'bytes=0-5242879'));
      await page.waitForFunction(() => !document.querySelector('[data-attach-menu]').disabled);
      await composer.fill(locale === 'en' ? 'My caption' : 'Minha legenda');
      const caption = await composer.inputValue();
      let release;
      const gate = new Promise(resolve => { release = resolve; });
      await page.route('**/ui/scheduling/stage?*', async route => {
        await gate;
        await route.continue();
      }, {times:1});
      const video = {name:'Draft.webm', mimeType:'video/webm', buffer:bytes};
      await picker.setInputFiles(video);
      await row.waitFor({state:'visible'});
      await page.evaluate(() => {
        const selectors = ['#message-timeline','#message-composer','#composer-split','[data-draft-attachment]'];
        const measure = () => selectors.flatMap(selector => {
          const node = document.querySelector(selector);
          const {x,y,width,height} = node.getBoundingClientRect();
          return [x,y,width,height,node.scrollTop];
        });
        window.videoRects = [measure()];
        function sample() {
          window.videoRects.push(measure());
          window.videoRAF = requestAnimationFrame(sample);
        }
        window.videoRAF = requestAnimationFrame(sample);
      });
      await still.waitFor({state:'visible'});
      await waitFrame(still);
      release();
      await row.getByRole('link',{name:video.name}).waitFor();
      const frames = await page.evaluate(() => {
        cancelAnimationFrame(window.videoRAF);
        return window.videoRects;
      });
      for (const sample of frames) assert(sample.every((value,index)=>Math.abs(value-frames[0][index])<=1),
        JSON.stringify({width, expected:frames[0], sample}));
      assert.equal(await composer.inputValue(),caption);
      if (locale === 'pt-BR' && process.env.OIE_VIDEO_DOC_SCREENSHOT) {
        const upper = await draft.boundingBox();
        const lower = await page.locator('#send-message-form').boundingBox();
        const bottom = Math.min(844, lower.y + lower.height);
        await page.screenshot({path:process.env.OIE_VIDEO_DOC_SCREENSHOT,
          clip:{x:upper.x,y:upper.y,width:upper.width,height:bottom-upper.y}});
      }
      const stagedReads = state.stagedReads;
      await row.locator('[data-draft-video-play]').click();
      await dialog.waitFor({state:'visible'});
      const waitPlayback = selector => page.waitForFunction(selector => {
        const video = document.querySelector(selector);
        return video?.videoWidth === 64 && video.currentTime > 0.05;
      }, selector);
      await waitPlayback('[data-draft-video-dialog] video');
      assert.equal(state.stagedReads,stagedReads,'picked video plays without downloading staged bytes');
      await page.keyboard.press('Escape');
      await dialog.waitFor({state:'hidden'});
      assert.equal(await dialog.locator('video').getAttribute('src'),null);
      const sampleReads = reads.length;
      await page.reload();
      await receive.waitFor({state:'visible'});
      await waitFrame(receive);
      // Some WebViews do not make durable browser storage available. They can
      // regenerate the frame from the same bounded sample after a reload.
      if (!process.argv.includes('--webkit')) assert.equal(reads.length, sampleReads, 'reload should reuse the browser JPEG');
      await still.waitFor({state:'visible'});
      await waitFrame(still);
      assert.equal(await composer.inputValue(),caption);
      await row.locator('[data-remove-attachment]').click();
      await row.waitFor({state:'hidden'});

      state.stageStatus = 503;
      await picker.setInputFiles(video);
      await row.locator('[data-retry-attachment]').waitFor({state:'visible'});
      await still.waitFor({state:'visible'});
      state.stageStatus = 201;
      await row.locator('[data-retry-attachment]').click();
      await row.getByRole('link',{name:video.name}).waitFor();
      assert.equal(posts.at(-1),posts.at(-2));
      await row.locator('[data-remove-attachment]').click();

      const corrupt = {...video, name:'Broken.webm', buffer:Buffer.from('not a video')};
      await picker.setInputFiles(corrupt);
      await row.getByRole('link',{name:corrupt.name}).waitFor();
      assert.equal(await still.isHidden(), true);
      await row.locator('[data-remove-attachment]').click();
      assert.equal(await composer.inputValue(),caption);

      let cancelRelease;
      const cancelGate = new Promise(resolve => { cancelRelease = resolve; });
      await page.route('**/ui/scheduling/stage?*', async route => {
        await cancelGate;
        await route.continue().catch(() => {});
      }, {times:1});
      await picker.setInputFiles(video);
      await row.locator('[data-remove-attachment]').click();
      cancelRelease();
      await row.waitFor({state:'hidden'});
      assert.equal(await composer.inputValue(),caption);

      if (index === 1) {
        await page.evaluate(() => localStorage.setItem('oie.device.auto-video-previews.v1','false'));
        await page.reload();
        state.oversize = true;
        const response = await page.request.get(base + '/ui/shared-content/' + clips[index].id + '/content?engineId=' + engine + '&preview=1');
        assert.equal(response.status(),422);
        await videoCard.locator('[data-video-thumbnail-load]').click();
        await videoCard.locator('[data-video-thumbnail-load][aria-label="Tentar novamente"]').waitFor();
        assert.equal(await player.getAttribute('src'),null);
        state.oversize = false;
        if (process.env.OIE_VIDEO_RECEIVED_SCREENSHOT) {
          await videoCard.locator('[data-video-thumbnail-load]').click();
          await receive.waitFor({state:'visible'});
          await videoCard.screenshot({path:process.env.OIE_VIDEO_RECEIVED_SCREENSHOT});
        }
      }
      await videoCard.locator('[data-video-play]').click();
      await waitPlayback('[data-video-item="' + clips[index].id + '"] video');
      assert.deepEqual(errors,[]);
      await page.close();
      reads.length = 0;
    }
    console.log('PASS: draft thumbnail, staging failure/retry, playback and reload, bounded received DM and room previews');
  } catch (error) {
    console.error(log);
    throw error;
  } finally {
    await browser.close();
    server.kill('SIGTERM');
    for (const stream of streams) stream.end();
    api.closeAllConnections();
    await new Promise(resolve => api.close(resolve));
  }
}

launch().catch(error => { console.error(error); process.exitCode = 1; });
