// Loaded only after a user-initiated native playback attempt cannot present video.
const base = '/ui/libmedia/5978d2ba/';
const decoders = new Map([
  [12, 'mpeg4'], [27, 'h264'], [139, 'vp8'], [167, 'vp9'], [173, 'hevc'],
  [86017, 'mp3'], [86018, 'aac'], [86019, 'ac3'], [86021, 'vorbis'],
  [86028, 'flac'], [86076, 'opus']
]);
const scripts = new Map();

function loadScript(path, id) {
  if (!scripts.has(path)) {
    const ready = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      if (id) script.id = id;
      script.src = base + path;
      script.onload = resolve;
      script.onerror = () => { script.remove(); reject(new Error('Player unavailable')); };
      document.head.append(script);
    }).catch(error => { scripts.delete(path); throw error; });
    scripts.set(path, ready);
  }
  return scripts.get(path);
}

function getWasm(type, codec) {
  let path;
  if (type === 'decoder') {
    const name = codec >= 65536 && codec <= 65572 ? 'pcm' : decoders.get(codec);
    if (name) path = 'decode/' + name;
  } else {
    path = {resampler:'resample/resample', stretchpitcher:'stretchpitch/stretchpitch'}[type];
  }
  if (!path) throw new Error('Unsupported codec');
  return location.origin + base + path + '-simd.wasm';
}

let teardown = Promise.resolve();

export async function playFallback({card, source, ext, signal, takeAudio, onState, onFailure, focus}) {
  await loadScript('cheap-polyfill.js', 'cheap-polyfill');
  signal.throwIfAborted();
  await loadScript('avplayer/avplayer.js');
  signal.throwIfAborted();

  await teardown;
  signal.throwIfAborted();
  const template = document.querySelector('[data-video-fallback-template]');
  const root = template.content.firstElementChild.cloneNode(true);
  const picture = root.querySelector('.oie-fallback-picture');
  const toggle = root.querySelector('[data-fallback-toggle]');
  const seek = root.querySelector('[data-fallback-seek]');
  const volume = root.querySelector('[data-fallback-volume]');
  const original = new URL(source);
  original.searchParams.set('download', '1');
  root.querySelector('[data-fallback-download]').href = original.href;
  card.querySelector('.oie-video-stage').append(root);
  const audio = takeAudio();
  if (audio) window.AVPlayer.audioContext = audio;
  let player, observer, playing = false, rendered = false;
  const active = () => !signal.aborted;
  const failed = () => { if (active()) onFailure(); };
  const setPlaying = value => {
    playing = value;
    toggle.querySelector('[data-fallback-play-icon]').hidden = value;
    toggle.querySelector('[data-fallback-pause-icon]').hidden = !value;
    toggle.setAttribute('aria-label', value ? toggle.dataset.pause : toggle.dataset.play);
  };
  function dispose() {
    observer?.disconnect();
    root.remove();
    const context = window.AVPlayer.audioContext;
    teardown = (async () => {
      try { await player?.destroy(); }
      finally {
        try { await window.AVPlayer.stopPipelines(); }
        finally { if (context && context.state !== 'closed') await context.close(); }
      }
    })().catch(() => {});
  }
  signal.addEventListener('abort', dispose, {once:true});
  player = new window.AVPlayer({container:picture, enableWorker:true, enableHardware:false,
    enableWebGPU:false, enableWebCodecs:false, checkUseMSE:() => false, getWasm});
  player.on('error', failed);
  player.on('firstVideoRendered', () => {
    if (!active()) return;
    rendered = true;
    onState('videoPlaying', 'playing');
  });
  player.on('time', time => {
    if (!active()) return;
    seek.value = Number(time) / 1000;
    if (!rendered && Number(time) > 1500) failed();
  });
  player.on('ended', () => {
    if (!active()) return;
    if (!rendered) { failed(); return; }
    setPlaying(false);
    onState('videoReady', 'ready');
  });
  const sound = root.querySelector('[data-fallback-sound]');
  player.on('resume', () => { if (active()) sound.hidden = false; });
  player.on('audioContextRunning', () => { sound.hidden = true; });
  sound.addEventListener('click', () => { void player.resume().catch(failed); }, {signal});
  const options = {audio:true, video:true, subtitle:false};
  toggle.disabled = seek.disabled = volume.disabled = true;
  toggle.addEventListener('click', async () => {
    toggle.disabled = true;
    try {
      if (playing) {
        await player.pause();
        if (active()) { setPlaying(false); onState('videoPaused', 'paused'); }
      } else {
        void player.resume().catch(failed);
        await player.play(options);
        if (active()) { setPlaying(true); onState('videoPlaying', 'playing'); }
      }
    } catch { failed(); }
    finally { toggle.disabled = false; }
  }, {signal});
  seek.addEventListener('change', () => {
    void player.seek(BigInt(Math.round(Number(seek.value) * 1000))).catch(failed);
  }, {signal});
  volume.addEventListener('input', () => player.setVolume(Number(volume.value)), {signal});
  await player.load(source, {ext, http:{credentials:'same-origin',referrerPolicy:'no-referrer'}});
  signal.throwIfAborted();
  seek.max = String(Number(player.getDuration()) / 1000);
  observer = new ResizeObserver(() => {
    if (active() && picture.clientWidth && picture.clientHeight) player.resize(picture.clientWidth, picture.clientHeight);
  });
  observer.observe(picture);
  toggle.disabled = volume.disabled = false;
  seek.disabled = !(Number(seek.max) > 0);
  if (focus && document.activeElement === document.body) toggle.focus({preventScroll:true});
  setPlaying(true);
  await player.play(options);
}
