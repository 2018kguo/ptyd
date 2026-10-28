package native

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const fixtureID = "11111111-1111-4111-8111-111111111111"

func fixtureCLI(t *testing.T, name string, data any, exit int) {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncat <<'NATIVE_JSON'\n%s\nNATIVE_JSON\nexit %d\n", raw, exit)
	if err = os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
func claudeRow(id, kind string, pid int) map[string]any {
	return map[string]any{"sessionId": id, "kind": kind, "id": "job-one", "pid": pid, "status": "busy", "state": "working", "startedAt": int64(1700000000000), "name": "compile", "cwd": "/tmp"}
}
func TestClaudeHandoffAndPartialAuthority(t *testing.T) {
	t.Run("foreground stays in original tab", func(t *testing.T) {
		fixtureCLI(t, "claude", []any{claudeRow(fixtureID, "interactive", 23)}, 0)
		rows, truncated, err := claudeSessions(context.Background())
		if err != nil || truncated || len(rows) != 1 || rows[0].Action != Background {
			t.Fatalf("%+v %v %v", rows, truncated, err)
		}
	})
	t.Run("live background attaches without resume", func(t *testing.T) {
		fixtureCLI(t, "claude", []any{claudeRow(fixtureID, "background", 23)}, 0)
		rows, _, err := claudeSessions(context.Background())
		if err != nil || len(rows) != 1 || rows[0].Action != Attach {
			t.Fatalf("%+v %v", rows, err)
		}
		p, err := Plan(rows[0], false)
		if err != nil || !reflect.DeepEqual(p.Args, []string{"attach", "job-one"}) {
			t.Fatalf("%+v %v", p, err)
		}
	})
	t.Run("duplicate beyond output budget still blocks", func(t *testing.T) {
		rows := []any{claudeRow(fixtureID, "background", 23)}
		for i := 1; i < maxSessions; i++ {
			rows = append(rows, claudeRow(fmt.Sprintf("%08x-1111-4111-8111-111111111111", i+1), "background", i+100))
		}
		rows = append(rows, claudeRow(fixtureID, "interactive", 24))
		fixtureCLI(t, "claude", rows, 0)
		sessions, truncated, err := claudeSessions(context.Background())
		if err != nil || !truncated || len(sessions) != maxSessions || sessions[0].Action != Unavailable {
			t.Fatalf("first=%+v n=%d truncated=%v err=%v", sessions[0], len(sessions), truncated, err)
		}
		_, err = new(Discoverer).Lookup(context.Background(), Ref{Claude, fixtureID})
		if err == nil {
			t.Fatal("incomplete native authority authorized import")
		}
	})
	t.Run("partial JSON followed by failed command is not success", func(t *testing.T) {
		fixtureCLI(t, "claude", []any{claudeRow(fixtureID, "background", 23)}, 1)
		if _, _, err := claudeSessions(context.Background()); err == nil {
			t.Fatal("failed command accepted")
		}
	})
}
func TestPlanDoesNotSelectByNameOrReplayPrompts(t *testing.T) {
	base := Session{Ref: Ref{Claude, fixtureID}, CWD: "/tmp", Handle: "job-1", Action: Attach, WorkerPID: os.Getpid()}
	for _, action := range []Action{Background, Unavailable, "unknown"} {
		s := base
		s.Action = action
		s.Reason = "not ready"
		if _, err := Plan(s, true); err == nil {
			t.Fatalf("authorized %s", action)
		}
	}
	for _, id := range []string{"latest", "--last", fixtureID + "; touch /tmp/no", ""} {
		s := base
		s.Ref.ID = id
		if _, err := Plan(s, true); err == nil {
			t.Fatalf("authorized non-exact id %q", id)
		}
	}
	s := base
	s.Action = Resume
	if _, err := Plan(s, false); err == nil {
		t.Fatal("stopped worker resumed without opt-in")
	}
	if _, err := Plan(s, true); err != nil {
		t.Fatal(err)
	}
	s = base
	s.Handle = "--other-session"
	if _, err := Plan(s, false); err == nil {
		t.Fatal("option-shaped handle accepted")
	}
	s = Session{Ref: Ref{Codex, fixtureID}, CWD: "/tmp", Endpoint: "/private/tmp/native daemon/rpc.sock", Action: Attach, WorkerPID: os.Getpid()}
	p, err := Plan(s, false)
	if err != nil || !reflect.DeepEqual(p.Args, []string{"resume", "--remote", "unix:///private/tmp/native daemon/rpc.sock", "--", fixtureID}) {
		t.Fatalf("%+v %v", p, err)
	}
}
func TestCodexDiscoveryUsesReadOnlyRPCAndPinsTheDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ptyd-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "rpc.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var methods []string
	path := "/tmp/rollout.jsonl"
	savedPath := &path
	ephemeral := false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rpc" {
			t.Errorf("wrong endpoint %s", r.URL.Path)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for {
			_, raw, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(raw, &req) != nil {
				return
			}
			mu.Lock()
			methods = append(methods, req.Method)
			mu.Unlock()
			if len(req.ID) == 0 {
				continue
			}
			var out any
			switch req.Method {
			case "initialize":
				out = map[string]any{"userAgent": "fixture"}
			case "thread/loaded/list":
				out = map[string]any{"data": []string{fixtureID}}
			case "thread/read":
				var p struct {
					ID    string `json:"threadId"`
					Turns bool   `json:"includeTurns"`
				}
				if json.Unmarshal(req.Params, &p) != nil || p.ID != fixtureID || p.Turns {
					t.Error("read altered scope or requested history")
				}
				mu.Lock()
				path, temporary := savedPath, ephemeral
				mu.Unlock()
				out = map[string]any{"thread": map[string]any{"id": fixtureID, "cwd": "/tmp", "name": "fixture", "path": path, "ephemeral": temporary, "source": "cli", "createdAt": 1700000000, "status": map[string]any{"type": "active", "activeFlags": []string{"waitingOnApproval"}}}}
			default:
				t.Errorf("discovery attempted %s", req.Method)
				return
			}
			response, _ := json.Marshal(map[string]any{"id": req.ID, "result": out})
			if c.Write(r.Context(), websocket.MessageText, response) != nil {
				return
			}
		}
	}))
	server.Listener = ln
	server.Start()
	t.Cleanup(server.Close)
	alias := filepath.Join(dir, "rendezvous.sock")
	if err = os.Symlink(socket, alias); err != nil {
		t.Fatal(err)
	}
	fixtureCLI(t, "codex", map[string]any{"status": "running", "socketPath": alias}, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, truncated, err := codexSessions(ctx)
	if err != nil || truncated || len(rows) != 1 {
		t.Fatalf("%+v %v %v", rows, truncated, err)
	}
	if rows[0].Action != Attach || rows[0].WorkerPID != os.Getpid() || rows[0].Endpoint != func() string { v, _ := filepath.EvalSymlinks(socket); return v }() || rows[0].Status != "waitingOnApproval" {
		t.Fatalf("wrong identity/status %+v", rows[0])
	}
	mu.Lock()
	gotMethods := append([]string(nil), methods...)
	mu.Unlock()
	if !reflect.DeepEqual(gotMethods, []string{"initialize", "initialized", "thread/loaded/list", "thread/read"}) {
		t.Fatalf("methods: %v", gotMethods)
	}
	for _, tc := range []struct {
		name      string
		path      *string
		ephemeral bool
	}{{"no rollout", nil, false}, {"ephemeral", &path, true}} {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			savedPath, ephemeral = tc.path, tc.ephemeral
			mu.Unlock()
			rows, _, err := codexSessions(ctx)
			if err != nil || len(rows) != 1 || rows[0].Action != Unavailable || rows[0].Status != "not_saved" || !strings.Contains(rows[0].Reason, "no saved rollout") {
				t.Fatalf("unsafe ready thread: %+v %v", rows, err)
			}
			if _, err = Plan(rows[0], true); err == nil {
				t.Fatal("unsaved thread authorized an import")
			}
		})
	}
}
func TestNativeLimitsAndCancellation(t *testing.T) {
	var output cappedOutput
	if _, err := output.Write(make([]byte, maxOutput)); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("x")); err == nil || output.Len() != maxOutput {
		t.Fatal("output grew beyond cap")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "waiting-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	var out any
	if err := commandJSON(ctx, path, nil, &out); err == nil {
		t.Fatal("cancelled probe succeeded")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("probe did not cancel promptly")
	}
	t.Setenv("PATH", dir)
	listing := new(Discoverer).Fresh(context.Background())
	if len(listing.Issues) != 2 || len(listing.Sessions) != 0 {
		t.Fatalf("missing runtimes look complete: %+v", listing)
	}
	for _, issue := range listing.Issues {
		if !strings.Contains(issue.Message, "query failed") {
			t.Fatal(issue.Message)
		}
	}
}
