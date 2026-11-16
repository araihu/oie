const assert = require('node:assert/strict');

async function messageRetractionFlow({base, api, until, send, alicePage, bobPage, alice, bob, aliceConversation, bobConversation, prefix}) {
  const path = conversation => '/api/v1/conversations/' + conversation.id + '/messages';
  const url = (account, conversation, lang) => base + '/?theme=dark&lang=' + lang + '&chat=' + conversation.id + '&account=' + account.id;
  const original = 'Fixture retraction ' + Date.now();
  await alicePage.goto(url(alice, aliceConversation, 'en'));
  await send(alicePage, original);
  let own, received;
  await until(async () => {
    own = (await api(path(aliceConversation))).items.find(m => m.originalText === original && m.canRetract);
    received = (await api(path(bobConversation))).items.find(m => m.originalText === original && !m.isOwn);
    return own && received;
  }, 'both accounts retained the original over XMPP');

  await alicePage.reload();
  await alicePage.waitForFunction(() => window.OieRuntime?.generation > 0);
  const row = alicePage.locator('[data-message-id="' + own.id + '"]');
  const composer = alicePage.locator('#message-composer');
  await composer.fill('Unsent draft');
  const open = async () => {
    const trigger = row.getByRole('button', {name: 'Message actions', exact: true});
    await trigger.focus();
    if (await trigger.getAttribute('aria-expanded') !== 'true') await trigger.press('Enter');
    await row.getByRole('menuitem', {name: 'Retract message', exact: true}).press('Enter');
  };
  const dialog = alicePage.getByRole('dialog', {name: 'Retract message', exact: true});
  await open();
  await dialog.waitFor();
  await alicePage.keyboard.press('Escape');
  await dialog.waitFor({state: 'hidden'});
  assert.equal((await api('/api/v1/messages/' + own.id)).originalText, original);

  const endpoint = '**/ui/messages/retract';
  let release, started;
  const gate = new Promise(resolve => { release = resolve; });
  const pending = new Promise(resolve => { started = resolve; });
  const delay = async route => {
    started();
    await gate;
    await route.continue();
  };
  await alicePage.route(endpoint, delay);
  const response = alicePage.waitForResponse(result => result.url().endsWith('/ui/messages/retract'));
  try {
    await composer.evaluate(async () => {
      await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    });
    const before = await composer.boundingBox();
    await open();
    await dialog.getByRole('button', {name: 'Retract message', exact: true}).click();
    await pending;
    await dialog.getByText('Requesting retraction…', {exact: true}).waitFor();
    const frames = await alicePage.evaluate(async () => {
      const positions = [];
      for (let i = 0; i < 8; i++) {
        await new Promise(requestAnimationFrame);
        const rect = document.querySelector('#message-composer')?.getBoundingClientRect();
        positions.push(rect ? [rect.x, rect.y, rect.width, rect.height] : null);
      }
      return positions;
    });
    for (const frame of frames) {
      assert(frame?.every((value, i) => Math.abs(value - [before.x, before.y, before.width, before.height][i]) <= 1), JSON.stringify({before, frame}));
    }
    assert.equal(await composer.inputValue(), 'Unsent draft');
  } finally {
    release();
  }
  const result = await response;
  assert.equal(result.status(), 200, await result.text());
  await dialog.waitFor({state: 'hidden'});
  await alicePage.unroute(endpoint, delay);
  await row.getByText('Message retracted', {exact: true}).waitFor();
  await until(async () => (await api('/api/v1/messages/' + received.id)).retraction, 'verified XMPP receipt of the retraction');
  assert.equal(await composer.inputValue(), 'Unsent draft');

  await bobPage.goto(url(bob, bobConversation, 'pt-BR'));
  const incoming = bobPage.locator('[data-message-id="' + received.id + '"]');
  await incoming.getByText('Mensagem retraída', {exact: true}).waitFor();
  assert.equal(await incoming.getByText(original, {exact: true}).count(), 0);
  const reveal = incoming.getByText('Revelar original', {exact: true});
  await reveal.click();
  await incoming.getByText(original, {exact: true}).first().waitFor();
  await incoming.screenshot({path: 'artifacts/' + prefix + '-retracted-original.png'});
  await reveal.click();
  await incoming.locator('[data-retracted-content]').waitFor({state: 'detached'});
  await bobPage.goto(base + '/search?lang=pt-BR&accountId=' + bob.id + '&q=' + encodeURIComponent(original));
  assert.equal(await bobPage.locator('[data-search-message="' + received.id + '"]').count(), 0);
  await bobPage.getByRole('checkbox', {name: 'Incluir retraídas', exact: true}).check();
  await bobPage.locator('[data-search-message="' + received.id + '"]').getByText(original, {exact: true}).waitFor();
}

module.exports = {messageRetractionFlow};
