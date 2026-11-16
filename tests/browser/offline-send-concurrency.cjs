const assert = require('node:assert/strict');

const composer = page => page.locator('#message-composer');
const pending = page => page.locator('[data-pending-send]');
const waiting = page => pending(page).locator('[role=img][aria-label="Waiting for connection"]');
const submit = page => page.locator('[data-send-message]').click();
const formKey = page => page.locator('#send-message-form input[name=operationKey]').inputValue();
const wake = page => page.evaluate(() => window.dispatchEvent(new Event('online')));

async function openChat(context, url) {
  const page = await context.newPage();
  await page.goto(url);
  await composer(page).waitFor();
  return page;
}

async function checkRetryDuringAutomaticAttempt(browser, url, state) {
  const context = await browser.newContext();
  const release = Promise.withResolvers();
  const before = state.posts || 0;
  try {
    const first = await openChat(context, url);
    const unavailable = route => route.fulfill({status:503, json:{code:'unavailable'}});
    let canLookup = false;
    await first.route('**/ui/messages/reconcile', route => canLookup ? route.continue() : unavailable(route));
    await first.route('**/ui/messages/send', async route => {
      await release.promise;
      await route.fulfill({status:503, json:{code:'engine_unavailable'}});
    });
    await context.setOffline(true);
    const text = 'May be cancelled only after the original attempt stops';
    await composer(first).fill(text);
    await submit(first);
    await waiting(first).waitFor();
    const queuedKey = await pending(first).getAttribute('data-operation-key');
    await context.setOffline(false);

    const second = await context.newPage();
    await second.route('**/ui/messages/reconcile', unavailable);
    await second.goto(url);
    await waiting(second).waitFor();
    const automatic = first.waitForRequest(req => req.url().endsWith('/ui/messages/send'));
    canLookup = true;
    await first.bringToFront();
    await wake(first);
    assert.equal(new URLSearchParams((await automatic).postData()).get('operationKey'), queuedKey);

    // A second page must not present an active transport attempt as a lost one.
    await second.locator('[data-pending-send]:not([data-pending-state=waiting])').waitFor();
    const retry = second.locator('[data-pending-send-retry]');
    assert(!(await retry.isVisible()) || await retry.isDisabled(),
      'a second tab cannot retry while the automatic attempt is in flight');
    const duplicateRequests = [];
    await second.route('**/ui/messages/send', route => {
      duplicateRequests.push(route.request().postData());
      return route.continue();
    });
    await second.locator('#send-message-form').evaluate(form => form.requestSubmit());

    // The BFF refused the only attempt before reaching the engine. No probe can
    // claim the send again while this confirmed outage persists.
    state.engineDown = true;
    release.resolve();
    await waiting(first).waitFor();
    await pending(first).getByRole('button',{name:'Cancel send',exact:true}).click();
    await pending(first).waitFor({state:'detached'});
    assert.equal(await composer(first).inputValue(),text);
    assert.deepEqual(duplicateRequests, [], 'stale form submission cannot start a second attempt');
    assert.equal(state.posts || 0,before);
    assert(!state.operations.has(queuedKey));
  } finally {
    state.engineDown = false;
    release.resolve();
    await context.close();
  }
}

async function checkFrozenIntent(browser, url, state, until) {
  const context = await browser.newContext();
  const resumeReceipts = Promise.withResolvers();
  try {
    await context.route('**/ui/messages/reconcile', async route => {
      await resumeReceipts.promise;
      await route.continue();
    });
    const first = await openChat(context, url);
    const original = 'First explicit offline intent';
    await composer(first).fill(original);
    const second = await context.newPage();
    // Models a suspended tab that still has its older copy of the shared draft.
    await second.addInitScript(() => window.addEventListener('storage', event => event.stopImmediatePropagation(), true));
    await second.goto(url);
    assert.equal(await composer(second).inputValue(),original);
    assert.equal(await formKey(second),await formKey(first));

    await context.setOffline(true);
    await submit(first);
    await waiting(first).waitFor();
    const queuedKey = await pending(first).getAttribute('data-operation-key');
    const edited = 'A stale tab tries a different body';
    await composer(second).fill(edited);
    await context.setOffline(false);
    const posts = [];
    await context.route('**/ui/messages/send', route => {
      posts.push(new URLSearchParams(route.request().postData()));
      return route.continue();
    });
    await second.locator('#send-message-form').evaluate(form => form.requestSubmit());
    resumeReceipts.resolve();
    await wake(first);
    await until(() => state.operations.has(queuedKey), 'frozen offline operation accepted');
    assert.equal(state.operations.get(queuedKey).acceptedMessage.originalText,original);
    assert.deepEqual(posts.filter(post => post.get('operationKey') === queuedKey).map(post => post.get('originalText')), [original]);
    await until(() => state.messages.some(message => message.originalText === edited), 'independent send from the other tab accepted');
    const editedPost = posts.find(post => post.get('originalText') === edited);
    assert(editedPost, 'the second draft was submitted');
    assert.notEqual(editedPost.get('operationKey'),queuedKey);
  } finally {
    resumeReceipts.resolve();
    await context.close();
  }
}

