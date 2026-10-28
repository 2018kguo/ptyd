# Phosphor v0.4.0 integration

Ptyd uses the published phosphor v0.4.0 module. Its smaller MAIN row
allocations and idle Compact reduce emulator memory while preserving the
new underline colors, prompt metadata, saved cursors and screen modes.
The daemon's checkpoint header stays version 2; the inner snapshot format
changes. Existing v0.2.0 binary checkpoints and older ANSI checkpoints
remain readable. New checkpoints require ptyd v0.3.0 or later.

The actual v0.3.0 snapshot fixture in `internal/terminal/testdata` exercises
old binary checkpoints through the daemon reader and tail replay. Other
cases cross underline colors, long clusters, semantic prompts, independent
modes, saved single shift, inactive screens, resize and future output.

## Profile refresh

`cmd/ptyd/default.pgo` merges macOS and Linux CPU profiles collected with
`bench/pgo.sh`: 32 producers at 1 MiB/s, then 8 producers without a rate
limit. Each has one attached client, one SSE client and persistence enabled;
each measured run lasts 10 seconds. The raw JSON results are in
`load-mac.jsonl` and `load-linux.jsonl`. They are profile/load smoke samples,
not an interleaved before/after performance comparison or a latency SLO.

macOS uses Go 1.26.3 on Apple M5 Pro arm64. Linux uses Go 1.26.10 arm64 in
Docker. The profile producer was built with PGO disabled. Phosphor runtime
source was `7a2e8f2f76e4b85a342ee87241e9738c7a39fad3`; later library
changes repair only the cold ANSI row formatter and tests/docs. The input
hot path sampled by these profiles is unchanged. Profiling used a
local modfile replacement with those exact sources. The shipped go.mod
uses the public tag and contains no replacement.

Reproduce the profile with the normal script on each OS, then merge:

```sh
OUT=/tmp/mac.pgo sh bench/pgo.sh
OUT=/tmp/linux.pgo sh bench/pgo.sh # on Linux
go tool pprof -proto /tmp/mac.pgo /tmp/linux.pgo > cmd/ptyd/default.pgo
```

A Linux test container must reap orphaned child processes (`docker run
--init`). The first container used sleep as PID 1 and left killed test
children as zombies; crash tests passed after enabling init. No daemon
process handling was changed for that test-environment issue.

See the [library comparison](https://github.com/2018kguo/phosphor/blob/v0.4.0/oracle/ghostty/results/2026-10-27-round6/README.md)
for emulator memory, throughput, finite compatibility counts and ANSI limits.
No installed binary or existing user daemon was changed by these checks.
