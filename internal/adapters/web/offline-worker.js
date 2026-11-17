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

function recent(entries) {
  return entries.filter(entry => Date.now() - entry.savedAt < maxAge);
}

function read() {
  return state(current => {
    const entries = recent(current.entries);
    if (entries.length !== current.entries.length) return {...current, entries};
  });
}

function newest(engine, previous, incoming) {
  return cursor(engine, incoming) > cursor(engine, previous) ? incoming : previous;
}

function save(current, data) {
  const sequence = cursor(data.engine, data.cursor);
  if (!id.test(data.engine) || current.blocked || current.engine && current.engine !== data.engine ||
      sequence < 0n || sequence < cursor(data.engine, current.minimum)) return;
  const next = {...current, engine: data.engine, lang: data.lang, theme: data.theme, entries: recent(current.entries)};
  if (!current.engine) next.checkpoint = data.cursor;
  const {entry} = data;
  if (!entry || !id.test(entry.account) || !id.test(entry.chat)) return next;
  if (current.missing?.some(item => item.chat === entry.chat && (!item.account || item.account === entry.account) &&
      sequence <= cursor(data.engine, item.through))) return next;
  // Make room for the newest messages even if the full loaded page is large.
  const messages = [];
  let bytes = encoder.encode(JSON.stringify({...entry, messages})).byteLength;
  for (const message of entry.messages.slice(-50).reverse()) {
    const size = encoder.encode(JSON.stringify(message)).byteLength + 1;
    if (bytes + size > maxConversationBytes) break;
    bytes += size;
    messages.unshift(message);
  }
  const entries = next.entries;
  const previous = entries.find(item => item.chat === entry.chat && item.account === entry.account);
  if (previous && cursor(data.engine, previous.cursor) > cursor(data.engine, data.cursor)) return next;
  next.entries = [{...entry, messages, bytes, savedAt: Date.now(), cursor: data.cursor}];
  let total = bytes;
  for (const item of entries) {
    if (item === previous) continue;
    if (next.entries.length >= maxConversations || total + item.bytes > maxBytes) break;
    total += item.bytes;
    next.entries.push(item);
  }
  return next;
}

function progress(current, {engine, through}) {
  if (current.blocked || engine !== current.engine || cursor(engine, through) <= cursor(engine, current.checkpoint)) return;
  return {...current, checkpoint: through};
}

function invalidate(current, {engine, account, chat, through, confirmedMissing = false}) {
  if (current.blocked || engine !== current.engine || cursor(engine, through) < 0n) return;
  const sequence = cursor(engine, through);
  const minimum = newest(engine, current.minimum, through);
  const checkpoint = newest(engine, current.checkpoint, through);
  const entries = current.entries.filter(entry => (account && entry.account !== account) || (chat && entry.chat !== chat) ||
    (confirmedMissing ? cursor(engine, entry.cursor) > sequence : cursor(engine, entry.cursor) >= sequence));
  let missing = current.missing;
  if (confirmedMissing) {
    // Fence a delayed snapshot read from the same event position as this 404.
    const previous = (missing ?? []).find(item => item.chat === chat && item.account === account);
    const other = (missing ?? []).filter(item => item !== previous);
    missing = [{account, chat, through: newest(engine, previous?.through, through)}, ...other].slice(0, maxConversations);
  }
  return {...current, minimum, checkpoint, entries, missing};
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
  if ((path === '/' || path === shell) && data?.type === 'oie:offline-history-read' && port) {
    event.waitUntil(read().then(value => {
      port.postMessage(value);
    }).catch(() => port.postMessage(null)).finally(() => port.close()));
    return;
  }
  if (path !== '/') return;
  const updates = {
    'oie:offline-history-save': current => save(current, data),
    'oie:offline-history-invalidate': current => invalidate(current, data),
    'oie:offline-history-progress': current => progress(current, data),
    'oie:offline-history-clear': current => {
      if (data.engine && data.engine !== current.engine) return;
      if (data.blocked || !data.through) {
        verifiedNavigation = ++navigation;
        return {...current, blocked: true, entries: []};
      }
      const minimum = newest(current.engine, current.minimum, data.through);
      const checkpoint = newest(current.engine, current.checkpoint, data.through);
      const entries = current.entries.filter(entry => cursor(current.engine, entry.cursor) >= cursor(current.engine, data.through));
      return {...current, minimum, checkpoint, entries};
    },
  };
  if (Object.hasOwn(updates, data?.type)) event.waitUntil(state(updates[data.type]).catch(() => {}));
});

function conversationURL(url) {
  return url.pathname === '/' && ['chats', 'archived'].includes(url.searchParams.get('section') || 'chats') &&
    !url.searchParams.has('open') && !url.searchParams.has('searchQuery');
}

async function offlineResponse(url, fallback) {
  if (!conversationURL(url)) return fallback ?? Response.error();
  const data = await read().catch(() => null);
  if (fallback && !data?.entries.length) return fallback;
  const lang = url.searchParams.get('lang') || data?.lang;
  const page = await caches.match(shell + '?lang=' + (lang === 'pt-BR' ? 'pt-BR' : 'en'), {cacheName: shellCache});
  if (!page) return fallback ?? Response.error();
  return new Response(page.body, {status: fallback?.status ?? 200, headers: page.headers});
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
    const missingChat = response.status === 404 && id.test(chat) &&
      (account === null || id.test(account)) && !url.searchParams.has('notificationEngine');
    await state(current => {
      if (turn < verifiedNavigation) return;
      verifiedNavigation = turn;
      const verified = verify(current, engine, through);
      return (missingChat && invalidate(verified, {engine, account, chat, through, confirmedMissing: true})) ||
        (verified !== current ? verified : undefined);
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
          return {...current, blocked: true, entries: []};
        }).catch(() => {});
      }
      return response;
    })());
  } else if (request.method === 'GET' && request.mode === 'navigate' && url.pathname === '/') {
    event.respondWith(workspace(request, url));
  } else if (request.method === 'GET' && scripts.includes(url.pathname)) {
    event.respondWith(fetch(request).catch(async () => (await caches.match(request, {cacheName: shellCache})) ?? Response.error()));
  }
});
