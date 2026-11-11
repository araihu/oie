window.OieRuntime.register('scheduling', life => {
  'use strict';
  const dialog = document.querySelector('[data-schedule-dialog]');
  if (!dialog) return;
  const composer = document.querySelector('[data-schedule-composer]');
  const picker = composer?.querySelector('[data-attachment-picker]');
  const attach = composer?.querySelector('[data-attach-file]');
  const attachments = composer?.querySelector('[data-composer-attachments]');
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content || '';
  const localTime = dialog.querySelector('#schedule-local-time');
  const offsets = dialog.querySelector('[data-schedule-offset]');
  const title = dialog.querySelector('h2');
  const error = dialog.querySelector('[data-schedule-error]');
  const previewText = dialog.querySelector('[data-schedule-preview-text]');
  const previewTime = dialog.querySelector('[data-schedule-preview-time]');
  const previewFiles = dialog.querySelector('[data-schedule-preview-files]');
  const quote = dialog.querySelector('[data-schedule-quote]');
  const submit = dialog.querySelector('[data-schedule-submit]');
  let activeForm, files = [], locked = false;
  const zone = () => window.OieTime.timeZone();
  const data = document.body.dataset;

  function wallTime(date) {
    const formatter = new Intl.DateTimeFormat('en-CA', {
      timeZone: zone(), hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit',
    });
    const values = Object.fromEntries(formatter.formatToParts(date).map(part => [part.type, part.value]));
    return values.year + '-' + values.month + '-' + values.day + 'T' + values.hour + ':' + values.minute;
  }

  function instants(value) {
    const utc = Date.parse(value + 'Z');
    if (!Number.isFinite(utc)) return [];
    const candidates = new Set();
    // Sample both sides of an offset transition. Round-tripping rejects gaps
    // and preserves both instants during a repeated local hour.
    for (const hours of [-36, 0, 36]) {
      const sample = utc + hours * 3600000;
      const offset = Date.parse(wallTime(new Date(sample)) + 'Z') - sample;
      const candidate = utc - offset;
      if (wallTime(new Date(candidate)) === value && candidate > Date.now()) candidates.add(candidate);
    }
    return [...candidates].sort((a, b) => a - b);
  }

  function updateTime() {
    const candidates = instants(localTime.value);
    const previous = offsets.value;
    offsets.replaceChildren();
    offsets.hidden = candidates.length < 2;
    if (!offsets.hidden) {
      offsets.add(new Option(dialog.dataset.timeAmbiguous, ''));
      for (const instant of candidates) {
        const date = new Date(instant);
        const label = window.OieTime.format(date, {hour: 'numeric', minute: 'numeric', timeZoneName: 'shortOffset'});
        offsets.add(new Option(label, String(instant)));
      }
      offsets.value = candidates.includes(Number(previous)) ? previous : '';
    }
    const selected = candidates.length === 1 ? candidates[0] : Number(offsets.value);
    previewTime.textContent = selected ? window.OieTime.format(new Date(selected), {hour: 'numeric', minute: '2-digit'}) : '';
    return selected;
  }

  function fileURL(id, download = false) {
    const query = new URLSearchParams({engineId: data.engineId, conversationId: data.conversationId, attachmentId: id});
    if (download) query.set('download', '1');
    return '/ui/scheduling/file?' + query;
  }

  function fileLink(file, preview) {
    const link = document.createElement('a');
    link.href = fileURL(file.id, true);
    link.download = file.fileName;
    if (preview && ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'image/avif'].includes(file.mediaType)) {
      const img = document.createElement('img');
      img.src = fileURL(file.id);
      img.alt = file.fileName;
      img.width = 224;
      img.height = 128;
      link.append(img);
    }
    link.append(file.fileName);
    return link;
  }

  function snapshot() {
    const detail = {blocked: true};
    composer?.dispatchEvent(new CustomEvent('oie:schedule-snapshot', {detail}));
    return detail;
  }

  function renderFiles() {
    if (!attachments) return;
    attachments.replaceChildren();
    attachments.hidden = !files.length;
    for (const file of files) {
      const item = document.createElement('span');
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.textContent = '×';
      remove.setAttribute('aria-label', dialog.dataset.removeAttachment + ' · ' + file.fileName);
      remove.disabled = locked || composer.dataset.staging === 'true';
      remove.dataset.removeAttachment = file.id;
      item.append(fileLink(file, false), remove);
      attachments.append(item);
    }
    if (attach) attach.disabled = picker.disabled || locked || composer.dataset.staging === 'true';
  }

  function newID() {
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    let ms = Date.now();
    for (let i = 5; i >= 0; i--) { bytes[i] = ms % 256; ms = Math.floor(ms / 256); }
    bytes[6] = (bytes[6] & 15) | 112;
    bytes[8] = (bytes[8] & 63) | 128;
    const raw = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
    return [raw.slice(0, 8), raw.slice(8, 12), raw.slice(12, 16), raw.slice(16, 20), raw.slice(20)].join('-');
  }

  function attachmentError(message) {
    window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message}}));
  }

  life.on(composer, 'oie:composer-attachments', event => {
    const changed = locked !== event.detail.locked || JSON.stringify(files) !== JSON.stringify(event.detail.files);
    files = event.detail.files;
    locked = event.detail.locked;
    if (changed) renderFiles();
  });
  if (composer) { files = snapshot().files || []; renderFiles(); }
  life.on(attach, 'click', () => picker?.click());
  life.on(attachments, 'click', event => {
    const remove = event.target.closest('[data-remove-attachment]');
    if (!remove || locked) return;
    composer.dispatchEvent(new CustomEvent('oie:attachments-change', {detail: files.filter(file => file.id !== remove.dataset.removeAttachment)}));
    attach?.focus();
  });
  life.on(picker, 'change', async () => {
    const picked = [...picker.files];
    picker.value = '';
    if (!picked.length || locked || composer.dataset.staging === 'true') return;
    if (files.length + picked.length > 16 || picked.some(file => file.size > 20 * 1024 * 1024)) {
      attachmentError(dialog.dataset.attachmentLimit);
      return;
    }
    composer.dataset.staging = 'true';
    composer.dispatchEvent(new Event('oie:attachment-staging'));
    renderFiles();
    const next = [...files];
    try {
      for (const file of picked) {
        const id = newID(), form = new FormData();
        form.set('file', file);
        const response = await life.fetch(fileURL(id).replace('/file?', '/stage?'), {
          method: 'POST', headers: {'X-CSRF-Token': csrf}, body: form,
        });
        if (!response.ok) throw Error('staging');
        const metadata = await response.json();
        if (metadata.id !== id || typeof metadata.fileName !== 'string' || metadata.size !== file.size) throw Error('staging');
        next.push(metadata);
      }
    } catch {
      if (!life.signal.aborted) attachmentError(dialog.dataset.attachmentFailed);
    } finally {
      if (!life.signal.aborted) {
        composer.dataset.staging = 'false';
        composer.dispatchEvent(new CustomEvent('oie:attachments-change', {detail: next}));
        renderFiles();
      }
    }
  });

  function open(form, initial, text, reply, items, reschedule) {
    activeForm = form;
    title.textContent = submit.textContent = reschedule ? dialog.dataset.update : dialog.dataset.create;
    error.hidden = true;
    offsets.value = '';
    previewText.replaceChildren();
    for (const fragment of text.split(/(https?:\/\/[^\s<>()]+)/gi)) {
      if (/^https?:\/\//i.test(fragment)) {
        const link = document.createElement('a');
        link.href = fragment;
        link.textContent = fragment;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        previewText.append(link);
      } else {
        previewText.append(fragment);
      }
    }
    quote.hidden = !reply;
    quote.textContent = reply ? [reply.senderName, reply.excerpt].filter(Boolean).join(' · ') : '';
    previewFiles.replaceChildren(...items.map(file => fileLink(file, true)));
    const requested = Date.parse(initial);
    localTime.value = wallTime(new Date(requested > Date.now() + 60000 ? requested : Date.now() + 3600000));
    dialog.querySelector('[data-schedule-zone]').textContent = zone().replaceAll('_', ' ');
    updateTime();
    dialog.showModal();
    localTime.focus();
  }

  life.on(document, 'oie:schedule-compose', () => {
    const current = snapshot();
    if (current.blocked || (!current.text.trim() && !current.files.length)) return;
    const editing = composer.hasAttribute('data-editing-schedule');
    const original = document.getElementById('schedule-edit-data');
    open(composer, original ? JSON.parse(original.textContent).scheduledAt : '', current.text, current.reply, current.files, editing);
  });
  life.on(document, 'click', event => {
    const reschedule = event.target.closest('[data-reschedule]');
    if (!reschedule) return;
    const row = reschedule.closest('[data-scheduled-row]');
    const form = row.querySelector('[data-schedule-update]');
    if (form.dataset.busy === 'true') return;
    const source = row.querySelector('[data-scheduled-quote]');
    const reply = source ? {senderName: source.dataset.sender, excerpt: source.content.textContent} : null;
    open(form, form.elements.namedItem('scheduledAt').value || reschedule.dataset.reschedule, row.querySelector('[data-scheduled-text]')?.textContent || '', reply, [], true);
    previewFiles.replaceChildren(...[...(row.querySelector('[data-message-attachments]')?.children || [])].map(node => node.cloneNode(true)));
  });
  life.on(dialog.querySelector('[data-schedule-close]'), 'click', () => dialog.close());
  life.on(localTime, 'input', () => { error.hidden = true; updateTime(); });
  life.on(offsets, 'change', () => { error.hidden = true; updateTime(); });
  life.on(dialog.querySelector('[data-schedule-confirm]'), 'submit', event => {
    event.preventDefault();
    const instant = updateTime();
    if (!instant) {
      error.textContent = offsets.hidden ? dialog.dataset.timeInvalid : dialog.dataset.timeAmbiguous;
      error.hidden = false;
      return;
    }
    if (activeForm === composer && snapshot().blocked) return;
    const input = activeForm.elements.namedItem('scheduledAt');
    input.value = new Date(instant).toISOString();
    dialog.close();
    activeForm.requestSubmit();
    if (activeForm === composer) input.value = '';
  });
  window.OieTime.localize(document);
  life.on(document, 'oie:time-zone', () => window.OieTime.localize(document));
});
