# Renders docs/demo.gif: vhs docs/demo.tape (from the repo root).
# macOS. Needs a logged-in `claude`, run once in /Users/Shared/app to trust the folder.
# It sends Claude Code two prompts. While the agent works the recording is
# paused, so the GIF jumps to the result.
Output docs/demo.gif
Set Shell bash
Set FontSize 16
Set Width 1200
Set Height 760
Set Padding 20
Set TypingSpeed 25ms
Set Framerate 20
Set Theme "Catppuccin Mocha"

Hide
Type "go build -o /tmp/ptyd-demo/ptyd ./cmd/ptyd && rm -rf /tmp/ptyd-demo/state && docs/demo-setup.sh /Users/Shared/app && cd /Users/Shared/app" Enter
Type "unset CLAUDECODE CLAUDE_CODE_CHILD_SESSION CLAUDE_CODE_SESSION_ID CLAUDE_CODE_ENTRYPOINT" Enter
Type "export PATH=/tmp/ptyd-demo:$PATH PTYD_ADDR=127.0.0.1:4301 PTYD_PERSIST_DIR=/tmp/ptyd-demo/state XDG_STATE_HOME=/tmp/ptyd-demo/state PTYD_TOKEN=demo BASH_SILENCE_DEPRECATION_WARNING=1" Enter
Type `export API=http://$PTYD_ADDR AUTH="X-Ptyd-Token: $PTYD_TOKEN" PS1='\[\e[1;35m\]$ \[\e[0m\]'; clear` Enter
Sleep 2s
Show

# 1. Run Claude Code under ptyd and prompt it over HTTP.
Type "ptyd 2>/dev/null &   # start the daemon" Enter
Sleep 300ms
Type "ID=$(ptyd new -d -n agent -- claude --model=haiku)   # Claude Code, in the background" Enter
Hide
Sleep 4s
Show
Type@12ms `curl -H "$AUTH" $API/terminals/$ID/write -d '{"data":"add a --version flag to todo.py","submit":true}'` Enter
Sleep 500ms
Type `ptyd attach agent   # Ctrl-\ twice detaches` Enter
Sleep 1500ms
Hide
Wait+Screen@90s /· done/
Show
Sleep 2500ms
Ctrl+\
Ctrl+\
Sleep 300ms

# 2. Crash the daemon; the conversation comes back.
Type "kill -9 %1   # crash the daemon" Enter
Sleep 300ms
Type "ptyd 2>/dev/null &   # start it again" Enter
Sleep 300ms
Type "ptyd attach agent   # same conversation, resumed" Enter
Hide
Wait+Screen@30s /· done/
Sleep 500ms
Show
Sleep 2500ms
Ctrl+\
Ctrl+\

# 3. A dozen terminals, managed by the agent.
Type@12ms "clear; for s in */; do ptyd new -d -n ${s%/} -- python3 -m unittest discover -v -s $s; done >/dev/null; sleep 1; ptyd ls" Enter
Sleep 2500ms
Type "ptyd attach agent" Enter
Sleep 800ms
Type@15ms "12 test suites ran in ptyd terminals (ptyd help). Fix the failing one and rerun it with ptyd restore. Be brief." Enter
Sleep 1500ms
Hide
Wait+Screen@180s /brief\.[\s\S]*· done/
Show
Sleep 3s
Ctrl+\
Ctrl+\
Sleep 300ms
Type "clear; ptyd ls" Enter
Sleep 3s

Hide
Type "for t in $(ptyd ls | awk 'NR>1{print $1}'); do ptyd kill $t; done; kill %2" Enter
Sleep 2s
