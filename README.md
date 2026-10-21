# ptyd

`ptyd` is a small daemon that runs long-lived terminal sessions (PTYs) and
lets other programs drive them over a local HTTP/JSON API. You can spawn a
shell or any interactive program, type into it, read its output as
ANSI-stripped lines with sequence numbers, stream output over SSE, block
until a regex shows up, and resize, signal, restart or kill it. Think `tmux`
with a JSON API instead of a screen, meant for scripts, test harnesses and
tools that need to operate interactive programs.

ptyd also keeps track of what each terminal's window shows, so you can
attach to it from a real terminal (`ptyd attach <id>`), and with
`--persist-dir` the window survives a daemon restart, a `kill -9` of the
daemon and a reboot. A terminal can be restored afterwards: its command
started again, or, for Claude Code and Codex, the exact conversation it
was in resumed.

## Install

```bash
go install github.com/2018kguo/ptyd/cmd/ptyd@latest
```

This puts `ptyd` in `$(go env GOPATH)/bin` (usually `~/go/bin`). ptyd runs
on Linux and macOS.

## Quick start

```bash
export PTYD_TOKEN=$(openssl rand -hex 16)
ptyd &                      # listens on 127.0.0.1:4242

H="X-Ptyd-Token: $PTYD_TOKEN"
API=http://127.0.0.1:4242

# start a shell
ID=$(curl -sS -H "$H" -X POST $API/terminals \
  -d '{"name":"demo","command":"sh"}' | sed 's/.*"id":"\([^"]*\)".*/\1/')

# type a command ("submit" appends a carriage return, like pressing Enter)
curl -sS -H "$H" -X POST $API/terminals/$ID/write -d '{"data":"echo hi","submit":true}'

# wait for the output, then read everything
curl -sS -H "$H" -X POST $API/terminals/$ID/wait -d '{"pattern":"^hi$","timeout_seconds":5}'
curl -sS -H "$H" $API/terminals/$ID/output

# kill it
curl -sS -H "$H" -X DELETE $API/terminals/$ID
```

## API

All bodies are JSON. Errors are `{"error": "..."}` with status 400 (bad
input), 401 (missing or wrong token), 404 (unknown terminal), 409 (terminal
has exited or is a ghost, or a restore was declined) or 413 (body over 1 MiB).

