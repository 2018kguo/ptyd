package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// A client that stops reading never holds up the PTY reader: its queue is
// bounded, and once it overflows the client gets a fresh repaint.
func TestSlowAttacherGetsRepaint(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{Name: "flood", Command: "sh", Cols: 40, Rows: 5,
		Args: []string{"-c", "read x; i=0; while [ $i -lt 40000 ]; do echo line-$i-padding-padding-padding; i=$((i+1)); done; echo FLOOD-DONE; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	at, err := m.Attach(info.ID, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	defer at.Close()
	ctx := context.Background()
	if first, _ := at.Read(ctx); !bytes.Contains(first, []byte("\x1b[?2026h")) {
		t.Fatalf("first read is not a repaint: %q", first)
	}
	// Start the flood and do not read: the terminal still sees all of it.
	_ = at.Write([]byte("\r"))
	ctxW, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, _, err := m.Wait(ctxW, info.ID, "^FLOOD-DONE$", -1, 0); err != nil {
		t.Fatalf("PTY reader stalled behind a slow client: %v", err)
	}
	at.a.mu.Lock()
	queued, overflow := len(at.a.buf), at.a.overflow
	at.a.mu.Unlock()
	if !overflow || queued != 0 {
		t.Fatalf("queue %d bytes, overflow %v; want an overflowed, empty queue", queued, overflow)
	}
	b, err := at.Read(ctx)
	if err != nil || !bytes.HasPrefix(b, []byte("\x1b[?2026h")) || !bytes.Contains(b, []byte("FLOOD-DONE")) {
		t.Fatalf("after overflow got %q, %v; want a repaint of the current screen", b, err)
	}
	// Then live output resumes.
	_ = at.Write([]byte("after\r"))
	for deadline := time.Now().Add(3 * time.Second); ; {
		b, err := at.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "after") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no live output after the repaint")
		}
	}
	// When the program ends, Read reports EOF.
	_ = m.Signal(info.ID, 9)
	for {
		if _, err := at.Read(ctx); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("end: %v", err)
			}
			break
		}
	}
}

// With several clients attached, the one that types gets its window
// size (tmux's "latest"), and Info counts the clients.
func TestTypingClientGetsItsSize(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{Name: "cat", Command: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	a, err := m.Attach(info.ID, 100, 30, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := m.Attach(info.ID, 60, 20, false)
	if err != nil {
		t.Fatal(err)
	}
	size := func() string {
		scr, _ := m.Screen(info.ID, false)
		return fmt.Sprintf("%dx%d", scr.Cols, scr.Rows)
	}
	if got := size(); got != "60x20" {
		t.Fatalf("after the second attach: %s", got)
	}
	if got, _ := m.Get(info.ID); got.Attached != 2 {
		t.Fatalf("attached %d", got.Attached)
	}
	_ = a.Write([]byte("x"))
	if got := size(); got != "100x30" {
		t.Fatalf("after a typed: %s", got)
	}
	_ = b.Resize(70, 25)
	_ = b.Write([]byte("y"))
	if got := size(); got != "70x25" {
		t.Fatalf("after b resized and typed: %s", got)
	}
	b.Close()
	if got, _ := m.Get(info.ID); got.Attached != 1 {
		t.Fatalf("attached %d after a detach", got.Attached)
	}
}

// Bulk output to a busy client is sent at most once per Coalesce, in one
// piece; a keystroke's echo is not held back, and a quiet client gives
// back a large buffer.
func TestAttachCoalescesBulkOutput(t *testing.T) {
	a := &attacher{wake: make(chan struct{}, 1)}
	at := &Attachment{a: a}
	ctx := context.Background()
	bulk := bytes.Repeat([]byte("x"), 300<<10)

	a.push(bulk)
	start := time.Now()
	if b, _ := at.Read(ctx); len(b) != len(bulk) || time.Since(start) > Coalesce*3/4 {
		t.Fatalf("first output after a quiet spell: %d bytes after %v", len(b), time.Since(start))
	}
	a.push([]byte("e"))
	if b, _ := at.Read(ctx); string(b) != "e" || time.Since(start) > Coalesce*3/4 {
		t.Fatalf("echo held back: %q after %v", b, time.Since(start))
	}
	sent := time.Now()
	a.push(bulk[:2048])
	go func() {
		time.Sleep(Coalesce / 4)
		a.push(bulk[:2048])
	}()
	b, _ := at.Read(ctx)
	if len(b) != 4096 || time.Since(sent) < Coalesce*3/4 {
		t.Fatalf("bulk output: %d bytes after %v, want 4096 after about %v", len(b), time.Since(sent), Coalesce)
	}

	// Quiet: after a second the large buffer is released.
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if _, err := at.Read(ctx); err == nil {
		t.Fatal("read without output")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if cap(a.spare) > maxIdleBuffer || cap(at.last) > maxIdleBuffer {
		t.Fatalf("quiet client keeps %d and %d bytes", cap(a.spare), cap(at.last))
	}
}
