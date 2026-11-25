const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const vm = require('node:vm');

function savedHistory() {
  const worker = navigator.serviceWorker.controller;
  if (!worker) return null;
  const {port1, port2} = new MessageChannel();
  return new Promise(resolve => {
    const finish = value => { clearTimeout(timeout); port1.close(); resolve(value); };
    const timeout = setTimeout(() => finish(null), 1000);
    port1.onmessage = ({data}) => finish(data);
    worker.postMessage({type: 'oie:offline-history-read'}, [port2]);
  });
}

async function checkNavigationAuthority(id) {
  const engine = id(1), other = id(2);
  const saved = {engine, checkpoint: engine + ':1', entries: [{chat: id(8), cursor: engine + ':1', savedAt: Date.now()}]};
  const source = await fs.readFile(path.join(__dirname, '../../internal/adapters/web/offline-worker.js'), 'utf8');
  const handlers = new Map();
  const requests = new Map();
  const notices = [];
  const sandbox = vm.createContext({
    self: {
      addEventListener: (type, handler) => handlers.set(type, handler),
      location: {origin: 'http://test'},
      clients: {matchAll: async () => [{url: 'http://test/', postMessage: notice => notices.push(notice)}]},
    },
    URL, Response, TextEncoder, caches: {match: async () => null},
    fetch: request => new Promise((resolve, reject) => requests.set(request.key, {resolve, reject})),
  });
  vm.runInContext(source, sandbox);
  let current = saved;
  sandbox.state = async update => current = update?.(current) ?? current;
  const send = (key, chat) => sandbox.workspace({key, signal: {aborted: false}},
    new URL('http://test/' + (chat ? '?chat=' + chat : '')));
  const ok = identity => new Response('workspace', {headers: {
    'X-Oie-Offline-History': identity, 'X-Oie-Offline-Cursor': identity + ':1',
  }});
  const denied = () => new Response('denied', {status: 503, headers: {'X-Oie-Offline-History': 'denied'}});

  const earlierSuccess = send('old success');
  const laterDenial = send('recent denial');
  requests.get('recent denial').resolve(denied());
  await laterDenial;
  requests.get('old success').resolve(ok(engine));
  await earlierSuccess;
  assert(current.blocked && !current.entries.length, 'an earlier verification cannot reopen a denied scope');

  current = saved;
  const earlierDenial = send('known denial');
  const laterOutage = send('outage');
  requests.get('known denial').resolve(denied());
  await earlierDenial;
  requests.get('outage').reject(new TypeError('offline'));
  await laterOutage;
  assert(current.blocked && !current.entries.length, 'a newer network outage cannot conceal a confirmed denial');

  current = saved;
  const lateMissing = send('missing chat', id(8));
  const otherChat = send('different chat', id(9));
  requests.get('different chat').resolve(ok(engine));
  await otherChat;
  requests.get('missing chat').resolve(new Response('missing', {status: 404, headers: {
    'X-Oie-Offline-History': engine, 'X-Oie-Offline-Cursor': engine + ':1',
  }}));
  await lateMissing;
  assert.equal(current.entries.length, 0, 'a disjoint newer success preserves the authority of an older scoped 404');

  current = saved;
  current = sandbox.invalidate(current, {engine, chat: id(9), through: engine + ':3', confirmedMissing: true});
  assert.equal(current.checkpoint, engine + ':1', 'another chat’s 404 cannot certify an unobserved event gap');
  assert.equal(sandbox.progress(current, {engine, through: engine + ':4'}), undefined, 'an isolated event cannot certify an earlier gap');
  const afterGap = sandbox.verify(current, engine, engine + ':4');
  assert.equal(afterGap.entries.length, 0, 'the unobserved gap still invalidates older history');
  assert.equal(afterGap.checkpoint, engine + ':4');

  current = saved;
  let reset;
  handlers.get('fetch')({
    request: {key: 'reset', method: 'POST', url: 'http://test/ui/engine/reset'},
    respondWith: promise => { reset = promise; },
  });
  const replacement = send('new engine');
  requests.get('new engine').resolve(ok(other));
  await replacement;
  current = {...current, entries: [{cursor: other + ':1'}]};
  requests.get('reset').resolve(new Response(null, {status: 204, headers: {'X-Oie-Reset-Engine': engine}}));
  await reset;
  assert.equal(current.engine, other);
  assert.equal(current.entries.length, 1, 'the delayed result of the old reset preserves the new engine');
  assert.equal(notices.length, 1);
  assert.equal(notices[0].engine, engine, 'the already open shells receive the reset of the old engine');

  current = sandbox.verify({...saved, byteLimit: 512 * 1024}, engine, 'invalid');
  assert.equal(current.engine, '');
  assert.equal(sandbox.storageLimit(current), 512 * 1024);
  handlers.get('fetch')({
    request: {key: 'unbound reset', method: 'POST', url: 'http://test/ui/engine/reset'},
    respondWith: promise => { reset = promise; },
  });
  requests.get('unbound reset').resolve(new Response(null, {status: 204, headers: {'X-Oie-Reset-Engine': engine}}));
  await reset;
  assert.equal(sandbox.storageLimit(current), 1024 * 1024, 'reset restores the default with an unbound cache');

  const account = id(20), otherAccount = id(21), chat = id(22);
  const through = engine + ':2';
  const snapshot = (account, chat, size = 0) => ({
    engine, cursor: through, entry: {account, chat, title: 'Fixture', messages: [{text: 'a'.repeat(size)}]},
  });
  const keep = (cache, account, chat, size) =>
    sandbox.keepChat(cache, {engine, account, chat, keep: true, snapshot: snapshot(account, chat, size)});
  let cache = keep({engine, entries: []}, account, chat).next;
  cache.entries[0].savedAt = 0;
  for (let n = 0; n < 15; n++) cache = sandbox.save(cache, snapshot(otherAccount, id(50 + n)));
  assert.equal(cache.entries.length, 12, 'the oldest kept chat still uses a slot in the shared cache');
  assert(cache.entries.some(entry => entry.chat === chat && entry.account === account), 'kept history survives age and count eviction');
  assert(!sandbox.isKept(cache, {account: otherAccount, chat}), 'another account cannot inherit the setting');
  assert.equal(sandbox.keepChat(cache, {engine: other, account, chat, keep: false}).error, 'unavailable');
  const afterUnkeep = sandbox.keepChat(cache, {engine, account, chat, keep: false}).next;
  assert(!afterUnkeep.entries.some(entry => entry.chat === chat), 'unkeeping expired history restores normal eviction');

  const invalidated = sandbox.invalidate(cache, {engine, account, chat, through: engine + ':3'});
  assert(!invalidated.entries.some(entry => entry.chat === chat), 'kept text is invalidated by a history change');
  assert(sandbox.isKept(invalidated, {account, chat}), 'the preference survives an update to the history');
  const removed = sandbox.invalidate(invalidated, {engine, account, chat, through: engine + ':4', removeKeep: true});
  assert(!sandbox.isKept(removed, {account, chat}), 'authoritative removal clears the preference');
  const delayed = sandbox.save(removed, {...snapshot(account, chat), cursor: engine + ':4'});
  assert(!delayed.entries.some(entry => entry.chat === chat), 'a same-cursor snapshot cannot restore a removed chat');
  assert(!sandbox.isKept(sandbox.verify(cache, other, other + ':1'), {account, chat}), 'engine replacement clears the old preference');

  const full = size => {
    let current = {engine, entries: []};
    const count = size ? 5 : 12;
    for (let n = 0; n < count; n++) current = keep(current, account, id(70 + n), size).next;
    assert.equal(keep(current, account, chat, size).error, 'full');
    assert.equal(sandbox.save(current, snapshot(account, chat, size)).entries.length, count);
    return current;
  };
  const twelve = full(0);
  const afterMissedRemovals = sandbox.verify(twelve, engine, engine + ':3');
  assert.equal(afterMissedRemovals.entries.length, 0);
  const newSnapshot = {...snapshot(otherAccount, chat), cursor: engine + ':3'};
  const newKeep = sandbox.keepChat(afterMissedRemovals, {engine, account: otherAccount, chat, keep: true, snapshot: newSnapshot});
  assert(newKeep.next.entries.some(item => item.chat === chat), 'preferences lacking snapshots do not consume cache slots');
  const fullBytes = full(190 * 1024);
  const raised = sandbox.resizeStorage(fullBytes, 2 * 1024 * 1024).next;
  assert(keep(raised, account, chat, 190 * 1024).next, 'raising the default budget makes a refused copy fit');
  assert.equal(sandbox.storageLimit(sandbox.verify(raised, other, other + ':3')), 2 * 1024 * 1024,
    'the browser limit survives a change of engine');
  assert.equal(sandbox.resizeStorage(fullBytes, 256 * 1024).excess,
    sandbox.totalBytes(fullBytes.entries) - 256 * 1024, 'refusal reports the exact protected excess');
  const visibleSmall = sandbox.save(fullBytes, snapshot(account, chat, 0));
  const newerLarge = {...snapshot(account, chat, 190 * 1024), cursor: engine + ':4'};
  const enable = (cache, snapshot) => sandbox.keepChat(cache, {engine, account, chat, keep: true, snapshot});
  const declined = enable(visibleSmall, newerLarge);
  assert.equal(declined.error, 'full', 'a small cached copy does not mask an oversized visible page');
  assert(!sandbox.isKept(declined.next, {account, chat}));
  const staleOrdinary = sandbox.save(visibleSmall, newerLarge);
  assert.equal(enable(staleOrdinary).error, 'full', 'a history-only view cannot protect a known stale snapshot');
  assert.equal(enable(staleOrdinary, snapshot(account, chat, 0)).error, 'full', 'an older mounted page cannot mask the failed refresh');
  const afterStaleSave = sandbox.save(staleOrdinary, snapshot(account, chat, 0));
  assert.equal(enable(afterStaleSave).error, 'full', 'a delayed automatic save preserves the failed refresh cursor');
  const freed = sandbox.keepChat(staleOrdinary, {engine, account, chat: id(70), keep: false}).next;
  const savedNew = enable(freed, newerLarge).next;
  assert.equal(savedNew.entries.find(entry => entry.chat === chat).cursor, newerLarge.cursor);
  const newerCached = sandbox.save({engine, entries: []}, {...snapshot(account, chat, 31), cursor: engine + ':6'});
  const pinFromOldTab = enable(newerCached, snapshot(account, chat, 0)).next;
  assert.equal(pinFromOldTab.entries[0].messages[0].text.length, 31, 'pinning from another tab preserves the newer saved message');

  const small = keep(fullBytes, account, chat, 0).next;
  const oversized = sandbox.save(small, snapshot(account, chat, 190 * 1024));
  assert.equal(oversized.entries.find(entry => entry.chat === chat).messages[0].text, '', 'a growing snapshot preserves the earlier copy when it cannot fit');
  const scope = {account, chat};
  const status = cache => ({...sandbox.keepStatus(cache, scope)});
  assert.deepEqual(status(oversized), {kept: true, available: true, full: true});
  const invalidatedFull = sandbox.invalidate(oversized, {...scope, engine, through: engine + ':3'});
  const grown = {...snapshot(account, chat, 190 * 1024), cursor: engine + ':3'};
  const skipped = sandbox.save(invalidatedFull, grown);
  assert.deepEqual(status(skipped), {kept: true, available: false, full: true});
  const released = sandbox.keepChat(skipped, {engine, account, chat: id(70), keep: false}).next;
  const recovered = sandbox.save(released, grown);
  assert.deepEqual(status(recovered), {kept: true, available: true, full: false});

  current = small;
  const automatic = {...snapshot(account, chat, 190 * 1024), type: 'oie:offline-history-save'};
  const dispatch = async (data, port) => {
    let work;
    handlers.get('message')({source: {type: 'window', url: 'http://test/', id: 'tab'}, data,
      ports: port ? [port] : [], waitUntil: promise => { work = promise; }});
    await work;
  };
  const noticeCount = notices.length;
  await dispatch(automatic);
  assert.equal(notices.length, noticeCount + 1);
  assert.equal(notices.at(-1).full, true, 'the first failed refresh tells the active chat');
  await dispatch(automatic);
  assert.equal(notices.length, noticeCount + 1, 'repeated failures do not repeat notifications');
  await dispatch({...snapshot(account, chat), type: 'oie:offline-history-save'});
  assert.equal(notices.at(-1).full, false, 'a later refresh clears the capacity warning');

  const actualState = sandbox.state;
  const failNextWrite = () => {
    sandbox.state = async update => {
      if (!update) return actualState();
      sandbox.state = actualState;
      throw Object.assign(new Error('device full'), {name: 'QuotaExceededError'});
    };
  };
  failNextWrite();
  await dispatch(automatic);
  assert.deepEqual(status(current), {kept: true, available: true, full: true}, 'device quota preserves the previous protected copy and marks refresh failure');
  failNextWrite();
  let refusal;
  await dispatch({type: 'oie:offline-keep-chat', engine, account, chat: id(99), keep: true}, {
    postMessage: value => { refusal = value; }, close() {},
  });
  assert.equal(refusal.error, 'full', 'an IndexedDB quota abort reports the device capacity failure');
  assert.equal(refusal.kept, false, 'a rolled-back keep is not reported as saved');
}

