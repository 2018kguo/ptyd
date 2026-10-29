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

- `cmd/ptyd`: group workspace, CLI, attach behavior and real-PTY end-to-end tests.
- `internal/terminal`: groups, session lifecycle, retained output, watches and persistence.
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
worker survival after detach and workspace cancellation/typeahead. Input tests
check `jk` across read boundaries, key events, literal-input timeouts, paste
frames and terminal control sequences. Workspace tests exercise create, launch, preview, watch, human control,
detach and reattach in real PTYs. Group tests cover inheritance after moves,
legacy migration, archives, deletion retries and concurrent lifecycle changes. History tests keep completed records available inside groups and saved
Inbox records behind the history toggle. Short-window tests render the selected row
and controls; incomplete discovery must not claim that no sessions exist.
Color checks cover a headless daemon, explicit viewer preferences, truecolor
repaint and title rewriting across every input split.

The optional installed-CLI witnesses use isolated profiles and local fake
model streams. They make no real model-provider calls and do not attach to
existing user sessions:

```sh
PTYD_CODEX_TEST_BIN="$(command -v codex)" \
PTYD_CLAUDE_TEST_BIN="$(command -v claude)" \
  go test ./internal/terminal -run '^TestInstalled(Codex|Claude)ImportKeepsActiveWorker$' -v -count=1
```

The Codex check also imports from a daemon with `TERM=dumb` and `NO_COLOR`
and verifies that the viewing terminal's profile preserves foreground colors
in the attachment repaint.

Each witness holds a response active while a real native CLI renders new
output in a ptyd view. Detach must leave the same process generation and
conversation alive with one model request. The Codex witness also keeps an
original native CLI open, verifies import leaves it untouched, and closes
that owned test window before releasing the ptyd view. It disconnects the
setup RPC client too, then checks the model stream stays active for 200 ms
without a viewer or observer before querying native state again. This is a
bounded disconnect check. Native UI redraw is
not an exact capture of another terminal's past screen or partial output.

Browser tests run `ls` in real wide and narrow PTYs, observe output changes
without input, switch groups, and delete a selected group during refresh. They
verify colored Unicode cells stay inside the preview rectangle, app control
sequences do not escape into the browser, and previewing leaves source size,
process identity and human control unchanged. Piped, JSON and `--plain` output
remain snapshots.

The README demo is reproducible with `vhs docs/demo.tape`. Its setup creates
disposable local terminals, uses an ephemeral port, and removes its
own processes and files on exit. It does not need an agent login or call a
model provider.

## Check human attachments

Set `PTYD_CODEX_TEST_BIN` and `PTYD_CLAUDE_TEST_BIN` to include the installed-CLI
pane witnesses when running `go test ./internal/client`. They use isolated
profiles and loopback fixture endpoints. They do not submit real model turns.

The Codex witness checks blank background-colored cells above the ptyd status
row. The Claude witness enters an unsent Unicode draft, resizes, compares the
app's screen and cursor with the framed view, and detaches without stopping
it. The separate native-import witnesses hold a local fake model stream open
across detach. These checks cover different parts of the attachment path.

Pane tests also cover clears, alternate screens, mixed Unicode modes, palette
changes, retained history, narrow footers and mouse coordinates. Framed streams
start and recover with exact snapshots rather than ANSI reconstruction.

Native discovery tests cover loaded Codex threads without rollouts, original-tab
observations without conversation authority, same-terminal deduplication and
managed child exclusion. Attachment tests retain a failed frontend screen through
ordinary keystrokes until `jk`. Process metadata parser tests include embedded
whitespace and stop before environment fields.
