// Independent integration observer: drives real rendered forms and checks the
// real engine/Prosody result. It never substitutes a mocked send response.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const {chromium} = require('playwright');

(async () => {
  const base = process.env.OIE_LIVE_URL || 'http://127.0.0.1:18087';
  const engine = process.env.OIE_ENGINE_URL || 'http://127.0.0.1:18086';
  const endpoint = process.env.OIE_XMPP_ENDPOINT || '127.0.0.1:5222';
  const prefix = process.env.OIE_BROWSER_SCENE || 'real-engine';
  const browser = await chromium.launch({headless: true});
  const evidence = {backend: 'real Oie engine + development Prosody', workflows: [], consoleErrors: [], expectedTransportFailures: []};
  const api = async path => {
    const response = await fetch(engine + path);
    assert.equal(response.status, 200, 'engine GET ' + path);
    return response.json();
  };
  const until = async (fn, name, timeout = 20000) => {
    const end = Date.now() + timeout;
    while (Date.now() < end) { if (await fn()) return; await new Promise(r => setTimeout(r, 50)); }
    throw Error('Timed out: ' + name);
  };
  const choose = async (page, form, label) => {
    await form.getByRole('combobox').click();
    await page.getByRole('option').filter({hasText: label}).click();
  };
  const openAccounts = async page => {
    await page.locator('[data-open-accounts]:visible').first().click();
    await page.locator('#register-account-form').waitFor({state: 'visible'});
  };
  const closeAccounts = async page => {
    await page.keyboard.press('Escape');
    await page.getByRole('dialog', {name: 'Accounts', exact: true}).waitFor({state:'hidden'});
  };
  const connectForm = (page, account) => page.locator('form[data-connect-account]').filter({has: page.locator('input[name="accountId"][value="' + account.id + '"]')});
  const disconnectForm = (page, account) => page.locator('form[data-disconnect-account]').filter({has: page.locator('input[name="accountId"][value="' + account.id + '"]')});
  const connect = async (page, account, password) => {
    const form = connectForm(page, account);
    await form.locator('[name="password"]').fill(password);
    await form.locator('[name="endpoint"]').fill(endpoint);
    await form.locator('button[type="submit"]').click();
    await until(async () => (await api('/api/v1/accounts/' + account.id + '/connection')).state === 'connected', 'verified XMPP connection ' + account.address);
    await until(async () => await page.locator('[data-connection-state="connected"][data-account-id="' + account.id + '"]').count() > 0, 'visible connected state');
    const remainingPassword = form.locator('[name="password"]');
    if (await remainingPassword.count()) assert.equal(await remainingPassword.inputValue(), '', 'password must clear after accepted connection');
  };
  const send = async (page, text) => {
    const form = page.locator('#send-message-form');
    await form.locator('#message-composer').fill(text);
    await form.locator('[data-send-message]').click();
    await until(async () => (await form.locator('#message-composer').inputValue()) === '', 'composer clears after engine acceptance');
  };
  const pickAccount = async (page, label) => {
    const form = page.locator('#account-filter-form');
    await choose(page, form, label);
    const submit = form.locator('button[type="submit"]');
    if (await submit.count() && await submit.isVisible()) await submit.click();
    const account = await form.locator('input[name="account"]').inputValue();
    await page.waitForURL(url => (url.searchParams.get('account') || '') === account);
  };
  try {
    await fs.mkdir('artifacts', {recursive: true});
    const aliceContext = await browser.newContext({viewport: {width: 1440, height: 900}, locale: 'en-US', timezoneId: 'America/Sao_Paulo'});
    const bobContext = await browser.newContext({viewport: {width: 1440, height: 900}, locale: 'en-US', timezoneId: 'America/Sao_Paulo'});
    const alicePage = await aliceContext.newPage();
    const bobPage = await bobContext.newPage();
    let expectedLostReply = false;
    for (const page of [alicePage, bobPage]) {
      page.on('pageerror', error => evidence.consoleErrors.push(error.message));
      page.on('console', message => {if (message.type() === 'error') {
        if (expectedLostReply && message.text().includes('net::ERR_FAILED')) evidence.expectedTransportFailures.push(message.text());
        else evidence.consoleErrors.push(message.text());
      }});
    }
    await alicePage.goto(base + '/?theme=dark&lang=en');
    await openAccounts(alicePage);
    const accounts = [];
    for (const who of ['alice', 'bob']) {
      const form = alicePage.locator('#register-account-form');
      await form.locator('[name="address"]').fill(who + '@localhost');
      await form.locator('[name="label"]').fill(who === 'alice' ? 'Alice development' : 'Bob development');
      await form.locator('button[type="submit"]').click();
      await until(async () => (await api('/api/v1/accounts')).items.some(a => a.address === who + '@localhost'), 'account created by browser ' + who);
      accounts.push((await api('/api/v1/accounts')).items.find(a => a.address === who + '@localhost'));
    }
    const [alice, bob] = accounts;
    await connect(alicePage, alice, 'alice-local-only');
    await connect(alicePage, bob, 'bob-local-only');
    evidence.workflows.push('Register and connect both development accounts through rendered forms; actual TLS states and cleared passwords');
    await closeAccounts(alicePage);
    await pickAccount(alicePage, 'Alice development');
    await alicePage.locator('[data-new-conversation]:visible').first().click();
    const newChat = alicePage.locator('#new-conversation-form');
    await newChat.waitFor({state: 'visible'});
    await choose(alicePage, newChat, 'Alice development');
    await newChat.locator('[name="address"]').fill('bob@localhost');
    await newChat.locator('button[type="submit"]').click();
    await alicePage.locator('#message-composer').waitFor({state: 'visible'});
    const nonce = Date.now().toString(36);
    const firstText = 'Olá, Bob! 👋\nA real XMPP message <&> ' + nonce;
    let aliceConversation;
    await until(async () => {
      const list = await api('/api/v1/conversations?accountId=' + alice.id);
      aliceConversation = list.items.find(c => c.address === 'bob@localhost'); return !!aliceConversation;
    }, 'Alice conversation created');
    await bobPage.goto(base + '/?theme=dark&lang=en&account=' + bob.id);
    const originalOperation = await alicePage.locator('#send-message-form [name="operationKey"]').inputValue();
    let replyLost = false;
    const loseReply = async route => {
      const response = await route.fetch();
      assert.equal(response.status(),200);
      const result = await response.json();
      assert(result.ok && result.accepted, 'injected loss occurs only AFTER real engine acceptance');
      await route.abort('failed');
      replyLost = true;
    };
    expectedLostReply = true;
    await alicePage.route('**/ui/messages/send',loseReply);
    await alicePage.locator('#message-composer').fill(firstText);
    await alicePage.locator('[data-send-message]').click();
    await until(async () => alicePage.locator('#message-composer').getAttribute('aria-readonly').then(value=>value==='true'), 'uncertain submission locks original body');
    await until(async () => replyLost, 'discarded the real successful browser response');
    await until(async () => (await api('/api/v1/operations/' + originalOperation)).kind === 'send_message', 'real acceptance receipt despite lost browser response');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),firstText);
    assert.equal(await alicePage.locator('#send-message-form [name="operationKey"]').inputValue(),originalOperation);
    await until(async () => (await alicePage.locator('#send-message-form').getAttribute('data-busy')) !== 'true', 'lost response shows retry state');
    await alicePage.unroute('**/ui/messages/send',loseReply);
    await alicePage.locator('[data-send-message]').click();
    await until(async () => (await alicePage.locator('#message-composer').inputValue()) === '', 'same-key retry reconciles accepted send');
    expectedLostReply = false;
    evidence.workflows.push('Drop the actual successful browser response, retain locked body/key, reconcile/retry once without a second engine/provider effect');
    let bobConversation;
    await until(async () => {
      const list = await api('/api/v1/conversations?accountId=' + bob.id);
      bobConversation = list.items.find(c => c.address === 'alice@localhost'); return !!bobConversation;
    }, 'Bob conversation created by incoming XMPP');
    await bobPage.locator('[data-conversation-id="' + bobConversation.id + '"]').waitFor({state: 'visible'});
    await bobPage.locator('[data-conversation-id="' + bobConversation.id + '"]').click();
    await bobPage.locator('[data-message-id]').filter({hasText: firstText}).waitFor({state: 'visible'});
    let bobMessages = await api('/api/v1/conversations/' + bobConversation.id + '/messages');
    assert.equal(bobMessages.items.filter(m => m.originalText === firstText && !m.isOwn).length, 1, 'one actual recipient effect');
    evidence.workflows.push('Start an account-scoped conversation and receive a Unicode/multiline/XML-character message over real XMPP');
    const draft = 'Keep this unsent draft while Bob replies';
    await alicePage.locator('#message-composer').fill(draft);
    const replyText = 'Hi Alice, received! ' + nonce;
    await send(bobPage, replyText);
    await alicePage.locator('[data-message-id]').filter({hasText: replyText}).waitFor({state: 'visible'});
    assert.equal(await alicePage.locator('#message-composer').inputValue(), draft, 'live update preserved unsent draft');
    await alicePage.locator('#message-composer').fill('');
    evidence.workflows.push('Bob replies; Alice receives automatically without losing her unsent draft');
    const ownStored = (await api('/api/v1/conversations/' + aliceConversation.id + '/messages')).items.find(m => m.originalText === firstText && m.isOwn);
    const ownTime = alicePage.locator('[data-message-id="' + ownStored.id + '"] time[datetime]').last();
    await until(async () => ownTime.evaluate(time => {
      const hour = new Date(time.dateTime).getHours();
      const visibleHour = Number(time.textContent.match(/\d+/)?.[0]);
      return visibleHour === hour || visibleHour === (hour % 12 || 12);
    }), 'timestamps follow browser device timezone');

    await openAccounts(alicePage);
    await disconnectForm(alicePage, alice).locator('button[type="submit"]').click();
    await until(async () => (await api('/api/v1/accounts/' + alice.id + '/connection')).state === 'not_connected', 'explicit disconnect');
    await closeAccounts(alicePage);
    const cancelledText = 'Cancel this queued message ' + nonce;
    await send(alicePage, cancelledText);
    let cancelled;
    await until(async () => {
      const messages = await api('/api/v1/conversations/' + aliceConversation.id + '/messages');
      cancelled = messages.items.find(m => m.originalText === cancelledText); return cancelled && cancelled.sendState === 'queued';
    }, 'queued message persisted while disconnected');
    const queuedRow = alicePage.locator('[data-message-id="' + cancelled.id + '"]');
    await queuedRow.locator('form[data-cancel-message] button[type="submit"]').click();
    await until(async () => (await api('/api/v1/messages/' + cancelled.id)).sendState === 'cancelled', 'guarded queued cancellation');
    const queuedText = 'Send this as soon as we reconnect ' + nonce;
    await send(alicePage, queuedText);
    await openAccounts(alicePage);
    await connect(alicePage, alice, 'alice-local-only');
    await closeAccounts(alicePage);
    await bobPage.locator('[data-message-id]').filter({hasText: queuedText}).waitFor({state: 'visible'});
    bobMessages = await api('/api/v1/conversations/' + bobConversation.id + '/messages');
    assert.equal(bobMessages.items.filter(m => m.originalText === queuedText).length, 1);
    assert.equal(bobMessages.items.filter(m => m.originalText === cancelledText).length, 0, 'cancelled intent never reached recipient');
    evidence.workflows.push('Disconnect queues sends; queued cancellation is terminal; reconnect transmits eligible work once');
    const longText = Array.from({length: 95}, (_, i) => 'Earlier line ' + (i + 1) + ' for live scroll preservation.').join('\n');
    await send(alicePage,longText);
    await bobPage.locator('[data-message-id]').filter({hasText: longText}).waitFor({state:'attached'});
    const timeline=bobPage.locator('#message-timeline');
    await until(async()=>timeline.evaluate(el=>el.scrollHeight>el.clientHeight+500),'long real message gives a scrollable timeline');
    await timeline.evaluate(el=>{el.scrollTop=0;el.dispatchEvent(new Event('scroll'));});
    const whileReading='A new message while you read earlier history '+nonce;
    await send(alicePage,whileReading);
    await bobPage.locator('[data-message-id]').filter({hasText:whileReading}).waitFor({state:'attached'});
    assert(await timeline.evaluate(el=>el.scrollTop<80),'incoming message preserves position away from bottom');
    await bobPage.locator('[data-jump-latest]').waitFor({state:'visible'});
    await bobPage.locator('[data-jump-latest]').click();
    assert(await timeline.evaluate(el=>el.scrollHeight-el.scrollTop-el.clientHeight<80),'jump to latest follows the current end');
    evidence.workflows.push('Real incoming message preserves older scroll position and exposes a working jump-to-latest action');

    await alicePage.screenshot({path: 'artifacts/' + prefix + '-alice-desktop.png', fullPage: true});
    await bobPage.setViewportSize({width: 390, height: 844});
    await bobPage.goto(base + '/?chat=' + bobConversation.id + '&account=' + bob.id + '&theme=dark&lang=pt-BR');
    await bobPage.locator('#message-composer').waitFor({state: 'visible'});
    assert(await bobPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile has no horizontal page overflow');
    await bobPage.addScriptTag({path: require.resolve('axe-core/axe.min.js')});
    const a11y = await bobPage.evaluate(async () => window.axe.run(document, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa']}}));
    assert.deepEqual(a11y.violations.map(v => ({id: v.id, targets: v.nodes.map(n => n.target)})), [], 'live mobile accessibility');
    await bobPage.screenshot({path: 'artifacts/' + prefix + '-bob-mobile-pt.png', fullPage: true});
    evidence.workflows.push('Actual chat usable at390px in Brazilian Portuguese with no checked WCAG A/AA violations');
    assert.deepEqual(evidence.consoleErrors, []);
    await fs.writeFile('artifacts/' + prefix + '-report.json', JSON.stringify(evidence, null, 2));
    console.log('PASS: real-engine browser connection, two-way XMPP, live draft preservation, queue/cancel/reconnect, mobile and accessibility');
  } finally { await browser.close(); }
})().catch(error => {console.error(error); process.exitCode = 1;});
