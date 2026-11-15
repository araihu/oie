// Handle clicks even after the page that showed the banner has closed.
self.addEventListener('notificationclick', event => {
  event.notification.close();
  const {engine, account, conversation, message} = event.notification.data ?? {};
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  if (![engine, account, conversation, message].every(id => typeof id === 'string' && uuid.test(id))) return;

  const destination = '/?' + new URLSearchParams({account, chat: conversation, message, notificationEngine: engine});
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
