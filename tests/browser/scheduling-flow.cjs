const assert = require('node:assert/strict');
const {randomUUID} = require('node:crypto');

async function schedulingFlow({base, api, until, alicePage, bobPage, alice, bob, aliceConversation, bobConversation}) {
  const source = '#message-composer';
  const preview = '[data-schedule-dialog]';
  const path = (conversation, account, lang) => base + '/?chat=' + conversation.id + '&account=' + account.id + '&lang=' + lang;
  const planned = new Date(Date.now() + 2 * 86400000);
  planned.setUTCHours(14, 0, 0, 0);
  const fillPlannedTime = async page => {
    const zone = await page.evaluate(() => window.OieTime.timeZone());
    const parts = new Intl.DateTimeFormat('sv-SE', {timeZone: zone, dateStyle: 'short', timeStyle: 'short'}).format(planned);
    await page.locator('#schedule-local-time').fill(parts.replace(' ', 'T'));
  };
  const content = 'Scheduling review ' + randomUUID() + ' https://example.com/studio';
  const attachment = {
    name: 'reference.png',
    mimeType: 'image/png',
    buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Z2gAAAABJRU5ErkJggg==', 'base64'),
  };
  const openMenu = async (page, label) => {
    await page.locator('#composer-split').getByRole('button', {name: label, exact: true}).click();
  };
  const agendaAction = async (row, label) => {
    await row.locator('[data-schedule-actions-trigger]').click();
    await row.getByRole('menuitem', {name: label, exact: true}).click();
  };
  const openDrawer = async (page, label) => {
    const trigger = page.locator('#chat-options [aria-controls="chat-options-panel"]');
    await trigger.click();
    await page.locator('#chat-options-panel').getByRole('menuitem', {name: label, exact: true}).click();
    const drawer = page.getByRole('dialog', {name: label, exact: true});
    await drawer.waitFor({state:'visible'});
    assert.equal(await trigger.getAttribute('aria-expanded'), 'false');
    return drawer;
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
  const draftURL = 'https://example.com/studio';
  await alicePage.route('**/ui/link-preview', async route => {
    assert.deepEqual(route.request().postDataJSON(), {url: draftURL});
    await route.fulfill({json: {url: draftURL, title: 'Studio directions'}});
  }, {times: 1});
  await alicePage.locator(source).fill(content);
  const draftPreview = alicePage.locator('[data-draft-link-preview]');
  await draftPreview.getByRole('link', {name: 'Studio directions'}).waitFor();
  await draftPreview.getByRole('button', {name: 'Dismiss link preview'}).click();
  assert.equal(await alicePage.locator(source).inputValue(), content);
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
  await fillPlannedTime(alicePage);
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
  await fillPlannedTime(bobPage);
  const bobSubmit = bobPage.waitForResponse(response => response.url().endsWith('/ui/messages/send') && response.request().method() === 'POST');
  await bobPage.locator(preview).getByRole('button', {name: 'Agendar mensagem', exact: true}).click();
  assert.equal((await (await bobSubmit).json()).ok, true);

  for (const [page, conversation, label, message, otherMessage] of [
    [alicePage, aliceConversation, 'Scheduled', content, 'Mensagem agendada em outro destino'],
    [bobPage, bobConversation, 'Agendadas', 'Mensagem agendada em outro destino', content],
  ]) {
    const before = page.url();
    const drawer = await openDrawer(page, label);
    const row = drawer.locator('[data-scheduled-row]');
    await row.getByText(message, {exact:true}).waitFor();
    assert.equal(await row.count(), 1);
    assert.equal(await drawer.getByText(otherMessage, {exact:true}).count(), 0);
    const agenda = drawer.getByRole('link', {name: label === 'Scheduled' ? 'View in agenda' : 'Ver na agenda', exact:true});
    const url = new URL(await agenda.getAttribute('href'), base);
    assert.equal(url.searchParams.get('scheduleConversation'), conversation.id);
    assert.equal(url.searchParams.get('section'), 'scheduled');
    if (page === alicePage) {
      assert.equal(await row.getByRole('link', {name: attachment.name, exact:true}).count(), 1);
      assert.equal(await page.locator(source).inputValue(), 'Existing independent draft');
    }
    await page.keyboard.press('Escape');
    await drawer.waitFor({state:'hidden'});
    assert.equal(page.url(), before);
  }

  await alicePage.locator('#desktop-navigation').getByRole('link', {name: 'Scheduled', exact: true}).click();
  await alicePage.locator('[data-scheduled-row]').first().waitFor();
  assert.equal(await alicePage.locator('[data-scheduled-row]').count(), 2);
  assert.equal(await alicePage.locator('[data-scheduled-day]:visible').count(), 1, 'one heading for schedules on the same local day');
  const filter = alicePage.locator('#schedule-conversation');
  await filter.getByRole('combobox').click();
  const search = filter.getByRole('textbox', {name: 'Search conversations'});
  await search.fill(aliceConversation.title);
  const firstOption = filter.locator('[role="option"][data-value="' + aliceConversation.id + '"]');
  await firstOption.click();
  await alicePage.waitForURL(url => url.searchParams.get('scheduleConversation') === aliceConversation.id);
  assert.equal(await search.inputValue(), aliceConversation.title);
  assert(await firstOption.locator('input[type="checkbox"]').isChecked());
  assert.equal(await alicePage.locator('[data-scheduled-row]').count(), 1);
  await search.fill('');
  const secondOption = filter.locator('[role="option"][data-value="' + bobConversation.id + '"]');
  await secondOption.click();
  await alicePage.waitForURL(url => url.searchParams.getAll('scheduleConversation').length === 2);
  assert(await secondOption.locator('input[type="checkbox"]').isChecked());
  assert.equal(await alicePage.locator('[data-scheduled-row]').count(), 2);
  await secondOption.click();
  await alicePage.waitForURL(url => url.searchParams.getAll('scheduleConversation').length === 1);
  await alicePage.keyboard.press('Escape');
  const row = alicePage.locator('[data-scheduled-row]').filter({hasText: content});
  const menuTrigger = row.locator('[data-schedule-actions-trigger]');
  await menuTrigger.press('Enter');
  await row.getByRole('menuitem', {name: 'Reschedule', exact: true}).waitFor();
  await alicePage.keyboard.press('Escape');
  await row.getByRole('menu').waitFor({state:'hidden'});
  const reschedule = alicePage.locator(preview);
  await agendaAction(row, 'Reschedule');
  await reschedule.waitFor({state:'visible'});
  await reschedule.getByRole('button', {name:'Back',exact:true}).click();
  await until(() => menuTrigger.evaluate(node => node === document.activeElement), 'agenda restores focus after closing the scheduling dialog');

  await alicePage.goto(path(aliceConversation, alice, 'en'));
  const aliceDrawer = await openDrawer(alicePage, 'Scheduled');
  const drawerRow = aliceDrawer.locator('[data-scheduled-row]').filter({hasText:content});
  const scheduledTime = drawerRow.locator('[data-scheduled-date]');
  const previous = await scheduledTime.getAttribute('datetime');
  const chatURL = alicePage.url();
  const rescheduleButton = drawerRow.getByRole('button', {name:'Reschedule',exact:true});
  await rescheduleButton.click();
  await reschedule.waitFor({state:'visible'});
  await reschedule.getByRole('link', {name:attachment.name,exact:true}).waitFor();
  const field = reschedule.locator('input[type="datetime-local"]');
  const [date, time] = (await field.inputValue()).split('T');
  const utc = new Date(date+'T'+time+'Z');
  utc.setUTCHours(utc.getUTCHours()+2);
  await field.fill(utc.toISOString().slice(0,16));
  const updated = alicePage.waitForResponse(response => response.url().endsWith('/ui/scheduling/update') && response.request().method() === 'POST');
  await reschedule.getByRole('button', {name:'Reschedule',exact:true}).click();
  assert.equal((await (await updated).json()).ok, true);
  await until(async () => {
    const current = await scheduledTime.getAttribute('datetime').catch(() => '');
    return current && current !== previous;
  }, 'new date appears in the same drawer');
  await until(() => rescheduleButton.evaluate(node => node === document.activeElement), 'drawer restores focus after rescheduling');
  assert.equal(alicePage.url(), chatURL);

  const editLink = drawerRow.getByRole('link', {name:'Edit message',exact:true});
  const editRequest = url => url.pathname === '/' && url.searchParams.has('editSchedule');
  await alicePage.route(editRequest, async route => {
    const noEdit = new URL(route.request().url());
    noEdit.searchParams.delete('editSchedule');
    await new Promise(resolve => setTimeout(resolve, 450));
    await route.fulfill({response: await route.fetch({url:noEdit.href})});
  }, {times:1});
  await editLink.click();
  const beforeRefusal = await sample(alicePage);
  await alicePage.getByText('This message or conversation is no longer available. Your draft is kept.', {exact:true}).waitFor();
  await stable(alicePage, beforeRefusal);
  assert(await aliceDrawer.isVisible());
  assert.equal(await alicePage.locator('[data-editing-schedule]').count(), 0);
  assert.equal(await alicePage.locator(source).inputValue(), 'Existing independent draft');
  assert(await editLink.evaluate(node => node === document.activeElement));
  assert.equal(alicePage.url(), chatURL);

  await alicePage.route(editRequest, async route => {
    await new Promise(resolve => setTimeout(resolve, 500));
    await route.continue();
  }, {times:1});
  await editLink.click();
  const beforeEdit = await sample(alicePage);
  await new Promise(resolve => setTimeout(resolve, 330));
  assert(await aliceDrawer.isVisible());
  await stable(alicePage, beforeEdit);
  await alicePage.locator('[data-editing-schedule]').waitFor();
  await aliceDrawer.waitFor({state:'hidden'});
  await until(() => alicePage.locator(source).evaluate(node => node === document.activeElement), 'editing focuses the composer');
  assert.equal(await alicePage.locator(source).inputValue(), content);
  assert.equal(await alicePage.locator('[data-composer-attachments] a').textContent(), attachment.name);
  const edit = content + ' updated';
  await alicePage.locator(source).fill(edit);
  await alicePage.locator('#composer-send').click();
  await alicePage.waitForURL(url => url.searchParams.get('chat') === aliceConversation.id && !url.searchParams.has('editSchedule'));
  await until(async () => await alicePage.locator(source).inputValue() === 'Existing independent draft', 'restored pre-existing draft');

  await openDrawer(alicePage, 'Scheduled');
  const edited = aliceDrawer.locator('[data-scheduled-row]').filter({hasText: edit});
  await edited.getByRole('button', {name:'Send now',exact:true}).click();
  await until(async () => {
    const page = await api('/api/v1/conversations/' + bobConversation.id + '/messages?limit=50');
    return page.items.some(message => message.originalText.startsWith(edit + '\nhttps://'));
  }, 'same scheduled composition delivered through XMPP after Send now');
  await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 1, 'Send now clears exactly one row');
  await edited.waitFor({state:'hidden'});
  assert(await aliceDrawer.isVisible());

  const bobDrawer = await openDrawer(bobPage, 'Agendadas');
  const bobRow = bobDrawer.locator('[data-scheduled-row]');
  const bobReschedule = bobRow.getByRole('button', {name:'Reagendar',exact:true});
  await bobReschedule.click();
  await bobPage.locator(preview).waitFor({state:'visible'});
  await bobPage.keyboard.press('Escape');
  await bobPage.locator(preview).waitFor({state:'hidden'});
  await until(() => bobReschedule.evaluate(node => node === document.activeElement), 'mobile returns focus to the drawer after Escape');
  await bobRow.getByRole('button', {name:'Cancelar mensagem agendada',exact:true}).click();
  await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 0, 'second schedule cancelled');
  await bobRow.waitFor({state:'hidden'});
  assert(await bobDrawer.isVisible());
}

module.exports = {schedulingFlow};
