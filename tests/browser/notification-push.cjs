const assert = require('node:assert/strict');
const {createECDH, randomBytes} = require('node:crypto');
const {mkdir, readFile} = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');
const {startHostedOie} = require('./fixtures/hosted-oie.cjs');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');
const pushPath = '/ui/notification-push';
const fixedID = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');

async function check() {
  const hosted = await startHostedOie(binary, 60000);
  let browser;
  try {
    browser = await chromium.launch({args: ['--ignore-certificate-errors-spki-list=' + hosted.certificatePin]});
    const context = await browser.newContext({ignoreHTTPSErrors: true, permissions: ['notifications'], viewport: {width: 390, height: 810}});
    await context.grantPermissions(['notifications'], {origin: hosted.url});
    const ecdh = createECDH('prime256v1');
    const subscription = {
      endpoint: 'https://web.push.apple.com/oie-browser-fixture',
      keys: {auth: randomBytes(16).toString('base64url'), p256dh: ecdh.generateKeys().toString('base64url')},
    };
    await context.addInitScript(subscription => {
      const key = 'oie-test-push-subscription';
      const permissionKey = 'oie-test-push-permission';
      // The test substitutes the browser's network-dependent subscription step.
      const permission = Object.getOwnPropertyDescriptor(Notification, 'permission').get;
      Object.defineProperty(Notification, 'permission', {
        configurable: true, get: () => localStorage.getItem(permissionKey) ? 'granted' : permission.call(Notification),
      });
      const current = () => {
        const version = localStorage.getItem(key);
        if (!version) return null;
        const value = version === '1' ? subscription : {...subscription, endpoint: subscription.endpoint + '-renewed'};
        return {
          endpoint: value.endpoint,
          toJSON: () => value,
          unsubscribe: async () => {
            if (localStorage.getItem(key) === version) localStorage.removeItem(key);
            return true;
          },
        };
      };
      PushManager.prototype.getSubscription = async () => current();
      PushManager.prototype.subscribe = async options => {
        if (!options.userVisibleOnly || options.applicationServerKey.length !== 65) {
          throw Error('Invalid push subscription options');
        }
        localStorage.setItem(key, '1');
        localStorage.setItem(permissionKey, '1');
        return current();
      };
    }, subscription);

    assert.equal((await context.request.get(hosted.url + pushPath)).status(), 401);
    const page = await context.newPage();
    const entry = '/?open=settings&lang=pt-BR';
    await hosted.signIn(page, entry);
    await page.locator('[data-settings-target=device]:visible').click();
    await page.locator('[data-settings-target=notifications]').click();
    const toggle = page.locator('[data-notification-background]');
    await page.waitForFunction(() => !document.querySelector('[data-notification-background]').disabled);
    assert.equal(await toggle.isChecked(), false);
    assert.equal((await page.request.delete(hosted.url + pushPath, {headers: {Origin: hosted.url}})).status(), 403);
    const getStatus = async () => (await context.request.get(hosted.url + pushPath)).json();
    const before = await getStatus();
    const csrf = await page.locator('[data-notification-settings]').getAttribute('data-csrf');
    const hostile = await page.request.put(hosted.url + pushPath, {
      headers: {Origin: hosted.url, 'X-CSRF-Token': csrf},
      data: {
        ...subscription, endpoint: 'https://169.254.169.254/private',
        engineId: before.engineId, lang: 'pt-BR', mutedConversations: [],
        sound: true, directSound: true, roomSound: 'attention', archivedSound: false,
      },
    });
    assert.equal(hostile.status(), 400);
    assert.equal(before.enabled, false);

    let continueSave, sawSave;
    const held = new Promise(resolve => { continueSave = resolve; });
    const observed = new Promise(resolve => { sawSave = resolve; });
    const delay = async route => {
      if (route.request().method() === 'PUT') {
        sawSave();
        await held;
      }
      await route.continue();
    };
    await page.route('**' + pushPath, delay);
    const geometry = async () => page.locator('#notification-settings-title').boundingBox();
    await toggle.focus();
    const initialGeometry = await geometry();
    await toggle.press('Space');
    await observed;
    assert.deepEqual(await geometry(), initialGeometry);
    assert.equal((await getStatus()).enabled, false);
    continueSave();
    await page.waitForFunction(() => {
      const toggle = document.querySelector('[data-notification-background]');
      return toggle.checked && toggle.getAttribute('aria-disabled') === 'false';
    });
    await page.unroute('**' + pushPath, delay);
    assert.deepEqual(await geometry(), initialGeometry);
    assert(await toggle.evaluate(control => control === document.activeElement));
    const enabled = await getStatus();
    assert.equal(enabled.enabled, true);
    assert.equal(enabled.lang, 'pt-BR');
    assert.equal(enabled.publicKey, before.publicKey);
    assert(await page.locator('[data-notification-banners]').isChecked());

    const renewal = page.waitForResponse(response => response.url().endsWith(pushPath) && response.request().method() === 'PUT');
    await page.evaluate(() => {
      localStorage.setItem('oie-test-push-subscription', '2');
      window.dispatchEvent(new Event('online'));
    });
    assert.equal((await renewal).status(), 204);
    const renewed = await getStatus();
    assert.equal(renewed.enabled, true);
    assert.notEqual(renewed.subscriptionTag, enabled.subscriptionTag);
    assert.equal((await page.request.delete(hosted.url + pushPath, {
      headers: {Origin: hosted.url, 'X-CSRF-Token': csrf, 'If-Match': '"' + enabled.subscriptionTag + '"'},
    })).status(), 409);
    assert.equal((await getStatus()).subscriptionTag, renewed.subscriptionTag);

    const second = await context.newPage();
    await second.goto(hosted.url + entry + '&settingsTab=notifications');
    const browserSound = '[data-notification-sound]';
    const initialSound = page.locator(browserSound);
    const otherSound = second.locator(browserSound);
    await otherSound.waitFor();
    let finishFirst, observedFirst;
    const firstHeld = new Promise(resolve => { finishFirst = resolve; });
    const firstObserved = new Promise(resolve => { observedFirst = resolve; });
    const holdFirstPreference = async route => {
      if (route.request().method() === 'PUT') {
        observedFirst();
        await firstHeld;
      }
      await route.continue();
    };
    await page.route('**' + pushPath, holdFirstPreference);
    await initialSound.press('Space');
    await firstObserved;
    try {
      await second.waitForFunction(selector => !document.querySelector(selector).checked, browserSound);
      const secondSaved = second.waitForResponse(response => response.url().endsWith(pushPath) && response.request().method() === 'PUT');
      await otherSound.press('Space');
      await second.waitForFunction(async lock => (await navigator.locks.query()).pending.some(entry => entry.name === lock), pushPath);
      finishFirst();
      assert.equal((await secondSaved).status(), 204);
      const saved = JSON.parse(await readFile(path.join(hosted.dir, 'host-auth', 'state.json')));
      assert.equal(saved.sessions.find(session => session.push).push.Sound, true);
    } finally {
      finishFirst();
    }
    await page.unroute('**' + pushPath, holdFirstPreference);
    await second.close();

    await mkdir(path.join(__dirname, 'artifacts'), {recursive: true});
    await page.locator('[data-notification-settings]').screenshot({
      path: path.join(__dirname, 'artifacts', 'notification-push-settings-pt.png'),
    });

    const accountResponse = await hosted.api.post('/api/v1/accounts', {
      headers: {'Idempotency-Key': fixedID(30)},
      data: {label: 'Fixture', protocol: 'xmpp', address: 'fixture@example.test'},
    });
    assert.equal(accountResponse.status(), 201);
    const account = await accountResponse.json();
    const conversationResponse = await hosted.api.post('/api/v1/conversations', {
      headers: {'Idempotency-Key': fixedID(31)},
      data: {accountId: account.id, address: 'peer@example.test'},
    });
    assert.equal(conversationResponse.status(), 201);
    const conversation = await conversationResponse.json();

    const [worker] = context.serviceWorkers().filter(worker => worker.url().includes('notification-worker'));
    assert(worker);
    await worker.evaluate(() => {
      self.foregroundDisplays = 0;
      self.registration.showNotification = async () => { self.foregroundDisplays++; };
    });
    const foreground = (messageId, phase = 'reserve') => page.request.post(hosted.url + pushPath + '/foreground', {
      headers: {Origin: hosted.url, 'X-CSRF-Token': csrf},
      data: {engineId: enabled.engineId, messageId, phase, accountId: account.id, conversationId: conversation.id},
    });
    const completeInWorker = message => page.evaluate(async payload => {
      const registration = await navigator.serviceWorker.getRegistration('/');
      const channel = new MessageChannel();
      try {
        return await new Promise((resolve, reject) => {
          const timer = setTimeout(() => reject(Error('Worker did not handle completion')), 5000);
          channel.port1.onmessage = event => { clearTimeout(timer); resolve(event.data); };
          registration.active.postMessage(payload, [channel.port2]);
        });
      } finally {
        channel.port1.close();
      }
    }, {
      type: 'oie:foreground-notification', engine: enabled.engineId, account: account.id,
      conversation: conversation.id, message, title: 'Oiê', body: 'Mensagem nova', lang: 'pt-BR', csrf,
    });
    const localMessage = fixedID(10);
    assert.equal((await foreground(localMessage)).status(), 204);
    assert.equal((await foreground(localMessage)).status(), 409);
    assert.equal(await completeInWorker(localMessage), true);
    assert.equal(await completeInWorker(localMessage), false);
    assert.equal(await completeInWorker(fixedID(11)), false);
    assert.equal(await worker.evaluate(() => self.foregroundDisplays), 1);

    const hiddenMessage = fixedID(14);
    await worker.evaluate(() => {
      const get = self.clients.get.bind(self.clients);
      let reads = 0;
      self.clients.get = id => ++reads === 1 ? get(id) : Promise.resolve({visibilityState: 'hidden'});
    });
    assert.equal((await foreground(hiddenMessage)).status(), 204);
    assert.equal(await completeInWorker(hiddenMessage), false);
    assert.equal(await worker.evaluate(() => self.foregroundDisplays), 1);
    await worker.evaluate(() => { delete self.clients.get; });

    const failedMessage = fixedID(12);
    await worker.evaluate(() => {
      self.registration.showNotification = async () => { throw Error('Platform refused to show the alert'); };
    });
    assert.equal((await foreground(failedMessage)).status(), 204);
    assert.equal(await completeInWorker(failedMessage), false);

    const dndMessage = fixedID(13);
    assert.equal((await foreground(dndMessage)).status(), 204);
    const dnd = await hosted.api.put('/api/v1/accounts/' + account.id + '/communication-preferences', {
      data: {revisionId: null, presence: 'dnd'},
    });
    assert.equal(dnd.status(), 200);
    assert.equal(await completeInWorker(dndMessage), false);
    assert.equal((await foreground(dndMessage, 'shown')).status(), 409);

    await page.close();
    assert.equal(context.pages().length, 0);
    const destination = hosted.url + '/?' + new URLSearchParams({
      notificationEngine: enabled.engineId, account: fixedID(2), chat: fixedID(3), message: fixedID(4),
    });
    const shown = await worker.evaluate(async destination => {
      const event = new PushEvent('push', {
        data: JSON.stringify({web_push: 8030, notification: {
          title: 'Oiê', body: 'Nova mensagem', lang: 'pt-BR', silent: false, navigate: destination,
        }}),
      });
      let shown;
      self.registration.showNotification = async (title, options) => { shown = {title, ...options}; };
      let handled;
      event.waitUntil = promise => { handled = promise; };
      self.dispatchEvent(event);
      await handled;
      return shown;
    }, destination);
    assert.equal(shown.title, 'Oiê');
    assert.equal(shown.body, 'Nova mensagem');
    assert.equal(shown.data.message, fixedID(4));
    assert.equal(shown.silent, false);
    const opened = await worker.evaluate(async data => {
      let opened;
      self.clients.openWindow = async url => { opened = new URL(url, self.location.origin).href; };
      const event = new Event('notificationclick');
      event.notification = {data, close() {}};
      let handled;
      event.waitUntil = promise => { handled = promise; };
      self.dispatchEvent(event);
      await handled;
      return opened;
    }, shown.data);
    assert.deepEqual(
      Object.fromEntries(new URL(opened).searchParams),
      Object.fromEntries(new URL(destination).searchParams),
    );

    const reopened = await context.newPage();
    const statusLoaded = () => reopened.waitForResponse(response =>
      response.url().endsWith(pushPath) && response.request().method() === 'GET');
    const startup = statusLoaded();
    await reopened.goto(hosted.url + entry);
    await reopened.locator('[data-settings-target=device]:visible').click();
    await reopened.locator('[data-settings-target=notifications]').click();
    assert.equal((await startup).status(), 200);
    const reopenedToggle = reopened.locator('[data-notification-background]');
    const banners = reopened.locator('[data-notification-banners]');
    await reopened.bringToFront();
    await banners.focus();
    await reopened.locator('[data-notification-banners]:enabled:checked').waitFor();
    assert(await reopenedToggle.isChecked());
    const failDisable = route => route.request().method() === 'DELETE'
      ? route.fulfill({status: 503}) : route.continue();
    const failure = 'Não foi possível salvar as notificações com o app fechado. Tente novamente.';
    await reopened.route('**' + pushPath, failDisable);
    const attemptedDisable = reopened.waitForResponse(response =>
      response.url().endsWith(pushPath) && response.request().method() === 'DELETE');
    await banners.press('Space');
    assert.equal((await attemptedDisable).status(), 503);
    await reopened.getByText(failure, {exact: true}).waitFor();
    assert(await banners.isChecked());
    assert.equal((await getStatus()).enabled, true);

    // A passive status refresh must not erase the result of a failed write.
    const refreshed = statusLoaded();
    await reopened.evaluate(() => window.dispatchEvent(new Event('focus')));
    assert.equal((await refreshed).status(), 200);
    await reopened.evaluate(key => navigator.locks.request(key, () => {}), pushPath);
    assert.equal(await reopened.locator('#notification-status').textContent(), failure);
    await reopened.unroute('**' + pushPath, failDisable);
    const off = reopened.waitForResponse(response => response.url().endsWith(pushPath) && response.request().method() === 'DELETE');
    await reopened.evaluate(() => localStorage.removeItem('oie.notifications.background'));
    await banners.press('Space');
    assert.equal((await off).status(), 204);
    assert.equal((await getStatus()).enabled, false);
    await reopened.waitForFunction(() => !document.querySelector('[data-notification-banners]').checked);

    const failSave = route => route.request().method() === 'PUT'
      ? route.fulfill({status: 503}) : route.continue();
    await reopened.route('**' + pushPath, failSave);
    await reopened.waitForFunction(() => {
      const toggle = document.querySelector('[data-notification-background]');
      return !toggle.checked && toggle.getAttribute('aria-disabled') === 'false';
    });
    await reopenedToggle.press('Space');
    await reopened.getByText(failure, {exact: true}).waitFor();
    assert.equal((await getStatus()).enabled, false);
    assert.equal(await reopenedToggle.isChecked(), false);
    assert(await reopenedToggle.evaluate(control => control === document.activeElement));
    await reopened.unroute('**' + pushPath, failSave);
    await reopened.route('**' + pushPath, failDisable);
    await banners.press('Space');
    await reopened.waitForFunction(() => document.querySelector('[data-notification-banners]').checked);
    await banners.press('Space');
    await reopened.waitForFunction(() => !document.querySelector('[data-notification-banners]').checked);
    console.log('PASS: hosted opt-in, stable settings geometry, subscription renewal, cross-tab writes, closed-page notification and click, unsubscribe and CSRF');
  } finally {
    await browser?.close();
    await hosted.stop();
  }
}

check().catch(error => { console.error(error); process.exitCode = 1; });
