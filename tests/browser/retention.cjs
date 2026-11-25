const assert = require('node:assert/strict');
const path = require('node:path');
const {chromium} = require('playwright');
const {startHostedOie} = require('./fixtures/hosted-oie.cjs');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');
const apiPath = '/api/v1/engine/retention';
const formPath = '**/ui/settings/retention?*';

async function policy(api) {
  const response = await api.get(apiPath);
  assert(response.ok(), await response.text());
  return response.json();
}

async function setPolicy(api, roomDays, directDays) {
  const {revision} = await policy(api);
  const response = await api.put(apiPath, {data: {expectedRevision: revision, roomDays, directDays}});
  assert(response.ok(), await response.text());
}

async function verify(browser, server, {width, lang, theme, engine, device}) {
  await setPolicy(server.api, 180, 0);
  const context = await browser.newContext({
    ignoreHTTPSErrors: true, locale: lang, viewport: {width, height: 870}, reducedMotion: 'reduce',
  });
  try {
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    let writes = 0;
    page.on('request', request => {
      if (request.method() === 'POST' && request.url().includes('/ui/settings/retention?')) writes++;
    });
    await server.signIn(page, '/?open=settings&lang=' + lang + '&theme=' + theme);
    await page.locator('[data-settings-target=device]:visible').click();
    assert.equal(await page.locator('[data-settings-page=device] #engine-retention').count(), 0);
    await page.locator('[data-settings-back]:visible').click();
    await page.locator('[data-settings-target=engine]:visible').press('Enter');
    const panel = page.locator('[data-settings-page=engine]');
    const form = panel.locator('[data-engine-retention]');
    const room = form.locator('#retention-days-room');
    const direct = form.locator('#retention-days-direct');
    const directMode = form.locator('#retention-direct-trigger');
    const options = form.locator('#retention-direct-listbox').getByRole('option');
    const feedback = form.locator('[data-retention-status]');
    const retry = form.locator('[data-retention-retry]');
    await room.waitFor();
    assert.equal(await form.locator('button[type=submit]').count(), 0);
    assert.equal(await room.inputValue(), '180');

    const release = Promise.withResolvers();
    await page.route(formPath, async route => {
      if (route.request().method() === 'POST') {
        await release.promise;
      }
      await route.continue();
    }, {times: 1});
    await room.fill('90');
    assert.equal(writes, 0, 'typing leaves the saved policy untouched');
    const firstWrite = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/ui/settings/retention?'));
    await directMode.focus();
    await firstWrite;
    const rect = await room.boundingBox();
    const focused = await directMode.evaluate(element => element === document.activeElement);
    assert(focused, 'blur save keeps focus on the next field');
    const shifts = await room.evaluate((element, origin) => new Promise(resolve => {
      const frames = [];
      const paint = () => {
        const current = element.getBoundingClientRect();
        frames.push(Math.max(...['x', 'y', 'width', 'height'].map(key => Math.abs(current[key] - origin[key]))));
        if (frames.length === 12) resolve(frames);
        else requestAnimationFrame(paint);
      };
      requestAnimationFrame(paint);
    }), rect);
    assert(shifts.every(shift => shift <= 1), JSON.stringify(shifts));

    await directMode.click();
    await options.first().click();
    await direct.focus();
    await direct.fill('60');
    await room.focus();
    assert.equal(writes, 1, 'later blurs queue behind the pending revision');
    const queued = page.waitForResponse(response => response.request().postData()?.includes('direct-days=60') && response.ok());
    release.resolve();
    await queued;
    await page.waitForFunction(() => {
      const form = document.querySelector('[data-engine-retention]');
      return form.querySelector('[data-retention-status]').textContent === form.dataset.saved;
    });
    let saved = await policy(server.api);
    assert.equal(saved.roomDays, 90);
    assert.equal(saved.directDays, 60);
    assert.equal(await direct.inputValue(), '60');
    assert(await room.evaluate(element => element === document.activeElement));

    await room.fill('0');
    await direct.focus();
    assert.equal(await room.getAttribute('aria-invalid'), 'true');
    assert.equal(writes, 2, 'an invalid period never reaches the engine');
    await room.fill('90');
    await direct.focus();
    assert.equal(await room.getAttribute('aria-invalid'), null);
    assert.equal(await feedback.textContent(), await form.getAttribute('data-saved'));
    assert.equal(writes, 2, 'reverting to the saved period clears validation without writing');

    const releaseFailure = Promise.withResolvers();
    await page.route(formPath, async route => {
      await releaseFailure.promise;
      await route.fulfill({status: 503});
    }, {times: 1});
    const failedWrite = page.waitForRequest(request => request.method() === 'POST' && request.url().includes('/ui/settings/retention?'));
    await room.fill('120');
    await direct.focus();
    await failedWrite;
    await room.fill('0');
    await direct.focus();
    releaseFailure.resolve();
    await page.waitForFunction(() => !document.querySelector('[data-retention-retry]').hasAttribute('aria-disabled'));
    assert.equal(await room.inputValue(), '0');
    assert.equal(await feedback.textContent(), await form.getAttribute('data-invalid'));
    assert.equal(await retry.isVisible(), false, 'a failed response cannot restore the superseded period');
    assert.equal((await policy(server.api)).roomDays, 90);

    await page.route(formPath, route => route.fulfill({status: 503}), {times: 1});
    await room.fill('120');
    await direct.focus();
    await retry.waitFor();
    assert.equal(await room.inputValue(), '120');
    assert.equal((await policy(server.api)).roomDays, 90);
    await retry.click();
    await retry.waitFor({state: 'hidden'});
    assert.equal((await policy(server.api)).roomDays, 120);

    await setPolicy(server.api, 120, 45);
    await room.fill('100');
    await direct.focus();
    await retry.waitFor();
    assert.equal(await feedback.textContent(), await form.getAttribute('data-conflict'));
    assert.equal(await room.inputValue(), '100');
    await retry.click();
    await retry.waitFor({state: 'hidden'});
    saved = await policy(server.api);
    assert.equal(saved.roomDays, 100);
    assert.equal(saved.directDays, 45, 'retry retains the other period changed by another client');

    assert.equal(await direct.inputValue(), '45');
    await directMode.click();
    await options.last().click();
    await room.focus();
    await page.waitForFunction(() => {
      const form = document.querySelector('[data-engine-retention]');
      return form.querySelector('[data-retention-status]').textContent === form.dataset.saved;
    });
    assert.equal((await policy(server.api)).directDays, 0);

    await setPolicy(server.api, 95, 45);
    const writesBeforeConcurrent = writes;
    const samePeriod = page.waitForResponse(response => response.url().includes('/ui/settings/retention?') &&
      response.request().postData()?.includes('room-days=95') && response.status() === 409);
    await room.fill('95');
    await directMode.focus();
    await samePeriod;
    await page.waitForFunction(() => {
      const form = document.querySelector('[data-engine-retention]');
      return form.querySelector('[data-retention-status]').textContent === form.dataset.saved;
    });
    assert.equal(await direct.isVisible(), false, 'the focused choice stays in place');
    await room.focus();
    await direct.waitFor();
    assert.equal(await direct.inputValue(), '45', 'leaving an unchanged choice reconciles the engine value');
    assert.equal(writes, writesBeforeConcurrent + 1);

    const reachedEngine = Promise.withResolvers();
    const loseResponse = Promise.withResolvers();
    await page.route(formPath, async route => {
      const response = await route.fetch();
      reachedEngine.resolve(response);
      await loseResponse.promise;
      await route.abort();
    }, {times: 1});
    await room.fill('110');
    await direct.focus();
    assert((await reachedEngine.promise).ok());
    assert.equal((await policy(server.api)).roomDays, 110);
    await room.fill('95');
    await direct.focus();
    loseResponse.resolve();
    await retry.waitFor();

    const lostRevision = page.waitForResponse(response => response.url().includes('/ui/settings/retention?') &&
      response.request().postData()?.includes('room-days=95') && response.status() === 409);
    await retry.click();
    await lostRevision;
    await page.waitForFunction(() => {
      const form = document.querySelector('[data-engine-retention]');
      return form.querySelector('[data-retention-status]').textContent === form.dataset.conflict;
    });
    assert.equal((await policy(server.api)).roomDays, 110);
    await retry.click();
    await retry.waitFor({state: 'hidden'});
    assert.equal((await policy(server.api)).roomDays, 95, 'a lost response cannot discard a queued undo');

    await page.goto(server.url + '/settings/retention?lang=' + lang + '&theme=' + theme);
    await panel.waitFor();
    await room.waitFor();
    assert.equal(await room.inputValue(), '95');
    assert.deepEqual(errors, []);
  } finally {
    await context.close();
  }
}

