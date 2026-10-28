package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

const apiNativeID = "11111111-1111-4111-8111-111111111111"

type nativeFixture struct {
	calls atomic.Int32
	s     native.Session
}

func (f *nativeFixture) List(context.Context) native.Listing {
	f.calls.Add(1)
	return native.Listing{Sessions: []native.Session{f.s}, Issues: []native.Issue{{Provider: native.Codex, Message: "Codex is unavailable"}}}
}
func (f *nativeFixture) Lookup(context.Context, native.Ref) (native.Session, error) {
	f.calls.Add(1)
	return f.s, nil
}
func TestNativeRoutesRequireAuthenticationAndDiscoveryIsExplicit(t *testing.T) {
	n := &nativeFixture{s: native.Session{Ref: native.Ref{Provider: native.Claude, ID: apiNativeID}, CWD: "/tmp", Name: "outside", Status: "busy", Action: native.Background, Reason: "Run /bg"}}
	mgr := terminal.NewManager()
	defer mgr.CloseAll()
	h := New(mgr, Config{Token: "secret", Native: n}).Handler()
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{"GET", "/agents", "", 401}, {"POST", "/agents/import", "", 401}, {"GET", "/overview?discover=true", "", 401},
		{"GET", "/overview", "secret", 200}, {"GET", "/overview?discover=false", "secret", 200}, {"GET", "/overview?discover=wrong", "secret", 400},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Ptyd-Token", tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	if n.calls.Load() != 0 {
		t.Fatal("ordinary overview or unauthorized request ran native probes")
	}
	req := httptest.NewRequest("GET", "/overview?discover=true", nil)
	req.Header.Set("X-Ptyd-Token", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var view terminal.Overview
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Counts.External != 1 || view.Counts.Running != 0 || view.Counts.Total != 1 || view.Sessions[0].State != terminal.OverviewExternal || len(view.DiscoveryIssues) != 1 {
		t.Fatalf("bad external view: %d %s", w.Code, w.Body)
	}
	if len(mgr.List()) != 0 {
		t.Fatal("discovery created registry entries")
	}
	req = httptest.NewRequest("POST", "/agents/import", strings.NewReader(`{"ref":{"provider":"claude","id":"`+apiNativeID+`"}}`))
	req.Header.Set("X-Ptyd-Token", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "/bg") || len(mgr.List()) != 0 {
		t.Fatalf("foreground import: %d %s", w.Code, w.Body)
	}
}
func TestNativeImportHTTPUsesExactAuthorityAndReturnsView(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\nexec cat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	n := &nativeFixture{s: native.Session{Ref: native.Ref{Provider: native.Claude, ID: apiNativeID}, CWD: dir, Name: "fixture", Status: "busy", Action: native.Attach, WorkerPID: os.Getpid(), Handle: "job-fixture"}}
	m := terminal.NewManager()
	defer m.CloseAll()
	h := New(m, Config{Native: n}).Handler()
	body := []byte(`{"ref":{"provider":"claude","id":"` + apiNativeID + `"},"name":"imported","command":"wrong-binary","args":["--last"]}`)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/agents/import", bytes.NewReader(body)))
	var result terminal.ImportResult
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if result.Terminal.Source != n.s.Ref || result.Terminal.Command != "claude" || strings.Join(result.Terminal.Args, " ") != "attach job-fixture" {
		t.Fatalf("caller controlled native launch: %+v", result)
	}
	if strings.Contains(w.Body.String(), "Endpoint") {
		t.Fatal("internal endpoint leaked")
	}
	again := httptest.NewRecorder()
	h.ServeHTTP(again, httptest.NewRequest("POST", "/agents/import", bytes.NewReader(body)))
	if again.Code != http.StatusConflict || len(m.List()) != 1 {
		t.Fatalf("duplicate view: %d %s", again.Code, again.Body)
	}
	a, err := m.Attach(result.Terminal.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()
	if len(m.List()) != 0 {
		t.Fatal("temporary frontend not released")
	}
}
