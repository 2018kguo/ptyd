package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/2018kguo/ptyd/internal/vt"
	"github.com/creack/pty"
)

const (
	ringCapacity     = 5000 // last N lines retained per terminal
	maxNameLen       = 64
	readBufSize      = 8192
	closeGrace       = 2 * time.Second
	pendingQuietTime = 250 * time.Millisecond // pending text considered "settled" after this long
	maxAttention     = 100                    // newest watch events kept per terminal
	// A live PTY mirror is a debugging aid for the open terminal, not an
	// archive. One unbounded file per spawn filled a 225GB disk.
	defaultMaxLogFileBytes int64 = 32 << 20
)

// Options configure a Manager.
type Options struct {
	// LogDir, if non-empty, is a directory where each terminal's raw PTY
	// output is mirrored to <LogDir>/<id>.log while that terminal is
	// alive. The file is capped and removed when the PTY closes. The ring
	// buffer still holds the recent lines the API serves.
	LogDir string
	// PersistDir, if non-empty, is a directory where per-terminal
	// metadata sidecar files are written so the daemon can recover
	// what terminals existed across restarts. PTYs themselves can't
	// survive (children of the daemon get killed) — but List() will
	// return ghost entries with Status=Exited so clients see a coherent
	// view across daemon restarts instead of bare 404s.
	PersistDir string
	// PersistGhostTTL is how long an exited/closed terminal's metadata
	// is retained before being pruned. Default 7 days.
	PersistGhostTTL time.Duration
	// ScrollbackBytes is the per-terminal budget for retained output
	// lines (see Ring). Default DefaultScrollbackBytes.
	ScrollbackBytes int
}

// Manager owns a set of PTY-backed terminal sessions.
type Manager struct {
	opts   Options
	mu     sync.RWMutex
	terms  map[ID]*terminal
	ghosts map[ID]Info // metadata-only entries from prior daemon runs
	next   atomic.Int64
	// startedAt is when this Manager was created; surfaced by /metrics.
	startedAt time.Time
	// counters surfaced by /metrics. Atomic so they can be read without
	// taking the manager mutex.
	totalCreated atomic.Int64
	totalClosed  atomic.Int64
	totalExited  atomic.Int64
	watchFires   atomic.Int64
	bytesEmitted atomic.Int64
	activeSubs   atomic.Int64
}

type terminal struct {
	mu   sync.Mutex // protects info, attention, lastOutputAt, pendingFired
	info Info

	// mgr backref so waitLoop can update counters + persist exit info
	// when the child process exits without an API close.
	mgr *Manager

	// cfg is retained so Restart can re-spawn with the same shape.
	cfg CreateConfig

	ptmx      *os.File
	cmd       *exec.Cmd
	closeOnce sync.Once

	ring *Ring
	done chan struct{} // closed when waitLoop returns

	// scrMu guards screen, the emulator fed with every byte of output, so
	// the daemon knows what the window looks like.
	scrMu  sync.Mutex
	screen *vt.Terminal

	logFile *os.File // optional, for raw output mirror
	logSize int64    // bytes written to logFile since it was last emptied
	// logCap overrides defaultMaxLogFileBytes in tests. 0 uses the default.
	logCap int64

	watches      []*compiledWatch
	attention    []AttentionEvent
	lastOutputAt time.Time

	// pendingFired records the pending-text value last evaluated against
	// watches so we don't re-fire the same attention event for an idle
	// prompt that hasn't changed.
	pendingFired string

	// passes counts watch and wait loop passes, so tests can check that
	// an idle terminal does not wake up.
	passes atomic.Int64
}

type compiledWatch struct {
	spec  WatchSpec
	re    *regexp.Regexp
	fired bool
}

var (
	// ErrNotFound is returned for ids that are neither live nor ghosts.
	ErrNotFound = errors.New("terminal not found")
	// ErrNotRunning is returned when an operation needs a live process.
	ErrNotRunning = errors.New("terminal is not running")
)

// NewManager returns an empty manager with default options.
func NewManager() *Manager {
	return NewManagerWithOptions(Options{})
}

