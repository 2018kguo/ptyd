package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
)

func TestGroupPersistenceMoveAndRestore(t *testing.T) {
	dir := t.TempDir()
	m := NewManagerWithOptions(Options{PersistDir: dir})
	t.Cleanup(m.CloseAll)
	a, err := m.CreateGroup(GroupConfig{Name: "checkout", CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateGroup(GroupConfig{Name: "research", CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	m.SetAddress("127.0.0.1:4243")
	term, err := m.Create(CreateConfig{GroupID: a.ID, Name: "worker", Command: "sh", Args: []string{"-c", "printf '%s/%s/%s\\n' \"$PTYD_GROUP_ID\" \"$PTYD_TERMINAL_ID\" \"$PTYD_ADDR\"; exec cat"}, Restore: RestoreRerun})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err = m.Wait(ctx, term.ID, string(a.ID)+"/"+string(term.ID)+"/127.0.0.1:4243", -1, 0); err != nil {
		t.Fatal(err)
	}
	name := "review-worker"
	updated, err := m.UpdateTerminal(term.ID, TerminalUpdate{Name: &name, GroupID: &b.ID})
	if err != nil || updated.PID != term.PID || updated.GroupID != b.ID {
		t.Fatalf("move replaced process: %+v %v", updated, err)
	}
	if got := m.Overview(OverviewOptions{GroupID: a.ID}); got.Counts.Total != 0 {
		t.Fatal("old group still includes moved terminal")
	}
	renamed := "long-lived research"
	if _, err = m.UpdateGroup(b.ID, GroupUpdate{Name: &renamed}); err != nil {
		t.Fatal(err)
	}
	m.CloseAll()
	restored := NewManagerWithOptions(Options{PersistDir: dir})
	t.Cleanup(restored.CloseAll)
	g, err := restored.Group(b.ID)
	if err != nil || g.Name != renamed {
		t.Fatalf("group metadata lost: %+v %v", g, err)
	}
	info, err := restored.Get(term.ID)
	if err != nil || info.GroupID != b.ID || info.Name != name {
		t.Fatalf("membership lost: %+v %v", info, err)
	}
	info, err = restored.Restore(term.ID, RestoreRerun)
	if err != nil || info.GroupID != b.ID {
		t.Fatalf("restore lost group: %+v %v", info, err)
	}
}
func TestArchivedGroupCannotDestroyOrRestoreMembers(t *testing.T) {
	dir := t.TempDir()
	m := NewManagerWithOptions(Options{PersistDir: dir})
	t.Cleanup(m.CloseAll)
	g, err := m.CreateGroup(GroupConfig{Name: "finished", CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Create(CreateConfig{GroupID: g.ID, Name: "result", Command: "sh", Args: []string{"-c", "printf FINISHED"}})
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(time.Millisecond) {
		i, _ := m.Get(info.ID)
		if i.Status != StatusRunning {
			break
		}
		if time.Now().After(end) {
			t.Fatal("fixture did not exit")
		}
	}
	archived := true
	if _, err = m.UpdateGroup(g.ID, GroupUpdate{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Restart(info.ID); !errors.Is(err, ErrGroupConflict) {
		t.Fatalf("restart admitted archived group: %v", err)
	}
	if _, err = m.Get(info.ID); err != nil {
		t.Fatal("failed restart deleted saved terminal")
	}
	m.CloseAll()
	again := NewManagerWithOptions(Options{PersistDir: dir})
	t.Cleanup(again.CloseAll)
	if _, err = again.Restore(info.ID, RestoreRerun); !errors.Is(err, ErrGroupConflict) {
		t.Fatalf("restore admitted archived group: %v", err)
	}
	if _, err = again.Get(info.ID); err != nil {
		t.Fatal("failed restore deleted ghost")
	}
}
func TestGroupDeleteAndCreateCannotLeaveDanglingMembership(t *testing.T) {
	for range 12 {
		m := NewManager()
		g, err := m.CreateGroup(GroupConfig{Name: "race", CWD: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = m.Create(CreateConfig{GroupID: g.ID, Name: "worker", Command: "cat"}) }()
		go func() { defer wg.Done(); _ = m.RemoveGroup(g.ID, true) }()
		wg.Wait()
		for _, term := range m.List() {
			if _, err := m.Group(term.GroupID); err != nil {
				t.Fatalf("terminal outlived its group: %+v", term)
			}
		}
		m.CloseAll()
	}
}
func TestLegacyMetadataEntersInboxWithoutRestarting(t *testing.T) {
	dir := t.TempDir()
	meta := persistedMeta{ID: "term-8-legacy", Name: "old", Command: "sh", Cols: 80, Rows: 24, CreatedAt: time.Now()}
	if err := writeMeta(dir, meta); err != nil {
		t.Fatal(err)
	}
	m := NewManagerWithOptions(Options{PersistDir: dir})
	defer m.CloseAll()
	info, err := m.Get(meta.ID)
	if err != nil || info.GroupID != DefaultGroup || info.Status != StatusExited {
		t.Fatalf("legacy migration changed lifecycle: %+v %v", info, err)
	}
	if _, err = m.CreateGroup(GroupConfig{Name: "Inbox", CWD: dir}); !errors.Is(err, ErrGroupConflict) {
		t.Fatalf("duplicate group accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "groups.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := NewManagerWithOptions(Options{PersistDir: dir})
	if _, err := corrupt.Groups(); err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("corruption hid group state: %v", err)
	}
}
func TestHumanControlTransactionsAndObservers(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()
	info, err := m.Create(CreateConfig{Name: "input", Command: "cat", Cols: 20, Rows: 5})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := m.Attach(info.ID, AttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	first, err := m.Attach(info.ID, AttachOptions{Human: true, Rendered: true, Cols: 20, Rows: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Attach(info.ID, AttachOptions{Human: true, Cols: 99, Rows: 30}); !errors.Is(err, ErrHumanControl) {
		t.Fatal(err)
	}
	screen, _ := m.Screen(info.ID, false)
	if screen.Cols != 20 || screen.Rows != 5 {
		t.Fatal("rejected human resized active terminal")
	}
	if err = m.Submit(info.ID, "agent must wait"); !errors.Is(err, ErrHumanControl) {
		t.Fatalf("agent wrote during takeover: %v", err)
	}
	if err = raw.Write([]byte("raw agent")); !errors.Is(err, ErrHumanControl) {
		t.Fatalf("raw automation bypassed ownership: %v", err)
	}
	observer, err := m.Attach(info.ID, AttachOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	observer.Close()
	first.Close()
	second, err := m.Attach(info.ID, AttachOptions{Human: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Write([]byte("stale owner")); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("closed owner wrote again: %v", err)
	}
	second.Close()
	if err = m.Write(info.ID, "agent continues\r"); err != nil {
		t.Fatal(err)
	}
}

func TestNamedGroupRetainsNativeFrontendAndCancelsInboxExpiry(t *testing.T) {
	m, session, lookup, worker := importFixture(t)
	result, err := m.ImportAgent(context.Background(), lookup, ImportRequest{Ref: session.Ref})
	if err != nil {
		t.Fatal(err)
	}
	g, err := m.CreateGroup(GroupConfig{Name: "native work", CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.UpdateTerminal(result.Terminal.ID, TerminalUpdate{GroupID: &g.ID}); err != nil {
		t.Fatal(err)
	}
	// Execute a queued old expiry synchronously, after the move won the lock.
	m.closeUnusedNativeView(result.Terminal.ID)
	a, err := m.Attach(result.Terminal.ID, AttachOptions{Human: true, Rendered: true})
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	info, err := m.Get(result.Terminal.ID)
	if err != nil || info.Status != StatusRunning {
		t.Fatalf("detaching discarded group work: %+v %v", info, err)
	}
	assertWorkerAlive(t, worker)
	if _, err = m.UpdateTerminal(info.ID, TerminalUpdate{GroupID: func() *GroupID { id := DefaultGroup; return &id }()}); err != nil {
		t.Fatal(err)
	}
	a, err = m.Attach(info.ID, AttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	if _, err = m.Get(info.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Inbox import lost temporary lifetime: %v", err)
	}
	assertWorkerAlive(t, worker)
}

func TestFramedHumanKeepsDaemonRepliesWithRawAutomationObserver(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()
	info, err := m.Create(CreateConfig{Name: "replies", Command: "cat"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := m.Attach(info.ID, AttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	human, err := m.Attach(info.ID, AttachOptions{Human: true, Rendered: true})
	if err != nil {
		t.Fatal(err)
	}
	defer human.Close()
	live, _ := m.get(info.ID)
	live.consume([]byte("\x1b[6n"))
	live.scrMu.Lock()
	hasReplies := live.replies != nil
	live.scrMu.Unlock()
	if !hasReplies {
		t.Fatal("blocked raw observer suppressed daemon terminal replies")
	}
}

func TestForceGroupRemovalReportsPersistenceFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission fault requires nonroot user")
	}
	dir := t.TempDir()
	m := NewManagerWithOptions(Options{PersistDir: dir})
	g, err := m.CreateGroup(GroupConfig{Name: "saved", CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	id := ID("term-1-saved")
	if err = writeMeta(dir, persistedMeta{ID: id, GroupID: g.ID, Name: "saved", Command: "sh", Cols: 80, Rows: 24, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	m = NewManagerWithOptions(Options{PersistDir: dir})
	member := terminalDir(dir, id)
	if err = os.Chmod(member, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(member, 0700)
	for range 2 {
		if err = m.RemoveGroup(g.ID, true); err == nil {
			t.Fatal("deletion reported success despite retained metadata")
		}
		if _, err = m.Get(id); err != nil {
			t.Fatal("failed deletion forgot member, making retries unsafe")
		}
	}
	reloaded := NewManagerWithOptions(Options{PersistDir: dir})
	if _, err = reloaded.Group(g.ID); err != nil {
		t.Fatal("failed deletion discarded group metadata")
	}
}

func TestGroupMetadataRejectsDuplicateDefaultIDAndReservedName(t *testing.T) {
	for _, groups := range [][]Group{
		{{ID: DefaultGroup, Name: "Inbox"}, {ID: DefaultGroup, Name: "Other"}},
		{{ID: "group-other", Name: "Inbox"}},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "groups.json")
		if err := writeJSONAtomic(path, groupFile{Format: 1, Groups: groups}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		m := NewManagerWithOptions(Options{PersistDir: dir})
		if _, err = m.Groups(); err == nil {
			t.Fatal("invalid group metadata accepted")
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatal("invalid group metadata was overwritten")
		}
	}
}

func TestHumanAdmissionDoesNotWaitForBlockedAgentWrite(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	m := NewManager()
	id := ID("blocked-input")
	live := &terminal{mgr: m, info: Info{ID: id, Name: "blocked", Status: StatusRunning}, ptmx: writer, screen: phosphor.New(20, 5)}
	m.terms[id] = live
	done := make(chan error, 1)
	go func() { done <- m.Write(id, strings.Repeat("x", 1<<20)) }()
	defer func() { reader.Close(); writer.Close(); <-done }()
	var first [1]byte
	if _, err = reader.Read(first[:]); err != nil {
		t.Fatal(err)
	}
	admitted := make(chan error, 1)
	go func() { _, err := m.Attach(id, AttachOptions{Human: true}); admitted <- err }()
	select {
	case err := <-admitted:
		if !errors.Is(err, ErrInputBusy) {
			t.Fatalf("blocked input admission: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("human admission hung behind blocked input")
	}
	observer, err := m.Attach(id, AttachOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	observer.Close()
}

func TestSubmitEndedTerminalKeepsConflictContract(t *testing.T) {
	m := NewManager()
	defer m.CloseAll()
	info, err := m.Create(CreateConfig{Name: "ended", Command: "sh", Args: []string{"-c", "exit 0"}})
	if err != nil {
		t.Fatal(err)
	}
	live, _ := m.get(info.ID)
	select {
	case <-live.done:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not exit")
	}
	if err = m.Submit(info.ID, "must not send"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("lost stopped-terminal conflict: %v", err)
	}
}
