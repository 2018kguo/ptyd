package terminal

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/2018kguo/ptyd/internal/native"
)

var ErrImportDeclined = errors.New("native import declined")

type ImportRequest struct {
	Ref         native.Ref          `json:"ref"`
	TerminalEnv TerminalEnvironment `json:"terminal_env,omitempty"`
	Name        string              `json:"name,omitempty"`
	Resume      bool                `json:"resume,omitempty"`
	Cols        uint16              `json:"cols,omitempty"`
	Rows        uint16              `json:"rows,omitempty"`
}
type ImportResult struct {
	Terminal Info           `json:"terminal"`
	Session  native.Session `json:"session"`
	Reused   bool           `json:"reused"`
}

// NativeLookup bypasses the overview cache and resolves an exact conversation.
type NativeLookup interface {
	Lookup(context.Context, native.Ref) (native.Session, error)
}

// ImportAgent creates a terminal frontend for an existing native conversation.
// Native workers remain owned by their daemon; closing this terminal only
// closes its client. The source reference is part of the normal registry and
// persistence, so import retries and daemon restarts do not invent a new owner.
func (m *Manager) ImportAgent(ctx context.Context, lookup NativeLookup, req ImportRequest) (ImportResult, error) {
	if err := req.Ref.Validate(); err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrImportDeclined, err)
	}
	m.importMu.Lock()
	defer m.importMu.Unlock()
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	observed, err := lookup.Lookup(ctx, req.Ref)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrImportDeclined, err)
	}
	if observed.Ref != req.Ref {
		return ImportResult{}, fmt.Errorf("%w: native provider returned a different conversation", ErrImportDeclined)
	}
	var stopped []ID
	for _, info := range m.List() {
		if info.Source != req.Ref && !(info.Agent == string(req.Ref.Provider) && info.AgentSession == req.Ref.ID) {
			continue
		}
		if info.Status == StatusRunning {
			if info.Source.Provider != "" {
				return ImportResult{}, fmt.Errorf("%w: imported window %s already exists; attach that window explicitly or close it first. Its original conversation is launch provenance, not a verified current input target", ErrImportDeclined, info.ID)
			}
			return ImportResult{Terminal: info, Session: observed, Reused: true}, nil
		}
		if info.Source.Provider != "" {
			stopped = append(stopped, info.ID)
		}
	}
	plan, err := native.Plan(observed, req.Resume)
	if err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrImportDeclined, err)
	}
	identity := ""
	if observed.WorkerPID > 0 {
		identity, err = procIdentity(observed.WorkerPID)
		if err != nil {
			return ImportResult{}, fmt.Errorf("%w: cannot verify native worker: %v", ErrImportDeclined, err)
		}
	}
	name := req.Name
	if name == "" {
		name = observed.Name
	}
	if name == "" {
		name = string(req.Ref.Provider) + "-" + req.Ref.ID[:8]
	}
	for len(name) > maxNameLen {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	if err = ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	cfg := CreateConfig{Name: name, CWD: plan.CWD, Command: plan.Command, Args: plan.Args, Cols: req.Cols, Rows: req.Rows, Restore: RestoreNone, source: req.Ref}
	if err := req.TerminalEnv.Apply(&cfg); err != nil {
		return ImportResult{}, fmt.Errorf("%w: %v", ErrImportDeclined, err)
	}
	info, err := m.Create(cfg)
	if err != nil {
		return ImportResult{}, err
	}
	// A successful fork/exec only confirms a frontend, not worker identity.
	// Check the native authority again before reporting a live import. Never
	// close the old tab or send input to make a handoff appear successful.
	current, err := lookup.Lookup(ctx, req.Ref)
	if err == nil && observed.Action == native.Resume {
		confirmation, cancel := context.WithTimeout(ctx, 4*time.Second)
		for err == nil && (current.Action != native.Attach || current.WorkerPID <= 0) {
			select {
			case <-confirmation.Done():
				err = fmt.Errorf("native daemon has not confirmed the resumed worker; refresh discovery")
			case <-time.After(100 * time.Millisecond):
				current, err = lookup.Lookup(confirmation, req.Ref)
			}
		}
		cancel()
	}
	if err == nil && (current.Ref != observed.Ref || current.CWD != observed.CWD || (observed.Action == native.Attach && (current.Action != native.Attach || current.WorkerPID != observed.WorkerPID || current.Endpoint != observed.Endpoint))) {
		err = fmt.Errorf("native worker changed during import; refresh and try again")
	}
	if err == nil && current.WorkerPID > 0 {
		after, e := procIdentity(current.WorkerPID)
		if e != nil || (identity != "" && after != identity) {
			err = fmt.Errorf("native process identity changed during import")
		}
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		_ = m.Close(info.ID)
		return ImportResult{}, fmt.Errorf("%w: %v", ErrImportDeclined, err)
	}
	m.expireNativeView(info.ID)
	for _, id := range stopped {
		_ = m.Close(id)
	}
	return ImportResult{Terminal: info, Session: current}, nil
}
