const assert = require('node:assert/strict');
const {randomUUID} = require('node:crypto');

async function attachmentComposition({base, api, until, alicePage, bobPage, alice, bob, aliceConversation, bobConversation, expectTransportFailure, prefix}) {
  const pageURL = (conversation, account, lang, theme) =>
    base + '/?' + new URLSearchParams({chat:conversation.id, account:account.id, lang, theme});
  const image = {
    name: 'planta-do-estudio.png', mimeType: 'image/png',
    buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z2gAAAABJRU5ErkJggg==', 'base64'),
  };
  const file = {name:'notas.txt', mimeType:'text/plain', buffer:Buffer.from('Notes')};
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
    const review = page.locator('#oie-attachment-review');
    const caption = review.locator('textarea');
    const original = pt ? 'Rascunho da conversa' : 'Independent text draft';
    const add = review.getByRole('button', {name:pt ? 'Adicionar ao rascunho' : 'Add to draft', exact:true});
    const send = review.getByRole('button', {name:pt ? 'Enviar mensagem' : 'Send message', exact:true});
    const attach = page.locator('[data-attach-file]');
    await page.locator('body[data-oie-live-ready]').waitFor();
    await until(() => attach.isEnabled(), 'file upload capability is available in current conversation');
    await composer.fill(original);
    await composer.evaluate(node => node.setSelectionRange(2, 5));
    page.on('request', track);

    assert.equal(await dataEvent(page, 'paste', [], 'ordinary pasted text'), false);
    assert.equal(await review.isVisible(), false);
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
    await page.evaluate(() => navigator.clipboard.writeText('text '));
    await composer.evaluate(node => node.setSelectionRange(0, 0));
    await page.keyboard.press('Control+v');
    await until(async () => await composer.inputValue() === 'text ' + original, 'ordinary paste inserts text');
    await composer.fill(original);
    await composer.evaluate(node => node.setSelectionRange(2, 5));
    assert.equal(await dataEvent(page, 'paste', [image, file]), true);
    await until(() => review.isVisible(), 'pasted files open a preview');
    await until(() => caption.evaluate(el => document.activeElement === el), 'caption receives focus on open');
    assert.equal(await caption.inputValue(), original);
    assert.equal(await review.locator('li').count(), 2);
    await until(() => review.locator('img').evaluate(img => img.complete && img.naturalWidth > 0), 'local image preview is decoded');
    assert.equal(requestPaths.length, 0, 'selecting files never stages or sends');
    await review.getByRole('button', {name:(pt ? 'Remover anexo' : 'Remove attachment') + ' · ' + file.name, exact:true}).click();
    assert.equal(await review.locator('li').count(), 1);
    await caption.fill('Discard this caption');
    await page.keyboard.press('Escape');
    await review.waitFor({state:'hidden'});
    assert.equal(await composer.inputValue(), original);
    assert.deepEqual(await composer.evaluate(el => [document.activeElement === el, el.selectionStart, el.selectionEnd]), [true,2,5]);
    assert.equal(requestPaths.length, 0, 'cancel and remove before confirmation have no network effect');

    assert.equal(await dataEvent(page, 'drop', [file], '', 20*1024*1024+1), true);
    const toast = page.locator('[data-toast-container] [role="alert"]').filter({hasText:pt ? '16 arquivos de 20 MiB' : '16 files of 20 MiB'});
    await toast.getByRole('button', {name:pt ? 'Fechar' : 'Close'}).click();
    await toast.waitFor({state:'hidden'});
    assert.equal(await review.isVisible(), false);
    assert.equal(await composer.inputValue(), original);

    const pane = page.locator('.oie-chat-pane');
    assert.equal(await dataEvent(page, 'dragenter', [image]), true);
    assert.equal(await pane.getAttribute('data-file-drag'), '');
    assert.equal(await dataEvent(page, 'drop', [image, file]), true);
    assert.equal(await pane.getAttribute('data-file-drag'), null);
    await review.waitFor({state:'visible'});
    const bounds = await review.evaluate(node => {
      const {left, right, top, bottom} = node.getBoundingClientRect();
      return {fits:left >= 15 && right <= innerWidth - 15 && top >= 0 && bottom <= innerHeight, overflow:document.documentElement.scrollWidth > innerWidth};
    });
    assert.deepEqual(bounds, {fits:true, overflow:false});
    await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
    const violations = await page.evaluate(async () =>
      (await window.axe.run('#oie-attachment-review', {runOnly:{type:'tag', values:['wcag2a','wcag2aa','wcag21aa']}})).violations.map(v => v.id));
    assert.deepEqual(violations, []);
    await review.screenshot({path:'artifacts/' + prefix + (pt ? '-attachment-mobile-pt.png' : '-attachment-desktop-en.png')});
    await review.getByRole('button', {name:(pt ? 'Remover anexo' : 'Remove attachment') + ' · ' + file.name, exact:true}).click();
    const revised = pt ? 'Legenda para envio ' + randomUUID() : 'Caption to keep';
    await caption.fill(revised);

    const holdStage = async route => {
      await new Promise(resolve => setTimeout(resolve, 550));
      await route.continue().catch(error => { if (pt) throw error; });
    };
    const operation = await page.locator('#send-message-form [name="operationKey"]').inputValue();
    const retained = () => api('/api/v1/conversations/' + conversation.id + '/messages?limit=100');
    const messagesBefore = (await retained()).items.length;
    const pending = page.waitForRequest(request => request.url().includes('/ui/scheduling/stage?'));
    await page.route(stagePath, holdStage, {times:1});
    const before = await sample(page);
    if (pt) await send.click(); else await add.click();
    await pending;
    await review.getByText(pt ? 'Preparando anexos…' : 'Preparing attachments…', {exact:true}).waitFor();
    assert(await send.isDisabled() && await add.isDisabled());
    assert.equal(await composer.inputValue(), original, 'staging does not edit the draft while waiting');
    if (!pt) {
      await page.keyboard.press('Escape');
      await review.waitFor({state:'hidden'});
      assert.equal(await composer.inputValue(), original);
      await until(async () => await page.evaluate(() => window.attachmentFrames.length > 10), 'painted frames were captured');
      const frames = await page.evaluate(() => window.attachmentFrames);
      for (const frame of frames) assert(frame.every((value,i) => Math.abs(value-before[i]) <= 1), JSON.stringify({frame,before}));
      assert.equal(await page.locator('[data-composer-attachments] a').count(), 0);
      assert.equal((await retained()).items.length, messagesBefore);
      assert.deepEqual(requestPaths.splice(0), ['/ui/scheduling/stage']);
      if (expectTransportFailure) expectTransportFailure(true);
      try {
        await dataEvent(page, 'paste', [image]);
        await caption.fill(revised);
        let lostURL;
        await page.route(stagePath, async route => {
          lostURL = route.request().url();
          const response = await route.fetch();
          assert.equal(response.status(), 200);
          await route.abort('failed');
        }, {times:1});
        await add.click();
        await review.getByText('Could not attach this file.', {exact:true}).waitFor();
        assert.equal(await caption.inputValue(), revised);
        assert.equal(await composer.inputValue(), original);
        const replayed = page.waitForResponse(response => response.url() === lostURL && response.ok());
        await add.click();
        await replayed;
        await review.waitFor({state:'hidden'});
        await page.locator('[data-composer-attachments]').getByRole('link',{name:image.name}).waitFor();
        assert.equal(await composer.inputValue(), revised);
        assert.deepEqual(requestPaths.splice(0), ['/ui/scheduling/stage','/ui/scheduling/stage']);
        await page.reload();
        await page.locator('[data-composer-attachments]').getByRole('link',{name:image.name}).waitFor();
        assert.equal(await composer.inputValue(), revised);
        await page.locator('[data-composer-attachments] [data-remove-attachment]').click();
        await composer.fill('');
      } finally {
        if (expectTransportFailure) expectTransportFailure(false);
      }
    } else {
      await review.waitFor({state:'hidden'});
      await until(async () => (await retained()).items.some(message =>
        message.originalText.startsWith(revised) && message.sendState === 'submitted'), 'attachment with caption is sent through XMPP');
      const sent = (await retained()).items.filter(message => message.originalText.startsWith(revised));
      assert.equal(sent.length, 1);
      assert.equal(sent[0].attachments[0].fileName, image.name);
      assert.equal((await api('/api/v1/operations/' + operation)).resourceId, sent[0].id);
      assert.deepEqual(requestPaths.splice(0), ['/ui/scheduling/stage','/ui/messages/send']);
      await until(async () => (await api('/api/v1/conversations/' + aliceConversation.id + '/messages?limit=100')).items.some(message =>
        message.originalText.startsWith(revised) && message.originalText.includes('https://')), 'receiver retains caption and uploaded URL');
    }
    page.off('request', track);
  }
}

module.exports = {attachmentComposition};
