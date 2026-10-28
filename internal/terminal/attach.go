package terminal

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// maxAttachBuffer bounds the output queued for one attached client. A
// client that falls further behind is sent a fresh repaint instead, so a
// slow client never holds up the PTY reader or grows memory.
const maxAttachBuffer = 1 << 20

// maxIdleBuffer is the largest queue buffer a quiet client keeps.
const maxIdleBuffer = 256 << 10

// A client being sent a stream of output gets it at most once per
// Coalesce: the reader waits out the rest of that time before the next
// write, so bulk output goes out in fewer, larger writes and wakeups
// (kitty's input_delay, ghostty's read batching, tmux's redraw timer).
// Output after a quiet spell, and less than CoalesceMin of it, such as a
// keystroke's echo, goes out at once.
const (
	Coalesce    = 4 * time.Millisecond
	CoalesceMin = 1 << 10
	// holdMax is the most output held back: a reader holding more
	// sends it at once, so holding never overflows the queue.
	holdMax = maxAttachBuffer / 4
)

// attacher is one attached client's output queue. The PTY reader appends
// to it under the terminal's scrMu (in the same critical section that
// feeds the screen), so a repaint taken under scrMu is exactly in step
// with the queue.
type attacher struct {
	mu       sync.Mutex
	buf      []byte
	spare    []byte // a buffer the reader is done with, reused by push
	overflow bool   // dropped output; the next read is a repaint
	ended    bool   // the terminal's output has ended
	wake     chan struct{}
}

func (a *attacher) push(p []byte) {
	a.mu.Lock()
	n := len(a.buf)
	wake := false
	switch {
	case a.overflow:
	case n+len(p) > maxAttachBuffer:
		a.overflow, a.buf, wake = true, nil, true
	default:
		if a.buf == nil {
			a.buf, a.spare = a.spare[:0], nil
		}
		a.buf = append(a.buf, p...)
		// The reader is waiting for output, or holding it back (see
		// Coalesce) while there is less than holdMax.
		wake = n == 0 || n < holdMax && len(a.buf) >= holdMax
	}
	a.mu.Unlock()
	if wake {
		a.signal()
	}
}

func (a *attacher) end() {
	a.mu.Lock()
	a.ended = true
	a.mu.Unlock()
	a.signal()
}

func (a *attacher) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

// Attachment is a client attached to a terminal: it receives an initial
// repaint, then the raw output, and may send input and resize.
type Attachment struct {
	t        *terminal // nil for a ghost
	a        *attacher
	initial  []byte
	last     []byte    // returned by the previous Read
	sent     time.Time // when the previous Read returned output
	held     bool      // the queue was held back once for Coalesce
	timer    *time.Timer
	readOnly bool
	once     sync.Once

	// The client's window size, set by Attach and Resize. When it types,
	// the terminal takes its size again if another client changed it.
	cols, rows uint16
}

// Attach attaches a client. If cols and rows are set, the terminal is
// resized to them first (the most recent attacher's size wins). The first
// Read returns a repaint of the screen with its scrollback. A ghost can be
// attached read-only: the client gets its saved screen, then EOF.
func (m *Manager) Attach(id ID, cols, rows uint16, readOnly bool) (*Attachment, error) {
	t, err := m.get(id)
	if err != nil {
		if g, ok := m.ghostScreen(id, cols, rows); ok {
			return &Attachment{initial: g, readOnly: true, a: &attacher{ended: true, wake: make(chan struct{}, 1)}}, nil
		}
		return nil, err
	}
	if cols > 0 && rows > 0 && !readOnly && t.running() {
		_ = t.resize(cols, rows)
	}
	a := &attacher{wake: make(chan struct{}, 1)}
	t.scrMu.Lock()
	if t.cfg.source.Provider != "" {
		if t.nativeClosing {
			if readOnly && t.outputEnded {
				// Copy the ended screen without claiming the native frontend or
				// extending its cleanup deadline.
				initial := t.screen.Repaint(true)
				t.scrMu.Unlock()
				return &Attachment{initial: initial, readOnly: true, a: &attacher{ended: true, wake: make(chan struct{}, 1)}}, nil
			}
			t.scrMu.Unlock()
			return nil, fmt.Errorf("native view has closed; refresh discovery and import again")
		}
		if t.nativeDeadline != nil {
			t.nativeDeadline.Stop()
			t.nativeDeadline = nil
		}
	}
	initial := t.screen.Repaint(true)
	if t.outputEnded {
		a.ended = true
	} else {
		if t.attachers == nil {
			t.attachers = map[*attacher]struct{}{}
		}
		t.attachers[a] = struct{}{}
		t.attached.Store(int32(len(t.attachers)))
	}
	t.scrMu.Unlock()
	m.activeSubs.Add(1)
	return &Attachment{t: t, a: a, initial: initial, readOnly: readOnly, cols: cols, rows: rows}, nil
}

// ReadOnly reports whether input and resizes are ignored.
func (at *Attachment) ReadOnly() bool { return at.readOnly }

