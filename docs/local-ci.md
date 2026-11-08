# Local CI loop

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

Use Dagger v0.21.9 and a running engine, plus Make, Git and Python 3 on the host:

```sh
make ci-check  # Contracts, generation drift, golangci-lint, vet, unit/race tests, builds
make ci        # Also isolated Prosody integration and all browser workflows
```

The Dagger Shell definitions and runner are under `tools/ci`. Build tools remain
outside the root application module. Images are pinned by digest. Go modules,
Go compilation, golangci-lint results, npm packages and Playwright browsers have
persistent engine caches.
The runner prints plain stage progress so cache/session failures and test output
remain visible during the local loop.
`make check` includes golangci-lint v2.13.1 pinned in the tools module.
Its standard linters include errcheck, govet, ineffassign, staticcheck and unused.
The first run populates those caches; later runs reuse them.

The runner materializes current tracked and untracked source, including local
edits. It excludes Git internals, data directories, credentials, binaries, browser
artifacts and dependency directories before invoking Dagger. A source-boundary
test verifies those exclusions, including an accidentally tracked data file.

The full pipeline creates its own Prosody service and development certificates.
It does not use the live-preview engine, account credentials or data directory.
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
