const assert = require('node:assert/strict');
const {randomUUID} = require('node:crypto');
const {readFileSync} = require('node:fs');
const {join} = require('node:path');

async function attachmentComposition({base, api, until, alicePage, bobPage, alice, bob, aliceConversation, bobConversation, expectTransportFailure, prefix}) {
  const pageURL = (conversation, account, lang, theme) =>
    base + '/?' + new URLSearchParams({chat:conversation.id, account:account.id, lang, theme});
  const image = {
    name: 'planta-do-estudio.png', mimeType: 'image/png',
    buffer: readFileSync(join(__dirname, 'fixtures', 'video-poster.png')),
  };
  const file = {name:'notas.txt', mimeType:'text/plain', buffer:Buffer.from('Notes')};
  const longFile = {...file, name:'roteiro-da-sessao-de-gravacao-e-notas-de-producao.txt'};
  const stagePath = '**/ui/scheduling/stage?*';
  const requestPaths = [];
  const track = request => {
    const url = new URL(request.url());
    if (['/ui/scheduling/stage', '/ui/messages/send'].includes(url.pathname)) requestPaths.push(url.pathname);
  };
  const dataEvent = (page, kind, files, text, size) => page.evaluate(({kind, files, text, size}) => {
    const target = document.querySelector(kind === 'paste' ? '#message-composer' : '.oie-chat-pane');
    const data = new DataTransfer();
    if (text) data.setData('text/plain', text);
    for (const file of files) {
      const bytes = size ? new Uint8Array(size) : Uint8Array.from(file.bytes);
      data.items.add(new File([bytes], file.name, {type:file.mimeType}));
    }
    const options = {bubbles:true, cancelable:true};
    const event = kind === 'paste' ? new ClipboardEvent('paste', {...options, clipboardData:data}) :
      new DragEvent(kind, {...options, dataTransfer:data});
    target.dispatchEvent(event);
    return event.defaultPrevented;
  }, {kind, files:files.map(f => ({name:f.name, mimeType:f.mimeType, bytes:[...f.buffer]})), text, size});
  const sample = page => page.evaluate(() => {
    const elements = ['#message-timeline','#message-composer','#composer-split'].map(id => document.querySelector(id));
    const measure = () => elements.flatMap(node => {
      const {x,y,width,height} = node.getBoundingClientRect();
      return [x,y,width,height,node.scrollTop];
    });
    const baseline = measure();
    const frames = [];
    window.attachmentFrames = frames;
    const end = performance.now() + 650;
    requestAnimationFrame(function frame(now) {
      frames.push(measure());
      if (now < end) requestAnimationFrame(frame);
    });
    return baseline;
  });

  for (const [page, conversation, account, lang, width, theme] of [
    [alicePage, aliceConversation, alice, 'en', 1440, 'light'],
    [bobPage, bobConversation, bob, 'pt-BR', 390, 'dark'],
  ]) {
    const pt = lang === 'pt-BR';
    await page.setViewportSize({width, height:844});
    await page.goto(pageURL(conversation, account, lang, theme));
    const composer = page.locator('#message-composer');
    const draftFiles = page.locator('[data-composer-attachments]');
    const remove = (name) => draftFiles.getByRole('button', {name:(pt ? 'Remover anexo' : 'Remove attachment') + ' · ' + name, exact:true});
    const original = pt ? 'Rascunho da conversa' : 'Independent text draft';
    const send = page.locator('#send-message-form [data-send-message]');
    const attach = page.locator('[data-attach-menu]');
    await page.locator('body[data-oie-live-ready]').waitFor();
    await until(() => attach.isEnabled(), 'file upload capability is available in current conversation');
    await composer.fill(original);
    await composer.evaluate(node => node.setSelectionRange(2, 5));
    page.on('request', track);

    for (const [label, accept] of [[pt ? 'Imagem' : 'Image', 'image/*'], [pt ? 'Arquivo' : 'File', '']]) {
      await attach.click();
      const chooser = page.waitForEvent('filechooser');
      await page.getByRole('menuitem', {name:label, exact:true}).click();
      assert.equal(await (await chooser).element().getAttribute('accept'), accept);
    }
    await attach.click();
    await page.getByRole('menuitem', {name:pt ? 'Localização' : 'Location', exact:true}).click();
    const location = page.locator('#oie-location-draft');
    const latitude = location.locator('[name="latitude"]');
    const longitude = location.locator('[name="longitude"]');
    if (pt) {
      await page.context().grantPermissions(['geolocation']);
      await page.context().setGeolocation({latitude:-23.55, longitude:-46.63});
      await location.getByRole('button', {name:'Usar localização atual'}).click();
      await until(async () => Number(await latitude.inputValue()) === -23.55, 'browser coordinates populate latitude');
      assert.equal(Number(await longitude.inputValue()), -46.63);
    } else {
      await latitude.fill('-23.55');
      await longitude.fill('-46.63');
    }
    const position = 'geo:-23.55,-46.63';
    await location.getByRole('button', {name:pt ? 'Adicionar ao rascunho' : 'Add to draft', exact:true}).click();
    await location.waitFor({state:'hidden'});
    assert.equal(await composer.inputValue(), original + '\n' + position);
    await page.reload();
    await until(async () => await composer.inputValue() === original + '\n' + position, 'coordinates remain in draft after reload');
    assert.equal(await page.locator('[data-draft-link-preview]').isVisible(), false);
    assert.equal(requestPaths.length, 0, 'location does not send or stage an attachment');
    await composer.fill(original);

    assert.equal(await dataEvent(page, 'paste', [], 'ordinary pasted text'), false);
    assert.equal(await draftFiles.isVisible(), false);
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.evaluate(() => navigator.clipboard.writeText('text '));
    await composer.evaluate(node => node.setSelectionRange(0, 0));
    await page.keyboard.press('Control+v');
    await until(async () => await composer.inputValue() === 'text ' + original, 'ordinary paste inserts text');
    await composer.fill(original);
    await composer.evaluate(node => node.setSelectionRange(2, 5));
    assert.equal(await dataEvent(page, 'drop', [file], '', 20*1024*1024+1), true);
    const toast = page.locator('[data-toast-container] [role="alert"]').filter({hasText:pt ? '16 arquivos de 20 MiB' : '16 files of 20 MiB'});
    await toast.getByRole('button', {name:pt ? 'Fechar' : 'Close'}).click();
    await toast.waitFor({state:'hidden'});
    assert.equal(await draftFiles.isVisible(), false);
    assert.equal(await composer.inputValue(), original);

    const retained = () => api('/api/v1/conversations/' + conversation.id + '/messages?limit=100');
    const messagesBefore = (await retained()).items.length;
    const operation = await page.locator('#send-message-form [name="operationKey"]').inputValue();
    let unblockStage;
    const delayedStage = new Promise(resolve => { unblockStage = resolve; });
    await page.route(stagePath, async route => {
      await delayedStage;
      await route.continue().catch(error => { if (pt) throw error; });
    }, {times:1});
    const before = await sample(page);
    if (pt) {
      const pane = page.locator('.oie-chat-pane');
      assert.equal(await dataEvent(page, 'dragenter', [image]), true);
      assert.equal(await pane.getAttribute('data-file-drag'), '');
      assert.equal(await dataEvent(page, 'drop', [image]), true);
      assert.equal(await pane.getAttribute('data-file-drag'), null);
    } else {
      assert.equal(await dataEvent(page, 'paste', [image]), true);
    }
    await draftFiles.getByText(pt ? 'Preparando anexos…' : 'Preparing attachments…', {exact:true}).first().waitFor();
    assert.equal(await page.locator('dialog[open]').count(), 0);
    assert(await send.isDisabled());
    assert.equal(await composer.inputValue(), original);
    assert.deepEqual(await composer.evaluate(el => [document.activeElement === el, el.selectionStart, el.selectionEnd]), [true,2,5]);
    await until(() => requestPaths.length === 1, 'file staging request reaches the browser');
    assert.deepEqual(requestPaths, ['/ui/scheduling/stage']);
    assert.equal((await retained()).items.length, messagesBefore);
    const revised = pt ? 'Legenda para envio ' + randomUUID() : 'Caption to keep';
    if (!pt) {
      await composer.fill(revised);
      await remove(image.name).click();
      unblockStage();
      await draftFiles.waitFor({state:'hidden'});
      assert.equal(await composer.inputValue(), revised);
      await until(async () => await page.evaluate(() => window.attachmentFrames.length > 10), 'painted frames were captured');
      const frames = await page.evaluate(() => window.attachmentFrames);
      for (const frame of frames) assert(frame.every((value,i) => Math.abs(value-before[i]) <= 1), JSON.stringify({frame,before}));
      assert.deepEqual(requestPaths.splice(0), ['/ui/scheduling/stage']);
      if (expectTransportFailure) expectTransportFailure(true);
      try {
        let lostURL;
        await page.route(stagePath, async route => {
          lostURL = route.request().url();
          const response = await route.fetch();
          assert.equal(response.status(), 200);
          await route.abort('failed');
        }, {times:1});
        const failed = draftFiles.locator('[data-file-state]').getByText('Could not attach this file.', {exact:true});
        assert.equal(await dataEvent(page, 'paste', [image]), true);
        await failed.waitFor();
        assert.equal(await composer.inputValue(), revised);
        const [replayed] = await Promise.all([
          page.waitForRequest(request => request.url() === lostURL),
          draftFiles.getByRole('button', {name:'Try again · ' + image.name, exact:true}).click(),
        ]);
        assert.equal((await replayed.response()).status(), 200);
        await draftFiles.getByRole('link',{name:image.name}).waitFor();
        assert.deepEqual(requestPaths.splice(0), ['/ui/scheduling/stage','/ui/scheduling/stage']);
        await page.reload();
        await draftFiles.getByRole('link',{name:image.name}).waitFor();
        assert.equal(await composer.inputValue(), revised);
        await draftFiles.screenshot({path:'artifacts/' + prefix + '-attachment-desktop-en.png'});
        await remove(image.name).click();
        await composer.fill('');
      } finally {
        if (expectTransportFailure) expectTransportFailure(false);
      }
    } else {
      await composer.fill(revised);
      unblockStage();
      await draftFiles.getByRole('link',{name:image.name,exact:true}).waitFor();
      assert.equal(await composer.inputValue(), revised, 'staging preserves text typed during the request');
      assert.deepEqual(requestPaths, ['/ui/scheduling/stage']);
      assert.equal((await retained()).items.length, messagesBefore);
      await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
      const violations = await page.evaluate(async () =>
        (await window.axe.run('[data-composer-attachments]', {runOnly:{type:'tag', values:['wcag2a','wcag2aa','wcag21aa']}})).violations.map(v => v.id));
      assert.deepEqual(violations, []);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.locator('[data-attachment-picker]').setInputFiles([file, longFile]);
      await draftFiles.getByRole('link', {name:longFile.name, exact:true}).waitFor();
      await draftFiles.getByRole('link', {name:file.name, exact:true}).waitFor();
      assert.deepEqual(await draftFiles.getByRole('link').allTextContents(), [image.name, file.name, longFile.name]);
      const layout = () => draftFiles.evaluate(region => {
        const list = region.querySelector('[data-attachment-items]');
        const auxiliary = region.closest('.oie-composer-auxiliary');
        return {
          rows: new Set([...list.children].map(row => row.offsetTop)).size,
          overflow: list.scrollWidth > list.clientWidth || auxiliary.scrollWidth > auxiliary.clientWidth ||
            document.documentElement.scrollWidth > innerWidth,
        };
      });
      assert.deepEqual(await layout(), {rows:3, overflow:false});
      await composer.fill('Materiais para a sessão de amanhã');
      const draftRect = await draftFiles.boundingBox();
      const formRect = await page.locator('#send-message-form').boundingBox();
      await page.screenshot({
        path:'artifacts/' + prefix + '-caption-mobile-pt.png',
        clip:{x:formRect.x, y:draftRect.y, width:formRect.width, height:formRect.y + formRect.height - draftRect.y},
      });
      await composer.fill(revised);
      await page.setViewportSize({width:1440, height:844});
      await until(async () => (await layout()).rows === 1, 'all three attachments fit on a wide screen');
      assert.deepEqual(await layout(), {rows:1, overflow:false});
      await page.setViewportSize({width,height:844});
      await remove(file.name).click();
      await remove(longFile.name).click();
      assert.deepEqual(requestPaths, Array(3).fill('/ui/scheduling/stage'));
      let releaseSend;
      const holdSend = new Promise(resolve => { releaseSend = resolve; });
      await page.route('**/ui/messages/send', async route => {
        await holdSend;
        await route.continue();
      }, {times:1});
      try {
        await send.click();
        const bubble = page.locator('[data-pending-send][data-pending-state="sending"]');
        const pendingFile = bubble.getByRole('link', {name:image.name, exact:true});
        await pendingFile.waitFor();
        assert.equal(await bubble.locator('[data-pending-send-text]').textContent(), revised);
        assert.equal(await pendingFile.getAttribute('download'), image.name);
        await until(() => pendingFile.locator('img').evaluate(img => img.complete && img.naturalWidth > 0), 'pending attachment preview is decoded');
        assert.equal((await retained()).items.length, messagesBefore, 'pending attachment is visible before acceptance');
      } finally {
        releaseSend();
      }
      await until(async () => (await retained()).items.some(message =>
        message.originalText.startsWith(revised) && message.sendState === 'submitted'), 'attachment with caption is sent through XMPP');
      const sent = (await retained()).items.filter(message => message.originalText.startsWith(revised));
      assert.equal(sent.length, 1);
      assert.equal(sent[0].attachments[0].fileName, image.name);
      assert.equal((await api('/api/v1/operations/' + operation)).resourceId, sent[0].id);
      assert.deepEqual(requestPaths.splice(0), [...Array(3).fill('/ui/scheduling/stage'), '/ui/messages/send']);
      await until(async () => (await api('/api/v1/conversations/' + aliceConversation.id + '/messages?limit=100')).items.some(message =>
        message.originalText.startsWith(revised) && message.originalText.includes('https://')), 'receiver retains caption and uploaded URL');
    }
    page.off('request', track);
  }
}

module.exports = {attachmentComposition};
