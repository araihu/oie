# Language settings

General settings uses Goshtoso's searchable single-select Combobox for
language and display time zone. The language options and selected value show
decorative flags alongside their native names. The time-zone list comes from
the browser and includes an option to follow the system. Selecting a time zone
updates displayed dates and times immediately and saves the preference on the
device. Each option shows its current UTC offset, including the system time
zone. The offset reflects daylight saving time when it applies and can be used
in the search.

The initial value follows the browser language and the existing device cookie.
Initializing the control and choosing its existing value do not save an
override. Changing language follows the existing presentation route through an
HTMX fragment, preserving account, filters and conversation state. The server
saves the device cookie and subsequent requests use it ahead of browser
negotiation. The engine and other devices keep their own locale resolution.

The drawer stays open after changing language. An explicit `settingsFocus`
marker restores focus to the language control after the drawer's initial focus
event. The selected language follows the applied fragment, so a failed request
leaves the previous language selected.

An explicit `settingsFocus=language` route takes priority over restoring focus
to the checked appearance radio. This prevents the appearance control from
taking focus after a language fragment swap. The existing language fixture
covers this interaction in English and Portuguese at mobile and desktop sizes.

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
