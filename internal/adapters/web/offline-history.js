(() => {
  'use strict';

  if (!navigator.serviceWorker || !window.isSecureContext) return;
  const body = document.body;
  const shell = body.hasAttribute('data-offline-history');
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);

  const revokedEngines = new Set();
  let displayedEngine, stopSearch, refreshSearch;
  let renderTurn = 0;
  if (shell) {
    const title = document.getElementById('offline-title');
    const recentTitle = title.textContent;
    title.dataset.recentTitle = recentTitle;
    const revoke = engine => {
      if (!uuid(engine)) return;
      revokedEngines.add(engine);
      if (displayedEngine !== engine) return;
      for (const id of ['offline-conversations', 'offline-messages']) {
        const list = document.getElementById(id);
        list.replaceChildren();
        list.hidden = true;
      }
      ++renderTurn;
      stopSearch?.();
      title.textContent = recentTitle;
      const full = document.getElementById('offline-full');
      if (full) full.hidden = true;
      const empty = document.getElementById('offline-empty');
      if (new URLSearchParams(location.search).has('chat')) empty.textContent = body.dataset.conversationMissing;
      empty.hidden = false;
    };
    window.addEventListener('storage', event => {
      if (event.key === 'oie.reset.notice') revoke(event.newValue);
    });
    navigator.serviceWorker.addEventListener('message', ({data}) => {
      if (data?.type === 'oie:offline-history-changed') {
        if (!displayedEngine || data.engine === displayedEngine) void renderOffline();
        return;
      }
      if (data?.type === 'oie:offline-history-reset') {
        revoke(data.engine);
        return;
      }
      if (data?.type !== 'oie:notification-click') return;
      const {engine, account, conversation, message} = data;
      if (![engine, account, conversation, message].every(uuid)) return;
      location.assign('/?' + new URLSearchParams({notificationEngine: engine, account, chat: conversation, message}));
    });
    const initialQuery = document.getElementById('offline-query');
    if (initialQuery) initialQuery.value = savedSearchRoute(new URLSearchParams(location.search)).initialQuery || '';
    window.addEventListener('pageshow', event => { if (event.persisted) void renderOffline(); });
    void renderOffline();
    return;
  }

  if (location.pathname !== '/' && location.pathname !== '/search') return;
  let registration;
  function offlineWorker() {
    if (!registration) {
      registration = navigator.serviceWorker.register('/offline-worker.js', {updateViaCache: 'none'}).then(async reg => {
        if (reg.active) return reg;
        const candidate = reg.installing || reg.waiting;
        if (!candidate) throw new Error('No offline worker');
        await new Promise((resolve, reject) => {
          function check() {
            if (candidate.state !== 'activated' && candidate.state !== 'redundant') return;
            candidate.removeEventListener('statechange', check);
            if (candidate.state === 'activated') resolve();
            else reject(new Error('Offline worker unavailable'));
          }
          candidate.addEventListener('statechange', check);
          check();
        });
        return reg;
      }).catch(() => { registration = undefined; return null; });
    }
    return registration;
  }
  void offlineWorker();
  const publish = data => {
    void offlineWorker().then(reg => reg?.active?.postMessage(data)).catch(() => {});
  };

  function renderKeepStatus(control, result) {
    control.checked = result.kept;
    const status = document.getElementById('keep-offline-status');
    if (!status) return;
    const message = result.full ? control.dataset.refreshFull :
      result.kept && result.available === false ? control.dataset.awaitingCopy : '';
    status.textContent = message;
    status.hidden = !message;
  }

  const keepControl = () => document.querySelector('[data-keep-chat-offline]');
  const keepScope = ({dataset}) => ({engine: dataset.engine, account: dataset.account, chat: dataset.keepChatOffline});
  const changingKeep = new WeakSet();
  let keepRead = 0;

  async function refreshKeep(control = keepControl(), retry = true) {
    if (!control || changingKeep.has(control)) return;
    const turn = ++keepRead;
    const result = await workerRequest('oie:offline-keep-status', keepScope(control));
    if (turn !== keepRead || !control.isConnected || changingKeep.has(control)) return;
    if (result && !result.error) {
      renderKeepStatus(control, result);
      control.disabled = false;
      return;
    }
    control.disabled = true;
    if (retry) setTimeout(() => {
      if (turn === keepRead && control.isConnected && control.getClientRects().length) {
        void refreshKeep(control, false);
      }
    }, 500);
  }

  async function workerRequest(type, scope) {
    let timer;
    try {
      const reg = await Promise.race([offlineWorker(), new Promise(resolve => {
        timer = setTimeout(() => resolve(null), 2000);
      })]);
      return await request(reg?.active, {type, ...scope});
    } catch {
      return null;
    } finally {
      clearTimeout(timer);
    }
  }

  window.OieRuntime.register('offline-storage', life => {
    const section = document.querySelector('[data-offline-storage]');
    if (!section) return;
    const select = section.querySelector('select');
    const usage = section.querySelector('#offline-storage-used');
    const status = section.querySelector('[role="status"]');
    const {engine} = section.dataset;
    const numbers = new Intl.NumberFormat(document.documentElement.lang);
    const size = bytes => bytes < 1024 ? numbers.format(bytes) + ' B' : numbers.format(Math.ceil(bytes / 1024)) + ' KiB';
    let confirmed, refusedLimit, saving = false, turn = 0, visible = false, refreshPending = false;

    async function update(limit) {
      const version = ++turn;
      const result = await workerRequest('oie:offline-storage', {engine, limit});
      if (life.signal.aborted || version !== turn) return;
      if (!result) {
        select.disabled = true;
        usage.textContent = '';
        status.textContent = section.dataset.failed;
        return;
      }
      if (limit !== undefined || confirmed !== result.limit) {
        refusedLimit = limit !== result.limit ? limit : undefined;
      }
      const excess = refusedLimit === undefined ? 0 : result.protectedBytes - refusedLimit;
      if (excess <= 0) refusedLimit = undefined;
      status.textContent = excess > 0 ? section.dataset.excess.replace('{size}', size(excess)) : '';
      confirmed = result.limit;
      select.value = String(confirmed);
      select.disabled = false;
      usage.textContent = section.dataset.usage.replace('{size}', size(result.used));
    }

    function refresh() {
      if (saving) refreshPending = true;
      else if (visible) void update();
    }
    select.disabled = true;
    section.hidden = false;
    const observer = new IntersectionObserver(([entry]) => {
      visible = entry.isIntersecting;
      if (visible) refresh();
    });
    observer.observe(section);
    life.cleanup(() => {
      observer.disconnect();
      select.removeAttribute('aria-disabled');
    });
    life.on(navigator.serviceWorker, 'controllerchange', refresh);
    life.on(window, 'online', refresh);
    life.on(navigator.serviceWorker, 'message', ({data}) => {
      if (!data || (data.engine && data.engine !== engine)) return;
      if (data.type === 'oie:offline-history-changed' || data.type === 'oie:offline-keep-changed') refresh();
    });
    life.on(select, 'change', async () => {
      const limit = Number(select.value);
      select.value = String(confirmed);
      if (saving) return;
      saving = true;
      select.setAttribute('aria-disabled', 'true');
      await update(limit);
      if (life.signal.aborted) return;
      saving = false;
      select.removeAttribute('aria-disabled');
      if (refreshPending) { refreshPending = false; refresh(); }
    });
  });

  navigator.serviceWorker.addEventListener('controllerchange', () => {
    const control = keepControl();
    if (control?.getClientRects().length) void refreshKeep(control);
  });
  window.addEventListener('drawer:open', event => {
    if (event.detail?.id === 'details') void refreshKeep();
  });
  document.addEventListener('oie:info-content', () => {
    const control = keepControl();
    if (control?.getClientRects().length) void refreshKeep(control);
  });
  navigator.serviceWorker.addEventListener('message', ({data}) => {
    const control = keepControl();
    if (data?.type !== 'oie:offline-keep-changed' || !control) return;
    const {engine, account, chat} = keepScope(control);
    if (data.engine !== engine || data.account !== account || data.chat !== chat) return;
    if (control.getClientRects().length) void refreshKeep(control);
    else if (data.full) window.dispatchEvent(new CustomEvent('notify', {
      detail: {kind: 'toast', tone: 'danger', message: control.dataset.refreshFull},
    }));
  });
  document.addEventListener('change', async ({target: control}) => {
    if (control !== keepControl() || !control) return;
    const keep = control.checked;
    control.checked = !keep;
    if (changingKeep.has(control)) return;
    changingKeep.add(control);
    ++keepRead;
    control.setAttribute('aria-disabled', 'true');
    const scope = keepScope(control);
    const pane = keep && document.getElementById('conversation-pane');
    const snapshot = pane ? capture(pane, pane.querySelector('#chat-header'), pane.querySelector('#message-timeline')) : undefined;
    const result = await workerRequest('oie:offline-keep-chat', {...scope, keep, snapshot: snapshot?.entry ? snapshot : undefined});
    changingKeep.delete(control);
    if (control.isConnected) {
      control.removeAttribute('aria-disabled');
      if (result) control.checked = result.kept;
      if (control.getClientRects().length && (!result || result.error)) {
        const message = result?.error === 'full' ? control.dataset.full : control.dataset.unavailable;
        window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message}}));
      }
    }
    const current = keepControl();
    if (current?.getClientRects().length) void refreshKeep(current);
  });

  window.OieOfflineHistory = {
    observe(action, response) {
      const url = new URL(action, location.href);
      const engine = document.body.dataset.engineId;
      if (url.origin !== location.origin || !['/', '/search'].includes(url.pathname) || !uuid(engine)) return;
      const identity = response.headers.get('X-Oie-Offline-History');
      if (identity === 'denied') {
        publish({type: 'oie:offline-history-clear', engine, blocked: true});
      } else if (url.pathname === '/' && response.status === 404 && identity === engine && !url.searchParams.has('notificationEngine')) {
        const chat = url.searchParams.get('chat');
        const account = url.searchParams.get('account');
        if (uuid(chat) && (account === null || uuid(account))) publish({
          type: 'oie:offline-history-invalidate', engine, account, chat,
          through: response.headers.get('X-Oie-Offline-Cursor'), confirmedMissing: true,
        });
      }
    },
    async searchFallback(params, current, response) {
      if (!navigator.serviceWorker.controller) return;
      const url = new URL('/search', location.href);
      url.search = new URLSearchParams(params);
      url.searchParams.set('lang', document.documentElement.lang);
      url.searchParams.set('theme', document.documentElement.dataset.mode);
      if (response) {
        this.observe(url, response);
        if (response.headers.get('X-Oie-Offline-History') !== 'unavailable') return;
      } else if (navigator.onLine) {
        // A failed fragment alone does not establish an outage. Keep Retry in the
        // current workspace if the presentation server still answers.
        try {
          await fetch('/ui/offline-history', {
            method: 'HEAD', cache: 'no-store', redirect: 'manual', signal: AbortSignal.timeout(2000),
          });
          return;
        } catch { /* The presentation server is unreachable. */ }
      }
      if (!current()) return;
      this.fallback(url, current, false, () => {});
    },
    fallback(action, current, replace, onFailure) {
      if (!navigator.serviceWorker.controller) return false;
      const url = new URL(action, location.href);
      const query = url.searchParams;
      if (url.origin !== location.origin) return false;
      if (url.pathname === '/search') {
        if (!['', 'messages'].includes(query.get('searchType') || '')) return false;
      } else if (url.pathname !== '/' || !['chats', 'archived'].includes(query.get('section') || 'chats') ||
          (query.has('open') && !(query.get('open') === 'details' && uuid(query.get('chat')))) ||
          (query.has('searchQuery') && !(query.get('searchMode') === 'messages' &&
            ['', 'messages'].includes(query.get('searchType') || '')))) return false;
      void read().catch(() => null).then(state => {
        if (!current()) return;
        if (!state || state.engine !== document.body.dataset.engineId || !(state.entries.length || state.kept?.some(item => item.full))) {
          onFailure();
          return;
        }
        location[replace ? 'replace' : 'assign'](url.href);
      });
      return true;
    },
  };

  function capture(scope, header, timeline) {
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
      if (title) data.entry = {account: accountId, chat: conversationId, title, label, kind: header.querySelector('[data-conversation-preference-state]')?.dataset.kind, messages};
    }
    return data;
  }

  function snapshot(scope, header, timeline) {
    const data = capture(scope, header, timeline);
    if (data) publish(data);
  }

  document.addEventListener('oie:route-ready', () => {
    const pane = document.getElementById('conversation-pane');
    if (pane) snapshot(pane, pane.querySelector('#chat-header'), pane.querySelector('#message-timeline'));
  });
  document.addEventListener('oie:shared-refresh', ({detail: page}) => {
    const regions = page.querySelector('#oie-live-regions');
    if (regions?.dataset.unavailable === 'false') snapshot(regions, page.querySelector('#live-header #chat-header'), page.querySelector('#live-timeline'));
  });
  document.addEventListener('oie:live-message-appended', ({detail: {cursor}}) => {
    const pane = document.getElementById('conversation-pane');
    if (!pane) return;
    const data = capture(pane, pane.querySelector('#chat-header'), pane.querySelector('#message-timeline'));
    if (data?.entry) publish({...data, cursor});
  });
  document.addEventListener('oie:timeline-latest', () => {
    const pane = document.getElementById('conversation-pane');
    if (pane) snapshot(pane, pane.querySelector('#chat-header'), pane.querySelector('#message-timeline'));
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
      'account.removed': value => ({account: value.removedAccountId, removeKeep: true}),
      'conversation.updated': ({conversation}) => (conversation?.removedFromChats || conversation?.request)
        ? {account: conversation.accountId, chat: conversation.id, removeKeep: true} : null,
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
    return request(registration?.active, {type: 'oie:offline-history-read'});
  }

  function request(active, data) {
    if (!active) return null;
    return new Promise(resolve => {
      const {port1, port2} = new MessageChannel();
      const finish = result => { clearTimeout(timeout); port1.close(); resolve(result); };
      const timeout = setTimeout(() => finish(null), 2000);
      port1.onmessage = ({data}) => finish(data);
      try { active.postMessage(data, [port2]); }
      catch { finish(null); }
    });
  }

  function enableOfflineSearch(scope, params, entry) {
    const search = document.getElementById('offline-search');
    if (refreshSearch) { refreshSearch(entry); return; }
    if (!search) return; // The previous offline shell can still be open during an update.
    const input = document.getElementById('offline-query');
    const results = document.getElementById('offline-results');
    const status = document.getElementById('offline-search-status');
    const main = search.closest('main');
    const filters = search.querySelector('#offline-filters');
    const sender = filters?.querySelector('#offline-sender');
    const from = filters?.querySelector('#offline-from');
    const to = filters?.querySelector('#offline-to');
    function updateSenders(entry, selected) {
      if (!sender || !entry) return;
      if (!sender.options) {
        sender.closest('label').hidden = true;
        sender.value = '';
        return;
      }
      const room = entry.kind === 'room';
      sender.closest('[data-offline-sender-field]').hidden = !room;
      const names = room ? [...new Set(entry.messages.map(m => m.sender).filter(Boolean))].sort() : [];
      sender.replaceChildren(sender.options[0], ...names.map(name => new Option(name, name, false, name === selected)));
    }
    const remembered = history.state?.offlineSearch;
    if (remembered?.engine === scope.engine) input.value = remembered.query;
    if (scope.chat) {
      search.querySelector('label').textContent = search.dataset.chatLabel;
      if (filters) {
        const route = new URLSearchParams(location.search);
        const saved = remembered?.engine === scope.engine && remembered.chat === scope.chat ? remembered : {};
        updateSenders(entry, saved.sender ?? route.get('sender') ?? route.get('chatSender'));
        from.value = saved.fromDate ?? route.get('fromDate') ?? route.get('chatFromDate') ?? '';
        to.value = saved.toDate ?? route.get('toDate') ?? route.get('chatToDate') ?? '';
        filters.open = Boolean(sender.value || from.value || to.value);
        filters.hidden = false;
      }
    }
    search.hidden = false;
    let generation = 0;
    let timer;

    function update(event) {
      clearTimeout(timer);
      const turn = ++generation;
      const query = input.value.trim();
      const senderName = sender?.value.trim() || '';
      const fromDate = from?.value || '';
      const toDate = to?.value || '';
      const active = Boolean(query || senderName || fromDate || toDate);
      if (event || !active) {
        results.replaceChildren();
        results.hidden = true;
        status.textContent = '';
      }
      main.classList.toggle('offline-searching', active);
      if (!active) return;
      if (fromDate && toDate && fromDate > toDate) {
        status.textContent = filters.dataset.dateOrder;
        return;
      }
      if (query.length > 400 || Array.from(query).length > 200) {
        results.replaceChildren();
        results.hidden = true;
        status.textContent = search.dataset.tooLong;
        return;
      }
      timer = setTimeout(async () => {
        const registration = await navigator.serviceWorker.getRegistration('/').catch(() => null);
        const hits = await request(registration?.active, {type: 'oie:offline-history-search', ...scope, query,
          senderName, fromDate, toDate, timeZone: window.OieTime?.timeZone() || 'UTC'});
        if (generation !== turn) return;
        const focused = results.contains(document.activeElement) ? document.activeElement.href : '';
        if (!Array.isArray(hits)) {
          results.replaceChildren();
          results.hidden = true;
          if (focused) input.focus();
          status.textContent = search.dataset.failed;
          return;
        }
        status.textContent = hits.length ? search.dataset.count.replace('{count}', String(hits.length)) : search.dataset.empty;
        const rows = document.createDocumentFragment();
        for (const hit of hits) {
          const row = document.createElement('li');
          const link = document.createElement('a');
          const route = new URLSearchParams(params);
          route.set('notificationEngine', scope.engine);
          route.set('account', hit.account);
          route.set('chat', hit.chat);
          route.set('message', hit.message);
          link.href = '/?' + route;
          const heading = document.createElement('strong');
          heading.textContent = [scope.chat ? '' : hit.title, scope.chat ? '' : hit.label, hit.sender].filter(Boolean).join(' · ');
          const match = document.createElement('mark');
          match.textContent = hit.match;
          link.append(heading, hit.before, match, hit.after);
          row.append(link);
          rows.append(row);
        }
        results.replaceChildren(rows);
        results.hidden = !hits.length;
        if (focused) {
          const next = [...results.querySelectorAll('a')].find(link => link.href === focused);
          (next ?? input).focus({preventScroll: Boolean(next)});
        }
      }, 100);
    }
    function remember() {
      // Preserve Back from a result without putting the offline query in a URL.
      history.replaceState({...history.state, offlineSearch: {engine: scope.engine, chat: scope.chat, query: input.value,
        sender: sender?.value || '', fromDate: from?.value || '', toDate: to?.value || ''}}, '');
    }
    input.addEventListener('change', remember);
    input.addEventListener('input', update);
    filters?.addEventListener('input', update);
    filters?.addEventListener('change', remember);
    results.addEventListener('click', remember);
    input.addEventListener('keydown', event => {
      if (event.key === 'Escape' && input.value) {
        event.preventDefault();
        input.value = '';
        remember();
        update(event);
      }
    });
    refreshSearch = entry => { updateSenders(entry, sender?.value); update(); };
    stopSearch = () => {
      refreshSearch = null;
      input.value = '';
      if (filters) {
        sender.value = from.value = to.value = '';
        filters.open = false;
        filters.hidden = true;
      }
      update();
      history.replaceState({...history.state, offlineSearch: null}, '');
      input.removeEventListener('input', update);
      filters?.removeEventListener('input', update);
      filters?.removeEventListener('change', remember);
      input.removeEventListener('change', remember);
      results.removeEventListener('click', remember);
      stopSearch = null;
      search.hidden = true;
    };
    refreshSearch();
  }

  function savedSearchRoute(query) {
    if (location.pathname !== '/search') {
      const notificationEngine = query.get('notificationEngine');
      const compact = query.get('searchMode') === 'messages';
      const chatSearch = query.get('chatSearch') === '1';
      const showChat = !compact || chatSearch || query.has('message');
      const accounts = compact ? query.getAll('searchAccount') : [];
      const searchEngine = compact ? query.get('searchEngine') : null;
      const conversation = compact ? query.get('searchConversation') : null;
      const chat = showChat ? query.get('chat') : conversation;
      const valid = accounts.length <= 100 && accounts.every(uuid) && (!conversation || uuid(conversation)) &&
        (!conversation || !showChat || conversation === chat) && (!chatSearch || uuid(chat)) &&
        (!notificationEngine || !searchEngine || notificationEngine === searchEngine);
      return {account: showChat ? query.get('account') : null, accounts, chat,
        expectedEngine: searchEngine || notificationEngine, valid,
        initialQuery: chatSearch ? query.get('chatQuery') : !showChat ? query.get('searchQuery') : ''};
    }
    // The native account picker overrides the original form's account filters.
    const picker = query.has('nativeAccountFilter') || query.has('nativeAccountId');
    const accounts = query.getAll(picker ? 'nativeAccountId' : 'accountIds');
    const account = picker ? null : query.get('accountId') || null;
    const chat = query.get('conversationId') || null;
    const expectedEngine = query.get('engineId') || null;
    const valid = !(account && accounts.length) && (!account || uuid(account)) &&
      (!chat || uuid(chat)) && (!expectedEngine || uuid(expectedEngine)) &&
      accounts.length <= 100 && accounts.every(uuid) && accounts.length === new Set(accounts).size;
    return {account, accounts, chat, expectedEngine, valid, initialQuery: query.get('q')};
  }

  async function renderOffline() {
    const turn = ++renderTurn;
    const state = await read().catch(() => null);
    if (turn !== renderTurn) return;
    const refresh = Boolean(displayedEngine);
    const title = document.getElementById('offline-title');
    const empty = document.getElementById('offline-empty');
    const back = document.getElementById('offline-back');
    const retry = document.getElementById('offline-retry');
    const query = new URLSearchParams(location.search);
    const lang = document.documentElement.lang;
    const theme = query.get('theme') || state?.theme;
    if (theme === 'light' || theme === 'dark') document.documentElement.style.colorScheme = theme;
    const {account, accounts, chat, expectedEngine, valid} = savedSearchRoute(query);
    const matchesAccount = entry => (!account || entry.account === account) && (!accounts.length || accounts.includes(entry.account));
    const message = location.pathname === '/search' ? null : query.get('message');
    // A mounted reader stays bound to the engine that supplied its first snapshot.
    displayedEngine ??= state?.engine;
    const currentScope = !state?.blocked && displayedEngine === state?.engine && !revokedEngines.has(displayedEngine);
    if (!currentScope) stopSearch?.();
    const entries = !valid || !currentScope || expectedEngine && expectedEngine !== displayedEngine ? [] :
      (state?.entries ?? []).filter(matchesAccount);
    const params = new URLSearchParams({lang});
    if (theme === 'light' || theme === 'dark') params.set('theme', theme);
    if (account) params.set('account', account);
    back.href = '/?' + params;
    if (chat) params.set('chat', chat);
    if (message) params.set('message', message);
    if (expectedEngine) params.set('notificationEngine', expectedEngine);
    retry.href = location.pathname === '/ui/offline-history' ? '/?' + params : location.href;
    if (!chat) {
      if (entries.length) enableOfflineSearch({engine: displayedEngine, account, accounts, chat: null}, params);
      const list = document.getElementById('offline-conversations');
      const focused = list.contains(document.activeElement) ? document.activeElement.href : '';
      list.replaceChildren();
      for (const entry of entries) {
        const item = document.createElement('li'), link = document.createElement('a');
        const route = new URLSearchParams(params);
        route.set('account', entry.account);
        route.set('chat', entry.chat);
        link.href = '/?' + route;
        link.textContent = entry.label ? entry.title + ' · ' + entry.label : entry.title;
        item.append(link);
        list.append(item);
      }
      if (focused) [...list.querySelectorAll('a')].find(link => link.href === focused)?.focus({preventScroll: true});
      if (!entries.length) refreshSearch?.();
      list.hidden = !entries.length;
      empty.hidden = entries.length > 0;
      return;
    }

    back.hidden = false;
    const inScope = valid && currentScope && (!expectedEngine || expectedEngine === displayedEngine);
    const full = document.getElementById('offline-full');
    if (full) full.hidden = !(inScope && state?.kept?.some(item =>
      item.full && item.chat === chat && matchesAccount(item)));
    const entry = entries.find(entry => entry.chat === chat);
    const list = document.getElementById('offline-messages');
    const scroll = list.scrollTop;
    const focused = document.activeElement?.closest('#offline-messages li')?.dataset.message;
    list.replaceChildren();
    list.hidden = true;
    if (!entry) {
      title.textContent = title.dataset.recentTitle;
      refreshSearch?.();
      empty.textContent = body.dataset.conversationMissing;
      empty.hidden = false;
      return;
    }
    enableOfflineSearch({engine: displayedEngine, account: entry.account, chat}, params, entry);
    title.textContent = entry.title;
    empty.hidden = true;
    const template = document.getElementById('offline-message');
    const dateOptions = {dateStyle: 'short', timeStyle: 'short'};
    const format = window.OieTime?.format ?? (date => new Intl.DateTimeFormat(lang, dateOptions).format(date));
    let target, focusedRow;
    for (const item of entry.messages) {
      const row = template.content.firstElementChild.cloneNode(true);
      row.dataset.message = item.id;
      row.classList.toggle('offline-own', item.own);
      row.querySelector('.offline-sender').textContent = item.sender;
      row.querySelector('p').textContent = item.withdrawn ? body.dataset[item.withdrawn] : item.text;
      const time = row.querySelector('time');
      time.dateTime = item.date;
      const date = new Date(item.date);
      if (Number.isFinite(date.getTime())) time.textContent = format(date, dateOptions);
      list.append(row);
      if (item.id === message) target = row;
      if (item.id === focused) focusedRow = row;
    }
    list.hidden = !entry.messages.length;
    if (message && !target) {
      empty.textContent = body.dataset.messageMissing;
      empty.hidden = false;
    }
    if (refresh) {
      list.scrollTop = scroll;
      if (focusedRow) {
        focusedRow.tabIndex = -1;
        focusedRow.focus({preventScroll: true});
      }
    } else if (target) {
      target.tabIndex = -1;
      target.focus();
    } else {
      list.scrollTop = list.scrollHeight;
    }
  }
})();
