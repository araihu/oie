window.OieRuntime.register('message-retractions', life => {
  const id = 'retract-message-dialog';
  const dialog = document.getElementById(id);
  const form = dialog?.querySelector('[data-retract-form]');
  if (!form) return;

  let selected, busy = false;
  const status = form.querySelector('[data-retract-status]');
  const submit = form.querySelector('button[type=submit]');
  const modal = action => window.dispatchEvent(new CustomEvent('modal:' + action, {detail: {id}}));
  const sameConversation = scope => document.body.dataset.engineId === scope.engineId &&
    document.body.dataset.conversationId === scope.conversationId;

  life.on(document, 'click', event => {
    const action = event.target.closest('[id^="retract-message-"][role="menuitem"]');
    const row = action?.closest('[data-message-id]');
    if (!row || busy) return;
    event.preventDefault();
    selected = {
      messageId: row.dataset.messageId,
      revisionId: row.dataset.messageRevision,
      conversationId: row.dataset.messageConversation,
      engineId: document.body.dataset.engineId,
      csrf: document.querySelector('meta[name="oie-csrf"]')?.content || ''
    };
    status.textContent = '';
    action.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true, cancelable: true}));
    life.raf(() => modal('open'));
  });

  life.on(form, 'submit', async event => {
    event.preventDefault();
    if (!selected || busy || !sameConversation(selected)) return;
    busy = true;
    submit.setAttribute('aria-disabled', 'true');
    status.textContent = form.dataset.pending;
    try {
      const response = await fetch('/ui/messages/retract', {
        method: 'POST', keepalive: true, body: new URLSearchParams(selected), headers: {Accept: 'application/json'}
      });
      const result = await response.json();
      if (life.signal.aborted || !sameConversation(selected)) return;
      if (!response.ok || !result.ok || result.messageId !== selected.messageId) {
        status.textContent = result.message || form.dataset.failed;
        return;
      }
      modal('close');
      window.dispatchEvent(new CustomEvent('notify', {detail: {kind: 'toast', tone: 'success', message: result.message}}));
      document.dispatchEvent(new CustomEvent('oie:refresh-request'));
    } catch {
      if (!life.signal.aborted) status.textContent = form.dataset.failed;
    } finally {
      busy = false;
      if (!life.signal.aborted) submit.removeAttribute('aria-disabled');
    }
  });

  const reveals = new WeakMap();
  life.on(document, 'toggle', async event => {
    const details = event.target;
    if (!details.matches?.('[data-reveal-retracted]')) return;
    const result = details.querySelector('[data-reveal-result]');
    reveals.get(details)?.abort();
    if (!details.open) {
      result.replaceChildren();
      return;
    }
    const request = new AbortController();
    reveals.set(details, request);
    const scope = {
      messageId: details.dataset.revealRetracted,
      conversationId: details.dataset.revealConversation,
      engineId: document.body.dataset.engineId
    };
    const status = details.querySelector('[role=status]');
    status.textContent = details.dataset.revealLoading;
    const active = () => !life.signal.aborted && !request.signal.aborted && details.isConnected && details.open && document.body.dataset.engineId === scope.engineId;
    try {
      const params = new URLSearchParams({...scope, lang: document.documentElement.lang});
      const response = await life.fetch('/ui/messages/retracted-original?' + params, {signal: request.signal, cache: 'no-store'});
      if (!response.ok) throw Error();
      const page = new DOMParser().parseFromString(await response.text(), 'text/html');
      const content = page.querySelector('[data-retracted-content]');
      if (content?.dataset.retractedContent !== scope.messageId || content.dataset.retractedConversation !== scope.conversationId) throw Error();
      if (!active()) return;
      status.textContent = '';
      result.replaceChildren(content);
    } catch {
      if (active()) status.textContent = details.dataset.revealFailed;
    }
  }, true);
});
