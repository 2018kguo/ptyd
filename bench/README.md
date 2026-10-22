# Benchmarks

## Reproduce

```sh
bench/run.sh > new.txt                       # every benchmark, -benchmem, 10 runs each
BENCH='Consume|WriteStream' COUNT=10 bench/run.sh > new.txt
go run golang.org/x/perf/cmd/benchstat@latest bench/baseline.txt new.txt

# A/B a change against a revision, runs interleaved (fairer on a busy host):
bench/ab.sh HEAD ./internal/vt 'WriteStream'
OUT=/tmp/ab bench/ab.sh <rev> './internal/vt ./internal/terminal ./internal/api'

# End-to-end load test (build tag loadtest); one JSON line per scenario:
bench/load.sh /tmp/load.jsonl
# The same, A/B against a revision, runs interleaved, a row per run:
ROUNDS=6 OUT=/tmp/ab.jsonl bench/load-ab.sh <rev>
# Refresh the PGO profile (cmd/ptyd/default.pgo), merging another OS's:
OUT=/tmp/lin.pgo bench/pgo.sh       # e.g. in a Linux container
bench/pgo.sh /tmp/lin.pgo
PTYD_LOAD_N=32 PTYD_LOAD_PROFILE=/tmp/prof go test -tags loadtest -run Load -v ./internal/loadtest
go tool pprof -top /tmp/prof/cpu.pprof          # also mutex, block, heap, allocs; trace.out
```

`PTYD_SCALE_N` sets how many terminals `BenchmarkIdleTerminals` spawns.

## Files

- `pass2/`: the second pass. `pass2/linux/baseline.txt` and `final.txt`
  are `bench/ab.sh cf16a03` (the code before it) on Linux, n=10
  interleaved, `final-vs-baseline.txt` their benchstat; `load-ab.jsonl`
  is `bench/load-ab.sh cf16a03` (6 rounds) and `load-after.jsonl`
  `bench/load.sh` on the final code, for macOS (top level) and Linux.

First pass:

- `baseline.txt`: `bench/run.sh` on the code before this pass (commit
  `471422c`), darwin/arm64 (Apple M5 Pro). The host is shared and was
  busy, so some rows are noisy (± column).
- `final.txt` and `baseline-rerun.txt`: the final code and the baseline
  code, run interleaved by `bench/ab.sh` (n=10); `final-vs-baseline.txt`
  is their benchstat. `linux/` has the same for Linux (Docker, arm64,
  8 CPUs).
- `load-before.jsonl`, `load-after.jsonl`: `bench/load.sh`, two rounds
  each, on the code before (`ea172f3`, which only adds the load test and
  disk counters) and after; `linux/` again for Linux.

## Second pass: ideas from other terminals (Oct 2026)

The first pass left the daemon's own code cheap: under load, profiles
showed 70–80% of its time in system calls and scheduler wakeups (on
macOS the emulator was under 1%). So this pass looked at how the
fastest terminals and multiplexers batch I/O, and took what moved the
load test.

