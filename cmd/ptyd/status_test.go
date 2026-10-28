package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestStatusSelectionAndStaleRefresh(t *testing.T) {
	v := statusView{}
	v.refresh(statusResult{err: errors.New("daemon unavailable")})
	if v.snapshot != nil || strings.Contains(statusFrame(v, 100, 20), "No matching sessions") {
		t.Fatal("failed read claimed an empty fleet")
	}
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "a"}, {ID: "b"}}}})
	v.move(1)
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "b"}, {ID: "a"}}}})
	if v.selected != "b" {
		t.Fatal("refresh attached selection to row index")
	}
	v.refresh(statusResult{err: errors.New("temporary failure")})
	if v.selected != "b" || len(v.sessions()) != 2 || !strings.Contains(statusFrame(v, 100, 20), "last received list") {
		t.Fatal("failed refresh discarded the snapshot")
	}
	v.query = "filter"
	v.refresh(statusResult{query: "old", view: terminal.Overview{}})
	if len(v.sessions()) != 2 {
		t.Fatal("old filter result replaced current data")
	}
	v.refresh(statusResult{query: "filter", view: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "a"}}}})
	if v.selected != "a" {
		t.Fatal("deleted selection did not move to a live id")
	}
}

func TestStatusEscapeChunkingAndControls(t *testing.T) {
	for _, tc := range []struct {
		key  string
		move int
	}{
		{"\x1b[A", -1}, {"\x1b[B", 1}, {"\x1bOA", -1}, {"\x1b[1;5B", 1}, {"\x1b[3~", 0},
	} {
		for i := 1; i < len(tc.key); i++ {
			if n, _ := statusEscape([]byte(tc.key[:i])); n != 0 {
				t.Fatalf("partial %q consumed %d", tc.key[:i], n)
			}
		}
		if n, move := statusEscape([]byte(tc.key)); n != len(tc.key) || move != tc.move {
			t.Fatalf("%q: %d %d", tc.key, n, move)
		}
	}
	if n, _ := statusEscape([]byte("\x1bq")); n != 1 {
		t.Fatal("Escape swallowed following quit key")
	}
	view := terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "t", Name: "bad\x1b]52;c;secret\a\nname", CWD: "a\tb", State: terminal.OverviewRunning,
		Preview: &terminal.OverviewPreview{Source: "pending", Text: "\x1b[31mhello\x1b[0m"}}}}
	var plain bytes.Buffer
	printStatus(&plain, view)
	if strings.ContainsAny(plain.String(), "\x1b\a") || strings.Contains(plain.String(), "a\tb") {
		t.Fatalf("terminal controls leaked: %q", plain.String())
	}
}

func TestCLIStatusOverview(t *testing.T) {
	d := startDaemon(t, freeAddr(t), "--persist-dir=")
	var watched terminal.Info
	if code := d.do("POST", "/terminals", terminal.CreateConfig{Name: "watch-build", Command: "sh", Args: []string{"-c", "echo BUILD-FAILED; exec cat"},
		Tags: []string{"build"}, Watches: []terminal.WatchSpec{{Name: "failed", Pattern: "^BUILD-FAILED$", Once: true}}}, &watched); code != 201 {
		t.Fatal(code)
	}
	if _, stderr, code := d.cli(nil, "new", "-d", "-n", "quiet", "--", "cat"); code != 0 {
		t.Fatal(stderr)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		out, stderr, code := d.cli(nil, "status", "--json", "--limit", "1")
		if code != 0 {
			t.Fatalf("status: %d %s", code, stderr)
		}
		var view terminal.Overview
		if err := json.Unmarshal([]byte(out), &view); err != nil {
			t.Fatal(err)
		}
		if view.Counts.Attention == 1 {
			if view.Counts.Total != 2 || !view.Truncated || len(view.Sessions) != 1 || view.Sessions[0].ID != watched.ID || view.Sessions[0].Preview.Source != "watch" {
				t.Fatalf("view: %+v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watch never surfaced: %s", out)
		}
	}
	out, stderr, code := d.cli(nil, "status", "--json", "--tag", "build", "--filter", "watch")
	var view terminal.Overview
	if code != 0 || json.Unmarshal([]byte(out), &view) != nil || view.Counts.Total != 1 || view.Sessions[0].ID != watched.ID {
		t.Fatalf("filtered: %s %s", out, stderr)
	}
	if out, _, code := d.cli([]string{"PTYD_TOKEN=wrong"}, "status", "--json"); code == 0 || out != "" {
		t.Fatal("failure printed a false empty snapshot")
	}
	for _, args := range [][]string{{"status", "--watch", "--json"}, {"status", "--limit", "0"}, {"status", "extra"}} {
		if _, _, code := d.cli(nil, args...); code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
	if _, stderr, code := d.cli(nil, "status", "--watch"); code != 1 || !strings.Contains(stderr, "interactive terminal") {
		t.Fatalf("nonterminal watch: %d %s", code, stderr)
	}
}

func TestCLIStatusWatchAttachReturn(t *testing.T) {
	d := startDaemon(t, freeAddr(t), "--persist-dir=")
	id, stderr, code := d.cli(nil, "new", "-d", "-n", "watch-target", "--", "cat")
	id = strings.TrimSpace(id)
	if code != 0 {
		t.Fatal(stderr)
	}
	w := d.open(110, 18, "status", "--watch")
	w.waitText("watch-target")
	for i := 0; i < 2; i++ {
		marker := strings.Repeat("live-", i+1)
		// Enter and subsequent program input can arrive in one PTY read.
		w.keys("\r" + marker + "\r")
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			var info terminal.Info
			d.do("GET", "/terminals/"+id, nil, &info)
			if info.Attached > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("overview did not attach selected id")
			}
		}
		d.waitScreen(id, func(s screen) bool { return strings.Contains(strings.Join(s.Lines, "\n"), marker) })
		if i == 1 {
			// The next overview key can share a read with both detach keys.
			w.keys("jkq")
			break
		}
		w.keys("jk")
		w.waitText("ptyd  /  Sessions")
		w.waitText("watch-target")
	}
	if code := w.waitExit(); code != 0 {
		t.Fatalf("watch exit %d", code)
	}
}

