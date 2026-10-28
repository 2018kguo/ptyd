# ptyd

**Long-lived terminals, controlled through a local HTTP API.**

Start a shell, editor or coding agent once. Drive it from a script, wait for
output, inspect its screen, or attach and type normally. Disconnecting a
client leaves the session running. After a daemon restart, ptyd brings back
the saved screen and can restart the command or resume a supported agent's
conversation.

![An agent running under ptyd, driven over HTTP and resumed after the daemon restarts](docs/demo.gif)

## Use it

```sh
go install github.com/2018kguo/ptyd/cmd/ptyd@latest
ptyd &
ptyd new
```

Linux and macOS; builds require Go 1.26.2 or later. The daemon listens on
`127.0.0.1:4242`. Commands find its saved token automatically.

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

## Why use it

- **Automation and manual use share a session.** A script can start a
  program, send keys and wait for a prompt; you can attach whenever you
  want to take over.
- **The screen is available as data.** The [phosphor](https://github.com/2018kguo/phosphor)
  emulator tracks cursor movement, colors and both screen buffers, so
  programs such as vim can be inspected as a window rather than a log.
- **A lost connection doesn't end the work.** Sessions stay in the daemon;
  saved windows and output tails survive a daemon restart.
- **Busy sessions have bounded queues.** Retained output has a byte budget.
  Slow attachers receive a fresh screen instead of blocking the program.
  Busy writes are batched; small echoes are sent immediately.

If you only need an interactive terminal multiplexer, tmux already does
that well. Choose ptyd when programs also need to inspect and control the
sessions through an API. If you only need an embedded emulator, use
[phosphor](https://github.com/2018kguo/phosphor) directly.

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
