const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { chromium } = require('playwright');
const { presentationVariants } = require('./fixtures/presentation-variants.cjs');

const base = process.env.OIE_BROWSER_URL || 'http://127.0.0.1:8080';
const artifacts = path.join(__dirname, 'artifacts');
const axePath = require.resolve('./fixtures/axe-core/axe.min.js');

async function openFixture(browser, variant, reducedMotion = 'no-preference') {
  const { width, theme, mode, locale } = variant;
  const page = await browser.newPage({ viewport: { width, height: 900 }, locale, reducedMotion });
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => {
    if (message.type() === 'error') errors.push(message.text());
  });
  const url = new URL('/', base);
  url.search = new URLSearchParams({ lang: locale, theme: mode, q: 'unmatched', filter: 'unread' });
  await page.goto(url.href);
  await page.evaluate(value => document.documentElement.dataset.theme = value, theme);
  const trigger = page.locator('[data-account-switcher]:visible');
  assert.equal(await trigger.count(), 1);
  const panel = page.getByRole('dialog', { name: locale === 'en' ? 'Switch account' : 'Trocar de conta', exact: true });
  return { page, trigger, panel, errors };
}

async function waitForInViewport(page, dialog) {
  await dialog.waitFor({ state: 'visible' });
  const element = await dialog.elementHandle();
  try {
    await page.waitForFunction(node => {
      const { width, height, x, y, right, bottom } = node.getBoundingClientRect();
      return getComputedStyle(node).opacity === '1' &&
        width > 0 && height > 0 && x >= -1 && y >= -1 &&
        right <= innerWidth + 1 && bottom <= innerHeight + 1;
    }, element, { timeout: 5000 });
  } finally {
    await element.dispose();
  }
}

async function checkJourney(browser, variant) {
  const { page, trigger, panel, errors } = await openFixture(browser, variant);
  try {
    await trigger.focus();
    await page.keyboard.press('Enter');
    await panel.waitFor({ state: 'visible' });
    assert.equal(await trigger.getAttribute('aria-expanded'), 'true');
    assert.equal(await panel.locator('[data-switch-account][aria-current=true]').count(), 1);
    const personal = panel.locator('[data-switch-account]').filter({ hasText: 'Personal' });
    const account = await personal.getAttribute('data-switch-account');
    assert.equal(await personal.locator('[data-account-unread]').textContent(), '6', 'totals must ignore current empty search');

    await page.keyboard.press('Escape');
    await panel.waitFor({ state: 'hidden' });
    assert(await trigger.evaluate(element => element === document.activeElement));
    await trigger.click();
    await personal.click();
    await page.waitForURL(url => url.searchParams.get('account') === account);
    const url = new URL(page.url());
    assert.equal(url.searchParams.get('q'), 'unmatched');
    assert.equal(url.searchParams.get('filter'), 'unread');
    assert.equal(url.searchParams.get('chat'), null);
    assert((await trigger.textContent()).includes('Personal'));

    await trigger.click();
    assert.equal(await panel.locator('[data-switch-account][aria-current=true]').getAttribute('data-switch-account'), account);
    await panel.locator('[data-open-accounts]').click();
    const overview = page.locator('#account-overview');
    await overview.waitFor({ state: 'visible' });
    assert.equal(await page.locator('[data-connect-account]').count(), 0);
    await page.locator(`[data-manage-account="${account}"]`).click();
    const detail = page.locator('.oie-account-detail');
    await detail.waitFor({ state: 'visible' });
    const connection = await detail.locator('[data-account-status]').getAttribute('data-connection-state');
    const canConnect = ['not_connected', 'error'].includes(connection);
    assert.equal(await detail.locator('[data-connect-account]').count(), Number(canConnect));
    assert.equal(await detail.locator('[data-disconnect-account]').count(), Number(['connected', 'connecting', 'error'].includes(connection)));
    if (canConnect) {
      const endpoint = detail.locator('[data-connect-account] [name=endpoint]');
      assert(!(await endpoint.isVisible()));
      const name = variant.locale === 'en' ? 'Advanced connection settings' : 'Configurações avançadas';
      await detail.getByRole('button', { name, exact: true }).click();
      await endpoint.waitFor({ state: 'visible' });
    }
    await page.locator('[data-account-back]').click();
    await overview.waitFor({ state: 'visible' });
    await page.keyboard.press('Escape');
    await trigger.click();
    await panel.locator('[data-add-account]:visible').click();
    await page.locator('#register-account-form').waitFor({ state: 'visible' });
    assert.equal(await page.locator('[data-connect-account]').count(), 0);
    assert.deepEqual(errors, []);
    return account;
  } finally {
    await page.close();
  }
}

