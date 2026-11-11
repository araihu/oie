(() => {
  'use strict';

  const dialogID = 'reset-data';
  const noticeKey = 'oie.reset.notice';
  const changeDialog = action => window.dispatchEvent(new CustomEvent('modal:' + action, {detail: {id: dialogID}}));
  const form = () => document.getElementById('reset-data-form');
  const confirmation = node => node.elements.namedItem('confirmation');
  const submitButton = node => node.querySelector('button[type="submit"]');

  function returnToEmptyApp() {
    window.OieRuntime?.stop();
    for (const name of ['localStorage', 'sessionStorage']) {
      try {
        const storage = window[name];
        for (let index = storage.length - 1; index >= 0; index--) {
          const key = storage.key(index);
          if (key?.startsWith('oie.')) storage.removeItem(key);
        }
      } catch {}
    }
    try {
      // Open cache connections close on versionchange. The deletion request
      // survives navigation and runs before a new tab can reopen this database.
      window.indexedDB?.deleteDatabase('oie.pdf-previews.v1');
    } catch {}
    location.replace('/');
  }

  window.addEventListener('storage', event => {
    if (event.key === noticeKey && event.newValue === document.body.dataset.engineId) returnToEmptyApp();
  });

  document.addEventListener('click', event => {
    if (event.target.closest('[data-reset-data-open]')) {
      const node = form();
      if (!node || node.getAttribute('aria-busy') === 'true') return;
      node.reset();
      submitButton(node).disabled = true;
      changeDialog('open');
    }
    if (event.target.closest('[data-reset-data-cancel]')) changeDialog('close');
  });

  document.addEventListener('input', event => {
    const node = form();
    if (node && event.target === confirmation(node)) submitButton(node).disabled = event.target.value !== 'delete';
  });

  document.addEventListener('cancel', event => {
    if (event.target.id !== dialogID || form()?.getAttribute('aria-busy') !== 'true') return;
    event.preventDefault();
    event.stopImmediatePropagation();
  }, true);

  document.addEventListener('submit', async event => {
    const node = event.target;
    if (node.id !== 'reset-data-form') return;
    event.preventDefault();
    if (confirmation(node).value !== 'delete' || node.getAttribute('aria-busy') === 'true') return;

    const body = new URLSearchParams(new FormData(node));
    const buttons = node.closest('dialog').querySelectorAll('button');
    const input = confirmation(node);
    node.setAttribute('aria-busy', 'true');
    input.readOnly = true;
    for (const button of buttons) button.disabled = true;

    try {
      const response = await fetch(node.action, {method: 'POST', body, credentials: 'same-origin'});
      if (response.status !== 204) throw new Error('reset failed');
      try { localStorage.setItem(noticeKey, body.get('engineId')); } catch {}
      returnToEmptyApp();
    } catch {
      changeDialog('close');
      window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message: node.dataset.failure}}));
    } finally {
      node.removeAttribute('aria-busy');
      input.readOnly = false;
      input.value = '';
      for (const button of buttons) button.disabled = false;
      submitButton(node).disabled = true;
    }
  });
})();
