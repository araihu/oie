// Exercise the real viewer, access check and exports on an isolated Oiê.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { chromium } = require('playwright');
const { startOie } = require('./fixtures/oie-server.cjs');
const { startHostedOie } = require('./fixtures/hosted-oie.cjs');

const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');
const logPath = '/ui/diagnostics/log';
const key = 'test-owner-key-sentinel';

async function run(browser, mode) {
  const hosted = mode === 'hosted';
  const server = hosted ? await startHostedOie(binary) : await startOie(binary, ['--role', 'local'], '/api/v1/health');
  const {url: base, dir: data} = server;
  const context = await browser.newContext({
    viewport: { width: hosted ? 1440 : 390, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
    reducedMotion: 'reduce',
    ignoreHTTPSErrors: hosted,
  });
  try {
    const log = path.join(data, 'diagnostics', 'oie-diagnostics.jsonl');
    const fixture = JSON.parse((await fs.readFile(log, 'utf8')).trim().split('\n').at(-1));
    const history = Array.from({ length: 300 }, (_, index) => JSON.stringify({
      ...fixture, event: 'upstream_http_error', component: 'presentation', level: 'warn',
      duration_ms: index + 1, http_status: 503,
    }));
    history.push(JSON.stringify({message: 'browser-private-message-sentinel', token: 'browser-private-token-sentinel', accessKey: key}));
    await fs.appendFile(log, history.join('\n') + '\n');
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    let requests = 0;
    page.on('request', request => {
      if ([logPath, '/ui/diagnostics.js'].includes(new URL(request.url()).pathname)) requests++;
    });
    const pt = mode === 'local';
    if (hosted) {
      const denied = await context.request.get(base + logPath, {headers: {'X-Oie-Diagnostics': '1'}});
      assert.equal(denied.status(), 401);
      await server.signIn(page, '/?lang=en');
    } else {
      await page.goto(base + '/?lang=pt-BR');
    }
    assert.equal(requests, 0, 'initial workspace must not read the diagnostics file');
    if (pt) await page.getByRole('button', { name: 'Abrir navegação', exact: true }).click();
    await page.locator('[data-navigation-settings]:visible').click();
    const settings = page.getByRole('dialog', { name: pt ? 'Configurações' : 'Settings', exact: true });
    await settings.waitFor();
    assert.equal(requests, 0, 'opening Settings must not read the diagnostics file');
    const readSnapshot = page.waitForResponse(response => response.url().includes(logPath) && response.ok());
    const started = Date.now();
    await settings.getByRole('button', { name: pt ? 'Diagnóstico' : 'Diagnostics', exact: true }).click();
    const viewer = page.getByRole('dialog', { name: pt ? 'Diagnóstico' : 'Diagnostics', exact: true });
    const records = viewer.locator('[data-diagnostics-records]');
    const readyEntry = records.getByText('ready', { exact: true });
    const menuLabel = pt ? 'Mais opções' : 'More options';
    const actions = viewer.getByRole('group', { name: menuLabel, exact: true });
    const copy = actions.getByRole('button', { name: pt ? 'Copiar' : 'Copy', exact: true });
    const more = actions.getByRole('button', { name: menuLabel, exact: true });
    const download = viewer.getByRole('menuitem', { name: pt ? 'Baixar' : 'Download', exact: true });
    const refresh = viewer.getByRole('button', { name: pt ? 'Atualizar' : 'Refresh', exact: true });
    await viewer.waitFor();
    if (hosted) assert.equal(await viewer.getByLabel('Diagnostics access key', {exact: true}).isVisible(), false);
    await readyEntry.waitFor();
    const firstReady = Date.now() - started;
    const loadTiming = await page.evaluate(path => {
      const read = performance.getEntriesByType('resource').find(entry => new URL(entry.name).pathname === path && entry.responseStatus === 200);
      return read && { transfer: Math.round(read.duration), afterResponse: Math.round(performance.now() - read.responseEnd) };
    }, logPath);
    const snapshot = await (await readSnapshot).text();
    const rendered = await records.textContent();
    assert(!snapshot.includes(key));
    assert(!snapshot.includes('browser-private'));
    assert(!rendered.includes(key));
    assert(!rendered.includes('browser-private'));
    assert.equal(snapshot.split('\n').filter(line => line.includes('upstream_http_error')).length, 300);
    const warning = records.locator('details').filter({ hasText: 'upstream_http_error' }).first();
    assert.equal(await records.getByText('upstream_http_error', { exact: true }).count(), 300);
    assert.equal(await warning.getByText(pt ? 'Aviso' : 'Warning', { exact: true }).count(), 1);
    assert(await warning.locator('time').textContent());
    const fields = warning.locator('pre');
    assert.equal(await fields.textContent(), '');
    await warning.locator('summary').press('Enter');
    await fields.waitFor();
    const firstWarning = snapshot.trim().split('\n').map(line => JSON.parse(line))
      .find(entry => entry.event === 'upstream_http_error');
    assert.deepEqual(JSON.parse(await fields.textContent()), firstWarning);
    await warning.locator('summary').press('Enter');
    await fields.waitFor({ state: 'hidden' });
    assert(await records.evaluate(node => node.scrollHeight > node.clientHeight));
    assert(snapshot.length <= 256000);
    assert(snapshot.endsWith('\n'));
    for (const action of [refresh, copy, more]) assert.equal(await action.locator('svg[aria-hidden="true"]').count(), 1);
    await copy.click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), snapshot);
    assert.equal(await download.isVisible(), false);
    await more.press('Enter');
    await download.waitFor();
    assert.equal(await download.locator('svg[aria-hidden="true"]').count(), 1);
    await page.waitForFunction(() => document.activeElement.id === 'diagnostics-download');
    const downloaded = page.waitForEvent('download');
    await download.press('Enter');
    const file = await downloaded;
    assert.equal(file.suggestedFilename(), 'oie-diagnostics.jsonl');
    assert.equal(await fs.readFile(await file.path(), 'utf8'), snapshot);
    await download.waitFor({ state: 'hidden' });
    await page.waitForFunction(() => {
      const trigger = document.querySelector('#diagnostics-export-menu button');
      return document.activeElement === trigger && trigger.getAttribute('aria-expanded') === 'false';
    });

    if (pt) {
      console.log(`local first open to usable log ${firstReady} ms, ${Buffer.byteLength(snapshot)} bytes, ${JSON.stringify(loadTiming)}`);
      const firstRect = await records.boundingBox();
      const copyRect = await copy.boundingBox();
      const range = viewer.getByRole('combobox', { name: 'Período', exact: true });
      const selectRange = async name => {
        await range.click();
        await viewer.getByRole('option', { name, exact: true }).click();
      };
      let release;
      let delayed;
      const waiting = new Promise(resolve => { delayed = resolve; });
      const intercept = async route => {
        const hours = new URL(route.request().url()).searchParams.get('hours');
        if (hours === '1') {
          await new Promise(resolve => {
            release = resolve;
            delayed();
          });
          await route.fulfill({ contentType: 'application/x-ndjson', body: 'unexpected-old-response\n' }).catch(() => {});
        } else if (hours === '168') {
          await route.fulfill({ status: 503 });
        } else {
          await route.continue();
        }
      };
      await page.route('**' + logPath + '?*', intercept);
      await selectRange('Última hora');
      await waiting;
      await page.evaluate(async () => {
        const records = document.querySelector('[data-diagnostics-records]');
        const copy = document.querySelector('#diagnostics-copy');
        const start = [records.getBoundingClientRect(), copy.getBoundingClientRect()];
        for (let i = 0; i < 5; i++) {
          await new Promise(resolve => requestAnimationFrame(resolve));
          for (const [index, element] of [records, copy].entries()) {
            if (Math.abs(element.getBoundingClientRect().y - start[index].y) > 1) throw new Error('diagnostics controls shifted');
          }
        }
      });
      assert.deepEqual(await records.boundingBox(), firstRect);
      assert.deepEqual(await copy.boundingBox(), copyRect);
      assert(await copy.isDisabled());
      assert(await more.isDisabled());
      await selectRange('Últimos 7 dias');
      await viewer.getByText('Não foi possível carregar o registro.', { exact: true }).waitFor();
      release();
      assert.equal(await records.textContent(), '');
      assert(await more.isDisabled());
      await page.unroute('**' + logPath + '?*', intercept);
      await refresh.click();
      await readyEntry.waitFor();
      assert.equal(await viewer.locator('[data-diagnostics-hours]').inputValue(), '168');
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    }
    await page.keyboard.press('Escape');
    await viewer.waitFor({ state: 'hidden' });
    assert.equal(await page.locator('[data-diagnostics-records]').textContent(), '');
    assert.deepEqual(errors, []);
    assert(!server.stderr().includes(key));
  } finally {
    await context.close();
    await server.stop();
  }
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  try {
    await run(browser, 'local');
    await run(browser, 'hosted');
    console.log('PASS: local and hosted diagnostics, access, loading, retry, clipboard, identical download, and close');
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
