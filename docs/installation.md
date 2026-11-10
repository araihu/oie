# Desktop installation on macOS and Linux

Oiê currently installs from source as the `oie-desktop` executable. The desktop
host uses Wails v3.1.0 and includes the local engine, SQLite store and web
interface. Add an existing XMPP account through the interface after launching.
An external XMPP server is still required for messaging.

The first alpha, v0.0.1, has not been tagged or released. Prebuilt release
archives, a macOS `.app` bundle and the release-download shell installer are
not available yet. The commands below select published development commit
`e4b77dd98a94`; they do not install a released alpha.

## Platforms and verified scope

| Target | Current evidence |
| --- | --- |
| macOS Apple Silicon, `darwin/arm64` | Native installation and WebView checks passed on macOS 26.6.2 with SDK 26.5 and Go 1.27.3. Remote installation by commit also passed. |
| Ubuntu 24.04 x86_64, `linux/amd64` | Native build and WebView checks passed in the amd64 CI lane under emulation. Installation on a separate user's Linux machine reached compilation and reported missing GTK/WebKit development packages; successful installation after installing those packages is not yet confirmed. |

These are test environments, not a promised minimum OS version. Other Linux
distributions need matching GTK 4, WebKitGTK 6 and libsoup 3 libraries, plus a
compatible C runtime. A compiled Linux executable is not a universal binary
for every distribution. Windows, Intel Macs, 32-bit Linux and other Linux
architectures are outside the initial Oiê validation matrix.

Check the machine and Go target before building:

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

## Ubuntu 24.04 x86_64

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
  go install -tags production github.com/araihu/oie/cmd/oie-desktop@e4b77dd98a94
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

This is a runtime prerequisite list, not an available release-download command.
If you installed the development packages, their runtime dependencies are
already present. WebKit's [runtime package dependencies](https://packages.ubuntu.com/noble/libwebkitgtk-6.0-4)
include JavaScriptCore and libsoup; GTK/GLib and related dependencies are
resolved by APT.

Run inside a working graphical desktop session with its display and session
D-Bus. A bare SSH shell or headless container is not a desktop session. Xvfb,
xdotool, scrot and `dbus-x11` are CI harness tools, not additional dependencies
that every desktop user must install. Runtime video format support depends on
the platform WebView and available codecs.

## macOS Apple Silicon

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
  github.com/araihu/oie/cmd/oie-desktop@e4b77dd98a94
```

These flags avoid the observed mismatch between macOS compilation targets and
the final link target. They are not evidence that Oiê runs on macOS 13; the
native runtime was tested on macOS 26.6.2. Keep any required custom compiler
flags when adapting this command to a separately configured toolchain.

The executable uses macOS Cocoa/WebKit frameworks. Running it does not require
Go, Xcode, GTK or a separately installed WebKit runtime. It is currently a
command-line-installed executable, not a Finder/Applications `.app` package.
The first alpha is planned without Developer ID signing or notarization.

## Launch and executable location

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
git clone --single-branch --branch codex/wails-integration https://github.com/araihu/oie.git
cd oie
```

Use the installation command for your platform above, replacing its final
`github.com/araihu/oie/cmd/oie-desktop@e4b77dd98a94` argument with
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

Updates are manual. Quit the app, choose the intended published commit or
release, then repeat the appropriate installation command. Repeating a command
pinned to the same hash reinstalls that snapshot; it does not fetch newer
application code. Replacing the executable preserves the separate profile.

To remove this per-user executable, quit the app and remove only
`~/.local/bin/oie-desktop`. Keep the profile unless you explicitly intend to
delete its history, credentials and files. There is no automatic updater or
release installer yet. Homebrew, MacPorts, APT and other Oiê package-manager
distributions remain backlog B22; automatic updates are B23 and desktop icon
variants are B24.

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
desktop commit is intentional: do not substitute `@latest` while the desktop
command remains outside remote main.

For a release, verify a complete remote `go install` on both targets, native
runtime behavior and installation from a clean machine. A successful
`go list -m` only proves module resolution. See
[desktop verification](desktop.md#verification) for existing native coverage and
its limits. Keep this guide current as installation testing continues.

Platform dependency references are [Wails installation](https://v3.wails.io/getting-started/installation/),
the Ubuntu package pages linked above and the
[Go module environment reference](https://go.dev/ref/mod#environment-variables).
