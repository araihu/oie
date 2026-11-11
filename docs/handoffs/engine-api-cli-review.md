# Handoff: independent engine API and CLI proof

Issued 2026-11-10. Updated 2026-11-11.

The independent engine and CLI milestone is implemented in
`codex/engine-api-cli`. The independently reviewed Oiê revision is `4ef445c`.
The current tools pin is `v0.0.0-20261111033254-90684aaa8e38`, from the
generator's `main`. The integrated engine build uses Go 1.27.4. The original
brief follows the integration record.

## Final independent reviews

Three reviewers checked the live engine contract, the generic generator, and
the rebuilt CLI independently. The seven findings and the follow-up cases
closed at Oiê `4ef445c` and generator `3de8ea1` within the tested scope.

| Finding | Verified result |
| --- | --- |
| Strict 0.27.0 Problem Details compatibility | Default engine responses 400, 404 and 405 validate against the published closed schema. The `Link` header carries the catalog URL. The named `Prefer` opt-in adds `documentation` to the body, including with an empty quoted value. |
| Request data reflected in CLI stderr | The Oiê CLI uses its bundled catalog. The generic runtime suppresses response-supplied metadata for requests with input, a server path prefix, request Content-Type, or authentication. Tests covered an empty body with parametrized Content-Type and an authenticated HTTP proxy in text and JSON modes. |
| Undeclared SSE handled as raw | `system health` rejects an unexpected SSE response. Declared `events stream` still emits frames incrementally and explicit `--output raw` is available. |
| Problem Details identity lost | Public requests without private input preserve standard `type` and `title` without requiring a proprietary `code`. Relative type URIs resolve against the response request URL. Private requests use trusted catalog metadata when available. |
| Deprecation notice on JSON stderr | Deprecated commands emit a single JSON diagnostic on failure. Text invocations retain the deprecation notice. |
| Requested response header absent | A missing header on HTTP 200 or 304 emits a local transport problem and exits 3. An explicitly empty response header succeeds. |
| Error body read failure classified as HTTP failure | Timeout, cancellation, and truncation detected while reading a declared Problem Details body exit 3 with empty stdout and a local JSON diagnostic. A complete HTTP error exits 4. Bodies that exceed the diagnostic byte limit use the HTTP status fallback. |

At `4ef445c`, native `make check`, the focused CLI/HTTP tests, the
0.27.0 compatibility gate, and Dagger `make ci-check` passed. The final
Dagger logs are in
`tests/browser/artifacts/engine-cli-edge-fixes-final-ci-retry/`. The isolated
Prosody CLI proof passed for the changes recorded in `716ba7f`. The earlier
complete Dagger graph, including desktop and browser coverage, was verified
at `1927d5b`. See the [verification record](../engine-api-cli-verification.md)
for the fixture, evidence, and limits of those runs.

## Engine toolchain

