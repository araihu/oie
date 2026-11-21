(() => {
  'use strict';
  const storageKey = 'oie.device.time-zone.v1';
  const formatters = new Map();
  const offsetFormatters = new Map();
  const pickerState = new WeakMap();
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

  function utcOffset(zone, date, instant) {
    if (instant) return 'UTC' + instant.toZonedDateTimeISO(zone).offset;
    if (!offsetFormatters.has(zone)) {
      offsetFormatters.set(zone, new Intl.DateTimeFormat('en-US', {timeZone: zone, timeZoneName: 'longOffset'}));
    }
    const offset = offsetFormatters.get(zone).formatToParts(date).find(part => part.type === 'timeZoneName').value;
    return offset === 'GMT' ? 'UTC+00:00' : offset.replace('GMT', 'UTC');
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
  const preciseDates = '[data-history-date],[data-diagnostics-date]';
  const fullDates = '[data-viewer-date],[data-capability-date],[data-details-date],[data-shared-source-date]';
  const calendarDate = {dateStyle: 'medium'};
  const agendaDay = {dateStyle: 'full'};

  function localize(root) {
    const today = dateKey(new Date(), 'day');
    for (const node of root.querySelectorAll('time[datetime]:not([data-day])')) {
      const date = new Date(node.dateTime);
      if (Number.isNaN(date.getTime())) continue;
      const options = node.matches(preciseDates) ? editTime : node.matches(fullDates) ? dateAndTime :
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
    else document.dispatchEvent(new Event('oie:system-time-zone'));
  });
  window.addEventListener('storage', event => {
    if (event.key === storageKey || event.key === null) change(validZone(event.newValue));
  });

  window.OieRuntime?.register('presentation-time', life => {
    const picker = document.getElementById('settings-time-zone');
    if (!picker) return;
    const trigger = picker.querySelector('[role="combobox"]');
    const systemLabel = trigger.dataset.systemLabel;

    function option(value, now, instant) {
      const parts = value.replaceAll('_', ' ').split('/');
      const city = parts.pop();
      const label = !value ? systemLabel : parts.length ? city + ' · ' + parts.join('/') : city;
      return {value, label: label + ' (' + utcOffset(value || systemTimeZone || 'UTC', now, instant) + ')'};
    }

    function syncPicker() {
      const now = new Date();
      const instant = globalThis.Temporal?.Instant?.fromEpochMilliseconds(now.getTime());
      const current = option(timeZone, now, instant);
      const previous = pickerState.get(picker);
      let options;
      if (trigger.getAttribute('aria-expanded') === 'true') {
        const zones = Intl.supportedValuesOf?.('timeZone') ?? [
          'America/Los_Angeles', 'America/Mexico_City', 'America/New_York', 'America/Sao_Paulo',
          'Asia/Kolkata', 'Asia/Tokyo', 'Australia/Sydney', 'Europe/Lisbon', 'Europe/London', 'Europe/Paris',
        ];
        const available = new Set(['UTC', ...zones, systemTimeZone, timeZone].filter(Boolean));
        const collator = new Intl.Collator(document.documentElement.lang);
        const next = [...available].map(value => option(value, now, instant)).sort((a, b) => collator.compare(a.label, b.label));
        next.unshift(option('', now, instant));
        if (next.length !== previous?.options.length || next.some((item, i) =>
          item.label !== previous.options[i].label || item.value !== previous.options[i].value)) options = next;
      } else if (!previous?.options.some(item => item.value === current.value && item.label === current.label)) {
        options = [current];
      }
      if (!options && previous?.value === timeZone) return;
      picker.dispatchEvent(new CustomEvent('combobox:load', {
        bubbles: true, detail: {id: picker.id, options, values: [timeZone]},
      }));
      pickerState.set(picker, {value: timeZone, options: options || previous.options});
    }
    // The component updates aria-expanded for pointer and keyboard activation.
    const observer = new MutationObserver(() => {
      if (trigger.getAttribute('aria-expanded') === 'true') syncPicker();
    });
    observer.observe(trigger, {attributes: true, attributeFilter: ['aria-expanded']});
    life.cleanup(() => observer.disconnect());
    syncPicker();
    trigger.disabled = false;

    life.on(document, 'oie:time-zone', syncPicker);
    life.on(document, 'oie:system-time-zone', syncPicker);
    life.on(window, 'drawer:open', ({detail}) => {
      if (detail?.id === 'settings') syncPicker();
    });
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
