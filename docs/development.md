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
