# Handoff: independent engine API and CLI proof

Date: 2026-11-10. Status: authorized next milestone, after the Wails alpha work.
This document commissions exploration, specification additions and implementation.
It is not a completed architecture or API compliance review.

## User direction and scope

The user wants any interface to operate Oiê through a distinct data/operations
API: web, Wails, TUI, CLI and third-party integrations. A CLI that actually
operates the existing application is sufficient proof for this milestone.
Building a TUI is not required.

API-first development already exists. The task is to verify that HTTP/REST
semantics, idempotency, versioning, caching and related contract practices are
implemented consistently, then correct evidenced gaps. A valid OpenAPI file or
a generated client alone does not establish correct runtime behavior.

The earlier discussion proposed an in-process client abstraction, public Go SDK
and additional modules. Those were recommendations, not accepted requirements.
Do not make them prerequisites for the CLI proof. Likewise, do not implement
WASM, another transport framework, a daemon or a new frontend for this milestone.
WASM remains a future execution target, not a currently implemented engine mode.

Keep the release work separate. This handoff does not request stopping its CI,
changing its running preview, merging its branch or publishing a release.

## Starting procedure

1. Read current `AGENTS.md`, `docs/oie-spec.md`, this handoff and the current
   release/integration notes. Preserve AGREED, RECOMMENDED, OPEN and BACKLOG labels.
2. Establish the integrated baseline after the active Wails work finishes. Use a
   managed worktree and preserve other sessions' edits and staging. Do not assume
   this handoff's inspected commit is the appropriate feature base.
3. Inventory the implemented engine API, its consumers and test coverage before
   proposing structural changes. Separate observed behavior from intended behavior.
4. Write the audit and an additive specification proposal, then implement bounded
   corrections and the CLI proof. Keep consequential unresolved product choices
   visible rather than silently treating recommendations as agreements.

Inspection for this handoff reached `b6d52751e00dd97332d74ed2b2d43963343b0f09`
in the active Wails worktree. That checkout also contained unrelated uncommitted
CI/desktop work. This is a historical locator, not a release or passing-CI claim.

## Existing code to examine

| Area | Starting locations |
| --- | --- |
| Requirements and decisions | `docs/oie-spec.md`, especially sections 1–3; `docs/unified-chat-discussion.md` |
| Core operations and ports | `internal/application`, `internal/domain` |
| Runtime composition | `internal/apphost/server.go`, `cmd/oie/main.go`, `cmd/oie-desktop/main.go` |
| Engine HTTP contract and implementation | `api/openapi.yaml`, `api/codegen.yaml`, `internal/adapters/httpapi` |
| Generated engine bindings | `internal/adapters/httpapi/engineapi`, regenerated through tooling |
| CLI and generation exclusions | `cli/go.mod`, `cli/cmd/oiectl`, `api/cli.overlay.yaml`, `api/openapi.cli.json` |
| Existing CLI proof | `tests/cli/client_test.go` |
| BFF contracts and clients | `api/bff.openapi.yaml`, `api/ui-preview.openapi.yaml`, `internal/adapters/web` |
| Cache policies | `docs/cache.md`, `api/cache.openapi.yaml`, `internal/adapters/cachehttp/policy.go` |
| Protocol integration | `tests/xmpp`, development Compose setup, `docs/milestone-two-engine.md` |
| Generation and CI | `Makefile`, `scripts/check-generated.sh`, `tools/ci`, `docs/local-ci.md` |

The application/domain packages already avoid UI and HTTP adapter imports. The
HTTP adapter translates structured contracts into application operations. The
BFF currently consumes generated HTTP clients. The CLI is already a separate Go
module and has real-store integration tests, including registration replay,
operation lookup, queued send acceptance and cancellation. Extend this foundation
rather than replacing it merely to demonstrate a new client.

The composition currently mounts both API and BFF for engine roles. Review how
to offer a data API without serving the BFF. Prefer a small composition change
when necessary. Separate Go modules or an in-process API are not automatically
needed; verify actual package/build dependencies before claiming independence.

## Concrete observations to verify first

These observations locate potential gaps. They are not a verdict on every route.

- `api/openapi.yaml` declares `info.version: 0.27.0`. Its `Engine.apiVersion`
  schema still enumerates `0.11.0` and `0.12.0`, while the HTTP handler returns
  `0.27.0`. Request validation and generation-drift checks can pass despite this
  response/schema mismatch. Recheck the baseline and add runtime response coverage.
- `api/cli.overlay.yaml` excludes SSE events, avatar/image bytes and shared-content
  media downloads. Do not describe the current generated CLI as covering those
  operations without implementing and testing an appropriate extension.
