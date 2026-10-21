package terminal

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/2018kguo/ptyd/internal/vt"
)

// Screen persistence. With a persist dir, each terminal's directory holds:
//
//   - screen.ckpt: a checkpoint of the screen, one JSON header line
//     (ckptHeader) followed by the bytes that repaint the screen and its
//     scrollback. Written atomically (temp file, fsync, rename).
//   - tail.<gen>: everything the program printed since checkpoint <gen>
//     was taken, as records of one type byte, a 4-byte big-endian length
//     and a payload: recData (raw output) or recResize (cols, rows). The
//     file is created on the first output after a checkpoint, so an idle
//     terminal holds no file open.
//
// The checkpoint plus the tails with gen >= its tailGen, replayed through
// a fresh emulator, give the screen as the program left it, even after a
// kill -9 of the daemon: tails are appended on every PTY read (not
// fsynced; the page cache survives a process crash). A torn last record
// is ignored, and output cut mid escape sequence is harmless.
//
// Checkpoints bound the tail: one is taken once output has been quiet
// for ckptSettle (at most every ckptMinInterval), whenever the tail
// passes maxTailBytes, on resize-free shutdown and when the program
// exits. Writes per terminal are therefore bounded by the output itself
// plus one checkpoint per maxTailBytes of output or per ckptMinInterval.
const (
	ckptFile        = "screen.ckpt"
	tailPrefix      = "tail."
	recData         = 'd'
	recResize       = 'r'
	maxTailBytes    = 4 << 20
	ckptSettle      = time.Second
	ckptMinInterval = 15 * time.Second
	ckptVersion     = 1
)

// diskStats counts what persistence writes (reported by /metrics).
var diskStats struct {
	bytes       atomic.Int64 // tails, checkpoints and metadata
	fsyncs      atomic.Int64
	checkpoints atomic.Int64
}

type ckptHeader struct {
	Version int       `json:"version"`
	Cols    int       `json:"cols"`
	Rows    int       `json:"rows"`
	TailGen uint64    `json:"tail_gen"`
	At      time.Time `json:"at"`
}

// persister is a terminal's screen persistence state. Fields marked
// scrMu are guarded by the terminal's scrMu (they change in step with
// the screen); the rest by the terminal's ckptMu.
type persister struct {
	dir string

	gen       uint64   // scrMu: tail generation being written
	tail      *os.File // scrMu: open tail file, nil until output arrives
	tailBytes int64    // scrMu
	stopped   bool     // scrMu: no more writes (shutdown, deleted)
	broken    bool     // scrMu: tail grew past twice the cap; stop appending

	timerMu  sync.Mutex
	timer    *time.Timer // armed while output is pending a checkpoint
	lastCkpt time.Time

	queued atomic.Bool // a tail-full checkpoint is on its way
}

// appendLocked records output (or a resize) in the tail. Caller holds
// scrMu. It reports whether a checkpoint is due because the tail is full.
func (p *persister) appendLocked(typ byte, data []byte) bool {
	if p == nil || p.stopped || p.broken {
		return false
	}
	if p.tail == nil {
		f, err := os.OpenFile(filepath.Join(p.dir, tailPrefix+strconv.FormatUint(p.gen, 10)), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			p.broken = true
			fmt.Fprintf(os.Stderr, "[ptyd] open tail in %s: %v\n", p.dir, err)
			return false
		}
		p.tail = f
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(data)))
	// One write per record keeps a record whole in the page cache.
	rec := make([]byte, 0, len(hdr)+len(data))
	rec = append(append(rec, hdr[:]...), data...)
	n, err := p.tail.Write(rec)
	p.tailBytes += int64(n)
	diskStats.bytes.Add(int64(n))
	if err != nil || p.tailBytes > 2*maxTailBytes {
		p.broken = true
	}
	return p.tailBytes > maxTailBytes
}

// noteOutput arms the settle timer after output (no-op if armed), or
// starts a checkpoint right away when the tail is full.
func (t *terminal) noteOutput(tailFull bool) {
	p := t.persist
	if tailFull && p.queued.CompareAndSwap(false, true) {
		go func() {
			defer p.queued.Store(false)
			t.checkpoint()
		}()
	}
	p.timerMu.Lock()
	defer p.timerMu.Unlock()
	if p.timer == nil {
		p.timer = time.AfterFunc(ckptSettle, t.settleFired)
	}
}

// settleFired takes a checkpoint once output has been quiet for
// ckptSettle and the last one is ckptMinInterval old; otherwise it re-arms
// for when that will be true. Once it checkpoints, no timer remains.
func (t *terminal) settleFired() {
	p := t.persist
	t.mu.Lock()
	quietFor := time.Since(t.lastOutputAt)
	t.mu.Unlock()
	p.timerMu.Lock()
	wait := max(ckptSettle-quietFor, ckptMinInterval-time.Since(p.lastCkpt))
	if wait > 0 {
		p.timer = time.AfterFunc(wait, t.settleFired)
		p.timerMu.Unlock()
		return
	}
	p.timer = nil
	p.timerMu.Unlock()
	t.checkpoint()
}

