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

async function verifySignInAppearance(browser, origin) {
  const page = await browser.newPage({ignoreHTTPSErrors: true, viewport: {width: 1440, height: 900}});
  try {
    await page.emulateMedia({colorScheme: 'light'});
    const action = page.getByRole('link', {name: 'Entrar', exact: true});
    const check = async (dark, width, height) => {
      const background = dark ? 'rgb(28, 29, 28)' : 'rgb(247, 248, 247)';
      const logo = dark ? '/logo-dark.svg' : '/logo-light.svg';
      await page.waitForFunction(({background, logo}) =>
        getComputedStyle(document.body).backgroundColor === background &&
        document.querySelector('h1 img')?.currentSrc.endsWith(logo) &&
        document.querySelector('h1 img').naturalWidth > 0, {background, logo});
      const frame = await page.getByRole('main').boundingBox();
      assert(frame.x >= 20 && frame.y >= 20 && frame.x + frame.width <= width - 20);
      assert(Math.abs(frame.x + frame.width / 2 - width / 2) <= 1);
      assert(Math.abs(frame.y + frame.height / 2 - height / 2) <= 1);
      assert.equal(await action.evaluate(element => getComputedStyle(element).backgroundColor), 'rgb(96, 146, 171)');
      assert((await action.boundingBox()).height >= 48);
    };

    await page.goto(origin + '/?open=settings&lang=pt-BR&theme=dark');
    await action.waitFor();
    await check(true, 1440, 900);
    await page.goto(origin + '/auth/sign-in');
    await check(true, 1440, 900);
    await page.goto(origin + '/auth/sign-in?theme=system');
    await check(false, 1440, 900);
    const originalFrame = await page.getByRole('main').boundingBox();
    await page.keyboard.press('Tab');
    assert(await action.evaluate(element => element.matches(':focus-visible')));
    await page.emulateMedia({colorScheme: 'dark'});
    await check(true, 1440, 900);
    assert.deepEqual(await page.getByRole('main').boundingBox(), originalFrame);
    assert(await action.evaluate(element => element.matches(':focus-visible')));

    await page.setViewportSize({width: 390, height: 700});
    await page.goto(origin + '/auth/sign-in?theme=light');
    await check(false, 390, 700);
    await page.goto(origin + '/auth/sign-in');
    await check(false, 390, 700);
    await page.emulateMedia({colorScheme: 'light'});
    await page.goto(origin + '/auth/sign-in?theme=dark');
    await check(true, 390, 700);
  } finally {
    await page.context().close();
  }
}

async function signOutConfirmation(page, copy) {
  const engine = page.getByRole('tabpanel', {name: copy.engine, exact: true});
  const open = engine.getByRole('button', {name: copy.action, exact: true});
  const dialog = page.getByRole('dialog', {name: copy.title, exact: true});
  const cancel = dialog.getByRole('button', {name: copy.cancel, exact: true});
  const confirm = dialog.getByRole('button', {name: copy.action, exact: true});
  let logoutRequests = 0;
  const countLogout = request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/auth/logout') logoutRequests++;
  };
  page.on('request', countLogout);
  try {
    await open.press('Enter');
    await dialog.waitFor();
    assert.equal(await dialog.locator('#hosted-sign-out-description').textContent(), copy.description);
    assert.equal(await confirm.evaluate(button => document.activeElement === button), false);
    await cancel.click();
    await dialog.waitFor({state: 'hidden'});
    assert(await open.evaluate(button => document.activeElement === button));
    await open.press('Enter');
    await dialog.waitFor();
    await page.keyboard.press('Escape');
    await dialog.waitFor({state: 'hidden'});
    assert.equal(logoutRequests, 0);
    assert.equal((await page.request.get(new URL('/auth/session', page.url()).href)).status(), 204);
    await open.press('Enter');
    await dialog.waitFor();
    return confirm;
  } finally {
    page.off('request', countLogout);
  }
}

