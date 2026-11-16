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
});
