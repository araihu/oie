(() => {
  const pending = new WeakSet();
  const status = (message, ok) => window.dispatchEvent(new CustomEvent('notify', {
    detail: {kind: 'toast', tone: ok ? 'success' : 'danger', message}
  }));

  document.addEventListener('submit', async event => {
    const form = event.target;
    const panel = form.closest('[data-account-contacts]');
    if (!panel || !form.action.endsWith('/ui/accounts/contacts')) return;
    event.preventDefault();
    if (pending.has(panel)) return;
    pending.add(panel);
    const values = new URLSearchParams(new FormData(form));
    const controls = [...panel.querySelectorAll('button[type="submit"]:enabled')];
    for (const control of controls) control.setAttribute('aria-disabled', 'true');
    panel.setAttribute('aria-busy', 'true');
    try {
      const response = await fetch(form.action, {
        method: 'POST',
        headers: {Accept: 'application/json'},
        body: values,
        signal: AbortSignal.timeout(15000)
      });
      const result = await response.json();
      if (!panel.isConnected) return;
      status(result.message || panel.dataset.contactFailed, result.ok === true);
      if (!response.ok || result.ok !== true) return;
      const input = form.querySelector('#add-contact-address');
      if (input?.value === values.get('address')) input.value = '';
      const current = panel.querySelector('[data-account-contact-list]');
      const fragment = await fetch(values.get('returnTo'), {
        headers: {'HX-Request': 'true', 'X-Oie-Fragment': 'accounts'},
        signal: AbortSignal.timeout(15000)
      });
      if (!fragment.ok) throw Error('Contact list unavailable');
      const next = new DOMParser().parseFromString(await fragment.text(), 'text/html');
      const nextPanel = next.querySelector('[data-account-contacts]');
      const list = nextPanel?.querySelector('[data-account-contact-list]');
      if (!panel.isConnected) return;
      if (nextPanel?.dataset.contactAccount !== panel.dataset.contactAccount ||
          next.querySelector('#account-workspace')?.dataset.engineId !== values.get('engineId') || !list) {
        throw Error('Contact list changed');
      }
      const restoreFocus = current.contains(document.activeElement);
      current.replaceWith(list);
      if (restoreFocus) panel.querySelector('#add-contact-address,[data-contact-title]')?.focus({preventScroll: true});
    } catch {
      if (panel.isConnected) status(panel.dataset.contactFailed, false);
    } finally {
      panel.removeAttribute('aria-busy');
      for (const control of controls) control.removeAttribute('aria-disabled');
      pending.delete(panel);
    }
  });
})();
