# Milestone two: basic XMPP engine

This is the engine-only direct-text increment following [the foundation](milestone-one.md).
It follows the agreed alpha specification without claiming that every milestone-two
UI requirement or later history/media/sync requirement is complete. UI work lives
in its own worktree against a mock API until integration.

## API and behavior

The source contract is `api/openapi.yaml`, version 0.2.0. Strict Go client/server
bindings and the standalone oapi-cli-gen development CLI are regenerated from it.
The engine owns structured data; no BFF, Goshtoso, protocol XML, or SQL types enter
the application core.

| Operation | Behavior |
| --- | --- |
| PUT /api/v1/accounts/{accountId}/connection | Save write-only password and optional TCP endpoint, then asynchronously connect. Repeating unchanged configuration keeps the existing session. |
| GET /api/v1/accounts/{accountId}/connection | Read not_connected, connecting, connected, or error and a stable error code. No credential readback. |
| DELETE /api/v1/accounts/{accountId}/connection | Disconnect and disable automatic connection. Retain metadata, history, credentials, and pending sends. |
| POST /api/v1/conversations | Open/find a canonical direct peer within an account. Does not add a roster contact or presence subscription. |
| GET /api/v1/conversations | Read activity-ordered pages, optionally filtering account, unread, favorite, archived, kind, or address/title/latest-preview substring. |
| GET /api/v1/conversations/{conversationId} | Read the current projection. |
| POST /api/v1/conversations/{conversationId}/messages | Atomically commit original text, initial revision, operation receipt, event and outbox intent; return 202. |
| GET /api/v1/conversations/{conversationId}/messages | Read newest-first retained pages; cursor continues into older ingestion IDs. BFF reverses a page for chronological display. |
| GET /api/v1/messages/{messageId} | Read current revision and actual transmission state. |
| POST /api/v1/messages/{messageId}/cancel | Cancel a queued own send using expectedRevisionId. Race atomically with dispatch; conflict includes currentMessage. |

Conversation open, send, and cancellation require UUIDv7 Idempotency-Key values.
The same key/input replays the original result; another payload or operation kind
conflicts. Send replay returns the original acceptance snapshot, so read the
message for its current state. Operation receipts prove local acceptance, not
provider or recipient delivery. Connection PUT/DELETE are desired-state operations,
reconciled with GET connection rather than a credential-bearing receipt.

Durable SSE now includes account.connection_changed, conversation.created/updated,
and message.created/updated, alongside account.registered. Each has its corresponding
structured payload. Existing engine-scoped resume semantics remain unchanged.

## Dispatch and retention

A single serial dispatcher wakes immediately after acceptance or reconnection.
A 500ms fallback poll recovers wakeups; it is not an artificial send/undo delay.
It checks connection availability, atomically claims queued work as dispatching,
commits that claim, then performs a bounded network write outside any SQL transaction.

- `queued`: committed and definitely not dispatched, including waiting for connectivity/retry.
- `dispatching`: an attempt has started; cancellation is rejected.
- `submitted`: written to the authenticated stream. There is no implied provider
  acknowledgement, recipient delivery, or read evidence.
- `unknown`: a transmission may have occurred. Network write errors and interrupted
  dispatch claims recovered on startup become unknown and are not blindly retried.
- `failed`: definitive rejection evidence or an invalid destination detected by
  the adapter. An authenticated server/target error must match account and message scope.
- `cancelled`: terminal before dispatch; ordinary restart does not revive it.
- `received`: a retained incoming message.

Known-unsent queued intents survive ordinary restart and resume once connected.
A definitely-unsent adapter result returns to queued with a one-second retry bound.
Messages already submitted are not resent merely because delivery/read receipts
are absent. There is no force-retry-unknown endpoint in this increment.

Original bodies and immutable revisions stay in SQLite, independently of server
retention. Send-state transitions also create revisions; they are transport-state
revisions, not user text edits. Incoming live replays deduplicate nonempty stanza
IDs within account, conversation, and direction. Missing IDs do not trigger unsafe
body-based deduplication. Provider/archive identity across independent engines is
still O04 work.

## XMPP adapter and credentials

