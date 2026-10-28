# ptyd

**Terminals that agents can drive and people can take over.**

An agent can leave a build running, return on its next turn and read what
changed. It can answer a prompt in the same shell or inspect an editor's
screen. You can attach to that session and type normally.

Ptyd owns the PTY and process, so they keep running when a tool call ends or
a client disconnects. The CLI and local HTTP API control the same sessions.
After a daemon restart, ptyd brings back the saved screen and can restart
the command or resume a supported agent's conversation.

![An agent running under ptyd, driven over HTTP and resumed after the daemon restarts](docs/demo.gif)

## What an agent gets

- **A session to return to.** Start a program once and address it by name
  or id on later turns. A shell keeps its working directory and environment
  while its process runs.
- **Only the output it needs.** Numbered lines and a `since` cursor let a
  caller read new output without feeding the whole transcript back into
  context. Request a short tail or search retained output across sessions.
- **Waiting with a result.** `wait` blocks for a regex match, including a
  settled prompt without a newline, and reports a match, timeout, exit or
  cancellation. Watches record named matches for later inspection.
- **Input with terminal meaning.** Submit text followed by Enter, with
  bracketed paste when the program enables it, or send named keys such as
  `ctrl+c`, `up` and `enter`. The agent can answer a question or interrupt
  a command in the same session.
- **A screen for full-screen programs.** Read the window an editor or TUI
  shows through `/screen`; use line output for builds and logs. The
  [phosphor](https://github.com/2018kguo/phosphor) emulator tracks cells,
  cursor movement and both screen buffers.
- **Manual takeover.** Attach with the CLI, work in the same terminal, then
  detach while the program keeps running.

If you only need an interactive terminal multiplexer, tmux already does
that well. Choose ptyd when programs also need to inspect and control the
sessions through an API. If you only need an embedded emulator, use
[phosphor](https://github.com/2018kguo/phosphor) directly.

## Use it

```sh
go install github.com/2018kguo/ptyd/cmd/ptyd@latest
ptyd &
ptyd new
```

Linux and macOS; the build toolchain is declared in [go.mod](go.mod).
The daemon listens on `127.0.0.1:4242`. Commands find its saved token automatically.

Press **Ctrl-\\ twice** to detach. Come back with:

```sh
ptyd ls
ptyd attach 1
```

Or start a command in the background:

```sh
ptyd new -d -n build -- make test
ptyd attach build
ptyd kill build
```

Names, ids, unique id prefixes and terminal numbers all work.

## Drive a terminal from a script

The CLI and HTTP API control the same sessions. For example:

```sh
ID=$(ptyd new -d -n demo -- sh)
API=http://127.0.0.1:4242
TOKEN=$(ptyd token)

curl -sS -H "X-Ptyd-Token: $TOKEN" -X POST "$API/terminals/$ID/write" \
  -d '{"data":"echo hello","submit":true}'
curl -sS -H "X-Ptyd-Token: $TOKEN" -X POST "$API/terminals/$ID/wait" \
  -d '{"pattern":"^hello$","timeout_seconds":5}'
curl -sS -H "X-Ptyd-Token: $TOKEN" "$API/terminals/$ID/screen"
ptyd attach "$ID"
```

Use `/output` for numbered text lines, `/stream` to follow them over SSE,
and `/screen` for the window an editor or terminal UI currently shows.
Watches record matches without a polling loop. The
[API reference](docs/reference.md) has the routes and request formats;
[`ptyd-ctl.sh`](scripts/ptyd-ctl.sh) wraps them for shell scripts.

## Why HTTP

HTTP lets a shell script, test harness or agent tool call the daemon with
the HTTP client it already has. JSON results are inspectable with curl, and
clients need no ptyd SDK. The CLI is a convenience over the same operations.

Agent ergonomics come from the operations above. A tool wrapper can expose
them as small actions; it does not need to make the model compose curl.
In a repo clone, the shell helper already wraps common actions:

```sh
scripts/ptyd-ctl.sh send "$ID" 'echo helper-ready'
scripts/ptyd-ctl.sh wait "$ID" '^helper-ready$' 5
scripts/ptyd-ctl.sh output "$ID" -1 20
```

Use `wait`, watches and SSE for output instead of repeated polling.
For repeated prompts, use the HTTP `since` filter to exclude old lines;
the [agent-turn guide](docs/reference.md#keep-context-between-agent-turns)
explains the cursor.
Interactive attach upgrades one connection to a persistent byte stream,
with frames for input and resize.

HTTP parsing, JSON and local TCP have a cost; spawning curl for every call
adds process overhead too. A Unix socket could carry the same HTTP API and
use filesystem permissions, but ptyd currently listens on TCP. A dedicated
binary protocol would need its own clients. The benchmarks do not compare
these transports, so they do not establish HTTP as the fastest choice.

## What restoring means

A restore keeps the saved screen and starts a new process. Normal commands
can run again; **Claude Code and Codex** can resume the recorded conversation.
If ptyd cannot identify that conversation safely, it leaves the terminal
stopped with its last screen and a notice.

`ptyd new` defaults to automatic restore; the HTTP create route defaults to
no automatic restore. Choose explicitly with `--restore` or the API's
`restore` field. [Operating ptyd](docs/operating.md#restore-policies) explains
the policies, agent argument restrictions and checkpoint compatibility.

## Tradeoffs to know

- **The token grants command execution as your user.** Keep the daemon on
  loopback and tunnel remote access. `/healthz` and `/metrics` are public.
- **It preserves terminal state, not a process's RAM.** Commands may repeat
  their startup work after a restore. Agent resume supports Claude Code
  and Codex; other agents can be restarted as ordinary commands.
- **Many sessions still mean many OS resources.** Each running session has
  a process, PTY, file descriptors and waiting threads. Output retention
  and editor screens also consume memory.
- **Exact checkpoints and ANSI attachment differ.** Checkpoints preserve
  emulator state; attached terminals inherit ANSI export limits. Phosphor
  favors a small headless model and does not implement terminal graphics.

[Benchmarks](bench/README.md) explain what was measured and how to rerun it.
The emulator's small idle footprint is only one part of total daemon memory.

## Docs

- [API and CLI reference](docs/reference.md): requests, output, keys and attach protocol
- [Operating ptyd](docs/operating.md): restore, persistence, authentication, services and scale
- [Benchmarks](bench/README.md): measurements, tradeoffs and reproduction
- [Development](docs/development.md): tests, load profiles and live agent checks

## License

MIT.
