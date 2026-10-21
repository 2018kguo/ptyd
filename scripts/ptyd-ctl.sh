#!/usr/bin/env bash
# ptyd-ctl.sh — control ptyd terminals from the command line.
#
# Env:
#   PTYD_API     base URL (default http://127.0.0.1:4242)
#   PTYD_TOKEN   auth token (sent as X-Ptyd-Token header)
set -euo pipefail

API="${PTYD_API:-http://127.0.0.1:4242}"
TOKEN="${PTYD_TOKEN:-}"

curl_args=(-sS)
if [[ -n "$TOKEN" ]]; then
  curl_args+=(-H "X-Ptyd-Token: $TOKEN")
fi

pretty() {
  if command -v jq >/dev/null 2>&1; then
    jq .
  else
    python3 -m json.tool
  fi
}

# json_obj k1 v1 k2 v2 ...
# value prefixes:
#   __TRUE__/__FALSE__   -> bool
#   __INT__<n>           -> integer
#   __FLOAT__<n>         -> float
#   __JSON__<json>       -> raw JSON value
#   anything else        -> string
json_obj() {
  python3 -c '
import json, sys
args = sys.argv[1:]
obj = {}
for i in range(0, len(args), 2):
    k = args[i]
    v = args[i+1]
    if v == "__TRUE__":
        v = True
    elif v == "__FALSE__":
        v = False
    elif v.startswith("__INT__"):
        v = int(v[len("__INT__"):])
    elif v.startswith("__FLOAT__"):
        v = float(v[len("__FLOAT__"):])
    elif v.startswith("__JSON__"):
        v = json.loads(v[len("__JSON__"):])
    obj[k] = v
print(json.dumps(obj))
' "$@"
}

