#!/bin/sh
# Native WebView validation, separate from the headless server/browser checks.
set -eu
mkdir -p tests/desktop/artifacts bin
case "$(uname -s)" in
  Linux)
    command -v xvfb-run >/dev/null
    command -v dbus-run-session >/dev/null
    command -v xdotool >/dev/null
    ;;
  Darwin) ;;
  *) echo 'Native desktop smoke requires Linux or macOS.' >&2; exit 1 ;;
esac
command -v python3 >/dev/null
# Upstream MCP is compiled only into this disposable test executable.
GOFLAGS= CGO_ENABLED=1 go build -tags mcp -o bin/oie-desktop-smoke ./cmd/oie-desktop
export OIE_DESKTOP_BINARY="$PWD/bin/oie-desktop-smoke"
case "$(uname -s)" in
  Linux) export OIE_DESKTOP_PRIVATE_DISPLAY=1; exec dbus-run-session -- xvfb-run -a -s '-screen 0 1440x1000x24' python3 tests/desktop/smoke.py ;;
  Darwin) exec python3 tests/desktop/smoke.py ;;
esac
