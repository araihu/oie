# Native desktop checks

Run `sh tools/ci/desktop.sh` on macOS or Linux with GTK4, WebKitGTK6,
Xvfb, xdotool and a session D-Bus. `make ci-desktop` runs the Linux amd64 lane through
Dagger on Ubuntu 24.04. The existing headless checks remain separate.

The script builds a disposable binary with upstream Wails' `mcp` build tag.
Release binaries use `production` and contain no MCP listener. The driver uses
Python's standard library. It starts a fake loopback engine, then separately
starts a real embedded engine with a fresh temporary data directory. It never
connects a provider account or reads an existing Oie data directory.

Checks cover native WebView asset loading, BFF CSRF, HTMX navigation,
EventSource, media byte ranges, original download bytes/headers, language and
theme persistence, draft persistence, closing/reopening, single instance,
explicit Quit, and retained embedded SQLite data. Logs and the JSON report are
written under the ignored `tests/desktop/artifacts/` directory.

Linux also drives the real GTK Save chooser on a private Xvfb display, checks
the saved bytes, and cancels another chooser without fetching or writing a file.
macOS file chooser completion remains a manual check. Checking download bytes
and attachment headers alone does not establish that the native chooser saves
a file. Keep that distinction in validation reports.