// NewManagerWithOptions returns a manager configured with opts. If
// PersistDir is set, ghost terminals (metadata-only entries from prior
// runs) are loaded into the manager so the API can report a coherent
// view across daemon restarts.
func NewManagerWithOptions(opts Options) *Manager {
	if opts.LogDir != "" {
		_ = os.MkdirAll(opts.LogDir, 0o700)
		// Previous process is gone. Its mirrors are not readable through
		// the API (that serves the ring), and leaving them is how the
		// log dir grew without bound across a long uptime.
		if n, err := PruneLogs(opts.LogDir); err != nil {
			fmt.Fprintf(os.Stderr, "[ptyd] log prune failed: %v\n", err)
		} else if n > 0 {
			fmt.Fprintf(os.Stderr, "[ptyd] pruned %d leftover terminal log(s)\n", n)
		}
	}
	if opts.PersistDir != "" {
		_ = os.MkdirAll(opts.PersistDir, 0o700)
	}
	if opts.PersistGhostTTL == 0 {
		opts.PersistGhostTTL = 7 * 24 * time.Hour
	}
	if opts.ScrollbackBytes <= 0 {
		opts.ScrollbackBytes = DefaultScrollbackBytes
	}
	m := &Manager{
		opts:      opts,
		terms:     map[ID]*terminal{},
		ghosts:    map[ID]Info{},
		startedAt: time.Now(),
	}
	if opts.PersistDir != "" {
		// Best-effort prune of stale ghost dirs so the persist dir
		// doesn't grow indefinitely.
		if n, _ := PruneGhosts(opts.PersistDir, opts.PersistGhostTTL); n > 0 {
			fmt.Fprintf(os.Stderr, "[ptyd] pruned %d ghost terminal(s) older than %s\n", n, opts.PersistGhostTTL)
		}
		if loaded, err := loadGhosts(opts.PersistDir); err == nil {
			for _, g := range loaded {
				m.ghosts[g.ID] = g
				// Keep the next id counter ahead of any seen so we
				// don't collide on a fresh Create after restart.
				var n int64
				if scanned, _ := fmt.Sscanf(string(g.ID), "term-%d-", &n); scanned == 1 && n > m.next.Load() {
					m.next.Store(n)
				}
			}
			if len(loaded) > 0 {
				fmt.Fprintf(os.Stderr, "[ptyd] loaded %d ghost terminal(s) from %s\n", len(loaded), opts.PersistDir)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[ptyd] ghost load failed: %v\n", err)
		}
	}
	return m
}

// Create spawns a new terminal.
func (m *Manager) Create(cfg CreateConfig) (Info, error) {
	if cfg.Name == "" {
		return Info{}, fmt.Errorf("terminal name is required")
	}
	if len(cfg.Name) > maxNameLen {
		return Info{}, fmt.Errorf("terminal name too long (max %d)", maxNameLen)
	}
	if cfg.Command == "" {
		cfg.Command = os.Getenv("SHELL")
		if cfg.Command == "" {
			cfg.Command = "/bin/sh"
		}
	}
	if cfg.Cols == 0 {
		cfg.Cols = 80
	}
	if cfg.Rows == 0 {
		cfg.Rows = 24
	}
	if cfg.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "/"
		}
		cfg.CWD = cwd
	}

	// Pre-compile watches before doing anything irreversible.
	compiled := make([]*compiledWatch, 0, len(cfg.Watches))
	for _, w := range cfg.Watches {
		if w.Name == "" {
			return Info{}, fmt.Errorf("watch with empty name")
		}
		re, err := regexp.Compile(w.Pattern)
		if err != nil {
			return Info{}, fmt.Errorf("watch %q: invalid regex %q: %w", w.Name, w.Pattern, err)
		}
		compiled = append(compiled, &compiledWatch{spec: w, re: re})
	}

	bin, err := exec.LookPath(cfg.Command)
	if err != nil {
		return Info{}, fmt.Errorf("command %q not found: %w", cfg.Command, err)
	}

	cmd := exec.Command(bin, cfg.Args...)
	cmd.Dir = cfg.CWD
	cmd.Env = mergeEnv(os.Environ(), cfg.Env)
	// Service managers start us without TERM; programs on a PTY need one.
	if !slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "TERM=") }) {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cfg.Cols, Rows: cfg.Rows})
	if err != nil {
		return Info{}, fmt.Errorf("failed to start pty: %w", err)
	}
	ptmx = pollable(ptmx)

	id := ID(fmt.Sprintf("term-%d-%s", m.next.Add(1), randHex(2)))

	var logFile *os.File
	if m.opts.LogDir != "" {
		path := filepath.Join(m.opts.LogDir, string(id)+".log")
		f, ferr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if ferr == nil {
			logFile = f
		}
	}

	t := &terminal{
		mgr: m,
		info: Info{
			ID:        id,
			Name:      cfg.Name,
			CWD:       cfg.CWD,
			Command:   cfg.Command,
			Args:      cfg.Args,
			Tags:      cfg.Tags,
			PID:       cmd.Process.Pid,
			Status:    StatusRunning,
			CreatedAt: time.Now(),
			Watches:   cfg.Watches,
		},
		cfg:     cfg,
		ptmx:    ptmx,
		cmd:     cmd,
		ring:    NewRing(ringCapacity, m.opts.ScrollbackBytes),
		screen:  vt.New(int(cfg.Cols), int(cfg.Rows), vt.Options{}),
		done:    make(chan struct{}),
		logFile: logFile,
		watches: compiled,
	}

	// Persist and register before the goroutines start: a child that
	// exits immediately must find meta.json in place for its exit.json.
	if err := writeMeta(m.opts.PersistDir, id, cfg, t.info.PID, t.info.CreatedAt); err != nil {
		fmt.Fprintf(os.Stderr, "[ptyd] writeMeta(%s) failed: %v\n", id, err)
	}
	m.mu.Lock()
	m.terms[id] = t
	// Creating an id that previously existed as a ghost (extremely
	// unlikely with the random suffix, but possible) clears the
	// ghost entry so List() doesn't show both.
	delete(m.ghosts, id)
	m.mu.Unlock()
	m.totalCreated.Add(1)

	go t.readLoop()
	go t.waitLoop()
	if len(compiled) > 0 {
		go t.watchLoop()
	}

	return t.snapshot(), nil
}

