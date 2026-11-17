(() => {
  'use strict';

  if (!navigator.serviceWorker || !window.isSecureContext) return;
  const body = document.body;
  const shell = body.hasAttribute('data-offline-history');
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);

  if (shell) {
    navigator.serviceWorker.addEventListener('message', ({data}) => {
      if (data?.type !== 'oie:notification-click') return;
      const {engine, account, conversation, message} = data;
      if (![engine, account, conversation, message].every(uuid)) return;
      location.assign('/?' + new URLSearchParams({notificationEngine: engine, account, chat: conversation, message}));
    });
    void renderOffline();
    return;
  }

  if (location.pathname !== '/') return;
  const worker = navigator.serviceWorker.register('/offline-worker.js', {updateViaCache: 'none'})
    .then(() => navigator.serviceWorker.ready)
    .catch(() => null);
  const publish = data => {
    void worker.then(registration => registration?.active?.postMessage(data)).catch(() => {});
  };

  window.OieOfflineHistory = {
    observe(action, response) {
      const url = new URL(action, location.href);
      const engine = document.body.dataset.engineId;
      if (url.origin !== location.origin || url.pathname !== '/' || !uuid(engine)) return;
      const identity = response.headers.get('X-Oie-Offline-History');
      if (identity === 'denied') {
        publish({type: 'oie:offline-history-clear', engine, blocked: true});
      } else if (response.status === 404 && identity === engine && !url.searchParams.has('notificationEngine')) {
        const chat = url.searchParams.get('chat');
        const account = url.searchParams.get('account');
        if (uuid(chat) && (account === null || uuid(account))) publish({
          type: 'oie:offline-history-invalidate', engine, account, chat,
          through: response.headers.get('X-Oie-Offline-Cursor'), confirmedMissing: true,
        });
      }
    },
    fallback(action, current, replace, onFailure) {
      if (!navigator.serviceWorker.controller) return false;
      const url = new URL(action, location.href);
      const query = url.searchParams;
      if (url.origin !== location.origin || url.pathname !== '/' ||
          !['chats', 'archived'].includes(query.get('section') || 'chats') ||
          query.has('open') || query.has('searchQuery')) return false;
      void read().catch(() => null).then(state => {
        if (!current()) return;
        if (!state || state.engine !== document.body.dataset.engineId || !state.entries.length) {
          onFailure();
          return;
        }
        location[replace ? 'replace' : 'assign'](url.href);
      });
      return true;
    },
  };

  function snapshot(scope, header, timeline) {
    const {engineId, accountId, conversationId, eventCursor, contextMissing} = scope.dataset;
    if (!uuid(engineId) || window.OieEvents?.replaced || window.OieRuntime?.memory.get('device-reset')) return;
    const {lang} = document.documentElement;
    const {mode: theme} = document.documentElement.dataset;
    const data = {type: 'oie:offline-history-save', engine: engineId, cursor: eventCursor, lang, theme};
    const envelope = timeline?.querySelector('[data-timeline-page]')?.dataset;
    if (header && envelope && scope.dataset.conversationRemoved === 'false' && !scope.dataset.conversationRequest &&
        contextMissing !== 'true' && envelope.hasNewer === 'false' && envelope.account === accountId &&
        envelope.conversation === conversationId && envelope.engine === engineId) {
      const messages = [...timeline.querySelectorAll(':scope > [data-message-id]')].slice(-50).flatMap(node => {
        const {messageId, messageAccount, messageConversation, messageSenderName, createdAt} = node.dataset;
        if (messageAccount !== accountId || messageConversation !== conversationId) return [];
        const withdrawn = node.querySelector('[data-message-retracted], [data-message-moderated]');
        const presentation = node.querySelector('[data-message-presentation]');
        const text = withdrawn ? '' : JSON.parse(presentation?.dataset.presentationVersion ?? '""');
        if (!text && !withdrawn) return [];
        return [{
          id: messageId, sender: messageSenderName, date: createdAt, text,
          withdrawn: withdrawn?.hasAttribute('data-message-retracted') ? 'retracted' : withdrawn ? 'moderated' : '',
          own: node.classList.contains('oie-message-own'),
        }];
      });
      const title = header.querySelector('h2')?.textContent;
      const label = uuid(accountId)
        ? document.querySelector(`[data-switch-account="${accountId}"] .oie-profile-identity strong`)?.textContent : '';
      if (title) data.entry = {account: accountId, chat: conversationId, title, label, messages};
    }
    publish(data);
  }

  document.addEventListener('oie:route-ready', () => {
    const pane = document.getElementById('conversation-pane');
    if (pane) snapshot(pane, pane.querySelector('#chat-header'), pane.querySelector('#message-timeline'));
  });
  document.addEventListener('oie:shared-refresh', ({detail: page}) => {
    const regions = page.querySelector('#oie-live-regions');
    if (regions?.dataset.unavailable === 'false') snapshot(regions, page.querySelector('#live-header #chat-header'), page.querySelector('#live-timeline'));
  });
  document.addEventListener('oie:appearance-changed', () => {
    const root = document.getElementById('oie-app');
    if (root) snapshot(root);
  });
  window.addEventListener('oie:device-reset', () => {
    const engine = document.body.dataset.engineId;
    if (uuid(engine)) publish({type: 'oie:offline-history-clear', engine, blocked: true});
  });
  const events = window.OieEvents?.target;
  if (events) {
    const findScope = {
      'account.removed': value => ({account: value.removedAccountId}),
      'conversation.updated': ({conversation}) => (conversation?.removedFromChats || conversation?.request)
        ? {account: conversation.accountId, chat: conversation.id} : null,
      'conversation.history_pruned': ({historyPruned}) => historyPruned
        ? {account: historyPruned.accountId, chat: historyPruned.conversationId} : null,
      'message.updated': ({message}) => message ? {account: message.accountId, chat: message.conversationId} : null,
    };
    for (const type of window.OieEvents.eventTypes) {
      events.addEventListener(type, ({detail: event}) => {
        const engine = document.body.dataset.engineId;
        const through = event.lastEventId;
        try {
          const target = findScope[type]?.(JSON.parse(event.data));
          publish(uuid(target?.account)
            ? {type: 'oie:offline-history-invalidate', engine, through, ...target}
            : {type: 'oie:offline-history-progress', engine, through});
        } catch {}
      });
    }
    events.addEventListener('history-gap', ({detail: through}) => {
      publish({type: 'oie:offline-history-clear', engine: document.body.dataset.engineId, through});
    });
    events.addEventListener('engine-changed', () => publish({
      type: 'oie:offline-history-clear', engine: document.body.dataset.engineId, blocked: true,
    }));
  }

  async function read() {
    const registration = await navigator.serviceWorker.getRegistration('/');
    if (!registration?.active) return null;
    return new Promise(resolve => {
      const {port1, port2} = new MessageChannel();
      const finish = result => { clearTimeout(timeout); port1.close(); resolve(result); };
      const timeout = setTimeout(() => finish(null), 2000);
      port1.onmessage = ({data}) => finish(data);
      try { registration.active.postMessage({type: 'oie:offline-history-read'}, [port2]); }
      catch { finish(null); }
    });
  }

  async function renderOffline() {
    const state = await read().catch(() => null);
    const title = document.getElementById('offline-title');
    const empty = document.getElementById('offline-empty');
    const back = document.getElementById('offline-back');
    const retry = document.getElementById('offline-retry');
    const query = new URLSearchParams(location.search);
    const lang = document.documentElement.lang;
    const theme = query.get('theme') || state?.theme;
    if (theme === 'light' || theme === 'dark') document.documentElement.style.colorScheme = theme;
    const account = query.get('account');
    const chat = query.get('chat');
    const message = query.get('message');
    const expectedEngine = query.get('notificationEngine');
    const entries = expectedEngine && expectedEngine !== state?.engine ? [] :
      (state?.entries ?? []).filter(entry => !account || entry.account === account);
    const params = new URLSearchParams({lang});
    if (theme === 'light' || theme === 'dark') params.set('theme', theme);
    if (account) params.set('account', account);
    back.href = '/?' + params;
    if (chat) params.set('chat', chat);
    if (message) params.set('message', message);
    if (expectedEngine) params.set('notificationEngine', expectedEngine);
    retry.href = '/?' + params;
    if (!chat) {
      const list = document.getElementById('offline-conversations');
      for (const entry of entries) {
        const item = document.createElement('li'), link = document.createElement('a');
        const route = new URLSearchParams(params);
        route.set('chat', entry.chat);
        link.href = '/?' + route;
        link.textContent = entry.label ? entry.title + ' · ' + entry.label : entry.title;
        item.append(link);
        list.append(item);
      }
      list.hidden = !entries.length;
      empty.hidden = entries.length > 0;
      return;
    }

    back.hidden = false;
    const entry = entries.find(entry => entry.chat === chat);
    if (!entry) {
      empty.textContent = body.dataset.conversationMissing;
      empty.hidden = false;
      return;
    }
    title.textContent = entry.title;
    const list = document.getElementById('offline-messages');
    const template = document.getElementById('offline-message');
    const dateOptions = {dateStyle: 'short', timeStyle: 'short'};
    const format = window.OieTime?.format ?? (date => new Intl.DateTimeFormat(lang, dateOptions).format(date));
    let target;
    for (const item of entry.messages) {
      const row = template.content.firstElementChild.cloneNode(true);
      row.classList.toggle('offline-own', item.own);
      row.querySelector('.offline-sender').textContent = item.sender;
      row.querySelector('p').textContent = item.withdrawn ? body.dataset[item.withdrawn] : item.text;
      const time = row.querySelector('time');
      time.dateTime = item.date;
      const date = new Date(item.date);
      if (Number.isFinite(date.getTime())) time.textContent = format(date, dateOptions);
      list.append(row);
      if (item.id === message) target = row;
    }
    list.hidden = !entry.messages.length;
    if (message && !target) {
      empty.textContent = body.dataset.messageMissing;
      empty.hidden = false;
    }
    if (target) {
      target.tabIndex = -1;
      target.focus();
    } else {
      list.scrollTop = list.scrollHeight;
    }
  }
})();
