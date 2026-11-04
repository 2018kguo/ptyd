package terminal

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Prime Agent has no hooks: restore must pick the newest root session in
// the terminal's own session dir, never a child (rlmDepth > 0), and start
// fresh when no transcript exists yet.
func TestPrimeAgentRestoreReadsItsSessionDir(t *testing.T) {
	cwd := t.TempDir()
	state := filepath.Join(t.TempDir(), "agent-session.json")
	cfg := CreateConfig{Command: "prime-agent", CWD: cwd, Args: []string{"--model=gpt-5.4"}}
	a := agents["prime-agent"]

	args, _, ok := agentPlan("prime-agent", a, cfg, state)
	if !ok || slices.Contains(args, "--resume") || !slices.Equal(args, []string{"--model", "gpt-5.4", "--session-dir", primeSessionDir(state)}) {
		t.Fatalf("no transcript should start fresh in the session dir: %v", args)
	}

	dir := primeSessionDir(state)
	_ = os.MkdirAll(dir, 0o700)
	write := func(id string, depth int, age time.Duration) {
		p := filepath.Join(dir, id+".jsonl")
		line := `{"type":"session","version":3,"id":"` + id + `","cwd":"` + cwd + `","rlmDepth":` + string(rune('0'+depth)) + "}\n"
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(-age)
		_ = os.Chtimes(p, mt, mt)
	}
	root := "01a1a478-4423-74c8-bf30-81958407f573"
	write("01a1a478-0000-74c8-bf30-000000000000", 0, time.Hour)
	write(root, 0, time.Minute)
	write("01a1a478-1111-74c8-bf30-111111111111", 1, 0) // newest, but a subagent

	args, _, ok = agentPlan("prime-agent", a, cfg, state)
	if !ok || !slices.Equal(args[len(args)-4:], []string{"--session-dir", dir, "--resume", root}) {
		t.Fatalf("resume args %v", args)
	}
	if err := a.validate([]string{"--resume=x"}); err == nil {
		t.Fatal("--resume must be refused")
	}
}