async function changeKeep(page, control, until, desired, refused = false) {
  await page.locator('label:has([data-keep-chat-offline])').click({trial: true});
  await until(() => control.isEnabled(), 'offline control ready');
  await control.focus();
  await page.evaluate(() => {
    const post = ServiceWorker.prototype.postMessage;
    const selectors = ['label:has([data-keep-chat-offline])', '#details-body', '#message-composer', '#message-timeline'];
    window.keepPaints = [];
    let active = true;
    const sample = () => {
      if (!active) return;
      window.keepPaints.push(selectors.map(selector => {
        const node = document.querySelector(selector);
        const {x, y, width, height} = node.getBoundingClientRect();
        return [x, y, width, height, node.scrollTop];
      }).flat());
      requestAnimationFrame(sample);
    };
    sample();
    ServiceWorker.prototype.postMessage = function (data, transfer) {
      if (data.type !== 'oie:offline-keep-chat') return post.call(this, data, transfer);
      window.releaseKeep = () => {
        ServiceWorker.prototype.postMessage = post;
        post.call(this, data, transfer);
      };
    };
    window.stopKeepPaints = () => { active = false; return window.keepPaints; };
  });
  await control.press('Space');
  await until(() => page.evaluate(() => Boolean(window.releaseKeep) && window.keepPaints.length > 4), 'painted pending keep');
  assert.equal(await control.isChecked(), !desired, 'the toggle waits for durable storage');
  assert.equal(await control.getAttribute('aria-disabled'), 'true');
  await page.evaluate(() => { window.releaseKeep(); delete window.releaseKeep; });
  await until(async () => await control.getAttribute('aria-disabled') !== 'true', 'confirmed offline preference');
  assert.equal(await control.isChecked(), refused ? !desired : desired);
  assert(await control.evaluate(node => node === document.activeElement), 'focus stays on the toggle');
  const paints = await page.evaluate(() => window.stopKeepPaints());
  for (const frame of paints) for (const [index, coordinate] of frame.entries()) {
    assert(Math.abs(coordinate - paints[0][index]) <= 1, 'pending offline preference preserves painted geometry');
  }
}

