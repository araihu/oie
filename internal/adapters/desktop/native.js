// Native window utilities only. Chat operations keep using the HTTP contract.
(async () => {
  const runtime = await import('/wails/runtime.js');
  const saveFailed = '__OIE_SAVE_FAILED__';
  window.OieDesktop = Object.freeze({
    attachFiles: '__OIE_FLATPAK__' ? (engineID, conversationID, csrf, remaining, imagesOnly) => runtime.Call.ByName(
      'github.com/araihu/oie/internal/adapters/desktop.runtimeService.AttachFiles',
      engineID, conversationID, csrf, remaining, imagesOnly
    ) : undefined,
    notifications: Object.freeze({
      request: () => runtime.Call.ByName('github.com/araihu/oie/internal/adapters/desktop.runtimeService.RequestNotifications'),
      send: (...args) => runtime.Call.ByName('github.com/araihu/oie/internal/adapters/desktop.runtimeService.NotifyMessage', ...args),
      chooseTone: '__OIE_FLATPAK__' ? title => runtime.Call.ByName(
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
