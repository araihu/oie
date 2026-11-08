#!/bin/sh
set -eu
umask 077
mode=${1:-all}
case "$mode" in all|checks) ;; *) echo 'usage: tools/ci/run.sh [all|checks]' >&2; exit 2;; esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
python3 -m unittest discover -s "$root/tools/ci" -p '*_test.py'
task_tmp=$(mktemp -d)
ssh_pid=
cleanup() {
  result=$?
  trap - EXIT HUP INT TERM
  if [ -n "$ssh_pid" ]; then kill "$ssh_pid" 2>/dev/null || true; wait "$ssh_pid" 2>/dev/null || true; fi
  rm -rf "$task_tmp"
  exit "$result"
}
trap cleanup EXIT HUP INT TERM
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
export OIE_CI_ARTIFACTS="$root/tests/browser/artifacts/ci" DAGGER_NO_NAG=1
if [ "$mode" = checks ]; then
  dagger --silent shell --no-mod tools/ci/checks.dag
else
  dagger --silent shell --no-mod tools/ci/pipeline.dag
fi
