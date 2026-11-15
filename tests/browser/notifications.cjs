const assert = require('node:assert/strict');
const http = require('node:http');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require('playwright');

const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const [engine, account, chat, other, revision, room, archivedRoom, own, someoneElse, foreign] = [1, 2, 3, 4, 5, 6, 7, 8, 10, 11].map(id);
const replyTo = messageId => ({available: true, messageId, bodyText: "Thanks", excerpt: "Hello"});
const now = '2026-11-14T18:00:00Z';
const pause = ms => new Promise(resolve => setTimeout(resolve, ms));
async function until(test, label) {
  for (let i = 0; i < 100; i++) {
    if (await test()) return;
    await pause(50);
  }
  throw Error('Timed out: ' + label);
}

async function run() {
  const streams = new Set();
  const events = [];
  let seq = 0;
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
    if (!repeat) events.push(entry);
    for (const stream of streams) stream.write(entry);
    return {message, entry};
  }

  const api = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://fixture');
    const send = (value, status = 200) => {
      res.writeHead(status, {'Content-Type': 'application/json'}).end(JSON.stringify(value));
    };
    const route = url.pathname;
    if (route === '/api/v1/engine') return send({id: engine, role: 'hosted', apiVersion: '0.31.0', eventCursor: engine + ':' + seq});
    if (route === '/api/v1/accounts') return send({items: [profile]});
    if (route === '/api/v1/accounts/' + account) return send(profile);
    if (route.endsWith('/connection')) return send({accountId: account, state: 'connected', updatedAt: now});
    if (route === '/api/v1/conversations') return send({items: chats.filter(c => c.archived === (url.searchParams.get('archived') === 'true'))});
    const originalID = route.split('/').at(-1);
    if (route.startsWith('/api/v1/messages/') && retained.has(originalID)) {
      return send({id: originalID, accountId: account, revisionId: revision, originalText: 'Hello', ...retained.get(originalID)});
    }
    const selected = chats.find(c => route === '/api/v1/conversations/' + c.id);
    if (selected) return send(selected);
    if (/\/(messages|avatars|previews|message-stars)$/.test(route)) return send({items: []});
    if (route === '/api/v1/events') {
      res.writeHead(200, {'Content-Type': 'text/event-stream'});
      res.write(': ready\n\n');
      const after = Number(req.headers['last-event-id']?.split(':').at(-1) || 0);
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
  const app = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--role', 'bff', '--listen', '127.0.0.1:' + port, '--engine-url', 'http://127.0.0.1:' + api.address().port],
    {stdio: ['ignore', 'ignore', 'inherit']});
  let browser;
  try {
    browser = await chromium.launch({headless: true});
    await until(async () => { try { return (await fetch(base)).ok; } catch { return false; } }, 'BFF');
    const profile = await browser.newContext({viewport: {width: 1440, height: 900}});
    await profile.addInitScript(() => {
      window.alerts = [];
      window.playedSounds = [];
      window.prompts = 0;
      window.answerPermission = () => {};
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

    // Reconnect from the last cursor and recover a new, previously unseen ID.
    for (const stream of streams) stream.end();
    await until(() => streams.size === 0, 'disconnected streams');
    push();
    await until(() => streams.size === 2, 'reconnect');
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

    const pick = async label => {
      await second.getByRole('combobox', {name: 'Som nas salas'}).click();
      await second.getByRole('option', {name: label, exact: true}).click();
    };
    await pick('Todas as mensagens');
    push({conversationId: room});
    await expectSound(6, 'all-room option plays ordinary messages');
    await quietWindow();
    push({conversationId: archivedRoom, mentionsYou: true});
    await expectSilent(6, 'all-room option still honors archive preference');
    await second.locator('label:has(#notification-archived)').click();
    push({conversationId: archivedRoom});
    await expectSound(7, 'archived room sound can be enabled');
    await quietWindow();

    await pick('Desativado');
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
    await pick('Menções e respostas a você');
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
        constructor() { throw new TypeError('Use a service worker'); }
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

    assert.deepEqual(errors, []);
    console.log('PASS: notification sound defaults, room replies, archives, per-device preferences, two-second window, mute, replay and worker fallback');
  } finally {
    await browser?.close();
    app.kill();
    api.closeAllConnections();
    api.close();
  }
}

run().catch(error => { console.error(error); process.exitCode = 1; });
