# Independent engine API audit

Audit baseline: `47870bd4be4c37a61b934237f39a8a4ef46867f7` (Wails integration,
23 commits beyond main `115e8eb`). The working handoff was committed in `3354b4f`.
Date: 2026-11-10. This is a source audit; executed evidence is recorded separately
in [verification](engine-api-cli-verification.md). Historical green CI is not
verification of this increment.

## Findings and priorities

| Finding | Baseline verdict | Priority | Correction / remaining scope |
| --- | --- | --- | --- |
| Runtime engine `0.27.0` violates Engine.apiVersion enum | inconsistent | P1 | Accept semantic contract versions; validate actual responses, including negative controls. |
| Engine roles always construct and serve BFF | missing | P1 | Add opt-in data-only composition; retain role and default behavior. |
| Four implemented binary/SSE operations excluded from CLI | missing | P1 | Enable generated raw commands; prove bounded stream consumption, resumption, bytes and failures. |
| BFF read-state, preview and thumbnail routes share engine paths with different header/CSRF rules | inconsistent | P1 | Move browser adapters under `/ui/api/v1`; engine paths remain structured API paths. |
| No explicit compatibility baseline/check | missing | P1 | Pin a reviewed baseline and check breaking changes plus real response validation. |
| Open conversation lacks Location; accepted commands have body IDs but no polling header | inconsistent | P2 | Add Location on creation/acceptance where a resource can be polled; clients poll with bounded delay. |
| Unknown method returns 405 without Allow; content-type/body limit collapse into 400 | inconsistent | P2 | Preserve stable Problem codes; document/cover middleware status behavior. |
| Conditional original-media GET has no If-None-Match/304 | intentionally unsupported | P3 | Engine originals are private no-store; If-Range protects ranged reads. Do not imply BFF cache behavior exists in engine. |
| Resource ETags/If-Match on JSON commands | intentionally unsupported | P3 | Existing body revisions return 409. Introducing an HTTP precondition is an OPEN decision. |
| Authentication, quotas, event expiry, SDK, WASM | intentionally unsupported | backlog/open | Do not introduce them to prove CLI independence. |
| Correlation IDs and measured rate limits | unknown | P3 | No contract or evidence of an implemented policy; do not invent limits. |

## Operation inventory

All engine operations below are in `api/openapi.yaml`, implemented through
`internal/adapters/httpapi` and application-owned ports. HTTP tests are in that
adapter's corresponding `*_test.go` files (names below). `tests/cli/client_test.go`
covers a subset operationally; generation alone is command availability, not
end-to-end coverage. The route list identifies the baseline contract; it does not assert
that every failure branch was exercised. All rows are engine-owned; BFF exceptions
are listed separately. Priority `—` means no evidenced correction required.

