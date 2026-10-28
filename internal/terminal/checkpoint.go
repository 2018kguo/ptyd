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
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/2018kguo/phosphor"
)

// Screen persistence. With a persist dir, each terminal's directory holds:
//
//   - screen.ckpt: a checkpoint of the screen, one JSON header line
//     (ckptHeader) followed by a phosphor snapshot of the exact emulator
//     state. Written atomically (temp file, fsync, rename).
//   - tail.<gen>: everything the program printed since checkpoint <gen>
//     was taken, as records of one type byte, a 4-byte big-endian length
//     and a payload: recData (raw output) or recResize (cols, rows). The
//     file is created on the first output after a checkpoint, so an idle
//     terminal holds no file open.
//
// The checkpoint plus the tails with gen >= its tailGen, replayed through
// a fresh emulator, give the screen as the program left it, even after a
// kill -9 of the daemon: every PTY read is in the tail (written, or on
// macOS stored into a mapping, see mapTails) before the next read, not
// fsynced; the page cache survives a process crash. A torn last record is
// ignored, and output cut mid escape sequence is harmless.
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
	ckptVersion     = 2
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

	gen       uint64    // scrMu: tail generation being written
	tail      *tailFile // scrMu: open tail file, nil until output arrives
	tailBytes int64     // scrMu
	stopped   bool      // scrMu: no more writes (shutdown, deleted)
	broken    bool      // scrMu: tail grew past twice the cap; stop appending

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
		p.tail = &tailFile{path: filepath.Join(p.dir, tailPrefix+strconv.FormatUint(p.gen, 10))}
	}
	if err := p.tail.append(typ, data); err != nil {
		p.broken = true
		fmt.Fprintf(os.Stderr, "[ptyd] tail in %s: %v\n", p.dir, err)
		return false
	}
	n := int64(5 + len(data))
	p.tailBytes += n
	diskStats.bytes.Add(n)
	if p.tailBytes > 2*maxTailBytes {
		p.broken = true
	}
	return p.tailBytes > maxTailBytes
}

// mapTails selects how records reach a tail file. On macOS a write(2) per
// PTY read was the largest single cost of the daemon under output (a
// quarter of its CPU flat out), so there the file is mapped and records
// are stored into it: a store into a MAP_SHARED page is in the kernel's
// page cache as soon as it is made, so it survives a kill -9 exactly as a
// write would. On Linux a write is cheap and the mapping was not faster;
// page faults on the mapping stalled the reader under writeback instead.
var mapTails = runtime.GOOS == "darwin"

// tailWindow is how much of a mapped tail is mapped at a time.
const tailWindow = 256 << 10

// tailFile appends records to one tail file. A mapped file is extended a
// window at a time, so after a crash it ends in zeros, which replay reads
// as the end; close cuts it to its records.
type tailFile struct {
	path string
	f    *os.File
	size int64  // bytes of records
	buf  []byte // write: the record being written
	mem  []byte // mapped: the window, starting at file offset off
	off  int64
}

