window.OieRuntime.register('actions', life => {
 'use strict';
 let active;

 function disposeMenu() {
  if (!active) return;
  const {slot, root, trigger, observer, request} = active;
  active = undefined;
  observer.disconnect();
  request?.abort();
  if (!slot.contains(root)) return;
  const restoreFocus = root.contains(document.activeElement);
  window.Alpine.mutateDom(() => {
   trigger.remove();
   window.Alpine.destroyTree(root);
   trigger.removeAttribute('aria-controls');
   trigger.setAttribute('aria-expanded', 'false');
   slot.replaceChildren(trigger);
  });
  if (restoreFocus) trigger.focus({preventScroll:true});
 }

 function openMenu(event) {
  const trigger = event.target.closest?.('[data-message-menu-trigger]');
  const slot = trigger?.closest('[data-menu-url]');
  if (!slot || active?.root.contains(trigger) || !window.Alpine) return;
  const controls = window.OieRuntime.messageControls('menu', slot.dataset.messageMenuSlot);
  if (!controls) return;
  const root = controls.firstElementChild;
  const extra = controls.querySelector('[data-menu-extra]');
  const panel = root.querySelector('[role="menu"]');
  event.preventDefault();
  event.stopImmediatePropagation();
  disposeMenu();
  extra.setAttribute('hx-get', slot.dataset.menuUrl);
  panel.append(extra);
  const retry = root.querySelector('[id^="retry-menu-"]');
  retry.hidden = true;
  const load = () => extra.dispatchEvent(new Event('oie:menu-load'));
  retry.addEventListener('click', load);
  const focused = document.activeElement === trigger;
  window.Alpine.mutateDom(() => {
   root.querySelector('[data-message-menu-trigger]').replaceWith(trigger);
   slot.replaceChildren(root);
   window.Alpine.initTree(root);
  });
  if (focused) trigger.focus({preventScroll:true});
  const observer = new MutationObserver(() => {
   if (trigger.getAttribute('aria-expanded') !== 'false' || panel.style.display !== 'none') return;
   // Let the component finish its transition and Escape focus restoration.
   queueMicrotask(() => {
    if (active?.root === root && trigger.getAttribute('aria-expanded') === 'false') disposeMenu();
   });
  });
  active = {slot, root, trigger, observer, extra, retry, load};
  window.htmx.process(root);
  // A long press already called trigger.click(). Replay through the mounted
  // popover without reentering the native button's click activation.
  if (event.type === 'click') trigger.dispatchEvent(new MouseEvent('click', {bubbles:true, cancelable:true}));
  else trigger.dispatchEvent(new KeyboardEvent('keydown', {key:event.key, bubbles:true, cancelable:true}));
  load();
  observer.observe(trigger, {attributes:true, attributeFilter:['aria-expanded']});
  observer.observe(panel, {attributes:true, attributeFilter:['style']});
 }

 // Keep local Copy mounted across the request, a retry, or a live update.
 life.on(document, 'htmx:before:request', ({detail:{ctx}}) => {
  if (!active || ctx.target !== active.extra) return;
  active.request = ctx.request;
  ctx.oieMenuStartedAt = performance.now();
  ctx.oieMenuStarGeneration = Number(document.body.dataset.starGeneration || 0);
  ctx.target.setAttribute('aria-busy','true');
 });
 life.on(document, 'htmx:after:request', event => {
  const ctx = event.detail.ctx;
  if (!ctx.target?.hasAttribute('data-menu-extra')) return;
  if (!active || ctx.target !== active.extra || !ctx.target.isConnected ||
      active.trigger.getAttribute('aria-expanded') === 'false' || ctx.response?.status !== 200) {
   event.preventDefault();
   return;
  }
  if (ctx.target.contains(document.activeElement) || document.activeElement === active.retry) {
   ctx.oieMenuFocus = document.activeElement.id;
  }
 });
 life.on(document, 'htmx:after:swap', ({detail:{ctx}}) => {
  if (!active || ctx.target !== active.extra) return;
  active.retry.hidden = true;
  const state = ctx.target.querySelector('[data-menu-state]');
  if (state) state.dataset.startedAt = String(ctx.oieMenuStartedAt);
  document.dispatchEvent(new CustomEvent('oie:actions-refreshed', {detail: {
   menuState: state?.dataset, starGeneration: ctx.oieMenuStarGeneration,
  }}));
  if (ctx.oieMenuFocus) {
   const previous = document.getElementById(ctx.oieMenuFocus);
   (previous && !previous.disabled && !previous.hidden ? previous : active.root.querySelector('[id^="copy-message-"]')).focus({preventScroll:true});
  }
 });
 life.on(document, 'htmx:finally:request', ({detail:{ctx}}) => {
  if (!active || ctx.target !== active.extra || active.request !== ctx.request) return;
  active.request = undefined;
  ctx.target.removeAttribute('aria-busy');
  if (ctx.response?.status !== 200) {
   const focused = ctx.target.contains(document.activeElement);
   ctx.target.replaceChildren();
   active.retry.hidden = false;
   if (focused) active.retry.focus({preventScroll:true});
  }
 });
 life.on(document, 'oie:message-menu-updated', event => {
  if (active?.slot === event.target) active.load();
 });
 life.on(document, 'click', openMenu, true);
 life.on(document, 'keydown', event => {
  if (['Enter',' ','ArrowDown'].includes(event.key)) openMenu(event);
 }, true);
 life.cleanup(disposeMenu);
 // Public Dropdown item IDs are application hooks. Escape uses its supported
 // keyboard lifecycle to dismiss and restore focus after native actions.
 life.on(document,'click',event=>{
  const item=event.target.closest('[id^="copy-message-"],[id^="reply-message-"],[id^="star-message-"],[id^="load-preview-"],[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"],[id^="refresh-preview-"],[id^="save-audio-"]');
  if(item&&!item.disabled)item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
 },true);
});

