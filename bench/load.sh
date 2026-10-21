#!/bin/sh
# Run the load-test scenarios and append one JSON line per scenario to
# $1 (default bench/load.jsonl). See internal/loadtest for the settings.
set -eu
cd "$(dirname "$0")/.."
out=$(cd "$(dirname "${1:-bench/load.jsonl}")" && pwd)/$(basename "${1:-bench/load.jsonl}")
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
# Built with the daemon's PGO profile, as go build builds the daemon.
go test -tags loadtest -c -pgo="$PWD/cmd/ptyd/default.pgo" -o "$bin/load.test" ./internal/loadtest
run() {
	env PTYD_LOAD_OUT="$out" "$@" "$bin/load.test" -test.run Load -test.timeout 10m >/dev/null 2>&1 ||
		echo "scenario failed: $*" >&2
}
# 32 agents printing 1 MiB/s each, each attached and streamed.
run PTYD_LOAD_LABEL=agents PTYD_LOAD_N=32 PTYD_LOAD_RATE=1048576
# 8 terminals printing as fast as the daemon takes it.
run PTYD_LOAD_LABEL=flat-out PTYD_LOAD_N=8 PTYD_LOAD_RATE=0
# 64 agents printing 64 KiB/s steadily, no clients: disk behavior.
run PTYD_LOAD_LABEL=steady-disk PTYD_LOAD_N=64 PTYD_LOAD_RATE=65536 PTYD_LOAD_ATTACH=0 PTYD_LOAD_STREAMS=0 PTYD_LOAD_SECONDS=40
# The same output in 2s bursts with 2s of quiet between.
run PTYD_LOAD_LABEL=bursty-disk PTYD_LOAD_N=64 PTYD_LOAD_RATE=65536 PTYD_LOAD_BURST=2 PTYD_LOAD_ATTACH=0 PTYD_LOAD_STREAMS=0 PTYD_LOAD_SECONDS=40
