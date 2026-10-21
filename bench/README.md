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
PTYD_LOAD_N=32 PTYD_LOAD_PROFILE=/tmp/prof go test -tags loadtest -run Load -v ./internal/loadtest
go tool pprof -top /tmp/prof/cpu.pprof          # also mutex, block, heap, allocs; trace.out
```

`PTYD_SCALE_N` sets how many terminals `BenchmarkIdleTerminals` spawns.

## Files

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

## Results

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
| PTY read (`consume`) | O(B) emulator, plus O(rows) pointer shift and O(cols) serialization per line scrolled off; O(B·K) attach copies; one tail write; O(B + lines·streams) ring. Independent of N, L and S (`TestOutputLineCostIndependentOfState`). |
| watch loop, per output | O(new lines · W) + pending (≤ 16 KiB) |
| checkpoint | O(S + C) and 2 fsyncs; at most once per 15 s of output and per 4 MiB |
| `GET /healthz` | O(1) |
| `GET /metrics` | O(N), one uncontended lock per terminal |
| `GET /terminals` | O(N log N) + per terminal O(attention ≤ 100 + pending); agent terminals also read their session file |
| `GET /terminals/{id}` | O(attention + pending) |
| `GET /output` | O(lines returned) |
| `GET /stream` | connect O(lines after `since`) (was O(L)); per line O(line); one flush per batch of queued lines (was one per line) |
| `GET /screen` | O(C), + O(S) with scrollback; a ghost replays its checkpoint and tail from disk |
| `GET /attach` | connect O(S + C) repaint; then O(B) per read per client; overflow repaint O(C) |
| `POST /write`, `/keys`, `/signal` | O(1) |
| `POST /wait` | per wake O(new lines + pending) regex |
| `POST /ack` | O(attention + W) |
| `POST /resize` | O(C) |
| `POST /restore`, startup restore | O(checkpoint + tail ≤ 8 MiB) replay, process start, 4 fsyncs; startup runs 16 at once |
| `DELETE` | O(files) |
| `GET /search` | O(N · L) regex, one terminal's lock held per 256 lines |
| startup | O(N) directory reads |

Nothing on a per-output path does work proportional to N, L or S.
