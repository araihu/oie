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
  let participant = '';
  const participantDetails = () => [...content.querySelectorAll('[data-room-participant-detail]')];

  function updateAvatarActions() {
    const remove = content.querySelector('#room-remove-avatar');
    if (!remove) return;
    const avatar = content.querySelector('.oie-room-management-avatar [data-avatar-src]');
    remove.hidden = !avatar?.dataset.avatarSrc;
  }
  const avatarChanges = new MutationObserver(updateAvatarActions);
  avatarChanges.observe(content, {subtree: true, childList: true, attributes: true, attributeFilter: ['data-avatar-src']});
  life.cleanup(() => avatarChanges.disconnect());

  function showParticipant(id, focus = false) {
    const list = content.querySelector('[data-room-participant-list]');
    if (!list) return;
    const details = participantDetails();
    const selected = details.find(panel => panel.dataset.roomParticipantDetail === id);
    const previous = participant;
    participant = selected ? id : '';
    list.hidden = !!selected;
    for (const panel of details) panel.hidden = panel !== selected;
    if (focus) {
      const target = selected?.querySelector('h3') || document.getElementById(previous) ||
        content.querySelector('[role=tab][aria-selected=true]');
      target?.focus({preventScroll:true});
    }
  }

  function refreshParticipantAvatars(fragment) {
    const avatars = fragment.querySelectorAll('[data-room-participant-avatar]');
    const cached = new Map([...avatars].map(node => [node.dataset.roomParticipantAvatar, node]));
    for (const node of content.querySelectorAll('[data-room-participant-avatar]')) {
      const fresh = cached.get(node.dataset.roomParticipantAvatar);
      if (fresh && fresh.dataset.avatarImage !== node.dataset.avatarImage) {
        node.replaceWith(fresh);
      }
    }
  }

  const detailsFields = [['name', 'originalName'], ['topic', 'originalTopic']];

  function roomDetailsValue(name, value) {
    const normalized = name === 'topic' ? value.replace(/\r\n?/g, '\n') : value;
    return normalized.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
  }

  function detailsChanged(form) {
    return detailsFields.some(([name, original]) => {
      const field = form.elements.namedItem(name);
      const baseline = form.elements.namedItem(original);
      return field && baseline && roomDetailsValue(name, field.value) !== roomDetailsValue(name, baseline.value);
    });
  }

  function syncDetailsSave(form) {
    form.querySelector('[type=submit]').disabled =
      form.dataset.busy === 'true' || form.dataset.permissionLost === 'true' || !detailsChanged(form);
  }

  life.on(content, 'input', event => {
    const form = event.target.form;
    if (form?.hasAttribute('data-room-details-form')) syncDetailsSave(form);
  });

  async function load(replace = true, announcement = '', avatarsOnly = false) {
    if (avatarsOnly && request) return;
    request?.abort();
    const current = new AbortController();
    request = current;
    if (!avatarsOnly) {
      status.textContent = root.dataset.loading;
      root.setAttribute('aria-busy', 'true');
    }
    const {chat, account, engine, lang} = root.dataset;
    const params = new URLSearchParams({chat, account, engine, lang, message, affiliation, invite});
    try {
      const response = await life.fetch('/ui/rooms/manage-fragment?' + params, {signal: current.signal});
      if (!response.ok) throw new Error();
      const markup = await response.text();
      if (request !== current || !active || life.signal.aborted) return;
      const template = document.createElement('template');
      template.innerHTML = markup;
      if (avatarsOnly) {
        refreshParticipantAvatars(template.content);
        return;
      }
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
          if (openForms.has(id)) setActionOpen(form, true);
          const draft = drafts.get(id);
          if (!draft) continue;
          for (const name of ['target','value','reason','name','topic','image']) {
            const field = form.elements.namedItem(name);
            const original = name === 'name' ? 'originalName' : name === 'topic' ? 'originalTopic' : '';
            if (field && field.type !== 'hidden' && draft.has(name) &&
                (!original || roomDetailsValue(name, draft.get(name)) !== roomDetailsValue(name, draft.get(original)))) {
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
              delete form.dataset.permissionLost;
              if (form.hasAttribute('data-room-details-form')) syncDetailsSave(form);
              else {
                const submit = form.querySelector('[type=submit]');
                if (submit) submit.disabled = false;
              }
            }
            form.elements.expectedRevision.value = revision;
          }
        }
      }
      status.textContent = announcement;
    } catch {
      if (!avatarsOnly && request === current && active && !life.signal.aborted) status.textContent = root.dataset.unavailable;
    } finally {
      if (request === current) {
        request = null;
        root.removeAttribute('aria-busy');
      }
    }
  }

  function render(template) {
    const currentTabs = content.querySelector('.oie-room-management-tabs');
    const incomingTabs = template.content.querySelector('.oie-room-management-tabs');
    const sameTabs = currentTabs && incomingTabs && currentTabs.dataset.tabsConfig === incomingTabs.dataset.tabsConfig;
    if (sameTabs) {
      const focused = document.activeElement;
      const focusedAction = focused?.id || focused?.closest('form')?.dataset.roomActionForm;
      const focusedParticipant = !!focused?.closest('[data-room-participant-detail]');
      for (const panel of currentTabs.querySelectorAll('[role=tabpanel]')) {
        const incoming = template.content.getElementById(panel.id);
        panel.replaceChildren(...incoming.childNodes);
      }
      showParticipant(participant, focusedParticipant);
      if (!focusedParticipant && focusedAction && !content.contains(focused)) {
        const replacement = document.getElementById(focusedAction);
        (replacement || currentTabs.querySelector('[role=tab][aria-selected=true]'))?.focus({preventScroll:true});
      }
    } else {
      content.replaceChildren(template.content);
      showParticipant(participant);
    }
    document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail: {root: content}}));
  }

  function identity(form) {
    const target = form.elements.namedItem('target');
    const value = form.elements.namedItem('value');
    return JSON.stringify([form.elements.managementAction.value, target?.type === 'hidden' ? target.value : '',
      form.elements.namedItem('messageId')?.value || '', value?.type === 'hidden' ? value.value : '']);
  }

  function setActionOpen(form, open) {
    form.hidden = !open;
    const trigger = document.getElementById(form.dataset.roomActionForm);
    if (trigger?.hasAttribute('aria-expanded')) trigger.setAttribute('aria-expanded', String(open));
    return trigger;
  }

  function cancelAction(form) {
    if (isPending(form)) return;
    const trigger = setActionOpen(form, false);
    clearInputs(form);
    remember(form);
    trigger?.focus({preventScroll:true});
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

  let avatarRefresh;
  life.on(document, 'oie:room-participant-avatar-changed', event => {
    if (event.detail.accountId !== root.dataset.account || !active || !content.querySelector('[data-room-participant-avatar]')) return;
    clearTimeout(avatarRefresh);
    avatarRefresh = life.timeout(() => { if (active) void load(false, '', true); }, 250);
  });

  life.on(document, 'click', event => {
    const row = event.target.closest('[data-room-participant-open]');
    if (row && content.contains(row)) {
      if (!hasPending()) showParticipant(row.dataset.roomParticipantOpen, true);
      return;
    }
    const back = event.target.closest('[data-room-participant-back]');
    if (back && content.contains(back)) {
      if (!hasPending()) showParticipant('', true);
      return;
    }
    const cancel = event.target.closest('[data-room-action-cancel]');
    if (cancel && content.contains(cancel)) {
      cancelAction(cancel.form);
      return;
    }
    const trigger = event.target.closest('[data-room-action-open]');
    if (trigger) {
      if (hasPending()) return;
      const form = content.querySelector(`[data-room-action-form="${CSS.escape(trigger.id)}"]`);
      if (!form) return;
      if (!form.hidden && trigger.hasAttribute('aria-expanded')) {
        setActionOpen(form, false);
        return;
      }
      for (const open of content.querySelectorAll('form[data-room-action-form]:not([hidden])')) {
        if (open.dataset.roomActionForm) setActionOpen(open, false);
      }
      setActionOpen(form, true);
      life.raf(() => form.querySelector('input:not([type=hidden]),[type=submit]')?.focus({preventScroll:true}));
      return;
    }
    const tabButton = event.target.closest('[role=tab]');
    if (!content.contains(tabButton) || hasPending()) return;
    if (tabButton.getAttribute('aria-controls') === 'tabpanelroom-management-tabsbans' && affiliation !== 'outcast') {
      affiliation = 'outcast';
      void load();
    }
  });

  life.on(document, 'click', event => {
    const trigger = event.target.closest('#chat-manage-room,#chat-invite-room,[id^=moderate-room-message-],[data-room-management-refresh]');
    if (!trigger) return;
    if (trigger === refresh) {
      if (hasPending()) return;
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
    if (nextMessage !== message || nextInvite !== invite) { affiliation = ''; participant = ''; }
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
    if (!active || event.key !== 'Escape' || !event.target.closest) return;
    if (!participant || !event.target.closest('[data-room-participant-detail]')) return;
    event.preventDefault();
    event.stopPropagation();
    if (hasPending()) return;
    const form = event.target.closest('[data-room-participant-detail]').querySelector('.oie-room-action form:not([hidden])');
    if (form) cancelAction(form);
    else showParticipant('', true);
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
      for (const [name, original] of detailsFields) {
        const field = form.elements.namedItem(name);
        const baseline = form.elements.namedItem(original);
        if (field && baseline && (result.ok || name === 'name' && result.state === 'name_confirmed')) baseline.value = field.value;
      }
      // The live submit handler restores the form controls before this runs.
      queueMicrotask(() => syncDetailsSave(form));
    }
    if (result.ok && action === 'invite') {
      form.elements.target.value = '';
      form.elements.reason.value = '';
    }
    for (const input of form.querySelectorAll('input:not([type=hidden]),textarea')) input.readOnly = false;
    const refreshContent = result.ok && (action === 'role' || action === 'affiliation' || action === 'set_avatar' || action === 'remove_avatar');
    if (refreshContent) {
      if (form.dataset.roomActionForm) setActionOpen(form, false);
      clearInputs(form);
    }
    life.timeout(() => { if (active) void load(refreshContent, refreshContent ? result.message : ''); }, 0);
  });
});
