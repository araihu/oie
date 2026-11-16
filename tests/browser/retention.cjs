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
    const tab = page.getByRole('tab', {name: engine, exact: true});
    await page.getByRole('tab', {name: device, exact: true}).click();
    assert.equal(await page.getByRole('tabpanel', {name: device, exact: true}).locator('#engine-retention').count(), 0);
    await tab.press('Enter');

    const panel = page.getByRole('tabpanel', {name: engine, exact: true});
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
    await page.goto(server.url + '/settings/retention?lang=' + lang + '&theme=' + theme);
    await tab.waitFor();
    assert.equal(await tab.getAttribute('aria-selected'), 'true');
    await room.waitFor();
    assert.equal(await room.inputValue(), '100');
    assert.deepEqual(errors, []);
  } finally {
    await context.close();
  }
}

(async () => {
  const server = await startHostedOie(binary);
  let browser;
  try {
    browser = await chromium.launch();
    await verify(browser, server, {width: 1440, lang: 'en', theme: 'light', engine: 'Engine', device: 'This device'});
    await verify(browser, server, {width: 390, lang: 'pt-BR', theme: 'dark', engine: 'Motor', device: 'Este dispositivo'});
    console.log('PASS: engine retention tab, blur saving, stable focus and geometry, invalid input, retry and concurrent revisions');
  } finally {
    await browser?.close();
    await server.stop();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
