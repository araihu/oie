(() => {
  'use strict';
  const storageKey = 'oie.device.time-zone.v1';
  const formatters = new Map();
  const deviceTimeZone = () => new Intl.DateTimeFormat().resolvedOptions().timeZone;
  let timeZone = '';
  let systemTimeZone = deviceTimeZone();

  function validZone(value) {
    if (typeof value !== 'string' || !/^[A-Za-z][A-Za-z0-9_+/-]{0,127}$/.test(value)) return '';
    try {
      new Intl.DateTimeFormat('en', {timeZone: value});
      return value;
    } catch {
      return '';
    }
  }

  try { timeZone = validZone(localStorage.getItem(storageKey)); } catch {}

  function formatter(locale, options) {
    const key = JSON.stringify([locale, options]);
    if (!formatters.has(key)) {
      formatters.set(key, new Intl.DateTimeFormat(locale, {...options, timeZone: timeZone || undefined}));
    }
    return formatters.get(key);
  }

  function format(date, options) {
    return formatter(document.documentElement.lang || undefined, options).format(date);
  }

  function dateKey(date, granularity) {
    const options = granularity === 'month'
      ? {year: 'numeric', month: '2-digit'}
      : {year: 'numeric', month: '2-digit', day: '2-digit'};
    const parts = formatter('en-u-ca-gregory-nu-latn', options).formatToParts(date);
    const values = Object.fromEntries(parts.map(({type, value}) => [type, value]));
    return granularity === 'month' ? values.year + '-' + values.month : values.year + '-' + values.month + '-' + values.day;
  }

  const clock = {hour: '2-digit', minute: '2-digit'};
  const dateAndTime = {dateStyle: 'medium', timeStyle: 'short'};
  const editTime = {year: 'numeric', month: 'short', day: 'numeric', ...clock, second: '2-digit'};
  const fullDates = '[data-viewer-date],[data-capability-date],[data-details-date],[data-shared-source-date],[data-message-search-time]';

  function localize(root) {
    for (const node of root.querySelectorAll('time[datetime]:not([data-day])')) {
      const date = new Date(node.dateTime);
      if (Number.isNaN(date.getTime())) continue;
      const options = node.hasAttribute('data-history-date') ? editTime : node.matches(fullDates) ? dateAndTime : clock;
      const label = format(date, options);
      if (node.textContent !== label) node.textContent = label;
    }
  }

  function refreshTimes() {
    formatters.clear();
    document.dispatchEvent(new Event('oie:time-zone'));
  }

  function change(value) {
    if (timeZone === value) return;
    timeZone = value;
    refreshTimes();
  }

  window.OieTime = {format, dateKey, localize};
  window.addEventListener('focus', () => {
    const nextSystemTimeZone = deviceTimeZone();
    if (nextSystemTimeZone === systemTimeZone) return;
    systemTimeZone = nextSystemTimeZone;
    if (!timeZone) refreshTimes();
  });
  window.addEventListener('storage', event => {
    if (event.key === storageKey || event.key === null) change(validZone(event.newValue));
  });

  window.OieRuntime?.register('presentation-time', life => {
    const select = document.getElementById('settings-time-zone');
    if (!select) return;

    const systemZone = validZone(systemTimeZone);
    const zones = Intl.supportedValuesOf?.('timeZone') ?? [
      'America/Los_Angeles', 'America/Mexico_City', 'America/New_York', 'America/Sao_Paulo',
      'Asia/Kolkata', 'Asia/Tokyo', 'Australia/Sydney', 'Europe/Lisbon', 'Europe/London', 'Europe/Paris',
    ];
    function option(zone) {
      const parts = zone.replaceAll('_', ' ').split('/');
      const city = parts.pop();
      return new Option(parts.length ? city + ' · ' + parts.join('/') : city, zone);
    }
    const options = [...new Set(['UTC', ...zones, systemZone, timeZone])].filter(Boolean);
    const collator = new Intl.Collator(document.documentElement.lang);
    select.length = 1;
    select.append(...options.map(option).sort((a, b) => collator.compare(a.text, b.text)));
    select.disabled = false;
    select.value = timeZone;

    life.on(document, 'oie:time-zone', () => {
      if (timeZone && !Array.from(select.options).some(item => item.value === timeZone)) select.add(option(timeZone));
      select.value = timeZone;
    });
    life.on(select, 'change', () => {
      const value = validZone(select.value);
      change(value);
      try {
        if (value) localStorage.setItem(storageKey, value);
        else localStorage.removeItem(storageKey);
      } catch {
        window.dispatchEvent(new CustomEvent('notify', {
          detail: {kind: 'toast', tone: 'danger', message: select.dataset.unsaved},
        }));
      }
    });
  });
})();