func TestStatusScalarLayoutAfterAttachment(t *testing.T) {
	for _, text := range []string{"👩‍💻", "☔︎", "e\u0301", "项目"} {
		s := phosphor.New(80, 2)
		s.Write([]byte("\x1b[?2027h" + statusEnter + padStatus(text, 20)))
		if s.Modes().GraphemeClustering || s.Cursor().X != 20 {
			t.Fatalf("%q: mode=%v x=%d", text, s.Modes().GraphemeClustering, s.Cursor().X)
		}
	}
	if overviewIdle(terminal.SessionSummary{State: terminal.OverviewRunning}) != "-" {
		t.Fatal("unobserved output claimed recent activity")
	}
}

func TestCLIStatusWatchDoesNotWaitForRefresh(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	d := &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}
	w := d.open(100, 16, "status", "--watch")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not start")
	}
	w.keys("q")
	select {
	case <-w.exited:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("daemon refresh blocked the quit key")
	}
	if code := w.waitExit(); code != 0 {
		t.Fatalf("exit: %d", code)
	}
}

func TestStatusUnsupportedDaemonShowsRecovery(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	c := client.API{Addr: strings.TrimPrefix(server.URL, "http://")}
	_, err := c.Overview(context.Background(), terminal.OverviewOptions{})
	frame := statusFrame(statusView{err: err}, 80, 24)
	if !strings.Contains(frame, "Restart ptyd after upgrading") || !strings.Contains(frame, "PTYD_ADDR") {
		t.Fatalf("recovery was clipped: %q", frame)
	}
}

