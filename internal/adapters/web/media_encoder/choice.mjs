import {inspectMedia, prepareMedia} from './prepare.mjs';

const codecRoot = new URL('./v0.12.15-core0.12.10/', import.meta.url);
let loading;
function loadEncoder() {
  if (!loading) {
    loading = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = new URL('ffmpeg.js', codecRoot);
      script.onload = resolve;
      script.onerror = () => { script.remove(); reject(new Error('Encoder unavailable')); };
      document.head.append(script);
    }).catch(error => { loading = undefined; throw error; });
  }
  return loading;
}

export async function chooseMedia(dialog, file, signal, preferred, recording) {
  signal.throwIfAborted();
  const events = new AbortController();
  const options = {signal: events.signal};
  const submit = dialog.querySelector('[data-prepare-media]');
  const raw = dialog.querySelector('[data-media-as-file]');
  const message = dialog.querySelector('[role=status]');
  const group = dialog.querySelector('fieldset');
  const radios = [...group.querySelectorAll('input[type=radio]')];
  const limit = Number(dialog.dataset.uploadLimit) || undefined;
  const originalFits = !limit || file.size <= limit;
  const limitLabel = dialog.querySelector('[data-media-limit]');
  limitLabel.hidden = !limit;
  if (limit) limitLabel.textContent = dialog.dataset.limitText.replace('{bytes}', new Intl.NumberFormat(document.documentElement.lang).format(limit));
  let ff, info;
  let finish;
  const choice = new Promise(resolve => { finish = resolve; });
  function cancel() { finish(null); }
  group.disabled = true;
  submit.disabled = true;
  raw.disabled = !originalFits;
  raw.hidden = !recording;
  for (const radio of radios) {
    radio.disabled = radio.value === 'OG';
    radio.checked = radio.value === (preferred === 'HD' ? 'HD' : 'SD');
  }
  dialog.querySelector('[data-media-filename]').textContent = file.name;
  message.textContent = dialog.dataset.checking;
  function offerFile(label) {
    group.disabled = true;
    submit.disabled = true;
    raw.hidden = false;
    raw.disabled = !originalFits;
    message.textContent = originalFits ? label : dialog.dataset.tooLarge;
    if (originalFits && dialog.contains(document.activeElement)) raw.focus({preventScroll:true});
  }
  raw.addEventListener('click', () => finish(file), options);
  dialog.querySelector('[data-media-cancel]').addEventListener('click', cancel, options);
  dialog.addEventListener('cancel', event => { event.preventDefault(); cancel(); }, options);
  dialog.addEventListener('close', () => { if (!dialog.open) cancel(); }, options);
  signal.addEventListener('abort', cancel, options);
  submit.addEventListener('click', async () => {
    if (!info || submit.disabled) return;
    group.disabled = submit.disabled = true;
    raw.disabled = true;
    message.textContent = dialog.dataset.preparing;
    try {
      const quality = radios.find(radio => radio.checked).value;
      const output = await prepareMedia(ff, file, info, quality, limit);
      if (events.signal.aborted) return;
      if (output.route === 'offer file') { offerFile(dialog.dataset.keepTracks); return; }
      if (output.route === 'too large') {
        group.disabled = submit.disabled = false;
        raw.disabled = !originalFits;
        message.textContent = dialog.dataset.tooLarge;
        return;
      }
      if (output.blob === file) { finish(file); return; }
      const suffix = info.video ? '.mp4' : '.m4a';
      finish(new File([output.blob], file.name.replace(/\.[^.]+$/, '') + suffix, {type:output.blob.type}));
    } catch {
      if (!events.signal.aborted) offerFile(dialog.dataset.unavailable);
    }
  }, options);
  async function inspect() {
    try {
      await loadEncoder();
      if (events.signal.aborted) return;
      ff = new window.FFmpegWASM.FFmpeg();
      await ff.load({coreURL: new URL('ffmpeg-core.js', codecRoot).href,
        wasmURL: new URL('ffmpeg-core.wasm', codecRoot).href});
      info = await inspectMedia(ff, file);
      if (events.signal.aborted) return;
      if (!info.preservable) { offerFile(dialog.dataset.keepTracks); return; }
      const og = radios.find(radio => radio.value === 'OG');
      og.disabled = !originalFits || !info.compatible;
      if (preferred === 'OG' && !og.disabled) og.checked = true;
      message.textContent = !originalFits ? dialog.dataset.originalTooLarge : '';
      submit.disabled = group.disabled = false;
    } catch {
      if (!events.signal.aborted) offerFile(dialog.dataset.unavailable);
    }
  }
  try {
    dialog.showModal();
    void inspect();
    return await choice;
  } finally {
    events.abort();
    ff?.terminate();
    dialog.close();
  }
}
