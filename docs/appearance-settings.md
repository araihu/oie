# Appearance settings

General settings compose Goshtoso's `radio.RadioBar` and segmented `radio.Radio`
components for System, Light and Dark. Each option includes a bundled icon and
uses the existing English and Brazilian Portuguese labels.

The component is in `internal/adapters/web/appearance.templ`. Regenerate its Go
output with the repository's templ tool.

Selection uses the existing theme route and device cookie, swapping `#oie-app`
with HTMX while retaining `open=settings`. The selected radio is synchronized
after swaps and receives focus so consecutive arrow-key changes work. System
mode follows device color-scheme changes. No engine operation or dependency was
added.

`tests/browser/appearance-settings.cjs` is a portable, isolated BFF fixture in the
Dagger browser stage. It checks 390 px and 1440 px in English and Brazilian
Portuguese, icons, document preservation during swaps, drawer visibility,
consecutive keyboard changes, cookie persistence, system color changes,
horizontal overflow and accessibility. The fixture passes against the current
application binary. Full pipeline validation is recorded in [local CI](local-ci.md).
