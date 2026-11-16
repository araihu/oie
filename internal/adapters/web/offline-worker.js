'use strict';

const shellCache = 'oie-offline-shell-v1';
const shell = '/ui/offline-history';
const assets = [shell + '?lang=en', shell + '?lang=pt-BR', shell + '.js', shell + '.css'];
const database = 'oie.offline-history';
const id = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const maxConversations = 12;
const maxBytes = 1024 * 1024;
const maxConversationBytes = 192 * 1024;
const maxAge = 7 * 24 * 60 * 60 * 1000;
const encoder = new TextEncoder();
let confirmedEngine;

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
  if (typeof raw !== 'string' || !raw.startsWith(engine + ':')) return -1n;
  const sequence = raw.slice(engine.length + 1);
  return /^\d{1,19}$/.test(sequence) ? BigInt(sequence) : -1n;
}

function recent(entries) {
  return entries.filter(entry => Date.now() - entry.savedAt < maxAge);
}

function save(current, data) {
  if (!id.test(data.engine) || current.blocked || current.engine && current.engine !== data.engine ||
      cursor(data.engine, data.cursor) < 0n ||
      cursor(data.engine, data.cursor) < cursor(data.engine, current.minimum)) return;
  const next = {...current, engine: data.engine, lang: data.lang, theme: data.theme};
  const {entry} = data;
  if (!entry || !id.test(entry.account) || !id.test(entry.chat)) return next;
  // Make room for the newest messages even if the full loaded page is large.
  const messages = [];
  let bytes = encoder.encode(JSON.stringify({...entry, messages})).byteLength;
  for (const message of entry.messages.slice(-50).reverse()) {
    const size = encoder.encode(JSON.stringify(message)).byteLength + 1;
    if (bytes + size > maxConversationBytes) break;
    bytes += size;
    messages.unshift(message);
  }
  const entries = recent(current.entries);
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

function invalidate(current, {engine, account, chat, through}) {
  if (engine !== current.engine || cursor(engine, through) < 0n) return;
  const minimum = cursor(engine, through) > cursor(engine, current.minimum) ? through : current.minimum;
  const entries = current.entries.filter(entry => entry.account !== account || (chat && entry.chat !== chat) ||
    cursor(engine, entry.cursor) >= cursor(engine, through));
  return {...current, minimum, entries};
}

self.addEventListener('message', event => {
  if (event.source?.type !== 'window') return;
  const path = new URL(event.source.url).pathname;
  const data = event.data;
  const port = event.ports[0];
  if ((path === '/' || path === shell) && data?.type === 'oie:offline-history-read' && port) {
    event.waitUntil(state().then(value => {
      port.postMessage({...value, entries: recent(value.entries)});
    }).catch(() => port.postMessage(null)).finally(() => port.close()));
    return;
  }
  if (path !== '/') return;
  const updates = {
    'oie:offline-history-save': current => save(current, data),
    'oie:offline-history-invalidate': current => invalidate(current, data),
    'oie:offline-history-clear': current => {
      if (data.engine && data.engine !== current.engine) return;
      const minimum = cursor(current.engine, data.through) > cursor(current.engine, current.minimum)
        ? data.through : current.minimum;
      return {...current, minimum, entries: []};
    },
  };
  if (Object.hasOwn(updates, data?.type)) event.waitUntil(state(updates[data.type]).catch(() => {}));
});

function conversationURL(url) {
  return url.pathname === '/' && ['chats', 'archived'].includes(url.searchParams.get('section') || 'chats') &&
    !url.searchParams.has('open') && !url.searchParams.has('searchQuery');
}

async function offlineResponse(request, url, fallback) {
  if (!conversationURL(url)) return fallback ?? Response.error();
  const data = await state().catch(() => null);
  if (fallback && !recent(data?.entries ?? []).length) return fallback;
  if (request.mode !== 'navigate') {
    return new Response('', {status: 503, headers: {'X-Oie-Offline-History': 'fallback'}});
  }
  const lang = url.searchParams.get('lang') || data?.lang;
  const page = await caches.match(shell + '?lang=' + (lang === 'pt-BR' ? 'pt-BR' : 'en'), {cacheName: shellCache});
  if (!page) return fallback ?? Response.error();
  return new Response(page.body, {status: fallback?.status ?? 200, headers: page.headers});
}

async function workspace(request, url) {
  let response;
  try {
    response = await fetch(request);
  } catch (error) {
    if (request.signal.aborted) throw error;
    return offlineResponse(request, url);
  }
  const engine = response.headers.get('X-Oie-Offline-History');
  if (id.test(engine)) {
    const chat = url.searchParams.get('chat');
    const account = url.searchParams.get('account');
    const missingChat = response.status === 404 && id.test(chat) && !url.searchParams.has('notificationEngine');
    if (missingChat || confirmedEngine !== engine) {
      await state(current => {
        if (current.engine !== engine || current.blocked) return {engine, entries: []};
        if (missingChat) return {...current, entries: current.entries.filter(entry =>
          entry.chat !== chat || account && entry.account !== account)};
      }).then(() => { confirmedEngine = engine; }).catch(() => {});
    }
  } else if (engine === 'unavailable' && response.status === 503) {
    return offlineResponse(request, url, response);
  } else if ([401, 403, 503].includes(response.status) || request.mode === 'navigate' &&
             (response.status === 200 || response.type === 'opaqueredirect' || response.redirected)) {
    // The origin may have been reconfigured to require authentication.
    confirmedEngine = undefined;
    await state(() => ({engine: '', blocked: true, entries: []})).catch(() => {});
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
      if (response.status === 204) {
        await state(current => ({...current, minimum: current.engine + ':9999999999999999999', entries: []})).catch(() => {});
      }
      return response;
    })());
  } else if (request.method === 'GET' && url.pathname === '/' &&
             (request.mode === 'navigate' || request.headers.get('HX-Request') === 'true')) {
    event.respondWith(workspace(request, url));
  } else if (request.method === 'GET' && [shell + '.js', shell + '.css'].includes(url.pathname)) {
    event.respondWith(fetch(request).catch(async () => (await caches.match(request, {cacheName: shellCache})) ?? Response.error()));
  }
});
