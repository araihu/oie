window.OieRuntime.register('draft-link-preview', life => {
  'use strict';
  const region = document.querySelector('[data-draft-link-preview]');
  if (!region) return;

  const form = region.closest('form');
  const input = document.getElementById('message-composer');
  const title = region.querySelector('[data-draft-link-title]');
  const description = region.querySelector('[data-draft-link-description]');
  const host = region.querySelector('[data-draft-link-host]');
  const image = region.querySelector('[data-draft-link-image]');
  const engineId = document.body.dataset.engineId;
  const csrf = form.elements.namedItem('csrf')?.value;
  const imageTypes = new Set(['image/png', 'image/jpeg', 'image/gif']);
  let current = '', dismissed = '', timer, request;

  function firstLink(text) {
    const candidates = text.matchAll(/https?:\/\/[^\s<>"\x00-\x1f]+/gi);
    for (const [candidate] of candidates) {
      const address = candidate.replace(/[.,;!)\]}]+$/, '');
      if (address.length > 4096) continue;
      try {
        const url = new URL(address);
        if (url.hostname && !url.username && !url.password && url.href.length <= 4096) return url;
      } catch { /* Keep looking for a complete URL. */ }
    }
    return null;
  }

  function stopRequest() {
    clearTimeout(timer);
    request?.abort();
    request = null;
  }

  function hideImage() {
    image.hidden = true;
    image.removeAttribute('src');
  }

  function fillMetadata(result) {
    if (typeof result.title === 'string' && result.title.trim()) title.textContent = result.title;
    if (typeof result.description === 'string') description.textContent = result.description;
    const hostname = new URL(title.href).hostname;
    host.textContent = title.textContent === hostname ? '' : hostname;
    const raster = result.image;
    if (imageTypes.has(raster?.mediaType) && typeof raster.data === 'string' &&
        raster.data.length <= 174764 && /^[A-Za-z0-9+/]+={0,2}$/.test(raster.data)) {
      image.src = 'data:' + raster.mediaType + ';base64,' + raster.data;
      image.hidden = false;
    }
  }

  async function resolve(address) {
    const controller = new AbortController();
    request = controller;
    try {
      const response = await life.fetch('/ui/link-preview', {
        method: 'POST',
        headers: {'Content-Type': 'application/json', 'X-Oie-Engine-ID': engineId, 'X-CSRF-Token': csrf},
        body: JSON.stringify({url: address}),
        signal: controller.signal,
      });
      if (response.status !== 200) return;
      const result = await response.json();
      if (request === controller && !life.signal.aborted && result.url === address) fillMetadata(result);
    } catch { /* The link remains available when metadata cannot be loaded. */ }
    finally {
      if (request === controller) request = null;
    }
  }

  function update() {
    const url = input.disabled || input.readOnly ? null : firstLink(input.value);
    const address = url?.href || '';
    if (address === current) return;
    current = address;
    stopRequest();
    hideImage();
    if (!url) dismissed = '';
    if (!url || address === dismissed) {
      region.hidden = true;
      return;
    }
    dismissed = '';
    title.href = address;
    title.textContent = url.pathname === '/' ? url.hostname : url.pathname;
    host.textContent = title.textContent === url.hostname ? '' : url.hostname;
    description.textContent = '';
    region.hidden = false;
    if (region.hasAttribute('data-fetch-preview') && engineId && csrf) timer = life.timeout(() => resolve(address), 500);
  }

  life.on(input, 'input', event => { if (!event.isComposing) update(); });
  life.on(input, 'compositionend', update);
  life.on(form, 'oie:composer-preview-change', update);
  life.on(image, 'error', hideImage);
  life.on(region.querySelector('[data-dismiss-draft-link]'), 'click', () => {
    dismissed = current;
    stopRequest();
    region.hidden = true;
    input.focus({preventScroll: true});
  });
  life.cleanup(stopRequest);
  update();
});
