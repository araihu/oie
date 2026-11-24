const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');

async function verifyMediaIntegrity(browser, href) {
  const page = await browser.newPage({locale:'en'});
  try {
    await page.goto(href + '&lang=en');
    const dir = path.join(__dirname, 'fixtures');
    const video = await fs.readFile(path.join(dir, 'covered-video.mp4'));
    const mp3 = await fs.readFile(path.join(dir, 'covered-tone.mp3'));
    const covers = await fs.readFile(path.join(dir, 'labeled-covers.mp3'));
    const alpha = await fs.readFile(path.join(dir, 'transparent.webm'));
    const partial = video.subarray(0, Math.floor(video.length / 2));
    const openEnded = Buffer.from(partial);
    const mdat = openEnded.indexOf('mdat');
    assert(mdat > 4);
    openEnded.writeUInt32BE(0, mdat - 4);
    const cases = [
      {name:'transparent.webm', mime:'video/webm', bytes:alpha, reason:'transparent'},
      {name:'short.mp4', mime:'video/mp4', bytes:partial, reason:'damaged'},
      {name:'open-ended.mp4', mime:'video/mp4', bytes:openEnded, reason:'damaged'},
      {name:'recording', mime:'application/octet-stream', bytes:mp3, output:'recording.mp3'},
      {name:'recording.wav', mime:'audio/wav', bytes:mp3, output:'recording.mp3'},
      {name:'covers.mp3', mime:'audio/mpeg', bytes:covers, reason:'coverLabels', output:'covers.mp3'},
      {name:'covered.mp4', mime:'video/mp4', bytes:video, output:'covered.mp4'},
      {name:'covered.mp4', mime:'video/mp4', bytes:video, output:'covered.mp4', quality:'SD'},
    ];
    const dialog = page.locator('#media-quality-dialog');
    const composer = page.locator('#message-composer');
    await composer.fill('Keep this caption');
    let staged = 0;
    page.on('request', req => { if (new URL(req.url()).pathname === '/ui/scheduling/stage') staged++; });
    for (const c of cases) {
      await page.locator('#composer-split [data-popover-trigger] button').click();
      const picking = page.waitForEvent('filechooser');
      await page.locator('#composer-attach-media').click();
      await (await picking).setFiles({name:c.name, mimeType:c.mime, buffer:c.bytes});
      const submit = dialog.locator('[data-prepare-media]');
      const before = staged;
      if (!c.output) {
        await dialog.locator('[data-media-as-file]:visible').waitFor();
        assert(await submit.isHidden(), c.name);
        assert.equal(await dialog.locator('input[type=radio]:enabled').count(), 0, c.name);
      } else {
        await dialog.locator('[data-prepare-media]:enabled').waitFor();
        assert.equal(await dialog.locator('[value=SD]').isEnabled(), !c.reason, c.name);
        assert.equal(await dialog.locator('[value=HD]').isEnabled(), !c.reason, c.name);
        assert(await dialog.locator('[value=OG]').isEnabled(), c.name);
      }
      if (c.reason) {
        const label = await dialog.locator('[data-transparent]').getAttribute(
          'data-' + (c.reason === 'coverLabels' ? 'cover-labels' : c.reason));
        assert.equal(await dialog.locator('[role=status]').textContent(), label, c.name);
      }
      if (c.output) {
        await dialog.locator('[value=' + (c.quality || 'OG') + ']').check();
        const staging = page.waitForResponse(r => new URL(r.url()).pathname === '/ui/scheduling/stage');
        await submit.click();
        const response = await staging;
        assert(response.ok());
        const receipt = await response.json();
        const bytes = await (await page.request.get(response.url().replace('/stage?', '/file?'))).body();
        assert.equal(receipt.fileName, c.output);
        assert.equal(receipt.mediaType, c.output.endsWith('mp4') ? 'video/mp4' : 'audio/mpeg');
        if (c.quality === 'SD') {
          assert(bytes.length <= c.bytes.length * 0.9, 'covered video was compressed');
          const png = video.indexOf(Buffer.from('89504e470d0a1a0a', 'hex'));
          const end = video.indexOf('IEND', png);
          assert(png >= 0 && end > png && bytes.includes(video.subarray(png, end + 8)));
        } else assert.deepEqual(bytes, c.bytes);
        await page.locator('[data-remove-attachment]').click();
      } else {
        await dialog.press('Escape');
        await dialog.waitFor({state:'hidden'});
        assert.equal(staged, before, 'cancel did not stage ' + c.name);
      }
      assert.equal(await composer.inputValue(), 'Keep this caption');
    }
  } finally {
    await page.close();
  }
}

