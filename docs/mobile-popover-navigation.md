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
Oiê pins `v0.6.2-0.20261110022421-7c69f01af2ef`.

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

For the earlier trigger-ownership increment, Dagger was attempted twice. The first attempt timed out before starting a
remote session. The retry ran the checks container after installing FFmpeg,
but the client timed out and canceled it before race tests and remaining builds
were confirmed complete. Those attempts did not produce a complete Dagger pass. Evidence is saved
in `/tmp/oie-popover-dagger-retry.log` and
`/tmp/oie-popover-dagger-remote-proof.log`. The host `make check` result and
isolated browser passes above are separate from these incomplete Dagger runs.

## Opening-frame placement

The reaction increment exposed an opening race in the same public Popover.
Alpine could paint its scale transition before the deferred positioner clamped
the panel. The generic correction in integration PR 375 keeps the measurable
panel invisible until positioning completes. Keyboard focus and the focus trap
wait for that visibility update. Oie retains its existing light bubble shadow
and uses no consumer workaround against private library markup.

The source-star fixture now samples every visible opening frame for 42 menu
openings across received, own, grouped, image and link messages at 390, 818 and
1440 px in light and dark themes. Existing settled bounds, Escape focus and
accessibility assertions remain. Upstream component/runtime checks and focused
Popover/Dropdown browser suites passed, including filtered and transformed
scroll containers. The upstream native lint executable was unavailable and was
not run locally. Oie's final CI results are recorded in `docs/local-ci.md`.