function editSavedHistory({age, kept} = {}) {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open('oie.offline-history');
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const tx = open.result.transaction('history', 'readwrite');
      const store = tx.objectStore('history');
      const read = store.get('current');
      read.onsuccess = () => {
        const value = read.result;
        if (age) for (const entry of value.entries) entry.savedAt = 0;
        if (kept) {
          const template = value.entries[0];
          value.kept = kept.map(scope => ({...scope, cursor: value.checkpoint}));
          value.entries = value.kept.map(scope => ({...template, ...scope}));
        }
        store.put(value, 'current');
      };
      tx.oncomplete = () => { open.result.close(); resolve(); };
      tx.onabort = () => reject(tx.error);
    };
  });
}

// Use the ordinary snapshot writer so search sees the same account and message
// identities that the offline timeline uses.
async function saveSecondChat(page, until, {account, chat, messageID, emojiID, text}) {
  const {engine, checkpoint} = await page.evaluate(savedHistory);
  const entry = {account, chat, title: 'Mira', label: 'Work', messages: [{
    id: messageID, sender: 'Mira', date: '2026-11-06T17:00:00Z', text, own: false,
  }, {id: emojiID, sender: 'Mira', date: '2026-11-06T18:00:00Z', text: '🧪'.repeat(200), own: false}]};
  await page.evaluate(data => navigator.serviceWorker.controller.postMessage(data), {
    type: 'oie:offline-history-save', engine, cursor: checkpoint, entry,
  });
  await until(async () => (await page.evaluate(savedHistory))?.entries.some(item =>
    item.account === account && item.chat === chat), 'second account’s chat saved for offline search');
}

