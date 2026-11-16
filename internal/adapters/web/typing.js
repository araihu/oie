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
  const update = active => ({
    method: 'PUT',
    headers: {...headers, 'Content-Type': 'application/json', 'X-CSRF-Token': csrf},
    body: JSON.stringify({active})
  });
  const visible = () => !document.hidden && document.hasFocus() && input.isConnected && input.getClientRects().length > 0;
  let polling = false;
  let supported = true;
  let desired = false;
  let mayBeActive = false;
  let confirmedAt = -Infinity;
  let sending = false;
  let pauseTimer;
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
  async function send() {
    if (!supported || sending || life.signal.aborted) return;
    const active = desired;
    if (active ? !visible() || input.readOnly || input.disabled || performance.now() - confirmedAt < 12000 : !mayBeActive) return;
    sending = true;
    if (active) mayBeActive = true;
    try {
      const response = await life.fetch(path, update(active));
      if (response.ok) {
        mayBeActive = active;
        confirmedAt = active ? performance.now() : -Infinity;
      }
    } catch {}
    finally {
      sending = false;
      if (active !== desired) void send();
    }
  }
  function stop() {
    clearTimeout(pauseTimer);
    desired = false;
    void send();
  }
  function leave() {
    clearTimeout(pauseTimer);
    desired = false;
    confirmedAt = -Infinity;
    if (!mayBeActive) return;
    mayBeActive = false;
    void window.OieRuntime.request(path, {...update(false), keepalive: true}).catch(() => {});
  }
  life.on(input, 'input', event => {
    if (!event.isTrusted || !visible()) return;
    clearTimeout(pauseTimer);
    desired = input.value.trim() !== '';
    void send();
    pauseTimer = life.timeout(stop, 3500);
  });
  life.on(input, 'blur', stop);
  life.on(input.form, 'submit', stop);
  life.on(window, 'focus', observe);
  life.on(document, 'visibilitychange', () => {
    if (document.hidden) stop();
    void observe();
  });
  life.on(window, 'pagehide', leave);
  life.cleanup(leave);
  life.interval(observe, 3500);
  observe();
});
