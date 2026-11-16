const assert = require('node:assert/strict');
const path = require('node:path');
const { chromium } = require('playwright');
const { startHostedOie } = require('./fixtures/hosted-oie.cjs');
const { resizeVisibleViewport } = require('./fixtures/viewport.cjs');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');

async function openSettings(page, server, fixture) {
  await server.signIn(page, '/?open=settings&lang=' + fixture.locale + '&theme=' + fixture.theme);
  const settings = page.getByRole('dialog', { name: fixture.settings, exact: true });
  const general = settings.getByRole('tabpanel', { name: fixture.general, exact: true });
  for (const name of [fixture.accessKeys, fixture.diagnostics, fixture.about, fixture.destructiveActions]) {
    await general.getByRole('button', { name, exact: true }).waitFor();
  }
  const footer = settings.locator('footer');
  await footer.getByRole('button', { name: fixture.signOut, exact: true }).waitFor();
  assert.equal(await footer.getByRole('button').count(), 1);
  assert.equal(await general.getByText(fixture.engineHeading, { exact: true }).count(), 0);
  assert.equal(await general.getByText(fixture.engineLabel, { exact: true }).count(), 0);
  await resizeVisibleViewport(page, { width: fixture.viewport.width, height: 280 });
  const placement = await footer.evaluate(node => {
    const panel = node.closest('[role="dialog"]');
    const content = panel.querySelector('.oie-settings-scroll');
    const before = node.getBoundingClientRect().bottom;
    content.scrollTop = content.scrollHeight;
    return {
      scrolled: content.scrollTop > 0,
      shift: Math.abs(node.getBoundingClientRect().bottom - before),
      gap: Math.abs(node.getBoundingClientRect().bottom - panel.getBoundingClientRect().bottom),
    };
  });
  assert(placement.scrolled && placement.shift <= 1 && placement.gap <= 1, JSON.stringify(placement));
  await resizeVisibleViewport(page, fixture.viewport);

  const entry = settings.getByRole('button', { name: fixture.destructiveActions, exact: true });
  const actions = page.getByRole('dialog', { name: fixture.destructiveActions, exact: true });
  assert.equal(await settings.getByRole('button', { name: fixture.action, exact: true }).count(), 0);
  await entry.press('Enter');
  await actions.waitFor();
  await settings.waitFor({ state: 'hidden' });
  assert.equal(await page.locator('#reset-data').isVisible(), false);
  await actions.getByRole('button', { name: fixture.back, exact: true }).click();
  await settings.waitFor();
  await actions.waitFor({ state: 'hidden' });
  assert.equal(await settings.getByRole('tab', { name: fixture.general, exact: true }).getAttribute('aria-selected'), 'true');
  await entry.click();
  await actions.waitFor();
  await settings.waitFor({ state: 'hidden' });

  const action = actions.getByRole('button', { name: fixture.action, exact: true });
  const [red, green, blue, alpha] = await action.evaluate(node => {
    const canvas = document.createElement('canvas');
    const paint = canvas.getContext('2d');
    paint.fillStyle = getComputedStyle(node).backgroundColor;
    paint.fillRect(0, 0, 1, 1);
    return Array.from(paint.getImageData(0, 0, 1, 1).data);
  });
  assert(alpha === 255 && red > green + 30 && red > blue + 30, 'reset action uses a filled red background');
  await action.click();
  return { actions, dialog: page.getByRole('dialog', { name: fixture.action, exact: true }) };
}