func TestStatusAttachPreservesCancellation(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
		http.NotFound(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // The overview was cancelled before attachment started.
	c := client.API{Addr: strings.TrimPrefix(server.URL, "http://")}
	attach(ctx, c, terminal.Info{ID: "term-test"}, client.Options{})
	select {
	case <-requested:
		t.Fatal("attachment discarded overview cancellation")
	default:
	}
}

const watchNativeID = "11111111-1111-4111-8111-111111111111"

func TestStatusNativeImportCanQuitDuringBlockedRequest(t *testing.T) {
	stopServer := make(chan struct{})
	started := make(chan struct{}, 1)
	session := native.Session{Ref: native.Ref{Provider: native.Claude, ID: watchNativeID}, Name: "native-fixture", Status: "busy", Action: native.Attach}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/overview" {
			json.NewEncoder(w).Encode(terminal.Overview{ActiveOnly: r.URL.Query().Get("active") == "true", Sessions: []terminal.SessionSummary{{ID: "native:claude:" + watchNativeID, Name: session.Name, External: &session, State: terminal.OverviewExternal}}})
			return
		}
		if r.URL.Path == "/agents/import" {
			_, _ = io.Copy(io.Discard, r.Body)
			started <- struct{}{}
			select {
			case <-r.Context().Done():
			case <-stopServer:
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	defer close(stopServer)
	d := &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}
	w := d.open(110, 18, "status", "--watch", "--discover")
	w.waitText("native-fixture")
	w.keys("\r")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("import never started")
	}
	w.keys("q")
	select {
	case <-w.exited:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("native import blocked quit")
	}
	if code := w.waitExit(); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

type watchNativeFixture struct {
	session native.Session
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (f *watchNativeFixture) List(context.Context) native.Listing {
	return native.Listing{Sessions: []native.Session{f.session}}
}
func (f *watchNativeFixture) Lookup(ctx context.Context, _ native.Ref) (native.Session, error) {
	if f.calls.Add(1) == 1 {
		close(f.started)
		select {
		case <-ctx.Done():
			return native.Session{}, ctx.Err()
		case <-f.release:
		}
	}
	return f.session, nil
}
func TestStatusNativeImportDiscardsPreHandoffKeysAndReleasesView(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nstty -echo\nprintf 'VIEW_READY\\n'\nexec cat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	worker := exec.Command("sleep", "60")
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { worker.Process.Kill(); worker.Wait() }()
	fixture := &watchNativeFixture{session: native.Session{Ref: native.Ref{Provider: native.Claude, ID: watchNativeID}, Name: "native-fixture", CWD: dir, Status: "busy", Action: native.Attach, WorkerPID: worker.Process.Pid, Handle: "job-fixture"}, started: make(chan struct{}), release: make(chan struct{})}
	mgr := terminal.NewManager()
	defer mgr.CloseAll()
	server := httptest.NewServer(api.New(mgr, api.Config{Native: fixture}).Handler())
	defer server.Close()
	defer func() {
		select {
		case <-fixture.release:
		default:
			close(fixture.release)
		}
	}()
	d := &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}
	w := d.open(110, 20, "status", "--watch", "--discover")
	w.waitText("native-fixture")
	// Typeahead delivered with Enter must not become native agent input.
	w.keys("\rSTALE_INPUT\r")
	select {
	case <-fixture.started:
	case <-time.After(5 * time.Second):
		t.Fatal("import never started")
	}
	w.keys("MORE_STALE_INPUT\r")
	close(fixture.release)
	w.waitText("VIEW_READY")
	infos := mgr.List()
	if len(infos) != 1 {
		t.Fatalf("views: %+v", infos)
	}
	snapshot, err := mgr.Output(infos[0].ID, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	text := snapshot.Pending
	for _, line := range snapshot.Lines {
		text += line.Text
	}
	if strings.Contains(text, "STALE_INPUT") {
		t.Fatalf("picker keys reached the native view: %q", text)
	}
	w.keys("fresh-input\r")
	w.waitText("fresh-input")
	w.keys("jkq")
	if code := w.waitExit(); code != 0 {
		t.Fatalf("watch exit %d", code)
	}
	if len(mgr.List()) != 0 {
		t.Fatal("detached native frontend was retained")
	}
	if err = worker.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("native worker stopped: %v", err)
	}
}

func nativeCLIFixture(t *testing.T, script string) (*daemon, *terminal.Manager, native.Ref) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fixture := &watchNativeFixture{session: native.Session{Ref: native.Ref{Provider: native.Claude, ID: watchNativeID}, Name: "native-fixture", CWD: dir, Status: "busy", Action: native.Attach, WorkerPID: os.Getpid(), Handle: "job-fixture"}, started: make(chan struct{}), release: make(chan struct{})}
	close(fixture.release)
	mgr := terminal.NewManager()
	t.Cleanup(mgr.CloseAll)
	server := httptest.NewServer(api.New(mgr, api.Config{Native: fixture}).Handler())
	t.Cleanup(server.Close)
	return &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}, mgr, fixture.session.Ref
}

