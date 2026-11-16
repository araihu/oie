const assert = require('node:assert/strict');
const http = require('node:http');
const { once } = require('node:events');
const path = require('node:path');
const { chromium } = require('playwright');
const { startOie } = require('./fixtures/oie-server.cjs');

const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const engine = id(1), account = id(2), chat = id(3), other = id(4), revision = id(5), media = id(6);
const date = Date.UTC(2026, 10, 12, 16), timestamp = new Date(date).toISOString();
const attachment = 'https://files.example.test/roteiro.wav';
const stagedAudio = id(7);
const messages = Array.from({length: 110}, (_, index) => ({
  id: id(100 + index), revisionId: revision, accountId: account, conversationId: chat,
  sender: {id: id(9), displayName: 'Lia'}, originalText: 'Trilha de sábado · mensagem ' + (index + 1),
  isOwn: false, sendState: 'received', createdAt: new Date(date + index * 1000).toISOString(),
}));
messages[21].originalText = 'Gravei o caminho até a entrada do parque.\n' + attachment;
const first = messages[20], newest = messages.at(-1), audioMessage = messages[21];
const owner = {id: account, revisionId: revision, label: 'Pessoal', address: 'marina@example.test', protocol: 'xmpp', connectionState: 'connected', createdAt: timestamp};
const connection = {accountId: account, state: 'connected', updatedAt: timestamp};
const chats = [chat, other].map((value, index) => ({
  id: value, revisionId: revision, accountId: account, title: index ? 'Café da manhã' : 'Trilha de sábado',
  address: index ? 'jo@example.test' : 'lia@example.test', kind: 'direct', lastMessage: index ? 'Combinado!' : 'Até sábado!',
  updatedAt: timestamp, unreadCount: index ? 0 : messages.length - 20, archived: false, favorite: false,
}));
const policies = new Map(), files = new Set();
const state = {reads: 0, failMedia: false, holdContext: null, firstUnread: first.id};
const wav = Buffer.alloc(44 + 16000 * 2 * 2);
wav.write('RIFF'); wav.writeUInt32LE(wav.length - 8, 4); wav.write('WAVEfmt ', 8);
wav.writeUInt32LE(16, 16); wav.writeUInt16LE(1, 20); wav.writeUInt16LE(1, 22);
wav.writeUInt32LE(16000, 24); wav.writeUInt32LE(32000, 28); wav.writeUInt16LE(2, 32); wav.writeUInt16LE(16, 34);
wav.write('data', 36); wav.writeUInt32LE(wav.length - 44, 40);
for (let index = 44; index < wav.length; index += 2) wav.writeInt16LE(Math.round(Math.sin((index - 44) * Math.PI * 440 / 16000) * 1200), index);