async function verifyMobileSignOut(browser, server, engineID) {
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, locale: 'pt-BR', viewport: {width: 390, height: 844},
  });
  try {
    const page = await context.newPage();
    await server.signIn(page, '/?open=settings&settingsTab=engine&lang=pt-BR');
    const confirm = await signOutConfirmation(page, {
      engine: 'Motor', action: 'Sair', title: 'Sair do Oiê?', cancel: 'Cancelar',
      description: 'Você vai sair do Oiê em todas as abas deste navegador. O motor continuará rodando no servidor.',
    });
    await Promise.all([page.waitForURL(signIn), confirm.click()]);
    assert.equal((await page.request.get(new URL('/auth/session', page.url()).href)).status(), 401);
    assert.equal((await (await server.api.get('/api/v1/engine')).json()).id, engineID);
  } finally {
    await context.close();
  }
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
    await verifySignInAppearance(browser, url);
    await verifyMobileSignOut(browser, server, engine.id);
    const context = await browser.newContext({ignoreHTTPSErrors: true, locale: 'en', viewport: {width: 1440, height: 900}});
    const page = await context.newPage();
    await server.signIn(page, search);

    await page.goto(url + '/?open=settings&lang=en');
    const settingsDrawer = page.getByRole('dialog', {name: 'Settings', exact: true});
    await settingsDrawer.getByRole('tab', {name: 'This device', exact: true}).click();
    const device = settingsDrawer.getByRole('tabpanel', {name: 'This device', exact: true});
    await device.getByRole('heading', {name: 'Notifications', exact: true}).waitFor();
    assert.equal(await device.locator('[data-download-retention]').count(), 0);
    assert.equal(await page.locator('#remove-device-downloads-dialog').count(), 0);
    await settingsDrawer.getByRole('tab', {name: 'General', exact: true}).click();
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
    await page.keyboard.press('Escape');
    await keyDrawer.waitFor({state: 'hidden'});
    assert.equal(await page.locator('#created-access-key').inputValue(), '');
    await page.locator('[data-navigation-settings]:visible').click();
    await access.click();
    await keyDrawer.waitFor();
    assert.equal(await generatedKey.isVisible(), false);
    await keyDrawer.getByRole('button', {name: 'Back to settings', exact: true}).click();

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
    await page.evaluate(() => window.OieEvents.suspend());
    const composer = page.locator('#message-composer');
    await composer.waitFor();
    const settings = await context.newPage();
    await settings.goto(url + '/?open=settings&settingsTab=engine&lang=en');
    const confirm = await signOutConfirmation(settings, {
      engine: 'Engine', action: 'Sign out', title: 'Sign out of Oiê?', cancel: 'Cancel',
      description: 'You’ll sign out of Oiê in all tabs in this browser. The engine will keep running on the server.',
    });
    const releaseLogout = Promise.withResolvers();
    let logoutRequests = 0;
    await settings.route('**/auth/logout', async route => {
      logoutRequests++;
      await releaseLogout.promise;
      await route.continue();
    });
    const logoutRequest = settings.waitForRequest(request => new URL(request.url()).pathname === '/auth/logout');
    await confirm.focus();
    const before = await confirm.boundingBox();
    await confirm.press('Enter');
    await logoutRequest;
    await confirm.press('Enter');
    assert(await confirm.evaluate(button => document.activeElement === button));
    const shifts = await confirm.evaluate((button, before) => new Promise(resolve => {
      const frames = [];
      const sample = () => {
        const now = button.getBoundingClientRect();
        frames.push(Math.max(...['x', 'y', 'width', 'height'].map(key => Math.abs(now[key] - before[key]))));
        if (frames.length === 10) resolve(frames);
        else requestAnimationFrame(sample);
      };
      requestAnimationFrame(sample);
    }), before);
    assert(shifts.every(shift => shift <= 1), JSON.stringify(shifts));
    assert.equal(logoutRequests, 1);
    const signedOut = Promise.all([page.waitForURL(signIn, {timeout: 6000}), settings.waitForURL(signIn)]);
    releaseLogout.resolve();
    await signedOut;
    releaseCheck.resolve();
    assert.equal((await (await api.get('/api/v1/engine')).json()).id, engine.id);
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
      window.OieEvents.suspend();
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
