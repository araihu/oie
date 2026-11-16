const assert = require('node:assert/strict');
const {randomUUID} = require('node:crypto');
const path = require('node:path');
const {chromium} = require('playwright');
const {startHostedOie} = require('./fixtures/hosted-oie.cjs');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');
const signIn = /\/auth\/sign-in(?:\?|$)/;
const loginLink = page => page.getByRole('link', {name: 'Sign in', exact: true});

function withDeadline(promise, label) {
  const timeout = AbortSignal.timeout(15000);
  const expired = new Promise((_, reject) => {
    timeout.addEventListener('abort', () => reject(new Error('Timed out: ' + label)), {once: true});
  });
  return Promise.race([promise, expired]);
}

async function expireWhenSending(page, context) {
  await page.route('**/ui/messages/send', async route => {
    await context.clearCookies({name: '__Host-oie-session'});
    const headers = await route.request().allHeaders();
    delete headers.cookie;
    const response = await route.fetch({headers});
    await route.fulfill({response});
  }, {times: 1});
}

async function create(api, endpoint, sequence, data) {
  const response = await api.post(endpoint, {
    data, headers: {'Idempotency-Key': '019a5df0-0000-7000-8000-' + String(sequence).padStart(12, '0')},
  });
  assert(response.ok(), await response.text());
  return response.json();
}

