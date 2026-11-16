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

  document.addEventListener('submit', async event => {
    const form = event.target;
    if (form.id !== 'engine-mode-form') return;
    event.preventDefault();
    if (form.getAttribute('aria-busy') === 'true') return;

    const address = form.elements.url;
    const modes = [...form.querySelectorAll('input[name=mode]')];
    const button = form.querySelector('button[type=submit]');
    const status = form.querySelector('[data-engine-connecting]');
    const body = new URLSearchParams(new FormData(form));
    form.setAttribute('aria-busy', 'true');
    button.setAttribute('aria-disabled', 'true');
    address.readOnly = true;
    for (const mode of modes) mode.disabled = true;
    status.textContent = status.dataset.engineConnecting;
    let message = form.dataset.failed;
    try {
      const response = await window.OieRuntime.request(form.action, {
        method: 'POST', body, headers: {Accept: 'application/json'},
      });
      if (response.status === 204) {
        window.OieEvents?.stop();
        location.replace('/');
        return;
      }
      const error = await response.text();
      if (error && response.headers.get('Content-Type')?.startsWith('text/plain')) message = error.trim();
    } catch {}
    if (!form.isConnected) return;
    button.removeAttribute('aria-disabled');
    address.readOnly = false;
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
