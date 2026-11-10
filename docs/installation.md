# Desktop installation on macOS and Linux

Oiê's desktop package includes the Go engine, SQLite store and web interface in
one application. Add an existing XMPP account after launching. An external XMPP
server is still required for messaging.

The current release candidate is **v0.0.1-rc.0**. The first alpha, v0.0.1,
requires the [initial-history usability improvement B25](history-bootstrap-discussion.md),
which is not implemented in rc.0. Its current history recovery is accepted for
this release candidate.

Download packages and the installer from
[v0.0.1-rc.0 on GitHub Releases](https://github.com/araihu/oie/releases/tag/v0.0.1-rc.0).
Source installation from the same immutable tag remains available.

## Install a release package

Prebuilt packages require no Go, compiler, headers, Node, Docker or separate
SQLite installation. On Ubuntu 24.04 x86_64, install the runtime libraries
yourself before running the installer:

```sh
sudo apt update
sudo apt install ca-certificates libgtk-4-1 libwebkitgtk-6.0-4 libsoup-3.0-0
```

macOS supplies Cocoa and WebKit. FFmpeg is optional on both platforms.
The installer also needs `curl`, `tar`, `gzip` and either `sha256sum` or
`shasum`. On Ubuntu, install `curl` separately if it is missing.

Save the release installer, then run it as your current user:

```sh
curl --fail --location --proto '=https' --proto-redir '=https' \
  --output oie-install.sh \
  https://github.com/araihu/oie/releases/download/v0.0.1-rc.0/install.sh &&
sh ./oie-install.sh --version v0.0.1-rc.0
```

You can inspect `oie-install.sh` before running the second command. Do not use
`sudo` for the installer. It detects the OS and architecture, downloads the
matching archive over HTTPS and verifies its SHA-256 against `SHA256SUMS` before
extraction. It checks native libraries and prints dependency guidance rather
than installing system packages. It does not remove macOS quarantine attributes.

| Platform | Default installation | Launch |
| --- | --- | --- |
| Ubuntu Linux x86_64 | Executable under `~/.local/bin`, desktop entry/icon/docs under `~/.local/share` | Applications menu or `~/.local/bin/oie-desktop` |
| macOS Apple Silicon | `~/Applications/Oie.app` | Finder or `open "$HOME/Applications/Oie.app"` |

The installer defaults to rc.0. Use `--version VERSION` for another published
release and `--prefix DIR` to choose a different install root. On macOS the
prefix is the parent of `Oie.app`; on Linux it contains `bin` and `share`.
For example:

```sh
sh ./oie-install.sh --version v0.0.1-rc.0 --prefix "$HOME/Apps/Oie desktop"
sh ./oie-install.sh --help
```

The release assets are:

| Asset | Contents |
| --- | --- |
| `oie_v0.0.1-rc.0_darwin_arm64.tar.gz` | `Oie.app` bundle with executable, resources and licenses |
| `oie_v0.0.1-rc.0_linux_amd64.tar.gz` | Executable, desktop entry, icon and documentation in `bin`/`share` |
| `install.sh` | Per-user installer for both targets |
| `SHA256SUMS` | SHA-256 checksums for the archives, installer and receipt |
| `packaging-receipt.json` | Source/tooling revisions, archive hashes, platform build metadata and verification scope |

The application source stays at tag `v0.0.1-rc.0`, commit
`e8ffe5d0d924727478b8a5cbe3567a4240d7ae48`. Newer packaging tooling does not
move that tag. The receipt records the packaging revision separately, and
`build-info.json` inside each package identifies its application source.

### macOS first open

The bundle has an ad-hoc signature. It is not Developer ID signed or notarized.
If macOS blocks the first launch, try opening the app once, then follow
[Apple's first-open procedure](https://support.apple.com/en-us/102445) in System
Settings, Privacy & Security, using Open Anyway for this app. The installer
does not disable Gatekeeper. The current flat app icon remains in this version;
desktop icon variants are backlog B24.

## Platforms and verified scope

| Target | Current evidence |
| --- | --- |
| macOS Apple Silicon, `darwin/arm64` | The actual ad-hoc signed archive installed into a path containing spaces on macOS 26.6.2. LaunchServices opened the bundle and initialized SQLite. Reinstallation preserved the profile and signature. Built with SDK 26.5 and Go 1.27.3. |
| Ubuntu 24.04 x86_64, `linux/amd64` | The actual amd64 archive installed and displayed the Oiê interface in a fresh Ubuntu 24.04 runtime container under emulation, without Go, a compiler or development headers. Ctrl+Q exited cleanly. Reinstallation preserved SQLite identity and retained files. |

These are test environments, not a promised minimum OS version. Other Linux
distributions need matching GTK 4, WebKitGTK 6 and libsoup 3 libraries, plus a
compatible C runtime. A compiled Linux executable is not a universal binary
for every distribution. Windows, Intel Macs, 32-bit Linux and other Linux
architectures are outside the initial Oiê validation matrix.

Check the machine and Go target before building from source:

```sh
uname -m
go version
go env GOOS GOARCH
```

Apple Silicon commonly reports `arm64`; Linux x86_64 reports `x86_64`, while
Go calls that architecture `amd64`. Build on the target OS with its native
toolchain. Setting `GOOS` or `GOARCH` alone is insufficient for this CGO build.

## Build dependencies versus runtime dependencies

| Dependency | Needed to compile/install from source | Needed to run an already built executable |
| --- | --- | --- |
| Go 1.27.3 or newer | Yes, as declared in `go.mod`. Install from the [official Go instructions](https://go.dev/doc/install). | No. |
| Git | Yes for checkout installation and the direct GitHub download used below. | No. |
| C compiler and native SDK/headers | Yes. CGO must be enabled. | No compiler or headers required; system runtime libraries remain necessary. |
| Xcode Command Line Tools | Yes on macOS. | No. Cocoa and WebKit come with macOS. |
| `pkg-config` | Yes on Linux. | No. |
| GTK 4, WebKitGTK 6 and libsoup 3 | Development packages on Linux. | Shared runtime libraries on Linux. APT installs their transitive dependencies. |
| System CA trust | Yes for HTTPS source/module downloads. | Yes for trusted TLS connections. Ubuntu provides `ca-certificates`; macOS supplies system trust. |
| FFmpeg | Not needed to build. | Optional, for locally generated video thumbnails. |

Source installation does not require the Wails CLI, Node/npm, Docker, Dagger,
code-generation tools or a separate SQLite installation. Generated files and
frontend assets are committed; the application includes its SQLite driver.
Make is needed only when choosing a `make` target. Docker and Node are used by
development/test workflows, documented in [local CI](local-ci.md).

## Build from source on Ubuntu 24.04 x86_64

Install Go 1.27.3 or newer first, then install the native build dependencies:

```sh
sudo apt update
sudo apt install git ca-certificates build-essential pkg-config \
  libgtk-4-dev libwebkitgtk-6.0-dev libsoup-3.0-dev
```

Confirm that the development packages are discoverable:

```sh
pkg-config --modversion gtk4 webkitgtk-6.0 libsoup-3.0 gio-unix-2.0
```

This must print versions for each library. A missing `.pc` file generally means
the development package is absent; installing only the runtime package does
not supply that file. The [Ubuntu WebKit development package](https://packages.ubuntu.com/noble/libwebkitgtk-6.0-dev)
also depends on libsoup development files; the command above lists both
explicitly to match the errors observed during installation.

Install for the current user:

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" GOFLAGS= GOWORK=off CGO_ENABLED=1 \
  GONOPROXY=github.com/araihu/oie \
  go install -tags production github.com/araihu/oie/cmd/oie-desktop@v0.0.1-rc.0
```

Do not run `go install` with `sudo`. The application and its data belong to
the current user. The commands above leave dependency installation under the
user's control; Oiê does not invoke APT or install system packages itself.

### Linux runtime libraries

On a separate Ubuntu 24.04 machine receiving a compatible prebuilt executable,
the main runtime packages are:

```sh
sudo apt update
sudo apt install ca-certificates libgtk-4-1 libwebkitgtk-6.0-4 libsoup-3.0-0
```

If you installed the development packages, their runtime dependencies are
already present. WebKit's [runtime package dependencies](https://packages.ubuntu.com/noble/libwebkitgtk-6.0-4)
include JavaScriptCore and libsoup; GTK/GLib and related dependencies are
resolved by APT.

Run inside a working graphical desktop session with its display and session
D-Bus. A bare SSH shell or headless container is not a desktop session. Xvfb,
xdotool, scrot and `dbus-x11` are CI harness tools, not additional dependencies
that every desktop user must install. Runtime video format support depends on
the platform WebView and available codecs.

## Build from source on macOS Apple Silicon

Run these commands in Terminal on the Mac, rather than inside a Linux VM.
Install Go 1.27.3 or newer and the Xcode Command Line Tools. If the command-line
tools are not installed:

```sh
xcode-select --install
```

Check the active tools:

```sh
xcode-select --print-path
xcrun --find clang
go version
```

Install for the current user with matching compilation and linker targets:

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" GOFLAGS= GOWORK=off CGO_ENABLED=1 \
  GONOPROXY=github.com/araihu/oie \
  MACOSX_DEPLOYMENT_TARGET=13.0 \
  CGO_CFLAGS="-O2 -g -mmacosx-version-min=13.0" \
  CGO_LDFLAGS="-O2 -g -mmacosx-version-min=13.0" \
  go install -tags production -ldflags='-extldflags=-mmacosx-version-min=13.0' \
  github.com/araihu/oie/cmd/oie-desktop@v0.0.1-rc.0
```

These flags avoid the observed mismatch between macOS compilation targets and
the final link target. They are not evidence that Oiê runs on macOS 13; the
native runtime was tested on macOS 26.6.2. Keep any required custom compiler
flags when adapting this command to a separately configured toolchain.

The executable uses macOS Cocoa/WebKit frameworks. Running it does not require
Go, Xcode, GTK or a separately installed WebKit runtime. This source command
produces an executable, rather than the release's Finder/Applications bundle.

## Launch a source installation

Both installation commands above select the same explicit output directory:

```sh
"$HOME/.local/bin/oie-desktop"
```

To launch by name in the current shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
oie-desktop
```

To retain that PATH change, add it to the appropriate shell startup file, such
as `~/.zshrc` or `~/.profile`. If you omitted `GOBIN` when installing, inspect
`go env GOBIN GOPATH`: a nonempty `GOBIN` selects the install directory;
otherwise it is normally `$(go env GOPATH)/bin`.

Closing the window hides it and keeps the engine running. Use Quit or
`Cmd/Ctrl+Q` to stop the app. Reopening the same profile shows the existing
instance. The desktop profile is separate from the browser live preview.

## Install from a checkout instead

For development, or when editing the source:

```sh
git clone --single-branch --branch v0.0.1-rc.0 https://github.com/araihu/oie.git
cd oie
```

Use the installation command for your platform above, replacing its final
`github.com/araihu/oie/cmd/oie-desktop@v0.0.1-rc.0` argument with
`./cmd/oie-desktop`. Keep the macOS compilation/linker flags. A checkout install
does not need the `GONOPROXY` exception for Oiê itself.

The remote commit installation and fresh-checkout installation were both
verified on the Mac. Build and integration details remain in
[the desktop host documentation](desktop.md).

## Optional FFmpeg

Without FFmpeg, the application starts and messaging works, but it cannot
generate video thumbnails locally. Video playback uses the WebView and does
not require the FFmpeg executable.

On Ubuntu:

```sh
sudo apt install ffmpeg
```

On macOS, install FFmpeg separately using your preferred distributor. Check
that the app can find it in the launching environment:

```sh
command -v ffmpeg
ffmpeg -version
```

Alternatively, supply its absolute path, which also avoids a different PATH
when an app is launched outside Terminal:

```sh
"$HOME/.local/bin/oie-desktop" --ffmpeg /absolute/path/to/ffmpeg
```

## Data, updates and removal

| Platform | Default profile directory |
| --- | --- |
| macOS | `~/Library/Application Support/Oiê` |
| Linux | `$XDG_DATA_HOME/oie` when `XDG_DATA_HOME` is absolute; otherwise `~/.local/share/oie` |

The profile contains the engine's database, credentials and retained media.
It stays separate from the executable. `--data-dir /absolute/path/to/profile`
selects another profile. Do not copy a running database to move a profile;
quit first. For an existing hosted engine, see `--engine-url` in
[desktop runtime options](desktop.md#runtime-options); this is not device sync.

Updates are manual. Quit the app, choose a published version and run the saved
installer again with the same prefix. It stages replacements and restores the
previous installation on a detected update failure. Source installations can
repeat their `go install` command with the desired tag. Repeating the same
version reinstalls it; it does not select newer application code. Both methods
preserve the separate profile.

To uninstall a default Linux release installation, quit the app and remove only
these application files:

```sh
rm -f "$HOME/.local/bin/oie-desktop" \
  "$HOME/.local/share/applications/org.araihu.oie.desktop" \
  "$HOME/.local/share/icons/hicolor/512x512/apps/org.araihu.oie.png"
rm -rf "$HOME/.local/share/doc/oie"
```

For the default macOS bundle, quit and remove `~/Applications/Oie.app` in Finder.
A source installation instead has only the executable at
`~/.local/bin/oie-desktop`. If you selected a custom prefix, remove those same
application paths under that prefix. Keep the profile containing history,
credentials, media and preferences unless you explicitly choose to delete it.

Homebrew, MacPorts, APT and other Oiê package-manager distributions remain
backlog B22. Automatic updates are B23; the shell installer updates only when
you run it.

## Installation issues observed on 2026-11-10

| Symptom | Cause and resolution | Verification |
| --- | --- | --- |
| `invalid version: disallowed version string` with `@codex/wails-integration` | The branch name contains a slash that Go rejects as a version query. Use the published commit hash, or clone the branch with Git and install the local package. | Reproduced; hash resolution and installation verified. |
| `loading deprecation ... no matching versions for query "latest"` | Go queries latest-module metadata even for a requested hash. Remote `main` initially contained only the specification, without `go.mod`. The already integrated main was published at `115e8eb`; the proxy continued returning the earlier missing-version result. The inline `GONOPROXY=github.com/araihu/oie` setting fetches only Oiê directly from GitHub, keeps checksum verification and leaves persisted Go settings unchanged. | Full remote installation passed on macOS. On the user's Linux machine, module downloads then succeeded and compilation reached the missing-library errors below. |
| `gtk4`, `webkitgtk-6.0` or `libsoup-3.0` not found by `pkg-config` | Install the Ubuntu development packages listed above and rerun the version check. Runtime libraries alone are insufficient to compile. Existing downloaded Go modules are reused on retry. | Reproduced locally and reported by the Linux tester. The Ubuntu CI lane builds with development packages; the tester's retry is not yet confirmed. |
| macOS `ld: warning: object file ... was built for newer macOS version ... than being linked` | Native objects and the linker used different deployment targets. Warnings alone did not prevent installation. Use all macOS compilation/linker settings above. | Native build and source installation passed without these warnings; Mach-O metadata reports a 13.0 deployment target. |

If diagnosing another Linux machine, retain `cat /etc/os-release`, `uname -m`,
`go version`, `go env GOOS GOARCH CGO_ENABLED` and the exact error. A distro name
or installation attempt alone does not establish runtime support. Do not
suggest `PKG_CONFIG_PATH` changes before checking whether the required
development package is actually installed.

Plain `go install` requires CGO to be enabled; the examples set it explicitly.
The Makefile uses the `server` tag for headless checks, so native install
commands explicitly clear `GOFLAGS` and use `-tags production`. The pinned
prerelease tag is intentional so the example selects a repeatable version.

For a release, verify a complete remote `go install` on both targets, native
runtime behavior and installation from a clean machine. A successful
`go list -m` only proves module resolution. See
[desktop verification](desktop.md#verification) for existing native coverage and
its limits. Keep this guide current as installation testing continues.

Platform dependency references are [Wails installation](https://v3.wails.io/getting-started/installation/),
the Ubuntu package pages linked above and the
[Go module environment reference](https://go.dev/ref/mod#environment-variables).

## Operating the engine without a web interface

For automation against a loopback data API, build the server and generated CLI:

```sh
make build build-cli
bin/oie -role local -data-only -listen 127.0.0.1:18090 -data-dir ./data-cli
bin/oiectl --server http://127.0.0.1:18090 --output json system get-engine
bin/oiectl --server http://127.0.0.1:18090 --output json system check-health
```

Run the two CLI commands in another terminal while the engine is running.
`-data-only` is supported by `local` and `hosted`; the default still serves the
web interface. The CLI's API base URL can also come from `OIECTL_SERVER`.
Credentials belong in `--body-stdin` or a private `--body-file`, not shell
arguments. Use `--output raw` for binary operations and `events stream`.
Streams are incremental; bound them with `--timeout` and `--max-output-bytes`.
Transport failure, timeout or cancellation returns exit 3, including after
partial output. Keep only fully consumed/applied SSE IDs and explicitly resume
with `--last-event-id`; the CLI does not retry or reconnect automatically.
See [the engine API policy](engine-api-policy.md) and
[CLI verification](engine-api-cli-verification.md) for scope and fixture commands.

### oiectl commands

Append `--help` to a group or command to see its options. Run
`oiectl accounts avatars --help` to see both avatar commands.
Commands use `get` for a single result, `list` for a collection,
`download` for bytes and `request` to start work on the engine.
Update scripts that use earlier command names to the names below.

| Group | Commands |
| --- | --- |
| `accounts` | `connect`, `disconnect`, `get`, `get-capabilities`, `get-connection`, `get-history-status`, `list`, `list-contacts`, `register` |
| `accounts avatars` | `download`, `list` |
| `conversations` | `get`, `get-details`, `get-read-state`, `get-room-session`, `join-room`, `leave-room`, `list`, `list-mention-candidates`, `mark-read`, `open`, `set-preferences` |
| `events` | `stream` |
| `messages` | `cancel-send`, `edit`, `get`, `get-context`, `get-edit-command`, `get-edit-target`, `get-latest-edit-target`, `get-reaction-command`, `get-reactions`, `get-sender-profile`, `get-star`, `list`, `list-reactions`, `list-revisions`, `list-star-states`, `list-starred`, `search`, `send`, `set-reactions`, `set-star` |
| `operations` | `get` |
| `previews` | `download-image`, `get`, `get-thumbnail`, `list`, `refresh`, `request-image`, `request-thumbnail` |
| `shared-content` | `download`, `list` |
| `system` | `check-health`, `get-engine` |

List avatar metadata by account and use the returned `imageHash` to download
the PNG.

```sh
bin/oiectl accounts avatars list --account-id ACCOUNT_UUID --output json
bin/oiectl accounts avatars download --hash IMAGE_HASH --output raw > avatar.png
```

`messages list-star-states` checks the star state of selected message IDs.
`messages list-starred` returns the starred messages themselves.

Use `--output raw` for `events stream`, `accounts avatars download`,
`previews download-image` and `shared-content download`. Use
`previews request-image` to request retention of an original image on
the engine. The response contains the download state. The CLI also
provides `help` and `completion`.
