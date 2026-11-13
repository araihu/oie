const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const {chromium} = require('playwright');

const base = process.env.OIE_BROWSER_URL || 'http://127.0.0.1:8080';
const chat = '019a5df0-0000-7000-8000-000000000102';
const key = 'oie.device.time-zone.v1';
const explicitZone = 'Pacific/Kiritimati';
const zoneTrigger = page => page.locator('#settings-time-zone-trigger');
const zoneValue = page => page.locator('#settings-time-zone input[name="time-zone"]');

async function chooseZone(page, value, query) {
  const trigger = zoneTrigger(page);
  await trigger.click();
  await page.locator('#settings-time-zone [data-combobox-search]').fill(query);
  const option = page.locator('#settings-time-zone').getByRole('option');
  await option.waitFor();
  assert.equal(await option.getAttribute('data-value'), value);
  await option.focus();
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.activeElement?.id === 'settings-time-zone-trigger');
}

async function expected(page, zone) {
  return page.evaluate(zone => {
    const time = document.querySelector('[data-message-time]');
    const date = new Date(time.dateTime);
    const locale = document.documentElement.lang;
    return {
      stamp: time.dateTime,
      clock: new Intl.DateTimeFormat(locale, {hour:'2-digit', minute:'2-digit', timeZone:zone}).format(date),
      day: new Intl.DateTimeFormat(locale, {dateStyle:'medium', timeZone:zone}).format(date),
    };
  }, zone);
}

async function assertDisplay(page, value) {
  const messageTime = page.locator('[data-message-time]').first();
  await page.waitForFunction(value => document.querySelector('[data-message-time]')?.textContent === value, value.clock);
  assert.equal(await messageTime.getAttribute('datetime'), value.stamp);
  assert.equal(await page.locator('.oie-day-divider').first().textContent(), value.day);
}

