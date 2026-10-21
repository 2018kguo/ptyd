#!/bin/sh
# A/B the load test: build it at git revision REV and in this tree (each
# with its own cmd/ptyd/default.pgo, as go build would), then run the
# agents and flat-out scenarios of bench/load.sh alternately ROUNDS times
# (default 5), so a busy host affects both sides alike. Appends one JSON
# line per run to OUT (default bench/load-ab.jsonl), labelled a:<scenario>
# for REV and b:<scenario> for this tree, and prints a row per run.
#
#   bench/load-ab.sh HEAD~3
#   ROUNDS=8 OUT=/tmp/ab.jsonl bench/load-ab.sh main
set -eu
cd "$(dirname "$0")/.."
rev=$1 rounds=${ROUNDS:-5}
out=$(cd "$(dirname "${OUT:-bench/load-ab.jsonl}")" && pwd)/$(basename "${OUT:-bench/load-ab.jsonl}")
tmp=$(mktemp -d)
trap 'git worktree remove --force "$tmp/old" >/dev/null 2>&1; rm -rf "$tmp"' EXIT
build() { # dir binary
	pgo=off
	[ ! -f "$1/cmd/ptyd/default.pgo" ] || pgo=$(cd "$1" && pwd)/cmd/ptyd/default.pgo
	(cd "$1" && go test -tags loadtest -c -pgo="$pgo" -o "$2" ./internal/loadtest)
}
git worktree add -q --detach "$tmp/old" "$rev"
build "$tmp/old" "$tmp/a.test"
build . "$tmp/b.test"
for i in $(seq "$rounds"); do
	for v in a b; do
		env PTYD_LOAD_OUT="$out" PTYD_LOAD_LABEL=$v:agents PTYD_LOAD_N=32 PTYD_LOAD_RATE=1048576 \
			"$tmp/$v.test" -test.run Load -test.timeout 10m >/dev/null 2>&1 || echo "run failed: $v agents" >&2
		env PTYD_LOAD_OUT="$out" PTYD_LOAD_LABEL=$v:flat-out PTYD_LOAD_N=8 PTYD_LOAD_RATE=0 \
			"$tmp/$v.test" -test.run Load -test.timeout 10m >/dev/null 2>&1 || echo "run failed: $v flat-out" >&2
	done
done
printf '%-12s %6s %7s %8s %8s %7s %4s\n' label cores MB/s echo50ms echo99ms RSSMiB GC
sed -n 's/.*"Label":"\([^"]*\)".*"EchoP50Ms":\([^,]*\),"EchoP99Ms":\([^,]*\),.*"OutputMBps":\([^,]*\),.*"CPUCores":\([^,]*\),"PeakRSSMiB":\([^,]*\),.*"GCCycles":\([^,]*\),.*/\1 \5 \4 \2 \3 \6 \7/p' "$out" |
	sort -s -k1,1 | awk '{printf "%-12s %6.2f %7.1f %8.3f %8.3f %7.1f %4d\n", $1, $2, $3, $4, $5, $6, $7}'
