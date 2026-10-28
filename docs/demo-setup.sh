#!/usr/bin/env bash
# Sourced by demo.tape. All terminals, state and binaries are disposable;
# the recording uses local programs and makes no model-provider calls.
ptyd_demo_root=$(mktemp -d "${TMPDIR:-/tmp}/ptyd-demo.XXXXXX")
export PATH="$ptyd_demo_root:$PATH"
export XDG_STATE_HOME="$ptyd_demo_root/state"
export PTYD_TOKEN=local-demo
unset PTYD_ADDR

go build -o "$ptyd_demo_root/ptyd" ./cmd/ptyd || { rm -rf -- "$ptyd_demo_root"; exit 1; }
ptyd --bind 127.0.0.1:0 --persist-dir "$ptyd_demo_root/sessions" >"$ptyd_demo_root/daemon.log" 2>&1 &
ptyd_demo_pid=$!
ptyd_demo_cleanup() {
  # Only this still-running shell job can own the recorded PID.
  if jobs -pr | awk -v pid="$ptyd_demo_pid" '$1 == pid {found=1} END {exit !found}'; then
    kill "$ptyd_demo_pid" 2>/dev/null || true
  fi
  wait "$ptyd_demo_pid" 2>/dev/null || true
  rm -rf -- "$ptyd_demo_root"
}
trap ptyd_demo_cleanup EXIT
for ptyd_demo_attempt in {1..100}; do
  ptyd_demo_line=$(cat "$ptyd_demo_root/daemon.log")
  if [[ "$ptyd_demo_line" =~ listening[[:space:]]on[[:space:]](127\.0\.0\.1:[0-9]+) ]]; then
    export PTYD_ADDR="${BASH_REMATCH[1]}"
    break
  fi
  sleep 0.05
done
if [[ -z "${PTYD_ADDR:-}" ]]; then
  cat "$ptyd_demo_root/daemon.log" >&2
  exit 1
fi
cd /tmp
ptyd new -d -n build -- sh -c 'printf "Build complete. 12 tests passed.\n"; exec cat' >/dev/null
ptyd new -d -n logs -- sh -c 'printf "Service ready. Listening for requests.\n"; exec cat' >/dev/null
ptyd new -d -n shell -- sh -c 'printf "A persistent shell is ready.\n"; exec cat' >/dev/null
export PS1='$ '
