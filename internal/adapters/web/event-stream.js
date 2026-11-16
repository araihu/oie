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
  const maxSequence = 9223372036854775807n;
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

  function sequence(id, owner = engine) {
    const prefix = owner + ':';
    if (!owner || typeof id !== 'string' || !id.startsWith(prefix)) return null;
    const value = id.slice(prefix.length);
    if (value.length > 19 || !/^(0|[1-9]\d*)$/.test(value)) return null;
    const parsed = BigInt(value);
    return parsed <= maxSequence ? parsed : null;
  }

  function connect() {
    if (source || retry !== undefined || suspended || replaced) return;
    const requested = sequence(cursor);
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
      activity();
    };
    connection.onerror = () => {
      if (source !== connection) return;
      window.OieHostedSession?.verify();
      reconnect(connection);
    };
    connection.addEventListener('oie.heartbeat', () => {
      if (source !== connection || !connected) return;
      delay = 500;
      activity();
    });
    connection.addEventListener('oie.live', event => {
      if (source !== connection) return;
      activity();
      try { liveAfter = sequence(JSON.parse(event.data)); }
      catch { liveAfter = null; }
      if (liveAfter === null || (requested !== null && liveAfter < requested)) {
        reconnect(connection);
        return;
      }
      if (!connected) {
        connected = true;
        emit('open');
      }
    });
    for (const type of eventTypes) {
      connection.addEventListener(type, event => {
        if (source !== connection || !connected) return;
        activity();
        const current = sequence(event.lastEventId), previous = sequence(cursor);
        if (current === null || (previous !== null && current < previous)) return;
        cursor = event.lastEventId;
        if (inboxEvents.has(type)) window.OieInbox.invalidate();
        emit(type, event);
        if (type === 'message.created' && liveAfter !== null && current > liveAfter) {
          emit('message.live', {engine, event});
        }
      });
    }
    connection.addEventListener('oie.reset', event => {
      if (source !== connection) return;
      try {
        const reset = JSON.parse(event.data);
        if (typeof reset.engineId !== 'string' || sequence(reset.cursor, reset.engineId) === null) throw new Error('invalid reset');
        if (reset.engineId !== engine) {
          markReplaced();
          emit('engine-changed');
          return;
        }
        cursor = reset.cursor;
        reconnect(connection);
      } catch { reconnect(connection); }
    });
  }

  function sync(id, snapshotCursor) {
    if (!id) return;
    if (id !== engine) {
      stop();
      if (engine) window.OieInbox.clear();
      engine = id;
      cursor = sequence(snapshotCursor) === null ? '' : snapshotCursor;
      replaced = false;
      delay = 500;
    }
    connect();
  }

  function latest(snapshotCursor) {
    const current = sequence(cursor), snapshot = sequence(snapshotCursor);
    if (current === null) return snapshot === null ? '' : snapshotCursor;
    return snapshot === null || current > snapshot ? cursor : snapshotCursor;
  }

  function markReplaced() {
    stop();
    replaced = true;
    window.OieInbox.clear();
  }

  function wake(online = false) {
    if (!engine || replaced || suspended) return;
    if (retry !== undefined && !online) return;
    if (source && Date.now() - lastActivity < idleTimeout) return;
    stop();
    connect();
  }

  function suspend() {
    suspended = true;
    stop();
  }

  window.addEventListener('pagehide', suspend);
  window.addEventListener('pageshow', event => {
    if (!event.persisted) return;
    suspended = false;
    window.OieInbox.invalidate();
    wake();
  });

  window.OieEvents = {target, sync, wake, suspend, latest, markReplaced, eventTypes, get connected() { return connected; }, get replaced() { return replaced; }};
})();
