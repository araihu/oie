const assert = require('node:assert/strict');
const http = require('node:http');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { chromium } = require('playwright');

const id = number => '019a5df0-0000-7000-8000-' + String(number).padStart(12, '0');
const engine = id(1);
const account = { id: id(2), revisionId: id(3), label: 'Personal', address: 'alice@fixture', protocol: 'xmpp' };
const conversation = (number, title, kind) => ({
  id: id(number), title, kind, revisionId: id(3), accountId: account.id,
  address: title.toLowerCase() + '@fixture', unreadCount: 0, archived: false, favorite: false,
});
const direct = conversation(4, 'Direct', 'direct');
const rooms = [conversation(5, 'Alpha', 'room'), conversation(6, 'Bravo', 'room'), conversation(7, 'Charlie', 'room')];
const [alpha, bravo, charlie] = rooms;
const message = (number, text) => ({
  id: id(number), revisionId: id(10), accountId: account.id, conversationId: direct.id,
  originalText: text, sender: { id: id(8), displayName: 'Bob' }, createdAt: '2026-11-08T12:00:00Z',
  isOwn: false, sendState: 'received',
});
const source = message(11, 'Original message');
const messages = [source, message(12, 'Message with a rejected destination')];

(async () => {
  const accepted = new Map(), requests = [], streams = new Set();
  const api = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://fixture');
    const send = (status, value) => {
      res.statusCode = status;
      res.setHeader('Content-Type', status >= 400 ? 'application/problem+json' : 'application/json');
      res.end(JSON.stringify(value));
    };
    const original = messages.find(item => url.pathname === '/api/v1/messages/' + item.id ||
      url.pathname === '/api/v1/messages/' + item.id + '/forward');
    if (original && req.method === 'POST') {
      let body = '';
      for await (const chunk of req) body += chunk;
      const data = JSON.parse(body);
      const key = req.headers['idempotency-key'];
      requests.push({ message: original.id, key, ...data });
      if (accepted.has(key)) {
        const previous = accepted.get(key);
        assert.deepEqual(data, previous.data);
        return send(200, previous.copy);
      }
      if (original.revisionId !== data.expectedRevisionId) {
        return send(409, { status: 409, code: 'state_conflict', currentMessage: original });
      }
      if (data.conversationId === bravo.id) {
        return send(404, { status: 404, code: 'not_found' });
      }
      const copy = { ...original, id: id(50 + accepted.size), conversationId: data.conversationId, sendState: 'queued' };
      accepted.set(key, { data, copy });
      return send(202, copy);
    }
    if (original) return send(200, original);
    if (url.pathname === '/api/v1/engine') return send(200, { id: engine, role: 'hosted', apiVersion: '0.15.0', eventCursor: engine + ':0' });
    if (url.pathname === '/api/v1/accounts') return send(200, { items: [account] });
    if (url.pathname.endsWith('/connection')) return send(200, { accountId: account.id, state: 'connected' });
    if (url.pathname === '/api/v1/conversations') {
      const items = url.searchParams.get('archived') === 'true' ? [] : rooms;
      return send(200, { items });
    }
    if (url.pathname.endsWith('/avatars')) return send(200, { items: [] });
    if (url.pathname === '/api/v1/starred-messages') {
      return send(200, { items: messages.map(message => ({
        conversation: direct, message,
        star: { messageId: message.id, accountId: account.id, conversationId: direct.id, revisionId: id(9), starred: true },
      })) });
    }
    if (url.pathname === '/api/v1/events') {
      res.setHeader('Content-Type', 'text/event-stream');
      res.write(': ready\n\n');
      streams.add(res);
      req.on('close', () => streams.delete(res));
      return;
    }
    return send(404, { status: 404, code: 'not_found' });
  });
  api.listen(0, '127.0.0.1');
  await once(api, 'listening');
  const child = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'), [
    '--role', 'bff', '--listen', '127.0.0.1:0', '--engine-url', 'http://127.0.0.1:' + api.address().port,
  ]);
  const address = new Promise((resolve, reject) => {
    let log = '';
    child.stderr.on('data', chunk => {
      log += chunk;
      const match = log.match(/address=(http:\/\/127\.0\.0\.1:\d+)/);
      if (match) resolve(match[1]);
    });
    child.once('error', reject);
    child.once('exit', code => reject(new Error(`BFF exited (${code}): ${log}`)));
  });
  const browser = await chromium.launch();
  try {
    const base = await address;
    for (const fixture of [
      {
        lang: 'en', width: 1440, forward: 'Forward', retry: 'Try again', success: 'Messages queued for forwarding.',
        sourceChanged: 'The message or room has changed. Reopen forwarding to try again.',
      },
      {
        lang: 'pt-BR', width: 390, forward: 'Encaminhar', retry: 'Tentar novamente', success: 'Mensagens na fila para encaminhamento.',
        sourceChanged: 'A mensagem ou a sala mudou. Abra o encaminhamento novamente para tentar.',
      },
    ]) {
      const context = await browser.newContext({
        locale: fixture.lang, viewport: { width: fixture.width, height: 820 }, reducedMotion: 'reduce',
      });
      try {
        source.revisionId = id(10);
        source.originalText = 'Original message';
        const page = await context.newPage();
        const legacy = 'forwarding-journals:' + engine;
        const current = 'oie.' + legacy;
        const legacyFixtureKey = 'test.forwarding-legacy';
        await page.addInitScript(({ legacy, current, legacyFixtureKey }) => {
          const rows = sessionStorage.getItem(legacyFixtureKey);
          if (rows === null) return;
          sessionStorage.removeItem(legacyFixtureKey);
          sessionStorage.removeItem(current);
          sessionStorage.setItem(legacy, rows);
        }, { legacy, current, legacyFixtureKey });
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        await page.goto(base + '/?section=favorites&lang=' + fixture.lang);
        await page.waitForFunction(() => window.OieRuntime?.generation > 0);
        const seedLegacy = async rows => {
          await page.evaluate(({ key, rows }) => sessionStorage.setItem(key, JSON.stringify(rows)), { key: legacyFixtureKey, rows });
          await page.reload();
          await page.waitForFunction(() => window.OieRuntime?.generation > 0);
        };
        const dialog = page.locator('#forward-message-dialog');
        const button = dialog.locator('[data-forward-submit]');
        const checkbox = room => dialog.locator(`input[value="${room.id}"]`);
        const choose = room => dialog.locator(`[data-forward-room-label][for="forward-room-${room.id}"]`).click();
        const status = room => dialog.locator(`label[for="forward-room-${room.id}"] + [data-forward-result]`);
        const open = async original => {
          const row = page.locator(`[data-info-star-message="${original.id}"]`);
          const trigger = row.locator('[data-favorite-menu-trigger]');
          const action = row.getByRole('menuitem', { name: fixture.forward, exact: true });
          await trigger.focus();
          if (await trigger.getAttribute('aria-expanded') !== 'true') await trigger.press('Enter');
          await action.press('Enter');
          await dialog.locator('[data-forward-form]').waitFor();
          await dialog.locator('[data-forward-search]').evaluate(input => {
            if (document.activeElement === input) return;
            return new Promise(resolve => input.addEventListener('focus', resolve, { once: true }));
          });
        };
        const close = async () => {
          await dialog.evaluate(element => {
            element.addEventListener('close', () => { element.dataset.testClosed = 'true'; }, { once: true });
            delete element.dataset.testClosed;
          });
          await page.keyboard.press('Escape');
          await page.waitForFunction(() => document.getElementById('forward-message-dialog').dataset.testClosed === 'true');
          await dialog.waitFor({ state: 'hidden' });
        };
        const waitForRetry = () => dialog.getByRole('button', { name: fixture.retry, exact: true }).waitFor();
        const loseResponse = async route => {
          if (route.request().method() !== 'POST') return route.continue();
          await route.fetch();
          await route.abort();
        };
        await open(source);
        await choose(alpha);
        const drop = '**/ui/messages/forward';
        await page.route(drop, loseResponse);
        const initial = requests.length;
        await button.click();
        await waitForRetry();
        await page.unroute(drop, loseResponse);
        assert.equal(requests.length, initial + 1);
        const firstAttempt = requests.at(-1);
        await close();
        source.revisionId = id(13);
        source.originalText = 'Edited message';

        await page.reload();
        await page.waitForFunction(() => window.OieRuntime?.generation > 0);
        await open(source);
        await dialog.getByRole('paragraph').filter({ hasText: 'Edited message' }).waitFor();
        assert(await checkbox(alpha).isChecked());
        await button.click();
        await dialog.waitFor({ state: 'hidden' });
        assert.deepEqual(requests.at(-1), firstAttempt);
        await page.getByText(fixture.success, { exact: true }).last().waitFor();
        assert.equal([...accepted.values()].filter(({ data }) => data.expectedRevisionId === id(13)).length, 0);

        await open(messages[1]);
        await choose(alpha);
        await choose(bravo);
        await button.click();
        await waitForRetry();
        assert(await checkbox(alpha).isDisabled());
        assert(await checkbox(bravo).isChecked());
        await status(bravo).waitFor();
        await choose(bravo);
        await choose(charlie);
        await button.click();
        await dialog.waitFor({ state: 'hidden' });
        await page.getByText(fixture.success, { exact: true }).last().waitFor();
        await open(messages[1]);
        assert.equal(await checkbox(bravo).isChecked(), false);
        await close();

        await open(source);
        await choose(charlie);
        source.revisionId = id(14);
        source.originalText = 'Updated while the dialog was open';
        await button.click();
        await status(charlie).getByText(fixture.sourceChanged, { exact: true }).waitFor();
        assert(await checkbox(charlie).isDisabled());
        assert(await button.isDisabled());
        const staleAttempt = requests.at(-1);
        assert.equal(staleAttempt.expectedRevisionId, id(13));
        await close();
        await open(source);
        assert.equal(await checkbox(charlie).isChecked(), false);
        await choose(charlie);
        await button.click();
        await dialog.waitFor({ state: 'hidden' });
        await page.getByText(fixture.success, { exact: true }).last().waitFor();
        assert.notEqual(requests.at(-1).key, staleAttempt.key);
        assert.equal(requests.at(-1).expectedRevisionId, id(14));

        await open(source);
        await choose(alpha);
        let attempts = 0;
        const failBeforeAcceptance = route => {
          if (route.request().method() !== 'POST') return route.continue();
          attempts++;
          return route.abort();
        };
        await page.route(drop, failBeforeAcceptance);
        await button.click();
        await waitForRetry();
        assert.equal(attempts, 1);
        await page.unroute(drop, failBeforeAcceptance);
        await close();
        source.revisionId = id(15);
        source.originalText = 'Edited after the interrupted request';
        await open(source);
        assert(await checkbox(alpha).isChecked());
        await button.click();
        await status(alpha).getByText(fixture.sourceChanged, { exact: true }).waitFor();
        assert.equal(await checkbox(alpha).isChecked(), false);
        assert(await checkbox(alpha).isEnabled());
        const reconciledAttempt = requests.at(-1);
        assert.equal(reconciledAttempt.expectedRevisionId, id(14));
        await choose(alpha);
        await button.click();
        await dialog.waitFor({ state: 'hidden' });
        assert.notEqual(requests.at(-1).key, reconciledAttempt.key);
        assert.equal(requests.at(-1).expectedRevisionId, id(15));

        const olderKey = id(fixture.width + 1), newerKey = id(fixture.width + 2);
        const previousRevision = id(14);
        const data = { expectedRevisionId: source.revisionId, conversationId: alpha.id };
        accepted.set(newerKey, { data, copy: { ...source, id: id(50 + accepted.size), conversationId: alpha.id, sendState: 'queued' } });
        await seedLegacy([
          [`${source.id}:${previousRevision}:${alpha.id}`, { key: olderKey, accepted: false, message: 'Unknown' }],
          [`${source.id}:${source.revisionId}:${alpha.id}`, { key: newerKey, accepted: false, message: 'Unknown' }],
        ]);
        await open(source);
        assert(await checkbox(alpha).isChecked());
        const beforeLegacyRetry = requests.length;
        await page.route(drop, async route => {
          if (route.request().method() === 'POST' && route.request().postData()?.includes(newerKey)) {
            await route.fetch();
            await route.abort();
          } else await route.continue();
        });
        await button.click();
        await waitForRetry();
        await page.unroute(drop);
        assert.deepEqual(requests.slice(beforeLegacyRetry).map(({ key }) => key), [olderKey, newerKey]);
        await page.reload();
        await page.waitForFunction(() => window.OieRuntime?.generation > 0);
        await open(source);
        assert(await checkbox(alpha).isChecked());
        await button.click();
        await dialog.waitFor({ state: 'hidden' });
        assert.deepEqual(requests.slice(beforeLegacyRetry).map(({ key }) => key), [olderKey, newerKey, newerKey]);

        const settledLegacy = [
          [`${source.id}:${previousRevision}:${alpha.id}`, { key: olderKey, accepted: false, message: 'Unknown' }],
          [`${source.id}:${source.revisionId}:${alpha.id}`, { key: newerKey, accepted: true, message: 'Queued' }],
        ];
        await seedLegacy(settledLegacy);
        await open(source);
        await status(alpha).getByText('Queued', { exact: true }).waitFor();
        assert(await checkbox(alpha).isDisabled());
        const stored = await page.evaluate(key => JSON.parse(sessionStorage.getItem(key)), current);
        assert.equal(stored[0][1].key, newerKey);
        assert.equal(stored[0][1].remaining[0].key, olderKey);
        await choose(charlie);
        const beforeOtherDestination = requests.length;
        await button.click();
        await dialog.waitFor({ state: 'hidden' });
        assert.deepEqual(requests.slice(beforeOtherDestination).map(({ conversationId }) => conversationId), [charlie.id]);
        await page.reload();
        await page.waitForFunction(() => window.OieRuntime?.generation > 0);
        await open(source);
        assert(await checkbox(alpha).isEnabled());
        assert.equal(await checkbox(alpha).isChecked(), false);
        await close();
        await seedLegacy(settledLegacy);
        await open(source);
        await status(alpha).getByText('Queued', { exact: true }).waitFor();
        assert(await checkbox(alpha).isDisabled());
        await close();
        await open(source);
        assert(await checkbox(alpha).isEnabled());
        assert.deepEqual(errors, []);
      } finally {
        await context.close();
      }
    }
    console.log('PASS: interrupted forwarding and legacy receipts retry original keys, skip accepted rooms and keep deselected rooms unchecked in English and Portuguese');
  } finally {
    await browser.close();
    if (child.exitCode === null) {
      const exited = once(child, 'exit');
      child.kill();
      await exited;
    }
    for (const stream of streams) stream.end();
    api.closeAllConnections();
    await new Promise(resolve => api.close(resolve));
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