- Some BFF-owned read-state and preview/thumbnail routes use `/api/v1` paths.
  `apphost` explicitly mounts them for the native presentation. Compare methods,
  headers, cookie/CSRF behavior and representations across local, hosted and
  BFF/native mounts. A path must not silently acquire different client semantics
  depending on which handler wins routing.
- The shared-content listing contract says it registers preview enrichment work.
  Review intended HTTP safety, replay/cache effects and provider-network effects.
  Do not equate every internal cache/database write during GET with an unsafe
  method; distinguish incidental work from requested application mutations.
- There are existing cache decorators, durable operation receipts, revision
  guards and SSE cursor tests. Audit their coverage and consistency instead of
  assuming these mechanisms are absent.

## Required audit

Produce a route/operation inventory with columns for contract, actual behavior,
engine versus BFF ownership, CLI coverage, test evidence, verdict, priority and
proposed correction. Use implemented, inconsistent, missing, unknown and
intentionally unsupported explicitly. Cite source locations and reproducible
tests. Do not label an intentional product restriction as a missing feature.

Cover the following concerns with a concise shared policy and exceptions table.

### HTTP semantics and resource boundaries

- Methods, safe reads, idempotent methods and explicit mutation operations.
- JSON/data versus HTML/fragments; API behavior must not require browser cookies,
  HTMX headers, localized text parsing or a Wails binding.
- Status codes and representations for creation, synchronous completion,
  asynchronous acceptance, replay, conflict, unavailability and missing resources.
  Define `Location` and polling guidance where appropriate.
- Distinguish request acceptance, persisted intent, protocol submission and
  confirmed delivery. An accepted command is not proof of recipient delivery.
- Request/response media types, bounded bodies, unsupported methods and content
  types, validation errors, UTC timestamps, opaque IDs and documented nullability.
- Keep justified action endpoints if they fit the operation. Avoid URL renaming
  or switching PUT/PATCH/POST for stylistic uniformity without a behavioral reason.

### Idempotency, concurrency and asynchronous recovery

- Inventory every mutating operation, including which intentionally do not use
  durable receipts. Define key scope, payload normalization/fingerprints,
  concurrent same-key behavior, different-payload reuse and receipt retention.
- Verify atomic command/receipt/outbox persistence and behavior across restart.
  Simulate a committed request whose response is lost, then reconcile and retry
  with the same key. One logical acceptance must not enqueue a second send.
- Verify replay before revision checks where the existing contract promises it;
  distinguish the original receipt snapshot from the current resource state.
- Review current body-based expected revisions against HTTP preconditions.
  Decide and document when `If-Match` or other conditional requests add value,
  and distinguish 409, 412 and any required-precondition policy. Do not require
  an ETag to equal an entity revision when it identifies a different representation.
- Cancellation and client timeouts must preserve real outcomes. Do not blindly
  retry uncertain sends or promise exactly-once delivery from the chat protocol.

### Versioning and compatibility

- Define the relationship between `/api/v1`, OpenAPI `info.version`, the runtime
  `apiVersion` and the application's `v0.0.1` release. They are different concepts.
- Establish additive versus breaking changes, enum/unknown-field behavior,
  stable operation IDs, error codes, generated CLI compatibility and deprecation
  policy. Do not bump the URL major version for every additive operation.
- Add a repeatable compatibility check against an explicit baseline. Check real
  responses as well as schema syntax and generated-file drift.

### Caching and conditional requests

- Classify mutable private state, immutable content-addressed bytes, derived
  previews, credentials, commands, errors and streams separately.
- Review `Cache-Control`, ETag strength, conditional GET/HEAD, bodyless 304s,
  representation variants and `Vary` where responses actually vary by headers.
- Distinguish `no-cache` from `no-store`; do not cache everything to demonstrate
  cache support. A correct no-store policy is a valid outcome for sensitive or
  rapidly changing resources.
- Include engine/account scope, permissions, revisions, invalidation and engine
  switching. Conditional requests must not bypass current resource access checks.
- Test media ranges, validators, truncation/cancellation and cache headers without
  conflating provider ETags with verified content hashes or retained original files.

### Pagination, events, errors and operational behavior

- Stable bounded ordering, cursor scope/validation, filter-before-limit behavior
  and documented consistency when new messages or edits arrive between pages.
- Structured SSE payloads, engine-scoped IDs, snapshot/subscription handoff,
  reconnect and deduplication, invalid cursors, bounded consumption, slow clients
  and cancellation. Define recovery for any future cursor-retention gap.
- Machine-readable Problem Details and stable codes. Keep status/body consistent;
  avoid leaking credentials or SQL/protocol internals. Localization belongs in
  presentation, not in error-code interpretation.
