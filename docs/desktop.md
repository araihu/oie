# Native desktop host

Oiê uses Wails v3.1.0 to display the existing Goshtoso/HTMX interface. The
desktop command starts the Go engine, SQLite store, media directory and BFF in
one process. The browser/server command remains `cmd/oie`.

## Installation

See [desktop installation on macOS and Linux](installation.md) for release
archives, the per-user installer, runtime/build dependencies, source installation,
macOS linker flags, optional FFmpeg and manual updates.
That guide owns the installation commands; this document describes the host
implementation and native verification.

## Release packaging

The application source for `v0.0.1-rc.0` is fixed at
`e8ffe5d0d924727478b8a5cbe3567a4240d7ae48`. Packaging tooling from later commits
builds the macOS `Oie.app` bundle and Linux `bin`/`share` archive without moving
that tag. Each archive includes source/build metadata and license notices. The
release's `packaging-receipt.json` records the source revision, packaging-tooling
revision, source archive hash, platform metadata and artifact hashes separately.

The installer selects either `darwin/arm64` or `linux/amd64`.
It verifies the selected archive against `SHA256SUMS` before extraction, rejects
unsafe archive entries and stages installation beside the destination. Existing
application files remain available for rollback until replacement completes.
The profile directory is outside this operation. No dependency installation,
process termination or quarantine removal is part of the installer.

The macOS bundle uses ad-hoc signing, without Developer ID signing or
notarization. Its current flat icon remains unchanged for rc.0; icon variants
are B24. Linux's desktop entry uses absolute escaped executable and icon paths
for the selected prefix.

