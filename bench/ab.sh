#!/bin/sh
# A/B a change: build the benchmarks of each package at git revision REV
# (with this tree's *bench_test.go files, so new benchmarks run on old
# code) and in this tree, run them alternately COUNT times (default 10),
# and print benchstat. OUT, if set, is a directory to keep old.txt and
# new.txt in. OLD_SED, if set, is a sed script applied to the copied
# benchmark files (to adapt a benchmark to an older API).
#
#   bench/ab.sh HEAD ./internal/vt 'WriteStream|Serialize'
#   OUT=/tmp/ab bench/ab.sh <rev> './internal/vt ./internal/terminal ./internal/api'
set -eu
cd "$(dirname "$0")/.."
rev=$1 pkgs=$2 bench=${3:-.} count=${COUNT:-10}
tmp=$(mktemp -d)
trap 'git worktree remove --force "$tmp/old" >/dev/null 2>&1; rm -rf "$tmp"' EXIT
git worktree add -q --detach "$tmp/old" "$rev"
for pkg in $pkgs; do
	for f in "$pkg"/*bench_test.go; do
		cp "$f" "$tmp/old/$f"
		[ -z "${OLD_SED:-}" ] || sed -i.bak "$OLD_SED" "$tmp/old/$f"
	done
	n=$(basename "$pkg")
	(cd "$tmp/old" && go test -c -o "$tmp/old-$n.test" "$pkg")
	go test -c -o "$tmp/new-$n.test" "$pkg"
done
for i in $(seq "$count"); do
	for pkg in $pkgs; do
		n=$(basename "$pkg")
		for v in old new; do
			(cd "$pkg" && "$tmp/$v-$n.test" -test.run '^$' -test.bench "$bench" -test.benchmem -test.count 1 -test.timeout 60m 2>/dev/null) |
				grep -v '^\[ptyd\]' >>"$tmp/$v.txt" || true
		done
	done
done
if [ -n "${OUT:-}" ]; then mkdir -p "$OUT" && cp "$tmp/old.txt" "$tmp/new.txt" "$OUT/"; fi
go run golang.org/x/perf/cmd/benchstat@latest -col .file old="$tmp/old.txt" new="$tmp/new.txt"
