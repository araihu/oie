# Room history and unread counts

The live room-message adapter previously restored the timestamp from an XMPP
delay element but omitted `Historical` when passing the message to the engine.
Cached room history could consequently increase the unread count despite being
inserted far above the currently viewed timeline.

[XEP-0045 discussion history](https://xmpp.org/extensions/xep-0045.html#enter-history)
requires a delayed-delivery stamp whose `from` is the room JID. The adapter now
classifies a room message as historical only when its timestamp parses and
that authority matches the room's bare JID. Other-room, occupant, server-only,
missing-authority and malformed stamps do not establish room history. Direct
offline delivery remains eligible for unread tracking.

This implements the existing distinction between retained history and new
traffic. It adds no API operation, database migration or new read policy. The
existing focused/visible read batches still apply only to observed messages.
Reaching the bottom does not clear unseen messages merely by timestamp.

The correction is prospective. Existing records do not retain the delay
authority needed to safely reclassify them retrospectively. An old timestamp
alone is insufficient evidence to alter a retained message or clear its unread
state. Those records require an explicit read operation or a separate repair
with provider evidence.

Work is isolated on `fix/room-history-unread`. No running engine, protocol
session or retained data directory is modified by this change.

Focused validation covers XML room-history ingestion, invalid authorities,
normal live traffic and direct offline delivery. Existing SQLite history and
read-evidence tests verify that historical messages do not add unread counts
and that unseen/new messages remain unread.

`make check` passes, including generation checks, lint, vet, application/CLI
tests and Compose validation. The initial host attempt hit a cache/temp-space
error; rerunning with task-owned temporary and lint caches passed.

The Dagger engine completed `make check test-race build build-cli` and the mock
build without errors. Its client connection failed health checks and the local
command subsequently timed out with exit 124, so this is not a successful CLI
completion. The run's engine evidence is saved in
`/tmp/oie-room-history-unread-dagger-engine.log`, and its client output is
`/tmp/oie-room-history-unread-dagger-final.log`.
