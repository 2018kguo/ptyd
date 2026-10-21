package terminal

import (
	"bytes"
	"sync"
	"time"
	"unicode/utf8"
)

// maxLineBytes caps a single line. Output with no newline (a spinner drawn
// with escape sequences, `yes | tr -d '\n'`) is broken into lines of at
// most this size instead of growing the pending buffer without bound.
const maxLineBytes = 16 << 10

// lineOverhead is what a retained line costs beyond its text: the Line
// struct itself (seq, string header, timestamp). Counting it keeps a flood
// of empty lines inside the byte budget too.
const lineOverhead = 48

// DefaultScrollbackBytes is the default per-terminal byte budget for
// retained lines (text plus lineOverhead per line).
const DefaultScrollbackBytes = 1 << 20

// Ring is a line-aware ring buffer with monotonic sequence numbers and
// fan-out subscribers. It retains the newest lines that fit in both a line
// cap and a byte budget, and its memory grows with what it actually holds.
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
	mu sync.Mutex
	// lines is a circular buffer holding n lines starting at head (the
	// oldest). It grows on demand up to maxLines slots.
	lines    []Line
	head, n  int
	bytes    int // retained cost: text bytes + lineOverhead per line
	maxLines int
	maxBytes int
	nextSeq  int64 // seq of the next line; retained seqs are nextSeq-n .. nextSeq-1

	// now is the commit time of the lines of the current Write: read
	// once, at its first line (zero outside Write).
	now time.Time

	pending   []byte // partial line being built (no newline yet)
	pendingCR bool   // last byte was a bare '\r' awaiting disambiguation

	subs    map[int]*subscriber
	nextSub int
	closed  bool
	changed chan struct{} // closed on the next change; nil until someone asks
}

type subscriber struct {
	ch chan Line
}

// closedChan is returned by Changed once the ring is closed.
var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// NewRing returns a ring that retains the newest finalized lines, at most
// maxLines of them and at most maxBytes in total (see lineOverhead). The
// byte budget always admits at least one maximal line.
func NewRing(maxLines, maxBytes int) *Ring {
	return &Ring{
		maxLines: max(maxLines, 1),
		maxBytes: max(maxBytes, maxLineBytes+lineOverhead),
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
	defer func() { r.now = time.Time{} }()

	nl := -1 // index of the next '\n' in chunk, once searched for
	for len(chunk) > 0 {
		if r.pendingCR {
			r.pendingCR = false
			if chunk[0] == '\n' {
				// CRLF: finalize without the CR.
				r.commitPendingLocked()
				chunk, nl = chunk[1:], -1
				continue
			}
			// Bare CR followed by something else: overwrite from
			// the start of the line.
			r.pending = r.pending[:0]
		}
		if nl < 0 {
			if nl = bytes.IndexByte(chunk, '\n'); nl < 0 {
				nl = len(chunk)
			}
		}
		end := nl
		if cr := bytes.IndexByte(chunk[:nl], '\r'); cr >= 0 {
			end = cr
		}
		r.appendPendingLocked(chunk[:end])
		if end == len(chunk) {
			break
		}
		if chunk[end] == '\n' {
			r.commitPendingLocked()
			nl = -1
		} else {
			r.pendingCR = true
			nl -= end + 1
		}
		chunk = chunk[end+1:]
	}
	r.notifyLocked()
}

// appendPendingLocked adds p to the partial line, committing it each time
// it reaches maxLineBytes.
func (r *Ring) appendPendingLocked(p []byte) {
	for len(p) > 0 {
		n := min(len(p), maxLineBytes-len(r.pending))
		r.pending = append(r.pending, p[:n]...)
		p = p[n:]
		if len(r.pending) >= maxLineBytes {
			r.breakLongLineLocked()
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
	r.commitLocked(p[:cut])
	r.pending = r.pending[:copy(r.pending, p[cut:])]
}

func (r *Ring) commitPendingLocked() {
	r.commitLocked(r.pending)
	r.pending = r.pending[:0]
}

// FlushPending finalizes any partial line currently in the pending buffer.
// Called when the underlying PTY closes so the final prompt is captured.
func (r *Ring) FlushPending() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingCR = false
	if len(r.pending) > 0 {
		r.commitPendingLocked()
		r.now = time.Time{}
		r.notifyLocked()
	}
}

// Snapshot returns committed lines whose Seq > sinceSeq. If tail > 0, only
// the trailing `tail` such lines are returned. The returned snapshot also
// includes the current pending bytes. It costs O(lines returned).
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
	return stripANSIBytes(r.pending)
}

// Changed returns a channel that is closed at the next change to the
// committed lines or the pending text, and whether the ring is already
// closed (the channel is then closed too). Take it before reading the
// state it guards so no change is missed.
func (r *Ring) Changed() (<-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return closedChan, true
	}
	if r.changed == nil {
		r.changed = make(chan struct{})
	}
	return r.changed, false
}