- Timeouts, retry eligibility/backoff, request correlation, redacted diagnostics
  and resource limits. Document rate-limit semantics if limits exist; do not
  invent arbitrary quotas as a substitute for measuring the problem.
- Distinguish browser CSRF from authentication/authorization for non-browser
  clients. Application authentication remains deferred unless separately agreed.
  Do not expose an unauthenticated engine beyond the existing authorized scope
  just to make the CLI demonstration convenient.

## CLI proof and acceptance scenarios

Use the generated `oiectl` as the starting client. The CLI must use the public
data API, not import the store, access SQLite directly, call the BFF or automate
browser/Wails controls. It must work against a configurable engine address.

Demonstrate the existing implemented scope against a disposable real engine and
the development XMPP server, with no browser or BFF needed for operation:

1. Start a data-only engine, read identity/readiness and inspect capabilities.
2. Register/configure development accounts, connect, observe connection state,
   list contacts and open a direct conversation. Supply credentials through stdin
   or another supported non-logging input, not command-line password arguments.
3. Send and receive actual direct text, list messages and search retained history.
   Prove delivery/receipt only to the extent the integration observes it.
4. Repeat an accepted request with the same key, reject changed-payload reuse,
   exercise concurrent replay and recover a lost response without a duplicate
   logical send. Test a stale revision and queued cancellation separately.
5. Restart the engine with the same disposable data directory and verify retained
   identity, history and operation recovery. Preserve documented outbox semantics.
6. Follow structured events, disconnect and resume from the applied cursor without
   losing changes. The fixture must observe events; merely having an SSE URL or
   ignoring the stream is insufficient.
7. Read shared-content metadata and download representative supported bytes
   through the engine API. Verify output bytes/errors and conditional behavior
   using the same fixture, without `/ui` download routes.
8. Show useful nonzero exit statuses, machine-readable output and separation of
   stdout results/bytes from stderr diagnostics. Support bounded waits and clean
   cancellation for automation. Keep retries explicit and safe.

Inventory coverage for all other implemented operations and list remaining CLI
gaps. Do not implement backlog features simply to fill the CLI command tree.
Auxiliary HTTP tools can inspect headers, but they cannot replace the operational
CLI proof. Binary/SSE extensions must use supported generation extension points
or code outside generated files. Never hand-edit generated CLI/bindings. Generic
generator gaps belong in an upstream Arai Hû PR with the agreed pseudo-version
workflow; a new generator or transport stack is not the default solution.

## Deliverables and working rules

- An evidence-based audit report and a short policy/exception matrix, not only a
  checklist of desired practices. Record unresolved findings and practical limits.
- Additions to the current spec, in the spec itself or a linked addendum, clearly
  separating existing agreements, proposed recommendations, open decisions and
  deferred work. Do not rewrite the whole spec or silently expand the alpha scope.
- Small implementation commits addressing prioritized contract/behavior gaps and
  the CLI proof, with reproducible commands and fixture outputs.
- Automated positive and negative contract tests, actual response validation,
  compatibility checks, CLI end-to-end coverage and the relevant CI wiring.
- A final evidence record tied to the tested commit/source, including unavailable
  checks. Passing unit tests alone is not an XMPP delivery or release-readiness claim.

Keep API-first order: contract/policy, generated bindings, implementation and
tests. Build tools stay under `tools`; use sqlc and embedded migrations for any
persistence changes. Preserve domain/application independence from transports,
SQL, protocol packages and presentation. Retain existing web/Wails behavior;
run their relevant regressions when composition or shared contracts change.

Run `make check`, relevant real XMPP/CLI checks and local Dagger CI. Follow
`docs/local-ci.md` for the current runner, with a separate artifact directory per
session. Do not start competing full pipelines or alter the release session's
timeouts/processes. Maintain the live preview through the owning workflow if
later implementation needs to update it; never use a real personal account as
an integration-test fixture. Do not add developer documentation to product UI.

No file in the active release work was modified to prepare this handoff. The
handoff itself does not mark audit findings fixed or authorize publishing alpha.

## Primary references for the review

Consult relevant sections and current errata. State which rules apply to each
operation; do not claim compliance with an entire standard from a single test.

- [RFC 9110: HTTP semantics](https://www.rfc-editor.org/rfc/rfc9110.html), including
  safe/idempotent methods, status codes, conditional requests and ranges.
- [RFC 9111: HTTP caching](https://www.rfc-editor.org/rfc/rfc9111.html), including
  storage directives, validators and revalidation.
- [RFC 9457: Problem Details](https://www.rfc-editor.org/rfc/rfc9457.html).
- [WHATWG server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html).

Idempotency-key behavior and API versioning still need an explicit Oiê contract.
If referring to an Idempotency-Key specification, verify its current standards
status rather than describing a draft or common convention as a universal rule.
