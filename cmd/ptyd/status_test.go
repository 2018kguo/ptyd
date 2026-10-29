package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusEscapeChunkingAndControls(t *testing.T) {
	for _, tc := range []struct {
		key  string
		move int
	}{{"\x1b[A", -1}, {"\x1b[B", 1}, {"\x1bOA", -1}, {"\x1b[1;5B", 1}, {"\x1b[3~", 0}} {
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
	view := terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "t", Name: "bad\x1b]52;c;secret\a\nname", CWD: "a\tb", State: terminal.OverviewRunning, Preview: &terminal.OverviewPreview{Source: "pending", Text: "\x1b[31mhello\x1b[0m"}}}}
	var plain bytes.Buffer
	printStatus(&plain, view)
	if strings.ContainsAny(plain.String(), "\x1b\a") || strings.Contains(plain.String(), "a\tb") {
		t.Fatalf("terminal controls leaked: %q", plain.String())
	}
}
func TestCLIStatusOverview(t *testing.T) {
	d := startDaemon(t, freeAddr(t), "--persist-dir=")
	var watched terminal.Info
	if code := d.do("POST", "/terminals", terminal.CreateConfig{Name: "watch-build", Command: "sh", Args: []string{"-c", "echo BUILD-FAILED; exec cat"}, Tags: []string{"build"}, Watches: []terminal.WatchSpec{{Name: "failed", Pattern: "^BUILD-FAILED$", Once: true}}}, &watched); code != 201 {
		t.Fatal(code)
	}
	if _, stderr, code := d.cli(nil, "term", "new", "-d", "-n", "quiet", "--", "cat"); code != 0 {
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

}
func TestStatusAttachPreservesCancellation(t *testing.T) {
	requested := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested <- struct{}{}
		http.NotFound(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := client.API{Addr: strings.TrimPrefix(server.URL, "http://")}
	attach(ctx, c, terminal.Info{ID: "term-test"}, client.Options{})
	select {
	case <-requested:
		t.Fatal("attachment discarded overview cancellation")
	default:
	}
}

const watchNativeID = "11111111-1111-4111-8111-111111111111"

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
func TestNativeCLIReportsFrontendExitCode(t *testing.T) {
	d, _, ref := nativeCLIFixture(t, "#!/bin/sh\nprintf 'VIEW_READY\\n'\nread line\nexit 7\n")
	w := d.open(110, 20, "term", "import", ref.Key())
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
