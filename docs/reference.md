# API and CLI reference

Use the CLI for a terminal you want to attach to, and HTTP for structured
control. Both use the same daemon and session ids.

## CLI

| Command | Use |
|---|---|
| `ptyd` | Start the daemon; `ptyd -h` lists flags. |
| `ptyd new [-n name] [-d] [--restore policy] [command [args...]]` | Start and attach; `-d` prints the id without attaching. |
| `ptyd ls` | List sessions. |
| `ptyd status [--json \| --watch] [--limit N] [--filter text] [--tag tags]` | Attention-first overview; live selection or compact agent JSON. |
| `ptyd attach [--readonly] terminal` | Attach to a running session or view a stopped one's screen. |
| `ptyd restore [--policy policy] terminal` | Start a stopped terminal again under the same id, then attach. |
| `ptyd kill terminal...` | Kill and forget sessions. |
| `ptyd token` | Print the daemon token for an HTTP client. |

A terminal can be named by id, unique prefix, name or number (`3` for
`term-3-ab12`). Commands use `PTYD_ADDR` (default `127.0.0.1:4242`) and
`PTYD_TOKEN`, or the daemon's saved token. Subcommands also accept `--addr`.

## HTTP

Send `X-Ptyd-Token` on every request except `/healthz` and `/metrics`. Bodies
are JSON. Errors are `{"error":"..."}`: 400 invalid input, 401 bad token,
404 missing resource, 409 stopped session or declined restore, 413 body over
1 MiB. Paths below use `/terminals/{id}` as `T`.

| Method | Path | Input / result |
|---|---|---|
| `GET` | `/healthz`, `/metrics` | Liveness; JSON counters and uptime. No auth. |
| `GET` | `/terminals` | Optional `?tag=a,b`; oldest first, matching any tag. |
| `GET` | `/overview` | Compact session summaries; `?limit=50&q=text&tag=a,b`. Counts include all matches. |
| `POST` | `/terminals` | Create; returns 201 and metadata. |
| `GET` | `T` | Metadata, status, pid, output position, attention and restore information. |
| `GET` | `T/output` | `?since=N&tail=N`; retained lines newer than `since`. |
| `GET` | `T/stream` | `?since=N`; SSE `line` events, then `end` on exit. |
| `GET` | `T/screen` | JSON window; `?scrollback=1` adds history, `?format=ansi` returns repaint bytes. |
| `GET` | `T/attach` | `?cols=&rows=&readonly=1`; upgraded interactive connection. |
| `POST` | `T/write` | `{"data":"ls","submit":true}`; optional Enter after input. |
| `POST` | `T/keys` | `{"keys":["ctrl+c","up","enter"]}`. |
| `POST` | `T/wait` | `{"pattern":"ready","timeout_seconds":30,"since":-1,"before_context":3}`. |
| `POST` | `T/ack` | `{"watch":"name"}`; omit watch to clear all attention. |
| `POST` | `T/resize` | `{"cols":120,"rows":40}`. |
| `POST` | `T/signal` | `{"signal":"int"}`; also term, kill, hup, quit, usr1, usr2, winch. |
| `POST` | `T/restart` | Kill and spawn again with the same config; 201, **new id**. |
| `POST` | `T/restore` | Optional `{"policy":"rerun"}`; 201, **same id**, or 409 with the reason. |
| `DELETE` | `T` | Terminate and forget; also deletes a stopped session. |
| `GET` | `/search` | `?q=regex&within_seconds=N&limit=N&tag=a,b`; retained output of non-ghost sessions. |

## Session overview

`GET /overview` reads the daemon's existing session registry. Rows put
retained watch matches first, then stopped sessions with restore notices,
running sessions and other exited sessions. It reports process state,
not inferred agent activity.

- `counts` describes all sessions matching `q` and any supplied tag.
  `attention` counts sessions with retained, unacknowledged watch events
  and can overlap running, exited and stopped counts.
- `sessions` contains at most `limit` rows (default 50, maximum 500).
  Top-level `truncated` means more matching rows exist. Names, directories
  and base command names are included; arguments and watch definitions
  stay on the detail route. This is a row limit, not a total byte budget.
- Each row keeps its exact `id`, observed `state` (`running`, `stopped`
  or `exited`), exit code, idle time,
  attached-client count and `latest_seq` for incremental output reads.
  `idle_seconds` is null when the process has not produced observed output;
  the human view shows a dash.
