(() => {
  'use strict';

  const prefix = 'oie.notifications.';
  const Audio = window.AudioContext || window.webkitAudioContext;
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
  const app = () => document.getElementById('oie-app');
  const settings = () => document.querySelector('[data-notification-settings]');
  const desktop = () => window.OieDesktop?.notifications;
  const bannerKey = prefix + 'banners';
  const soundKey = prefix + 'sound';
  const muteKey = (engine, chat) => prefix + 'mute.' + engine + '.' + chat;
  const focusKey = prefix + 'focus';
  const tab = Math.random().toString(36).slice(2);
  let context, soundBuffer, soundLoad, database, worker, pending = false, notice = '';

  function read(key) {
    try { return localStorage.getItem(key); } catch { return null; }
  }

  function write(key, value) {
    try {
      if (value === null) localStorage.removeItem(key);
      else localStorage.setItem(key, value);
      return true;
    } catch { return false; }
  }

  function focusedChat() {
    const root = app();
    if (!document.hidden && document.hasFocus() &&
        document.getElementById('message-timeline')?.getClientRects().length &&
        !document.querySelector('[data-message-search-root][data-search-open="true"],[data-conversation-search][data-search-open="true"]')) {
      return root?.dataset.conversationId;
    }
  }

  function recordedFocus() {
    try { return JSON.parse(read(focusKey)); } catch { return null; }
  }

  function publishFocus(release = false) {
    const engine = app()?.dataset.engineId;
    const conversation = release ? null : focusedChat();
    if (engine && conversation) {
      write(focusKey, JSON.stringify({tab, engine, conversation, expires: Date.now() + 15000}));
    } else if (recordedFocus()?.tab === tab) {
      write(focusKey, null);
    }
  }

  function viewing(engine, conversation) {
    if (focusedChat() === conversation) return true;
    const current = recordedFocus();
    return current?.engine === engine && current.conversation === conversation && current.expires > Date.now();
  }

  function claim(engine, message) {
    if (!database) {
      database = new Promise(resolve => {
        try {
          const request = indexedDB.open('oie-notifications', 1);
          request.onupgradeneeded = () => {
            request.result.createObjectStore('delivered').createIndex('deliveredAt', 'deliveredAt');
          };
          request.onerror = request.onblocked = () => resolve(null);
          request.onsuccess = () => {
            const db = request.result;
            db.onversionchange = () => { db.close(); database = null; };
            const cutoff = IDBKeyRange.upperBound(Date.now() - 86400000);
            const prune = db.transaction('delivered', 'readwrite').objectStore('delivered').index('deliveredAt').openKeyCursor(cutoff);
            prune.onsuccess = () => {
              const cursor = prune.result;
              if (cursor) {
                cursor.source.objectStore.delete(cursor.primaryKey);
                cursor.continue();
              }
            };
            resolve(db);
          };
        } catch { resolve(null); }
      });
    }
    return database.then(db => new Promise(resolve => {
      if (!db) return resolve(false);
      try {
        const tx = db.transaction('delivered', 'readwrite');
        const add = tx.objectStore('delivered').add({deliveredAt: Date.now()}, [engine, message]);
        tx.oncomplete = () => resolve(true);
        tx.onabort = () => resolve(false);
        add.onerror = () => resolve(false);
      } catch { resolve(false); }
    }));
  }

  async function unlockSound() {
    try {
      if (!Audio) return false;
      context ??= new Audio();
      const ready = context.state === 'running' ? Promise.resolve() : context.resume();
      if (!soundBuffer && !soundLoad) {
        soundLoad = fetch('/ui/notification.wav')
          .then(response => {
            if (!response.ok) throw Error('Notification sound unavailable');
            return response.arrayBuffer();
          })
          .then(bytes => context.decodeAudioData(bytes))
          .then(buffer => { soundBuffer = buffer; })
          .finally(() => { soundLoad = undefined; });
      }
      await Promise.all([ready, soundLoad]);
      return context.state === 'running' && Boolean(soundBuffer);
    } catch { return false; }
  }

  function playSound() {
    if (context?.state !== 'running' || !soundBuffer) return;
    const source = context.createBufferSource();
    source.buffer = soundBuffer;
    source.connect(context.destination);
    source.onended = () => source.disconnect();
    source.start();
  }

  function open(engine, account, conversation, message) {
    if (app()?.dataset.engineId !== engine || window.OieEvents.replaced ||
        ![account, conversation, message].every(uuid)) return;
    const path = '/?' + new URLSearchParams({account, chat: conversation, message});
    window.focus();
    if (window.OieNavigation) window.OieNavigation.go(path);
    else location.assign(path);
  }

  function notificationWorker() {
    if (!worker) {
      worker = navigator.serviceWorker.register('/ui/notification-worker.js').then(registration => {
        if (registration.active) return registration;
        const candidate = registration.installing || registration.waiting;
        if (!candidate) throw Error('No notification worker');
        return new Promise((resolve, reject) => {
          const check = () => {
            if (candidate.state === 'activated') resolve(registration);
            if (candidate.state === 'redundant') reject(Error('Notification worker unavailable'));
          };
          candidate.addEventListener('statechange', check);
          check();
        });
      }).catch(error => { worker = undefined; throw error; });
    }
    return worker;
  }

  async function showBanner(engine, message) {
    const title = settings()?.dataset.messageTitle;
    if (!title) return;
    const body = (message.sender?.displayName || '').slice(0, 120);
    if (desktop()) {
      await desktop().send(engine, message.accountId, message.conversationId, message.id, title, body);
      return;
    }
    const options = {body, tag: engine + ':' + message.id, silent: true, lang: document.documentElement.lang};
    try {
      const banner = new Notification(title, options);
      banner.onclick = () => {
        banner.close();
        open(engine, message.accountId, message.conversationId, message.id);
      };
    } catch {
      if (!navigator.serviceWorker) return;
      const registration = await notificationWorker();
      await registration.showNotification(title, {...options, data: {
        engine, account: message.accountId, conversation: message.conversationId, message: message.id
      }});
    }
  }

  function canBanner() {
    return Boolean(desktop() || typeof window.Notification === 'function');
  }

  function wantsBanner() {
    return read(bannerKey) === '1' && Boolean(desktop() || window.Notification?.permission === 'granted');
  }

  async function onMessage({detail: {engine, event}}) {
    let message;
    try { message = JSON.parse(event.data).message; } catch { return; }
    if (!message || message.sendState !== 'received' || message.isOwn || message.historical ||
        ![message.id, message.accountId, message.conversationId].every(uuid)) return;

    const conversation = message.conversationId;
    const eligible = () => app()?.dataset.engineId === engine && !window.OieEvents.replaced &&
      read(muteKey(engine, conversation)) !== '1' && !viewing(engine, conversation);

    const wantsSound = () => soundBuffer && context?.state === 'running' && read(soundKey) === '1';
    if (!eligible() || (!wantsBanner() && !wantsSound())) return;
    // Give a window with unlocked sound the first chance to claim the message.
    if (read(soundKey) === '1' && !wantsSound()) {
      await new Promise(resolve => setTimeout(resolve, 300));
    }
    if (!eligible() || !(await claim(engine, message.id)) || !eligible()) return;
    if (wantsSound()) playSound();
    if (wantsBanner()) await showBanner(engine, message).catch(() => {});
  }

  function renderSettings() {
    const root = settings();
    if (!root) return;
    const banners = root.querySelector('[data-notification-banners]');
    const sound = root.querySelector('[data-notification-sound]');
    banners.checked = wantsBanner();
    banners.disabled = pending || !canBanner();
    sound.checked = read(soundKey) === '1';
    sound.disabled = !Audio;
    root.querySelector('[data-notification-preview]').disabled = !Audio;
    root.querySelector('[role="status"]').textContent =
      root.dataset[notice] || (!canBanner() ? root.dataset.unavailable :
        window.Notification?.permission === 'denied' && !desktop() ? root.dataset.denied : '');
  }

  function renderMute() {
    const control = document.querySelector('[data-notification-mute]');
    if (!control) return;
    control.checked = read(muteKey(control.dataset.muteEngine, control.dataset.notificationMute)) === '1';
    control.disabled = false;
  }

  async function change(event) {
    const control = event.target;
    const desired = control.checked;
    if (control.matches('[data-notification-mute]')) {
      const key = muteKey(control.dataset.muteEngine, control.dataset.notificationMute);
      if (!write(key, desired ? '1' : null)) {
        window.dispatchEvent(new CustomEvent('notify', {
          detail: {kind: 'toast', tone: 'danger', message: control.dataset.unsaved}
        }));
      }
      renderMute();
      return;
    }
    if (control.matches('[data-notification-sound]')) {
      notice = write(soundKey, desired ? '1' : null) ? '' : 'soundUnavailable';
      if (desired) void unlockSound();
      renderSettings();
      return;
    }
    if (!control.matches('[data-notification-banners]') || pending) return;

    pending = true;
    notice = '';
    renderSettings();
    try {
      if (!desired) {
        if (!write(bannerKey, null)) notice = 'unavailable';
      } else {
        const granted = desktop()
          ? await desktop().request()
          : await Notification.requestPermission() === 'granted';
        if (!granted) notice = 'denied';
        else if (!write(bannerKey, '1')) notice = 'unavailable';
      }
    } catch { notice = 'unavailable'; }
    finally {
      pending = false;
      renderSettings();
    }
  }

  function sync() {
    publishFocus();
    renderSettings();
    renderMute();
  }

  document.addEventListener('change', change);
  document.addEventListener('click', event => {
    if (event.target.closest('[data-notification-preview]')) {
      void unlockSound().then(ready => {
        if (ready) playSound();
        else notice = 'soundUnavailable';
        renderSettings();
      });
    } else if (event.isTrusted && read(soundKey) === '1') {
      void unlockSound();
    }
  });
  document.addEventListener('keydown', event => {
    if (event.isTrusted && read(soundKey) === '1') void unlockSound();
  });
  for (const name of ['oie:route-ready', 'oie:info-content', 'visibilitychange']) {
    document.addEventListener(name, sync);
  }
  window.addEventListener('oie:desktop-ready', sync);
  window.addEventListener('focus', sync);
  window.addEventListener('blur', () => publishFocus(true));
  window.addEventListener('pagehide', () => publishFocus(true));
  window.addEventListener('pageshow', sync);
  window.addEventListener('storage', event => {
    if (event.key === null || (event.key !== focusKey && event.key.startsWith(prefix))) {
      renderSettings();
      renderMute();
    }
  });
  setInterval(publishFocus, 5000);
  navigator.serviceWorker?.addEventListener('message', event => {
    if (event.data?.type !== 'oie:notification-click') return;
    const {engine, account, conversation, message} = event.data;
    open(engine, account, conversation, message);
  });
  window.OieEvents.target.addEventListener('message.live', onMessage);
  window.OieNotifications = {open};
  sync();
})();
