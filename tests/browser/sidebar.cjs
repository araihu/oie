const assert = require("node:assert/strict");
const { mkdir } = require("node:fs/promises");
const { chromium } = require("playwright");

const base = process.env.OIE_BROWSER_URL || "http://127.0.0.1:8080";
const pane = "#desktop-navigation";
const toggle = "[data-sidebar-toggle]";
const splitter = "[data-sidebar-splitter]";
const menuIcon = "/assets/icons/heroicons.svg#hi-16-solid-bars-3";
const chatIcon = "/ui/icons/heroicons-extra.svg#heroicons-16-solid-chat-bubble-left";

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
  const colors = await control.evaluate(async button => {
    const styles = getComputedStyle(button);
    const transitions = button.getAnimations().filter(animation =>
      ["background-color", "color"].includes(animation.transitionProperty));
    await Promise.all(transitions.map(animation => animation.finished));
    const reference = document.createElement("span");
    reference.style.backgroundColor = "var(--oie-accent)";
    reference.style.color = "var(--oie-on-accent)";
    button.append(reference);
    const primaryStyles = getComputedStyle(reference);
    const result = {
      background: styles.backgroundColor,
      primary: primaryStyles.backgroundColor,
      color: styles.color,
      onPrimary: primaryStyles.color,
    };
    reference.remove();
    return result;
  });
  assert.equal(colors.background === colors.primary, expanded, JSON.stringify(colors));
  assert.equal(colors.color === colors.onPrimary, expanded, JSON.stringify(colors));
  assert.equal(await control.locator("use").getAttribute("href"), menuIcon);
  assert.equal(await control.locator("svg").evaluate(icon => getComputedStyle(icon).transform), "none");
  assert.equal(await page.locator(pane).isVisible(), expanded);
}

async function assertNoOverflow(page) {
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
}

async function assertAccessibleControls(page) {
  await page.addScriptTag({ path: require.resolve("./fixtures/axe-core/axe.min.js") });
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
    const desktopChats = page.locator(pane).getByRole("link", { name: /^Chats\b/ });
    assert.equal(await desktopChats.locator("use").getAttribute("href"), chatIcon);
    const [spriteURL, symbol] = chatIcon.split("#");
    const sprite = await page.request.get(base + spriteURL);
    assert.equal(sprite.status(), 200);
    const contents = await sprite.text();
    assert(contents.includes('id="' + symbol + '"'));
    assert(contents.includes('fill="currentColor"'));
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
    const mobile = page.locator(".oie-mobile-nav");
    await mobile.waitFor();
    const chats = mobile.getByRole("link", { name: "Conversas", exact: true });
    assert.equal(await chats.locator("use").getAttribute("href"), chatIcon);
    assert.equal(await chats.getAttribute("aria-current"), "page");
    const box = await mobile.boundingBox();
    assert(box.x > 0 && box.y > 750 && box.y + box.height < 844);
    for (const item of await mobile.locator(".oie-mobile-tab,[data-account-switcher]").all()) {
      const target = await item.boundingBox();
      assert(target.width >= 44 && target.height >= 44);
    }
    await page.screenshot({ path: "artifacts/sidebar-mobile-tabs.png" });
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
    console.log("PASS: desktop and medium sidebar resizing, keyboard, persistence, routes, and floating mobile navigation");
  } finally {
    await browser.close();
  }
})().catch(error => {
  console.error(error);
  process.exitCode = 1;
});
