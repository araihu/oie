const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { mkdtemp, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const path = require('node:path');
const { chromium } = require('playwright');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');

async function openSettings(page, url, fixture) {
  await page.goto(url + '/?open=settings&lang=' + fixture.locale + '&theme=dark');
  const action = page.getByRole('button', { name: fixture.action, exact: true }).first();
  await action.click();
  return page.getByRole('dialog', { name: fixture.action });
}

async function testReset(browser, url, fixture) {
  const before = await (await fetch(url + '/api/v1/engine')).json();
  const registered = await fetch(url + '/api/v1/accounts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': '019a5df0-0000-7000-8000-000000000001' },
    body: JSON.stringify({ protocol: 'xmpp', address: 'alice@localhost', label: 'Temporary account' }),
  });
  assert.equal(registered.status, 201, await registered.text());

  const context = await browser.newContext({ locale: fixture.locale, viewport: fixture.viewport });
  try {
    const page = await context.newPage();
    const dialog = await openSettings(page, url, fixture);
    const input = dialog.getByRole('textbox', { name: fixture.confirmation });
    const submit = dialog.getByRole('button', { name: fixture.action, exact: true });
    await dialog.waitFor();
    assert(await submit.isDisabled());
    await input.fill('Delete');
    assert(await submit.isDisabled());
    await input.fill('delete');
    assert(await submit.isEnabled());
    await input.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    assert.equal((await (await fetch(url + '/api/v1/accounts')).json()).items.length, 1);

    const entry = page.getByRole('button', { name: fixture.action, exact: true }).first();
    await entry.click();
    assert.equal(await input.inputValue(), '');
    await page.route('**/ui/engine/reset', route => route.fulfill({ status: 503 }), { times: 1 });
    await input.fill('delete');
    await submit.click();
    await dialog.waitFor({ state: 'hidden' });
    await page.getByText(fixture.failure, { exact: true }).waitFor();
    assert.equal(await page.evaluate(() => document.body.dataset.engineId), before.id);

    const peer = await context.newPage();
    await peer.goto(url + '/?lang=' + fixture.locale);
    await page.evaluate(async () => {
      localStorage.setItem('oie.draft.test', 'remove me');
      sessionStorage.setItem('oie.test', 'remove me');
      localStorage.setItem('unrelated', 'keep me');
      await new Promise((resolve, reject) => {
        const request = indexedDB.open('oie.pdf-previews.v1', 1);
        request.onupgradeneeded = () => request.result.createObjectStore('thumbnails');
        request.onerror = reject;
        request.onsuccess = () => { request.result.close(); resolve(); };
      });
    });
    await entry.click();
    await input.fill('delete');

    let release;
    const gate = new Promise(resolve => { release = resolve; });
    await page.route('**/ui/engine/reset', async route => {
      await gate;
      await route.continue();
    }, { times: 1 });

    await page.evaluate(() => {
      const selectors = ['#workspace', '#reset-data', '#reset-data-confirmation', '#reset-data-form button[type="submit"]'];
      const measure = () => selectors.flatMap(selector => {
        const { x, y, width, height } = document.querySelector(selector).getBoundingClientRect();
        return [x, y, width, height];
      });
      const before = measure();
      window.resetGeometry = { maxShift: 0, frames: 0, id: 0 };
      function frame() {
        const state = window.resetGeometry;
        const shift = Math.max(...measure().map((value, index) => Math.abs(value - before[index])));
        state.maxShift = Math.max(state.maxShift, shift);
        state.frames++;
        state.id = requestAnimationFrame(frame);
      }
      frame();
    });
    try {
      await submit.click();
      await page.locator('#reset-data-form[aria-busy="true"]').waitFor();
      await page.keyboard.press('Escape');
      assert(await dialog.isVisible());
      assert(await submit.isDisabled());
      const geometry = await page.evaluate(async () => {
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        cancelAnimationFrame(window.resetGeometry.id);
        return window.resetGeometry;
      });
      assert(geometry.frames >= 2 && geometry.maxShift <= 1, JSON.stringify(geometry));
    } finally {
      release();
    }

    await Promise.all([page.waitForURL(url + '/'), peer.waitForURL(url + '/')]);
    await page.waitForFunction(id => document.body.dataset.engineId && document.body.dataset.engineId !== id, before.id);
    await page.waitForFunction(async () => !(await indexedDB.databases()).some(db => db.name === 'oie.pdf-previews.v1'));
    const storage = await page.evaluate(() => ({
      draft: localStorage.getItem('oie.draft.test'),
      session: sessionStorage.getItem('oie.test'),
      unrelated: localStorage.getItem('unrelated'),
    }));
    assert.deepEqual(storage, { draft: null, session: null, unrelated: 'keep me' });
    assert(!(await context.cookies()).some(cookie => cookie.name === 'oie_theme' || cookie.name === 'oie_lang'));
    assert.deepEqual((await (await fetch(url + '/api/v1/accounts')).json()).items, []);
  } finally {
    await context.close();
  }
}

(async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'oie-reset-browser-'));
  const child = spawn(binary, ['--role', 'hosted', '--listen', '127.0.0.1:0', '--data-dir', dir]);
  const browser = await chromium.launch();
  try {
    let log = '';
    const address = new Promise((resolve, reject) => {
      child.stderr.on('data', chunk => {
        log += chunk;
        const match = log.match(/address=(http:\/\/127\.0\.0\.1:\d+)/);
        if (match) resolve(match[1]);
      });
      child.once('error', reject);
      child.once('exit', code => reject(new Error(`engine exited (${code}): ${log}`)));
    });
    const url = await address;
    await testReset(browser, url, {
      locale: 'en', viewport: { width: 1440, height: 900 }, action: 'Delete all data',
      confirmation: 'Type delete to confirm', failure: 'Could not delete all data.',
    });
    await testReset(browser, url, {
      locale: 'pt-BR', viewport: { width: 390, height: 844 }, action: 'Apagar todos os dados',
      confirmation: 'Digite delete para confirmar', failure: 'Não foi possível apagar todos os dados.',
    });
    console.log('PASS: typed reset confirmation, cancellation, refusal, stable pending dialog and empty installation');
  } finally {
    await browser.close();
    if (child.exitCode === null) {
      const exited = once(child, 'exit');
      child.kill();
      await exited;
    }
    await rm(dir, { recursive: true, force: true });
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
