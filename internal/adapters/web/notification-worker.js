const hosted = new URL(self.location.href).searchParams.get('hosted') === '1';
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

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
