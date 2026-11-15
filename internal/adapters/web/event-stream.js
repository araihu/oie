(() => {
  'use strict';

  const target = new EventTarget();
  const eventTypes = [
    'message.preview_changed', 'profile.avatar_changed', 'account.registered', 'account.updated', 'account.removed',
    'account.connection_changed', 'account.history_changed', 'conversation.created',
    'conversation.updated', 'conversation.history_pruned', 'message.created', 'message.updated',
    'conversation.room_session_changed', 'message.star_changed'
  ];
  const inboxEvents = new Set(['account.registered', 'account.removed', 'conversation.created', 'conversation.updated', 'conversation.history_pruned', 'message.created', 'message.updated']);
  const idleTimeout = 40000;
  let source, retry, watchdog, lastActivity = 0, delay = 500;
  let engine = '', cursor = '', connected = false, replaced = false, suspended = false;

  function emit(type, detail) {
    target.dispatchEvent(new CustomEvent(type, {detail}));
  }

  function stop() {
    clearTimeout(retry);
    clearTimeout(watchdog);
    retry = undefined;
    source?.close();
    source = undefined;
    connected = false;
  }

  function reconnect(connection) {
    if (source !== connection) return;
    stop();
    window.OieInbox.invalidate();
    emit('error');
    if (suspended || replaced) return;
    retry = setTimeout(() => { retry = undefined; connect(); }, delay * (0.75 + Math.random() * 0.25));
    delay = Math.min(15000, delay * 2);
  }

  function connect() {
    if (source || retry !== undefined || suspended || replaced) return;
    const connection = new EventSource('/ui/events?cursor=' + encodeURIComponent(cursor));
    source = connection;
    let liveAfter = null;
    function activity() {
      lastActivity = Date.now();
      clearTimeout(watchdog);
      watchdog = setTimeout(() => reconnect(connection), idleTimeout);
    }
    activity();
    connection.onopen = () => {
      if (source !== connection) return;
      connected = true;
      activity();
      emit('open');
    };
    connection.onerror = () => reconnect(connection);
    connection.addEventListener('oie.heartbeat', () => {
      if (source !== connection) return;
      delay = 500;
      activity();
    });
    function sequence(id) {
      const prefix = engine + ':';
      if (typeof id !== 'string' || !id.startsWith(prefix)) return null;
      const value = id.slice(prefix.length);
      return /^\d+$/.test(value) ? BigInt(value) : null;
    }
    connection.addEventListener('oie.live', event => {
      if (source !== connection) return;
      activity();
      try { liveAfter = sequence(JSON.parse(event.data)); }
      catch { liveAfter = null; }
    });
    for (const type of eventTypes) {
      connection.addEventListener(type, event => {
        if (source !== connection) return;
        activity();
        if (event.lastEventId) cursor = event.lastEventId;
        if (inboxEvents.has(type)) window.OieInbox.invalidate();
        emit(type, event);
        if (type === 'message.created' && liveAfter !== null) {
          const current = sequence(event.lastEventId);
          if (current !== null && current > liveAfter) {
            emit('message.live', {engine, event});
          }
        }
      });
    }
    connection.addEventListener('oie.reset', event => {
      if (source !== connection) return;
      try {
        const reset = JSON.parse(event.data);
        if (typeof reset.engineId !== 'string' || typeof reset.cursor !== 'string') throw new Error('invalid reset');
        if (reset.engineId !== engine) {
          markReplaced();
          emit('engine-changed');
          return;
        }
        stop();
        window.OieInbox.invalidate();
        cursor = reset.cursor;
        connect();
      } catch { reconnect(connection); }
    });
  }

  function sync(id, snapshotCursor) {
    if (!id) return;
    if (id !== engine) {
      stop();
      if (engine) window.OieInbox.clear();
      engine = id;
      cursor = snapshotCursor;
      replaced = false;
      delay = 500;
    }
    connect();
  }

  function latest(snapshotCursor) {
    if (!cursor || !snapshotCursor) return snapshotCursor || cursor;
    if (cursor === snapshotCursor) return cursor;
    const prefix = engine + ':';
    if (![cursor, snapshotCursor].every(value => value.startsWith(prefix) && /^\d+$/.test(value.slice(prefix.length)))) return snapshotCursor;
    return BigInt(cursor.slice(prefix.length)) > BigInt(snapshotCursor.slice(prefix.length)) ? cursor : snapshotCursor;
  }

  function markReplaced() {
    stop();
    replaced = true;
    window.OieInbox.clear();
  }

  function wake() {
    if (!engine || replaced || suspended) return;
    if (source && Date.now() - lastActivity < idleTimeout) return;
    stop();
    connect();
  }

  window.addEventListener('pagehide', () => { suspended = true; stop(); });
  window.addEventListener('pageshow', event => {
    if (!event.persisted) return;
    suspended = false;
    window.OieInbox.invalidate();
    wake();
  });

  window.OieEvents = {target, sync, wake, latest, markReplaced, eventTypes, get connected() { return connected; }, get replaced() { return replaced; }};
})();
