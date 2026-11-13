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
  const openForms = new Set();
  const isPending = form => form.dataset.busy === 'true' || pending.has(form);
  const hasPending = () => [...content.querySelectorAll('form')].some(isPending);
  let accessList = 'member';

  function updateAvatarActions() {
    const remove = content.querySelector('#room-remove-avatar');
    if (!remove) return;
    const avatar = content.querySelector('.oie-room-management-avatar [data-avatar-src]');
    remove.hidden = !avatar?.dataset.avatarSrc;
  }
  const avatarChanges = new MutationObserver(updateAvatarActions);
  avatarChanges.observe(content, {subtree: true, childList: true, attributes: true, attributeFilter: ['data-avatar-src']});
  life.cleanup(() => avatarChanges.disconnect());

  async function load(replace = true, announcement = '') {
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
          if (isPending(form)) {
            status.textContent = '';
            return;
          }
          remember(form);
        }
        render(template);
        for (const form of content.querySelectorAll('form')) {
          const id = identity(form);
          if (openForms.has(id)) form.hidden = false;
          const draft = drafts.get(id);
          if (!draft) continue;
          for (const name of ['target','value','reason','name','topic','image']) {
            const field = form.elements.namedItem(name);
            const original = name === 'name' ? 'originalName' : name === 'topic' ? 'originalTopic' : '';
            if (field && field.type !== 'hidden' && draft.has(name) &&
                (!original || draft.get(name) !== draft.get(original))) {
              if (field.type === 'file') {
                const file = draft.get(name);
                if (!file.size) continue;
                const transfer = new DataTransfer();
                transfer.items.add(file);
                field.files = transfer.files;
                field.dispatchEvent(new Event('change', {bubbles:true}));
              } else {
                field.value = draft.get(name);
              }
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
      status.textContent = announcement;
    } catch {
      if (request === current && active && !life.signal.aborted) status.textContent = root.dataset.unavailable;
    } finally {
      if (request === current) root.removeAttribute('aria-busy');
    }
  }

  function render(template) {
    const currentTabs = content.querySelector('.oie-room-management-tabs');
    const incomingTabs = template.content.querySelector('.oie-room-management-tabs');
    const sameTabs = currentTabs && incomingTabs && currentTabs.dataset.tabsConfig === incomingTabs.dataset.tabsConfig;
    if (sameTabs) {
      const focused = document.activeElement;
      const focusedList = focused?.dataset.roomAffiliationList;
      const focusedAction = focused?.id || focused?.closest('form')?.dataset.roomActionForm;
      const focusedMenu = focused?.closest('[data-room-participant]')?.querySelector('[aria-controls]')?.getAttribute('aria-controls');
      for (const panel of currentTabs.querySelectorAll('[role=tabpanel]')) {
        const incoming = template.content.getElementById(panel.id);
        panel.replaceChildren(...incoming.childNodes);
      }
      if (focusedList) {
        currentTabs.querySelector(`[data-room-affiliation-list="${CSS.escape(focusedList)}"]`)?.focus({preventScroll:true});
      } else if (focusedAction && content.contains(focused) === false) {
        const replacement = document.getElementById(focusedAction);
        const menu = focusedMenu && currentTabs.querySelector(`[aria-controls="${CSS.escape(focusedMenu)}"]`);
        (replacement || menu || currentTabs.querySelector('[role=tab][aria-selected=true]'))?.focus({preventScroll:true});
      }
    } else {
      content.replaceChildren(template.content);
    }
    document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail: {root: content}}));
  }

  function identity(form) {
    const target = form.elements.namedItem('target');
    const value = form.elements.namedItem('value');
    return JSON.stringify([form.elements.managementAction.value, target?.type === 'hidden' ? target.value : '',
      form.elements.namedItem('messageId')?.value || '', value?.type === 'hidden' ? value.value : '']);
  }

  function clearInputs(form) {
    const key = form.elements.operationKey.value;
    const revision = form.elements.expectedRevision.value;
    form.reset();
    form.elements.operationKey.value = key;
    form.elements.expectedRevision.value = revision;
    form.elements.namedItem('image')?.dispatchEvent(new Event('change', {bubbles:true}));
    const feedback = form.querySelector('[data-form-feedback]');
    if (feedback) feedback.textContent = '';
  }

  function remember(form) {
    const id = identity(form);
    drafts.set(id, new FormData(form));
    if (form.dataset.roomActionForm && !form.hidden) openForms.add(id);
    else openForms.delete(id);
  }

  life.on(document, 'click', event => {
    const cancel = event.target.closest('[data-room-action-cancel]');
    if (cancel && content.contains(cancel)) {
      const form = cancel.form;
      if (isPending(form)) return;
      form.hidden = true;
      clearInputs(form);
      remember(form);
      const trigger = document.getElementById(form.dataset.roomActionForm);
      (trigger?.closest('[role=menu]')?.parentElement.querySelector('[aria-controls]') || trigger)?.focus({preventScroll:true});
      return;
    }
    const trigger = event.target.closest('[data-room-action-open],[id^=room-participant-][role=menuitem]');
    if (trigger) {
      if (hasPending()) return;
      const form = content.querySelector(`[data-room-action-form="${CSS.escape(trigger.id)}"]`);
      if (!form) return;
      for (const open of content.querySelectorAll('form[data-room-action-form]:not([hidden])')) {
        if (open.dataset.roomActionForm) open.hidden = true;
      }
      const menu = trigger.closest('[role=menu]');
      const menuTrigger = document.getElementById(menu?.id?.replace(/-panel$/, ''))
        ?.querySelector('[data-popover-trigger] [aria-expanded=true]');
      menuTrigger?.click();
      form.hidden = false;
      const control = form.querySelector('input:not([type=hidden]),[type=submit]');
      const focus = () => {
        if (!form.isConnected || form.hidden) return;
        if (menu?.getClientRects().length) {
          life.raf(focus);
          return;
        }
        control?.focus({preventScroll:true});
      };
      life.raf(focus);
      return;
    }
    const tabButton = event.target.closest('[role=tab]');
    if (!content.contains(tabButton) || hasPending()) return;
    const tab = tabButton.getAttribute('aria-controls');
    if (!tab) return;
    const next = tab.endsWith('tabsbans') ? 'outcast' : tab.endsWith('tabsaccess') ? accessList : affiliation;
    if (next !== affiliation) {
      affiliation = next;
      void load();
    }
  });

  life.on(document, 'click', event => {
    const trigger = event.target.closest('#chat-manage-room,#chat-invite-room,[id^=moderate-room-message-],[data-room-management-refresh],[data-room-affiliation-list]');
    if (!trigger) return;
    if (trigger === refresh || trigger.hasAttribute('data-room-affiliation-list')) {
      if (hasPending()) return;
      if (trigger.dataset.roomAffiliationList) affiliation = accessList = trigger.dataset.roomAffiliationList;
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
    if (nextMessage !== message || nextInvite !== invite) affiliation = '';
    active = true;
    if (nextMessage !== message || nextInvite !== invite) {
      if (hasPending()) {
        life.raf(open);
        return;
      }
      for (const form of content.querySelectorAll('form')) remember(form);
      content.replaceChildren();
    }
    message = nextMessage;
    invite = nextInvite;
    if (title) title.textContent = invite ? root.dataset.inviteTitle : root.dataset.manageTitle;
    life.raf(open);
  });

  life.on(document, 'keydown', event => {
    if (!active || event.key !== 'Escape') return;
    const menu = content.querySelector('[data-room-participant] [data-popover-trigger] [aria-expanded=true]');
    if (!menu) return;
    event.preventDefault();
    event.stopPropagation();
    menu.click();
    menu.focus({preventScroll:true});
  }, true);

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
    const action = form.elements.managementAction.value;
    if (action === 'update_details') {
      for (const [name, original] of [['name','originalName'],['topic','originalTopic']]) {
        const field = form.elements.namedItem(name);
        const baseline = form.elements.namedItem(original);
        if (field && baseline && (result.ok || name === 'name' && result.state === 'name_confirmed')) baseline.value = field.value;
      }
    }
    if (result.ok && action === 'invite') {
      form.elements.target.value = '';
      form.elements.reason.value = '';
    }
    for (const input of form.querySelectorAll('input:not([type=hidden]),textarea')) input.readOnly = false;
    const refreshContent = result.ok && (action === 'role' || action === 'affiliation' || action === 'set_avatar' || action === 'remove_avatar');
    if (refreshContent) {
      if (form.dataset.roomActionForm) form.hidden = true;
      clearInputs(form);
    }
    life.timeout(() => { if (active) void load(refreshContent, refreshContent ? result.message : ''); }, 0);
  });
});
