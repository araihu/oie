// Exercise removal against a disposable engine, including delayed/failed responses.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const {randomUUID} = require('node:crypto');
const {spawn} = require('node:child_process');
const {once} = require('node:events');
const {chromium} = require('playwright');

(async () => {
  const data = await fs.mkdtemp(path.join(os.tmpdir(), 'oie-removal-'));
  const artifacts = process.env.OIE_REMOVAL_ARTIFACTS || path.join(__dirname, 'artifacts');
  const port = await new Promise((resolve, reject) => {
    const socket = net.createServer();
    socket.once('error', reject);
    socket.listen(0, '127.0.0.1', () => {
      const assigned = socket.address().port;
      socket.close(() => resolve(assigned));
    });
  });
  const base = 'http://127.0.0.1:' + port;
  const child = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--role', 'local', '--listen', '127.0.0.1:' + port, '--data-dir', data], {stdio: ['ignore', 'ignore', 'pipe']});
  let stderr = '', browser;
  child.stderr.on('data', chunk => stderr += chunk);
  const statuses = [], errors = [];
  const key = () => {
    const random = randomUUID();
    const millis = Date.now().toString(16).padStart(12, '0');
    return millis.slice(0, 8) + '-' + millis.slice(8) + '-7' + random.slice(15);
  };
  const register = async (address, label) => {
    const response = await fetch(base + '/api/v1/accounts', {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'Idempotency-Key': key()},
      body: JSON.stringify({protocol: 'xmpp', address, label}),
    });
    assert.equal(response.status, 201);
    return (await response.json()).id;
  };
  const readAccount = account => fetch(base + '/api/v1/accounts/' + account);
  try {
    for (let i = 0; ; i++) {
      if (await fetch(base + '/api/v1/health').then(r => r.ok, () => false)) break;
      if (i > 100) throw Error(stderr || 'engine did not start');
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    const keep = await register('trabalho@exemplo.org', 'Trabalho');
    browser = await chromium.launch();
    await fs.mkdir(artifacts, {recursive: true});
    for (const width of [390, 1440]) for (const theme of ['goshtoso', 'minimal'])
    for (const mode of ['light', 'dark']) for (const locale of ['en', 'pt-BR']) {
      const context = await browser.newContext({viewport: {width, height: 880}, locale});
      const page = await context.newPage();
      page.on('pageerror', error => errors.push(error.message));
      const address = 'pessoal@exemplo.org';
      const remove = await register(address, 'Pessoal');
      const query = new URLSearchParams({open: 'accounts', managedAccount: remove, account: remove, lang: locale, theme: mode});
      const start = base + '/?' + query;
      await page.goto(start);
      await page.evaluate(value => document.documentElement.dataset.theme = value, theme);
      const link = page.locator('.oie-account-detail-footer [data-account-remove]');
      const title = page.locator('[data-account-remove-title]');
      const form = page.locator('#remove-account-form');
      const confirm = form.locator('button[type=submit]');
      const cancel = form.getByRole('link');
      let posts = 0;
      page.on('request', request => { if (request.url().endsWith('/ui/accounts/remove')) posts++; });
      const waitForDangerBackground = selector => page.waitForFunction(selector => {
        const control = document.querySelector(selector);
        if (!control) return false;
        const theme = document.documentElement.classList.contains('dark') ? '-dark' : '';
        // A link can still be transitioning when a drawer fragment first renders.
        const sample = document.createElement('span');
        sample.hidden = true;
        sample.style.backgroundColor = `var(--color-danger-action${theme})`;
        control.append(sample);
        const expected = getComputedStyle(sample).backgroundColor;
        sample.remove();
        return expected !== 'rgba(0, 0, 0, 0)' && getComputedStyle(control).backgroundColor === expected;
      }, selector);
      const open = async () => {
        await link.focus();
        await waitForDangerBackground('.oie-account-detail-footer [data-account-remove]');
        await page.keyboard.press('Enter');
        await title.waitFor();
        await waitForDangerBackground('#remove-account-form button[type=submit]');
        await page.waitForFunction(() => document.activeElement?.matches('[data-account-remove-title]'));
        assert.equal(await title.textContent(), locale === 'en' ? 'Remove account' : 'Remover conta');
        assert((await page.locator('.oie-account-intro').textContent()).includes(address));
      };
      await open();
      await cancel.click();
      await link.waitFor();
      await page.waitForFunction(() => document.activeElement?.matches('[data-account-remove]'));
      assert.equal(posts, 0);
      assert.equal((await readAccount(remove)).status, 200);
      await open();
      await page.addScriptTag({path: require.resolve('axe-core/axe.min.js')});
      const a11y = await page.evaluate(() => axe.run(document, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa']}}));
      assert.deepEqual(a11y.violations.map(v => ({id: v.id, targets: v.nodes.map(n => n.target)})), [], `${width}-${theme}-${mode}-${locale}`);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));

      if (width === 1440 && theme === 'goshtoso' && mode === 'light' && locale === 'pt-BR') {
        await page.screenshot({path: path.join(artifacts, 'remove-account-original.png')});
        await page.locator('#accounts-body').screenshot({path: path.join(artifacts, 'remove-account.png')});
      }

      let unblock;
      const held = new Promise(resolve => { unblock = resolve; });
      let intercept;
      const intercepted = new Promise(resolve => { intercept = resolve; });
      await page.route('**/ui/accounts/remove', async route => {
        intercept();
        await held;
        await route.abort('failed');
      }, {times: 1});
      await confirm.focus();
      await page.evaluate(() => {
        const watched = ['#remove-account-form button[type=submit]', '#accounts-body', '#conversation-pane'];
        const snapshot = () => watched.flatMap(selector => {
          const rect = document.querySelector(selector).getBoundingClientRect();
          return [rect.x, rect.y, rect.width, rect.height];
        });
        const original = snapshot();
        window.removalFrames = {max: 0, count: 0, active: true};
        const frame = () => {
          const result = window.removalFrames;
          if (!result.active) return;
          result.count++;
          result.max = Math.max(result.max, ...snapshot().map((n, index) => Math.abs(n - original[index])));
          requestAnimationFrame(frame);
        };
        requestAnimationFrame(frame);
      });
      await page.keyboard.press('Enter');
      await intercepted;
      assert.equal(await confirm.getAttribute('aria-disabled'), 'true');
      assert((await form.locator('[data-form-feedback]').textContent()).length > 0);
      await page.evaluate(() => document.getElementById('remove-account-form').requestSubmit());
      await page.waitForFunction(() => window.removalFrames.count >= 5);
      assert.equal(posts, 1, 'pending form sent a duplicate request');
      unblock();
      await page.getByText(locale === 'en' ? 'Couldn’t remove the account. Try again.' : 'Não foi possível remover a conta. Tente novamente.', {exact: true}).waitFor();
      await page.waitForFunction(() => !document.querySelector('#remove-account-form').hasAttribute('aria-busy'));
      const max = await page.evaluate(() => { window.removalFrames.active = false; return window.removalFrames.max; });
      assert(max <= 1, `pending or failed request moved the drawer by ${max}px`);
      assert(await confirm.evaluate(el => el === document.activeElement));
      assert.equal((await readAccount(remove)).status, 200);

      // Another tab viewing the same account should leave its stale detail.
      let other;
      if (locale === 'pt-BR' && mode === 'light' && theme === 'goshtoso') {
        other = await context.newPage();
        await other.goto(start);
        await other.locator('[data-account-remove]').waitFor({state: 'visible'});
        await other.waitForFunction(() => window.OieEvents?.connected === true);
      }
      await confirm.click();
      await page.locator('[data-account-add]').waitFor({state: 'visible'});
      const destination = new URL(page.url()).searchParams;
      assert.equal(destination.get('open'), 'accounts');
      assert.equal(destination.get('managedAccount'), null);
      assert.equal(destination.get('account'), null);
      assert.equal(await page.locator('[data-manage-account]').count(), 1);
      assert.equal(await page.locator('[data-manage-account]').getAttribute('data-manage-account'), keep);
      assert.equal((await readAccount(remove)).status, 404);
      assert.equal((await readAccount(keep)).status, 200);
      if (other) await other.locator('[data-account-add]').waitFor({state: 'visible'});
      assert.equal(posts, 2);
      statuses.push({width, theme, mode, locale, maxShift: max, axe: 0});
      await context.close();
    }
    assert.deepEqual(errors, []);
    await fs.writeFile(path.join(artifacts, 'account-removal-report.json'), JSON.stringify(statuses, null, 2));
    console.log('PASS: account removal, cancellation, refusal, retry, live reconciliation and stable geometry across 16 browser variants.');
  } finally {
    await browser?.close();
    if (child.exitCode === null) {
      child.kill('SIGTERM');
      await once(child, 'exit');
    }
    await fs.rm(data, {recursive: true, force: true});
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
