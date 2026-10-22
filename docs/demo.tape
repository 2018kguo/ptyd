# Renders docs/demo.gif: vhs docs/demo.tape (from the repo root).
# macOS. Needs a logged-in `claude`, run once in /Users/Shared/app to trust the folder.
# It sends Claude Code three prompts.
Output docs/demo.gif
Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 760
Set Padding 20
Set TypingSpeed 35ms
Set Framerate 20
Set Theme "Catppuccin Mocha"

Hide
Type "go build -o /tmp/ptyd-demo/ptyd ./cmd/ptyd && rm -rf /tmp/ptyd-demo/state && docs/demo-setup.sh /Users/Shared/app && cd /Users/Shared/app" Enter
Type "unset CLAUDECODE CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_SESSION_ID CLAUDE_CODE_ENTRYPOINT" Enter
Type "export PATH=/tmp/ptyd-demo:$PATH PTYD_ADDR=127.0.0.1:4301 PTYD_PERSIST_DIR=/tmp/ptyd-demo/state XDG_STATE_HOME=/tmp/ptyd-demo/state PTYD_TOKEN=demo BASH_SILENCE_DEPRECATION_WARNING=1" Enter
Type `export API=http://$PTYD_ADDR AUTH="X-Ptyd-Token: $PTYD_TOKEN" PS1='\[\e[1;35m\]$ \[\e[0m\]'; clear` Enter
Sleep 2s
Show

# 1. Run Claude Code under ptyd and drive it over HTTP.
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

# 2. Crash the daemon; the conversation comes back.
Type "kill -9 %1   # crash the daemon" Enter
Sleep 800ms
Type "ptyd 2>/dev/null &   # start it again" Enter
Sleep 3s
Type "ptyd attach agent   # the same conversation, resumed" Enter
Wait+Screen@30s /· done/
Sleep 1500ms
Type "what did you just change?" Enter
Wait+Screen@60s /change\?[\s\S]*· done/
Sleep 3s
Ctrl+\
Ctrl+\
Sleep 800ms

# 3. Many terminals, managed by the agent.
Type "clear; for s in */; do ptyd new -d -n ${s%/} -- python3 -m unittest discover -v -s $s; done >/dev/null" Enter
Sleep 1500ms
Type "ptyd ls" Enter
Sleep 3500ms
Type "ptyd attach agent" Enter
Sleep 1500ms
Type "12 test suites ran in other ptyd terminals (see ptyd help). Find the one that failed, fix it, rerun it in ptyd. Keep the reply short." Enter
Wait+Screen@180s /short\.[\s\S]*· done/
Sleep 3s
Ctrl+\
Ctrl+\
Sleep 800ms
Type "clear; ptyd ls" Enter
Sleep 5s

Hide
Type "for t in $(ptyd ls | awk 'NR>1{print $1}'); do ptyd kill $t; done; kill %2" Enter
Sleep 2s
