window.OieRuntime.register('actions', life => {
 'use strict';
 function initializeSourceMenu(event){
  const trigger=event.target.closest?.('[data-message-menu-trigger]');
  const slot=trigger?.closest('[data-source-menu-deferred]');
  if(!slot||!window.Alpine)return;
  // The same input event continues into the now-initialized Goshtoso menu.
  window.Alpine.mutateDom(()=>{
   window.Alpine.destroyTree(slot);
   slot.removeAttribute('x-ignore');
   slot.removeAttribute('data-source-menu-deferred');
   window.Alpine.initTree(slot);
  });
 }
 life.on(document,'click',initializeSourceMenu,true);
 life.on(document,'keydown',event=>{
  if(['Enter',' ','ArrowDown'].includes(event.key))initializeSourceMenu(event);
 },true);
 // Public Dropdown item IDs are application hooks. Escape uses its supported
 // keyboard lifecycle to dismiss and restore focus after native actions.
 life.on(document,'click',event=>{
  const item=event.target.closest('[id^="copy-message-"],[id^="reply-message-"],[id^="star-message-"],[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"],[id^="refresh-preview-"]');
  if(item&&!item.disabled)item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
 },true);
});

(() => {
  'use strict';

  document.addEventListener('input', event => {
    const address = event.target;
    if (address.form?.id !== 'engine-mode-form' || address.name !== 'url') return;
    const key = address.form.elements.apiKey;
    const normalized = value => value.trim().replace(/\/$/, '').toLowerCase();
    key.placeholder = normalized(address.value) === normalized(address.defaultValue) ? key.dataset.savedPlaceholder : '';
  });

  document.addEventListener('submit', async event => {
    const form = event.target;
    if (form.id !== 'engine-mode-form') return;
    event.preventDefault();
    if (form.getAttribute('aria-busy') === 'true') return;

    const address = form.elements.url;
    const key = form.elements.apiKey;
    const modes = [...form.querySelectorAll('input[name=mode]')];
    const button = form.querySelector('button[type=submit]');
    const status = form.querySelector('[data-engine-connecting]');
    const body = new URLSearchParams(new FormData(form));
    form.setAttribute('aria-busy', 'true');
    button.setAttribute('aria-disabled', 'true');
    address.readOnly = true;
    key.readOnly = true;
    for (const mode of modes) mode.disabled = true;
    status.textContent = status.dataset.engineConnecting;
    let message = form.dataset.failed;
    try {
      const response = await window.OieRuntime.request(form.action, {
        method: 'POST', body, headers: {Accept: 'application/json'},
      });
      if (response.status === 204) {
        key.value = '';
        window.OieEvents?.suspend();
        location.replace('/');
        return;
      }
      const error = await response.text();
      if (error && response.headers.get('Content-Type')?.startsWith('text/plain')) message = error.trim();
    } catch {}
    if (!form.isConnected) return;
    button.removeAttribute('aria-disabled');
    address.readOnly = false;
    key.readOnly = false;
    for (const mode of modes) mode.disabled = false;
    form.removeAttribute('aria-busy');
    status.textContent = '';
    window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message}}));
  });
})();

window.OieRuntime.register('diagnostics', life => {
  let opening, viewer;
  const close = () => {
    opening = undefined;
    viewer?.close();
  };
  life.on(window, 'drawer:open', async event => {
    if (event.detail?.id !== 'diagnostics') return;
    const panel = document.querySelector('[data-diagnostics]');
    if (!panel) return;
    const pending = opening = {};
    const status = panel.querySelector('[data-diagnostics-status]');
    status.textContent = panel.dataset.loading;
    try {
      viewer = await import('/ui/diagnostics.js');
      if (opening === pending && panel.isConnected) viewer.open();
    } catch {
      if (opening === pending) status.textContent = panel.dataset.unavailable;
    }
  });
  for (const name of ['drawer:close', 'drawer:close-request']) {
    life.on(window, name, event => {
      if (event.detail?.id !== 'diagnostics') return;
      const pending = opening;
      queueMicrotask(() => {
        if (!event.defaultPrevented && opening === pending) close();
      });
    });
  }
  life.cleanup(close);
});


window.OieRuntime.register('access-keys', life => {
  const form = document.querySelector('[data-create-access-key]');
  if (!form) return;
  const name = form.elements.namedItem('name');
  const create = form.querySelector('button[type=submit]');
  const panel = form.parentElement;
  const result = panel.querySelector('[data-created-access-key]');
  const secret = result.querySelector('input');
  const copy = result.querySelector('button');
  let pending;
  const notify = (tone, message) => window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone, message}}));

  function clear() {
    pending?.abort();
    pending = undefined;
    secret.value = '';
    result.hidden = true;
    name.readOnly = false;
    create.removeAttribute('aria-disabled');
    form.removeAttribute('aria-busy');
  }

  life.on(window, 'drawer:close', event => {
    if (event.detail?.id === 'access-keys') clear();
  });
  life.cleanup(clear);
  life.on(form, 'submit', async event => {
    event.preventDefault();
    if (pending) return;
    const request = pending = new AbortController();
    const body = new URLSearchParams(new FormData(form));
    name.readOnly = true;
    create.setAttribute('aria-disabled', 'true');
    form.setAttribute('aria-busy', 'true');
    try {
      const response = await life.fetch(form.action, {method: 'POST', body, signal: request.signal});
      if (request.signal.aborted) return;
      if (response.status !== 201) {
        const reason = response.status === 400 ? await response.text() : form.dataset.failed;
        if (!request.signal.aborted) notify('danger', reason.trim());
        return;
      }
      const {key} = await response.json();
      if (request.signal.aborted) return;
      secret.value = key;
      result.hidden = false;
      copy.focus();
    } catch {
      if (!request.signal.aborted) notify('danger', form.dataset.failed);
    } finally {
      if (pending === request) {
        pending = undefined;
        name.readOnly = false;
        create.removeAttribute('aria-disabled');
        form.removeAttribute('aria-busy');
      }
    }
  });
  life.on(copy, 'click', async () => {
    const key = secret.value;
    try {
      await navigator.clipboard.writeText(key);
      if (key && secret.value === key) notify('success', copy.dataset.copied);
    } catch {
      if (secret.value) {
        secret.focus();
        secret.select();
      }
    }
  });
});
