#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
tmp=$(mktemp -d)
api_pid=
web_pid=
cleanup() {
  result=$?
  if [ "$result" -ne 0 ]; then cat "$tmp/engine.log" "$tmp/web.log" 2>/dev/null || true; fi
  [ -z "$web_pid" ] || kill "$web_pid" 2>/dev/null || true
  [ -z "$api_pid" ] || kill "$api_pid" 2>/dev/null || true
  [ -z "$web_pid" ] || wait "$web_pid" 2>/dev/null || true
  [ -z "$api_pid" ] || wait "$api_pid" 2>/dev/null || true
  rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM
cert=${OIE_XMPP_CA_FILE:-bin/xmpp-localhost.crt}
if [ ! -f "$cert" ]; then
  docker compose cp xmpp:/var/lib/prosody/certs/localhost.crt "$tmp/localhost.crt"
  cert="$tmp/localhost.crt"
fi
bin/oie --role hosted --listen 127.0.0.1:18086 --data-dir "$tmp/engine" --xmpp-ca-file "$cert" > "$tmp/engine.log" 2>&1 &
api_pid=$!
bin/oie --role bff --listen 127.0.0.1:18087 --engine-url http://127.0.0.1:18086 > "$tmp/web.log" 2>&1 &
web_pid=$!
node -e '
let remaining = 60;
async function ready() {
  try {
    if ((await fetch("http://127.0.0.1:18087/", {signal: AbortSignal.timeout(1000)})).ok) return;
  } catch {}
  if (--remaining === 0) { console.error("Live browser BFF did not become ready"); process.exit(1); }
  setTimeout(ready, 1000);
}
ready();
'
cd tests/browser
OIE_LIVE_URL=http://127.0.0.1:18087 OIE_ENGINE_URL=http://127.0.0.1:18086 node engine-browser.cjs
