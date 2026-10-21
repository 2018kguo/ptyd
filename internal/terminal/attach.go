package terminal

import (
	"context"
	"io"
	"sync"
)

// maxAttachBuffer bounds the output queued for one attached client. A
// client that falls further behind is sent a fresh repaint instead, so a
// slow client never holds up the PTY reader or grows memory.
const maxAttachBuffer = 1 << 20

// maxSpare is the largest queue buffer an idle attacher keeps for reuse.
const maxSpare = 256 << 10

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
	switch {
	case a.overflow:
	case len(a.buf)+len(p) > maxAttachBuffer:
		a.overflow, a.buf = true, nil
	default:
		if a.buf == nil {
			a.buf, a.spare = a.spare[:0], nil
		}
		a.buf = append(a.buf, p...)
	}
	a.mu.Unlock()
	a.signal()
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
	last     []byte // returned by the previous Read
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
		if len(a.buf) > 0 {
			// The caller is done with what the previous Read returned:
			// the queue reuses it, so a client that keeps up swaps two
			// buffers instead of allocating one per read.
			b := a.buf
			a.buf, a.spare = nil, at.last
			at.last = b
			a.mu.Unlock()
			return b, nil
		}
		// Going idle: keep no large buffer for a quiet client.
		if cap(a.spare) > maxSpare {
			a.spare = nil
		}
		if cap(at.last) > maxSpare {
			at.last = nil
		}
		ended := a.ended
		a.mu.Unlock()
		if ended {
			return nil, io.EOF
		}
		select {
		case <-a.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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
		at.t.scrMu.Unlock()
		if at.t.mgr != nil {
			at.t.mgr.activeSubs.Add(-1)
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
