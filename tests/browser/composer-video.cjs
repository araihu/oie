// Isolated engine fixture for attachment staging and conversation media.
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
const audioURL = 'https://files.example.test/mensagem.wav';
const receivedAudio = {
  ...clips[0].message,id:id(30),originalText:audioURL,createdAt:'2026-11-08T12:00:01Z',
};
const audioItem = {...clips[0],id:id(31),kind:'audio',url:audioURL,fileName:'mensagem.wav',message:receivedAudio};
const audioAttachment = {id:id(32),fileName:'Gravação.wav',mediaType:'audio/wav',size:32044,sha256:'a'.repeat(64)};
const sentAudio = {
  ...receivedAudio,id:id(33),isOwn:true,sendState:'submitted',originalText:'',attachments:[audioAttachment],
  createdAt:'2026-11-08T12:00:02Z',
};
const ownText = {
  ...receivedAudio,id:id(34),isOwn:true,sendState:'submitted',originalText:'My earlier message',
  createdAt:'2026-11-08T12:00:03Z',
};

function audioFixture(seconds = 2) {
  const samples = 8000 * seconds;
  const pcm = Buffer.alloc(44 + samples * 2);
  pcm.write('RIFF',0); pcm.writeUInt32LE(pcm.length - 8,4); pcm.write('WAVEfmt ',8);
  pcm.writeUInt32LE(16,16); pcm.writeUInt16LE(1,20); pcm.writeUInt16LE(1,22);
  pcm.writeUInt32LE(8000,24); pcm.writeUInt32LE(16000,28); pcm.writeUInt16LE(2,32);
  pcm.writeUInt16LE(16,34); pcm.write('data',36); pcm.writeUInt32LE(samples * 2,40);
  for(let i=0;i<Math.min(samples,16000);i++) pcm.writeInt16LE(Math.round(1500 * Math.sin(i * Math.PI * .11)),44+i*2);
  return pcm;
}
const features = ['schedules', 'file_upload', 'room_messaging', 'send_corrections'].map(id => ({
  id, appSupport:'supported', protocolSupport:'supported', serverSupport:'supported', scope:'service',
}));

async function installMicrophoneTone(page) {
  // The tone and the amplified microphone meter each have their own trusted activation.
  await page.evaluate(() => {
    // Keep the mock method on a stable wrapper across WebKit garbage collection.
    Object.defineProperty(navigator, 'mediaDevices', {value:navigator.mediaDevices,configurable:true});
    const button = document.createElement('button');
    button.id = 'audio-fixture-start-tone';
    button.textContent = 'Start fixture tone';
    button.style.cssText = 'position:fixed;top:0;left:0;z-index:9999';
    button.addEventListener('click', () => {
      const context = new AudioContext();
      const output = context.createMediaStreamDestination();
      const tone = context.createOscillator();
      const gain = context.createGain();
      gain.gain.value = 0.6;
      tone.connect(gain).connect(output);
      tone.start();
      void context.resume().then(() => {
        window.microphoneTone = {context, stream:output.stream};
        button.remove();
      });
    }, {once:true});
    document.body.append(button);
  });
  await page.locator('#audio-fixture-start-tone').click();
  await page.waitForFunction(() => !!window.microphoneTone);
}

