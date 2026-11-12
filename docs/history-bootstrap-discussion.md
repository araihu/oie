# Initial room-history recovery discussion

Recorded on 2026-11-10 from testing a fresh standalone installation.
Tracking item: **B25, required for alpha v0.0.1**. The room recovery changes
described under Implementation are included in v0.0.1-rc.1.

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

In XMPP, use the normalized domain of the archive address as the queue key.
Rooms on the same domain share request limits across accounts. Serialize
archive requests within each domain and alternate room pages in that queue.
Different domains use independent limits and can recover history in parallel.

When the current archive query finishes, give pending search and scroll
requests the next available turn on that server. Wait at least one second
between automatic backfill pages on the same server. Search and scroll
requests can use that interval.

## Agreed recovery checkpoints

Advance the archive cursor only after the entire page has been stored. Keep
the archive timestamp at the confirmed page boundary for the current paging
direction. If the cursor expires, resume from that timestamp within the
original window. Include the boundary timestamp so messages sharing it can
be recovered. Deduplicate reread messages by their archive identity.

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

Wait for a short typing pause before starting remote search. Within each
active room search, keep only the latest term queued for the server. Apply
arriving results to the interface only if they match the active search.

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

## Implementation

Engine API 0.28.0 exposes the room recovery mode and progress under
`/api/v1/conversations/{conversationId}/archive`. The browser can prioritize
the initial page, ask for older messages, fetch context around a retained
message or search a capable room archive. Reads of saved progress use SQLite.
Migration 17 adds one mode and one recovery checkpoint per room. Existing
history and the direct-message archive checkpoint remain available.

The XMPP adapter requests up to 50 entries per page. After it has covered
the 48-hour window, the bootstrap requests only enough older entries to
fill the remaining gap to 128 retained messages. Each domain takes one
automatic turn at a time and alternates rooms. Initial pages take priority
over older sweeps within the domain. Explicit archive requests can use the
one-second pause between automatic turns. A reconnect also catches up on
messages newer than the original window.

Room search waits 350 ms before requesting a page from the server.
The browser keeps local results available while that request runs. The XMPP
adapter enables server search for a room that advertises both MAM and the
XEP-0431 discovery feature. Opening an isolated search hit can fetch a page
on each side. Those pages and individual hits do not move the continuous
recovery checkpoint.

## Verification

Cover active rooms, rooms quiet for more than 48 hours, rooms with fewer than
128 messages, empty or inaccessible archives, and interruption/resume. Verify
live sending/receiving and navigation while recovery is in progress, eventual
older-history coverage, live/archive deduplication and preservation of deletion
markers. Historical imports must not create new unread messages or notifications.
Verify that a busy open room receives its first page promptly and that other
rooms receive pages before the open room completes its initial target.
Verify that rooms with the same archive domain share its limits across
accounts. A slow request to one archive domain lets another domain continue.
Verify that a pending search or scroll request gets the next turn after an
in-flight page, including during the one-second interval before the next
automatic backfill page.
Interrupt recovery, advance the clock and verify that the resumed query uses
the same UTC bounds for that room.
Interrupt a page write and verify that its cursor has not advanced. Expire a
cursor at a boundary shared by several messages and verify that resuming keeps
the original window and recovers those messages without duplicates.
Verify that on-demand rooms fetch older pages when requested, and that local
and remote hits merge without duplicates or advancing continuous coverage.
Verify that search remains usable with a slow server or an archive that only
supports page retrieval.
Verify that rapid typing queues only the latest remote search and that a late
response leaves the current results intact.

The existing history implementation and its checks are described in
[the history milestone](milestone-three-history.md). The agreed retention and
provider-pacing requirements remain in [the specification](oie-spec.md#7-history-search-and-shared-links).
