(() => {
  'use strict';
  const runtime = window.OieRuntime;
  const emoji = window.OieEmojiPicker;
  const languages = ['en', 'pt-BR'];
  const preferenceKey = 'oie.device.emoji-shortcut-languages.v1';
  const indices = new WeakMap();
  let enabled = languages, preferenceNotSaved = false;

  function decodePreference(value) {
    try {
      const parsed = JSON.parse(value);
      if (Array.isArray(parsed) && parsed.every(lang => languages.includes(lang))) {
        return languages.filter(lang => parsed.includes(lang));
      }
    } catch {}
    return languages;
  }

  function token(input) {
    if (!enabled.length || input.selectionStart !== input.selectionEnd) return null;
    const caret = input.selectionStart;
    const before = input.value.slice(0, caret);
    const match = before.match(/(?:^|[\s([{])(:([\p{L}\p{M}\p{N}_+-]{1,80}):?)$/u);
    if (!match) return null;
    const [, shortcode, query] = match;
    const start = caret - shortcode.length;
    const suffix = shortcode.endsWith(':') ? '' : input.value.slice(caret).match(/^[\p{L}\p{M}\p{N}_+-]*:?/u)[0];
    const end = caret + suffix.length;
    return {start, end, query, key: start + ':' + end + ':' + shortcode};
  }

  const normalize = value => emoji.fold(value).replace(/[_-]+/g, '-');
  function search(catalog, query, locale) {
    if (!indices.has(catalog)) {
      const index = [];
      for (const {base} of catalog.families.values()) {
        const shortcuts = catalog.shortcuts.get(base.glyph);
        if (!shortcuts) continue;
        const names = {};
        for (const lang of languages) {
          names[lang] = shortcuts[lang].split(' | ').map(term => {
            const label = term.toLowerCase().replace(/[^\p{L}\p{M}\p{N}+-]+/gu, '-');
            return {label, key: normalize(label)};
          });
        }
        index.push({item: base, names});
      }
      indices.set(catalog, index);
    }
    const term = normalize(query);
    const order = enabled.includes(locale) ? [locale, ...enabled.filter(lang => lang !== locale)] : enabled;
    const exact = [], prefixes = [];
    for (const {item, names} of indices.get(catalog)) {
      let match, prefix;
      for (const lang of order) {
        match = names[lang].find(name => name.key === term);
        if (match) break;
        if (prefixes.length < 8) prefix ??= names[lang].find(name => name.key.startsWith(term));
      }
      if (match) {
        exact.push({item, label: match.label});
        if (exact.length === 8) break;
      } else if (prefix) prefixes.push({item, label: prefix.label});
    }
    return [...exact, ...prefixes].slice(0, 8);
  }

  // A shortcode takes precedence over a mention query that spans multiple words.
  window.OieEmojiCompletion = {hasToken: input => token(input) !== null};
  runtime.register('emoji-completion', life => {
    let storageDenied = preferenceNotSaved;
    if (!preferenceNotSaved) {
      try {
        enabled = decodePreference(localStorage.getItem(preferenceKey));
      } catch { storageDenied = true; }
    }
    const languagePicker = document.getElementById('emoji-shortcut-languages');
    const languageOptions = languagePicker?.querySelector('[role=listbox]');
    languageOptions?.setAttribute('aria-multiselectable', 'true');
    // The option carries selection semantics. The checkbox is its visual indicator.
    for (const indicator of languageOptions?.querySelectorAll('input[type=checkbox]') ?? []) {
      indicator.inert = true;
    }
    const form = document.getElementById('send-message-form');
    function applyPreference() {
      if (storageDenied) {
        const warning = document.querySelector('[data-device-preference-warning]');
        if (warning) warning.hidden = false;
      }
      form?.dispatchEvent(new Event('oie:emoji-languages'));
    }
    function syncLanguagePicker() {
      languagePicker?.dispatchEvent(new CustomEvent('combobox:load', {
        bubbles: true, detail: {id: languagePicker.id, values: enabled},
      }));
      applyPreference();
    }
    syncLanguagePicker();
    life.on(languagePicker, 'combobox:change', event => {
      if (event.detail?.id !== languagePicker.id || !Array.isArray(event.detail.values)) return;
      enabled = languages.filter(lang => event.detail.values.includes(lang));
      try {
        localStorage.setItem(preferenceKey, JSON.stringify(enabled));
        preferenceNotSaved = false;
      } catch { preferenceNotSaved = storageDenied = true; }
      applyPreference();
    });
    // Let the picker handle Escape before the enclosing drawer can close.
    let dismissingLanguages = false;
    life.on(languagePicker, 'keydown', event => {
      if (event.key !== 'Escape' || !languagePicker.querySelector('[role=combobox][aria-expanded=true]')) return;
      dismissingLanguages = true;
      life.timeout(() => { dismissingLanguages = false; }, 0);
    });
    life.on(document, 'drawer:close-request', event => {
      if (dismissingLanguages && event.detail?.id === 'settings') event.preventDefault();
    });
    life.on(window, 'storage', event => {
      if (event.key !== preferenceKey && event.key !== null) return;
      enabled = decodePreference(event.newValue);
      preferenceNotSaved = false;
      syncLanguagePicker();
    });

    const input = document.getElementById('message-composer');
    const root = document.getElementById('oie-emoji-completion');
    if (!form || !input || !root) return;
    const composer = root.parentElement;
    const panel = root.querySelector('.oie-emoji-completion-panel');
    const list = root.querySelector('[role=listbox]');
    const status = root.querySelector('[role=status]');
    const template = root.querySelector('template');
    const locale = document.documentElement.lang;
    let catalog, loading = false, composing = false, failed = '', dismissed = '';
    let rendered = '', shownToken = '', visible = [], active = -1;
    const writable = () => input.isConnected && !input.disabled && !input.readOnly &&
      form.dataset.busy !== 'true' && form.dataset.composeAllowed === 'true';
    function current() {
      if (!writable() || composing || document.activeElement !== input) return null;
      const value = token(input);
      return value?.key === dismissed ? null : value;
    }
    function hide() {
      root.hidden = true;
      rendered = '';
      shownToken = '';
      if (input.getAttribute('aria-controls') === list.id) {
        for (const attr of ['aria-controls', 'aria-autocomplete', 'aria-haspopup', 'aria-activedescendant']) {
          input.removeAttribute(attr);
        }
      }
    }
    function dismiss() {
      dismissed = token(input)?.key ?? '';
      hide();
    }
    function position() {
      if (root.hidden) return;
      const top = composer.getBoundingClientRect().top - (window.visualViewport?.offsetTop ?? 0);
      root.style.setProperty('--oie-completion-max-height', Math.max(48, top - 12) + 'px');
    }
    function setActive(index, scroll = false) {
      active = (index + visible.length) % visible.length;
      for (const [i, option] of Array.from(list.children).entries()) {
        option.setAttribute('aria-selected', String(i === active));
      }
      const selected = list.children[active];
      input.setAttribute('aria-activedescendant', selected.id);
      if (scroll) {
        const row = selected.getBoundingClientRect(), bounds = panel.getBoundingClientRect();
        if (row.top < bounds.top) panel.scrollTop += row.top - bounds.top;
        else if (row.bottom > bounds.bottom) panel.scrollTop += row.bottom - bounds.bottom;
      }
    }
    async function load() {
      loading = true;
      try {
        catalog = await emoji.loadCatalog(root.dataset.emojiUrl, locale, life);
      } catch {
        failed = current()?.key ?? '';
      } finally {
        loading = false;
        if (!life.signal.aborted) update();
      }
    }
    function update() {
      const value = current();
      if (!value || value.key === failed) { hide(); return; }
      const key = value.key + ':' + enabled.join(',') + ':' + Boolean(catalog);
      if (key === rendered) return;
      if (!catalog && !loading) void load();
      visible = catalog ? search(catalog, value.query, locale) : [];
      if (catalog && !visible.length) {
        hide();
        rendered = key;
        return;
      }
      rendered = key;
      shownToken = value.key;
      const options = document.createDocumentFragment();
      for (const [index, {item, label}] of visible.entries()) {
        const option = template.content.firstElementChild.cloneNode(true);
        const shortcut = ':' + label + ':';
        option.id = list.id + '-' + index;
        option.dataset.emojiOption = String(index);
        option.setAttribute('aria-label', item.name + ' ' + shortcut);
        option.querySelector('[data-emoji-glyph]').textContent = item.glyph;
        option.querySelector('[data-emoji-shortcode]').textContent = shortcut;
        options.append(option);
      }
      list.replaceChildren(options);
      list.hidden = visible.length === 0;
      status.textContent = catalog ? '' : root.dataset.emojiLoading;
      root.hidden = false;
      position();
      panel.scrollTop = 0;
      if (visible.length) {
        input.setAttribute('aria-controls', list.id);
        input.setAttribute('aria-autocomplete', 'list');
        input.setAttribute('aria-haspopup', 'listbox');
        setActive(0);
      }
    }
    function choose(index) {
      const value = current(), glyph = visible[index]?.item.glyph;
      if (!value || !glyph || value.key !== shownToken) return;
      hide();
      emoji.insert(input, glyph, value.start, value.end);
      emoji.remember(glyph);
    }

    life.on(input, 'input', event => {
      dismissed = failed = '';
      composing = event.isComposing === true;
      update();
    }, {capture: true});
    life.on(input, 'compositionstart', () => { composing = true; hide(); });
    life.on(input, 'compositionend', () => { composing = false; update(); });
    life.on(input, 'keydown', event => {
      if (root.hidden || !current() || event.isComposing || event.keyCode === 229) return;
      if (event.key === 'Tab') { hide(); return; }
      if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      if (!['Enter', 'ArrowDown', 'ArrowUp', 'Escape'].includes(event.key)) return;
      if (!visible.length && !['Enter', 'Escape'].includes(event.key)) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      if (event.key === 'Escape') dismiss();
      else if (event.key === 'Enter' && visible.length) choose(active);
      else if (visible.length) setActive(active + (event.key === 'ArrowDown' ? 1 : -1), true);
    }, {capture: true});
    life.on(input, 'focus', update);
    life.on(input, 'blur', hide);
    life.on(input, 'click', () => { dismissed = failed = ''; update(); });
    life.on(input, 'select', update);
    life.on(input, 'keyup', event => {
      if (!['Escape', 'Tab', 'Enter', 'ArrowDown', 'ArrowUp'].includes(event.key)) update();
    });
    life.on(list, 'pointerdown', event => { if (event.target.closest('[data-emoji-option]')) event.preventDefault(); });
    life.on(list, 'click', event => {
      const option = event.target.closest('[data-emoji-option]');
      if (option) choose(Number(option.dataset.emojiOption));
    });
    life.on(document, 'pointerdown', event => {
      if (!root.hidden && event.target !== input && !root.contains(event.target)) dismiss();
    }, {capture: true});
    life.on(form, 'oie:emoji-languages', update);
    life.on(form, 'oie:mentions-restore', hide);
    life.on(window, 'resize', position);
    life.on(window.visualViewport, 'resize', position);
    life.on(window.visualViewport, 'scroll', position);
    const resize = new ResizeObserver(position);
    resize.observe(composer);
    const observer = new MutationObserver(() => { if (!writable()) hide(); });
    observer.observe(input, {attributes: true, attributeFilter: ['disabled', 'readonly']});
    observer.observe(form, {attributes: true, attributeFilter: ['data-busy', 'data-compose-allowed']});
    life.cleanup(() => { resize.disconnect(); observer.disconnect(); hide(); });
  });
})();
