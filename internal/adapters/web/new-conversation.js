window.OieRuntime.register('new-conversation-picker', life => {
  const dialog = document.querySelector('#new-conversation-picker');
  const trigger = document.querySelector('[data-new-conversation]');
  if (!dialog) return;

  const search = dialog.querySelector('#picker-search');
  const sections = [...dialog.querySelectorAll('[data-picker-section]')];
  const accounts = new Map([...dialog.querySelector('[data-picker-accounts]').content.children]
    .map(node => [node.dataset.account, node.textContent]));
  const title = dialog.querySelector('#picker-title');
  const homeTitle = title.textContent;
  const back = dialog.querySelector('[data-picker-back]');
  const searchBar = search.closest('.oie-picker-search');
  const list = dialog.querySelector('.oie-picker-scroll');
  const steps = [...dialog.querySelectorAll('[data-picker-step]')];
  let returnFocus, previousAction;

  let controller, timer;

  let lookup;
  function cancelLookup() {
    lookup?.abort();
    lookup = null;
    dialog.querySelector('#new-conversation-direct')?.removeAttribute('aria-busy');
  }
  function cancel() {
    cancelLookup();
    clearTimeout(timer);
    controller?.abort();
  }

  function clearSection(section) {
    section.hidden = false;
    section.removeAttribute('aria-busy');
    section.querySelector('[data-picker-list]').replaceChildren();
    section.querySelector('[role=status]').textContent = dialog.dataset.loading;
  }

  async function load(section, cursor = '') {
    if (section.hasAttribute('aria-busy')) return;
    section.setAttribute('aria-busy', 'true');
    const group = section.dataset.pickerSection;
    const term = search.value.trim();
    const signal = controller.signal;
    const list = section.querySelector('[data-picker-list]');
    const status = section.querySelector('[role=status]');
    const params = new URLSearchParams({
      group, term, cursor, account: dialog.dataset.account, lang: dialog.dataset.locale, theme: document.documentElement.dataset.mode
    });
    const previous = list.querySelector('[data-picker-more]');
    const retry = status.querySelector('button');
    // Keep the active pagination control focusable until the response arrives.
    previous?.setAttribute('aria-disabled', 'true');
    retry?.setAttribute('aria-disabled', 'true');
    if (!retry) status.textContent = dialog.dataset.loading;
    try {
      const response = await life.fetch('/ui/new-conversation/results?' + params, {signal});
      if (!response.ok) throw Error();
      const html = await response.text();
      if (signal.aborted || !dialog.open) return;
      const fragment = document.createElement('template');
      fragment.innerHTML = html;
      const first = fragment.content.querySelector('[data-picker-account]');
      if (accounts.size > 1 && !dialog.dataset.account) {
        for (const row of fragment.content.querySelectorAll('[data-picker-account]')) {
          const account = accounts.get(row.dataset.pickerAccount);
          const subtitle = row.querySelector('small');
          if (account) subtitle.textContent = [subtitle.textContent.trim(), account].filter(Boolean).join(' · ');
        }
      }
      const coverage = fragment.content.querySelector('[data-picker-coverage]');
      coverage?.remove();
      const advanceFocus = document.activeElement === previous || document.activeElement === retry;
      previous?.remove();
      list.append(fragment.content);
      document.dispatchEvent(new CustomEvent('oie:picker-content', {detail:{root:list}}));
      status.textContent = coverage?.content.textContent || '';
      if (!list.childElementCount && !coverage) {
        section.hidden = group === 'chats' && sections.length > 1;
        if (!section.hidden) status.textContent = term || group === 'chats' ? dialog.dataset.noMatch : dialog.dataset.empty;
      }
      if (advanceFocus) (first || search).focus({preventScroll: !first});
    } catch {
      if (signal.aborted || life.signal.aborted) return;
      if (!retry) {
        status.textContent = dialog.dataset.unavailable + ' ';
        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = dialog.dataset.retry;
        button.onclick = () => load(section, cursor);
        status.append(button);
      }
    } finally {
      if (!signal.aborted) {
        section.removeAttribute('aria-busy');
        previous?.removeAttribute('aria-disabled');
        retry?.removeAttribute('aria-disabled');
      }
    }
  }

  function refresh(delay) {
    cancel();
    controller = new AbortController();
    const searching = Boolean(search.value.trim());
    dialog.toggleAttribute('data-searching', searching);
    sections[0].querySelector('h3').textContent = searching ? dialog.dataset.chats : dialog.dataset.recent;
    sections.forEach(clearSection);
    dialog.querySelector('.oie-picker-scroll').scrollTop = 0;
    timer = setTimeout(() => sections.forEach(section => load(section)), delay);
  }

  function show(step) {
    cancelLookup();
    if (steps.some(pane => !pane.hidden && pane !== step && pane.dataset.pickerStep === 'join-room'))
      document.dispatchEvent(new CustomEvent('oie:picker-leave-join'));
    for (const pane of steps) pane.hidden = pane !== step;
    title.textContent = step?.dataset.title || homeTitle;
    back.hidden = !step;
    searchBar.hidden = list.hidden = Boolean(step);
    const mobile = matchMedia('(max-width:720px), (pointer:coarse) and (max-height:600px)').matches;
    const focus = step || mobile ? title : search;
    if (!dialog.open) {
      focus.setAttribute('autofocus', '');
      dialog.showModal();
      focus.removeAttribute('autofocus');
    }
    focus.focus({preventScroll:true});
  }

  function openPicker() {
    returnFocus = trigger;
    previousAction = null;
    search.value = '';
    show(null);
    refresh(0);
  }

  function openStep(id, action) {
    const step = steps.find(pane => pane.dataset.pickerStep === id);
    if (!step) return;
    previousAction = action;
    show(step);
  }

  async function openDirect(action) {
    const form = dialog.querySelector('#new-conversation-form');
    const address = search.value.trim();
    const account = dialog.dataset.account || (accounts.size === 1 ? accounts.keys().next().value : '');
    if (form && form.dataset.busy !== 'true') {
      form.elements.address.value = address.includes('@') ? address : '';
      if (account && form.elements.address.value) {
        if (lookup) return;
        lookup = new AbortController();
        const {signal} = lookup;
        action.setAttribute('aria-busy','true');
        const params = new URLSearchParams({account, address, lang:dialog.dataset.locale, theme:document.documentElement.dataset.mode});
        try {
          const response = await life.fetch('/ui/new-conversation/direct?' + params, {signal});
          if (response.status === 200) {
            const url = new URL(await response.text(), location.href);
            if (signal.aborted || !dialog.open) return;
            if (url.origin === location.origin && url.pathname === '/' && url.searchParams.has('chat')) {
              const current = document.querySelector('#conversation-pane[data-open-chat="true"]');
              closePicker();
              if (current?.dataset.conversationId !== url.searchParams.get('chat')) {
                if (window.OieNavigation) window.OieNavigation.go(url.href);
                else location.assign(url.href);
              }
              return;
            }
          }
        } catch {
          // The form can still open or create the conversation when the lookup fails.
        }
        if (signal.aborted || !dialog.open) return;
      }
    }
    openStep('newchat', action);
  }

  function closePicker() {
    dialog.close();
    returnFocus?.focus({preventScroll:true});
  }

  life.on(dialog, 'oie:contact-added', event => {
    const form = event.target;
    if (form.elements.address.value === event.detail.address) form.elements.address.value = '';
    if (!dialog.open || !form.getClientRects().length) return;
    show(null);
    refresh(0);
    previousAction?.focus({preventScroll:true});
  });
  life.on(trigger, 'click', openPicker);
  life.on(document, 'oie:picker-step', event => {
    returnFocus = event.detail.trigger;
    openStep(event.detail.id);
  });
  const initial = new URLSearchParams(location.search).get('open') || document.body.dataset.openDrawer;
  if (initial === 'conversation-picker') openPicker();
  else if (initial) {
    returnFocus = trigger;
    openStep(initial);
  }
  life.on(search, 'input', () => refresh(160));
  life.on(dialog, 'click', event => {
    const link = event.target.closest('a[data-picker-account]');
    if (!link || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const pane = document.querySelector('#conversation-pane[data-open-chat="true"]');
    if (pane && pane.dataset.conversationId === new URL(link.href).searchParams.get('chat')) {
      event.preventDefault();
      event.stopPropagation();
    }
    dialog.close();
  }, {capture:true});
  life.on(dialog, 'click', event => {
    const target = event.target;
    if (target === dialog || target.closest('[data-picker-close]')) {
      if (target === dialog) {
        const box = dialog.getBoundingClientRect();
        if (event.clientX >= box.left && event.clientX <= box.right && event.clientY >= box.top && event.clientY <= box.bottom) return;
      }
      closePicker();
      return;
    }
    if (target.closest('[data-picker-back]')) {
      show(null);
      if (previousAction?.isConnected) previousAction.focus({preventScroll:true});
      else refresh(0);
      return;
    }
    const person = target.closest('button[data-picker-account]');
    if (person) {
      const form = dialog.querySelector('#new-conversation-form');
      if (!form || form.dataset.busy === 'true') return;
      form.elements.accountId.value = person.dataset.pickerAccount;
      form.elements.accountId.dispatchEvent(new Event('input', {bubbles:true}));
      form.elements.address.value = person.dataset.pickerAddress;
      openStep('newchat', person);
      return;
    }
    const action = target.closest('[data-picker-target]');
    if (action) {
      const id = action.dataset.pickerTarget;
      if (id === 'newchat') void openDirect(action);
      else openStep(id, action);
      return;
    }
    const more = target.closest('[data-picker-more]');
    if (more) load(more.closest('section'), more.dataset.pickerMore);
  });
  // The handle and title dismiss the sheet without stealing vertical scrolling from the list.
  const header = dialog.querySelector('header');
  let drag;
  life.on(header, 'pointerdown', event => {
    if (event.pointerType !== 'touch' || event.target.closest('button')) return;
    drag = {id:event.pointerId, y:event.clientY};
    header.setPointerCapture(event.pointerId);
  });
  life.on(header, 'pointerup', event => {
    if (drag?.id === event.pointerId && event.clientY - drag.y > 60) {
      closePicker();
    }
    drag = undefined;
  });
  life.on(header, 'lostpointercapture', () => { drag = undefined; });
  life.on(dialog, 'cancel', () => {
    closePicker();
  });
  life.on(dialog, 'close', () => {
    if (dialog.open) return;
    cancel();
    if (dialog.querySelector('[data-picker-step="join-room"]:not([hidden])'))
      document.dispatchEvent(new CustomEvent('oie:picker-leave-join'));
  });
  life.cleanup(() => {
    cancel();
    if (dialog.open) dialog.close();
  });
});
