<h1 align="center">ptyd</h1>
<p align="center">Terminals that agents can drive and people can take over.</p>
<p align="center">
  <a href="#use-it">Get started</a> ·
  <a href="#what-an-agent-gets">Why ptyd</a> ·
  <a href="docs/reference.md">Reference</a> ·
  <a href="docs/operating.md">Operating ptyd</a>
</p>

An agent can leave a build running, return on its next turn and read what
changed. It can answer a prompt in the same shell or inspect an editor's
screen. You can attach to that session and type normally.

Ptyd owns the PTY and process, so they keep running when a tool call ends or
a client disconnects. The CLI and local HTTP API control the same sessions.
After a daemon restart, ptyd brings back the saved screen and can restart
the command or resume a supported agent's conversation.

![Switching between running terminals with Vim-style controls](docs/demo.gif)

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
- **One overview for many sessions.** See watch matches, process status,
  idle time and directories together. Use a live selector yourself or a
  compact JSON snapshot in an agent tool.

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

Type **jk** quickly (j, then k) to detach. Open the session picker with:

```sh
ptyd ui
```

Use `j/k` to move, Enter or `i` to open, and quick `jk` to return.
`/` searches; `h` shows saved and completed sessions. The picker starts with
live sessions, grouped by open terminals, agents and those needing a handoff.
Your terminal's window title shows the attached session and the `jk` hint.
Press `?` for controls or `q` to leave; work keeps running.

Or start a command in the background:

```sh
ptyd new -d -n build -- make test
ptyd attach build
ptyd kill build
```

Names, ids, unique id prefixes and terminal numbers all work.

## See what needs attention

```sh
ptyd status                 # attention-first snapshot
ptyd status --json          # structured overview for an agent
ptyd status --watch         # live selector; Enter attaches
```

`ptyd ui` opens the live view with native discovery enabled and history hidden.
Completed work remains available with `h`; opening it shows its last screen
until you return with `jk`. This does not restart the command. The selection
follows its session id as rows reorder. Use `:b name` to attach to a shown
session, `:bn`/`:bp` for the next/previous one, and `:ls` to clear the search.
The controls follow Vim/Helix's modal pattern, including the documented
[`jk` mapping](https://docs.helix-editor.com/remapping.html).

Attention means a configured watch matched output. Running and idle time
are process observations; they do not classify an agent's thoughts.
The default overview covers sessions managed by this daemon. Add `--discover`
to include native Codex and Claude sessions started in other tabs.

Agents can call `GET /overview` with a row limit, text filter and tags.
Counts cover every matching session; `truncated` says when rows were omitted.
Each row includes an output cursor and a short preview with its own
truncation flag. [The reference](docs/reference.md#session-overview) explains
the fields. The live interaction was informed by
[AgentView](https://github.com/moritzWa/agentview).

## Bring existing agents into one pane

```sh
ptyd agents                         # native sessions and what can be imported
ptyd ui                             # managed terminals and native agents together
```

Select an outside session and press Enter. Ptyd opens a terminal view using
that agent's native attach API, with its exact conversation id. Quick `jk`
returns to the picker and releases the view; the native worker keeps running.
Your original tab stays open until you close it yourself. New views use your
terminal's color settings, even when the daemon runs as a headless service.
An intentional `NO_COLOR` setting is respected.

Codex sessions must be loaded in its shared local daemon. For a foreground
Claude session, run `/bg` in its original tab and let Claude finish its own
handoff, then refresh the picker. A stopped Claude conversation requires
an explicit `ptyd import --resume` using the reference printed by `ptyd agents`.

Ptyd rechecks native identity around view creation. It refuses incomplete or
ambiguous listings and never resumes “the latest” conversation. Native CLIs
redraw their own history; the original tab's terminal scrollback is not
transferred. See [import behavior and limits](docs/reference.md#import-native-agent-sessions).

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

HTTP makes the same operations available to shell scripts, test harnesses
and agent tools without a ptyd SDK. The useful parts for agents are named
sessions, output cursors, waits, watches and screen reads. A tool can expose
those as small actions; the model does not need to construct curl commands.

Use `wait`, watches or SSE to avoid polling, and `since` to read only new
output. Interactive attach uses a persistent byte stream with input and
resize frames. The [shell helper](scripts/ptyd-ctl.sh) wraps common calls.

HTTP, JSON and local TCP add overhead. Ptyd does not currently support Unix
sockets, and its benchmarks do not compare control transports. HTTP was
chosen for integration, not a measured speed advantage.

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

[Benchmarks](bench/README.md) explain the current measurements, their limits and how to rerun them.
The emulator's small idle footprint is only one part of total daemon memory.

## Documentation

- [API and CLI reference](docs/reference.md): requests, output, keys and attach protocol
- [Operating ptyd](docs/operating.md): restore, persistence, authentication, services and scale
- [Benchmarks](bench/README.md): measurements, tradeoffs and reproduction
- [Development](docs/development.md): tests, load profiles and live agent checks

## License

MIT.