- A `preview` comes from the newest retained watch match, a restore notice
  or pending output, in that order. It is at most 160 codepoints and marks
  truncation explicitly. A row's `attention` counts its retained events,
  capped by the normal watch retention policy.

`ptyd status --json` returns this same result. On failure it exits nonzero
and writes an error to stderr. The live `--watch` view keeps the last
successful snapshot with a visible stale/error notice if refresh fails.
Enter attaches to the selected exact id; detaching returns to the view.
Stopped sessions show their saved window. Restarting them still requires
an explicit `ptyd restore`.

## Create a session

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
  "watches": [{"name":"done","pattern":"BUILD (OK|FAILED)","once":true}],
  "restore": "rerun"
}
```

Only `name` is required. Defaults: the daemon's shell (`SHELL`, else
`/bin/sh`), working directory and environment; `TERM=xterm-256color` if
unset; 80x24; restore `none`. Commands are looked up in the daemon's PATH.
See [restore policies](operating.md#restore-policies) before using agent restore.

## Choose lines or a screen

`/output` strips escapes and commits a line at `\n`; `\r` replaces the
pending line, so progress bars collapse to their last state:

```json
{"lines":[{"seq":0,"text":"hi","at":"2026-01-01T00:00:00Z"}],"latest_seq":0,"pending":"$ "}
```

Pass the last `latest_seq` back as `since`. Retention is bounded by 5,000
lines and `--scrollback-bytes` (default 1 MiB, including per-line overhead).
Lines over 16 KiB split. Exited sessions retain output until deleted.

`/screen` handles cursor movement and full-screen programs:

```json
{"cols":80,"rows":24,"cursor_x":2,"cursor_y":5,"cursor_visible":true,"alt_screen":false,"title":"vim","lines":["..."],"scrollback":["..."]}
```

Screen scrollback is a separate emulator budget: 2,000 logical lines /
256 KiB. ANSI output targets a terminal of the same size.

## Wait, watch and write

`/wait` checks committed lines newer than `since` (default: all retained)
and the pending line after 250 ms of quiet. A pending match has `seq:-1`.
Success returns `matched:true`, `match` and `context`; otherwise the reason
is `timeout`, `exited` or `canceled`.

Watches check new and settled pending lines. The newest 100 matches remain
in `attention` until acknowledged; a `once` watch can fire again after ack.

`submit:true` sends Enter after the input. When the program enables
bracketed paste, the payload is one paste followed by Enter. Named keys:
enter, tab, esc, space, backspace, arrows, home, end, pgup, pgdn, delete,
a single character, or control forms `ctrl+c`, `c-c`, `^c`.

## Keep context between agent turns

Keep the terminal id and the last `latest_seq` you read. On the next turn,
request `/output?since=N` to fetch new lines. Add `tail` to cap the returned
context; retention and tail limits can omit older lines. Use `/screen`
when the program redraws a window rather than printing a transcript.

Before submitting another command, note the current output position and
pass it as `since` to `/wait`. This excludes old committed lines that happen
to match the same pattern. Pending prompt matches use `seq:-1`; they are
observations of the current prompt, not positions to use as the next cursor.

Check `matched` and `reason` before acting on a wait. A timeout leaves the
program running. Read its state before resending input, since a repeated
write can submit a command twice.

## Attach

Ctrl-\\ twice within a second detaches; a single Ctrl-\\ reaches the program
after that second. Other keys go straight to it. On detach, terminal modes
reset and the attached window moves into your local scrollback. A daemon
connection loss exits the client with code 1; a normal program exit uses
its exit code. Stopped sessions attach read-only and return immediately.

Attaching resizes the PTY. Several clients can type; the latest one to type
wins its window size. `/resize` wins until the next client resize or input.
A slow client's 1 MiB queue overflows into a fresh repaint. Continuous
output flushes at most every 4 ms; small echoes and output after quiet flush
immediately.

For custom clients, request `Connection: Upgrade` and `Upgrade: ptyd-attach`
on `T/attach`, with authentication. After 101, the server sends raw output.
Client frames are a type byte, a big-endian uint32 payload length, then
payload: `i` for input; `r` for two big-endian uint16 values (cols, rows).
