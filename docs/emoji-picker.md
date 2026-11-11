# Searchable emoji selector

The composer has a local emoji selector built from Goshtoso's public Popover, Button and TextInput components. No dependency upgrade or protocol operation is needed. Unicode emoji are ordinary message text. Stickers and GIF discovery/sending are explicitly backlogged as B19 and B20 in the spec.

The selector and composer shortcuts share the current locale's embedded catalog, loaded on first use. Interface labels and accessible emoji names follow the current English or Brazilian Portuguese locale. Each catalog indexes names and keywords from both supported languages, plus a small app-owned synonym list. Picker search ignores case and accents, also accepts an emoji itself, and requires every search term to match. For example, `thumbs up` and `joinha` resolve to the same emoji family in either interface language. Browsing uses one continuous list, starting with Recent and followed by category headers. Footer icons move the internal scrollbar to those headers without filtering the catalog or changing the URL. Search covers every category regardless of the last shortcut used. A device-specific list of up to 24 recent choices appears at the top. Recent choices fall back to tab memory if browser storage is unavailable. Recent entries are grouped by family, showing its most recently chosen variant.

Skin-tone variants share one main grid entry. A small adjacent arrow opens a nested Goshtoso Popover containing the original emoji and its variants in a compact horizontal strip, preferably above it, with a below fallback when viewport space requires. Its colors, border and rounding follow the active theme tokens, and its shadow uses the public Goshtoso shadow class. The strip has rounded corners and a pointer toward the originating emoji, with its accessible name retained without a visible title. Larger mixed-tone families scroll horizontally. Mixed-tone pairs map to their retained base emoji too; no Unicode sequence is synthesized. The subpicker supports keyboard navigation and Escape returns to its trigger without closing the main selector. Hover uses a rounded background highlight without underlining; the variant trigger appears on hover or focus and remains available on touch devices.

Picking an emoji replaces the textarea's selection or inserts at its cursor. It updates the existing draft journal through the input event and returns focus to the composer. It does not submit the form, change the operation key, discard a reply target, or require a protocol capability. The selector is disabled while the composer is disabled, read-only or handling a send. HTMX route cleanup removes its listeners and aborts pending catalog reads.

Keyboard users can open with Enter or Arrow Down, search, move into results with Arrow Down, navigate emoji buttons with arrow keys, select with Enter, and dismiss with Escape. The panel uses Goshtoso's viewport-aware placement and the browser's emoji font. Names are accessible labels, not HTML from the catalog. The main list shows one entry per emoji family; variant choices are created only when their subpicker opens. The panel keeps its responsive height across categories and empty states, including Recent lists without any choices.

The catalog contains Unicode 17.0 and CLDR 48 data with the retained Unicode-3.0 license. See [the data generation instructions](../tools/emoji/README.md). No external service receives search terms, and the app does not download an emoji font or image set.

## Composer shortcuts

Type `:joinha`, `:thumbs-up`, or `:thumbs_up:` at a word boundary in a chat
composer. The list prioritizes exact matches, then prefixes, with one option per
emoji family. Use the arrow keys and Enter, or select by click or touch. Escape
or leaving the composer dismisses the list. The replacement includes an optional
closing colon and preserves adjacent text. It updates the existing draft and
recent choices. The picker provides skin-tone variants.

**Settings → Composition → Emoji shortcut languages** defaults to both English
and Brazilian Portuguese. Clearing both choices disables shortcuts. The device
preference takes effect immediately and falls back to tab memory if storage is
unavailable. The interface language still supplies accessible emoji names. The
picker searches both languages.

The composer keeps its Goshtoso Textarea as the focus owner. The shortcut list
shares the room mention overlay's styling through public Panel and Button root
hooks. A shortcode takes precedence when a room mention query spans the same
text. A delayed catalog response checks the current selection, focus and route
before showing results. An unavailable catalog leaves literal entry available.

## Verification

Validation includes the isolated `tests/browser/emoji-picker.cjs` fixture, which checks lazy loading, cursor/selection replacement, composed sequences, keyboard actions, draft isolation through HTMX, pending-send locks, Portuguese search, mobile placement, denied storage, catalog retry and absence of sends. The accessibility scan covers the picker itself. The existing empty fixture timeline has an unrelated `aria-prohibited-attr` warning and is outside that scan.

Verified on 2026-11-08 with `make check`, Dagger `make ci-check` including golangci-lint and race tests, the emoji browser fixture, and the existing reply-composer browser regression. The first reply regression attempt hit its immediate keyboard-focus assertion under concurrent checks; the unchanged baseline and the feature rerun both passed. The full Dagger browser/XMPP pipeline was not run for this frontend increment.

The continuous-section increment passed `make check` and the expanded emoji browser fixture, including footer jumps without filtering, global bilingual search, nested variant selection and empty-state dimensions. Dagger completed lint, generation checks, normal/race tests and builds with `Container.stdout DONE`, but `make ci-check` exited 2 during client cleanup because the `/shutdown` request exceeded its deadline. The CI command did not finish cleanly; this was a session-shutdown failure after the checks completed. The isolated live BFF on port 8081 was rebuilt and its new section markup verified over HTTP.

The compact variant-strip and theme followup passed the browser fixture, `make check`, and Dagger `make ci-check` with exit 0 using the independent SSH runner connection. Browser checks cover the single-row strip, centered origin pointer, rounded hover and inherited theme colors, radius and shadow in dark and light modes. The live BFF on port 8081 serves this build.
