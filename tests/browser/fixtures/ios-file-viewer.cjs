const assert = require('node:assert/strict');
const path = require('node:path');
const {readFileSync} = require('node:fs');
const {devices} = require('playwright');

function iosAttachmentFixture(messageID) {
  const filenames = ['image.png', 'film.webm', 'archive.zip'];
  const files = ['video-poster.png', 'tiny-video.webm', 'tiny-document.txt'];
  const prefix = '/api/v1/messages/' + messageID + '/omemo-attachments/';
  let resume;
  const pending = new Promise(resolve => { resume = resolve; });
  const fixture = {
    filenames, resume, fail:false,
    serve(pathname, response) {
      if (!pathname.startsWith(prefix)) return false;
      const index = Number(pathname.slice(prefix.length));
      if (fixture.fail || !files[index]) {
        response.writeHead(404);
        response.end();
        return true;
      }
      const bytes = readFileSync(path.join(__dirname, files[index]));
      response.writeHead(200, {
        'Content-Type':'application/octet-stream', 'Content-Length':bytes.length,
        'Content-Disposition':'attachment; filename="' + filenames[index] + '"',
      });
      response.flushHeaders();
      pending.then(() => { if (!response.destroyed) response.end(bytes); });
      return true;
    },
  };
  return fixture;
}

async function checkIOSFileViewer(browser, url, fixture) {
  const context = await browser.newContext({...devices['iPhone 13']});
  const page = await context.newPage();
  try {
    await page.goto(url);
    const links = page.locator('a[download][href^="/ui/omemo-attachments/"]');
    const dialog = page.locator('#oie-file-viewer');
    const close = dialog.getByRole('button', {name:'Fechar'});
    const timeline = page.locator('#message-timeline');
    const composer = page.locator('#message-composer');
    const first = links.first();
    await composer.fill('Rascunho');
    await first.scrollIntoViewIfNeeded();
    const scroll = await timeline.evaluate(node => node.scrollTop);
    const bounds = await composer.boundingBox();
    await Promise.all([page.waitForRequest(request => request.url().includes('/ui/omemo-attachments/') && request.resourceType() === 'fetch'), first.click()]);
    await close.waitFor();
    assert.deepEqual(await composer.boundingBox(), bounds);
    await close.click();
    fixture.resume();
    for (const [index, filename] of fixture.filenames.entries()) {
      await links.nth(index).click();
      if (index === 0) {
        await page.waitForFunction(() => {
          const img = document.querySelector('#oie-file-viewer img');
          return img?.naturalWidth && img.getBoundingClientRect().width > 0;
        });
      } else if (index === 1) {
        await page.waitForFunction(() => {
          const video = document.querySelector('#oie-file-viewer video');
          return video?.readyState >= 1 && video.controls && video.playsInline;
        });
      } else {
        await page.waitForFunction(() => document.querySelector('#oie-file-viewer [role="status"]').textContent === 'Prévia indisponível.');
      }
      assert.equal(await dialog.locator('h2').textContent(), filename);
      await close.click();
      await dialog.waitFor({state:'hidden'});
      assert.equal(await links.nth(index).evaluate(node => node === document.activeElement), true);
      assert.equal(page.url(), url);
      assert.equal(await composer.inputValue(), 'Rascunho');
      assert.equal(await timeline.evaluate(node => node.scrollTop), scroll);
    }
    fixture.fail = true;
    await first.click();
    await page.waitForFunction(() => {
      const viewer = document.querySelector('#oie-file-viewer');
      return viewer.querySelector('[role="status"]').textContent === viewer.dataset.failed;
    });
    await close.click();
    assert.equal(await composer.inputValue(), 'Rascunho');
    assert.equal(await context.pages().length, 1);
  } finally {
    fixture.resume();
    await context.close();
  }
}

module.exports = {checkIOSFileViewer, iosAttachmentFixture};
