// Exercise the real viewer, access check and exports on an isolated Oiê.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const { once } = require('node:events');
const { chromium } = require('playwright');

const logPath = '/ui/diagnostics/log';
const key = 'test-owner-key-sentinel';
const widths = { local: 390, hosted: 1440 };

async function ready(base) {
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      if ((await fetch(base + '/api/v1/health')).ok) return;
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error('Oiê did not become ready');
}

async function run(browser, mode) {
  const data = await fs.mkdtemp(path.join(os.tmpdir(), 'oie-diagnostics-'));
  const socket = http.createServer();
  socket.listen(0, '127.0.0.1');
  await once(socket, 'listening');
  const listen = '127.0.0.1:' + socket.address().port;
  await new Promise(resolve => socket.close(resolve));
  const base = 'http://' + listen;
  const child = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--role', mode, '--listen', listen, '--data-dir', data], {
      env: { ...process.env, OIE_DIAGNOSTICS_KEY: mode === 'hosted' ? key : '' },
      stdio: ['ignore', 'ignore', 'pipe'],
    });
  const childExit = once(child, 'exit');
  let stderr = '';
  child.stderr.setEncoding('utf8').on('data', value => { stderr += value; });
  const context = await browser.newContext({
    viewport: { width: widths[mode], height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'],
    reducedMotion: 'reduce',
  });
  try {
    await ready(base);
    const log = path.join(data, 'diagnostics', 'oie-diagnostics.jsonl');
    const fixture = JSON.parse((await fs.readFile(log, 'utf8')).trim().split('\n').at(-1));
    const history = Array.from({ length: 300 }, (_, index) => JSON.stringify({
      ...fixture, event: 'upstream_http_error', component: 'presentation', level: 'warn',
      duration_ms: index + 1, http_status: 503,
    }));
    history.push('{"message":"browser-private-message-sentinel","token":"browser-private-token-sentinel"}');
    await fs.appendFile(log, history.join('\n') + '\n');
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    let requests = 0;
    page.on('request', request => {
      if ([logPath, '/ui/diagnostics.js'].includes(new URL(request.url()).pathname)) requests++;
    });
    const pt = mode === 'local';
    await page.goto(base + '/?lang=' + (pt ? 'pt-BR' : 'en'));
    assert.equal(requests, 0, 'initial workspace must not read the diagnostics file');
    if (pt) await page.getByRole('button', { name: 'Abrir navegação', exact: true }).click();
    await page.locator('[data-navigation-settings]:visible').click();
    const settings = page.getByRole('dialog', { name: pt ? 'Configurações' : 'Settings', exact: true });
    await settings.waitFor();
    assert.equal(requests, 0, 'opening Settings must not read the diagnostics file');
    const started = Date.now();
    await settings.getByRole('button', { name: pt ? 'Diagnóstico' : 'Diagnostics', exact: true }).click();
    const viewer = page.getByRole('dialog', { name: pt ? 'Diagnóstico' : 'Diagnostics', exact: true });
    const records = viewer.locator('[data-diagnostics-records]');
    const copy = viewer.getByRole('button', { name: pt ? 'Copiar' : 'Copy', exact: true });
    const download = viewer.getByRole('button', { name: pt ? 'Baixar' : 'Download', exact: true });
    await viewer.waitFor();
    if (!pt) {
      const input = viewer.getByLabel('Diagnostics access key', { exact: true });
      await input.waitFor();
      await input.fill('wrong-owner-key-sentinel');
      await Promise.all([
        page.waitForResponse(response => response.url().includes(logPath) && response.status() === 403),
        input.press('Enter'),
      ]);
      assert.equal(await records.textContent(), '');
      assert.equal(await copy.isDisabled(), true);
      await input.fill(key);
      await input.press('Enter');
    }
    await page.waitForFunction(() => document.querySelector('[data-diagnostics-records]').textContent.includes('"event":"ready"'));
    const firstReady = Date.now() - started;
    const snapshot = await records.textContent();
    assert(!snapshot.includes(key));
    assert(!snapshot.includes('browser-private'));
    assert.equal(snapshot.split('\n').filter(line => line.includes('upstream_http_error')).length, 300);
    assert(await records.evaluate(node => node.scrollHeight > node.clientHeight));
    assert(snapshot.length <= 256000);
    assert(snapshot.endsWith('\n'));
    await copy.click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), snapshot);
    const downloaded = page.waitForEvent('download');
    await download.click();
    const file = await downloaded;
    assert.equal(file.suggestedFilename(), 'oie-diagnostics.jsonl');
    assert.equal(await fs.readFile(await file.path(), 'utf8'), snapshot);

    if (pt) {
      console.log(`local first open to usable log ${firstReady} ms, ${Buffer.byteLength(snapshot)} bytes`);
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
        const copy = document.querySelector('[data-diagnostics-copy]');
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
      await selectRange('Últimos 7 dias');
      await viewer.getByText('Não foi possível carregar o registro.', { exact: true }).waitFor();
      release();
      assert.equal(await records.textContent(), '');
      await page.unroute('**' + logPath + '?*', intercept);
      await viewer.getByRole('button', { name: 'Atualizar', exact: true }).click();
      await page.waitForFunction(() => document.querySelector('[data-diagnostics-records]').textContent.includes('"event":"ready"'));
      assert.equal(await viewer.locator('[data-diagnostics-hours]').inputValue(), '168');
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    }
    await page.keyboard.press('Escape');
    await viewer.waitFor({ state: 'hidden' });
    assert.equal(await page.locator('[data-diagnostics-records]').textContent(), '');
    assert.deepEqual(errors, []);
    assert(!stderr.includes(key));
  } finally {
    await context.close();
    child.kill('SIGTERM');
    await childExit;
    await fs.rm(data, { recursive: true, force: true });
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
