<h1 align="center">ptyd</h1>
<p align="center">A workspace for your agents. A terminal when you need one.</p>
<p align="center">
  <a href="#get-started">Get started</a> ·
  <a href="#work-with-agents">For agents</a> ·
  <a href="docs/reference.md">Reference</a> ·
  <a href="docs/operating.md">Operating ptyd</a>
</p>

Put a piece of work in a group. Start Codex, Claude Code, shells and commands
inside it. Come back to see what is running, preview a terminal or take over
the keyboard. Detach, close your laptop's terminal window, and return later.
Work keeps running while the daemon and its processes stay alive.

Ptyd combines a terminal workspace for people with a local API for agents.
Both use the same groups, terminals and output. An agent can launch work,
wait for a result and read only what changed; you can enter that same
terminal whenever you want.

![Entering a group, switching terminals and leaving work running](docs/demo.gif)

## Get started

```sh
go install github.com/2018kguo/ptyd/cmd/ptyd@latest
ptyd
```

The local daemon starts automatically. Press **n** to name a group, then **n** inside it to launch
Codex, Claude, a shell or a custom command. The built-in Codex and Claude
choices use **full access (YOLO)** by default. Press **f** in the launch menu
to use your CLI permission settings instead. The choice stays with that browser.

Or start an agent with the command and flags you choose:

```sh
ptyd new my-project -- codex
ptyd ls
ptyd attach my-project
```

Linux and macOS. The build toolchain is declared in [go.mod](go.mod).
The default daemon address is `127.0.0.1:4242`; `PTYD_ADDR` selects another.

## Find your way around

Running `ptyd` or `ptyd ls` opens your groups. Move with `j/k` to see a
group's terminals and live screen preview before entering it. Inside a group,
press **v** to pin a terminal beside the list. Pin up to three to follow several
agents at once while moving between them. Pins survive leaving and reopening
the browser. Previews keep
application colors and backgrounds; smaller windows stack the preview below
the list. Previews keep application footers and input boxes in view. Pane edges
show when rows above or columns to the right are hidden; Enter opens the terminal.
Finished screens stay available without being restarted.

Use `ptyd ls --plain` for a table that exits, or `--json` for structured output.
Piped output stays plain text. Older ungrouped terminals live in
**Inbox**, behind **h** when none are running.

| Where | Keys |
|---|---|
| Groups | `j/k` select · Enter open · `n` create · `h` history |
| Inside a group | `j/k` select · Enter take control · `v` pin/unpin pane · `n` launch |
| Finished terminal | `R` launch again · Enter view history |
| Anywhere | Quick `jk` goes back: terminal → group → groups → shell |
| Workspace | `/` search · `?` help · `q` quit |

`jk` also dismisses menus and cancels prompts. Use slower `j` and `k` to move
down and up; pasted text stays literal. **●** running, **◉** human control,
**!** attention and **◆** pinned give each view a quick visual cue. A small
ASCII cube rotates in the header; use `:motion` or set `PTYD_REDUCED_MOTION=1`
for a still header.

An attached terminal keeps **group / terminal** and the return hint in its
bottom row, including when an app clears the screen. The app keeps its colors,
backgrounds and cursor. A read-only view says “viewing”; an ended one says
“finished.” Only one human has keyboard control at a time. Agent input is refused
while you have control; observation remains available.

When an attached program exits, you return to its group with the finished
terminal selected. Press **R** to launch it again, or Enter to read its last
screen. History views stay open until **jk**.

Rename with **r**, move a terminal with **m**, and close with **x**. Closing
requires confirmation. Archive finished groups with **a** in the group list.

## Work with agents

Ask your agent to use ptyd and choose a group for the work. A managing agent
is an ordinary terminal in that group: it can start workers, inspect output
and wait for results. You can browse those same workers without requiring
a handoff format or a task workflow.

```sh
ptyd new -d research
ptyd term new -d --group research -n tests -- make test
ptyd status --json --group research
ptyd attach research
```

Terminals receive their daemon address, terminal id and group context.
Commands started inside one inherit its current group, including after a
move. Use `--group` to choose another. Full terminal ids keep working across
groups; names resolve within the chosen group.

The HTTP API keeps the operations useful to agents:

- **Incremental output:** numbered lines and a `since` cursor avoid rereading
  the transcript. Tail or search retained output when needed.
- **Waits and watches:** wait for a regex or settled prompt; record matches
  for later attention without a polling loop.
- **Terminal input:** submit text with bracketed paste and Enter, or send
  named keys. Human keyboard control returns a clear conflict to automation.
- **Screens:** inspect what an editor or terminal UI currently shows through
  `/screen`, alongside text output for builds and logs.
- **Groups:** create, rename, archive and inspect work through `/groups`;
  use `group_id` when creating or moving terminals.

Existing terminal ids and HTTP routes remain valid. Ungrouped API creations
enter Inbox. The CLI puts individual-terminal operations under `ptyd term`;
`new`, `ls` and `attach` operate on groups. See the [reference](docs/reference.md)
for requests, JSON results and controls.

## Bring existing agents along

Open a group and press **a** to discover agents started outside ptyd.
Enter imports a supported running conversation into that group. A stopped
conversation requires explicit confirmation before resuming. Imported native
workers keep their own owner; ptyd opens a frontend for them.

Claude foreground sessions need `/bg` in their original tab before import.
Codex needs saved history and a verified route through its shared local daemon.
Some standalone original tabs cannot move live. Keep those open and start
future agents inside a ptyd group. Discovery shows the reason when a session
cannot be imported. [Import details](docs/reference.md#import-native-agent-sessions)
explain identity checks and limits.

## What persists

Groups, membership and saved terminal screens survive daemon restarts.
Detaching leaves processes running. Restarting the daemon starts new processes
according to each terminal's restore policy; it does not preserve process RAM.
Supported agents can resume an exact recorded conversation. Custom commands
launched from the workspace are not automatically rerun.

The [operating guide](docs/operating.md) covers restore policies, backups,
services and resource limits. Run `ptyd serve` for a foreground daemon.

## Why ptyd

Choose ptyd when people and agents need to share persistent terminal work.
The human workspace groups the work; output cursors, waits, watches and screen
reads make that work accessible to programs. If you need an embedded emulator,
use [Phosphor](https://github.com/2018kguo/phosphor) directly.

HTTP makes the same operations accessible from scripts and agent tools without
an SDK. Attach uses a persistent stream. HTTP and JSON add overhead; ptyd does
not claim a speed advantage over other control transports or support Unix
sockets today. [Benchmarks](bench/README.md) explain what has been measured.

Keep the daemon on loopback or use a private tunnel: its token grants command
execution as your user. Human views reserve a status row and render through
Phosphor; `ptyd term attach --raw` provides direct output for features outside
that model, including terminal graphics.

[Development and verification](docs/development.md) · [MIT license](LICENSE)
