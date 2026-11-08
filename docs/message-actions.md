# Message action placement

The action trigger shares an application-owned header/content wrapper inside
Goshtoso's public `ChatBubble.MessageContent` slot. It anchors to the painted
bubble instead of the outer row, whose height includes avatars and previews.
Header and compact-body spacing reserve room for the trigger without covering
text. No dependency-private selectors are used for styling.

Oie pins Goshtoso `v0.6.2-0.20261108214005-3c5adf6acc24` from the open
[consumer integration PR #375](https://github.com/araihu/goshtoso/pull/375).
The generic collision handling is independently reviewable in
[popover PR #374](https://github.com/araihu/goshtoso/pull/374). Panels flip/shift
within the viewport, escape clipping ancestors through a native manual popover
where available, and scroll when oversized. Focus, transitions and observer
cleanup remain library responsibilities.

## Verification on 2026-11-08

`make check` and Dagger `make ci-check` pass. The browser star/action fixture
covers 42 message-shape, viewport and light/dark combinations at 390, 818 and
1440 pixels, with zero trigger/content overlaps or out-of-viewport menus.
Keyboard, touch, Escape/focus restoration, original copying, cached-image saving,
source stars and accessibility checks also pass.

A read-only live observation of the reported message confirms the trigger is
inside the bubble, 4 pixels from the top and 6 from the right. The opened menu
fits the viewport; Escape restores focus. No mutation requests or page errors
occurred during that observation.

The original placement pin, `v0.6.2-0.20261108205852-5fad228c5fb9`, passed focused
Popover/Dropdown/Dialog browser tests, unit/build checks and lint. The current pin
also includes the searchable account-filter fixes from
[combobox PR #376](https://github.com/araihu/goshtoso/pull/376). Full upstream
browser-suite completion remains unverified; these PRs remain draft.
