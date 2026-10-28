# Operating ptyd

Keep the daemon reachable only by clients you trust. Its token allows the
same command execution as your local user.

## Configure it

| Flag | Environment | Default |
|---|---|---|
| `--bind` | `PTYD_ADDR` | `127.0.0.1:4242` |
| `--token` | `PTYD_TOKEN` | Generated at startup |
| `--persist-dir` | `PTYD_PERSIST_DIR` | `~/.local/state/ptyd/terminals-<port>` |
| `--log-dir` | `PTYD_LOG_DIR` | Off |
| `--scrollback-bytes` | None | `1048576` retained-line bytes per terminal |

An empty `--persist-dir=` disables saving. Raw log mirrors cap at 32 MiB,
wrap, and are removed when the terminal ends. SIGHUP leaves the daemon
running. SIGINT/SIGTERM stops requests, checkpoints screens, terminates
process groups and shuts down.

## Terminal display settings

The daemon can run headless. Programs on PTYs receive `TERM=xterm-256color`
by default; daemon display flags such as `NO_COLOR` are excluded. CLI-created
terminals and imported views use the viewing terminal's display settings.
HTTP callers can set them explicitly. The viewer also supplies its default colors when the host answers the startup
probe. The emulator can answer an app's queries before a viewer is present.
Existing processes keep the display decisions they made at startup; reopen an
imported view to apply a corrected profile. This leaves the native worker alive.

## Restore policies

| Policy | After a daemon restart |
|---|---|
| `none` | Show the saved screen as a stopped session (a ghost). HTTP create default. |
| `rerun` | Start the command again below its saved screen, under the same id. |
| `agent` | Resume the recorded Claude Code or Codex conversation; leave a ghost with a notice if verification fails. |
| `auto` | Agent resume for claude/codex, rerun for other commands, none when persistence is off. `ptyd new` default. |

Only sessions that were running when the daemon stopped restore automatically.
Programs that exited and restores that were declined stay stopped. Use
`ptyd restore` or `/restore` to try again; `/restart` creates a new terminal
and agent session. A restore starts a new process and preserves the window,
not the previous process's memory. A process group left by the old run must
be stopped and verified before another starts.

Agent hooks record the current session id, directory and transcript path.
A resume uses that exact id; it never selects the latest conversation with
`--last` or a picker. User hook settings remain intact. Other agent CLIs can
use `rerun`; they do not have session-resume adapters.

For `agent`/agent-selected `auto`, pass options as `--flag` or `--name=value`;
Codex also accepts `-c key=value`. Positional prompts and session-selection
flags are refused to avoid replaying a prompt or resuming the wrong session.
Send prompts through `/write`. Claude sessions cleared before their first
message can start fresh. Codex has no new conversation id until the first
message: after `/new`, an earlier existing conversation may be resumed.
A resumed agent can redraw and clear the saved window; it is not a permanent
transcript viewer.

## Imported native views

An imported Codex or Claude view is a client of the agent's existing local
daemon. That daemon owns the worker and conversation. Detaching, deleting the
view or shutting down ptyd closes the frontend, while native work continues.
Unclaimed views expire after one minute. They use no automatic restore
policy; discover and import again after restarting ptyd.

Use `ptyd ui` for the combined picker. It hides saved history until `h` and
shows an existing imported window under Open terminals. A source
reference records which conversation was selected at launch, and does not
track later navigation inside the native CLI. Generic restart/restore cannot
reuse that reference without fresh validation. [The import reference](reference.md#import-native-agent-sessions)
explains handoff, profile scope and incomplete discovery.

## Persistence and upgrades

Each terminal directory contains `meta.json`, `screen.ckpt`, bounded
`tail.<gen>` files, optional `exit.json`, and agent session metadata. Files
are 0600 inside 0700 directories; output and environment variables may
contain secrets. Delete a terminal to remove its directory. Ghosts unchanged
for seven days are pruned at startup.

Checkpoints are written atomically. Output and resize records after them
replay on restore, including unfinished escape sequences. A torn tail is
tolerated. Every PTY read reaches its tail before the next read, but tails
are not fsynced each time: process crashes survive; a power loss can lose
recent output. Tail files total about 8 MiB plus the checkpoint. Checkpoints
run after quiet output, large tails, normal exits and graceful shutdown.
After quiet output, the existing persistence callback compacts the emulator
and releases oversized output-ring buffers while keeping retained contents.
With persistence disabled, ring reclamation runs on process exit. Idle sessions
have no persistence timer or open tail file. Reclaimed capacity may need to
grow again when output resumes; [measurements](../bench/README.md#storage-after-quiet-output)
include that cost.

Ptyd reads the earlier binary and ANSI checkpoint formats. Upgrade readers
before writing new checkpoints; an older binary may reject the emulator
snapshot even when it recognizes the outer header. Keep a backup before an
upgrade if you need to roll back. Reading an old file leaves it unchanged
until the next checkpoint.
Use exact checkpoints for persistence; ANSI attachment has the emulator's
[export limits](https://github.com/2018kguo/phosphor#tradeoffs).

## Authentication and remote access

Prefer `PTYD_TOKEN` to a command-line token visible in process listings.
Without one, ptyd generates a token and saves it at
`$XDG_STATE_HOME/ptyd/token-<port>` (normally `~/.local/state/ptyd/`). The CLI
reads it automatically; an explicit `PTYD_TOKEN` wins. A clean shutdown
removes the token file.

Use SSH or another private tunnel for remote clients. The shared token is
sent over plain HTTP. `/healthz` and `/metrics` are unauthenticated; metrics
include configured directory paths and terminal counts.

## Run it as a service

[systemd user unit](../infra/systemd/ptyd.service):

```sh
mkdir -p ~/.config/systemd/user
cp infra/systemd/ptyd.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now ptyd
```

[launchd user agent](../infra/launchd/com.ptyd.plist):

```sh
mkdir -p ~/Library/LaunchAgents ~/Library/Logs/ptyd
sed "s|__HOME__|$HOME|g" infra/launchd/com.ptyd.plist > ~/Library/LaunchAgents/com.ptyd.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.ptyd.plist
```

Set the service's PATH so it can find the commands you spawn.

## Many sessions

Budget retained lines, screen/history, attach queues and process memory
separately. Idle checkpointing compacts unused emulator storage; retained
editor buffers remain meaningful state. Linux normally uses two fds per
running terminal (PTY and pidfd), macOS one, plus a raw log if enabled.
Each session also holds a waiting OS thread (two on macOS). Check OS PTY,
process, thread and file-descriptor limits before planning thousands of
sessions; raising one limit does not remove the others.
