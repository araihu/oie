'use strict';

const shellCache = 'oie-offline-shell-v1';
const shell = '/ui/offline-history';
const scripts = [shell + '.js', shell + '.css', shell + '-time.js'];
const assets = [shell + '?lang=en', shell + '?lang=pt-BR', ...scripts];
const database = 'oie.offline-history';
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const maxConversations = 12;
const maxBytes = 1024 * 1024;
const maxConversationBytes = 192 * 1024;
const maxAge = 7 * 24 * 60 * 60 * 1000;
const encoder = new TextEncoder();
let navigation = 0;
let verifiedNavigation = 0;

self.addEventListener('install', event => event.waitUntil((async () => {
  await (await caches.open(shellCache)).addAll(assets.map(path => new Request(path, {cache: 'reload'})));
  await self.skipWaiting();
})()));

self.addEventListener('activate', event => event.waitUntil(self.clients.claim()));

// One read/write transaction orders updates from all tabs, including during a worker update.
function state(update) {
  return new Promise((resolve, reject) => {
    const open = indexedDB.open(database, 1);
    open.onupgradeneeded = () => open.result.createObjectStore('history');
    open.onerror = () => reject(open.error);
    open.onsuccess = () => {
      const db = open.result;
      const tx = db.transaction('history', update ? 'readwrite' : 'readonly');
      const store = tx.objectStore('history');
      const query = store.get('current');
      let value;
      query.onsuccess = () => {
        const current = query.result ?? {engine: '', entries: []};
        const next = update?.(current);
        if (next) store.put(next, 'current');
        value = next ?? current;
      };
      tx.oncomplete = () => { db.close(); resolve(value); };
      tx.onabort = () => { db.close(); reject(tx.error); };
    };
  });
}

function cursor(engine, raw) {
  if (typeof engine !== 'string' || typeof raw !== 'string' || !raw.startsWith(engine + ':')) return -1n;
  const sequence = raw.slice(engine.length + 1);
  return /^\d{1,19}$/.test(sequence) ? BigInt(sequence) : -1n;
}

function sameChat(left, right) {
  return left.account === right.account && left.chat === right.chat;
}

function isKept(current, entry) {
  return current.kept?.some(chat => sameChat(chat, entry)) ?? false;
}

function keepStatus(current, scope) {
  const kept = current.kept?.find(item => sameChat(item, scope));
  return {
    kept: Boolean(kept),
    full: kept?.full === true,
    available: current.entries.some(entry => sameChat(entry, scope) &&
      (kept || Date.now() - entry.savedAt < maxAge)),
  };
}

async function notifyKeep(engine, scope, status, exceptID) {
  const {account, chat} = scope;
  for (const client of await self.clients.matchAll({type: 'window', includeUncontrolled: true})) {
    if ((!exceptID || client.id !== exceptID) && ['/', '/search'].includes(new URL(client.url).pathname)) {
      client.postMessage({type: 'oie:offline-keep-changed', engine, account, chat, ...status});
    }
  }
}

function recent(current) {
  const cutoff = Date.now() - maxAge;
  return current.entries.filter(entry => entry.savedAt > cutoff || isKept(current, entry));
}

function read() {
  return state(current => {
    const entries = recent(current);
    if (entries.length !== current.entries.length) return {...current, entries};
  });
}

function newest(engine, previous, incoming) {
  return cursor(engine, incoming) > cursor(engine, previous) ? incoming : previous;
}

function canSave(current, data) {
  const sequence = cursor(data.engine, data.cursor);
  return id.test(data.engine) && !current.blocked && (!current.engine || current.engine === data.engine) &&
    sequence >= 0n && sequence >= cursor(data.engine, current.minimum);
}

function snapshotEntry(current, data) {
  const {entry} = data;
  if (!entry || !id.test(entry.account) || !id.test(entry.chat) || !Array.isArray(entry.messages)) return;
  const sequence = cursor(data.engine, data.cursor);
  if (current.missing?.some(item => (!item.chat || item.chat === entry.chat) && (!item.account || item.account === entry.account) &&
      sequence <= cursor(data.engine, item.through))) return;
  // Make room for the newest messages even if the full loaded page is large.
  const messages = [];
  let bytes = encoder.encode(JSON.stringify({...entry, messages})).byteLength;
  for (const message of entry.messages.slice(-50).reverse()) {
    const size = encoder.encode(JSON.stringify(message)).byteLength + 1;
    if (bytes + size > maxConversationBytes) break;
    bytes += size;
    messages.unshift(message);
  }
  return {...entry, messages, bytes, savedAt: Date.now(), cursor: data.cursor};
}

