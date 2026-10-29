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

Human attachments keep a status row below the app. The client compares exact
rendered rows, moves matching transcript rows within the app rectangle, and
paints only the rows that changed. Keyboard and wheel responses bypass the
background paint budget. Idle views have no periodic redraw wakeups.

Six alternating A/B rounds on an Apple M5 Pro, with the same synthetic transcript
and Go toolchain, measured these medians:

| Measurement | Window | Before | After |
|---|---|---:|---:|
| Sequential wheel input → completed paint, p50 | 120×30 / 240×64 | 16.7 ms | 4.5 ms |
| Sequential wheel input → completed paint, p95 | 120×30 / 240×64 | 17.4 ms | 4.6 ms |
| Bytes sent to the viewing terminal per frame | 120×30 | 6,162 | 385 |
| Bytes sent to the viewing terminal per frame | 240×64 | 21,242 | 729 |
| Parse and render time per frame | 120×30 | 37.7–38.2 µs | 25.3–26.0 µs |
| Parse and render time per frame | 240×64 | 105–110 µs | 66.7–67.7 µs |

Latency includes real local PTYs, the app, daemon, TCP and the client painting
into a host emulator. Each run sends 100 sequential wheel events, waiting for
each synchronized frame to finish. The direct PTY control uses equal app
dimensions and measured about 0.05–0.09 ms p50. Ptyd still adds work and output
batching; this is a comparison with the previous attachment path, not a claim
of direct-terminal latency. Neither case measures a GUI, GPU or physical display.

The separate rendering fixture parses full transcript redraws and renders into
the same host emulator. Timings are Go benchmark elapsed time on a shared
workstation; ranges cover upward and downward scrolling. Emitted
bytes include occasional full redraws when the finite transcript wraps. These
synthetic fixtures do not establish a speedup for every terminal application.

[Download the samples, source patch and fixture](https://github.com/2018kguo/ptyd/releases/download/v0.12.1/ptyd-attachment-benchmarks.tar.gz).
The [evidence index](../docs/benchmarks.json) pins the source revisions and archive
checksum. To compare an explicit baseline with your checkout:

```sh
BASELINE=22af6fb358221be74a35df32a86aa7d5c7eb871c
COUNT=6 BENCHTIME=200ms OUT=.bench/paint \
  bench/ab.sh "$BASELINE" ./internal/client '^BenchmarkPaneScroll$'
COUNT=6 BENCHTIME=100x OUT=.bench/latency \
  bench/ab.sh "$BASELINE" ./internal/client '^BenchmarkAttachScrollLatency$'
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