// Read waits for output and returns it. It returns io.EOF once the
// terminal's output has ended and everything was delivered, and ctx's
// error if ctx ends first. The bytes are valid until the next Read.
func (at *Attachment) Read(ctx context.Context) ([]byte, error) {
	if at.initial != nil {
		b := at.initial
		at.initial = nil
		return b, nil
	}
	a := at.a
	for {
		a.mu.Lock()
		if a.overflow {
			a.mu.Unlock()
			return at.repaint(), nil
		}
		if n := len(a.buf); n > 0 {
			if wait := Coalesce - time.Since(at.sent); wait > 0 && !at.held && n >= CoalesceMin && n < holdMax {
				a.mu.Unlock()
				at.held = true
				if err := at.sleep(ctx, wait); err != nil {
					return nil, err
				}
				continue
			}
			at.held, at.sent = false, time.Now()
			// The caller is done with what the previous Read returned:
			// the queue reuses it, so a busy client swaps two buffers
			// instead of allocating one per read.
			b := a.buf
			a.buf, a.spare = nil, at.last
			at.last = b
			a.mu.Unlock()
			return b, nil
		}
		ended := a.ended
		big := cap(a.spare) > maxIdleBuffer || cap(at.last) > maxIdleBuffer
		a.mu.Unlock()
		if ended {
			return nil, io.EOF
		}
		// A client that stays quiet for a second gives back the large
		// buffers a burst left it.
		var idle <-chan time.Time
		if big {
			idle = at.arm(time.Second)
		}
		select {
		case <-a.wake:
		case <-idle:
			a.mu.Lock()
			if cap(a.spare) > maxIdleBuffer {
				a.spare = nil
			}
			a.mu.Unlock()
			if cap(at.last) > maxIdleBuffer {
				at.last = nil
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// arm starts the attachment's timer for d and returns its channel.
func (at *Attachment) arm(d time.Duration) <-chan time.Time {
	if at.timer == nil {
		at.timer = time.NewTimer(d)
	} else {
		at.timer.Reset(d)
	}
	return at.timer.C
}

func (at *Attachment) sleep(ctx context.Context, d time.Duration) error {
	select { // drop a wakeup for output already queued
	case <-at.a.wake:
	default:
	}
	select {
	case <-at.arm(d):
		return nil
	case <-at.a.wake: // holdMax reached, overflow or end
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// repaint returns the current screen (without scrollback, which the
// client already has) and resets the queue in step with it.
func (at *Attachment) repaint() []byte {
	t := at.t
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	at.a.mu.Lock()
	at.a.buf, at.a.overflow = nil, false
	at.a.mu.Unlock()
	return t.screen.Repaint(false)
}

// Write sends input to the terminal. Like tmux's "latest" window size,
// the client that types gets its size: if another client resized the
// terminal since, it is resized back first.
func (at *Attachment) Write(p []byte) error {
	if at.readOnly || at.t == nil {
		return nil
	}
	if !at.t.running() {
		return ErrNotRunning
	}
	if at.cols > 0 && at.rows > 0 {
		at.t.scrMu.Lock()
		cols, rows := at.t.screen.Size()
		at.t.scrMu.Unlock()
		if cols != int(at.cols) || rows != int(at.rows) {
			_ = at.t.resize(at.cols, at.rows)
		}
	}
	_, err := at.t.ptmx.Write(p)
	return err
}

// Resize resizes the terminal to this client's window.
func (at *Attachment) Resize(cols, rows uint16) error {
	if at.readOnly || at.t == nil || cols == 0 || rows == 0 {
		return nil
	}
	if !at.t.running() {
		return ErrNotRunning
	}
	at.cols, at.rows = cols, rows
	return at.t.resize(cols, rows)
}

// Close detaches the client.
func (at *Attachment) Close() {
	at.once.Do(func() {
		if at.t == nil {
			return
		}
		at.t.scrMu.Lock()
		delete(at.t.attachers, at.a)
		at.t.attached.Store(int32(len(at.t.attachers)))
		closeNative := at.t.cfg.source.Provider != "" && len(at.t.attachers) == 0
		if closeNative {
			at.t.nativeClosing = true
			if at.t.outputEnded && at.t.mgr != nil {
				// EOF reaches the CLI before it reads the process exit status.
				// Keep that result briefly instead of racing its GET with 404.
				if at.t.nativeDeadline != nil {
					at.t.nativeDeadline.Stop()
				}
				at.t.nativeDeadline = time.AfterFunc(time.Minute, func() { _ = at.t.mgr.Close(at.t.info.ID) })
				closeNative = false
			}
		}
		at.t.scrMu.Unlock()
		if at.t.mgr != nil {
			at.t.mgr.activeSubs.Add(-1)
			if closeNative {
				_ = at.t.mgr.Close(at.t.info.ID)
			}
		}
	})
}

// broadcastLocked queues output for every attached client. Caller holds
// t.scrMu.
func (t *terminal) broadcastLocked(p []byte) {
	for a := range t.attachers {
		a.push(p)
	}
}

// endAttachers tells attached clients the output has ended.
func (t *terminal) endAttachers() {
	t.scrMu.Lock()
	t.outputEnded = true
	for a := range t.attachers {
		a.end()
	}
	t.attachers = nil
	t.attached.Store(0)
	t.scrMu.Unlock()
}

// An abandoned import request must not leave a native frontend running forever.
// The deadline ends once an attachment claims the view; last detach closes it.
func (m *Manager) expireNativeView(id ID) {
	t, err := m.get(id)
	if err != nil {
		return
	}
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	if t.cfg.source.Provider == "" || len(t.attachers) > 0 || t.nativeClosing {
		return
	}
	t.nativeDeadline = time.AfterFunc(time.Minute, func() {
		t.scrMu.Lock()
		closeView := len(t.attachers) == 0 && !t.nativeClosing
		if closeView {
			t.nativeClosing = true
		}
		t.scrMu.Unlock()
		if closeView {
			_ = m.Close(id)
		}
	})
}
