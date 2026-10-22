# Renders docs/demo.gif: vhs docs/demo.tape (from the repo root).
# Needs a logged-in `claude`, run once in /tmp/todo to trust the folder.
# It sends Claude Code one prompt.
Output docs/demo.gif
Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 760
Set Padding 20
Set TypingSpeed 35ms
Set Theme "Catppuccin Mocha"

Hide
Type "go build -o /tmp/ptyd-demo/ptyd ./cmd/ptyd && rm -rf /tmp/ptyd-demo/state && rm -rf /tmp/todo && mkdir -p /tmp/todo && cd /tmp/todo" Enter
Type `printf 'import argparse\n\np = argparse.ArgumentParser(prog="todo")\np.add_argument("item", nargs="*")\nargs = p.parse_args()\nprint("added:", " ".join(args.item))\n' > todo.py` Enter
Type "unset CLAUDECODE CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_SESSION_ID CLAUDE_CODE_ENTRYPOINT" Enter
Type "export PATH=/tmp/ptyd-demo:$PATH PTYD_ADDR=127.0.0.1:4301 PTYD_PERSIST_DIR=/tmp/ptyd-demo/state XDG_STATE_HOME=/tmp/ptyd-demo/state PTYD_TOKEN=demo BASH_SILENCE_DEPRECATION_WARNING=1" Enter
Type `export API=http://$PTYD_ADDR AUTH="X-Ptyd-Token: $PTYD_TOKEN" PS1='\[\e[1;35m\]$ \[\e[0m\]'; clear` Enter
Sleep 2s
Show

Type "ptyd 2>/dev/null &   # start the daemon" Enter
Sleep 500ms
Type "ID=$(ptyd new -d -n agent -- claude --model=haiku)   # Claude Code, in the background" Enter
Sleep 4s
Type "# drive it over HTTP, the way a script or another agent would:" Enter
Type `curl -H "$AUTH" $API/terminals/$ID/write -d '{"data":"add a --version flag to todo.py","submit":true}'` Enter
Sleep 1s
Type `ptyd attach agent   # watch it work; Ctrl-\ twice detaches` Enter
Wait+Screen@90s /· done/
Sleep 2500ms
Ctrl+\
Ctrl+\
Sleep 800ms
Type "kill -9 %1   # crash the daemon" Enter
Sleep 800ms
Type "ptyd 2>/dev/null &   # start it again" Enter
Sleep 3s
Type "ptyd ls" Enter
Sleep 2s
Type "ptyd attach agent   # the same conversation, resumed" Enter
Wait+Screen@30s /· done/
Sleep 1500ms
Type "what did you just change?" Enter
Wait+Screen@60s /change\?[\s\S]*· done/
Sleep 6s

Hide
Ctrl+\
Ctrl+\
Type "ptyd kill agent; kill %2" Enter
Sleep 1s
