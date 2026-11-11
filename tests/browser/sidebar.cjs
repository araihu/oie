const assert = require("node:assert/strict");
const { mkdir } = require("node:fs/promises");
const { chromium } = require("playwright");

const base = process.env.OIE_BROWSER_URL || "http://127.0.0.1:8080";
const pane = "#desktop-navigation";
const toggle = "[data-sidebar-toggle]";
const splitter = "[data-sidebar-splitter]";

async function width(page) {
  return Math.round((await page.locator(pane).boundingBox())?.width || 0);
}

async function drag(page, distance) {
  const handle = await page.locator(splitter).boundingBox();
  const x = handle.x + handle.width / 2;
  const y = handle.y + handle.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + distance, y, { steps: 8 });
  await page.mouse.up();
}

async function open(page, path) {
  const response = await page.goto(base + path);
  assert.equal(response.status(), 200);
  await page.waitForFunction(() => document.documentElement.hasAttribute("data-sidebar-ready"));
}

async function assertToggle(page, expanded, label) {
  const control = page.getByRole("button", { name: label, exact: true });
  await control.waitFor();
  assert.equal(await control.getAttribute("aria-expanded"), String(expanded));
  assert.equal(await page.locator(pane).isVisible(), expanded);
}

async function assertNoOverflow(page) {
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
}

async function assertAccessibleControls(page) {
  await page.addScriptTag({ path: require.resolve("axe-core/axe.min.js") });
  const failures = await page.evaluate(async selectors => {
    const { violations } = await axe.run({ include: selectors }, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"] },
    });
    return violations.map(({ id, nodes }) => ({ id, targets: nodes.map(node => node.target) }));
  }, [toggle, splitter]);
  assert.deepEqual(failures, []);
}

(async () => {
  const browser = await chromium.launch();
  const errors = [];
  const watch = page => {
    page.on("pageerror", error => errors.push(error.message));
    page.on("console", message => {
      if (message.type() === "error") errors.push(message.text());
    });
  };

  try {
    await mkdir("artifacts", { recursive: true });
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
    watch(page);
    await open(page, "/?lang=en&theme=light");
    await assertToggle(page, true, "Collapse sidebar");
    assert.equal(await width(page), 208);
    await drag(page, 75);
    assert.equal(await width(page), 283);
    await drag(page, -35);
    assert.equal(await width(page), 248);
    const handle = page.getByRole("separator", { name: "Navigation" });
    assert.equal(await handle.getAttribute("aria-valuenow"), "248");
    await handle.press("ArrowRight");
    assert.equal(await width(page), 264);
    await handle.press("Home");
    assert.equal(await width(page), 184);
    await handle.press("End");
    assert.equal(await width(page), 360);
    await assertAccessibleControls(page);
    await handle.press("Enter");
    await assertToggle(page, false, "Expand sidebar");
    assert(await page.locator(toggle).evaluate(button => button === document.activeElement));
    await page.reload();
    await assertToggle(page, false, "Expand sidebar");
    await page.locator(toggle).press("Space");
    assert.equal(await width(page), 360);
    await page.screenshot({ path: "artifacts/sidebar-wide-expanded.png" });

    await page.evaluate(() => { window.sidebarTestDocument = true; });
    await page.locator(pane).getByRole("link", { name: "Media & links" }).click();
    await page.locator(".oie-shared-header").waitFor();
    await assertToggle(page, true, "Collapse sidebar");
    assert(await page.evaluate(() => window.sidebarTestDocument));
    assert.equal(await width(page), 360);
    await page.locator(toggle).click();
    await assertToggle(page, false, "Expand sidebar");
    await page.screenshot({ path: "artifacts/sidebar-wide-collapsed.png" });

    await open(page, "/search?lang=pt-BR&theme=dark");
    await assertToggle(page, false, "Expandir barra lateral");
    await page.setViewportSize({ width: 768, height: 900 });
    await page.locator(toggle).click();
    await assertToggle(page, true, "Recolher barra lateral");
    assert.equal(await width(page), 215);
    await assertNoOverflow(page);
    await page.setViewportSize({ width: 1440, height: 900 });
    assert.equal(await width(page), 360, "the medium viewport cap must not replace the saved width");

    await page.setViewportSize({ width: 768, height: 900 });
    await open(page, "/?lang=pt-BR&theme=dark");
    await assertToggle(page, true, "Recolher barra lateral");
    assert.equal(await page.locator(".oie-list-account").isVisible(), false);
    await drag(page, -25);
    assert.equal(await width(page), 190);
    await page.screenshot({ path: "artifacts/sidebar-medium-expanded.png" });
    await page.locator(toggle).click();
    await assertToggle(page, false, "Expandir barra lateral");
    assert.equal(await page.locator(".oie-list-account").isVisible(), true);
    await page.screenshot({ path: "artifacts/sidebar-medium-collapsed.png" });

    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.locator(toggle).isVisible(), false);
    assert.equal(await page.locator(splitter).isVisible(), false);
    const mobile = page.getByRole("button", { name: "Abrir navegação", exact: true });
    assert.equal(await mobile.count(), 1);
    await mobile.click();
    const overlay = page.locator("#mobile-navigation-panel");
    await overlay.waitFor();
    const box = await overlay.boundingBox();
    assert(box.width > 0 && box.height > 0 && box.x < 390 && box.y < 844);
    await page.screenshot({ path: "artifacts/sidebar-mobile-overlay.png" });
    await page.keyboard.press("Escape");
    await overlay.waitFor({ state: "hidden" });
    assert.equal(await mobile.getAttribute("aria-expanded"), "false");
    assert(await mobile.evaluate(button => button === document.activeElement));
    await assertNoOverflow(page);
    await page.setViewportSize({ width: 768, height: 900 });
    await assertToggle(page, false, "Expandir barra lateral");
    await page.locator(toggle).click();
    assert.equal(await width(page), 190);
    await page.close();

    const noStorage = await browser.newPage({ viewport: { width: 1024, height: 768 } });
    watch(noStorage);
    await noStorage.addInitScript(() => {
      Object.defineProperty(window, "localStorage", { get() { throw new Error("storage unavailable"); } });
    });
    await open(noStorage, "/?lang=en&theme=dark");
    await drag(noStorage, -20);
    await noStorage.locator(toggle).click();
    await assertToggle(noStorage, false, "Expand sidebar");
    await noStorage.locator(toggle).click();
    assert.equal(await width(noStorage), 188);
    await noStorage.close();
    assert.deepEqual(errors, []);
    console.log("PASS: desktop and medium sidebar resizing, keyboard, persistence, routes, and mobile overlay");
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