(() => {
  'use strict';

  function clearKey(form) {
    form.elements.apiKey.value = '';
    form.elements.apiKeyAddress.value = '';
  }

  function clearMismatchedKey(form) {
    const {url, apiKeyAddress} = form.elements;
    if (apiKeyAddress.value !== url.value) {
      clearKey(form);
    }
  }

  function clearEngineKey() {
    const form = document.getElementById('engine-mode-form');
    if (form) clearKey(form);
  }

  for (const type of ['drawer:close', 'drawer:close-request']) {
    window.addEventListener(type, event => {
      if (event.detail?.id !== 'settings') return;
      queueMicrotask(() => {
        if (!event.defaultPrevented) clearEngineKey();
      });
    });
  }
  window.addEventListener('pagehide', clearEngineKey);

  document.addEventListener('input', event => {
    const input = event.target;
    const form = input.form;
    if (form?.id !== 'engine-mode-form') return;
    if (input.name === 'apiKey') {
      form.elements.apiKeyAddress.value = input.value ? form.elements.url.value : '';
    } else if (input.name === 'url') {
      clearMismatchedKey(form);
      const key = form.elements.apiKey;
      key.placeholder = input.value === input.defaultValue ? key.dataset.savedPlaceholder : '';
    }
  });

  document.addEventListener('submit', async event => {
    const form = event.target;
    if (form.id !== 'engine-mode-form') return;
    event.preventDefault();
    if (form.getAttribute('aria-busy') === 'true') return;

    const address = form.elements.url;
    const key = form.elements.apiKey;
    const modes = [...form.querySelectorAll('input[name=mode]')];
    const button = form.querySelector('button[type=submit]');
    const status = form.querySelector('[data-engine-connecting]');
    clearMismatchedKey(form);
    const body = new URLSearchParams(new FormData(form));
    form.setAttribute('aria-busy', 'true');
    button.setAttribute('aria-disabled', 'true');
    address.readOnly = true;
    key.readOnly = true;
    for (const mode of modes) mode.disabled = true;
    status.textContent = status.dataset.engineConnecting;
    let message = form.dataset.failed;
    try {
      const response = await window.OieRuntime.request(form.action, {
        method: 'POST', body, headers: {Accept: 'application/json'},
      });
      if (response.status === 204) {
        clearKey(form);
        window.OieEvents?.suspend();
        location.replace('/');
        return;
      }
      const error = await response.text();
      if (error && response.headers.get('Content-Type')?.startsWith('text/plain')) message = error.trim();
    } catch {}
    if (!form.isConnected) return;
    button.removeAttribute('aria-disabled');
    address.readOnly = false;
    key.readOnly = false;
    for (const mode of modes) mode.disabled = false;
    form.removeAttribute('aria-busy');
    status.textContent = '';
    window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message}}));
  });
})();

