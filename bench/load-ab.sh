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
# Keep measurement code identical; production and build profiles stay at REV.
rm -rf "$tmp/old/internal/loadtest"
mkdir -p "$tmp/old/internal"
cp -R internal/loadtest "$tmp/old/internal/loadtest"
build "$tmp/old" "$tmp/a.test"
build . "$tmp/b.test"
run() { # label binary settings...
	label=$1 binary=$2
	shift 2
	if env PTYD_LOAD_OUT="$out" PTYD_LOAD_LABEL="$label" "$@" "$binary" -test.run '^TestLoad$' -test.timeout 10m >"$tmp/run.log" 2>&1; then
		return
	else
		status=$?
		cat "$tmp/run.log" >&2
		echo "run failed: $label" >&2
		return "$status"
	fi
}
for i in $(seq "$rounds"); do
	for v in a b; do
		run "$v:agents" "$tmp/$v.test" PTYD_LOAD_N=32 PTYD_LOAD_RATE=1048576
		run "$v:flat-out" "$tmp/$v.test" PTYD_LOAD_N=8 PTYD_LOAD_RATE=0
	done
done
printf '%-12s %6s %7s %8s %8s %7s %4s\n' label cores MB/s echo50ms echo99ms RSSMiB GC
sed -n 's/.*"Label":"\([^"]*\)".*"EchoP50Ms":\([^,]*\),"EchoP99Ms":\([^,]*\),.*"OutputMBps":\([^,]*\),.*"CPUCores":\([^,]*\),"PeakRSSMiB":\([^,]*\),.*"GCCycles":\([^,]*\),.*/\1 \5 \4 \2 \3 \6 \7/p' "$out" |
	sort -s -k1,1 | awk '{printf "%-12s %6.2f %7.1f %8.3f %8.3f %7.1f %4d\n", $1, $2, $3, $4, $5, $6, $7}'