async function verifyConversation(browser, server) {
  const create = async (path, data, key) => {
    const response = await server.api.post(path, {data, headers: {'Idempotency-Key': key}});
    assert(response.ok(), await response.text());
    return response.json();
  };
  const account = await create('/api/v1/accounts', {protocol:'xmpp', address:'owner@example.test', label:'Owner'},
    '019a5df0-0000-7000-8000-000000000011');
  const chat = await create('/api/v1/conversations', {accountId:account.id, address:'reader@example.test'},
    '019a5df0-0000-7000-8000-000000000012');
  const page = await browser.newPage({ignoreHTTPSErrors:true, viewport:{width:390,height:860}, reducedMotion:'reduce'});
  try {
    await server.signIn(page, '/?chat=' + chat.id + '&open=details&lang=en');
    await page.locator('[data-info-entry=preferences]').click();
    await page.locator('[data-info-entry=retention]').click();
    const form = page.locator('[data-conversation-retention-form]');
    const trigger = form.locator('#retention-override-trigger');
    const days = form.locator('#retention-days-override');
    const posts = [];
    page.on('request', request => {
      if (request.method() === 'POST' && request.url().includes('/ui/settings/retention?')) posts.push(request);
    });
    const read = async () => (await server.api.get('/api/v1/conversations/' + chat.id + '/retention')).json();
    const choose = async index => {
      await trigger.click();
      await form.getByRole('option').nth(index).click();
    };
    const saved = () => page.waitForResponse(response => response.request().method() === 'POST' &&
      response.url().includes('/ui/settings/retention?') && response.ok());
    await choose(1);
    await days.fill('48');
    await page.waitForTimeout(200);
    assert.equal(posts.length, 0);
    await Promise.all([saved(), days.press('Enter')]);
    assert.equal((await read()).days, 48);
    await Promise.all([saved(), choose(2)]);
    assert.equal((await read()).days, 0);
    await Promise.all([saved(), choose(0)]);
    assert.equal((await read()).days, null);
    assert.equal(posts.length, 3);
    await form.locator('button[type=submit]').waitFor({state:'hidden'});
    await page.locator('[data-info-back]').click();
    await page.waitForFunction(() => document.activeElement?.dataset.infoEntry === 'retention');
  } finally {
    await page.close();
  }
}

(async () => {
  const server = await startHostedOie(binary);
  let browser;
  try {
    browser = await chromium.launch();
    await verify(browser, server, {width: 1440, lang: 'en', theme: 'light', engine: 'Engine', device: 'This device'});
    await verify(browser, server, {width: 390, lang: 'pt-BR', theme: 'dark', engine: 'Motor', device: 'Este dispositivo'});
    await verifyConversation(browser, server);
    console.log('PASS: engine and conversation retention, blur saving, stable focus and geometry, invalid input, retry and concurrent revisions');
  } finally {
    await browser?.close();
    await server.stop();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
