# Engine API policy and milestone decisions

This addendum supplements specification sections 2.1–2.2. Labels retain their
meaning from the specification. The [audit](engine-api-audit.md) separates observed
behavior from proposed improvements. The handoff authorizes the bounded API/CLI
milestone; it does not authorize a release or deferred application features.

**AGREED:** interfaces operate the engine through structured data operations,
queries and events. Prove the current scope through generated `oiectl`, using a
configurable address, disposable data and development XMPP credentials supplied
through stdin. No BFF/browser/native bindings are needed for that operation.

**IMPLEMENTATION:** `-data-only` omits presentation routes for local/hosted roles.
It preserves their application role, storage and workers. Default composition
continues serving the web UI. This is runtime independence; the server binary
still compiles presentation packages. The CLI is a separate Go module without
store/application imports. No SDK, extra module, daemon or transport is required.

| Concern | Current contract / bounded correction | Exceptions / practical limits |
| --- | --- | --- |
| Resource ownership | `/api/v1` is engine JSON/bytes/SSE; browser adapters live under `/ui/api/v1` | BFF headers/cookies/CSRF are presentation guards, not engine authentication |
| Safe reads | GET queries observations or retained content; incidental cache/index enrichment is permitted | Shared catalog/preview reads can schedule bounded enrichment; no send, star, read-mark or room-intent mutation. Original bytes require explicit content GET |
| Acceptance | 201 creates; 200 reads/completes/replays; 202 persists or accepts asynchronous intent | Acceptance is distinct from connection, protocol submission and recipient delivery. Poll the returned resource; use bounded waits, initially 250 ms, with backoff |
| Locations | Resource creation/async responses identify a pollable resource with Location | Operation lookup identifies acceptance; GET resource returns current state, which can differ from receipt snapshot |
| Input / errors | JSON commands, 1 MiB HTTP body cap, schema and application validation; opaque UUIDv7 IDs and UTC RFC3339 instants | Unsupported methods return 405 with Allow; unsupported request media types return 415 and bodies beyond the cap return 413. Engine errors use RFC 9457 Problem Details with stable type URIs and short codes |
| Concurrency | Existing required body revisions and 409 state_conflict | No If-Match/412/428 policy yet. Content ETags identify representations, not automatically domain revisions |
| Idempotency | Engine-wide UUIDv7 keys, normalized operation fingerprints, transactional receipt/state/outbox/events; no expiry | Exact authored text is retained. Connection and derived preview work deliberately do not have durable receipts; see audit matrix |
| Retry | No automatic CLI retry. Reconcile uncertain sends using the original key/body | A timeout/cancellation is not rollback. Never mint a new key to hide an uncertain send; dispatch-unknown is not blindly retried |
| JSON cache | Mutable private state, commands and errors use no-store | No-cache permits storage with revalidation; it is not a substitute for no-store |
| Retained bytes | Verified hash image/avatar URLs use private immutable cache and bodyless conditional 304 | Resource lookup/validation precedes validator handling. Hash availability does not grant cross-engine access |
| Original media | Private no-store, bounded single ranges and If-Range | Provider ETag is not a verified hash. HEAD and conditional If-None-Match are intentionally unsupported for original content; BFF revalidation is separate |
| Streams | Current SSE uses no-cache; clients should consume without persistent caching. Structured UTF-8 SSE, engine:sequence IDs; bounded DB batches, heartbeat, write deadline and cancellation | Snapshot cursor before reading snapshots, then subscribe/replay and deduplicate applied IDs. No retention expiry now; future gaps must return explicit reset/conflict, never skip silently |
| Pagination | Bounded stable source ordering; cursor belongs to documented engine/filter/snapshot scope | Conversation activity can move between pages; resnapshot/deduplicate on live changes. Edit-history pages reject changed snapshots |
| Operations | CLI results/bytes go to stdout, sanitized diagnostics to stderr; exits 2 input, 3 transport/deadline/cancellation, 4 HTTP | SSE is consumed incrementally by default. Timeout/cancellation returns 3 even after partial output. Persist only fully applied SSE IDs. No implicit reconnect/retry |
| Exposure | Default loopback listener; preserve existing explicitly configured hosted scope | Application authentication remains deferred. No external listener or arbitrary quota is introduced by this proof |

**RECOMMENDED:** `/api/v1` identifies the wire-contract major; OpenAPI
`info.version` and runtime `apiVersion` identify a contract revision. The app's
`v0.0.1-rc.0`/`v0.0.1` release identifies a distribution and is independent.
An additive operation does not require `/api/v2`. `Engine.apiVersion` accepts
semantic version strings rather than enumerating historical minor revisions.
Keep operation IDs and machine error codes stable. Generated clients should
tolerate unknown response properties; current request schemas reject unknown
fields. Unknown enum values need explicit client handling; adding enum values
is compatibility-sensitive and must pass the baseline gate or be reviewed.

**AGREED:** product releases use `vX.Y.Z`, engine contracts use
`openapi/engine/vX.Y.Z`, and the standalone CLI uses `cli/vX.Y.Z`.
See [engine and CLI releases](engine-cli-releases.md) for publishing and retries.

**RECOMMENDED:** run the breaking-change checker against the checked-in
baseline with explicit provenance, alongside runtime response validation and
generation drift. Deprecate before removal and describe migration in docs;
do not silently replace the baseline to pass CI. No removal is proposed here.

**OPEN:** a release-wide deprecation duration, future enum policy for clients,
HTTP preconditions for JSON mutations, request correlation headers, measured
rate limits and event/receipt retention. These are not prerequisites for CLI
operation. Current no-expiry receipts must not acquire undocumented expiry.

**BACKLOG / OUTSIDE THIS MILESTONE:** application authentication, TUI, WASM,
public SDK, new transport, schedules, sync and outgoing attachments. Existing
restrictions on corrections, rooms and reaction identity remain in force.

## Engine problem responses

The engine returns `application/problem+json` as defined by
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html#section-3). The `type`
URI is the primary identifier, in the form `urn:oie:problem:<code>`. The
`title` summarizes the type and stays consistent across occurrences. The
`status` matches the HTTP response. The `code` extension gives clients a
short label for diagnostics and localized messages. Known problems provide an
HTTPS link with `Link: <URL>; rel="help"`. Send
`Prefer: oie-problem-documentation` to include the URL as `documentation` in
the JSON body. The preference also accepts an empty quoted value
(`oie-problem-documentation=""`). A response that honors it sends
`Preference-Applied: oie-problem-documentation`. The default body remains
compatible with the published closed 0.27.0 schema. The link can change
independently of the URN and code. See the
[engine API error catalog](engine-api-problems.md) for recovery steps.

For example, a 409 response with type
`urn:oie:problem:idempotency_conflict` means the idempotency key was already
used with different input. A 409 with type `urn:oie:problem:state_conflict`
can include the current resource in a documented extension so the caller can
refresh its state. The CLI uses the declared short code and its bundled
catalog for the title, type and documentation link on stderr.

## Standards used in the review

The relevant rules are method safety/idempotency, 201/202/409/405, conditional
requests and ranges in [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html);
storage/revalidation in [RFC 9111](https://www.rfc-editor.org/rfc/rfc9111.html);
stable problem types/status/extensions in
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html); and event framing,
UTF-8 and Last-Event-ID in the
[WHATWG SSE specification](https://html.spec.whatwg.org/multipage/server-sent-events.html).
Consulted on 2026-11-10. The RFC errata index could not be fetched by the review
browser; no claim of complete errata review is made. Idempotency-Key here is
an explicit Oiê application contract, not a claim that an IETF draft is a standard.
