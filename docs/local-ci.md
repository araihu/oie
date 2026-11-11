# Local CI loop

The CI graph runs through Dagger's public Go engine client and SDK, in the
isolated `tools/ci` module. The CLI's telemetry subscriptions could leave both
completed queries and session shutdown waiting indefinitely on this runner.
Silent CLI mode did not prevent the failure. The direct client omits those
subscriptions, captures command output per stage, and treats execution,
artifact-export and session-close errors as failures.

The Wails integration adds `make ci-desktop` and includes that lane in
`make ci`. It uses Ubuntu 24.04 with GTK 4/WebKitGTK 6, explicitly selecting
`linux/amd64` even when Dagger runs on an arm64 host. Native build, production
golangci-lint, vet, race tests and an actual WebView smoke run there. Xvfb,
a private D-Bus session and xdotool exercise the native Save/Cancel chooser.
See [desktop verification](desktop.md#verification).

Release packaging has its own `make package-linux` Dagger lane. It obtains the
application tree from the selected immutable tag, then uses current packaging
tooling to build on Ubuntu 24.04 amd64. A separate runtime container installs
the actual archive with no Go, compiler or development headers. It waits for
the branded WebView content, initializes SQLite, quits through Ctrl+Q and
reinstalls while preserving the profile. The installer fixture suite covers
platform selection, checksums, unsafe archives, dependency checks, escaped paths
and update rollback. `make check` includes these 27 installer cases.

The rc.0 archive lane passed with packaging revision
`6711a951c4343bf81e9a6c9c4bf4f913fb255056` and application source
`e8ffe5d0d924727478b8a5cbe3567a4240d7ae48`. Logs, the painted production window,
archive and source receipt are under
`tests/browser/artifacts/ci-rc0-packaging-painted`. The native macOS bundle
installation, LaunchServices launch, signature and retained-profile checks
passed under `tests/desktop/artifacts/rc0-packaged-macos`. These package checks
use isolated profiles; they do not replace messaging integration coverage.

The final packaging-tooling checks passed through `make ci-check` on
2026-11-10, including lint, generated-file verification, 27 installer fixtures,
race tests and builds. Evidence is under
`tests/browser/artifacts/ci-rc0-final-check`; setup took 51s, checks 6m39s and
the mock build 3s. The application source was unchanged from rc.0. After
publication, all five release assets downloaded over public HTTPS and matched
`SHA256SUMS`. The published installer also installed the Mac archive with only
system commands on PATH, with Go excluded, and its bundle signature verified.
Logs are retained under `bin/rc0-published-downloads.log` and
`bin/rc0-published-install-macos.log`.

`make check` remains independent of native libraries through the `server` build
tag. Desktop targets clear that tag. The Ubuntu desktop container explicitly
sets `GOPATH=/go` to use the mounted module cache; copying the Go toolchain alone
does not inherit the Go image's environment. Native build and lint caches are
separate from the headless arm64 lane. This avoids downloading tool dependencies
again whenever a source edit invalidates a container layer.

The real-engine browser fixture verifies a fresh account's empty conversation
list against the API and waits for the matching empty pane. A selected
conversation still requires the account/engine/conversation draft scope and
restored text to match. This covers first-run accounts as well as accounts with
retained history. Timeout diagnostics preserve the DOM scope, API list and
screenshot.

On 2026-11-10, the complete Wails integration graph passed with exit 0 on
`7499ad1b12a237196080a0c1ea398a72c5f5f5b9`. This includes checks, generation,
lint, race tests, builds, the Ubuntu 24.04 amd64 desktop/WebView lane, isolated
XMPP integration and all 43 browser workflows including the real-engine flow.
Evidence is under `tests/browser/artifacts/ci-wails-release-ready`, with the
exact materialized source hashes in `source-snapshot.json` and the final result
in `integration-receipt.json`. Subsequent application changes for the rc.0 tag
are documentation only; later packaging and installer tooling has the separate
validation described above.

Checks took 24s, desktop dependency setup 4m36s, native build/lint/race 6m35s,
native WebView 58s, XMPP 50s and browser checks 10m49s. Native amd64 execution
uses emulation on this arm64 runner. The same host integration also passed
actual macOS Apple Silicon source installation, vet/race checks, two complete
native lifecycle runs and a separate real video/PDF probe. macOS file-chooser
completion remains a manual check; the Linux lane drives the actual GTK chooser.

The Go container limits `GOMAXPROCS` to four, which also bounds the Go command's
default package/build parallelism on shared runners. The real FFmpeg fixture
exceeded its five-second deadline during concurrent unrestricted race runs on
the 12-CPU development runner. The decoder now caps PNG output encoding
threads as well as decoding threads; the application deadline stays unchanged.
Thirty local extractions before and after the output cap produced identical PNG
sizes, with observed peak threads falling from 15 to 5. This measurement explains
the resource change, rather than claiming a general performance benchmark.

Each concurrent session should select its own artifact directory:

```sh
OIE_CI_SSH_HOST=dagger-vm \
OIE_CI_ARTIFACTS=tests/browser/artifacts/my-run make ci
```

Relative artifact paths resolve against the repository before the runner creates
its temporary source snapshot. The default remains `tests/browser/artifacts/ci`.
Seven shell lifecycle tests cover defaults, absolute/relative overrides, failure
evidence and exit-status preservation. Separate paths prevent one session from
overwriting another session's stage logs.

On 2026-11-09, the full graph completed with exit 0 on
`ef9a9a6aec7ad39eff9c05995859b2ff3efc2574`, using
`OIE_CI_ARTIFACTS=tests/browser/artifacts/ci-unified-search`. Checks, lint,
generation, race tests, builds, isolated XMPP integration and all 39 browser
workflows passed, including unified global search, simultaneous conversation
search and saved XMPP contacts. Checks took 1m03s, XMPP integration 54s and the
browser stage 10m34s. The six stage logs, complete browser log, real-engine report,
preview preservation checks and exact source/exit receipt are retained in that
ignored directory. Native `GOMAXPROCS=4 make check` also passed.

The final run includes the current sender-color and avatar-grouping changes.
It preserves the original Control-click and middle-click popup assertions and
30-second bounds. That fixture uses full Chromium's new headless mode because
the lighter headless shell intermittently loaded a real background tab without
emitting Playwright's initialized-page event. Five focused and three complete
native runs, a scoped Dagger run and the full graph passed after the browser
selection change. Original-page instrumentation now stays on that page.

The combined suite also verified search Back/Forward query restoration and
localized, accessible conversation-search links. A room-reply recovery helper
now checks that controls belong to their modal and remain outside the timeline,
including when the modal stays open through a revision conflict. No send,
conflict, draft, accessibility or popup assertions were removed.

The same engine and SSH tunnel passed two uncached FFmpeg installations with
clean client exits in 44 and 37 seconds. An intentional container exit of 23 and
cancellation of a running command both returned errors and closed their sessions.
These probes did not require restarting the shared engine or preview processes.

On 2026-11-09, `OIE_CI_SSH_HOST=dagger-vm make ci` completed with exit 0 on
`da7a27e1b1d119c9aa8a76e880d983f53a0b2626`, including conversation archive and
favorites. Checks, lint, generation, race tests, builds, isolated XMPP integration
and all 38 browser workflows passed. Checks took 53s, XMPP integration 34s and
the browser stage 8m23s. Logs, the real-engine report and exact source/exit record
are under the ignored `tests/browser/artifacts/ci-conversation-preferences`
directory. Native `make check` also passed during this increment.

The new fixture covers scoped archive/favorite mutations, canonical state after
receipt replay, uncertain retries, revision conflicts, native forms and retained
drafts, timeline and list position. Its implicit-selection regression fails the
previous binary and passes the fix. The real-engine workflow verifies persisted
preferences and an incoming XMPP message that leaves its conversation archived.
The full run also passed the existing modifier-key and middle-click navigation
checks without changing their assertions or timeout.

An earlier full run on the same date completed with exit 0 on
`45e4f147553039332b01e177f57edfc2c6f202ca`, including all current app changes.
The previously uncommitted appearance, pending-send, media feedback and wallpaper
work is integrated in `9f794e0`; no app edits were excluded. Checks, lint,
generation, race tests, builds, isolated XMPP integration and all 37 browser
workflows passed. Checks took 1m00s, XMPP integration 34s and the browser stage
8m39s. Current logs, the real-engine report and source/exit metadata are under
the ignored `tests/browser/artifacts/ci-current-full` directory.

The combined run exposed insufficient pending-bubble text contrast and a real
emoji keyboard-focus race during cached rendering. Pending text now retains
contrast while faded. The emoji regression deterministically fails the previous
binary and passes the fix, including a native run with 4x CPU throttling.
Appearance and send-geometry fixtures now run permanently in Dagger; the latter
covers 24 sends and 32 accessibility scans across widths, modes and library
themes. The pipeline also covers the current queued-cancellation menu and
complete two-way messaging against its disposable Prosody service.

An earlier full run on the same date completed with exit 0 on
committed source `2bdaa06749ced7e3e69bf7be9aa7f2310758d245`. The client closed
normally after checks, lint, generation, race tests, builds, isolated Prosody
integration and all 35 browser workflows, including real two-way XMPP messaging.
Checks took 1m06s, XMPP integration 37s and the browser stage 9m43s. Evidence and
the exact source/exit record are under the ignored
`tests/browser/artifacts/ci-committed-full` directory. That earlier snapshot
excluded independent UI edits; the current run above includes them.

Native `make check` also passed after the cancellation hardening. The runner's
Go race tests and six shell/source lifecycle tests cover failed commands,
expired deadlines, blocked session cleanup and a runner that ignores TERM.
`OIE_CI_SSH_HOST=dagger-vm make ci-check` then passed with clean exit 0 on
`60e9bc9`, including the final shutdown protection. Its separate evidence is in
`tests/browser/artifacts/ci-committed-checks`.
Cancellation gives session cleanup at most 15 seconds before failing the process;
the shell also kills an unresponsive owned runner after its grace period. An
intentionally expired `OIE_CI_TIMEOUT=1ms` returned a failing status promptly.
Browser failures export evidence and preserve a failing status.

The per-feature notes below describe earlier snapshots, including the previous
CLI runner's timeouts. They do not override the completed full-suite result above.

The video playback increment passes native `make check`, backend/BFF race tests,
real native WebM playback/lifecycle checks and the 16-case shared-content browser
regression. Isolated committed-source web/API tests also pass. Dagger passed the
initial engine/player implementation; the final repeat ended with exit 143
while connecting to the runner, so final-snapshot Dagger completion remains
unverified. See [video playback](video-playback.md) for the measured scope,
codec and streaming-retention limits, and live data preservation.

The searchable shared-content account filter passes final native `make check`,
Dagger `make ci-check` and the shared-content browser fixture with 16 accessible
viewport/theme/locale variants. Selection follows the URL through fragment
navigation, Back/Forward and reload; account search does not submit an extra `q`.
A read-only live check also passes with no mutations or page errors. Goshtoso
[combobox PR #376](https://github.com/araihu/goshtoso/pull/376) provides the generic
form-isolation, search feedback and accessibility fixes through the pinned
[integration PR #375](https://github.com/araihu/goshtoso/pull/375).

The account-avatar indicator and state-aware account controls pass native
`make check` and Dagger `make ci-check`, including lint, generation, race tests
and builds. The account-status browser fixture covers four viewport/locale/theme
variants, live state transitions and preservation of unfinished connection fields.
The affected avatar, live-chat and capabilities browser fixtures also pass.
A read-only live account-detail check passes with no mutations or page errors.
The development XMPP/browser integration was not rerun for this UI change; its
connected-state selector was updated to the avatar indicator.

The message-action corner fix passes native `make check`, Dagger `make ci-check`
and the 42-case browser layout/action matrix. Its exact upstream dependency is
pinned to an open integration PR. See [message actions](message-actions.md) for
the verified scope and the broader upstream-suite limitation.

The conversation-wallpaper update passes `make check` and Dagger `make ci-check`,
including lint, generation checks, native/race tests and builds. Read-only live
browser checks pass at 390 px and 1440 px in light and dark modes, with no page
errors or mutations. This native Dagger result does not claim the full XMPP and
browser pipeline has completed.

The received-corrections increment passes native `make check`, core and web race
tests, direct/room development XMPP integration, and browser workflows. Its
final Dagger attempt remained at runner connection and was stopped after 196
seconds. Full pipeline completion remains unverified. See
[received corrections](received-corrections.md) for validation and live data preservation.

The outgoing-replies increment passes native checks, changed-package races,
development XMPP integration and host browser workflows. The corrected capability
fixture passes locally. The final Dagger retry failed attaching a runner step with
`waiting for client session attachables: context deadline exceeded`; full pipeline
completion remains unverified. See [outgoing replies](outgoing-replies.md).

The fragment-navigation increment on 2026-11-08 passes native checks and all host browser suites. Dagger completes Go checks/races/builds and isolated XMPP integration, but its browser initialization currently fails expanding the cache argument with `waiting for client session attachables: context deadline exceeded`. A fresh client retry reproduced the failure. Full Dagger completion for this increment is unverified. See [fragment navigation](fragment-navigation.md) for the verified scope.

Keep Oie increments as local commits. Open a pull request when there is a version
ready for alpha v0.0.1. Existing draft requests remain historical review points.

Use Go 1.27.4 or newer, Make, Git, Python 3 and Docker on the host. The runner pins Dagger
v0.21.9. It honors `_EXPERIMENTAL_DAGGER_RUNNER_HOST`; without an override it
provisions the pinned engine image through Dagger's container driver:

```sh
make ci-check  # Contracts, generation drift, golangci-lint, vet, unit/race tests, builds
make ci        # Also isolated Prosody integration and all browser workflows
```

GitHub Actions runs the same `make ci` command and uploads its stage logs and
test evidence as `dagger-ci-evidence`, including when tests fail.
The workflow uses `hostinger-vps-trusted` for allowlisted pushes to `main` and
`hostinger-vps-pr` for ready pull requests opened by allowlisted actors from
branches in the same repository. The allowlist comes from the organization
variable `HOSTINGER_PR_ACTORS_JSON`. Other events, including fork pull requests,
run on `ubuntu-latest`. The Hostinger runners supply separate Dagger sockets and
caches for the two lanes.

The Dagger Go graph and shell entrypoint are under `tools/ci`. Dagger dependencies
remain separate from the root application and generation-tool modules. Images
are pinned by digest. Go modules,
Go compilation, golangci-lint results, npm packages and Playwright browsers have
persistent engine caches.
The runner prints stage progress every 30 seconds and saves command output under
`tests/browser/artifacts/ci`. Browser evidence is under its `browser` subdirectory.
`checks`, `race` and `build` run as separate stages with their own logs.
Go and browser stages use a bounded memory-backed `TMPDIR` for disposable
SQLite databases and browser profiles. Go keeps its compiler scratch files in
disk-backed `GOTMPDIR` because a cold tool build can exceed the memory mount's
capacity. The binaries, logs and browser exit receipt also live outside those
temporary mounts.
On the SSH runner, a probe with 12 fresh SQLite databases and 120 committed
inserts took 6.58 seconds on the shared disk and 0.039 seconds in memory.
The shell lifecycle tests took 38.04 seconds in the disk-backed baseline,
where the shell-only termination test timed out, and 1.16 seconds in the
memory-backed check stage.
`OIE_CI_TIMEOUT` sets the overall execution deadline, defaulting to 30 minutes.
Failure and interruption preserve a failing exit status and clean only the
snapshot, runner process and optional SSH tunnel owned by that invocation.
The Dagger checks download the precompiled Vacuum and golangci-lint releases
for the engine's architecture and verify their SHA-256 digests. The desktop lane
uses the amd64 golangci-lint binary. The pinned golangci-lint release reads the
export data produced by the application's Go toolchain. Direct `make check`
runs the versions pinned in the tools modules, with optional `VACUUM` and
`GOLANGCI_LINT` overrides.

`make check-ci-tools` checks the CI module and shell lifecycle regressions and is
part of `make check`. The CI module uses the `http2legacy` build tag for Dagger's
gRPC dependency on Go 1.27. Its `distconsts` replacement points to the exact
Dagger v0.21.9 commit because the published root module requires an unpublished
nested-module tag. Neither workaround changes the application or generator
dependency graphs. System packages install before source is added to the graph,
so a source edit reuses the FFmpeg installation layer.

The runner materializes current tracked and untracked source, including local
edits. It excludes Git internals, data directories, credentials, binaries, browser
artifacts and dependency directories before invoking Dagger. A source-boundary
test verifies those exclusions, including an accidentally tracked data file.

Each full Dagger run starts one disposable Prosody service and shares its TLS
certificate across the protocol smoke check, Go integration tests and live
browser workflow. The smoke check and Go integration tests run in sequence
while Playwright setup runs in parallel.
The browser evidence records the number of Prosody starts for that run. CI
fails if the count differs from one.

Dagger sets `OIE_XMPP_BROWSER_FIXTURES=1` for Prosody and the live browser
workflow. This provisions separate `browser-alice@localhost` and
`browser-bob@localhost` accounts so the browser's empty-inbox check is independent
of the messages, rosters and profiles from the other suites. The browser still
uses a fresh Oie data directory. Standalone runs default to the standard
development accounts.

The profile integration test requires the repository's Prosody configuration,
which enables `vcard_legacy`. If a local Compose server returns
`service-unavailable` for the fixture vCard, run `make dev-up` to rebuild the
service with that configuration. Its data volume persists across the rebuild.
Integration tests accept `OIE_XMPP_ENDPOINT` for the Dagger service binding while
keeping account-domain TLS verification. Host Compose tests keep their existing
loopback default. Browser evidence is exported to the ignored directory
`tests/browser/artifacts/ci`, including `ci.log` on browser failure.
The pipeline exports that evidence before enforcing the browser exit status.

Failures stop the pipeline. No image publication, application deployment, push,
pull request or merge is part of either command.

For an engine behind an SSH Unix-socket forward, each run can own its own tunnel:

```sh
OIE_CI_SSH_HOST=dagger-vm make ci
```

This uses the configured SSH alias and `/run/dagger/engine.sock` on that host.
It does not restart the engine or a shared tunnel. Override `OIE_CI_SSH_SOCKET`
when the engine uses another remote Unix socket. Without this option, Dagger uses
its ordinary configured runner connection.

## Engine API and CLI lane

`make check` now includes `make check-api-compatibility`, using the frozen
engine 0.27 baseline and pinned oasdiff with positive/negative controls.
Existing HTTP tests validate actual JSON responses against generated schemas.
The isolated XMPP stage also runs `tests/cli` with `OIE_XMPP_INTEGRATION=1`,
including generated raw SSE/media commands, committed-response loss and restart.
The two packages run with `-p 1` because their development Alice/Bob accounts
share one isolated Prosody service. This serializes packages, not production
requests; the CLI fixture still exercises concurrent replay.

Use `OIE_CI_ARTIFACTS=tests/browser/artifacts/ci-engine-api-cli make ci` (plus the
usual runner configuration) for this milestone. See
[the verification record](engine-api-cli-verification.md) for source and limits.

## Validation on 2026-11-08

The message-star increment passes native `make check`, including golangci-lint
with zero findings, changed-package race tests, development Prosody integration,
and the complete real-engine browser workflow. Browser acceptance also covers
source stars, retries, conflicts, menus, the compact sender header and existing
preview/Markdown/avatar/live-chat behavior.

Earlier Dagger runs completed Go checks, races, builds, isolated XMPP integration
and browser UI suites. The final real-engine readiness probe initially failed
because the slim image lacked curl; it now uses Node and passes locally.

Full `make ci` completion for the current increment remains unverified on this
host. The latest attempt timed out after 180 seconds. Dagger v0.21.9 clients have
intermittently stopped receiving completed API results and failed their engine
healthcheck. Independent SSH and Docker connection attempts did not establish a
completed pipeline. No green full-pipeline result is claimed. The live-preview
engine and its data are excluded from Dagger tests.

The received-reply increment adds the browser reply fixture to the Dagger browser
stage. Native checks, engine/web races, development Prosody live/archive reply
integration and the real-engine browser workflow pass. A read-only observation
also confirms navigation for a recovered reply in the existing live engine.
The full Dagger run was attempted with a 180-second bound; its result is recorded
separately from these native checks.
The current attempt exited 124 after 180 seconds without a Dagger stage result.
Native `make check` completed successfully. Full Dagger completion remains
unverified; no green pipeline result is claimed.

The account-capabilities increment adds `capabilities.cjs` to the Dagger browser
stage. Native `make check`, engine/API/XMPP/web race tests, development-server
discovery integration and the real-engine browser workflow pass. The fixture
covers sixteen responsive theme/color/language combinations and read-only
unknown/partial/error recovery. A real-account browser observation confirms the
scoped matrix and return to management. Full Dagger completion is tracked
separately from these checks.
The capabilities run again timed out at 180 seconds with exit 124 before
returning Dagger stage results. No green full-pipeline result is claimed.

The conversation-details increment adds `conversation-details.cjs` to the browser
stage. Native checks/races, Prosody no-join metadata integration, sixteen browser
variants and the real-engine browser workflow pass. The bounded full Dagger
attempt timed out after 180 seconds with exit 124 before returning stage results.
