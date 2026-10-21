package terminal

import (
	"regexp"
	"time"
)

// ansiPattern matches the ANSI escape sequences emitted by terminals:
//
//   - CSI:   ESC [  parameters? intermediates? final-byte
//   - OSC:   ESC ]  ... terminator (BEL or ST)
//   - other: ESC <single byte>  (e.g. ESC = / ESC > keypad modes)
//
// This is intentionally aggressive: a borderline byte dropped is better
// than cursor/mode sequences (ESC[K, ESC[?1l, ESC>) leaking into text.
var ansiPattern = regexp.MustCompile(
	`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]` + // CSI
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?` + // OSC ending in BEL or ST (or unterminated)
		`|\x1b[^\x5b\x5d]`, // single-byte escape (ESC <c>) excluding ESC[ and ESC]
)

// stripANSI removes ANSI escape sequences from s.
func stripANSI(s string) string {
	if s == "" {
		return s
	}
	return ansiPattern.ReplaceAllString(s, "")
}

// ID is a unique terminal identifier.
type ID string

// Status represents the current state of a terminal.
type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusClosed  Status = "closed"
)

// Info is metadata about a terminal session. It deliberately does NOT include
// scrollback; fetch that via GET /terminals/{id}/output (Manager.Output).
type Info struct {
	ID           ID         `json:"id"`
	Name         string     `json:"name"`
	CWD          string     `json:"cwd"`
	Command      string     `json:"command"`
	Args         []string   `json:"args,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	PID          int        `json:"pid"`
	Status       Status     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	LastOutputAt *time.Time `json:"last_output_at,omitempty"`
	IdleSeconds  float64    `json:"idle_seconds"`
	ExitCode     *int       `json:"exit_code,omitempty"`

	// LatestSeq is the highest committed line sequence number (-1 if none).
	LatestSeq int64 `json:"latest_seq"`
	// Pending is the current partial line (e.g. an interactive prompt that
	// has not yet emitted a newline). It has no sequence number and may
	// be replaced or finalized by the next byte of output.
	Pending string `json:"pending,omitempty"`

	// Watches and Attention are the configured patterns and the events
	// they have produced and not yet acknowledged (newest 100 kept).
	Watches   []WatchSpec      `json:"watches,omitempty"`
	Attention []AttentionEvent `json:"attention,omitempty"`
}

// CreateConfig is the body of POST /terminals.
type CreateConfig struct {
	Name    string            `json:"name"`
	CWD     string            `json:"cwd"`
	Command string            `json:"command"` // binary to exec (looked up in PATH); default $SHELL or /bin/sh
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Tags    []string          `json:"tags,omitempty"`
	Watches []WatchSpec       `json:"watches,omitempty"`
	Cols    uint16            `json:"cols"`
	Rows    uint16            `json:"rows"`
}

// Line is a single captured terminal line with a monotonic sequence number.
type Line struct {
	Seq  int64     `json:"seq"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// OutputSnapshot is the response body of GET /terminals/{id}/output.
type OutputSnapshot struct {
	Lines     []Line `json:"lines"`
	LatestSeq int64  `json:"latest_seq"`
	Pending   string `json:"pending,omitempty"`
}

// WatchSpec describes a regex pattern to monitor on each new committed line.
// When it matches, an AttentionEvent is recorded on the terminal (see
// Info.Attention) until acknowledged.
type WatchSpec struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Once    bool   `json:"once,omitempty"` // fire only the first time it matches
}

// AttentionEvent records a watch firing on a particular line.
type AttentionEvent struct {
	Watch string    `json:"watch"`
	Line  Line      `json:"line"`
	At    time.Time `json:"at"`
}
