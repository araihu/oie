const assert = require('node:assert/strict');

module.exports = async function protectedJoin({page, room, state, open, roomControls, until}) {
  const password = 'fixture-room-secret';
  const retryLabel = await page.locator('#oie-live-copy').getAttribute('data-retry');
  const form = page.locator('[data-room-command=join]');
  const retry = form.locator('[data-room-action=join]');

  async function checkLocalStorage() {
    const absent = await page.evaluate(secret =>
      [localStorage, sessionStorage].every(storage =>
        Object.keys(storage).every(key => !key.includes(secret) && !storage.getItem(key).includes(secret))), password);
    assert(absent, 'room password must never enter browser storage');
  }

  async function loseJoinReply(accepted) {
    await open(page, room);
    await roomControls(page);
    await form.locator('[name=nickname]').fill('Protected room nickname');
    await form.locator('[name=password]').fill(password);
    const key = await form.locator('[name=operationKey]').inputValue();
    await page.route('**/ui/rooms/join', async route => {
      if (accepted) await route.fetch();
      await route.abort('connectionreset');
    }, {times: 1});
    await retry.click();
    await until(async () => await retry.isEnabled() && await retry.innerText() === retryLabel, 'uncertain protected join');
    await checkLocalStorage();
    await page.reload();
    await until(async () => await form.count() === 1 && await retry.innerText() === retryLabel, 'protected journal restored');
    await roomControls(page);
    assert.equal(await form.locator('[name=operationKey]').inputValue(), key);
    assert.equal(await form.locator('[name=password]').inputValue(), '');
    await checkLocalStorage();
    return key;
  }

  const effects = state.joinEffects;
  const acceptedKey = await loseJoinReply(true);
  const reads = state.receiptReads;
  await retry.click();
  await until(async () => state.receiptReads > reads && await page.locator('[data-room-command=leave]').count() === 1,
    'accepted protected join recovered from receipt');
  assert.equal(state.joinEffects, effects + 1);
  assert.equal(state.roomBodies.filter(item => item.key === acceptedKey).length, 1, 'no second join write');
  await checkLocalStorage();
  await page.locator('[data-room-action=leave]').click();
  await until(async () => await form.count() === 1, 'left protected fixture room');

  const missingKey = await loseJoinReply(false);
  await retry.click();
  const passwordBox = form.getByLabel('Original group password');
  await passwordBox.waitFor({state: 'visible'});
  assert(await passwordBox.evaluate(input => input.required && !input.disabled && !input.readOnly));
  await retry.click();
  assert.equal(state.roomBodies.filter(item => item.key === missingKey).length, 0, 'empty retry cannot send a join');
  await passwordBox.fill(password);
  await retry.click();
  await until(async () => await page.locator('[data-room-command=leave]').count() === 1, 'join completed with re-entered password');
  const writes = state.roomBodies.filter(item => item.key === missingKey);
  assert.equal(writes.length, 1);
  assert(writes[0].input.password === password, 'original password reached the engine');
  assert.equal(writes[0].input.nickname, 'Protected room nickname');
  assert.equal(state.joinEffects, effects + 2);
  await checkLocalStorage();
};
