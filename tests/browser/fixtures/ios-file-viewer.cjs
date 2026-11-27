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
    filenames, resume, fail:false, reads:0,
    serve(pathname, response) {
      if (!pathname.startsWith(prefix)) return false;
      fixture.reads++;
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

async function checkColdFileTap(context, url, fixture, changeRoute) {
  const page = await context.newPage();
  let release;
  const ready = new Promise(resolve => { release = resolve; });
  try {
    await page.route('**/ui/documents.js', async route => {
      await ready;
      await route.continue();
    });
    await page.goto(url, {waitUntil:'commit'});
    const links = page.locator('a[download][href^="/ui/omemo-attachments/"]');
    await links.first().waitFor({state:'visible'});
    await page.evaluate(() => {
      window.fileTaps = [];
      document.addEventListener('click', event => {
        if (event.target.closest?.('a[download]')) window.fileTaps.push(event.defaultPrevented);
      });
    });
    const reads = fixture.reads;
    await links.first().tap();
    // The synthetic download link is detached immediately after the click.
    await links.nth(1).evaluate(link => window.OieRuntime.saveFile(link.href, link.download, link));
    assert.deepEqual(await page.evaluate(() => window.fileTaps), [true, true]);
    assert.equal(fixture.reads, reads);
    if (changeRoute) await page.evaluate(() => history.replaceState(null, '', '/?inbox=1'));
    release();
    await page.waitForLoadState('domcontentloaded');
    const dialog = page.locator('#oie-file-viewer');
    if (changeRoute) {
      assert.equal(await dialog.count(), 0);
      assert.equal(fixture.reads, reads);
    } else {
      await dialog.getByRole('button', {name:'Fechar'}).waitFor();
      assert.equal(await dialog.locator('h2').textContent(), fixture.filenames[1]);
      await dialog.getByRole('button', {name:'Fechar'}).click();
      assert.equal(page.url(), url);
    }
  } finally {
    release();
    await page.close();
  }
}

async function checkIOSFileViewer(browser, url, fixture) {
  const context = await browser.newContext({...devices['iPhone 13'], serviceWorkers:'block'});
  const page = await context.newPage();
  try {
    await checkColdFileTap(context, url, fixture, false);
    await checkColdFileTap(context, url, fixture, true);
    await page.addInitScript(() => {
      const shared = [];
      window.sharedFiles = shared;
      Object.defineProperties(navigator, {
        canShare: {value: ({files}) => files?.length === 1 && files[0] instanceof File},
        share: {value: async ({files:[file]}) => {
          shared.push({name:file.name, bytes:Array.from(new Uint8Array(await file.arrayBuffer()))});
          if (shared.length === 1) throw new DOMException('Cancelled', 'AbortError');
          if (shared.length === 2) throw new DOMException('Denied', 'NotAllowedError');
        }},
      });
    });
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
        const share = dialog.getByRole('button', {name:'Compartilhar arquivo'});
        const feedback = dialog.locator('[data-file-share-feedback]');
        const save = dialog.locator('[data-file-save]');
        const original = readFileSync(path.join(__dirname, 'video-poster.png'));
        for (let attempt = 1; attempt <= 3; attempt++) {
          await share.click();
          await page.waitForFunction(count => window.sharedFiles.length === count, attempt);
          if (attempt === 2) {
            await page.waitForFunction(() => {
              const viewer = document.querySelector('#oie-file-viewer');
              const feedback = viewer.querySelector('[data-file-share-feedback]');
              const rect = feedback.getBoundingClientRect();
              return feedback.textContent === viewer.dataset.failed && rect.height > 0 &&
                document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2) === feedback;
            });
            const reads = fixture.reads;
            fixture.fail = true;
            try {
              const [download] = await Promise.all([page.waitForEvent('download'), save.click()]);
              assert.equal(await download.failure(), null);
              assert.equal(download.suggestedFilename(), filename);
              assert.deepEqual(readFileSync(await download.path()), original);
              assert.equal(fixture.reads, reads, 'Save reuses the bytes that were opened');
              assert(await dialog.isVisible());
            } finally {
              fixture.fail = false;
            }
          } else {
            assert.equal(await feedback.textContent(), '');
            if (attempt === 1) assert(await save.isHidden());
          }
        }
        const expected = {name:filename, bytes:[...original]};
        assert.deepEqual(await page.evaluate(() => window.sharedFiles), [expected, expected, expected]);
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