async function checkSavedSearch(page, until, {root, account, chat, latest, withdrawn, draft, second, missing}) {
  const query = page.locator('#offline-query');
  const results = page.locator('#offline-results');
  const links = results.getByRole('link');
  const empty = await page.locator('#offline-search').getAttribute('data-empty');
  const status = page.locator('#offline-search-status');
  const search = async (text, count) => {
    await query.fill(text);
    await until(async () => await links.count() === count &&
      (count > 0 || (await status.textContent()) === (text ? empty : '')), 'saved search results for ' + text);
  };
  await query.waitFor();
  const requests = [];
  const record = request => requests.push(request.url());
  page.on('request', record);
  try {
    await search('Texto literal', 2);
    await search('<b>', 1);
    assert((await links.innerText()).includes('<b>'), 'message markup in search results is literal text');
    assert.equal(await links.locator('mark').textContent(), '<b>');
    assert.equal(await links.locator('b').count(), 0);
    for (const length of [129, 200]) {
      const emoji = '🧪'.repeat(length);
      await search(emoji, 1);
      await until(async () => (await links.locator('mark').textContent()) === emoji, 'full Unicode search is highlighted');
    }
    const tooLong = await page.locator('#offline-search').getAttribute('data-too-long');
    await query.fill('a'.repeat(201));
    await until(async () => (await status.textContent()) === tooLong, 'oversized query explains the length limit');
    assert.equal(await links.count(), 0);
    await search(draft, 0);
    await search('Texto literal', 2);
    await search(withdrawn, 0);
    await search('Texto literal', 2);
    await search('', 0);
    await page.locator('#offline-conversations').getByRole('link', {name: /Mira/}).waitFor();
    assert.deepEqual(requests, [], 'typing and clearing a saved message search makes no network requests');
  } finally {
    page.off('request', record);
  }

  await search('Texto literal', 2);
  await links.filter({hasText: second.text}).click();
  const target = page.locator('#offline-messages li:focus');
  await target.getByText(second.text, {exact: true}).waitFor();
  const source = new URL(page.url()).searchParams;
  assert.equal(source.get('account'), second.account);
  assert.equal(source.get('chat'), second.chat);
  assert.equal(source.get('message'), second.messageID);
  await page.goBack();
  await until(async () => await links.count() === 2, 'Back restores offline search results');
  assert.equal(await query.inputValue(), 'Texto literal');

  const native = new URL('/search', root);
  native.search = new URL(root).search;
  native.searchParams.set('q', 'Texto literal');
  native.searchParams.append('accountIds', second.account);
  native.searchParams.append('accountIds', missing);
  await page.goto(native.href);
  await until(async () => await links.count() === 1, 'offline native search preserves multiple selected accounts');
  assert.equal(await query.inputValue(), 'Texto literal');
  assert((await links.innerText()).includes(second.text));
  native.searchParams.set('nativeAccountId', account);
  native.searchParams.set('conversationId', chat);
  await page.goto(native.href);
  await until(async () => await links.count() === 1, 'native picker and conversation scope restored');
  assert((await links.innerText()).includes('queda'));
  native.searchParams.set('engineId', missing);
  await page.goto(native.href);
  await page.locator('#offline-empty').waitFor();
  assert.equal(await query.isVisible(), false, 'a foreign engine cannot search this device’s saved messages');

  await page.goto(root + '&chat=' + chat + '&searchMode=messages&searchQuery=Texto+literal&searchAccount=' + second.account);
  await until(async () => await links.count() === 1, 'compact search URL restores its own account filters');
  assert((await links.innerText()).includes(second.text));
  await page.goto(root + '&chat=' + chat + '&chatSearch=1&chatQuery=Texto+literal');
  await until(async () => await links.count() === 1, 'in-chat search URL restores its query and scope');
  assert((await links.innerText()).includes('queda'));
  await page.goto(root + '&account=' + account);
  await search('Texto literal', 1);
  assert((await links.innerText()).includes('queda'), 'account-scoped search excludes other accounts');
  await page.goto(root + '&chat=' + chat);
  await search('Texto literal', 1);
  assert((await links.innerText()).includes('queda'), 'in-chat search excludes other chats');
  await search('', 0);
  await page.locator('#offline-messages').getByText(latest, {exact: true}).waitFor();
}

