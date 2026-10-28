# Phosphor v0.4.0 profile capture

The current ptyd build profile combines these four load runs:
[macOS](load-mac.jsonl) and [Linux](load-linux.jsonl), each with 32 producers
at 1 MiB/s and eight without a rate limit. Every producer has one attach
client, one SSE client and persistence. Runs last ten seconds; producers
are built with PGO disabled.

These are profile and integration smoke samples, not an alternating
before/after performance comparison or a latency SLO. See the
[benchmark guide](../README.md) for reproduction and interpretation.

macOS: Apple M5 Pro arm64, Go 1.26.3. Linux: arm64 Docker VM, Go 1.26.10,
with init enabled to reap orphaned test children. Profile source:
`7a2e8f2f76e4b85a342ee87241e9738c7a39fad3`; later library changes affect
only cold ANSI formatting and tests/docs. Capture used a local modfile
pointing to those sources; the released dependency uses the public tag.

The [library report](https://github.com/2018kguo/phosphor/blob/v0.4.0/oracle/ghostty/results/2026-10-27-round6/README.md)
contains emulator memory, throughput and ANSI limits. The new inner snapshot
format and older-checkpoint reader tests are covered in
[Operating ptyd](../../docs/operating.md#persistence-and-upgrades).
