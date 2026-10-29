# API and CLI reference

Use the CLI for a terminal you want to attach to, and HTTP for structured
control. Both use the same daemon and session ids.

## CLI

| Command | Use |
|---|---|
| `ptyd`, `ptyd ui` | Open your groups; start a local daemon if needed. |
| `ptyd new [-d] [--cwd directory] name [-- command args...]` | Create a group and enter it; `-d` prints its id. An optional command opens its first terminal. |
| `ptyd ls [--json] [--all]` | List groups and counts; `--all` includes archived groups and saved-only Inbox. JSON always includes all groups. |
| `ptyd attach [group]` | Enter a group's workspace, or choose a group. |
| `ptyd rename group name` | Rename without changing its id. |
| `ptyd archive [--undo] group` | Archive finished work or bring it back. Running terminals must stop first. |
| `ptyd remove [--force] group` | Remove an empty group; `--force` stops and removes its terminals. |
| `ptyd term new [-n name] [-d] [--group group] [--restore policy] [-- command args...]` | Create a terminal; `-d` prints its id. |
| `ptyd term ls [--json] [--group group]` | List individual terminals. Inside a terminal, defaults to its current group; outside, lists all. |
| `ptyd term attach [--readonly] [--raw] [--group group] terminal` | Open an individual terminal. |
| `ptyd term rename [--group group] terminal name` | Rename without restarting. |
| `ptyd term move [--group group] terminal destination` | Move without restarting. |
| `ptyd term restore [-d] [--group group] [--policy policy] terminal` | Relaunch an ended terminal; a ghost keeps its id, an exited live record gets a new id. |
| `ptyd term kill [--group group] terminal...` | Stop and forget terminals. |
| `ptyd term import [-d] [--group group] [-n name] [--resume] provider:UUID` | Open an exact native conversation. |
| `ptyd status [--json] [--group group] [--discover] [--limit N] [--filter text] [--tag tags]` | Terminal overview for scripts and agents. |
| `ptyd agents [--json]` | Discover native conversations and original tabs. |
| `ptyd serve [flags]` | Run the daemon in the foreground; `ptyd serve -h` lists flags. |
| `ptyd token` | Print the token for an HTTP client. |

Put flags before positional arguments. Group names are unique ignoring case;
full group ids take precedence over names, and unique id prefixes work.
Terminal names, numbers and prefixes resolve inside `--group`, the current
terminal's group, or Inbox. Exact terminal ids work globally. `term ls` outside
ptyd is the exception: it lists all groups.

Commands use `PTYD_ADDR` (default `127.0.0.1:4242`) and `PTYD_TOKEN`, or the
daemon's saved token. Subcommands accept `--addr`. Child terminals receive
`PTYD_ADDR`, `PTYD_GROUP_ID` and `PTYD_TERMINAL_ID`. CLI inheritance consults
current membership before the launch-time group variable, so a moved manager's
new children follow it.

The root `new`, `ls` and `attach` commands operate on groups. Terminal commands
live under `term`; scripts using the HTTP terminal routes need no migration.

## HTTP

Send `X-Ptyd-Token` on every request except `/healthz` and `/metrics`. Bodies
are JSON. Errors are `{"error":"..."}`: 400 invalid input, 401 bad token,
404 missing resource, 409 stopped session, group conflict, human input control or declined restore, 413 body over
1 MiB. Paths below use `/terminals/{id}` as `T`.

