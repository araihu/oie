# Engine API and CLI verification

Date: 2026-11-10. Baseline `47870bd`, after the completed Wails integration.
This milestone is separate from release publication and initial-history B25.
The final evidence section below records the tested implementation revision.

## Reproduction

```sh
GOMAXPROCS=4 make check
GOFLAGS=-tags=server GOMAXPROCS=4 go test -race -p 1 ./internal/adapters/httpapi ./internal/adapters/web ./internal/apphost ./tests/architecture
OIE_XMPP_CA_FILE=/path/to/development/localhost.crt \
OIE_XMPP_ENDPOINT=127.0.0.1:5222 GOMAXPROCS=4 make test-cli-xmpp
OIE_CI_SSH_HOST=dagger-vm \
OIE_CI_ARTIFACTS=tests/browser/artifacts/ci-engine-api-cli make ci
```

The CLI proof is `TestCLIDataOnlyXMPP` in `tests/cli/engine_proof_test.go`.
It builds the generated executable in its own module and operates a disposable
production application/SQLite/native-XMPP/HTTP composition with no BFF. The
fixture owns setup/lifetime; every demonstrated command, query, receipt and
stream is exercised through the generated executable against public API routes.
Production `apphost` data-only composition is separately verified for both
local/hosted roles, including absence of assets/presentation and identical
engine read-state behavior with and without the BFF. This is not a replacement
engine, store shortcut in the CLI, or a claim that UI packages leave the server
binary's dependency graph.

The native run uses a dedicated Prosody container/volume and development CA,
with independent port 15222 forwarding. Docker Compose build was unavailable
because its overlay mount returned operation-not-permitted; the same pinned
Prosody image was started with copied repository config/startup files in a new
container. No existing development/preview service or data volume was changed.
The Dagger graph creates its own Prosody service and certificate.

## Acceptance evidence

| Scenario | Assertion in the automated proof | Limits |
| --- | --- | --- |
| Identity/readiness/capabilities | CLI reads engine, health and connected direct-text capability | Snapshot evidence, not all protocol features |
| Accounts/connections/contacts | CLI registers Alice/Bob, configures through stdin, observes actual connection, lists retained contacts, opens direct chat | Existing development accounts; provider registration is backlog |
| Text in both directions | Bob's independent account actually receives Alice's marker; Bob replies and Alice receives it via structured event | Actual XMPP receipt in this fixture; submitted alone still does not prove delivery |
| Replay/conflict/concurrency | Original acceptance bytes unchanged; changed payload exits 4; four concurrent same-key replays; exactly one retained/recipient effect | Not protocol exactly-once delivery |
| Lost response | Proxy closes socket after actual committed success; CLI exits 3; operation lookup plus same-key retry recover one message | No implicit retry or newly minted key |
| Stale/cancelled | Wrong queued revision rejected; valid queued cancellation retained; recipient does not receive cancelled text | Cancellation only while queued |
| Restart | Same directory preserves identity/history/receipt; enabled Bob reconnects; Alice's queued URL texts resume once; SSE old cursor replays retained changes | Unknown dispatch is covered by existing engine tests, never automatically resent |
| Events | CLI raw SSE frames parsed/applied; ID/type/data agree; disconnect via SIGINT exits 3; reconnect after applied cursor observes reply and retained post-restart changes | No automatic CLI reconnect; clients own applied-cursor persistence |
| Shared bytes | Same real engine indexes sent URL references; CLI downloads exact video/TXT/PDF bytes, tests matching/mismatched If-Range, 416 and unsupported DOCX | External byte provider is an injected deterministic application port; no real Internet/provider-media interoperability claim |
| Byte bounds/truncation | Output limit exits 3 with bounded partial stdout; truncated declared source exits 3; no JSON appended to original bytes | Raw consumers must discard incomplete output |
| Exit/output discipline | JSON decoded from stdout, bytes compared verbatim, diagnostics only on stderr; input 2 / transport 3 / HTTP 4; request credentials absent from diagnostics | Machine error-code projection is in HTTP Problem Details; CLI emits sanitized HTTP status diagnostics |

`make check-api-compatibility` uses the explicit normalized baseline documented in
`api/compatibility/README.md`; controls reject removed operations/required output
fields and accept an additive optional field. `response_contract_test.go` checks
actual HTTP bytes, invalid version/missing-field/enum negative controls, 413/415,
405 Allow, matching Problem status and pollable creation/acceptance Location.
The existing request helper applies response validation to its successful/error
HTTP calls; this is not universal runtime response-validation middleware.

Browser regressions run against the built BFF via `OIE_WEB_BINARY=.../bin/oie`:
`read-state.cjs`, `previews.cjs`, `video-thumbnails.cjs` passed natively. They cover
visibility/CSRF/uncertain retries, lazy image behavior and playback/poster geometry.
Native host handler tests preserve private capability and browser scope checks.
The complete Dagger graph includes the desktop/WebView and remaining browser
regressions; its final result must be read separately below.

## Remaining scope

All 55 implemented operations have generated CLI commands; four previously
excluded SSE/binary commands are enabled through the overlay and use supported
`--output raw`. JSON room/star/preferences branches retain their existing
real-store CLI tests. Live CLI edit/reaction/room/profile/avatar/retained-image
branches are not all exercised by this fixture; their specialized HTTP/XMPP
and web tests remain complementary evidence. No TUI, SDK, WASM, daemon,
authentication, release or backlog feature is claimed.

No universal If-Match/ETag revision policy, deprecation duration, event expiry,
rate-limit quota or correlation header is introduced. Original media remains
private no-store with If-Range; conditional avatar/image GET is covered by the
existing HTTP tests. RFC errata index access was unavailable to the review
browser. See the [audit](engine-api-audit.md) and [policy](engine-api-policy.md).

## Final evidence

Tested implementation: `a42f7aa9af3730f11c035399191013faaccb0642`.
The Dagger materialized snapshot contains 994 source files, all byte-for-byte
identical to that commit. Per-file SHA-256 values and the empty mismatch list
are in `tests/browser/artifacts/ci-engine-api-cli/source-snapshot.json`.
Later documentation corrections do not change that implementation.

A separate black-box smoke run built `bin/oie` and `bin/oiectl`, launched the
actual `-data-only` command and exercised readiness, register/open/send/cancel
and same-directory restart. `/`, `/ui/events` and `/assets/` returned 404.
Its result is `engine-api-cli-native/binary-data-only.json`.

Native validation passed: `make check`, the CLI XMPP proof with `-race` (8.87s),
the entire development XMPP suite with `-race` (65.687s), focused adapter/host/
architecture race checks with packages serialized, and all three affected
browser suites. One earlier concurrent-package race invocation exceeded the
existing three-second host-test cleanup deadline. The host suite passed when
repeated alone (9.776s) and in the serialized complete focused run (10.663s for
host). The deadline was not increased; both the failure and passing logs are
retained. No data race was reported.

Dagger checks passed (4m6s), including lint, generation drift, compatibility,
unit/race tests and builds. The complete graph continues with desktop, isolated
XMPP/CLI and browser checks; its final exit will be recorded below.
Native logs are retained in the ignored
`tests/browser/artifacts/engine-api-cli-native`; full graph evidence uses
`tests/browser/artifacts/ci-engine-api-cli`.