The root Go module and desktop packaging script require Go 1.27.4 or newer.
GitHub Actions reads the version from the root module. Dagger pins the official
`golang:1.27.4-bookworm` image by its multi-platform digest for the engine
checks, Linux desktop CI, and Linux release packaging. Go 1.27.4 includes the
standard-library fix for `http.MaxBytesReader` used by the engine HTTP adapter.
[Go advisory GO-2026-6859](https://vuln.go.dev/ID/GO-2026-6859.json)

Native `make check` and `make build` passed on Linux arm64. `go version -m bin/oie`
reports `go1.27.4`. The native `make test-cli-xmpp` passed with a disposable
Prosody fixture. The packaging script rejected Go 1.27.3 and advanced past the
version check with Go 1.27.4 and 1.27.5 in a simulated environment.

The complete Dagger `make ci` passed, including Go race tests, the Linux amd64
desktop build and WebView smoke, isolated Prosody and CLI integration, and the
browser suites. The first run received SIGTERM during checks. The next run hit
the FFmpeg thumbnail deadline under `test-race`. The focused native race test
and the final Dagger run passed with the existing five-second deadline. The
final logs are in `tests/browser/artifacts/oie-go-1-27-4-ci-final/`. Native macOS
packaging and release archive smoke checks remain for the release workflow.

## Integration into main

### Generator PRs

| PR | Result on 2026-11-11 |
| --- | --- |
| [oapi-cli-gen #2](https://github.com/araihu/oapi-cli-gen/pull/2) | Nested command groups squash-merged into `main` at `1a98fcb`. GitHub check and CodeQL passed. |
| [oapi-cli-gen #3](https://github.com/araihu/oapi-cli-gen/pull/3) | Ten commits rebased onto the #2 squash. The PR then passed GitHub check and CodeQL and squash-merged into `main` at `90684aa`. |

The rebase preserved the reviewed generator tree. The subsequent PR check
flagged a Go standard-library issue with the runner's Go 1.27.3. The generator
now selects Go 1.27.4 for development and CI. Its module and generated CLI
still target Go 1.27.3 as the minimum version. The merged implementation
matches the independently reviewed generator code.

The Oiê tools module now pins the merged commit `90684aa`. Regeneration
preserved the committed CLI files byte for byte, and the CLI was rebuilt
with Go 1.27.4.

### Oiê integration

The initial candidate `3a3b1ff` combined the engine and CLI with Oiê `main`
at `ac34a2c` and the published Wails and rc.0 baseline `47870bd`. Its native
checks, isolated Prosody CLI proof, sidebar and About drawer tests, and Dagger
Go checks passed. `main` advanced during its desktop run. Merge `5f931eb`
integrated Wails, initial room history, and the engine reset workflow. The
first candidate's Dagger run was stopped after that change.

The integration merge `93b7986` uses `388de70` as its first parent and the
reviewed engine and CLI line through `9988790` as its second. The combined
engine and BFF contracts retain the room archive and reset routes. Generated
clients and `oiectl` come from those combined contracts. The command reference
includes `conversations rooms archive status`, `set`, and `request`, and the
existing engine reset operation is available as `system reset` with explicit
confirmation. Problem Details diagnostics link to the
[catalog on `main`](https://github.com/araihu/oie/blob/main/docs/engine-api-problems.md).

The engine still reports API `0.29.0`. It derives the runtime value from the
OpenAPI document and checks that it belongs to the published response enum.
The schema retains the enum from `main` and the version pattern from the
normalized 0.27.0 compatibility baseline. The [specification](../oie-spec.md)
uses the IDs already assigned on `main`, including B25 for initial room history.
Alpha publication follows the [Oiê contributor policy](../../AGENTS.md).

Native `make check`, `make build`, and `make build-cli` passed on `f76a2a4`,
with Go 1.27.4 in both binaries. The contract comparison passed against both
`388de70` and the normalized 0.27.0 baseline. Native browser tests passed for
confirmed reset, conversation navigation, and retained and room archive
search. Logs are in `tests/browser/artifacts/oie-main-final-native/`.

The final candidate incorporates the CI dependency updates through `adf823c`.
Dagger verified the Go checks and race tests, Linux amd64 desktop build and
WebView, and the isolated Prosody tests for the CLI and room archive recovery.
The browser suites passed across targeted reruns. The avatar test now counts
painted indicators, the video test exercises both tablet sidebar layouts, and
the mention test retries the pending operation by its key. Application source
is identical across these runs. The evidence is in these directories under
`tests/browser/artifacts/`:

- `oie-main-final-ci-retry/` records the desktop and WebView pass on `3ea8b65`.
- `oie-main-browser-ci/` records the browser scenarios through the emoji picker
  on `df66d52`, including avatar and video regressions.
- `oie-main-browser-tail-ci/` records checks, XMPP and the browser scenarios
  from mentions through the live engine proof on `71dadb2`.

After the push, GitHub's race job reported HTTP 503 while serving the maximum
edit-history page from the test fixture. The fixture now encodes the same page
before serving requests. The focused test passed three native race runs, and
Dagger `make ci-check` passed in `tests/browser/artifacts/oie-main-history-check/`.
The first two GitHub browser attempts failed during Playwright dependency
installation because the Ubuntu mirror hostname could not be resolved.

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

## Original starting procedure

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

## Observations at the start of the milestone

These observations describe the baseline before implementation. See the
[audit](../engine-api-audit.md) for the operation inventory and verdicts.

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

Release publication is handled by the release workflow.

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
