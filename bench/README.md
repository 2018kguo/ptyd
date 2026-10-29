# Performance and measurement

Measure the path your application uses. Emulator parsing, local output handling,
PTY I/O and client delivery have different costs. The table below measures the
subscriber-free `consume` path: screen parsing, retained lines, rings and counters.
The persistence row also writes tails and checkpoints. It excludes real PTY reads,
attach/SSE/HTTP clients and log mirroring.

## Recorded output processing

Five alternating 200 ms samples per build, one Go execution thread, 120×40
geometry and chunks up to 8 KiB. Both builds use identical ptyd production
and fixture files, against the old and new Phosphor cores. Unicode fixtures
enable grapheme mode; bytes count only after synchronous parsing returns.

| Input | Baseline MB/s | Updated core MB/s | Ratio |
|---|---:|---:|---:|
| Synthetic agent TUI | 366.21 | 394.29 | 1.08× |
| Logs | 407.95 | 418.71 | 1.03× |
| Progress | 1,311.12 | 1,659.20 | 1.27× |
| Mixed graphemes | 21.36 | 129.24 | 6.05× |
| CJK | 23.27 | 118.01 | 5.07× |
| Agent TUI with persistence | 268.81 | 293.62 | 1.09× |

These are wall-time decimal MB/s. Small differences need longer measurements.
They do not establish the same gains in complete agent sessions. Mixed Unicode
allocates about 1.25 MB per MiB of output, down from 154 MB; CJK about 1.48 MB,
down from 158 MB. Rings and retained lines still allocate.