| Idea (source) | Verdict | Why |
| --- | --- | --- |
| Batch output to clients on a short timer: kitty `input_delay` (3 ms, skipped under 1 KB), ghostty's ≤ 3 ms read batches, tmux's redraw timer | **Taken** (`terminal.Coalesce` = 4 ms) | One write and wakeup per client per 4 ms instead of per PTY read. Output after a quiet spell and anything under 1 KiB (an echo) goes out at once; a queue reaching 256 KiB is sent at once, so holding never overflows it. macOS CPU −45–50%. |
| Bounded per-client queue, drop and redraw when behind (tmux `TTY_BLOCK`, zellij #536) | Already there | 1 MiB queue, then a repaint. |
| Pull, don't push (alacritty/wezterm: one wakeup per batch of parsed output) | **Taken** | Streams read the ring through a cursor (one wake per output) instead of a channel send per line. |
| Write the tail through a shared mapping (database-style mmap log) | **Taken on macOS**, rejected on Linux | macOS: a write(2) per 1 KiB read was 20–25% of CPU; a store into a MAP_SHARED page is in the page cache at once, so `kill -9` durability is unchanged (`TestTailSurvivesKill9`). Linux: no faster, and page faults stalled the reader under writeback (echo p99 up to 4.9 s once). |
| Scan printable runs with SIMD (ghostty `utf8DecodeUntilControlSeq`, kitty `find_either_of_two_bytes`) | **Taken** as SWAR (8 bytes per step; Go has no portable SIMD) | progress −19%; other streams unchanged (their runs are short). |
| Encode hot JSON by hand (no reflection, no `time.Time.MarshalJSON`) | **Taken** for SSE lines | Byte-identical to encoding/json (exhaustive test + fuzz); stream −28%, allocs −50%. |
| Recycle client buffers (Go: pools of big buffers pin memory, so keep two per client) | **Taken** | Two buffers swap while busy, big ones released after 1 s quiet. Flat-out GC 375 → 161 per 10 s before the other changes. |
| PGO (Go 1.21+, 2–14% typical) | **Taken** | `cmd/ptyd/default.pgo` from the load test on both OSes. Linux: emulator −12%, per-read path −15%, load test −2% CPU / +2% MB/s; macOS within noise (its time is system calls). |
| Big PTY reads, drain until EAGAIN (alacritty 1 MiB, ghostty 4×64 KiB, zellij 64 KiB) | Rejected | The kernel caps a read: 1 KiB on macOS (a second read right after returns EAGAIN until the writer runs again) and the 4 KiB n_tty buffer on Linux (avg 2.9–3.8 KiB measured). Draining adds an EAGAIN read per batch and saves no reads; client batching gets the downstream win instead. |
| writev for tail records | Rejected | Already one write per record (header and data copied into one buffer); os.File has no writev. |
| splice/sendfile to clients | Rejected | The source is a userspace queue (the emulator and the tail need the bytes), not a file or socket. |
| Page-based screen storage and style dedup (ghostty `PageList`, `RefCountedSet`) | Rejected | The emulator was 1% (macOS) to 13% (Linux) of load; idle memory is already ~2.8 KB per emulator. |
| CSI bulk parsing (ghostty) | Rejected | Same Amdahl bound: `step` is ~7% of Linux load CPU. |
| Cache agents' session file for `GET /terminals` | Rejected | Hooks rewrite it at any time with no notification; a cache needs a stat, which costs about the read. |
| `slices.SortFunc` + `cmp.Or` in List, a map increment per terminal in Metrics | Rejected | +60% and +125% vs the existing code (Linux, n=10). |

Load test, the code before this pass (`cf16a03`) vs after, built as
`go install` builds (after: with PGO), interleaved by
`bench/load-ab.sh`; ranges, Linux 6 rounds, macOS 3 rounds (a first
6-round run, also in `pass2/load-ab.jsonl`, had 3 rounds ruined by an
overloaded host and agrees on the rest):

| Scenario | Metric | macOS | Linux |
| --- | --- | --- | --- |
| 32 agents at 1 MiB/s, attached + streamed, persisted | CPU (cores) | 2.10–2.16 → 1.06–1.09 | 1.17–1.30 → 0.90–1.09 |
| | keystroke echo p50 / p99 (ms) | 0.09 / 0.56–0.70 → 0.10 / 0.72–0.75 | 0.18–0.19 / 0.38–0.44 → 0.18–0.20 / 0.37–0.66 |
| | GC cycles in 10 s | 9 → 2 | 9 → 2 |
| | peak RSS (MiB) | 93–94 → 82–84 | 73–75 → 61–64 |
| 8 terminals flat out | throughput (MB/s) | 113–114 → 118–125 | 258–274 → 307–344 |
| | CPU (cores) | 6.9–7.1 → 3.5–3.6 | 2.78–2.92 → 2.27–2.37 |
| | keystroke echo p99 (ms) | 0.76–0.93 → 0.26–0.34 | 4.0–5.1 → 3.2–3.9 |
| | GC cycles in 10 s | 103–106 → 20 | 468–510 → 35–38 |
| | peak RSS (MiB) | 46–48 → 50–51 | 46–50 → 52–54 |
| 64 terminals at 64 KiB/s, steady / bursty | write amplification | 1.006 / 1.034 | 1.003 / 1.03 |

Flat-out RSS is up 4 MiB because a busy client keeps its two queue
buffers. On macOS, echo p99 moves with how busy the host's threads are:
a nearly idle daemon (one terminal) shows 1.4–4 ms there, so halving
CPU raises the 32-agent p99 a little (0.6 → 0.7 ms) while p50 holds.

Micro benchmarks, Linux, before vs after (`pass2/linux`, n=10, p < 0.05;
built without PGO):

| Benchmark | Change |
| --- | --- |
| `WriteStream` progress / logs / claude | −21% / −6% / −3% |
| `Consume` progress / logs / claude / claude-persist | −18% / −7% / −2% / −1.5% |
| `AttachFanout` K=1 / K=64 | −3% / −32% |
| `RingSubscribe` (stream connect against a full ring) | 1028 ns → 11 ns, 13 KiB → 0 |
| `RingSnapshotTail`, `RingWrite` | −20%, −4% |
| `Stream` bytes / allocs per op | −66% / −50% |
| `Stream` time | 0.19 → 4.8 ms per burst: it reads back-to-back 200-line bursts, so it now includes the 4 ms hold (by design) |
| `TailAppend` allocs | 1 → 2 per MiB (a record buffer per tail generation) |
| `IdleTerminals` | unchanged (heap +1.5%, RSS −5% per terminal; `terminal` and `Ring` structs smaller) |

## First pass results

Baseline vs final, interleaved A/B, n=10 (macOS host busy, ± 15–40%;
Linux ± 5–15%). Only differences with p < 0.05 are listed.

| | macOS | Linux |
| --- | --- | --- |
| `vt` agent-TUI stream (`WriteStream/claude`) | −40% time, 12 MB → 27 KB allocated per MB | −45% |
| `vt` less / vim / logs | −49% / −8% / ~ | −55% / −7% / −13% |
| Per-read path, agent output (`Consume/claude`) | −41%, allocs −66% | −43% |
| Per-read path with persistence | −32% | −43% |
| Attach fan-out K = 1 / 8 / 64 | −31% / −37% / −42% | −45% / −53% / −46% |
| Screen rebuild from a 4 MiB tail | −45%, 52 MB → 0.55 MB allocated | −46% |
| SSE stream, 200-line burst | −71% | −68% |
| Stream connect against a full ring | 27.7 µs → 1.1 µs (O(L) → O(new lines)) | −96% |
| Restore 100 rerun terminals | 2.45 s → 1.09 s | 251 ms → 63 ms |
| `/metrics` at N = 1000 / 5000 | −55% / −56% | −55% / −55% |
| Search at N = 5000 | −20% | −25% |
| Idle terminal (heap, RSS, goroutines, threads, fds, CPU) | unchanged | unchanged |

Regressions flagged on Linux: `Serialize/RepaintScreen` +8% (rows that
were drawn on and erased now stay allocated, so repaint scans them;
repaints happen per attach and checkpoint, not per byte), and a few
untouched paths at p ≈ 0.04 (`HTTP/List`, `EncodeList`,
`StripANSI/plain`), which are within the false-positive rate of ~70
comparisons.

Load test (`bench/load.sh`; two rounds, before → after):

| Scenario | Metric | macOS | Linux |
| --- | --- | --- | --- |
| 32 agents at 1 MiB/s, attached + streamed, persisted | CPU (cores) | 5.0–5.4 → 2.6–2.8 | 2.6–2.7 → 1.5 |
| | keystroke echo p50 / p99 (ms) | 0.20–0.24 / 4.6–8.7 → 0.11–0.13 / 0.6 | 0.23–0.24 / 0.8–1.2 → 0.23 / 0.54–0.58 |
| | GC cycles in 10 s | 221–224 → 10 | 231 → 10 |
| | peak RSS (MiB) | 104–105 → 88–91 | 76–78 → 77–78 |
| 8 terminals flat out | throughput (MB/s) | 68 → 84–87 | 98–103 → 190–198 |
| | CPU (cores) | 8.3 → 6.7–7.3 | 4.1 → 2.8 |
| | keystroke echo p99 (ms) | 8.1–9.9 → 1.3–2.4 | 17–19 → 5.2–6.6 |
| | peak RSS (MiB) | 90–148 → 48 | 87–99 → 46–51 |
| 64 terminals at 64 KiB/s, steady / 2 s bursts | write amplification | 1.006 / 1.033 (unchanged) | 1.003 / 1.030 |
| | fsyncs/s, checkpoints/s | 0, 0 / 6.4, 3.2 (unchanged) | same |

GC pause p99 (stop-the-world) is unchanged within noise: with ~10 cycles
per run it is the maximum pause, 0.3–0.6 ms on Linux and up to 4–10 ms on
the busy macOS host, where the trace shows most pauses under 0.4 ms and
an occasional multi-millisecond one.

Disk: writes are the output itself plus 5 bytes per PTY read plus one
checkpoint (~100–300 KiB) per 4 MiB of output or per burst at most every
15 s: 1.003–1.06 bytes written per byte printed. Steady output never
settles, so it checkpoints only when the tail reaches 4 MiB (0 fsyncs/s
at 64 KiB/s per terminal); bursty output checkpoints after each burst
that comes 15 s after the last checkpoint (2 fsyncs each). Flat out,
that is 2 fsyncs per 4 MiB per terminal. Nothing was changed here: the
tail must be written on every read to survive `kill -9`.

## What is measured

| Benchmark | Path |
| --- | --- |
| `vt` `WriteStream/{vim,less,claude,logs,progress}` | Emulator throughput on recorded vim/less sessions and generated agent-TUI (`internal/benchdata.Claude`), log and `\r` progress-bar output, fed in 8 KiB chunks |
| `vt` `Serialize/*`, `Repaint`, `Replay` | Repaint with/without scrollback, screen and scrollback as text, rebuilding a screen from a checkpoint |
| `terminal` `Consume/*` | Everything one PTY read goes through (screen, attachers, tail, line ring, counters), per MiB; `claude-persist` with the tail and checkpoints |
| `terminal` `AttachFanout/K=` | The same with K attached clients, each drained by a reader |
| `terminal` `Checkpoint`, `TailAppend` | One checkpoint of a full screen (repaint, temp file, fsync, rename, dir fsync); appending a MiB to the tail |
| `terminal` `LoadScreen/tail=`, `Startup1000/*`, `RestoreGhosts` | Rebuilding a screen after a crash; daemon start over 1000 persisted terminals; restoring 100 rerun terminals |
| `terminal` `ManagerScale/*/N=` | List, Metrics, Get, Search against N = 100 / 1000 / 5000 terminals |
| `terminal` `RingSubscribe`, `Ring*` | Stream connect against a full ring; ring write and snapshots |
| `api` `HTTP/*`, `Stream`, `EncodeList` | Routes against 200 terminals with 5000 lines; the SSE stream end to end; JSON of a 1000-terminal list |
| `terminal` `IdleTerminals` | Per idle terminal: heap, RSS, goroutines, threads, fds, idle CPU |

## Complexity

N terminals, L retained lines per terminal (≤ 5000), S scrollback bytes
(≤ 256 KiB), C screen cells, K attached clients, W watches, B bytes in
one PTY read (≤ 1 KiB on macOS, ≤ 4 KiB on Linux).

| Route or loop | Cost |
| --- | --- |
| PTY read (`consume`) | O(B) emulator, plus O(rows) pointer shift and O(cols) serialization per line scrolled off; O(B·K) attach copies; one tail write (a memory copy on macOS); O(B) ring, no per-stream work. Independent of N, L and S (`TestOutputLineCostIndependentOfState`). |
| watch loop, per output | O(new lines · W) + pending (≤ 16 KiB) |
| checkpoint | O(S + C) and 2 fsyncs; at most once per 15 s of output and per 4 MiB |
| `GET /healthz` | O(1) |
| `GET /metrics` | O(N), one uncontended lock per terminal |
| `GET /terminals` | O(N log N) + per terminal O(attention ≤ 100 + pending); agent terminals also read their session file |
| `GET /terminals/{id}` | O(attention + pending) |
| `GET /output` | O(lines returned) |
| `GET /stream` | connect O(1); per output one wake, O(new lines) copied out in 256-line chunks, one flush, at most one per 4 ms while busy |
| `GET /screen` | O(C), + O(S) with scrollback; a ghost replays its checkpoint and tail from disk |
| `GET /attach` | connect O(S + C) repaint; then O(B) per read per client, one write per 4 ms while busy; overflow repaint O(C) |
| `POST /write`, `/keys`, `/signal` | O(1) |
| `POST /wait` | per wake O(new lines + pending) regex |
| `POST /ack` | O(attention + W) |
| `POST /resize` | O(C) |
| `POST /restore`, startup restore | O(checkpoint + tail ≤ 8 MiB) replay, process start, 4 fsyncs; startup runs 16 at once |
| `DELETE` | O(files) |
| `GET /search` | O(N · L) regex, one terminal's lock held per 256 lines |
| startup | O(N) directory reads |

Nothing on a per-output path does work proportional to N, L or S.