func (r *Ring) notifyLocked() {
	if r.changed != nil {
		close(r.changed)
		r.changed = nil
	}
}

// Subscribe returns a buffered channel that receives newly committed lines,
// a unique subscription id, and a snapshot of the retained lines with
// Seq > sinceSeq at subscribe time (so it costs what it returns). Slow
// subscribers will drop lines; consumers should detect gaps via Line.Seq
// and refetch via Snapshot if needed.
func (r *Ring) Subscribe(buf int, sinceSeq int64) (int, <-chan Line, OutputSnapshot) {
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
		if r.subs == nil {
			r.subs = map[int]*subscriber{}
		}
		r.subs[id] = sub
	}
	return id, sub.ch, r.snapshotLocked(sinceSeq, 0)
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
	r.notifyLocked()
}

// commitLocked commits text, which the ring may reuse afterwards, as a
// line.
func (r *Ring) commitLocked(text []byte) {
	at := r.now
	if at.IsZero() {
		at = time.Now()
		r.now = at
	}
	line := Line{Seq: r.nextSeq, Text: stripANSIBytes(text), At: at}
	r.nextSeq++

	cost := len(line.Text) + lineOverhead
	for r.n > 0 && (r.n >= r.maxLines || r.bytes+cost > r.maxBytes) {
		old := &r.lines[r.head]
		r.bytes -= len(old.Text) + lineOverhead
		*old = Line{} // drop the text reference
		r.head = (r.head + 1) % len(r.lines)
		r.n--
	}
	if r.n == len(r.lines) {
		grown := make([]Line, min(max(2*r.n, 16), r.maxLines))
		r.copyLocked(grown, 0)
		r.lines, r.head = grown, 0
	}
	r.lines[(r.head+r.n)%len(r.lines)] = line
	r.n++
	r.bytes += cost

	// Fan out, dropping if buffers are full.
	for _, sub := range r.subs {
		select {
		case sub.ch <- line:
		default:
		}
	}
}

// indexAfterLocked returns the position (0 = oldest retained) of the first
// line with Seq > sinceSeq, or r.n if there is none. Seqs are contiguous,
// so this is arithmetic, not a scan.
func (r *Ring) indexAfterLocked(sinceSeq int64) int {
	oldest := r.nextSeq - int64(r.n)
	if d := sinceSeq + 1 - oldest; d > 0 {
		return int(min(d, int64(r.n)))
	}
	return 0
}

// copyLocked copies retained lines starting at position start into dst and
// returns how many it copied.
func (r *Ring) copyLocked(dst []Line, start int) int {
	k := min(len(dst), r.n-start)
	if k <= 0 {
		return 0
	}
	i := (r.head + start) % len(r.lines)
	c := copy(dst[:k], r.lines[i:])
	copy(dst[c:k], r.lines)
	return k
}

func (r *Ring) linesAfterLocked(sinceSeq int64, tail int) []Line {
	start := r.indexAfterLocked(sinceSeq)
	if tail > 0 && r.n-start > tail {
		start = r.n - tail
	}
	out := make([]Line, r.n-start)
	r.copyLocked(out, start)
	return out
}

func (r *Ring) snapshotLocked(sinceSeq int64, tail int) OutputSnapshot {
	return OutputSnapshot{
		Lines:     r.linesAfterLocked(sinceSeq, tail),
		LatestSeq: r.nextSeq - 1,
		Pending:   stripANSIBytes(r.pending),
	}
}

// linesAfter returns the committed lines with Seq > sinceSeq.
func (r *Ring) linesAfter(sinceSeq int64) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.linesAfterLocked(sinceSeq, 0)
}

// read copies up to len(dst) committed lines with Seq > sinceSeq into dst
// and returns how many it copied, so callers can walk the ring in bounded
// chunks without holding the lock for the whole scan.
func (r *Ring) read(dst []Line, sinceSeq int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.copyLocked(dst, r.indexAfterLocked(sinceSeq))
}

// seqBefore returns the seq just before the first line committed at or
// after t (lines are in time order, so this is a binary search).
func (r *Ring) seqBefore(t time.Time) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	lo, hi := 0, r.n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if r.lines[(r.head+mid)%len(r.lines)].At.Before(t) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return r.nextSeq - int64(r.n) + int64(lo) - 1
}