[Mellium v0.23.0](https://pkg.go.dev/mellium.im/xmpp@v0.23.0) is compiled in behind
the application protocol port. It normalizes bare JIDs, negotiates STARTTLS,
verifies the account domain, authenticates with supported SCRAM or TLS-protected
PLAIN, binds an account-specific resource, announces presence, receives direct text,
and responds to XMPP ping. It never disables certificate verification.

An optional endpoint overrides TCP dialing; omitted endpoints use XMPP discovery.
System trust is used normally. `--xmpp-ca-file` adds an explicitly trusted private
or development certificate. Transient stream failures reconnect with backoff from
one to thirty seconds; authentication/TLS failures stop automatic attempts until
configuration is corrected or the engine restarts with corrected trust/configuration.
New registrations canonicalize bare JIDs before account matching. Earlier foundation metadata is retained without rewriting account identities; aliases/matching for imported or independently ingested identities remain O04 work.
Outgoing text is capped at 16,384 Unicode characters by the API. Incoming stanzas have a separate 1 MiB parsing bound; a larger valid incoming body is not silently subjected to the smaller outgoing composer limit.
Basic direct-text capability is reported only when connected. This is not a claim
that all optional server features have been discovered or implemented.

Desired enabled connections live in `DATA_DIR/private/connections/ACCOUNT_UUID.json`.
Files are atomically replaced, synced, and created with mode 0600 inside a private
directory. Passwords are currently plaintext in these private engine-local files;
OS credential-store/encryption integration remains open. They are absent from
account records, operation results, structured errors, and SSE. Backup and sync
are not implemented, so credential export/transfer is not implied.

An OS-backed exclusive lock on `DATA_DIR/engine.lock` admits one engine process
per data directory. It is released on close/process exit; restarting does not
require deleting a stale lock file. BFF-only processes do not open or lock the store.

## SQLite and build tooling

- Application SQL is in `internal/adapters/sqlite/queries`; sqlc v1.31.1 generates
  the adapter's `dbgen` package. Transaction orchestration stays in the adapter.
  Only catalog/bootstrap PRAGMAs remain handwritten SQL housekeeping.
- golang-migrate v4.20.1 runs embedded numbered up/down migrations using its modernc
  SQLite driver. The application applies only Up. Explicit down migrations remove
  messaging data and are never an automatic recovery action.
- Existing shipped foundation data (`user_version=1`, no migration journal, known
  foundation tables) is adopted at migration 1, then upgraded transactionally.
  Engine/account/revision identities, receipts and stream records are preserved.
  Dirty/future migration journals are rejected rather than silently forced.
- Repo-owned generators live in `tools/go.mod`: templ, oapi-codegen, oapi-cli-gen,
  and sqlc. Invoke them from the root via `go tool -modfile=tools/go.mod`.
  Vacuum remains under `tools/vacuum` to isolate its incompatible templ graph.
  Root go.mod has app dependencies; transitive module metadata remains Go-managed.

`make generate` regenerates SQL, API, templ and CLI output. `make check` also
recreates SQL generation into a temporary directory and compares it for drift.

## Verification and limits

The invariant tests use real SQLite, HTTP handlers, and generated CLI commands:

| Scenario | Evidence |
| --- | --- |
| Same send operation/input repeated | Original acceptance, one retained message and intent |
| Different input with same key, invalid text/extra fields | Stable rejection without another effect |
| SQL failure during revision insert or conversation update | Message/revision/outbox/receipt/event/projection all roll back |
| Cancel races dispatch | Exactly one atomic winner; in-flight cancellation reports current message |
| Restart with queued/cancelled/interrupted work | Queued resumes, cancelled stays terminal, dispatching becomes unknown |
| Unknown network outcome | No automatic second write |
| Replayed incoming stanza | One retained message within its scope; independent accounts remain isolated |
| Legacy schema upgrade / dirty or newer journal | Retained data; explicit refusal of unsafe automatic repair |
| Two engine opens on one directory | Second owner rejected; repeated old cleanup cannot unlock a new owner |

`make test-engine-xmpp` compiles an isolated test binary and runs it beside real
Prosody. It verifies TLS, both fixture logins, direct-text exchange with Unicode,
newlines and XML characters, send replay, offline queue/cancel, automatic reconnect
after a forced TCP interruption, enabled-connection reload after engine restart,
server rejection, wrong password, untrusted TLS, and absence of credentials in events.
A separate transport test rejects a server offering SASL without TLS before auth.

No browser UI changes are part of this engine increment. The independently running
UI preview remains backed by its mock until coordinated integration. Rooms/MUC,
MAM serial backfill, carbons, stream acknowledgements/resumption, recipient/read
receipts, typing, Markdown translation, pending-content edits/schedules, media,
search over all message bodies, sync, PWA/offline behavior, and restore remain later
work. No optional capability or delivery evidence is fabricated for these features.
