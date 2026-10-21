package terminal

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWatchAttention(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "watcher",
		Command: "sh",
		Args:    []string{"-c", "echo BOOT; echo TASK COMPLETE; sleep 0.3"},
		Watches: []WatchSpec{
			{Name: "done", Pattern: "TASK COMPLETE", Once: true},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.Get(info.ID)
		if len(got.Attention) >= 1 {
			ev := got.Attention[0]
			if ev.Watch != "done" {
				t.Fatalf("got watch %q want done", ev.Watch)
			}
			if !strings.Contains(ev.Line.Text, "TASK COMPLETE") {
				t.Fatalf("attention line text = %q", ev.Line.Text)
			}
			// Ack and confirm cleared.
			if err := m.Ack(info.ID, ""); err != nil {
				t.Fatalf("ack: %v", err)
			}
			got2, _ := m.Get(info.ID)
			if len(got2.Attention) != 0 {
				t.Fatalf("expected attention cleared, got %+v", got2.Attention)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	got, _ := m.Get(info.ID)
	t.Fatalf("watch never fired; info=%+v", got)
}

func TestWaitMatch(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "waiter",
		Command: "sh",
		Args:    []string{"-c", "sleep 0.2; echo first; sleep 0.2; echo SUCCESS marker; sleep 0.5"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	line, before, err := m.Wait(ctx, info.ID, "SUCCESS", -1, 1)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(line.Text, "SUCCESS") {
		t.Fatalf("matched line = %q", line.Text)
	}
	if len(before) != 1 || before[0].Text != "first" {
		t.Fatalf("context = %+v, want [first]", before)
	}
}

func TestWaitTimeout(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "timeout",
		Command: "sh",
		Args:    []string{"-c", "sleep 1"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err = m.Wait(ctx, info.ID, "never-going-to-match", -1, 0)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errorsContains(err, "deadline exceeded") {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}

func TestSearchAcrossTerminals(t *testing.T) {
	m := NewManager()
	a, _ := m.Create(CreateConfig{
		Name: "a", Command: "sh", Args: []string{"-c", "echo apple banana"},
		Tags: []string{"group1"},
	})
	b, _ := m.Create(CreateConfig{
		Name: "b", Command: "sh", Args: []string{"-c", "echo banana cherry"},
		Tags: []string{"group2"},
	})
	defer m.Close(a.ID)
	defer m.Close(b.ID)
	time.Sleep(400 * time.Millisecond)

	hits, err := m.Search("banana", 0, 0, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %d (%+v)", len(hits), hits)
	}

	hits, err = m.Search("banana", 0, 0, []string{"group1"})
	if err != nil {
		t.Fatalf("search filtered: %v", err)
	}
	if len(hits) != 1 || hits[0].TerminalID != a.ID {
		t.Fatalf("tag filter wrong: %+v", hits)
	}
}

func TestRestart(t *testing.T) {
	m := NewManager()
	first, err := m.Create(CreateConfig{
		Name:    "restartable",
		Command: "sh",
		Args:    []string{"-c", "echo run1; sleep 0.05"},
		Tags:    []string{"keep"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	second, err := m.Restart(first.ID)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer m.Close(second.ID)
	if second.ID == first.ID {
		t.Fatal("restart should produce a new id")
	}
	if len(second.Tags) != 1 || second.Tags[0] != "keep" {
		t.Fatalf("tags lost on restart: %+v", second.Tags)
	}
	if second.Command != first.Command {
		t.Fatalf("command lost: %q vs %q", second.Command, first.Command)
	}
}

func TestActivityTracking(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "active",
		Command: "sh",
		Args:    []string{"-c", "echo hi; sleep 0.5"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.Get(info.ID)
		if got.LastOutputAt != nil && got.LatestSeq >= 0 {
			if got.IdleSeconds < 0 {
				t.Fatalf("idle seconds negative: %v", got.IdleSeconds)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("never observed last_output_at")
}

func TestInvalidWatchPattern(t *testing.T) {
	m := NewManager()
	_, err := m.Create(CreateConfig{
		Name:    "bad",
		Command: "sh",
		Watches: []WatchSpec{{Name: "broken", Pattern: "[unterminated"}},
	})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestWaitMatchesPending(t *testing.T) {
	// An interactive shell sitting at a prompt never emits a newline; the
	// match must come from the pending buffer.
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "pending",
		Command: "sh",
		Args:    []string{"-c", "printf 'ready> '; sleep 5"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	line, _, err := m.Wait(ctx, info.ID, `ready>\s*$`, -1, 0)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if line.Seq != -1 {
		t.Fatalf("expected pending match (Seq=-1), got Seq=%d text=%q", line.Seq, line.Text)
	}
	if !strings.Contains(line.Text, "ready>") {
		t.Fatalf("text = %q", line.Text)
	}
}

func TestPendingPromptFiresWatch(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "prompt-watcher",
		Command: "sh",
		Args:    []string{"-c", "printf 'continue? '; sleep 5"},
		Watches: []WatchSpec{{Name: "prompt", Pattern: `\?\s*$`, Once: true}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := m.Get(info.ID)
		if len(got.Attention) >= 1 {
			ev := got.Attention[0]
			if ev.Watch != "prompt" {
				t.Fatalf("got watch %q", ev.Watch)
			}
			if !strings.Contains(ev.Line.Text, "continue?") {
				t.Fatalf("text=%q", ev.Line.Text)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("prompt watch never fired against pending buffer")
}

// helper: errors.Is(err, context.DeadlineExceeded) wrapped check
func errorsContains(err error, sub string) bool {
	return err != nil && strings.Contains(err.Error(), sub)
}
