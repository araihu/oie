# Initial room-history recovery discussion

Recorded on 2026-11-10 from testing a fresh standalone installation.
Tracking item: **B25, first backlog priority after the current rc.0 packaging
work**. This is a discussion record, not an implemented recovery policy.

## Agreed goals and release boundary

- Fresh-install room-history recovery currently makes the app unpleasant to use.
  Improve it before the first alpha, **v0.0.1**.
- The current behavior is acceptable in **v0.0.1-rc.0**. Continue the current
  desktop installation/packaging work; do not implement this item now.
- Make recent conversation history usable without waiting for the full archive.
  The user must be able to send, receive, navigate and otherwise interact while
  older history and local derived data are recovered in the background.
- Still recover all available authorized history, paced and resumable, and
  retain it until explicit deletion. Prioritizing recent messages does not
  reduce the retention scope or erase existing local history.

## User proposal to develop

For each room, restore messages from the **last 48 hours** first. For a room
without recent activity, fall back to its **latest 128 messages** so a new
installation can show conversation context even when the room has been quiet
for a long time. Continue recovering the remaining archive in the background.

The 48-hour window and 128-message fallback are the proposed starting point.
They have not been implemented or validated against protocol archive queries.

## Open decisions for the next discussion

- Does fallback apply only when the 48-hour query returns no messages, or should
  rooms with fewer than 128 recent messages also load older messages to fill it?
- What anchors the 48-hour cutoff, and what page/message/time budgets keep
  a very active room from delaying every other room? Store time bounds in UTC.
- How should the selected room, other rooms and multiple accounts share the
  queue while retaining provider pacing and foreground/live-message priority?
- How do recent coverage and older backfill checkpoints resume after reconnect,
  interruption or an expired archive cursor, without restarting a complete scan?
- Which protocol capabilities and archive failures need fallbacks? How do we
  keep database writes, indexing and preview work from blocking interaction?

These choices must be resolved before treating the proposal as an exact API or
adapter contract. Keep the detailed operation/state design out of product UI.

## Verification to define before implementation

Cover active rooms, rooms quiet for more than 48 hours, rooms with fewer than
128 messages, empty or inaccessible archives, and interruption/resume. Verify
live sending/receiving and navigation while recovery is in progress, eventual
older-history coverage, live/archive deduplication and preservation of deletion
markers. Historical imports must not create new unread messages or notifications.

The existing history implementation and its checks are described in
[the history milestone](milestone-three-history.md). The agreed retention and
provider-pacing requirements remain in [the specification](oie-spec.md#7-history-search-and-shared-links).
