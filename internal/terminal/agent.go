package terminal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Agent adapters let the agent restore policy bring back the exact
// conversation a coding agent was in. They share one mechanism: ptyd
// injects two lifecycle hooks into the agent's own hook system when it
// launches it, and the hooks write to files in the terminal's directory:
//
//   - SessionStart (fires when a session starts, is resumed, cleared or
//     compacted, with the session id and transcript path on stdin) saves
//     that JSON to agent-session.json and removes the prompted marker.
//   - UserPromptSubmit (fires before each prompt) creates
//     agent-session.json.prompted; the prompt text is discarded.
//
// So agent-session.json always names the session the agent is in now,
// whatever the user did inside it (/clear, /resume, /new), and the marker
// says whether a message was sent in it. The hooks are plain sh with the
// paths embedded, so they depend on nothing but the files.
//
// On restore ptyd relaunches the agent only with that exact session id,
// and only if its transcript exists, the session's directory is the
// terminal's, and the id is well formed; a session with no transcript and
// no message sent is replaced by a new session in the same directory
// (nothing is lost). In every other case it does not start the agent:
// the terminal stays a ghost showing its last screen with a notice. ptyd
// never asks an agent for "the most recent" session or opens a picker.
type agentAdapter interface {
	// validate rejects arguments ptyd cannot carry over safely.
	validate(args []string) error
	// launch returns the arguments for a new session with the hooks.
	launch(args []string, stateFile string) []string
	// resume returns the arguments that resume exactly session id.
	resume(args []string, stateFile, id string) []string
}

var agents = map[string]agentAdapter{
	"claude": claudeAgent{},
	"codex":  codexAgent{},
}

func supportedAgents() string {
	return strings.Join(slices.Sorted(maps.Keys(agents)), ", ")
}

// detectAgent maps a command to an adapter name by its basename.
func detectAgent(command string) string {
	name := filepath.Base(command)
	if _, ok := agents[name]; ok {
		return name
	}
	return ""
}

func agentStateFile(persistDir string, id ID) string {
	return filepath.Join(terminalDir(persistDir, id), "agent-session.json")
}

// shellQuote quotes s for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Hook commands (see agentAdapter).
func sessionStartHook(stateFile string) string {
	q, tmp := shellQuote(stateFile), shellQuote(stateFile+".tmp")
	return fmt.Sprintf("cat > %s.$$ && mv -f %s.$$ %s && rm -f %s", tmp, tmp, q, shellQuote(stateFile+".prompted"))
}

func promptHook(stateFile string) string {
	return fmt.Sprintf("cat > /dev/null; : > %s", shellQuote(stateFile+".prompted"))
}

// checkLongOptions enforces that every argument is a long option
// (--name or --name=value) and none is in forbidden. Positional arguments
// (an initial prompt would be sent again on every restore) and the
// two-argument --name value form are ambiguous to carry over, so they
// are refused.
func checkLongOptions(args []string, forbidden ...string) error {
	for _, a := range args {
		if !strings.HasPrefix(a, "--") || a == "--" {
			return fmt.Errorf("with restore \"agent\" every argument must be a long option written --name or --name=value (got %q); send the first prompt with /write instead", a)
		}
		name, _, _ := strings.Cut(a, "=")
		if slices.Contains(forbidden, name) {
			return fmt.Errorf("argument %s cannot be used with restore \"agent\": ptyd chooses the session and must be able to resume it", name)
		}
	}
	return nil
}

// --- Claude Code ---

type claudeAgent struct{}

func (claudeAgent) validate(args []string) error {
	// Session choice, sessions elsewhere (cloud, desktop, worktrees),
	// one-shot modes, and modes that skip hooks or transcripts.
	return checkLongOptions(args, "--continue", "--resume", "--session-id", "--fork-session",
		"--from-pr", "--teleport", "--cloud", "--desktop", "--worktree", "--tmux", "--bg", "--background",
		"--print", "--file", "--settings", "--bare", "--safe-mode", "--no-session-persistence")
}

// settings is the --settings JSON. Claude Code merges hooks from flag
// settings with the user's and the project's, so this adds to them.
func (claudeAgent) settings(stateFile string) string {
	hook := func(cmd string) []map[string]any {
		return []map[string]any{{"hooks": []map[string]any{{"type": "command", "command": cmd}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionStart":     hook(sessionStartHook(stateFile)),
		"UserPromptSubmit": hook(promptHook(stateFile)),
	}})
	return string(b)
}

func (c claudeAgent) launch(args []string, stateFile string) []string {
	return append(slices.Clone(args), "--session-id", newUUID(), "--settings", c.settings(stateFile))
}

func (c claudeAgent) resume(args []string, stateFile, id string) []string {
	return append(slices.Clone(args), "--resume", id, "--settings", c.settings(stateFile))
}

// --- Codex ---

type codexAgent struct{}

func (codexAgent) validate(args []string) error {
	// Session choice, a different directory, and inputs that would be
	// sent again on every resume.
	if err := checkLongOptions(args, "--last", "--all", "--include-non-interactive", "--cd", "--worktree", "--image"); err != nil {
		return err
	}
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--config="); ok && strings.HasPrefix(strings.TrimSpace(v), "hooks") {
			return fmt.Errorf("--config=hooks... cannot be used with restore \"agent\": ptyd sets session hooks")
		}
	}
	return nil
}