| Contract operation (method and path) | Actual behavior / source | Owner | CLI at baseline | Test evidence | Verdict | Priority | Proposed correction |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `listContacts` GET `/api/v1/contacts` | Register persists receipt/account/revision/event atomically; bounded reads; `contacts.go` | engine | generated + real-store test | `server_test.go` | implemented | — | — |
| `searchMessages` GET `/api/v1/messages/search` | Filter-before-limit; retained content and optional edit history; `message_search.go` | engine | generated + real-store test | `message_search_test.go` | implemented | — | — |
| `listSharedContent` GET `/api/v1/shared-content` | Retained catalog; incidental preview scheduling; `shared_content.go` | engine | generated + real-store test | `shared_content_test.go` | implemented | — | — |
| `getVideoThumbnail` GET `/api/v1/shared-content/{itemId}/thumbnail` | Cached metadata; GET does not request extraction; `previews.go` | engine | generated; operational branch unverified | `video_thumbnails_test.go` | implemented | — | — |
| `requestVideoThumbnail` POST `/api/v1/shared-content/{itemId}/thumbnail` | Explicit derived work; coalesced, no durable operation receipt; `previews.go` | engine | generated; operational branch unverified | `video_thumbnails_test.go` | implemented | — | — |
| `getSharedContentMedia` GET `/api/v1/shared-content/{itemId}/content` | Explicit bounded original bytes/range; no-store and If-Range; `shared_media.go` | engine | excluded | `shared_media_test.go` | implemented | P1 | Enable raw CLI and exercise |
| `getMessageContext` GET `/api/v1/conversations/{conversationId}/messages/{messageId}/context` | Bounded source window; `replies.go` | engine | generated + real-store test | `message_search_test.go` | implemented | — | — |
| `getMessageStar` GET `/api/v1/messages/{messageId}/star` | App-owned retained star; `stars.go` | engine | generated; operational branch unverified | `stars_test.go` | implemented | — | — |
| `setMessageStar` PUT `/api/v1/messages/{messageId}/star` | Durable key + revision CAS; replay returns original snapshot; `stars.go` | engine | generated + real-store test | `stars_test.go` | implemented | — | — |
| `getConversationReadState` GET `/api/v1/conversations/{conversationId}/read-state` | Scoped retained unread count; `read_state.go` | engine | generated; operational branch unverified | `read_state_test.go` | implemented | — | — |
| `markConversationMessagesRead` POST `/api/v1/conversations/{conversationId}/read-state` | Durable key, normalized ID set; replay original snapshot, no provider receipt; `read_state.go` | engine | generated; operational branch unverified | `read_state_test.go` | implemented | — | — |
| `listConversationMessageStars` GET `/api/v1/conversations/{conversationId}/message-stars` | Bounded source-linked star projections; `stars.go` | engine | generated; operational branch unverified | `stars_test.go` | implemented | — | — |
| `listStarredMessages` GET `/api/v1/starred-messages` | Scoped cursor and retained source projection; `stars.go` | engine | generated + real-store test | `stars_test.go` | implemented | — | — |
| `listConversationPreviews` GET `/api/v1/conversations/{conversationId}/previews` | Incidental cached enrichment of retained sources; `previews.go` | engine | generated; operational branch unverified | `previews_test.go` | implemented | — | — |
| `getMessagePreview` GET `/api/v1/previews/{previewId}` | Retained preview status; no original download; `previews.go` | engine | generated; operational branch unverified | `previews_test.go` | implemented | — | — |
| `refreshMessagePreview` POST `/api/v1/previews/{previewId}/refresh` | Explicit refresh; current pending work coalesces, no durable receipt; `previews.go` | engine | generated; operational branch unverified | `preview_refresh_test.go` | implemented | — | — |
| `downloadPreviewImage` POST `/api/v1/previews/{previewId}/download` | Explicit image retention, bounded; coalesces pending/ready work; `previews.go` | engine | generated; operational branch unverified | `previews_test.go` | implemented | — | — |
| `getMediaImage` GET `/api/v1/media/{hash}` | Verified retained image hash and private immutable conditional bytes; `previews.go` | engine | excluded | `previews_test.go` | implemented | P1 | Enable raw CLI and exercise |
| `getHealth` GET `/api/v1/health` | Readiness/identity; private no-store JSON; `server.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `getEngine` GET `/api/v1/engine` | Persistent identity and cursor; runtime version outside enum; `server.go` | engine | generated; operational branch unverified | `engine_version_test.go` | inconsistent | P1 | Response schema and runtime validation |
| `listAccounts` GET `/api/v1/accounts` | Register persists receipt/account/revision/event atomically; bounded reads; `server.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `registerAccount` POST `/api/v1/accounts` | Register persists receipt/account/revision/event atomically; bounded reads; `server.go` | engine | generated + real-store test | `server_test.go` | implemented | — | — |
| `getAccount` GET `/api/v1/accounts/{accountId}` | Register persists receipt/account/revision/event atomically; bounded reads; `server.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `listAccountAvatars` GET `/api/v1/accounts/{accountId}/avatars` | Register persists receipt/account/revision/event atomically; bounded reads; `avatars.go` | engine | generated; operational branch unverified | `avatars_test.go` | implemented | — | — |
| `getAvatarImage` GET `/api/v1/avatars/{hash}` | Register persists receipt/account/revision/event atomically; bounded reads; `avatars.go` | engine | excluded | `avatars_test.go` | implemented | P1 | Enable raw CLI and exercise |
| `getAccountHistory` GET `/api/v1/accounts/{accountId}/history` | Register persists receipt/account/revision/event atomically; bounded reads; `messaging.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `getAccountCapabilities` GET `/api/v1/accounts/{accountId}/capabilities` | Register persists receipt/account/revision/event atomically; bounded reads; `capabilities.go` | engine | generated + real-store test | `server_test.go` | implemented | — | — |
| `getOperation` GET `/api/v1/operations/{operationId}` | Durable acceptance lookup; succeeded does not mean delivery; `server.go` | engine | generated + real-store test | `server_test.go` | implemented | — | — |
| `streamEvents` GET `/api/v1/events` | 100-event batches, scoped canonical cursor, heartbeat/write deadlines; `server.go` | engine | excluded | `server_test.go` | implemented | P1 | Enable raw CLI and exercise |
| `getAccountConnection` GET `/api/v1/accounts/{accountId}/connection` | Register persists receipt/account/revision/event atomically; bounded reads; `messaging.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `configureAccountConnection` PUT `/api/v1/accounts/{accountId}/connection` | Register persists receipt/account/revision/event atomically; bounded reads; `messaging.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `disconnectAccount` DELETE `/api/v1/accounts/{accountId}/connection` | Register persists receipt/account/revision/event atomically; bounded reads; `messaging.go` | engine | generated; operational branch unverified | `server_test.go` | implemented | — | — |
| `listConversations` GET `/api/v1/conversations` | Account-scoped reads; activity ordering can move between pages; `messaging.go` | engine | generated; operational branch unverified | `timeline_test.go` | implemented | — | — |
| `openConversation` POST `/api/v1/conversations` | Durable key; existing scope returns 200; new scope 201; `messaging.go` | engine | generated + real-store test | `messaging_test.go` | inconsistent | P2 | Location on 201 |
| `getRoomSession` GET `/api/v1/conversations/{conversationId}/room-session` | Current room intent and observed state; `room_sessions.go` | engine | generated + real-store test | `room_sessions_test.go` | implemented | — | — |
| `joinRoom` POST `/api/v1/conversations/{conversationId}/room-session` | Explicit durable join intent; replay does not reactivate after leave; `room_sessions.go` | engine | generated + real-store test | `room_sessions_test.go` | implemented | — | — |
| `leaveRoom` POST `/api/v1/conversations/{conversationId}/room-session/leave` | Explicit durable leave intent; revision CAS; `room_sessions.go` | engine | generated + real-store test | `room_sessions_test.go` | implemented | — | — |
| `getMentionCandidates` GET `/api/v1/conversations/{conversationId}/mention-candidates` | Verified scope and UTF-16 mention selections; `mentions.go` | engine | generated; operational branch unverified | `mentions_test.go` | implemented | — | — |
| `getConversationDetails` GET `/api/v1/conversations/{conversationId}/details` | Read-only scoped snapshot, including unknown observations; `conversation_details.go` | engine | generated + real-store test | `conversation_details_test.go` | implemented | — | — |
| `getConversation` GET `/api/v1/conversations/{conversationId}` | Account-scoped reads; activity ordering can move between pages; `messaging.go` | engine | generated; operational branch unverified | `timeline_test.go` | implemented | — | — |
| `updateConversationPreferences` PATCH `/api/v1/conversations/{conversationId}` | Durable key + revision CAS; replay original snapshot; `conversation_preferences.go` | engine | generated; operational branch unverified | `conversation_preferences_test.go` | implemented | — | — |
| `listMessages` GET `/api/v1/conversations/{conversationId}/messages` | Durable queued intent / original acceptance replay; bounded reads; `messaging.go` | engine | generated + real-store test | `messaging_test.go` | implemented | — | — |
| `sendMessage` POST `/api/v1/conversations/{conversationId}/messages` | Durable queued intent / original acceptance replay; bounded reads; `messaging.go` | engine | generated + real-store test | `messaging_test.go` | implemented | — | — |
| `getMessageReactions` GET `/api/v1/messages/{messageId}/reactions` | Scoped observed actors and commands; `reactions.go` | engine | generated; operational branch unverified | `reactions_test.go` | implemented | — | — |
| `setMessageReactions` POST `/api/v1/messages/{messageId}/reactions` | Durable command, observed protocol result remains separate; `reactions.go` | engine | generated; operational branch unverified | `reactions_test.go` | implemented | — | — |
| `listConversationReactions` GET `/api/v1/conversations/{conversationId}/reactions` | Scoped bounded batch; `reactions.go` | engine | generated; operational branch unverified | `reactions_test.go` | implemented | — | — |
| `getMessageReactionCommand` GET `/api/v1/message-reaction-commands/{commandId}` | Retained command state; `reactions.go` | engine | generated; operational branch unverified | `reactions_test.go` | implemented | — | — |
| `getMessageEditTarget` GET `/api/v1/messages/{messageId}/edit-target` | Latest supported original from current verified connection only; `message_edits.go` | engine | generated; operational branch unverified | `message_edits_test.go` | implemented | — | — |
| `getConversationEditTarget` GET `/api/v1/conversations/{conversationId}/edit-target` | Current eligible edit target; `message_edits.go` | engine | generated; operational branch unverified | `message_edits_test.go` | implemented | — | — |
| `editMessage` POST `/api/v1/messages/{messageId}/edits` | Durable edit command, current verified scope, replay first; `message_edits.go` | engine | generated; operational branch unverified | `message_edits_test.go` | implemented | — | — |
| `getMessageEdit` GET `/api/v1/message-edits/{editId}` | Retained edit command state; `message_edits.go` | engine | generated; operational branch unverified | `message_edits_test.go` | implemented | — | — |
| `getMessageEditHistory` GET `/api/v1/messages/{messageId}/edit-history` | Snapshot-bound edit pagination, 409 history_changed; `message_history.go` | engine | generated; operational branch unverified | `message_history_test.go` | implemented | — | — |
| `getMessageSenderProfile` GET `/api/v1/messages/{messageId}/sender-profile` | Retained identity and bounded session profile cache; `sender_profile.go` | engine | generated; operational branch unverified | `sender_profile_test.go` | implemented | — | — |
| `getMessage` GET `/api/v1/messages/{messageId}` | Durable queued intent / original acceptance replay; bounded reads; `messaging.go` | engine | generated; operational branch unverified | `messaging_test.go` | implemented | — | — |
| `cancelMessage` POST `/api/v1/messages/{messageId}/cancel` | Atomic queued cutoff; stale revision 409; replay first; `messaging.go` | engine | generated + real-store test | `messaging_test.go` | implemented | — | — |

