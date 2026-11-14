(() => {
  'use strict';

  const target = new EventTarget();
  const eventTypes = [
    'message.preview_changed', 'profile.avatar_changed', 'account.registered',
    'account.connection_changed', 'account.history_changed', 'conversation.created',
    'conversation.updated', 'message.created', 'message.updated',
    'conversation.room_session_changed', 'message.star_changed'
  ];
  const inboxEvents = new Set(['account.registered', 'conversation.created', 'conversation.updated', 'message.created', 'message.updated']);
  let source, engine = '', cursor = '', connected = false, replaced = false;

  function emit(type, event) {
    target.dispatchEvent(new CustomEvent(type, {detail: event}));
  }

  function stop() {
    source?.close();
    source = undefined;
    connected = false;
  }

  function connect() {
    const connection = new EventSource('/ui/events?cursor=' + encodeURIComponent(cursor));
    source = connection;
    connection.onopen = () => {
      connected = true;
      emit('open');
    };
    connection.onerror = () => {
      connected = false;
      window.OieInbox.invalidate();
      if (connection.readyState === EventSource.CLOSED) stop();
      emit('error');
    };
    for (const type of eventTypes) {
      connection.addEventListener(type, event => {
        if (event.lastEventId) cursor = event.lastEventId;
        if (inboxEvents.has(type)) window.OieInbox.invalidate();
        emit(type, event);
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
