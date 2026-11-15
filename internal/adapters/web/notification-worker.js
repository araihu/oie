// Persistent notification UI for browsers that require a worker to show a banner.
self.addEventListener('notificationclick', event => {
  event.notification.close();
  event.waitUntil((async () => {
    const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
    const client = windows.find(candidate => new URL(candidate.url).pathname === '/');
    if (client) {
      client.postMessage({...event.notification.data, type: 'oie:notification-click'});
      await client.focus();
    }
  })());
});
