const assert = require('node:assert/strict');
const http = require('node:http');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {createInterface} = require('node:readline');

const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const [engine, account, chat, other, revision, room, archivedRoom, own, someoneElse, foreign] = [1, 2, 3, 4, 5, 6, 7, 8, 10, 11].map(id);
const replyTo = messageId => ({available: true, messageId, bodyText: "Thanks", excerpt: "Hello"});
const now = '2026-11-14T18:00:00Z';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
function fixtureTone(seconds) {
  const rate = 24000;
  const samples = rate * seconds;
  const buffer = Buffer.alloc(44 + samples * 2);
  buffer.write('RIFF', 0);
  buffer.writeUInt32LE(buffer.length - 8, 4);
  buffer.write('WAVEfmt ', 8);
  buffer.writeUInt32LE(16, 16);
  buffer.writeUInt16LE(1, 20);
  buffer.writeUInt16LE(1, 22);
  buffer.writeUInt32LE(rate, 24);
  buffer.writeUInt32LE(rate * 2, 28);
  buffer.writeUInt16LE(2, 32);
  buffer.writeUInt16LE(16, 34);
  buffer.write('data', 36);
  buffer.writeUInt32LE(samples * 2, 40);
  for (let i = 0; i < samples; i++) buffer.writeInt16LE(Math.round(12000 * Math.sin(i * Math.PI / 30)), 44 + i * 2);
  return {name: 'Meu toque.wav', mimeType: 'audio/wav', buffer};
}

async function until(test, label) {
  for (let i = 0; i < 100; i++) {
    if (await test()) return;
    await pause(50);
  }
  throw Error('Timed out: ' + label);
}

// Headless Chromium has no notification center to supply click activation.
// Exercise the real worker and its window discovery, then follow the URL
// recorded by the openWindow probe in a real browser page.
async function clickWorkerBanner(worker, data) {
  return worker.evaluate(async data => {
    const focus = WindowClient.prototype.focus;
    const openWindow = clients.openWindow;
    let destination, closed = false, focused = false, done;
    WindowClient.prototype.focus = async function () { focused = true; return this; };
    clients.openWindow = async url => { destination = url; };
    try {
      const event = new Event('notificationclick');
      event.notification = {data, close() { closed = true; }};
      event.waitUntil = promise => { done = promise; };
      self.dispatchEvent(event);
      await done;
      return {closed, focused, destination};
    } finally {
      WindowClient.prototype.focus = focus;
      clients.openWindow = openWindow;
    }
  }, data);
}

