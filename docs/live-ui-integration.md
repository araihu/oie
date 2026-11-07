# Live browser integration: direct XMPP text

This increment connects the chat workspace to the canonical generated
`engineapi` client. Production web code does not import the mock contract,
application/domain packages, SQL, or an XMPP client. The old mock remains an
independent development fixture; its marker disables mutations in the BFF.

The supported user flow is adding an existing XMPP account, configuring its
connection, selecting an explicit sending account and recipient, sending and
receiving direct text, and cancelling a queued own message before dispatch.
Connection status and errors come from GET connection. The UI never assumes a
successful configuration request means authentication succeeded.

## Mutation invariant ledger

The engine owns acceptance, idempotency, and transition guards. These rows add
presentation and transport requirements; API invariants also remain covered by
[the engine tests](milestone-two-engine.md).

| Route/state | Action/request | Allowed? | Response / truth | Input and context preserved | Focus/destination | Effects / evidence |
| --- | --- | --- | --- | --- | --- | --- |
| Existing XMPP account, ready | Register metadata | Yes | Stable UUIDv7 operation key; actual engine account result | Address/alias retained on errors; password is a separate form | Accounts drawer after native PRG or JS success navigation | One account; independent real browser observer plus API replay tests |
| Any mutation, missing/forged CSRF | POST | No | 403 before any backend access; GET actions return 405 | No mutation; no credential echo | Visible recovery or JSON form error | Zero backend calls; `TestEveryMutationRequiresCSRFBeforeBackendAccess` |
| Account connection, configured credentials | Connect | Yes | Desired-state PUT accepted; GET/SSE determines actual connecting/connected/error | Endpoint remains; password clears immediately and is not journaled | Same account card and current status | Real verified-TLS Alice/Bob observer; password HTTP/browser checks |
| Account authentication error | Connect with wrong password | Configuration allowed; authentication fails | Actual authentication_failed is localized and visible; no connected success is invented | Account/endpoint retained; password empty | Account status/error in its drawer | Independent HTTP fixture browser check; engine verified real wrong-password behavior |
| Connected account | Disconnect | Yes | DELETE desired state; history and queued sends retained | Account, conversation, draft remain | Same account card; actual not_connected | Real engine browser observer |
| Account/peer selection | Open conversation | Yes | Engine returns the account-scoped conversation | Explicit sending account and peer; canonical IDs drive selection | PRG/JS navigation to returned chat/account | Actual real browser observer; engine open/replay tests |
| Valid selected conversation | Send original text | Yes | Commit/receipt is acceptance, not recipient delivery | Original Unicode/newline text and UUIDv7 key | Composer clears only after confirmed acceptance; focus returns there | One accepted intent and one actual recipient message in real observer |
| Submission already in flight | Repeat submit/double activation | Deduplicated | Controls disabled and form guard active; engine idempotency still authoritative | Same body/key | Same composer/announced progress | Browser holds real BFF reply, attempts repeat, checks one fixture API effect |
| Backend commits, success response is lost | Reconcile/retry | Yes, same key and original body only | GET operation + GET message confirms matching conversation/body; repeat does not create another send | Device journal retains key/body and locks content while acceptance is uncertain | Retry control and visible uncertainty until confirmed | Actual dropped backend HTTP socket test; browser-dropped BFF response test; independent real engine observer also drops actual success |
| Same operation key, different body | Send | No | 409; never mint a replacement logical send silently | Existing key/body evidence remains unresolved | Conflict feedback, no false acceptance | `TestLiveCSRFAndLostAcceptanceKeepOneEffect`; one engine effect |
| UI engine identity differs from current API | Mutate | No | 409 engine_changed before action; no implicit engine transfer | Draft stays in original engine/account/chat namespace | Reload/review notice | Adversarial browser guard checks zero effects |
| Own queued message | Cancel with current revision | Yes | Engine atomic cutoff decides; cancelled is terminal | Message identity and expected revision | Actual cancelled status in timeline | Real observer verifies zero recipient effect after cancellation/reconnect |
| Message dispatched / stale queued form | Cancel | No | Conflict includes actual current state; UI does not announce success | Context kept; refresh projects actual submitted state | Announced rejection and updated row | HTTP/browser fixture starts from submitted; zero cancellation effects |
| Native successful send | Refresh / Back after POST | GET only | 303 PRG targets actual conversation | Selected conversation preserved | Stable GET document | `TestLiveNativePRGAndPostOnlyGuard`; effect stays one after refresh |
| Native send, backend unavailable | Submit/retry | Not confirmed | Localized error with visible readonly original text and same operation key | Complete original body, account/chat/engine IDs; no password readback | Recovery form and retry | `TestNativeUnavailableSendRetainsOriginalAndOperationKey`; zero effect |
| Active chat receives / status changes | SSE projection refresh | Yes | Engine cursor retained; server-rendered list/header/timeline/status regions update | Composer node, text, focus and draft stay; scroll anchor stays when not at bottom | New-messages/jump action when appropriate | Real observer checks actual receive, draft preservation, scroll anchoring and no duplicate recipient effects |
| Reload with normal device draft | Reopen same engine/account/chat | Yes | Draft restored locally; a draft alone never sends | Original text and operation journal | Same selected chat | Adversarial browser reload check |
| Device storage denied | Type/send | Degraded, explicitly warned | In-memory fallback; no false claim of durable device storage | Current-window text; before-unload warning for unconfirmed work | Persistent storage warning | Browser makes Storage methods throw; accepted send still works, no script error |
| Password/error display | HTML/JSON/reload/live refresh | Write-only | Password never returned in markup, error text, SSE, or draft journal | Other valid values remain | Password must be re-entered after failure | Native HTML + JSON tests, browser checks markup/localStorage and field clearing |