usage() {
  cat >&2 <<'EOF'
Usage: ptyd-ctl.sh <command> [args]

Inspection:
  list [tags]                             All terminals, optionally filtered by comma-separated tags
  get <id>                                Get terminal metadata (JSON)
  output <id> [since] [tail]              Get output (lines with seq>since, last `tail`)
  stream <id> [since]                     Stream output via SSE (Ctrl-C to stop)
  search <pattern> [within_secs] [tag]    Regex search across terminals

Spawn / lifecycle:
  create <name> [cwd] [cmd] [args...]     Create a terminal (cmd defaults to $SHELL)
  create-tagged <name> <tags> [cwd] [cmd] [args...]
                                          Create with comma-separated tags
  restart <id>                            Re-spawn with the same config
  close <id>                              Kill and remove a terminal (or forget a ghost)

Interaction:
  write  <id> <data>                      Raw write (no auto-submit)
  send   <id> <data>                      Write + append \r to submit
  keys   <id> <key1> [key2...]            Send named keys (ctrl+c, enter, up, ...)
  signal <id> <int|term|kill|hup|quit|usr1|usr2|winch>
  resize <id> <cols> <rows>

Reactive:
  wait   <id> <pattern> [timeout_secs] [before_ctx]
                                          Block until a line matches a regex
  ack    <id> [watch]                     Acknowledge attention (or one watch)

Env:
  PTYD_API     base URL (default http://127.0.0.1:4242)
  PTYD_TOKEN   auth token (sent as X-Ptyd-Token header)

Notes:
  • `write` does NOT add a newline. Use `send` to submit a command, or pass
    your own \r in the data:  ptyd-ctl.sh write t1 $'ls\r'
  • Watches are set at create time via the HTTP API (see README).
EOF
}

cmd="${1:-}"
shift || true

case "$cmd" in
  list)
    url="$API/terminals"
    [[ -n "${1:-}" ]] && url="$url?tag=$1"
    curl "${curl_args[@]}" "$url" | pretty
    ;;

  create)
    name="${1:-term}"
    cwd="${2:-$PWD}"
    bin="${3:-${SHELL:-/bin/sh}}"
    if [[ $# -gt 3 ]]; then shift 3; else shift $#; fi
    args_json=$(python3 -c 'import sys, json; print(json.dumps(sys.argv[1:]))' "$@")
    payload=$(json_obj name "$name" cwd "$cwd" command "$bin" args "__JSON__$args_json")
    curl "${curl_args[@]}" -X POST "$API/terminals" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  create-tagged)
    name="${1:?name required}"
    tags="${2:?comma-separated tags required}"
    cwd="${3:-$PWD}"
    bin="${4:-${SHELL:-/bin/sh}}"
    if [[ $# -gt 4 ]]; then shift 4; else shift $#; fi
    args_json=$(python3 -c 'import sys, json; print(json.dumps(sys.argv[1:]))' "$@")
    tags_json=$(python3 -c 'import sys, json; print(json.dumps([t for t in sys.argv[1].split(",") if t]))' "$tags")
    payload=$(json_obj name "$name" cwd "$cwd" command "$bin" \
      args "__JSON__$args_json" tags "__JSON__$tags_json")
    curl "${curl_args[@]}" -X POST "$API/terminals" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  get)
    curl "${curl_args[@]}" "$API/terminals/${1:?id required}" | pretty
    ;;

  output)
    id="${1:?id required}"; shift
    qs=()
    [[ -n "${1:-}" ]] && qs+=("since=$1")
    [[ -n "${2:-}" ]] && qs+=("tail=$2")
    url="$API/terminals/$id/output"
    if [[ ${#qs[@]} -gt 0 ]]; then
      url="$url?$(IFS='&'; echo "${qs[*]}")"
    fi
    curl "${curl_args[@]}" "$url" | pretty
    ;;

  stream)
    id="${1:?id required}"
    url="$API/terminals/$id/stream"
    [[ -n "${2:-}" ]] && url="$url?since=$2"
    curl "${curl_args[@]}" -N "$url"
    ;;

  search)
    q="${1:?regex required}"
    within="${2:-}"
    tag="${3:-}"
    qs=("q=$(python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))' "$q")")
    [[ -n "$within" ]] && qs+=("within_seconds=$within")
    [[ -n "$tag" ]] && qs+=("tag=$tag")
    curl "${curl_args[@]}" "$API/search?$(IFS='&'; echo "${qs[*]}")" | pretty
    ;;

  write|send)
    id="${1:?id required}"
    data="${2-}"
    if [[ -z "${2+set}" ]] && [[ ! -t 0 ]]; then
      data=$(cat)
    fi
    if [[ "$cmd" == "send" ]]; then
      payload=$(json_obj data "$data" submit "__TRUE__")
    else
      payload=$(json_obj data "$data")
    fi
    curl "${curl_args[@]}" -X POST "$API/terminals/$id/write" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  keys)
    id="${1:?id required}"; shift
    [[ $# -gt 0 ]] || { echo "at least one key required" >&2; exit 1; }
    keys_json=$(python3 -c 'import sys, json; print(json.dumps(sys.argv[1:]))' "$@")
    payload=$(json_obj keys "__JSON__$keys_json")
    curl "${curl_args[@]}" -X POST "$API/terminals/$id/keys" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  signal)
    id="${1:?id required}"
    sig="${2:-int}"
    payload=$(json_obj signal "$sig")
    curl "${curl_args[@]}" -X POST "$API/terminals/$id/signal" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  wait)
    id="${1:?id required}"
    pattern="${2:?pattern required}"
    timeout="${3:-30}"
    before="${4:-0}"
    payload=$(json_obj pattern "$pattern" timeout_seconds "__FLOAT__$timeout" \
      since "__INT__-1" before_context "__INT__$before")
    curl "${curl_args[@]}" -X POST "$API/terminals/$id/wait" \
      -H "Content-Type: application/json" -d "$payload" \
      --max-time $(python3 -c "print(float('$timeout') + 5)") | pretty
    ;;

  ack)
    id="${1:?id required}"
    watch="${2:-}"
    payload=$(json_obj watch "$watch")
    curl "${curl_args[@]}" -X POST "$API/terminals/$id/ack" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  restart)
    curl "${curl_args[@]}" -X POST "$API/terminals/${1:?id required}/restart" | pretty
    ;;

  close)
    curl "${curl_args[@]}" -X DELETE "$API/terminals/${1:?id required}" | pretty
    ;;

  resize)
    id="${1:?id required}"
    cols="${2:-80}"
    rows="${3:-24}"
    payload=$(json_obj cols "__INT__$cols" rows "__INT__$rows")
    curl "${curl_args[@]}" -X POST "$API/terminals/$id/resize" \
      -H "Content-Type: application/json" -d "$payload" | pretty
    ;;

  ""|-h|--help|help)
    usage
    [[ -z "$cmd" ]] && exit 1 || exit 0
    ;;

  *)
    echo "unknown command: $cmd" >&2
    usage
    exit 1
    ;;
esac
