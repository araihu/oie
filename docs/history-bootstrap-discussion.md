# Initial room-history recovery discussion

Recorded on 2026-11-10 from testing a fresh standalone installation.
Tracking item: **B25, first backlog priority before alpha v0.0.1**. The rc.0
packages are published. This discussion now defines the recovery policy;
implementation has not started.

## Agreed goals and release boundary

- Fresh-install room-history recovery currently makes the app unpleasant to use.
  Improve it before the first alpha, **v0.0.1**.
- The current behavior is acceptable in the published **v0.0.1-rc.0**.
- Make recent conversation history usable without waiting for the full archive.
  The user must be able to send, receive, navigate and otherwise interact while
  older history and local derived data are recovered in the background.
- Still recover all available authorized history, paced and resumable, and
  retain it until explicit deletion. Prioritizing recent messages does not
  reduce the retention scope or erase existing local history.

## Agreed initial history target

When initial recovery starts for a room, set the end of its window to that
instant and the start to 48 hours earlier. Persist both bounds in UTC and reuse
them when resuming the same recovery after an interruption or reconnect.

For each room, prioritize messages from the **last 48 hours**. If that interval
contains fewer than **128 messages**, include the immediately preceding messages
until the room has 128 messages, or the available authorized archive is
exhausted. The fallback also applies when the interval contains some messages;
it is not limited to rooms with no recent activity.

- 20 messages in the interval: include up to 108 earlier messages.
- No messages in the interval: include up to the latest 128 messages.
- More than 128 messages in the interval: retain the full 48-hour target;
  128 is not a cap on recent history.
- Fewer than 128 messages available in total: retain what is available.

## Agreed recovery order

Start with the newest page of the open room when it needs recovery. Alternate
pages across rooms and give the open room preference while each room with
pending work continues to receive turns. Show each page as soon as it is stored.
Keep navigation, sending and receiving available throughout recovery.

Prioritize enough recent history to give each room useful context quickly.
Continue towards the agreed initial target for each room. Fetch the remaining
authorized history gradually from the server in the background. Page limits,
how often the open room gets a turn and scheduling across accounts remain open.

## Open decisions for the next discussion

- What page, message and time budgets keep a busy room from delaying the others?
- What turn frequency gives the open room preference while preserving progress
  across rooms and accounts? Keep requests paced and live traffic responsive.
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
Verify that a busy open room receives its first page promptly and that other
rooms receive pages before the open room completes its initial target.
Interrupt recovery, advance the clock and verify that the resumed query uses
the same UTC bounds for that room.

The existing history implementation and its checks are described in
[the history milestone](milestone-three-history.md). The agreed retention and
provider-pacing requirements remain in [the specification](oie-spec.md#7-history-search-and-shared-links).