async function outgoingMedia(browser, href, state, sends) {
  const fixtures = path.join(__dirname, 'fixtures');
  const cases = [
    ['en', 1000, 'alternate-audio.mkv', 'SD'],
    ['pt-BR', 390, 'alternate-audio.mkv', 'HD'],
    ['en', 1000, 'styled-subtitles.mkv', 'file'],
    ['pt-BR', 390, 'covered-tone.mp3', 'SD'],
    ['en', 1000, 'covered-tone.mp3', 'HD'],
    ['pt-BR', 390, 'covered-tone.mp3', 'OG'],
  ];
  let compatible;
  for (const [locale, width, name, quality] of cases) {
    const page = await browser.newPage({locale, viewport:{width, height:844}});
    try {
      const requests = [];
      page.on('request', req => requests.push(new URL(req.url()).pathname));
      await page.goto(href + '&lang=' + locale);
      const composer = page.locator('#message-composer');
      const dialog = page.getByRole('dialog',{name:locale === 'en' ? 'Media quality' : 'Qualidade da mídia',exact:true});
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
      const mp3 = name === 'covered-tone.mp3';
      const wasm = '**/media-encoder/*/ffmpeg-core.wasm';
      let releaseCodec;
      const gate = new Promise(resolve => { releaseCodec = resolve; });
      await page.route(wasm, async route => { await gate; await route.continue(); }, {times:1});
      await select(file);
      try {
        await dialog.waitFor();
        assert(await submit.isDisabled());
        const centered = await dialog.evaluate(node => {
          const r = node.getBoundingClientRect();
          return Math.abs(r.x + r.width / 2 - innerWidth / 2) <= 1 &&
            Math.abs(r.y + r.height / 2 - innerHeight / 2) <= 1 && node.scrollWidth <= node.clientWidth;
        });
        assert(centered, 'media quality dialog is centered and fits the viewport');
        const shift = await page.evaluate(async () => {
          const nodes = ['#message-composer', '#composer-split', '#media-quality-dialog'].map(s => document.querySelector(s));
          const bounds = () => nodes.flatMap(n => {
            const r = n.getBoundingClientRect();
            return [r.x, r.y, r.width, r.height];
          });
          const initial = bounds();
          let max = 0;
          for (let i = 0; i < 6; i++) {
            await new Promise(requestAnimationFrame);
            bounds().forEach((value, index) => { max = Math.max(max, Math.abs(value - initial[index])); });
          }
          return max;
        });
        assert(shift <= 1, 'composer and dialog stay in place while codecs load');
      } finally {
        releaseCodec();
      }
      if (quality === 'file') {
        await dialog.locator('[data-media-as-file]:visible').waitFor();
        assert(await submit.isHidden());
      } else {
        await dialog.locator('[data-prepare-media]:enabled').waitFor();
        assert(await dialog.locator('[value=SD]').isChecked());
        assert.equal(await dialog.locator('[value=OG]').isEnabled(), mp3);
        await dialog.locator('[value=' + quality + ']').check();
      }
      const staged = page.waitForResponse(r => r.url().includes('/ui/scheduling/stage?'));
      await (quality === 'file' ? dialog.locator('[data-media-as-file]') : submit).click();
      const response = await staged;
      assert(response.ok());
      const receipt = await response.json();
      const bytes = await (await page.request.get(response.url().replace('/stage?', '/file?'))).body();
      assert.equal(bytes.length, receipt.size);
      if (mp3) {
        const source = await fs.readFile(file);
        if (quality === 'SD') {
          assert.equal(receipt.fileName, 'covered-tone.m4a');
          assert.equal(receipt.mediaType, 'audio/mp4');
          assert(bytes.length <= source.length * 0.9);
          const png = source.indexOf(Buffer.from('89504e470d0a1a0a','hex'));
          const end = source.indexOf('IEND', png);
          assert(png >= 0 && end > png && bytes.includes(source.subarray(png, end + 8)), 'preserves the complete PNG cover');
          for (const tag of ['Fixture tone', 'Fixture artist', 'fixture_note', 'Keep this custom tag']) assert(bytes.includes(tag));
        } else {
          assert.equal(receipt.fileName, name);
          assert.equal(receipt.mediaType, 'audio/mpeg');
          assert.deepEqual(bytes, source);
        }
        await page.locator('[data-remove-attachment]').click();
        continue;
      }
      if (quality === 'file') assert.deepEqual(bytes, await fs.readFile(file));
      else {
        assert.equal(receipt.fileName, 'alternate-audio.mp4');
        assert.equal(receipt.mediaType, 'video/mp4');
        assert.equal(bytes.toString('ascii',4,8),'ftyp');
        compatible = {name:receipt.fileName,mimeType:receipt.mediaType,buffer:bytes};
      }
      assert.equal(await composer.inputValue(), 'Fixture caption');
      assert(!requests.includes('/ui/messages/send'));
      if (quality === 'SD') {
        await page.locator('#composer-split [data-popover-trigger] button').click();
        await page.locator('#composer-schedule-new').click();
        const review = page.locator('[data-schedule-dialog][open]');
        await review.getByText(receipt.fileName, {exact:true}).waitFor();
        const when = new Date(Date.now() + 2 * 86400000).toISOString().slice(0, 16);
        await review.locator('#schedule-local-time').fill(when);
        const action = await page.locator('#send-message-form').evaluate(form => form.action);
        const submitted = page.waitForResponse(r => r.request().method() === 'POST' && r.url() === action);
        await review.getByRole('button', {name:'Schedule message',exact:true}).click();
        await submitted;
        assert.deepEqual(sends.at(-1).attachmentIds, [receipt.id]);
        assert(Date.parse(sends.at(-1).scheduledAt) > Date.now());
        continue;
      }
      await page.locator('[data-remove-attachment]').click();
      if (quality !== 'HD') continue;

      // Check the advertised OG cutoff and cancellation against the same MP4.
      state.uploadMaxBytes = compatible.buffer.length - 1;
      await page.reload();
      await select(compatible);
      await dialog.locator('[data-prepare-media]:enabled').waitFor();
      assert(await dialog.locator('[value=OG]').isDisabled());
      await dialog.getByRole('button',{name:'Fechar',exact:true}).click();
      await page.waitForFunction(() => !document.querySelector('#send-message-form').hasAttribute('data-attachments-pending'));
      assert.equal(await composer.inputValue(), 'Fixture caption');
      state.uploadMaxBytes = compatible.buffer.length;
      await page.reload();
      await select([compatible, {...compatible, name:'second.mp4'}]);
      await dialog.locator('[value=OG]').check();
      const original = page.waitForResponse(r => r.url().includes('/ui/scheduling/stage?'));
      await submit.click();
      const accepted = await original;
      assert.deepEqual(await (await page.request.get(accepted.url().replace('/stage?', '/file?'))).body(), compatible.buffer);
      await dialog.getByText('second.mp4', {exact:true}).waitFor();
      await dialog.press('Escape');
      await dialog.waitFor({state:'hidden'});
      assert.equal(await composer.inputValue(), 'Fixture caption');
    } finally {
      delete state.uploadMaxBytes;
      await page.close();
    }
  }
  await verifyMediaIntegrity(browser, href);
}
module.exports = {outgoingMedia};