window.OieRuntime.register('diagnostics', life => {
  let opening, viewer;
  const close = () => {
    opening = undefined;
    viewer?.close();
  };
  life.on(window, 'drawer:open', async event => {
    if (event.detail?.id !== 'diagnostics') return;
    const panel = document.querySelector('[data-diagnostics]');
    if (!panel) return;
    const pending = opening = {};
    const status = panel.querySelector('[data-diagnostics-status]');
    status.textContent = panel.dataset.loading;
    try {
      viewer = await import('/ui/diagnostics.js');
      if (opening === pending && panel.isConnected) viewer.open();
    } catch {
      if (opening === pending) status.textContent = panel.dataset.unavailable;
    }
  });
  for (const name of ['drawer:close', 'drawer:close-request']) {
    life.on(window, name, event => {
      if (event.detail?.id !== 'diagnostics') return;
      const pending = opening;
      queueMicrotask(() => {
        if (!event.defaultPrevented && opening === pending) close();
      });
    });
  }
  life.cleanup(close);
});

window.OieRuntime.register('audio', life => {
  const players = new Set();
  function release(player) {
    player.pause();
    if (!player.isConnected) player.removeAttribute('src');
    player.load();
    players.delete(player);
  }
  life.cleanup(() => { for (const player of players) release(player); });
  life.on(document, 'oie:timeline-content', () => {
    for (const player of players) if (!player.isConnected) release(player);
  });
  function recover(player) {
    if (!player.isConnected || player.hidden) return;
    const card = player.closest('[data-audio-id]');
    const retry = card.querySelector('[data-audio-retry]');
    const restoreFocus = document.activeElement === player || (document.activeElement === document.body && players.has(player));
    players.delete(player);
    player.hidden = true;
    if (retry) retry.hidden = false;
    if (restoreFocus) {
      const trigger = card.closest('[data-shared-item]')?.querySelector('[data-shared-menu-trigger]');
      (retry || trigger)?.focus({preventScroll:true});
    }
    window.dispatchEvent(new CustomEvent('notify', {detail:{kind:'toast', tone:'danger', message:card.dataset.audioError}}));
  }

  life.on(document, 'play', event => {
    const player = event.target;
    if (!player.matches?.('[data-audio-player]')) return;
    for (const previous of players) if (previous !== player) previous.pause();
    players.add(player);
  }, true);

  life.on(document, 'error', event => {
    if (event.target.matches?.('[data-audio-player]') && !event.target.closest('[data-waveform-player]')) recover(event.target);
  }, true);

  life.on(document, 'click', async event => {
    const action = event.target.closest('[id^="save-audio-"]');
    if (!action || action.disabled) return;
    const id = action.id.slice('save-audio-'.length);
    const card = action.closest('[data-message-bubble]')?.querySelector(`[data-audio-id="${CSS.escape(id)}"]`);
    if (!card?.dataset.audioDownload) return;
    const address = card.dataset.audioDownload, name = card.dataset.audioFilename;
    try {
      const opener = action.closest('[data-message-menu-slot]')?.querySelector('[data-message-menu-trigger]');
      await window.OieRuntime.saveFile(address, name, opener);
    } catch {
      if (!life.signal.aborted) window.dispatchEvent(new CustomEvent('notify', {detail:{kind:'toast', tone:'danger', message:card.dataset.audioDownloadFailed}}));
    }
  });

  life.on(document, 'click', event => {
    const retry = event.target.closest('[data-audio-retry],[id^="shared-retry-audio-"]');
    if (!retry) return;
    const inline = retry.hasAttribute('data-audio-retry');
    const owner = retry.closest(inline ? '[data-audio-id]' : '[data-shared-item]');
    const player = owner?.querySelector('[data-audio-player]');
    if (!player?.hidden) return;
    if (inline) retry.hidden = true;
    player.hidden = false;
    if (inline) player.focus({preventScroll:true});
    player.load();
    player.play().catch(error => { if (error.name !== 'AbortError') recover(player); });
  });
});

