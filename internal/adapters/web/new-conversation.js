window.OieRuntime.register('new-conversation-picker', life => {
  const dialog = document.querySelector('#new-conversation-picker');
  const trigger = document.querySelector('[data-new-conversation]');
  if (!dialog || !trigger) return;

  const search = dialog.querySelector('#picker-search');
  const sections = [...dialog.querySelectorAll('[data-picker-section]')];
  const accounts = new Map([...dialog.querySelector('[data-picker-accounts]').content.children]
    .map(node => [node.dataset.account, node.textContent]));
  let controller, timer;

  function cancel() {
    clearTimeout(timer);
    controller?.abort();
  }

  function clearSection(section) {
    section.hidden = false;
    section.querySelector('[data-picker-list]').replaceChildren();
    section.querySelector('[role=status]').textContent = dialog.dataset.loading;
  }

  async function load(section, cursor = '') {
    const group = section.dataset.pickerSection;
    const term = search.value.trim();
    const signal = controller.signal;
    const list = section.querySelector('[data-picker-list]');
    const status = section.querySelector('[role=status]');
    const params = new URLSearchParams({
      group, term, cursor, account: dialog.dataset.account, lang: dialog.dataset.locale, theme: dialog.dataset.theme
    });
    status.textContent = dialog.dataset.loading;
    const previous = list.querySelector('[data-picker-more]');
    if (previous) previous.disabled = true;
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
      previous?.remove();
      list.append(fragment.content);
      document.dispatchEvent(new CustomEvent('oie:picker-content', {detail:{root:list}}));
      status.textContent = '';
      if (!list.childElementCount) {
        section.hidden = group === 'chats' && sections.length > 1;
        if (!section.hidden) status.textContent = term || group === 'chats' ? dialog.dataset.noMatch : dialog.dataset.empty;
      }
      if (cursor) first?.focus();
    } catch {
      if (signal.aborted || life.signal.aborted) return;
      status.textContent = dialog.dataset.unavailable + ' ';
      const retry = document.createElement('button');
      retry.type = 'button';
      retry.textContent = dialog.dataset.retry;
      retry.onclick = () => load(section, cursor);
      status.append(retry);
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

  function openPicker() {
    if (dialog.open) return;
    search.value = '';
    dialog.querySelector('details')?.removeAttribute('open');
    // Opening the mobile keyboard here would cover the shortcuts and contacts.
    const mobile = matchMedia('(max-width:720px), (pointer:coarse) and (max-height:600px)').matches;
    const initial = mobile ? dialog.querySelector('h2') : search;
    initial.setAttribute('autofocus', '');
    dialog.showModal();
    initial.focus({preventScroll:true});
    initial.removeAttribute('autofocus');
    refresh(0);
  }
  life.on(trigger, 'click', openPicker);
  if (new URLSearchParams(location.search).get('open') === 'conversation-picker') openPicker();
  life.on(search, 'input', () => refresh(160));
  life.on(dialog, 'click', event => {
    const target = event.target;
    if (target === dialog || target.closest('[data-picker-close]')) {
      if (target === dialog) {
        const box = dialog.getBoundingClientRect();
        if (event.clientX >= box.left && event.clientX <= box.right && event.clientY >= box.top && event.clientY <= box.bottom) return;
      }
      dialog.close();
      trigger.focus({preventScroll:true});
      return;
    }
    const action = target.closest('[data-picker-drawer]');
    if (action) {
      const id = action.dataset.pickerDrawer;
      if (id === 'newchat' && search.value.includes('@')) {
        const address = document.getElementById('peer-address');
        if (address) address.value = search.value.trim();
      }
      dialog.close();
      document.dispatchEvent(new CustomEvent('oie:new-conversation', {detail:{id}}));
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
      dialog.close();
      trigger.focus({preventScroll:true});
    }
    drag = undefined;
  });
  life.on(header, 'lostpointercapture', () => { drag = undefined; });
  life.on(dialog, 'cancel', () => {
    dialog.close();
    trigger.focus({preventScroll:true});
  });
  life.on(dialog, 'close', () => { if (!dialog.open) cancel(); });
  life.cleanup(() => {
    cancel();
    if (dialog.open) dialog.close();
  });
});
