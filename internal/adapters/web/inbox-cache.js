(() => {
  'use strict';

  const snapshots = new WeakMap();
  let generation = 0;
  let parked;

  function key(url) {
    const query = url.searchParams;
    if (url.pathname !== '/' || (query.get('section') || 'chats') !== 'chats' || query.get('searchMode') === 'messages') return '';
    const app = document.getElementById('oie-app');
    return JSON.stringify([
      app?.dataset.engineId, query.get('account') || '', query.get('filter') || 'all',
      query.get('q') || '', query.get('lang') || app?.dataset.routeLocale,
      query.get('theme') || app?.dataset.routeTheme
    ]);
  }

  function clear() {
    if (parked) window.Alpine?.destroyTree(parked.list);
    parked = undefined;
  }

  function park(url, hiddenTop) {
    const list = document.getElementById('conversation-list');
    const scope = key(url);
    if (!scope || !list?.dataset.inboxCacheScope) return;
    clear();
    // The parked list keeps its initialized avatars and row handlers.
    parked = {list, key: scope, top: list.clientHeight ? list.scrollTop : hiddenTop};
    if (window.Alpine) window.Alpine.mutateDom(() => list.remove());
    else list.remove();
  }

  function restore(entry, placeholder, conversation) {
    if (parked === entry) parked = undefined;
    const list = entry.list;
    for (const row of list.querySelectorAll('a[data-conversation-id]')) {
      row.setAttribute('aria-current', String(row.dataset.conversationId === conversation));
    }
    if (window.Alpine) window.Alpine.mutateDom(() => placeholder.replaceWith(list));
    else placeholder.replaceWith(list);
    list.scrollTop = entry.top;
  }

  window.OieInbox = {
    get generation() { return generation; },
    invalidate() { generation++; },
    clear() { clear(); generation++; },
    holds(entry) { return parked === entry; },
    match(url) { return parked?.key === key(url) ? parked : undefined; },
    park,
    restore,
    seed(list) {
      if (list && !snapshots.has(list)) snapshots.set(list, generation);
    },
    current(list) { return snapshots.get(list) === generation; },
    track(list, version, cursor) {
      if (!list) return;
      snapshots.set(list, version);
      if (cursor) list.dataset.inboxCursor = cursor;
    }
  };
})();
