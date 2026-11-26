const assert = require('node:assert/strict');
const path = require('node:path');
const {devices} = require('playwright');

async function checkIOSFileViewer(browser, url) {
  const context = await browser.newContext({...devices['iPhone 13']});
  let release;
  const pending = new Promise(resolve => { release = resolve; });
  try {
    const page = await context.newPage();
    await page.goto(url);
    const links = page.locator('a[download][href^="/ui/omemo-attachments/"]');
    const dialog = page.locator('#oie-file-viewer');
    const close = dialog.getByRole('button', {name:'Fechar'});
    const timeline = page.locator('#message-timeline');
    const composer = page.locator('#message-composer');
    const filenames = ['image.png', 'film.mp4', 'archive.zip'];
    const sources = ['video-poster.png', 'covered-video.mp4', 'tiny-document.txt'];
    let fail = false;
    let hold = true;
    await page.route('**/ui/omemo-attachments/**', async route => {
      const index = Number(new URL(route.request().url()).pathname.split('/').pop());
      if (hold) await pending;
      await route.fulfill(fail ? {status:404} : {
        path:path.join(__dirname, sources[index]), contentType:'application/octet-stream',
        headers:{'Content-Disposition':'attachment; filename="' + filenames[index] + '"'},
      });
    });

    const first = links.first();
    await composer.fill('Rascunho');
    await first.scrollIntoViewIfNeeded();
    const scroll = await timeline.evaluate(node => node.scrollTop);
    const bounds = await composer.boundingBox();
    await Promise.all([page.waitForRequest(request => request.url().includes('/ui/omemo-attachments/') && request.resourceType() === 'fetch'), first.click()]);
    await close.waitFor();
    assert.deepEqual(await composer.boundingBox(), bounds);
    await close.click();
    hold = false;
    release();
    for (const [index, selector] of ['img', 'video', '[role="status"]'].entries()) {
      await links.nth(index).click();
      const content = dialog.locator(selector);
      if (index === 0) {
        await page.waitForFunction(() => {
          const img = document.querySelector('#oie-file-viewer img');
          return img?.naturalWidth && img.getBoundingClientRect().width > 0;
        });
      } else if (index === 1) {
        await content.waitFor();
        assert(await content.evaluate(video => video.controls && video.playsInline));
      } else {
        await page.waitForFunction(() => document.querySelector('#oie-file-viewer [role="status"]').textContent === 'Prévia indisponível.');
      }
      assert.equal(await dialog.locator('h2').textContent(), filenames[index]);
      await close.click();
      await dialog.waitFor({state:'hidden'});
      assert.equal(await links.nth(index).evaluate(node => node === document.activeElement), true);
      assert.equal(page.url(), url);
      assert.equal(await composer.inputValue(), 'Rascunho');
      assert.equal(await timeline.evaluate(node => node.scrollTop), scroll);
    }
    fail = true;
    await first.click();
    await page.waitForFunction(() => {
      const viewer = document.querySelector('#oie-file-viewer');
      return viewer.querySelector('[role="status"]').textContent === viewer.dataset.failed;
    });
    await close.click();
    assert.equal(await composer.inputValue(), 'Rascunho');
    assert.equal(await context.pages().length, 1);
  } finally {
    release();
    await context.close();
  }
}

module.exports = {checkIOSFileViewer};