window.OieRuntime.register('engine-retention', life => {
  const states = new WeakMap();
  const snapshot = form => ({
    revision: form.elements.revision.value,
    room: Number(form.dataset.roomDays),
    direct: Number(form.dataset.directDays),
  });
  const stateFor = form => {
    if (!states.has(form)) {
      const saved = snapshot(form);
      states.set(form, {saved, observed: {...saved}, queued: new Map(), pending: null, uncertain: false, failed: false});
    }
    return states.get(form);
  };
  const statusFor = form => form.querySelector('[data-retention-status]');
  const modeFor = choice => choice.querySelector('[name$="-mode"]');
  const retryFor = form => form.querySelector('[data-retention-retry]');

  function choiceValue(choice) {
    if (modeFor(choice).value === 'unlimited') return 0;
    const input = choice.querySelector('input[type=number]');
    const days = input.valueAsNumber;
    return input.validity.valid && Number.isInteger(days) && days > 0 ? days : null;
  }

  function syncChoice(choice, state) {
    const key = choice.dataset.retentionChoice;
    const days = state.saved[key];
    if (state.uncertain || state.queued.has(key) || state.pending?.has(key) ||
        choiceValue(choice) !== state.observed[key] || state.observed[key] === days) return;
    const mode = modeFor(choice);
    if (days > 0) choice.querySelector('input[type=number]').value = days;
    mode.value = days === 0 ? 'unlimited' : 'finite';
    mode.dispatchEvent(new Event('change', {bubbles: true}));
    state.observed[key] = days;
  }

  function acceptSnapshot(form, state, saved) {
    state.saved = saved;
    for (const choice of form.querySelectorAll('[data-retention-choice]')) {
      if (!choice.contains(document.activeElement)) syncChoice(choice, state);
    }
  }

  async function save(form, state) {
    if (state.pending || state.failed || state.queued.size === 0 || !form.isConnected || life.signal.aborted) return;
    const changes = [...state.queued].filter(([key, days]) => state.uncertain || days !== state.saved[key]);
    state.queued.clear();
    if (changes.length === 0) {
      finish(form, '');
      return;
    }
    const desired = {...state.saved, ...Object.fromEntries(changes)};
    const body = new URLSearchParams({
      csrf: form.elements.csrf.value, engineId: form.elements.engineId.value, revision: desired.revision,
      'room-mode': desired.room === 0 ? 'unlimited' : 'finite', 'room-days': desired.room,
      'direct-mode': desired.direct === 0 ? 'unlimited' : 'finite', 'direct-days': desired.direct,
    });
    const status = statusFor(form);
    const retry = retryFor(form);
    state.pending = new Set(changes.map(([key]) => key));
    status.textContent = form.dataset.saving;
    retry.setAttribute('aria-disabled', 'true');
    let error = form.dataset.unavailable;
    let saved;
    try {
      const response = await life.fetch(form.action, {method: 'POST', body});
      if (response.ok || response.status === 409) {
        const doc = new DOMParser().parseFromString(await response.text(), 'text/html');
        const updated = doc.querySelector('[data-engine-retention]');
        if (updated?.elements.engineId.value === form.elements.engineId.value) {
          saved = snapshot(updated);
          // A response can be lost after the engine saved the change.
          if (response.ok || changes.every(([key, days]) => saved[key] === days)) error = '';
        }
        if (error && response.status === 409) error = form.dataset.conflict;
      } else if (response.status === 422) {
        error = form.dataset.invalid;
      } else if (response.status === 403) {
        error = form.dataset.forbidden;
      }
    } catch {}
    if (life.signal.aborted || !form.isConnected) return;

    state.pending = null;
    // A failed response without a snapshot may hide a successful write.
    state.uncertain = !saved;
    if (error) {
      for (const [key, days] of changes) {
        if (state.observed[key] === days && !state.queued.has(key)) state.queued.set(key, days);
      }
    }
    state.failed = error !== '' && state.queued.size > 0;
    if (saved) acceptSnapshot(form, state, saved);
    finish(form, state.failed ? error : '', state.failed);
    if (!state.failed) void save(form, state);
  }

  function finish(form, error, failed = false) {
    const status = statusFor(form);
    const retry = retryFor(form);
    status.textContent = error || (form.querySelector('[aria-invalid=true]') ? form.dataset.invalid : form.dataset.saved);
    if (!failed && document.activeElement === retry) status.focus({preventScroll:true});
    retry.hidden = !failed;
    retry.removeAttribute('aria-disabled');
  }

  life.on(document, 'focusout', event => {
    const field = event.target;
    const choice = field.closest('[data-retention-choice]');
    const form = choice?.closest('form[data-engine-retention]');
    if (!form) return;
    const input = choice.querySelector('input[type=number]');
    if (field !== input && choice.contains(event.relatedTarget)) return;
    const state = stateFor(form);
    const key = choice.dataset.retentionChoice;
    const days = choiceValue(choice);
    if (days === null) {
      if (field === input) {
        input.setAttribute('aria-invalid', 'true');
        statusFor(form).textContent = form.dataset.invalid;
      }
      state.queued.delete(key);
      delete state.observed[key];
      if (state.failed && state.queued.size === 0) {
        state.failed = false;
        finish(form, '');
      }
      return;
    }
    input.removeAttribute('aria-invalid');
    if (state.observed[key] === days) {
      // A concurrent update may have arrived while this unchanged choice had focus.
      if (!choice.contains(event.relatedTarget)) syncChoice(choice, state);
      return;
    }
    state.observed[key] = days;
    state.queued.set(key, days);
    void save(form, state);
  });

  life.on(document, 'click', event => {
    const retry = event.target.closest('[data-retention-retry]');
    if (!retry || retry.getAttribute('aria-disabled') === 'true') return;
    const form = retry.form;
    const state = stateFor(form);
    state.failed = false;
    void save(form, state);
  });

  life.on(document, 'submit', event => {
    if (event.target.hasAttribute('data-engine-retention')) event.preventDefault();
  });
});