| Method | Path | Input / result |
|---|---|---|
| `GET` | `/healthz`, `/metrics` | Liveness; JSON counters and uptime. No auth. |
| `GET`, `POST` | `/groups` | Group summaries, or create with `{"name":"work","cwd":"/path"}`. |
| `GET`, `PATCH`, `DELETE` | `/groups/{group}` | Read, rename/archive, or remove; deletion with members requires `?force=true`. |
| `GET` | `/terminals` | Optional `?group=id&tag=a,b`; oldest first, matching any tag. |
| `GET` | `/agents` | Native sessions, action requirements, discovery issues and truncation. |
| `POST` | `/agents/import` | Freshly validate a native reference and create a terminal frontend. |
| `GET` | `/overview` | Compact session summaries; `?group=id&limit=50&q=text&tag=a,b`. Counts include all matches. |
| `POST` | `/terminals` | Create; returns 201 and metadata. |
| `GET` | `T` | Metadata, group id, status, pid, output position, attention and restore information. |
| `PATCH` | `T` | `{"name":"reviewer","group_id":"group-..."}`; either field can be omitted. |
| `GET` | `T/output` | `?since=N&tail=N`; retained lines newer than `since`. |
| `GET` | `T/stream` | `?since=N`; SSE `line` events, then `end` on exit. |
| `GET` | `T/screen` | JSON window; `?scrollback=1` adds history, `?format=ansi` returns repaint bytes. |
| `GET` | `T/attach` | `?cols=&rows=&readonly=1&human=1`; upgraded interactive connection. |
| `POST` | `T/write` | `{"data":"ls","submit":true}`; optional Enter after input. |
| `POST` | `T/keys` | `{"keys":["ctrl+c","up","enter"]}`. |
| `POST` | `T/wait` | `{"pattern":"ready","timeout_seconds":30,"since":-1,"before_context":3}`. |
| `POST` | `T/ack` | `{"watch":"name"}`; omit watch to clear all attention. |
| `POST` | `T/resize` | `{"cols":120,"rows":40}`. |
| `POST` | `T/signal` | `{"signal":"int"}`; also term, kill, hup, quit, usr1, usr2, winch. |
| `POST` | `T/restart` | Kill and spawn again with the same config; 201, **new id**. |
| `POST` | `T/restore` | Optional `{"policy":"rerun"}`; 201, **same id**, or 409 with the reason. |
| `POST` | `T/relaunch` | Optional restore policy; ghost restore or exited-terminal restart. Refuses running terminals. |
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

`?active=true` excludes completed/stopped managed terminals and saved native
conversations before applying the row limit. `active_only: true` confirms
that the daemon applied it; `history_hidden` counts matching records omitted
by this filter. Counts describe the remaining matches. The default API and
`ptyd status --json` include history.

The workspace shows all members of an opened group by default; `h` hides or
shows finished members. The group list hides archived work and saved-only Inbox
until `h`. It never hides a running group. Filtering never deletes records.
The workspace requests at most 500 rows and shows a notice when more exist.
It keeps the last successful view with a stale/error notice on refresh failure.
Opening or changing a selected item waits for a matching successful snapshot.
`ptyd status --json` returns the overview directly and fails nonzero on error.

## Groups

A group is a persistent name, default directory and archive state. Its summary
adds `terminals`, `running`, `attention`, `viewing` (attached clients) and
`last_output_at`. It has no manager process or required task lifecycle.
Group metadata persists independently of terminals; membership lives on each
terminal. Removing the last member does not remove the group.

Create terminals with `group_id`; omitting it uses Inbox (`default`). An omitted
`cwd` uses the group's directory. CLI `term new` uses the caller's directory.
Unknown group ids fail with 404; archived groups refuse creation, move-in and
relaunch. A group can be archived only when its members have stopped. Inbox
cannot be removed or archived. Names and ids are separate: renaming either a
group or terminal does not restart a process or alter its id.

## Create a session

```json
{
  "name": "worker",
  "group_id": "group-...",
  "command": "bash",
  "args": ["-l"],
  "cwd": "/tmp",
  "env": {"FOO": "bar"},
  "terminal_colors": {"foreground":"#d8dee9","background":"#282c34"},
  "tags": ["build"],
  "cols": 120,
  "rows": 40,
  "watches": [{"name":"done","pattern":"BUILD (OK|FAILED)","once":true}],
  "restore": "rerun"
}
```

Only `name` is required. Defaults: the daemon's shell (`SHELL`, else
`/bin/sh`), group's working directory and daemon environment; 80x24; restore `none`.
Display settings from the daemon are excluded. `TERM` defaults to
`xterm-256color`; explicit display settings in `env` win. The CLI forwards
the viewing terminal's `TERM`, `COLORTERM`, `TERM_PROGRAM`, `NO_COLOR`,
`CLICOLOR`, `CLICOLOR_FORCE` and `FORCE_COLOR` settings. Commands are looked
up in the daemon's PATH.

`terminal_colors` optionally supplies the default foreground/background pair
as `#RRGGBB`. Interactive creation and import probe the viewing terminal under
a shared 250 ms deadline; unsupported hosts use the emulator's default palette.
Unrelated keys read during this probe are preserved. Detached/API creation can
supply colors explicitly. Ptyd answers VT queries while no writable raw viewer
is responsible for replies, including before the first attachment.
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

Type `j` then `k` within 200 ms to return from a terminal. Inside a workspace,
this returns to its group. Standalone `term attach`, `term new` and `term import`
return to your shell. A lone `j` waits up to 200 ms for `k`;
to send literal `jk`, pause between the letters or use bracketed paste.
Bracketed-paste frames pass through unchanged. Unframed pasted input is
indistinguishable from typing and follows the same shortcut rule. Piped
input passes through without local shortcuts.

