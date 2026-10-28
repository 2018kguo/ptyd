// Package native discovers agent sessions through the agents' own local APIs.
// It never starts, stops or resumes a worker during discovery.
package native

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

type Provider string

const (
	Codex  Provider = "codex"
	Claude Provider = "claude"
)

type Ref struct {
	Provider Provider `json:"provider"`
	ID       string   `json:"id"`
}

func (r Ref) Key() string { return string(r.Provider) + ":" + r.ID }

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (r Ref) Validate() error {
	if r.Provider != Codex && r.Provider != Claude {
		return fmt.Errorf("provider must be codex or claude")
	}
	if !uuid.MatchString(r.ID) {
		return fmt.Errorf("session id must be a full UUID")
	}
	return nil
}

type Action string

const (
	Attach      Action = "attach"
	Background  Action = "background"
	Resume      Action = "resume"
	Unavailable Action = "unavailable"
)

type Session struct {
	Ref        Ref       `json:"ref"`
	Name       string    `json:"name"`
	CWD        string    `json:"cwd"`
	Status     string    `json:"status"`
	WaitingFor string    `json:"waiting_for,omitempty"`
	Action     Action    `json:"action"`
	Reason     string    `json:"reason,omitempty"`
	WorkerPID  int       `json:"worker_pid,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	// Handle is a Claude background job id, not a transcript/session-id guess.
	Handle   string `json:"handle,omitempty"`
	Endpoint string `json:"-"`
}

type Issue struct {
	Provider Provider `json:"provider"`
	Message  string   `json:"message"`
}
type Listing struct {
	Sessions  []Session `json:"sessions"`
	Issues    []Issue   `json:"issues"`
	Truncated bool      `json:"truncated"`
}

type Launch struct {
	Command string
	Args    []string
	CWD     string
}

// Plan is a pure decision. Only fresh native observations can authorize a
// frontend launch; an HTTP caller cannot supply argv, a directory or a job id.
func Plan(s Session, allowResume bool) (Launch, error) {
	if err := s.Ref.Validate(); err != nil {
		return Launch{}, err
	}
	if !filepath.IsAbs(s.CWD) {
		return Launch{}, fmt.Errorf("native session has no absolute working directory")
	}
	switch s.Action {
	case Attach:
		if s.WorkerPID <= 0 {
			return Launch{}, fmt.Errorf("native provider did not identify a live hosting process")
		}
	case Resume:
		if !allowResume {
			return Launch{}, fmt.Errorf("session is stopped; import with --resume to continue its saved conversation")
		}
	case Background, Unavailable:
		return Launch{}, fmt.Errorf("%s", s.Reason)
	default:
		return Launch{}, fmt.Errorf("unrecognized native action %q; refresh discovery", s.Action)
	}
	switch s.Ref.Provider {
	case Codex:
		if s.Action != Attach {
			return Launch{}, fmt.Errorf("Codex session is not loaded; open it in Codex before importing")
		}
		if !filepath.IsAbs(s.Endpoint) {
			return Launch{}, fmt.Errorf("Codex did not report a local daemon endpoint")
		}
		return Launch{Command: "codex", Args: []string{"resume", "--remote", "unix://" + s.Endpoint, "--", s.Ref.ID}, CWD: s.CWD}, nil
	case Claude:
		if !jobID.MatchString(s.Handle) {
			return Launch{}, fmt.Errorf("Claude did not report a valid background job id; run /bg in its original tab")
		}
		return Launch{Command: "claude", Args: []string{"attach", s.Handle}, CWD: s.CWD}, nil
	}
	return Launch{}, fmt.Errorf("unsupported provider")
}

// Discovery is also implemented by deterministic integration fixtures.
type Discovery interface{ List(context.Context) Listing }
