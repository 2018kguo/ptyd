#!/bin/sh
# Run every benchmark with -benchmem, COUNT times (default 10), one package
# at a time, and print the results for benchstat. The test binaries are
# built first, so editing the tree while this runs does not change what is
# measured.
#
#   bench/run.sh > /tmp/new.txt
#   BENCH='Consume|WriteStream' COUNT=10 bench/run.sh > /tmp/new.txt
set -eu
cd "$(dirname "$0")/.."
COUNT=${COUNT:-10}
BENCH=${BENCH:-.}
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
# The emulator's own benchmarks live with it, in github.com/2018kguo/phosphor.
pkgs="./internal/terminal ./internal/api"
for p in $pkgs; do
	go test -c -o "$bin/$(basename "$p").test" "$p"
done
for p in $pkgs; do
	if (cd "$p" && "$bin/$(basename "$p").test" -test.run '^$' -test.bench "$BENCH" \
		-test.benchmem -test.count "$COUNT" -test.timeout 60m) >"$bin/run.log"; then
		sed '/^\[ptyd\]/d' "$bin/run.log"
	else
		status=$?
		cat "$bin/run.log" >&2
		echo "benchmark failed: $p" >&2
		exit "$status"
	fi
done
