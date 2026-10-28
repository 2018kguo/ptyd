# Development

The normal test suite uses disposable terminals and fake agent binaries.
It does not need a logged-in agent:

```sh
go build ./...
go vet ./...
go test -race ./...
```

CLI tests start real PTYs and exercise daemon crashes. When testing in
Docker, use `--init` so orphaned test children are reaped.

- `cmd/ptyd`: CLI, attach behavior and end-to-end tests.
- `internal/terminal`: session lifecycle, retained output, watches and persistence.
- `internal/api`, `internal/client`: HTTP server and client.
- `internal/native`: read-only Codex/Claude discovery and exact import references.
- [bench/](../bench/README.md): maintained measurement tools, scope and archived evidence.

Keep run output under ignored `.bench/`. Fixtures and executable measurement
tools belong in source; publish checksummed result bundles separately. Add tests
to the existing subject suite instead of a numbered or per-patch file.

A live agent check is separate from the local suite. It needs an installed,
logged-in Claude Code or Codex and sends three one-word prompts:

```sh
scripts/verify-agent-resume.sh claude crash /path/to/project
scripts/verify-agent-resume.sh codex graceful /path/to/project
```

It checks that a session changed inside the app resumes by its exact current
id after the daemon restarts. Do not infer live-provider behavior from the
fake-agent tests alone. Emulator changes belong in
[phosphor](https://github.com/2018kguo/phosphor).

## Verify native imports

Ordinary tests use fake native CLIs, a Unix WebSocket fixture and real local
PTYs. They cover exact identity, partial discovery, concurrent imports,
process-generation changes, foreground handoff refusal, persistence, native
worker survival after detach and picker cancellation/typeahead.

The optional installed-CLI witnesses use isolated profiles and local fake
model streams. They make no real model-provider calls and do not attach to
existing user sessions:

```sh
PTYD_CODEX_TEST_BIN="$(command -v codex)" \
PTYD_CLAUDE_TEST_BIN="$(command -v claude)" \
  go test ./internal/terminal -run '^TestInstalled(Codex|Claude)ImportKeepsActiveWorker$' -v -count=1
```

Each witness holds a response active while a real native CLI renders new
output in a ptyd view. Detach must leave the same process generation and
conversation alive with one model request. The Codex witness also keeps an
original native CLI open, verifies import leaves it untouched, and closes
that owned test window before releasing the ptyd view. It disconnects the
setup RPC client too, then checks the model stream stays active for 200 ms
without a viewer or observer before querying native state again. This is a
bounded disconnect check. Native UI redraw is
not an exact capture of another terminal's past screen or partial output.
