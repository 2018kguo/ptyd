#!/usr/bin/env bash
# Sourced by demo.tape. All terminals, state and binaries are disposable;
# the recording uses local programs and makes no model-provider calls.
ptyd_demo_root=$(mktemp -d "${TMPDIR:-/tmp}/ptyd-demo.XXXXXX")
export PATH="$ptyd_demo_root:$PATH"
export XDG_STATE_HOME="$ptyd_demo_root/state"
export PTYD_TOKEN=local-demo
unset PTYD_ADDR NO_COLOR CLICOLOR CLICOLOR_FORCE FORCE_COLOR
export TERM=xterm-256color COLORTERM=truecolor

go build -o "$ptyd_demo_root/ptyd" ./cmd/ptyd || { rm -rf -- "$ptyd_demo_root"; exit 1; }
ptyd serve --bind 127.0.0.1:0 --persist-dir "$ptyd_demo_root/sessions" >"$ptyd_demo_root/daemon.log" 2>&1 &
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
ptyd new -d --cwd /tmp website >/dev/null
ptyd new -d --cwd /tmp research >/dev/null
ptyd term new -d --group website -n build -- sh -c 'printf "\033[48;2;40;42;54m\033[32m Build complete. 12 tests passed. \033[0m\n"; exec cat' >/dev/null
ptyd term new -d --group website -n logs -- sh -c 'printf "\033[36mService ready. Listening for requests.\033[0m\n"; i=0; while [ "$i" -lt 30 ]; do printf "\rRequests served: %s" "$i"; i=$((i+1)); sleep 1; done; exec cat' >/dev/null
ptyd term new -d --group research -n notes -- sh -c 'printf "A persistent research shell is ready.\n"; exec cat' >/dev/null
export PS1='$ '