async function checkPresentation(browser, variant, account) {
  const { page, trigger, panel, errors } = await openFixture(browser, variant, 'reduce');
  const { width, theme, mode, locale } = variant;
  const suffix = `${width}-${theme}-${mode}-${locale}`;
  const titles = locale === 'en' ? { list: 'Accounts', detail: 'Profile', add: 'Accounts' } :
    { list: 'Contas', detail: 'Perfil', add: 'Contas' };
  const findings = [];
  async function audit(screen, dialog) {
    await waitForInViewport(page, dialog);
    const result = await page.evaluate(() => window.axe.run(document, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] },
    }));
    assert.deepEqual(result.violations.map(violation => ({
      id: violation.id, nodes: violation.nodes.map(node => node.target),
    })), [], `${screen}-${suffix}`);
    await page.screenshot({ path: path.join(artifacts, `${screen}-${suffix}.png`) });
    findings.push({ screen, ...variant, accessibilityViolations: 0 });
  }
  const drawer = screen => page.getByRole('dialog', { name: titles[screen], exact: true });
  try {
    await page.addScriptTag({ path: axePath });
    await trigger.click();
    await audit('account-switcher', panel);
    await panel.locator(`[data-switch-account="${account}"]`).click();
    await page.waitForURL(url => url.searchParams.get('account') === account);
    await trigger.click();
    await panel.locator('[data-open-accounts]').click();
    await page.locator('#account-overview').waitFor({ state: 'visible' });
    await audit('accounts-list', drawer('list'));
    await page.locator(`[data-manage-account="${account}"]`).click();
    await page.locator('.oie-account-detail').waitFor({ state: 'visible' });
    await audit('accounts-detail', drawer('detail'));
    await page.keyboard.press('Escape');
    await drawer('detail').waitFor({ state: 'hidden' });
    await trigger.click();
    await panel.locator('[data-add-account]:visible').click();
    await page.locator('#register-account-form').waitFor({ state: 'visible' });
    await audit('accounts-add', drawer('add'));
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
    assert.deepEqual(errors, []);
    return findings;
  } finally {
    await page.close();
  }
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  await fs.mkdir(artifacts, { recursive: true });
  try {
    const journeys = [
      { width: 390, theme: 'goshtoso', mode: 'light', locale: 'pt-BR' },
      { width: 1440, theme: 'minimal', mode: 'dark', locale: 'en' },
    ];
    const account = await checkJourney(browser, journeys[0]);
    await checkJourney(browser, journeys[1]);

    const findings = [];
    for (const variant of presentationVariants()) {
      findings.push(...await checkPresentation(browser, variant, account));
    }
    await fs.writeFile(path.join(artifacts, 'account-switcher-report.json'), JSON.stringify({
      journeys, findings, keyboard: 'Escape restores trigger focus',
      navigation: 'account URL, profile and selected row agree; filters retained', consoleErrors: [],
    }, null, 2));
    console.log(`PASS: ${journeys.length} account journeys and ${findings.length} presentation and accessibility checks`);
  } finally {
    await browser.close();
  }
}

main().catch(error => { console.error(error); process.exitCode = 1; });
