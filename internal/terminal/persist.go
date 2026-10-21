package terminal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// persistedMeta is what we write to disk per terminal at create time.
// PTY state itself can't survive a daemon restart (children of the
// daemon get killed), but the metadata can, so the daemon can present
// "ghost" terminals via the API after a restart instead of pretending
// nothing happened. Files are owner-only: Env may hold secrets.
//
// Path: <persist-dir>/<id>/meta.json (one dir per terminal). Deleting a
// terminal through the API removes its directory.
type persistedMeta struct {
	ID        ID                `json:"id"`
	Name      string            `json:"name"`
	CWD       string            `json:"cwd"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Tags      []string          `json:"tags,omitempty"`
	Watches   []WatchSpec       `json:"watches,omitempty"`
	Cols      uint16            `json:"cols"`
	Rows      uint16            `json:"rows"`
	PID       int               `json:"pid"`
	CreatedAt time.Time         `json:"created_at"`
}

// persistedExit is written when a terminal closes (gracefully or via
// child exit). The exit_code may be nil if we couldn't determine it.
type persistedExit struct {
	ExitedAt time.Time `json:"exited_at"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Reason   string    `json:"reason"` // "child_exit" | "shutdown"
}

// writeMeta serializes the create-time metadata for a terminal. Failures
// log to stderr but don't fail the create — persistence is best-effort.
func writeMeta(persistDir string, id ID, cfg CreateConfig, pid int, createdAt time.Time) error {
	if persistDir == "" {
		return nil
	}
	dir := filepath.Join(persistDir, string(id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	meta := persistedMeta{
		ID:        id,
		Name:      cfg.Name,
		CWD:       cfg.CWD,
		Command:   cfg.Command,
		Args:      cfg.Args,
		Env:       cfg.Env,
		Tags:      cfg.Tags,
		Watches:   cfg.Watches,
		Cols:      cfg.Cols,
		Rows:      cfg.Rows,
		PID:       pid,
		CreatedAt: createdAt,
	}
	return writeJSONAtomic(filepath.Join(dir, "meta.json"), meta)
}

// writeExit records the close event so we know the terminal is dead
// even before reading meta.json on a future boot.
func writeExit(persistDir string, id ID, exitCode *int, reason string) error {
	if persistDir == "" {
		return nil
	}
	dir := filepath.Join(persistDir, string(id))
	if _, err := os.Stat(dir); err != nil {
		// Meta wasn't persisted (persist-dir was added later, perhaps);
		// nothing to update.
		return nil
	}
	return writeJSONAtomic(filepath.Join(dir, "exit.json"), persistedExit{
		ExitedAt: time.Now(),
		ExitCode: exitCode,
		Reason:   reason,
	})
}

// loadGhosts walks the persist directory and returns Info stubs for
// every terminal whose process is no longer alive. The PTY can't be
// resurrected (process is gone, fd is dead) but the metadata is
// preserved so:
//
//   - GET /terminals lists what existed before the restart
//   - GET /terminals/{id} returns metadata even after the daemon
//     forgets the PTY
//   - clients can tell "existed before the restart" from "never existed"
//
// Ghosts are returned with Status = StatusExited. Mutations on them
// (write/keys/signal/resize/wait) return ErrGhostTerminal; DELETE
// removes them.
func loadGhosts(persistDir string) ([]Info, error) {
	if persistDir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(persistDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var ghosts []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := ID(e.Name())
		metaPath := filepath.Join(persistDir, e.Name(), "meta.json")
		raw, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta persistedMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		// The PTY master fd died with the previous daemon, so there is
		// no reattaching; the entry is metadata only.
		exitCode := readExit(persistDir, id)
		ghost := Info{
			ID:        id,
			Name:      meta.Name,
			CWD:       meta.CWD,
			Command:   meta.Command,
			Args:      meta.Args,
			Tags:      meta.Tags,
			PID:       meta.PID,
			Status:    StatusExited,
			CreatedAt: meta.CreatedAt,
			LatestSeq: -1,
			Watches:   meta.Watches,
			ExitCode:  exitCode,
		}
		ghosts = append(ghosts, ghost)
	}
	return ghosts, nil
}

// readMeta reads a terminal directory's meta.json.
func readMeta(dir string) (persistedMeta, error) {
	var meta persistedMeta
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return meta, err
	}
	err = json.Unmarshal(raw, &meta)
	return meta, err
}

// readExit returns the exit code from exit.json if present, else nil.
func readExit(persistDir string, id ID) *int {
	raw, err := os.ReadFile(filepath.Join(persistDir, string(id), "exit.json"))
	if err != nil {
		return nil
	}
	var e persistedExit
	if json.Unmarshal(raw, &e) != nil {
		return nil
	}
	return e.ExitCode
}

// PruneGhosts deletes ghost dirs older than maxAge. Run from boot to
// keep the persist dir from growing without bound.
func PruneGhosts(persistDir string, maxAge time.Duration) (int, error) {
	if persistDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(persistDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	pruned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(persistDir, e.Name())
		// Age is time since the terminal last changed anything on disk
		// (metadata, exit record, checkpoint or tail), so a terminal that
		// ran for weeks is not pruned the moment the daemon restarts.
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var ts time.Time
		for _, f := range files {
			if fi, err := f.Info(); err == nil && fi.ModTime().After(ts) {
				ts = fi.ModTime()
			}
		}
		if ts.IsZero() {
			continue
		}
		if ts.Before(cutoff) {
			if err := os.RemoveAll(dir); err == nil {
				pruned++
			}
		}
	}
	return pruned, nil
}

// removePersisted deletes a terminal's metadata directory.
func removePersisted(persistDir string, id ID) error {
	if persistDir == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(persistDir, string(id)))
}

// writeJSONAtomic writes v to path atomically (see writeFileAtomic).
func writeJSONAtomic(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw)
}

// ErrGhostTerminal is returned by mutations attempted on a ghost.
var ErrGhostTerminal = errors.New("terminal is a ghost (metadata-only; daemon restarted since spawn)")