The published archives passed installation and reinstallation with retained
SQLite identity and user files. On native Apple Silicon, LaunchServices opened
the installed signed bundle and initialized the engine. In an Ubuntu 24.04
amd64 runtime container under emulation, the installed production executable
painted the Oiê interface and exited through Ctrl+Q. That container had runtime
libraries and an Xvfb test harness, with no Go, compiler or development headers.
The package checks use disposable profiles and do not connect real XMPP accounts.
The [installation guide](installation.md#platforms-and-verified-scope) and
[rc.0 release notes](releases/v0.0.1-rc.0.md) describe platform limits.

### Rebuild the release archives

Use the packaging revision in the release receipt as the tooling checkout.
`OIE_RELEASE_VERSION=v0.0.1-rc.2 make package-linux` obtains the tagged source
and runs the Ubuntu amd64 build and installation checks through Dagger. Its
output is under the selected CI artifact directory's `release` folder.

On native Apple Silicon with the build dependencies installed, obtain the
application tree separately from the tooling checkout:

```sh
release_inputs=$(mktemp -d)
release_version=v0.0.1-rc.2
release_revision=$(git rev-parse "$release_version^{commit}")
mkdir "$release_inputs/source"
git archive --format=tar --output="$release_inputs/source.tar" "$release_revision"
tar -xf "$release_inputs/source.tar" -C "$release_inputs/source"
sh scripts/package-desktop.sh --version "$release_version" \
  --revision "$release_revision" \
  --source "$release_inputs/source" --output "$PWD/dist/$release_version"
```

The builder refuses to overwrite an existing archive. Build timestamps and
native toolchain metadata can change the archive hash; rebuilding does not
promise bit-for-bit identical output. Keep the source tag fixed and record the
new tooling, source hash and build metadata for every published package.

## Runtime options

Optional command flags:

```sh
bin/oie-desktop --data-dir /absolute/path/to/profile
bin/oie-desktop --engine-url http://127.0.0.1:18084
bin/oie-desktop --lang pt-BR
```

`--engine-url` uses that engine through the same BFF HTTP contract and does not
open a local engine database. It does not implement device sync. The desktop
host does not inherit the live browser preview's data directory.

## Data and lifecycle

The default data directory is `~/Library/Application Support/Oiê` on macOS.
On Linux it is `$XDG_DATA_HOME/oie` when that variable is absolute, otherwise
`~/.local/share/oie`. Moving or replacing the executable leaves this data intact.

Closing the window hides it. The engine stays alive. The tray menu, macOS dock
reopen and another invocation for the same profile show the existing window.
Quit from the menu or `Cmd/Ctrl+Q` stops the engine and closes SQLite. A second
invocation for the same canonical data directory does not start another engine.

Linux WebKit storage resides inside the profile's `webview` directory. Wails'
public macOS API currently uses the default WKWebsiteDataStore. Engine IDs scope
Oiê's drafts/cache, but separate custom profiles on macOS do not yet have fully
isolated native website storage. The normal single-profile installation is the
initial target.

## Transport

The window uses the stable `wails://localhost` origin. Wails serves the existing
presentation handler and assets, including the BFF's scoped read-state and
preview/thumbnail status routes. It does not expose the structured engine API.
The BFF calls the same generated engine HTTP client as the browser build.

An embedded engine binds an ephemeral loopback port. A per-launch capability
header protects that listener, including its presentation routes. The capability
does not appear in page HTML, URLs or persisted preferences.

WebKit custom-scheme responses do not retain the BFF's HTTP cookies. The native
host supplies them inside the process, preserving existing CSRF checks. It
persists only the language/theme preference allowlist in a private file and
rotates CSRF state at every launch. Streaming responses and byte ranges pass
through the wrapper without buffering the complete response.

The small desktop script loads the Wails runtime, opens external HTTP(S) links
in the system browser and uses the native Save dialog for existing BFF download
routes. Downloads stream to a temporary file and replace the chosen destination
only after completion. Cancelling the dialog does not report success. Chat
operations continue through the existing HTTP contract.

## Verification

`make check` uses the `server` build tag so web/API development does not require
GTK libraries. `make ci-desktop` runs a separate native Ubuntu 24.04 x86_64 lane
with GTK/WebKit, native build, golangci-lint, vet, race tests and a real WebView
smoke test. `make ci` includes both lanes and the existing XMPP/browser suite.

The smoke test uses a disposable embedded profile and a fake loopback engine.
Wails' MCP driver is enabled only in the disposable test binary. It exercises
HTMX, CSRF, events, partial media reads, retained drafts/preferences,
hide/reopen, single-instance behavior and engine/database shutdown. Evidence is
written to `tests/desktop/artifacts` and exported with Dagger's stage logs.
It never connects a real XMPP account.

On a macOS development host, run `GOFLAGS= sh tools/ci/desktop.sh` for the same
native transport/lifecycle smoke. Linux CI also drives the actual native file
chooser inside its isolated Xvfb display. macOS file-chooser completion requires
a manual check when OS accessibility automation is unavailable.

Tested OS versions describe evidence, not a promised minimum OS version. Release
support targets remain macOS Apple Silicon and Ubuntu Linux x86_64. Native
macOS validation must run on macOS; Linux compilation cannot verify Cocoa or
WKWebView behavior.

The 2026-11-10 macOS run passed on Apple Silicon with macOS 26.6.2, SDK 26.5
and Go 1.27.3. It covered all six smoke groups, production source installation,
vet and desktop/runtime race tests. The build used a macOS 13 deployment target;
that flag is not evidence of runtime support on macOS 13. Logs and the report
are under `tests/desktop/artifacts/macos-source-install.log` and `report.json`.

A separate macOS media probe loaded the checked-in WebM fixture through the
native BFF, observed partial requests and advancing playback, then loaded the
PDF fixture through PDF.js with a real Worker and rendered its expected text.
The driver, invocation, binary metadata and results are retained under
`tests/desktop/artifacts/mac-native-media*`. This verifies those fixtures on that
WebView, not every video codec or document.

The Ubuntu 24.04 `linux/amd64` lane passes seven native smoke groups, including
the actual GTK Save/Cancel chooser. The development runner is arm64, so this
target runs under emulation. Its first successful chooser report is under
`tests/browser/artifacts/ci-wails-dialog-fixed/desktop/report.json`.
