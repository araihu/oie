# Source message stars

## Agreed scope

A favorite in the image viewer stars the source message. Multiple image previews
of one message share that state. Message stars are independent of conversation
favorites, content edits, read state and protocol capabilities. This increment
adds the owning-engine implementation. Device synchronization remains later work.

## Contract and storage

API 0.6.0 adds:

- `GET /api/v1/messages/{messageId}/star` for current state.
- `PUT` on the same path for an explicit desired value, a UUIDv7
  `Idempotency-Key`, and mandatory `expectedRevisionId`, which is null initially.
- `GET /api/v1/conversations/{conversationId}/message-stars` with 1–100 repeated
  `messageId` parameters for the displayed messages. A mixed foreign or missing
  source rejects the entire batch.
- `GET /api/v1/starred-messages` with account/conversation filters and bounded
  keyset pagination. Items include the current source message and conversation.
- Durable `message.star_changed` events with the complete source-linked star.

An untouched message reads as false with null revision/time. Reads do not create
rows. Each actual change creates a UUIDv7 revision in a separate immutable star
history, whose parent is the previous star revision. The stable key is the source
message UUID. The message content revision never changes because of a star.

Migration 006 adds only star state and its revision table. Existing messages,
content revisions, outbox work and other data remain intact. An explicit schema
downgrade removes star metadata, receipts and star events, preserving the source
messages and pending sends. Upgrades never reset the data directory or force a
dirty journal.

The collection currently sorts by original UTC message timestamp at millisecond
precision descending,
then message UUID descending. This is an implementation choice, separate from
the product requirement to browse starred messages. Unstarring a cursor message
does not invalidate the retained source used for paging. New stars can appear
before a page already read; pages do not promise a frozen snapshot.

## Action invariants

| State or action | Required outcome |
| --- | --- |
| First star | Compare null revision and commit state, revision, receipt and event together. |
| Known current revision | Compare atomically, including when the requested value already matches. |
| Stale tab | Return 409 `state_conflict` with `currentStar`; do not retry against a fresh revision automatically. |
| Lost response | Retry the exact key, desired value and precondition; reconcile current state with GET. |
| Replayed old operation | Return its original snapshot without changing newer star state. |
| Reused key with different input | Return `idempotency_conflict`. |
| Matching value with matching revision | Record a receipt without another revision or event. |
| Storage failure | Roll back state, revision, receipt and event together. |
| Queued or cancelled message | Star metadata leaves send evidence, cancellation preconditions and outbox ownership intact. |
| Read-only saved room | Allow app-owned stars without joining or sending to the room. |
| Provider retention or connectivity | Use retained local state; issue no protocol operation or media download. |

A receipt describes acceptance at that operation, not the latest state after
other devices or tabs act. Consumers must read current state before presenting
it after replay or uncertainty. This also applies when later source-message
revisions change the content returned by the starred collection.

## Verification

Backend/API race tests cover concurrent stale tabs, exact replay, no-op receipts,
revision/event deduplication, injected failures at each transactional write,
restart, schema down/up, queued-send cancellation, source chronology with UUID
ties, account/conversation isolation, batch rejection and cursor continuity.
The generated CLI exercises explicit null preconditions, replay, current source
projections and operation receipts. A saved-room test reads the actual SSE event
without enabling room sending.

The interface now exposes a source-message chevron menu with Copy original and
Star/Unstar. A single-image message shares that menu with Open, Image info and
Save original; multiple-image messages have scoped image menus. The expanded
viewer has a compact sender/avatar/device-time toolbar with Star, Info and Close.
It preserves the image-info popover and omits the technical file-identification
note. Save reads the cached original through the same-origin BFF.

UI checks cover two tabs, exact-key retries across reload, stale conflicts,
provider-free cached saving, clipboard source preservation, denied storage,
keyboard/touch interaction and Brazilian Portuguese mobile accessibility. Delayed
SSE events invalidate state rather than overwriting newer authoritative reads.
The real engine/Prosody browser workflow verifies star/unstar across two tabs,
preserved draft/focus, account-copy isolation and no additional XMPP message.
`make check`, changed-package race tests and native XMPP integration pass.

The live engine is API 0.6.0/schema 6. Migration verification preserved all prior
account, conversation, message and content-revision UUIDs, the engine UUID and
outbox state. Browser inspection of retained room images verified the source
header, unavailable-note removal, known star state and no page errors/overflow.

A full Dagger run remains unverified because its client timed out after 180
seconds. See [local CI status](local-ci.md). Zoom/navigation, sharing, Copy image
and protocol actions remain outside this increment. The starred collection is
available through the engine API and generated CLI; no separate library UI has
been added.

The reusable public `modal.DialogConfig.HeaderContent` slot is supplied by
[Goshtoso PR 373](https://github.com/araihu/goshtoso/pull/373), pinned at
`v0.6.2-0.20261108145912-0cfde7dfc89c`.
