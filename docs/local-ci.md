# Local CI loop

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
`tests/browser/artifacts/ci`.

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
