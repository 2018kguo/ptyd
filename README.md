# ptyd

`ptyd` is a small daemon that runs long-lived terminal sessions (PTYs) and
lets other programs drive them over a local HTTP/JSON API. You can spawn a
shell or any interactive program, type into it, read its output as
ANSI-stripped lines with sequence numbers, stream output over SSE, block
until a regex shows up, and resize, signal, restart or kill it. Think `tmux`
with a JSON API instead of a screen, meant for scripts, test harnesses and
tools that need to operate interactive programs.

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
has exited or is a ghost) or 413 (body over 1 MiB).

| Method | Path | Body / query | Description |
| --- | --- | --- | --- |
| `GET` | `/healthz` | | Liveness probe. No auth. |
| `GET` | `/metrics` | | Counters and uptime (JSON). No auth. |
| `GET` | `/terminals` | `?tag=a,b` | List terminals, oldest first, optionally only those with any of the tags. |
| `POST` | `/terminals` | see below | Start a terminal. Returns `201` and its metadata. |
| `GET` | `/terminals/{id}` | | Metadata: status, pid, exit code, idle time, `latest_seq`, `pending`, watch events. |
| `GET` | `/terminals/{id}/output` | `?since=N&tail=N` | Lines with `seq > since` (default: all retained), optionally only the last `tail`. |
| `GET` | `/terminals/{id}/stream` | `?since=N` | Server-sent events: `line` per committed line, `end` when the terminal exits. |
| `POST` | `/terminals/{id}/write` | `{"data":"ls","submit":true}` | Write to stdin. `submit` appends `\r`. |
| `POST` | `/terminals/{id}/keys` | `{"keys":["ctrl+c","up","enter"]}` | Send named keys (see below). |
| `POST` | `/terminals/{id}/wait` | `{"pattern":"re","timeout_seconds":30,"since":-1,"before_context":3}` | Block until a line matches. |
| `POST` | `/terminals/{id}/ack` | `{"watch":"name"}` | Clear watch events (all if `watch` is omitted). |
| `POST` | `/terminals/{id}/resize` | `{"cols":120,"rows":40}` | Resize the PTY. |
| `POST` | `/terminals/{id}/signal` | `{"signal":"int"}` | Signal the process group: `int term kill hup quit usr1 usr2 winch`. |
| `POST` | `/terminals/{id}/restart` | | Kill and re-spawn with the same config. Returns `201` with a **new** id. |
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
  ]
}
```

Only `name` is required. `command` is looked up in the daemon's `PATH` and
defaults to `$SHELL` (or `/bin/sh`); `cwd` defaults to the daemon's working
directory; the environment is the daemon's plus `env` (with `TERM=xterm-256color`
if neither sets `TERM`); size defaults to
80x24.

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
keeps its last 5000 lines; a line longer than 16 KiB is split. Exited
terminals stay listed with their output and `exit_code` until deleted.

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

## Flags

| Flag | Env | Default | Description |
| --- | --- | --- | --- |
| `--bind` | | `127.0.0.1:4242` | Listen address. |
| `--token` | `PTYD_TOKEN` | none | If set, every request except `/healthz` and `/metrics` must send it in the `X-Ptyd-Token` header. |
| `--persist-dir` | `PTYD_PERSIST_DIR` | none | Keep each terminal's metadata here. After a restart, terminals that had not been deleted are listed as **ghosts** (`status: "exited"`, metadata only) until deleted; ghosts older than 7 days are pruned at startup. |
| `--log-dir` | `PTYD_LOG_DIR` | none | Mirror each live terminal's raw output to `<id>.log` (capped at 32 MiB, wraps; removed when the terminal ends). |

On SIGINT or SIGTERM, ptyd stops accepting requests, ends open streams and
waits, terminates every terminal's process group and exits.

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
- Persisted metadata (including `env`) and log mirrors are written
  owner-only (0600).

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
