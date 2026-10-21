package terminal

import (
	"strings"
	"testing"
	"time"
)

func TestStripANSI(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m":         "red",        // SGR
		"line\x1b[K":                 "line",       // erase to EOL
		"\x1b[?1lnormal":             "normal",     // private mode reset
		"\x1b[?2004hpaste":           "paste",      // bracketed paste
		"prompt\x1b>":                "prompt",     // ESC > keypad
		"hello\x1b=world":            "helloworld", // ESC = keypad
		"\x1b]0;title\x07rest":       "rest",       // OSC with BEL
		"\x1b]0;title\x1b\\after":    "after",      // OSC with ST
		"\x1b[1;2;3mhi\x1b[0m there": "hi there",   // SGR with params
		"plain text":                 "plain text", // pass-through
		"":                           "",           // empty
	}
	for in, want := range cases {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRingBasic(t *testing.T) {
	r := NewRing(3, DefaultScrollbackBytes)
	r.Write([]byte("a\nb\nc\nd\n"))
	snap := r.Snapshot(-1, 0)
	if len(snap.Lines) != 3 {
		t.Fatalf("want 3 lines, got %d (%+v)", len(snap.Lines), snap.Lines)
	}
	if snap.Lines[0].Text != "b" || snap.Lines[2].Text != "d" {
		t.Fatalf("unexpected lines: %+v", snap.Lines)
	}
	if snap.LatestSeq != 3 {
		t.Fatalf("latest seq = %d, want 3", snap.LatestSeq)
	}
}

func TestRingSince(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	r.Write([]byte("a\nb\nc\n"))
	snap := r.Snapshot(1, 0)
	if len(snap.Lines) != 1 {
		t.Fatalf("want 1 line, got %+v", snap.Lines)
	}
	if snap.Lines[0].Text != "c" || snap.Lines[0].Seq != 2 {
		t.Fatalf("got %+v want {Seq:2, Text:c}", snap.Lines[0])
	}
}

func TestRingTail(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	r.Write([]byte("a\nb\nc\nd\n"))
	snap := r.Snapshot(-1, 2)
	if len(snap.Lines) != 2 || snap.Lines[0].Text != "c" || snap.Lines[1].Text != "d" {
		t.Fatalf("unexpected: %+v", snap.Lines)
	}
}

func TestRingPending(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	r.Write([]byte("hi\n$ "))
	snap := r.Snapshot(-1, 0)
	if snap.Pending != "$ " {
		t.Fatalf("pending = %q want %q", snap.Pending, "$ ")
	}
	if len(snap.Lines) != 1 || snap.Lines[0].Text != "hi" {
		t.Fatalf("want one finalized line 'hi', got %+v", snap.Lines)
	}
}

func TestRingFlushPending(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	r.Write([]byte("$ "))
	r.FlushPending()
	snap := r.Snapshot(-1, 0)
	if len(snap.Lines) != 1 || snap.Lines[0].Text != "$ " {
		t.Fatalf("want 1 line '$ ', got %+v", snap.Lines)
	}
	if snap.Pending != "" {
		t.Fatalf("pending should be empty, got %q", snap.Pending)
	}
}

func TestRingCarriageReturn(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	// Progress-bar-style overwrite: each \r resets, then \n finalizes.
	r.Write([]byte("10%\r20%\r30%\n"))
	snap := r.Snapshot(-1, 0)
	if len(snap.Lines) != 1 || snap.Lines[0].Text != "30%" {
		t.Fatalf("unexpected: %+v", snap.Lines)
	}
}

func TestRingCRLF(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	r.Write([]byte("hello\r\nworld\r\n"))
	snap := r.Snapshot(-1, 0)
	if len(snap.Lines) != 2 || snap.Lines[0].Text != "hello" || snap.Lines[1].Text != "world" {
		t.Fatalf("unexpected: %+v", snap.Lines)
	}
}

func TestRingSubscribe(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	r.Write([]byte("a\n"))
	id, ch, snap := r.Subscribe(8, -1)
	if len(snap.Lines) != 1 {
		t.Fatalf("snap want 1, got %d", len(snap.Lines))
	}
	r.Write([]byte("b\n"))
	select {
	case line := <-ch:
		if line.Text != "b" {
			t.Fatalf("got %q want b", line.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscribed line")
	}
	r.Unsubscribe(id)
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after Unsubscribe")
	}
}

func TestRingCloseSubscribers(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	_, ch, _ := r.Subscribe(8, -1)
	r.CloseSubscribers()
	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after CloseSubscribers")
	}
	// Subsequent subscribe returns an already-closed channel.
	_, ch2, _ := r.Subscribe(8, -1)
	if _, ok := <-ch2; ok {
		t.Fatal("post-close subscribe should return closed channel")
	}
}

func TestManagerEcho(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name:    "test-echo",
		Command: "sh",
		Args:    []string{"-c", "echo hello-ptyd"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer m.Close(info.ID)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := m.Output(info.ID, -1, 0)
		if err != nil {
			t.Fatalf("output: %v", err)
		}
		for _, line := range snap.Lines {
			if strings.Contains(line.Text, "hello-ptyd") {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	snap, _ := m.Output(info.ID, -1, 0)
	t.Fatalf("did not see expected output, got: %+v", snap)
}

func TestManagerCommandNotFound(t *testing.T) {
	m := NewManager()
	_, err := m.Create(CreateConfig{Name: "x", Command: "definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestManagerNameRequired(t *testing.T) {
	m := NewManager()
	if _, err := m.Create(CreateConfig{}); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestMergeEnv(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/root", "WEIRD"}
	out := mergeEnv(parent, map[string]string{"PATH": "/custom", "NEW": "x"})
	has := func(s string) bool {
		for _, v := range out {
			if v == s {
				return true
			}
		}
		return false
	}
	if !has("PATH=/custom") {
		t.Fatal("missing custom PATH")
	}
	if has("PATH=/usr/bin") {
		t.Fatal("parent PATH not removed")
	}
	if !has("HOME=/root") {
		t.Fatal("missing HOME")
	}
	if !has("NEW=x") {
		t.Fatal("missing NEW")
	}
	if !has("WEIRD") {
		t.Fatal("dropped malformed entry")
	}
}
