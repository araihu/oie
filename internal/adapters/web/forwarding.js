window.OieRuntime.register('forwarding', life => {
  'use strict';
  const id = 'forward-message-dialog';
  const dialog = document.getElementById(id);
  const body = dialog?.querySelector('[data-forward-body]');
  if (!body) return;

  const journalsKey = 'forwarding-journals:' + document.body.dataset.engineId;
  let saved;
  try { saved = JSON.parse(sessionStorage.getItem(journalsKey)); } catch { /* Use the in-memory journal. */ }
  const journals = window.OieRuntime.memory.get(journalsKey) || new Map(Array.isArray(saved) ? saved : []);
  window.OieRuntime.memory.set(journalsKey, journals);
  function persist() {
    try { sessionStorage.setItem(journalsKey, JSON.stringify([...journals])); } catch { /* Keep in-memory retries. */ }
  }

  let source = '', opener, request, submitting = false;
  const notify = (tone, message) => window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone, message}}));
  const modal = action => window.dispatchEvent(new CustomEvent('modal:' + action, {detail: {id}}));
  const boxes = form => [...form.querySelectorAll('input[name=conversationId]')];
  const resultNode = box => box.closest('[data-forward-room]').querySelector('[data-forward-result]');
  const receiptKey = (form, box) => [source, form.elements.revisionId.value, box.value].join(':');
  const pending = form => boxes(form).filter(box => box.checked && !box.disabled);
  const updateButton = form => {
    const button = form.querySelector('[data-forward-submit]');
    if (button) button.setAttribute('aria-disabled', String(submitting || !pending(form).length));
  };

  async function open(messageId, trigger) {
    if (submitting) return;
    request?.abort();
    const controller = new AbortController();
    request = controller;
    source = messageId;
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
        const receipt = journals.get(receiptKey(fresh, box));
        if (!receipt) continue;
        box.checked = true;
        box.disabled = receipt.accepted;
        resultNode(box).textContent = receipt.message;
        resultNode(box).dataset.accepted = String(receipt.accepted);
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
    if (event.target.name === 'conversationId') {
      updateButton(event.target.form);
      event.target.form.querySelector('[data-forward-search]').dispatchEvent(new Event('input', {bubbles: true}));
    }
  });
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
      if (!journals.has(key)) journals.set(key, {key: box.dataset.key, accepted: false, message: ''});
    }
    persist();
    const button = form.querySelector('[data-forward-submit]');
    updateButton(form);
    button.textContent = body.dataset.sending;
    for (const box of available) box.disabled = true;
    try {
      for (const box of selected) {
        if (life.signal.aborted) return;
        const key = receiptKey(form, box);
        const receipt = journals.get(key);
        receipt.message = body.dataset.unconfirmed;
        persist();
        const status = resultNode(box);
        status.textContent = body.dataset.sending;
        const data = new URLSearchParams();
        for (const name of ['engineId','csrf','messageId','revisionId']) data.set(name, form.elements[name].value);
        data.set('conversationId', box.value);
        data.set('operationKey', receipt.key);
        try {
          const response = await life.fetch('/ui/messages/forward', {method: 'POST', body: data, headers: {Accept: 'application/json'}});
          const result = await response.json();
          receipt.accepted = response.ok && result.accepted === true;
          receipt.message = result.message || body.dataset.unconfirmed;
        } catch { /* Keep this room and key available for an exact retry. */ }
        persist();
        status.textContent = receipt.message;
        status.dataset.accepted = String(receipt.accepted);
      }
      if (life.signal.aborted) return;
      if (selected.every(box => journals.get(receiptKey(form, box))?.accepted) &&
          boxes(form).every(box => !journals.has(receiptKey(form, box)) || journals.get(receiptKey(form, box)).accepted)) {
        for (const box of boxes(form)) {
          const key = receiptKey(form, box);
          if (journals.get(key)?.accepted) journals.delete(key);
        }
        persist();
        if (dialog.open) modal('close');
        notify('success', body.dataset.complete);
        document.dispatchEvent(new CustomEvent('oie:refresh-request'));
      } else if (dialog.open) {
        button.textContent = button.dataset.retry;
        button.focus({preventScroll: true});
      } else notify('danger', body.dataset.unconfirmed);
    } finally {
      submitting = false;
      for (const box of available) box.disabled = journals.get(receiptKey(form, box))?.accepted || false;
      updateButton(form);
    }
  });
});
