#!/bin/sh
# A/B a change: build the benchmarks of each package at git revision REV
# (with this tree's *bench_test.go files, so new benchmarks run on old
# code) and in this tree, run them alternately COUNT times (default 10),
# and print benchstat. OUT, if set, is a directory to keep old.txt and
# new.txt in. Both builds must accept the same benchmark source unchanged.
#
#   bench/ab.sh HEAD ./internal/terminal 'Consume|Attach'
#   OUT=/tmp/ab bench/ab.sh <rev> './internal/terminal ./internal/api'
set -eu
cd "$(dirname "$0")/.."
rev=$1 pkgs=$2 bench=${3:-.} count=${COUNT:-10} benchtime=${BENCHTIME:-1s}
tmp=$(mktemp -d)
trap 'git worktree remove --force "$tmp/old" >/dev/null 2>&1; rm -rf "$tmp"' EXIT
git worktree add -q --detach "$tmp/old" "$rev"
for pkg in $pkgs; do
	for f in "$pkg"/*bench_test.go; do
		cp "$f" "$tmp/old/$f"
	done
	n=$(basename "$pkg")
	(cd "$tmp/old" && go test -c -o "$tmp/old-$n.test" "$pkg")
	go test -c -o "$tmp/new-$n.test" "$pkg"
done
for i in $(seq "$count"); do
	order="old new"
	if [ $((i % 2)) -eq 0 ]; then order="new old"; fi
	for pkg in $pkgs; do
		n=$(basename "$pkg")
		for v in $order; do
			if (cd "$pkg" && "$tmp/$v-$n.test" -test.run '^$' -test.bench "$bench" -test.benchtime "$benchtime" -test.benchmem -test.count 1 -test.timeout 60m) >"$tmp/run.log"; then
				sed '/^\[ptyd\]/d' "$tmp/run.log" >>"$tmp/$v.txt"
			else
				status=$?
				cat "$tmp/run.log" >&2
				echo "benchmark failed: $v $pkg" >&2
				exit "$status"
			fi
		done
	done
done
if [ -n "${OUT:-}" ]; then mkdir -p "$OUT" && cp "$tmp/old.txt" "$tmp/new.txt" "$OUT/"; fi
go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e -col .file old="$tmp/old.txt" new="$tmp/new.txt"
