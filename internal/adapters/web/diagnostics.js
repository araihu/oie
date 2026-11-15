const path = '/ui/diagnostics/log';
const filename = 'oie-diagnostics.jsonl';
let active;

export function open() {
  const panel = document.querySelector('[data-diagnostics]');
  if (!panel || active?.panel === panel) return;
  close();

  const controls = new AbortController();
  const find = name => panel.querySelector('[data-diagnostics-' + name + ']');
  const status = find('status'), records = find('records'), hours = find('hours');
  const source = find('source'), access = find('access'), keyInput = access.querySelector('input');
  const refresh = find('refresh'), copy = find('copy'), download = find('download');
  let request, key = '', snapshot = '';
  active = { panel, close: () => {
    controls.abort();
    request?.abort();
    keyInput.value = '';
    access.hidden = true;
    display('');
  } };

  function display(text) {
    snapshot = text;
    records.textContent = text;
    records.scrollTop = 0;
    copy.disabled = download.disabled = !text;
  }

  async function load() {
    request?.abort();
    const read = request = new AbortController();
    display('');
    status.textContent = panel.dataset.loading;
    records.setAttribute('aria-busy', 'true');
    const search = new URLSearchParams({ hours: hours.value });
    if (source?.value === 'server') search.set('source', 'server');
    const headers = { 'X-Oie-Diagnostics': '1' };
    if (key) headers['X-Oie-Diagnostics-Key'] = key;
    try {
      const result = await fetch(path + '?' + search, {
        headers, cache: 'no-store', redirect: 'error', signal: read.signal
      });
      if (read.signal.aborted || !panel.isConnected) return;
      if (result.status === 403) {
        key = '';
        access.hidden = false;
        status.textContent = panel.dataset.enterKey;
        return;
      }
      if (!result.ok || result.headers.get('content-type') !== 'application/x-ndjson') throw new Error();
      const text = await result.text();
      if (read.signal.aborted || !panel.isConnected) return;
      access.hidden = true;
      display(text);
      status.textContent = !text ? panel.dataset.empty :
        result.headers.get('X-Oie-Diagnostics-Truncated') === '1' ? panel.dataset.truncated : '';
    } catch {
      if (!read.signal.aborted && panel.isConnected) status.textContent = panel.dataset.unavailable;
    } finally {
      if (request === read && !read.signal.aborted) records.removeAttribute('aria-busy');
    }
  }

  const on = (node, name, fn) => node?.addEventListener(name, fn, { signal: controls.signal });
  on(refresh, 'click', load);
  on(hours, 'change', load);
  on(source, 'change', () => {
    key = '';
    keyInput.value = '';
    access.hidden = true;
    void load();
  });
  on(access, 'submit', event => {
    event.preventDefault();
    key = keyInput.value;
    keyInput.value = '';
    if (key) void load();
  });
  on(copy, 'click', async () => {
    if (!snapshot) return;
    try {
      if (window.OieDesktop?.copyDiagnostics) {
        if (!await window.OieDesktop.copyDiagnostics(snapshot)) throw new Error();
      } else {
        await navigator.clipboard.writeText(snapshot);
      }
      if (!controls.signal.aborted) status.textContent = panel.dataset.copied;
    } catch {
      if (!controls.signal.aborted) status.textContent = panel.dataset.copyFailed;
    }
  });
  on(download, 'click', async () => {
    if (!snapshot) return;
    if (window.OieDesktop?.saveDiagnostics) {
      try {
        await window.OieDesktop.saveDiagnostics(snapshot);
      } catch {
        if (!controls.signal.aborted) status.textContent = panel.dataset.downloadFailed;
      }
      return;
    }
    const url = URL.createObjectURL(new Blob([snapshot], { type: 'application/x-ndjson' }));
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;
    link.hidden = true;
    panel.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  void load();
}

export function close() {
  active?.close();
  active = undefined;
}