The [evidence index](../docs/benchmarks.json) pins sources and the archive checksum.
[Download the complete evidence bundle](https://github.com/2018kguo/ptyd/releases/download/v0.4.1/ptyd-benchmark-evidence.tar.gz)
for all pairs, medians, profile captures and historical experiments. Its manifest
preserves the original file names and hashes. Historical reports describe their
recorded revisions. Maintained scripts stay here; new run output goes in ignored
`.bench/`, rather than a directory or development log per experiment.

## Storage after quiet output

A burst-then-idle output-ring fixture retains the same output with about 20%
less heap after reclaiming unused capacity. Resuming output can allocate again.
A full-window retained-output wait scan allocates about 66% fewer bytes in its
fixture. Bounded overviews return fleet counts and at most the requested rows,
with explicit truncation, instead of every session's full arguments and watches.
These are separate observations; they are not a combined daemon speedup.

Phosphor's compacted prompt footprint excludes ptyd's processes, PTYs, waiting
threads, rings, queues and persistence. Editor screens and retained logs add
memory. See the [emulator comparison](https://github.com/2018kguo/phosphor/blob/main/docs/benchmarks.md)
for its workload conditions and remaining native leads.

## Attached scrolling

Human attachments compare exact rendered rows, move matching transcript rows
within the app rectangle, and keep the status bar outside it. The bar follows
the viewer's theme and stays cached while the app scrolls. Keyboard and wheel
input bypass the background paint budget; incomplete synchronized frames keep
that priority until they can actually paint. Idle panes have no paint timer.
The daemon also prioritizes a short output burst after attached input while
retaining bulk-output batching.

Six alternating A/B rounds on an Apple M5 Pro used identical synthetic transcript
fixtures and Go toolchains. These are medians across the six runs:

| Sequential wheel input to completed paint | Direct PTY control | Ptyd before | Ptyd after |
|---|---:|---:|---:|
| 120×30, p50 | 0.03 ms | 4.50 ms | 0.15 ms |
| 120×30, p95 | 0.04 ms | 4.56 ms | 0.18 ms |
| 240×64, p50 | 0.06 ms | 4.49 ms | 0.21 ms |
| 240×64, p95 | 0.08 ms | 4.55 ms | 0.25 ms |

Latency includes real local PTYs, the app, daemon, TCP and the client painting
into a host emulator. Each run sends 100 sequential wheel events, waiting for
each synchronized frame to finish. The direct control uses equal app dimensions.
These results require both the updated client and daemon. A client update works
with an existing daemon, but does not change that daemon's batching behavior.
Leave running work intact and update the daemon at its next safe restart.

Snapshot setup also avoids an ANSI repaint that a framed viewer never uses,
and skips cloning retained screens when their geometry already matches:

| Snapshot setup, 2,000 history rows | Before | After |
|---|---:|---:|
| Live screen, elapsed time | 42.6 µs | 22.5 µs |
| Loaded static screen, elapsed time | 139 µs | 22.1 µs |
| Live screen, allocated bytes | 287,040 | 139,584 |
| Loaded static screen, allocated bytes | 593,048 | 139,584 |

This fixture verifies exactly 2,000 retained rows. It excludes network, PTY I/O,
disk loading and human-client painting. The separate one/eight-attacher fanout
fixture found no measured throughput change. Prioritizing response chunks can
cost a few more allocations per input; these are targeted improvements rather
than a claim that every metric improves.

All timings are elapsed measurements on a shared workstation. No fixture
measures a GUI, GPU or physical display, and none establishes a speedup for
every terminal application.
[Download all samples, source patch and fixtures](https://github.com/2018kguo/ptyd/releases/download/v0.12.2/ptyd-attachment-benchmarks.tar.gz).
The [evidence index](../docs/benchmarks.json) pins sources and the archive checksum.
To repeat the comparisons:

```sh
BASELINE=23b2404a86bbabc16f95a93d225bd6441aeb5586
COUNT=6 BENCHTIME=100x OUT=.bench/latency \
  bench/ab.sh "$BASELINE" ./internal/client '^BenchmarkAttachScrollLatency$'
COUNT=6 BENCHTIME=200ms OUT=.bench/snapshot \
  bench/ab.sh "$BASELINE" ./internal/terminal '^BenchmarkRenderedAttach$'
COUNT=6 BENCHTIME=200ms OUT=.bench/fanout \
  bench/ab.sh "$BASELINE" ./internal/terminal '^BenchmarkAttachFanout/(K=1|K=8)$'
```

## Workspace browsing

The eight-pane browser keeps its read and rendering budgets separate:

| Work | Bound or cost |
|---|---|
| Live previews | Eight panes, four concurrent reads; source geometry stays unchanged. |
| Conversation titles | Reuse preview titles, then at most sixteen nearby records; four workers and a one-second budget. |
| Content search | At most 500 candidates, four workers; linear in the visible cells and retained text read. |
| Browser hidden behind an attachment | Preview, title and search polling stops. |
| HTTP connections | A shared pool keeps up to eight idle connections per daemon, sixteen overall, for up to thirty seconds. |

Pin and match lookups multiply by at most eight. The existing daemon overview
still sorts registry matches in O(M log M), where M is the number of matches.
The browser adds no growing quadratic scan.

Reusing the canonical renderer's temporary row model and buffers reduced an
eight-pane fixture from about **1.56 MB to 0.60 MB allocated per frame** (62%).
The allocation figures describe temporary Go storage per frame, excluding
retained session memory and RSS. That recorded comparison emitted 58,213 bytes
per frame; pane boundary cleanup adds control bytes in current builds.
Keeping four search connections
alive also avoided the default transport's repeated connection creation between
batches. Both changes are in the client and work with an already running daemon.

The fixture uses eight dense, styled 160×48 screens in a 240×80 browser on an
Apple M5 Pro. Raw samples, source hashes, baseline overlays and toolchain details
are in the [workspace evidence bundle](https://github.com/2018kguo/ptyd/releases/download/v0.13.0/ptyd-workspace-evidence.tar.gz).
Frame times measure Go work producing ANSI bytes, excluding terminal display
and GPU latency. Session-count tests use small synthetic snapshots over local
HTTP; they do not establish timings for hundreds of busy real agents. Large
open-wrap searches report incomplete boundary coverage under the documented
memory budget; their times are separate from searches with complete coverage.

Reproduce the maintained fixtures:

```sh
go test ./cmd/ptyd -run '^$' -bench '^BenchmarkWorkspaceFrame$' -benchtime=250ms -count=3
go test ./internal/client -run '^$' -bench '^BenchmarkContentSearchBytes$' -benchtime=250ms -count=3
go test ./internal/client -run '^$' -bench '^BenchmarkContentSearchSessions$' -benchtime=5x -count=3
```

## Run a comparison

```sh
mkdir -p .bench
bench/run.sh > .bench/new.txt
BENCH='Consume|WriteStream' COUNT=10 bench/run.sh > .bench/output.txt
OUT=.bench/consume bench/ab.sh main ./internal/terminal 'Consume'
ROUNDS=6 OUT=.bench/load-ab.jsonl bench/load-ab.sh main
bench/load.sh .bench/load.jsonl
```

Choose an explicit baseline commit. The A/B tools build once, keep the current
measurement fixtures fixed, alternate order and stop on build or sample failure.
A partial benchmark output is not a valid comparison. Both builds must accept
the same benchmark source unchanged. Keep all samples, source revisions,
toolchain/build settings and producer/client settings with the result.
The statistics tool is pinned in the script; read distributions rather than
one fastest row.

Load tests exercise real producer PTYs, attached clients, SSE and persistence.
They fail if a configured producer or client exits early or makes no progress.
CPU includes the daemon and in-process test clients; it excludes producer-child
CPU. RSS is the harness process's lifetime high-water mark, which can include
startup or earlier activity. It is not an isolated daemon allocation counter
or a measured-window peak. `PTYD_SCALE_N` controls idle session counts; OS PTY
and process limits still apply. Check the scenario settings in
[the load harness](../internal/loadtest/load_test.go) before generalizing a result.

## Tradeoffs the load test exercises

- Busy attach/SSE output can coalesce for up to 4 ms. Small echoes and output
  after quiet are immediate.
- Slow attachers have bounded queues and receive a repaint on overflow.
  They can miss intermediate bytes while the program continues.
- Persistence appends each PTY read. macOS uses a shared mapping; Linux uses
  writes. Per-read fsync would measure a different durability policy.
- Busy clients reuse buffers, retaining some memory until they become quiet.

## Refresh the build profile

`go build` and `go install` use the tracked `cmd/ptyd/default.pgo`. Collect the
32-producer and 8-flat-out scenarios on each OS, then merge their CPU profiles:

```sh
OUT=/tmp/mac.pgo sh bench/pgo.sh
OUT=/tmp/linux.pgo sh bench/pgo.sh # on Linux
go tool pprof -proto /tmp/mac.pgo /tmp/linux.pgo > cmd/ptyd/default.pgo
```

Collection disables PGO. Failed scenarios preserve the previous profile and
print diagnostics. The published captures are profile/smoke evidence, separate
from before/after timings. Keep inputs, hashes and build settings when refreshing
this intentional build artifact. [Development](../docs/development.md) covers
local tests and separate live-agent checks.
