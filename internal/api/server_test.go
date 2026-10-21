package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// startTestServer wires a Manager + a Server backed by httptest so we
// can issue real HTTP requests against the routes. The httptest.Server
// starts a goroutine listener; defer srv.Close() to shut down.
func startTestServer(t *testing.T, opts terminal.Options, token string) (*httptest.Server, *terminal.Manager) {
	t.Helper()
	mgr := terminal.NewManagerWithOptions(opts)
	apiSrv := New(mgr, Config{Token: token})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(func() {
		// Close any live terminals first so their goroutines don't leak.
		for _, info := range mgr.List() {
			if info.Status == terminal.StatusRunning {
				_ = mgr.Close(info.ID)
			}
		}
		srv.Close()
	})
	return srv, mgr
}

func mustGet(t *testing.T, url string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), "GET", url, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func TestHealthzAlwaysOpen(t *testing.T) {
	srv, _ := startTestServer(t, terminal.Options{}, "secret-token")
	resp, body := mustGet(t, srv.URL+"/healthz", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("/healthz status=%d body=%s", resp.StatusCode, string(body))
	}
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(body))
	}
	if got["status"] != "ok" {
		t.Fatalf("expected status=ok, got %v", got)
	}
}

func TestMetricsAlwaysOpen(t *testing.T) {
	srv, _ := startTestServer(t, terminal.Options{}, "secret-token")
	// No auth header — should still pass since /metrics is exempt.
	resp, body := mustGet(t, srv.URL+"/metrics", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("/metrics status=%d body=%s", resp.StatusCode, string(body))
	}
	var m terminal.Metrics
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(body))
	}
	if m.PID == 0 {
		t.Fatal("PID should be set")
	}
	if m.UptimeSeconds < 0 {
		t.Fatal("UptimeSeconds < 0")
	}
}