func TestNativeCLIDetachKeepsOtherViewers(t *testing.T) {
	for _, mode := range []string{"import", "picker"} {
		t.Run(mode, func(t *testing.T) {
			d, mgr, ref := nativeCLIFixture(t, "#!/bin/sh\nstty -echo\nprintf 'VIEW_READY\\n'\nexec cat\n")
			var first *window
			if mode == "picker" {
				first = d.open(110, 20, "status", "--watch", "--discover")
				first.waitText("native-fixture")
				first.keys("\r")
			} else {
				first = d.open(110, 20, "import", ref.Key())
			}
			first.waitText("VIEW_READY")
			rows := mgr.List()
			if len(rows) != 1 {
				t.Fatalf("views: %+v", rows)
			}
			id := rows[0].ID
			second := d.open(110, 20, "attach", string(id))
			second.waitText("VIEW_READY")
			first.keys("jk")
			if mode == "picker" {
				first.waitText("native-fixture")
				first.keys("q")
			}
			if code := first.waitExit(); code != 0 {
				t.Fatalf("first viewer exit: %d", code)
			}
			info, err := mgr.Get(id)
			if err != nil || info.Status != terminal.StatusRunning {
				t.Fatalf("first viewer deleted shared frontend: %+v %v", info, err)
			}
			second.keys("second-client-live\r")
			second.waitText("second-client-live")
			second.keys("jk")
			if code := second.waitExit(); code != 0 {
				t.Fatalf("second viewer exit: %d", code)
			}
			for deadline := time.Now().Add(3 * time.Second); len(mgr.List()) != 0; time.Sleep(10 * time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("last viewer retained running frontend")
				}
			}
		})
	}
}

func TestNativeCLIReportsFrontendExitCode(t *testing.T) {
	d, _, ref := nativeCLIFixture(t, "#!/bin/sh\nprintf 'VIEW_READY\\n'\nread line\nexit 7\n")
	w := d.open(110, 20, "import", ref.Key())
	w.waitText("VIEW_READY")
	w.keys("finish\r")
	if code := w.waitExit(); code != 7 {
		t.Fatalf("frontend exit code %d, window: %q", code, w.lines())
	}
	w.waitText("[terminal exited (code 7)]")
}

type delayedNativeFixture struct{ session native.Session }

func (f delayedNativeFixture) List(ctx context.Context) native.Listing {
	select {
	case <-time.After(2300 * time.Millisecond):
		return native.Listing{Sessions: []native.Session{f.session}, Issues: []native.Issue{{Provider: native.Claude, Message: "Claude query timed out"}}}
	case <-ctx.Done():
		return native.Listing{Issues: []native.Issue{{Provider: native.Claude, Message: ctx.Err().Error()}}}
	}
}
func (f delayedNativeFixture) Lookup(context.Context, native.Ref) (native.Session, error) {
	return native.Session{}, errors.New("not used")
}
func TestStatusDiscoveryAllowsBoundedSlowProvider(t *testing.T) {
	mgr := terminal.NewManager()
	t.Cleanup(mgr.CloseAll)
	if _, err := mgr.Create(terminal.CreateConfig{Name: "managed-visible", Command: "cat", CWD: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	fixture := delayedNativeFixture{session: native.Session{Ref: native.Ref{Provider: native.Codex, ID: watchNativeID}, Name: "healthy-codex", CWD: "/tmp", Status: "active", Action: native.Attach}}
	server := httptest.NewServer(api.New(mgr, api.Config{Native: fixture}).Handler())
	t.Cleanup(server.Close)
	d := &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}
	w := d.open(110, 20, "status", "--watch", "--discover")
	w.waitText("managed-visible")
	w.waitText("healthy-codex")
	w.waitText("Some agents could not be discovered")
	w.keys("?")
	w.waitText("Claude query timed out")
	w.keys("q")
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatalf("picker exit %d", code)
	}
}

