const assert = require('node:assert/strict');
const {spawn} = require('node:child_process');
const {randomUUID} = require('node:crypto');
const {once} = require('node:events');
const fs = require('node:fs/promises');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const {chromium} = require('playwright');

const operationKey = () => randomUUID().replace(/^(.{14})4/, (_, prefix) => prefix + '7');

(async () => {
  const listener = net.createServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const address = '127.0.0.1:' + listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  const base = 'http://' + address;
  const data = await fs.mkdtemp(path.join(os.tmpdir(), 'oie-conversation-removal-'));
  const engine = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--listen', address, '--data-dir', data], {stdio: ['ignore', 'ignore', 'pipe']});
  const stopped = once(engine, 'exit');
  let log = '';
  engine.stderr.on('data', chunk => { log += chunk; });
  let browser;
  const api = async (route, body, key = operationKey(), status = body ? 201 : 200) => {
    const response = await fetch(base + '/api/v1' + route, body && {
      method: 'POST',
      headers: {'Content-Type': 'application/json', 'Idempotency-Key': key},
      body: JSON.stringify(body),
    });
    const raw = await response.text();
    assert.equal(response.status, status, raw);
    return raw && JSON.parse(raw);
  };

  try {
    for (let attempt = 0; ; attempt++) {
      try { await api('/engine'); break; }
      catch (error) {
        if (attempt === 100) throw Error('Engine startup: ' + log, {cause: error});
        await new Promise(resolve => setTimeout(resolve, 50));
      }
    }
    const account = await api('/accounts', {address:'review@localhost', label:'Demo', protocol:'xmpp'});
    const peer = {accountId:account.id, address:'peer@localhost'};
    const chat = await api('/conversations', peer);
    const route = '/conversations/' + chat.id;
    const sourceKey = operationKey();
    const source = await api(route + '/messages', {originalText:'Retained history'}, sourceKey, 202);
    const url = base + '/?account=' + account.id + '&chat=' + chat.id;
    const chatPath = '#message-timeline [data-message-id="';
    browser = await chromium.launch();
    const owner = await browser.newPage({viewport:{width:1025,height:900}, locale:'en'});
    const reader = await browser.newPage({viewport:{width:390,height:844}, locale:'pt-BR'});
    const pageErrors = [];
    for (const page of [owner, reader]) {
      page.setDefaultTimeout(10000);
      page.on('pageerror', error => pageErrors.push(error.message));
    }
    await owner.goto(url + '&lang=en');
    await reader.goto(url + '&lang=pt-BR');
    await owner.waitForFunction(() => window.OieEvents?.connected);
    await reader.waitForFunction(() => window.OieEvents?.connected);
    await reader.locator('#message-composer').fill('Rascunho desta aba');

    const trigger = owner.locator('#chat-options button[aria-haspopup=menu]');
    const item = owner.locator('#chat-remove-direct');
    const dialog = owner.locator('#conversation-removal');
    const revision = dialog.locator('[name=expectedRevisionId]');
    const submit = dialog.locator('button[type=submit]');
    const open = async (keyboard = false) => {
      if (keyboard) {
        await trigger.press('Enter');
        await item.press('Enter');
      } else {
        await trigger.click();
        await item.click();
      }
      await dialog.waitFor();
    };
    const cancel = async () => {
      await owner.keyboard.press('Escape');
      await dialog.waitFor({state:'hidden'});
      assert(await trigger.evaluate(node => node === document.activeElement), 'focus returned to More options');
    };
    const submitRemoval = () => Promise.all([
      owner.waitForResponse(response => response.url().endsWith('/ui/conversations/remove') && response.request().method() === 'POST'),
      submit.click(),
    ]);

    await open(true);
    await cancel();
    const oldRevision = await revision.inputValue();
    const beforeOpen = await api(route + '/messages', {originalText:'Message before confirmation'}, undefined, 202);
    await owner.locator(chatPath + beforeOpen.id + '"]').waitFor();
    const current = await api(route);
    assert.notEqual(current.revisionId, oldRevision);
    await open();
    assert.equal(await revision.inputValue(), current.revisionId);

    const during = await api(route + '/messages', {originalText:'Message during confirmation'}, undefined, 202);
    await owner.locator(chatPath + during.id + '"]').waitFor();
    assert.equal(await revision.inputValue(), current.revisionId, 'open confirmation kept the reviewed revision');
    const [conflict] = await submitRemoval();
    assert.equal(conflict.status(), 409);
    await dialog.getByText('The chat has changed. Close and reopen the confirmation to try again.').waitFor();
    await cancel();
    await open();
    assert.equal(await revision.inputValue(), (await api(route)).revisionId);
    const [removed] = await submitRemoval();
    assert.equal(removed.status(), 200);

    await reader.locator('#conversation-pane[data-conversation-removed=true]').waitFor();
    assert.equal(await reader.locator('#send-message-form').count(), 0);
    await reader.locator(chatPath + source.id + '"]').waitFor();
    assert(!(await api('/conversations')).items.some(item => item.id === chat.id));
    await api(route + '/messages', {originalText:'A stale send'}, undefined, 409);
    const replay = await api(route + '/messages', {originalText:'Retained history'}, sourceKey, 200);
    assert.equal(replay.id, source.id);

    const sourceURL = url + '&message=' + source.id + '&lang=pt-BR';
    const page = await browser.newPage({locale:'pt-BR'});
    assert.equal((await page.goto(sourceURL)).status(), 200);
    await page.locator(chatPath + source.id + '"]').waitFor();
    assert.equal(await page.locator('#send-message-form').count(), 0);
    const fragment = await fetch(sourceURL, {headers:{'HX-Request':'true','X-Oie-Fragment':'source-pane'}});
    assert.equal(fragment.status, 200);
    const html = await fragment.text();
    assert(html.includes('Retained history'));
    assert(!html.includes('id="send-message-form"'));
    assert(!(await api('/conversations')).items.some(item => item.id === chat.id), 'history views did not restore inbox visibility');
    await page.close();

    await api('/conversations', peer, undefined, 200);
    const input = reader.locator('#message-composer');
    await input.waitFor();
    assert.equal(await input.inputValue(), 'Rascunho desta aba');
    assert.deepEqual(pageErrors, []);
    console.log('PASS: live removal confirmation, focus, conflict retry, other tab, retained history, rejected stale sends, and draft restoration');
  } finally {
    await browser?.close();
    engine.kill('SIGTERM');
    await stopped;
    await fs.rm(data, {recursive:true, force:true});
  }
})().catch(error => {console.error(error); process.exitCode = 1;});
