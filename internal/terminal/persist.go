package terminal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/2018kguo/ptyd/internal/native"
)

// persistedMeta is what we write to disk per terminal: its config, and
// which process is running it. The PTY itself can't survive a daemon
// restart (the children get SIGHUP when the PTY master closes), but the
// metadata can, so the daemon can present "ghost" terminals after a
// restart and restore them according to their policy. Files are
// owner-only: Env may hold secrets.
//
// Path: <persist-dir>/<id>/meta.json (one dir per terminal). Deleting a
// terminal through the API removes its directory.
type persistedMeta struct {
	TerminalColors *TerminalColors   `json:"terminal_colors,omitempty"`
	Source         native.Ref        `json:"source,omitzero"`
	ID             ID                `json:"id"`
	Name           string            `json:"name"`
	CWD            string            `json:"cwd"`
	Command        string            `json:"command"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Tags           []string          `json:"tags,omitempty"`
	Watches        []WatchSpec       `json:"watches,omitempty"`
	Cols           uint16            `json:"cols"`
	Rows           uint16            `json:"rows"`
	Restore        RestorePolicy     `json:"restore,omitempty"`
	Agent          string            `json:"agent,omitempty"`
	PID            int               `json:"pid"`
	// ProcIdentity identifies the process running the terminal across
	// pid reuse and reboots (see procIdentity), so a restore can tell its
	// own orphan from an unrelated process.
	ProcIdentity string     `json:"proc_identity,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	Restored     int        `json:"restored,omitempty"`
	RestoredAt   *time.Time `json:"restored_at,omitempty"`
}

func (meta persistedMeta) config() CreateConfig {
	return CreateConfig{
		source: meta.Source, TerminalColors: meta.TerminalColors,
		Name: meta.Name, CWD: meta.CWD, Command: meta.Command, Args: meta.Args, Env: meta.Env,
		Tags: meta.Tags, Watches: meta.Watches, Cols: meta.Cols, Rows: meta.Rows,
		Restore: meta.Restore, Agent: meta.Agent,
	}
}

// Exit reasons recorded in exit.json.
const (
	exitChild       = "child_exit"   // the program ended by itself
	exitShutdown    = "shutdown"     // the daemon stopped it on a graceful shutdown
	exitNotRestored = "not_restored" // a restore was declined; see Notice
)

// persistedExit is written when a terminal stops running, or when a
// restore is declined. The exit_code may be nil if we couldn't determine
// it.
type persistedExit struct {
	ExitedAt time.Time `json:"exited_at"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Reason   string    `json:"reason"`
	Notice   string    `json:"notice,omitempty"`
}

func terminalDir(persistDir string, id ID) string {
	return filepath.Join(persistDir, string(id))
}

// writeMeta writes a terminal's metadata atomically.
func writeMeta(persistDir string, meta persistedMeta) error {
	if persistDir == "" {
		return nil
	}
	dir := terminalDir(persistDir, meta.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(dir, "meta.json"), meta)
}

// writeExit records why a terminal is no longer running.
func writeExit(persistDir string, id ID, exitCode *int, reason, notice string) error {
	if persistDir == "" {
		return nil
	}
	dir := terminalDir(persistDir, id)
	if _, err := os.Stat(dir); err != nil {
		// Meta wasn't persisted (persist-dir was added later, perhaps);
		// nothing to update.
		return nil
	}
	return writeJSONAtomic(filepath.Join(dir, "exit.json"), persistedExit{
		ExitedAt: time.Now(),
		ExitCode: exitCode,
		Reason:   reason,
		Notice:   notice,
	})
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

// readExit returns exit.json, and false if there is none (the daemon died
// while the terminal was running).
func readExit(dir string) (persistedExit, bool) {
	var e persistedExit
	raw, err := os.ReadFile(filepath.Join(dir, "exit.json"))
	if err != nil || json.Unmarshal(raw, &e) != nil {
		return e, false
	}
	return e, true
}

// ghostInfo is the Info a ghost presents.
func ghostInfo(meta persistedMeta, exit persistedExit) Info {
	return Info{
		Source:     meta.Source,
		ID:         meta.ID,
		Name:       meta.Name,
		CWD:        meta.CWD,
		Command:    meta.Command,
		Args:       meta.Args,
		Tags:       meta.Tags,
		PID:        meta.PID,
		Status:     StatusExited,
		CreatedAt:  meta.CreatedAt,
		LatestSeq:  -1,
		Watches:    meta.Watches,
		ExitCode:   exit.ExitCode,
		Restore:    meta.Restore,
		Agent:      meta.Agent,
		Restored:   meta.Restored,
		RestoredAt: meta.RestoredAt,
		Notice:     exit.Notice,
	}
}

// loadGhosts walks the persist directory and returns Info stubs for every
// terminal in it. The PTY can't be resurrected (the process is gone and
// the fd is dead), but the metadata and the saved screen are preserved so
//
//   - GET /terminals lists what existed before the restart
//   - GET /terminals/{id} and /screen work for it
//   - the terminal can be restored according to its policy.
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
		dir := filepath.Join(persistDir, e.Name())
		meta, err := readMeta(dir)
		if err != nil || meta.ID != ID(e.Name()) {
			continue
		}
		exit, _ := readExit(dir)
		ghosts = append(ghosts, ghostInfo(meta, exit))
	}
	return ghosts, nil
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
	return os.RemoveAll(terminalDir(persistDir, id))
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
