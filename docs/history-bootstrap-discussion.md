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
- Recover all available authorized history gradually by default. Allow the
  user to select on-demand recovery per room. Retain imported messages and
  new messages until explicit deletion in either mode.

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
authorized history gradually from the server in the background.

Rooms on the same server share its recovery queue and request limits, including
rooms reached through different accounts. Serialize history requests within
each server. Different servers use independent limits and can recover history
in parallel. The round robin runs within each server's queue.

## Gajim comparison

Gajim 2.7.0 starts room archive recovery after joining a room that advertises
MAM. Without a saved cursor, it requests messages from the preceding day. On
later joins, it can resume from the saved archive ID or use a configured time
threshold. [Room join](https://github.com/gajim/gajim/blob/048a2f3d986a68ca07bde52a2f6a22ef2926188b/src/gajim/common/modules/muc.py#L1122-L1139)
and [query bounds](https://github.com/gajim/gajim/blob/048a2f3d986a68ca07bde52a2f6a22ef2926188b/src/gajim/common/modules/mam.py#L266-L315).

Each archive task awaits a page, saves its last archive ID and requests the next
page directly. Gajim tracks active queries by archive JID in each account's MAM
module. The inspected path has no fixed delay between pages or shared limit by
server, so tasks for different rooms can overlap. The nbxmpp 7.5.0 version
bundled by the Flatpak build requests up to 70 results per page by default.
[Gajim pagination](https://github.com/gajim/gajim/blob/048a2f3d986a68ca07bde52a2f6a22ef2926188b/src/gajim/common/modules/mam.py#L424-L465),
[query tracking](https://github.com/gajim/gajim/blob/048a2f3d986a68ca07bde52a2f6a22ef2926188b/src/gajim/common/modules/mam.py#L238-L246)
and [nbxmpp page size](https://github.com/gajim/python-nbxmpp/blob/c3cc88b25e99a42bf936041800f59e478b3459cf/nbxmpp/modules/mam.py#L39-L53).

## Agreed recovery modes and room search

Gradual backfill is the default. Each room can instead use on-demand recovery,
which fetches older pages when needed for navigation or search. Both modes
use the agreed initial history target and retain new messages, fetched pages
and authorized search hits in SQLite. Unvisited history can expire on the
server, and local searches cover the messages already stored.

For a room search, query SQLite and, when its archive supports text search,
query the server in parallel. Show local results as soon as they are ready
and add remote matches as they arrive. Store the imported matches through
historical ingestion and deduplicate by canonical ID. Track continuous archive
coverage separately from individual hits. Fetch surrounding messages as needed
when opening a remote hit.

Give timeline and search requests priority over backfill in the server's
queue. Report the available search scope when coverage is partial.
The initial recommendation for global search is to use local data.

## XMPP server search feasibility

XMPP MAM supports fetching archive pages by time and cursor. Full-text search
is optional. XEP-0431 is Deferred and leaves matching semantics to the
server. It defines a separate discovery feature for text search. Ejabberd
25.07 added support, and MongooseIM 6.2.1 documents its own query field.
Check capability and behavior for the specific archive before enabling remote
search. [MAM](https://xmpp.org/extensions/xep-0313.html),
[XEP-0431](https://xmpp.org/extensions/xep-0431.html),
[ejabberd 25.07](https://www.process-one.net/blog/ejabberd-25-07/) and
[MongooseIM](https://esl.github.io/MongooseDocs/6.2.1/modules/mod_mam/).

## Open decisions for the next discussion

- What page, message and time budgets keep a busy room from delaying the others?
- When does typing trigger remote search, and how are obsolete searches
  cancelled or superseded?
- How does each protocol adapter identify which archives share a server?
- What turn frequency gives the open room preference while preserving progress
  across rooms on the same server? Keep requests paced and live traffic responsive.
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
Verify that rooms on the same server share its limits across accounts and that
a slow request to one server lets recovery continue on another server.
Interrupt recovery, advance the clock and verify that the resumed query uses
the same UTC bounds for that room.
Verify that on-demand rooms fetch older pages when requested, and that local
and remote hits merge without duplicates or advancing continuous coverage.
Verify that search remains usable with a slow server or an archive that only
supports page retrieval.

The existing history implementation and its checks are described in
[the history milestone](milestone-three-history.md). The agreed retention and
provider-pacing requirements remain in [the specification](oie-spec.md#7-history-search-and-shared-links).
