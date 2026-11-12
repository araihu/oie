window.OieRuntime.register('room-entry', life => {
  'use strict';
  const join = document.querySelector('#join-room-form');
  const preview = join?.querySelector('[data-room-preview]');
  let request;
  let lastTrigger;
  let activeDrawer;
  let openedStatus = false;
  const drawer = (type, id) => window.dispatchEvent(new CustomEvent('drawer:' + type, {detail: {id}}));

  function clearPreview() {
    request?.abort();
    request = undefined;
    if (!preview) return;
    preview.hidden = true;
    preview.textContent = '';
  }

  async function showPreview() {
    clearPreview();
    const {address, accountId, engineId, csrf, lang} = join.elements;
    if (!address.value.trim()) {
      address.reportValidity();
      return;
    }
    if (!accountId.value) return;
    const current = new AbortController();
    request = current;
    preview.hidden = false;
    preview.textContent = preview.dataset.loading;
    const body = new URLSearchParams({
      address: address.value, accountId: accountId.value, engineId: engineId.value,
      csrf: csrf.value, lang: lang.value
    });
    try {
      const response = await life.fetch('/ui/rooms/preview', {method: 'POST', body, signal: current.signal});
      if (request !== current || life.signal.aborted) return;
      if (!response.ok) {
        preview.textContent = response.status === 400 ? preview.dataset.invalid : preview.dataset.unavailable;
        return;
      }
      const result = await response.text();
      if (request === current && !life.signal.aborted) preview.innerHTML = result;
    } catch {
      if (request === current && !life.signal.aborted) preview.textContent = preview.dataset.unavailable;
    }
  }

  function openDrawer(id, trigger, address) {
    activeDrawer = id;
    if (trigger) lastTrigger = trigger;
    if (address && join) {
      join.elements.address.value = address;
      const currentAccount = document.body.dataset.accountId;
      if (join.dataset.roomAccountIds.split(' ').includes(currentAccount)) {
        const account = join.elements.accountId;
        account.value = currentAccount;
        account.dispatchEvent(new Event('input', {bubbles: true}));
      }
      void showPreview();
    }
    drawer('open', id);
  }

  life.on(document, 'click', event => {
    const trigger = event.target.closest('[data-room-invitation],[data-room-entry-open]');
    if (!trigger || event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    const id = trigger.dataset.roomEntryOpen || 'join-room';
    if (!document.getElementById(id + '-body')) return;
    event.preventDefault();
    if (trigger.dataset.roomEntryOpen) drawer('close', 'newchat');
    openDrawer(id, trigger, trigger.dataset.roomInvitation);
  });

  if (join) {
    life.on(join, 'click', event => {
      if (event.target.closest('[data-load-room-preview]')) void showPreview();
    });
    life.on(join.elements.address, 'input', clearPreview);
    life.on(join.elements.accountId, 'change', () => {
      if (join.elements.address.value.trim() && !preview.hidden) void showPreview();
    });
  }

  function restoreFocus(event) {
    if (!activeDrawer || event.detail?.id !== activeDrawer) return;
    activeDrawer = undefined;
    if (request && !request.signal.aborted && preview?.textContent === preview.dataset.loading) clearPreview();
    const trigger = lastTrigger?.matches('[data-room-entry-open]') ? document.querySelector('[data-new-conversation]') : lastTrigger;
    life.timeout(() => trigger?.isConnected && trigger.focus({preventScroll: true}), 220);
  }
  life.on(window, 'drawer:close-request', restoreFocus);

  const target = new URLSearchParams(location.search).get('open') || document.body.dataset.openDrawer;
  const showInitial = () => {
    if (target === 'join-room' || target === 'create-room') {
      openDrawer(target);
      if (target === 'join-room' && join?.elements.address.value && new URLSearchParams(location.search).has('room')) void showPreview();
    } else if (target === 'room-session' && document.querySelector('#room-session-status:not([data-room-state="joined"])')) {
      openedStatus = true;
      window.dispatchEvent(new CustomEvent('modal:open', {detail: {id: 'room-session-dialog'}}));
    }
  };
  if (window.Alpine) life.timeout(showInitial, 0);
  else life.on(document, 'alpine:initialized', showInitial, {once: true});

  life.on(document, 'oie:room-status-updated', () => {
    if (!openedStatus || !document.querySelector('#room-session-status[data-room-state="joined"]')) return;
    openedStatus = false;
    window.dispatchEvent(new CustomEvent('modal:close', {detail: {id: 'room-session-dialog'}}));
  });
});