## Ownership exceptions

| Baseline route | Actual behavior | Owner | Evidence | Verdict / correction |
| --- | --- | --- | --- | --- |
| `/api/v1/conversations/{id}/read-state` GET/POST in BFF/native presentation | Engine/account headers, cookie/CSRF on POST; network engine mount bypasses BFF and has public API semantics | BFF adapter | `web/read_state.go`, `apphost/server.go`, `apphost/server_test.go` | inconsistent; move browser path under `/ui/api/v1` |
| `/api/v1/previews/{id}` GET in BFF/native | Engine header validation and BFF projection | BFF adapter | `web/previews.go`, `web/server.go` | inconsistent; same move |
| `/api/v1/shared-content/{id}/thumbnail` GET in BFF/native | Engine binding and validated projection | BFF adapter | `web/video_thumbnails.go`, `web/server.go` | inconsistent; same move |
| `/ui/*` HTML/actions, assets, BFF original downloads | Browser session/CSRF and presentation contracts; never the CLI transport | BFF | `api/bff.openapi.yaml`, `api/ui-preview.openapi.yaml`, `api/cache.openapi.yaml` | implemented; preserve regressions |

## Receipt and recovery audit

Keys are UUIDv7 in a single engine-wide operation namespace, not per URL/account.
`sqlite/store.go:operation` checks kind and normalized payload fingerprint;
`begin` transactions plus one DB connection serialize contenders. Failed
transactions roll back their inserted receipt. No receipt expiry or change-log
pruning is implemented: receipts/events persist with the data directory.

