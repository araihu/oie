window.OieRuntime.register('actions', life => {
 'use strict';
 function initializeSourceMenu(event){
  const trigger=event.target.closest?.('[data-message-menu-trigger]');
  const slot=trigger?.closest('[data-source-menu-deferred]');
  if(!slot||!window.Alpine)return;
  // The same input event continues into the now-initialized Goshtoso menu.
  window.Alpine.mutateDom(()=>{
   window.Alpine.destroyTree(slot);
   slot.removeAttribute('x-ignore');
   slot.removeAttribute('data-source-menu-deferred');
   window.Alpine.initTree(slot);
  });
 }
 life.on(document,'click',initializeSourceMenu,true);
 life.on(document,'keydown',event=>{
  if(['Enter',' ','ArrowDown'].includes(event.key))initializeSourceMenu(event);
 },true);
 // Public Dropdown item IDs are application hooks. Escape uses its supported
 // keyboard lifecycle to dismiss and restore focus after native actions.
 life.on(document,'click',event=>{
  const item=event.target.closest('[id^="copy-message-"],[id^="reply-message-"],[id^="star-message-"],[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"],[id^="refresh-preview-"]');
  if(item&&!item.disabled)item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
 },true);
});

(() => {
  'use strict';

  document.addEventListener('submit', async event => {
    const form = event.target;
    if (form.id !== 'engine-mode-form') return;
    event.preventDefault();
    if (form.getAttribute('aria-busy') === 'true') return;

    const address = form.elements.url;
    const modes = [...form.querySelectorAll('input[name=mode]')];
    const button = form.querySelector('button[type=submit]');
    const status = form.querySelector('[data-engine-connecting]');
    const body = new URLSearchParams(new FormData(form));
    form.setAttribute('aria-busy', 'true');
    button.setAttribute('aria-disabled', 'true');
    address.readOnly = true;
    for (const mode of modes) mode.disabled = true;
    status.textContent = status.dataset.engineConnecting;
    let message = form.dataset.failed;
    try {
      const response = await fetch(form.action, {
        method: 'POST', body, headers: {Accept: 'application/json'},
      });
      if (response.status === 204) {
        window.OieEvents?.stop();
        location.replace('/');
        return;
      }
      const error = await response.text();
      if (error && response.headers.get('Content-Type')?.startsWith('text/plain')) message = error.trim();
    } catch {}
    if (!form.isConnected) return;
    button.removeAttribute('aria-disabled');
    address.readOnly = false;
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
    player.removeAttribute('src');
    player.load();
    players.delete(player);
  }
  life.cleanup(() => { for (const player of players) release(player); });
  life.on(document, 'oie:timeline-content', () => {
    for (const player of players) if (!player.isConnected) release(player);
  });
  function recover(player) {
    if (!player.isConnected || player.hidden) return;
    const card = player.closest('[data-audio-source]');
    const play = card.querySelector('[data-audio-play]');
    player.hidden = true;
    release(player);
    play.hidden = false;
    if (document.activeElement === player || document.activeElement === document.body) play.focus({preventScroll:true});
    window.dispatchEvent(new CustomEvent('notify', {detail:{kind:'toast', tone:'danger', message:card.dataset.audioError}}));
  }

  life.on(document, 'error', event => {
    if (event.target.matches?.('[data-audio-player]')) recover(event.target);
  }, true);

  life.on(document, 'click', event => {
    const play = event.target.closest('[data-audio-play]');
    if (!play) return;
    const card = play.closest('[data-audio-source]');
    const player = card.querySelector('[data-audio-player]');
    if (!card.dataset.audioSource) return;
    for (const previous of players) previous.pause();
    players.add(player);
    player.src = card.dataset.audioSource;
    play.hidden = true;
    player.hidden = false;
    player.focus({preventScroll:true});
    player.play().catch(error => { if (error.name !== 'AbortError') recover(player); });
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
  const headers = {'X-Oie-Engine-ID':engine};
  let busy = false;

  async function update(action) {
    if (busy) return;
    busy = true;
    form.setAttribute('aria-busy', 'true');
    const options = {credentials:'same-origin', cache:'no-store', headers};
    if (action) {
      options.method = 'POST';
      options.body = new URLSearchParams({action, days:select.value});
      options.headers = {...headers, 'X-CSRF-Token':document.querySelector('meta[name="oie-csrf"]').content};
    }
    try {
      const response = await life.fetch(downloadRetention, options);
      if (!response.ok) throw Error('retention_unavailable');
      const policy = await response.json();
      if (life.signal.aborted) return;
      if (!Number.isInteger(policy.days) || policy.days < 0 || policy.days > 3650) throw Error('retention_unavailable');
      const days = String(policy.days);
      if (![...select.options].some(option => option.value === days)) select.add(new Option(daysLabel.replace('{days}', days), days));
      select.value = days;
      fields.disabled = false;
      status.textContent = action === 'clear' ? cleared : action === 'save' ? saved : '';
    } catch {
      if (!life.signal.aborted) {
        fields.disabled = false;
        status.textContent = failed;
      }
    } finally {
      if (!life.signal.aborted) form.removeAttribute('aria-busy');
      busy = false;
    }
  }

  life.on(window, 'drawer:open', event => { if (event.detail?.id === 'settings') update(); });
  life.on(form.querySelector('[data-save-download-retention]'), 'click', () => update('save'));
  const modal = type => window.dispatchEvent(new CustomEvent('modal:'+type, {detail:{id:dialog.id}}));
  life.on(remove, 'click', () => modal('open'));
  life.on(dialog.querySelector('[data-confirm-remove-downloads]'), 'click', () => {
    modal('close');
    update('clear');
  });
  life.raf(() => { if (section.closest('[role="dialog"]')?.getClientRects().length) update(); });
});