async function checkStorageLimit({browser, base, account, chat, id, until}) {
  const context = await browser.newContext({viewport: {width: 390, height: 760}});
  const separate = await browser.newContext();
  const settingsURL = base + '/?lang=en&inbox=1&open=settings&settingsTab=storage';
  async function openSettings(profile) {
    const page = await profile.newPage();
    await page.goto(settingsURL);
    const select = page.locator('#offline-storage-limit');
    await select.scrollIntoViewIfNeeded();
    await until(() => select.isEnabled(), 'offline storage usage loaded');
    return page;
  }
  try {
    const online = await context.newPage();
    await online.goto(base + '/?lang=en&chat=' + chat);
    await until(async () => (await online.evaluate(savedHistory))?.entries.length, 'first chat saved');
    const settings = await openSettings(context);
    const shellRequest = '**/ui/offline-history?lang=*';
    await separate.route(shellRequest, route => route.abort('failed'));
    const other = await separate.newPage();
    await other.goto(settingsURL);
    const otherLimit = other.locator('#offline-storage-limit');
    await otherLimit.scrollIntoViewIfNeeded();
    const otherFeedback = other.locator('[data-offline-storage] [role=status]');
    await until(async () => (await otherFeedback.textContent()).includes('Offline storage is unavailable'),
      'failed worker installation reports the error');
    assert(await otherLimit.isDisabled());
    await separate.unroute(shellRequest);
    await other.locator('[aria-labelledby=settingsTitle] [data-drawer-back]').click();
    // Return after the browser has observed the device panel leaving the viewport.
    await otherLimit.evaluate(node => new Promise(resolve => {
      const observer = new IntersectionObserver(([entry]) => {
        if (entry.isIntersecting) return;
        observer.disconnect();
        resolve();
      });
      observer.observe(node);
    }));
    await other.locator('[data-settings-target=storage]:visible').click();
    await otherLimit.scrollIntoViewIfNeeded();
    await until(() => otherLimit.isEnabled(), 'returning to device settings retries a failed worker installation');
    const select = settings.locator('#offline-storage-limit');
    const {engine, checkpoint} = await online.evaluate(savedHistory);
    const protectedChat = id(810), oldest = id(811), newest = id(812);
    for (const [n, conversation] of [oldest, protectedChat, newest].entries()) {
      const entry = {account, chat: conversation, title: 'Saved ' + n,
        messages: [{id: id(820 + n), sender: 'Bob', date: '2026-11-07T20:00:00Z', text: 'needle' + n + ' ' + 'x'.repeat(100 * 1024)}]};
      await online.evaluate(data => navigator.serviceWorker.controller.postMessage(data), {
        type: 'oie:offline-history-save', engine, cursor: checkpoint, entry,
      });
      await until(async () => (await online.evaluate(savedHistory)).entries.some(e => e.chat === conversation), 'saved test conversation');
    }
    const pin = (conversation, keep) => online.evaluate(data => new Promise(resolve => {
      const channel = new MessageChannel();
      channel.port1.onmessage = event => resolve(event.data.kept);
      navigator.serviceWorker.controller.postMessage(data, [channel.port2]);
    }), {type: 'oie:offline-keep-chat', engine, account, chat: conversation, keep});
    for (const conversation of [oldest, protectedChat, newest]) assert(await pin(conversation, true));
    const feedback = settings.locator('[data-offline-storage] [role=status]');
    await select.selectOption('262144');
    await until(async () => (await feedback.textContent()).includes('Free at least'), 'refused limit shows space to free');
    assert.equal(await select.inputValue(), '1048576');
    assert.equal((await online.evaluate(savedHistory)).entries.filter(e => [oldest, protectedChat, newest].includes(e.chat)).length, 3);
    await pin(oldest, false);
    await until(async () => (await feedback.textContent()) === '', 'another tab freed enough protected space');
    assert.equal(await select.inputValue(), '1048576', 'freeing space does not apply a previously refused limit');
    await pin(newest, false);

    const composer = online.locator('#message-composer');
    await composer.fill('Send when connected');
    const draftTab = await context.newPage();
    await draftTab.goto(base + '/?lang=en&chat=' + chat);
    const draft = draftTab.locator('#message-composer');
    await draft.fill('An unsent draft');
    await context.setOffline(true);
    await online.locator('[data-send-message]').click();
    await online.locator('[data-pending-send]').getByRole('img', {name: 'Waiting for connection'}).waitFor();
    await select.selectOption('262144');
    await until(async () => await select.inputValue() === '262144', 'lower limit saved');
    await until(async () => !(await online.evaluate(savedHistory)).entries.some(e => e.chat === oldest), 'oldest ordinary copy evicted');
    await other.reload();
    await otherLimit.scrollIntoViewIfNeeded();
    await until(() => otherLimit.isEnabled(), 'independent profile reloaded its own limit');
    assert.equal(await otherLimit.inputValue(), '1048576');
    const stored = await online.evaluate(({engine, account, chat}) => {
      const read = kind => JSON.parse(localStorage.getItem([kind, engine, account, chat].join(':')));
      return {draft: read('oie.draft.v1'), send: read('oie.offline-send.v1')};
    }, {engine, account, chat});
    assert.equal(stored.draft?.text, 'An unsent draft');
    assert.equal(stored.send?.text, 'Send when connected');
    assert.equal(await draft.inputValue(), 'An unsent draft');
    assert(await online.locator('[data-pending-send]').getByText('Send when connected', {exact:true}).isVisible());

    const reader = await context.newPage();
    await reader.goto(base + '/?lang=en');
    await reader.locator('#offline-query').fill('needle');
    await until(async () => await reader.locator('#offline-results a').count() === 2, 'protected and newest copies remain searchable');
    const text = await reader.locator('#offline-results').innerText();
    assert(text.includes('needle1') && text.includes('needle2') && !text.includes('needle0'));
  } finally {
    await context.close();
    await separate.close();
  }
}

