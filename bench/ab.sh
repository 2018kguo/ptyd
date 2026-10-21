#!/bin/sh
# A/B a change: build the benchmarks of package PKG at git revision REV
# (with this tree's *bench_test.go files, so new benchmarks run on old code) and
# in this tree, run them alternately COUNT times, and print benchstat.
#
#   bench/ab.sh HEAD ./internal/vt 'WriteStream|Serialize'
set -eu
cd "$(dirname "$0")/.."
rev=$1 pkg=$2 bench=${3:-.} count=${COUNT:-10}
tmp=$(mktemp -d)
trap 'git worktree remove --force "$tmp/old" >/dev/null 2>&1; rm -rf "$tmp"' EXIT
git worktree add -q --detach "$tmp/old" "$rev"
for f in "$pkg"/*bench_test.go; do cp "$f" "$tmp/old/$f"; done
(cd "$tmp/old" && go test -c -o "$tmp/old.test" "$pkg")
go test -c -o "$tmp/new.test" "$pkg"
for i in $(seq "$count"); do
	for v in old new; do
		(cd "$pkg" && "$tmp/$v.test" -test.run '^$' -test.bench "$bench" -test.benchmem -test.count 1 2>/dev/null) |
			grep -v '^\[ptyd\]' >>"$tmp/$v.txt"
	done
done
go run golang.org/x/perf/cmd/benchstat@latest -col .file old="$tmp/old.txt" new="$tmp/new.txt"