function fitSnapshot(current, entry) {
  const others = recent(current).filter(item => !sameChat(item, entry));
  const entries = [entry, ...others.filter(item => isKept(current, item))];
  let total = entries.reduce((bytes, item) => bytes + item.bytes, 0);
  if (entries.length > maxConversations || total > maxBytes) return;
  for (const item of others) {
    if (entries.length === maxConversations) break;
    if (isKept(current, item) || total + item.bytes > maxBytes) continue;
    total += item.bytes;
    entries.push(item);
  }
  return entries;
}

function recordCapacity(current, entry, full) {
  const pin = current.kept?.find(item => sameChat(item, entry));
  const previous = current.entries.find(item => sameChat(item, entry));
  const sequence = cursor(current.engine, entry.cursor);
  if (cursor(current.engine, pin?.cursor) > sequence || cursor(current.engine, previous?.refreshBlockedAt) > sequence) {
    return current;
  }
  if (full && previous && previous.refreshBlockedAt !== entry.cursor) {
    current = {...current, entries: current.entries.map(item => item === previous ? {...item, refreshBlockedAt: entry.cursor} : item)};
  }
  if (!pin) return current;
  return {...current, kept: current.kept.map(item => item === pin ? {...pin, cursor: entry.cursor, full} : item)};
}

function save(current, data) {
  if (!canSave(current, data)) return;
  const next = {...current, engine: data.engine, lang: data.lang, theme: data.theme, entries: recent(current)};
  if (!current.engine) next.checkpoint = data.cursor;
  const entry = snapshotEntry(current, data);
  if (!entry) return next;
  const previous = next.entries.find(item => sameChat(item, entry));
  const pin = next.kept?.find(item => sameChat(item, entry));
  const sequence = cursor(data.engine, entry.cursor);
  if (cursor(data.engine, previous?.cursor) > sequence || cursor(data.engine, previous?.refreshBlockedAt) > sequence ||
      cursor(data.engine, pin?.cursor) > sequence) return next;
  const entries = fitSnapshot(next, entry);
  if (entries) next.entries = entries;
  return recordCapacity(next, entry, !entries);
}

function keepChat(current, data) {
  const {engine, account, chat, keep, snapshot} = data;
  if (current.blocked || current.engine && current.engine !== engine) return {error: 'unavailable'};
  const kept = current.kept ?? [];
  if (!keep) {
    const next = {...current, kept: kept.filter(item => !sameChat(item, data))};
    return {next: {...next, entries: recent(next)}};
  }
  if (isKept(current, data)) return {};
  let next = current;
  let entry = recent(current).find(item => sameChat(item, data));
  if (snapshot && (snapshot.engine !== engine || !snapshot.entry || !sameChat(snapshot.entry, data))) return {error: 'unavailable'};
  const incoming = cursor(engine, snapshot?.cursor);
  const blocked = cursor(engine, entry?.refreshBlockedAt);
  if (blocked >= 0n && incoming < blocked) return {error: 'full'};

  // Prefer a more recent copy already saved by another tab over the mounted page.
  if (snapshot && incoming >= cursor(engine, entry?.cursor) && canSave(current, snapshot)) {
    const candidate = snapshotEntry(current, snapshot);
    if (!candidate) return {error: 'unavailable'};
    const entries = fitSnapshot(current, candidate);
    if (!entries) {
      // Remember this refusal even when the automatic save has not arrived yet.
      return {error: 'full', next: recordCapacity(current, candidate, true)};
    }
    entry = candidate;
    next = {...current, engine, checkpoint: current.checkpoint ?? snapshot.cursor, lang: snapshot.lang, theme: snapshot.theme, entries};
  } else if (blocked >= 0n) {
    return {error: 'full'};
  } else if (!entry) {
    return {error: snapshot ? 'unavailable' : 'missing'};
  }
  return {next: {...next, kept: [...kept, {account, chat, cursor: entry.cursor, full: false}]}};
}

function progress(current, {engine, through}) {
  const checkpoint = cursor(engine, current.checkpoint);
  if (current.blocked || engine !== current.engine || checkpoint < 0n ||
      cursor(engine, through) !== checkpoint + 1n) return;
  return {...current, checkpoint: through};
}

