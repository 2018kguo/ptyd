package terminal

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func waitScreen(t *testing.T, m *Manager, id ID, re string) string {
	t.Helper()
	pat := regexp.MustCompile(re)
	var text string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		scr, err := m.Screen(id, true)
		if err == nil {
			text = strings.Join(append(scr.Scrollback, scr.Lines...), "\n")
			if pat.MatchString(text) {
				return text
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen of %s never matched %q:\n%s", id, re, text)
		}
	}
}

func mustAgentState(t *testing.T, dir string, id ID) agentState {
	t.Helper()
	st, err := readAgentState(agentStateFile(dir, id))
	if err != nil {
		t.Fatalf("agent state: %v", err)
	}
	return st
}

func waitAgentSession(t *testing.T, dir string, id ID, cond func(string) bool) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if st, err := readAgentState(agentStateFile(dir, id)); err == nil && cond(st.SessionID) {
			return st.SessionID
		}
		if time.Now().After(deadline) {
			t.Fatal("agent session never reported")
		}
	}
}

// invocation waits for the n-th (1-based) launch of a fake agent.
func invocation(t *testing.T, home string, n int) string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if inv := fakeInvocations(home); len(inv) >= n && inv[0] != "" {
			return inv[n-1]
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent launch %d never happened: %q", n, fakeInvocations(home))
		}
	}
}

func assertNoPickerForms(t *testing.T, home string) {
	t.Helper()
	for _, inv := range fakeInvocations(home) {
		for _, bad := range []string{"--continue", "--last", " -c ", " -r "} {
			if strings.Contains(inv+" ", bad) && !strings.HasPrefix(inv, "codex") {
				t.Fatalf("invocation used %q: %s", bad, inv)
			}
		}
		if strings.HasPrefix(inv, "codex") && strings.Contains(inv, "--last") {
			t.Fatalf("codex invocation used --last: %s", inv)
		}
	}
}

// Claude: the session changes mid-run (/clear); after a crash the restore
// resumes exactly the latest session, by id, in the same terminal id.
func TestClaudeResumesLatestSessionAfterCrash(t *testing.T) {
	bin, home, env := fakeAgentEnv(t)
	dir := filepath.Join(t.TempDir(), "p")
	cwd := t.TempDir()
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "cl", Command: filepath.Join(bin, "claude"), CWD: cwd, Env: env,
		Restore: RestoreAgent, Args: []string{"--model=fake", "--dangerously-skip-permissions"}})
	if err != nil {
		t.Fatal(err)
	}
	first := waitAgentSession(t, dir, info.ID, func(string) bool { return true })
	_ = m1.Write(info.ID, "hello one\r")
	waitScreen(t, m1, info.ID, `reply to "hello one"`)
	_ = m1.Write(info.ID, "/clear\r")
	second := waitAgentSession(t, dir, info.ID, func(s string) bool { return s != first })
	_ = m1.Write(info.ID, "hello two\r")
	waitScreen(t, m1, info.ID, `\[`+second+`\] reply to "hello two"`)
	if got, _ := m1.Get(info.ID); got.AgentSession != second {
		t.Fatalf("Info.AgentSession = %q, want %q", got.AgentSession, second)
	}
	oldPID := info.PID

	// Crash: m1 is abandoned with its process still running. The next
	// daemon must stop that process before resuming, never run two.
	m2 := persistManager(t, dir)
	m2.RestoreGhosts()
	got, err := m2.Get(info.ID)
	if err != nil || got.Status != StatusRunning || got.Restored != 1 {
		t.Fatalf("not restored: %+v %v", got, err)
	}
	if syscall.Kill(oldPID, 0) == nil {
		t.Fatalf("previous claude %d still running", oldPID)
	}
	text := waitScreen(t, m2, info.ID, `fake-claude resumed `+second)
	if !strings.Contains(text, `reply to "hello two"`) {
		t.Fatalf("old screen not kept above the resumed session:\n%s", text)
	}
	if inv := invocation(t, home, 2); !strings.Contains(inv, "--resume "+second) || !strings.Contains(inv, "--model=fake") || !strings.Contains(inv, "--dangerously-skip-permissions") {
		t.Fatalf("resumed with %q", inv)
	}
	assertNoPickerForms(t, home)

	// The resumed session keeps being tracked, and a graceful restart
	// (a crash loop of sorts) resumes it again.
	_ = m2.Write(info.ID, "hello three\r")
	waitScreen(t, m2, info.ID, `\[`+second+`\] reply to "hello three"`)
	m2.CloseAll()
	m3 := persistManager(t, dir)
	m3.RestoreGhosts()
	waitScreen(t, m3, info.ID, `fake-claude resumed `+second)
	if got, _ := m3.Get(info.ID); got.Restored != 2 {
		t.Fatalf("restored count %d", got.Restored)
	}
}

