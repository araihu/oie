const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {chromium} = require('playwright');

const script = fs.readFileSync(path.resolve(__dirname, '../../internal/adapters/web/actions.js'), 'utf8');
const first = 'https://one.example.test';
const second = 'https://two.example.test';

(async () => {
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage();
    const requests = [];
    await page.route('http://desktop.test/', route => route.fulfill({
      contentType: 'text/html',
      body: `
        <form id="engine-mode-form" action="/ui/engine-mode" method="post">
          <input type="radio" name="mode" value="remote" checked>
          <input type="url" name="url" value="${first}">
          <input type="password" name="apiKey" data-saved-placeholder="">
          <input type="hidden" name="apiKeyAddress">
          <span data-engine-connecting></span>
          <button type="submit">Save</button>
        </form>
        <script>window.OieRuntime = {request: fetch.bind(window), register() {}};</script>
        <script src="/ui/actions.js"></script>`,
    }));
    await page.route('http://desktop.test/ui/actions.js', route => route.fulfill({contentType: 'text/javascript', body: script}));
    await page.route('http://desktop.test/ui/engine-mode', route => {
      requests.push(new URLSearchParams(route.request().postData()));
      return route.fulfill({status: 401, contentType: 'text/plain', body: 'Invalid key'});
    });

    await page.goto('http://desktop.test/');
    const address = page.locator('[name=url]');
    const key = page.locator('[name=apiKey]');
    const save = async () => {
      await Promise.all([
        page.waitForResponse(response => response.url().endsWith('/ui/engine-mode')),
        page.getByRole('button', {name: 'Save'}).click(),
      ]);
      await page.waitForFunction(() => !document.getElementById('engine-mode-form').hasAttribute('aria-busy'));
      return requests.at(-1);
    };

    await key.fill('first-fixture-key');
    const firstRequest = await save();
    assert.equal(firstRequest.get('apiKeyAddress'), first);
    assert.equal(firstRequest.get('apiKey'), 'first-fixture-key');

    await address.fill(second);
    assert.equal(await key.inputValue(), '');
    assert.equal((await save()).get('apiKey'), '');

    await key.fill('second-fixture-key');
    const secondRequest = await save();
    assert.equal(secondRequest.get('apiKeyAddress'), second);
    assert.equal(secondRequest.get('apiKey'), 'second-fixture-key');

    await address.evaluate((node, value) => { node.value = value; }, first);
    const staleRequest = await save();
    assert.equal(staleRequest.get('url'), first);
    assert.equal(staleRequest.get('apiKey'), '');
    assert.equal(staleRequest.get('apiKeyAddress'), '');

    for (const type of ['drawer:close', 'drawer:close-request', 'pagehide']) {
      await key.fill('abandoned-fixture-key');
      await page.evaluate(eventType => {
        window.dispatchEvent(new CustomEvent(eventType, {detail: {id: 'engine-settings'}}));
      }, type);
      assert.equal(await key.inputValue(), '');
      const request = await save();
      assert.equal(request.get('apiKey'), '');
      assert.equal(request.get('apiKeyAddress'), '');
    }
    console.log('PASS: editing the engine address or leaving settings discards the drafted key');
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
