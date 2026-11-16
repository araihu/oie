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
    const refresh = panel.querySelector('[data-contact-refresh]');
    if (pending.has(panel) || !refresh.hidden) return;
    pending.add(panel);
    const values = new URLSearchParams(new FormData(form));
    const controls = [...panel.querySelectorAll('button[type="submit"]:enabled')];
    for (const control of controls) control.setAttribute('aria-disabled', 'true');
    panel.setAttribute('aria-busy', 'true');
    let saved = false;
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
      saved = true;
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
      document.dispatchEvent(new CustomEvent('oie:navigation-upgrade'));
      if (restoreFocus) panel.querySelector('#add-contact-address,[data-contact-title]')?.focus({preventScroll: true});
    } catch {
      if (panel.isConnected) {
        if (saved) {
          refresh.hidden = false;
          refresh.querySelector('a')?.focus({preventScroll: true});
        } else {
          status(panel.dataset.contactFailed, false);
        }
      }
    } finally {
      panel.removeAttribute('aria-busy');
      if (refresh.hidden) {
        for (const control of controls) control.removeAttribute('aria-disabled');
      }
      pending.delete(panel);
    }
  });
})();