func (tf *tailFile) append(typ byte, data []byte) error {
	if tf.f == nil {
		flag := os.O_WRONLY | os.O_APPEND
		if mapTails {
			flag = os.O_RDWR
		}
		f, err := os.OpenFile(tf.path, flag|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		tf.f = f
	}
	n := 5 + len(data)
	if !mapTails {
		// One write per record keeps a record whole in the page cache.
		tf.buf = append(append(tf.buf[:0], typ, 0, 0, 0, 0), data...)
		binary.BigEndian.PutUint32(tf.buf[1:], uint32(len(data)))
		_, err := tf.f.Write(tf.buf)
		tf.size += int64(n)
		return err
	}
	if tf.size+int64(n) > tf.off+int64(len(tf.mem)) {
		if err := tf.remap(n); err != nil {
			return err
		}
	}
	if !putRecord(tf.mem[tf.size-tf.off:][:n], typ, data) {
		return errors.New("fault writing the tail (disk full?)")
	}
	tf.size += int64(n)
	return nil
}

// putRecord stores one record, its type byte last: a record a crash cuts
// short still has type 0 and ends the replay. A fault on the mapping (the
// disk is full, so the page cannot be allocated) is reported, not fatal.
func putRecord(rec []byte, typ byte, data []byte) (ok bool) {
	defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
	defer func() { ok = recover() == nil }()
	copy(rec[5:], data)
	binary.BigEndian.PutUint32(rec[1:], uint32(len(data)))
	rec[0] = typ
	return true
}

// remap maps a new window that has room for need more bytes.
func (tf *tailFile) remap(need int) error {
	tf.unmap()
	page := int64(os.Getpagesize())
	off := tf.size &^ (page - 1)
	size := (max(tailWindow, tf.size-off+int64(need)) + page - 1) &^ (page - 1)
	if err := tf.f.Truncate(off + size); err != nil {
		return err
	}
	mem, err := syscall.Mmap(int(tf.f.Fd()), off, int(size), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	tf.mem, tf.off = mem, off
	return nil
}

func (tf *tailFile) unmap() {
	if tf.mem != nil {
		_ = syscall.Munmap(tf.mem)
		tf.mem = nil
	}
}

// close closes the file, cutting a mapped one to its records.
func (tf *tailFile) close() {
	if tf == nil || tf.f == nil {
		return
	}
	if mapTails {
		tf.unmap()
		_ = tf.f.Truncate(tf.size)
	}
	_ = tf.f.Close()
	tf.f = nil
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
	// Check again while holding the screen: output may have resumed since
	// the timer's first check. Compact before checkpoint I/O can block.
	t.scrMu.Lock()
	t.mu.Lock()
	if !p.stopped && time.Since(t.lastOutputAt) >= ckptSettle {
		t.screen.Compact()
		t.ring.Compact()
	}
	t.mu.Unlock()
	t.scrMu.Unlock()
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
	body := t.screen.Snapshot()
	// Start a new tail generation; output from here on belongs to it.
	old := p.tail
	p.gen++
	p.tail, p.tailBytes, p.broken = nil, 0, false
	gen := p.gen
	t.scrMu.Unlock()
	old.close()

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
// snapshot is body, followed by tails from generation gen on.
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
	p.tail.close()
	p.tail = nil
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
func loadScreen(dir string, cols, rows int) (*phosphor.Terminal, uint64, error) {
	var hdr ckptHeader
	var body []byte
	raw, err := os.ReadFile(filepath.Join(dir, ckptFile))
	switch {
	case err == nil:
		line, rest, ok := bytes.Cut(raw, []byte("\n"))
		if !ok || json.Unmarshal(line, &hdr) != nil || (hdr.Version != 1 && hdr.Version != ckptVersion) {
			return nil, 0, fmt.Errorf("corrupt checkpoint in %s", dir)
		}
		// Every released version 1 writer began with synchronized ANSI
		// painting. A damaged version field must not reinterpret binary
		// snapshot bytes as an old terminal stream.
		if hdr.Version == 1 && !bytes.HasPrefix(rest, []byte("\x1b[?2026h")) {
			return nil, 0, fmt.Errorf("checkpoint format differs from its version in %s", dir)
		}
		body = rest
		cols, rows = hdr.Cols, hdr.Rows
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, 0, err
	}
	var scr *phosphor.Terminal
	if hdr.Version == ckptVersion {
		scr = new(phosphor.Terminal)
		// These are owner-only files written by this daemon. Preserve the
		// existing uint16 PTY geometry and storage contract rather than
		// imposing the library's smaller defaults on accepted sessions.
		limits := phosphor.SnapshotLimits{
			MaxBytes: int(^uint(0) >> 1),
			MaxCells: int(min(uint64(^uint(0)>>1), uint64(65535)*65535)),
		}
		if err := scr.RestoreSnapshot(body, limits); err != nil {
			return nil, 0, fmt.Errorf("corrupt checkpoint in %s: %w", dir, err)
		}
		if c, r := scr.Size(); c != cols || r != rows {
			return nil, 0, fmt.Errorf("checkpoint dimensions differ in %s", dir)
		}
	} else {
		// Version 1 was ANSI replay. Keep reading existing checkpoints;
		// every new checkpoint uses the exact snapshot format.
		scr = phosphor.New(max(cols, 1), max(rows, 1))
		scr.Write(body)
	}
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
func replayTail(scr *phosphor.Terminal, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	var hdr [5]byte
	var data []byte // reused: the emulator copies what it keeps
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > 2*maxTailBytes {
			return
		}
		if cap(data) < int(n) {
			data = make([]byte, n)
		}
		data = data[:n]
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