func (m *Manager) get(id ID) (*terminal, error) {
	m.mu.RLock()
	t, ok := m.terms[id]
	_, isGhost := m.ghosts[id]
	m.mu.RUnlock()
	if !ok {
		if isGhost {
			return nil, ErrGhostTerminal
		}
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return t, nil
}

// GhostMeta returns the saved metadata for an exited/closed terminal
// whose process is gone but whose sidecar is still on disk. Used by
// callers that want to differentiate "never existed" from "existed,
// but the daemon has restarted since".
func (m *Manager) GhostMeta(id ID) (Info, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	g, ok := m.ghosts[id]
	return g, ok
}

// Get returns metadata for a single terminal (no scrollback). If the
// terminal isn't live but a ghost exists for the same id (daemon was
// restarted since spawn), returns the ghost metadata.
func (m *Manager) Get(id ID) (Info, error) {
	t, err := m.get(id)
	if err == nil {
		return t.snapshot(), nil
	}
	if g, ok := m.GhostMeta(id); ok {
		return g, nil
	}
	return Info{}, err
}

// List returns metadata for all terminals (live AND ghost), oldest
// first. Ghosts are returned with Status=Exited.
func (m *Manager) List() []Info {
	m.mu.RLock()
	out := make([]Info, 0, len(m.terms)+len(m.ghosts))
	for _, t := range m.terms {
		out = append(out, t.snapshot())
	}
	for _, g := range m.ghosts {
		out = append(out, g)
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Output returns lines with seq > sinceSeq, optionally limited to the last
// `tail` such lines.
func (m *Manager) Output(id ID, sinceSeq int64, tail int) (OutputSnapshot, error) {
	t, err := m.get(id)
	if err != nil {
		return OutputSnapshot{}, err
	}
	return t.ring.Snapshot(sinceSeq, tail), nil
}

// Subscribe attaches a streaming consumer.
func (m *Manager) Subscribe(id ID, buf int) (int, <-chan Line, OutputSnapshot, error) {
	t, err := m.get(id)
	if err != nil {
		return 0, nil, OutputSnapshot{}, err
	}
	subID, ch, snap := t.ring.Subscribe(buf)
	m.activeSubs.Add(1)
	return subID, ch, snap, nil
}

// Unsubscribe detaches a streaming consumer.
func (m *Manager) Unsubscribe(id ID, subID int) {
	defer m.activeSubs.Add(-1)
	t, err := m.get(id)
	if err != nil {
		return
	}
	t.ring.Unsubscribe(subID)
}

// Wait blocks until a line matching `pattern` is committed (with seq>sinceSeq)
// or until the pending buffer matches and has been quiet for at least
// pendingQuietTime, or until ctx is canceled. Returns the matching line and
// a window of context lines before it. A pending-buffer match is returned
// with Seq=-1 so callers can tell it apart from a committed line. If the
// terminal's output ends without a match, it returns ErrNotRunning.
func (m *Manager) Wait(ctx context.Context, id ID, pattern string, sinceSeq int64, beforeContext int) (Line, []Line, error) {
	t, err := m.get(id)
	if err != nil {
		return Line{}, nil, err
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Line{}, nil, fmt.Errorf("invalid pattern: %w", err)
	}
	beforeContext = max(beforeContext, 0)

	// recent holds the matching candidate plus up to beforeContext lines.
	var recent []Line
	check := func(lines []Line) (Line, []Line, bool) {
		for _, line := range lines {
			recent = append(recent, line)
			if len(recent) > beforeContext+1 {
				recent = recent[len(recent)-(beforeContext+1):]
			}
			if line.Seq > sinceSeq && re.MatchString(line.Text) {
				return line, contextBefore(recent, len(recent)-1, beforeContext), true
			}
		}
		return Line{}, nil, false
	}

	// Lines at or before sinceSeq only matter as context for a match.
	cur := cursor{ring: t.ring, last: -1}
	if sinceSeq >= 0 {
		cur.last = max(sinceSeq-int64(beforeContext), -1)
	}
	var quiet *time.Timer
	for {
		// Retained lines first, then a prompt sitting in pending. Nothing
		// polls: the loop sleeps until the ring changes or, while the
		// pending text is still settling, until it has been quiet long
		// enough to match.
		t.passes.Add(1)
		changed, closed := t.ring.Changed()
		if line, before, ok := check(cur.catchUp()); ok {
			return line, before, nil
		}
		text, settleIn := t.settledPending()
		if text != "" && settleIn == 0 && re.MatchString(text) {
			return Line{Seq: -1, Text: text, At: time.Now()}, contextBefore(recent, len(recent), beforeContext), nil
		}
		if closed {
			return Line{}, nil, ErrNotRunning
		}
		var settled <-chan time.Time
		if settleIn > 0 {
			if quiet == nil {
				quiet = time.NewTimer(settleIn)
				defer quiet.Stop()
			} else {
				quiet.Reset(settleIn)
			}
			settled = quiet.C
		}
		select {
		case <-changed:
		case <-settled:
		case <-ctx.Done():
			return Line{}, nil, ctx.Err()
		}
	}
}

// cursor reads a ring in order, remembering the last line it returned.
type cursor struct {
	ring *Ring
	last int64
}

// catchUp returns any retained lines after the cursor.
func (c *cursor) catchUp() []Line {
	lines := c.ring.linesAfter(c.last)
	if n := len(lines); n > 0 {
		c.last = lines[n-1].Seq
	}
	return lines
}

// settledPending returns the pending text and, if output arrived less than
// pendingQuietTime ago, how long until it counts as settled (0 = settled).
func (t *terminal) settledPending() (string, time.Duration) {
	pending := t.ring.Pending()
	if pending == "" {
		return "", 0
	}
	t.mu.Lock()
	last := t.lastOutputAt
	t.mu.Unlock()
	if !last.IsZero() {
		if wait := pendingQuietTime - time.Since(last); wait > 0 {
			return pending, wait
		}
	}
	return pending, 0
}

func contextBefore(lines []Line, matchIdx, n int) []Line {
	if n <= 0 || matchIdx <= 0 {
		return nil
	}
	start := matchIdx - n
	if start < 0 {
		start = 0
	}
	out := make([]Line, matchIdx-start)
	copy(out, lines[start:matchIdx])
	return out
}

// Ack clears attention events. If watchName is empty, all are cleared.
func (m *Manager) Ack(id ID, watchName string) error {
	t, err := m.get(id)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if watchName == "" {
		t.attention = nil
		// Allow once-watches to fire again.
		for _, w := range t.watches {
			w.fired = false
		}
		return nil
	}
	out := t.attention[:0]
	for _, ev := range t.attention {
		if ev.Watch != watchName {
			out = append(out, ev)
		}
	}
	t.attention = out
	for _, w := range t.watches {
		if w.spec.Name == watchName {
			w.fired = false
		}
	}
	return nil
}

// Write sends bytes to the terminal's stdin. The PTY write is performed
// WITHOUT holding t.mu — a write that blocks (because the consumer's
// stdin buffer is full and the program isn't reading) must not stall
// every other operation on this terminal (snapshot, attention checks,
// etc.), which would cascade into Manager.List() hanging because it
// iterates every terminal's t.mu via snapshot().
//
// PTY file handles are safe for concurrent writes; the kernel
// serializes at the device layer.
func (m *Manager) Write(id ID, data string) error {
	t, err := m.get(id)
	if err != nil {
		return err
	}
	if !t.running() {
		return ErrNotRunning
	}
	_, err = io.WriteString(t.ptmx, data)
	return err
}

func (t *terminal) running() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info.Status == StatusRunning
}

// Resize changes the PTY window size.
func (m *Manager) Resize(id ID, cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return fmt.Errorf("cols and rows must be positive")
	}
	t, err := m.get(id)
	if err != nil {
		return err
	}
	if !t.running() {
		return ErrNotRunning
	}
	return t.resize(cols, rows)
}

func (t *terminal) resize(cols, rows uint16) error {
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	if err := setSize(t.ptmx, cols, rows); err != nil {
		return err
	}
	t.screen.Resize(int(cols), int(rows))
	return nil
}

// Signal delivers a signal to the terminal's process group, falling back to
// the leader if the pgrp send fails. Exited terminals are refused: their
// pid has been reaped and may belong to an unrelated process by now.
func (m *Manager) Signal(id ID, sig syscall.Signal) error {
	t, err := m.get(id)
	if err != nil {
		return err
	}
	if !t.running() {
		return ErrNotRunning
	}
	pid := t.cmd.Process.Pid
	if pgrpErr := syscall.Kill(-pid, sig); pgrpErr == nil {
		return nil
	}
	return t.cmd.Process.Signal(sig)
}

// Close terminates a terminal and forgets it, including its persisted
// metadata, so it does not come back as a ghost after a restart. Closing a
// ghost deletes its metadata.
func (m *Manager) Close(id ID) error {
	m.mu.Lock()
	t, ok := m.terms[id]
	_, ghost := m.ghosts[id]
	delete(m.terms, id)
	delete(m.ghosts, id)
	m.mu.Unlock()
	if !ok && !ghost {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if ok {
		t.terminate()
		m.totalClosed.Add(1)
	}
	if err := removePersisted(m.opts.PersistDir, id); err != nil {
		fmt.Fprintf(os.Stderr, "[ptyd] remove persisted %s: %v\n", id, err)
	}
	return nil
}

// CloseAll terminates every live terminal in parallel (so shutdown takes
// at most one closeGrace, not one per terminal). Their metadata is kept so
// they are listed as ghosts on the next start.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	terms := make([]*terminal, 0, len(m.terms))
	for id, t := range m.terms {
		terms = append(terms, t)
		delete(m.terms, id)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, t := range terms {
		wg.Go(func() {
			if t.terminate() {
				if err := writeExit(m.opts.PersistDir, t.info.ID, t.exitCode(), "shutdown"); err != nil {
					fmt.Fprintf(os.Stderr, "[ptyd] writeExit(%s) failed: %v\n", t.info.ID, err)
				}
			}
		})
	}
	wg.Wait()
}

// terminate stops the process (SIGTERM to its group, then SIGKILL after
// closeGrace), closes the PTY and waits for the child to be reaped. It
// reports whether the process was still running.
func (t *terminal) terminate() bool {
	t.mu.Lock()
	wasRunning := t.info.Status == StatusRunning
	if wasRunning {
		t.info.Status = StatusClosed
	}
	t.mu.Unlock()

	pid := t.cmd.Process.Pid
	if wasRunning {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
	// Closing the PTY hangs up the session; readLoop ends with EIO once
	// the child side is gone. closePtmx is idempotent.
	t.closePtmx()

	select {
	case <-t.done:
	case <-time.After(closeGrace):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-t.done
	}
	t.finishLog()
	return wasRunning
}

func (t *terminal) exitCode() *int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info.ExitCode
}

// Restart closes a terminal and spawns a new one with the same config.
// The new terminal has a different ID.
func (m *Manager) Restart(id ID) (Info, error) {
	t, err := m.get(id)
	if err != nil {
		return Info{}, err
	}
	cfg := t.cfg
	if err := m.Close(id); err != nil {
		return Info{}, err
	}
	return m.Create(cfg)
}

// Metrics is a snapshot of the daemon's operational state, returned by
// GET /metrics. Counters are monotonic across the daemon's uptime.
type Metrics struct {
	StartedAt      time.Time      `json:"started_at"`
	UptimeSeconds  float64        `json:"uptime_seconds"`
	PID            int            `json:"pid"`
	LiveTerminals  int            `json:"live_terminals"`
	GhostTerminals int            `json:"ghost_terminals"`
	PerStatus      map[string]int `json:"per_status"`
	ActiveSubs     int64          `json:"active_subscribers"`
	TotalCreated   int64          `json:"total_created"`
	TotalClosed    int64          `json:"total_closed_via_api"`
	TotalExited    int64          `json:"total_exited_on_own"`
	WatchFires     int64          `json:"watch_fires_total"`
	BytesEmitted   int64          `json:"bytes_emitted_total"`
	PersistDir     string         `json:"persist_dir,omitempty"`
	LogDir         string         `json:"log_dir,omitempty"`
}

// Metrics returns operational stats. Cheap (atomics + a single mutex
// hold for terminal-count). No allocations beyond the returned value.
func (m *Manager) Metrics() Metrics {
	m.mu.RLock()
	live := len(m.terms)
	ghosts := len(m.ghosts)
	perStatus := map[string]int{}
	for _, t := range m.terms {
		t.mu.Lock()
		perStatus[string(t.info.Status)]++
		t.mu.Unlock()
	}
	for _, g := range m.ghosts {
		perStatus[string(g.Status)]++
	}
	m.mu.RUnlock()
	return Metrics{
		StartedAt:      m.startedAt,
		UptimeSeconds:  time.Since(m.startedAt).Seconds(),
		PID:            os.Getpid(),
		LiveTerminals:  live,
		GhostTerminals: ghosts,
		PerStatus:      perStatus,
		ActiveSubs:     m.activeSubs.Load(),
		TotalCreated:   m.totalCreated.Load(),
		TotalClosed:    m.totalClosed.Load(),
		TotalExited:    m.totalExited.Load(),
		WatchFires:     m.watchFires.Load(),
		BytesEmitted:   m.bytesEmitted.Load(),
		PersistDir:     m.opts.PersistDir,
		LogDir:         m.opts.LogDir,
	}
}

// SearchHit is a single match across terminals.
type SearchHit struct {
	TerminalID ID       `json:"terminal_id"`
	Name       string   `json:"name"`
	Tags       []string `json:"tags,omitempty"`
	Line       Line     `json:"line"`
}

// Search runs a regex across the most recent lines of every terminal,
// optionally limited to lines newer than `within` ago and capped at `limit`
// total hits (0 = no cap). If tagFilter is non-empty, only terminals with
// at least one of those tags are searched.
func (m *Manager) Search(pattern string, within time.Duration, limit int, tagFilter []string) ([]SearchHit, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}
	var cutoff time.Time
	if within > 0 {
		cutoff = time.Now().Add(-within)
	}

	tagSet := map[string]bool{}
	for _, t := range tagFilter {
		tagSet[t] = true
	}
	matchTag := func(tags []string) bool {
		if len(tagSet) == 0 {
			return true
		}
		for _, t := range tags {
			if tagSet[t] {
				return true
			}
		}
		return false
	}

	hits := make([]SearchHit, 0)
	m.mu.RLock()
	terms := make([]*terminal, 0, len(m.terms))
	for _, t := range m.terms {
		terms = append(terms, t)
	}
	m.mu.RUnlock()
	// Lines are copied out a chunk at a time and matched outside the ring
	// lock, so a long search never stalls a terminal's output.
	buf := make([]Line, 256)
	for _, t := range terms {
		t.mu.Lock()
		tags := append([]string(nil), t.info.Tags...)
		name := t.info.Name
		id := t.info.ID
		t.mu.Unlock()
		if !matchTag(tags) {
			continue
		}
		since := int64(-1)
		if !cutoff.IsZero() {
			since = t.ring.seqBefore(cutoff)
		}
		for n := t.ring.read(buf, since); n > 0; n = t.ring.read(buf, since) {
			for _, line := range buf[:n] {
				if re.MatchString(line.Text) {
					hits = append(hits, SearchHit{TerminalID: id, Name: name, Tags: tags, Line: line})
					if limit > 0 && len(hits) >= limit {
						return hits, nil
					}
				}
			}
			since = buf[n-1].Seq
		}
	}
	return hits, nil
}

func (t *terminal) snapshot() Info {
	t.mu.Lock()
	info := t.info
	if len(t.info.Tags) > 0 {
		info.Tags = append([]string(nil), t.info.Tags...)
	}
	if !t.lastOutputAt.IsZero() {
		ts := t.lastOutputAt
		info.LastOutputAt = &ts
		info.IdleSeconds = roundSeconds(time.Since(ts))
	}
	if len(t.attention) > 0 {
		info.Attention = append([]AttentionEvent(nil), t.attention...)
	}
	t.mu.Unlock()
	info.LatestSeq = t.ring.LatestSeq()
	info.Pending = t.ring.Pending()
	return info
}

func (t *terminal) closePtmx() {
	t.closeOnce.Do(func() { _ = t.ptmx.Close() })
}

// readLoop pumps PTY bytes into the ring (and the optional log file). It
// reads in chunks (not lines) so prompts and progress bars surface
// immediately, and lets the ring handle line splitting and \r overwrite
// semantics. It also bumps lastOutputAt on every byte burst so an
// interactive shell sitting at a prompt is correctly reported as recently
// active.
func (t *terminal) readLoop() {
	defer t.ring.CloseSubscribers()
	defer t.finishLog()

	buf := make([]byte, readBufSize)
	for {
		n, err := t.ptmx.Read(buf)
		if n > 0 {
			now := time.Now()
			t.mu.Lock()
			t.lastOutputAt = now
			t.mu.Unlock()
			t.scrMu.Lock()
			t.screen.Write(buf[:n])
			t.scrMu.Unlock()
			t.ring.Write(buf[:n])
			t.writeLog(buf[:n])
			if t.mgr != nil {
				t.mgr.bytesEmitted.Add(int64(n))
			}
		}
		if err != nil {
			// EIO: the child side hung up. ErrClosed: Close closed the
			// master under us (a pollable read returns at once).
			if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EIO) && !errors.Is(err, os.ErrClosed) {
				t.ring.Write([]byte(fmt.Sprintf("[ptyd read error] %v\n", err)))
			}
			t.ring.FlushPending()
			return
		}
	}
}

