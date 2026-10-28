# Faster Unicode output in ptyd

The final Phosphor update makes the in-process output path about **6× faster
on mixed graphemes**, **5× on CJK** and **27% faster on progress output** in
this fixture. The compact idle emulator model and saved terminal format stay
the same. Smaller log/TUI differences need longer measurements.

| Input | Baseline MB/s | Candidate MB/s | Ratio |
|---|---:|---:|---:|
| claude | 366.21 | 394.29 | 1.08× |
| logs | 407.95 | 418.71 | 1.03× |
| progress | 1,311.12 | 1,659.20 | 1.27× |
| graphemes | 21.36 | 129.24 | 6.05× |
| cjk | 23.27 | 118.01 | 5.07× |
| claude-persist | 268.81 | 293.62 | 1.09× |

Five alternating 200 ms samples per build, one Go execution thread, 120×40
geometry and reads up to 8 KiB. Identical ptyd production and benchmark files run
against old and new Phosphor source. Both Unicode inputs explicitly enable
grapheme mode. Each output count is credited after parsing.

Mixed Unicode allocates about 1.25 MB per MiB output, down from 154 MB. CJK allocates
about 1.48 MB, down from 158 MB. Rings and output lines still allocate after the
emulator stops allocating for warm repeated text.

This measures `consume`: screen parsing, ring/counter updates and related local
work. The persistence case also writes tails and checkpoints. PTY reads, HTTP
and client delivery are excluded. [All pairs](consume-paired.txt),
[medians](consume-summary.json) and [source](source.json) retain the full method.

The separate [native comparison](https://github.com/2018kguo/phosphor/tree/main/oracle/ghostty/results/2026-10-28-native-mining)
shows remaining native leads and actual history retention. Phosphor's prompt
heap stays about 1.51 KB; ptyd also retains rings, processes, PTYs and queues.

[Fresh macOS/Linux profile captures](profiles/metadata.json) update the packaged
build profile. All configured producers and clients passed progress/error gates.
These four profiled runs are smoke captures, separate from the timings above.
The [earlier stage](../unicode-history/README.md) retains its source and samples.

Build the same current benchmark file against each dependency, then alternate
its compiled binaries:

```sh
go test -c -o /tmp/terminal.test ./internal/terminal
GOMAXPROCS=1 /tmp/terminal.test -test.run '^$' \
  -test.bench '^BenchmarkConsume$' -test.benchtime 200ms
```
