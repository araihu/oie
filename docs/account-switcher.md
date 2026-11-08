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
