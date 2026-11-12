#!/bin/sh
set -eu
export OIE_WEB_BINARY="$PWD/bin/oie"

run() {
  label=$1
  shift
  started=$(date +%s)
  printf '[browser] %s started\n' "$label"
  result=0
  "$@" || result=$?
  printf '[browser] %s finished in %ss (exit %s)\n' "$label" "$(( $(date +%s) - started ))" "$result"
  return "$result"
}

run_case() {
  run "$1" node "tests/browser/$1.cjs"
}

# The two groups have similar elapsed times in the Hostinger evidence.
consumer() (
  mock_pid=
  web_pid=
  cleanup() {
    result=$?
    if [ "$result" -ne 0 ]; then cat /tmp/oie-mock.log /tmp/oie-web.log 2>/dev/null || true; fi
    [ -z "$web_pid" ] || kill "$web_pid" 2>/dev/null || true
    [ -z "$mock_pid" ] || kill "$mock_pid" 2>/dev/null || true
    [ -z "$web_pid" ] || wait "$web_pid" 2>/dev/null || true
    [ -z "$mock_pid" ] || wait "$mock_pid" 2>/dev/null || true
  }
  trap cleanup EXIT
  trap 'exit 129' HUP
  trap 'exit 130' INT
  trap 'exit 143' TERM
  bin/oie-mock > /tmp/oie-mock.log 2>&1 &
  mock_pid=$!
  bin/oie --role bff --listen 127.0.0.1:8080 --engine-url http://127.0.0.1:18082 > /tmp/oie-web.log 2>&1 &
  web_pid=$!
  node -e 'let left=100;async function ready(){try{if((await fetch("http://127.0.0.1:8080/")).ok)return}catch{}if(--left===0)process.exit(1);setTimeout(ready,100)}ready()'
  run_case branding
  run test npm --prefix tests/browser test
  run test:chat npm --prefix tests/browser run test:chat
  run test:states npm --prefix tests/browser run test:states
  run test:accounts npm --prefix tests/browser run test:accounts
  run_case live-chat
  run send-geometry env OIE_SEND_GEOMETRY_ONLY=1 node tests/browser/live-chat.cjs
  run_case account-status
  run_case avatars
  run_case sender-info
  run_case previews
  run_case markdown
  run_case stars
  run_case replies
  run_case capabilities
  run_case account-drawer
  run_case conversation-details
  run_case room-entry
  run_case shared-content
  run_case presentation-time
)

integration() {
  run_case shared-actions
  run_case settings-entry
  run_case reset-data
  run_case appearance-settings
  run_case language-settings
  run_case shared-content-scroll
  run_case link-fallbacks
  run_case video
  run_case video-thumbnails
  run_case documents
  run_case shared-search
  run_case message-search
  run_case chat-info
  run_case fragment-navigation
  run_case timeline-scroll
  run_case read-state
  run_case conversation-navigation
  run_case conversation-preferences
  run_case reply-compose
  run_case emoji-picker
  run_case mentions
  run_case corrections
  run_case message-history
  run_case sent-corrections
  run_case reactions
  run real-engine sh scripts/test-live-browser.sh
}

case "${1:-all}" in
  consumer) consumer ;;
  integration) integration ;;
  all) consumer; integration ;;
  *) printf 'usage: tools/ci/browser.sh [all|consumer|integration]\n' >&2; exit 2 ;;
esac
