#!/bin/sh
# Refresh cmd/ptyd/default.pgo, the profile `go build` and `go install`
# use for profile-guided optimization: CPU profiles of the load test's
# 32 agents and 8 flat-out scenarios, merged with any profiles given as
# arguments (such as the same two from the other OS, so the profile
# covers both macOS and Linux):
#
#   bench/pgo.sh                              # this host only
#   OUT=/tmp/lin.pgo bench/pgo.sh             # write elsewhere (e.g. in a Linux container)
#   bench/pgo.sh /tmp/lin.pgo                 # merge with it
set -eu
cd "$(dirname "$0")/.."
out=${OUT:-cmd/ptyd/default.pgo}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go test -tags loadtest -c -pgo=off -o "$tmp/load.test" ./internal/loadtest
PTYD_LOAD_PROFILE=$tmp/agents "$tmp/load.test" -test.run Load -test.timeout 10m >/dev/null 2>&1
PTYD_LOAD_PROFILE=$tmp/flat PTYD_LOAD_N=8 PTYD_LOAD_RATE=0 "$tmp/load.test" -test.run Load -test.timeout 10m >/dev/null 2>&1
go tool pprof -proto "$tmp/agents/cpu.pprof" "$tmp/flat/cpu.pprof" "$@" >"$tmp/merged.pgo"
mv "$tmp/merged.pgo" "$out"
