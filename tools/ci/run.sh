#!/bin/sh
set -eu
umask 077
mode=${1:-all}
case "$mode" in all|checks) ;; *) echo 'usage: tools/ci/run.sh [all|checks]' >&2; exit 2;; esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
ci_artifacts=${OIE_CI_ARTIFACTS:-"$root/tests/browser/artifacts/ci"}
case "$ci_artifacts" in /*) ;; *) ci_artifacts="$root/$ci_artifacts" ;; esac
python3 -m unittest discover -s "$root/tools/ci" -p '*_test.py'
task_tmp=$(mktemp -d)
ssh_pid=
runner_pid=
watchdog_pid=
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ -n "$runner_pid" ]; then
    kill -TERM "$runner_pid" 2>/dev/null || true
    # One Python process owns the delay, so stopping it cannot orphan a sleep.
    python3 - "$runner_pid" "${OIE_CI_CLEANUP_GRACE_SECONDS:-15}" <<'PY_WATCHDOG' &
import math
import os
import signal
import sys
import time

pid = int(sys.argv[1])
try:
    grace = float(sys.argv[2])
    if not math.isfinite(grace) or grace < 0:
        raise ValueError('invalid grace')
except ValueError:
    grace = 15
try:
    handle = os.pidfd_open(pid) if hasattr(os, 'pidfd_open') else None
except ProcessLookupError:
    sys.exit(0)
time.sleep(grace)
try:
    if handle is not None:
        signal.pidfd_send_signal(handle, signal.SIGKILL)
    else:
        os.kill(pid, signal.SIGKILL)
except ProcessLookupError:
    pass
PY_WATCHDOG
    watchdog_pid=$!
    wait "$runner_pid" 2>/dev/null || true
    kill -TERM "$watchdog_pid" 2>/dev/null || true
    wait "$watchdog_pid" 2>/dev/null || true
    watchdog_pid=
  fi
  if [ -n "$ssh_pid" ]; then kill "$ssh_pid" 2>/dev/null || true; wait "$ssh_pid" 2>/dev/null || true; fi
  rm -rf "$task_tmp"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
GOWORK=off go -C "$root/tools/ci" test -tags=http2legacy ./...
GOWORK=off go -C "$root/tools/ci" build -tags=http2legacy -o "$task_tmp/oie-ci" .
python3 "$root/tools/ci/materialize.py" "$root" "$task_tmp/source"
# Optional independent Unix-socket forwarding avoids disrupting shared tunnels.
if [ -n "${OIE_CI_SSH_HOST:-}" ]; then
  ssh -NT -o BatchMode=yes -o ExitOnForwardFailure=yes -o ControlMaster=no -o ControlPath=none \
    -o StreamLocalBindUnlink=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
    -L "$task_tmp/engine.sock:${OIE_CI_SSH_SOCKET:-/run/dagger/engine.sock}" "$OIE_CI_SSH_HOST" > "$task_tmp/ssh.log" 2>&1 &
  ssh_pid=$!
  attempt=0
  while [ ! -S "$task_tmp/engine.sock" ]; do
    if ! kill -0 "$ssh_pid" 2>/dev/null || [ "$attempt" -ge 100 ]; then cat "$task_tmp/ssh.log" >&2; exit 1; fi
    attempt=$((attempt+1)); sleep .1
  done
  export _EXPERIMENTAL_DAGGER_RUNNER_HOST="unix://$task_tmp/engine.sock"
fi
cd "$task_tmp/source"
export OIE_CI_MODE="$mode" OIE_CI_RUN_ID="$(date -u +%Y%m%dT%H%M%S)-$$"
export OIE_CI_ARTIFACTS="$ci_artifacts" DAGGER_NO_NAG=1
"$task_tmp/oie-ci" -mode "$mode" -source "$task_tmp/source" \
  -artifacts "$OIE_CI_ARTIFACTS" -timeout "${OIE_CI_TIMEOUT:-30m}" &
runner_pid=$!
result=0
wait "$runner_pid" || result=$?
runner_pid=
exit "$result"