function invalidate(current, {engine, account, chat, through, confirmedMissing = false, removeKeep = false}) {
  if (current.blocked || engine !== current.engine || cursor(engine, through) < 0n) return;
  const sequence = cursor(engine, through);
  const minimum = newest(engine, current.minimum, through);
  const checkpoint = confirmedMissing ? current.checkpoint : progress(current, {engine, through})?.checkpoint ?? current.checkpoint;
  const removed = confirmedMissing || removeKeep;
  const outside = item => (account && item.account !== account) || (chat && item.chat !== chat);
  const entries = current.entries.filter(entry => outside(entry) ||
    (removed ? cursor(engine, entry.cursor) > sequence : cursor(engine, entry.cursor) >= sequence));
  const kept = removed ? current.kept?.filter(item => outside(item) || cursor(engine, item.cursor) > sequence ||
    entries.some(entry => sameChat(entry, item))) : current.kept;
  let missing = current.missing;
  if (removed) {
    // A delayed snapshot from this event position cannot restore removed history.
    const previous = (missing ?? []).find(item => item.chat === chat && item.account === account);
    const other = (missing ?? []).filter(item => item !== previous);
    missing = [{account, chat, through: newest(engine, previous?.through, through)}, ...other].slice(0, maxConversations);
  }
  return {...current, minimum, checkpoint, entries, missing, kept};
}

function verify(current, engine, through) {
  const sequence = cursor(engine, through);
  if (sequence < 0n) return {engine: '', blocked: true, entries: []};
  if (current.engine !== engine || current.blocked) return {engine, checkpoint: through, entries: []};
  if (sequence <= cursor(engine, current.checkpoint)) return current;
  // A fresh document starts its stream at this cursor. Changes in the gap
  // were not seen by a page that could invalidate the older snapshots.
  const entries = current.entries.filter(entry => cursor(engine, entry.cursor) >= sequence);
  return {...current, checkpoint: through, minimum: newest(engine, current.minimum, through), entries};
}

self.addEventListener('message', event => {
  if (event.source?.type !== 'window') return;
  const path = new URL(event.source.url).pathname;
  const data = event.data;
  const port = event.ports[0];
  const workspacePath = path === '/' || path === '/search';
  if ((workspacePath || path === shell) && data?.type === 'oie:offline-history-read' && port) {
    event.waitUntil(read().then(value => {
      port.postMessage(value);
    }, () => port.postMessage(null)).finally(() => port.close()));
    return;
  }
  if (!workspacePath) return;
  if (port && data && ['oie:offline-keep-status', 'oie:offline-keep-chat'].includes(data.type) &&
      [data.engine, data.account, data.chat].every(value => typeof value === 'string' && id.test(value))) {
    const write = data.type === 'oie:offline-keep-chat' && typeof data.keep === 'boolean';
    let error;
    let changed = false;
    const update = write ? current => {
      const result = keepChat(current, data);
      error = result.error;
      changed = Boolean(result.next && !result.error);
      return result.next;
    } : undefined;
    const reply = async current => {
      const matches = !current.blocked && current.engine === data.engine;
      const status = matches ? keepStatus(current, data) : {kept: false, available: false, full: false};
      if (!matches && (current.engine || current.blocked)) error = 'unavailable';
      port.postMessage({...status, error});
      if (changed) await notifyKeep(data.engine, data, status, event.source.id);
    };
    event.waitUntil(state(update).then(reply, async cause => {
      if (cause?.name !== 'QuotaExceededError') return port.postMessage(null);
      changed = false;
      error = 'full';
      // The transaction rolled back. Report the previously committed setting.
      return state().then(reply, () => port.postMessage(null));
    }).finally(() => port.close()));
    return;
  }
  const updates = {
    'oie:offline-history-save': current => save(current, data),
    'oie:offline-history-invalidate': current => invalidate(current, data),
    'oie:offline-history-progress': current => progress(current, data),
    'oie:offline-history-clear': current => {
      if (data.engine && data.engine !== current.engine) return;
      if (data.blocked || !data.through) {
        verifiedNavigation = ++navigation;
        return {...current, blocked: true, entries: [], kept: []};
      }
      const minimum = newest(current.engine, current.minimum, data.through);
      const checkpoint = newest(current.engine, current.checkpoint, data.through);
      const entries = current.entries.filter(entry => cursor(current.engine, entry.cursor) >= cursor(current.engine, data.through));
      return {...current, minimum, checkpoint, entries};
    },
  };
  if (!Object.hasOwn(updates, data?.type)) return;
  if (data.type === 'oie:offline-history-progress') {
    event.waitUntil(state(updates[data.type]).catch(() => {}));
    return;
  }
  event.waitUntil(updateOfflineState(data, updates[data.type]).catch(() => {}));
});