Escape, double Escape, control keys and function keys belong to the attached
program. Modified keys and key-repeat events do not activate `jk`; unmodified
kitty and modifyOtherKeys encodings do. On detach, terminal modes reset and
the attached window moves into local scrollback. Connection loss exits the
client with code 1; a normal program exit uses its exit code. Stopped sessions
attach read-only and return immediately from the standalone command. In the
workspace, saved screens and newly exited frontends stay open until `jk`. Further
keystrokes stay local after output ends; the footer changes to “finished.”

Human TTY attachments reserve the bottom row for the session name and
`jk: return`. The app receives one fewer PTY row and cannot draw over this
indicator by clearing, scrolling or switching screens. The client draws cells
and preserves the app's cursor, keyboard modes, colors and links. Mouse events
for the status row do not reach the app. The window/tab title identifies the
view too. App title updates retain the ptyd prefix and hint;
the previous title is restored on return. `ptyd term attach --raw` forwards terminal
output directly without reserving a row; piped output remains raw too. The title is a view label; it does not verify
the current conversation after navigation inside an agent.

### Workspace controls

| Key or command | Action |
|---|---|
| `j/k`, arrows; `0/G` | Move; first/last item. |
| Enter, `i` | Open a group or take keyboard control of a terminal. |
| `v` | Watch a terminal read-only. An occupied human view also opens read-only. |
| `n` | Create a group, or launch Codex (`c`), Claude (`a`), shell (`s`) or a custom command (`r`). |
| `r` | Rename the selected group or terminal. |
| `m` | Move a terminal to another unarchived group. |
| `x` | Close/remove; requires typing `close` to confirm. |
| `R` | Relaunch an ended terminal; imported views undergo fresh native validation. |
| `a` | Archive/unarchive a group, or toggle outside-agent discovery inside a group. |
| `h` | Show archived/saved-only groups; inside a group, toggle finished terminals. |
| `g`, `:groups` | Return to your groups. |
| `/` | Search as you type; Enter or Escape leaves the search field. |
| `:b name`, `:open name` | Open by id, unique name, number or prefix within the shown rows; ambiguous names are refused. |
| `:run command` | Run a custom command through your shell in the current group. |
| `:new`, `:ls` | Open creation controls; clear search. |
| `?` | Show controls; `q`, `?` or Escape returns. |
| `q`, `:q`, `:detach` | Leave the workspace; work keeps running. |

Escape cancels a prompt. Pasted text can edit prompts but cannot execute
commands or move the selection. Custom commands run through the caller's shell
with `-lc`; they use restore `none`. Built-in agent and shell launches use
restore `auto`. Native stopped conversations require typing `resume` before
launch. Views require at least 40 columns and 16 rows for workspace controls.

### Human input control

CLI human views request `human=1`. One writable human attachment owns keyboard
input until it detaches or disconnects. `/write`, `/keys` and other writable
attachments return 409 while it owns control; read-only viewers, screen reads
and output reads continue. The daemon does not queue or retry rejected agent
input. Automation should observe `human_control` and retry only after checking
the current state. This coordinates input; the shared token still permits
other operations such as stopping a process. If a prior write has blocked,
a human claim returns 409 with an input-busy message instead of hanging;
read-only viewing stays available.

Without a human owner, writable automation clients retain the existing behavior:
the last client to type supplies its window size. Read-only clients never resize
the process. A rejected second human cannot resize it either. A slow client's
1 MiB queue overflows into an exact snapshot for a framed view or an ANSI
repaint for raw output. Bulk output coalesces for up to 4 ms; small echoes and
output after quiet flush immediately.

For custom clients, request `Connection: Upgrade` and `Upgrade: ptyd-attach`
on `T/attach`, with authentication. After 101, the server sends raw output.
Client frames are a type byte, a big-endian uint32 payload length, then
payload: `i` for input; `r` for two big-endian uint16 values (cols, rows).

## Import native agent sessions

`ptyd agents --json` and `GET /agents` query the installed native CLIs in the
daemon's environment. Codex discovery reads its running local daemon over its
Unix socket. Threads without a saved rollout or marked ephemeral remain
unavailable; a loaded/idle status alone does not make them resumable. Claude discovery uses `claude agents --json --all`. Neither probe
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

Discovery also returns a separate `processes` list for same-user foreground
Codex and Claude terminal processes not identified by their native APIs. These
are observations of original tabs, not conversation references. Rows include
provider, PID, process generation, terminal path, directory and parent PIDs;
arguments, prompts and environment values are omitted. The outside-agent view labels them as original tabs. They cannot authorize import or input. A standalone Codex
process must keep using its original terminal; Claude can register its handoff
with `/bg` there.

