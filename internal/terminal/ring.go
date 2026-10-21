package terminal

import (
	"sync"
	"time"
	"unicode/utf8"
)

// maxLineBytes caps a single line. Output with no newline (a spinner drawn
// with escape sequences, `yes | tr -d '\n'`) is broken into lines of at
// most this size instead of growing the pending buffer without bound.
const maxLineBytes = 16 << 10

// Ring is a line-aware ring buffer with monotonic sequence numbers and
// fan-out subscribers.
//
// Bytes are written via Write; the ring splits on '\n' and treats a bare
// '\r' (without a following '\n') as "reset the current line", which lets
// progress-bar style output overwrite itself rather than producing thousands
// of intermediate lines.
//
// Lines are stored ANSI-stripped, which is what programmatic readers
// almost always want. Raw bytes are not retained; use the log mirror
// (Options.LogDir) for raw output.
type Ring struct {
	mu      sync.Mutex
	lines   []Line
	cap     int
	head    int // next write position
	full    bool
	nextSeq int64

	pending   []byte // partial line being built (no newline yet)
	pendingCR bool   // last byte was a bare '\r' awaiting disambiguation

	subs    map[int]*subscriber
	nextSub int
	closed  bool
}

type subscriber struct {
	ch chan Line
}

// NewRing returns a ring that retains the last `capacity` finalized lines.
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{
		lines: make([]Line, capacity),
		cap:   capacity,
		subs:  map[int]*subscriber{},
	}
}

// Write appends a chunk of bytes, finalizing a line on '\n' and treating a
// bare '\r' followed by a non-newline byte as "reset the current line"
// (progress-bar overwrite). CRLF is collapsed to a single line break.
// Safe for concurrent use.
func (r *Ring) Write(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, b := range chunk {
		if r.pendingCR {
			r.pendingCR = false
			if b == '\n' {
				// CRLF: finalize without the CR.
				r.commitLocked(string(r.pending))
				r.pending = r.pending[:0]
				continue
			}
			// Bare CR followed by something else: overwrite from
			// the start of the line, then process this byte.
			r.pending = r.pending[:0]
		}
		switch b {
		case '\n':
			r.commitLocked(string(r.pending))
			r.pending = r.pending[:0]
		case '\r':
			r.pendingCR = true
		default:
			r.pending = append(r.pending, b)
			if len(r.pending) >= maxLineBytes {
				r.breakLongLineLocked()
			}
		}
	}
}

// breakLongLineLocked commits the pending bytes as a line, holding back an
// incomplete trailing UTF-8 sequence so a rune is never split.
func (r *Ring) breakLongLineLocked() {
	p := r.pending
	cut := len(p)
	for i := len(p) - 1; i >= 0 && i >= len(p)-utf8.UTFMax; i-- {
		if utf8.RuneStart(p[i]) {
			if !utf8.FullRune(p[i:]) && i > 0 {
				cut = i
			}
			break
		}
	}
	r.commitLocked(string(p[:cut]))
	r.pending = r.pending[:copy(r.pending, p[cut:])]
}

// FlushPending finalizes any partial line currently in the pending buffer.
// Called when the underlying PTY closes so the final prompt is captured.
func (r *Ring) FlushPending() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingCR = false
	if len(r.pending) > 0 {
		r.commitLocked(string(r.pending))
		r.pending = r.pending[:0]
	}
}

// Snapshot returns committed lines whose Seq > sinceSeq. If tail > 0, only
// the trailing `tail` such lines are returned. The returned snapshot also
// includes the current pending bytes.
func (r *Ring) Snapshot(sinceSeq int64, tail int) OutputSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotLocked(sinceSeq, tail)
}

// LatestSeq returns the seq of the last committed line, or -1 if none.
func (r *Ring) LatestSeq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nextSeq - 1
}

// Pending returns the current partial line (no newline yet), with any
// completed ANSI escape sequences stripped. Partial sequences (e.g. an
// incomplete CSI whose final byte hasn't arrived) are left in place; they
// will be stripped once the sequence completes.
func (r *Ring) Pending() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return stripANSI(string(r.pending))
}

// Subscribe returns a buffered channel that receives newly committed lines,
// a unique subscription id, and a snapshot of the current ring contents at
// subscribe time. Slow subscribers will drop lines; consumers should detect
// gaps via Line.Seq and refetch via Snapshot if needed.
func (r *Ring) Subscribe(buf int) (int, <-chan Line, OutputSnapshot) {
	if buf < 1 {
		buf = 256
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextSub
	r.nextSub++
	sub := &subscriber{ch: make(chan Line, buf)}
	if r.closed {
		// Already closed: hand back a closed channel so the caller's
		// receive loop terminates immediately.
		close(sub.ch)
	} else {
		r.subs[id] = sub
	}
	return id, sub.ch, r.snapshotLocked(-1, 0)
}

// Unsubscribe removes a subscriber. Idempotent.
func (r *Ring) Unsubscribe(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sub, ok := r.subs[id]; ok {
		close(sub.ch)
		delete(r.subs, id)
	}
}

// CloseSubscribers closes all current subscribers and rejects new ones.
// Called when the underlying terminal exits or is killed.
func (r *Ring) CloseSubscribers() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for id, sub := range r.subs {
		close(sub.ch)
		delete(r.subs, id)
	}
}

func (r *Ring) commitLocked(text string) {
	line := Line{Seq: r.nextSeq, Text: stripANSI(text), At: time.Now()}
	r.nextSeq++

	r.lines[r.head] = line
	r.head++
	if r.head >= r.cap {
		r.head = 0
		r.full = true
	}

	// Fan out, dropping if buffers are full.
	for _, sub := range r.subs {
		select {
		case sub.ch <- line:
		default:
		}
	}
}

func (r *Ring) sizeLocked() int {
	if r.full {
		return r.cap
	}
	return r.head
}

func (r *Ring) snapshotLocked(sinceSeq int64, tail int) OutputSnapshot {
	n := r.sizeLocked()
	out := make([]Line, 0, n)
	for i := 0; i < n; i++ {
		idx := i
		if r.full {
			idx = (r.head + i) % r.cap
		}
		ln := r.lines[idx]
		if ln.Seq > sinceSeq {
			out = append(out, ln)
		}
	}
	if tail > 0 && len(out) > tail {
		out = out[len(out)-tail:]
	}
	return OutputSnapshot{
		Lines:     out,
		LatestSeq: r.nextSeq - 1,
		Pending:   stripANSI(string(r.pending)),
	}
}