// Notify only after the transaction commits. Snapshots can be updated by any tab.
async function updateOfflineState(data, update) {
  let changes = [];
  const apply = mutation => state(current => {
    const next = mutation(current);
    changes = [];
    if (!next) return;
    const affected = data.entry ? [data.entry] : current.kept ?? [];
    for (const pin of affected) {
      const before = keepStatus(current, pin);
      if (!before.kept) continue;
      const after = keepStatus(next, pin);
      if (before.kept !== after.kept || before.full !== after.full || before.available !== after.available) {
        changes.push({pin, after});
      }
    }
    return next;
  });
  try {
    await apply(update);
  } catch (cause) {
    if (cause?.name !== 'QuotaExceededError' || !data.entry) throw cause;
    // Recording a boolean on the existing pin does not require the larger snapshot.
    await apply(current => {
      if (!canSave(current, data)) return;
      const cached = current.entries.find(item => sameChat(item, data.entry));
      if (cursor(data.engine, cached?.cursor) > cursor(data.engine, data.cursor)) return;
      return recordCapacity(current, {...data.entry, cursor: data.cursor}, true);
    });
  }
  await Promise.all(changes.map(({pin, after}) => notifyKeep(data.engine, pin, after)));
}

function conversationURL(url) {
  return url.pathname === '/' && ['chats', 'archived'].includes(url.searchParams.get('section') || 'chats') &&
    (!url.searchParams.has('open') || url.searchParams.get('open') === 'details' && id.test(url.searchParams.get('chat'))) &&
    !url.searchParams.has('searchQuery');
}

async function offlineResponse(url, fallback) {
  if (!conversationURL(url)) return fallback ?? Response.error();
  const data = await read().catch(() => null);
  if (fallback && !data?.entries.length && !data?.kept?.some(item => item.full)) return fallback;
  const lang = url.searchParams.get('lang') || data?.lang;
  const page = await caches.match(shell + '?lang=' + (lang === 'pt-BR' ? 'pt-BR' : 'en'), {cacheName: shellCache});
  if (!page) return fallback ?? Response.error();
  return new Response(page.body, {status: fallback?.status ?? 200, headers: page.headers});
}

async function notifyReset(engine) {
  const windows = await self.clients.matchAll({type: 'window', includeUncontrolled: true});
  for (const client of windows) {
    const url = new URL(client.url);
    if (url.origin === self.location.origin && (url.pathname === '/' || url.pathname === shell)) {
      client.postMessage({type: 'oie:offline-history-reset', engine});
    }
  }
}

async function workspace(request, url) {
  const turn = ++navigation;
  let response;
  try {
    response = await fetch(request);
  } catch (error) {
    if (request.signal.aborted) throw error;
    return offlineResponse(url);
  }
  const engine = response.headers.get('X-Oie-Offline-History');
  if (id.test(engine)) {
    const through = response.headers.get('X-Oie-Offline-Cursor');
    const chat = url.searchParams.get('chat');
    const account = url.searchParams.get('account');
    const missingChat = url.pathname === '/' && response.status === 404 && id.test(chat) &&
      (account === null || id.test(account)) && !url.searchParams.has('notificationEngine');
    const removal = {engine, account, chat, through, confirmedMissing: true};
    await state(current => {
      if (turn < verifiedNavigation) {
        // A newer response can establish engine scope without proving that
        // another conversation still exists.
        return missingChat ? invalidate(current, removal) : undefined;
      }
      verifiedNavigation = turn;
      const verified = verify(current, engine, through);
      const next = missingChat ? invalidate(verified, removal) ?? verified : verified;
      if (next !== current) return next;
    }).catch(() => {});
  } else if (engine === 'unavailable' && response.status === 503) {
    return offlineResponse(url, response);
  } else if ([200, 401, 403, 503].includes(response.status) || response.type === 'opaqueredirect' || response.redirected) {
    // The origin may have been reconfigured to require authentication.
    await state(() => {
      if (turn < verifiedNavigation) return;
      verifiedNavigation = turn;
      return {engine: '', blocked: true, entries: []};
    }).catch(() => {});
  }
  return response;
}

self.addEventListener('fetch', event => {
  const {request} = event;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (request.method === 'POST' && url.pathname === '/ui/engine/reset') {
    event.respondWith((async () => {
      const response = await fetch(request);
      const engine = response.headers.get('X-Oie-Reset-Engine');
      if (response.status === 204 && id.test(engine)) {
        await state(current => {
          if (current.engine !== engine) return;
          verifiedNavigation = ++navigation;
          return {...current, blocked: true, entries: [], kept: []};
        }).then(() => notifyReset(engine)).catch(() => {});
      }
      return response;
    })());
  } else if (request.method === 'GET' && request.mode === 'navigate' && ['/', '/search'].includes(url.pathname)) {
    event.respondWith(workspace(request, url));
  } else if (request.method === 'GET' && scripts.includes(url.pathname)) {
    event.respondWith(fetch(request).catch(async () => (await caches.match(request, {cacheName: shellCache})) ?? Response.error()));
  }
});
