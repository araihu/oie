window.OieRuntime.register('forwarding', life => {
  'use strict';
  const id = 'forward-message-dialog';
  const dialog = document.getElementById(id);
  const body = dialog?.querySelector('[data-forward-body]');
  if (!body) return;

  const engine = document.body.dataset.engineId;
  const journalsKey = 'oie.forwarding-journals:' + engine;
  const legacyJournalsKey = 'forwarding-journals:' + engine;
  let saved;
  let legacy;
  try {
    saved = JSON.parse(sessionStorage.getItem(journalsKey));
    if (!Array.isArray(saved)) legacy = JSON.parse(sessionStorage.getItem(legacyJournalsKey));
  } catch { /* Use the in-memory journal. */ }
  const memory = window.OieRuntime.memory;
  const journals = memory.get(journalsKey) || new Map(Array.isArray(saved) ? saved : []);
  memory.set(journalsKey, journals);
  function persist() {
    if (life.signal.aborted) return;
    try {
      if (journals.size) sessionStorage.setItem(journalsKey, JSON.stringify([...journals]));
      else sessionStorage.removeItem(journalsKey);
      sessionStorage.removeItem(legacyJournalsKey);
    } catch { /* Keep in-memory retries. */ }
  }
  if (Array.isArray(legacy)) {
    const byDestination = new Map();
    for (const [key, receipt] of legacy) {
      const [source, revision, room] = key.split(':');
      const destination = source + ':' + room;
      if (!byDestination.has(destination)) byDestination.set(destination, []);
      byDestination.get(destination).push({key: receipt.key, revision, state: receipt.accepted ? 'accepted' : 'uncertain', selected: true, message: receipt.message});
    }
    for (const [destination, attempts] of byDestination) {
      // An accepted legacy attempt already settles the destination. Other
      // keys remain recorded but are unsafe to retry as additional sends.
      const accepted = attempts.findIndex(receipt => receipt.state === 'accepted');
      const [receipt] = attempts.splice(Math.max(0, accepted), 1);
      if (attempts.length) receipt.remaining = attempts;
      journals.set(destination, receipt);
    }
    persist();
  }

  let opener, request, submitting = false;
  const notify = (tone, message) => window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone, message}}));
  const modal = action => window.dispatchEvent(new CustomEvent('modal:' + action, {detail: {id}}));
  const boxes = form => [...form.querySelectorAll('input[name=conversationId]')];
  const resultNode = box => box.closest('[data-forward-room]').querySelector('[data-forward-result]');
  const receiptKey = (form, box) => form.elements.messageId.value + ':' + box.value;
  const roomReceipt = (form, box) => journals.get(receiptKey(form, box));
  const superseded = (form, receipt) => receipt.revision !== form.elements.revisionId.value &&
    (receipt.state === 'pending' || receipt.state === 'rejected');
  const pending = form => boxes(form).filter(box => box.checked && !box.disabled);
  const updateButton = form => {
    const button = form.querySelector('[data-forward-submit]');
    if (!button) return;
    const selected = pending(form);
    button.setAttribute('aria-disabled', String(submitting || !selected.length));
    const retry = selected.some(box => {
      const receipt = roomReceipt(form, box);
      return receipt && receipt.message && !superseded(form, receipt);
    });
    button.textContent = submitting ? body.dataset.sending : retry ? button.dataset.retry : button.dataset.send;
  };

  async function open(messageId, trigger) {
    if (submitting) return;
    request?.abort();
    const controller = new AbortController();
    request = controller;
    opener = trigger || opener;
    const loading = document.createElement('p');
    loading.role = 'status';
    loading.textContent = body.dataset.loading;
    body.replaceChildren(loading);
    modal('open');
    const params = new URLSearchParams({messageId, engineId: document.body.dataset.engineId, lang: document.documentElement.lang, theme: document.documentElement.dataset.mode || 'system'});
    try {
      const response = await life.fetch('/ui/messages/forward?' + params, {signal: controller.signal, cache: 'no-store'});
      if (!response.ok) throw new Error();
      const page = new DOMParser().parseFromString(await response.text(), 'text/html');
      if (request !== controller || controller.signal.aborted || life.signal.aborted) return;
      const fresh = page.querySelector('[data-forward-form]');
      if (!fresh || fresh.elements.messageId.value !== messageId ||
          fresh.elements.engineId.value !== document.body.dataset.engineId) throw new Error();
      const insert = () => {
        for (const child of body.children) window.Alpine?.destroyTree(child);
        body.replaceChildren(fresh);
        window.Alpine?.initTree(fresh);
      };
      if (window.Alpine?.mutateDom) window.Alpine.mutateDom(insert);
      else insert();
      window.OieTime?.localize(fresh);
      document.dispatchEvent(new Event('oie:forward-content'));
      for (const box of boxes(fresh)) {
        const receipt = roomReceipt(fresh, box);
        if (!receipt || superseded(fresh, receipt)) continue;
        box.checked = receipt.selected;
        box.disabled = receipt.state === 'accepted';
        resultNode(box).textContent = receipt.message;
        resultNode(box).dataset.accepted = String(box.disabled);
      }
      updateButton(fresh);
      fresh.querySelector('[data-forward-search]')?.focus({preventScroll: true});
    } catch {
      if (controller.signal.aborted || life.signal.aborted) return;
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.textContent = body.dataset.retry;
      retry.addEventListener('click', () => open(messageId));
      loading.textContent = body.dataset.failed;
      body.replaceChildren(loading, retry);
      retry.focus({preventScroll: true});
    }
  }

  life.on(document, 'click', event => {
    const action = event.target.closest('[id^="forward-message-"][role="menuitem"]');
    if (!action || action.disabled) return;
    const sourceID = action.id.slice(-36);
    const trigger = action.closest('.oie-message-menu, .oie-shared-menu')?.querySelector('[aria-expanded]');
    event.preventDefault();
    action.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true, cancelable: true}));
    life.raf(() => open(sourceID, trigger));
  });
  life.on(dialog, 'close', () => {
    request?.abort();
    const form = body.querySelector('[data-forward-form]');
    if (!submitting && form && !boxes(form).some(box => {
      const receipt = roomReceipt(form, box);
      return receipt && receipt.state !== 'accepted' && !superseded(form, receipt);
    })) {
      // Dismissing a settled legacy batch acknowledges its displayed receipts.
      for (const box of boxes(form)) {
        const receipt = roomReceipt(form, box);
        if (receipt?.state === 'accepted' && receipt.remaining?.length) journals.delete(receiptKey(form, box));
      }
      persist();
    }
    window.OieVideos?.release(body);
    if (opener?.isConnected) opener.focus({preventScroll: true});
  });
  life.on(body, 'input', event => {
    if (!event.target.matches('[data-forward-search]')) return;
    const term = event.target.value.normalize('NFD').replace(/\p{M}/gu, '').trim().toLocaleLowerCase();
    let visible = 0;
    for (const room of body.querySelectorAll('[data-forward-room]')) {
      const label = (room.querySelector('[data-forward-room-label]').textContent + ' ' + room.dataset.roomAddress).normalize('NFD').replace(/\p{M}/gu, '').toLocaleLowerCase();
      room.hidden = !room.querySelector('input').checked && !label.includes(term);
      if (!room.hidden) visible++;
    }
    body.querySelector('[data-forward-no-results]').hidden = visible !== 0;
  });
  life.on(body, 'change', event => {
    const box = event.target;
    if (box.name !== 'conversationId') return;
    const receipt = roomReceipt(box.form, box);
    if (receipt) {
      receipt.selected = box.checked;
      persist();
    }
    updateButton(box.form);
    box.form.querySelector('[data-forward-search]').dispatchEvent(new Event('input', {bubbles: true}));
  });

  async function submitRoom(form, box) {
    const key = receiptKey(form, box);
    const status = resultNode(box);
    let receipt = journals.get(key);
    while (receipt && !life.signal.aborted) {
      const wasUncertain = receipt.state === 'uncertain';
      receipt.state = 'uncertain';
      receipt.message = body.dataset.unconfirmed;
      persist();
      status.textContent = body.dataset.sending;
      const data = new URLSearchParams();
      for (const name of ['engineId','csrf','messageId']) data.set(name, form.elements[name].value);
      data.set('revisionId', receipt.revision);
      data.set('conversationId', box.value);
      data.set('operationKey', receipt.key);
      try {
        const response = await life.fetch('/ui/messages/forward', {method: 'POST', body: data, headers: {Accept: 'application/json'}});
        const result = await response.json();
        if (life.signal.aborted) return;
        if (response.ok && result.accepted === true) receipt.state = 'accepted';
        else if (result.rejected && (!wasUncertain || result.code === 'source_changed')) {
          receipt.state = 'rejected';
          if (result.code === 'source_changed') {
            const [next, ...remaining] = receipt.remaining || [];
            if (next) {
              next.selected = receipt.selected;
              if (remaining.length) next.remaining = remaining;
              journals.set(key, next);
              receipt = next;
              continue;
            }
            if (superseded(form, receipt)) {
              box.checked = false;
              journals.delete(key);
            } else {
              // The user must reopen the dialog to review the current source.
              box.dataset.forwardStale = '';
            }
          }
        }
        receipt.message = result.message || body.dataset.unconfirmed;
      } catch { /* Keep this room and key available for an exact retry. */ }
      if (life.signal.aborted) return;
      persist();
      status.textContent = receipt.message;
      status.dataset.accepted = String(receipt.state === 'accepted');
      break;
    }
  }

  life.on(body, 'submit', async event => {
    const form = event.target;
    if (!form.matches('[data-forward-form]')) return;
    event.preventDefault();
    if (submitting || !pending(form).length) return;
    submitting = true;
    const selected = pending(form);
    const available = boxes(form).filter(box => !box.disabled);
    for (const box of selected) {
      const key = receiptKey(form, box);
      let receipt = journals.get(key);
      if (!receipt || superseded(form, receipt)) {
        receipt = {key: box.dataset.key, revision: form.elements.revisionId.value, state: 'pending', selected: true, message: ''};
        journals.set(key, receipt);
      }
      receipt.selected = true;
    }
    persist();
    const button = form.querySelector('[data-forward-submit]');
    updateButton(form);
    for (const box of available) box.disabled = true;
    try {
      for (const box of selected) {
        if (life.signal.aborted) return;
        await submitRoom(form, box);
      }
      if (life.signal.aborted) return;
      if (selected.every(box => roomReceipt(form, box)?.state === 'accepted')) {
        for (const box of boxes(form)) {
          const receipt = roomReceipt(form, box);
          if (receipt?.state === 'accepted') journals.delete(receiptKey(form, box));
        }
        persist();
        if (dialog.open) modal('close');
        notify('success', body.dataset.complete);
        document.dispatchEvent(new CustomEvent('oie:refresh-request'));
      } else if (dialog.open) {
        button.focus({preventScroll: true});
      } else notify('danger', body.dataset.unconfirmed);
    } finally {
      submitting = false;
      for (const box of available) box.disabled = Object.hasOwn(box.dataset, 'forwardStale') || roomReceipt(form, box)?.state === 'accepted';
      updateButton(form);
    }
  });
});