| Method | Path | Body / query | Description |
| --- | --- | --- | --- |
| `GET` | `/healthz` | | Liveness probe. No auth. |
| `GET` | `/metrics` | | Counters and uptime (JSON). No auth. |
| `GET` | `/terminals` | `?tag=a,b` | List terminals, oldest first, optionally only those with any of the tags. |
| `POST` | `/terminals` | see below | Start a terminal. Returns `201` and its metadata. |
| `GET` | `/terminals/{id}` | | Metadata: status, pid, exit code, idle time, `latest_seq`, `pending`, watch events, and `restore`, `restored`, `notice`, `agent_session`. |
| `GET` | `/terminals/{id}/output` | `?since=N&tail=N` | Lines with `seq > since` (default: all retained), optionally only the last `tail`. |
| `GET` | `/terminals/{id}/stream` | `?since=N` | Server-sent events: `line` per committed line, `end` when the terminal exits. |
| `GET` | `/terminals/{id}/screen` | `?format=text\|ansi&scrollback=1` | The current window (see [Screen](#screen)). Works for ghosts. |
| `GET` | `/terminals/{id}/attach` | `?cols=&rows=&readonly=1` | Interactive attach over an upgraded connection (see [Attach](#attach)). |
| `POST` | `/terminals/{id}/write` | `{"data":"ls","submit":true}` | Write to stdin. `submit` appends `\r`. |
| `POST` | `/terminals/{id}/keys` | `{"keys":["ctrl+c","up","enter"]}` | Send named keys (see below). |
| `POST` | `/terminals/{id}/wait` | `{"pattern":"re","timeout_seconds":30,"since":-1,"before_context":3}` | Block until a line matches. |
| `POST` | `/terminals/{id}/ack` | `{"watch":"name"}` | Clear watch events (all if `watch` is omitted). |
| `POST` | `/terminals/{id}/resize` | `{"cols":120,"rows":40}` | Resize the PTY. |
| `POST` | `/terminals/{id}/signal` | `{"signal":"int"}` | Signal the process group: `int term kill hup quit usr1 usr2 winch`. |
| `POST` | `/terminals/{id}/restart` | | Kill and re-spawn with the same config. Returns `201` with a **new** id. |
| `POST` | `/terminals/{id}/restore` | `{"policy":"rerun"}` (optional) | Restore a ghost under the same id with its policy or the one given (see [Restore](#restore-after-a-restart)). `201`, or `409` with the reason if declined. |
| `DELETE` | `/terminals/{id}` | | Kill (SIGTERM, then SIGKILL after 2s) and forget. Also deletes a ghost. |
| `GET` | `/search` | `?q=re&within_seconds=N&limit=N&tag=a,b` | Regex search over the retained output of all non-ghost terminals. |

### Creating a terminal

```json
{
  "name": "worker",
  "command": "bash",
  "args": ["-l"],
  "cwd": "/tmp",
  "env": {"FOO": "bar"},
  "tags": ["build"],
  "cols": 120,
  "rows": 40,
  "watches": [
    {"name": "done", "pattern": "BUILD (OK|FAILED)", "once": true},
    {"name": "error", "pattern": "(?i)error"}
  ],
  "restore": "rerun"
}
```

Only `name` is required. `command` is looked up in the daemon's `PATH` and
defaults to `$SHELL` (or `/bin/sh`); `cwd` defaults to the daemon's working
directory; the environment is the daemon's plus `env` (with `TERM=xterm-256color`
if neither sets `TERM`); size defaults to
80x24. `restore` (`none`, `rerun` or `agent`, needs `--persist-dir`) and
`agent` are described under [Restore](#restore-after-a-restart).

### Output model

The PTY's bytes are split into lines on `\n`, with escape sequences removed.
A bare `\r` resets the current line, so progress bars collapse to their last
state. Each line gets a sequence number that only increases:

```json
{
  "lines": [{"seq": 0, "text": "hi", "at": "2026-01-01T00:00:00Z"}],
  "latest_seq": 0,
  "pending": "$ "
}
```

`pending` is the unfinished current line, typically a prompt. To poll
incrementally, pass the last `latest_seq` back as `since`. Each terminal
keeps its newest lines up to 5000 lines or `--scrollback-bytes` (default
1 MiB, counting 48 bytes of overhead per line), whichever is smaller; older
lines are dropped, and a line longer than 16 KiB is split. Exited
terminals stay listed with their output and `exit_code` until deleted.

### Screen

Every byte a program prints also goes through a terminal emulator in the
daemon, so ptyd knows the window: main and alternate screen, cursor,
colors and attributes, scroll region, the input modes the program
enabled (cursor keys, keypad, bracketed paste, mouse, focus, kitty
keyboard), the title, and up to 2000 lines / 256 KiB of scrollback.

`GET /terminals/{id}/screen` returns it as JSON:

```json
{"cols": 80, "rows": 24, "cursor_x": 2, "cursor_y": 5, "cursor_visible": true,
 "alt_screen": false, "title": "vim", "lines": ["...", "..."], "scrollback": ["..."]}
```

`scrollback` is included with `?scrollback=1`. With `?format=ansi` the
response is the byte sequence that repaints the window (and with
`?scrollback=1` the scrollback above it) on a terminal of the same size.

### Waiting

`wait` returns `{"matched":true,"match":{...},"context":[...]}` as soon as a
committed line with `seq > since` matches (default `since`: all retained
output), or when the pending line matches and output has been quiet for
250ms; such a match has `"seq": -1`. Otherwise it returns `{"matched":false}`
with `"reason"` set to `"timeout"`, `"exited"` or `"canceled"`.

### Watches

Watches are regexes checked against every new line and against a settled
pending line. Each match is recorded in the terminal's `attention` list (the
newest 100 are kept) until cleared with `ack`. A `once` watch fires a single
time until acknowledged.

### Keys

`enter`, `tab`, `esc`, `space`, `backspace`, `up`, `down`, `left`, `right`,
`home`, `end`, `pgup`, `pgdn`, `delete`, any single character (sent as-is),
and control keys as `ctrl+c`, `c-c` or `^c`.

## Attach

```bash
ptyd attach term-1-ab12            # Ctrl-\ twice to detach
ptyd attach --readonly term-1-ab12 # watch only
```

`ptyd attach` connects your terminal to a terminal in the daemon, like
`tmux attach`: it repaints the window (with its scrollback, so you can
scroll back natively), then streams output live and sends your keys. It
reads the address from `--addr` or `PTYD_ADDR` (default
`127.0.0.1:4242`) and the token from `PTYD_TOKEN`.

- **Detach:** press **Ctrl-\\ twice** within a second. A single Ctrl-\\
  is passed to the program after that second (so a shell's SIGQUIT still
  works, just delayed). It is recognized in the kitty keyboard and
  modifyOtherKeys encodings too. Ctrl-\\ is not bound by Claude Code,
  Codex, vim, less or shells beyond SIGQUIT. On detach (or when the
  program exits) your terminal is restored: raw mode off, and modes the
  program had switched on (alternate screen, mouse, bracketed paste,
  kitty keyboard, hidden cursor...) reset.
- **Size:** the most recent attacher's window size wins. Attaching
  resizes the terminal to your window, and so does each resize of it
  (SIGWINCH). `POST /resize` still works and also wins until the next one.
- **Several clients** can attach at once; all see the output and all can
  type.
- **Slow clients never slow the program:** each client has a 1 MiB output
  queue; one that falls behind is sent a fresh repaint instead of the
  missed bytes.
- **Ghosts** (see below) attach read-only and show their saved window.

The protocol is `GET /terminals/{id}/attach` with `Connection: Upgrade`,
`Upgrade: ptyd-attach` (and the token header). After `101 Switching
Protocols` the server sends raw terminal output (EOF when the program's
output ends); the client sends frames of a type byte, a 4-byte big-endian
length and a payload: `i` with input bytes, or `r` with cols and rows as
two big-endian uint16s.

## Persistence

With `--persist-dir DIR`, each terminal has a directory `DIR/<id>/`
(mode 0700, files 0600):

| File | Contents |
| --- | --- |
| `meta.json` | Create config (including `env`), restore policy, pid and process identity, restore count. |
| `screen.ckpt` | Checkpoint: a JSON header line, then the bytes that repaint the screen and scrollback. Written atomically (temp file, fsync, rename). |
| `tail.<gen>` | Everything printed (and every resize) since the checkpoint, as length-prefixed records. |
| `exit.json` | Why the terminal is not running: `child_exit`, `shutdown`, or `not_restored` with the notice. |
| `agent-session.json` | Agent policy only: the session the agent is in, written by its hooks. |

The checkpoint plus the tails replayed through a fresh emulator give the
exact window, also after `kill -9` of the daemon: tails are appended on
every read from the PTY (not fsynced; they survive a process crash, a
power loss can lose the last moments). A torn last record, or output cut
in the middle of an escape sequence, is tolerated.

Bounds: a checkpoint is taken when output has been quiet for 1s (at most
every 15s), when a tail passes 4 MiB, when the program exits, and on a
graceful shutdown (before the program is stopped, so what it prints on
its way out is not saved). So disk use per terminal stays under about
8.5 MiB, replay after a crash reads at most 8 MiB, and writes are the
output itself plus at most one checkpoint (≤ ~300 KiB) per 4 MiB of
output or per 15s of activity. An idle terminal has no open file and no
timer. Directories are deleted with the terminal (`DELETE`), and ghosts
untouched for 7 days are pruned at startup.

After a restart, every terminal in the directory is listed. Those not
restored are **ghosts**: `status: "exited"`, metadata, `GET /screen` and
read-only attach work; input does not.

## Restore after a restart

The `restore` field picks what happens to a terminal when the daemon comes
back after a crash, `kill -9`, graceful stop or reboot:

| Policy | On startup |
| --- | --- |
| `none` (default) | Stays a ghost showing its saved window. |
| `rerun` | The same command, args, cwd and env start again under the **same id**, below the saved window, which stays in view (like zellij's resurrection). |
| `agent` | The exact agent session the terminal was in is resumed under the same id, or, if that cannot be done with certainty, it stays a ghost with a notice on its last screen. |

Only terminals that were running when the daemon went away are restored.
One whose program ended by itself stays a ghost, and so does one whose
restore was declined, so a crash loop never restores a terminal twice or
repeats a notice. `POST /terminals/{id}/restore` restores a ghost on
demand, with its policy or with `{"policy": "rerun"|"agent"}` (which then
becomes its policy). A restored terminal keeps its id; `restored` counts
restores, `restored_at` is the last one, and `notice` says what was done
(or why not).

Before starting anything, a restore stops what is left of the previous
run's process group, but only if it is provably ours (its leader is gone,
or is the very process ptyd started: same pid, boot and start time), and
declines if it cannot. Two copies of a program never run for one
terminal.

### Agent sessions

`"restore": "agent"` works for **Claude Code** (`claude`) and **Codex**
(`codex`), detected from the command's name or set with `"agent"`. ptyd
adds two hooks to the agent's own hook system when it launches it: a
SessionStart hook that records the session id and transcript path each
time a session starts, is resumed, cleared or compacted, and a
UserPromptSubmit hook that marks that a message was sent (the prompt text
is discarded). So ptyd always knows the session the agent is in *now*,
including after `/clear` or `/resume` in Claude Code and `/new` in Codex.

- Claude Code: launched with `--session-id <new uuid>` and the hooks in
  `--settings`, which Claude Code merges with your user, project and
  plugin settings (your own hooks keep running). Resumed with
  `claude --resume <id>`.
- Codex: the hooks are passed as `-c` session overrides together with
  their trust hashes, so they run without a review prompt and your
  `config.toml` (including your own hooks and `notify`) is untouched.
  Resumed with `codex resume -- <id>`. Codex prints a startup warning
  that it runs without its shared background server, which `-c` overrides
  require.

Guarantees on restore:

- The agent is relaunched only with the exact current session id, only if
  the id is well formed, the session ran in the terminal's directory, and
  its transcript file exists. `--continue`, `--last`, a bare `--resume`
  or any picker are never used.
- If the session never got a message (Claude Code after `/clear` with
  nothing typed yet), there is no transcript and nothing to lose: a new
  session starts in the same directory.
- Otherwise (no session recorded, hooks disabled, transcript gone, other
  directory) the agent is **not** started. The terminal stays a ghost
  showing its last screen, with a line such as
  `ptyd: could not identify the claude session to resume; showing last screen`.

Limits:

- Codex creates a conversation only when its first message is sent. A
  Codex terminal that never got a message has no session, which ptyd
  cannot tell from hooks that did not run, so it stays a ghost. After
  `/new` and before the first message of the new conversation, the latest
  existing conversation is the one before `/new`, and that is what is
  resumed.
- Arguments must be long options written `--name` or `--name=value`
  (for Codex, `-c key=value` becomes `--config=key=value`): a positional
  prompt would be sent again on every restore, and `--name value` cannot
  be told apart from one. Flags that pick or skip sessions or transcripts
  (`--continue`, `--resume`, `--session-id`, `--settings`, `--print`,
  `--bare`, `--worktree`, `--last`, `--cd`, ... ) are refused at create
  time. Send the first prompt with `POST /write`.
- `POST /restart` starts a new session (it is a new terminal).
- Claude Code redraws its whole UI when resumed, so the old window is in
  the restored terminal's scrollback only until Claude Code clears it.

Not supported: grok, cursor-agent, amp, opencode, pi and other agents.
Each has a way to resume a session by id, but ptyd ships an adapter only
when it can track the current session through in-app session changes and
the result is verified end to end against the real CLI; for these that has
not been done, so `"restore": "agent"` refuses them. `"restore": "rerun"`
works for any command (it starts a new session).

`scripts/verify-agent-resume.sh claude|codex crash|graceful [project-dir]`
runs that end-to-end check against an installed, logged-in agent: prompt,
switch session in the app, prompt again, kill -9 (or stop) the daemon,
restart it, and check that the exact latest session was resumed and a
further prompt lands in its transcript. It sends three one-word prompts.

## Flags

| Flag | Env | Default | Description |
| --- | --- | --- | --- |
| `--bind` | `PTYD_ADDR` | `127.0.0.1:4242` | Listen address. |
| `--token` | `PTYD_TOKEN` | none | If set, every request except `/healthz` and `/metrics` must send it in the `X-Ptyd-Token` header. |
| `--persist-dir` | `PTYD_PERSIST_DIR` | none | Keep each terminal's metadata and window here so they survive restarts (see [Persistence](#persistence)), and restore terminals on startup by their policy. |
| `--log-dir` | `PTYD_LOG_DIR` | none | Mirror each live terminal's raw output to `<id>.log` (capped at 32 MiB, wraps; removed when the terminal ends). |
| `--scrollback-bytes` | | `1048576` | Memory budget per terminal for retained output lines. |

On SIGINT or SIGTERM, ptyd stops accepting requests, ends open streams and
waits, checkpoints every terminal's window, terminates every terminal's
process group and exits.

## Scaling

A terminal costs memory in proportion to the output it retains, never more
than its budgets: `--scrollback-bytes` of lines, plus the screen model
(16 bytes per cell, rows allocated once drawn on, the alternate screen only
while in use) and its scrollback (at most 2000 lines / 256 KiB). So
worst-case memory is about terminals × (`--scrollback-bytes` + ~300 KiB at
80x24). An idle terminal costs roughly 10–20 KiB of heap and no CPU:
nothing polls, watches and waits sleep until output arrives, and
persistence arms a timer only while output is pending. Measured with 1000
idle terminals on Linux: ~13.5 KiB heap, 2.5 goroutines, 1 OS thread and
2 file descriptors per terminal, and about 0.2 ms of CPU per second in
total. The screen model added ~2 KiB per idle terminal (Linux, 1000
terminals: 11.1–12.1 → 13.4–13.6 KiB; macOS, 400: 18.7 → 20.5 KiB; idle
CPU unchanged).

Limits to know about when running thousands of terminals:

- **File descriptors:** 2 per terminal on Linux (the PTY and a pidfd), 1 on
  macOS, plus 1 with `--log-dir`. Go raises the soft `RLIMIT_NOFILE` to the
  hard limit at startup, so raise the hard limit (`LimitNOFILE=` in the
  systemd unit) if it is low.
- **PTYs:** Linux allows 4096 by default (`/proc/sys/kernel/pty/max`);
  macOS only 511 (`sysctl kern.tty.ptmx_max`).
- **Threads:** each terminal holds one OS thread waiting for its process
  to exit (two on macOS, where PTY reads block too). Go aborts above 10000
  threads, which caps a daemon at roughly 9000 terminals on Linux and 4500
  on macOS.
- **Processes:** every terminal is at least one process, so the user's
  process limit (`ulimit -u`) applies.

### Performance

Measured on an Apple M5 Pro, on macOS and in Docker on the same machine
(Linux arm64, 8 CPUs). [`bench/README.md`](bench/README.md) has the
method, the results and the cost of every route and loop.

- **Emulator:** 300–400 MB/s per core on agent TUI, log and vim output,
  ~850 MB/s on progress bars; redrawing a frame allocates nothing.
  Everything one PTY read goes through (screen, line ring, attached
  clients, persisted tail) runs at 250–310 MB/s.
- **Under load:** 32 terminals each printing 1 MiB/s of agent output,
  each attached and streamed, with `--persist-dir`: 1.5 cores on Linux
  (2.6 on macOS); a keystroke typed through `attach` is echoed back in
  0.23 ms at the median and 0.55 ms at p99 on Linux (0.12 and 0.6 ms on
  macOS); one GC cycle per second; 1.03 bytes written to disk per byte of
  output.
- **Flat out:** 8 terminals printing as fast as they can reach ~195 MB/s
  on Linux and ~85 MB/s on macOS, where a PTY read returns at most 1 KiB
  and each read is also a write to the tail.
- **Restore:** ~0.6 ms per terminal on Linux and ~10 ms on macOS (where
  fsync flushes the drive), 16 at a time at startup.

Reproduce with `bench/run.sh` (benchmarks), `bench/ab.sh` (A/B against a
revision) and `bench/load.sh` (end-to-end load test, build tag
`loadtest`). The idle-terminal benchmark spawns `PTYD_SCALE_N` terminals
(default 1000, 400 on macOS).

## Security

ptyd runs arbitrary commands as the user it runs as. Anyone who can reach
the port can do the same.

- Keep it bound to `127.0.0.1` (the default). ptyd warns at startup if it
  listens elsewhere without a token.
- Set a token whenever other users or containers share the host. Prefer
  `PTYD_TOKEN` over `--token`, which is visible in `ps`.
- `/healthz` and `/metrics` never require the token; metrics include the
  configured directory paths and terminal counts.
- The token is a shared secret over plain HTTP. For remote access, tunnel
  (SSH, WireGuard) rather than exposing the port.
- Persisted metadata (including `env`), saved windows and output tails
  (which can contain anything a program printed, secrets included) and
  log mirrors are written owner-only (0600, in 0700 directories), deleted
  with the terminal, and pruned 7 days after a ghost last changed.
- Agent restore runs hook commands inside the agent (plain `sh` writing
  only into the terminal's directory) and, for Codex, marks exactly those
  hooks as trusted for that session.

## Running as a service

- **systemd (Linux):** [`infra/systemd/ptyd.service`](infra/systemd/ptyd.service)
  is a user unit:
  `cp infra/systemd/ptyd.service ~/.config/systemd/user/ && systemctl --user enable --now ptyd`.
- **launchd (macOS):** [`infra/launchd/com.ptyd.plist`](infra/launchd/com.ptyd.plist)
  is a user agent. Fill in your home directory and load it:

  ```bash
  sed "s|__HOME__|$HOME|g" infra/launchd/com.ptyd.plist > ~/Library/LaunchAgents/com.ptyd.plist
  mkdir -p ~/Library/Logs/ptyd
  launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.ptyd.plist
  ```

Services start with a minimal `PATH`; set it in the unit so the commands you
spawn can be found. To use a token, add `Environment=PTYD_TOKEN=...` (systemd)
or a `PTYD_TOKEN` entry under `EnvironmentVariables` (launchd).

## Command-line helper

[`scripts/ptyd-ctl.sh`](scripts/ptyd-ctl.sh) wraps the API (needs `bash`,
`curl` and `python3`; uses `jq` if present). It reads `PTYD_API` (default
`http://127.0.0.1:4242`) and `PTYD_TOKEN`.

```bash
scripts/ptyd-ctl.sh create demo "$PWD" sh     # prints the new terminal
scripts/ptyd-ctl.sh send   term-1-abcd 'echo hello'
scripts/ptyd-ctl.sh wait   term-1-abcd '^hello$' 10
scripts/ptyd-ctl.sh output term-1-abcd
scripts/ptyd-ctl.sh keys   term-1-abcd ctrl+c
scripts/ptyd-ctl.sh close  term-1-abcd
scripts/ptyd-ctl.sh help                     # all commands
```

## Development

```bash
go test -race ./...
```

The end-to-end tests in `cmd/ptyd` build the binary and `kill -9` it.
Agent restore is covered with fake `claude` and `codex` binaries (the
test binary behind symlinks); no agent needs to be installed. The live
check against real agents is `scripts/verify-agent-resume.sh`.
