#!/bin/sh
# Run every benchmark with -benchmem, COUNT times (default 10), one package
# at a time, and print the results for benchstat. The test binaries are
# built first, so editing the tree while this runs does not change what is
# measured.
#
#   bench/run.sh > bench/new.txt
#   BENCH='Consume|WriteStream' COUNT=10 bench/run.sh > bench/new.txt
set -eu
cd "$(dirname "$0")/.."
COUNT=${COUNT:-10}
BENCH=${BENCH:-.}
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
pkgs="./internal/vt ./internal/terminal ./internal/api"
for p in $pkgs; do
	go test -c -o "$bin/$(basename "$p").test" "$p"
done
for p in $pkgs; do
	(cd "$p" && "$bin/$(basename "$p").test" -test.run '^$' -test.bench "$BENCH" \
		-test.benchmem -test.count "$COUNT" -test.timeout 60m 2>/dev/null)
done | grep -v '^\[ptyd\]'