module.exports = async function checkOfflineHistory({browser, base, account, chat, state, until, id}) {
  await checkNavigationAuthority(id);
  const artifacts = path.join(__dirname, 'artifacts');
  await fs.mkdir(artifacts, {recursive: true});
  const latest = 'A mensagem chegou antes da queda. <b>Texto literal</b>';
  const draft = 'Meu rascunho ainda está aqui.';
  const withdrawn = 'Original retraído que não pode aparecer sem conexão.';
  const missing = id(999);
  const second = {account: id(310), chat: id(311), messageID: id(312), emojiID: id(313), text: 'Texto literal do outro perfil.'};
  const messages = [
    {id: id(101), revisionId: id(201), originalText: withdrawn, createdAt: '2026-11-07T19:00:00Z',
      retraction: {at: '2026-11-07T19:01:00Z', originalAvailable: true}},
    {id: id(102), revisionId: id(202), originalText: latest, createdAt: '2026-11-07T20:00:00Z'},
  ];
  state.messages = messages.map(message => ({
    ...message, conversationId: chat, accountId: account, sender: {id: id(6), displayName: 'Bob'},
    isOwn: false, sendState: 'received',
  }));

  await checkStorageLimit({browser, base, account, chat, id, until});

  for (const [width, lang, theme] of [[390, 'pt-BR', 'light'], [1440, 'en', 'dark']]) {
    const context = await browser.newContext({viewport: {width, height: 760}, locale: lang});
    const timeZone = 'Asia/Tokyo';
    await context.addInitScript(zone => {
      localStorage.setItem('oie.device.time-zone.v1', zone);
    }, timeZone);
    const errors = [];
    const newPage = async () => {
      const page = await context.newPage();
      page.on('pageerror', error => errors.push(error.message));
      return page;
    };
    const root = base + '/?' + new URLSearchParams({lang, theme});
    const route = root + '&chat=' + chat;
    const status = lang === 'pt-BR' ? 'Sem conexão' : 'Offline';
    try {
      let page = await newPage();
      if (width === 1440) {
        await page.goto(base + '/search?' + new URLSearchParams({lang, theme}));
        await page.evaluate(() => { window.openedFromSearch = true; });
        await page.locator('a[data-oie-navigation-item]').filter({hasText: 'Chats'}).filter({visible: true}).click();
        await page.locator('#chat-header h2').getByText('Bob', {exact: true}).waitFor();
        assert(await page.evaluate(() => window.openedFromSearch), 'returning to chats uses fragment navigation');
      } else {
        await page.goto(route);
      }
      await page.locator('#message-composer').fill(draft);
      await until(async () => (await page.evaluate(savedHistory))?.entries.some(entry =>
        entry.messages.some(message => message.text === latest)), 'message cached for offline use');

      const keepLabel = lang === 'pt-BR' ? 'Manter disponível sem conexão' : 'Keep available offline';
      const keepControl = page.getByLabel(keepLabel, {exact: true});
      await page.locator('#chat-header .oie-chat-avatar-trigger').click();
      await page.locator('[data-info-entry=preferences]').click();
      assert.equal(await keepControl.isChecked(), false);
      const observer = await newPage();
      await observer.goto(route + '&open=details&infoView=preferences');
      const observerControl = observer.getByLabel(keepLabel, {exact: true});
      await until(() => observerControl.isEnabled(), 'offline control in the second tab');
      await page.bringToFront();
      await page.evaluate(editSavedHistory, {kept: Array.from({length: 12}, (_, index) => ({account, chat: id(400 + index)}))});
      await changeKeep(page, keepControl, until, true, true);
      const fullNotice = lang === 'pt-BR' ? 'Não há espaço para esta conversa.' : 'Not enough space for this chat.';
      await page.getByText(fullNotice, {exact: false}).waitFor();
      await page.evaluate(editSavedHistory, {kept: []});
      await changeKeep(page, keepControl, until, true);
      await until(() => observerControl.isChecked(), 'offline preference updates across tabs');
      await observer.addInitScript(() => {
        const post = ServiceWorker.prototype.postMessage;
        let first = 0;
        ServiceWorker.prototype.postMessage = function (data, transfer) {
          if (data.type === 'oie:offline-keep-status') {
            if (!first) first = performance.now();
            if (performance.now() - first < 2000) return;
            window.disabledBeforeKeepRetry = document.querySelector('[data-keep-chat-offline]')?.disabled;
            ServiceWorker.prototype.postMessage = post;
          }
          return post.call(this, data, transfer);
        };
      });
      await observer.bringToFront();
      await observer.reload();
      await until(async () => await observerControl.isEnabled() && await observerControl.isChecked(), 'timed-out status read retries');
      assert(await observer.evaluate(() => window.disabledBeforeKeepRetry), 'chat stays disabled until its saved preference is confirmed');
      await observer.close();
      await page.evaluate(editSavedHistory, {age: true});
      await saveSecondChat(page, until, second);

      // The two search clients handle transport errors differently. Exercise each
      // with both a disconnected browser and an unreachable engine.
      for (const browserOffline of [true, false]) {
        await context.setOffline(false);
        let field;
        if (width === 390) {
          await page.goto(route + '&open=details');
          await page.locator('[data-open-conversation-search]').filter({visible: true}).click();
          field = page.locator('#conversation-search-query');
        } else {
          await page.goto(root + '&inbox=1');
          field = page.locator('#conversation-search');
        }
        await field.waitFor();
        if (browserOffline) await context.setOffline(true);
        else state.engineDown = true;
        await field.fill('Texto literal');
        const firstResults = page.locator('#offline-results').getByRole('link');
        await until(async () => await firstResults.count() === (width === 390 ? 1 : 2),
          'ordinary search opens saved messages when the ' + (browserOffline ? 'browser disconnects' : 'engine is down'));
        assert.equal(await page.locator('#offline-query').inputValue(), 'Texto literal');
        state.engineDown = false;
      }

      // A new document can open the inbox and follow a saved chat with no network.
      await page.close();
      await context.setOffline(true);
      page = await newPage();
      await page.goto(root);
      await page.getByText(status, {exact: true}).waitFor();
      await checkSavedSearch(page, until, {root, account, chat, latest, withdrawn, draft, second, missing});
      await page.goto(root);
      await page.locator('#offline-conversations').getByRole('link', {name: /Bob/}).click();
      const timeline = page.locator('#offline-messages');
      await timeline.getByText(latest, {exact: true}).waitFor();
      assert.equal(await timeline.locator('b').count(), 0, 'offline content is rendered as text');
      const expectedTime = await page.evaluate(({lang, timeZone, at}) =>
        new Intl.DateTimeFormat(lang, {dateStyle: 'short', timeStyle: 'short', timeZone}).format(new Date(at)),
        {lang, timeZone, at: messages[1].createdAt});
      assert.equal(await timeline.locator('time').last().textContent(), expectedTime, 'offline uses Oiê’s configured zone');
      assert.equal(await page.getByText(withdrawn, {exact: true}).count(), 0, 'withdrawn original is excluded');
      await timeline.getByText(lang === 'pt-BR' ? 'Mensagem retraída' : 'Message retracted', {exact: true}).waitFor();
      assert.equal(await page.locator('#message-composer').count(), 0);
      assert.equal(await timeline.getByText(draft, {exact: true}).count(), 0);
      assert.equal(await page.evaluate(() => document.documentElement.style.colorScheme), theme);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({path: path.join(artifacts, 'offline-history-' + lang + '.png')});

      if (lang === 'en') await page.evaluate(async () => {
        const cache = await caches.open('oie-offline-shell-v1');
        const key = '/ui/offline-history?lang=en';
        const response = await cache.match(key);
        const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
        doc.getElementById('offline-full').remove();
        await cache.put(key, new Response('<!doctype html>' + doc.documentElement.outerHTML, {headers: response.headers}));
      });
      await page.goto(route + '&open=details&infoView=preferences');
      await timeline.getByText(latest, {exact: true}).waitFor();
      assert.equal(await page.locator('[data-keep-chat-offline]').count(), 0, 'offline details URL opens the read-only chat');
      if (lang === 'en') assert.equal(await page.locator('#offline-full').count(), 0, 'older cached shell still renders the saved messages');
      await page.goto(root + '&chat=' + missing);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a cache miss cannot show a different chat');
      await page.goto(route + '&account=' + missing);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a different account cannot reuse a snapshot');
      await page.goto(route + '&notificationEngine=' + missing);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a different engine cannot reuse a snapshot');

      await page.goto(route);
      await timeline.getByText(latest, {exact: true}).waitFor();
      await context.setOffline(false);
      await page.locator('#offline-retry').click();
      await until(async () => await page.locator('#message-composer').inputValue() === draft, 'draft after offline browsing');
      assert.equal(state.effects, 0, 'reading offline and reconnecting does not send a draft');
      const {engine: activeEngine, checkpoint} = await page.evaluate(savedHistory);
      await page.evaluate(data => navigator.serviceWorker.controller.postMessage(data), {
        type: 'oie:offline-history-invalidate', engine: activeEngine, account: second.account, chat: second.chat,
        through: checkpoint, confirmedMissing: true,
      });
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 1, 'second chat removed');
      await page.locator('#chat-header .oie-chat-avatar-trigger').click();
      await page.locator('[data-info-entry=preferences]').click();
      const reopenedControl = page.getByLabel(keepLabel, {exact: true});
      await until(() => reopenedControl.isChecked(), 'offline preference persists after reopening the browser page');
      await changeKeep(page, reopenedControl, until, false);

      // A reachable BFF can lose its connection to the engine independently.
      await page.goto(root + '&inbox=1');
      await page.locator('#conversation-list a[data-conversation-id="' + chat + '"]').waitFor({state: 'visible'});
      state.engineDown = true;
      await page.locator('#conversation-list a[data-conversation-id="' + chat + '"]').click();
      await timeline.getByText(latest, {exact: true}).waitFor();
      state.engineDown = false;
      await page.locator('#offline-retry').click();
      await page.locator('#chat-header h2').getByText('Bob', {exact: true}).waitFor();

      const stale = await page.evaluate(savedHistory);
      assert.equal(stale.entries.length, 1);
      await page.goto(root + '&inbox=1');
      const searchPeer = await newPage();
      await context.setOffline(true);
      await searchPeer.goto(route);
      await searchPeer.locator('#offline-query').fill('Texto literal');
      const peerResults = searchPeer.locator('#offline-results').getByRole('link');
      await peerResults.waitFor();
      await context.setOffline(false);
      state.chatRemoved = true;
      const missingFragment = page.waitForResponse(response =>
        new URL(response.url()).searchParams.get('chat') === chat &&
        response.request().headers()['hx-request'] === 'true' && response.status() === 404);
      await page.locator('#conversation-list a[data-conversation-id="' + chat + '"]').click();
      await missingFragment;
      await peerResults.waitFor({state: 'detached'});
      assert.equal(await searchPeer.locator('#offline-results').textContent(), '', 'removing a chat clears its active search results');
      const peerQuery = searchPeer.locator('#offline-query');
      if (await peerQuery.isVisible()) await peerQuery.fill('');
      assert.equal(await searchPeer.locator('#offline-messages').getByText(latest, {exact: true}).count(), 0,
        'clearing search after a chat removal cannot reveal the old message');
      await searchPeer.close();
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 0, 'HTMX 404 evicts the chat');
      await page.evaluate(({engine, entries: [entry]}) => navigator.serviceWorker.controller.postMessage({
        type: 'oie:offline-history-save', engine, cursor: entry.cursor, entry,
      }), stale);
      await context.setOffline(true);
      await page.goto(route);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a delayed snapshot cannot restore a confirmed missing chat');

      // A new online document can miss events while every tab was closed.
      state.chatRemoved = false;
      state.seq++;
      await context.setOffline(false);
      await page.goto(route);
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 1, 'chat cached again after a newer snapshot');
      await page.close();
      state.seq++;
      page = await newPage();
      await page.goto(root + '&inbox=1');
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 0, 'unobserved event gap invalidates the old snapshot');

      // Malformed authentication responses are distinct from engine downtime.
      await page.goto(route);
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 1, 'cached before denied response');
      state.engineDenied = true;
      const denied = width === 1440 ? await page.goto(base + '/search?lang=en') : await page.reload();
      assert.equal(denied.status(), 503);
      assert.equal(denied.headers()['x-oie-offline-history'], 'denied');
      await page.locator('#oie-route[data-engine-unavailable]').waitFor();
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 0, 'denied response purges history');

      state.engineDenied = false;
      state.resetEnabled = true;
      await page.goto(route);
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 1, 'chat saved before same-profile reset');
      const peer = await newPage();
      await context.setOffline(true);
      await peer.goto(route);
      await peer.locator('#offline-messages').getByText(latest, {exact: true}).waitFor();
      await peer.locator('#offline-query').fill('Texto literal');
      await peer.locator('#offline-results').getByRole('link').waitFor();
      await context.setOffline(false);
      const engine = (await page.evaluate(savedHistory)).engine;
      const resetStatus = await page.evaluate(async engine => {
        const csrf = document.querySelector('meta[name="oie-csrf"]').content;
        const body = new URLSearchParams({csrf, engineId: engine, confirmation: 'delete'});
        return (await fetch('/ui/engine/reset', {method: 'POST', body})).status;
      }, engine);
      assert.equal(resetStatus, 204);
      await peer.locator('#offline-empty').waitFor();
      assert.equal(await peer.getByText(latest, {exact: true}).count(), 0, 'reset immediately clears already open offline history');
      assert.equal(await peer.locator('#offline-results').getByRole('link').count(), 0, 'reset clears search results in another tab');
      assert.equal(await peer.locator('#offline-query').inputValue(), '', 'reset clears the query in another tab');
      assert.deepEqual(errors, []);
      console.log('PASS: Keep available offline, cache limits, draft recovery and engine outage (' + lang + ', ' + width + ' px)');
    } finally {
      state.engineDown = false;
      state.chatRemoved = false;
      state.engineDenied = false;
      state.resetEnabled = false;
      await context.close();
    }
  }
};
