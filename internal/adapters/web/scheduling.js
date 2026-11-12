window.OieRuntime.register('scheduling', life => {
  'use strict';
  const errorToast = message => window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message}}));
  const filter = document.querySelector('[data-schedule-filter]');
  if (filter) {
    const id = 'schedule-conversation';
    const name = 'scheduleConversation';
    const selectionKey = values => JSON.stringify([...values].sort());
    let committed = new FormData(filter).getAll(name);
    let request;
    life.cleanup(() => request?.abort());
    async function updateAgenda(url, values, form) {
      const mutation = !!form;
      const previousURL = location.href;
      const rowID = form?.closest('[data-scheduled-row]')?.dataset.scheduledRow;
      const results = document.getElementById('scheduled-results');
      request?.abort();
      if (!results || (!mutation && selectionKey(values) === selectionKey(committed))) {
        request = null;
        results?.removeAttribute('aria-busy');
        if (results) results.inert = false;
        return;
      }
      const controller = new AbortController();
      request = controller;
      results.setAttribute('aria-busy', 'true');
      results.inert = true;
      const timeout = life.timeout(() => controller.abort(), 10000);
      try {
        const response = await life.fetch(url, {cache: 'no-store', signal: controller.signal, headers:{'HX-Request':'true','X-Oie-Fragment':'scheduled'}});
        if (!response.ok) throw Error('schedule filter');
        const page = new DOMParser().parseFromString(await response.text(), 'text/html');
        const next = page.getElementById('scheduled-results');
        if (request !== controller || life.signal.aborted || !results.isConnected || location.href !== previousURL) return;
        if (!next || next.dataset.engineId !== document.body.dataset.engineId) throw Error('schedule scope');
        const replacement = document.importNode(next, true);
        const workspace = document.getElementById('workspace'), top = workspace.scrollTop;
        window.OieTime.localize(replacement);
        const replace = () => {
          window.Alpine?.destroyTree(results);
          results.replaceWith(replacement);
          window.Alpine?.initTree(replacement);
        };
        if (window.Alpine?.mutateDom) window.Alpine.mutateDom(replace); else replace();
        workspace.scrollTop = top;
        if (!mutation) {
          committed = values;
          window.OieNavigation.addressSearch(url, true);
        } else if (rowID) {
          const rows = [...replacement.querySelectorAll('[data-scheduled-row]')];
          const oldRows = [...results.querySelectorAll('[data-scheduled-row]')];
          const nextRow = rows.find(row => row.dataset.scheduledRow === rowID) || rows[Math.min(rows.length - 1, oldRows.findIndex(row => row.dataset.scheduledRow === rowID))];
          (nextRow?.querySelector('[data-schedule-actions-trigger], .oie-agenda-chat') || filter.querySelector('[data-agenda-filter-control]'))?.focus({preventScroll:true});
        }
        document.dispatchEvent(new Event('oie:navigation-upgrade'));
      } catch {
        if (request !== controller || life.signal.aborted) return;
        request = null;
        if (!mutation) document.getElementById(id)?.dispatchEvent(new CustomEvent('combobox:load', {bubbles:true,detail:{id,values:committed}}));
        window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'danger', message: filter.dataset.filterUnavailable}}));
      } finally {
        clearTimeout(timeout);
        if (request === controller) request = null;
        if (!request) {
          results.removeAttribute('aria-busy');
          results.inert = false;
        }
      }
    }
    life.on(filter, 'combobox:change', ({detail}) => {
      if (detail?.id !== id) return;
      const values = [...detail.values].sort();
      const url = new URL(location.href);
      url.searchParams.delete('scheduleCursor');
      url.searchParams.delete(name);
      for (const value of values) url.searchParams.append(name, value);
      void updateAgenda(url, values);
    });
    life.on(document,'oie:agenda-refresh',event=>void updateAgenda(new URL(location.href),committed,event.detail.form));
    life.on(document,'oie:agenda-settled',event=>{
      request?.abort();
      committed = new URLSearchParams(location.search).getAll(name);
      document.getElementById(id)?.dispatchEvent(new CustomEvent('combobox:load',{bubbles:true,detail:{id,values:committed}}));
      window.OieTime.localize(event.detail.root);
      if(event.detail.source?.matches('[data-agenda-next]')){
        const row=event.detail.root.querySelector('[data-scheduled-row] a');
        row?.focus({preventScroll:true});
        document.getElementById('workspace').scrollTop=0;
      }
    });
  }
  const drawer = document.querySelector('[data-conversation-schedules]');
  const drawerID = 'conversation-schedules';
  let drawerEditRequest;
  if (drawer) {
    const conversationId = drawer.dataset.conversationSchedules;
    const engineId = document.body.dataset.engineId;
    const results = drawer.querySelector('[data-conversation-schedules-results]');
    const status = drawer.querySelector('[data-conversation-schedules-status]');
    const retry = drawer.querySelector('[data-conversation-schedules-retry]');
    const more = drawer.querySelector('[data-conversation-schedules-more]');
    let request, open = false, nextCursor = '', failedCursor = '', loadedPages = 0, retryRefresh = false;
    let drawerGeneration = 0;
    const stop = () => {
      request?.abort();
      request = null;
      results.removeAttribute('aria-busy');
    };
    life.cleanup(stop);
    async function fetchConversationSchedules(cursor, signal) {
      const query = new URLSearchParams({engineId, conversationId, lang: document.documentElement.lang});
      if (cursor) query.set('cursor', cursor);
      const response = await life.fetch('/ui/scheduling/conversation?' + query, {signal});
      if (!response.ok) throw Error('conversation schedules');
      const html = new DOMParser().parseFromString(await response.text(), 'text/html');
      const page = html.querySelector('[data-conversation-schedule-page]');
      if (page?.dataset.engineId !== engineId || page.dataset.conversationId !== conversationId) throw Error('conversation scope');
      for (const link of page.querySelectorAll('[data-conversation-schedule-edit]')) {
        const url = new URL(location.href);
        url.searchParams.set('chat', conversationId);
        url.searchParams.set('editSchedule', link.dataset.conversationScheduleEdit);
        url.searchParams.delete('inbox');
        url.searchParams.delete('open');
        link.href = url.pathname + url.search;
      }
      return page;
    }
    function appendScheduleRows(list, page) {
      const nextList = page.querySelector('[data-schedule-agenda]');
      if (!list || !nextList) return;
      const seen = new Set([...list.children].map(row => row.dataset.scheduledRow));
      for (const row of [...nextList.children]) {
        if (seen.has(row.dataset.scheduledRow)) continue;
        list.append(row);
        seen.add(row.dataset.scheduledRow);
      }
    }
    async function loadConversationSchedules(cursor = '', refresh = false) {
      if (request) return;
      const controller = new AbortController();
      request = controller;
      results.setAttribute('aria-busy', 'true');
      status.textContent = drawer.dataset.loading;
      status.hidden = refresh;
      try {
        const first = await fetchConversationSchedules(cursor, controller.signal);
        const pages = [first];
        while (refresh && pages.length < loadedPages && pages.at(-1).dataset.nextCursor) {
          pages.push(await fetchConversationSchedules(pages.at(-1).dataset.nextCursor, controller.signal));
        }
        if (request !== controller || life.signal.aborted) return;
        const focused = document.activeElement;
        const focusedRow = focused?.closest('[data-scheduled-row]');
        const rowIndex = [...results.querySelectorAll('[data-scheduled-row]')].indexOf(focusedRow);
        if (cursor) {
          appendScheduleRows(results.querySelector('[data-schedule-agenda]'), first);
          loadedPages++;
        } else {
          const list = first.querySelector('[data-schedule-agenda]');
          for (const page of pages.slice(1)) appendScheduleRows(list, page);
          results.replaceChildren(...first.children);
          loadedPages = pages.length;
        }
        window.OieTime.localize(results);
        document.dispatchEvent(new Event('oie:navigation-upgrade'));
        nextCursor = pages.at(-1).dataset.nextCursor || '';
        if (rowIndex >= 0 && !focused.isConnected) {
          const rows = [...results.querySelectorAll('[data-scheduled-row]')];
          const same = rows.find(row => row.dataset.scheduledRow === focusedRow.dataset.scheduledRow);
          const nearby = rows[Math.min(rowIndex, rows.length - 1)];
          const command = focused.dataset.conversationScheduleCommand;
          const selector = command ? `[data-conversation-schedule-command="${CSS.escape(command)}"]` :
            `a[href="${CSS.escape(focused.getAttribute('href') || '')}"]`;
          (same?.querySelector(selector) || (same || nearby)?.querySelector('[data-conversation-schedule-edit]') || drawer.querySelector('a')).focus({preventScroll: true});
        } else if ((!nextCursor && document.activeElement === more) || document.activeElement === retry) {
          results.tabIndex = -1;
          results.focus({preventScroll: true});
        }
        retry.hidden = true;
        more.hidden = !nextCursor;
        status.hidden = true;
      } catch {
        if (request !== controller || life.signal.aborted) return;
        failedCursor = cursor;
        retryRefresh = refresh;
        status.textContent = drawer.dataset.unavailable;
        status.hidden = false;
        more.hidden = true;
        retry.hidden = false;
        if (document.activeElement === document.body) retry.focus({preventScroll: true});
      } finally {
        if (request === controller) stop();
      }
    }
    life.on(window, 'drawer:open', event => {
      if (event.detail?.id !== drawerID) return;
      drawerGeneration++;
      open = true;
      stop();
      drawerEditRequest?.request.abort();
      results.replaceChildren();
      loadedPages = 0;
      retry.hidden = true;
      more.hidden = true;
      loadConversationSchedules();
    });
    for (const name of ['drawer:close', 'drawer:close-request']) {
      life.on(window, name, event => {
        if (event.detail?.id !== drawerID) return;
        const closingGeneration = drawerGeneration;
        const closingRequest = request;
        const closingEdit = drawerEditRequest;
        life.raf(() => {
          if (event.defaultPrevented || closingGeneration !== drawerGeneration) return;
          open = false;
          if (request === closingRequest) stop();
          if (closingEdit && drawerEditRequest === closingEdit) closingEdit.request.abort();
        });
      });
    }
    life.on(retry, 'click', () => loadConversationSchedules(failedCursor, retryRefresh));
    life.on(more, 'click', () => { if (nextCursor) loadConversationSchedules(nextCursor); });
    life.on(drawer, 'oie:schedule-refresh', () => {
      if (!open) return;
      stop();
      loadConversationSchedules('', true);
    });
    life.on(document, 'htmx:before:request', event => {
      const ctx = event.detail.ctx;
      const link = ctx.sourceElement?.closest('[data-conversation-schedule-edit]');
      if (!drawer.contains(link)) return;
      const editBlocked = snapshot().editBlocked !== false;
      if (!open || request || drawerEditRequest || results.querySelector('form[data-busy="true"]') || editBlocked) {
        event.preventDefault();
        if (editBlocked) errorToast(drawer.dataset.editUnavailable);
        return;
      }
      drawerEditRequest = ctx;
      link.setAttribute('aria-busy', 'true');
    }, {capture: true});
    life.on(document, 'htmx:after:request', event => {
      const ctx = event.detail.ctx;
      if (ctx !== drawerEditRequest) return;
      if (!open) { event.preventDefault(); return; }
      const page = new DOMParser().parseFromString(ctx.text || '', 'text/html');
      const root = page.getElementById('schedule-editor');
      const editor = root?.querySelector('input[name="messageId"]');
      if (ctx.response?.status !== 200 || root?.dataset.engineId !== engineId || root.dataset.conversationId !== conversationId ||
          editor?.value !== ctx.sourceElement.dataset.conversationScheduleEdit || snapshot().editBlocked !== false) {
        event.preventDefault();
        errorToast(drawer.dataset.editUnavailable);
      }
    }, {capture: true});
    life.on(document, 'htmx:finally:request', ({detail: {ctx}}) => {
      if (ctx !== drawerEditRequest) return;
      ctx.sourceElement.removeAttribute('aria-busy');
      drawerEditRequest = null;
    }, {capture: true});
  }
  const dialog = document.querySelector('[data-schedule-dialog]');
  if (!dialog) return;
  const composer = document.querySelector('[data-schedule-composer]');
  const picker = composer?.querySelector('[data-attachment-picker]');
  const attach = composer?.querySelector('[data-attach-file]');
  const attachments = composer?.querySelector('[data-composer-attachments]');
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content || '';
  const localTime = dialog.querySelector('#schedule-local-time');
  const offsets = dialog.querySelector('[data-schedule-offset]');
  const title = dialog.querySelector('h2');
  const error = dialog.querySelector('[data-schedule-error]');
  const previewText = dialog.querySelector('[data-schedule-preview-text]');
  const previewTime = dialog.querySelector('[data-schedule-preview-time]');
  const previewFiles = dialog.querySelector('[data-schedule-preview-files]');
  const quote = dialog.querySelector('[data-schedule-quote]');
  const submit = dialog.querySelector('[data-schedule-submit]');
  let activeForm, reviewedComposition, reviewZone, agendaReturnFocus, files = [], locked = false;
  const data = document.body.dataset;

  function wallTime(date) {
    const formatter = new Intl.DateTimeFormat('en-CA', {
      timeZone: reviewZone, hourCycle: 'h23', year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit',
    });
    const values = Object.fromEntries(formatter.formatToParts(date).map(part => [part.type, part.value]));
    return values.year + '-' + values.month + '-' + values.day + 'T' + values.hour + ':' + values.minute;
  }

  function instants(value) {
    const utc = Date.parse(value + 'Z');
    if (!Number.isFinite(utc)) return [];
    const candidates = new Set();
    // Sample both sides of an offset transition. Round-tripping rejects gaps
    // and preserves both instants during a repeated local hour.
    for (const hours of [-36, 0, 36]) {
      const sample = utc + hours * 3600000;
      const offset = Date.parse(wallTime(new Date(sample)) + 'Z') - sample;
      const candidate = utc - offset;
      if (wallTime(new Date(candidate)) === value && candidate > Date.now()) candidates.add(candidate);
    }
    return [...candidates].sort((a, b) => a - b);
  }

  function updateTime() {
    const candidates = instants(localTime.value);
    const previous = offsets.value;
    offsets.replaceChildren();
    offsets.hidden = candidates.length < 2;
    if (!offsets.hidden) {
      offsets.add(new Option(dialog.dataset.timeAmbiguous, ''));
      for (const instant of candidates) {
        const date = new Date(instant);
        const label = window.OieTime.format(date, {hour: 'numeric', minute: 'numeric', timeZone: reviewZone, timeZoneName: 'shortOffset'});
        offsets.add(new Option(label, String(instant)));
      }
      offsets.value = candidates.includes(Number(previous)) ? previous : '';
    }
    const selected = candidates.length === 1 ? candidates[0] : Number(offsets.value);
    previewTime.textContent = selected ? window.OieTime.format(new Date(selected), {hour: 'numeric', minute: '2-digit', timeZone: reviewZone}) : '';
    return selected;
  }

  function fileURL(id, download = false) {
    const query = new URLSearchParams({engineId: data.engineId, conversationId: data.conversationId, attachmentId: id});
    if (download) query.set('download', '1');
    return '/ui/scheduling/file?' + query;
  }

  function attachmentLink(href, name, thumbnail) {
    const link = document.createElement('a');
    link.href = href;
    link.download = name;
    if (thumbnail) {
      const img = document.createElement('img');
      img.src = thumbnail;
      img.alt = name;
      img.width = 224;
      img.height = 128;
      link.append(img);
      link.setAttribute('aria-label', name);
    }
    link.append(name);
    return link;
  }

  function fileLink(file, preview) {
    const raster = ['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'image/avif'].includes(file.mediaType);
    return attachmentLink(fileURL(file.id, true), file.fileName, preview && raster ? fileURL(file.id) : '');
  }

  function snapshot() {
    const detail = {blocked: true};
    composer?.dispatchEvent(new CustomEvent('oie:schedule-snapshot', {detail}));
    return detail;
  }

  function renderFiles() {
    if (!attachments) return;
    attachments.replaceChildren();
    attachments.hidden = !files.length;
    for (const file of files) {
      const item = document.createElement('span');
      const remove = document.createElement('button');
      remove.type = 'button';
      remove.textContent = '×';
      remove.setAttribute('aria-label', dialog.dataset.removeAttachment + ' · ' + file.fileName);
      remove.disabled = locked || composer.dataset.staging === 'true';
      remove.dataset.removeAttachment = file.id;
      item.append(fileLink(file, false), remove);
      attachments.append(item);
    }
    if (attach) attach.disabled = picker.disabled || locked || composer.dataset.staging === 'true';
  }

  function newID() {
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    let ms = Date.now();
    for (let i = 5; i >= 0; i--) { bytes[i] = ms % 256; ms = Math.floor(ms / 256); }
    bytes[6] = (bytes[6] & 15) | 112;
    bytes[8] = (bytes[8] & 63) | 128;
    const raw = Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('');
    return [raw.slice(0, 8), raw.slice(8, 12), raw.slice(12, 16), raw.slice(16, 20), raw.slice(20)].join('-');
  }

  life.on(composer, 'oie:composer-attachments', event => {
    const changed = locked !== event.detail.locked || JSON.stringify(files) !== JSON.stringify(event.detail.files);
    files = event.detail.files;
    locked = event.detail.locked;
    if (changed) renderFiles();
  });
  if (composer) { files = snapshot().files || []; renderFiles(); }
  life.on(attach, 'click', () => picker?.click());
  life.on(attachments, 'click', event => {
    const remove = event.target.closest('[data-remove-attachment]');
    if (!remove || locked) return;
    composer.dispatchEvent(new CustomEvent('oie:attachments-change', {detail: files.filter(file => file.id !== remove.dataset.removeAttachment)}));
    attach?.focus();
  });
  life.on(picker, 'change', async () => {
    const picked = [...picker.files];
    picker.value = '';
    if (!picked.length || locked || composer.dataset.staging === 'true') return;
    if (files.length + picked.length > 16 || picked.some(file => file.size > 20 * 1024 * 1024)) {
      errorToast(dialog.dataset.attachmentLimit);
      return;
    }
    composer.dataset.staging = 'true';
    composer.dispatchEvent(new Event('oie:attachment-staging'));
    renderFiles();
    const next = [...files];
    try {
      for (const file of picked) {
        const id = newID(), form = new FormData();
        form.set('file', file);
        const response = await life.fetch(fileURL(id).replace('/file?', '/stage?'), {
          method: 'POST', headers: {'X-CSRF-Token': csrf}, body: form,
        });
        if (!response.ok) throw Error('staging');
        const metadata = await response.json();
        if (metadata.id !== id || typeof metadata.fileName !== 'string' || metadata.size !== file.size) throw Error('staging');
        next.push(metadata);
      }
    } catch {
      if (!life.signal.aborted) errorToast(dialog.dataset.attachmentFailed);
    } finally {
      if (!life.signal.aborted) {
        composer.dataset.staging = 'false';
        composer.dispatchEvent(new CustomEvent('oie:attachments-change', {detail: next}));
        renderFiles();
      }
    }
  });

  function open(form, initial, text, reply, links, reschedule) {
    activeForm = form;
    reviewZone = window.OieTime.timeZone();
    title.textContent = submit.textContent = reschedule ? dialog.dataset.update : dialog.dataset.create;
    error.hidden = true;
    offsets.value = '';
    previewText.replaceChildren();
    for (const fragment of text.split(/(https?:\/\/[^\s<>()]+)/gi)) {
      if (/^https?:\/\//i.test(fragment)) {
        const link = document.createElement('a');
        link.href = fragment;
        link.textContent = fragment;
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
        previewText.append(link);
      } else {
        previewText.append(fragment);
      }
    }
    quote.hidden = !reply;
    quote.textContent = reply ? [reply.senderName, reply.excerpt].filter(Boolean).join(' · ') : '';
    previewFiles.replaceChildren(...links);
    const requested = Date.parse(initial);
    localTime.value = wallTime(new Date(requested > Date.now() + 60000 ? requested : Date.now() + 3600000));
    dialog.querySelector('[data-schedule-zone]').textContent = reviewZone.replaceAll('_', ' ');
    updateTime();
    dialog.showModal();
    localTime.focus();
  }

  life.on(document, 'oie:schedule-compose', () => {
    const current = snapshot();
    if (current.blocked || (!current.text.trim() && !current.files.length)) return;
    reviewedComposition = current.review;
    const editing = composer.hasAttribute('data-editing-schedule');
    const original = document.getElementById('schedule-edit-data');
    open(composer, original ? JSON.parse(original.textContent).scheduledAt : '', current.text, current.reply, current.files.map(file => fileLink(file, true)), editing);
  });
  function runScheduleAction(row, command, trigger) {
    if (!row.isConnected || dialog.open) return;
    const drawerResults = row.closest('[data-conversation-schedules-results]');
    const results = drawerResults || row.closest('#scheduled-results');
    if (results?.getAttribute('aria-busy') === 'true' || (drawerResults || row).querySelector('form[data-busy="true"]') || drawerEditRequest) return;
    const form = row.querySelector(`form[data-schedule-command="${CSS.escape(command)}"]`);
    if (!form) return;
    trigger.focus({preventScroll: true});
    if (command !== 'reschedule') {
      form.requestSubmit();
      return;
    }
    agendaReturnFocus = trigger;
    const source = row.querySelector('[data-scheduled-quote]');
    const reply = source ? {senderName: source.dataset.sender, excerpt: source.content.textContent} : null;
    const scheduledAt = row.querySelector('[data-scheduled-date]').dateTime;
    const links = Array.from(row.querySelectorAll('[data-message-attachments] a'), link => attachmentLink(link.href, link.download, link.querySelector('img')?.src));
    open(form, form.elements.namedItem('scheduledAt').value || scheduledAt, row.querySelector('[data-scheduled-text]')?.textContent || '', reply, links, true);
  }
  life.on(drawer, 'click', event => {
    const trigger = event.target.closest('[data-conversation-schedule-command]');
    if (trigger) runScheduleAction(trigger.closest('[data-scheduled-row]'), trigger.dataset.conversationScheduleCommand, trigger);
  });
  life.on(document, 'oie:schedule-action', ({target, detail: command}) => {
    const row = target.closest('[data-scheduled-row]');
    const trigger = row?.querySelector('[data-schedule-actions-trigger]');
    if (!trigger) return;
    const run = () => life.raf(() => runScheduleAction(row, command, trigger));
    const menu = target.closest('[role="menu"]');
    // Let the menu release keyboard focus before the modal claims it.
    if (typeof menu?.hidePopover === 'function' && menu.matches(':popover-open')) {
      life.on(menu, 'toggle', run, {once: true});
      trigger.click();
    } else {
      if (trigger.getAttribute('aria-expanded') === 'true') trigger.click();
      run();
    }
  });
  life.on(dialog, 'keydown', event => {
    if (event.key !== 'Escape' || !drawer?.contains(agendaReturnFocus)) return;
    event.stopPropagation();
  });
  life.on(dialog, 'close', () => {
    agendaReturnFocus?.focus({preventScroll: true});
    agendaReturnFocus = null;
  });
  life.on(dialog.querySelector('[data-schedule-close]'), 'click', () => dialog.close());
  life.on(localTime, 'input', () => { error.hidden = true; updateTime(); });
  life.on(offsets, 'change', () => { error.hidden = true; updateTime(); });
  life.on(dialog.querySelector('[data-schedule-confirm]'), 'submit', event => {
    event.preventDefault();
    if (!activeForm?.isConnected) {
      dialog.close();
      errorToast(dialog.dataset.unavailable);
      return;
    }
    const instant = updateTime();
    if (!instant) {
      error.textContent = offsets.hidden ? dialog.dataset.timeInvalid : dialog.dataset.timeAmbiguous;
      error.hidden = false;
      return;
    }
    if (activeForm === composer) {
      const current = snapshot();
      if (current.blocked) return;
      if (current.review !== reviewedComposition) {
        dialog.close();
        errorToast(dialog.dataset.reviewChanged);
        return;
      }
    }
    const input = activeForm.elements.namedItem('scheduledAt');
    input.value = new Date(instant).toISOString();
    dialog.close();
    activeForm.requestSubmit();
    if (activeForm === composer) input.value = '';
  });
  window.OieTime.localize(document);
  life.on(document, 'oie:time-zone', () => window.OieTime.localize(document));
});
