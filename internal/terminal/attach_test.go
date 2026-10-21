package terminal

import (
	"bytes"
	"context"
	"errors"
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
