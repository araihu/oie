const assert = require('node:assert/strict');
const path = require('node:path');
const {chromium} = require('playwright');
const {startOie} = require('./fixtures/oie-server.cjs');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');
const enginePath = '/api/v1/engine/read-receipts';
const fixtureKey = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const policyRoute = '**/ui/settings/read-receipts?*';

async function verify(browser, server, {width, lang, engine, navigation, on, off, retry}) {
  const context = await browser.newContext({viewport: {width, height: 900}, hasTouch: width === 390, reducedMotion: 'reduce'});
  try {
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    const call = async (method, route, data, headers) => {
      const response = await context.request.fetch(server.url + route, {method, data, headers});
      assert(response.ok(), await response.text());
      return response.json();
    };
    const get = route => call('GET', route);
    const set = async (route, values) => {
      const {revision} = await get(route);
      return call('PUT', route, {expectedRevision: revision, ...values});
    };
    const account = await call('POST', '/api/v1/accounts',
      {protocol: 'xmpp', address: 'alice@example.test', label: 'Alice'}, {'Idempotency-Key': fixtureKey(1)});
    const chat = await call('POST', '/api/v1/conversations',
      {accountId: account.id, address: 'bruno@example.test'}, {'Idempotency-Key': fixtureKey(2)});
    const chatPath = '/api/v1/conversations/' + chat.id;
    const conversation = chatPath + '/read-receipts';
    await set(enginePath, {enabled: true});
    await set(conversation, {override: false});

    const settings = page.locator('#engine-read-receipts-settings');
    const globalMode = settings.locator('#engine-read-receipts-mode-trigger');
    const expectMode = async (selector, value) => {
      await page.waitForFunction(({selector, value}) =>
        document.querySelector(selector)?.textContent.trim() === value, {selector, value});
    };
    const policyResponse = method => page.waitForResponse(r =>
      r.request().method() === method && r.url().includes('/ui/settings/read-receipts?') && r.status() === 200);
    const choose = async (trigger, option) => {
      await trigger.click();
      await page.getByRole('option', {name: option, exact: true}).click();
    };
    const reopenSettings = async () => {
      if (width === 390) await page.getByRole('button', {name: navigation, exact: true}).click();
      await page.locator('[data-navigation-settings]:visible').click();
      const engineEntry = page.locator('[data-settings-target=engine]');
      if (await engineEntry.isVisible()) await engineEntry.click();
      await settings.scrollIntoViewIfNeeded();
    };
    const holdSave = async change => {
      const pending = Promise.withResolvers();
      const release = Promise.withResolvers();
      const route = async request => {
        pending.resolve();
        await release.promise;
        await request.continue();
      };
      await page.route(policyRoute, route, {times: 1});
      await change();
      await pending.promise;
      return release.resolve;
    };

    // A failed initial GET and an HTML error page preserve the retry control.
    await page.route(policyRoute, route => route.abort(), {times: 1});
    await page.goto(server.url + '/?inbox=1&open=settings&settingsTab=engine&lang=' + lang);
    await settings.scrollIntoViewIfNeeded();
    const retryButton = settings.getByRole('button', {name: retry, exact: true});
    page.setDefaultTimeout(10000);
    await retryButton.waitFor();
    await page.route(policyRoute, route => route.fulfill({status: 503, contentType: 'text/html', body: '<p>Proxy error</p>'}), {times: 1});
    const response = page.waitForResponse(r => r.url().includes('/ui/settings/read-receipts?') && r.status() === 503);
    await retryButton.click();
    await response;
    await retryButton.click();
    await expectMode('#engine-read-receipts-mode-trigger', on);

    // Reopening sees changes from another client.
    await page.keyboard.press('Escape');
    await globalMode.waitFor({state: 'hidden'});
    await set(enginePath, {enabled: false});
    await reopenSettings();
    await expectMode('#engine-read-receipts-mode-trigger', off);

    // An overlapping reopen waits for the save instead of rendering old state.
    const releaseGlobal = await holdSave(() => choose(globalMode, on));
    await page.keyboard.press('Escape');
    await settings.waitFor({state: 'hidden'});
    const globalSaved = policyResponse('POST');
    const globalReloaded = policyResponse('GET');
    await reopenSettings();
    releaseGlobal();
    await globalSaved;
    await globalReloaded;
    await expectMode('#engine-read-receipts-mode-trigger', on);
    assert.equal((await get(enginePath)).enabled, true);

    // The conversation row remains a navigation link with no separate action.
    await page.goto(server.url + '/?inbox=1&lang=' + lang);
    assert.equal(await page.locator('#message-timeline').count(), 0);
    const row = page.locator(`[data-conversation-row="${chat.id}"]`);
    const rowLink = row.locator('a[data-conversation-id]');
    await rowLink.focus();
    assert.equal(await row.locator('button').count(), 0);
    const rowVersion = await row.getAttribute('data-version');
    const currentChat = await get(chatPath);
    await call('PATCH', chatPath, {accountId: account.id, expectedRevisionId: currentChat.revisionId, favorite: !currentChat.favorite},
      {'Idempotency-Key': fixtureKey(width)});
    await page.waitForFunction(({id, version}) => {
      const next = document.querySelector(`[data-conversation-row="${CSS.escape(id)}"]`);
      return next && next.dataset.version !== version;
    }, {id: chat.id, version: rowVersion});
    assert.equal(await rowLink.evaluate(link => link === document.activeElement), true);
    assert.equal(await row.locator('button').count(), 0);
    await rowLink.press('Enter');
    await page.locator('#chat-header .oie-chat-avatar-trigger').click();
    await page.locator('[data-info-entry=preferences]').click();
    const entry = page.locator('[data-info-entry="read-receipts"]');
    await entry.click();
    const modeSelector = '#conversation-read-receipts-mode-trigger';
    const mode = page.locator(modeSelector);
    await expectMode(modeSelector, off);
    const releaseConversation = await holdSave(() => choose(mode, on));
    await page.waitForFunction(() => document.querySelector('#conversation-read-receipts fieldset').disabled);
    const conversationSaved = policyResponse('POST');
    releaseConversation();
    await conversationSaved;
    await expectMode(modeSelector, on);
    assert.equal((await get(conversation)).override, true);
    await page.locator('#chat-info-content [data-info-back]').click();
    await mode.waitFor({state: 'hidden'});
    await set(conversation, {override: false});
    await entry.click();
    await expectMode(modeSelector, off);
    assert.deepEqual(errors, []);
  } finally {
    await context.close();
  }
}

(async () => {
  const server = await startOie(binary, [], '/api/v1/engine');
  let browser;
  try {
    browser = await chromium.launch();
    await verify(browser, server, {width: 1440, lang: 'pt-BR', engine: 'Motor', navigation: 'Abrir navegação',
      on: 'Enviar', off: 'Não enviar', retry: 'Tentar novamente'});
    await verify(browser, server, {width: 390, lang: 'en', engine: 'Engine', navigation: 'Open navigation',
      on: 'Send', off: 'Do not send', retry: 'Try again'});
    console.log('PASS: read receipt failures and retry, fresh policy after reopen, queued saves and conversation details');
  } finally {
    await browser?.close();
    await server.stop();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
