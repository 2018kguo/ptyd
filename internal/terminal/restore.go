package terminal

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/2018kguo/ptyd/internal/vt"
)

var (
	// ErrNotGhost is returned when restoring a terminal that is running.
	ErrNotGhost = errors.New("terminal is not a ghost")
	// ErrRestoreDeclined is returned when a restore was refused; the
	// terminal stays a ghost and its Notice says why.
	ErrRestoreDeclined = errors.New("restore declined")
)

// RestoreGhosts runs at daemon startup. It restores every ghost whose
// policy asks for it and that was still running when the previous daemon
// went away (it crashed, or shut down gracefully). Ghosts whose program
// had ended by itself, or whose restore was already declined, stay ghosts,
// so a crash loop cannot restore one twice or pile up notices.
func (m *Manager) RestoreGhosts() {
	m.mu.RLock()
	var ids []ID
	for id, g := range m.ghosts {
		if g.Restore == RestoreRerun || g.Restore == RestoreAgent {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	slices.Sort(ids)
	// Restores are independent and mostly wait (on fsync, on process
	// start, on an orphan to exit), so a few run at once.
	sem := make(chan struct{}, restoreParallelism)
	var wg sync.WaitGroup
	for _, id := range ids {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if exit, ok := readExit(terminalDir(m.opts.PersistDir, id)); ok && exit.Reason != exitShutdown {
				return
			}
			info, err := m.Restore(id, "")
			switch {
			case err == nil:
				fmt.Fprintf(os.Stderr, "[ptyd] restored %s (%s): %s\n", id, info.Restore, info.Notice)
			default:
				fmt.Fprintf(os.Stderr, "[ptyd] did not restore %s: %v\n", id, err)
			}
		})
	}
	wg.Wait()
}

// restoreParallelism is how many restores RestoreGhosts runs at once.
var restoreParallelism = 16