func (t *terminal) logLimit() int64 {
	if t.logCap > 0 {
		return t.logCap
	}
	return defaultMaxLogFileBytes
}

// writeLog mirrors PTY bytes and wraps the file back to empty once it
// reaches the cap. Callers can race finishLog; a nil file means the
// mirror is already gone.
func (t *terminal) writeLog(p []byte) {
	if len(p) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	f := t.logFile
	if f == nil {
		return
	}
	if t.logSize >= t.logLimit() {
		_ = f.Truncate(0)
		_, _ = f.Seek(0, io.SeekStart)
		t.logSize = 0
	}
	n, _ := f.Write(p)
	t.logSize += int64(n)
}

// finishLog closes the mirror and deletes it. The ring still has the
// tail the API serves. Safe to call from readLoop and Close.
func (t *terminal) finishLog() {
	t.mu.Lock()
	f := t.logFile
	t.logFile = nil
	t.mu.Unlock()
	if f == nil {
		return
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}

// PruneLogs deletes leftover PTY mirrors in logDir. The daemon calls
// this on startup, when no terminal from this process is alive yet.
func PruneLogs(logDir string) (int, error) {
	if logDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pruned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		if err := os.Remove(filepath.Join(logDir, entry.Name())); err != nil {
			return pruned, err
		}
		pruned++
	}
	return pruned, nil
}

