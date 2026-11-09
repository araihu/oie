# Mobile navigation and account popovers

The account selector inside the mobile sidebar uses the public Goshtoso Popover.
Alpine can resolve `$refs` from ancestor scopes. The popover previously adopted
the sidebar's trigger and changed its `aria-controls` to the account panel. Its
own custom trigger received no panel association.

The generic correction belongs in Goshtoso. Trigger and panel refs must belong
to the current popover root before the component uses them. The existing public
custom-trigger lookup handles inherited refs. Oiê keeps its current sidebar,
account selector and HTMX navigation contract.

The correction is open in [Goshtoso PR 379](https://github.com/araihu/goshtoso/pull/379)
and included in [integration PR 375](https://github.com/araihu/goshtoso/pull/375).
Oiê pins `v0.6.2-0.20261109092607-b79c3a7a8527`.

The consumer regression is `tests/browser/fragment-navigation.cjs`. It checks
both control associations before and after mobile fragment transitions, in
addition to the existing account selection, drafts, Back/Forward, accessibility
and JavaScript-disabled navigation cases. The test uses an isolated HTTP engine
and does not access retained protocol accounts.

The unmodified fixture passes against the rebuilt pinned dependency. It checks
25 desktop and 11 mobile fragment requests with one document request per
context. The same ownership assertion fails against the preceding dependency.
Upstream Popover, Dropdown and Sidebar browser checks pass, including the new
ownership and remount regression in both light/dark Goshtoso and Minimal themes.

`make check` passes, as does the language/settings browser fixture after giving
an explicit language focus request priority over appearance focus restoration.
Read-only inspection of the live BFF confirms distinct sidebar/account control
targets, one document request across media navigation, and no browser errors.

The focused conversation fixture now measures the retained account/profile
footer instead of the removed engine-status footer. Its layout checks advance,
but the later held-request scenario times out waiting for a conversation to
settle with both the previous binary and the corrected dependency. This fixture
is not recorded as passing. Logs are `/tmp/oie-popover-conversation-final.log`
and `/tmp/oie-popover-conversation-baseline.log`.

Dagger was attempted twice. The first attempt timed out before starting a
remote session. The retry ran the checks container after installing FFmpeg,
but the client timed out and canceled it before race tests and remaining builds
were confirmed complete. No complete Dagger pass is claimed. Evidence is saved
in `/tmp/oie-popover-dagger-retry.log` and
`/tmp/oie-popover-dagger-remote-proof.log`. The host `make check` result and
isolated browser passes above are separate from these incomplete Dagger runs.
