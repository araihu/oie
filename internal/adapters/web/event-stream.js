(() => {
  'use strict';

  const target = new EventTarget();
  const eventTypes = [
    'message.preview_changed', 'profile.avatar_changed', 'account.registered', 'account.updated', 'account.removed',
    'account.connection_changed', 'account.history_changed', 'conversation.created',
    'conversation.updated', 'conversation.history_pruned', 'message.created', 'message.updated',
    'conversation.room_session_changed', 'message.star_changed'
  ];
  const inboxEvents = new Set([
    'account.registered', 'account.removed', 'account.connection_changed',
    'conversation.created', 'conversation.updated', 'conversation.history_pruned', 'conversation.room_session_changed',
    'message.created', 'message.updated'
  ]);
  let source, engine = '', cursor = '', connected = false, replaced = false;

  function emit(type, detail) {
    target.dispatchEvent(new CustomEvent(type, {detail}));
  }

  function stop() {
    source?.close();
    source = undefined;
    connected = false;
  }

  function connect() {
    const connection = new EventSource('/ui/events?cursor=' + encodeURIComponent(cursor));
    source = connection;
    let liveAfter = null;
    connection.onopen = () => {
      liveAfter = null;
      connected = true;
      emit('open');
    };
    connection.onerror = () => {
      connected = false;
      window.OieInbox.invalidate();
      if (connection.readyState === EventSource.CLOSED) stop();
      emit('error');
    };
    function sequence(id) {
      const prefix = engine + ':';
      if (typeof id !== 'string' || !id.startsWith(prefix)) return null;
      const value = id.slice(prefix.length);
      return /^\d+$/.test(value) ? BigInt(value) : null;
    }
    connection.addEventListener('oie.live', event => {
      try { liveAfter = sequence(JSON.parse(event.data)); }
      catch { liveAfter = null; }
    });
    for (const type of eventTypes) {
      connection.addEventListener(type, event => {
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
      try {
        const reset = JSON.parse(event.data);
        if (reset.engineId !== engine) {
          markReplaced();
          emit('engine-changed');
          return;
        }
        stop();
        window.OieInbox.invalidate();
        cursor = reset.cursor;
        connect();
      } catch { emit('error'); }
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
    }
    if (!source && !replaced) connect();
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

  window.addEventListener('pagehide', stop);
  window.addEventListener('pageshow', event => {
    if (!event.persisted || !engine || replaced) return;
    window.OieInbox.invalidate();
    connect();
  });

  window.OieEvents = {target, sync, stop, latest, markReplaced, eventTypes, get connected() { return connected; }, get replaced() { return replaced; }};
})();