(async () => {
  const server = await startHostedOie(binary);
  let browser;
  try {
    const {api, url} = server;
    const account = await create(api, '/api/v1/accounts', 1, {protocol: 'xmpp', address: 'alice@fixture.invalid', label: 'Fixture account'});
    const chat = await create(api, '/api/v1/conversations', 2, {accountId: account.id, address: 'bob@fixture.invalid'});
    const engine = await (await api.get('/api/v1/engine')).json();
    const message = await create(api, '/api/v1/conversations/' + chat.id + '/messages', 3, {originalText: 'Notification fixture'});
    const search = '/search?' + new URLSearchParams({q: 'a+b & ação ' + '😀'.repeat(180), lang: 'en'});
    const workspace = '/?' + new URLSearchParams({chat: chat.id, lang: 'en'});
    browser = await chromium.launch({args: ['--ignore-certificate-errors-spki-list=' + server.certificatePin]});
    const context = await browser.newContext({ignoreHTTPSErrors: true, locale: 'en', viewport: {width: 1440, height: 900}});
    const page = await context.newPage();
    await server.signIn(page, search);

    await page.goto(url + '/?open=settings&lang=en');
    const access = page.getByRole('button', {name: 'Access keys', exact: true});
    await access.click();
    const keyDrawer = page.getByRole('dialog', {name: 'Access keys', exact: true});
    const keyName = keyDrawer.getByRole('textbox', {name: 'Key name'});
    await keyName.fill('Laptop fixture');
    await keyDrawer.getByRole('button', {name: 'Create access key'}).click();
    const generatedKey = keyDrawer.getByRole('textbox', {name: 'New access key'});
    await generatedKey.waitFor();
    const desktopKey = await generatedKey.inputValue();
    assert(desktopKey.length > 32, 'the owner can create a key without using the host');
    const keyCheck = await context.request.get(url + '/api/v1/engine', {
      headers: {Authorization: 'Bearer ' + desktopKey},
    });
    assert.equal(keyCheck.status(), 200);
    assert.equal((await keyCheck.json()).id, engine.id);
    await keyDrawer.getByRole('button', {name: 'Back to settings', exact: true}).click();
    assert.equal(await page.locator('#created-access-key').inputValue(), '');

    const staleCheck = Promise.withResolvers();
    const releaseCheck = Promise.withResolvers();
    await page.route('**/auth/session', async route => {
      const response = await route.fetch();
      staleCheck.resolve(response.status());
      await releaseCheck.promise;
      await route.fulfill({response}).catch(() => {});
    }, {times: 1});
    await page.goto(url + workspace);
    assert.equal(await withDeadline(staleCheck.promise, 'pre-logout session check'), 204);
    await page.evaluate(() => window.OieEvents.stop());
    const composer = page.locator('#message-composer');
    await composer.waitFor();
    const settings = await context.newPage();
    await settings.goto(url + '/?open=settings&lang=en');
    await Promise.all([
      page.waitForURL(signIn, {timeout: 6000}),
      settings.waitForURL(signIn),
      settings.getByRole('button', {name: 'Sign out', exact: true}).click(),
    ]);
    releaseCheck.resolve();
    assert.equal(await composer.count(), 0);
    assert.equal(new URL(page.url()).searchParams.get('next'), workspace);
    await loginLink(page).click();
    await page.waitForURL(url + workspace);

    const draft = 'Keep my draft ' + randomUUID();
    const storageKey = await page.evaluate(() => {
      const {engineId, accountId, conversationId} = document.body.dataset;
      return ['oie.draft.v1', engineId, accountId, conversationId].join(':');
    });
    await page.evaluate(() => {
      const setItem = Storage.prototype.setItem;
      Storage.prototype.setItem = function (key, value) {
        if (this === localStorage && key.startsWith('oie.draft.v1:')) throw new Error('Local draft storage unavailable');
        return setItem.call(this, key, value);
      };
    });
    await composer.fill(draft);
    await expireWhenSending(page, context);
    await Promise.all([
      page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send' && response.status() === 401),
      composer.press('Enter'),
    ]);
    await page.waitForURL(signIn);
    const saved = await page.evaluate(key => JSON.parse(sessionStorage.getItem(key)), storageKey);
    assert.equal(saved.text, draft);
    assert.equal(saved.pending, false, 'authentication rejected the first send before it reached the engine');
    assert.equal(new URL(page.url()).searchParams.get('next'), workspace);
    await loginLink(page).click();
    await page.waitForURL(url + workspace);
    assert.equal(await composer.inputValue(), draft);

    await page.route('**/ui/messages/send', route => route.abort('connectionreset'), {times: 1});
    await composer.press('Enter');
    const retry = page.locator('[data-pending-send-retry]');
    await retry.waitFor({state: 'visible'});
    const uncertain = await page.evaluate(key => JSON.parse(localStorage.getItem(key)), storageKey);
    await expireWhenSending(page, context);
    await Promise.all([
      page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send' && response.status() === 401),
      retry.click(),
    ]);
    await page.waitForURL(signIn);
    const afterRetry = await page.evaluate(key => JSON.parse(localStorage.getItem(key)), storageKey);
    assert.equal(afterRetry.pending, true, 'a rejected retry leaves the earlier attempt unresolved');
    assert.equal(afterRetry.key, uncertain.key);
    await page.evaluate(key => localStorage.removeItem(key), storageKey);
    await loginLink(page).click();
    await page.waitForURL(url + workspace);

    const pendingSearch = Promise.withResolvers();
    const releaseSearch = Promise.withResolvers();
    await page.route('**/search?*', async route => {
      await context.clearCookies({name: '__Host-oie-session'});
      pendingSearch.resolve();
      await releaseSearch.promise;
      await route.abort().catch(() => {});
    }, {times: 1});
    await page.evaluate(target => { void window.OieNavigation.go(target); }, search);
    await withDeadline(pendingSearch.promise, 'pending search navigation');
    await page.evaluate(() => window.OieHostedSession.verify());
    await page.waitForURL(signIn);
    releaseSearch.resolve();
    assert.equal(new URL(page.url()).searchParams.get('next'), search);
    await loginLink(page).click();
    await page.waitForURL(url + search);
    await page.goto(url + workspace);
    await withDeadline(page.evaluate(async () => {
      const registration = await navigator.serviceWorker.register('/ui/notification-worker.js?hosted=1');
      const worker = registration.active ?? registration.installing ?? registration.waiting;
      if (!worker) throw new Error('Missing notification worker');
      if (worker.state !== 'activated') {
        await new Promise((resolve, reject) => {
          worker.addEventListener('statechange', () => {
            if (worker.state === 'activated') resolve();
            else if (worker.state === 'redundant') reject(new Error('Notification worker failed to activate'));
          });
        });
      }
      window.OieEvents.stop();
    }), 'notification worker activation');
    const worker = context.serviceWorkers().find(worker => new URL(worker.url()).searchParams.get('hosted') === '1');
    assert(worker, 'hosted notification worker is active');
    await context.clearCookies({name: '__Host-oie-session'});
    const destination = await withDeadline(worker.evaluate(async data => {
      // Headless Chromium gives a synthetic click no OS notification activation.
      const openWindow = clients.openWindow;
      let handled, destination;
      clients.openWindow = async url => { destination = url; };
      try {
        const event = new Event('notificationclick');
        event.notification = {data, close() {}};
        event.waitUntil = promise => { handled = promise; };
        self.dispatchEvent(event);
        await handled;
        return destination;
      } finally {
        clients.openWindow = openWindow;
      }
    }, {engine: engine.id, account: account.id, conversation: chat.id, message: message.id}), 'notification click');
    const notification = '/?' + new URLSearchParams({
      account: account.id, chat: chat.id, message: message.id, notificationEngine: engine.id,
    });
    assert.equal(destination, notification);
    const notificationPage = await context.newPage();
    await notificationPage.goto(url + destination);
    assert.equal(new URL(notificationPage.url()).searchParams.get('next'), notification);
    await loginLink(notificationPage).click();
    await notificationPage.waitForURL(url + notification);
    await notificationPage.locator('[data-message-id="' + message.id + '"]').waitFor();
    console.log('PASS: hosted OIDC, long deep links, concurrent session checks, saved drafts, desktop key creation, and notification reauthentication');
  } finally {
    await browser?.close();
    await server.stop();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
