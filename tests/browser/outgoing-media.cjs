const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');

async function outgoingMedia(browser, href, state) {
  const fixtures = path.join(__dirname, 'fixtures');
  const cases = [
    ['en', 1000, 'alternate-audio.mkv', 'SD'],
    ['pt-BR', 390, 'alternate-audio.mkv', 'HD'],
    ['en', 1000, 'styled-subtitles.mkv', 'file'],
  ];
  let compatible;
  for (const [locale, width, name, quality] of cases) {
    const page = await browser.newPage({locale, viewport:{width, height:844}});
    try {
      const requests = [];
      page.on('request', req => requests.push(new URL(req.url()).pathname));
      await page.goto(href + '&lang=' + locale);
      const composer = page.locator('#message-composer');
      const dialog = page.locator('[data-media-choice]');
      const submit = dialog.locator('[data-prepare-media]');
      await composer.fill('Fixture caption');
      assert(!requests.some(url => url.includes('/media-encoder/')));
      async function select(file) {
        await page.locator('#composer-split [data-popover-trigger] button').click();
        const picking = page.waitForEvent('filechooser');
        await page.locator('#composer-attach-media').click();
        await (await picking).setFiles(file);
      }
      const file = path.join(fixtures, name);
      await select(file);
      if (quality === 'file') await dialog.locator('[data-media-as-file]:visible').waitFor();
      else {
        await dialog.locator('[data-prepare-media]:enabled').waitFor();
        assert(await dialog.locator('[value=SD]').isChecked());
        assert(await dialog.locator('[value=OG]').isDisabled());
        await dialog.locator('[value=' + quality + ']').check();
      }
      const staged = page.waitForResponse(r => r.url().includes('/ui/scheduling/stage?'));
      await (quality === 'file' ? dialog.locator('[data-media-as-file]') : submit).click();
      const response = await staged;
      assert(response.ok());
      const receipt = await response.json();
      const bytes = await (await page.request.get(response.url().replace('/stage?', '/file?'))).body();
      assert.equal(bytes.length, receipt.size);
      if (quality === 'file') assert.deepEqual(bytes, await fs.readFile(file));
      else {
        assert.equal(receipt.fileName, 'alternate-audio.mp4');
        assert.equal(receipt.mediaType, 'video/mp4');
        assert.equal(bytes.toString('ascii',4,8),'ftyp');
        compatible = {name:receipt.fileName,mimeType:receipt.mediaType,buffer:bytes};
      }
      assert.equal(await composer.inputValue(), 'Fixture caption');
      assert(!requests.includes('/ui/messages/send'));
      await page.locator('[data-remove-attachment]').click();
      if (quality !== 'HD') continue;

      // Check the advertised OG cutoff and cancellation against the same MP4.
      state.uploadMaxBytes = compatible.buffer.length - 1;
      await page.reload();
      await select(compatible);
      await dialog.locator('[data-prepare-media]:enabled').waitFor();
      assert(await dialog.locator('[value=OG]').isDisabled());
      await dialog.locator('[data-media-cancel]').click();
      await page.waitForFunction(() => !document.querySelector('#send-message-form').hasAttribute('data-attachments-pending'));
      assert.equal(await composer.inputValue(), 'Fixture caption');
      state.uploadMaxBytes = compatible.buffer.length;
      await page.reload();
      await select(compatible);
      await dialog.locator('[value=OG]').check();
      const original = page.waitForResponse(r => r.url().includes('/ui/scheduling/stage?'));
      await submit.click();
      const accepted = await original;
      assert.deepEqual(await (await page.request.get(accepted.url().replace('/stage?', '/file?'))).body(), compatible.buffer);
    } finally {
      delete state.uploadMaxBytes;
      await page.close();
    }
  }
}
module.exports = {outgoingMedia};
