(() => {
  'use strict';

  const ios = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
  const prefix = 'oie.notifications.';
  const Audio = window.AudioContext || window.webkitAudioContext;
  const OfflineAudio = window.OfflineAudioContext || window.webkitOfflineAudioContext;
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
  const app = () => document.getElementById('oie-app');
  const settings = () => document.querySelector('[data-notification-settings]');
  const desktop = () => window.OieDesktop?.notifications;
  const bannerKey = prefix + 'banners';
  const pushPath = '/ui/notification-push';
  const pushStateKey = prefix + 'background';
  const soundKey = prefix + 'sound';
  const roomSoundKey = prefix + 'room-sound';
  const soundScopeKey = scope => prefix + 'sound-' + scope;
  const defaultSound = {name: 'Oiê'};
  const toneSampleRate = 24000;
  const maxToneFrames = toneSampleRate * 2;
  const maxToneFileSize = 5000000;
  const roomSoundValues = ['attention', 'all', 'off'];
  const pendingContexts = new Map();
  const lastSoundKey = prefix + 'last-sound-at';
  const soundInterval = 2000;
  const muteKey = (engine, chat) => prefix + 'mute.' + engine + '.' + chat;
  const focusKey = prefix + 'focus';
  const tab = Math.random().toString(36).slice(2);
  let context, iosPlayer, database, worker, pending = false, notice = '';
  let customSound, toneReady, toneChanges, editingTone = false;
  let pushStatus, pushManager, pushCheck, pushPending = false;
  let pushWrites = Promise.resolve();

  function read(key) {
    try { return localStorage.getItem(key); } catch { return null; }
  }

  function soundEnabled() {
    return read(soundKey) !== '0';
  }

  function currentSound() {
    return customSound ?? defaultSound;
  }

  function soundScopeEnabled(scope) {
    return (read(soundScopeKey(scope)) ?? (scope === 'direct' ? '1' : '0')) === '1';
  }

  function roomSound() {
    const value = read(roomSoundKey);
    return roomSoundValues.includes(value) ? value : 'attention';
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

  function notificationDatabase() {
    if (!database) {
      database = new Promise(resolve => {
        try {
          const request = indexedDB.open('oie-notifications', 2);
          request.onupgradeneeded = event => {
            if (event.oldVersion < 1) {
              request.result.createObjectStore('delivered').createIndex('deliveredAt', 'deliveredAt');
            }
            request.result.createObjectStore('tone');
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
    return database;
  }

  async function claim(engine, message, receivedAt = null) {
    const db = await notificationDatabase();
    if (!db) return false;
    return new Promise(resolve => {
      try {
        const tx = db.transaction('delivered', 'readwrite');
        const store = tx.objectStore('delivered');
        let claimedAt = 0;
        tx.oncomplete = () => resolve(claimedAt);
        tx.onabort = () => resolve(false);
        if (receivedAt === null) {
          const now = Date.now();
          store.add({deliveredAt: now}, [engine, message]).onsuccess = () => { claimedAt = now; };
          return;
        }
        const last = store.get(lastSoundKey);
        last.onsuccess = () => {
          const now = Date.now();
          // A single transaction protects the cooldown and message identity
          // across tabs. A candidate inside the window is dropped in place.
          if (now - receivedAt >= soundInterval ||
              receivedAt < (last.result?.deliveredAt ?? 0) + soundInterval) return;
          store.add({deliveredAt: now}, [engine, message, 'sound']).onsuccess = () => {
            store.put({deliveredAt: now}, lastSoundKey);
            claimedAt = now;
          };
        };
      } catch { resolve(false); }
    });
  }

  // With no argument read the tone. Null deletes it and restores Oiê.
  async function toneStorage(value) {
    const db = await notificationDatabase();
    if (!db) throw Error('Notification storage unavailable');
    return new Promise((resolve, reject) => {
      const tx = db.transaction('tone', value === undefined ? 'readonly' : 'readwrite');
      const store = tx.objectStore('tone');
      const request = value === undefined ? store.get('custom') :
        value === null ? store.delete('custom') : store.put(value, 'custom');
      tx.oncomplete = () => resolve(request.result);
      tx.onabort = () => reject(tx.error);
    });
  }

  function refreshTone() {
    const request = toneStorage().catch(() => null).then(value => {
      if (toneReady !== request) return;
      customSound = value;
      renderSettings();
      if (soundEnabled() && context?.state === 'running') void unlockSound();
    });
    toneReady = request;
    return request;
  }

  async function decodeTone(file) {
    const renderer = new OfflineAudio(1, maxToneFrames, toneSampleRate);
    const decoded = await renderer.decodeAudioData(await file.arrayBuffer());
    const frames = Math.min(decoded.length, maxToneFrames);
    const source = renderer.createBufferSource();
    source.buffer = decoded;
    source.connect(renderer.destination);
    source.start();
    const samples = (await renderer.startRendering()).getChannelData(0);
    const wav = new ArrayBuffer(44 + frames * 2);
    const view = new DataView(wav);
    new Uint8Array(wav).set([82, 73, 70, 70], 0); // RIFF
    view.setUint32(4, wav.byteLength - 8, true);
    new Uint8Array(wav).set([87, 65, 86, 69, 102, 109, 116, 32], 8); // WAVEfmt
    view.setUint32(16, 16, true);
    view.setUint16(20, 1, true); // PCM
    view.setUint16(22, 1, true); // mono
    view.setUint32(24, toneSampleRate, true);
    view.setUint32(28, toneSampleRate * 2, true);
    view.setUint16(32, 2, true);
    view.setUint16(34, 16, true);
    new Uint8Array(wav).set([100, 97, 116, 97], 36); // data
    view.setUint32(40, frames * 2, true);
    const trimmed = decoded.length > frames;
    for (let i = 0; i < frames; i++) {
      const fade = trimmed ? Math.min(1, (frames - 1 - i) / 120) : 1;
      const sample = Math.max(-1, Math.min(1, samples[i] * fade));
      view.setInt16(44 + i * 2, sample * (sample < 0 ? 32768 : 32767), true);
    }
    return {name: file.name, wav, trimmed};
  }

  function prepareIOSPlayer() {
    const media = document.createElement('audio');
    // A sourceless load during a gesture authorizes later playback in WebKit.
    media.preload = 'none';
    media.load();
    let selected, ambient = false;
    const session = navigator.audioSession;
    function restoreSession() {
      if (ambient && session.type === 'ambient') session.type = 'auto';
      ambient = false;
    }
    media.addEventListener('ended', restoreSession);
    media.addEventListener('pause', () => { if (media.paused) restoreSession(); });
    media.addEventListener('error', restoreSession);
    return async sound => {
      if (selected !== sound) {
        if (selected?.wav) URL.revokeObjectURL(media.src);
        media.src = sound.wav ? URL.createObjectURL(new Blob([sound.wav], {type: 'audio/wav'})) : '/ui/notification.wav';
        selected = sound;
      } else {
        media.currentTime = 0;
      }
      // A notification can arrive while music is playing in another app.
      if (session?.type === 'auto' && session.state !== 'active') {
        session.type = 'ambient';
        ambient = true;
      }
      try {
        await media.play();
      } catch (error) {
        if (media.paused) restoreSession();
        throw error;
      }
    };
  }

  async function unlockSound() {
    if (ios && !desktop()?.playSound) iosPlayer ??= prepareIOSPlayer();
    if (iosPlayer || desktop()?.playSound) {
      await toneReady;
      return currentSound();
    }
    try {
      if (!Audio) return;
      context ??= new Audio();
      const ready = context.state === 'running' ? Promise.resolve() : context.resume();
      await Promise.all([ready, toneReady]);
      const sound = currentSound();
      if (!sound.load) {
        const bytes = sound.wav ? Promise.resolve(sound.wav.slice(0)) :
          window.OieRuntime.request('/ui/notification.wav').then(response => {
            if (!response.ok) throw Error('Notification sound unavailable');
            return response.arrayBuffer();
          });
        sound.load = bytes.then(bytes => context.decodeAudioData(bytes))
          .then(buffer => { sound.buffer = buffer; })
          .catch(error => { sound.load = undefined; throw error; });
      }
      await sound.load;
      if (context.state === 'running') return sound;
    } catch {}
  }

  function playSound(sound) {
    const nativeSound = desktop()?.playSound;
    if (nativeSound) {
      if (sound.wav && !sound.nativeWAV) {
        const bytes = new Uint8Array(sound.wav);
        let data = '';
        for (let offset = 0; offset < bytes.length; offset += 8192) {
          data += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
        }
        sound.nativeWAV = btoa(data);
      }
      return nativeSound(sound.nativeWAV ?? null);
    }
    if (iosPlayer) return iosPlayer(sound);
    if (context?.state !== 'running' || !sound.buffer) return;
    const source = context.createBufferSource();
    source.buffer = sound.buffer;
    source.connect(context.destination);
    source.onended = () => source.disconnect();
    source.start();
  }

  function open(engine, account, conversation, message) {
    if (app()?.dataset.engineId !== engine || window.OieEvents.replaced ||
        ![account, conversation, message].every(uuid)) return;
    const path = '/?' + new URLSearchParams({account, chat: conversation, message, notificationEngine: engine});
    window.focus();
    if (window.OieNavigation) window.OieNavigation.go(path);
    else location.assign(path);
  }

  function notificationWorker() {
    if (!worker) {
      const script = window.OieHostedSession ? '/ui/notification-worker.js?hosted=1&foreground=1' : '/ui/notification-worker.js';
      const options = settings()?.querySelector('[data-notification-background]')
        ? {scope: '/'} : undefined;
      worker = navigator.serviceWorker.register(script, options).then(registration => {
        if (registration.active && (!window.OieHostedSession || registration.active.scriptURL === new URL(script, location.origin).href)) {
          return registration;
        }
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

  function canPush() {
    return Boolean(settings()?.querySelector('[data-notification-background]') && window.isSecureContext &&
      navigator.serviceWorker && (window.PushManager || window.pushManager) &&
      (!ios || navigator.standalone || matchMedia('(display-mode: standalone)').matches));
  }

  function pushEnabled(engine = app()?.dataset.engineId) {
    return Boolean(uuid(engine) && read(pushStateKey) === engine);
  }

  function pushPaused(engine = app()?.dataset.engineId) {
    return Boolean(uuid(engine) && read(pushStateKey) === engine + ':expired');
  }

  async function pushRequest(method, body, suffix = '', expected) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5000);
    try {
      const headers = {'Content-Type': 'application/json', 'X-CSRF-Token': settings()?.dataset.csrf ?? ''};
      if (expected) headers['If-Match'] = '"' + expected + '"';
      const response = await window.OieRuntime.request(pushPath + suffix, {
        method, cache: 'no-store', signal: controller.signal,
        keepalive: body?.phase === 'suppress',
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      if (!response.ok) throw Object.assign(Error('Background notifications unavailable'), {status: response.status});
      return response;
    } finally {
      clearTimeout(timeout);
    }
  }

  function pushLanguage() {
    return document.documentElement.lang === 'pt-BR' ? 'pt-BR' : 'en';
  }

  function pushSettings(engine) {
    const prefix = muteKey(engine, '');
    const mutedConversations = [];
    for (let index = 0; index < localStorage.length; index++) {
      const key = localStorage.key(index);
      const chat = key?.slice(prefix.length);
      if (key?.startsWith(prefix) && uuid(chat) && read(key) === '1') mutedConversations.push(chat);
    }
    return {
      engineId: engine, lang: pushLanguage(),
      mutedConversations, sound: soundEnabled(), directSound: soundScopeEnabled('direct'),
      roomSound: roomSound(), archivedSound: soundScopeEnabled('archived'),
    };
  }

  function savePush(subscription, engine, expected) {
    const {endpoint, keys} = subscription.toJSON();
    return pushRequest('PUT', {endpoint, keys, ...pushSettings(engine)}, '', expected);
  }

  async function pushFingerprint(subscription) {
    const {endpoint, keys} = subscription.toJSON();
    const value = new TextEncoder().encode([endpoint, keys.auth, keys.p256dh].join('\n'));
    const fingerprint = await crypto.subtle.digest('SHA-256', value);
    return Array.from(new Uint8Array(fingerprint), byte => byte.toString(16).padStart(2, '0')).join('');
  }

  async function boundedPush(action) {
    let timer;
    try {
      return await Promise.race([action, new Promise((_, reject) => {
        timer = setTimeout(() => reject(Error('Background notifications unavailable')), 5000);
      })]);
    } finally {
      clearTimeout(timer);
    }
  }

  function unsubscribePush(subscription) {
    return boundedPush(subscription?.unsubscribe()).catch(() => {});
  }

  async function matchesPush(subscription, status) {
    return status.subscriptionTag?.startsWith(await pushFingerprint(subscription) + '.');
  }

  function serializePush(action) {
    const result = pushWrites.then(() => navigator.locks
      ? navigator.locks.request(pushPath, action) : action());
    pushWrites = result.catch(() => {});
    return result;
  }

  async function readPushStatus() {
    const response = await pushRequest('GET');
    return response.json();
  }

  function preparePush(refresh = false) {
    const engine = app()?.dataset.engineId;
    if (!canPush() || !uuid(engine) || pushPending) return Promise.resolve();
    if (pushCheck) return pushCheck;
    if (!refresh && pushStatus?.engineId === engine) return Promise.resolve();
    pushCheck = serializePush(async () => {
      const [status, registration] = await Promise.all([readPushStatus(), boundedPush(notificationWorker())]);
      if (status.engineId !== app()?.dataset.engineId) return;
      pushManager = window.pushManager || registration.pushManager;
      const subscription = await boundedPush(pushManager.getSubscription());
      let active = status.enabled && !status.expired;
      if (status.enabled && ((!subscription && !status.expired) ||
          window.Notification?.permission === 'denied' || read(bannerKey) !== '1')) {
        await pushRequest('DELETE', undefined, '', status.subscriptionTag);
        await unsubscribePush(subscription);
        active = false;
      } else if (status.enabled && subscription) {
        const matches = await matchesPush(subscription, status);
        if (status.expired && matches) {
          await unsubscribePush(subscription);
        } else if (!matches || status.lang !== pushLanguage()) {
          await savePush(subscription, status.engineId, status.subscriptionTag);
          active = true;
        }
      }
      const optedIn = status.enabled && read(bannerKey) === '1' && window.Notification?.permission !== 'denied';
      write(pushStateKey, active ? status.engineId : optedIn && status.expired ? status.engineId + ':expired' : null);
      pushStatus = status;
      if (notice === 'backgroundCheckFailed') notice = '';
    }).catch(() => {
      if (!pushStatus && !notice && !pending && !pushPending) notice = 'backgroundCheckFailed';
    }).finally(() => {
      pushCheck = undefined;
      renderSettings();
    });
    return pushCheck;
  }

  async function setPush(desired, clearBanner = false) {
    if (pushPending || (pending && desired)) {
      renderSettings();
      return false;
    }
    pushPending = true;
    notice = '';
    renderSettings();
    try {
      if (desired) {
        const engine = app()?.dataset.engineId;
        if (!pushManager || pushStatus?.engineId !== engine) throw Error('Background notifications unavailable');
        const key = pushStatus.publicKey.replaceAll('-', '+').replaceAll('_', '/');
        // Call subscribe directly from the toggle gesture on iOS.
        const subscription = await pushManager.subscribe({
          userVisibleOnly: true, applicationServerKey: Uint8Array.from(atob(key), value => value.charCodeAt(0)),
        });
        await serializePush(async () => {
          const current = await boundedPush(pushManager.getSubscription());
          if (!current || current.endpoint !== subscription.endpoint || app()?.dataset.engineId !== engine) {
            throw Error('Background notifications changed');
          }
          const status = await readPushStatus();
          const previous = read(bannerKey);
          const subscribed = read(pushStateKey);
          try {
            if (!write(bannerKey, '1') || !write(pushStateKey, engine)) throw Error('Notification storage unavailable');
            await savePush(current, engine, status.subscriptionTag);
          } catch (error) {
            write(bannerKey, previous);
            write(pushStateKey, subscribed);
            if (!status.enabled || status.expired) await unsubscribePush(current);
            throw error;
          }
        });
      } else {
        await serializePush(async () => {
          if (clearBanner && !pushEnabled() && !pushPaused()) {
            const status = await readPushStatus().catch(() => pushStatus);
            if (status?.enabled === false) {
              if (!write(bannerKey, null)) throw Error('Notification storage unavailable');
              return;
            }
          }
          const subscription = await boundedPush(pushManager?.getSubscription()).catch(() => null);
          await pushRequest('DELETE');
          write(pushStateKey, null);
          if (clearBanner && !write(bannerKey, null)) throw Error('Notification storage unavailable');
          await unsubscribePush(subscription);
        });
      }
      return true;
    } catch {
      notice = window.Notification?.permission === 'denied' ? 'denied' : 'backgroundFailed';
      return false;
    } finally {
      pushPending = false;
      renderSettings();
    }
  }

  async function writeNotificationPreference(key, value) {
    if (!canPush()) return write(key, value);
    return serializePush(async () => {
      const previous = read(key);
      if (!write(key, value)) return false;
      const engine = app()?.dataset.engineId;
      if (!pushEnabled(engine)) return true;
      try {
        const save = async () => {
          const status = await readPushStatus();
          if (!status.enabled || status.engineId !== engine) return;
          const subscription = await boundedPush(pushManager?.getSubscription());
          if (status.expired && (!subscription || await matchesPush(subscription, status))) return;
          if (!subscription) throw Error('Background notifications unavailable');
          await savePush(subscription, engine, status.subscriptionTag);
        };
        try { await save(); }
        catch (error) {
          if (error.status !== 409) throw error;
          await save();
        }
        return true;
      } catch {
        if (read(key) === value) write(key, previous);
        return false;
      }
    });
  }

  async function showBanner(engine, message, eligible) {
    const title = settings()?.dataset.messageTitle;
    if (!title) return false;
    const body = (message.sender?.displayName || '').slice(0, 120);
    const canDeliver = async () => {
      if (!eligible()) return false;
      const current = await notificationContext(engine, message, true);
      return current && !current.doNotDisturb && eligible();
    };
    if (desktop()) {
      if (!await canDeliver()) return false;
      await desktop().send(engine, message.accountId, message.conversationId, message.id, title, body);
      return true;
    }
    const options = {body, tag: engine + ':' + message.id, silent: true, lang: document.documentElement.lang};
    if (navigator.serviceWorker) {
      try {
        const registration = await notificationWorker();
        if (!await canDeliver()) return false;
        await registration.showNotification(title, {...options, data: {
          engine, account: message.accountId, conversation: message.conversationId, message: message.id
        }});
        return true;
      } catch {
        // A page notification can still work when the worker is unavailable.
      }
    }
    if (!await canDeliver()) return false;
    const banner = new Notification(title, options);
    banner.onclick = () => {
      banner.close();
      open(engine, message.accountId, message.conversationId, message.id);
    };
    return true;
  }

  function canBanner() {
    return Boolean(desktop() || typeof window.Notification === 'function');
  }

  function wantsBanner() {
    return read(bannerKey) === '1' && Boolean(desktop() || window.Notification?.permission === 'granted');
  }

  async function showHostedForeground(engine, message, eligible) {
    const registration = await boundedPush(notificationWorker());
    const title = settings()?.dataset.messageTitle;
    if (!title || !registration.active || !eligible()) return false;
    const channel = new MessageChannel();
    try {
      return await boundedPush(new Promise(resolve => {
        channel.port1.onmessage = event => resolve(event.data === true);
        registration.active.postMessage({
          type: 'oie:foreground-notification', engine, account: message.accountId,
          conversation: message.conversationId, message: message.id, title,
          body: (message.sender?.displayName || '').slice(0, 120), lang: document.documentElement.lang,
          csrf: settings()?.dataset.csrf ?? '',
        }, [channel.port2]);
      }));
    } finally {
      channel.port1.close();
    }
  }

  function onMessage({detail: {engine, event}}) {
    let message;
    try { message = JSON.parse(event.data).message; } catch { return; }
    if (!message || message.sendState !== 'received' || message.isOwn || message.historical ||
        ![message.id, message.accountId, message.conversationId].every(uuid)) return;

    const conversation = message.conversationId;
    const canNotify = () => app()?.dataset.engineId === engine && !window.OieEvents.replaced &&
      read(muteKey(engine, conversation)) !== '1';
    const canShowBanner = () => canNotify() && wantsBanner() && !viewing(engine, conversation);
    const canPlaySound = () => canNotify() && (!pushEnabled(engine) || !document.hidden) &&
      (soundScopeEnabled('focused') || !viewing(engine, conversation));

    const handleBanner = () => {
      if (!canShowBanner()) return;
      void notificationContext(engine, message).then(async context => {
        if (!context || context.doNotDisturb || !canShowBanner()) return;
        const claimed = await claim(engine, message.id);
        if (claimed && canShowBanner()) return showBanner(engine, message, canShowBanner);
      }).catch(() => {});
    };
    if (pushEnabled(engine)) {
      const visible = () => !document.hidden && canNotify() && wantsBanner();
      if (!visible()) {
        if (canPlaySound()) void playMessageSound(engine, message, canPlaySound);
        return;
      }
      void notificationContext(engine, message).then(async context => {
        if (!context || context.doNotDisturb || !visible()) return;
        const request = {
          engineId: engine, messageId: message.id, accountId: message.accountId, conversationId: message.conversationId,
        };
        const started = performance.now();
        const reserved = () => visible() && performance.now() - started < 1750;
        try {
          await pushRequest('POST', {...request, phase: 'reserve'}, '/foreground');
        } catch (error) {
          if (error.status === 404 && visible()) {
            handleBanner();
            if (canPlaySound()) void playMessageSound(engine, message, canPlaySound);
          }
          return;
        }
        if (!reserved()) return;
        if (viewing(engine, conversation)) {
          await pushRequest('POST', {...request, phase: 'suppress'}, '/foreground');
        } else {
          if (!await claim(engine, message.id) || !await showHostedForeground(engine, message, () => reserved() && canShowBanner())) return;
        }
        if (canPlaySound()) await playMessageSound(engine, message, canPlaySound);
      }).catch(() => {});
      return;
    }
    handleBanner();
    if (canPlaySound()) void playMessageSound(engine, message, canPlaySound);
  }

  function notificationContext(engine, message, fresh = false) {
    const params = new URLSearchParams({engineId: engine, accountId: message.accountId, conversationId: message.conversationId});
    if (message.reply?.available === true && uuid(message.reply.messageId)) {
      params.set('replyTo', message.reply.messageId);
    }
    const key = params.toString();
    const load = () => window.OieRuntime.request('/ui/notification-context?' + key, {cache: 'no-store'})
      .then(response => response.ok ? response.json() : null);
    if (fresh) return load();
    if (!pendingContexts.has(key)) {
      pendingContexts.set(key, load()
        .finally(() => pendingContexts.delete(key)));
    }
    return pendingContexts.get(key);
  }

  async function playMessageSound(engine, message, eligible) {
    const receivedAt = Date.now();
    const loadedTone = toneReady;
    await loadedTone;
    const sound = currentSound();
    const withinWindow = () => receivedAt < Number(read(lastSoundKey)) + soundInterval;
    const ready = () => soundEnabled() && loadedTone === toneReady && sound === currentSound() &&
      (desktop()?.playSound || iosPlayer || sound.buffer && context?.state === 'running');
    if (!ready() || withinWindow() || (!soundScopeEnabled('direct') && roomSound() === 'off')) return;
    try {
      const conversation = await notificationContext(engine, message);
      if (!conversation || conversation.doNotDisturb || !ready() || !eligible() || withinWindow()) return;
      const permitsSound = current => {
        if (current.kind === 'direct') return soundScopeEnabled('direct');
        if (current.kind !== 'room' || (current.archived && !soundScopeEnabled('archived'))) return false;
        const roomMode = roomSound();
        return roomMode === 'all' || (roomMode === 'attention' &&
          (message.mentionsYou === true || current.replyToYou === true));
      };
      if (!permitsSound(conversation)) return;
      const claimedAt = await claim(engine, message.id, receivedAt);
      if (!claimedAt || !ready() || !eligible()) return;
      const current = await notificationContext(engine, message, true);
      if (current && !current.doNotDisturb && ready() && eligible() && permitsSound(current)) {
        write(lastSoundKey, String(claimedAt));
        await playSound(sound);
      }
    } catch {}
  }

  function syncSelect(control, value) {
    if (control.value === value) return;
    control.value = value;
    control.dispatchEvent(new Event('input', {bubbles: true}));
  }

  function renderSettings() {
    const root = settings();
    if (!root) return;
    const banners = root.querySelector('[data-notification-banners]');
    const sound = root.querySelector('[data-notification-sound]');
    const canSound = Boolean(ios || Audio || desktop()?.playSound);
    banners.checked = wantsBanner();
    banners.disabled = pending || pushPending || !canBanner();
    const background = root.querySelector('[data-notification-background]');
    if (background) {
      background.closest('[data-notification-background-option]').hidden = !canPush();
      background.checked = pushEnabled() || pushPaused();
      background.disabled = !canPush() || (!background.checked && pushStatus?.engineId !== app()?.dataset.engineId);
      background.setAttribute('aria-disabled', String(pending || pushPending || background.disabled));
    }
    sound.checked = soundEnabled();
    sound.disabled = !canSound;
    for (const scope of root.querySelectorAll('[data-notification-sound-scope]')) {
      scope.checked = soundScopeEnabled(scope.dataset.notificationSoundScope);
      scope.disabled = !canSound;
    }
    const toneName = root.querySelector('[data-notification-tone-name]');
    toneName.textContent = currentSound().name;
    toneName.title = currentSound().name;
    root.querySelector('[data-notification-tone-choose]').disabled = editingTone || !OfflineAudio || !canSound;
    const reset = root.querySelector('[data-notification-tone-reset]');
    reset.hidden = !customSound;
    reset.disabled = editingTone;
    syncSelect(root.querySelector('[data-notification-room-sound]'), roomSound());
    root.querySelector('[data-notification-preview]').disabled = !canSound;
    const status = notice === 'backgroundCheckFailed' ? 'backgroundFailed' : notice;
    root.querySelector('[role="status"]').textContent =
      root.dataset[status] || (pushPaused() ? root.dataset.backgroundPaused : !canBanner() ? root.dataset.unavailable :
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
      if (!await writeNotificationPreference(key, desired ? '1' : null)) {
        window.dispatchEvent(new CustomEvent('notify', {
          detail: {kind: 'toast', tone: 'danger', message: control.dataset.unsaved}
        }));
      }
      renderMute();
      return;
    }
    if (control.matches('[data-notification-sound]')) {
      notice = await writeNotificationPreference(soundKey, desired ? '1' : '0') ? '' : 'soundUnavailable';
      if (desired) void unlockSound();
      renderSettings();
      return;
    }
    if (control.matches('[data-notification-tone-file]')) {
      const file = control.files[0];
      control.value = '';
      if (file) void changeTone(file);
      return;
    }
    if (control.matches('[data-notification-room-sound]')) {
      if (!roomSoundValues.includes(control.value)) return;
      notice = await writeNotificationPreference(roomSoundKey, control.value) ? '' : 'soundUnavailable';
      renderSettings();
      return;
    }
    if (control.matches('[data-notification-sound-scope]')) {
      notice = await writeNotificationPreference(soundScopeKey(control.dataset.notificationSoundScope), desired ? '1' : '0') ? '' : 'soundUnavailable';
      renderSettings();
      return;
    }
    if (control.matches('[data-notification-background]')) {
      await setPush(desired);
      return;
    }
    if (!control.matches('[data-notification-banners]') || pending || pushPending) return;

    pending = true;
    notice = '';
    renderSettings();
    try {
      if (!desired) {
        if (canPush()) await setPush(false, true);
        else if (!write(bannerKey, null)) notice = 'unavailable';
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

  async function changeTone(file) {
    if (editingTone) return;
    editingTone = true;
    renderSettings();
    try {
      if (file === undefined) {
        const title = settings()?.querySelector('[data-notification-tone-choose]').textContent.trim();
        const selected = await desktop().chooseTone(title);
        if (!selected) return;
        file = selected;
      }
      if (file?.size > maxToneFileSize) {
        notice = 'largeTone';
        return;
      }
      let value = null;
      if (file) {
        try {
          if (!(file instanceof File)) {
            const data = atob(file.bytes ?? '');
            const bytes = new Uint8Array(data.length);
            for (let i = 0; i < bytes.length; i++) bytes[i] = data.charCodeAt(i);
            file = new File([bytes], file.name);
          }
          value = await decodeTone(file);
        }
        catch { notice = 'invalidTone'; return; }
      }
      await toneStorage(value);
      await refreshTone();
      toneChanges?.postMessage('changed');
      notice = value?.trimmed ? 'trimmedTone' : '';
    } catch { notice = 'unsaved'; }
    finally {
      editingTone = false;
      renderSettings();
    }
  }

  function sync() {
    void preparePush();
    publishFocus();
    renderSettings();
    renderMute();
  }

  async function previewSound() {
    try {
      const sound = await unlockSound();
      if (!sound) throw Error('Notification sound unavailable');
      if (sound !== currentSound()) return;
      await playSound(sound);
      notice = '';
    } catch { notice = 'soundUnavailable'; }
    renderSettings();
  }

  document.addEventListener('change', change);
  document.addEventListener('click', event => {
    if (event.target.closest('[data-notification-preview]')) {
      void previewSound();
      return;
    }
    const reset = event.target.closest('[data-notification-tone-reset]');
    if (event.target.closest('[data-notification-tone-choose]')) {
      if (desktop()?.chooseTone) void changeTone();
      else settings()?.querySelector('[data-notification-tone-file]').click();
    } else if (reset) {
      void changeTone(null).then(() => {
        if (reset.isConnected && reset.hidden && [reset, document.body].includes(document.activeElement)) {
          reset.closest('[data-notification-settings]')?.querySelector('[data-notification-tone-choose]').focus();
        }
      });
    }
    if (event.isTrusted && soundEnabled()) void unlockSound();
  });
  document.addEventListener('keydown', event => {
    if (event.isTrusted && soundEnabled()) void unlockSound();
  });
  for (const name of ['oie:route-ready', 'oie:info-content', 'visibilitychange']) {
    document.addEventListener(name, sync);
  }
  window.addEventListener('oie:desktop-ready', sync);
  window.addEventListener('focus', () => { sync(); void preparePush(true); });
  window.addEventListener('online', () => void preparePush(true));
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) void preparePush(true);
  });
  window.addEventListener('blur', () => publishFocus(true));
  window.addEventListener('pagehide', () => publishFocus(true));
  window.addEventListener('pageshow', event => {
    sync();
    if (event.persisted) void preparePush(true);
  });
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
  try {
    toneChanges = new BroadcastChannel('oie-notification-tone');
    toneChanges.onmessage = refreshTone;
  } catch {}
  void refreshTone();
  sync();
})();