| Mutation | Fingerprint / guard | Receipt result / retry |
| --- | --- | --- |
| Register | Canonical protocol/address + trimmed label | Original account snapshot; 201 then 200 |
| Open direct conversation | Account + canonical peer | Original scoped conversation; existing conversation can return 200 |
| Send | Conversation + exact authored content + frozen reply/mention input | Original queued acceptance; persisted message/outbox/event in one transaction |
| Cancel | Message + expected revision | Atomic queued state cutoff; replay before guard |
| Star / preferences | Target + desired state + expected revision | Original acceptance snapshot, even after later writes; GET the resource for current state |
| Read-state | Conversation + sorted/deduplicated source IDs | Original acceptance snapshot; GET returns current read state; does not send XMPP receipts |
| Room join/leave | Conversation + intent and revision | Current room projection; old join replay cannot undo later leave |
| Edits / reactions | Scoped target, revision/command details | Durable command acceptance; protocol observation remains separate |
| PUT/DELETE connection | Desired configuration / disconnected state | Intentionally no operation receipt; repeated PUT may reconnect. Password never returned |
| Preview download/refresh / thumbnail | Scoped current work | Intentionally no operation receipt; concurrent pending work coalesces. A later explicit refresh may fetch again |

`messaging_test.go` covers committed response loss, restart, dispatch cutoff,
unknown outcomes and queued recovery; `server_test.go` covers concurrent
registration and lost responses. Protocol exactly-once delivery is not promised.
A cancelled HTTP client cannot infer rollback after commit. Retry an uncertain
send only with the original key/body after operation lookup.

