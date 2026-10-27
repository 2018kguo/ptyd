# Exact checkpoints with phosphor v0.3.0

Ptyd now requires the published phosphor v0.3.0 module and writes version 2
checkpoints with its exact binary snapshot API. Existing version 1 ANSI
checkpoints remain readable; reading one does not rewrite it. New version 2
files require this ptyd version or later. No daemon or installed binary was
restarted or replaced for this work.

Both writers use the same format: ordinary/final checkpoints and the saved
notice when an agent cannot resume. The reader checks the snapshot before
replaying tail data and resizes. A header/body version mismatch is rejected.
Owner-only daemon files retain the existing accepted geometry/storage
contract instead of inheriting the library's smaller default restore limits.

`TestExactCheckpointContinuesThroughTail` covers REP, inactive/saved screens,
outside-origin cursors, graphemes and incomplete UTF-8/CSI/OSC, followed by
actual tail data and a resize. Additional tests cover existing checkpoints,
corruption, version mismatch, accepted sparse geometry and restore notices.
The dependency was downloaded from its public tag, without a module replace.

The profile in `cmd/ptyd/default.pgo` was regenerated using the existing
`bench/pgo.sh` procedure: 32 producers at 1 MiB/s and eight flat-out producers,
with attach clients, SSE and persistence. macOS arm64 used Go 1.26.3; Linux
arm64 used Go 1.26.10 in a four-CPU VM. CPU profiles from both systems were
merged. The two `profile-*.jsonl` files describe those unoptimized collection
runs, not a before/after performance comparison.

`load-smoke.jsonl` compares v0.1.0 with the new candidate on macOS, both
using their own PGO profiles. There is one agents run per variant and two
flat-out runs, reversing order for the second pair. The first pass's labels
were normalized from the recorded driver order; measurements are unchanged.
Flat-out output spans 102-127 MB/s on the baseline and 104-120 MB/s on the
candidate. The samples do not establish an end-to-end gain or regression;
the host varied substantially. Both variants completed all keystroke/output
checks. No fleet-throughput claim follows from these smoke runs.

The emulator's compatibility, idle memory and checkpoint restore measurements
are in the [phosphor comparison](https://github.com/2018kguo/phosphor/blob/v0.3.0/oracle/ghostty/results/2026-10-27-round3/README.md).
