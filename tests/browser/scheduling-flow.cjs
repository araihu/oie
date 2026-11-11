const assert = require('node:assert/strict');
const {randomUUID} = require('node:crypto');

async function schedulingFlow({base, api, until, alicePage, bobPage, alice, bob, aliceConversation, bobConversation}) {
  const source = '#message-composer';
  const preview = '[data-schedule-dialog]';
  const path = (conversation, account, lang) => base + '/?chat=' + conversation.id + '&account=' + account.id + '&lang=' + lang;
  const content = 'Scheduling review ' + randomUUID() + ' https://example.com/studio';
  const attachment = {
    name: 'reference.png',
    mimeType: 'image/png',
    buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z2gAAAABJRU5ErkJggg==', 'base64'),
  };
  const openMenu = async (page, label) => {
    await page.locator('#composer-split').getByRole('button', {name: label, exact: true}).click();
  };
  const sample = page => page.evaluate(() => {
    const selectors = ['#message-composer', '#message-timeline', '#composer-split'];
    const sampleRects = () => selectors.map(selector => {
      const node = document.querySelector(selector);
      const {x,y,width,height} = node.getBoundingClientRect();
      return [x,y,width,height,node.scrollTop];
    });
    const baseline = sampleRects();
    window.scheduleFrames = [];
    const end = performance.now() + 320;
    function frame(now) {
      window.scheduleFrames.push(sampleRects());
      if (now < end) requestAnimationFrame(frame);
    }
    requestAnimationFrame(frame);
    return baseline;
  });
  const stable = async (page, baseline) => {
    const frames = await page.evaluate(() => window.scheduleFrames);
    assert(frames.length > 2, 'sample painted frames while request is pending');
    for (const frame of frames) for (const [index, values] of frame.entries()) {
      assert(values.every((value, i) => Math.abs(value - baseline[index][i]) <= 1), JSON.stringify({frame,baseline}));
    }
  };

  await alicePage.goto(path(aliceConversation, alice, 'en'));
  await alicePage.locator('body[data-oie-live-ready]').waitFor();
  await alicePage.locator(source).fill(content);
  await alicePage.route('**/ui/scheduling/stage?*', async route => {
    await new Promise(resolve => setTimeout(resolve, 550));
    await route.continue();
  }, {times: 1});
  await alicePage.evaluate(async () => {
    await document.fonts.ready;
    const timeline = document.getElementById('message-timeline');
    timeline.scrollTop = timeline.scrollHeight;
    let previous, stable = 0;
    while (stable < 40) {
      await new Promise(requestAnimationFrame);
      const current = [timeline.clientHeight, timeline.scrollHeight, timeline.scrollTop].join(':');
      stable = current === previous ? stable + 1 : 0;
      previous = current;
    }
  });
  const beforeStage = await sample(alicePage);
  await alicePage.locator('[data-attachment-picker]').setInputFiles(attachment);
  await alicePage.getByRole('link', {name: attachment.name, exact: true}).waitFor();
  await stable(alicePage, beforeStage);
  await openMenu(alicePage, 'More options');
  await alicePage.getByRole('menuitem', {name: 'Schedule message'}).click();
  const dialog = alicePage.locator(preview);
  await dialog.waitFor({state: 'visible'});
  assert.equal(await dialog.locator('[data-schedule-preview-text]').textContent(), content);
  assert.equal(await dialog.locator('img').getAttribute('alt'), attachment.name);
  await dialog.getByRole('button', {name: 'Back', exact: true}).click();
  assert.equal(await alicePage.locator(source).inputValue(), content, 'closing preview preserves composition');
  await openMenu(alicePage, 'More options');
  await alicePage.getByRole('menuitem', {name: 'Schedule message'}).click();
  await dialog.waitFor({state: 'visible'});
  const submitted = alicePage.waitForResponse(response => response.url().endsWith('/ui/messages/send') && response.request().method() === 'POST');
  await dialog.getByRole('button', {name: 'Schedule message', exact: true}).click();
  assert.equal((await (await submitted).json()).ok, true);
  await until(async () => await alicePage.locator(source).inputValue() === '', 'scheduled acceptance clears the submitted composition');
  await alicePage.locator(source).fill('Existing independent draft');

  await bobPage.addInitScript(() => localStorage.setItem('oie.device.time-zone.v1', 'Europe/Lisbon'));
  await bobPage.setViewportSize({width: 390, height: 844});
  await bobPage.goto(path(bobConversation, bob, 'pt-BR'));
  await bobPage.locator(source).fill('Mensagem agendada em outro destino');
  await openMenu(bobPage, 'Mais opções');
  await bobPage.getByRole('menuitem', {name: 'Agendar mensagem'}).click();
  await bobPage.locator(preview).waitFor({state: 'visible'});
  assert.equal(await bobPage.locator('[data-schedule-zone]').textContent(), 'Europe/Lisbon');
  assert(await bobPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'scheduling dialog fits narrow viewport');
  const bobSubmit = bobPage.waitForResponse(response => response.url().endsWith('/ui/messages/send') && response.request().method() === 'POST');
  await bobPage.locator(preview).getByRole('button', {name: 'Agendar mensagem', exact: true}).click();
  assert.equal((await (await bobSubmit).json()).ok, true);

  await alicePage.locator('#desktop-navigation').getByRole('link', {name: 'Scheduled', exact: true}).click();
  await alicePage.locator('[data-scheduled-row]').first().waitFor();
  assert.equal(await alicePage.locator('[data-scheduled-row]').count(), 2);
  await alicePage.locator('#schedule-conversation').selectOption(aliceConversation.id);
  await alicePage.getByRole('button', {name: 'Filter', exact: true}).click();
  await alicePage.waitForURL(url => url.searchParams.get('scheduleConversation') === aliceConversation.id);
  const row = alicePage.locator('[data-scheduled-row]').filter({hasText: content});
  const previous = await row.locator('time').getAttribute('datetime');
  await row.getByRole('button', {name: 'Reschedule', exact: true}).click();
  const reschedule = alicePage.locator(preview);
  await reschedule.waitFor({state:'visible'});
  const field = reschedule.locator('input[type="datetime-local"]');
  const [date, time] = (await field.inputValue()).split('T');
  const utc = new Date(date+'T'+time+'Z');
  utc.setUTCHours(utc.getUTCHours()+2);
  await field.fill(utc.toISOString().slice(0,16));
  const updated = alicePage.waitForResponse(response => response.url().endsWith('/ui/scheduling/update') && response.request().method() === 'POST');
  await reschedule.getByRole('button', {name:'Reschedule',exact:true}).click();
  assert.equal((await updated).ok(), true);
  await until(async () => {
    const current = await row.locator('time').getAttribute('datetime').catch(() => '');
    return current && current !== previous;
  }, 'new date appears on the same scheduled message');
  await row.getByRole('link', {name: 'Edit message', exact: true}).click();
  await alicePage.locator('[data-editing-schedule]').waitFor();
  assert.equal(await alicePage.locator(source).inputValue(), content);
  assert.equal(await alicePage.locator('[data-composer-attachments] a').textContent(), attachment.name);
  const edit = content + ' updated';
  await alicePage.locator(source).fill(edit);
  await alicePage.locator('#composer-send').click();
  await alicePage.waitForURL(url => url.searchParams.get('chat') === aliceConversation.id && !url.searchParams.has('editSchedule'));
  await until(async () => await alicePage.locator(source).inputValue() === 'Existing independent draft', 'restored pre-existing draft');

  await alicePage.locator('#desktop-navigation').getByRole('link', {name: 'Scheduled', exact: true}).click();
  const edited = alicePage.locator('[data-scheduled-row]').filter({hasText: edit});
  await edited.getByRole('button', {name: 'Send now', exact: true}).click();
  await until(async () => {
    const page = await api('/api/v1/conversations/' + bobConversation.id + '/messages?limit=50');
    return page.items.some(message => message.originalText.startsWith(edit + '\nhttps://'));
  }, 'same scheduled composition delivered through XMPP after Send now');
  await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 1, 'Send now clears exactly one row');
  await alicePage.locator('[data-scheduled-row]').filter({hasText: 'Mensagem agendada em outro destino'}).getByRole('button', {name: 'Cancel scheduled message', exact: true}).click();
  await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 0, 'second schedule cancelled');
}

module.exports = {schedulingFlow};