## Shared policy and exceptions

See [contract policy and decisions](engine-api-policy.md). Inventory verdicts
are bounded to source/test evidence, not compliance with an entire standard.
Unknown failures and cross-provider interoperability remain unknown until tested.

## Implemented corrections

The implementation corrects all P1 findings above: data-only composition,
separate browser paths, runtime version schema, raw binary/SSE CLI availability
and a pinned compatibility gate. It also adds Location on conversation creation,
connection/send/edit/reaction acceptance; 405 Allow; 413/415 request rejection;
and missing 503 declarations for details/profile reads. Runtime JSON validation
is now part of the existing HTTP test request helper, with deliberate negative
controls. Dedicated media/SSE tests verify streamed representations separately.

Reproduce adapter evidence with `GOFLAGS=-tags=server go test
./internal/adapters/httpapi ./internal/apphost ./tests/architecture`; reproduce
the operational fixture with `make test-cli-xmpp` and the development endpoint/CA
variables described in [verification](engine-api-cli-verification.md).
Other JSON operations retain their generated commands and existing specialized
HTTP tests; no claim of live CLI execution of every supported reaction, edit,
room, profile or preview branch is made. No backlog operation is added.

Pagination exceptions: accounts/avatars use ascending UUID anchors; messages
use retained conversation-scoped UUID anchors and timestamp/UUID order; shared
content uses timestamp/UUID anchors, while edit-history cursors bind a snapshot.
Conversation activity can move between pages. These are not one uniform cursor
format. A CLI consumer must preserve its query/scope and deduplicate live results;
no snapshot isolation across arbitrary query changes is promised.

Cache/access detail: `application.AvatarImage` reads a valid hash from this
engine's avatar file adapter before checking If-None-Match. `MediaImage` resolves
the media metadata and verifies stored bytes before its conditional response.
Neither is an account authorization system: the current product has one owner
and deferred application authentication. Engine switches scope BFF/device cache
keys; verified hash bytes can remain in private browser caches. Do not claim
revocation of a prior cached copy. Representations here do not negotiate by
Accept/Accept-Language, so no header-based variant requiring Vary was observed.
BFF language is selected in its presentation URL, not parsed from engine text.

Operational limits: `apphost/server.go` sets a five-second read-header deadline
and sixty-second idle timeout, while original byte streams renew a thirty-second
write deadline and SSE uses five-second writes/fifteen-second heartbeats. The
CLI bounds the complete request; it does not impose a server-side full-body
read deadline. A complete mutation-body read deadline and correlation header
remain **missing (P3)**; a future bound must preserve long-lived event/media
responses. No global rate-limit or Retry-After contract was observed. Protocol
reconnect backoff and unknown-send recovery are separate from HTTP retry policy.