// Claude after /clear with no message: no transcript exists for the new
// session, so the restore starts a new session (nothing to lose).
func TestClaudeClearedSessionWithoutMessageStartsFresh(t *testing.T) {
	bin, home, env := fakeAgentEnv(t)
	dir := filepath.Join(t.TempDir(), "p")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "cl", Command: filepath.Join(bin, "claude"), CWD: t.TempDir(), Env: env, Restore: RestoreAgent})
	if err != nil {
		t.Fatal(err)
	}
	first := waitAgentSession(t, dir, info.ID, func(string) bool { return true })
	_ = m1.Write(info.ID, "msg\r")
	waitScreen(t, m1, info.ID, `reply to "msg"`)
	_ = m1.Write(info.ID, "/clear\r")
	cleared := waitAgentSession(t, dir, info.ID, func(s string) bool { return s != first })
	m1.CloseAll()

	m2 := persistManager(t, dir)
	m2.RestoreGhosts()
	got, _ := m2.Get(info.ID)
	if got.Status != StatusRunning || !strings.Contains(got.Notice, "had no messages yet") {
		t.Fatalf("restore: %+v", got)
	}
	inv := invocation(t, home, 2)
	if strings.Contains(inv, "--resume") || !strings.Contains(inv, "--session-id") || strings.Contains(inv, cleared) || strings.Contains(inv, first) {
		t.Fatalf("expected a fresh session, got %q", inv)
	}
}

// When ptyd cannot be sure which session the agent was in, it does not
// start it: the terminal stays a ghost with a notice on its last screen,
// and later startups do not retry (or repeat the notice).
func TestAgentRestoreFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, notice string
		break_       func(t *testing.T, dir string, id ID, home string)
	}{
		{"unknown session", "could not identify the claude session to resume", func(t *testing.T, dir string, id ID, _ string) {
			_ = os.Remove(agentStateFile(dir, id))
		}},
		{"unknown session, full-screen UI", "could not identify the claude session to resume", func(t *testing.T, dir string, id ID, _ string) {
			_ = os.Remove(agentStateFile(dir, id))
		}},
		{"transcript missing", "has no transcript", func(t *testing.T, dir string, id ID, _ string) {
			_ = os.Remove(mustAgentState(t, dir, id).TranscriptPath)
		}},
		{"other directory", "ran in", func(t *testing.T, dir string, id ID, _ string) {
			st := mustAgentState(t, dir, id)
			st.CWD = os.TempDir()
			raw := []byte(`{"session_id":"` + st.SessionID + `","transcript_path":"` + st.TranscriptPath + `","cwd":"` + st.CWD + `"}`)
			_ = os.WriteFile(agentStateFile(dir, id), raw, 0o600)
		}},
		{"invalid id", "invalid session id", func(t *testing.T, dir string, id ID, _ string) {
			_ = os.WriteFile(agentStateFile(dir, id), []byte(`{"session_id":"--continue","transcript_path":"/x"}`), 0o600)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin, home, env := fakeAgentEnv(t)
			if strings.Contains(tc.name, "full-screen") {
				env["FAKE_ALT"] = "1"
			}
			dir := filepath.Join(t.TempDir(), "p")
			cwd := t.TempDir()
			m1 := NewManagerWithOptions(Options{PersistDir: dir})
			info, err := m1.Create(CreateConfig{Name: "cl", Command: filepath.Join(bin, "claude"), CWD: cwd, Env: env, Restore: RestoreAgent})
			if err != nil {
				t.Fatal(err)
			}
			waitAgentSession(t, dir, info.ID, func(string) bool { return true })
			_ = m1.Write(info.ID, "important\r")
			waitScreen(t, m1, info.ID, `reply to "important"`)
			m1.CloseAll()
			launches := len(fakeInvocations(home))
			tc.break_(t, dir, info.ID, home)

			m2 := persistManager(t, dir)
			m2.RestoreGhosts()
			got, _ := m2.Get(info.ID)
			if got.Status != StatusExited || !strings.Contains(got.Notice, tc.notice) {
				t.Fatalf("want a ghost with notice %q, got %+v", tc.notice, got)
			}
			if n := len(fakeInvocations(home)); n != launches {
				t.Fatalf("agent was launched: %v", fakeInvocations(home)[launches:])
			}
			text := waitScreen(t, m2, info.ID, regexp.QuoteMeta(tc.notice))
			if !strings.Contains(text, `reply to "important"`) {
				t.Fatalf("last screen not shown:\n%q", text)
			}
			// A crash loop does not retry or repeat the notice.
			m3 := persistManager(t, dir)
			m3.RestoreGhosts()
			if got, _ := m3.Get(info.ID); got.Status != StatusExited {
				t.Fatalf("restored on second startup: %+v", got)
			}
			if text := waitScreen(t, m3, info.ID, regexp.QuoteMeta(tc.notice)); strings.Count(text, tc.notice) != 1 {
				t.Fatalf("notice repeated:\n%s", text)
			}
			// An explicit restore with the agent policy is declined the same way.
			if _, err := m3.Restore(info.ID, ""); !errors.Is(err, ErrRestoreDeclined) {
				t.Fatalf("explicit restore: %v", err)
			}
		})
	}
}

