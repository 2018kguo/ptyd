package terminal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPersistenceRoundtrip: create a terminal with persist-dir set,
// verify meta.json appears, simulate a daemon restart by spinning up
// a fresh Manager pointed at the same dir, and check the prior
// terminal shows up as a ghost in List() with Status=Exited.
func TestPersistenceRoundtrip(t *testing.T) {
	dir := t.TempDir()
	persistDir := filepath.Join(dir, "persist")

	// First daemon: spawn one terminal.
	m1 := NewManagerWithOptions(Options{PersistDir: persistDir})
	info, err := m1.Create(CreateConfig{
		Name:    "test",
		Command: "sh",
		Args:    []string{"-c", "echo hello"},
		Tags:    []string{"ghost-test"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := info.ID

	// meta.json should exist on disk.
	metaPath := filepath.Join(persistDir, string(id), "meta.json")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("meta.json not written: %v", err)
	}

	// Wait a moment for child to exit + waitLoop to fire writeExit.
	time.Sleep(400 * time.Millisecond)
	exitPath := filepath.Join(persistDir, string(id), "exit.json")
	if _, err := os.Stat(exitPath); err != nil {
		t.Fatalf("exit.json not written after child exit: %v", err)
	}

	// Simulate daemon restart: boot a fresh Manager on the same dir.
	// (The PTY itself is dead — the original m1 still has it but a
	// real daemon-restart would have killed all children.)
	m2 := NewManagerWithOptions(Options{PersistDir: persistDir})

	list := m2.List()
	if len(list) != 1 {
		t.Fatalf("ghost not loaded: got %d entries", len(list))
	}
	g := list[0]
	if g.ID != id {
		t.Fatalf("ghost id mismatch: %v vs %v", g.ID, id)
	}
	if g.Status != StatusExited {
		t.Fatalf("ghost status should be Exited, got %v", g.Status)
	}
	if g.Name != "test" {
		t.Fatalf("ghost name lost: %q", g.Name)
	}
	if len(g.Tags) != 1 || g.Tags[0] != "ghost-test" {
		t.Fatalf("ghost tags lost: %+v", g.Tags)
	}

	// Mutations on a ghost should error.
	if err := m2.Write(id, "foo"); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("ghost write should fail with ghost-error, got %v", err)
	}
}

// TestPruneGhosts: write a stale meta.json with old mtime, verify it
// gets pruned on a fresh Manager boot with a short TTL.
func TestPruneGhosts(t *testing.T) {
	dir := t.TempDir()
	persistDir := filepath.Join(dir, "persist")
	stale := filepath.Join(persistDir, "term-99-old")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stale, "meta.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(stale, "meta.json"), old, old)

	n, err := PruneGhosts(persistDir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dir not removed: %v", err)
	}
}

func TestPruneLogsRemovesLeftoverMirrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "term-1-abcd.log"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	n, err := PruneLogs(dir)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "term-1-abcd.log")); !os.IsNotExist(err) {
		t.Fatalf("log still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("non-log file removed: %v", err)
	}
}

func TestWriteLogCapsAndFinishRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "term-2-ef01.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	term := &terminal{logFile: f, logCap: 64}
	chunk := []byte("0123456789abcdef")
	for range 8 {
		term.writeLog(chunk)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// One write may land after the file has already reached the cap,
	// so the mirror can overshoot by a single chunk and no more.
	if info.Size() > int64(term.logCap)+int64(len(chunk)) {
		t.Fatalf("log grew to %d, cap %d", info.Size(), term.logCap)
	}
	if info.Size() == 0 {
		t.Fatal("log was truncated before any byte was kept")
	}

	term.finishLog()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("finished log still present: %v", err)
	}
	term.finishLog()
	term.writeLog(chunk)
}

// TestMetricsCounters: spawn a terminal, exercise the counters,
// verify Metrics() returns sane values.
func TestMetricsCounters(t *testing.T) {
	m := NewManager()
	pre := m.Metrics()
	if pre.PID == 0 {
		t.Fatal("PID should be set")
	}
	if pre.UptimeSeconds < 0 {
		t.Fatal("UptimeSeconds should be >= 0")
	}

	info, err := m.Create(CreateConfig{
		Name:    "metrics-test",
		Command: "sh",
		Args:    []string{"-c", "echo hi"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	time.Sleep(300 * time.Millisecond)

	post := m.Metrics()
	if post.TotalCreated != pre.TotalCreated+1 {
		t.Fatalf("TotalCreated didn't increment: %d -> %d", pre.TotalCreated, post.TotalCreated)
	}
	if post.BytesEmitted < 3 {
		t.Fatalf("BytesEmitted should be >= 3 after 'echo hi', got %d", post.BytesEmitted)
	}
	if post.LiveTerminals != 1 {
		t.Fatalf("LiveTerminals should be 1, got %d", post.LiveTerminals)
	}
}
