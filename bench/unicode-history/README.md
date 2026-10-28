# Unicode and terminal output processing

Using the improved Phosphor core makes the in-process output path about **2.8×
faster on mixed graphemes**, **4.1× on CJK** and **12% on progress output** in
this fixture. The smaller log/TUI differences need longer measurements.

| Input | Baseline MB/s | Candidate MB/s | Ratio |
|---|---:|---:|---:|
| claude | 352.36 | 364.22 | 1.03× |
| logs | 425.12 | 434.69 | 1.02× |
| progress | 1,287.80 | 1,445.71 | 1.12× |
| graphemes | 19.94 | 55.01 | 2.76× |
| cjk | 20.68 | 85.72 | 4.15× |
| claude-persist | 257.92 | 275.55 | 1.07× |

Five alternating 200 ms samples per build, one Go execution thread, 120×40
geometry and reads up to 8 KiB. The same ptyd source and fixtures run against
the old and new Phosphor source. All configured output is parsed before its
byte count is credited. The Unicode fixtures enable grapheme mode explicitly.

The mixed fixture allocates about **1.31 MB per MiB of output**, down from
154 MB. CJK allocates about 1.50 MB, down from 158 MB. Ring parsing and retained
output still have costs after the emulator stops allocating for repeated text.

These are `consume` benchmarks: screen parsing, ring/counter updates and
related in-process work. The persistence case also writes tails and checkpoints.
They exclude PTY reads, HTTP and client delivery; they are not whole-daemon
speedup claims. [All pairs](consume-paired.txt), [medians](consume-summary.json)
and [source metadata](source.json) retain the method and allocations.

Phosphor's separate [native comparison](https://github.com/2018kguo/phosphor/tree/main/oracle/ghostty/results/2026-10-28-unicode-history)
retains equal-input checks, actual history counts and remaining native leads.
Its compacted prompt heap stays about 1.51 KB; ptyd also retains rings, processes,
PTYs and delivery queues. Refresh the build profile with `bench/pgo.sh` after
changing the core. Profile capture is a smoke check, separate from these timings.

To compare dependencies, build the same current benchmark file against each
source, then alternate their compiled test binaries:

```sh
go test -c -o /tmp/terminal.test ./internal/terminal
GOMAXPROCS=1 /tmp/terminal.test -test.run '^$' \
  -test.bench '^BenchmarkConsume$' -test.benchtime 200ms
```