async function verifyAudioCapture(page, locale, useTone) {
  const copy = locale === 'pt-BR' ? {
    record:'Gravar áudio', waiting:'Aguardando microfone…', stop:'Parar gravação', recording:'Gravando áudio…', cancel:'Cancelar',
  } : {
    record:'Record audio', waiting:'Waiting for microphone…', stop:'Stop recording', recording:'Recording audio…', cancel:'Cancel',
  };
  const record = page.getByRole('button', {name:copy.record,exact:true});
  const stop = page.getByRole('button', {name:copy.stop,exact:true});
  const audio = page.locator('[data-record-audio]');
  const dot = audio.locator('.oie-audio-recording-dot');
  const time = audio.locator('[data-record-audio-time]');
  const composer = page.locator('#message-composer');
  const geometry = await composer.boundingBox();
  if (useTone) await installMicrophoneTone(page);
  await page.evaluate(() => {
    const getUserMedia = navigator.mediaDevices.getUserMedia;
    const Recorder = MediaRecorder;
    let releaseStart;
    const started = new Promise(resolve => { releaseStart = resolve; });
    window.startAudioTestRecorder = releaseStart;
    window.audioTestRecorderCalls = 0;
    window.MediaRecorder = class extends Recorder {
      set onstart(callback) {
        super.onstart = event => {
          window.audioTestRecorderReady = true;
          void started.then(() => callback.call(this, event));
        };
      }
      start(...args) {
        window.audioTestRecorderCalls++;
        return super.start(...args);
      }
    };
    navigator.mediaDevices.getUserMedia = async options => {
      const microphone = window.microphoneTone;
      const stream = microphone?.stream ?? await getUserMedia.call(navigator.mediaDevices, options);
      const track = stream.getAudioTracks()[0];
      let muted = true;
      Object.defineProperty(track,'muted',{get:() => muted});
      window.unmuteAudioTestTrack = () => {
        muted = false;
        track.dispatchEvent(new Event('unmute'));
      };
      window.cleanupAudioTest = () => {
        navigator.mediaDevices.getUserMedia = getUserMedia;
        window.MediaRecorder = Recorder;
        void microphone?.context.close();
      };
      return stream;
    };
    const measure = () => ['#message-timeline','#composer-split','.oie-composer-field'].flatMap(selector => {
      const node = document.querySelector(selector);
      const {x,y,width,height} = node.getBoundingClientRect();
      return [x,y,width,height,node.scrollTop];
    });
    window.audioTestFrames = [measure()];
    function sample() {
      window.audioTestFrames.push(measure());
      window.audioTestFrame = requestAnimationFrame(sample);
    }
    window.audioTestFrame = requestAnimationFrame(sample);
  });
  await record.click();
  const waiting = audio.getByText(copy.waiting, {exact:true});
  await waiting.waitFor();
  assert(await waiting.evaluate(node => node.scrollWidth <= node.clientWidth));
  await page.waitForFunction(() => typeof window.unmuteAudioTestTrack === 'function');
  assert(await dot.isHidden());
  assert(await time.isHidden());
  assert(await record.isDisabled());
  assert.equal(await page.evaluate(() => window.audioTestRecorderCalls),0,'muted microphone must not start a silent recording');
  await page.evaluate(() => window.unmuteAudioTestTrack());
  await page.waitForFunction(() => window.audioTestRecorderReady);
  assert(await waiting.isVisible());
  assert(await dot.isHidden());
  assert(await stop.isHidden());
  await page.evaluate(() => window.startAudioTestRecorder());
  await stop.waitFor();
  await audio.getByText(copy.recording,{exact:true}).waitFor();
  assert(await dot.isVisible());
  assert(await time.isVisible());
  assert.deepEqual(await audio.boundingBox(),geometry);
  await page.waitForFunction(() => {
    const canvas = document.querySelector('[data-record-audio-wave] > canvas');
    if (!canvas?.width || !canvas?.height) return false;
    const pixels = canvas.getContext('2d').getImageData(0,Math.floor(canvas.height / 3),canvas.width,1).data;
    return pixels.some((value,index) => index % 4 === 3 && value > 0);
  });
  await audio.getByRole('button',{name:copy.cancel,exact:true}).click();
  await audio.waitFor({state:'hidden'});
  assert(await composer.evaluate(node => node === document.activeElement));
  const frames = await page.evaluate(() => {
    cancelAnimationFrame(window.audioTestFrame);
    window.cleanupAudioTest();
    return window.audioTestFrames;
  });
  for (const frame of frames) assert(frame.every((value,index) => Math.abs(value-frames[0][index]) <= 1),
    JSON.stringify({locale,expected:frames[0],frame}));
}

async function verifyDraftPlayback(page, href, locale, queuedSend) {
  const portuguese = locale === 'pt-BR';
  const audio = page.locator('[data-record-audio]');
  const record = page.getByRole('button', {name:portuguese ? 'Gravar áudio' : 'Record audio',exact:true});
  const play = audio.getByRole('button', {name:portuguese ? 'Reproduzir gravação' : 'Play recording',exact:true});
  const stop = page.getByRole('button', {name:portuguese ? 'Parar gravação' : 'Stop recording',exact:true});
  const position = audio.getByRole('slider', {name:portuguese ? 'Posição da gravação' : 'Recording position',exact:true});
  const media = '[data-record-audio-media]';
  await page.addInitScript(() => {
    const decode = OfflineAudioContext.prototype.decodeAudioData;
    const gate = new Promise(resolve => { window.releaseAudioWaveform = resolve; });
    OfflineAudioContext.prototype.decodeAudioData = async function(...args) {
      await gate;
      return decode.apply(this,args);
    };
  });
  await page.goto(href);
  const geometry = await page.locator('#message-composer').boundingBox();
  await installMicrophoneTone(page);
  await page.evaluate(() => {
    navigator.mediaDevices.getUserMedia = async () => window.microphoneTone.stream;
  });
  await record.click();
  await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:02');
  const stage = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/scheduling/stage');
  await stop.click();
  await play.focus();
  await play.press('Enter');
  await page.waitForFunction(selector => document.querySelector(selector).currentTime > .05,media);
  assert(await position.isHidden());
  await page.evaluate(selector => {
    const audio = document.querySelector(selector);
    window.beforeAudioWaveform = {audio,src:audio.currentSrc,time:audio.currentTime,focus:document.activeElement};
    window.releaseAudioWaveform();
  },media);
  await position.waitFor();
  await page.waitForFunction(selector => {
    const current = document.querySelector(selector);
    return current.currentTime > window.beforeAudioWaveform.time;
  },media);
  assert(await page.evaluate(selector => {
    const current = document.querySelector(selector);
    const {audio,src,focus} = window.beforeAudioWaveform;
    return current === audio && current.currentSrc === src && !current.paused && document.activeElement === focus;
  },media));
  assert.deepEqual(await audio.boundingBox(),geometry);
  const upload = await stage;
  assert(upload.ok());
  await page.evaluate(() => window.microphoneTone.context.close());
  await page.waitForFunction(() => !document.querySelector('#send-message-form').hasAttribute('data-attachments-pending'));
  const restoredID = new URL(upload.url()).searchParams.get('attachmentId');
  let releaseRestoredWave;
  const restoredWave = new Promise(resolve => { releaseRestoredWave = resolve; });
  const nativeFailure = queuedSend ? page.waitForResponse(response => response.status() === 416 &&
    new URL(response.url()).searchParams.get('attachmentId') === restoredID) : null;
  await page.route(url => url.pathname === '/ui/scheduling/file' && url.searchParams.get('attachmentId') === restoredID,
    async route => {
      if (route.request().resourceType() === 'fetch') await restoredWave;
      else if (queuedSend) return route.fulfill({status:416,headers:{'Content-Range':'bytes */0'}});
      await route.continue();
    });
  await page.reload();
  try {
    await play.click();
    if (nativeFailure) {
      await nativeFailure;
      releaseRestoredWave();
    }
    await page.waitForFunction(selector => document.querySelector(selector).currentTime > .05,media);
    assert(await position.isHidden());
    const source = new URL(await page.locator(media).evaluate(audio => audio.currentSrc));
    if (nativeFailure) assert.equal(source.protocol,'blob:');
    else assert.equal(source.searchParams.get('attachmentId'),restoredID);
  } finally {
    releaseRestoredWave();
    await page.evaluate(() => window.releaseAudioWaveform());
  }
  await position.waitFor();
  await audio.locator('[data-record-audio-remove]').click();
  await audio.waitFor({state:'hidden'});

  if (!queuedSend) return;
  await installMicrophoneTone(page);
  await page.evaluate(() => {
    navigator.mediaDevices.getUserMedia = async () => window.microphoneTone.stream;
  });
  await record.click();
  await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:01');
  const send = page.getByRole('button', {name:portuguese ? 'Enviar mensagem' : 'Send message',exact:true});
  let resumeUpload;
  const uploadGate = new Promise(resolve => { resumeUpload = resolve; });
  await page.route('**/ui/scheduling/stage?*', async route => {
    await uploadGate;
    await route.continue();
  }, {times:1});
  const uploadRequest = page.waitForRequest(request => new URL(request.url()).pathname === '/ui/scheduling/stage');
  await stop.click();
  const attachmentID = new URL((await uploadRequest).url()).searchParams.get('attachmentId');
  let requests = 0;
  const response = page.waitForResponse(result => new URL(result.url()).pathname === '/ui/messages/send');
  page.on('request', request => {
    if (new URL(request.url()).pathname === '/ui/messages/send') requests++;
  });
  try {
    assert(await send.isEnabled());
    await send.click();
    assert.equal(await send.getAttribute('aria-busy'),'true');
    assert.equal(await send.getAttribute('aria-disabled'),'true');
    assert.equal(requests,0);
  } finally {
    resumeUpload();
  }
  const sent = await response;
  assert(sent.ok());
  assert(sent.request().postData().includes(attachmentID));
  assert.equal(requests,1);
  await audio.waitFor({state:'hidden'});
  await page.evaluate(() => window.microphoneTone.context.close());
  await page.reload();
  await page.locator('[data-message-id="' + id(35) + '"]').waitFor();
}

