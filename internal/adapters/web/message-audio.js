window.OieRuntime.register('message-audio', life => {
  const app = document.getElementById('oie-app');
  if (!app) return;
  const selector = '[data-message-audio]';
  // Bound decoding while leaving large remote files available through streaming playback.
  const waveformBudget = 20 * 1024 * 1024;
  const players = new Map();
  const visible = new Set();
  const observed = new Set();
  const waiting = new Set();
  let active, prefetching;

  function status(card, message = '') {
    card.querySelector('[role="status"]').textContent = message;
  }

  function offerPlayback(root) {
    if (!root.querySelectorAll) return;
    const cards = root.matches?.(selector) ? [root] : root.querySelectorAll(selector);
    for (const card of cards) {
      const media = card.querySelector('audio');
      if (observed.has(card) || !media.paused || document.activeElement === media ||
          (card.dataset.audioType && !media.canPlayType(card.dataset.audioType))) continue;
      observed.add(card);
      card.setAttribute('data-waveform-player', '');
      media.removeAttribute('src');
      media.removeAttribute('controls');
      // Discard the server-rendered element's resource selection before preloading a blob.
      media.replaceWith(media.cloneNode(false));
      for (const control of card.querySelectorAll('[data-message-audio-play], [data-message-audio-wave], .oie-message-audio-time')) {
        control.hidden = false;
      }
      visibility.observe(card);
    }
  }

  function clock(seconds) {
    const whole = Math.floor(seconds);
    return Math.floor(whole / 60) + ':' + String(whole % 60).padStart(2, '0');
  }

  function update(player) {
    const {card, media, button, wave, shape} = player;
    const playing = player.playRequested || (!media.paused && !media.ended);
    button.setAttribute('aria-label', (playing ? card.dataset.audioPauseLabel : card.dataset.audioPlayLabel) + ' · ' + card.closest('[data-audio-id]').dataset.audioFilename);
    button.querySelector('[data-record-play-icon]').hidden = playing;
    button.querySelector('[data-record-pause-icon]').hidden = !playing;
    const duration = wave?.getDuration() || player.duration || media.duration;
    if (!Number.isFinite(duration)) return;
    card.querySelector('[data-message-audio-time]').textContent = clock(duration);
    if (shape.matches('[role="slider"]')) {
      const position = Math.min(media.currentTime, duration);
      shape.setAttribute('aria-valuemax', duration);
      shape.setAttribute('aria-valuenow', position);
      shape.setAttribute('aria-valuetext', clock(position) + ' / ' + clock(duration));
    }
  }

  function showUnavailable(card) {
    window.dispatchEvent(new CustomEvent('notify', {
      detail: {kind: 'toast', tone: 'danger', message: card.dataset.audioUnavailable}
    }));
  }

  function release(player) {
    if (!player || players.get(player.card) !== player) return;
    players.delete(player.card);
    if (active === player) active = null;
    const {card, media, button, shape} = player;
    player.playRequested = false;
    player.controller.abort();
    player.wave?.destroy();
    const focused = card.isConnected && (document.activeElement === media || document.activeElement === shape);
    media.pause();
    media.removeAttribute('src');
    media.removeAttribute('controls');
    media.preload = 'none';
    media.load();
    player.wave = null;
    if (player.url) URL.revokeObjectURL(player.url);
    card.removeAttribute('aria-busy');
    button.hidden = shape.hidden = false;
    for (const name of ['role', 'tabindex', 'aria-label', 'aria-valuemin', 'aria-valuemax', 'aria-valuenow', 'aria-valuetext']) {
      shape.removeAttribute(name);
    }
    status(card);
    update(player);
    if (focused) button.focus({preventScroll: true});
  }

  async function fetchAudio(source, mediaType, signal) {
    // Catalog media can be much larger than a staged attachment. Request a
    // bounded range so automatic preparation never triggers a full engine read.
    const headers = new URL(source).pathname.startsWith('/ui/shared-content/')
      ? {Range: 'bytes=0-' + (waveformBudget - 1)}
      : {};
    const response = await life.fetch(source, {signal, headers});
    if (!response.ok || !response.body) throw Error('audio unavailable');
    const lengthHeader = response.headers.get('Content-Length');
    const total = response.status === 206 ? response.headers.get('Content-Range')?.split('/').at(-1) : lengthHeader;
    if (Number(total) > waveformBudget || (response.status === 206 && Number(total) !== Number(lengthHeader))) {
      await response.body.cancel();
      return null;
    }
    const type = response.headers.get('Content-Type')?.split(';', 1)[0];
    const reader = response.body.getReader();
    const chunks = [];
    let length = 0;
    try {
      for (;;) {
        const {value, done} = await reader.read();
        if (done) break;
        length += value.byteLength;
        if (length > waveformBudget) {
          await reader.cancel();
          return null;
        }
        chunks.push(value);
      }
    } finally {
      reader.releaseLock();
    }
    return new Blob(chunks, {type: type?.startsWith('audio/') ? type : mediaType});
  }

  function current(player) {
    return players.get(player.card) === player && player.card.isConnected;
  }

  function useNativeControls(player) {
    const focused = document.activeElement === player.button;
    player.media.controls = true;
    player.button.hidden = player.shape.hidden = true;
    if (focused) player.media.focus({preventScroll: true});
  }

  async function play(player) {
    player.playRequested = true;
    update(player);
    if (player.recovering) return;
    const {card, media, source} = player;
    if (!media.hasAttribute('src')) media.src = source;
    const attemptedSource = media.src;
    if (player.waveUnavailable && !media.controls) useNativeControls(player);
    if (media.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) status(card, card.dataset.audioLoading);
    try {
      const playback = player.wave?.getMediaElement() === media ? player.wave : media;
      await playback.play();
    } catch (error) {
      if (active !== player || media.src !== attemptedSource || error.name === 'AbortError') return;
      if (error.name === 'NotAllowedError') {
        player.playRequested = false;
        update(player);
        return;
      }
      void playbackError(player);
    } finally {
      if (current(player) && active === player && media.src === attemptedSource && !player.recovering) status(card);
    }
  }

  async function playbackError(player) {
    if (!current(player) || player.recovering) return;
    const {media, source} = player;
    if (media.src === source) {
      const position = media.currentTime;
      // A provider or WebView can reject the native range probe while the
      // bounded waveform request succeeds. Reuse those bytes for playback.
      player.recovering = true;
      await player.buffered.catch(() => {});
      if (!current(player)) return;
      player.recovering = false;
      if (player.url) {
        media.preload = 'metadata';
        media.src = player.url;
        // A seek before metadata sets the new resource's playback start position.
        if (position > 0) media.currentTime = position;
        if (active === player && player.playRequested) void play(player);
        return;
      }
    }
    const requested = active === player;
    release(player);
    if (requested) showUnavailable(player.card);
  }

  async function preloadBlob(player) {
    const {card, media, source, controller} = player;
    const fetchTimeout = setTimeout(() => controller.abort(), 20000);
    try {
      const blob = await fetchAudio(source, card.dataset.audioType, controller.signal);
      if (!blob || !current(player)) return null;
      player.url = URL.createObjectURL(blob);
      // Reuse the fetched bytes when playback has yet to select a source.
      if (!media.hasAttribute('src')) {
        media.preload = 'metadata';
        media.src = player.url;
      }
      return blob;
    } finally {
      clearTimeout(fetchTimeout);
    }
  }

  async function prepare(player) {
    const {card, media, shape, controller} = player;
    card.setAttribute('aria-busy', 'true');
    try {
      const blob = await player.buffered;
      if (!current(player)) return;
      if (!blob) throw Error('waveform unavailable');
      const {audioDuration, audioPeaks, waveformDurationLimit} = await import('/ui/audio-duration.js');
      const mediaDuration = media.duration;
      const duration = Number.isFinite(mediaDuration) && mediaDuration > 0
        ? mediaDuration : await audioDuration(blob, controller.signal);
      if (!current(player)) return;
      player.duration = duration;
      if (!Number.isFinite(duration) || duration <= 0 || duration > waveformDurationLimit) throw Error('waveform unavailable');
      const [{default: WaveSurfer}, peaks] = await Promise.all([
        import('/ui/wavesurfer/player.mjs'), audioPeaks(blob, controller.signal),
      ]);
      if (!current(player)) return;
      const colors = getComputedStyle(shape);
      // Initializing with a blank element prevents WaveSurfer from fetching the
      // playback URL or replacing the audio that is already playing.
      player.wave = WaveSurfer.create({container: shape, media: document.createElement('audio'), waveColor: colors.color,
        progressColor: colors.getPropertyValue('--oie-accent-text').trim(), height: 24, normalize: true,
        barWidth: 2, barGap: 2, barRadius: 2, barMinHeight: 2, cursorWidth: 0});
      await player.wave.load('', peaks, duration);
      if (!current(player)) return;
      player.wave.setMediaElement(media);
      shape.tabIndex = 0;
      shape.setAttribute('role', 'slider');
      shape.setAttribute('aria-label', card.dataset.audioPositionLabel);
      shape.setAttribute('aria-valuemin', '0');
      // Draw the current position even if playback paused while decoding.
      media.dispatchEvent(new Event('timeupdate'));
    } catch {
      player.wave?.destroy();
      player.wave = null;
      player.waveUnavailable = true;
      if (current(player) && active === player) useNativeControls(player);
    } finally {
      if (current(player)) {
        card.removeAttribute('aria-busy');
        update(player);
      }
    }
  }

  function loadPlayer(card) {
    if (players.has(card)) return players.get(card);
    const source = new URL(card.dataset.audioSource, location.href);
    if (source.protocol !== location.protocol || source.host !== location.host ||
        !/^\/ui\/(?:scheduling\/file|shared-content\/[a-f0-9-]+\/content)$/.test(source.pathname)) return;
    const player = {
      card, source: source.href, controller: new AbortController(),
      media: card.querySelector('audio'),
      shape: card.querySelector('[data-message-audio-wave]'),
      button: card.querySelector('[data-message-audio-play]'),
    };
    players.set(card, player);
    player.buffered = preloadBlob(player);
    player.preparation = prepare(player);
    return player;
  }

  // Keep automatic waveform decoding serial.
  function prefetchNext() {
    if (prefetching || life.signal.aborted) return;
    for (const card of waiting) {
      waiting.delete(card);
      if (!visible.has(card) || !card.isConnected || players.has(card)) continue;
      prefetching = loadPlayer(card);
      if (!prefetching) continue;
      void prefetching.preparation.finally(() => {
        prefetching = null;
        prefetchNext();
      });
      break;
    }
  }

  function selectPlayer(player) {
    const previous = active;
    active = player;
    if (!previous || previous === player) return;
    previous.playRequested = false;
    previous.media.pause();
    status(previous.card);
    if (!visible.has(previous.card)) release(previous);
    else update(previous);
  }

  life.on(app, 'click', event => {
    const button = event.target.closest('[data-message-audio-play]');
    const card = button?.closest(selector);
    if (!card) return;
    waiting.delete(card);
    const player = loadPlayer(card);
    if (!player) return;
    if (active === player && (player.playRequested || !player.media.paused)) {
      player.playRequested = false;
      const {wave, media} = player;
      const playback = wave?.getMediaElement() === media ? wave : media;
      playback.pause();
      status(card);
      update(player);
      return;
    }
    selectPlayer(player);
    void play(player);
  });

  life.on(app, 'keydown', event => {
    const player = players.get(event.target.closest(selector));
    if (player?.shape !== event.target || !player.wave) return;
    const duration = player.wave.getDuration();
    const now = player.media.currentTime;
    const time = {ArrowLeft: now - 5, ArrowDown: now - 5, ArrowRight: now + 5, ArrowUp: now + 5, Home: 0, End: duration}[event.key];
    if (!Number.isFinite(time)) return;
    event.preventDefault();
    player.wave.setTime(Math.max(0, Math.min(duration, time)));
  });

  for (const event of ['timeupdate', 'durationchange', 'loadedmetadata', 'play', 'pause', 'ended']) {
    life.on(app, event, event => {
      const player = players.get(event.target.closest?.(selector));
      if (event.type === 'play' && event.target.matches?.('[data-audio-player]') && !event.target.paused) {
        selectPlayer(player);
      }
      if (event.target !== player?.media) return;
      if (event.type === 'play' && !player.media.paused) player.playRequested = true;
      if (event.type === 'ended' || (event.type === 'pause' && player.media.paused && !player.media.error)) {
        player.playRequested = false;
      }
      update(player);
    }, true);
  }
  life.on(app, 'error', event => {
    if (!event.target.matches?.('audio')) return;
    const player = players.get(event.target.closest(selector));
    if (event.target === player?.media) void playbackError(player);
  }, true);

  const visibility = new IntersectionObserver(entries => {
    for (const {target: card, isIntersecting} of entries) {
      if (!observed.has(card)) continue;
      if (isIntersecting) {
        visible.add(card);
        if (!players.has(card)) waiting.add(card);
      } else {
        visible.delete(card);
        waiting.delete(card);
        const player = players.get(card);
        if (player !== active) release(player);
      }
    }
    prefetchNext();
  });
  offerPlayback(app);

  const observer = new MutationObserver(records => {
    for (const card of observed) {
      if (card.isConnected) continue;
      visibility.unobserve(card);
      observed.delete(card);
      visible.delete(card);
      waiting.delete(card);
      release(players.get(card));
    }
    for (const record of records) for (const node of record.addedNodes) offerPlayback(node);
  });
  observer.observe(app, {childList: true, subtree: true});
  life.on(window, 'pagehide', () => {
    waiting.clear();
    for (const player of players.values()) release(player);
  });
  life.on(window, 'pageshow', event => {
    if (!event.persisted) return;
    for (const card of visible) waiting.add(card);
    prefetchNext();
  });
  life.cleanup(() => {
    observer.disconnect();
    visibility.disconnect();
    waiting.clear();
    for (const player of players.values()) release(player);
  });
});
