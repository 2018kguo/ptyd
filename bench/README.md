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
alternate builds on the same host. The load A/B script uses the current
harness for both builds and each revision's own build profile. Runs fail if a
configured client ends early, makes no progress or a producer exits. Compare distributions,
not one fastest sample. Load tests report output throughput, echo latency,
CPU, RSS, GC and persistence overhead. `PTYD_SCALE_N` controls the idle
session count; host PTY and process limits still apply.

For the screen emulator alone, use the
[Phosphor benchmarks](https://github.com/2018kguo/phosphor/tree/main/oracle/ghostty).
Its idle heap excludes processes, output rings and attach queues in ptyd.

## Results and tradeoffs

[Output waits, session summaries and quiet storage](agent-round/README.md) measures
retained-output matching, overview response size and reclaimed ring capacity. The default full-window wait scan allocates
66% fewer bytes; a 50-row overview includes fleet counts without returning
every session's arguments and watch details. A burst-then-idle ring fixture
retains the same output with about 20% less heap.

[The idle-state profile capture](phosphor-v0.4.0/README.md) records four
load runs on two OSes, with attached clients, SSE and
persistence. They are smoke/profile samples, not a before/after speedup.
[The exact-checkpoint integration](phosphor-v0.3.0/README.md) retains the earlier
checkpoint and restore measurements.

The earlier daemon I/O experiments remain under [pass2/](pass2/) and
[pass2/linux/](pass2/linux/). The macOS file mixes exploratory runs with later
pairs, including failed or noisy cases. Selected subsets are not a reliable
current speedup claim. Keep the raw records when comparing new runs.

The daemon makes several throughput and latency tradeoffs:

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