window.OieRuntime.register('download-retention', life => {
  const section = document.querySelector('[data-download-retention]');
  if (!section) return;
  const {downloadRetention, engine, daysLabel, saved, cleared, failed} = section.dataset;
  const form = section.querySelector('[data-download-preferences]');
  const fields = form.querySelector('fieldset');
  const select = form.querySelector('select');
  const status = section.querySelector('[role="status"]');
  const dialog = document.getElementById('remove-device-downloads-dialog');
  const remove = form.querySelector('[data-remove-device-downloads]');
  const confirm = dialog.querySelector('[data-confirm-remove-downloads]');
  const headers = {'X-Oie-Engine-ID':engine};
  let busy = false;
  let confirmedDays = select.value;
  let queuedDays;

  async function update(action, days = confirmedDays) {
    if (busy) return;
    busy = true;
    form.setAttribute('aria-busy', 'true');
    for (const button of [remove, confirm]) button.setAttribute('aria-disabled', 'true');
    if (action) status.textContent = '';
    const options = {credentials:'same-origin', cache:'no-store', headers};
    if (action) {
      options.method = 'POST';
      options.body = new URLSearchParams({action, days});
      options.headers = {...headers, 'X-CSRF-Token':document.querySelector('meta[name="oie-csrf"]').content};
    }
    let applied = false;
    try {
      const response = await life.fetch(downloadRetention, options);
      if (!response.ok) throw Error('retention_unavailable');
      const policy = await response.json();
      if (life.signal.aborted) return;
      if (!Number.isInteger(policy.days) || policy.days < 0 || policy.days > 3650) throw Error('retention_unavailable');
      confirmedDays = String(policy.days);
      if (![...select.options].some(option => option.value === confirmedDays)) {
        select.add(new Option(daysLabel.replace('{days}', confirmedDays), confirmedDays));
      }
      applied = true;
      status.textContent = action === 'clear' ? cleared : action === 'save' ? saved : '';
      if (action) document.dispatchEvent(new Event('oie:refresh-request'));
    } catch {
      if (!life.signal.aborted) status.textContent = failed;
    } finally {
      busy = false;
      if (!life.signal.aborted) {
        fields.disabled = false;
        form.removeAttribute('aria-busy');
        for (const button of [remove, confirm]) button.removeAttribute('aria-disabled');
        const next = queuedDays;
        queuedDays = undefined;
        if (next !== undefined && (!applied || next !== confirmedDays)) {
          void update('save', next);
        } else {
          select.value = confirmedDays;
        }
      }
    }
  }

  life.on(window, 'drawer:open', event => { if (event.detail?.id === 'settings') update(); });
  life.on(select, 'change', () => {
    const days = select.value;
    if (busy) queuedDays = days;
    else void update('save', days);
  });
  const modal = type => window.dispatchEvent(new CustomEvent('modal:'+type, {detail:{id:dialog.id}}));
  life.on(remove, 'click', () => { if (!busy) modal('open'); });
  life.on(confirm, 'click', () => {
    if (busy) return;
    modal('close');
    update('clear');
  });
  life.raf(() => { if (section.closest('[role="dialog"]')?.getClientRects().length) update(); });
});

