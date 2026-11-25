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

function mediaAttachment(file, info, blob) {
  const originalMP3 = blob === file && info.mp3;
  const type = originalMP3 ? 'audio/mpeg' : info.video ? 'video/mp4' : 'audio/mp4';
  const suffix = originalMP3 ? '.mp3' : info.video ? '.mp4' : '.m4a';
  const named = file.name.toLowerCase().endsWith(suffix) || blob === file && !originalMP3 &&
    (info.video ? /\.m4v$/i : /\.mp4$/i).test(file.name);
  const name = named ? file.name : (file.name.replace(/\.[^.]+$/, '') || file.name) + suffix;
  if (blob === file && file.name === name && file.type === type) return file;
  return new File([blob], name, {type, lastModified:file.lastModified});
}

export async function chooseMedia(content, file, signal, preferred, recording) {
  signal.throwIfAborted();
  const dialog = content.closest('dialog');
  const events = new AbortController();
  const options = {signal: events.signal};
  const submit = dialog.querySelector('[data-prepare-media]');
  const raw = dialog.querySelector('[data-media-as-file]');
  const message = dialog.querySelector('[role=status]');
  const group = dialog.querySelector('fieldset');
  const radios = [...group.querySelectorAll('input[type=radio]')];
  const limit = Number(content.dataset.uploadLimit) || undefined;
  const originalFits = !limit || file.size <= limit;
  const limitLabel = dialog.querySelector('[data-media-limit]');
  limitLabel.hidden = !limit;
  if (limit) {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    const unit = Math.min(Math.floor(Math.log2(limit) / 10), units.length - 1);
    const size = new Intl.NumberFormat(document.documentElement.lang, {maximumFractionDigits: 2}).format(limit / 1024 ** unit);
    limitLabel.textContent = content.dataset.limitText.replace('{size}', size + ' ' + units[unit]);
  }
  let ff, info;
  let finish;
  const choice = new Promise(resolve => { finish = resolve; });
  let selected = null;
  function close() {
    dialog.dispatchEvent(new CustomEvent('modal:close', {bubbles:true, detail:{id:dialog.id}}));
  }
  function accept(output) {
    selected = output;
    close();
  }
  group.disabled = true;
  submit.disabled = true;
  raw.disabled = !originalFits;
  submit.hidden = false;
  raw.hidden = !recording;
  for (const radio of radios) {
    radio.disabled = radio.value === 'OG';
    radio.checked = radio.value === (preferred === 'HD' ? 'HD' : 'SD');
  }
  dialog.querySelector('[data-media-filename]').textContent = file.name;
  message.textContent = content.dataset.checking;
  function offerOriginal(label) {
    if (info?.compatible && originalFits) {
      for (const radio of radios) {
        radio.disabled = radio.value !== 'OG';
        radio.checked = !radio.disabled;
      }
      group.disabled = submit.disabled = submit.hidden = false;
      message.textContent = label;
      return;
    }
    group.disabled = true;
    submit.disabled = true;
    submit.hidden = true;
    raw.hidden = false;
    raw.disabled = !originalFits;
    message.textContent = originalFits ? label : content.dataset.tooLarge;
    if (originalFits && dialog.contains(document.activeElement)) raw.focus({preventScroll:true});
  }
  raw.addEventListener('click', () => accept(file), options);
  dialog.addEventListener('close', () => { if (!dialog.open) finish(selected); }, options);
  signal.addEventListener('abort', () => finish(null), options);
  submit.addEventListener('click', async () => {
    if (!info || submit.disabled) return;
    group.disabled = submit.disabled = true;
    raw.disabled = true;
    message.textContent = content.dataset.preparing;
    try {
      const quality = radios.find(radio => radio.checked).value;
      const output = await prepareMedia(ff, file, info, quality, limit);
      if (events.signal.aborted) return;
      if (output.route === 'offer file') { offerOriginal(content.dataset.keepTracks); return; }
      if (output.route === 'too large') {
        group.disabled = submit.disabled = false;
        raw.disabled = !originalFits;
        message.textContent = content.dataset.tooLarge;
        return;
      }
      accept(mediaAttachment(file, info, output.blob));
    } catch {
      if (!events.signal.aborted) offerOriginal(content.dataset.unavailable);
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
      if (info.problem) { offerOriginal(content.dataset[info.problem]); return; }
      const og = radios.find(radio => radio.value === 'OG');
      og.disabled = !originalFits || !info.compatible;
      if (preferred === 'OG' && !og.disabled) og.checked = true;
      message.textContent = !originalFits ? content.dataset.originalTooLarge : '';
      submit.disabled = group.disabled = false;
    } catch {
      if (!events.signal.aborted) offerOriginal(content.dataset.unavailable);
    }
  }
  try {
    dialog.dispatchEvent(new CustomEvent('modal:open', {bubbles:true, detail:{id:dialog.id}}));
    void inspect();
    return await choice;
  } finally {
    events.abort();
    ff?.terminate();
    if (signal.aborted) close();
  }
}