const api = http.createServer(async (request, response) => {
  const url = new URL(request.url, 'http://fixtures.test');
  const send = (status, json) => {
    response.writeHead(status, {'Content-Type': 'application/json'});
    response.end(JSON.stringify(json));
  };
  const {pathname: endpoint} = url;
  if (endpoint === '/api/v1/engine') return send(200, {id: engine, role: 'local', apiVersion: '0.38.0', eventCursor: engine + ':0'});
  if (endpoint === '/api/v1/events') {
    response.writeHead(200, {'Content-Type': 'text/event-stream'});
    response.write(': ready\n\n');
    return;
  }
  if (endpoint === '/api/v1/accounts') return send(200, {items: [owner]});
  if (endpoint === '/api/v1/accounts/' + account) return send(200, owner);
  if (endpoint.endsWith('/connection')) return send(200, connection);
  if (endpoint.endsWith('/history')) return send(200, {accountId: account, state:'complete', retainedMessages: messages.length, conversations: 2, updatedAt:timestamp});
  if (endpoint === '/api/v1/conversations') return send(200, {items: chats});
  for (const entry of chats) {
    const base = '/api/v1/conversations/' + entry.id;
    if (endpoint === base) return send(200, entry);
    if (endpoint === base + '/details') return send(200, {conversationId: entry.id, accountId: account, kind: 'direct', address: entry.address, state: 'known', accountConnection: connection, checkedAt: timestamp, participants: {scope: 'peer', state: 'unknown', items: []}, features: []});
    if (endpoint === base + '/read-state' && request.method === 'GET') return send(200, {conversationId: entry.id, accountId: account, unreadCount: entry.unreadCount, ...(entry.unreadCount ? {firstUnreadMessageId: state.firstUnread} : {})});
    if (endpoint === base + '/messages') {
      const collection = entry.id === chat ? messages : [];
      const limit = Number(url.searchParams.get('limit') || 50), initial = Math.max(0, collection.length - limit);
      return send(200, {items: collection.slice(initial).reverse(), nextCursor: initial ? collection[initial].id : undefined});
    }
    if (endpoint === base + '/staged-attachments/' + stagedAudio) {
      if (request.headers.range !== 'bytes=0-1') return send(400, {status: 400});
      response.writeHead(206, {'Content-Type': 'application/octet-stream', 'Content-Length': 2, 'Content-Range': 'bytes 0-1/' + wav.length,
        'Accept-Ranges': 'bytes', 'X-Oie-Attachment-Type': 'audio/wav', 'Content-Disposition': 'attachment; filename="roteiro.wav"'});
      return response.end(wav.subarray(0, 2));
    }
    const target = endpoint.match(new RegExp('^' + base + '/messages/([^/]+)/context$'))?.[1];
    if (target && entry.id === chat) {
      const index = messages.findIndex(message => message.id === target);
      if (index < 0) return send(404, {status: 404});
      if (state.holdContext) await state.holdContext;
      const limit = Number(url.searchParams.get('limit') || 50);
      const from = Math.max(0, index - Math.floor(limit / 2)), end = Math.min(from + limit, messages.length);
      return send(200, {targetId: target, items: messages.slice(from, end).reverse(), hasOlder: from > 0, hasNewer: end < messages.length});
    }
  }
  if (endpoint === '/api/v1/shared-content') {
    const playable = url.searchParams.get('type') === 'audio' && url.searchParams.getAll('messageId').includes(audioMessage.id);
    return send(200, {items: playable ? [{id: media, revisionId: revision, kind:'audio', url: attachment, fileName: 'roteiro.wav', message: audioMessage, accountLabel: owner.label, conversationTitle: chats[0].title}] : [], indexing: {state: 'complete', indexedMessages: messages.length, totalMessages: messages.length}});
  }
  if (endpoint === '/api/v1/shared-content/' + media + '/content') {
    state.reads++;
    await new Promise(resolve => setTimeout(resolve, 120));
    if (state.failMedia) return send(502, {status: 502, code: 'media_fetch_failed'});
    const download = url.searchParams.get('download') === 'true';
    if (download && request.headers['x-oie-download-device']) files.add(request.headers['x-oie-download-device']);
    const partial = !download && request.headers.range;
    const headers = {'Content-Type':'audio/wav', 'Content-Length':wav.length, 'Content-Disposition':'inline; filename="roteiro.wav"'};
    if (partial) Object.assign(headers, {'Content-Range': 'bytes 0-' + (wav.length-1) + '/' + wav.length, 'Accept-Ranges': 'bytes'});
    response.writeHead(partial ? 206 : 200, headers);
    return response.end(wav);
  }
  const device = endpoint.match(/^\/api\/v1\/downloads\/([^/]+)\/retention$/)?.[1];
  if (device) {
    if (request.method === 'PUT') {
      let content = '';
      for await (const chunk of request) content += chunk;
      policies.set(device, JSON.parse(content).days);
    }
    if (request.method === 'DELETE') files.delete(device);
    return send(200, {days: policies.get(device) || 0, count: Number(files.has(device))});
  }
  if (endpoint.endsWith('/avatars') || endpoint.endsWith('/previews') || endpoint.endsWith('/message-stars')) return send(200, {items: []});
  return send(404, {status: 404, code: 'not_found'});
});

