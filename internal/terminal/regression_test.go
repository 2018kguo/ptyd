package terminal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A program that never prints a newline must not grow memory without bound.
func TestRingPendingIsBounded(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	chunk := []byte(strings.Repeat("é", 1000)) // 2 bytes per rune
	for range 1000 {
		r.Write(chunk)
	}
	r.mu.Lock()
	pending := len(r.pending)
	r.mu.Unlock()
	if pending > maxLineBytes {
		t.Fatalf("pending grew to %d bytes (cap %d)", pending, maxLineBytes)
	}
	snap := r.Snapshot(-1, 0)
	if len(snap.Lines) == 0 {
		t.Fatal("over-long line was never committed")
	}
	for _, l := range snap.Lines {
		if !utf8.ValidString(l.Text) {
			t.Fatalf("forced line break split a UTF-8 rune: %q", l.Text[len(l.Text)-4:])
		}
	}
}

// Watches must see every line still in the ring, even when a burst
// overflows the watch subscription's channel.
func TestWatchSeesLinesDroppedFromBurst(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name: "burst", Command: "sh", Args: []string{"-c", "sleep 5"},
		Watches: []WatchSpec{{Name: "needle", Pattern: "^NEEDLE$"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	term, _ := m.get(info.ID)

	var b strings.Builder
	for i := range 4999 {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	b.WriteString("NEEDLE\n")
	// Hold the terminal lock so the watch goroutine cannot drain its
	// channel while the burst is committed.
	term.mu.Lock()
	term.ring.Write([]byte(b.String()))
	term.mu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.Get(info.ID)
		for _, ev := range got.Attention {
			if ev.Watch == "needle" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watch missed a line that is still in the ring")
}

// A chatty terminal with a non-once watch must not accumulate events forever.
func TestAttentionIsBounded(t *testing.T) {
	term := &terminal{}
	term.watches = []*compiledWatch{{spec: WatchSpec{Name: "err"}, re: regexp.MustCompile("err")}}
	for i := range 10 * maxAttention {
		term.processLine(Line{Seq: int64(i), Text: "err"})
	}
	if len(term.attention) > maxAttention {
		t.Fatalf("attention grew to %d (cap %d)", len(term.attention), maxAttention)
	}
	if last := term.attention[len(term.attention)-1].Line.Seq; last != int64(10*maxAttention-1) {
		t.Fatalf("newest event dropped; last seq %d", last)
	}
}

func TestWaitNegativeBeforeContext(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name: "neg", Command: "sh", Args: []string{"-c", "sleep 0.3; echo a; echo NEEDLE; sleep 5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	line, _, err := m.Wait(ctx, info.ID, "NEEDLE", -1, -5)
	if err != nil || line.Text != "NEEDLE" {
		t.Fatalf("wait: line=%+v err=%v", line, err)
	}
}

func TestWaitOnExitedTerminalReportsNotRunning(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{Name: "x", Command: "sh", Args: []string{"-c", "echo bye"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	time.Sleep(300 * time.Millisecond)
	_, _, err = m.Wait(context.Background(), info.ID, "never", -1, 0)
	if err != ErrNotRunning {
		t.Fatalf("got %v, want ErrNotRunning", err)
	}
}

// DELETE means gone, including after a daemon restart. Terminals that were
// still around when the daemon stopped come back as ghosts.
func TestDeleteForgetsButShutdownKeepsGhost(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persist")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	deleted, err := m1.Create(CreateConfig{Name: "deleted", Command: "sh", Args: []string{"-c", "sleep 5"}})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := m1.Create(CreateConfig{Name: "kept", Command: "sh", Args: []string{"-c", "sleep 5"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m1.Close(deleted.ID); err != nil {
		t.Fatal(err)
	}
	m1.CloseAll()

	m2 := NewManagerWithOptions(Options{PersistDir: dir})
	list := m2.List()
	if len(list) != 1 || list[0].ID != kept.ID {
		t.Fatalf("want only ghost %s, got %+v", kept.ID, list)
	}
	// Deleting the ghost removes it for good.
	if err := m2.Close(kept.ID); err != nil {
		t.Fatalf("delete ghost: %v", err)
	}
	if n := len(NewManagerWithOptions(Options{PersistDir: dir}).List()); n != 0 {
		t.Fatalf("deleted ghost came back (%d entries)", n)
	}
}

// meta.json can carry env vars (tokens); the log mirror carries raw output.
func TestPersistedFilesArePrivate(t *testing.T) {
	root := t.TempDir()
	persist, logs := filepath.Join(root, "persist"), filepath.Join(root, "logs")
	m := NewManagerWithOptions(Options{PersistDir: persist, LogDir: logs})
	info, err := m.Create(CreateConfig{Name: "p", Command: "sh", Args: []string{"-c", "sleep 5"}, Env: map[string]string{"SECRET": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	for _, p := range []string{filepath.Join(persist, string(info.ID), "meta.json"), filepath.Join(logs, string(info.ID)+".log")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v, want owner-only", filepath.Base(p), st.Mode().Perm())
		}
	}
}

// Service managers start the daemon without TERM; children still need one.
func TestDefaultTERMWhenUnset(t *testing.T) {
	t.Setenv("TERM", "")
	os.Unsetenv("TERM")
	m := NewManager()
	info, err := m.Create(CreateConfig{Name: "term", Command: "sh", Args: []string{"-c", "sleep 0.2; echo term=$TERM; sleep 0.5"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := m.Wait(ctx, info.ID, "term=xterm-256color", -1, 0); err != nil {
		t.Fatalf("wait: %v", err)
	}
}
