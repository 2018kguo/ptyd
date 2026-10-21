package terminal

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

// stripANSI removes the ANSI escape sequences terminals emit:
//
//   - CSI:   ESC [  parameters (0x30-0x3f)* intermediates (0x20-0x2f)* final (0x40-0x7e)
//   - OSC:   ESC ]  ... up to and including BEL or ST (ESC \), or to the end
//     of s if unterminated
//   - other: ESC <single character>  (e.g. ESC = / ESC > keypad modes)
//
// This is intentionally aggressive: a borderline byte dropped is better
// than cursor/mode sequences (ESC[K, ESC[?1l, ESC>) leaking into text. An
// ESC that starts none of these (a lone trailing ESC, an unfinished CSI) is
// kept, so a partial sequence in the pending line is stripped once it
// completes. Text without ESC is returned as is, without allocating.
func stripANSI(s string) string {
	i := strings.IndexByte(s, 0x1b)
	if i < 0 {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i >= 0 {
		out.WriteString(s[:i])
		s = s[i:]
		n := escapeLen(s)
		if n == 0 {
			out.WriteByte(s[0]) // not a sequence: keep the ESC
			n = 1
		}
		s = s[n:]
		i = strings.IndexByte(s, 0x1b)
	}
	out.WriteString(s)
	return out.String()
}

// stripANSIBytes is stripANSI for bytes, with a single allocation. The
// result never aliases p.
func stripANSIBytes(p []byte) string {
	if bytes.IndexByte(p, 0x1b) < 0 {
		return string(p)
	}
	// stripANSI only reads its argument and, as s has an ESC, returns a
	// new string, so a read-only view of p is enough.
	return stripANSI(unsafe.String(unsafe.SliceData(p), len(p)))
}

// escapeLen returns the length of the escape sequence at the start of s
// (s[0] is ESC), or 0 if s does not start with one.
func escapeLen(s string) int {
	if len(s) < 2 {
		return 0
	}
	i := 2
	switch s[1] {
	case '[': // CSI
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
		return 0
	case ']': // OSC
		for i < len(s) && s[i] != 0x07 && s[i] != 0x1b {
			i++
		}
		switch {
		case i < len(s) && s[i] == 0x07:
			return i + 1
		case i+1 < len(s) && s[i] == 0x1b && s[i+1] == '\\':
			return i + 2
		}
		return i
	}
	_, size := utf8.DecodeRuneInString(s[1:])
	return 1 + size
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

	// Attached is the number of clients attached (see Manager.Attach).
	Attached int `json:"attached,omitempty"`

	// Watches and Attention are the configured patterns and the events
	// they have produced and not yet acknowledged (newest 100 kept).
	Watches   []WatchSpec      `json:"watches,omitempty"`
	Attention []AttentionEvent `json:"attention,omitempty"`

	// Restore is the terminal's restore policy and Agent the adapter used
	// by the agent policy. Restored counts how many times the terminal was
	// brought back after a daemon restart (it keeps its id), RestoredAt is
	// the last time. Notice explains the last restore decision, such as
	// why an agent session was not resumed. AgentSession is the agent
	// session the terminal is known to be in.
	Restore      RestorePolicy `json:"restore,omitempty"`
	Agent        string        `json:"agent,omitempty"`
	Restored     int           `json:"restored,omitempty"`
	RestoredAt   *time.Time    `json:"restored_at,omitempty"`
	Notice       string        `json:"notice,omitempty"`
	AgentSession string        `json:"agent_session,omitempty"`
}

// RestorePolicy says what happens to a terminal when the daemon comes back
// after a restart or crash that ended it.
type RestorePolicy string

const (
	// RestoreNone keeps the terminal as a ghost showing its saved screen.
	RestoreNone RestorePolicy = "none"
	// RestoreRerun starts the same command again (same cwd and env), below
	// the saved screen and scrollback.
	RestoreRerun RestorePolicy = "rerun"
	// RestoreAgent resumes the exact agent session the terminal was in,
	// or leaves a ghost with a notice if that session cannot be
	// identified with certainty.
	RestoreAgent RestorePolicy = "agent"
)

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
	// Restore is the restore policy (default none); Agent names the agent
	// adapter for the agent policy (default: detected from Command).
	Restore RestorePolicy `json:"restore,omitempty"`
	Agent   string        `json:"agent,omitempty"`
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