window.OieRuntime.register('access-keys', life => {
  const form = document.querySelector('[data-create-access-key]');
  if (!form) return;
  const name = form.elements.namedItem('name');
  const create = form.querySelector('button[type=submit]');
  const panel = form.parentElement;
  const result = panel.querySelector('[data-created-access-key]');
  const secret = result.querySelector('input');
  const copy = result.querySelector('button');
  let pending;
  const notify = (tone, message) => window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone, message}}));

  function clear() {
    pending?.abort();
    pending = undefined;
    secret.value = '';
    result.hidden = true;
    name.readOnly = false;
    create.removeAttribute('aria-disabled');
    form.removeAttribute('aria-busy');
  }

  for (const type of ['drawer:close', 'drawer:close-request']) {
    life.on(window, type, event => {
      if (event.detail?.id !== 'access-keys') return;
      queueMicrotask(() => {
        if (!event.defaultPrevented) clear();
      });
    });
  }
  life.on(window, 'pagehide', clear);
  life.cleanup(clear);
  life.on(form, 'submit', async event => {
    event.preventDefault();
    if (pending) return;
    const request = pending = new AbortController();
    const body = new URLSearchParams(new FormData(form));
    name.readOnly = true;
    create.setAttribute('aria-disabled', 'true');
    form.setAttribute('aria-busy', 'true');
    try {
      const response = await life.fetch(form.action, {method: 'POST', body, signal: request.signal});
      if (request.signal.aborted) return;
      if (response.status !== 201) {
        const reason = response.status === 400 ? await response.text() : form.dataset.failed;
        if (!request.signal.aborted) notify('danger', reason.trim());
        return;
      }
      const {key} = await response.json();
      if (request.signal.aborted) return;
      secret.value = key;
      result.hidden = false;
      copy.focus();
    } catch {
      if (!request.signal.aborted) notify('danger', form.dataset.failed);
    } finally {
      if (pending === request) {
        pending = undefined;
        name.readOnly = false;
        create.removeAttribute('aria-disabled');
        form.removeAttribute('aria-busy');
      }
    }
  });
  life.on(copy, 'click', async () => {
    const key = secret.value;
    try {
      await navigator.clipboard.writeText(key);
      if (key && secret.value === key) notify('success', copy.dataset.copied);
    } catch {
      if (secret.value) {
        secret.focus();
        secret.select();
      }
    }
  });
});

window.OieRuntime.register('global-read-receipts', life => {
  const section = document.getElementById('engine-read-receipts-settings');
  const page = section?.closest('[data-settings-page]');
  const dialog = section?.closest('[role=dialog]');
  if (!page || !dialog) return;
  const form = section.querySelector('[data-read-receipts-settings]');
  const loading = section.querySelector('[data-read-receipts-loading]');
  const error = section.querySelector('[data-read-receipts-load-error]');
  const refresh = () => section.dispatchEvent(new Event('oie:refresh-read-receipts'));
  const isRefresh = ctx => ctx?.target?.id === 'engine-read-receipts' && ctx.request.method.toUpperCase() === 'GET';
  let visible = Boolean(section.getClientRects().length);
  let attempted = false;
  const observer = new MutationObserver(() => {
    const showing = Boolean(section.getClientRects().length);
    if (showing && !visible && attempted) refresh();
    visible = showing;
  });
  observer.observe(page, { attributes: true, attributeFilter: ['hidden', 'style'] });
  observer.observe(dialog, { attributes: true, attributeFilter: ['hidden', 'style'] });
  life.cleanup(() => observer.disconnect());
  life.on(section.querySelector('[data-read-receipts-retry]'), 'click', refresh);
  life.on(section, 'htmx:before:request', event => {
    if (!isRefresh(event.detail.ctx)) return;
    attempted = true;
    form.hidden = true;
    loading.hidden = false;
    error.hidden = true;
  });
  life.on(section, 'htmx:after:request', event => {
    const ctx = event.detail.ctx;
    if (!isRefresh(ctx)) return;
    const fragment = new DOMParser().parseFromString(ctx.text || '', 'text/html').querySelector('#engine-read-receipts form');
    ctx.oieReadReceiptsReady = ctx.response?.status === 200 && Boolean(fragment);
    if (!ctx.oieReadReceiptsReady) event.preventDefault();
  });
  life.on(section, 'htmx:finally:request', event => {
    const ctx = event.detail.ctx;
    if (!isRefresh(ctx)) return;
    loading.hidden = true;
    form.hidden = !ctx.oieReadReceiptsReady;
    error.hidden = Boolean(ctx.oieReadReceiptsReady);
  });
});

