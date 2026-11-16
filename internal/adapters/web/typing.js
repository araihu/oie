window.OieRuntime.register('typing', life => {
  const root = document.getElementById('oie-app');
  const notice = document.querySelector('[data-typing-notice]');
  const input = document.getElementById('message-composer');
  if (!notice || !input || root?.dataset.mock === 'true') return;
  const {engineId, accountId, conversationId} = root.dataset;
  if (!engineId || !accountId || !conversationId) return;
  const path = '/ui/api/v1/conversations/' + conversationId + '/typing';
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content || '';
  const headers = {'X-Oie-Engine-ID': engineId, 'X-Oie-Account-ID': accountId};
  const visible = () => !document.hidden && document.hasFocus() && input.isConnected && input.getClientRects().length > 0;
  let polling = false;
  let supported = true;
  let active = false;
  let pauseTimer;
  let request = Promise.resolve();
  async function observe() {
    if (polling || !supported) return;
    if (!visible()) {
      notice.hidden = true;
      return;
    }
    polling = true;
    try {
      const response = await life.fetch(path, {headers, cache: 'no-store'});
      if ([404, 405, 409].includes(response.status)) supported = false;
      const state = response.ok ? await response.json() : null;
      notice.hidden = !visible() || state?.active !== true;
    } catch { notice.hidden = true; }
    finally { polling = false; }
  }
  function send(next) {
    if (!supported || next === active || input.readOnly || input.disabled) return;
    active = next;
    request = request.then(async () => {
      if (life.signal.aborted || next && !visible()) return;
      try {
        await life.fetch(path, {
          method: 'PUT',
          headers: {...headers, 'Content-Type': 'application/json', 'X-CSRF-Token': csrf},
          body: JSON.stringify({active: next})
        });
      } catch {}
    });
  }
  function stop() {
    clearTimeout(pauseTimer);
    send(false);
  }
  life.on(input, 'input', event => {
    if (!event.isTrusted || !visible()) return;
    clearTimeout(pauseTimer);
    send(input.value.trim() !== '');
    pauseTimer = life.timeout(stop, 3500);
  });
  life.on(input, 'blur', stop);
  life.on(input.form, 'submit', stop);
  life.on(window, 'focus', observe);
  life.on(document, 'visibilitychange', observe);
  life.interval(observe, 3500);
  observe();
});
