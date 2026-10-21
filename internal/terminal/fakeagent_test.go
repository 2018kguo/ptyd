package terminal

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The test binary doubles as fake claude and codex binaries: a symlink
// named after the agent points at it, and PTYD_FAKE_AGENT turns it into
// the fake. The fakes mimic the parts of the real CLIs ptyd relies on:
// launch and resume flags, injected hooks (run through sh with the event
// JSON on stdin, as the real ones do), transcripts on disk, and in-app
// session switches. Every invocation is logged so tests can check that
// no "most recent"/picker form is ever used.

func TestMain(m *testing.M) {
	if os.Getenv("PTYD_FAKE_AGENT") != "" {
		os.Exit(runFakeAgent(filepath.Base(os.Args[0]), os.Args[1:]))
	}
	if dir := os.Getenv("PTYD_KILL9_TAIL"); dir != "" {
		writeTailThenDie(dir)
	}
	os.Exit(m.Run())
}

// fakeAgentEnv sets up a bin dir with fake agents and a home for their
// transcripts, returning the bin dir and the env for CreateConfig.
func fakeAgentEnv(t *testing.T) (bin string, home string, env map[string]string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	bin, home = filepath.Join(root, "bin"), filepath.Join(root, "home")
	for _, d := range []string{bin, home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"claude", "codex"} {
		if err := os.Symlink(self, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	return bin, home, map[string]string{"PTYD_FAKE_AGENT": "1", "FAKE_HOME": home}
}

func fakeInvocations(home string) []string {
	raw, _ := os.ReadFile(filepath.Join(home, "invocations.log"))
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

type fakeAgent struct {
	name, home, cwd string
	hooks           map[string]string // event -> command
	session         string
	transcript      string
}

func (f *fakeAgent) transcriptPath(id string) string {
	if f.name == "codex" {
		return filepath.Join(f.home, "sessions", "rollout-"+id+".jsonl")
	}
	return filepath.Join(f.home, "projects", strings.ReplaceAll(f.cwd, "/", "-"), id+".jsonl")
}

func (f *fakeAgent) runHook(event string, payload map[string]any) {
	cmd, ok := f.hooks[event]
	if !ok {
		return
	}
	payload["hook_event_name"] = event
	payload["cwd"] = f.cwd
	in, _ := json.Marshal(payload)
	c := exec.Command("sh", "-c", cmd)
	c.Stdin = strings.NewReader(string(in))
	if out, err := c.CombinedOutput(); err != nil {
		fmt.Printf("hook %s failed: %v %s\r\n", event, err, out)
	}
}

func (f *fakeAgent) start(id, source string) {
	f.session = id
	f.transcript = f.transcriptPath(id)
	f.runHook("SessionStart", map[string]any{"session_id": id, "transcript_path": f.transcript, "source": source})
}

func runFakeAgent(name string, args []string) int {
	home := os.Getenv("FAKE_HOME")
	cwd, _ := os.Getwd()
	logf, _ := os.OpenFile(filepath.Join(home, "invocations.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	fmt.Fprintf(logf, "%s %s\n", name, strings.Join(args, " "))
	logf.Close()
	f := &fakeAgent{name: name, home: home, cwd: cwd, hooks: map[string]string{}}

	var resumeID, sessionID string
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			i++
			if i < len(args) {
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--continue" || a == "-c" && name == "claude" || a == "--last" || a == "--resume" && i+1 == len(args):
			fmt.Printf("FORBIDDEN-PICKER %s\r\n", a)
			return 3
		case name == "claude" && a == "--session-id":
			sessionID = next()
		case name == "claude" && a == "--resume":
			resumeID = next()
		case name == "claude" && a == "--settings":
			var s struct {
				Hooks map[string][]struct {
					Hooks []struct{ Command string } `json:"hooks"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(next()), &s); err != nil {
				fmt.Printf("bad settings: %v\r\n", err)
				return 2
			}
			for ev, groups := range s.Hooks {
				f.hooks[ev] = groups[0].Hooks[0].Command
			}
		case name == "codex" && a == "resume":
		case name == "codex" && a == "--":
			resumeID = next()
		case name == "codex" && a == "-c":
			if !f.codexConfig(next()) {
				fmt.Printf("Hooks need review\r\n")
				f.hooks = map[string]string{}
			}
		}
	}
	if name == "codex" && f.hooks["trust"] != "ok" {
		f.hooks = map[string]string{}
	}

	switch {
	case resumeID != "":
		if _, err := os.Stat(f.transcriptPath(resumeID)); err != nil {
			fmt.Printf("No conversation found with session ID: %s\r\n", resumeID)
			return 1
		}
		if name == "claude" {
			f.start(resumeID, "resume")
		} else {
			f.session = resumeID // codex: SessionStart fires on the first turn
			f.transcript = f.transcriptPath(resumeID)
		}
		fmt.Printf("fake-%s resumed %s\r\n", name, resumeID)
	case name == "claude":
		if sessionID == "" {
			sessionID = newUUID()
		}
		f.start(sessionID, "startup")
		fmt.Printf("fake-claude new %s\r\n", sessionID)
	default:
		fmt.Printf("fake-codex ready\r\n")
	}
	pendingStart := name == "codex" // codex starts or resumes a thread lazily
	if os.Getenv("FAKE_ALT") != "" {
		fmt.Print("\x1b[?1049h\x1b[H") // full-screen UI, as Claude Code's fullscreen mode
	}

	in := bufio.NewScanner(os.Stdin)
	for fmt.Print("> "); in.Scan(); fmt.Print("> ") {
		line := strings.TrimSpace(in.Text())
		switch {
		case line == "/clear" && name == "claude":
			f.start(newUUID(), "clear")
			fmt.Printf("cleared, now %s\r\n", f.session)
		case strings.HasPrefix(line, "/resume ") && name == "claude":
			f.start(strings.TrimPrefix(line, "/resume "), "resume")
			fmt.Printf("switched to %s\r\n", f.session)
		case line == "/new" && name == "codex":
			f.session, pendingStart = "", true
			fmt.Printf("new thread\r\n")
		case line == "":
		default:
			if pendingStart {
				if f.session == "" {
					f.start(newUUID(), "startup")
				} else {
					f.start(f.session, "resume")
				}
				pendingStart = false
			}
			f.runHook("UserPromptSubmit", map[string]any{"session_id": f.session, "prompt": line})
			_ = os.MkdirAll(filepath.Dir(f.transcript), 0o700)
			tf, _ := os.OpenFile(f.transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			fmt.Fprintf(tf, "%q\n", line)
			tf.Close()
			fmt.Printf("[%s] reply to %q\r\n", f.session, line)
		}
	}
	return 0
}

var codexHookArg = regexp.MustCompile(`^hooks\.(\w+)=\[\{hooks=\[\{type="command",command=("(?:[^"\\]|\\.)*"),timeout=(\d+)\}\]\}\]$`)

// codexConfig applies one -c override. Like codex, hooks are only kept
// if hooks.state trusts their hash.
func (f *fakeAgent) codexConfig(kv string) bool {
	if m := codexHookArg.FindStringSubmatch(kv); m != nil {
		var cmd string
		_ = json.Unmarshal([]byte(m[2]), &cmd)
		f.hooks[m[1]] = cmd
		return true
	}
	if strings.HasPrefix(kv, "hooks.state=") {
		for ev, label := range map[string]string{"SessionStart": "session_start", "UserPromptSubmit": "user_prompt_submit"} {
			cmd := f.hooks[ev]
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(false) // codex hashes serde_json output, which does not escape <>&
			_ = enc.Encode(map[string]any{"event_name": label, "hooks": []map[string]any{{"async": false, "command": cmd, "timeout": codexHookTimeout, "type": "command"}}})
			ident := bytes.TrimSpace(buf.Bytes())
			sum := sha256.Sum256(ident)
			want := fmt.Sprintf(`"/<session-flags>/config.toml:%s:0:0"={trusted_hash="sha256:%s"}`, label, hex.EncodeToString(sum[:]))
			if !strings.Contains(kv, want) {
				return false
			}
		}
		f.hooks["trust"] = "ok"
		return true
	}
	return true
}