window.OieRuntime.register('conversation-read-receipt-settings', life => {
  life.on(document, 'htmx:after:request', event => {
    const ctx = event.detail.ctx;
    const target = ctx?.target;
    if (target?.id !== 'conversation-read-receipts') return;
    const chat = new URL(ctx.request.action, location.href).searchParams.get('chat');
    if (!target.isConnected || target.dataset.readReceiptsConversation !== chat) {
      event.preventDefault();
      return;
    }
    if (ctx.request.method.toUpperCase() !== 'GET') return;
    const fragment = new DOMParser().parseFromString(ctx.text || '', 'text/html').getElementById(target.id);
    if (fragment?.dataset.readReceiptsConversation !== chat) {
      event.preventDefault();
      target.textContent = target.dataset.unavailable;
    }
  });
});

// A docked drawer shares the workspace with the chat. Keep one destination open
// and return keyboard focus when its own close control dismisses it.
window.OieRuntime.register('workspace-drawers', life => {
  let current;
  let opener;
  life.on(window, 'drawer:open', event => {
    const id = event.detail?.id;
    const panel = document.getElementById(id + '-body')?.closest('.oie-drawer');
    if (!panel || current === id) return;
    if (current) window.dispatchEvent(new CustomEvent('drawer:close', {detail: {id: current}}));
    current = id;
    opener = document.activeElement;
    const origin = opener;
    if (panel.getAttribute('aria-modal') === 'false') window.Alpine.nextTick(() => {
      life.raf(() => life.raf(() => {
        if (current !== id || !panel.getClientRects().length || document.activeElement !== origin) return;
        const title = panel.querySelector('header h2');
        title.tabIndex = -1;
        title.focus({preventScroll: true});
      }));
    });
  });
  for (const type of ['drawer:close', 'drawer:close-request']) {
    life.on(window, type, event => {
      const id = event.detail?.id;
      if (!current || current !== id || event.defaultPrevented) return;
      const panel = document.getElementById(id + '-body')?.closest('.oie-drawer');
      const origin = opener;
      current = undefined;
      if (type === 'drawer:close-request' && panel?.getAttribute('aria-modal') === 'false' && panel.contains(document.activeElement)) {
        life.raf(() => { if (!current && origin?.isConnected) origin.focus({preventScroll: true}); });
      }
    });
  }
});

// Subpages can be replaced independently by HTMX. Keep one focusable Back control
// in the header and resolve its destination from the currently visible subpage.
window.OieRuntime.register('drawer-back', life => {
  let panel, body, leading, frame;
  const origins = new Map();
  const observer = new MutationObserver(schedule);

  function destination() {
    if (!body) return;
    return Array.from(body.querySelectorAll('template[data-drawer-return]')).reverse()
      .find(marker => !marker.hidden && marker.parentElement.getClientRects().length);
  }

  function sync() {
    frame = 0;
    if (!leading) return;
    const marker = destination();
    leading.hidden = !marker;
    if (!marker) return;
    const button = leading.querySelector('[data-drawer-back]');
    const label = marker.dataset.drawerReturn;
    if (button.getAttribute('aria-label') !== label) {
      button.setAttribute('aria-label', label);
      document.getElementById(body.id.replace(/-body$/, '-back-tip')).textContent = label;
    }
  }

  function schedule() {
    if (!frame) frame = life.raf(sync);
  }

  function watch(nextBody) {
    observer.disconnect();
    body = nextBody;
    panel = body?.closest('.oie-drawer');
    leading = panel?.querySelector('header [data-drawer-leading]');
    if (!leading) return;
    observer.observe(panel, {attributes:true, attributeFilter:['style','hidden']});
    observer.observe(body, {subtree:true, childList:true, attributes:true, attributeFilter:['hidden','style','aria-selected']});
    schedule();
  }

  life.on(window, 'drawer:open', event => watch(document.getElementById(event.detail?.id + '-body')));
  life.on(window, 'drawer:close', event => {
    if (body?.id === event.detail?.id + '-body') watch(null);
  });
  life.on(document, 'oie:route-ready', () => {
    const visible = [...document.querySelectorAll('[data-drawer-leading]')].find(node => node.closest('.oie-drawer').getClientRects().length);
    if (visible) watch(visible.closest('.oie-drawer').querySelector(':scope > [id$="-body"]'));
  });
  life.cleanup(() => observer.disconnect());

  life.on(document, 'click', event => {
    const entry = event.target.closest('[data-drawer-child]');
    if (!entry) return;
    const scroll = entry.closest('.oie-settings-scroll, [id$="-body"]');
    origins.set(entry.dataset.drawerChild, {entry, scroll, top:scroll?.scrollTop});
  }, true);

  life.on(document, 'click', event => {
    if (!leading?.contains(event.target.closest('[data-drawer-back]'))) return;
    const marker = destination();
    if (!marker) return;
    if (marker.dataset.returnHref) {
      window.OieNavigation.go(marker.dataset.returnHref, {workspace:'chat-info-content'});
    } else if (marker.dataset.returnParent) {
      const child = body.id.slice(0, -5);
      const parent = marker.dataset.returnParent;
      const origin = origins.get(child);
      window.dispatchEvent(new CustomEvent('drawer:close', {detail:{id:child}}));
      queueMicrotask(() => {
        window.dispatchEvent(new CustomEvent('drawer:open', {detail:{id:parent}}));
        if (origin) life.raf(() => life.raf(() => {
          if (!origin.entry.isConnected || !origin.entry.getClientRects().length) return;
          if (origin.scroll) origin.scroll.scrollTop = origin.top;
          origin.entry.focus({preventScroll:true});
        }));
      });
    } else {
      marker.dispatchEvent(new CustomEvent('oie:drawer-back', {bubbles:true}));
    }
  });
});