func (t *terminal) waitLoop() {
	defer close(t.done)

	err := t.cmd.Wait()
	t.mu.Lock()
	wasRunning := t.info.Status == StatusRunning
	if wasRunning {
		t.info.Status = StatusExited
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			t.info.ExitCode = &code
		}
	} else {
		zero := 0
		t.info.ExitCode = &zero
	}
	exitCode := t.info.ExitCode
	t.mu.Unlock()

	// On natural child exit (vs Close), record the event but LEAVE the
	// entry in m.terms with Status=Exited so clients can still read its
	// final output. A future daemon boot loads it as a ghost.
	if t.mgr != nil && wasRunning {
		t.mgr.totalExited.Add(1)
		if err := writeExit(t.mgr.opts.PersistDir, t.info.ID, exitCode, "child_exit"); err != nil {
			fmt.Fprintf(os.Stderr, "[ptyd] writeExit(%s) failed: %v\n", t.info.ID, err)
		}
	}
}

// watchLoop evaluates watches against (a) every newly committed line and
// (b) the pending buffer once it has been quiet for pendingQuietTime. The
// pending check catches programs waiting at a prompt that never gets a
// trailing newline. It only runs for terminals that have watches, and it
// sleeps until the ring changes (or pending text is about to settle), so
// an idle terminal costs no CPU.
func (t *terminal) watchLoop() {
	cur := cursor{ring: t.ring, last: -1}
	quiet := time.NewTimer(time.Hour)
	quiet.Stop()
	for {
		t.passes.Add(1)
		changed, closed := t.ring.Changed()
		for _, l := range cur.catchUp() {
			t.processLine(l)
		}
		if closed {
			return
		}
		var settled <-chan time.Time
		if settleIn := t.processPending(); settleIn > 0 {
			quiet.Reset(settleIn)
			settled = quiet.C
		}
		select {
		case <-changed:
		case <-settled:
		}
	}
}

