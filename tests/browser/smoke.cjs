const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const { chromium } = require("playwright");

(async () => {
  const base = process.env.OIE_BROWSER_URL || "http://127.0.0.1:8080";
  const scene = process.env.OIE_BROWSER_SCENE || "initial";
  const browser = await chromium.launch({headless: true});
  const findings = [];
  await fs.mkdir("artifacts", {recursive: true});
  try {
    for (const width of [390, 1440]) {
      for (const theme of ["goshtoso", "minimal"]) {
        for (const mode of ["light", "dark"]) {
          for (const locale of ["en", "pt-BR"]) {
            const page = await browser.newPage({viewport: {width, height: 900}, locale});
            const errors = [];
            page.on("pageerror", e => errors.push(e.message));
            page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
            const response = await page.goto(base + "/?theme=" + mode + "&lang=" + locale);
            assert.equal(response.status(), 200);
            await page.waitForFunction(() => window.Alpine && window.htmx);
            await page.evaluate(theme => document.documentElement.setAttribute("data-theme", theme), theme);
            assert.equal(await page.locator("html").getAttribute("lang"), locale);
            assert.equal(await page.locator("main").count(), 1);
            assert.equal(await page.locator("h1").count(), 1);
            assert.equal(await page.evaluate(() => document.documentElement.classList.contains("dark")), mode === "dark");
            assert(await page.locator("h2").filter({hasText: locale === "en" ? "Accounts" : "Contas"}).count());
            assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
            await page.addScriptTag({path: require.resolve("axe-core/axe.min.js")});
            const accessibility = await page.evaluate(async () => window.axe.run(document, {
              runOnly: {type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa"]}
            }));
            assert.deepEqual(accessibility.violations.map(v => ({id: v.id, nodes: v.nodes.map(n => n.target)})), [],
              [width, theme, mode, locale].join("-") + " accessibility");
            await page.keyboard.press("Tab");
            assert(await page.locator("a").first().evaluate(e => e === document.activeElement));
            assert.deepEqual(errors, []);
            const name = [width, theme, mode, locale].join("-");
            await page.screenshot({path: "artifacts/" + scene + "-" + name + ".png", fullPage: true});
            findings.push({name, status: "passed", consoleErrors: errors, accessibilityViolations: accessibility.violations.length});
            await page.close();
          }
        }
      }
    }
    const page = await browser.newPage({colorScheme: "dark"});
    await page.goto(base + "/?theme=system");
    assert(await page.evaluate(() => document.documentElement.classList.contains("dark")));
    await page.emulateMedia({colorScheme: "light"});
    await page.waitForFunction(() => !document.documentElement.classList.contains("dark"));
    const notFound = await page.goto(base + "/missing?lang=pt-BR");
    assert.equal(notFound.status(), 404);
    assert.equal(await page.locator("main").count(), 1);
    assert(await page.getByText("Página não encontrada", {exact: true}).count());
    await page.close();
    await fs.writeFile("artifacts/" + scene + "-report.json", JSON.stringify({findings, systemTheme: "passed", missingRoute: "passed"}, null, 2));
    console.log("PASS: 16 viewport/theme/mode/locale combinations, keyboard skip link, axe checks, console checks, and system-theme changes");
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