Overview process rows use `local_process`, `state: "external"` and a
`process:...` picker key. `counts.original_tabs` counts these rows separately;
`counts.total` and `counts.external` include them. Managed process descendants
are excluded, and multiple observations of one foreground group/terminal are
collapsed. A process observation cannot prove which shared-daemon conversation
an original frontend displays. `process_issue` and `processes_truncated` report
inventory limits separately from native import authority. The overview folds
process truncation into `discovery_truncated` and retains `process_issue`.

Discovery is cached for five seconds. `issues` (or `discovery_issues` in the
overview) and `truncated`/`discovery_truncated` describe incomplete reads;
partial discovery is not proof that a session is absent. Each native response
is capped at 4 MiB, and at most 500 native records per provider are returned.
Import bypasses the cache and refuses incomplete requested-provider listings.
Native background/foreground ambiguity is checked before applying the row cap.

To import through HTTP, send the exact reference from discovery:

```json
{
  "group_id": "group-...",
  "ref": {"provider": "codex", "id": "11111111-1111-4111-8111-111111111111"},
  "name": "review",
  "cols": 120,
  "rows": 40
}
```

HTTP callers can add `terminal_env`, a map restricted to the display settings
listed under [Create a session](#create-a-session). String values set a
setting; `null` removes it. CLI imports send this profile automatically, so
a service's `TERM=dumb` or `NO_COLOR` cannot disable the viewer's colors.
Credentials and agent profile selection are never forwarded through this map.
Imports also accept the optional `terminal_colors` pair described above.

Replace the example UUID with the discovered id. A stopped Claude conversation
also needs `"resume": true`; the CLI equivalent is `ptyd term import --resume`
followed by its `claude:UUID` reference. The response contains `terminal`,
`session` and `reused`. An existing normal ptyd terminal whose current agent
hook reports that exact conversation may be reused. Otherwise the server
creates a frontend and checks the native reference, directory and
hosting process generation again. Codex connects to the specific daemon
socket observed during validation, not a newly selected server.

Use the returned terminal id for normal attach/screen/input routes. Add
`group_id` to retain this frontend in a named group after detach. It stays
available until it exits or you close it. Moving it into a named group cancels
the Inbox expiry; moving it back restores temporary-view behavior.

Imports in Inbox preserve the temporary contract: an unclaimed view expires
in one minute and last detach closes a running frontend. A frontend's exit
status remains readable for one minute. Closing a frontend never stops its
native worker. Outside rows are discovery results, not members of the opened
group, until an import succeeds.

An imported window can navigate to another conversation. Its `source` is
launch provenance, not proof of its current input target. Another import of
the original reference conflicts while that window is open; attach it by its
explicit terminal id or close it first. The workspace never silently reuses a
window based on launch provenance. Native view input typed before import
finishes is discarded, including queued cancellation keys.

Imported views are not automatically restored after a ptyd restart. Refresh
native discovery and import again; generic restart/restore refuses to bypass
fresh native validation. This leaves native worker lifecycle with its owner.

Only the configured local profiles supply native conversation identities.
Original-tab process observations can reveal additional running tabs without
an import route. Codex must expose saved, loaded sessions through the shared
daemon; standalone workers, other
profiles and remote hosts are outside this import path. Claude foreground
handoff follows [Claude's native rules](https://code.claude.com/docs/en/agent-view#what-carries-over-when-you-background),
including work that must finish or restart. Ptyd does not press those controls
for you, close an original tab, inject a prompt or adopt an arbitrary macOS PTY.
The native CLI may show its own authentication, trust or update prompts, and
its redraw/history behavior determines what the new view displays.

### Framed attach protocol

The default attach protocol remains an ANSI repaint followed by raw output.
Clients that render their own view request `?rendered=1`; the server confirms
with `X-Ptyd-Rendered: 1`. Server output then uses the existing five-byte frame
header (type byte, big-endian uint32 length) with `s` for an exact Phosphor
snapshot and `o` for subsequent terminal bytes. The first frame is a snapshot.
Overflow and resize also send snapshots, preserving palette and hidden state.
The bundled client accepts frames up to 64 MiB and fails on malformed framing.
Client input and resize frames keep their existing format.

The emulator supplies query replies for framed viewers and headless sessions.
A writable raw viewer answers through its real terminal; a read-only raw viewer
never takes that responsibility. Replies use a bounded byte queue so a child
that stops reading stdin cannot block terminal output.
