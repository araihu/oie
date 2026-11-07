# Milestone one implementation

This is the API-first foundation for the alpha described in [the specification](oie-spec.md).
This records the foundation as delivered in PR #1; see [the next engine increment](milestone-two-engine.md) for current XMPP messaging.
It implements selected foundation recommendations without treating the entire
alpha feature list as completed.

## Implemented boundary

- One executable with local, hosted, and remote-BFF modes.
- Reviewed OpenAPI 3.0.3 contract, generated strict Go handlers/client/models, and
  an embedded contract served for development.
- SQLite schema migration, a persistent engine UUIDv7, account/revision UUIDv7
  identities, operation receipts, and durable application change records.
- Atomic account-metadata registration with idempotent replay and conflict checks.
- Account reads, keyset pagination, unknown capability evidence until connection,
  operation reconciliation, readiness, and recoverable SSE.
- Protocol-neutral domain/application packages; SQL/HTTP/Goshtoso stay in adapters.
- A read-only Goshtoso BFF proof that uses the generated HTTP client, including
  a separate process pointing at a hosted engine.
- Embedded en/pt-BR product catalogs, Goshtoso expression context, device language
  and theme cookies, system/light/dark behavior, empty/populated/recovery fragments.
- A standalone generated development CLI, actual CLI/server integration tests,
  and a pinned Prosody Compose environment with a protocol smoke check.

No message send endpoint or production XMPP client is present. The change log is
for UI recovery, **not** a provider-send outbox or device-sync implementation.

## Contracts and invariant ledger

| State/action | Contract | Verification |
| --- | --- | --- |
| New valid registration | 201 with stable account/revision and operation identity; one durable change | HTTP and generated CLI integration tests |
| Same operation, same normalized payload | 200 replay with original result; no new account/revision/change | Repeated request and restart tests |
| Committed success response is lost | Reconcile receipt, replay original result, keep one effect | Actual dropped HTTP socket test |
| Same operation, different payload | 409 idempotency_conflict; no mutation | HTTP and CLI exit-code tests |
| Existing address, different operation | 409 account_exists; no orphan operation receipt | Transaction conflict/reconciliation test |
| Missing/extra fields, unsupported protocol, malformed/non-v7 ID | 400 stable problem; no mutation | Contract/validation tests |
| Storage fails while writing revision | Roll back account, operation, revision, and change together | SQLite injected-failure test |
| Engine restart | Same engine ID, account/revision IDs, receipt and event | Reopen persisted directory test |
| SSE resume | Replay strictly after the supplied engine-scoped cursor | Real HTTP stream test |
| Foreign/negative/future cursor | 409 before streaming | Cursor validation tests |
| Closed/unavailable store | 503 stable problem without raw SQL errors | Readiness failure test |
| BFF engine transport failure | Localized recovery inside the shell | HTTP BFF test |
| Background typing/receipts/messages | Not implemented in this milestone | No simulated success endpoints |

This milestone matches addresses exactly after trimming. Canonical XMPP JID
normalization, account matching across independently configured engines, and
provider-ID/revision deduplication are still O04 work before actual ingestion/sync.
The pure revision selector implements ancestry-first selection and deterministic
timestamp/UUID concurrent-head ordering, but there is no public revision-import
API yet. Ordinary shared preference-field merging is also later work.

SSE IDs are `engineUUID:sequence`, distinct from entity UUIDs and future provider
cursors. Without Last-Event-ID, replay starts at zero. Clients can read the engine
cursor before fetching snapshots and then replay to avoid missing intervening
changes. Duplicate projections can be reconciled by stable entity/revision IDs.
The foundation retains the entire change log; expiry/reset/retention is not
silently claimed. Polling is bounded to 100 changes per batch; heartbeats occur
every 15 seconds and stream writes have bounded deadlines.

## Dependencies and component fit

Implementation choices are pinned in go.mod/go.sum and the tool/CLI modules:

- Goshtoso v0.6.1; core `appshell`, `panel`, `emptystate`, `badge`, `link`,
  `head`, assets, and expressions.
- templ v0.3.1100, matching generator and runtime.
- oapi-codegen v2.8.2, nethttp request validation, and generated client bindings.
- oapi-cli-gen commit `ca5a6b82aac1`, resolved as a concrete published-module
  pseudo-version; no runtime dependency on the generator.
- modernc SQLite v1.62.0 and Google UUID v1.6.0.
- Vacuum v0.39.3 in its isolated tool module.

Goshtoso v0.3.0 was the initial skill baseline; it does not expose the required
public expressions package. The consumer therefore pins the inspected v0.6.1
release. Core AppShell can be composed with an Oie-owned localized document;
there is no need to copy ConsoleShell's fixed-language document in this proof.

The custom CSS only composes the product layout. Component surfaces, controls,
labels, slots, and runtime assets use public APIs. A short document-level theme
coordinator applies server-resolved cookie preferences without localStorage.

The spec's rich chat-bubble and Margo semantic gaps are still relevant for later
message UI; this read-only consumer does not need those APIs yet. No upstream
patch or unmerged dependency has been fabricated. When a feature needs one, use
the agreed generic upstream PR and concrete commit pseudo-version workflow.

## Surface brief

- User/task: a single owner checks registered account metadata and engine reachability.
- Constraints: browser-first; local or hosted API; multiple devices; no protocol session yet.
- Register/archetype: product; application shell with account list and conversation placeholder.
- Priority: reachability, accounts and honest connection state, then future conversation workspace.
- Navigation: ordinary document links and an explicit HTMX fragment contract.
- Identity: Oie text identity, existing Goshtoso tokens/type stack, no invented brand assets.
- Density/motion: standard density and restrained library-owned motion.
- Consequential browser actions: none; API mutations are covered by the ledger above.
- Future rich-chat layout and controls remain part of later messaging work.

## Verification recorded

Local verification passed `make check` (including warning-free Vacuum lint and
fresh-generation comparisons), `make test-race`, generated CLI build/integration,
and the real Prosody TLS/login/discovery/message smoke check. Browser checks
passed all 16 viewport/theme/mode/locale combinations against both an empty local
engine and a separate BFF displaying a registered account. Both runs reported no
WCAG A/AA violations or console errors; keyboard skip navigation, system-theme
changes, and localized unknown-route recovery also passed. Screenshots were
inspected at desktop and narrow widths.

The nested Docker environment required the classic-builder fallback documented
in the README; Compose then started a healthy server. GitHub Actions repeats
these checks on the PR; local results do not imply a remote CI result.

## Remaining work

The next milestone needs the actual XMPP adapter, session/credential configuration,
scoped discovery, and live message ingestion/send operations. Resolve the provider
identity and durable outbox/attempt contracts before enabling sends. Rich bubbles,
Margo semantic output, and any generic library changes should be developed in
their upstream PRs when those consumers are implemented.

Full sync, per-device history deletion, PWA/offline queues, media, scheduling,
and merge backup/restore remain agreed alpha requirements with later milestones.