async function testReset(browser, server, fixture) {
  const {api, url} = server;
  const before = await (await api.get('/api/v1/engine')).json();
  const registered = await api.post('/api/v1/accounts', {
    headers: { 'Content-Type': 'application/json', 'Idempotency-Key': '019a5df0-0000-7000-8000-000000000001' },
    data: { protocol: 'xmpp', address: 'alice@localhost', label: 'Temporary account' },
  });
  assert.equal(registered.status(), 201, await registered.text());

  const context = await browser.newContext({ locale: fixture.locale, viewport: fixture.viewport, ignoreHTTPSErrors: true });
  try {
    if (fixture.blockCompletionStorage) {
      await context.addInitScript(() => {
        const setItem = Storage.prototype.setItem;
        Storage.prototype.setItem = function (key, value) {
          if (this === sessionStorage && key === 'oie.reset.complete') throw new Error('Storage unavailable');
          return setItem.call(this, key, value);
        };
      });
    }
    const page = await context.newPage();
    const { actions, dialog } = await openSettings(page, server, fixture);
    const input = dialog.getByRole('textbox', { name: fixture.confirmation });
    const submit = dialog.locator('button[type="submit"]');
    await dialog.waitFor();
    assert(await submit.isDisabled());
    await input.fill('Delete');
    assert(await submit.isDisabled());
    await input.fill('delete');
    assert(await submit.isEnabled());
    await input.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    assert.equal((await (await api.get('/api/v1/accounts')).json()).items.length, 1);

    assert(await actions.isVisible(), 'cancelling the confirmation keeps destructive actions open');
    const entry = actions.getByRole('button', { name: fixture.action, exact: true });
    await entry.click();
    assert.equal(await input.inputValue(), '');
    await page.route('**/ui/engine/reset', route => route.fulfill({ status: 503 }), { times: 1 });
    await input.fill('delete');
    await submit.click();
    await dialog.waitFor({ state: 'hidden' });
    await page.getByText(fixture.failure, { exact: true }).waitFor();
    assert.equal(await page.getByText(fixture.success, { exact: true }).count(), 0);
    assert.equal(await page.evaluate(() => document.body.dataset.engineId), before.id);

    const peer = await context.newPage();
    await peer.goto(url + '/?lang=' + fixture.locale);
    await page.evaluate(async engine => {
      localStorage.setItem('oie.draft.test', 'remove me');
      sessionStorage.setItem('oie.test', 'remove me');
      sessionStorage.setItem('oie.forwarding-journals:' + engine, 'remove current forwarding journal');
      sessionStorage.setItem('forwarding-journals:' + engine, 'remove legacy forwarding journal');
      localStorage.setItem('unrelated', 'keep me');
      await new Promise((resolve, reject) => {
        const request = indexedDB.open('oie.pdf-previews.v1', 1);
        request.onupgradeneeded = () => request.result.createObjectStore('thumbnails');
        request.onerror = reject;
        request.onsuccess = () => { request.result.close(); resolve(); };
      });
    }, before.id);
    await entry.click();
    await input.fill('delete');
    if (fixture.blockCompletionStorage) await page.evaluate(() => history.replaceState(history.state, '', '/'));

    let release;
    const gate = new Promise(resolve => { release = resolve; });
    await page.route('**/ui/engine/reset', async route => {
      await gate;
      await route.continue();
    }, { times: 1 });

    await page.evaluate(() => {
      const selectors = ['#workspace', '#destructive-actions-body', '#reset-data', '#reset-data-confirmation', '#reset-data-form button[type="submit"]'];
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
      await dialog.getByRole('button', { name: fixture.pending, exact: true }).waitFor();
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
    await page.getByText(fixture.success, { exact: true }).waitFor();
    const addAccount = page.locator('#conversation-list').getByRole('link', { name: fixture.addAccount, exact: true });
    await addAccount.waitFor();
    assert.equal(await peer.getByText(fixture.success, { exact: true }).count(), 0);
    await page.waitForFunction(async () => !(await indexedDB.databases()).some(db => db.name === 'oie.pdf-previews.v1'));
    const storage = await page.evaluate(engine => ({
      draft: localStorage.getItem('oie.draft.test'),
      session: sessionStorage.getItem('oie.test'),
      forwarding: sessionStorage.getItem('oie.forwarding-journals:' + engine),
      legacyForwarding: sessionStorage.getItem('forwarding-journals:' + engine),
      unrelated: localStorage.getItem('unrelated'),
    }), before.id);
    assert.deepEqual(storage, { draft: null, session: null, forwarding: null, legacyForwarding: null, unrelated: 'keep me' });
    assert(!(await context.cookies()).some(cookie => cookie.name === 'oie_theme' || cookie.name === 'oie_lang'));
    assert.deepEqual((await (await api.get('/api/v1/accounts')).json()).items, []);
    await page.reload();
    await page.waitForFunction(() => window.OieRuntime?.generation > 0);
    assert.equal(await page.getByText(fixture.success, { exact: true }).count(), 0);
    await addAccount.click();
    await page.locator('#register-address').waitFor();
  } finally {
    await context.close();
  }
}

(async () => {
  const server = await startHostedOie(binary);
  let browser;
  try {
    browser = await chromium.launch();
    await testReset(browser, server, {
      locale: 'en', viewport: { width: 1440, height: 900 }, action: 'Delete all data',
      settings: 'Settings', general: 'General', about: 'About', theme: 'light',
      accessKeys: 'Access keys', diagnostics: 'Diagnostics', signOut: 'Sign out',
      destructiveActions: 'Destructive actions', back: 'Back to settings',
      engineHeading: 'Engine', engineLabel: 'Hosted engine',
      confirmation: 'Type delete to confirm', failure: 'Could not delete all data.',
      pending: 'Deleting…', success: 'All Oiê data was deleted. Add an account to start again.', addAccount: 'Add account',
    });
    await testReset(browser, server, {
      locale: 'pt-BR', viewport: { width: 390, height: 844 }, action: 'Apagar todos os dados',
      settings: 'Configurações', general: 'Geral', about: 'Sobre', theme: 'dark',
      accessKeys: 'Chaves de acesso', diagnostics: 'Diagnóstico', signOut: 'Sair',
      destructiveActions: 'Ações destrutivas', back: 'Voltar às configurações',
      engineHeading: 'Motor', engineLabel: 'Motor hospedado',
      confirmation: 'Digite delete para confirmar', failure: 'Não foi possível apagar todos os dados.',
      pending: 'Apagando…', success: 'Todos os dados do Oiê foram apagados. Adicione uma conta para recomeçar.',
      addAccount: 'Adicionar conta', blockCompletionStorage: true,
    });
    console.log('PASS: nested destructive actions, typed reset confirmation, cancellation, refusal, stable pending dialog and empty installation');
  } finally {
    await browser?.close();
    await server.stop();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