window.OieRuntime.register('settings-pages', life => {
  const stack = document.querySelector('[data-settings-stack]');
  if (!stack) return;
  const pages = [...stack.querySelectorAll('[data-settings-page]')];
  const heading = document.getElementById('settingsTitle');
  const footer = document.querySelector('#settings-body .oie-engine-sign-out');
  const scroll = stack.closest('.oie-settings-scroll');
  const positions = new Map();
  let current = 'general';
  function show(id, focus) {
    const next = pages.find(page => page.dataset.settingsPage === id) || pages[0];
    const previous = current;
    positions.set(previous, scroll.scrollTop);
    current = next.dataset.settingsPage;
    for (const page of pages) page.hidden = page !== next;
    if (footer) footer.hidden = current !== 'general';
    heading.textContent = next.dataset.title || stack.dataset.rootTitle;
    const returnTo = next.querySelector('[data-settings-target="' + previous + '"]');
    scroll.scrollTop = returnTo ? positions.get(current) || 0 : 0;
    if (focus) {
      heading.tabIndex = -1;
      (returnTo || heading).focus({preventScroll:true});
    }
    if (previous === 'engine' && current !== 'engine') {
      const form = stack.querySelector('#engine-mode-form');
      if (form) { form.elements.apiKey.value = ''; form.elements.apiKeyAddress.value = ''; }
    }
  }
  function navigate(page) {
    show(page, true);
    document.dispatchEvent(new CustomEvent('oie:settings-navigate', {detail:{page:current}}));
  }
  life.on(stack, 'click', event => {
    const target = event.target.closest('[data-settings-target]');
    if (target) navigate(target.dataset.settingsTarget);
  });
  life.on(stack, 'oie:drawer-back', event => navigate(event.target.dataset.returnPage));
  life.on(window,'drawer:open',event => {
    if (event.detail?.id === 'settings') show(event.detail.page || new URL(location.href).searchParams.get('settingsTab') || 'general', Boolean(event.detail.focus));
  });
  show(stack.dataset.startPage,false);
});

// A finite conversation override needs a deliberate day count before submission.
window.OieRuntime.register('conversation-retention', life => {
  life.on(document,'change',event => {
    const field = event.target;
    const form = field.closest('[data-conversation-retention-form]');
    if (!form) return;
    const days = form.elements.namedItem('override-days');
    if (field.name === 'override-mode' && field.value === 'finite') {
      days.value = '';
      queueMicrotask(() => days.focus());
      return;
    }
    if (field.name !== 'override-mode' && field !== days) return;
    queueMicrotask(() => { if (form.isConnected && form.reportValidity()) form.requestSubmit(); });
  });
});
