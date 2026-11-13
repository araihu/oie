window.OieRuntime.register('room-management', life => {
  const root = document.querySelector('[data-room-management]');
  if (!root) return;
  const content = root.querySelector('[data-room-management-content]');
  const status = root.querySelector('[data-room-management-status]');
  const refresh = root.querySelector('[data-room-management-refresh]');
  let request, opener, message = '', affiliation = '', invite = false, active = false;
  const pending = new WeakMap();
  const title = document.getElementById("room-managementTitle");
  const drafts = new Map();

  async function load(replace = true) {
    request?.abort();
    const current = new AbortController();
    request = current;
    status.textContent = root.dataset.loading;
    root.setAttribute('aria-busy', 'true');
    const {chat, account, engine, lang} = root.dataset;
    const params = new URLSearchParams({chat, account, engine, lang, message, affiliation, invite});
    try {
      const response = await life.fetch('/ui/rooms/manage-fragment?' + params, {signal: current.signal});
      if (!response.ok) throw new Error();
      const markup = await response.text();
      if (request !== current || !active || life.signal.aborted) return;
      const template = document.createElement('template');
      template.innerHTML = markup;
      if (replace) {
        for (const form of content.querySelectorAll('form')) {
          if (form.dataset.busy === 'true' || pending.has(form)) {
            status.textContent = '';
            return;
          }
          drafts.set(identity(form), new FormData(form));
        }
        render(template);
        for (const form of content.querySelectorAll('form')) {
          const draft = drafts.get(identity(form));
          if (!draft) continue;
          for (const name of ['target','value','reason','name','topic']) {
            const field = form.elements.namedItem(name);
            const original = name === 'name' ? 'originalName' : name === 'topic' ? 'originalTopic' : '';
            if (field && field.type !== 'hidden' && draft.has(name) &&
                (!original || draft.get(name) !== draft.get(original))) {
              field.value = draft.get(name);
              field.dispatchEvent(new Event('input', {bubbles:true}));
            }
          }
        }
      } else {
        const revision = template.content.querySelector('[name=expectedRevision]')?.value;
        if (!revision) {
          render(template);
        } else {
          const fresh = new Map([...template.content.querySelectorAll('form')].map(form => [identity(form), form]));
          for (const form of content.querySelectorAll('form')) {
            if (pending.has(form) || form.dataset.busy === 'true') continue;
            const freshForm = fresh.get(identity(form));
            const permissionChanged = form.elements.managementAction.value === 'update_details' &&
              ['name','topic'].some(name => !!form.elements.namedItem(name) !== !!freshForm?.elements.namedItem(name));
            if (!freshForm || permissionChanged) {
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

  function render(template) {
    const currentTabs = content.querySelector('.oie-room-management-tabs');
    const incomingTabs = template.content.querySelector('.oie-room-management-tabs');
    if (currentTabs && incomingTabs) {
      const focusedList = document.activeElement?.dataset.roomAffiliationList;
      for (const panel of currentTabs.querySelectorAll('[role=tabpanel]')) {
        const incoming = template.content.getElementById(panel.id);
        panel.replaceChildren(...incoming.childNodes);
      }
      if (focusedList) {
        currentTabs.querySelector(`[data-room-affiliation-list="${CSS.escape(focusedList)}"]`)?.focus({preventScroll:true});
      }
    } else {
      content.replaceChildren(template.content);
    }
    document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail: {root: content}}));
  }

  function identity(form) {
    const target = form.elements.namedItem('target');
    return JSON.stringify([form.elements.managementAction.value, target?.type === 'hidden' ? target.value : '',
      form.elements.namedItem('messageId')?.value || '']);
  }

  life.on(document, 'click', event => {
    const trigger = event.target.closest('#chat-manage-room,#chat-invite-room,[id^=moderate-room-message-],[data-room-management-refresh],[data-room-affiliation-list]');
    if (!trigger) return;
    if (trigger === refresh || trigger.hasAttribute('data-room-affiliation-list')) {
      if ([...content.querySelectorAll('form')].some(form => pending.has(form) || form.dataset.busy === 'true')) return;
      affiliation = trigger.dataset.roomAffiliationList || affiliation;
      void load();
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
    const nextMessage = trigger.id.startsWith('moderate-room-message-') ? trigger.id.slice('moderate-room-message-'.length) : '';
    const nextInvite = trigger.id === 'chat-invite-room';
    affiliation = '';
    active = true;
    if (nextMessage !== message || nextInvite !== invite) {
      if ([...content.querySelectorAll('form')].some(form => pending.has(form) || form.dataset.busy === 'true')) {
        life.raf(open);
        return;
      }
      for (const form of content.querySelectorAll('form')) drafts.set(identity(form), new FormData(form));
      content.replaceChildren();
    }
    message = nextMessage;
    invite = nextInvite;
    if (title) title.textContent = invite ? root.dataset.inviteTitle : root.dataset.manageTitle;
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
    if (form.elements.managementAction.value === 'update_details') {
      for (const [name, original] of [['name','originalName'],['topic','originalTopic']]) {
        const field = form.elements.namedItem(name);
        const baseline = form.elements.namedItem(original);
        if (field && baseline && (result.ok || name === 'name' && result.state === 'name_confirmed')) baseline.value = field.value;
      }
    }
    if (result.ok && form.elements.managementAction.value === 'invite') {
      form.elements.target.value = '';
      form.elements.reason.value = '';
    }
    for (const input of form.querySelectorAll('input:not([type=hidden]),textarea')) input.readOnly = false;
    life.timeout(() => { if (active) void load(false); }, 0);
  });
});
