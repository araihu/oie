const hosted = new URL(self.location.href).searchParams.get('hosted') === '1';
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

self.addEventListener('install', event => {
  if (hosted) event.waitUntil(self.skipWaiting());
});

self.addEventListener('message', event => {
  const data = event.data;
  const port = event.ports[0];
  if (!hosted || !port || data?.type !== 'oie:foreground-notification' || event.source?.type !== 'window') return;
  const {engine, account, conversation, message, title, body, lang, csrf} = data;
  if (![engine, account, conversation, message].every(id => typeof id === 'string' && uuid.test(id))) return;
  event.waitUntil((async () => {
    let shown = false;
    try {
      const client = await self.clients.get(event.source.id);
      if (client?.visibilityState !== 'visible') return;
      const started = performance.now();
      const request = phase => fetch('/ui/notification-push/foreground', {
        method: 'POST', cache: 'no-store', keepalive: true,
        headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf},
        body: JSON.stringify({engineId: engine, accountId: account, conversationId: conversation, messageId: message, phase}),
      });
      const grant = await request('display');
      if (!grant.ok) return;
      const current = await self.clients.get(event.source.id);
      if (current?.visibilityState !== 'visible' || performance.now() - started >= 2000) return;
      await self.registration.showNotification(title, {
        body, lang, tag: engine + ':' + message, silent: true,
        data: {engine, account, conversation, message},
      });
      for (let attempt = 0; attempt < 2; attempt++) {
        const recorded = await request('shown').catch(() => null);
        if (recorded && recorded.status < 500) {
          shown = recorded.ok;
          break;
        }
      }
    } catch {} finally {
      port.postMessage(shown);
      port.close();
    }
  })());
});

self.addEventListener('push', event => {
  if (!hosted) return;
  let data, destination;
  try {
    const push = event.data.json();
    if (push.web_push !== 8030) return;
    data = push.notification;
    destination = new URL(data.navigate);
  } catch { return; }
  if (destination.origin !== self.location.origin || destination.pathname !== '/') return;
  const engine = destination.searchParams.get('notificationEngine');
  const account = destination.searchParams.get('account');
  const conversation = destination.searchParams.get('chat');
  const message = destination.searchParams.get('message');
  if (![engine, account, conversation, message].every(id => id && uuid.test(id))) return;

  event.waitUntil((async () => {
    await self.registration.showNotification(data.title, {
      body: data.body, lang: data.lang, tag: engine + ':' + message,
      silent: data.silent === true,
      data: {engine, account, conversation, message},
    });
  })());
});

// Handle clicks even after the page that showed the banner has closed.
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const {engine, account, conversation, message} = event.notification.data ?? {};
  if (![engine, account, conversation, message].every(id => typeof id === 'string' && uuid.test(id))) return;

  const destination = '/?' + new URLSearchParams({account, chat: conversation, message, notificationEngine: engine});
  if (hosted) {
    event.waitUntil(self.clients.openWindow(destination));
    return;
  }
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    for (const client of windows) {
      const url = new URL(client.url);
      if (url.origin !== self.location.origin || url.pathname !== '/') continue;
      try {
        await client.focus();
        client.postMessage({type: 'oie:notification-click', engine, account, conversation, message});
        return;
      } catch {
        // The window may have closed while the notification was opening.
      }
    }
    await self.clients.openWindow(destination);
  })());
});