async function launch() {
  const bytes = await fs.readFile(path.join(__dirname, 'fixtures/tiny-video.webm'));
  const audioBytes = audioFixture();
  const files = new Map();
  const staged = new Map();
  files.set(direct + '/' + audioAttachment.id, audioBytes);
  const streams = new Set();
  const reads = [], posts = [], audioReads = [], sendRequests = [];
  let acceptedAudio;
  const state = {stageStatus:201, oversize:false, stagedReads:0, rejectSend:false};
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
    if (chat && name === 'messages') {
      if (request.method === 'POST') {
        const chunks = [];
        request.on('data', chunk => chunks.push(chunk));
        request.on('end', () => {
          const payload = JSON.parse(Buffer.concat(chunks));
          sendRequests.push(payload);
          if (!Array.isArray(payload.attachmentIds)) return send(422,{code:'invalid_request',status:422});
          if (state.rejectSend) return send(422,{code:'invalid_request',status:422});
          const attachments = payload.attachmentIds.map(id => staged.get(chat.id + '/' + id));
          if (attachments.some(file => !file)) return send(422,{code:'invalid_request',status:422});
          acceptedAudio = {...sentAudio,id:id(35),originalText:payload.originalText,attachments,
            composition:{attachmentIds:payload.attachmentIds},createdAt:'2026-11-08T12:00:04Z'};
          send(202,acceptedAudio);
        });
        return;
      }
      const items = clips.filter(clip => clip.message.conversationId === chat.id).map(clip => clip.message);
      if (chat.id === direct) items.unshift(ownText, sentAudio, receivedAudio);
      if (chat.id === direct && acceptedAudio) items.unshift(acceptedAudio);
      return send(200,{items});
    }
    if (name === 'edit-target') return send(200,{messageId:ownText.id,accountId:account,conversationId:direct,
      currentRevisionId:ownText.revisionId,originalText:ownText.originalText,eligible:true});
    if (name === 'shared-content') {
      const chatID = url.searchParams.get('conversationId');
      const selected = [...clips, audioItem].filter(item => (!chatID || item.message.conversationId === chatID) &&
        (!['video','audio'].includes(url.searchParams.get('type')) || url.searchParams.get('type') === item.kind));
      return send(200,{items:selected,indexing:{state:'complete',indexedMessages:4,totalMessages:4}});
    }
    if (name === 'thumbnail') return send(503,{status:503});
    if (name === 'content') {
      const item = url.pathname.split('/').at(-2);
      if (item === audioItem.id) {
        const range = request.headers.range;
        audioReads.push({item, range, download:url.searchParams.has('download')});
        const headers = {'Content-Type':'audio/wav','Accept-Ranges':'bytes',
          'Content-Disposition':'inline; filename=mensagem.wav'};
        const [, from, to] = /^bytes=(\d+)-(\d*)$/.exec(range || '') || [];
        if (from === undefined) return response.writeHead(200, {...headers,'Content-Length':audioBytes.length}).end(audioBytes);
        const start = Number(from);
        const end = Math.min(to ? Number(to) : audioBytes.length - 1, audioBytes.length - 1);
        if (start > end) return response.writeHead(416, {...headers,'Content-Range':'bytes */' + audioBytes.length}).end();
        return response.writeHead(206, {...headers,'Content-Length':end - start + 1,
          'Content-Range':`bytes ${start}-${end}/${audioBytes.length}`}).end(audioBytes.subarray(start,end + 1));
      }
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
        if (name === audioAttachment.id) audioReads.push({item:name, range:request.headers.range, download:false});
        else state.stagedReads++;
        const file = files.get(key);
        if (!file) return send(404,{status:404});
        const headers = {'Content-Type':'application/octet-stream','Content-Length':file.length};
        const metadata = name === audioAttachment.id ? audioAttachment : staged.get(key);
        if (metadata) {
          headers['X-Oie-Attachment-Type'] = metadata.mediaType;
          headers['Content-Disposition'] = "attachment; filename*=UTF-8''" + encodeURIComponent(metadata.fileName);
        }
        const range = /^bytes=(\d+)-(\d*)$/.exec(request.headers.range || '');
        if (!range) return response.writeHead(200,headers).end(file);
        const start = Number(range[1]);
        const end = range[2] ? Math.min(Number(range[2]), file.length - 1) : file.length - 1;
        if (start > end) return response.writeHead(416,{'Content-Range':'bytes */' + file.length}).end();
        headers['Content-Range'] = 'bytes ' + start + '-' + end + '/' + file.length;
        headers['Content-Length'] = end - start + 1;
        headers['Accept-Ranges'] = 'bytes';
        return response.writeHead(206,headers).end(file.subarray(start,end + 1));
      }
      posts.push(key);
      const chunks = [];
      request.on('data', chunk => chunks.push(chunk));
      request.on('end', () => {
        if (state.stageStatus !== 201) return send(state.stageStatus,{status:state.stageStatus});
        const multipart = Buffer.concat(chunks);
        const endOfHeaders = multipart.indexOf('\r\n\r\n');
        const headers = multipart.subarray(0,endOfHeaders).toString();
        const fileName = /filename="([^"]+)"/.exec(headers)[1];
        const mediaType = /Content-Type: ([^\r\n]+)/i.exec(headers)[1];
        const limit = multipart.lastIndexOf('\r\n--');
        const file = multipart.subarray(endOfHeaders + 4,limit);
        files.set(key,file);
        const metadata = {id:name,fileName,mediaType,size:file.length,sha256:createHash('sha256').update(file).digest('hex')};
        staged.set(key,metadata);
        send(201,metadata);
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
  const useWebKit = process.argv.includes('--webkit');
  const browser = useWebKit ? await webkit.launch() : await chromium.launch({
    args:['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream'],
    // Chromium uses the process locale when converting the suggested filename.
    env: process.platform === 'linux' ? {...process.env, LC_ALL:'C.UTF-8'} : process.env,
  });
  try {
    for (let attempt = 0; attempt < 100; attempt++) {
      if (await fetch(base).then(response => response.ok, () => false)) break;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    for (const [locale, width] of [['en',1440], ['pt-BR',390]]) {
      const page = await browser.newPage({locale,viewport:{width,height:844}});
      try {
        await page.goto(base + '/?chat=' + direct + '&lang=' + locale);
        await verifyAudioCapture(page, locale, useWebKit);
      } finally {
        await page.close();
      }
      const draftPage = await browser.newPage({locale,viewport:{width,height:844}});
      try {
        await verifyDraftPlayback(draftPage, base + '/?chat=' + direct + '&lang=' + locale, locale, useWebKit && locale === 'pt-BR');
      } finally {
        await draftPage.close();
      }
    }
    for (const rejectNativeRange of [false, true]) {
      const page = await browser.newPage({locale:'pt-BR',viewport:{width:390,height:844}});
      let releaseWave;
      const waveGate = new Promise(resolve => { releaseWave = resolve; });
      const nativeRequest = page.waitForRequest(request => request.url().includes('/' + audioItem.id + '/content') &&
        request.headers().range !== 'bytes=0-20971519');
      await page.route(url => url.pathname.endsWith('/' + audioItem.id + '/content'), async route => {
        if (route.request().headers().range === 'bytes=0-20971519') await waveGate;
        else if (rejectNativeRange) return route.fulfill({status:416,headers:{'Content-Range':`bytes */${audioBytes.length}`}});
        await route.continue();
      });
      try {
        await page.goto(base + '/?chat=' + direct + '&lang=pt-BR');
        const player = page.locator('[data-message-id="' + receivedAudio.id + '"] [data-message-audio]');
        const audio = player.locator('audio');
        const play = player.getByRole('button',{name:/^Reproduzir áudio/});
        const pause = player.getByRole('button',{name:/^Pausar áudio/});
        const slider = player.getByRole('slider',{name:'Posição do áudio',exact:true});
        await player.scrollIntoViewIfNeeded();
        await play.click();
        await nativeRequest;
        if (rejectNativeRange) {
          await page.waitForFunction(node => !!node.error, await audio.elementHandle());
          assert.equal(await player.getAttribute('aria-busy'),'true');
          releaseWave();
          await page.waitForFunction(node => node.currentSrc.startsWith('blob:') && node.currentTime > 0, await audio.elementHandle());
          await slider.waitFor();
          continue;
        }
        await page.waitForFunction(node => !node.paused && node.currentTime > 0, await audio.elementHandle());
        assert.equal(await player.getAttribute('aria-busy'),'true');
        assert.equal(await slider.count(),0);
        await pause.focus();
        await pause.press('Enter');
        const before = await audio.evaluate(node => [node.currentSrc, node.currentTime]);
        releaseWave();
        await slider.waitFor();
        await player.locator('canvas').first().waitFor();
        assert.deepEqual(await audio.evaluate(node => [node.currentSrc, node.currentTime]),before);
        assert(await audio.evaluate(node => node.paused));
        assert(await play.evaluate(node => node === document.activeElement));
      } finally {
        releaseWave();
        await page.close();
      }
    }
    if (!useWebKit) {
      for (const [locale, width, playLabel, pauseLabel, positionLabel, downloadLabel, theme] of [
        ['en', 1440, 'Play audio', 'Pause audio', 'Audio position', 'Download audio', 'light'],
        ['pt-BR', 390, 'Reproduzir áudio', 'Pausar áudio', 'Posição do áudio', 'Baixar áudio', 'dark'],
        ['pt-BR', 838, 'Reproduzir áudio', 'Pausar áudio', 'Posição do áudio', 'Baixar áudio', 'dark'],
      ]) {
        const page = await browser.newPage({locale,viewport:{width,height:844}});
        const startReads = audioReads.length;
        let releaseWaves;
        const waveGate = new Promise(resolve => { releaseWaves = resolve; });
        await page.route(url => url.pathname.endsWith('/' + audioItem.id + '/content') ||
          url.searchParams.get('attachmentId') === audioAttachment.id, async route => {
          await waveGate;
          await route.continue();
        });
        await page.goto(base + '/?chat=' + direct + '&lang=' + locale + '&theme=' + theme);
        const players = page.locator('[data-message-audio]:visible');
        await players.last().waitFor();
        assert.equal(await players.count(),2);
        try {
          await page.locator('[data-message-audio][aria-busy="true"]').waitFor();
          assert.equal(audioReads.length,startReads);
          await page.evaluate(() => {
            const nodes = [...document.querySelectorAll('[data-message-audio],#message-timeline,#send-message-form')];
            const measure = () => nodes.flatMap(node => {
              const {x,y,width,height} = node.getBoundingClientRect();
              return [x,y,width,height,node.scrollTop];
            });
            window.audioRects = [];
            function sample() {
              window.audioRects.push(measure());
              window.audioRAF = requestAnimationFrame(sample);
            }
            sample();
          });
        } finally {
          releaseWaves();
        }
        for (const player of await players.all()) {
          await player.getByRole('slider',{name:positionLabel,exact:true}).waitFor();
          await player.locator('canvas').first().waitFor();
          await player.locator('[data-message-audio-time]').filter({hasText:/^0:02$/}).waitFor();
          assert(await player.locator('audio').evaluate(audio => audio.paused));
          assert(await player.evaluate(node => {
            const card = node.getBoundingClientRect();
            const bubble = node.closest('.oie-message-body-content').getBoundingClientRect();
            return card.left >= bubble.left - 1 && card.right <= bubble.right + 1;
          }), 'the player fits inside the message bubble');
        }
        const frames = await page.evaluate(() => {
          cancelAnimationFrame(window.audioRAF);
          return window.audioRects;
        });
        for (const sample of frames) assert(sample.every((value,index)=>Math.abs(value-frames[0][index])<=1),
          JSON.stringify({width,expected:frames[0],sample}));
        const expectedReads = [{item:audioItem.id,range:'bytes=0-20971519',download:false},
          {item:audioAttachment.id,range:undefined,download:false}];
        const preparedReads = () => audioReads.slice(startReads).sort((a,b) => a.item.localeCompare(b.item));
        assert.deepEqual(preparedReads(),expectedReads);
        for (const player of await players.all()) {
          await player.getByRole('button',{name:new RegExp('^' + playLabel)}).click();
          await player.getByRole('button',{name:new RegExp('^' + pauseLabel)}).click();
          const seek = player.getByRole('slider',{name:positionLabel,exact:true});
          await seek.press('End');
          await page.waitForFunction(slider => Number(slider.getAttribute('aria-valuenow')) >= 1.9,await seek.elementHandle());
          assert.equal(await seek.getAttribute('aria-valuetext'),'0:02 / 0:02');
          assert.equal(await player.locator('[data-message-audio-time]').textContent(),'0:02');
        }
        assert.equal(await players.getByRole('slider').count(),2);
        assert.deepEqual(preparedReads(),expectedReads,'playback reuses the prepared audio');
        for (const [message, filename] of [[receivedAudio,'mensagem.wav'], [sentAudio,'Gravação.wav']]) {
          const row = page.locator('[data-message-id="' + message.id + '"]');
          const menuTrigger = row.locator('[data-message-menu-trigger]');
          await menuTrigger.focus();
          await menuTrigger.press('Enter');
          const action = row.getByRole('menuitem',{name:downloadLabel,exact:true});
          await action.focus();
          const downloading = page.waitForEvent('download');
          await action.press('Enter');
          const file = await downloading;
          assert.equal(file.suggestedFilename(),filename);
          assert.deepEqual(await fs.readFile(await file.path()),audioBytes);
          await page.waitForFunction(node => node === document.activeElement,await menuTrigger.elementHandle());
        }
        await page.close();
        reads.length = 0;
      }
    }
    if (!useWebKit) {
      const page = await browser.newPage({viewport:{width:838,height:844}});
      let rejectedRange = 0;
      await page.route(url => url.pathname.endsWith('/' + audioItem.id + '/content'), route => {
        if (route.request().headers().range !== 'bytes=0-20971519') return route.continue();
        rejectedRange++;
        return route.fulfill({status:416});
      });
      await page.goto(base + '/?chat=' + direct + '&lang=en');
      const player = message => page.locator('[data-message-id="' + message.id + '"] [data-message-audio]');
      await player(sentAudio).getByRole('slider',{name:'Audio position'}).waitFor({timeout:5000});
      assert.equal(await player(sentAudio).locator('[data-message-audio-time]').textContent(),'0:02');
      assert.equal(await player(receivedAudio).getAttribute('aria-busy'),null);
      assert.equal(rejectedRange,1);
      await player(receivedAudio).getByRole('button',{name:/^Play audio/}).click();
      const streaming = player(receivedAudio).locator('audio[controls]');
      await streaming.waitFor();
      await page.waitForFunction(audio => !audio.paused && audio.currentTime > 0, await streaming.elementHandle());
      await page.close();
      reads.length = 0;
    }
    if (!useWebKit) {
      const page = await browser.newPage({viewport:{width:838,height:844}});
      await page.route(url => url.pathname.endsWith('/' + audioItem.id + '/content'), route =>
        route.fulfill({contentType:'audio/wav',body:audioFixture(301)}), {times:1});
      await page.goto(base + '/?chat=' + direct + '&lang=en');
      const player = page.locator('[data-message-id="' + receivedAudio.id + '"] [data-message-audio]');
      await player.locator('[data-message-audio-time]').filter({hasText:/^5:01$/}).waitFor();
      assert.equal(await player.getByRole('slider').count(),0,'long clips play without full waveform decoding');
      await player.getByRole('button',{name:/^Play audio/}).click();
      await player.locator('audio[controls]').waitFor();
      await page.close();
      reads.length = 0;
    }
    if (!useWebKit) {
      const page = await browser.newPage({viewport:{width:838,height:844}});
      await page.addInitScript(() => {
        OfflineAudioContext.prototype.decodeAudioData = () => Promise.reject(new Error('Waveform decoder unavailable'));
      });
      await page.goto(base + '/?chat=' + direct + '&lang=en');
      const player = page.locator('[data-message-id="' + sentAudio.id + '"] [data-message-audio]');
      await player.locator('[data-message-audio-time]').filter({hasText:/^0:02$/}).waitFor();
      await page.waitForFunction(node => !node.hasAttribute('aria-busy'), await player.elementHandle());
      assert.equal(await player.getByRole('slider').count(),0);
      const before = await player.boundingBox();
      await player.getByRole('button',{name:/^Play audio/}).click();
      await player.locator('audio[controls]').waitFor();
      await page.waitForFunction(node => !node.paused,await player.locator('audio').elementHandle());
      assert.equal((await player.boundingBox()).height,before.height);
      assert.equal(await player.locator('[data-message-audio-time]').textContent(),'0:02');
      await page.close();
      reads.length = 0;
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
      const options = page.locator('#composer-split [data-popover-trigger] button');
      const menu = page.locator('#composer-split').getByRole('menu');
      const imageAction = menu.getByRole('menuitem', {name:locale === 'en' ? 'Image' : 'Imagem', exact:true});
      const fileAction = menu.getByRole('menuitem', {name:locale === 'en' ? 'File' : 'Arquivo', exact:true});
      const scheduleAction = menu.getByRole('menuitem', {name:locale === 'en' ? 'Schedule message' : 'Agendar mensagem', exact:true});
      assert.equal(await composer.inputValue(), '');
      for (const [action, accept] of [[imageAction, 'image/*'], [fileAction, '']]) {
        await options.click();
        await menu.waitFor();
        assert.equal(await menu.getByRole('menuitem').count(), 3);
        assert(await scheduleAction.isEnabled());
        const chooser = page.waitForEvent('filechooser');
        await action.click();
        assert.equal(await (await chooser).element().getAttribute('accept'), accept);
      }
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
      if (!useWebKit && index === 0) {
        await composer.fill('');
        const record = page.getByRole('button', {name:'Record audio',exact:true});
        const send = page.getByRole('button', {name:'Send message',exact:true});
        const audio = page.locator('[data-record-audio]');
        const stop = page.getByRole('button', {name:'Stop recording',exact:true});
        const play = page.getByRole('button', {name:'Play recording',exact:true});
        const position = audio.getByRole('slider', {name:'Recording position',exact:true});
        const cancel = audio.getByRole('button',{name:'Cancel',exact:true});
        const retry = audio.getByRole('button',{name:'Try again',exact:true});
        const geometry = await composer.boundingBox();
        const before = posts.length;
        let sends = 0;
        page.on('request', request => {
          if (new URL(request.url()).pathname === '/ui/messages/send') sends++;
        });
        await page.evaluate(() => {
          const getUserMedia = navigator.mediaDevices.getUserMedia.bind(navigator.mediaDevices);
          navigator.mediaDevices.getUserMedia = async options => {
            navigator.mediaDevices.getUserMedia = getUserMedia;
            await new Promise(resolve => { window.grantMicrophone = resolve; });
            const stream = await getUserMedia(options);
            window.lateMicrophone = stream;
            return stream;
          };
        });
        await record.click();
        await audio.getByText('Waiting for microphone…',{exact:true}).waitFor();
        await page.evaluate(() => window.dispatchEvent(new Event('oie:desktop-hide')));
        await audio.waitFor({state:'hidden'});
        await page.evaluate(() => window.grantMicrophone());
        await page.waitForFunction(() => window.lateMicrophone?.getTracks().every(track => track.readyState === 'ended'));
        assert.equal(posts.length,before);
        await page.evaluate(() => {
          const getUserMedia = navigator.mediaDevices.getUserMedia;
          navigator.mediaDevices.getUserMedia = async () => {
            navigator.mediaDevices.getUserMedia = getUserMedia;
            throw new DOMException('Microphone denied','NotAllowedError');
          };
        });
        await record.click();
        await page.getByText('Could not record audio. Check microphone access.',{exact:true}).waitFor();
        assert(await composer.evaluate(node => node === document.activeElement));
        await record.click();
        await audio.getByText('Recording audio…',{exact:true}).waitFor();
        assert(await composer.isHidden());
        assert(await audio.locator('.oie-audio-recording-dot .animate-ping').isVisible());
        assert.deepEqual(await audio.boundingBox(),geometry);
        await audio.locator('[data-record-audio-wave] canvas').first().waitFor();
        await cancel.click();
        await audio.waitFor({state:'hidden'});
        assert(await composer.isVisible());
        assert.equal(posts.length,before);
        await record.click();
        await audio.locator('[data-record-audio-wave] canvas').first().waitFor();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:02');
        let releaseFailedUpload;
        const failedUpload = new Promise(resolve => { releaseFailedUpload = resolve; });
        await page.route('**/ui/scheduling/stage?*', async route => {
          await failedUpload;
          await route.continue();
        }, {times:1});
        state.stageStatus = 503;
        await stop.click();
        try {
          await send.click();
          assert.equal(await send.getAttribute('aria-disabled'),'true');
          assert.equal(sends,0);
        } finally {
          releaseFailedUpload();
        }
        await position.waitFor();
        await retry.waitFor();
        await page.locator('#toast-container').getByText('Could not attach this file.',{exact:true}).waitFor();
        assert(await send.isDisabled());
        state.stageStatus = 201;
        let releaseRetryUpload;
        const retryUpload = new Promise(resolve => { releaseRetryUpload = resolve; });
        await page.route('**/ui/scheduling/stage?*', async route => {
          await retryUpload;
          await route.continue();
        }, {times:1});
        const stagedRetry = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/scheduling/stage');
        try {
          await retry.click();
          assert(await send.isEnabled(),'send is available during an upload retry');
        } finally {
          releaseRetryUpload();
        }
        assert((await stagedRetry).ok());
        assert(Number(await position.getAttribute('aria-valuemax')) >= 1);
        await page.waitForFunction(() => !document.querySelector('#composer-send').disabled);
        assert(await cancel.evaluate(node => node === document.activeElement));
        assert.deepEqual(await audio.boundingBox(),geometry);
        assert(await send.isVisible());
        assert(await composer.isHidden());
        assert.equal(posts.length,before + 2);
        assert.equal(posts.at(-1),posts.at(-2),'retry stages the same recording');
        assert(files.get(posts.at(-1)).length > 0);
        assert.equal(sends,0);
        await play.click();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-media]').currentTime > 0);
        const firstRecording = posts.at(-1);
        const earlier = page.locator('[data-message-id="' + ownText.id + '"]');
        await earlier.locator('[data-message-menu-trigger]').click();
        const editTarget = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/message-edit-target');
        await earlier.locator('#edit-message-' + ownText.id).click();
        assert((await editTarget).ok());
        await composer.waitFor();
        assert.equal(await composer.inputValue(),ownText.originalText);
        assert(await composer.evaluate(node => node === document.activeElement));
        assert(await audio.isHidden());
        await page.reload();
        await page.locator('[data-edit-cancel]').waitFor();
        assert.equal(await composer.inputValue(),ownText.originalText);
        assert(await composer.isVisible());
        assert(await audio.isHidden());
        await page.locator('[data-edit-cancel]').click();
        await position.waitFor();
        assert(await cancel.evaluate(node => node === document.activeElement));
        assert(await composer.isHidden());
        await cancel.click();
        await composer.waitFor();

        await record.click();
        await audio.locator('[data-record-audio-wave] canvas').first().waitFor();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:01');
        await stop.click();
        await position.waitFor();
        await page.waitForFunction(() => !document.querySelector('#composer-send').disabled);
        assert.equal(posts.length,before + 3);
        assert.notEqual(posts.at(-1),firstRecording);
        assert.equal(sends,0);
        await play.click();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-media]').currentTime > 0);
        const newRecordingID = posts.at(-1).split('/').at(-1);
        await page.route(url => url.pathname === '/ui/scheduling/file' && url.searchParams.get('attachmentId') === newRecordingID,
          route => route.fulfill({contentType:'audio/webm',body:'Corrupt recording'}), {times:1});
        await page.reload();
        await retry.waitFor();
        await page.locator('#toast-container').getByText('Could not play this audio.',{exact:true}).waitFor();
        assert(await play.isDisabled());
        await retry.click();
        await position.waitFor();
        assert(await play.isEnabled());
        assert(await composer.isHidden());
        state.rejectSend = true;
        const rejected = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send');
        await send.click();
        assert.equal((await rejected).status(),422);
        await page.waitForFunction(() => document.activeElement?.id === 'composer-send' && !document.activeElement.disabled);
        assert(await audio.isVisible());
        assert(await play.isEnabled());
        state.rejectSend = false;
        const accepted = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send');
        await send.click();
        assert.equal((await (await accepted).json()).accepted,true);
        await audio.waitFor({state:'hidden'});
        await composer.waitFor();
        assert.equal(await composer.inputValue(),'');
        await page.reload();
        await page.locator('[data-message-id="' + id(35) + '"]').waitFor();
        assert(await audio.isHidden());
        assert.equal(sends,2);
        assert.equal(sendRequests.length,2);
        assert.deepEqual(sendRequests[1].attachmentIds,[newRecordingID]);
        assert.equal(sendRequests[1].originalText,'');

        await record.click();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:01');
        let releaseUpload;
        const upload = new Promise(resolve => { releaseUpload = resolve; });
        await page.route('**/ui/scheduling/stage?*', async route => {
          await upload;
          await route.continue();
        }, {times:1});
        const uploadRequest = page.waitForRequest(request => new URL(request.url()).pathname === '/ui/scheduling/stage');
        await stop.click();
        const attachmentID = new URL((await uploadRequest).url()).searchParams.get('attachmentId');
        const queued = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send');
        try {
          await audio.evaluate(panel => {
            const nodes = [panel, panel.closest('form'), document.getElementById('message-timeline')];
            window.uploadFrames = [];
            const sample = () => {
              window.uploadFrames.push(nodes.flatMap(node => {
                const {x,y,width,height} = node.getBoundingClientRect();
                return [x,y,width,height,node.scrollTop];
              }));
              window.uploadFrame = requestAnimationFrame(sample);
            };
            sample();
          });
          await send.click();
          assert.equal(await send.getAttribute('aria-disabled'),'true');
          assert(await send.evaluate(node => node === document.activeElement));
          await page.locator('#send-message-form').evaluate(form => form.requestSubmit());
          assert.equal(sends,2,'no message request before the attachment is staged');
          const frames = await page.evaluate(() => {
            cancelAnimationFrame(window.uploadFrame);
            return window.uploadFrames;
          });
          assert(frames.length > 1);
          for (const frame of frames) assert(frame.every((value,index) => Math.abs(value-frames[0][index]) <= 1));
        } finally {
          releaseUpload();
        }
        assert.equal((await (await queued).json()).accepted,true);
        await audio.waitFor({state:'hidden'});
        assert.equal(sends,3,'upload completion dispatches the queued click once');
        assert.equal(sendRequests.length,3);
        assert.deepEqual(sendRequests[2].attachmentIds,[attachmentID]);

        await record.click();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:01');
        let releaseCancelledUpload;
        const cancelledUpload = new Promise(resolve => { releaseCancelledUpload = resolve; });
        let uploadRouted;
        const cancelPageUpload = new Promise(resolve => { uploadRouted = resolve; });
        await page.route('**/ui/scheduling/stage?*', async route => {
          uploadRouted();
          await cancelledUpload;
          await route.continue().catch(() => {});
        }, {times:1});
        await stop.click();
        await cancelPageUpload;
        try {
          await send.click();
          await cancel.click();
          await record.waitFor();
          assert(await composer.evaluate(node => node === document.activeElement));
        } finally {
          releaseCancelledUpload();
        }
        assert.equal(sends,3,'discarding a pending voice draft cancels its queued send');

        await record.click();
        await page.waitForFunction(() => document.querySelector('[data-record-audio-time]').textContent === '0:01');
        let finishBackgroundUpload;
        const backgroundUpload = new Promise(resolve => { finishBackgroundUpload = resolve; });
        await page.route('**/ui/scheduling/stage?*', async route => {
          await backgroundUpload;
          await route.continue();
        }, {times:1});
        const background = page.waitForRequest(request => new URL(request.url()).pathname === '/ui/scheduling/stage');
        await stop.click();
        const backgroundID = new URL((await background).url()).searchParams.get('attachmentId');
        const backgroundSend = page.waitForRequest(request => new URL(request.url()).pathname === '/ui/messages/send');
        try {
          await send.click();
          await page.locator('#conversation-list a[href*="chat=' + room + '"]').first().click();
          await page.waitForFunction(id => document.body.dataset.conversationId === id, room);
          assert.equal(sends,3);
        } finally {
          finishBackgroundUpload();
        }
        const originalScope = new URLSearchParams((await backgroundSend).postData());
        assert.equal(originalScope.get('conversationId'),direct);
        assert.deepEqual(JSON.parse(originalScope.get('attachmentIds')),[backgroundID]);
        assert.equal(sends,4,'switching conversations preserves the explicitly requested send');
      }
      assert.deepEqual(errors,[]);
      await page.close();
      reads.length = 0;
    }
    console.log('PASS: attachment staging and video previews, voice capture, playback during waveform preparation, and queued voice sends');
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
