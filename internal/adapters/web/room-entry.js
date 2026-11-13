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
      if (request !== current || life.signal.aborted) return;
      preview.innerHTML = result;
      const summary = preview.querySelector('template[data-room-preview-summary]');
      if (summary) {
        document.dispatchEvent(new CustomEvent('oie:room-preview-resolved', {
          detail: {address: summary.dataset.roomPreviewSummary, accountId: body.get('accountId'), summary: summary.innerHTML}
        }));
      }
    } catch {
      if (request === current && !life.signal.aborted) preview.textContent = preview.dataset.unavailable;
    }
  }

  function openDrawer(id, trigger, address) {
    activeDrawer = id;
    if (trigger) lastTrigger = trigger;
    if (address && join && join.dataset.busy !== 'true') {
      const currentAccount = document.body.dataset.accountId;
      const account = join.elements.accountId;
      const priorAddress = join.elements.address.value;
      const recovering = join.dataset.roomEntryPending === 'true' && priorAddress === address;
      let selected = account.value;
      if (!recovering && join.dataset.roomAccountIds.split(' ').includes(currentAccount)) selected = currentAccount;
      clearPreview();
      document.dispatchEvent(new CustomEvent('oie:room-entry-target', {detail: {form: join, account: selected, address}}));
      if (join.dataset.roomEntryPending !== 'true') {
        join.elements.address.value = address;
        account.value = selected;
        account.dispatchEvent(new Event('input', {bubbles: true}));
        void showPreview();
      }
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

window.OieRuntime.register('room-invitations', life => {
  'use strict';
  const timeline = document.querySelector('#message-timeline');
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content;
  const {engineId, accountId} = document.body.dataset;
  if (!timeline || !csrf || !engineId || !accountId || typeof IntersectionObserver !== 'function') return;

  const visible = new Set();
  const requests = new Map();
  const resolved = new Map();
  const applied = new WeakSet();
  const watched = new WeakSet();
  let active = 0;

  async function load(address) {
    const body = new URLSearchParams({
      address, accountId, engineId, csrf, lang: document.documentElement.lang, presentation: 'message'
    });
    try {
      const response = await life.fetch('/ui/rooms/preview', {method: 'POST', body});
      return response.status === 200 ? await response.text() : '';
    } catch {
      return '';
    }
  }

  function apply(card, html) {
    const summary = card.querySelector('[data-room-bubble-summary]');
    if (html && summary && timeline.contains(card) && !life.signal.aborted) summary.innerHTML = html;
  }

  life.on(document, 'oie:room-preview-resolved', event => {
    const {address, accountId: selectedAccount, summary} = event.detail;
    if (selectedAccount !== accountId) return;
    if (resolved.size >= 12) resolved.delete(resolved.keys().next().value);
    resolved.set(address, summary);
    for (const card of timeline.querySelectorAll('[data-room-bubble]')) {
      if (card.dataset.roomBubble === address && card.dataset.roomBubbleAccount === accountId) {
        apply(card, summary);
        card.dataset.roomBubbleReady = 'true';
      }
    }
  });

  function pump() {
    if (life.signal.aborted) return;
    for (const card of visible) {
      if (!timeline.contains(card) || applied.has(card)) {
        visible.delete(card);
        continue;
      }
      const address = card.dataset.roomBubble;
      let pending = resolved.has(address) ? Promise.resolve(resolved.get(address)) : requests.get(address);
      if (!pending) {
        if (active >= 2 || requests.size >= 12) continue;
        active++;
        pending = load(address).finally(() => {
          active--;
          pump();
        });
        requests.set(address, pending);
      }
      applied.add(card);
      visible.delete(card);
      observer.unobserve(card);
      void pending.then(html => {
        if (!html || card.dataset.roomBubbleReady === 'true' || life.signal.aborted) return;
        apply(card, html);
        card.dataset.roomBubbleReady = 'true';
      });
    }
  }

  const observer = new IntersectionObserver(entries => {
    for (const {target, isIntersecting} of entries) {
      if (isIntersecting) visible.add(target);
      else visible.delete(target);
    }
    pump();
  }, {root: timeline, threshold: 0.01});

  function watch(card) {
    if (card.dataset.roomBubbleAccount !== accountId || watched.has(card)) return;
    watched.add(card);
    observer.observe(card);
  }

  const changes = new MutationObserver(records => {
    for (const record of records) {
      for (const node of record.addedNodes) {
        if (node.nodeType !== Node.ELEMENT_NODE) continue;
        if (node.matches('[data-room-bubble]')) watch(node);
        else node.querySelectorAll('[data-room-bubble]').forEach(watch);
      }
      for (const node of record.removedNodes) {
        if (node.nodeType !== Node.ELEMENT_NODE || timeline.contains(node)) continue;
        const forget = card => {
          visible.delete(card);
          watched.delete(card);
          observer.unobserve(card);
        };
        if (node.matches('[data-room-bubble]')) forget(node);
        else node.querySelectorAll('[data-room-bubble]').forEach(forget);
      }
    }
  });
  timeline.querySelectorAll('[data-room-bubble]').forEach(watch);
  changes.observe(timeline, {childList: true, subtree: true});
  life.cleanup(() => {
    changes.disconnect();
    observer.disconnect();
  });
});
