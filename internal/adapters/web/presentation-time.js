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
      formatters.set(key, new Intl.DateTimeFormat(locale, {timeZone: timeZone || undefined, ...options}));
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
  const fullDates = '[data-viewer-date],[data-capability-date],[data-details-date],[data-shared-source-date]';
  const calendarDate = {dateStyle: 'medium'};
  const agendaDay = {dateStyle: 'full'};

  function localize(root) {
    const today = dateKey(new Date(), 'day');
    for (const node of root.querySelectorAll('time[datetime]:not([data-day])')) {
      const date = new Date(node.dateTime);
      if (Number.isNaN(date.getTime())) continue;
      const options = node.hasAttribute('data-history-date') ? editTime : node.matches(fullDates) ? dateAndTime :
        node.hasAttribute('data-scheduled-day') ? agendaDay : clock;
      const todayLabel = node.dataset.messageSearchTime;
      const label = todayLabel
        ? (dateKey(date, 'day') === today ? todayLabel : format(date, calendarDate)) + ' ' + format(date, clock)
        : format(date, options);
      if (node.textContent !== label) node.textContent = label;
    }
    for (const list of root.querySelectorAll('[data-schedule-agenda]')) {
      let previous;
      for (const heading of list.querySelectorAll('time[data-scheduled-day]')) {
        const day = dateKey(new Date(heading.dateTime), 'day');
        heading.parentElement.hidden = day === previous;
        previous = day;
      }
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

  window.OieTime = {format, dateKey, localize, timeZone: () => timeZone || systemTimeZone};
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
    const picker = document.getElementById('settings-time-zone');
    if (!picker) return;
    const trigger = picker.querySelector('[role="combobox"]');
    const systemLabel = picker.querySelector('[data-combobox-option-label]').textContent;

    const zones = Intl.supportedValuesOf?.('timeZone') ?? [
      'America/Los_Angeles', 'America/Mexico_City', 'America/New_York', 'America/Sao_Paulo',
      'Asia/Kolkata', 'Asia/Tokyo', 'Australia/Sydney', 'Europe/Lisbon', 'Europe/London', 'Europe/Paris',
    ];
    function option(zone) {
      const parts = zone.replaceAll('_', ' ').split('/');
      const city = parts.pop();
      return {value: zone, label: parts.length ? city + ' · ' + parts.join('/') : city};
    }
    const collator = new Intl.Collator(document.documentElement.lang);
    let available;
    function syncPicker() {
      const values = [timeZone];
      let options;
      const systemZone = validZone(systemTimeZone);
      if (!available || !available.has(timeZone) || !available.has(systemZone)) {
        available = new Set(['', 'UTC', ...zones, systemZone, timeZone]);
        options = [...available].filter(Boolean).map(option).sort((a, b) => collator.compare(a.label, b.label));
        options.unshift({value: '', label: systemLabel});
      }
      picker.dispatchEvent(new CustomEvent('combobox:load', {
        bubbles: true, detail: {id: picker.id, options, values},
      }));
    }
    syncPicker();
    trigger.disabled = false;

    life.on(document, 'oie:time-zone', syncPicker);
    life.on(picker, 'combobox:change', ({detail}) => {
      if (detail.id !== picker.id) return;
      const value = validZone(detail.values[0]);
      change(value);
      try {
        if (value) localStorage.setItem(storageKey, value);
        else localStorage.removeItem(storageKey);
      } catch {
        window.dispatchEvent(new CustomEvent('notify', {
          detail: {kind: 'toast', tone: 'danger', message: trigger.dataset.unsaved},
        }));
      }
    });
  });
})();