func TestTokenEnforcedOnNonExempt(t *testing.T) {
	srv, _ := startTestServer(t, terminal.Options{}, "secret-token")
	// Without token: should 401
	resp, _ := mustGet(t, srv.URL+"/terminals", nil)
	if resp.StatusCode != 401 {
		t.Fatalf("/terminals without token: expected 401, got %d", resp.StatusCode)
	}
	// With token: should 200 (empty list)
	resp, body := mustGet(t, srv.URL+"/terminals", map[string]string{
		"X-Ptyd-Token": "secret-token",
	})
	if resp.StatusCode != 200 {
		t.Fatalf("/terminals with token: expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestMetricsCountersIncrementOnTerminalLifecycle(t *testing.T) {
	srv, mgr := startTestServer(t, terminal.Options{}, "")

	// Spawn one terminal directly via the manager (matches what
	// POST /terminals would do, but lets us avoid encoding the
	// CreateConfig as JSON in the test).
	info, err := mgr.Create(terminal.CreateConfig{
		Name:    "metrics-int-test",
		Command: "sh",
		Args:    []string{"-c", "echo hi; sleep 0.1"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer mgr.Close(info.ID)

	// Wait for child to emit some bytes.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, body := mustGet(t, srv.URL+"/metrics", nil)
		var m terminal.Metrics
		if err := json.Unmarshal(body, &m); err == nil && m.BytesEmitted >= 3 {
			if m.TotalCreated < 1 {
				t.Fatalf("TotalCreated should be >= 1, got %d", m.TotalCreated)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("did not see BytesEmitted increment within 2s")
}

func TestGhostListedAfterDaemonRestartSimulation(t *testing.T) {
	dir := t.TempDir()
	persistDir := filepath.Join(dir, "persist")

	// First daemon: spawn a terminal so meta.json + exit.json are
	// written.
	mgr1 := terminal.NewManagerWithOptions(terminal.Options{
		PersistDir: persistDir,
	})
	info, err := mgr1.Create(terminal.CreateConfig{
		Name:    "ghost-test",
		Command: "sh",
		Args:    []string{"-c", "echo bye"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := info.ID
	time.Sleep(300 * time.Millisecond) // child exits, exit.json written

	// "Restart": fresh Manager + Server pointed at the same persist
	// dir. List should see the ghost.
	mgr2 := terminal.NewManagerWithOptions(terminal.Options{
		PersistDir: persistDir,
	})
	apiSrv := New(mgr2, Config{})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(srv.Close)

	resp, body := mustGet(t, srv.URL+"/terminals", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("/terminals status=%d", resp.StatusCode)
	}
	var list []terminal.Info
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, x := range list {
		if x.ID == id {
			found = true
			if x.Status != terminal.StatusExited {
				t.Errorf("ghost status: expected exited, got %v", x.Status)
			}
			if x.Name != "ghost-test" {
				t.Errorf("ghost name lost: %q", x.Name)
			}
		}
	}
	if !found {
		t.Fatalf("ghost id %s not in /terminals list: %s", id, string(body))
	}
}

func TestUnknownRoutesReturn404(t *testing.T) {
	srv, _ := startTestServer(t, terminal.Options{}, "")
	resp, _ := mustGet(t, srv.URL+"/no-such-route", nil)
	if resp.StatusCode != 404 {
		// http.NewServeMux returns 404 by default, but if it's something
		// else we want to know.
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestMissingTerminalReturns404(t *testing.T) {
	srv, _ := startTestServer(t, terminal.Options{}, "")
	resp, body := mustGet(t, srv.URL+"/terminals/term-9999-zzzz", nil)
	if resp.StatusCode != 404 {
		t.Fatalf("expected 404 for missing id, got %d body=%s", resp.StatusCode, string(body))
	}
}

func TestGhostTerminalReturnsMetadata(t *testing.T) {
	dir := t.TempDir()
	persistDir := filepath.Join(dir, "persist")
	mgr1 := terminal.NewManagerWithOptions(terminal.Options{PersistDir: persistDir})
	info, err := mgr1.Create(terminal.CreateConfig{
		Name:    "ghost-get",
		Command: "sh",
		Args:    []string{"-c", "exit 0"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	mgr2 := terminal.NewManagerWithOptions(terminal.Options{PersistDir: persistDir})
	apiSrv := New(mgr2, Config{})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(srv.Close)

	resp, body := mustGet(t, srv.URL+"/terminals/"+string(info.ID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("ghost get expected 200, got %d body=%s", resp.StatusCode, string(body))
	}
	var got terminal.Info
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, string(body))
	}
	if got.Status != terminal.StatusExited {
		t.Errorf("expected ghost status=exited, got %v", got.Status)
	}
}

func TestGhostMutationReturns409(t *testing.T) {
	dir := t.TempDir()
	persistDir := filepath.Join(dir, "persist")
	mgr1 := terminal.NewManagerWithOptions(terminal.Options{PersistDir: persistDir})
	info, err := mgr1.Create(terminal.CreateConfig{
		Name:    "ghost-write",
		Command: "sh",
		Args:    []string{"-c", "exit 0"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	mgr2 := terminal.NewManagerWithOptions(terminal.Options{PersistDir: persistDir})
	apiSrv := New(mgr2, Config{})
	srv := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(srv.Close)

	url := srv.URL + "/terminals/" + string(info.ID) + "/write"
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url,
		strings.NewReader(`{"data":"foo"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusConflict || !strings.Contains(string(body), "ghost") {
		t.Fatalf("ghost write: status=%d body=%s", resp.StatusCode, body)
	}
}

// A stream's line event is what encoding/json writes for the line.
func TestLineEventMatchesEncodingJSON(t *testing.T) {
	texts := []string{"", "plain", `q"uo\te`, "<a href='x'>&amp;</a>", "tab\tnl\ncr\rbs\bff\f", "\x00\x01\x1f\x7f",
		"漢字 ✓ 🙂", "\u2028\u2029", "bad \xff\xfe utf8 \xe2\x82", "\xed\xa0\x80 surrogate"}
	for c := range 256 {
		texts = append(texts, string([]byte{byte(c)}), "a"+string([]byte{byte(c)})+"b")
	}
	for i, text := range texts {
		checkLineEvent(t, terminal.Line{Seq: int64(i) * 1e9, Text: text, At: time.Date(2026, 1, 2, 3, 4, 5, i, time.FixedZone("x", -4*3600))})
	}
	checkLineEvent(t, terminal.Line{Seq: 0, Text: "now", At: time.Now()})
}

func FuzzLineEvent(f *testing.F) {
	f.Add(int64(3), "a<b>\"c\xff\u2028", int64(1700000000123456789))
	f.Fuzz(func(t *testing.T, seq int64, text string, ns int64) {
		checkLineEvent(t, terminal.Line{Seq: seq, Text: text, At: time.Unix(0, ns).UTC()})
	})
}

func checkLineEvent(t *testing.T, l terminal.Line) {
	t.Helper()
	want, err := json.Marshal(l)
	if err != nil {
		t.Skip(err)
	}
	if got := appendLineEvent(nil, l); string(got) != "event: line\ndata: "+string(want)+"\n\n" {
		t.Fatalf("line %+v:\n got %q\nwant %q", l, got, want)
	}
}
