# API and CLI reference

Use the CLI for a terminal you want to attach to, and HTTP for structured
control. Both use the same daemon and session ids.

## CLI

| Command | Use |
|---|---|
| `ptyd` | Start the daemon; `ptyd -h` lists flags. |
| `ptyd new [-n name] [-d] [--restore policy] [command [args...]]` | Start and attach; `-d` prints the id without attaching. |
| `ptyd ls` | List sessions. |
| `ptyd ui [--discover=false] [--filter text] [--limit N]` | Open the Vim-style session picker; native discovery is on by default. |
| `ptyd status [--json \| --watch] [--discover] [--limit N] [--filter text] [--tag tags]` | Attention-first overview; live selection or compact agent JSON. |
| `ptyd agents [--json]` | Discover native Codex and Claude sessions and their import actions. |
| `ptyd import [-d] [-n name] [--resume] provider:UUID` | Open a view for an exact native conversation; `-d` prints its terminal id. |
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
| `GET` | `/agents` | Native sessions, action requirements, discovery issues and truncation. |
| `POST` | `/agents/import` | Freshly validate a native reference and create a terminal frontend. |
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
  Use a narrower filter or `GET /terminals` when you need the complete list.
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

Type `j` then `k` within 200 ms to detach. In `ptyd ui` (or
`status --watch`), this returns to the picker. Standalone `attach`, `new`
and `import` return to your shell. A lone `j` waits up to 200 ms for `k`;
to send literal `jk`, pause between the letters or use bracketed paste.
Bracketed-paste frames pass through unchanged. Unframed pasted input is
indistinguishable from typing and follows the same shortcut rule. Piped
input passes through without local shortcuts.

Escape, double Escape, control keys and function keys belong to the attached
program. Modified keys and key-repeat events do not activate `jk`; unmodified
kitty and modifyOtherKeys encodings do. On detach, terminal modes reset and
the attached window moves into local scrollback. Connection loss exits the
client with code 1; a normal program exit uses its exit code. Stopped sessions
attach read-only and return immediately.

### Picker controls

| Key or command | Action |
|---|---|
| `j` / `k`, arrows | Next / previous session. |
| `gg` / `G` | First / last session. |
| `i`, Enter | Attach to the selected session. |
| `/` | Search; Enter applies, Escape returns to normal mode. |
| `:b name` | Attach by exact name, id, unique id prefix or terminal number within shown rows. Ambiguous names are refused. |
| `:bn` / `:bp` | Attach to the next / previous shown session, wrapping at the ends. |
| `:ls` | Clear search and refresh the session list. |
| `r` | Refresh. |
| `?`, `:help` | Show controls; `q`, `?` or Escape returns. |
| `q`, `:q` | Leave the picker; sessions keep running. |

Escape cancels an unfinished `:` command. Pasted text can edit search and
command prompts, but cannot execute commands or move the selection. At a
row limit, `:b` only resolves shown rows; clear the filter or raise `--limit`
for a target outside that snapshot. Native session handoff still follows the
[import rules](#import-native-agent-sessions).

Attaching resizes the PTY. Several clients can type; the latest one to type
wins its window size. `/resize` wins until the next client resize or input.
A slow client's 1 MiB queue overflows into a fresh repaint. Continuous
output flushes at most every 4 ms; small echoes and output after quiet flush
immediately.

For custom clients, request `Connection: Upgrade` and `Upgrade: ptyd-attach`
on `T/attach`, with authentication. After 101, the server sends raw output.
Client frames are a type byte, a big-endian uint32 payload length, then
payload: `i` for input; `r` for two big-endian uint16 values (cols, rows).

## Import native agent sessions

`ptyd agents --json` and `GET /agents` query the installed native CLIs in the
daemon's environment. Codex discovery reads its running local daemon over its
Unix socket. Claude discovery uses `claude agents --json --all`. Neither probe
starts a turn or selects a conversation by title, timestamp or “last.”

Each session has a `ref` (`provider` and full UUID), `name`, `cwd`, native
`status`, optional `waiting_for`, `worker_pid` and an `action`:

| Action | What to do |
|---|---|
| `attach` | Import the running conversation. |
| `background` | Run `/bg` in the original Claude tab, wait for its handoff, then refresh. |
| `resume` | Explicitly allow continuing a stopped Claude background conversation. |
| `unavailable` | Follow `reason`; ptyd cannot establish a safe import yet. |

`GET /overview?discover=true` combines these observations with managed
terminals. External rows have `state: "external"` and an `external` object;
their `id` is a picker key, not a `/terminals` id. `counts.external` counts
these rows. Running, stopped and exited counts still describe managed
terminals. Counts cover all matching rows before the view limit. Native
`status` comes from the runtime rather than terminal-output heuristics.
Ordinary `GET /overview` does not run native probes.

Discovery is cached for five seconds. `issues` (or `discovery_issues` in the
overview) and `truncated`/`discovery_truncated` describe incomplete reads;
partial discovery is not proof that a session is absent. Each native response
is capped at 4 MiB, and at most 500 native records per provider are returned.
Import bypasses the cache and refuses incomplete requested-provider listings.
Native background/foreground ambiguity is checked before applying the row cap.

To import through HTTP, send the exact reference from discovery:

```json
{
  "ref": {"provider": "codex", "id": "11111111-1111-4111-8111-111111111111"},
  "name": "review",
  "cols": 120,
  "rows": 40
}
```

Replace the example UUID with the discovered id. A stopped Claude conversation
also needs `"resume": true`; the CLI equivalent is `ptyd import --resume`
followed by its `claude:UUID` reference. The response contains `terminal`,
`session` and `reused`. An existing normal ptyd terminal whose current agent
hook reports that exact conversation may be reused. Otherwise the server
creates a temporary frontend and checks the native reference, directory and
hosting process generation again. Codex connects to the specific daemon
socket observed during validation, not a newly selected server.

Use the returned terminal id for the normal attach/screen/input routes. Claim
the view with an attachment within one minute; an unclaimed view expires.
Last detach closes a running temporary frontend. After a frontend exits, its
exit status remains readable for one minute before cleanup. Closing, killing or expiring that
frontend does not stop the native worker. Native conversations appear in the
combined picker; temporary frontends do not add duplicate session rows.

An imported window can navigate to another conversation. Its `source` is
launch provenance, not proof of its current input target. Another import of
the original reference conflicts while that window is open; attach it by its
explicit terminal id or close it first. The picker never silently reuses a
window based on launch provenance. Native view input typed before import
finishes is discarded, including queued cancellation keys.

Imported views are not automatically restored after a ptyd restart. Refresh
native discovery and import again; generic restart/restore refuses to bypass
fresh native validation. This leaves native worker lifecycle with its owner.

Only the configured local profiles are discovered. Codex must expose its
loaded sessions through the shared daemon; standalone `--no-daemon`, other
profiles and remote hosts are outside this import path. Claude foreground
handoff follows [Claude's native rules](https://code.claude.com/docs/en/agent-view#what-carries-over-when-you-background),
including work that must finish or restart. Ptyd does not press those controls
for you, close an original tab, inject a prompt or adopt an arbitrary macOS PTY.
The native CLI may show its own authentication, trust or update prompts, and
its redraw/history behavior determines what the new view displays.
