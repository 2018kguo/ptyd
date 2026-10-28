# Output waits, session summaries and quiet storage

Agent control often reads retained output or asks which sessions need attention.
This experiment measures those operations separately from PTY throughput.
[Source and fixture metadata](source.json) records the tested code and inputs.

## Wait for output

Five alternating 200 ms samples per build and context size, scanning 5,000
retained lines for a match at the end. The fix keeps only the requested
context in a circular slice; a default wait keeps none.

| Context lines | Baseline median | Candidate median | Baseline allocations | Candidate allocations |
|---|---:|---:|---:|---:|
| 0 | 173.8 µs | 88.5 µs | 5,044 | 44 |
| 3 | 142.7 µs | 91.6 µs | 1,297 | 48 |
| 256 | 114.0 µs | 94.2 µs | 70 | 54 |

The default full-window scan uses 66% fewer allocated bytes. This is an
in-process Wait measurement, not a reduction in every HTTP request or idle
session memory. It adds no retained per-terminal fields or timers.
[Raw pairs](wait-paired.jsonl), [medians](wait-summary.json) and
[method](wait-source.json) retain all samples. Behavior tests cover context
order, pending matches, cursor bounds and oversized requests.

## Overview size

For a fixture of 1,000 sessions, the full list JSON is 374,781 bytes.
The overview is 12,695 bytes for 50 summaries plus counts across all 1,000.
That is about 29.5× less response data because it omits most rows, arguments,
watch definitions and older match detail. It is deliberately a summary;
its top-level truncation flag says there are more rows.

[The fixture](../../internal/terminal/overview_test.go) and
[raw output](overview-payload.txt) preserve the byte counts. The single
iteration's timing is not used as a latency claim. The overview scans
canonical in-memory metadata; it skips agent-session file reads and starts
no daemon background polling. Live refresh belongs to the client.

## Storage after quiet output

A terminal may produce a burst and then wait at a prompt for hours. The output
ring now releases oversized line slots and pending buffers after quiet output,
while retaining every line allowed by the configured budgets. It reuses the
existing persistence callback and adds no timer. With persistence disabled,
reclamation runs only when the process exits.

Three fresh processes per case measured retained Go heap after collection.
A burst of 5,000 short lines, 64 maximum-sized lines and a prompt retained the
same 63 lines and 1,035,153 budgeted bytes on both sides. Heap per ring fell
from 1,294,446 to 1,036,768 bytes: **19.9% less**, about 251 KiB. Four repeated
bursts with quiet intervals produced the same saving. A fresh prompt stayed
at 166 bytes. These are Ring measurements, not whole-daemon RSS.

Reclaiming capacity has a resume cost. Small prompt writes stay allocation-free;
the first 8 KiB burst regrows a buffer once, costing about 0.3 µs more in this
fixture. The benchmark restores the cold backing before each iteration so it
cannot hide that allocation. [Raw samples and method](cold-output/metadata.json),
[heap summary](cold-output/summary.json), [witnesses](cold-output/witness.json)
and [resume measurements](cold-output/resume-bench.txt) retain the tradeoff.
Sampled compaction times are not fleet tail-latency bounds.

[Egres](https://manishrjain.com/egres/) inspired two choices here: share fixed
costs and use different storage policies during active and quiet work.
Here, reclaiming unused capacity is enough; terminal processes and output
retention budgets still determine much of the daemon's memory use.

## Measurement checks

The load harness credits PTY bytes after the emulator has parsed them.
A regression test feeds more than 2.5 MiB, verifies every byte count and all
65,000 processed line numbers, then checks a slow attacher's overflow repaint.
Output retention and client delivery can discard old data after parsing;
they do not skip terminal input.

Load runs now reject early client exits, HTTP/scanner errors, empty measured
clients and exited producers. Failed scenarios return failure and preserve
diagnostics. A/B runs use the same harness against both production revisions;
each revision keeps its own build profile. Earlier mixed exploratory runs are
retained without quoting selected subsets as current gains.

## Reproduce

```sh
go test ./internal/terminal -run '^$' -bench '^BenchmarkWaitRetained$' -benchmem -count 5
go test ./internal/terminal -run '^$' -bench '^BenchmarkOverviewJSON$' -benchmem
go test ./internal/terminal -run '^$' -bench '^BenchmarkRingColdResume$' -benchmem
```

For an end-to-end stream/attach/persistence workload, use the
[load measurements](../README.md). They include costs these microbenchmarks omit.