Primary browser acceptance is driven independently in
`tests/browser/engine-browser.cjs` against a real engine and development Prosody.
`tests/browser/live-chat.cjs` uses a separate stateful HTTP fixture to inject
response loss, stale state, and denied browser storage without changing the
user's real account data. Its evidence path is anchored to
`tests/browser/artifacts`, regardless of shell working directory.

## Live updates and device state

The initial page reads the engine cursor before its snapshots. `/ui/events`
proxies the canonical structured SSE using a separate generated raw client with
no total timeout. It forwards Last-Event-ID on reconnect; upstream headers,
downstream writes and cancellation are bounded. A foreign/reset cursor triggers
a fresh snapshot/reset notice. A changed engine identity blocks further writes
until the user reloads and reviews it.

A small browser coordinator coalesces events and fetches BFF-owned HTML from
`/ui/refresh`. It does not poll/replace whole documents or the composer. A bounded
15-second recovery poll covers unavailable/buffered SSE. List position and
message scroll anchors are preserved; users already at the bottom follow new
messages. A new-message action jumps to latest when the user has scrolled up.
Initial documents that open while the engine is unreachable offer an explicit
Retry; existing active documents retain their composer during an outage.

Draft/submission records live only in browser storage, scoped by engine,
account, and conversation UUIDs. They never live in shared BFF memory or the
engine database. Password inputs are excluded. An uncertain submission keeps its
original key and body and is reconciled before clearing; it is distinct from an
engine-accepted message whose transport evidence is unknown. Unknown transport
outcomes do not gain a force-resend action in this increment.

All API instants remain UTC. Browser `Intl.DateTimeFormat` renders timestamps and
day separators using the device timezone and selected English/Brazilian
Portuguese locale. Theme/language choices stay device preferences.

## Verification and limits

The focused HTTP suite and `go test -race ./internal/adapters/web` passed. The
adversarial browser suite passed with no page errors; the independent real-XMPP
observer passed account forms, two-way text, actual success-response loss,
queue/cancel/reconnect, live draft/scroll preservation, device timezone and
390px Portuguese accessibility. The reported 1076x1109 viewport is covered by
actual square-bounds checks for circular icon controls. Their public root hooks
now set matching width/height and minimum block size; no private component DOM
is styled.

This is direct text integration, not completion of the full alpha. Media stays
disabled; rooms, history backfill/older-page UI, rich Markdown, read/recipient
receipts, typing, message editing/schedules, notifications, PWA/offline caches,
full sync, and cross-tab draft conflict resolution remain separate work. The UI
shows received/queued/dispatching/submitted/unknown/failed/cancelled evidence as
provided. Submitted means written to the XMPP connection; it does not assert
provider acknowledgement, recipient delivery, or reading.