func TestVimPickerKeysAndCommands(t *testing.T) {
	v := statusView{}
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "term-1-aaaa", Name: "one"}, {ID: "term-2-bbbb", Name: "two"}, {ID: "term-3-cccc", Name: "two"}}}})
	keys := func(s string) statusAction {
		var action statusAction
		for _, r := range s {
			action = v.key(r)
		}
		return action
	}
	keys("G")
	if v.selected != "term-3-cccc" {
		t.Fatal("G did not reach last")
	}
	keys("gg")
	if v.selected != "term-1-aaaa" {
		t.Fatal("gg did not reach first")
	}
	keys("j")
	if v.selected != "term-2-bbbb" {
		t.Fatal("j did not move")
	}
	keys("k")
	if v.selected != "term-1-aaaa" {
		t.Fatal("k did not move")
	}
	if keys(":b 2\r") != statusAttach || v.selected != "term-2-bbbb" {
		t.Fatal("numeric :b did not resolve exact terminal")
	}
	if keys(":b two\r") != statusStay || !strings.Contains(v.notice, "matches 2") {
		t.Fatal("ambiguous buffer target accepted")
	}
	if keys(":b absent\r") != statusStay || !strings.Contains(v.notice, "no terminal") {
		t.Fatal("missing buffer attached fallback")
	}
	if keys(":bn\r") != statusAttach || v.selected != "term-3-cccc" {
		t.Fatal(":bn did not advance")
	}
	if keys(":bn\r") != statusAttach || v.selected != "term-1-aaaa" {
		t.Fatal(":bn did not wrap")
	}
	if keys(":bp\r") != statusAttach || v.selected != "term-3-cccc" {
		t.Fatal(":bp did not wrap")
	}
	keys("/snow雪")
	keys("\x7f")
	if v.query != "snow" || v.mode != statusSearch {
		t.Fatal("search backspace split a rune")
	}
	keys("\x1b")
	if v.mode != statusNormal || v.query != "snow" {
		t.Fatal("Esc did not leave search")
	}
	if keys(":ls\r") != statusRefresh || v.query != "" {
		t.Fatal(":ls did not clear search")
	}
	keys(":q\x1b")
	if v.mode != statusNormal || v.command != "" {
		t.Fatal("cancelled command retained")
	}
	keys("?")
	if v.mode != statusHelp {
		t.Fatal("help not opened")
	}
	if keys("q") != statusStay || v.mode != statusNormal {
		t.Fatal("help q quit the picker")
	}
	if keys("i") != statusAttach || keys(":q\r") != statusQuit {
		t.Fatal("Vim attach/quit action missing")
	}
}

func TestCLIUIVimControlsAndPaste(t *testing.T) {
	d := startDaemon(t, freeAddr(t), "--persist-dir=")
	id, stderr, code := d.cli(nil, "new", "-d", "-n", "vim-target", "--", "cat")
	if code != 0 {
		t.Fatal(stderr)
	}
	id = strings.TrimSpace(id)
	w := d.open(80, 24, "ui", "--discover=false")
	w.waitText("vim-target")
	w.waitText("quick jk returns")
	// A pasted q/i/:q must not quit, attach, or run picker commands.
	w.mu.Lock()
	pasteEnabled := w.screen.Modes().BracketedPaste
	w.mu.Unlock()
	if !pasteEnabled {
		t.Fatal("picker did not request bracketed paste")
	}
	w.keys("\x1b[200~qi:q\r\x1b[201~?")
	w.waitText("ptyd  /  Controls")
	w.keys("q:b vim-target\r")
	w.keys("vim-native-input\r")
	w.waitText("vim-native-input")
	// Return and issue a command in the same read; no trailing key is lost.
	w.keys("jk\x1b[M i!:ls\r")
	w.waitText("/ Search by name or folder")
	w.mu.Lock()
	pasteEnabled = w.screen.Modes().BracketedPaste
	w.mu.Unlock()
	if !pasteEnabled {
		t.Fatal("picker did not restore paste mode after attachment")
	}

	w.keys("/vim\r")
	w.waitText("Search: vim")
	for deadline := time.Now().Add(5 * time.Second); strings.Contains(strings.Join(w.lines(), "\n"), "Updating sessions"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("search stayed stale")
		}
	}
	w.keys("i")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var info terminal.Info
		d.do("GET", "/terminals/"+id, nil, &info)
		if info.Attached > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("i did not attach")
		}
	}

	w.waitText("vim-native-input")
	w.keys("jk:q\r")
	if code := w.waitExit(); code != 0 {
		t.Fatalf("picker exit %d", code)
	}
	w.mu.Lock()
	pasteEnabled = w.screen.Modes().BracketedPaste
	w.mu.Unlock()
	if pasteEnabled {
		t.Fatal("picker left paste mode enabled on exit")
	}
	var info terminal.Info
	if d.do("GET", "/terminals/"+id, nil, &info) != 200 || info.Status != terminal.StatusRunning {
		t.Fatal("quitting picker stopped session")
	}
}

func TestPickerFooterFitsShortWindows(t *testing.T) {
	v := statusView{}
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "one", Name: "one", Preview: &terminal.OverviewPreview{Text: "output"}}}}})
	for _, rows := range []int{8, 16, 24} {
		frame := statusFrame(v, 80, rows)
		if !(strings.Contains(frame, "j then k") || strings.Contains(frame, "jk returns here")) || !strings.Contains(frame, "q quit") || !strings.Contains(frame, "one") {
			t.Fatalf("controls clipped at height%d", rows)
		}
	}
}