func (t *terminal) processLine(line Line) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.fireLocked(line)
}

// processPending evaluates watches against the current pending buffer if it
// has settled (no new bytes for pendingQuietTime), and otherwise returns how
// long until it does. It de-duplicates against the last evaluated pending
// value so the same prompt does not generate repeated attention events.
func (t *terminal) processPending() time.Duration {
	pending, settleIn := t.settledPending()
	if pending == "" || settleIn > 0 {
		return settleIn
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pendingFired == pending {
		return 0
	}
	t.pendingFired = pending
	// Seq=-1 marks a pending (uncommitted) match.
	t.fireLocked(Line{Seq: -1, Text: pending, At: time.Now()})
	return 0
}

// fireLocked records an attention event for every watch matching line,
// keeping only the newest maxAttention events. Caller holds t.mu.
func (t *terminal) fireLocked(line Line) {
	for _, w := range t.watches {
		if w.spec.Once && w.fired {
			continue
		}
		if !w.re.MatchString(line.Text) {
			continue
		}
		w.fired = true
		t.attention = append(t.attention, AttentionEvent{Watch: w.spec.Name, Line: line, At: time.Now()})
		if n := len(t.attention); n > maxAttention {
			t.attention = append(t.attention[:0], t.attention[n-maxAttention:]...)
		}
		if t.mgr != nil {
			t.mgr.watchFires.Add(1)
		}
	}
}

// mergeEnv overlays `override` on top of `parent`, replacing any existing
// entry by key rather than appending duplicates.
func mergeEnv(parent []string, override map[string]string) []string {
	if len(override) == 0 {
		return parent
	}
	out := make([]string, 0, len(parent)+len(override))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, ok := override[kv[:eq]]; ok {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range override {
		out = append(out, k+"="+v)
	}
	return out
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(b)
}

func roundSeconds(d time.Duration) float64 {
	return float64(d.Round(100*time.Millisecond)) / float64(time.Second)
}