async function run() {
  const streams = new Set();
  const events = [];
  const eventAttempts = [];
  const messages = new Map();
  let seq = 0, activeEngine = engine, engineAvailable = true, eventsAvailable = true, resetsRemaining = 0;
  const chats = [[chat, 'direct', false], [other, 'direct', false], [room, 'room', false], [archivedRoom, 'room', true]]
    .map(([id, kind, archived], index) => ({
      id, revisionId: revision, accountId: account, kind, archived, address: 'chat' + index + '@example',
      title: 'Conversation ' + index, updatedAt: now, unreadCount: 1, favorite: false
    }));
  const retained = new Map([
    [own, {conversationId: room, isOwn: true}],
    [someoneElse, {conversationId: room, isOwn: false}],
    [foreign, {conversationId: other, isOwn: true}]
  ]);
  const profile = {id: account, revisionId: revision, protocol: 'xmpp', address: 'alice@example', createdAt: now};

  function push(overrides = {}, type = 'message.created', repeat) {
    const message = {id: id(100 + seq), revisionId: revision, accountId: account, conversationId: other,
      sender: {id: id(9), displayName: 'Bob'}, originalText: 'Private body', createdAt: now,
      isOwn: false, sendState: 'received', ...overrides};
    const entry = repeat ?? `id: ${engine}:${++seq}\nevent: ${type}\ndata: ${JSON.stringify({message})}\n\n`;
    if (!repeat) {
      events.push(entry);
      messages.set(message.id, message);
    }
    for (const stream of streams) stream.write(entry);
    return {message, entry};
  }

  const api = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://fixture');
    const send = (value, status = 200) => {
      res.writeHead(status, {'Content-Type': 'application/json'}).end(JSON.stringify(value));
    };
    const route = url.pathname;
    if (!engineAvailable || route === '/api/v1/events' && !eventsAvailable) return send({status: 503}, 503);
    if (route === '/api/v1/engine') return send({id: activeEngine, role: 'hosted', apiVersion: '0.31.0', eventCursor: activeEngine + ':' + seq});
    if (route === '/api/v1/accounts') return send({items: [profile]});
    if (route === '/api/v1/accounts/' + account) return send(profile);
    if (route.endsWith('/connection')) return send({accountId: account, state: 'connected', updatedAt: now});
    if (route === '/api/v1/conversations') return send({items: chats.filter(c => c.archived === (url.searchParams.get('archived') === 'true'))});
    const originalID = route.split('/').at(-1);
    if (route.startsWith('/api/v1/messages/') && retained.has(originalID)) {
      return send({id: originalID, accountId: account, revisionId: revision, originalText: 'Hello', ...retained.get(originalID)});
    }
    const context = /^\/api\/v1\/conversations\/([^/]+)\/messages\/([^/]+)\/context$/.exec(route);
    if (context) {
      const message = messages.get(context[2]);
      if (message?.conversationId === context[1]) return send({targetId: message.id, items: [message], hasOlder: false, hasNewer: false});
    }
    const selected = chats.find(c => route === '/api/v1/conversations/' + c.id);
    if (selected) return send(selected);
    if (/\/(messages|avatars|previews|message-stars)$/.test(route)) return send({items: []});
    if (route === '/api/v1/events') {
      const cursor = req.headers['last-event-id'];
      eventAttempts.push({cursor, at: performance.now()});
      if (resetsRemaining > 0) {
        resetsRemaining--;
        return send({status: 409}, 409);
      }
      if (cursor && !cursor.startsWith(activeEngine + ':')) return send({status: 409}, 409);
      res.writeHead(200, {'Content-Type': 'text/event-stream'});
      res.write(': ready\n\n');
      const after = Number(cursor?.split(':').at(-1) || 0);
      for (const event of events.slice(after)) res.write(event);
      streams.add(res);
      res.on('close', () => streams.delete(res));
      return;
    }
    send({status: 404, code: 'not_found'}, 404);
  });
  api.listen(0, '127.0.0.1');
  await once(api, 'listening');
  const reserve = http.createServer().listen(0, '127.0.0.1');
  await once(reserve, 'listening');
  const port = reserve.address().port;
  await new Promise(resolve => reserve.close(resolve));
  const base = 'http://127.0.0.1:' + port;
  const engineURL = 'http://127.0.0.1:' + api.address().port;
  const app = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--role', 'bff', '--listen', '127.0.0.1:' + port, '--engine-url', engineURL],
    {stdio: ['ignore', 'ignore', 'inherit']});
  let browser;
  try {
    await until(async () => { try { return (await fetch(base)).ok; } catch { return false; } }, 'BFF');
    if (process.argv.includes('--manual')) {
      console.log(`Web UI: ${base}\nWails --engine-url: ${engineURL}`);
      console.log('Enter sends a message to Conversation 1. Type a label before Enter to use it as the message text.');
      const input = createInterface({input: process.stdin, output: process.stdout});
      input.on('SIGINT', () => input.close());
      for await (const line of input) {
        if (!streams.size) {
          console.log('Open Oiê and wait for it to connect before sending a test message.');
          continue;
        }
        const {message} = push({originalText: line.trim() || 'Notification test ' + (seq + 1)});
        console.log(`${message.originalText} (${message.id})`);
      }
      return;
    }

    const {chromium} = require('playwright');
    browser = await chromium.launch({headless: true});
    const profile = await browser.newContext({viewport: {width: 1440, height: 900}});
    await profile.addInitScript(() => {
      window.alerts = [];
      window.playedSounds = [];
      window.prompts = 0;
      window.answerPermission = () => {};
      ServiceWorkerRegistration.prototype.showNotification = async function () {
        throw Error('Notification worker unavailable');
      };
      window.testFocus = false;
      Object.defineProperty(document, 'hasFocus', {value: () => window.testFocus});
      window.Notification = class {
        static permission = 'default';
        static requestPermission() {
          window.prompts++;
          return new Promise(resolve => {
            window.answerPermission = value => { this.permission = value; resolve(value); };
          });
        }
        constructor(title, options) {
          assertGranted();
          Object.assign(this, {title, options});
          window.alerts.push(this);
        }
        close() {}
      };
      function assertGranted() {
        if (window.Notification.permission !== 'granted') throw Error('Permission not granted');
      }
      const startSound = AudioBufferSourceNode.prototype.start;
      AudioBufferSourceNode.prototype.start = function (...args) {
        window.playedSounds.push(this.buffer);
        return startSound.apply(this, args);
      };
    });
    for (const [width, lang] of [[1440, 'en'], [390, 'pt-BR']]) {
      engineAvailable = false;
      const cold = await profile.newPage();
      await cold.setViewportSize({width, height: 900});
      const address = base + '/?lang=' + lang + '&chat=' + chat;
      const response = await cold.goto(address);
      assert.equal(response.status(), 503);
      await cold.locator('body[data-oie-live-ready]').waitFor();
      assert.equal(await cold.locator('#oie-route[data-engine-unavailable]').count(), 1);
      assert.equal(await cold.locator('#message-composer').count(), 0);
      await cold.evaluate(() => { window.initialDocument = true; });
      engineAvailable = true;
      await cold.evaluate(() => window.dispatchEvent(new Event('online')));
      await cold.locator('#message-composer').waitFor({state: 'visible'});
      await cold.waitForFunction(() => window.OieEvents.connected);
      assert.equal(await cold.evaluate(() => window.initialDocument), true);
      assert.equal(cold.url(), address);
      await cold.close();
      await until(() => streams.size === 0, 'cold-start stream closed');
    }
    const resetPage = await profile.newPage();
    await resetPage.addInitScript(() => {
      window.EventSource = class extends EventSource {
        constructor(...options) {
          super(...options);
          this.addEventListener('message.created', event => { window.lastWireEvent = event.lastEventId; });
        }
      };
    });
    const resetStart = eventAttempts.length;
    resetsRemaining = 2;
    await resetPage.goto(base + '/?chat=' + chat);
    await resetPage.waitForFunction(() => window.OieEvents.connected);
    const resetAttempts = eventAttempts.slice(resetStart);
    assert.equal(resetAttempts.length, 3);
    assert(resetAttempts[1].at - resetAttempts[0].at >= 350, 'first reset backs off');
    assert(resetAttempts[2].at - resetAttempts[1].at >= 700, 'repeated reset increases the delay');

    push({conversationId: chat});
    const lastCursor = engine + ':' + seq;
    await resetPage.waitForFunction(cursor => window.OieEvents.latest('') === cursor, lastCursor);
    const staleCursor = engine + ':0', foreignCursor = id(9999) + ':3';
    for (const stream of streams) stream.write(`id: ${staleCursor}\nevent: message.created\ndata: {}\n\nid: ${foreignCursor}\nevent: message.created\ndata: {}\n\n`);
    await resetPage.waitForFunction(cursor => window.lastWireEvent === cursor, foreignCursor);
    assert.equal(await resetPage.evaluate(() => window.OieEvents.latest('')), lastCursor);
    const reconnectStart = eventAttempts.length;
    for (const stream of streams) stream.end();
    await until(() => eventAttempts.length > reconnectStart, 'resume after invalid event IDs');
    assert.equal(eventAttempts[reconnectStart].cursor, lastCursor);
    await resetPage.waitForFunction(() => window.OieEvents.connected);

    const oldComposer = resetPage.locator('#message-composer');
    await oldComposer.fill('Draft for the original engine');
    await resetPage.route('**/ui/refresh?*', route => route.abort('connectionreset'));
    activeEngine = id(9999);
    for (const stream of streams) stream.end();
    await resetPage.waitForFunction(() => window.OieEvents.replaced);
    assert.equal(await resetPage.locator('body').getAttribute('data-engine-id'), engine);
    assert.equal(await oldComposer.inputValue(), 'Draft for the original engine');
    assert(await resetPage.locator('[data-send-message]').isDisabled(), 'engine replacement blocks writes in the old workspace');
    await resetPage.close();
    activeEngine = engine;
    await until(() => streams.size === 0, 'reset test stream closed');

    const first = await profile.newPage();
    const second = await profile.newPage();
    // The first tab uses Chromium's playback path. Use offline decoding and a
    // playback probe in the second tab to exercise competing window claims.
    await second.addInitScript(() => {
      window.AudioContext = class {
        state = 'running';
        destination = {};

        decodeAudioData(bytes) {
          return new OfflineAudioContext(1, 14400, 48000).decodeAudioData(bytes);
        }

        createBufferSource() {
          return {
            connect() {},
            start() { window.playedSounds.push(this.buffer); }
          };
        }
      };
    });
    const soundRequests = [];
    first.on('request', request => {
      if (new URL(request.url()).pathname === '/ui/notification.wav') soundRequests.push(request);
    });
    first.setDefaultTimeout(8000);
    second.setDefaultTimeout(8000);
    const errors = [];
    for (const page of [first, second]) page.on('pageerror', error => errors.push(error.message));
    await first.goto(base + '/?lang=en&chat=' + chat);
    await second.goto(base + '/?lang=pt-BR');
    await until(() => streams.size === 2, 'both live streams');
    const settings = async page => {
      if (page.viewportSize().width === 390) {
        await page.getByRole('button', {name: 'Abrir navegação', exact: true}).click();
      }
      await page.locator('[data-navigation-settings]:visible').click();
      await page.getByRole('tab', {name: page === first ? 'This device' : 'Este dispositivo'}).click();
      await page.locator('[data-notification-banners]').waitFor({state: 'visible'});
    };
    assert.equal(soundRequests.length, 0, 'the initial page should not load the sound');
    const previewResponse = first.waitForResponse(response => new URL(response.url()).pathname === '/ui/notification.wav');
    await settings(first);
    assert.equal((await previewResponse).headers()['content-type'], 'audio/wav');
    const banners = first.locator('[data-notification-banners]');
    const sound = first.locator('[data-notification-sound]');
    const preview = first.locator('[data-notification-preview]');
    assert.equal(await first.evaluate(() => window.prompts), 0);
    assert.equal(await banners.isChecked(), false);
    assert.equal(await sound.isChecked(), true);
    await first.locator('label:has(#notification-banners)').scrollIntoViewIfNeeded();
    const geometry = async () => first.evaluate(() => {
      const rects = ['#message-timeline', '#send-message-form', 'label:has(#notification-banners)', '[data-notification-preview]']
        .map(selector => document.querySelector(selector).getBoundingClientRect().toJSON());
      return rects.map(({x, y, width, height}) => [x, y, width, height]);
    });
    const before = await geometry();
    await first.evaluate(() => {
      window.geometryFrames = [];
      window.captureGeometry = true;
      const sample = () => {
        window.geometryFrames.push(['#message-timeline', '#send-message-form', 'label:has(#notification-banners)', '[data-notification-preview]']
          .map(selector => { const r = document.querySelector(selector).getBoundingClientRect(); return [r.x, r.y, r.width, r.height]; }));
        if (window.captureGeometry) requestAnimationFrame(sample);
      };
      requestAnimationFrame(sample);
    });
    await first.locator('label:has(#notification-banners)').click();
    await until(() => banners.isDisabled(), 'permission pending');
    await pause(200);
    await first.evaluate(() => window.answerPermission('denied'));
    await until(async () => (await first.locator('#notification-status').textContent()).includes('Allow notifications'), 'denied feedback');
    const frames = await first.evaluate(() => { window.captureGeometry = false; return window.geometryFrames; });
    assert(frames.length >= 3);
    for (const frame of [...frames, await geometry()]) {
      frame.forEach((row, i) => row.forEach((value, j) => assert(Math.abs(value - before[i][j]) <= 1, 'layout shifted')));
    }
    await first.locator('label:has(#notification-banners)').click();
    await first.evaluate(() => window.answerPermission('granted'));
    await until(() => banners.isChecked(), 'enabled banners');
    await preview.click();
    await until(() => first.evaluate(() => window.playedSounds.length === 1), 'bundled preview sound');
    const duration = await first.evaluate(() => window.playedSounds[0].duration);
    assert(Math.abs(duration - 0.3) < 0.045, 'preview should play the 300 ms sound');
    assert.equal(await sound.isChecked(), true);
    assert.equal(await first.locator('#notification-direct').isChecked(), true);
    assert.equal(await first.locator('#notification-archived').isChecked(), false);
    assert.equal(await first.locator('#notification-focused').isChecked(), false);
    assert.equal(await first.locator('[data-notification-room-sound]').inputValue(), 'attention');
    const closeSettings = page => page.locator('#settings-body').evaluate(node =>
      node.dispatchEvent(new CustomEvent('drawer:close', {detail: {id: 'settings'}, bubbles: true})));
    await closeSettings(first);
    await first.evaluate(() => { window.testFocus = true; window.dispatchEvent(new Event('focus')); });
    const total = async field => (await first.evaluate(field => typeof window[field] === 'number' ? window[field] : window[field].length, field)) +
      (await second.evaluate(field => typeof window[field] === 'number' ? window[field] : window[field].length, field));

    push({historical: true});
    push({isOwn: true});
    push({}, 'message.updated');
    push({conversationId: chat});
    await pause(800);
    assert.equal(await total('alerts'), 0);
    assert.equal(await total('playedSounds'), 1);
    const {message, entry} = push();
    await until(async () => (await total('alerts')) === 1 && (await total('playedSounds')) === 2, 'direct message alert and sound');
    assert(await first.evaluate(() => window.playedSounds[0] === window.playedSounds[1]), 'preview and live alert should reuse the same decoded audio');
    assert.equal(soundRequests.length, 1, 'the sound should load only once');
    push({}, 'message.created', entry);
    await pause(500);
    assert.equal(await total('alerts'), 1, 'same-origin claim and repeated ID');
    assert.equal(await total('playedSounds'), 2);

    // Return an HTTP error on reconnect, then replay from the last received ID.
    eventsAvailable = false;
    const failedStream = first.waitForResponse(response => new URL(response.url()).pathname === '/ui/events' && response.status() === 503);
    const composer = first.locator('#message-composer');
    await first.bringToFront();
    await composer.fill('Draft during reconnect');
    const beforeReconnect = await first.evaluate(() => {
      const sample = () => ['#message-composer', '#message-timeline'].map(selector => {
        const {x, y, width, height} = document.querySelector(selector).getBoundingClientRect();
        return [x, y, width, height];
      });
      window.reconnectFrames = [];
      window.reconnectSampling = true;
      const nextFrame = () => {
        if (!window.reconnectSampling) return;
        window.reconnectFrames.push(sample());
        requestAnimationFrame(nextFrame);
      };
      requestAnimationFrame(nextFrame);
      return sample();
    });
    for (const stream of streams) stream.end();
    await failedStream;
    await until(() => streams.size === 0, 'disconnected streams');
    push();
    eventsAvailable = true;
    await until(() => streams.size === 2, 'reconnect after HTTP 503');
    assert.equal(await composer.inputValue(), 'Draft during reconnect');
    assert.equal(await composer.evaluate(node => document.activeElement === node), true);
    const recoveryFrames = await first.evaluate(() => {
      window.reconnectSampling = false;
      return window.reconnectFrames;
    });
    assert(recoveryFrames.length >= 3);
    for (const frame of recoveryFrames) frame.forEach((row, i) => row.forEach((value, j) => {
      assert(Math.abs(value - beforeReconnect[i][j]) <= 1, 'chat geometry changed during reconnection');
    }));
    await composer.fill('');
    await pause(500);
    assert.equal(await total('alerts'), 1, 'replay stayed silent');
    assert.equal(await total('playedSounds'), 2);
    const alert = await first.evaluate(() => window.alerts[0]);
    assert.equal(alert.title, 'New message · Oiê');
    assert.equal(alert.options.body, 'Bob');
    assert.equal(alert.options.silent, true);
    await second.evaluate(() => { Notification.permission = 'granted'; });

    // Real controls, two unlocked tabs, and no delayed playback after a burst.
    await second.bringToFront();
    await settings(second);
    const secondBeforePreview = await second.evaluate(() => window.playedSounds.length);
    await second.locator('[data-notification-preview]').click();
    await until(() => second.evaluate(count => window.playedSounds.length === count + 1, secondBeforePreview), 'second tab unlocked');
    const quietWindow = () => pause(2100);
    const expectSound = async (count, label) => {
      await until(async () => (await total('playedSounds')) === count, label);
    };
    const expectSilent = async (count, label) => {
      await pause(350);
      assert.equal(await total('playedSounds'), count, label);
    };
    await quietWindow();
    push({conversationId: room});
    push({conversationId: room, reply: replyTo(someoneElse)});
    push({conversationId: room, reply: replyTo(foreign)});
    push({conversationId: archivedRoom, mentionsYou: true});
    await expectSilent(3, 'ordinary room messages, unowned or foreign replies, archived mentions');
    push({conversationId: room, mentionsYou: true});
    await expectSound(4, 'a room mention sounds by default');
    push();
    push({conversationId: room, reply: replyTo(own)});
    await quietWindow();
    assert.equal(await total('playedSounds'), 4, 'the burst neither overlaps nor plays when its window ends');
    push({conversationId: room, reply: replyTo(own)});
    await expectSound(5, 'a verified reply to your own room message sounds');
    await quietWindow();

    const pick = async (label, value) => {
      await second.getByRole('combobox', {name: 'Som nas salas'}).click();
      await second.getByRole('option', {name: label, exact: true}).click();
      await until(() => second.evaluate(value => localStorage.getItem('oie.notifications.room-sound') === value, value),
        'room sound preference saved: ' + value);
    };
    await pick('Todas as mensagens', 'all');
    push({conversationId: room});
    await expectSound(6, 'all-room option plays ordinary messages');
    await quietWindow();
    push({conversationId: archivedRoom, mentionsYou: true});
    await expectSilent(6, 'all-room option still honors archive preference');
    await second.locator('label:has(#notification-archived)').click();
    push({conversationId: archivedRoom});
    await expectSound(7, 'archived room sound can be enabled');
    await quietWindow();

    await pick('Desativado', 'off');
    await second.locator('label:has(#notification-direct)').click();
    push({conversationId: room, mentionsYou: true});
    push();
    await expectSilent(7, 'room and direct sounds can each be turned off');
    await second.locator('label:has(#notification-direct)').click();
    await second.locator('label:has(#notification-focused)').click();
    await first.bringToFront();
    await first.evaluate(() => { window.testFocus = true; window.dispatchEvent(new Event('focus')); });
    push({conversationId: chat});
    await expectSound(8, 'the focused conversation can play sound when requested');
    await quietWindow();
    await second.locator('label:has(#notification-sound)').click();
    push();
    await expectSilent(8, 'master sound preference applies in both tabs');
    await second.locator('label:has(#notification-sound)').click();
    await pick('Menções e respostas a você', 'attention');
    await second.locator('label:has(#notification-archived)').click();
    await second.locator('label:has(#notification-focused)').click();
    await closeSettings(second);

    await first.evaluate(() => window.alerts[0].onclick());
    await first.waitForURL(url => url.searchParams.get('message') === message.id);
    assert.equal(new URL(first.url()).searchParams.get('chat'), other);
    await first.goto(base + '/?lang=en&chat=' + other + '&open=details');
    await first.evaluate(() => { Notification.permission = 'granted'; });
    const mute = first.locator('[data-notification-mute]');
    await first.locator('label:has(#notification-mute)').click();
    assert.equal(await mute.isChecked(), true);
    await until(() => streams.size === 2, 'live after navigation');
    const previousAlerts = await total('alerts');

    await second.setViewportSize({width: 390, height: 844});
    await settings(second);
    assert.equal(await second.locator('#notification-settings-title').textContent(), 'Notificações');
    if (process.env.OIE_NOTIFICATION_SCREENSHOT) {
      await second.locator('[data-notification-settings]').screenshot({path: process.env.OIE_NOTIFICATION_SCREENSHOT});
    }
    assert.equal(await second.locator('[data-notification-sound]').isChecked(), true);
    const soundsBeforeMute = await total('playedSounds');
    push();
    await pause(500);
    assert.equal(await total('alerts'), previousAlerts, 'mute applied in both tabs');
    assert.equal(await total('playedSounds'), soundsBeforeMute, 'a muted direct conversation stays silent');
    await first.locator('label:has(#notification-mute)').click();
    assert.equal(await mute.isChecked(), false);
    push();
    await until(async () => (await total('alerts')) === previousAlerts + 1, 'unmuted conversation');

    const mobile = await browser.newContext({viewport: {width: 390, height: 844}});
    const mobilePage = await mobile.newPage();
    mobilePage.on('pageerror', error => errors.push(error.message));
    await mobilePage.addInitScript(() => {
      localStorage.setItem('oie.notifications.banners', '1');
      Object.defineProperty(document, 'hasFocus', {value: () => false});
      // Chromium's headless notification service has no OS permission grant.
      // Keep real registration and activation, and record the call to the OS.
      ServiceWorkerRegistration.prototype.showNotification = async function (title, options) {
        window.workerBanner = {title, options, state: this.active?.state};
      };
      window.Notification = class {
        static permission = 'granted';
        constructor() { throw new Error('Expected a persistent notification'); }
      };
    });
    await mobilePage.goto(base + '/?lang=pt-BR');
    await until(() => streams.size === 3, 'independent mobile stream');
    const next = push().message;
    await until(() => mobilePage.evaluate(() => Boolean(window.workerBanner)), 'worker notification');
    const workerBanner = await mobilePage.evaluate(() => window.workerBanner);
    assert.equal(workerBanner.state, 'activated');
    assert.equal(workerBanner.title, 'Nova mensagem · Oiê');
    assert.equal(workerBanner.options.silent, true);
    assert.deepEqual(workerBanner.options.data, {engine, account, conversation: other, message: next.id});
    await until(async () => (await total('alerts')) === previousAlerts + 2, 'independent browser profiles');
    const [worker] = mobile.serviceWorkers();
    const target = '#message-timeline [data-message-id="' + next.id + '"]';
    await mobilePage.evaluate(() => { window.notificationClickPreservedPage = true; });
    assert.deepEqual(await clickWorkerBanner(worker, workerBanner.options.data), {closed: true, focused: true, destination: undefined});
    await mobilePage.waitForURL(url => url.searchParams.get('message') === next.id);
    await mobilePage.locator(target).waitFor();
    assert.equal(await mobilePage.evaluate(() => window.notificationClickPreservedPage), true);
    await mobilePage.close();
    assert.deepEqual(await clickWorkerBanner(worker, {...workerBanner.options.data, message: '/outside'}),
      {closed: true, focused: false, destination: undefined});
    const coldClick = await clickWorkerBanner(worker, workerBanner.options.data);
    assert.equal(coldClick.closed, true);
    assert.equal(coldClick.focused, false);
    const reopened = await mobile.newPage();
    const destination = new URL(coldClick.destination, base);
    assert.equal(destination.origin, base);
    assert.equal(destination.searchParams.get('notificationEngine'), engine);
    await reopened.goto(destination.href);
    await reopened.locator(target).waitFor();
    assert.equal(await reopened.locator('#oie-app').getAttribute('data-focus-message'), next.id);
    destination.searchParams.set('notificationEngine', id(999));
    assert.equal((await reopened.goto(destination.href)).status(), 404);
    assert.equal(await reopened.locator(target).count(), 0);
    await mobile.close();

    await first.goto(base + '/?lang=en&chat=' + room + '&open=details');
    await until(() => streams.size === 2, 'room settings stream');
    await first.locator('label:has(#notification-mute)').click();
    assert.equal(await first.locator('[data-notification-mute]').isChecked(), true);
    await quietWindow();
    const soundsBeforeRoomMute = await total('playedSounds');
    const alertsBeforeRoomMute = await total('alerts');
    push({conversationId: room, mentionsYou: true});
    await expectSilent(soundsBeforeRoomMute, 'a muted room stays silent even for mentions');
    assert.equal(await total('alerts'), alertsBeforeRoomMute);
    await first.locator('label:has(#notification-mute)').click();
    push({conversationId: room, mentionsYou: true});
    await expectSound(soundsBeforeRoomMute + 1, 'unmuted room plays eligible messages again');

    const toneProfile = await browser.newContext({viewport: {width: 1440, height: 900}});
    const tonePage = await toneProfile.newPage();
    tonePage.on('pageerror', error => errors.push(error.message));
    await tonePage.addInitScript(() => {
      // Render the Web Audio graph without relying on the host's audio device.
      window.AudioContext = class extends AudioContext {
        constructor() {
          super({sinkId: {type: 'none'}});
        }
      };
      window.tonePreviews = [];
      const start = AudioBufferSourceNode.prototype.start;
      AudioBufferSourceNode.prototype.start = function (...args) {
        if (this.context instanceof AudioContext) window.tonePreviews.push(this.buffer);
        return start.apply(this, args);
      };
    });
    const toneURL = base + '/?lang=pt-BR&chat=' + chat + '&open=settings&settingsTab=device';
    const tone = tonePage.locator('[data-notification-tone-name]');
    const toneFile = tonePage.locator('[data-notification-tone-file]');
    const chooseTone = tonePage.getByRole('button', {name: 'Escolher arquivo', exact: true});
    const tonePreview = tonePage.getByRole('button', {name: 'Ouvir som', exact: true});
    const resetTone = tonePage.getByRole('button', {name: 'Voltar ao Oiê', exact: true});
    const toneStatus = tonePage.locator('#notification-status');
    await tonePage.route(base + '/old-profile', route => route.fulfill({contentType: 'text/html', body: '<title>Old profile</title>'}));
    await tonePage.goto(base + '/old-profile');
    await tonePage.evaluate(() => new Promise(resolve => {
      const request = indexedDB.open('oie-notifications', 1);
      request.onupgradeneeded = () => request.result.createObjectStore('delivered').createIndex('deliveredAt', 'deliveredAt');
      request.onsuccess = () => { request.result.close(); resolve(); };
      localStorage.setItem('oie.notifications.tone', 'wood');
    }));
    await tonePage.goto(toneURL);
    await until(() => streams.size === 3, 'tone selection live stream');
    assert.equal(await tone.textContent(), 'Oiê');
    assert.equal(await resetTone.isVisible(), false);
    assert.equal(await chooseTone.locator('svg[aria-hidden="true"]').count(), 1);
    const file = fixtureTone(3);
    const chooser = tonePage.waitForEvent('filechooser');
    await chooseTone.press('Enter');
    await (await chooser).setFiles(file);
    await until(async () => (await tone.textContent()) === file.name, 'custom tone saved');
    assert.equal((await resetTone.textContent()).trim(), '');
    const [filenameBox, resetBox] = await Promise.all([tone.boundingBox(), resetTone.boundingBox()]);
    assert(resetBox.x >= filenameBox.x + filenameBox.width, 'reset sits to the right of the filename');
    assert(Math.abs(resetBox.y + resetBox.height / 2 - filenameBox.y - filenameBox.height / 2) <= 1, 'reset is centered on the filename');
    await until(async () => (await toneStatus.textContent()).includes('os primeiros 2 segundos'), 'trimmed tone feedback');
    await tonePreview.press('Space');
    await until(() => tonePage.evaluate(() => window.tonePreviews.length === 1), 'custom tone previewed');
    const customDuration = await tonePage.evaluate(() => window.tonePreviews[0].duration);
    assert(Math.abs(customDuration - 2) < 0.001, 'preview uses two seconds after device resampling');
    push();
    await until(() => tonePage.evaluate(() => window.tonePreviews.length === 2), 'custom tone played for a new message');
    assert(await tonePage.evaluate(() => window.tonePreviews[0] === window.tonePreviews[1]));
    await toneFile.setInputFiles({name: 'Inválido.wav', mimeType: 'audio/wav', buffer: Buffer.from('invalid audio')});
    await until(async () => (await toneStatus.textContent()).includes('que possa ser reproduzido'), 'unplayable file rejected');
    assert.equal(await tone.textContent(), file.name);
    await toneFile.setInputFiles({name: 'Grande.wav', mimeType: 'audio/wav', buffer: Buffer.alloc(5000001)});
    await until(async () => (await toneStatus.textContent()).includes('até 5 MB'), 'oversized file rejected');
    assert.equal(await tone.textContent(), file.name);
    await tonePage.reload();
    await until(async () => (await tone.textContent()) === file.name, 'custom tone restored after reload');
    const mirror = await toneProfile.newPage();
    await mirror.goto(toneURL);
    const mirrorTone = mirror.locator('[data-notification-tone-name]');
    await until(async () => (await mirrorTone.textContent()) === file.name, 'custom tone shared in this profile');
    await resetTone.press('Enter');
    await until(async () => (await mirrorTone.textContent()) === 'Oiê', 'custom tone removed across views');
    assert.equal(await tone.textContent(), 'Oiê');
    await until(() => chooseTone.evaluate(button => document.activeElement === button), 'focus restored to file chooser');
    await tonePage.reload();
    assert.equal(await tone.textContent(), 'Oiê');
    await toneProfile.close();

    const nativeProfile = await browser.newContext();
    const nativePage = await nativeProfile.newPage();
    nativePage.on('pageerror', error => errors.push(error.message));
    await nativePage.addInitScript(() => {
      window.AudioContext = window.webkitAudioContext = undefined;
      window.nativeWindowHidden = true;
      window.nativeSounds = [];
      window.nativeBanners = 0;
      Object.defineProperty(document, 'hidden', {get: () => window.nativeWindowHidden});
      window.OieDesktop = {notifications: {
        playSound: async tone => { window.nativeSounds.push(tone); },
        send: async () => { window.nativeBanners++; }
      }};
    });
    await nativePage.goto(base + '/?lang=pt-BR');
    await until(() => streams.size === 3, 'native window live stream');
    push();
    await until(() => nativePage.evaluate(() => window.nativeSounds[0] === null), 'hidden native window plays the default tone before any interaction');
    push();
    await quietWindow();
    assert.deepEqual(await nativePage.evaluate(() => [window.nativeSounds, window.nativeBanners]), [[null], 0],
      'native sound respects the rolling interval and works with banners off');
    await nativePage.evaluate(() => { window.nativeWindowHidden = false; });
    await settings(nativePage);
    await nativePage.locator('[data-notification-tone-file]').setInputFiles(file);
    await until(async () => (await nativePage.locator('[data-notification-tone-name]').textContent()) === file.name, 'native tone saved');
    await nativePage.locator('label:has(#notification-sound)').click();
    push();
    await pause(350);
    assert.equal(await nativePage.evaluate(() => window.nativeSounds.length), 1, 'native playback respects the sound preference');
    await nativePage.locator('[data-notification-preview]').click();
    await until(() => nativePage.evaluate(() => window.nativeSounds.length === 2), 'the custom tone previews natively even with alerts muted');
    const encodedTone = await nativePage.evaluate(() => window.nativeSounds[1]);
    const nativeWav = Buffer.from(encodedTone, 'base64');
    assert.equal(nativeWav.toString('ascii', 0, 4), 'RIFF');
    assert.equal(nativeWav.readUInt32LE(24), 24000);
    assert.equal(nativeWav.length, 96044);
    await nativePage.locator('label:has(#notification-sound)').click();
    await nativePage.evaluate(() => { window.nativeWindowHidden = true; });
    push();
    await until(() => nativePage.evaluate(wav => window.nativeSounds[2] === wav, encodedTone), 'the hidden native window plays the selected tone');
    await nativeProfile.close();

    assert.deepEqual(errors, []);
    console.log('PASS: notification preferences, native sound without Web Audio, two-second window, mute, replay and banner clicks');
  } finally {
    await browser?.close();
    app.kill();
    api.closeAllConnections();
    api.close();
  }
}

run().catch(error => { console.error(error); process.exitCode = 1; });