func TestPickerHistoryToggleKeepsCompletedWork(t *testing.T) {
	d := startDaemon(t, freeAddr(t), "--persist-dir=")
	old, stderr, code := d.cli(nil, "new", "-d", "-n", "completed-build", "--", "sh", "-c", "echo SAVED_BUILD")
	if code != 0 {
		t.Fatal(stderr)
	}
	old = strings.TrimSpace(old)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var info terminal.Info
		d.do("GET", "/terminals/"+old, nil, &info)
		if info.Status == terminal.StatusExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not exit")
		}
	}
	if _, stderr, code := d.cli(nil, "new", "-d", "-n", "current-shell", "--", "cat"); code != 0 {
		t.Fatal(stderr)
	}
	w := d.open(100, 24, "ui", "--discover=false")
	w.waitText("current-shell")
	w.waitText("1 saved in history")
	if strings.Contains(strings.Join(w.lines(), "\n"), "completed-build") {
		t.Fatal("history visible by default")
	}
	w.keys("h")
	w.waitText("completed-build")
	w.waitText("history shown")
	w.keys("h")
	w.waitText("1 saved in history")
	w.keys("q")
	if w.waitExit() != 0 {
		t.Fatal("picker failed")
	}
	var info terminal.Info
	if d.do("GET", "/terminals/"+old, nil, &info) != 200 || info.Status != terminal.StatusExited {
		t.Fatal("h deleted or restarted completed work")
	}
}

func TestPickerHumanNamesAndPartialDiscovery(t *testing.T) {
	session := native.Session{Ref: native.Ref{Provider: native.Codex, ID: watchNativeID}, Name: "Review the terminal color handoff", CWD: "/projects/terminal", Action: native.Attach}
	row := terminal.SessionSummary{ID: terminal.ID("native:" + session.Ref.Key()), Name: session.Name, CWD: session.CWD, External: &session, State: terminal.OverviewExternal}
	v := statusView{color: true}
	v.refresh(statusResult{view: terminal.Overview{Counts: terminal.OverviewCounts{Total: 1}, Sessions: []terminal.SessionSummary{row}}})
	for _, height := range []int{8, 16, 24} {
		screen := phosphor.New(100, height)
		screen.Write([]byte(statusFrame(v, 100, height)))
		text := strings.Join(screen.Lines(), "\n")
		if !strings.Contains(text, session.Name) || strings.Contains(text, watchNativeID) || !strings.Contains(text, "q quit") {
			t.Fatalf("height%d: %s", height, text)
		}
	}
	row.Name = session.Ref.Key()
	if got := sessionName(row); got != "terminal" {
		t.Fatalf("unnamed native session exposed ref: %s", got)
	}
	v = statusView{}
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{row}}})
	v.command = "b terminal"
	if v.runCommand() != statusAttach || v.selected != row.ID {
		t.Fatalf("displayed name could not be opened: %s", v.notice)
	}
	other := row
	other.ID = "another-id"
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{row, other}}})
	v.command = "b terminal"
	if v.runCommand() != statusStay || !strings.Contains(v.notice, "matches 2") {
		t.Fatal("ambiguous displayed names must be refused")
	}
	v = statusView{}
	v.refresh(statusResult{view: terminal.Overview{DiscoveryIssues: []native.Issue{{Provider: native.Codex, Message: "query timed out"}}}})
	for _, height := range []int{8, 24} {
		frame := statusFrame(v, 100, height)
		if strings.Contains(frame, "No live sessions") || !(strings.Contains(frame, "incomplete") || strings.Contains(frame, "unavailable")) {
			t.Fatalf("partial read claimed absence: %s", frame)
		}
	}
}

func TestCompactPickerRetainsRefreshWarnings(t *testing.T) {
	v := statusView{}
	v.refresh(statusResult{view: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "old", Name: "old-session"}}}})
	v.refresh(statusResult{err: errors.New("daemon unavailable")})
	for _, tc := range []struct {
		state statusView
		want  string
	}{
		{v, "Refresh failed"},
		{func() statusView { w := v; w.err = nil; w.query = "new"; return w }(), "Updating sessions"},
		{func() statusView { w := v; w.err = nil; w.snapshot.view.DiscoveryTruncated = true; return w }(), "could not be discovered"},
	} {
		screen := phosphor.New(100, 8)
		screen.Write([]byte(statusFrame(tc.state, 100, 8)))
		text := strings.Join(screen.Lines(), "\n")
		if !strings.Contains(text, tc.want) || !strings.Contains(text, "old-session") || !strings.Contains(text, "q quit") {
			t.Fatalf("compact state hidden: %s", text)
		}
	}
}
