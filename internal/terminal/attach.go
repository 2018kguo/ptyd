package terminal

import (
	"context"
	"fmt"
	"github.com/2018kguo/phosphor"
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
	human            bool
	renderedView     bool
	rawResponder     bool // writable raw viewer supplies terminal replies
	mu               sync.Mutex
	buf              []byte
	spare            []byte    // a buffer the reader is done with, reused by push
	overflow         bool      // dropped output; the next read is a repaint
	ended            bool      // the terminal's output has ended
	interactiveUntil time.Time // a short response burst bypasses bulk batching
	wake             chan struct{}
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

func (a *attacher) prioritizeInput(until time.Time) {
	a.mu.Lock()
	a.interactiveUntil = until
	a.mu.Unlock()
	a.signal()
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
	rendered bool
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
type AttachOptions struct {
	Cols, Rows uint16
	ReadOnly   bool
	Rendered   bool
	Human      bool
}

func (m *Manager) Attach(id ID, options AttachOptions) (*Attachment, error) {
	cols, rows, readOnly := options.Cols, options.Rows, options.ReadOnly
	t, err := m.get(id)
	if err != nil {
		if scr, ok := m.loadGhostScreen(id); ok {
			return staticAttachment(scr, options)
		}
		return nil, err
	}
	if !readOnly {
		if options.Human {
			if !t.inputMu.TryLock() {
				return nil, ErrInputBusy
			}
		} else {
			t.inputMu.Lock()
		}
		defer t.inputMu.Unlock()
	}
	a := &attacher{human: options.Human && !readOnly, wake: make(chan struct{}, 1), renderedView: options.Rendered, rawResponder: !options.Rendered && !options.ReadOnly}
	t.scrMu.Lock()
	if !readOnly && t.humans.Load() > 0 {
		t.scrMu.Unlock()
		return nil, ErrHumanControl
	}
	if cols > 0 && rows > 0 && !readOnly && t.running() {
		_ = t.resizeLocked(cols, rows)
	}
	if readOnly && t.outputEnded {
		// A static view neither claims a native frontend nor changes its lifetime.
		at, err := staticAttachment(t.screen, options)
		t.scrMu.Unlock()
		return at, err
	}
	if t.cfg.source.Provider != "" {
		if t.nativeClosing {
			t.scrMu.Unlock()
			return nil, fmt.Errorf("native view has closed; refresh discovery and import again")
		}
		if t.nativeDeadline != nil {
			t.nativeDeadline.Stop()
			t.nativeDeadline = nil
		}
	}

	var initial []byte
	if options.Rendered {
		initial = t.screen.Snapshot()
	} else {
		initial = t.screen.Repaint(true)
	}
	if t.outputEnded {
		a.ended = true
	} else {
		if t.attachers == nil {
			t.attachers = map[*attacher]struct{}{}
		}
		if a.human {
			if t.humans.Load() > 0 {
				t.scrMu.Unlock()
				return nil, ErrHumanControl
			}
			t.humans.Add(1)
		}
		t.attachers[a] = struct{}{}
		t.attached.Store(int32(len(t.attachers)))
	}
	t.scrMu.Unlock()
	m.activeSubs.Add(1)
	return &Attachment{t: t, a: a, initial: initial, rendered: options.Rendered, readOnly: readOnly, cols: cols, rows: rows}, nil
}

// ReadOnly reports whether input and resizes are ignored.
func (at *Attachment) ReadOnly() bool { return at.readOnly }

// Read waits for output and returns it. It returns io.EOF once the
// terminal's output has ended and everything was delivered, and ctx's
// error if ctx ends first. The bytes are valid until the next Read.
type AttachmentUpdate struct {
	Data     []byte
	Snapshot bool
}

func (at *Attachment) Read(ctx context.Context) ([]byte, error) {
	update, err := at.ReadUpdate(ctx)
	return update.Data, err
}

func (at *Attachment) ReadUpdate(ctx context.Context) (AttachmentUpdate, error) {
	if at.initial != nil {
		b := at.initial
		at.initial = nil
		return AttachmentUpdate{Data: b, Snapshot: at.rendered}, nil
	}
	a := at.a
	for {
		a.mu.Lock()
		if a.overflow {
			a.mu.Unlock()
			return AttachmentUpdate{Data: at.repaint(), Snapshot: at.rendered}, nil
		}
		if n := len(a.buf); n > 0 {
			if wait := Coalesce - time.Since(at.sent); wait > 0 && time.Until(a.interactiveUntil) <= 0 && !at.held && n >= CoalesceMin && n < holdMax {
				a.mu.Unlock()
				at.held = true
				if err := at.sleep(ctx, wait); err != nil {
					return AttachmentUpdate{}, err
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
			return AttachmentUpdate{Data: b}, nil
		}
		ended := a.ended
		big := cap(a.spare) > maxIdleBuffer || cap(at.last) > maxIdleBuffer
		a.mu.Unlock()
		if ended {
			return AttachmentUpdate{}, io.EOF
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
			return AttachmentUpdate{}, ctx.Err()
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
	// A wake may have arrived between choosing to coalesce and this drain.
	// Recheck its sticky condition before sleeping; later events keep their wake.
	at.a.mu.Lock()
	ready := time.Until(at.a.interactiveUntil) > 0 || len(at.a.buf) >= holdMax || at.a.overflow || at.a.ended
	at.a.mu.Unlock()
	if ready {
		return nil
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
	if at.rendered {
		return t.screen.Snapshot()
	}
	return t.screen.Repaint(false)
}

// Write sends input to the terminal. Like tmux's "latest" window size,
// the client that types gets its size: if another client resized the
// terminal since, it is resized back first.
func (at *Attachment) Write(p []byte) error {
	if at.readOnly || at.t == nil {
		return nil
	}
	t := at.t
	t.inputMu.Lock()
	defer t.inputMu.Unlock()
	t.scrMu.Lock()
	_, active := t.attachers[at.a]
	if !active {
		t.scrMu.Unlock()
		return ErrNotRunning
	}
	if !at.a.human && t.humans.Load() > 0 {
		t.scrMu.Unlock()
		return ErrHumanControl
	}
	if at.cols > 0 && at.rows > 0 {
		cols, rows := t.screen.Size()
		if cols != int(at.cols) || rows != int(at.rows) {
			_ = t.resizeLocked(at.cols, at.rows)
		}
	}
	t.scrMu.Unlock()
	if !t.running() {
		return ErrNotRunning
	}
	// Large redraws caused by typing or a wheel event are interactive too.
	// Wake an already-held read as well as bypassing the next coalescing wait.
	if len(p) > 0 {
		at.a.prioritizeInput(time.Now().Add(Coalesce))
	}
	_, err := t.ptmx.Write(p)
	return err
}

// Resize resizes the terminal to this client's window.
func (at *Attachment) Resize(cols, rows uint16) error {
	if at.readOnly || at.t == nil || cols == 0 || rows == 0 {
		return nil
	}
	t := at.t
	t.inputMu.Lock()
	defer t.inputMu.Unlock()
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	if _, active := t.attachers[at.a]; !active {
		return ErrNotRunning
	}
	if !at.a.human && t.humans.Load() > 0 {
		return ErrHumanControl
	}
	if !t.running() {
		return ErrNotRunning
	}
	at.cols, at.rows = cols, rows
	return t.resizeLocked(cols, rows)
}

// Close detaches the client.
func (at *Attachment) Close() {
	at.once.Do(func() {
		if at.t == nil {
			return
		}
		if !at.readOnly {
			at.t.inputMu.Lock()
		}
		at.t.scrMu.Lock()
		if _, present := at.t.attachers[at.a]; present && at.a.human {
			at.t.humans.Add(-1)
		}
		delete(at.t.attachers, at.a)
		at.t.attached.Store(int32(len(at.t.attachers)))
		nativeView := at.t.cfg.source.Provider != ""
		at.t.scrMu.Unlock()
		if !at.readOnly {
			at.t.inputMu.Unlock()
		}
		if at.t.mgr != nil {
			at.t.mgr.activeSubs.Add(-1)
			if nativeView {
				at.t.mgr.releaseNativeView(at.t.info.ID)
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
	t.humans.Store(0)
	t.attached.Store(0)
	t.scrMu.Unlock()
}

// Inbox imports are temporary views. Named groups retain their frontends.
// The workspace lock orders expiry with moves so a promoted view cannot be
// removed by a timer or last-detach decision taken in its old group.
func (m *Manager) expireNativeView(id ID) {
	t, err := m.get(id)
	if err != nil {
		return
	}
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	if t.nativeDeadline != nil {
		t.nativeDeadline.Stop()
		t.nativeDeadline = nil
	}
	if t.snapshot().GroupID != DefaultGroup {
		t.nativeClosing = false
		return
	}
	if t.cfg.source.Provider == "" || len(t.attachers) > 0 || t.nativeClosing {
		return
	}
	t.nativeDeadline = time.AfterFunc(time.Minute, func() { m.closeUnusedNativeView(id) })
}
func (m *Manager) releaseNativeView(id ID) {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	t, err := m.get(id)
	if err != nil {
		return
	}
	t.scrMu.Lock()
	closeView := t.cfg.source.Provider != "" && t.snapshot().GroupID == DefaultGroup && len(t.attachers) == 0
	if closeView {
		t.nativeClosing = true
		if t.outputEnded {
			// Let standalone clients retrieve the frontend's exit status.
			if t.nativeDeadline != nil {
				t.nativeDeadline.Stop()
			}
			t.nativeDeadline = time.AfterFunc(time.Minute, func() { m.closeUnusedNativeView(id) })
			closeView = false
		}
	}
	t.scrMu.Unlock()
	if closeView {
		_ = m.closeTerminal(id)
	}
}
func (m *Manager) closeUnusedNativeView(id ID) {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	t, err := m.get(id)
	if err != nil {
		return
	}
	t.scrMu.Lock()
	closeView := t.cfg.source.Provider != "" && t.snapshot().GroupID == DefaultGroup && len(t.attachers) == 0
	if closeView {
		t.nativeClosing = true
	}
	t.scrMu.Unlock()
	if closeView {
		_ = m.closeTerminal(id)
	}
}

func staticAttachment(scr *phosphor.Terminal, options AttachOptions) (*Attachment, error) {
	cols, rows := scr.Size()
	if options.Cols > 0 && options.Rows > 0 && (cols != int(options.Cols) || rows != int(options.Rows)) {
		copy := phosphor.New(1, 1)
		if err := copy.RestoreSnapshot(scr.Snapshot(), trustedSnapshotLimits()); err != nil {
			return nil, err
		}
		copy.Resize(int(options.Cols), int(options.Rows))
		scr = copy
	}
	var initial []byte
	if options.Rendered {
		initial = scr.Snapshot()
	} else {
		initial = scr.Repaint(true)
	}
	return &Attachment{initial: initial, readOnly: true, rendered: options.Rendered, a: &attacher{ended: true, wake: make(chan struct{}, 1)}}, nil
}
