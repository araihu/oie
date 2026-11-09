# Language settings

The settings drawer uses Goshtoso's public Select with `Option.Leading` for
decorative flag emojis. English uses 🇺🇸 and Brazilian Portuguese uses 🇧🇷,
alongside the existing native language names. Margo's locale selector provides
the reference for explicit locale-to-flag mapping. No dependency change or
upstream component work is needed.

The initial value follows the browser language and the existing device cookie.
Initializing the control and choosing its existing value do not save an
override. Changing language follows the existing presentation route through an
HTMX fragment, preserving account, filters and conversation state. The server
saves the device cookie and subsequent requests use it ahead of browser
negotiation. The engine and other devices keep their own locale resolution.

The drawer stays open after changing language. An explicit `settingsFocus`
marker restores focus to the language control after the drawer's initial focus
event. This uses public DOM events and Select attributes, without accessing
component internals or delaying focus by an arbitrary timeout.

## Verification

The isolated language fixture passes in English and Brazilian Portuguese at
390 and 1440 pixels, including keyboard operation, visible flags, same-value
selection, one fragment per change, route preservation, focus, cookie
persistence and accessibility with the options open and closed. The existing
settings-entry fixture and `make check` also pass. Read-only live inspection
confirms the selected flag and both options.

Dagger completed `make check test-race build build-cli` and the mock build
without errors in its engine logs. The client transport failed afterwards and
the local command timed out with exit 124. That attempt is not a successful
CLI completion. Evidence is saved in
`/tmp/oie-language-dagger-remote-proof.log`; the isolated browser fixture runs
are separate from the Dagger checks.
