(() => {
function filterRoomList(search, scope) {
  const results = scope.querySelector('#' + search.getAttribute('aria-controls'));
  const empty = results?.querySelector('[data-room-list-no-results]');
  if (!empty) return;
  const fold = value => value.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase().trim();
  const query = fold(search.value);
  let found = false;
  for (const row of results.querySelectorAll('[data-room-list-match]')) {
    row.hidden = !fold(row.dataset.roomListMatch).includes(query);
    found ||= !row.hidden;
  }
  for (const group of results.querySelectorAll('.oie-room-participant-group')) {
    group.hidden = !group.querySelector('[data-room-list-match]:not([hidden])');
  }
  empty.hidden = found;
}

window.OieRuntime.register('room-management', life => {
  life.on(document, 'input', event => {
    if (event.target.id === 'info-room-participants-search') {
      filterRoomList(event.target, event.target.closest('[data-info-room-participants]'));
    }
  });
  life.on(document, 'keydown', event => {
    if (event.key !== 'Escape' || event.target.id !== 'info-room-participants-search' || !event.target.value) return;
    event.preventDefault();
    event.stopPropagation();
    event.target.value = '';
    event.target.dispatchEvent(new Event('input', {bubbles:true}));
  }, true);
  const root = document.querySelector('[data-room-management]');
  if (!root) return;
  const content = root.querySelector('[data-room-management-content]');
  const loadError = root.querySelector('[data-room-management-error]');
  let request, opener, message = '', invite = false, active = false;
  let bansRequest, bansFresh = false, participantsDirty = false;
  let bansLoaded = false;
  const bansPanelID = 'tabpanelroom-management-tabsbans';
  const participantsPanelID = 'tabpanelroom-management-tabsparticipants';
  const pending = new WeakMap();
  const title = document.getElementById("room-managementTitle");
  const drafts = new Map();
  const openForms = new Set();
  const isPending = form => form.dataset.busy === 'true' || pending.has(form);
  const hasPending = () => [...content.querySelectorAll('form')].some(isPending);
  const participantViews = new Map();
  const listQueries = new Map();
  const tabSelected = id => content.querySelector('[aria-controls="' + id + '"]')?.getAttribute('aria-selected') === 'true';

  function updateAvatarActions() {
    const remove = content.querySelector('#room-remove-avatar');
    if (!remove) return;
    const avatar = content.querySelector('.oie-room-management-avatar [data-avatar-src]');
    remove.hidden = !avatar?.dataset.avatarSrc;
  }
  const avatarChanges = new MutationObserver(updateAvatarActions);
  avatarChanges.observe(content, {subtree: true, childList: true, attributes: true, attributeFilter: ['data-avatar-src']});
  life.cleanup(() => avatarChanges.disconnect());

  function fragmentURL(extra = {}) {
    const {chat, account, engine, lang} = root.dataset;
    return '/ui/rooms/manage-fragment?' + new URLSearchParams({chat, account, engine, lang, ...extra});
  }

  function managementError() {
    window.dispatchEvent(new CustomEvent('notify', {detail:{kind:'toast',tone:'danger',message:root.dataset.unavailable}}));
  }

  function closeParticipant(panel, focus = false) {
    const list = panel.querySelector('[data-room-participant-list]');
    const slot = panel.querySelector('[data-room-profile-slot]');
    const view = participantViews.get(panel.id);
    view?.controller.abort();
    participantViews.delete(panel.id);
    slot.hidden = true;
    slot.removeAttribute('aria-busy');
    slot.querySelector('[data-room-profile-content]').replaceChildren();
    slot.querySelector('[data-room-profile-status]').replaceChildren();
    list.hidden = false;
    if (view) {
      const viewport = list.querySelector('[data-goshtoso-scroll-viewport]') || panel;
      viewport.scrollTop = view.scrollTop;
    }
    if (focus) {
      (document.getElementById(view?.id) || list.querySelector('input[type=search]') ||
        content.querySelector('[role=tab][aria-selected=true]'))?.focus({preventScroll:true});
    }
  }

  async function openParticipant(panel, id, focus = true, keepProfile = false) {
    const list = panel.querySelector('[data-room-participant-list]');
    const slot = panel.querySelector('[data-room-profile-slot]');
    const pendingPane = slot.querySelector('[data-room-profile-pending]');
    const status = slot.querySelector('[data-room-profile-status]');
    const profile = slot.querySelector('[data-room-profile-content]');
    const viewport = list.querySelector('[data-goshtoso-scroll-viewport]') || panel;
    const previous = participantViews.get(panel.id);
    previous?.controller.abort();
    const view = {id, scrollTop: previous?.scrollTop ?? viewport.scrollTop, controller: new AbortController()};
    participantViews.set(panel.id, view);
    list.hidden = true;
    slot.hidden = false;
    if (!keepProfile) {
      panel.scrollTop = 0;
      profile.replaceChildren();
      pendingPane.hidden = false;
      status.replaceChildren(root.querySelector('[data-room-profile-loading]').content.cloneNode(true));
      if (focus && tabSelected(panel.id)) pendingPane.querySelector('button').focus({preventScroll:true});
    }
    slot.setAttribute('aria-busy', 'true');
    try {
      const response = await life.fetch(fragmentURL({profile:id}), {signal:view.controller.signal});
      if ([403,404,409].includes(response.status)) {
        if (!view.controller.signal.aborted && active && slot.isConnected) {
          closeParticipant(panel, focus && tabSelected(panel.id));
          if (panel.id === bansPanelID && response.status === 404) void loadBans();
          else void load();
        }
        return;
      }
      if (!response.ok) throw new Error();
      const template = document.createElement('template');
      template.innerHTML = await response.text();
      if (view.controller.signal.aborted || life.signal.aborted || !active || !slot.isConnected) return;
      if ([...profile.querySelectorAll('form')].some(isPending)) return;
      const moveFocus = focus && (slot.contains(document.activeElement) || document.activeElement.id === id ||
        document.activeElement === document.body) && tabSelected(panel.id);
      const focusID = keepProfile && (document.activeElement.id || document.activeElement.closest('form')?.dataset.roomActionForm);
      if (keepProfile) for (const form of profile.querySelectorAll('form')) remember(form);
      profile.replaceChildren(template.content);
      restoreForms(profile);
      status.replaceChildren();
      pendingPane.hidden = true;
      if (moveFocus) ((focusID && document.getElementById(focusID)) || profile.querySelector('h3'))?.focus({preventScroll:true});
      document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail:{root:profile}}));
    } catch {
      if (view.controller.signal.aborted || life.signal.aborted || !active || !slot.isConnected) return;
      if (!keepProfile) closeParticipant(panel, focus && tabSelected(panel.id));
      managementError();
    } finally {
      if (participantViews.get(panel.id) === view) slot.removeAttribute('aria-busy');
    }
  }

  async function loadBans(retry = false) {
    const panel = document.getElementById(bansPanelID);
    if (!panel || bansRequest) return;
    const controller = new AbortController();
    bansRequest = controller;
    panel.setAttribute('aria-busy','true');
    try {
      const response = await life.fetch(fragmentURL({affiliation:'outcast'}), {signal:controller.signal});
      if (!response.ok) throw new Error();
      const template = document.createElement('template');
      template.innerHTML = await response.text();
      if (!active || controller.signal.aborted || life.signal.aborted || !panel.isConnected) return;
      const incoming = template.content.querySelector('#room-ban-results');
      const results = panel.querySelector('#room-ban-results');
      if (!incoming || !results) throw new Error();
      const viewport = panel.querySelector('[data-goshtoso-scroll-viewport]');
      const scrollTop = viewport?.scrollTop;
      const focusedRow = document.activeElement.closest('[data-room-participant-open]');
      results.replaceWith(incoming);
      filterRoomList(panel.querySelector('[data-room-list-search]'), panel);
      const form = panel.querySelector('.oie-room-ban-footer form');
      if (!isPending(form)) form.elements.expectedRevision.value = incoming.dataset.roomRevision;
      const view = participantViews.get(panel.id);
      if (view && !incoming.querySelector('#' + CSS.escape(view.id))) closeParticipant(panel, tabSelected(panel.id));
      else if (focusedRow && !focusedRow.isConnected) {
        (document.getElementById(focusedRow.id) || panel.querySelector('[data-room-list-search]'))?.focus({preventScroll:true});
      }
      if (viewport && scrollTop && (!view || participantViews.has(panel.id))) viewport.scrollTop = scrollTop;
      bansFresh = true;
      bansLoaded = true;
      document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail:{root:incoming}}));
    } catch {
      if (controller.signal.aborted || life.signal.aborted || !active || !panel.isConnected) return;
      if (retry) {
        bansRequest = null;
        return loadBans();
      }
      managementError();
    } finally {
      if (bansRequest === controller) {
        bansRequest = null;
        panel.removeAttribute('aria-busy');
      }
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
    const profile = document.getElementById(participantsPanelID);
    const view = profile && participantViews.get(profile.id);
    const oldAvatar = profile?.querySelector('[data-room-profile-content] [data-avatar-image]');
    const freshAvatar = view && fragment.getElementById(view.id)?.querySelector('[data-avatar-image]');
    if (oldAvatar && freshAvatar && oldAvatar.dataset.avatarImage !== freshAvatar.dataset.avatarImage && !hasPending()) {
      void openParticipant(profile, view.id, true, true);
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
    if (event.target.matches('[data-room-list-search]')) {
      const search = event.target;
      listQueries.set(search.id, search.value);
      const panel = search.closest('[role=tabpanel]');
      const viewport = panel.querySelector('[data-goshtoso-scroll-viewport]') || panel;
      viewport.scrollTop = 0;
      filterRoomList(search, panel);
      return;
    }
    const form = event.target.form;
    if (form?.hasAttribute('data-room-details-form')) syncDetailsSave(form);
  });

  async function load(replace = true, avatarsOnly = false, participantsOnly = false) {
    if (avatarsOnly && request) return;
    request?.abort();
    const controller = new AbortController();
    request = controller;
    if (!avatarsOnly) {
      loadError.hidden = true;
      root.setAttribute('aria-busy','true');
    }
    try {
      const response = await life.fetch(fragmentURL(participantsOnly ? {participants:true} : {message,invite}), {signal:controller.signal});
      if (participantsOnly && response.status === 409) return load();
      if (!response.ok) throw new Error();
      const template = document.createElement('template');
      template.innerHTML = await response.text();
      if (request !== controller || controller.signal.aborted || !active || life.signal.aborted) return;
      if (avatarsOnly) {
        refreshParticipantAvatars(template.content);
        return;
      }
      if (participantsOnly) {
        const panel = document.getElementById(participantsPanelID);
        if (!panel || [...panel.querySelectorAll('form')].some(isPending)) return;
        replacePanel(panel, template.content);
        participantsDirty = false;
        document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail:{root:panel}}));
        return;
      }
      if (replace) {
        if (hasPending()) return;
        for (const form of content.querySelectorAll('form')) {
          if (!form.closest('[data-room-profile-slot]')) remember(form);
        }
        render(template);
        restoreForms(content);
      } else {
        const revision = template.content.querySelector('[name=expectedRevision]')?.value;
        if (!revision && !template.content.querySelector('.oie-room-management-tabs')) {
          render(template);
        } else {
          const fresh = new Map([...template.content.querySelectorAll('form')].map(form => [identity(form), form]));
          for (const form of content.querySelectorAll('form')) {
            if (isPending(form) || form.closest('[data-room-profile-slot]')) continue;
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
            // An open destructive confirmation keeps the revision the owner reviewed.
            if (revision && (form.elements.managementAction.value !== 'destroy' || form.hidden)) {
              form.elements.expectedRevision.value = revision;
            }
          }
        }
      }
    } catch {
      if (!avatarsOnly && request === controller && !controller.signal.aborted && active && !life.signal.aborted) {
        if (content.childElementCount) managementError();
        else loadError.hidden = false;
      }
    } finally {
      if (request === controller) {
        request = null;
        root.removeAttribute('aria-busy');
      }
    }
  }

  function restoreForms(scope) {
    for (const form of scope.querySelectorAll('form')) {
      const id = identity(form);
      if (openForms.has(id)) setActionOpen(form, true);
      const draft = drafts.get(id);
      if (!draft) continue;
      for (const name of ['target','value','reason','name','topic','image']) {
        const field = form.elements.namedItem(name);
        const original = name === 'name' ? 'originalName' : name === 'topic' ? 'originalTopic' : '';
        if (!field || field.type === 'hidden' || !draft.has(name) ||
            original && roomDetailsValue(name, draft.get(name)) === roomDetailsValue(name, draft.get(original))) continue;
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

  function render(template) {
    const currentTabs = content.querySelector('.oie-room-management-tabs');
    const incomingTabs = template.content.querySelector('.oie-room-management-tabs');
    const sameTabs = currentTabs && incomingTabs && currentTabs.dataset.tabsConfig === incomingTabs.dataset.tabsConfig;
    if (sameTabs) {
      for (const panel of currentTabs.querySelectorAll('[role=tabpanel]')) {
        if (panel.id === bansPanelID) continue;
        replacePanel(panel, template.content.getElementById(panel.id));
      }
    } else {
      stopBans();
      bansFresh = bansLoaded = false;
      for (const view of participantViews.values()) view.controller.abort();
      participantViews.clear();
      content.replaceChildren(template.content);
      for (const search of content.querySelectorAll('[data-room-list-search]')) {
        search.value = listQueries.get(search.id) || '';
        filterRoomList(search, content);
      }
    }
    participantsDirty = false;
    document.dispatchEvent(new CustomEvent('oie:room-management-content', {detail:{root:content}}));
  }

  function replacePanel(panel, incoming) {
    const focused = document.activeElement;
    const focusInside = panel.contains(focused);
    const searchSelection = focused?.matches('[data-room-list-search]') ? [focused.selectionStart, focused.selectionEnd] : null;
    const focusedID = focused?.id || focused?.closest('form')?.dataset.roomActionForm;
    const view = participantViews.get(panel.id);
    view?.controller.abort();
    const slot = view && panel.querySelector('[data-room-profile-slot]');
    if (panel.querySelector('#room-destructive-actions')?.open) {
      const disclosure = incoming.querySelector('#room-destructive-actions');
      if (disclosure) disclosure.open = true;
    }
    panel.replaceChildren(...incoming.childNodes);
    const search = panel.querySelector('[data-room-list-search]');
    if (search) {
      search.value = listQueries.get(search.id) || '';
      filterRoomList(search, panel);
    }
    if (view && panel.querySelector('#' + CSS.escape(view.id))) {
      const hasProfile = !!slot?.querySelector('[data-room-participant-detail]');
      if (hasProfile) {
        panel.querySelector('[data-room-profile-slot]').replaceWith(slot);
        if (focusInside && tabSelected(panel.id)) focused?.focus({preventScroll:true});
      }
      void openParticipant(panel, view.id, focusInside, hasProfile);
    } else {
      participantViews.delete(panel.id);
      if (focusInside && tabSelected(panel.id)) {
        const replacement = (focusedID && document.getElementById(focusedID)) || search ||
          content.querySelector('[role=tab][aria-selected=true]');
        replacement?.focus({preventScroll:true});
        if (searchSelection && replacement === search) search.setSelectionRange(...searchSelection);
      }
    }
  }

  function stopBans() {
    bansRequest?.abort();
    bansRequest = null;
    document.getElementById(bansPanelID)?.removeAttribute('aria-busy');
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

  function cancelAction(form, restoreFocus = true) {
    if (isPending(form)) return;
    const trigger = setActionOpen(form, false);
    clearInputs(form);
    remember(form);
    if (restoreFocus) trigger?.focus({preventScroll:true});
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
    avatarRefresh = life.timeout(() => { if (active) void load(false, true); }, 250);
  });

  life.on(content, 'toggle', event => {
    const disclosure = event.target;
    if (disclosure.id !== 'room-destructive-actions' || disclosure.open) return;
    const form = disclosure.querySelector('#room-destroy-form:not([hidden])');
    if (form) cancelAction(form, false);
  }, true);

  life.on(document, 'click', event => {
    const row = event.target.closest('[data-room-participant-open]');
    if (row && content.contains(row)) {
      if (!hasPending()) void openParticipant(row.closest('[role=tabpanel]'), row.dataset.roomParticipantOpen);
      return;
    }
    const back = event.target.closest('[data-room-participant-back]');
    if (back && content.contains(back)) {
      if (!hasPending()) closeParticipant(back.closest('[role=tabpanel]'), true);
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
      const focusTarget = form.hasAttribute('data-room-action-confirmation')
        ? '[data-room-action-cancel]'
        : 'input:not([type=hidden]),[type=submit]';
      life.raf(() => form.querySelector(focusTarget)?.focus({preventScroll:true}));
      return;
    }
    const tabButton = event.target.closest('[role=tab]');
    if (!content.contains(tabButton)) return;
    const panel = tabButton.getAttribute('aria-controls');
    if (panel === bansPanelID && !bansFresh && !hasPending()) void loadBans();
    if (panel === participantsPanelID && participantsDirty && !hasPending()) void load(true, false, true);
  });

  life.on(document, 'click', event => {
    const trigger = event.target.closest('#chat-manage-room,#chat-invite-room,[id^=moderate-room-message-]');
    if (!trigger) return;
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
      void load().then(() => {
        if (active && !bansFresh && tabSelected(bansPanelID)) void loadBans();
      });
    };
    const nextMessage = trigger.id.startsWith('moderate-room-message-') ? trigger.id.slice('moderate-room-message-'.length) : '';
    const nextInvite = trigger.id === 'chat-invite-room';
    if (nextMessage !== message || nextInvite !== invite) {
      stopBans();
      bansFresh = bansLoaded = false;
      for (const view of participantViews.values()) view.controller.abort();
      participantViews.clear();
      listQueries.clear();
    }
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
    if (event.target.matches('[data-room-list-search]') && event.target.value) {
      event.preventDefault();
      event.stopPropagation();
      event.target.value = '';
      event.target.dispatchEvent(new Event('input', {bubbles:true}));
      return;
    }
    const confirmation = event.target.closest('form[data-room-action-confirmation]:not([hidden])') ||
      event.target.closest('.oie-room-ban-footer')?.querySelector('form:not([hidden])');
    const detail = event.target.closest('[data-room-participant-detail]');
    const slot = event.target.closest('[data-room-profile-slot]');
    if (!confirmation && !slot) return;
    event.preventDefault();
    event.stopPropagation();
    if (hasPending()) return;
    const form = confirmation || detail?.querySelector('.oie-room-action form:not([hidden])');
    if (form) cancelAction(form);
    else closeParticipant(slot.closest('[role=tabpanel]'), true);
  }, true);

  life.on(window, 'drawer:close-request', event => {
    if (event.detail?.id !== 'room-management') return;
    active = false;
    request?.abort();
    stopBans();
    bansFresh = false;
    for (const view of participantViews.values()) view.controller.abort();
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
    if (action === 'destroy') {
      const focused = form.contains(document.activeElement);
      const trigger = setActionOpen(form, false);
      if (result.ok) {
        window.dispatchEvent(new CustomEvent('drawer:close', {detail: {id: 'room-management'}}));
      } else {
        if (focused) trigger?.focus({preventScroll:true});
        if (result.message) window.dispatchEvent(new CustomEvent('notify', {
          detail: {kind:'toast', tone:'danger', message:result.message}
        }));
        life.timeout(() => {if (active) void load(true);}, 0);
      }
      return;
    }
    const panel = form.closest('[role=tabpanel]');
    const changesBans = result.ok && action === 'affiliation' &&
      (form.elements.value.value === 'outcast' || panel?.id === bansPanelID);
    if (changesBans) {
      bansFresh = false;
      stopBans();
    }
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
      if (form.dataset.roomActionForm) {
        const focused = form.contains(document.activeElement);
        const trigger = setActionOpen(form, false);
        if (focused) trigger?.focus({preventScroll: true});
      }
      clearInputs(form);
    }
    const view = panel && participantViews.get(panel.id);
    life.timeout(() => {
      if (!active) return;
      if (panel?.id === bansPanelID) {
        if (result.ok) {
          participantsDirty = true;
          void loadBans(true);
          if (tabSelected(participantsPanelID)) void load(true, false, true);
        } else if (view) {
          void openParticipant(panel, view.id, true, true);
        } else {
          void loadBans();
        }
        return;
      }
      if (view && !result.ok) void openParticipant(panel, view.id, true, true);
      else if (result.ok && panel?.id === participantsPanelID) {
        participantsDirty = true;
        void load(true, false, true);
      } else void load(refreshContent);
      if (!bansFresh && (tabSelected(bansPanelID) || changesBans && bansLoaded)) {
        void loadBans(changesBans);
      }
    }, 0);
  });
});
})();
