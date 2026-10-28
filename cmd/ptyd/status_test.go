package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if v.selected != "b" || len(v.sessions()) != 2 || !strings.Contains(statusFrame(v, 100, 20), "STALE") {
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
		w.keys("\r")
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
		marker := strings.Repeat("live-", i+1)
		w.keys(marker + "\r")
		d.waitScreen(id, func(s screen) bool { return strings.Contains(strings.Join(s.Lines, "\n"), marker) })
		w.keys("\x1c\x1c")
		w.waitText("ptyd |")
		w.waitText("watch-target")
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatalf("watch exit %d", code)
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