async function checkIndependentDraft(browser, url, state, until) {
  const context = await browser.newContext();
  try {
    const first = await openChat(context, url);
    const message = 'Send this without changing a draft in another tab';
    await composer(first).fill(message);
    const second = await openChat(context, url);
    await composer(second).fill('A draft already being edited in a second tab');
    await context.setOffline(true);
    await submit(first);
    await waiting(first).waitFor();
    const queuedKey = await pending(first).getAttribute('data-operation-key');
    const draft = 'Draft entered in a previously open tab';
    await composer(second).fill(draft);
    await second.close();

    await context.setOffline(false);
    await until(() => state.operations.has(queuedKey), 'offline send accepted before reopening the other draft');
    assert.equal(state.operations.get(queuedKey).acceptedMessage.originalText,message);
    await pending(first).waitFor({state:'detached'});
    const reopened = await openChat(context, url);
    assert.equal(await composer(reopened).inputValue(),draft);
  } finally {
    await context.close();
  }
}

async function checkAcceptedBeforeOutage(browser, url, state, until, offlineFirst = false) {
  const context = await browser.newContext();
  const before = state.posts || 0;
  try {
    const first = await openChat(context, url);
    const message = offlineFirst ? 'Another tab already sent this offline composition' : 'Another tab already sent this composition';
    await composer(first).fill(message);
    const second = await openChat(context, url);
    const sharedKey = await formKey(first);
    assert.equal(await formKey(second),sharedKey);
    assert.equal(await composer(second).inputValue(),message);
    if (offlineFirst) await context.setOffline(true);
    await submit(first);
    if (offlineFirst) {
      await waiting(first).waitFor();
      await first.bringToFront();
      await context.setOffline(false);
      await wake(first);
    }
    await until(() => state.operations.has(sharedKey), 'first tab commits the shared operation');
    await pending(first).waitFor({state:'detached'});

    await second.evaluate(() => {
      window.observedCancellationOfAcceptedSend = false;
      const observer = new MutationObserver(() => {
        const action = document.querySelector('[data-pending-state=waiting] [data-cancel-offline-send]');
        if (action?.getClientRects().length && !action.disabled) window.observedCancellationOfAcceptedSend = true;
      });
      observer.observe(document.body, {subtree:true, childList:true, attributes:true});
    });
    let sendResponse;
    second.on('response', response => {
      if (response.request().method() === 'POST' && response.url().endsWith('/ui/messages/send')) sendResponse = response;
    });
    state.engineDown = true;
    await submit(second);
    await until(async() => Boolean(sendResponse) ||
      await composer(second).inputValue() === '' && await pending(second).count() === 0,
      'the stale composition is handled using the prior acceptance');
    if (sendResponse) {
      assert.equal(sendResponse.status(),503,'a network attempt receives the confirmed engine outage');
      await sendResponse.finished();
    }
    const secondKey = await pending(second).evaluateAll(rows => rows[0]?.dataset.operationKey);
    if (secondKey) assert.equal(secondKey,sharedKey,'a stale tab keeps the operation key if it has to reconcile again');
    state.engineDown = false;
    await wake(second);
    await pending(second).waitFor({state:'detached'});
    assert.equal(await composer(second).inputValue(),'');
    assert(!await second.evaluate(() => window.observedCancellationOfAcceptedSend),
      'a stale tab never offers to cancel the already accepted operation');
    assert.equal(state.posts || 0,before+1, 'the already accepted operation does not create another engine write');
    assert.equal(state.messages.filter(item => item.originalText === message).length,1);
  } finally {
    state.engineDown = false;
    await context.close();
  }
}

