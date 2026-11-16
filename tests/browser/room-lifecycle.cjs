const assert = require('node:assert/strict');
const {randomUUID} = require('node:crypto');
const fs = require('node:fs/promises');
const {chromium} = require('playwright');

// Run against a disposable live engine with the connected development accounts.
const base = process.env.OIE_LIVE_URL || 'http://127.0.0.1:18854';
const engine = process.env.OIE_ENGINE_URL || base;
const artifacts = process.env.OIE_ROOM_ARTIFACTS;
const xmppEndpoint = process.env.OIE_XMPP_ENDPOINT;
const operationKey = () => randomUUID().replace(/^(.{14})4/, (_, prefix) => prefix + '7');
async function api(path, body, method = body ? 'POST' : 'GET') {
  const response = await fetch(engine + '/api/v1' + path, {
    method, headers: {'Content-Type': 'application/json', 'Idempotency-Key': operationKey()}, body: JSON.stringify(body)
  });
  assert(response.ok, path + ': ' + response.status);
  return response.json();
}
async function until(test, label) {
  for (let n = 0; n < 100; n++) {
    if (await test()) return;
    await new Promise(resolve => setTimeout(resolve, 100));
  }
  throw Error('Timed out: ' + label);
}

(async () => {
  const {items: accounts} = await api('/accounts');
  const alice = accounts.find(a => a.address === 'alice@localhost').id;
  const bob = accounts.find(a => a.address === 'bob@localhost').id;
  const tag = 'browser-' + Date.now();
  const wrong = tag + '@localhost';
  const address = tag + '@conference.localhost';
  const chats = async (account, archived = false) => (await api('/conversations?accountId=' + account + '&archived=' + archived)).items;
  const session = id => api('/conversations/' + id + '/room-session');
  const pageErrors = [];
  const browser = await chromium.launch({headless: true});
  try {
    const page = await browser.newPage({viewport: {width: 1440, height: 900}, locale: 'pt-BR'});
    page.setDefaultTimeout(10000);
    page.on('pageerror', err => pageErrors.push(err.message));
    await page.goto(base + '/?lang=pt-BR&account=' + alice);
    await page.locator('[data-new-conversation]:visible').first().click();
    await page.getByRole('menuitem', {name: 'Criar sala', exact: true}).click();
    const create = page.locator('#create-room-form');
    await create.locator('[name=address]').fill(wrong);
    await create.locator('[name=name]').fill('Clube de leitura');
    await create.locator('[name=nickname]').fill('Marina');

    const confirmCreate = create.locator('button[type=submit]');
    let release, notify;
    const delay = new Promise(resolve => {release = resolve;});
    const intercepted = new Promise(resolve => {notify = resolve;});
    await page.route('**/ui/rooms/open', async route => {notify(); await delay; await route.continue();}, {times:1});
    const buttonBefore = await confirmCreate.boundingBox();
    await confirmCreate.click();
    await intercepted;
    try {
      assert(await confirmCreate.isDisabled());
      await create.getByText('Conferindo endereço da sala…', {exact:true}).waitFor();
      const pendingBox = await confirmCreate.boundingBox();
      assert(Math.abs(pendingBox.y - buttonBefore.y) <= 1, 'pending creation moved the button');
    } finally { release(); }
    await create.getByText('Esse domínio não hospeda salas XMPP.', {exact:false}).waitFor();
    assert.equal(await create.locator('[name=address]').inputValue(), wrong);
    assert.equal(await create.locator('[name=name]').inputValue(), 'Clube de leitura');
    assert.equal(await create.locator('[name=nickname]').inputValue(), 'Marina');
    assert(!(await chats(alice)).some(c => c.address === wrong));

    await create.locator('[name=address]').fill(address);
    await confirmCreate.click();
    await page.waitForURL(url => url.searchParams.has('chat') && url.searchParams.get('open') === 'room-session');
    const room = new URL(page.url()).searchParams.get('chat');
    await until(async () => (await session(room)).state === 'joined', 'creation confirmed by XMPP');
    await page.locator('#room-session-dialog').waitFor({state:'hidden'});
    const bobRoom = await api('/accounts/' + bob + '/rooms', {address, action:'join', nickname:'Leitor'});
    await until(async () => (await session(bobRoom.id)).state === 'joined', 'second participant joined');

    const failed = await api('/accounts/' + alice + '/rooms', {address:wrong, action:'join'});
    await page.setViewportSize({width:390, height:844});
    await page.goto(base + '/?lang=pt-BR&theme=dark&account=' + alice + '&chat=' + failed.id);
    const menu = () => page.locator('#chat-options button[aria-haspopup="menu"]');
    const openRemove = async () => {
      await menu().click();
      await page.locator('#chat-remove-room').click();
    };
    const remove = page.getByRole('dialog', {name:'Remover das conversas'});
    await openRemove();
    await remove.waitFor();
    if (artifacts) {
      await fs.mkdir(artifacts, {recursive:true});
      await remove.screenshot({path:artifacts + '/remover-sala.png'});
      await page.screenshot({path:artifacts + '/remover-sala-original.png'});
    }
    assert(await remove.evaluate(node => node.getBoundingClientRect().right <= innerWidth + 1));
    await page.keyboard.press('Escape');
    await remove.waitFor({state:'hidden'});
    assert(!(await api('/conversations/' + failed.id)).removedFromChats);
    await openRemove();
    await remove.getByRole('button', {name:'Remover das conversas', exact:true}).click();
    await page.waitForURL(url => !url.searchParams.has('chat'));
    assert((await api('/conversations/' + failed.id)).removedFromChats);
    assert(!(await session(failed.id)).active);
    for (const archived of [false,true]) assert(!(await chats(alice, archived)).some(c => c.id === failed.id));

    await page.goto(base + '/?lang=en&account=' + bob + '&chat=' + bobRoom.id);
    await menu().click();
    await page.locator('#chat-manage-room').click();
    await page.locator('.oie-room-management-tabs').waitFor();
    assert.equal(await page.locator('#room-destroy-open').count(), 0, 'a participant cannot delete the server room');

    const participant = await browser.newPage({viewport:{width:1025, height:1108}, locale:'pt-BR'});
    participant.on('pageerror', err => pageErrors.push(err.message));
    const participantURL = base + '/?lang=pt-BR&theme=dark&account=' + bob + '&chat=' + bobRoom.id;
    const statusLabel = participant.locator('#chat-header [data-room-status-label]');
    const roomRow = participant.locator('[data-conversation-row="' + bobRoom.id + '"]');
    await participant.goto(participantURL);
    await statusLabel.getByText('Na sala', {exact:true}).waitFor();
    await roomRow.locator('[data-room-status-label]').getByText('Na sala', {exact:true}).waitFor({state:'attached'});
    if (xmppEndpoint) {
      const connection = '/accounts/' + bob + '/connection';
      await api(connection, undefined, 'DELETE');
      try {
        await statusLabel.getByText('Conta desconectada', {exact:true}).waitFor();
        await roomRow.getByText('Conta desconectada', {exact:true}).waitFor();
      } finally {
        await api(connection, {password:'bob-local-only', endpoint:xmppEndpoint}, 'PUT');
      }
      await statusLabel.getByText('Na sala', {exact:true}).waitFor();
      await roomRow.locator('[data-room-status-label]').getByText('Na sala', {exact:true}).waitFor({state:'attached'});
    }
    const layoutSelectors = ['[data-conversation-row="' + bobRoom.id + '"]', '#chat-header', '#message-composer'];
    const sampleLayout = await participant.evaluateHandle(selectors => {
      const samples = [];
      let frame;
      const sample = () => {
        samples.push(selectors.map(selector => {
          const rect = document.querySelector(selector)?.getBoundingClientRect();
          return rect && [rect.y, rect.height];
        }));
        frame = requestAnimationFrame(sample);
      };
      sample();
      return () => {cancelAnimationFrame(frame); return samples;};
    }, layoutSelectors);
    await participant.route('**/ui/refresh?*', async route => {
      await new Promise(resolve => setTimeout(resolve, 300));
      await route.continue();
    });

    await page.setViewportSize({width:1440, height:900});
    await page.goto(base + '/?lang=pt-BR&account=' + alice + '&chat=' + room);
    await menu().click();
    await page.locator('#chat-manage-room').click();
    const destroy = page.locator('#room-destroy-form');
    await page.locator('#room-destroy-open').click();
    await destroy.waitFor();
    if (artifacts) {
      await destroy.screenshot({path:artifacts + '/excluir-sala.png'});
      await page.screenshot({path:artifacts + '/excluir-sala-original.png'});
    }
    await page.keyboard.press('Escape');
    await destroy.waitFor({state:'hidden'});
    assert((await session(room)).active);
    await page.locator('#room-destroy-open').click();
    const oldRevision = await destroy.locator('[name=expectedRevision]').inputValue();
    const path = '/conversations/' + room + '/room-management';
    const changed = await api(path, {action:'topic', value:'Próximo encontro', expectedRevision:oldRevision});
    assert.equal(changed.state, 'submitted');
    const [refused] = await Promise.all([
      page.waitForResponse(r => r.url() === base + '/ui/rooms/manage' && r.request().method() === 'POST'),
      destroy.getByRole('button', {name:'Excluir sala para todos', exact:true}).click()
    ]);
    assert.equal(refused.status(), 409, 'a stale confirmation was refused');
    assert((await session(room)).active);
    await destroy.waitFor({state:'hidden'});
    await until(async () => await destroy.locator('[name=expectedRevision]').inputValue() !== oldRevision, 'new room controls');
    await page.locator('#room-destroy-open').click();
    await destroy.getByRole('button', {name:'Excluir sala para todos', exact:true}).click();
    await until(async () => (await session(room)).destroyed, 'owner deletion confirmed');
    await until(async () => (await session(bobRoom.id)).destroyed, 'second participant received destruction notice');
    await page.waitForURL(url => !url.searchParams.has('chat'), {timeout: 15000});
    assert((await api('/conversations/' + room)).removedFromChats, 'local conversation is retained and marked as removed');
    for (const archived of [false, true]) {
      assert(!(await chats(alice, archived)).some(c => c.id === room), 'confirmed deletion removes the room from the owner’s lists');
    }
    assert((await chats(bob)).some(c => c.id === bobRoom.id && c.roomStatus === 'destroyed'), 'the other participant keeps the deleted room');
    await statusLabel.getByText('Sala excluída', {exact:true}).waitFor();
    await roomRow.locator('[data-room-status-label]').getByText('Sala excluída', {exact:true}).waitFor();
    assert.equal(await participant.locator('#chat-header .oie-chat-avatar .bg-danger').count(), 1);
    assert.equal(await roomRow.locator('.oie-avatar-initials .bg-danger').count(), 1);
    assert.equal(participant.url(), participantURL, 'the participant stays in their conversation');
    await participant.evaluate(() => new Promise(requestAnimationFrame));
    const samples = await sampleLayout.evaluate(stop => stop());
    assert(samples.length > 1);
    for (const sample of samples) {
      sample.forEach((box, i) => {
        assert(box && box.every((value, axis) => Math.abs(value - samples[0][i][axis]) <= 1),
          'the status update moved ' + layoutSelectors[i]);
      });
    }
    if (artifacts) {
      await roomRow.screenshot({path:artifacts + '/estado-sala-excluida.png'});
      await participant.screenshot({path:artifacts + '/estado-sala-excluida-original.png'});
    }
    await participant.setViewportSize({width:390, height:844});
    await statusLabel.getByText('Sala excluída', {exact:true}).waitFor();
    assert(await statusLabel.isVisible());
    assert.deepEqual(pageErrors, []);
    console.log('PASS: delayed invalid creation, form correction, local removal, owner deletion, and live room status with XMPP');
  } finally { await browser.close(); }
})().catch(err => {console.error(err); process.exitCode=1;});