// Codex: no id at launch; the hook reports the thread on its first turn.
// After /new and a message, a crash restores exactly the new thread.
func TestCodexResumesLatestThread(t *testing.T) {
	bin, home, env := fakeAgentEnv(t)
	dir := filepath.Join(t.TempDir(), "p")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "cx", Command: filepath.Join(bin, "codex"), CWD: t.TempDir(), Env: env,
		Restore: RestoreAgent, Args: []string{"--model=fake", "--dangerously-bypass-approvals-and-sandbox"}})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, m1, info.ID, `fake-codex ready`)
	if strings.Contains(waitScreen(t, m1, info.ID, `>`), "need review") {
		t.Fatal("codex asked to review ptyd's hooks: trust hash mismatch")
	}
	_ = m1.Write(info.ID, "first\r")
	first := waitAgentSession(t, dir, info.ID, func(string) bool { return true })
	_ = m1.Write(info.ID, "/new\r")
	_ = m1.Write(info.ID, "second\r")
	second := waitAgentSession(t, dir, info.ID, func(s string) bool { return s != first })
	waitScreen(t, m1, info.ID, `\[`+second+`\] reply to "second"`)

	m2 := persistManager(t, dir) // crash: m1 abandoned
	m2.RestoreGhosts()
	waitScreen(t, m2, info.ID, `fake-codex resumed `+second)
	inv := invocation(t, home, 2)
	if !strings.HasPrefix(inv, "codex resume ") || !strings.HasSuffix(inv, "-- "+second) || !strings.Contains(inv, "--model=fake") || !strings.Contains(inv, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("resumed with %q", inv)
	}
	assertNoPickerForms(t, home)
	// The resumed thread is still tracked.
	_ = m2.Write(info.ID, "third\r")
	waitScreen(t, m2, info.ID, `\[`+second+`\] reply to "third"`)
}

// Codex that never got a message has no thread; ptyd cannot tell that
// from hooks that did not run, so it does not relaunch it.
func TestCodexWithoutThreadStaysGhost(t *testing.T) {
	bin, _, env := fakeAgentEnv(t)
	dir := filepath.Join(t.TempDir(), "p")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "cx", Command: filepath.Join(bin, "codex"), CWD: t.TempDir(), Env: env, Restore: RestoreAgent})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, m1, info.ID, `fake-codex ready`)
	m1.CloseAll()
	m2 := persistManager(t, dir)
	m2.RestoreGhosts()
	if got, _ := m2.Get(info.ID); got.Status != StatusExited || !strings.Contains(got.Notice, "could not identify the codex session") {
		t.Fatalf("got %+v", got)
	}
}

