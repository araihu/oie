(() => {
  'use strict';

  let leaving = false;
  let check;
  let navigation;
  const channel = typeof BroadcastChannel === 'function' ? new BroadcastChannel('oie.hosted-session') : null;

  function requiresSignIn(response) {
    return response?.status === 401 && response.headers.get('X-Oie-Sign-In-Required') === '1';
  }

  function conceal() {
    document.documentElement.style.display = 'none';
  }

  function signIn(destination = navigation?.destination ?? location.pathname + location.search) {
    if (leaving) return;
    leaving = true;
    conceal();
    const next = destination && destination !== '/' ? '?' + new URLSearchParams({next: destination}) : '';
    // Let the handler of a rejected request finish saving its draft before leaving.
    setTimeout(() => location.replace('/auth/sign-in' + next), 0);
  }

  function observe(response) {
    if (requiresSignIn(response)) signIn();
    return response;
  }

  function verify() {
    if (leaving || check) return;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5000);
    check = fetch('/auth/session', {cache: 'no-store', redirect: 'error', signal: controller.signal})
      .then(observe).then(response => {
        if (!leaving && response.status === 204) document.documentElement.style.removeProperty('display');
      }).catch(() => {}).finally(() => {
        clearTimeout(timeout);
        check = undefined;
      });
  }

  document.addEventListener('htmx:before:request', event => {
    const ctx = event.detail.ctx;
    if (ctx?.request?.method?.toUpperCase() !== 'GET') return;
    const history = [ctx.push, ctx.replace].find(value => value && value !== 'false');
    if (!history && ctx.request.headers['HX-History-Restore-Request'] !== 'true') return;
    const action = typeof history === 'string' && history.startsWith('/') ? history : ctx.request.action;
    const target = new URL(action, location.href);
    if (target.origin === location.origin && (target.pathname === '/' || target.pathname === '/search')) {
      navigation = {ctx, destination: target.pathname + target.search};
    }
  });

  document.addEventListener('htmx:before:response', event => {
    if (!requiresSignIn(event.detail.ctx?.response?.raw)) return;
    event.preventDefault();
    signIn();
  }, true);

  document.addEventListener('htmx:finally:request', event => {
    if (navigation?.ctx === event.detail.ctx) navigation = undefined;
  });

  document.addEventListener('submit', async event => {
    const form = event.target;
    if (!form.matches('form[data-hosted-sign-out]')) return;
    event.preventDefault();
    if (leaving || form.getAttribute('aria-busy') === 'true') return;
    form.setAttribute('aria-busy', 'true');
    try {
      const response = await fetch(form.action, {
        method: 'POST', headers: {Accept: 'application/json'}, redirect: 'error',
      });
      if (response.status === 204) {
        channel?.postMessage('signed-out');
        signIn('');
        return;
      }
    } catch {}
    HTMLFormElement.prototype.submit.call(form);
  });

  channel?.addEventListener('message', event => {
    if (event.data === 'signed-out') signIn();
  });
  window.addEventListener('pagehide', event => { if (event.persisted) conceal(); });
  window.addEventListener('pageshow', verify);
  window.addEventListener('online', verify);
  document.addEventListener('visibilitychange', () => { if (!document.hidden) verify(); });
  setInterval(() => { if (!document.hidden) verify(); }, 30000);
  window.OieHostedSession = {observe, requiresSignIn, verify, get leaving() { return leaving; }};
})();