async function checkDraftSwitch(browser, url, state) {
  const context = await browser.newContext();
  const before = state.posts || 0;
  try {
    await context.route('**/ui/messages/reconcile', route => route.fulfill({status:503, json:{code:'unavailable'}}));
    const first = await openChat(context, url);
    const queued = 'Return this text when the queued send is canceled';
    const regular = 'Keep this separately edited draft too';
    await composer(first).fill(queued);
    const second = await openChat(context, url);
    await composer(second).fill(regular);
    await context.setOffline(true);
    await submit(first);
    await waiting(first).waitFor();
    const key = await pending(first).getAttribute('data-operation-key');
    await first.close();

    await context.setOffline(false);
    await second.reload();
    assert.equal(await composer(second).inputValue(),regular);
    const showPending = second.locator('[data-draft-switch]');
    await showPending.waitFor();
    await showPending.click();
    await waiting(second).waitFor();
    assert.equal(await pending(second).getAttribute('data-operation-key'),key);
    await pending(second).getByRole('button',{name:'Cancel send',exact:true}).click();
    await pending(second).waitFor({state:'detached'});
    assert.equal(await composer(second).inputValue(),queued);

    const reopened = await openChat(context, url);
    assert.equal(await composer(reopened).inputValue(),regular);
    const switchDraft = reopened.locator('[data-draft-switch]');
    await switchDraft.click();
    assert.equal(await composer(reopened).inputValue(),queued);
    await switchDraft.click();
    assert.equal(await composer(reopened).inputValue(),regular);
    assert.equal(state.posts || 0,before);
    assert(!state.operations.has(key));
  } finally {
    await context.close();
  }
}

async function checkBlockedLockFallback(browser, url, state) {
  const context = await browser.newContext();
  const before = state.posts || 0;
  try {
    await context.addInitScript(() => {
      Object.defineProperty(Navigator.prototype, 'locks', {configurable:true, get:() => undefined});
      Object.defineProperty(window, 'indexedDB', {
        configurable:true,
        get:() => { throw new DOMException('Denied', 'SecurityError'); },
      });
    });
    const page = await openChat(context, url);
    await context.setOffline(true);
    const draft = 'Draft with writable storage and no safe offline claim';
    await composer(page).fill(draft);
    await submit(page);
    await page.getByText('Couldn’t save the message for sending. Your draft is still open.',{exact:true}).waitFor();
    assert.equal(await pending(page).count(),0);
    assert.equal(await composer(page).inputValue(),draft);
    assert(await composer(page).isEditable());
    await context.setOffline(false);
    await page.reload();
    assert.equal(await composer(page).inputValue(),draft);
    assert.equal(state.posts || 0,before);
  } finally {
    await context.close();
  }
}

async function checkCancelAtStorageLimit(browser, url, state) {
  const context = await browser.newContext();
  const before = state.posts || 0;
  try {
    const page = await openChat(context, url);
    await context.setOffline(true);
    const message = 'Return this composition even when the browser storage is full';
    await composer(page).fill(message);
    await submit(page);
    await waiting(page).waitFor();
    const queuedKey = await pending(page).getAttribute('data-operation-key');
    const paddingLength = await page.evaluate(() => {
      const filler = 'fixture.local-storage-padding';
      let fits = 0;
      let exceeds = 10 * 1024 * 1024;
      while (exceeds - fits > 1) {
        const length = Math.floor((fits + exceeds) / 2);
        try {
          localStorage.setItem(filler, 'x'.repeat(length));
          fits = length;
        } catch (error) {
          if (error.name !== 'QuotaExceededError') throw error;
          exceeds = length;
        }
      }
      return fits;
    });
    assert(paddingLength > 0 && paddingLength < 10 * 1024 * 1024 - 1,
      'Chromium enforced a storage quota');

    await pending(page).getByRole('button',{name:'Cancel send',exact:true}).click();
    await pending(page).waitFor({state:'detached'});
    assert.equal(await composer(page).inputValue(),message);
    assert(await composer(page).isEditable());
    await context.setOffline(false);
    await page.reload();
    assert.equal(await composer(page).inputValue(),message);
    assert.equal(state.posts || 0,before);
    assert(!state.operations.has(queuedKey));
  } finally {
    await context.close();
  }
}

module.exports = async function offlineSendConcurrency({browser, base, chat, state, until, findings}) {
  const url = base + '/?chat=' + chat + '&lang=en';
  await checkRetryDuringAutomaticAttempt(browser, url, state);
  findings.push('Other tabs cannot retry an active automatic attempt or cancel a concurrently running send');
  await checkFrozenIntent(browser, url, state, until);
  findings.push('A stale tab cannot replace an offline send with a different body under the original operation key');
  await checkIndependentDraft(browser, url, state, until);
  findings.push('An unsent draft from another tab survives a previous offline send and reopening');
  await checkAcceptedBeforeOutage(browser, url, state, until);
  await checkAcceptedBeforeOutage(browser, url, state, until, true);
  findings.push('A stale tab cannot duplicate or offer to cancel a prior online or offline acceptance during an outage');
  await checkDraftSwitch(browser, url, state);
  findings.push('A separate draft does not hide cancellation, and both drafts are available again after reopening');
  await checkBlockedLockFallback(browser, url, state);
  findings.push('Writable drafts remain available when safe offline queue coordination is denied');
  await checkCancelAtStorageLimit(browser, url, state);
  findings.push('Cancellation still restores the composition when Chromium localStorage is full');
};
