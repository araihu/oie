# Room actions menu

Room-session controls open from **More → Room actions** in a room conversation.
The status, nickname, join/leave form and refresh action live in a Goshtoso
`modal.Dialog`; the timeline has no persistent room-session banner. Direct
conversations do not show this menu item.

The controls remain mounted so existing live session updates, composer
permissions, compare-and-swap revisions and pending-command recovery continue
to work. Opening the menu or dialog does not join or leave a room.

Verification passed with `make check`, the local Dagger check pipeline including
race tests and builds, and the isolated reply-composer browser fixture. That
fixture covers join/leave, stale revisions, uncertain command retries, draft
preservation and the Portuguese mobile dialog with no accessibility violations.
A read-only live-preview check confirmed the removed banner and working menu
without protocol mutations or JavaScript errors. The full Dagger browser
pipeline was not run.

Logs: `/tmp/oie-room-menu-check.log`, `/tmp/oie-room-menu-dagger.log`,
`/tmp/oie-room-menu-browser.log` and `/tmp/oie-room-menu-live.log`.
