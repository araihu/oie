// A local engine with an older destination outside both first pages.
const assert = require('node:assert/strict');
const path = require('node:path');
const {chromium} = require('playwright');
const {startOie} = require('./fixtures/oie-server.cjs');

(async () => {
  const binary = process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie');
  const server = await startOie(binary, [], '/api/v1/engine', undefined, 60000);
  const browser = await chromium.launch();
  let sequence = 0;
  async function post(resource, body) {
    const response = await fetch(server.url + '/api/v1/' + resource, {
      method:'POST', headers:{'Content-Type':'application/json', 'Idempotency-Key':'019a5df0-0000-7000-8000-' + String(++sequence).padStart(12,'0')},
      body:JSON.stringify(body),
    });
    if (!response.ok) throw Error(await response.text());
    return response.json();
  }
  try {
    const account = await post('accounts', {protocol:'xmpp', address:'agenda@fixture.invalid', label:'Agenda'});
    const chat = name => post('conversations', {accountId:account.id, address:name + '@fixture.invalid'});
    const schedule = (id, text, hours) => post('conversations/' + id + '/messages', {
      originalText:text, scheduledAt:new Date(Date.now() + hours * 3600000).toISOString(),
    });
    const old = await chat('older-recipient'), later = await chat('second-older-recipient'), recent = await chat('daily');
    const oldReminder = await schedule(old.id, 'Find this later reminder', 48);
    for (let i=0; i<53; i++) await schedule(recent.id, 'Nearer reminder ' + i, 1 + i/60);
    for (let i=0; i<103; i++) await chat('new-' + i);
    const page = await browser.newPage();
    await page.goto(server.url + '/?section=scheduled&lang=en');
    const picker = page.locator('#schedule-conversation'), results = page.locator('[data-scheduled-row]');
    assert.equal(await results.count(), 50);
    assert.equal(await results.filter({hasText:'Find this later reminder'}).count(), 0);
    await picker.getByRole('combobox').click();
    const search = picker.getByRole('textbox');
    await search.fill(old.title);
    await picker.locator('[role="option"][data-value="' + old.id + '"]').click();
    await page.waitForURL(url => url.searchParams.get('scheduleConversation') === old.id);
    assert.equal(await results.count(), 1);
    assert.match(await results.first().innerText(), /Find this later reminder/);
    await search.fill(later.title);
    const lateOption = picker.locator('[role="option"][data-value="' + later.id + '"]');
    assert.equal(await lateOption.count(), 0);
    await schedule(later.id, 'Scheduled while the filter is open', 72);
    await lateOption.waitFor();
    assert.equal(await search.inputValue(), later.title);
    assert.equal(new URL(page.url()).searchParams.get('scheduleConversation'), old.id);
    await lateOption.click();
    await page.waitForURL(url => url.searchParams.getAll('scheduleConversation').length === 2);
    assert.equal(await results.count(), 2);
    await picker.locator('[data-combobox-clear]').click();
    await page.waitForURL(url => !url.searchParams.has('scheduleConversation'));
    await post('messages/' + oldReminder.id + '/cancel', {expectedRevisionId:oldReminder.revisionId});
    // Cancelling can make this conversation recent again.
    for (let i=0; i<101; i++) await chat('next-' + i);
    await page.evaluate(() => document.dispatchEvent(new Event('oie:refresh-request')));
    await picker.locator('[data-value="' + old.id + '"]').waitFor({state:'detached'});
    await page.goBack();
    await picker.locator('input[name="scheduleConversation"][value="' + old.id + '"]').waitFor({state:'attached'});
    assert.equal(await results.count(), 1);
    assert.match(await results.first().innerText(), /Scheduled while the filter is open/);
    console.log('PASS: earlier scheduled recipients, filtered agenda and live picker updates');
  } finally {
    await browser.close();
    await server.stop();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
