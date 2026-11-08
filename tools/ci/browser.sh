#!/bin/sh
set -eu
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
trap cleanup EXIT HUP INT TERM
bin/oie-mock > /tmp/oie-mock.log 2>&1 &
mock_pid=$!
bin/oie --role bff --listen 127.0.0.1:8080 --engine-url http://127.0.0.1:18082 > /tmp/oie-web.log 2>&1 &
web_pid=$!
node -e 'let left=100;async function ready(){try{if((await fetch("http://127.0.0.1:8080/")).ok)return}catch{}if(--left===0)process.exit(1);setTimeout(ready,100)}ready()'
npm --prefix tests/browser test
npm --prefix tests/browser run test:chat
npm --prefix tests/browser run test:states
npm --prefix tests/browser run test:accounts
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/live-chat.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/avatars.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/previews.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/markdown.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/stars.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/replies.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/capabilities.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/account-drawer.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/conversation-details.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/shared-content.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/chat-info.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/fragment-navigation.cjs
sh scripts/test-live-browser.sh
