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
      query.get('q') || '', query.get('inboxCursor') || '', query.get('lang') || app?.dataset.routeLocale,
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

  function selectConversation(list, id) {
    if (!list) return;
    for (const row of list.querySelectorAll('a[data-conversation-id][aria-current="true"]')) {
      if (row.dataset.conversationId !== id) row.setAttribute('aria-current', 'false');
    }
    const next = id && list.querySelector('a[data-conversation-id="' + CSS.escape(id) + '"]');
    if (next && next.getAttribute('aria-current') !== 'true') next.setAttribute('aria-current', 'true');
  }

  function restore(entry, placeholder, conversation, fresh) {
    if (parked === entry) parked = undefined;
    const list = entry.list;
    if (window.Alpine) window.Alpine.mutateDom(() => placeholder.replaceWith(list));
    else placeholder.replaceWith(list);
    list.scrollTop = entry.top;
    if (fresh && list.dataset.version !== placeholder.dataset.version) {
      reconcile(list, placeholder);
      list.dataset.version = placeholder.dataset.version;
    }
    selectConversation(list, conversation);
    return list;
  }

  function reconcile(list,fresh){
    const scroll=list.scrollTop,rect=list.getBoundingClientRect(),rows=[...list.querySelectorAll('[data-conversation-row]')];
    const anchors=rows.filter(row=>row.getBoundingClientRect().bottom>=rect.top&&row.getBoundingClientRect().top<rect.bottom).map(row=>({id:row.dataset.conversationRow,top:row.getBoundingClientRect().top}));
    const active = document.activeElement;
    const focusedRow = list.contains(active) && active.closest('[data-conversation-row]');
    const existing=list.querySelector('ul.oie-conversations'),incoming=fresh.querySelector('ul.oie-conversations');
    if(existing&&incoming){
      const known=new Map(rows.map(row=>[row.dataset.conversationRow,row])),wanted=new Set();
      for(const next of incoming.children){
        const id=next.dataset.conversationRow;if(!id)continue;wanted.add(id);let row=known.get(id);
        if(!row){row=next.cloneNode(true);mutateDOM(()=>{existing.append(row);window.Alpine?.initTree(row)})}
        else if(row.dataset.version!==next.dataset.version){replaceContents(row,next);row.dataset.version=next.dataset.version}
        const count=next.getAttribute('data-latest-message-page-count');
        if(count===null)row.removeAttribute('data-latest-message-page-count');
        else row.dataset.latestMessagePageCount=count;
        // Appending an unchanged node moves it without replacing its identity.
        if(existing.children[wanted.size-1]!==row)mutateDOM(()=>existing.insertBefore(row,existing.children[wanted.size-1]||null));
      }
      for(const row of rows)if(!wanted.has(row.dataset.conversationRow))mutateDOM(()=>{window.Alpine?.destroyTree(row);row.remove()});
      const currentPage = list.querySelector('[data-inbox-pagination]');
      const nextPage = fresh.querySelector('[data-inbox-pagination]');
      if (nextPage && !currentPage) list.append(nextPage.cloneNode(true));
      else if (!nextPage) currentPage?.remove();
      else if (!currentPage.isEqualNode(nextPage)) currentPage.replaceWith(nextPage.cloneNode(true));
    }else replaceContents(list,fresh);
    const anchor=anchors.find(anchor=>list.querySelector(`[data-conversation-row="${CSS.escape(anchor.id)}"]`)),surviving=anchor&&list.querySelector(`[data-conversation-row="${CSS.escape(anchor.id)}"]`);
    list.scrollTop=surviving?scroll+surviving.getBoundingClientRect().top-anchor.top:scroll;
    if (focusedRow && !list.contains(document.activeElement)) {
      const row = list.querySelector(`[data-conversation-row="${CSS.escape(focusedRow.dataset.conversationRow)}"]`);
      const target = row?.querySelector('a[data-conversation-id]') || document.querySelector('#chat-options button');
      target?.focus({preventScroll: true});
    }
  }
  function mutateDOM(fn){if(window.Alpine?.mutateDom)window.Alpine.mutateDom(fn);else fn()}
  function replaceContents(target, fresh) {
    mutateDOM(() => {
      for (const child of target.children) window.Alpine?.destroyTree(child);
      target.innerHTML = fresh.innerHTML;
      for (const child of target.children) window.Alpine?.initTree(child);
    });
  }

  window.OieInbox = {
    get generation() { return generation; },
    invalidate() { generation++; },
    clear() { clear(); generation++; },
    holds(entry) { return parked === entry; },
    match(url) { return parked?.key === key(url) ? parked : undefined; },
    park,
    restore,
    reconcile,
    selectConversation,
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
