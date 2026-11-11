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
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/branding.cjs
npm --prefix tests/browser test
npm --prefix tests/browser run test:chat
npm --prefix tests/browser run test:states
npm --prefix tests/browser run test:accounts
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/live-chat.cjs
OIE_WEB_BINARY="$PWD/bin/oie" OIE_SEND_GEOMETRY_ONLY=1 node tests/browser/live-chat.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/account-status.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/avatars.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/sender-info.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/previews.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/markdown.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/stars.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/replies.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/capabilities.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/account-drawer.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/conversation-details.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/shared-content.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/shared-actions.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/settings-entry.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/reset-data.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/appearance-settings.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/language-settings.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/shared-content-scroll.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/link-fallbacks.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/video.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/video-thumbnails.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/documents.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/shared-search.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/message-search.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/chat-info.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/fragment-navigation.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/timeline-scroll.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/read-state.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/conversation-navigation.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/conversation-preferences.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/reply-compose.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/emoji-picker.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/mentions.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/corrections.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/message-history.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/sent-corrections.cjs
OIE_WEB_BINARY="$PWD/bin/oie" node tests/browser/reactions.cjs
sh scripts/test-live-browser.sh