(async () => {
  const browser = await chromium.launch({headless:true});
  const artifacts = path.join(__dirname, 'artifacts');
  await fs.mkdir(artifacts, {recursive:true});
  const errors = [];
  try {
    for (const width of [390, 1440]) {
      for (const theme of ['goshtoso', 'minimal']) for (const mode of ['light', 'dark']) {
        const locale = mode === 'light' ? 'en' : 'pt-BR';
        const context = await browser.newContext({
          viewport:{width, height:900}, locale, reducedMotion:'reduce',
        });
        const page = await context.newPage();
        const session = await context.newCDPSession(page);
        await session.send('Emulation.setTimezoneOverride', {timezoneId:'America/New_York'});
        page.on('pageerror', error => errors.push(error.message));
        const url = base + '/?chat=' + chat + '&lang=' + locale + '&theme=' + mode + '&open=settings';
        await page.goto(url);
        await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
        const dialog = page.getByRole('dialog', {name:locale === 'en' ? 'Settings' : 'Configurações', exact:true});
        await dialog.waitFor();
        const trigger = dialog.getByRole('combobox', {name:locale === 'en' ? 'Display time zone' : 'Fuso horário de exibição'});
        assert.equal(await zoneValue(page).inputValue(), '');
        const system = await expected(page, 'America/New_York');
        const preferred = await expected(page, explicitZone);
        assert.notEqual(system.day, preferred.day, 'fixture crosses midnight in the preferred zone');
        await assertDisplay(page, system);

        await dialog.locator('input[name="theme"]:checked:focus').waitFor({state:'attached'});
        await trigger.focus();
        await trigger.click();
        const search=dialog.getByRole('textbox',{name:locale==='en'?'Search time zones':'Buscar fusos horários'});
        await search.fill('no matching zone');
        await dialog.getByText(locale==='en'?'No time zones found':'Nenhum fuso horário encontrado',{exact:true}).waitFor();
        await search.press('Escape');
        await page.waitForFunction(() => document.activeElement?.id === 'settings-time-zone-trigger' && document.activeElement.getAttribute('aria-expanded') === 'false');
        await page.evaluate(() => {
          const nodes = ['#message-timeline', '#send-message-form', '#settings-time-zone'].map(id => document.querySelector(id));
          const rects = () => nodes.map(node => {
            const {x,y,width,height} = node.getBoundingClientRect();
            return [x,y,width,height];
          }).flat();
          const reference = rects();
          window.timeZoneGeometry = {max:0, running:true};
          const tick = () => {
            if (!window.timeZoneGeometry.running) return;
            const delta = rects().map((value, index) => Math.abs(value - reference[index]));
            window.timeZoneGeometry.max = Math.max(window.timeZoneGeometry.max, ...delta);
            requestAnimationFrame(tick);
          };
          requestAnimationFrame(tick);
        });
        await chooseZone(page, explicitZone, 'Kiritimati');
        await assertDisplay(page, preferred);
        assert(await trigger.evaluate(node => node === document.activeElement));
        const drift = await page.evaluate(async () => {
          await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
          window.timeZoneGeometry.running = false;
          return window.timeZoneGeometry.max;
        });
        assert(drift <= 1, 'unintended movement: ' + drift + 'px');
        assert.equal(await page.evaluate(key => localStorage.getItem(key), key), explicitZone);

        const peer = await context.newPage();
        await peer.goto(url);
        await assertDisplay(peer, preferred);
        await chooseZone(peer, '', locale === 'en' ? 'System time zone' : 'Fuso horário do sistema');
        await assertDisplay(page, system);
        assert.equal(await zoneValue(page).inputValue(), '');
        await peer.close();

        await session.send('Emulation.setTimezoneOverride', {timezoneId:'Europe/Lisbon'});
        await page.evaluate(() => window.dispatchEvent(new Event('focus')));
        await assertDisplay(page, await expected(page, 'Europe/Lisbon'));

        await chooseZone(page, explicitZone, 'Kiritimati');
        await page.reload();
        await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
        await assertDisplay(page, preferred);
        assert.equal(await zoneValue(page).inputValue(), explicitZone);
        await page.addScriptTag({path:require.resolve('axe-core/axe.min.js')});
        const result = await dialog.evaluate(node => axe.run(node, {
          runOnly:{type:'tag', values:['wcag2a','wcag2aa','wcag21aa']},
        }));
        assert.deepEqual(result.violations.map(item => item.id), []);
        await trigger.click();
        await search.fill('Kiritimati');
        const option=dialog.getByRole('option',{name:'Kiritimati · Pacific'});
        await option.waitFor();
        const bounds=await option.boundingBox();
        assert(bounds.x>=0 && bounds.y>=0 && bounds.x+bounds.width<=width && bounds.y+bounds.height<=900);
        const openResult=await dialog.evaluate(node=>axe.run(node,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
        assert.deepEqual(openResult.violations.map(item=>item.id),[]);
        await page.screenshot({path:path.join(artifacts, 'presentation-time-' + width + '-' + theme + '-' + mode + '.png')});
        await search.press('Escape');
        const language=dialog.getByRole('combobox',{name:locale==='en'?'Language':'Idioma'});
        await language.click();
        await dialog.getByRole('textbox',{name:locale==='en'?'Search languages':'Buscar idiomas'}).fill('English');
        await dialog.getByRole('option',{name:'English',exact:true}).waitFor();
        const languageResult=await dialog.evaluate(node=>axe.run(node,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
        assert.deepEqual(languageResult.violations.map(item=>item.id),[]);
        await page.screenshot({path:path.join(artifacts, 'language-settings-' + width + '-' + theme + '-' + mode + '.png')});
        await context.close();
      }
    }

    const page = await browser.newPage({timezoneId:'America/New_York'});
    page.on('pageerror', error => errors.push(error.message));
    await page.addInitScript(key => {
      const {setItem} = Storage.prototype;
      Storage.prototype.setItem = function(name, value) {
        if (name === key) throw new DOMException('Storage disabled for this preference', 'SecurityError');
        return setItem.call(this, name, value);
      };
    }, key);
    await page.goto(base + '/?chat=' + chat + '&lang=en&open=settings');
    await chooseZone(page, explicitZone, 'Kiritimati');
    await assertDisplay(page, await expected(page, explicitZone));
    await page.getByText('Device storage is unavailable. This setting lasts until the page reloads.', {exact:true}).filter({visible:true}).waitFor();
    await page.reload();
    await assertDisplay(page, await expected(page, 'America/New_York'));
    assert.deepEqual(errors, []);
    console.log('PASS: system default, time and day grouping across midnight, stable layout, per-profile persistence, cross-tab reset, storage refusal, EN/PT and accessibility');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