func (p *persister) stopTimer() {
	p.timerMu.Lock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.timerMu.Unlock()
}

// checkpoint writes screen.ckpt and drops the tails it covers. It is
// serialized per terminal by ckptMu.
func (t *terminal) checkpoint() {
	p := t.persist
	if p == nil {
		return
	}
	t.ckptMu.Lock()
	defer t.ckptMu.Unlock()

	t.scrMu.Lock()
	if p.stopped {
		t.scrMu.Unlock()
		return
	}
	cols, rows := t.screen.Size()
	body := t.screen.Repaint(true)
	// Start a new tail generation; output from here on belongs to it.
	old := p.tail
	p.gen++
	p.tail, p.tailBytes, p.broken = nil, 0, false
	gen := p.gen
	t.scrMu.Unlock()
	if old != nil {
		_ = old.Close()
	}

	if err := writeCheckpoint(p.dir, cols, rows, gen, body); err != nil {
		fmt.Fprintf(os.Stderr, "[ptyd] checkpoint %s: %v\n", p.dir, err)
		return
	}
	diskStats.checkpoints.Add(1)
	p.timerMu.Lock()
	p.lastCkpt = time.Now()
	p.timerMu.Unlock()
	removeTailsBefore(p.dir, gen)
}

// writeCheckpoint writes screen.ckpt: a screen of the given size whose
// repaint is body, followed by tails from generation gen on.
func writeCheckpoint(dir string, cols, rows int, gen uint64, body []byte) error {
	hdr, _ := json.Marshal(ckptHeader{Version: ckptVersion, Cols: cols, Rows: rows, TailGen: gen, At: time.Now()})
	return writeFileAtomic(filepath.Join(dir, ckptFile), append(append(hdr, '\n'), body...))
}

// finalCheckpoint checkpoints and stops persisting, so output printed
// while the program is being terminated does not reach the saved screen.
func (t *terminal) finalCheckpoint() {
	p := t.persist
	if p == nil {
		return
	}
	p.stopTimer()
	t.checkpoint()
	t.stopPersisting()
}

// stopPersisting closes the tail and ignores further output.
func (t *terminal) stopPersisting() {
	p := t.persist
	if p == nil {
		return
	}
	p.stopTimer()
	t.ckptMu.Lock()
	t.scrMu.Lock()
	p.stopped = true
	if p.tail != nil {
		_ = p.tail.Close()
		p.tail = nil
	}
	t.scrMu.Unlock()
	t.ckptMu.Unlock()
}

func tailGens(dir string) []uint64 {
	entries, _ := os.ReadDir(dir)
	var gens []uint64
	for _, e := range entries {
		if s, ok := strings.CutPrefix(e.Name(), tailPrefix); ok {
			if g, err := strconv.ParseUint(s, 10, 64); err == nil {
				gens = append(gens, g)
			}
		}
	}
	slices.Sort(gens)
	return gens
}

func removeTailsBefore(dir string, gen uint64) {
	for _, g := range tailGens(dir) {
		if g < gen {
			_ = os.Remove(filepath.Join(dir, tailPrefix+strconv.FormatUint(g, 10)))
		}
	}
}

// loadScreen rebuilds a terminal's screen from its checkpoint and tails.
// cols and rows are used when there is no checkpoint. It also returns the
// next tail generation to write, so a restored terminal continues the
// sequence.
func loadScreen(dir string, cols, rows int) (*vt.Terminal, uint64, error) {
	var hdr ckptHeader
	var body []byte
	raw, err := os.ReadFile(filepath.Join(dir, ckptFile))
	switch {
	case err == nil:
		line, rest, ok := bytes.Cut(raw, []byte("\n"))
		if !ok || json.Unmarshal(line, &hdr) != nil || hdr.Version != ckptVersion {
			return nil, 0, fmt.Errorf("corrupt checkpoint in %s", dir)
		}
		body = rest
		cols, rows = hdr.Cols, hdr.Rows
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, 0, err
	}
	scr := vt.New(max(cols, 1), max(rows, 1), vt.Options{})
	scr.Write(body)
	next := hdr.TailGen
	for _, g := range tailGens(dir) {
		if g < hdr.TailGen {
			continue
		}
		replayTail(scr, filepath.Join(dir, tailPrefix+strconv.FormatUint(g, 10)))
		next = g + 1
	}
	return scr, next, nil
}

// replayTail feeds a tail file's records to scr, stopping at a torn
// record.
func replayTail(scr *vt.Terminal, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReader(f)
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > 2*maxTailBytes {
			return
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return
		}
		switch hdr[0] {
		case recData:
			scr.Write(data)
		case recResize:
			if n == 4 {
				scr.Resize(int(binary.BigEndian.Uint16(data)), int(binary.BigEndian.Uint16(data[2:])))
			}
		default:
			return
		}
	}
}

// writeFileAtomic writes data to path via a synced owner-only temp file
// and a rename, so a crash leaves the old file or the new one.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		diskStats.bytes.Add(int64(len(data)))
		diskStats.fsyncs.Add(1)
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Make the rename itself durable.
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		diskStats.fsyncs.Add(1)
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
