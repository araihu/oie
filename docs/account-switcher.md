# Profile account switcher

Agreed reference: the user's X account popover. This presentation change retains
Oie's unified inbox and simultaneous protocol connections.

Surface brief: one owner moves between personal/work protocol identities in a
compact chat workspace. The profile footer shows the current inbox account's
name/address (or All accounts). Its upward popover prioritizes identities,
selection and unread badges, followed by Add account, Manage accounts,
and Disconnect when the selected connection can be disconnected. The existing
Oie palette, typography and conversation layout remain in use. Navigation uses
normal GET links; protocol mutations retain the existing guarded BFF forms.

Public surface decision: **compose** Goshtoso Sidebar.FooterSlot, Popover,
Avatar, Link and Button. Popover owns Escape, outside dismissal, ARIA wiring and
keyboard focus. Application CSS owns the profile rows and responsive footer;
there is no dependency gap or upstream change. The compact footer exposes the
same control when the desktop sidebar is hidden. Unique popover IDs separate
desktop and compact instances. The mobile navigation overlay contains navigation
only; its profile control remains in the conversation-list footer, avoiding a
second account switcher inside the navigation overlay.

Unread badges compose the existing generated ListConversations API, paging all
unread active and archived conversations across accounts. They do not inherit
search, group, favorite or account filters. Incomplete totals are hidden. Live
refresh retains the popover lifecycle and focused control, and updates badges
without replacing a focused menu. No new API, database or account-login semantics
are introduced.

Switching changes the inbox account and clears the old conversation selection.
It retains list filters; device journals retain each account/conversation draft.
It never sends, disconnects, or retargets pending messages. Add/manage actions
open the existing account interface; Disconnect reuses the existing CSRF-checked
connection operation. App authentication/logout remains backlog.

Settings remain in the conversation bar; the profile area does not duplicate
that action, per the user’s follow-up review.

The conversation header has no duplicate Accounts/More menu. At compact widths,
the account control and settings gear share the conversation-list footer.

Verification: `make check` and the web race suite passed. The account-popover
browser suite passed 390px/1440px × Goshtoso/Minimal × light/dark × English/pt-BR,
including keyboard-open/Escape focus return, viewport bounds, unread counts
independent of a filtered-empty list, selected identity/URL/profile consistency,
add/manage actions, no horizontal overflow, and zero checked WCAG A/AA violations
or console errors. Existing chat/navigation/settings and64 presentation-state
checks also passed. The real Prosody browser workflow verified switching between
Alice/Bob without transferring a draft, restoring it on return, real unread
badges, disconnecting only the selected account through the profile menu, and
subsequent queued cancellation/reconnect/two-way delivery. Fault-injection
browser checks continued to pass.


## Account management · 2026-11-08

Agreed follow-up: replace the stacked connection forms with an account list and
one-account detail screen. Add account has its own screen. The list shows
identity and current connection state; selecting a row opens that account only,
with a return action to the list. The detail uses a clear identity heading and
status region, password input, public Goshtoso Accordion for advanced endpoint
settings, and connection actions. Disconnect appears only for connected,
connecting or error states. Unknown account IDs never fall back to another
account's form. Unavailable engines show retry instead of an empty account list.

Surface decision: **compose** Drawer, Avatar, Link, Form, TextInput, Button and
Accordion through public APIs; product-owned semantic rows/layout use the
existing Oie palette. Correct the app's dark primary foreground token so form
and add-account actions use its contrasting foreground/background pair.

The BFF retains existing canonical mutations. GET screen selection uses
`open=accounts`, `managedAccount=<uuid>` or `accountPage=add`. Registration PRG
opens the saved account's details. Native validation reopens its relevant screen
and retains non-secret values; registration keeps its original operation key on
an unavailable response. Passwords remain write-only. SSE updates connection
status/actions and the account summary list without replacing the password or
advanced-settings controls while the user edits them.

| State/action | Result and invariant | Evidence |
| --- | --- | --- |
| Manage accounts | Summary list, zero connection/registration forms | HTTP render test and browser screen matrix |
| Select known account | Exactly its one connection form, matching ID in URL | HTTP + browser + real engine workflow |
| Select unknown ID | Missing-account recovery, zero connection forms | HTTP render test |
| Add account | Separate metadata form, no connection forms | HTTP + browser screen matrix |
| Register successfully | Saved account details after confirmed response/navigation | Independent real Prosody browser workflow |
| Native invalid connection | Same account detail, endpoint retained, no password readback | Native HTTP recovery test |
| Native unavailable registration | Same add screen, valid values and original operation key retained | Native HTTP recovery test |
| Live connection changes | Status and available disconnect action refresh; inputs stay intact | Adversarial browser + real Prosody workflow |

The real-engine browser observer now waits for each registration destination to
render and asserts the actual connection configuration response before waiting
for XMPP authentication. This removes navigation races between account creation
and connection and exposes connection error codes directly in failed checks.

Verification for the individual-account flow: `make check`, web race tests, and
the native recovery/render tests passed. The16-variant browser suite additionally
opened and scanned the account list, single-account details and add screen at
390px/1440px, both themes/modes/locales, with zero checked accessibility
violations. It exercised the advanced-settings disclosure and return-to-list
navigation. Adversarial browser checks and the real Prosody registration,
connection, send/receive and reconnect workflow passed with the revised screens.

History recovery and its progress display are a subsequent increment; see
[milestone three](milestone-three-history.md) for the current archive/room scope.

## Fixed drawer action

The account-list page keeps Add account at the bottom of the drawer. Its list scrolls independently above the action, including during live updates. Account detail, add and capabilities pages retain their own scrolling layout.

`tests/browser/account-drawer.cjs` verifies three and 35 fixture accounts across 390, 818 and 1440 px, light/dark, English/pt-BR. It checks wheel scrolling, last-row clearance, stable footer position, live scroll/focus preservation, keyboard access to Add account and accessibility. Native `make check` passes. The bounded Dagger check attempt timed out after 45 seconds with exit 124; full Dagger completion remains unverified.