// Codex runs a hook only if it is trusted. Trust is a hash of the hook's
// normalized definition stored under hooks.state; ptyd passes that state
// for its own hooks in the same session-flag config layer, so they run
// without a review prompt and the user's trust settings are untouched.
const codexHookTimeout = 10

func codexHookHash(event, cmd string, timeout int) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]any{
		"event_name": event,
		"hooks":      []map[string]any{{"async": false, "command": cmd, "timeout": timeout, "type": "command"}},
	})
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// tomlString encodes s as a TOML basic string.
func tomlString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(buf.String(), "\n")
}

func (codexAgent) config(stateFile string) []string {
	start, prompt := sessionStartHook(stateFile), promptHook(stateFile)
	group := func(cmd string) string {
		return fmt.Sprintf(`[{hooks=[{type="command",command=%s,timeout=%d}]}]`, tomlString(cmd), codexHookTimeout)
	}
	const layer = "/<session-flags>/config.toml"
	return []string{
		"-c", "hooks.SessionStart=" + group(start),
		"-c", "hooks.UserPromptSubmit=" + group(prompt),
		"-c", fmt.Sprintf(`hooks.state={"%s:session_start:0:0"={trusted_hash="%s"},"%s:user_prompt_submit:0:0"={trusted_hash="%s"}}`,
			layer, codexHookHash("session_start", start, codexHookTimeout), layer, codexHookHash("user_prompt_submit", prompt, codexHookTimeout)),
	}
}

func (c codexAgent) launch(args []string, stateFile string) []string {
	return append(c.config(stateFile), args...)
}

func (c codexAgent) resume(args []string, stateFile, id string) []string {
	out := append([]string{"resume"}, c.config(stateFile)...)
	out = append(out, args...)
	return append(out, "--", id)
}

// --- session state written by the hooks ---

type agentState struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	Source         string `json:"source"`
}

func readAgentState(stateFile string) (agentState, error) {
	var st agentState
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(raw, &st)
}

func agentPrompted(stateFile string) bool {
	_, err := os.Stat(stateFile + ".prompted")
	return err == nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func newUUID() string {
	b := []byte(randHex(16))
	// Version 4, RFC 4122 variant.
	b[12] = '4'
	b[16] = "89ab"[b[16]%4]
	return fmt.Sprintf("%s-%s-%s-%s-%s", b[0:8], b[8:12], b[12:16], b[16:20], b[20:32])
}

func sameDir(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// agentPlan decides how to restore an agent terminal: the arguments to
// launch it with, or ok=false and a notice when it must not be launched.
func agentPlan(name string, a agentAdapter, cfg CreateConfig, stateFile string) (args []string, notice string, ok bool) {
	st, err := readAgentState(stateFile)
	if err != nil {
		return nil, fmt.Sprintf("ptyd: could not identify the %s session to resume; showing last screen", name), false
	}
	id := st.SessionID
	if !uuidPattern.MatchString(id) {
		return nil, fmt.Sprintf("ptyd: %s reported an invalid session id %q; not resuming, showing last screen", name, id), false
	}
	if !sameDir(st.CWD, cfg.CWD) {
		return nil, fmt.Sprintf("ptyd: %s session %s ran in %s, not %s; not resuming, showing last screen", name, id, st.CWD, cfg.CWD), false
	}
	tp := st.TranscriptPath
	if !filepath.IsAbs(tp) || !strings.Contains(filepath.Base(tp), id) {
		return nil, fmt.Sprintf("ptyd: %s session %s has no usable transcript path; not resuming, showing last screen", name, id), false
	}
	if fi, err := os.Stat(tp); err == nil && fi.Mode().IsRegular() {
		return a.resume(cfg.Args, stateFile, id), fmt.Sprintf("ptyd: resumed %s session %s", name, id), true
	}
	if agentPrompted(stateFile) {
		return nil, fmt.Sprintf("ptyd: %s session %s has no transcript at %s; not resuming, showing last screen", name, id, tp), false
	}
	// The session never got a message, so there is nothing to resume: a
	// new session in the same directory loses nothing.
	_ = os.Remove(stateFile)
	return a.launch(cfg.Args, stateFile), fmt.Sprintf("ptyd: %s session %s had no messages yet; started a new session", name, id), true
}
