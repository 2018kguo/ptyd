package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/native"
)

const importFixtureID = "11111111-1111-4111-8111-111111111111"

type nativeLookupFunc func(context.Context, native.Ref) (native.Session, error)

func (f nativeLookupFunc) Lookup(ctx context.Context, r native.Ref) (native.Session, error) {
	return f(ctx, r)
}
func importFixture(t *testing.T) (*Manager, native.Session, nativeLookupFunc, *os.Process) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nprintf 'native frontend\\n'\nexec cat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	worker := exec.Command("sleep", "60")
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Process.Kill(); _ = worker.Wait() })
	m := NewManagerWithOptions(Options{PersistDir: filepath.Join(dir, "state")})
	t.Cleanup(m.CloseAll)
	session := native.Session{Ref: native.Ref{Provider: native.Claude, ID: importFixtureID}, Name: "fixture", CWD: dir, Status: "busy", Action: native.Attach, WorkerPID: worker.Process.Pid, StartedAt: time.Now(), Handle: "job-fixture"}
	lookup := nativeLookupFunc(func(ctx context.Context, r native.Ref) (native.Session, error) {
		if err := ctx.Err(); err != nil {
			return native.Session{}, err
		}
		if r != session.Ref {
			return native.Session{}, fmt.Errorf("wrong ref")
		}
		return session, nil
	})
	return m, session, lookup, worker.Process
}
func assertWorkerAlive(t *testing.T, p *os.Process) {
	t.Helper()
	if err := p.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("native worker was stopped: %v", err)
	}
}
func TestImportCreatesAViewAndLastDetachKeepsNativeWorker(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	identity, _ := procIdentity(worker.Pid)
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	if result.Terminal.PID == worker.Pid || result.Terminal.Source != session.Ref || result.Reused {
		t.Fatalf("wrong frontend identity: %+v", result)
	}
	a, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	a.Close()
	if _, err = m.Get(result.Terminal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("temporary view retained: %v", err)
	}
	assertWorkerAlive(t, worker)
	after, _ := procIdentity(worker.Pid)
	if after != identity {
		t.Fatal("worker generation changed")
	}
}
func TestImportCannotReuseLaunchProvenanceAsCurrentConversation(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	first, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	// The CLI may have navigated to B. No API asserts current identity for this
	// imported frontend, so a request for A must never receive that window.
	again, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if !errors.Is(err, ErrImportDeclined) || again.Terminal.ID != "" || !strings.Contains(err.Error(), string(first.Terminal.ID)) {
		t.Fatalf("unverified reuse: %+v %v", again, err)
	}
	if len(m.List()) != 1 || m.totalCreated.Load() != 1 {
		t.Fatal("retry created duplicate frontends")
	}
	listing := native.Listing{Sessions: []native.Session{session}}
	view := BuildDiscoveredOverview(m.List(), listing, OverviewOptions{})
	if len(view.Sessions) != 2 || view.Counts.External != 1 {
		t.Fatalf("launch provenance hid native A: %+v", view)
	}
	foundNative, foundWindow := false, false
	for _, row := range view.Sessions {
		foundNative = foundNative || (row.External != nil && row.External.Ref == session.Ref)
		foundWindow = foundWindow || (row.ID == first.Terminal.ID && row.Source == session.Ref)
	}
	if !foundNative || !foundWindow {
		t.Fatal("native authority and its independent window are not both discoverable")
	}
	assertWorkerAlive(t, worker)
}
func TestConcurrentImportsDoNotCreateDuplicateFrontends(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	var created, declined atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
			switch {
			case err == nil && !result.Reused:
				created.Add(1)
			case errors.Is(err, ErrImportDeclined):
				declined.Add(1)
			default:
				t.Errorf("%+v %v", result, err)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 || declined.Load() != 15 || m.totalCreated.Load() != 1 {
		t.Fatalf("created=%d declined=%d total=%d", created.Load(), declined.Load(), m.totalCreated.Load())
	}
	assertWorkerAlive(t, worker)
}
func TestImportDeclinesChangedWorkerAndCleansOnlyItsFrontend(t *testing.T) {
	m, session, _, worker := importFixture(t)
	var calls int
	lookup := nativeLookupFunc(func(context.Context, native.Ref) (native.Session, error) {
		calls++
		s := session
		if calls > 1 {
			s.CWD = "/different-project"
		}
		return s, nil
	})
	_, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if !errors.Is(err, ErrImportDeclined) || len(m.List()) != 0 {
		t.Fatalf("changed worker accepted or leaked view: %v %+v", err, m.List())
	}
	assertWorkerAlive(t, worker)
}
func TestNativeSourceSurvivesRestartButCannotBypassFreshImport(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	first, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Restart(first.Terminal.ID); err == nil {
		t.Fatal("generic restart bypassed native validation")
	}
	m.CloseAll()
	assertWorkerAlive(t, worker)
	restored := NewManagerWithOptions(m.opts)
	t.Cleanup(restored.CloseAll)
	restored.RestoreGhosts()
	rows := restored.List()
	if len(rows) != 1 || rows[0].Source != session.Ref || rows[0].Status == StatusRunning {
		t.Fatalf("bad ghost state: %+v", rows)
	}
	if _, err = restored.Restore(first.Terminal.ID, RestoreRerun); err == nil {
		t.Fatal("generic restore bypassed native validation")
	}
	next, err := restored.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	if next.Terminal.ID == first.Terminal.ID || len(restored.List()) != 1 {
		t.Fatal("stopped view was reused or left duplicated")
	}
	assertWorkerAlive(t, worker)
}
func TestImportRequiresExplicitResumeAndMatchingIdentity(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	for _, action := range []native.Action{native.Background, native.Unavailable, native.Resume} {
		s := session
		s.Action = action
		s.Reason = "finish handoff"
		_, err := m.ImportAgent(context.Background(), nativeLookupFunc(func(context.Context, native.Ref) (native.Session, error) { return s, nil }), ImportRequest{Ref: session.Ref})
		if !errors.Is(err, ErrImportDeclined) || m.totalCreated.Load() != 0 {
			t.Fatalf("authorized %s: %v", action, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ImportAgent(ctx, lookup, ImportRequest{Ref: session.Ref}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(m.List()) != 0 {
		t.Fatal("cancelled import created frontend")
	}
	assertWorkerAlive(t, worker)
}

func TestImportResumeWaitsForNativeWorkerConfirmation(t *testing.T) {
	m, session, _, worker := importFixture(t)
	var calls int
	lookup := nativeLookupFunc(func(context.Context, native.Ref) (native.Session, error) {
		calls++
		s := session
		if calls < 3 {
			s.Action = native.Resume
			s.WorkerPID = 0
		}
		return s, nil
	})
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref, Resume: true})
	if err != nil || result.Session.Action != native.Attach || calls < 3 {
		t.Fatalf("unconfirmed resume: %+v calls=%d %v", result, calls, err)
	}
	assertWorkerAlive(t, worker)
}

// Two clients can share a view. Releasing one must not remove the other's
// frontend, and its native worker must outlive both.
func TestNativeViewLivesUntilLastAttachment(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	info, err := m.Get(result.Terminal.ID)
	if err != nil || info.Status != StatusRunning || info.Attached != 1 {
		t.Fatalf("other client lost its view: %+v %v", info, err)
	}
	if err = second.Write([]byte("still attached\r")); err != nil {
		t.Fatal(err)
	}
	second.Close()
	if _, err = m.Get(result.Terminal.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("last detach retained view: %v", err)
	}
	assertWorkerAlive(t, worker)
}

func TestImportReusesCurrentManagedHookWithoutChangingItsLifetime(t *testing.T) {
	bin, home, env := fakeAgentEnv(t)
	dir := filepath.Join(t.TempDir(), "state")
	m := NewManagerWithOptions(Options{PersistDir: dir})
	t.Cleanup(m.CloseAll)
	info, err := m.Create(CreateConfig{Name: "managed", Command: filepath.Join(bin, "claude"), CWD: t.TempDir(), Env: env, Restore: RestoreAgent})
	if err != nil {
		t.Fatal(err)
	}
	id := waitAgentSession(t, dir, info.ID, func(s string) bool { return s != "" })
	session := native.Session{Ref: native.Ref{Provider: native.Claude, ID: id}, CWD: info.CWD, Action: native.Background, WorkerPID: info.PID}
	lookup := nativeLookupFunc(func(context.Context, native.Ref) (native.Session, error) { return session, nil })
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil || !result.Reused || result.Terminal.ID != info.ID || result.Terminal.Source.Provider != "" {
		t.Fatalf("managed session not reused: %+v %v", result, err)
	}
	a, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	current, err := m.Get(info.ID)
	if err != nil || current.Status != StatusRunning || current.AgentSession != id {
		t.Fatalf("managed lifetime changed: %+v %v", current, err)
	}
	if launches := fakeInvocations(home); len(launches) != 1 {
		t.Fatalf("import relaunched a managed worker: %v", launches)
	}
	view := BuildDiscoveredOverview(m.List(), native.Listing{Sessions: []native.Session{session}}, OverviewOptions{})
	if view.Counts.Total != 1 || view.Counts.External != 0 || view.Sessions[0].ID != info.ID {
		t.Fatalf("managed hook not deduplicated: %+v", view)
	}
}

func TestNativeFrontendExitStatusSurvivesOutputEOFAndLastDetach(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	script := "#!/bin/sh\nprintf 'ready\\n'\nread line\nexit 7\n"
	if err := os.WriteFile(filepath.Join(session.CWD, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, err = a.Read(ctx)
		if err != nil {
			break
		}
	}
	if !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	a.Close()
	for {
		info, err := m.Get(result.Terminal.ID)
		if err != nil {
			t.Fatalf("last detach lost exit status: %v", err)
		}
		if info.ExitCode != nil {
			if *info.ExitCode != 7 {
				t.Fatalf("wrong exit status: %+v", info)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("exit status not reported")
		case <-time.After(10 * time.Millisecond):
		}
	}
	saved, err := m.Attach(result.Terminal.ID, 80, 24, true)
	if err != nil {
		t.Fatalf("ended native screen could not be viewed: %v", err)
	}
	defer saved.Close()
	if !saved.ReadOnly() || saved.t != nil {
		t.Fatal("saved screen claimed the native frontend")
	}
	if raw, err := saved.Read(ctx); err != nil || !strings.Contains(string(raw), "ready") {
		t.Fatalf("missing ended frontend screen: %q %v", raw, err)
	}
	if _, err := saved.Read(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("ended frontend screen did not end: %v", err)
	}
	assertWorkerAlive(t, worker)
}

func TestImportedFrontendUsesViewingTerminalColors(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	t.Setenv("TERM", "dumb")
	t.Setenv("NO_COLOR", "1")
	script := "#!/bin/sh\nif [ \"$TERM\" != xterm-256color ] || [ \"${NO_COLOR+x}\" = x ]; then exit 3; fi\nprintf '\\033[38;2;120;60;200mVIEW_COLOR_OK\\033[0m\\n'\nexec cat\n"
	if err := os.WriteFile(filepath.Join(session.CWD, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	term, color := "xterm-256color", "truecolor"
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref, TerminalEnv: TerminalEnvironment{"TERM": &term, "COLORTERM": &color, "NO_COLOR": nil}})
	if err != nil {
		t.Fatal(err)
	}
	waitScreen(t, m, result.Terminal.ID, "VIEW_COLOR_OK")
	a, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	raw, err := a.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "38;2;120;60;200") {
		t.Fatalf("view lost truecolor attributes: %q", raw)
	}
	assertWorkerAlive(t, worker)
}
func TestImportDoesNotAcceptArbitraryEnvironmentOverrides(t *testing.T) {
	m, session, lookup, _ := importFixture(t)
	value := "injected"
	_, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref, TerminalEnv: TerminalEnvironment{"LD_PRELOAD": &value}})
	if !errors.Is(err, ErrImportDeclined) || len(m.List()) != 0 {
		t.Fatalf("accepted non-display override: %v", err)
	}
}
