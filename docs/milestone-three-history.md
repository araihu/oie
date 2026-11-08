# Existing conversations and authorized archive recovery

Agreed scope carried forward: recover available history serially, retain it in
SQLite independently of provider retention, and preserve account isolation and
outbox ownership. This increment restores saved room entries and authorized text
archives; it does not implement complete room membership or room sending.
Room conversations remain explicitly read-only. Registration/connection alone
previously only enabled live direct-text ingestion, which left existing accounts
with an empty conversation list.

API first: GET /api/v1/accounts/{accountId}/history exposes persistent progress,
retained counts and partial/unsupported/error states. Account history events
refresh the UI. Existing message paging follows original UTC timestamps at millisecond precision and UUID
ties; opaque provider archive positions stay separate from UI/SSE cursors.

XMPP uses service discovery, MAM v2 with forward RSM pages, and saved room
bookmarks (modern PEP with legacy fallback). Only the authenticated account and
saved-room archive scopes may contribute results. Random active query IDs,
validated archive senders and forwarded peers, namespace checks and bounded
pages protect ingestion. One background network query at a time per engine,
with paced pages, avoids parallel archive floods. Interrupted pages replay
through durable archive aliases; checkpoints advance only after local commits.
Expired provider cursors restart the available range without deleting retained
records. Historical messages do not create unread counts or outbox work, and
older imports never replace a newer conversation preview.

References: [MAM](https://xmpp.org/extensions/xep-0313.html),
[RSM](https://xmpp.org/extensions/xep-0059.html),
[service discovery](https://xmpp.org/extensions/xep-0030.html),
[bookmarks](https://xmpp.org/extensions/xep-0402.html).

Not claimed: E2EE decryption, attachment transfer, full room joining/sending,
contact management, edits/retractions, read receipts, continuous multi-client
carbons, full sync or complete alpha delivery. Missing/forbidden room archives
remain partial coverage; saved room entries are still visible.


Recovery retries partial/transient failures after one minute, with a five-minute
backoff ceiling; unsupported archives wait for reconnection. Provider protocol
errors never disable direct messaging. The bounded text reader and page budget
can leave unsupported/oversized content as partial coverage. The API marks
archive messages with `historical: true`, allowing consumers to suppress fresh
notifications; UI unread counts remain unchanged by imports. Room speaker names
are observed nicknames under a room-scoped source, not verified personal JIDs.

Migration003 adds archive aliases/checkpoints/progress and a derived chronological
message index. Up/down/up verification preserves messages, revisions and outbox
intent. Downgrade removes derived history progress events/aliases, retaining
message data. Never force a dirty migration or replace a data directory.

Verification: `make check`, race tests, API/history ingestion tests, archive
sender/query/recipient validation, chronological cursor paging, live/archive
replay deduplication, cancelled-intent preservation, and migration down/up passed.
The real development Prosody test seeds history before Oie registration, restores
a saved-room entry, verifies no archive-generated unread/outbox work, reconnects
from its checkpoint, and recovers after an expired archive cursor without losing
or duplicating retained messages. Existing direct messaging/network interruption
and real-browser workflows remain covered. The adversarial browser test also
attempts a forged valid-CSRF room send and verifies zero provider effects.
