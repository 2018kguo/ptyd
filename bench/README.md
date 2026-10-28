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
