package native

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"sync"
	"time"
)

const maxSessions = 500
const maxOutput = 4 << 20
const timeout = 8 * time.Second

var jobID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

type Discoverer struct {
	mu     sync.Mutex
	cached Listing
	at     time.Time
}

// List caches briefly so several overview clients do not spawn CLI probes on
// every frame. Import uses Fresh and never authorizes from this cache.
func (d *Discoverer) List(ctx context.Context) Listing {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.at.IsZero() && time.Since(d.at) < 5*time.Second {
		return d.cached
	}
	out := d.Fresh(ctx)
	out.Processes, out.ProcessIssue, out.ProcessesTruncated = discoverProcesses(ctx, out.Sessions)
	if ctx.Err() == nil {
		d.cached = out
		d.at = time.Now()
	}
	return out
}
func (d *Discoverer) Fresh(ctx context.Context) Listing {
	out := Listing{Sessions: []Session{}, Issues: []Issue{}}
	type result struct {
		provider  Provider
		sessions  []Session
		truncated bool
		err       error
	}
	ch := make(chan result, 2)
	for _, p := range []Provider{Codex, Claude} {
		go func() {
			probe, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			var r result
			r.provider = p
			if p == Codex {
				r.sessions, r.truncated, r.err = codexSessions(probe)
			} else {
				r.sessions, r.truncated, r.err = claudeSessions(probe)
			}
			ch <- r
		}()
	}
	for range 2 {
		r := <-ch
		if r.err != nil {
			out.Issues = append(out.Issues, Issue{r.provider, r.err.Error()})
		} else {
			out.Sessions = append(out.Sessions, r.sessions...)
		}
		out.Truncated = out.Truncated || r.truncated
		if r.truncated {
			out.Issues = append(out.Issues, Issue{r.provider, "Native discovery omitted records; exact import is unavailable until a complete listing can be read."})
		}
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].Ref.Key() < out.Sessions[j].Ref.Key() })
	sort.Slice(out.Issues, func(i, j int) bool { return out.Issues[i].Provider < out.Issues[j].Provider })
	return out
}

type cappedOutput struct{ bytes.Buffer }

func (w *cappedOutput) Write(p []byte) (int, error) {
	if len(p) > maxOutput-w.Len() {
		return 0, fmt.Errorf("native response exceeds %d bytes", maxOutput)
	}
	return w.Buffer.Write(p)
}
func commandJSON(ctx context.Context, program string, args []string, out any) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.WaitDelay = time.Second
	var stdout, stderr cappedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s query failed (%v); check that its CLI and native daemon are available", program, err)
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("%s returned an unsupported response: %w", program, err)
	}
	return nil
}

func claudeSessions(ctx context.Context) ([]Session, bool, error) {
	var rows []struct {
		CWD        string `json:"cwd"`
		Kind       string `json:"kind"`
		StartedAt  int64  `json:"startedAt"`
		ID         string `json:"id"`
		State      string `json:"state"`
		PID        int    `json:"pid"`
		Status     string `json:"status"`
		WaitingFor string `json:"waitingFor"`
		SessionID  string `json:"sessionId"`
		Name       string `json:"name"`
	}
	if err := commandJSON(ctx, "claude", []string{"agents", "--json", "--all"}, &rows); err != nil {
		return nil, false, err
	}
	truncated := len(rows) > maxSessions

	out := make([]Session, 0, len(rows))
	seen := map[string]bool{}
	for _, r := range rows {
		ref := Ref{Claude, r.SessionID}
		if ref.Validate() != nil {
			truncated = true
			continue
		}
		s := Session{Ref: ref, Name: r.Name, CWD: r.CWD, Status: r.Status, WaitingFor: r.WaitingFor, WorkerPID: r.PID, StartedAt: time.UnixMilli(r.StartedAt), Handle: r.ID}
		if s.Status == "" {
			s.Status = r.State
		}
		switch r.Kind {
		case "interactive":
			s.Action = Background
			s.Reason = "Run /bg in the original Claude tab, let its handoff finish, then refresh and import this session."
		case "background":
			if !jobID.MatchString(r.ID) {
				s.Action = Unavailable
				s.Reason = "Claude did not report a background job id; refresh after the handoff finishes."
			} else if r.PID > 0 && (r.Status == "busy" || r.Status == "waiting" || r.Status == "idle") {
				s.Action = Attach
			} else if r.PID == 0 && (r.State == "done" || r.State == "failed" || r.State == "stopped") {
				s.Action = Resume
			} else {
				s.Action = Unavailable
				s.Reason = "Claude has not confirmed a live worker or a stopped conversation; refresh before importing."
			}
		default:
			s.Action = Unavailable
			s.Reason = "Claude reported an unsupported session kind; use its own agent view."
		}
		// A foreground record and its new background worker can coexist briefly.
		// Fail closed during that handoff rather than choosing one by timestamp.
		if seen[ref.Key()] {
			for i := range out {
				if out[i].Ref == ref {
					out[i].Action = Unavailable
					out[i].Reason = "Claude reports more than one worker for this conversation; finish the native handoff and refresh."
				}
			}
			continue
		}
		seen[ref.Key()] = true
		out = append(out, s)
	}
	return out[:min(len(out), maxSessions)], truncated || len(out) > maxSessions, nil
}

func (d *Discoverer) Lookup(ctx context.Context, ref Ref) (Session, error) {
	if err := ref.Validate(); err != nil {
		return Session{}, err
	}
	listing := d.Fresh(ctx)
	for _, issue := range listing.Issues {
		if issue.Provider == ref.Provider {
			return Session{}, fmt.Errorf("%s: %s", issue.Provider, issue.Message)
		}
	}
	for _, s := range listing.Sessions {
		if s.Ref == ref {
			return s, nil
		}
	}
	if listing.Truncated {
		return Session{}, fmt.Errorf("native listing was truncated; narrow the native session list and refresh")
	}
	return Session{}, fmt.Errorf("native session %s is no longer available; refresh discovery", ref.Key())
}
