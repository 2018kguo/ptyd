#!/usr/bin/env bash
# Live check that ptyd's agent restore resumes the exact latest session of
# a real coding agent, through a daemon crash (kill -9) and a graceful
# restart. Not run in CI: it needs the agent installed and logged in, and
# it sends a few one-word prompts (minimal tokens).
#
#   scripts/verify-agent-resume.sh claude [crash|graceful] [project-dir]
#   scripts/verify-agent-resume.sh codex  [crash|graceful] [project-dir]
#
# The project dir must be one the agent already trusts (Claude Code asks
# to trust new folders on first launch). It defaults to $PWD.
#
# Steps: start a private daemon with --persist-dir; launch the agent with
# restore "agent"; prompt it; switch session in-app (claude /clear, codex
# /new) and prompt again; stop the daemon (kill -9 or SIGTERM); check the
# agent process is gone; start the daemon again; then check that the
# terminal came back under the same id, that the agent was launched with
# the exact latest session id (never --continue/--last/a picker), that its
# screen shows the conversation, and that a further prompt lands in that
# session's transcript.
set -euo pipefail

agent=${1:?usage: $0 claude|codex [crash|graceful] [project-dir]}
mode=${2:-crash}
project=$(cd "${3:-$PWD}" && pwd -P)
port=${PTYD_VERIFY_PORT:-47199}
api=http://127.0.0.1:$port
work=$(mktemp -d "${TMPDIR:-/tmp}/ptyd-verify.XXXXXX")
persist=$work/persist
ptyd=${PTYD_BIN:-$work/ptyd}
log=$work/daemon.log

say() { printf '\n== %s\n' "$*"; }
fail() { printf '\nFAIL: %s\n' "$*"; printf -- '--- daemon log:\n'; cat "$log"; exit 1; }
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {}, {'d': d}))" "$1"; }

if [ ! -x "$ptyd" ]; then
  (cd "$(dirname "$0")/.." && go build -o "$ptyd" ./cmd/ptyd)
fi

# The daemon requires a token; every request sends it.
token=$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')
curl() { command curl -H "X-Ptyd-Token: $token" "$@"; }

daemon_pid=
start_daemon() {
  # Run like a service would: without the environment of an agent session
  # this script may itself be running in.
  env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_CHILD_SESSION \
    -u CLAUDE_CODE_EXECPATH -u CLAUDE_CODE_SESSION_ATTENDED -u CLAUDE_CODE_MESSAGING_SOCKET \
    -u CLAUDE_CODE_MESSAGING_TOKEN -u CLAUDE_PID -u CLAUDE_EFFORT PTYD_TOKEN="$token" \
    "$ptyd" serve --bind 127.0.0.1:$port --persist-dir "$persist" >>"$log" 2>&1 &
  daemon_pid=$!
  for _ in $(seq 50); do curl -fs $api/healthz >/dev/null && return; sleep 0.1; done
  fail "daemon did not start"
}
cleanup() {
  if [ -n "${id:-}" ]; then
    # Keep the evidence, then delete the terminal (this stops the agent).
    curl -s "$api/terminals/$id/screen?scrollback=1" >"$work/final-screen.json" 2>/dev/null || true
    cp "$persist/$id/agent-session.json" "$work/" 2>/dev/null || true
    curl -s -X DELETE $api/terminals/$id >/dev/null 2>&1 || true
  fi
  [ -n "$daemon_pid" ] && kill "$daemon_pid" 2>/dev/null || true
  echo "work dir (daemon log, persisted state): $work"
}
trap cleanup EXIT