// rerun: the same command starts again under the same id, below the old
// screen; a program that ended by itself is not restarted.
func TestRerunRestore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "sh", Command: "sh", Cols: 40, Rows: 6, Restore: RestoreRerun,
		Args: []string{"-c", `echo "run at $$"; exec cat`}})
	if err != nil {
		t.Fatal(err)
	}
	ended, err := m1.Create(CreateConfig{Name: "done", Command: "sh", Restore: RestoreRerun, Args: []string{"-c", "echo bye"}})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, m1, info.ID, `run at`)
	waitFor(t, "short program to exit", func() bool { got, _ := m1.Get(ended.ID); return got.Status == StatusExited })
	time.Sleep(100 * time.Millisecond)
	before := waitScreen(t, m1, info.ID, `run at`)

	m2 := persistManager(t, dir)
	m2.RestoreGhosts()
	got, _ := m2.Get(info.ID)
	if got.Status != StatusRunning || got.PID == info.PID || got.Restored != 1 {
		t.Fatalf("rerun: %+v", got)
	}
	text := waitScreen(t, m2, info.ID, `(?s)run at.*restarted sh.*run at`)
	if !strings.HasPrefix(text, strings.TrimRight(before, "\n")+"\n") {
		t.Fatalf("old output not above the new run:\n%q\nbefore:\n%q", text, before)
	}
	if g, _ := m2.Get(ended.ID); g.Status != StatusExited {
		t.Fatalf("a program that exited by itself was restarted: %+v", g)
	}
	// It is a live terminal now.
	_ = m2.Write(info.ID, "echo-me\r")
	waitScreen(t, m2, info.ID, `echo-me\n(.*\n)?echo-me`)
}

// Concurrent restores of one ghost start one process.
func TestRestoreIsExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "sh", Command: "sh", Restore: RestoreRerun, Args: []string{"-c", "exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	m1.CloseAll()
	m2 := persistManager(t, dir)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, conflicts int
	for range 8 {
		wg.Go(func() {
			_, err := m2.Restore(info.ID, "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrNotGhost):
				conflicts++
			default:
				t.Errorf("restore: %v", err)
			}
		})
	}
	wg.Wait()
	if ok != 1 || conflicts != 7 {
		t.Fatalf("%d restores succeeded, %d conflicts", ok, conflicts)
	}
}

func TestAgentCreateValidation(t *testing.T) {
	bin, _, env := fakeAgentEnv(t)
	m := persistManager(t, filepath.Join(t.TempDir(), "p"))
	claude := filepath.Join(bin, "claude")
	for _, tc := range []struct {
		cfg  CreateConfig
		want string
	}{
		{CreateConfig{Command: claude, Restore: RestoreAgent, Args: []string{"fix the bug"}}, "long option"},
		{CreateConfig{Command: claude, Restore: RestoreAgent, Args: []string{"--model", "opus"}}, "long option"},
		{CreateConfig{Command: claude, Restore: RestoreAgent, Args: []string{"--resume=abc"}}, "ptyd chooses the session"},
		{CreateConfig{Command: claude, Restore: RestoreAgent, Args: []string{"--continue"}}, "ptyd chooses the session"},
		{CreateConfig{Command: claude, Restore: RestoreAgent, Args: []string{"--settings=x.json"}}, "ptyd chooses the session"},
		{CreateConfig{Command: filepath.Join(bin, "codex"), Restore: RestoreAgent, Args: []string{"--last"}}, "ptyd chooses the session"},
		{CreateConfig{Command: filepath.Join(bin, "codex"), Restore: RestoreAgent, Args: []string{"--config=hooks.Stop=[]"}}, "session hooks"},
		{CreateConfig{Command: "sh", Restore: RestoreAgent}, "no agent adapter"},
		{CreateConfig{Command: "sh", Agent: "claude"}, `only used with "restore": "agent"`},
		{CreateConfig{Command: "sh", Restore: "sometimes"}, "unknown restore policy"},
	} {
		tc.cfg.Name, tc.cfg.Env = "v", env
		if _, err := m.Create(tc.cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v, want error containing %q", tc.cfg.Args, err, tc.want)
		}
	}
	noPersist := NewManager()
	if _, err := noPersist.Create(CreateConfig{Name: "v", Command: "sh", Restore: RestoreRerun}); err == nil || !strings.Contains(err.Error(), "--persist-dir") {
		t.Fatalf("restore without persist dir: %v", err)
	}
}

// The codex trust hash must match the definition codex hashes; this pins
// it to a value computed by codex 0.163.0 for a known hook.
func TestCodexHookHashMatchesCodex(t *testing.T) {
	cmd := "cat >> /tmp/claude-501/ptyd-cx-hook.log; echo >> /tmp/claude-501/ptyd-cx-hook.log"
	// Codex wrote this trusted_hash for the hook above with timeout 600.
	const want = "sha256:b49f96f69db07bf6c72236b66c7f40946246e586f74f80a89598f8492ecd0a62"
	got := codexHookHash("session_start", cmd, 600)
	if got != want {
		t.Fatalf("hash %s, want %s", got, want)
	}
	if !slices.Contains([]string{"claude", "codex"}, detectAgent("/usr/local/bin/claude")) {
		t.Fatal("detect by basename")
	}
}
