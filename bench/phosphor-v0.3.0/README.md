# Exact checkpoint measurements

Recorded for ptyd v0.2.0 with phosphor v0.3.0. Exact binary checkpoints
replaced ANSI checkpoints while preserving old-file reads, tail replay and
accepted terminal geometry. The reader rejects version/body mismatches.

| Files | What they measure |
|---|---|
| `profile-*.jsonl` | macOS/Linux profile capture: 32 producers at 1 MiB/s, then eight without a rate limit, with attach, SSE and persistence. |
| `load-smoke.jsonl` | macOS v0.1.0 versus the candidate, each with its own PGO profile: one agents run and two alternating flat-out runs. |

macOS used Go 1.26.3; Linux arm64 used Go 1.26.10 in a four-CPU VM. Profiles
were collected without PGO and merged with the [standard procedure](../README.md#refresh-the-build-profile).
The dependency came from its public tag without a module replacement.

Flat-out smoke samples overlap: 102–127 MB/s baseline, 104–120 MB/s candidate.
They establish neither a speedup nor a regression; all completed the output
and keystroke checks. Initial labels in the smoke file were normalized from
recorded driver order; measurement values were unchanged.

See the [library report](https://github.com/2018kguo/phosphor/blob/v0.3.0/oracle/ghostty/results/2026-10-27-round3/README.md)
for emulator measurements. Current reader requirements are in
[Operating ptyd](../../docs/operating.md#persistence-and-upgrades).
