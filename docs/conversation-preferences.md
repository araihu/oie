# Conversation archive and favorites

Archive and favorite are retained conversation preferences. They persist in the
owning engine and are visible to its other browser clients. Archiving preserves
history and room membership. Incoming messages keep an archived conversation
archived and update its unread count normally. Conversation favorites and
source-message stars are separate metadata.

## Engine contract

API 0.20 adds `PATCH /api/v1/conversations/{conversationId}`. Supply an
`Idempotency-Key` UUIDv7 header, `accountId`, `expectedRevisionId` and at least one
explicit boolean, `archived` or `favorite`. Omitted fields remain unchanged;
null values and unknown properties are invalid.

The transaction checks an existing receipt before the revision precondition.
Repeating the same operation returns its original accepted snapshot with
`Idempotency-Replayed: true`. Reusing its key with different input returns an
idempotency conflict. Read the conversation again to obtain its current state.

A stale revision returns HTTP 409 with `currentConversation`. Message arrival
and read updates also change the conversation revision. A fresh action must use
that current revision and a new key. An uncertain retry keeps its original key,
precondition and desired values.

An actual change records an immutable revision and `conversation.updated` event
with its receipt. A matching no-op records only the receipt. The transaction
preserves activity order, activity time, unread count, messages, outbox and
unspecified preferences. The existing SQLite columns suffice; this increment
requires no migration or protocol operation.

The generated CLI accepts the same JSON through `--body-stdin` or `--body-file`.
Explicit `false` remains distinct from an omitted field.

## Browser mutation invariants

The header menu uses public Goshtoso dropdown items. Native POST forms remain
reachable with JavaScript disabled. Successful changes refresh canonical state
while retaining the selected conversation, draft, timeline and list position.
An implicitly selected desktop conversation is pinned into the current URL's
history entry before mutation, so filtered row removal cannot retarget live reads.

| State or action | Required outcome |
| --- | --- |
| Current revision | Send an explicit desired flag with its owning account and operation key; apply only canonical state. |
| Stale revision, HTTP 409 | Show current state without overwriting it. A subsequent explicit action uses a new key and revision. |
| Uncertain transport or HTTP 503 | Keep the exact intent for explicit retry; observed flags alone do not prove acceptance. Reload does not automatically resend it. |
| Accepted replay | Read fresh state after the receipt, so its historical response cannot undo a newer change. |
| Forged account, engine, CSRF or method | Reject without changing engine state or dispatching protocol work. |
| Native success | Use POST/Redirect/GET with a safe scoped return URL, preserving the selected conversation even when its list row disappears. |
| Incoming event or filtered row removal | Preserve the active draft, timeline and unaffected sibling rows. Archived arrivals remain archived and unread favorites retain both indicators. |

## Validation

Application, SQLite and independent HTTP tests cover revision and account guards,
field isolation, transaction rollback, concurrent arrivals/reads, no-op behavior,
receipt replay and restart. A real generated CLI test covers omitted values,
explicit false and the replay header. Browser and full pipeline results are
recorded in [local CI](local-ci.md).

The complete Dagger pipeline passed on `da7a27e`, including all 38 browser
workflows and actual two-way XMPP messaging against its disposable Prosody
service. The implicitly selected conversation regression first failed the older
binary and then passed the URL-pinning fix.

The live-preview engine was upgraded from API 0.19 to 0.20 after a consistent
SQLite backup. Its engine identity and all existing account, conversation,
message, revision and operation IDs were retained. The schema remains at clean
migration 11, and the previously connected account reconnected. No preference
mutation was tested against live user conversations.
Read-only BFF checks confirmed the new controls and navigation scripts. A stale
Air child had retained the preview port after earlier rebuilds; it was stopped
gracefully and the supervisor rebuilt the current source. A subsequent automatic
reload also passed with an event stream open, without restarting the engine.
