window.OieRuntime.register('video-thumbnails', life => {
  'use strict';
  const preference = 'oie.device.auto-video-previews.v1';
  const suppliedTimeout = 45000;
  const frameTimeout = 12000;
  const selector = '[data-video-item][data-video-thumbnail-state]:not([data-video-thumbnail-state=""])';
  const uuid = value => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
  const hash = value => /^[0-9a-f]{64}$/.test(value || '');
  const engine = document.body.dataset.engineId;
  const csrf = document.querySelector('meta[name=oie-csrf]')?.content || '';
  const cards = () => [...document.querySelectorAll(selector)];
  const scope = card => {
    const {videoEngine, videoAccount, videoConversation, videoMessage, videoRevision, videoItem} = card.dataset;
    return [videoEngine, videoAccount, videoConversation, videoMessage, videoRevision, videoItem];
  };
  const key = card => JSON.stringify(scope(card));
  const matching = identity => [...observed].filter(card => card.isConnected && key(card) === identity);
  const pending = new Map();
  const visible = new Set();
  const observed = new Set();
  const finished = new Map();
  const urls = new Map();
  let running = 0;
  let cachedFrame;
  let automatic;
  try { automatic = localStorage.getItem(preference) !== 'false'; }
  catch { automatic = window.OieRuntime.memory.get(preference) !== false; }

  function frameCache() {
    cachedFrame ||= window.OieCache.decorate({
      namespace: 'video-frame-jpeg-v1',
      key: input => input.scope,
      load: async (input, {signal}) => {
        const {boundedVideoBlob, videoFrame} = await import('/ui/video-frames.js');
        const response = await window.OieRuntime.request(input.source + '&preview=1', {signal, cache:'no-store'});
        const type = response.headers.get('Content-Type');
        if (!['video/mp4', 'video/webm', 'video/ogg', 'video/quicktime'].includes(type)) {
          await response.body?.cancel();
          throw Error('video_sample_type');
        }
        return videoFrame(await boundedVideoBlob(response, 5 * 1024 * 1024, type, signal), {signal});
      },
      size: blob => blob.size,
      failureTTL: 0,
      storage: window.OieCache.indexedDBStore({name:'oie.video-previews.v1'}),
      validate: blob => blob instanceof Blob && blob.type === 'image/jpeg' && blob.size > 0 && blob.size <= 1024 * 1024,
    });
    return cachedFrame;
  }

  function paint(card, state, poster = '') {
    card.dataset.videoThumbnailState = state;
    if (poster && card.dataset.videoPoster !== poster) {
      const image = card.querySelector('[data-video-poster-image]');
      if (poster.startsWith('blob:')) {
        const old = urls.get(card);
        if (old && old !== poster) URL.revokeObjectURL(old);
        urls.set(card, poster);
      }
      card.dataset.videoPoster = poster;
      image.src = poster;
      image.hidden = false;
      card.querySelector('[data-video-player]').poster = poster;
    }
    const status = card.querySelector('[data-video-thumbnail-status]');
    const text = state === 'queued' ? card.dataset.videoThumbnailLoading :
      state === 'unavailable' ? card.dataset.videoThumbnailFailed : '';
    if (status && status.textContent !== text) {
      status.textContent = text;
      status.hidden = !text;
    }
    const menu = card.closest('[data-shared-item]')?.querySelector('[id^="shared-thumbnail-"]');
    for (const button of [card.querySelector('[data-video-thumbnail-load]'), menu]) {
      if (!button) continue;
      const label = state === 'unavailable' ? card.dataset.thumbnailRetryLabel : card.dataset.thumbnailLoadLabel;
      const copy = button.querySelector('[data-video-thumbnail-action-label],span.block') || button;
      if (copy.textContent !== label) copy.textContent = label;
      button.setAttribute('aria-label', label);
      button.title = state === 'unavailable' ? card.dataset.videoThumbnailFailed + ' ' + label : label;
      button.hidden = !!card.dataset.videoPoster;
      button.disabled = state === 'queued';
    }
  }

  function apply(card, result) {
    if (card.dataset.videoPoster) return;
    const poster = result.blob ? URL.createObjectURL(result.blob) : result.poster;
    paint(card, poster ? 'ready' : 'unavailable', poster);
  }

  async function supplied(card, signal) {
    const item = card.dataset.videoItem;
    const valid = p => p?.kind === 'video' && p.sharedContentId === item &&
      p.accountId === card.dataset.videoAccount && p.conversationId === card.dataset.videoConversation &&
      p.messageId === card.dataset.videoMessage;
    const body = new URLSearchParams({csrf, engineId:engine, itemId:item, returnTo:location.pathname + location.search});
    let response = await life.fetch('/ui/videos/thumbnail', {
      method:'POST', headers:{Accept:'application/json'}, body, signal,
    });
    if (!response.ok) return '';
    let p = (await response.json()).thumbnail;
    while (valid(p) && p.state === 'queued') {
      await new Promise(resolve => setTimeout(resolve, 500));
      signal.throwIfAborted();
      response = await life.fetch('/ui/api/v1/shared-content/' + item + '/thumbnail', {
        headers:{'X-Oie-Engine-ID':engine}, cache:'no-store', signal,
      });
      if (!response.ok) return '';
      p = await response.json();
    }
    return valid(p) && p.state === 'ready' && hash(p.imageHash) ? '/ui/media/' + p.imageHash : '';
  }

  function request(card, manual = false) {
    const identity = key(card);
    const ids = scope(card);
    if (!ids.every(uuid) || ids[0] !== engine || card.dataset.videoPoster || pending.has(identity) ||
      !manual && (!automatic || finished.has(identity))) return;
    const previous = card.dataset.videoThumbnailState === 'unavailable' ? 'unavailable' : 'not_requested';
    pending.set(identity, {card, manual, previous, controller: new AbortController()});
    for (const current of matching(identity)) paint(current, 'queued');
    drain();
  }

  function cancel(identity, job) {
    pending.delete(identity);
    job.controller.abort();
    for (const card of matching(identity)) if (!card.dataset.videoPoster) paint(card, job.previous);
  }

  function cancelOffscreen() {
    for (const [identity, job] of pending) {
      const current = matching(identity);
      if (!current.length || !job.manual && (!automatic || !current.some(card => visible.has(card)))) {
        cancel(identity, job);
      }
    }
  }

  function drain() {
    if (life.signal.aborted || running >= 2) return;
    for (const [identity, job] of pending) {
      if (job.started) continue;
      const current = matching(identity);
      if (!current.length || !job.manual && (!automatic || !current.some(card => visible.has(card)))) {
        cancel(identity, job);
        continue;
      }
      job.started = true;
      running++;
      void run(identity, job);
      if (running >= 2) break;
    }
  }

  async function run(identity, job) {
    const {card, manual, controller} = job;
    const {signal} = controller;
    const result = {};
    try {
      const suppliedController = new AbortController();
      const abortSupplied = () => suppliedController.abort();
      signal.addEventListener('abort', abortSupplied, {once:true});
      const suppliedTimer = setTimeout(abortSupplied, suppliedTimeout);
      try {
        result.poster = await supplied(card, suppliedController.signal);
      } catch {
        // The bounded video sample can still provide a frame.
      } finally {
        clearTimeout(suppliedTimer);
        signal.removeEventListener('abort', abortSupplied);
      }
      if (!result.poster && !signal.aborted) {
        const item = card.dataset.videoItem;
        const source = '/ui/shared-content/' + item + '/content?engineId=' + engine;
        const timer = setTimeout(() => controller.abort(), frameTimeout);
        try {
          result.blob = await frameCache()({scope:scope(card), source}, {signal, refresh:manual});
        } finally {
          clearTimeout(timer);
        }
      }
    } catch {
      // The play control remains available when a still frame cannot be made.
    } finally {
      running--;
      if (pending.get(identity) === job) {
        pending.delete(identity);
        const current = matching(identity);
        if (!life.signal.aborted && current.length) {
          // Store failure state only, plus the supplied URL. JPEGs use the shared LRU.
          if (result.blob) finished.delete(identity);
          else finished.set(identity, result);
          if (finished.size > 128) finished.delete(finished.keys().next().value);
          for (const node of current) apply(node, result);
        }
      }
      drain();
    }
  }

  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      const card = entry.target;
      if (entry.isIntersecting && card.isConnected) {
        visible.add(card);
      } else visible.delete(card);
    }
    cancelOffscreen();
    for (const {target, isIntersecting} of entries) {
      if (isIntersecting && target.isConnected) request(target);
    }
  }, {rootMargin:'100px'});

  function observe() {
    for (const card of observed) {
      if (card.isConnected) continue;
      observer.unobserve(card);
      observed.delete(card);
      visible.delete(card);
      const url = urls.get(card);
      if (url) URL.revokeObjectURL(url);
      urls.delete(card);
    }
    for (const card of cards()) {
      if (observed.has(card)) continue;
      observed.add(card);
      const identity = key(card);
      if (card.dataset.videoPoster) card.querySelector('[data-video-player]').poster = card.dataset.videoPoster;
      else if (finished.get(identity)) apply(card, finished.get(identity));
      else if (pending.has(identity)) paint(card, 'queued');
      else paint(card, card.dataset.videoThumbnailState === 'unavailable' ? 'unavailable' : 'not_requested');
      observer.observe(card);
    }
    for (const [identity, job] of pending) {
      if (!matching(identity).length) cancel(identity, job);
    }
  }

  const changes = new MutationObserver(observe);
  changes.observe(document.querySelector('#oie-app') || document.body, {childList:true, subtree:true});
  life.on(document, 'click', event => {
    const button = event.target.closest('[data-video-thumbnail-load], [id^="shared-thumbnail-"]');
    if (!button || button.disabled) return;
    const card = button.closest(selector) || button.closest('[data-shared-item]')?.querySelector(selector);
    if (!card) return;
    event.preventDefault();
    button.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape', bubbles:true, cancelable:true}));
    request(card, true);
  });

  function setAutomatic(value) {
    automatic = value;
    for (const control of document.querySelectorAll('[data-auto-video-thumbnails]')) control.checked = value;
    if (value) {
      for (const card of visible) if (card.isConnected) request(card);
    } else cancelOffscreen();
  }
  life.on(document, 'change', event => {
    if (!event.target.matches('[data-auto-video-thumbnails]')) return;
    setAutomatic(event.target.checked);
    window.OieRuntime.memory.set(preference, automatic);
    try { localStorage.setItem(preference, String(automatic)); } catch {}
  });
  life.on(window, 'storage', event => {
    if (event.key === preference) setAutomatic(event.newValue !== 'false');
  });
  life.cleanup(() => {
    observer.disconnect();
    changes.disconnect();
    for (const job of pending.values()) job.controller.abort();
    pending.clear();
    for (const url of urls.values()) URL.revokeObjectURL(url);
  });
  setAutomatic(automatic);
  observe();
});
