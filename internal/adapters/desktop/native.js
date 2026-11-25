// Native window utilities only. Chat operations keep using the HTTP contract.
(async () => {
  const runtime = await import('/wails/runtime.js');
  const saveFailed = '__OIE_SAVE_FAILED__';
  const flatpak = '__OIE_FLATPAK__';
  const mediaCall = (method, ...args) => runtime.Call.ByName(
    'github.com/araihu/oie/internal/adapters/desktop.runtimeService.' + method, ...args
  );
  async function selectMedia(remaining, signal) {
    const selection = await mediaCall('SelectMedia', remaining);
    const files = [];
    try {
      for (const [index, meta] of (selection.files || []).entries()) {
        let offset = 0;
        const source = new ReadableStream({
          async pull(controller) {
            signal.throwIfAborted();
            if (offset === meta.size) { controller.close(); return; }
            const encoded = await mediaCall('ReadMediaChunk', selection.id, index, offset);
            signal.throwIfAborted();
            const data = atob(encoded);
            if (!data.length || offset + data.length > meta.size) throw Error('incomplete selection');
            const chunk = new Uint8Array(data.length);
            for (let i = 0; i < chunk.length; i++) chunk[i] = data.charCodeAt(i);
            offset += chunk.length;
            controller.enqueue(chunk);
          }
        });
        const blob = await new Response(source).blob();
        files.push(new File([blob], meta.name, {type: meta.type}));
      }
      return {files, limit: selection.limit};
    } finally {
      if (selection.id) await mediaCall('ReleaseMedia', selection.id);
    }
  }
  window.OieDesktop = Object.freeze({
    selectMedia: flatpak ? selectMedia : undefined,
    attachFiles: flatpak ? (engineID, conversationID, csrf, remaining, imagesOnly) => runtime.Call.ByName(
      'github.com/araihu/oie/internal/adapters/desktop.runtimeService.AttachFiles',
      engineID, conversationID, csrf, remaining, imagesOnly
    ) : undefined,
    notifications: Object.freeze({
      request: () => runtime.Call.ByName('github.com/araihu/oie/internal/adapters/desktop.runtimeService.RequestNotifications'),
      send: (...args) => runtime.Call.ByName('github.com/araihu/oie/internal/adapters/desktop.runtimeService.NotifyMessage', ...args),
      chooseTone: flatpak ? title => runtime.Call.ByName(
        'github.com/araihu/oie/internal/adapters/desktop.runtimeService.ChooseNotificationTone', title
      ) : undefined,
      playSound: '__OIE_MAC_SOUND__' ? wav => runtime.Call.ByName(
        'github.com/araihu/oie/internal/adapters/desktop.runtimeService.PlayNotificationSound', wav
      ) : undefined
    }),
    copyDiagnostics: snapshot => runtime.Call.ByName(
      'github.com/araihu/oie/internal/adapters/desktop.runtimeService.CopyDiagnostics', snapshot
    ),
    saveDiagnostics: snapshot => runtime.Call.ByName(
      'github.com/araihu/oie/internal/adapters/desktop.runtimeService.SaveDiagnostics', snapshot
    ),
    saveFile: (address, name) => {
      const url = new URL(address, location.href);
      if (url.origin !== location.origin) return Promise.reject(new Error('invalid_download'));
      return runtime.Call.ByName(
        'github.com/araihu/oie/internal/adapters/desktop.runtimeService.SaveFile',
        url.pathname + url.search, name
      );
    }
  });
  window.dispatchEvent(new Event('oie:desktop-ready'));
  const save = (url, name) => window.OieDesktop.saveFile(url.href, name)
    .catch(() => window.dispatchEvent(new CustomEvent('notify', {
      detail: {kind: 'toast', tone: 'danger', message: saveFailed}
    })));
  const external = value => {
    let url;
    try { url = new URL(value, location.href); } catch { return false; }
    if (url.protocol !== 'https:' && url.protocol !== 'http:') return false;
    void runtime.Browser.OpenURL(url.href);
    return true;
  };
  const open = window.open.bind(window);
  window.open = (url, ...options) => external(url) ? null : open(url, ...options);
  document.addEventListener('click', event => {
    const anchor = event.target.closest?.('a[href]');
    if (!anchor) return;
    const url = new URL(anchor.href, location.href);
    if (anchor.hasAttribute('download') && url.origin === location.origin) {
      event.preventDefault();
      event.stopImmediatePropagation();
      void save(url, anchor.download);
      return;
    }
    if (url.origin === location.origin || (url.protocol !== 'https:' && url.protocol !== 'http:')) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    external(url.href);
  }, true);
})();