screen() { curl -fs "$api/terminals/$id/screen?scrollback=1" | json '"\n".join(d.get("scrollback") or []) + "\n" + "\n".join(d["lines"])'; }
wait_screen() { # regex, timeout seconds
  for _ in $(seq $((${2:-60} * 4))); do screen | grep -Eq "$1" && return; sleep 0.25; done
  screen | tail -25; fail "screen never matched /$1/"
}
send() {
  curl -fs -X POST $api/terminals/$id/write -d "$(python3 -c 'import json,sys; print(json.dumps({"data": sys.argv[1]}))' "$1")" >/dev/null
  sleep 0.5
  curl -fs -X POST $api/terminals/$id/keys -d '{"keys":["enter"]}' >/dev/null
}
info() { curl -fs $api/terminals/$id; }

case $agent in
  claude) ready='❯'; reply='⏺'; switch=/clear ;;
  codex) ready='›'; reply='•'; switch=/new ;;
  *) fail "unknown agent $agent" ;;
esac

say "daemon on :$port, persist dir $persist"
start_daemon
id=$(curl -fs -X POST $api/terminals -d "{\"name\":\"verify-$agent\",\"command\":\"$agent\",\"cwd\":\"$project\",\"restore\":\"agent\",\"cols\":120,\"rows\":40}" | json 'd["id"]')
echo "terminal $id"
wait_screen "$ready" 60

say "prompt 1"
send "reply with the single word ok"
wait_screen "$reply ok" 120
first=$(info | json 'd.get("agent_session","")')
echo "session after prompt 1: $first"
[ -n "$first" ] || fail "no session reported after the first prompt"

say "switch session ($switch) and prompt 2"
send "$switch"
sleep 3
send "reply with the single word two"
wait_screen "$reply two" 120
latest=$(info | json 'd.get("agent_session","")')
echo "session after $switch + prompt 2: $latest"
[ -n "$latest" ] && [ "$latest" != "$first" ] || fail "session did not change after $switch"
transcript=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["transcript_path"])' "$persist/$id/agent-session.json")
grep -q 'single word two' "$transcript" || fail "prompt 2 not in $transcript"
agent_pid=$(info | json 'd["pid"]')

say "stop the daemon ($mode)"
if [ "$mode" = crash ]; then kill -9 "$daemon_pid"; else kill -TERM "$daemon_pid"; fi
wait "$daemon_pid" 2>/dev/null || true
for _ in $(seq 50); do kill -0 "$agent_pid" 2>/dev/null || break; sleep 0.1; done
if kill -0 "$agent_pid" 2>/dev/null; then echo "agent $agent_pid outlived the daemon; the restore must stop it"; else echo "agent $agent_pid is gone"; fi

say "restart the daemon"
start_daemon
for _ in $(seq 50); do [ "$(info | json 'd["status"]')" = running ] && break; sleep 0.2; done
info | python3 -m json.tool | grep -E '"(id|status|pid|restored|notice|agent_session)"'
[ "$(info | json 'd["status"]')" = running ] || fail "terminal not restored"
[ "$(info | json 'd.get("agent_session","")')" = "$latest" ] || fail "restored session is not $latest"
kill -0 "$agent_pid" 2>/dev/null && fail "the previous agent process $agent_pid is still running"
new_pid=$(info | json 'd["pid"]')
args=$(ps -o args= -p "$new_pid" || true)
echo "relaunched as: $(echo "$args" | cut -c1-200)"
case $agent in
  claude) echo "$args" | grep -q -- "--resume $latest" || fail "claude not launched with --resume $latest" ;;
  codex) echo "$args" | grep -q -- "resume .*-- $latest" || fail "codex not launched with resume -- $latest" ;;
esac
echo "$args" | grep -Eq -- '--continue|--last' && fail "picker/most-recent flag used"
wait_screen 'single word two' 60
screen | grep -Eqi 'select a (session|conversation)|resume a previous|search sessions' && fail "a picker is showing"

say "prompt 3 goes to session $latest"
sleep 3
send "reply with the single word three"
wait_screen "$reply three" 120
grep -q 'single word three' "$transcript" || fail "prompt 3 not in $transcript"
[ "$(info | json 'd.get("agent_session","")')" = "$latest" ] || fail "session changed after resume"

say "PASS: $agent resumed exactly $latest after a $mode restart"
