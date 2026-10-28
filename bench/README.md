# Benchmarks

Measure the path you care about: emulator writes, daemon I/O, or an entire
session with attached clients and persistence. A faster emulator does not
translate directly into the same daemon speedup; PTY system calls and
client delivery can dominate.

## Run them

```sh
bench/run.sh > new.txt
BENCH='Consume|WriteStream' COUNT=10 bench/run.sh > new.txt
bench/ab.sh main ./internal/terminal 'Consume'
ROUNDS=6 OUT=/tmp/load-ab.jsonl bench/load-ab.sh main
bench/load.sh /tmp/load.jsonl
```

Replace `main` with the baseline commit you want to compare. The A/B scripts
alternate builds on the same host. Compare distributions,
not one fastest sample. Load tests report output throughput, echo latency,
CPU, RSS, GC and persistence overhead. `PTYD_SCALE_N` controls the idle
session count; host PTY and process limits still apply.

For the screen emulator alone, use the
[Phosphor benchmarks](https://github.com/2018kguo/phosphor/tree/main/oracle/ghostty).
Its idle heap excludes processes, output rings and attach queues in ptyd.

## Results and tradeoffs

[The idle-state profile capture](phosphor-v0.4.0/README.md) records four
load runs on two OSes, with attached clients, SSE and
persistence. They are smoke/profile samples, not a before/after speedup.
[The exact-checkpoint integration](phosphor-v0.3.0/README.md) retains the earlier
checkpoint and restore measurements.

The daemon's main I/O optimizations were measured before the emulator was
split into its own package. These are historical A/B results, not fresh
numbers for the latest release:

| Workload | Before / after | Evidence |
|---|---|---|
| 32 terminals at 1 MiB/s, attached, streamed and persisted | macOS CPU 2.10–2.16 / 1.06–1.09 cores; Linux 1.17–1.30 / 0.90–1.09 | `pass2/load-ab.jsonl`, `pass2/linux/load-ab.jsonl` |
| 8 terminals without a rate limit | macOS 113–114 / 118–125 MB/s; Linux 258–274 / 307–344 MB/s | Same alternating runs |

Both sides used their own build profiles. The baseline was `cf16a03`;
Linux used six rounds, macOS three, on an Apple M5 Pro and a Linux arm64 VM.
All raw samples, including noisy runs, remain under [pass2/](pass2/).

The choices behind those results are useful beyond the numbers:

- Busy attach/SSE output is coalesced for up to 4 ms, reducing writes and
  wakeups. Small echoes and output after quiet are immediate.
- Slow attachers have bounded queues and receive a repaint on overflow.
  They miss intermediate bytes instead of blocking the program.
- Persistence writes each PTY read. macOS uses a shared mapping; Linux uses
  writes. Per-read fsync would give different durability and latency costs.
- Busy clients reuse buffers, reducing GC at the cost of a little retained
  memory until they become quiet.

The first optimization pass is retained in `baseline.txt`, `final.txt`,
`final-vs-baseline.txt` and `load-{before,after}.jsonl`; [linux/](linux/) holds
the matching Linux data. `vt` rows refer to the old embedded emulator.
Do not compare them with current Phosphor results as if the harnesses matched.

## Refresh the build profile

`go build` and `go install` use `cmd/ptyd/default.pgo`. Collect the standard
32-agent and 8-flat-out scenarios on each OS, then merge their CPU profiles:

```sh
OUT=/tmp/mac.pgo sh bench/pgo.sh
OUT=/tmp/linux.pgo sh bench/pgo.sh # on Linux
go tool pprof -proto /tmp/mac.pgo /tmp/linux.pgo > cmd/ptyd/default.pgo
```

The script builds its producer with PGO disabled. For detailed profiles,
set `PTYD_LOAD_PROFILE` on the load test; see its settings in
[load_test.go](../internal/loadtest/load_test.go).
