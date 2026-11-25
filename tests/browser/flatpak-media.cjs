// Exercise the real desktop bridge and composer with a fixture Documents portal.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');

module.exports = async function flatpakMedia(browser, href) {
  const page = await browser.newPage();
  try {
    const mp3 = await fs.readFile(path.join(__dirname, 'fixtures/covered-tone.mp3'));
    const bridge = await fs.readFile(path.join(__dirname, '../../internal/adapters/desktop/native.js'), 'utf8');
    await page.addInitScript(({base64, size}) => {
      window.portalCalls = [];
      let generation = 0;
      const bytes = atob(base64);
      window.fixtureRuntime = {Call: {ByName: async (name, ...args) => {
        const method = name.split('.').at(-1);
        window.portalCalls.push(method);
        if (method === 'SelectMedia') return {id:String(++generation), files:[{name:'tone.mp3', type:'audio/mpeg', size}]};
        if (args[0] !== String(generation)) throw Error('wrong portal handle');
        if (method === 'ReleaseMedia') return;
        if (method === 'ReadMediaChunk') {
          if (args[1] !== 0) throw Error('wrong file');
          if (window.delayPortalRead) await new Promise(resolve => { window.resumePortalRead = resolve; });
          return btoa(bytes.slice(args[2], args[2] + 10000));
        }
        throw Error('unexpected desktop call ' + method);
      }}};
    }, {base64:mp3.toString('base64'), size:mp3.length});
    await page.goto(href + '&lang=en');
    await page.addScriptTag({content:bridge.replace("await import('/wails/runtime.js')", 'window.fixtureRuntime')
      .replaceAll("'__OIE_FLATPAK__'", 'true')});
    const composer = page.locator('#message-composer');
    const dialog = page.locator('#media-quality-dialog');
    await composer.fill('Portal draft');
    let stages = 0;
    page.on('request', req => { if (new URL(req.url()).pathname === '/ui/scheduling/stage') stages++; });
    async function pick() {
      const attach = page.locator('#composer-attach-media');
      if (!await attach.isVisible()) await page.locator('#composer-split [data-popover-trigger] button').click();
      await attach.click();
    }
    // A draft change while the bridge is reading revokes the selection.
    await page.evaluate(() => { window.delayPortalRead = true; });
    await pick();
    await page.waitForFunction(() => typeof window.resumePortalRead === 'function');
    await page.evaluate(() => {
      document.querySelector('#send-message-form').dispatchEvent(new Event('oie:attachment-draft-changing'));
      window.delayPortalRead = false;
      window.resumePortalRead();
    });
    await page.waitForFunction(() => window.portalCalls.at(-1) === 'ReleaseMedia');
    assert(await dialog.isHidden());
    assert.equal(stages, 0);
    for (const cancel of [true, false]) {
      await pick();
      await dialog.locator('[data-prepare-media]:enabled').waitFor();
      assert.equal(stages, 0, 'original stayed on device');
      if (cancel) {
        await dialog.press('Escape');
        await dialog.waitFor({state:'hidden'});
        assert.equal(await composer.inputValue(), 'Portal draft');
        continue;
      }
      await dialog.locator('[value=OG]').check();
      const stage = page.waitForResponse(res => new URL(res.url()).pathname === '/ui/scheduling/stage');
      await dialog.locator('[data-prepare-media]').click();
      const response = await stage;
      assert(response.ok());
      assert.deepEqual(await (await page.request.get(response.url().replace('/stage?', '/file?'))).body(), mp3);
      assert.equal(stages, 1);
      await page.locator('[data-remove-attachment]').click();
    }
    const selection = ['SelectMedia', 'ReadMediaChunk', 'ReadMediaChunk', 'ReadMediaChunk', 'ReleaseMedia'];
    assert.deepEqual(await page.evaluate(() => window.portalCalls), [
      'SelectMedia', 'ReadMediaChunk', 'ReleaseMedia', ...selection, ...selection,
    ]);
  } finally {
    await page.close();
  }
};
