const assert = require('node:assert/strict');
const {createHash, randomUUID} = require('node:crypto');

async function schedulingFlow({base, api, until, alicePage, bobPage, alice, bob, aliceConversation, bobConversation}) {
  const source = '#message-composer';
  function monitorNavigation(page) {
    const traffic = {documents:0, streams:0};
    const count = request => {
      if (request.resourceType() === 'document') traffic.documents++;
      if (request.resourceType() === 'eventsource') traffic.streams++;
    };
    page.on('request',count);
    return {traffic, stop:() => page.off('request',count)};
  }
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
  const attachToDraft = async page => {
    await page.locator('[data-attachment-picker]').setInputFiles(attachment);
    await page.locator('[data-composer-attachments]').getByRole('link', {name:attachment.name, exact:true}).waitFor();
  };
  const openMenu = async (page, label) => {
    await page.locator('#composer-split').getByRole('button', {name: label, exact: true}).click();
  };
  const openSchedule = async (page, portuguese = false) => {
    const name = portuguese ? 'Agendar mensagem' : 'Schedule message';
    await openMenu(page, portuguese ? 'Mais opções' : 'More options');
    await page.getByRole('menuitem', {name, exact:true}).click();
    const dialog = page.locator(preview);
    await dialog.waitFor({state:'visible'});
    return dialog.getByRole('button', {name, exact:true});
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
  const assertCentered = async dialog => {
    const bounds = await dialog.evaluate(node => {
      const {left, right, top, bottom} = node.getBoundingClientRect();
      return [left, innerWidth - right, top, innerHeight - bottom];
    });
    assert(bounds.every(inset => inset >= 15), JSON.stringify(bounds));
    assert(Math.abs(bounds[0] - bounds[1]) <= 1 && Math.abs(bounds[2] - bounds[3]) <= 1, JSON.stringify(bounds));
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
  const dismissToast = async (page, message, portuguese) => {
    const toast = page.locator('[data-toast-container] [role="alert"]').filter({hasText:message});
    await toast.getByRole('button', {name:portuguese ? 'Fechar' : 'Close', exact:true}).click();
    await toast.waitFor({state:'hidden'});
  };
  const rejectAttachment = async (page, path, submit, original) => {
    const message = 'An attachment exceeds the provider’s upload limit. Remove it or choose a smaller file.';
    await page.route('**' + path, route => route.fulfill({json:{ok:false, rejected:true, code:'media_too_large', message}}), {times:1});
    await submit.click();
    await page.getByText(message, {exact:true}).last().waitFor();
    await until(() => page.locator(source).isEditable(), 'a definitive upload rejection unlocks the composer');
    assert.equal(await page.locator(source).inputValue(), original);
    const files = page.locator('[data-composer-attachments]');
    await files.getByRole('link', {name:attachment.name, exact:true}).waitFor();
    await files.getByRole('button', {name:'Remove attachment · ' + attachment.name, exact:true}).click();
    await files.waitFor({state:'hidden'});
    await attachToDraft(page);
    await files.getByRole('link', {name:attachment.name, exact:true}).waitFor();
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
  await assertCentered(dialog);
  assert.equal(await dialog.locator('[data-schedule-preview-text]').textContent(), content);
  assert.equal(await dialog.locator('img').getAttribute('alt'), attachment.name);
  await dialog.getByRole('button', {name: 'Back', exact: true}).click();
  assert.equal(await alicePage.locator(source).inputValue(), content, 'closing preview preserves composition');
  const rejectedSubmit = await openSchedule(alicePage);
  await fillPlannedTime(alicePage);
  await rejectAttachment(alicePage, '/ui/messages/send', rejectedSubmit, content);
  const confirmedSubmit = await openSchedule(alicePage);
  await fillPlannedTime(alicePage);
  const shownZone = await dialog.locator('[data-schedule-zone]').textContent();
  const timezone = await alicePage.context().newPage();
  try {
    await timezone.goto(base + '/?section=scheduled');
    const otherZone = shownZone === 'Asia/Tokyo' ? 'America/Sao_Paulo' : 'Asia/Tokyo';
    await timezone.evaluate(value => localStorage.setItem('oie.device.time-zone.v1', value), otherZone);
    await alicePage.waitForFunction(value => window.OieTime.timeZone() === value, otherZone);
    assert.equal(await dialog.locator('[data-schedule-zone]').textContent(), shownZone);
  } finally {
    await timezone.close();
  }
  const submitted = alicePage.waitForResponse(response => response.url().endsWith('/ui/messages/send') && response.request().method() === 'POST');
  await confirmedSubmit.click();
  const acceptedSchedule = await submitted;
  assert.equal(new URLSearchParams(acceptedSchedule.request().postData()).get('scheduledAt'), planned.toISOString());
  assert.equal((await acceptedSchedule.json()).ok, true);
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
    const actions = await row.locator('.oie-conversation-schedule-actions').evaluate(node => {
      return [...node.children].map(action => {
        const {x, y, width, height} = action.getBoundingClientRect();
        return {x, y, width, height, fits:action.scrollWidth <= action.clientWidth};
      });
    });
    assert.equal(actions.length, 4);
    assert(actions.every(action => action.fits && action.height >= 36 && Math.abs(action.width - actions[0].width) <= 1));
    assert.equal(actions[0].y, actions[1].y);
    assert.equal(actions[2].y, actions[3].y);
    assert.equal(actions[0].x, actions[2].x);
    assert.equal(actions[1].x, actions[3].x);
    assert(actions[2].y > actions[0].y);
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
  let releaseFilter;
  const filterGate = new Promise(resolve => { releaseFilter = resolve; });
  const filterFetch = url => url.pathname === '/' && url.searchParams.getAll('scheduleConversation').length === 2;
  await alicePage.route(filterFetch, async route => {
    await filterGate;
    await route.continue();
  }, {times:1});
  const filterStarted = alicePage.waitForRequest(req => filterFetch(new URL(req.url())));
  await filter.getByRole('combobox').click();
  await secondOption.click();
  await filterStarted;
  try {
    await alicePage.keyboard.press('Escape');
    assert.equal(await alicePage.locator('#scheduled-results').evaluate(node => node.inert), true);
    await row.evaluate(node => node.dispatchEvent(new CustomEvent('oie:schedule-action', {bubbles:true, detail:'reschedule'})));
    await alicePage.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    assert.equal(await alicePage.locator(preview).isVisible(), false, 'cannot open an action for results about to be replaced');
  } finally {
    releaseFilter();
  }
  await alicePage.waitForURL(url => url.searchParams.getAll('scheduleConversation').length === 2);
  await filter.getByRole('combobox').click();
  await secondOption.click();
  await alicePage.waitForURL(url => url.searchParams.getAll('scheduleConversation').length === 1);
  await alicePage.keyboard.press('Escape');
  const menuTrigger = row.locator('[data-schedule-actions-trigger]');
  await menuTrigger.press('Enter');
  await row.getByRole('menuitem', {name: 'Reschedule', exact: true}).waitFor();
  await alicePage.keyboard.press('Escape');
  await row.getByRole('menu').waitFor({state:'hidden'});
  const reschedule = alicePage.locator(preview);
  const agendaNavigation = monitorNavigation(alicePage);
  await alicePage.evaluate(() => {
    window.scheduleAgenda = document.getElementById('workspace');
    window.scheduleFilter = document.querySelector('[data-schedule-filter]');
    window.agendaGeneration = window.OieRuntime.generation;
  });
  await agendaAction(row, 'Reschedule');
  await reschedule.waitFor({state:'visible'});
  await reschedule.getByRole('button', {name:'Back',exact:true}).click();
  await until(() => menuTrigger.evaluate(node => node === document.activeElement), 'agenda restores focus after closing the scheduling dialog');
  const agendaDate = row.locator('[data-scheduled-date]');
  const oldAgendaDate = await agendaDate.getAttribute('datetime');
  const agendaURL = alicePage.url();
  await agendaAction(row,'Reschedule');
  await reschedule.waitFor({state:'visible'});
  const agendaTime = reschedule.locator('#schedule-local-time');
  const newAgendaTime = new Date((await agendaTime.inputValue())+'Z');
  newAgendaTime.setUTCHours(newAgendaTime.getUTCHours()+1);
  await agendaTime.fill(newAgendaTime.toISOString().slice(0,16));
  await reschedule.getByRole('button',{name:'Reschedule',exact:true}).click();
  await until(async()=>await agendaDate.getAttribute('datetime')!==oldAgendaDate,'agenda row updates in place');
  assert.equal(alicePage.url(),agendaURL);
  assert(await alicePage.evaluate(()=>window.scheduleAgenda===document.getElementById('workspace')&&
    window.scheduleFilter===document.querySelector('[data-schedule-filter]')&&window.agendaGeneration===window.OieRuntime.generation));
  assert.deepEqual(agendaNavigation.traffic,{documents:0,streams:0});
  agendaNavigation.stop();


  await alicePage.goto(path(aliceConversation, alice, 'en'));
  const aliceDrawer = await openDrawer(alicePage, 'Scheduled');
  const drawerRow = aliceDrawer.locator('[data-scheduled-row]').filter({hasText:content});
  const scheduledTime = drawerRow.locator('[data-scheduled-date]');
  const previous = await scheduledTime.getAttribute('datetime');
  const chatURL = alicePage.url();
  const rescheduleButton = drawerRow.getByRole('button', {name:'Reschedule',exact:true});
  await rescheduleButton.click();
  await reschedule.waitFor({state:'visible'});
  await assertCentered(reschedule);
  await reschedule.getByRole('link', {name:attachment.name,exact:true}).waitFor();
  assert.equal(await reschedule.locator('[data-schedule-preview-files] img').getAttribute('alt'), attachment.name);
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

  const editNavigation = monitorNavigation(alicePage);
  const preserved = await alicePage.evaluate(() => {
    const timeline = document.getElementById('message-timeline');
    window.scheduleChat = [timeline, document.getElementById('message-composer')];
    return {top:timeline.scrollTop,generation:window.OieRuntime.generation};
  });
  const checkEditNavigation = async () => {
    assert.deepEqual(editNavigation.traffic,{documents:0,streams:0});
    assert(await alicePage.evaluate(({top,generation}) => window.scheduleChat.every(node=>node.isConnected)&&
      Math.abs(window.scheduleChat[0].scrollTop-top)<=1&&window.OieRuntime.generation===generation,preserved));
  };
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
  await checkEditNavigation();
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
  await rejectAttachment(alicePage, '/ui/scheduling/update', alicePage.locator('#composer-send'), edit);
  await alicePage.locator('#composer-send').click();
  await alicePage.waitForURL(url => url.searchParams.get('chat') === aliceConversation.id && !url.searchParams.has('editSchedule'));
  await until(async () => await alicePage.locator(source).inputValue() === 'Existing independent draft', 'restored pre-existing draft');
  await checkEditNavigation();
  editNavigation.stop();

  const intervening = 'Mensagem recebida depois do agendamento ' + randomUUID().slice(0,8);
  await bobPage.locator(source).fill(intervening);
  await bobPage.locator('#composer-send').click();
  const aliceMessages = '/api/v1/conversations/' + aliceConversation.id + '/messages?limit=1';
  await until(async () => (await api(aliceMessages)).items[0]?.originalText === intervening, 'a newer message arrives before Send now');
  await openDrawer(alicePage, 'Scheduled');
  const edited = aliceDrawer.locator('[data-scheduled-row]').filter({hasText: edit});
  await edited.getByRole('button', {name:'Send now',exact:true}).click();
  await until(async () => {
    const page = await api('/api/v1/conversations/' + bobConversation.id + '/messages?limit=50');
    return page.items.some(message => message.originalText.startsWith(edit + '\nhttps://'));
  }, 'same scheduled composition delivered through XMPP after Send now');
  await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 1, 'Send now clears exactly one row');
  const published = (await api(aliceMessages)).items[0];
  assert.equal(published.originalText, edit, 'Send now appears after the intervening message');
  assert(Date.parse(published.timelineAt) > Date.parse(published.createdAt));
  const latest = alicePage.locator('#message-timeline [data-message-id]').last();
  await until(() => latest.getAttribute('data-message-id').then(id => id === published.id), 'sender timeline shows the released message at the end');
  await edited.waitFor({state:'hidden'});
  assert(await aliceDrawer.isVisible());

  const bobDrawer = await openDrawer(bobPage, 'Agendadas');
  const bobRow = bobDrawer.locator('[data-scheduled-row]');
  const bobReschedule = bobRow.getByRole('button', {name:'Reagendar',exact:true});
  await bobReschedule.click();
  await bobPage.locator(preview).waitFor({state:'visible'});
  await assertCentered(bobPage.locator(preview));
  await bobPage.keyboard.press('Escape');
  await bobPage.locator(preview).waitFor({state:'hidden'});
  await until(() => bobReschedule.evaluate(node => node === document.activeElement), 'mobile returns focus to the drawer after Escape');
  await bobRow.getByRole('button', {name:'Cancelar envio',exact:true}).click();
  await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 0, 'second schedule cancelled');
  await bobRow.waitFor({state:'hidden'});
  assert(await bobDrawer.isVisible());

  for (const [page, conversation, account, lang] of [[alicePage, aliceConversation, alice, 'en'], [bobPage, bobConversation, bob, 'pt-BR']]) {
    const portuguese = lang === 'pt-BR';
    await page.goto(path(conversation, account, lang));
    await page.locator('#message-timeline [data-message-menu-trigger]').last().click();
    await page.getByRole('menuitem', {name:portuguese ? 'Responder' : 'Reply', exact:true}).click();
    const composer = page.locator(source);
    const reviewed = 'Reviewed ' + randomUUID().slice(0,8);
    const newText = 'Changed ' + randomUUID().slice(0,8);
    await page.locator('[data-composer-reply]').waitFor();
    await composer.fill(reviewed);
    const schedule = await openSchedule(page, portuguese);
    await fillPlannedTime(page);
    assert.equal(await page.locator('[data-schedule-preview-text]').textContent(), reviewed);
    await page.locator('[data-schedule-quote]').waitFor();
    let releaseReply;
    const replyGate = new Promise(resolve => { releaseReply = resolve; });
    await page.route('**/ui/reply-source?*', async route => {
      await replyGate;
      await route.continue();
    }, {times:1});
    const replyStarted = page.waitForRequest(req => new URL(req.url()).pathname === '/ui/reply-source');
    let posts = 0;
    const countSend = req => { if (new URL(req.url()).pathname === '/ui/messages/send') posts++; };
    page.on('request', countSend);
    try {
      await schedule.click();
      await replyStarted;
      const beforeChange = await sample(page);
      await composer.fill(newText);
      await new Promise(resolve => setTimeout(resolve, 350));
      await stable(page, beforeChange);
    } finally {
      releaseReply();
    }
    const changedMessage = await page.locator(preview).getAttribute('data-review-changed');
    await page.getByText(changedMessage, {exact:true}).waitFor();
    page.off('request', countSend);
    assert.equal(posts, 0, 'a changed composition requires a fresh review');
    assert.equal(await composer.inputValue(), newText);
    assert(await composer.isEditable());
    assert(await composer.evaluate(node => node === document.activeElement));
    await dismissToast(page, changedMessage, portuguese);

    const staleQuote = await openSchedule(page, portuguese);
    await fillPlannedTime(page);
    let staleQuoteRejected = false;
    const refreshedQuote = page.waitForRequest(req => staleQuoteRejected && new URL(req.url()).pathname === '/ui/reply-source');
    await page.route('**/ui/messages/send', route => {
      staleQuoteRejected = true;
      return route.fulfill({json:{ok:false, rejected:true, code:'reply_source_changed'}});
    }, {times:1});
    const refusedQuote = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send');
    await staleQuote.click();
    await refusedQuote;
    await dismissToast(page, changedMessage, portuguese);
    await refreshedQuote;
    await until(() => composer.isEditable(), 'a reply revision conflict unlocks the composer');
    assert.equal(await composer.inputValue(), newText);
    await page.locator('[data-composer-reply]').waitFor();
    await until(() => page.locator('#composer-send').isEnabled(), 'the quote refresh allows a new review');

    const reviewedAgain = await openSchedule(page, portuguese);
    await fillPlannedTime(page);
    assert.equal(await page.locator('[data-schedule-preview-text]').textContent(), newText);
    const excerpt = await page.locator('[data-composer-reply-excerpt]').textContent();
    const replayed = page.waitForResponse(response => new URL(response.url()).pathname === '/ui/messages/send');
    await reviewedAgain.click();
    const confirmation = await replayed;
    const body = new URLSearchParams(confirmation.request().postData());
    assert.equal(body.get('originalText'), newText);
    assert(body.get('replyToMessageId'));
    assert.match(body.get('expectedReplyRevisionId') || '', /^[0-9a-f-]{36}$/);
    assert.equal(body.get('expectedReplyExcerptSha256'), createHash('sha256').update(excerpt).digest('hex'));
    const result = await confirmation.json();
    assert.equal(result.ok, true);
    await dismissToast(page, result.message, portuguese);
    const drawer = await openDrawer(page, portuguese ? 'Agendadas' : 'Scheduled');
    await drawer.locator('[data-scheduled-row]').filter({hasText:newText}).getByRole('button', {name:portuguese ? 'Cancelar envio' : 'Cancel send', exact:true}).click();
    await until(async () => (await api('/api/v1/scheduled-messages')).items.length === 0, 'cancel the reviewed reply fixture');
    await drawer.locator('[data-scheduled-row]').filter({hasText:newText}).waitFor({state:'hidden'});
  }

  await bobPage.goto(path(bobConversation, bob, 'pt-BR'));
  await bobPage.locator('body[data-oie-live-ready]').waitFor();
  const engineId = await bobPage.locator('body').getAttribute('data-engine-id');
  const pages = [];
  let failNextPage = false;
  let releaseRefresh, beginRefresh;
  const refreshGate = new Promise(resolve => { releaseRefresh = resolve; });
  const refreshStarted = new Promise(resolve => { beginRefresh = resolve; });
  await bobPage.route('**/ui/scheduling/conversation?*', async route => {
    const cursor = new URL(route.request().url()).searchParams.get('cursor') || '';
    pages.push(cursor);
    if (pages.length === 3) {
      beginRefresh();
      await refreshGate;
    }
    if (cursor && failNextPage) {
      failNextPage = false;
      await route.fulfill({contentType:'text/html', body:'<p>Indisponível</p>'});
      return;
    }
    const id = cursor ? 'second' : 'first';
    await route.fulfill({contentType:'text/html', body:`
      <div data-conversation-schedule-page data-engine-id="${engineId}" data-conversation-id="${bobConversation.id}"${cursor ? '' : ' data-next-cursor="next"'}>
        <ul data-schedule-agenda>
          <li data-scheduled-row="${id}">
            <a href="/" data-conversation-schedule-edit="${id}">Editar ${id}</a>
            <button data-conversation-schedule-command="send-now">Enviar agora</button>
            <form action="/ui/scheduling/send-now" method="post" data-oie-action="schedule-row" data-schedule-command="send-now" hidden></form>
          </li>
        </ul>
      </div>`});
  });
  await bobPage.route('**/ui/scheduling/send-now', route => route.fulfill({json:{ok:false, rejected:true, code:'state_conflict', message:'A mensagem mudou.'}}), {times:1});
  const paginated = await openDrawer(bobPage, 'Agendadas');
  await paginated.locator('[data-conversation-schedules-more]').click();
  const second = paginated.locator('[data-scheduled-row="second"]');
  await second.evaluate(node => { node.dataset.staleSchedule = 'true'; });
  await second.getByRole('button', {name:'Enviar agora',exact:true}).click();
  await refreshStarted;
  const editSecond = second.getByRole('link', {name:'Editar second',exact:true});
  try {
    await editSecond.focus();
  } finally {
    releaseRefresh();
  }
  await bobPage.getByText('A mensagem mudou.', {exact:true}).waitFor();
  await paginated.locator('[data-stale-schedule]').waitFor({state:'detached'});
  assert.deepEqual(pages, ['', 'next', '', 'next']);
  assert.equal(await paginated.locator('[data-scheduled-row]').count(), 2);
  assert(await editSecond.evaluate(node => node === document.activeElement), 'conflict refresh preserves focus on a loaded later page');
  await dismissToast(bobPage, 'A mensagem mudou.', true);

  failNextPage = true;
  await second.evaluate(node => node.closest('[data-conversation-schedules]').dispatchEvent(new Event('oie:schedule-refresh')));
  const status = paginated.locator('[data-conversation-schedules-status]');
  await status.waitFor({state:'visible'});
  assert.equal(await status.textContent(), await paginated.locator('[data-conversation-schedules]').getAttribute('data-unavailable'));
  assert.equal(await paginated.locator('[data-scheduled-row]').count(), 2, 'refresh failure retains loaded schedules');
  await paginated.locator('[data-conversation-schedules-retry]').click();
  await status.waitFor({state:'hidden'});
  await paginated.locator('[data-conversation-schedules-retry]').waitFor({state:'hidden'});
  assert.deepEqual(pages, ['', 'next', '', 'next', '', 'next', '', 'next']);
}

module.exports = {schedulingFlow};
