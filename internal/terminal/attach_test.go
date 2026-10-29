package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/2018kguo/phosphor"
	"io"
	"os"
	"path/filepath"
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
	at, err := m.Attach(info.ID, AttachOptions{Cols: 0, Rows: 0, ReadOnly: false})
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
	a, err := m.Attach(info.ID, AttachOptions{Cols: 100, Rows: 30, ReadOnly: false})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := m.Attach(info.ID, AttachOptions{Cols: 60, Rows: 20, ReadOnly: false})
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

// Done is evaluated after Read arms its timer. This gives the test a barrier
// before it expires the timer or pushes output, without scheduler deadlines.
type attachmentReadBarrier struct {
	context.Context
	waiting chan struct{}
}

func (c *attachmentReadBarrier) Done() <-chan struct{} {
	select {
	case c.waiting <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

// Bulk output waits for the coalescing timer, echoes skip it, a full queue
// wakes it early, and an idle client releases its large buffer.
func TestAttachCoalescesBulkOutput(t *testing.T) {
	a := &attacher{wake: make(chan struct{}, 1)}
	at := &Attachment{a: a}
	t.Cleanup(func() {
		if at.timer != nil {
			at.timer.Stop()
		}
	})
	bulk := bytes.Repeat([]byte("x"), 300<<10)
	ready, cancelReady := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelReady()
	// Quiet output and echoes must never arm the coalescing timer.
	a.push(bulk[:2048])
	if b, err := at.Read(ready); len(b) != 2048 || err != nil || at.timer != nil {
		t.Fatalf("first output after a quiet spell was held: %d bytes, %v", len(b), err)
	}
	// Keep the coalescing deadline far ahead so scheduling cannot expire it.
	at.sent = time.Now().Add(time.Hour)
	a.push([]byte("e"))
	if b, err := at.Read(ready); string(b) != "e" || err != nil || at.timer != nil {
		t.Fatalf("echo was held: %q, %v", b, err)
	}
	at.sent = time.Now().Add(time.Hour)
	a.push(bulk)
	if b, err := at.Read(ready); len(b) != len(bulk) || err != nil || at.timer != nil {
		t.Fatalf("already full queue was held: %d bytes, %v", len(b), err)
	}
	type readResult struct {
		data []byte
		err  error
	}
	startRead := func() (*attachmentReadBarrier, <-chan readResult, context.CancelFunc) {
		parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		finished := make(chan struct{})
		t.Cleanup(func() { cancel(); <-finished })
		ctx := &attachmentReadBarrier{Context: parent, waiting: make(chan struct{}, 1)}
		done := make(chan readResult, 1)
		go func() {
			defer close(finished)
			b, err := at.Read(ctx)
			done <- readResult{b, err}
		}()
		return ctx, done, cancel
	}
	wait := func(ctx *attachmentReadBarrier, done <-chan readResult) {
		t.Helper()
		select {
		case <-ctx.waiting:
		case result := <-done:
			t.Fatalf("read returned before waiting: %d bytes, %v", len(result.data), result.err)
		case <-ctx.Context.Done():
			t.Fatal("read did not reach its wait")
		}
	}
	at.sent = time.Now().Add(time.Hour)
	a.push(bulk[:2048])
	ctx, done, _ := startRead()
	wait(ctx, done)
	a.push(bulk[:2048])
	at.timer.Reset(0)
	if result := <-done; result.err != nil || len(result.data) != 4096 {
		t.Fatalf("timer did not release coalesced output: %d bytes, %v", len(result.data), result.err)
	}
	// Reaching holdMax must wake the reader without the timer expiring.
	at.sent = time.Now().Add(time.Hour)
	a.push(bulk[:2048])
	ctx, done, _ = startRead()
	wait(ctx, done)
	a.push(bulk)
	if result := <-done; result.err != nil || len(result.data) != 2048+len(bulk) {
		t.Fatalf("full queue did not release held output: %d bytes, %v", len(result.data), result.err)
	}
	if !at.timer.Stop() {
		t.Fatal("full queue waited for its timer")
	}
	// Expire the idle timer, then wait until Read has released the buffer
	// and entered its next wait before canceling it.
	ctx, done, cancel := startRead()
	wait(ctx, done)
	at.timer.Reset(0)
	wait(ctx, done)
	cancel()
	if result := <-done; !errors.Is(result.err, context.Canceled) {
		t.Fatalf("idle read did not wait for cancellation: %v", result.err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if cap(a.spare) > maxIdleBuffer || cap(at.last) > maxIdleBuffer {
		t.Fatalf("quiet client keeps %d and %d bytes", cap(a.spare), cap(at.last))
	}
}

func TestTerminalRepliesBeforeViewAndWithReadOnlyRawObserver(t *testing.T) {
	for _, observed := range []bool{false, true} {
		t.Run(fmt.Sprint(observed), func(t *testing.T) {
			m := NewManager()
			defer m.CloseAll()
			reply := filepath.Join(t.TempDir(), "replies")
			script := `stty -echo; read start; stty raw; printf '\033[H'; i=0; while [ $i -lt 16 ]; do printf '\033[6n'; i=$((i+1)); done; dd bs=1 count=96 of="$1" 2>/dev/null; printf 'REPLIES_DONE\r\n'; exec cat`
			info, err := m.Create(CreateConfig{Name: "query-burst", Command: "sh", Args: []string{"-c", script, "fixture", reply}})
			if err != nil {
				t.Fatal(err)
			}
			if observed {
				passive, err := m.Attach(info.ID, AttachOptions{Cols: 80, Rows: 24, ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer passive.Close()
				view, err := m.Attach(info.ID, AttachOptions{Cols: 80, Rows: 24, Rendered: true})
				if err != nil {
					t.Fatal(err)
				}
				defer view.Close()
			}
			if err := m.Write(info.ID, "\n"); err != nil {
				t.Fatal(err)
			}
			waitScreen(t, m, info.ID, "REPLIES_DONE")
			data, err := os.ReadFile(reply)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != strings.Repeat("\x1b[1;1R", 16) {
				t.Fatalf("lost finite query burst: %q", data)
			}
		})
	}
}
func TestSparseStaticViewKeepsAcceptedSnapshotGeometry(t *testing.T) {
	scr := phosphor.New(65535, 65)
	scr.Write([]byte("SPARSE"))
	at, err := staticAttachment(scr, AttachOptions{Cols: 80, Rows: 24, ReadOnly: true, Rendered: true})
	if err != nil {
		t.Fatal(err)
	}
	defer at.Close()
	data, err := at.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	restored := phosphor.New(1, 1)
	if err := restored.RestoreSnapshot(data, trustedSnapshotLimits()); err != nil {
		t.Fatal(err)
	}
	if w, h := scr.Size(); w != 65535 || h != 65 {
		t.Fatal("viewer changed retained screen")
	}
	if w, h := restored.Size(); w != 80 || h != 24 {
		t.Fatal("static view not fitted")
	}
}

func TestRenderedOverflowRecoversPaletteAndFutureOutput(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()
	script := `read start; printf '\033]4;1;rgb:12/34/56\007\033[31mX\033[0m'; dd if=/dev/zero bs=1024 count=2048 2>/dev/null; printf '\r\nDONE\r\n'; exec cat`
	info, err := m.Create(CreateConfig{Name: "render-overflow", Command: "sh", Args: []string{"-c", script}})
	if err != nil {
		t.Fatal(err)
	}
	at, err := m.Attach(info.ID, AttachOptions{Cols: 80, Rows: 24, Rendered: true})
	if err != nil {
		t.Fatal(err)
	}
	defer at.Close()
	initial, err := at.ReadUpdate(context.Background())
	if err != nil || !initial.Snapshot {
		t.Fatal("missing initial snapshot", err)
	}
	if err := m.Write(info.ID, "\n"); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, m, info.ID, "DONE")
	next, err := at.ReadUpdate(context.Background())
	if err != nil || !next.Snapshot {
		t.Fatal("overflow lost snapshot framing", err)
	}
	view := phosphor.New(1, 1)
	if err := view.RestoreSnapshot(next.Data, trustedSnapshotLimits()); err != nil {
		t.Fatal(err)
	}
	if c := view.Palette().Colors[1]; c != (phosphor.RGB{R: 18, G: 52, B: 86}) {
		t.Fatalf("resync lost palette: %+v", c)
	}
	if err := at.Write([]byte("future\r")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		update, err := at.ReadUpdate(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if update.Snapshot {
			t.Fatal("ordinary output marked snapshot")
		}
		view.Write(update.Data)
		if strings.Contains(strings.Join(view.Lines(), "\n"), "future") {
			break
		}
	}
}