async function test(base) {
  const browser = await chromium.launch({headless: true});
  try {
    for (const [lang, width] of [['en', 1280], ['pt-BR', 390]]) {
      const context = await browser.newContext({viewport:{width, height: 820}, isMobile: width < 500, hasTouch: width < 500, reducedMotion:'reduce', acceptDownloads: true});
      const page = await context.newPage(), errors = [];
      page.on('pageerror', error => errors.push(error.message));
      let release;
      state.holdContext = new Promise(resolve => {release = resolve;});
      const address = new URL('/?lang=' + lang + '&chat=' + other, base);
      await page.goto(address.href);
      await page.evaluate(({id}) => {
        const samples = [];
        window.anchorFrames = samples;
        function sample() {
          const timeline = document.getElementById('message-timeline');
          const row = timeline?.querySelector('[data-message-id="' + id + '"]');
          if (row) samples.push(row.getBoundingClientRect().top - timeline.getBoundingClientRect().top);
          window.anchorFrame = requestAnimationFrame(sample);
        }
        sample();
      }, first);
      if (width < 500) await page.locator('.oie-chat-pane').getByRole('link', {name: 'Voltar'}).first().click();
      const target = page.locator('#conversation-list a[data-conversation-id="' + chat + '"]');
      const beforeOpen = state.reads;
      state.failMedia = true;
      const selectChat = target.click();
      await page.waitForTimeout(150);
      assert.equal(await page.locator('#message-timeline [data-message-id="' + first.id + '"]').count(), 0);
      release();
      state.holdContext = null;
      await selectChat;
      const timeline = page.locator('#message-timeline');
      await timeline.locator('[data-message-id="' + first.id + '"]').waitFor();
      const samples = await page.evaluate(() => {cancelAnimationFrame(window.anchorFrame); return window.anchorFrames;});
      assert(samples.length > 0 && samples.every(offset => Math.abs(offset) < 3), 'first painted frame is positioned on the unread anchor: ' + samples);
      assert(!new URL(page.url()).searchParams.has('message'));
      assert.equal(await timeline.locator('[data-message-id]').count(), 50);
      const audio = timeline.locator('[data-audio-player]');
      const player = timeline.locator('[data-message-audio]');
      const play = player.getByRole('button', {name: lang === 'en' ? /^Play audio/ : /^Reproduzir áudio/});
      await play.waitFor();
      assert.equal(await audio.getAttribute('controlslist'), 'nodownload');
      assert(await audio.evaluate(player => player.paused));
      await play.scrollIntoViewIfNeeded();
      await play.focus();
      await page.evaluate(() => {
        const selectors = ['.oie-audio-controls', '#message-composer', '#message-timeline'];
        window.audioFrames = [];
        const sample = () => {
          window.audioFrames.push(selectors.map(selector => document.querySelector(selector).getBoundingClientRect().toJSON()));
          window.audioFrame = requestAnimationFrame(sample);
        };
        sample();
      });
      const failedRead = page.waitForResponse(response => response.url().includes('/' + media + '/content') && response.status() === 502);
      await play.press('Enter');
      await failedRead;
      await page.waitForFunction(node => !node.hasAttribute('aria-busy'), await player.elementHandle());
      assert(await play.evaluate(button => button === document.activeElement));
      state.failMedia = false;
      await play.press('Enter');
      await page.waitForFunction(() => document.querySelector('[data-audio-player]')?.currentTime > .1);
      assert(await play.evaluate(button => document.activeElement === button));
      assert(state.reads > beforeOpen);
      const frames = await page.evaluate(() => {cancelAnimationFrame(window.audioFrame); return window.audioFrames;});
      assert(frames.length > 2, 'sampled painted frames during playback preparation');
      for (const frame of frames) assert.deepEqual(frame, frames[0], 'loading and playing do not move the controls or composer');

      assert.equal(await timeline.locator('.oie-audio-card a[download]').count(), 0);
      const audioRow = timeline.locator('[data-message-id="' + audioMessage.id + '"]');
      const messageMenu = audioRow.locator('[data-message-menu-trigger]');
      await messageMenu.click();
      const downloadEvent = page.waitForEvent('download');
      await page.getByRole('menuitem', {name: lang === 'en' ? 'Download audio' : 'Baixar áudio', exact: true}).click();
      const download = await downloadEvent;
      assert.equal(download.suggestedFilename(), 'roteiro.wav');
      assert.equal(await download.failure(), null);
      const staged = await context.request.get(new URL('/ui/scheduling/file?' + new URLSearchParams({
        engineId: engine, conversationId: chat, attachmentId: stagedAudio,
      }), base).href, {headers: {Range: 'bytes=0-1'}});
      assert.equal(staged.status(), 206);
      assert.equal(staged.headers()['content-type'], 'audio/wav');
      assert.equal(staged.headers()['content-range'], 'bytes 0-1/' + wav.length);
      assert.deepEqual(await staged.body(), wav.subarray(0, 2));

      if (width < 500) {
        await page.locator('.oie-chat-pane').getByRole('link', {name: 'Voltar'}).first().click();
        await page.getByRole('button', {name:'Abrir navegação', exact:true}).click();
      }
      await page.locator('[data-navigation-settings]:visible').click();
      await page.getByRole('tab', {name: lang === 'en' ? 'This device' : 'Este dispositivo', exact: true}).click();
      const settings = page.locator('[data-download-retention]');
      const days = settings.locator('select');
      const feedback = settings.getByRole('status');
      const retentionRoute = '**/ui/settings/download-retention';
      const requestedPeriods = [];
      const releaseSave = Promise.withResolvers();
      const firstSave = page.waitForRequest(request =>
        request.url().endsWith('/ui/settings/download-retention') && request.method() === 'POST');
      await page.route(retentionRoute, async route => {
        const request = route.request();
        if (request.method() === 'POST') {
          requestedPeriods.push(new URLSearchParams(request.postData()).get('days'));
          if (requestedPeriods.length === 1) await releaseSave.promise;
        }
        await route.continue();
      });
      await days.selectOption('7');
      await firstSave;
      await days.focus();
      const remove = settings.locator('[data-remove-device-downloads]');
      const before = await remove.boundingBox();
      await days.selectOption('90');
      await days.selectOption('30');
      assert(await days.evaluate(input => document.activeElement === input));
      assert.deepEqual(requestedPeriods, ['7']);
      const paintShifts = await remove.evaluate((element, origin) => new Promise(resolve => {
        const shifts = [];
        const sample = () => {
          const rect = element.getBoundingClientRect();
          shifts.push(Math.max(...['x', 'y', 'width', 'height'].map(key => Math.abs(rect[key] - origin[key]))));
          if (shifts.length === 10) resolve(shifts);
          else requestAnimationFrame(sample);
        };
        requestAnimationFrame(sample);
      }), before);
      assert(paintShifts.every(shift => shift <= 1), JSON.stringify(paintShifts));
      const latest = page.waitForResponse(response =>
        response.url().endsWith('/ui/settings/download-retention') &&
        new URLSearchParams(response.request().postData()).get('days') === '30' && response.ok());
      releaseSave.resolve();
      assert.equal((await (await latest).json()).days, 30);
      assert.deepEqual(requestedPeriods, ['7', '30']);
      const saved = lang === 'en' ? 'Download preference saved.' : 'Preferência de downloads salva.';
      await feedback.getByText(saved).waitFor();
      assert.equal(await days.inputValue(), '30');

      await page.route(retentionRoute, route => route.fulfill({status: 503}), {times: 1});
      await days.selectOption('90');
      await feedback.getByText(lang === 'en' ? 'Could not update downloaded files.' : 'Não foi possível atualizar os arquivos baixados.').waitFor();
      assert.equal(await days.inputValue(), '30');
      assert(await days.evaluate(input => document.activeElement === input));
      await days.selectOption('90');
      await feedback.getByText(saved).waitFor();
      assert.equal(await days.inputValue(), '90');
      await page.unroute(retentionRoute);
      await settings.locator('[data-remove-device-downloads]').click();
      await page.locator('#remove-device-downloads-dialog [data-confirm-remove-downloads]').click();
      await settings.getByRole('status').getByText(lang === 'en' ? 'Downloaded files removed.' : 'Arquivos baixados excluídos.').waitFor();

      await page.goto(new URL('/?chat=' + chat + '&lang=' + lang, base).href);
      await page.locator('[data-jump-latest]').click();
      await page.locator('[data-message-id="' + newest.id + '"]').waitFor();
      assert.equal(await page.locator('[data-message-id="' + first.id + '"]').count(), 0);
      assert(!new URL(page.url()).searchParams.has('message'));
      state.firstUnread = messages[94].id;
      await page.goto(new URL('/?chat=' + chat + '&lang=' + lang, base).href);
      state.firstUnread = first.id;
      const jump = page.locator('[data-jump-latest]');
      await jump.waitFor({state:'visible'});
      assert.equal(await page.locator('#message-timeline').getAttribute('data-timeline-newer'), 'false');
      await jump.click();
      await jump.waitFor({state:'hidden'});
      await page.waitForFunction(() => window.OieTimeline?.atBottom());
      await page.goto(new URL('/?chat=' + chat + '&message=' + messages[74].id + '&lang=' + lang, base).href);
      const explicit = page.locator('[data-message-id="' + messages[74].id + '"]');
      await explicit.waitFor();
      assert(await explicit.evaluate(row => document.activeElement === row));
      await page.goto(new URL('/?chat=' + chat + '&message=' + id(999) + '&lang=' + lang, base).href);
      await page.locator('[data-return-latest]').click();
      await page.waitForURL(url => !url.searchParams.has('message'));
      await page.locator('[data-message-id="' + newest.id + '"]').waitFor();
      assert.equal(await page.locator('[data-message-id="' + first.id + '"]').count(), 0);
      assert(!new URL(page.url()).searchParams.has('message'));
      assert.deepEqual(errors, []);
      await context.close();
    }
  } finally {
    await browser.close();
  }
}

(async () => {
  api.listen(0, '127.0.0.1');
  await once(api, 'listening');
  const app = await startOie(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--role','bff','--engine-url','http://127.0.0.1:' + api.address().port], '/');
  try {
    if (process.argv.includes('--preview')) {
      process.stdout.write(new URL('/?chat=' + chat + '&lang=pt-BR', app.url).href + '\n');
      await new Promise(resolve => {process.once('SIGTERM', resolve); process.once('SIGINT', resolve);});
    } else {
      await test(app.url);
      console.log('Unread positioning, explicit navigation, audio playback and device download settings passed');
    }
  } finally {
    await app.stop();
    api.closeAllConnections();
    api.close();
  }
})().catch(error => {console.error(error); process.exitCode = 1;});
