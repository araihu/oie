window.OieRuntime.register('room-management', life => {
  const root = document.querySelector('[data-room-management]');
  if (!root) return;
  const content = root.querySelector('[data-room-management-content]');
  const status = root.querySelector('[data-room-management-status]');
  const refresh = root.querySelector('[data-room-management-refresh]');
  let request, opener, message = '', affiliation = '', active = false;
  const pending = new WeakMap();
  const drafts = new Map();

  async function load(replace = true) {
    request?.abort();
    const current = new AbortController();
    request = current;
    status.textContent = root.dataset.loading;
    root.setAttribute('aria-busy', 'true');
    const {chat, account, engine, lang} = root.dataset;
    const params = new URLSearchParams({chat, account, engine, lang, message, affiliation});
    try {
      const response = await life.fetch('/ui/rooms/manage-fragment?' + params, {signal: current.signal});
      if (!response.ok) throw new Error();
      const markup = await response.text();
      if (request !== current || !active || life.signal.aborted) return;
      if (replace) {
        for (const form of content.querySelectorAll('form')) {
          if (form.dataset.busy === 'true' || pending.has(form)) {
            status.textContent = '';
            return;
          }
          drafts.set(identity(form), new FormData(form));
        }
        content.innerHTML = markup;
        for (const form of content.querySelectorAll('form')) {
          const draft = drafts.get(identity(form));
          if (!draft) continue;
          for (const name of ['target','value','reason']) {
            const field = form.elements.namedItem(name);
            if (field && field.type !== 'hidden' && draft.has(name)) {
              field.value = draft.get(name);
              field.dispatchEvent(new Event('input', {bubbles:true}));
            }
          }
        }
      } else {
        const template = document.createElement('template');
        template.innerHTML = markup;
        const revision = template.content.querySelector('[name=expectedRevision]')?.value;
        if (!revision) {
          content.innerHTML = markup;
        } else {
          const fresh = new Set([...template.content.querySelectorAll('form')].map(identity));
          for (const form of content.querySelectorAll('form')) {
            if (pending.has(form) || form.dataset.busy === 'true') continue;
            if (!fresh.has(identity(form))) {
              const submit = form.querySelector('[type=submit]');
              if (submit) submit.disabled = true;
              form.dataset.permissionLost = 'true';
              continue;
            }
            if (form.dataset.permissionLost === 'true') {
              const submit = form.querySelector('[type=submit]');
              if (submit) submit.disabled = false;
              delete form.dataset.permissionLost;
            }
            form.elements.expectedRevision.value = revision;
          }
        }
      }
      status.textContent = '';
    } catch {
      if (request === current && active && !life.signal.aborted) status.textContent = root.dataset.unavailable;
    } finally {
      if (request === current) root.removeAttribute('aria-busy');
    }
  }

  function identity(form) {
    const target = form.elements.namedItem('target');
    return JSON.stringify([form.elements.managementAction.value, target?.type === 'hidden' ? target.value : '',
      form.elements.namedItem('messageId')?.value || '']);
  }

  life.on(document, 'click', event => {
    const trigger = event.target.closest('#chat-manage-room,[id^=moderate-room-message-],[data-room-management-refresh],[data-room-affiliation-list]');
    if (!trigger) return;
    if (trigger === refresh || trigger.hasAttribute('data-room-affiliation-list')) {
      if ([...content.querySelectorAll('form')].some(form => pending.has(form) || form.dataset.busy === 'true')) return;
      affiliation = trigger.dataset.roomAffiliationList || affiliation;
      void load(trigger !== refresh || !content.querySelector('form'));
      return;
    }
    opener = trigger.closest('[data-message-id]')?.querySelector('[data-message-menu-trigger]') ||
      document.querySelector('#chat-options [aria-controls="chat-options-panel"]');
    const menu = trigger.closest('[role=menu]');
    if (opener?.getAttribute('aria-expanded') === 'true') opener.click();
    const open = () => {
      if (!root.isConnected) return;
      if (menu?.isConnected && menu.getClientRects().length && opener?.getAttribute('aria-expanded') === 'false') {
        life.raf(open);
        return;
      }
      window.dispatchEvent(new CustomEvent('drawer:open', {detail: {id: 'room-management'}}));
      void load();
    };
    message = trigger.id.startsWith('moderate-room-message-') ? trigger.id.slice('moderate-room-message-'.length) : '';
    affiliation = '';
    active = true;
    if (message !== root.dataset.message) {
      if ([...content.querySelectorAll('form')].some(form => pending.has(form) || form.dataset.busy === 'true')) {
        message = root.dataset.message || '';
        life.raf(open);
        return;
      }
      drafts.clear();
      content.replaceChildren();
    }
    root.dataset.message = message;
    life.raf(open);
  });

  life.on(window, 'drawer:close-request', event => {
    if (event.detail?.id !== 'room-management') return;
    active = false;
    request?.abort();
    life.timeout(() => opener?.isConnected && opener.focus({preventScroll: true}), 220);
  });

  life.on(document, 'oie:room-management-result', event => {
    const {form, result} = event.detail;
    if (!active || !root.contains(form)) return;
    if (!result.ok && !result.rejected) {
      pending.set(form, true);
      for (const input of form.querySelectorAll('input:not([type=hidden]),textarea')) input.readOnly = true;
      return;
    }
    pending.delete(form);
    for (const input of form.querySelectorAll('input:not([type=hidden]),textarea')) input.readOnly = false;
    life.timeout(() => { if (active) void load(false); }, 0);
  });
});