// Restore brings a ghost back under the same id, with policy (or its own
// policy if empty): rerun starts the same command again, agent resumes
// its exact agent session. The saved screen stays in view above the new
// process, scrolling into scrollback as needed. If the restore is refused
// (the agent session cannot be identified with certainty, a previous
// process cannot be stopped, the command fails to start), the terminal
// stays a ghost, the notice is printed on its saved screen and recorded,
// and the error wraps ErrRestoreDeclined.
func (m *Manager) Restore(id ID, policy RestorePolicy) (Info, error) {
	m.mu.Lock()
	_, ghost := m.ghosts[id]
	_, live := m.terms[id]
	busy := m.restoring[id]
	if ghost && !busy {
		m.restoring[id] = true
	}
	m.mu.Unlock()
	switch {
	case live:
		return Info{}, fmt.Errorf("%w: %s is running", ErrNotGhost, id)
	case !ghost:
		return Info{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	case busy:
		return Info{}, fmt.Errorf("%w: a restore of %s is in progress", ErrNotGhost, id)
	}
	defer func() {
		m.mu.Lock()
		delete(m.restoring, id)
		m.mu.Unlock()
	}()
	if m.opts.PersistDir == "" {
		return Info{}, fmt.Errorf("restore needs --persist-dir")
	}

	dir := terminalDir(m.opts.PersistDir, id)
	meta, err := readMeta(dir)
	if err != nil {
		return Info{}, fmt.Errorf("read metadata: %w", err)
	}
	cfg := meta.config()
	if policy != "" {
		cfg.Restore = policy
	}
	switch cfg.Restore {
	case RestoreRerun:
	case RestoreAgent:
		if cfg.Agent == "" {
			cfg.Agent = detectAgent(cfg.Command)
		}
		if _, ok := agents[cfg.Agent]; !ok {
			return Info{}, fmt.Errorf("no agent adapter for %q (supported: %s)", cfg.Command, supportedAgents())
		}
	default:
		return Info{}, fmt.Errorf(`terminal %s has restore policy %q; pass {"policy":"rerun"} or {"policy":"agent"}`, id, cmp.Or(string(cfg.Restore), "none"))
	}
	scr, gen, err := loadScreen(dir, int(meta.Cols), int(meta.Rows))
	if err != nil {
		return Info{}, err
	}

	// A process from the previous run may survive the hangup (or the
	// daemon may have died moments after starting it). Never run two.
	if err := stopOrphan(meta); err != nil {
		return m.decline(id, scr, gen, fmt.Sprintf("ptyd: %v; not restoring, showing last screen", err))
	}

	args, notice := cfg.Args, fmt.Sprintf("ptyd: restarted %s after a daemon restart", cfg.Command)
	if cfg.Restore == RestoreAgent {
		var ok bool
		args, notice, ok = agentPlan(cfg.Agent, agents[cfg.Agent], cfg, agentStateFile(m.opts.PersistDir, id))
		if !ok {
			return m.decline(id, scr, gen, notice)
		}
	}

	// The old screen and scrollback stay visible above the new process.
	scr.ArchiveScreen()
	writeNotice(scr, notice, false)
	info, err := m.spawn(cfg, spawnSpec{
		id: id, createdAt: meta.CreatedAt, args: args,
		screen: scr, tailGen: gen, restored: meta.Restored + 1, notice: notice,
	})
	if err != nil {
		// spawn failed before touching the saved screen; reload it.
		scr, gen, _ = loadScreen(dir, int(meta.Cols), int(meta.Rows))
		return m.decline(id, scr, gen, fmt.Sprintf("ptyd: could not restart %s: %v; showing last screen", cfg.Command, err))
	}
	return info, nil
}

// writeNotice prints a one-line notice on scr. For a new program it goes
// on its own line at the cursor. On a saved screen (atBottom) it goes on
// the last row if that is blank, and otherwise scrolls in at the bottom,
// so nothing else on the screen is overwritten.
func writeNotice(scr *vt.Terminal, notice string, atBottom bool) {
	line := "\x1b[0;2m" + notice + "\x1b[0m"
	if !atBottom {
		scr.Write([]byte(line + "\r\n"))
		return
	}
	scr.Write([]byte("\x1b[r\x1b[999;1H"))
	if lines := scr.Lines(); lines[len(lines)-1] != "" {
		scr.Write([]byte("\r\n"))
	}
	scr.Write([]byte(line))
}

// decline keeps the terminal a ghost, with the notice on its saved screen
// and in its metadata.
func (m *Manager) decline(id ID, scr *vt.Terminal, gen uint64, notice string) (Info, error) {
	dir := terminalDir(m.opts.PersistDir, id)
	writeNotice(scr, notice, true)
	cols, rows := scr.Size()
	if err := writeCheckpoint(dir, cols, rows, gen+1, scr.Repaint(true)); err == nil {
		removeTailsBefore(dir, gen+1)
	}
	if err := writeExit(m.opts.PersistDir, id, nil, exitNotRestored, notice); err != nil {
		fmt.Fprintf(os.Stderr, "[ptyd] writeExit(%s): %v\n", id, err)
	}
	m.mu.Lock()
	if g, ok := m.ghosts[id]; ok {
		g.Notice = notice
		m.ghosts[id] = g
	}
	m.mu.Unlock()
	return Info{}, fmt.Errorf("%w: %s", ErrRestoreDeclined, notice)
}

// stopOrphan ends what is left of the terminal's previous process group,
// if anything. Children of the daemon get SIGHUP when it dies, but a
// program may ignore it. The group is ours only if its leader is gone
// (a pid that still leads a group cannot be reused) or is the very
// process we started (same procIdentity).
func stopOrphan(meta persistedMeta) error {
	pid := meta.PID
	if pid <= 0 || syscall.Kill(-pid, 0) != nil {
		return nil
	}
	if ident, err := procIdentity(pid); err == nil {
		if meta.ProcIdentity == "" {
			return fmt.Errorf("process %d may still be running the previous session and cannot be verified", pid)
		}
		if ident != meta.ProcIdentity {
			return nil // an unrelated process reused the pid
		}
	}
	gone := func(d time.Duration) bool {
		for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if syscall.Kill(-pid, 0) != nil {
				return true
			}
		}
		return false
	}
	_ = syscall.Kill(-pid, syscall.SIGHUP)
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if gone(3 * time.Second) {
		return nil
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	if gone(2 * time.Second) {
		return nil
	}
	return fmt.Errorf("the previous process group %d is still running", pid)
}
