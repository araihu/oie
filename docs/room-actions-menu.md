# Room actions menu

**More → Leave group** sends the existing leave command directly and has a leading
exit icon. It is disabled while the command is pending or the room is inactive.
An uncertain result opens the existing recovery dialog; retry preserves the
operation key and command body.

For a saved room with inactive entry, **More → Join group** opens the nickname
and password form. The dialog also shows room status and supports retrying
uncertain commands. The timeline has no persistent room-session banner.
Opening the dialog does not change the room session.

Join and Leave stay disabled while their request is in flight, including
when a background refresh arrives. If a protected Join loses its response
and the tab reloads, Retry checks the original operation receipt. A matching
receipt confirms acceptance. If the engine has no receipt, the dialog lets
the user re-enter the password and retry with the original operation key.
The browser keeps the password in memory only.

Verification passed with `make check`, the local Dagger check pipeline including
race tests and builds, and the isolated reply-composer browser fixture. That
fixture covers join/leave, stale revisions, uncertain command retries, draft
preservation and the Portuguese mobile dialog with no accessibility violations.
A read-only live-preview check confirmed the removed banner and working menu
without protocol mutations or JavaScript errors. The full Dagger browser
pipeline was not run.

The direct-menu fixture also verifies the icon, submission without an
intermediate panel, disabled pending actions and an uncertain Leave retry that
does not duplicate the operation.

Logs: `/tmp/oie-direct-leave-check.log`, `/tmp/oie-direct-leave-dagger.log`,
`/tmp/oie-direct-leave-browser.log` and `/tmp/oie-direct-leave-live.log`.
