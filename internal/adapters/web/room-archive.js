(() => {
 'use strict';
 function current() {
  return document.getElementById('message-timeline')?.dataset;
 }
 async function request(life, payload, signal, method = 'POST') {
  const {engineId, accountId, conversationId} = document.body.dataset;
  const csrf = document.querySelector('meta[name="oie-csrf"]')?.content;
  const timeline = current();
  if (!timeline?.roomArchive || !csrf || !conversationId) return null;
  const response = await life.fetch('/ui/room-archive/' + encodeURIComponent(conversationId), {
   method, signal, credentials: 'same-origin', cache: 'no-store', redirect: 'error',
   headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'X-Oie-Engine-ID': engineId, 'X-Oie-Account-ID': accountId},
   body: JSON.stringify(payload),
  });
  if (!response.ok || !response.headers.get('Content-Type')?.startsWith('application/json')) throw Error('archive');
  const result = await response.json();
  const archive = method === 'PUT' ? result : result.archive;
  if (archive?.conversationId !== conversationId || life.signal.aborted || signal?.aborted || timeline !== current()) return null;
  if (method === 'PUT' && !['gradual', 'on_demand'].includes(archive.mode)) throw Error('archive mode');
  timeline.roomArchive = archive.availability;
  timeline.archiveTextSearch = archive.textSearch;
  timeline.archiveInitial = String(archive.initialComplete);
  timeline.archiveComplete = String(archive.olderComplete);
  timeline.archiveCovered = archive.oldestCoveredAt ? String(Date.parse(archive.oldestCoveredAt)) : '';
  return result;
 }
 window.OieRoomArchive = {
  request,
  older: () => {
   const state = current();
   return !!state?.roomArchive && ['supported','unknown'].includes(state.roomArchive) && state.archiveComplete !== 'true';
  },
  search: () => {
   const state = current();
   return !!state?.roomArchive && ['supported','unknown'].includes(state.archiveTextSearch);
  },
  partial: () => !!current()?.roomArchive && current().archiveComplete !== 'true',
 };
 window.OieRuntime.register('room-archive', life => {
  const state = current();
  if (!state?.roomArchive) return;
  if (state.archiveInitial !== 'true' && ['supported','unknown'].includes(state.roomArchive)) {
   request(life, {action:'focus'}).then(result => {
    if (result?.outcome === 'stored') document.dispatchEvent(new Event('oie:refresh-request'));
   }).catch(() => {});
  }
  let saving = false;
  life.on(document, 'click', event => {
   if (saving && event.target.matches?.('[data-room-archive-mode]')) event.preventDefault();
  }, true);
  life.on(document, 'change', async event => {
   const toggle = event.target.closest?.('[data-room-archive-mode]');
   if (!toggle || saving) return;
   const mode = toggle.checked ? 'gradual' : 'on_demand';
   if (mode === toggle.dataset.roomArchiveMode) return;
   saving = true;
   toggle.setAttribute('aria-disabled', 'true');
   let confirmed;
   try {
    const archive = await request(life, {mode}, undefined, 'PUT');
    confirmed = archive?.mode;
   } catch {
    if (!life.signal.aborted) {
     window.dispatchEvent(new CustomEvent('notify', {detail:{kind:'toast',tone:'danger',message:toggle.dataset.failed}}));
    }
   } finally {
    saving = false;
    if (!life.signal.aborted) {
     const currentToggle = document.getElementById(toggle.id) ?? toggle;
     if (confirmed) currentToggle.dataset.roomArchiveMode = confirmed;
     currentToggle.checked = currentToggle.dataset.roomArchiveMode === 'gradual';
     currentToggle.removeAttribute('aria-disabled');
    }
   }
  });
 });
})();
