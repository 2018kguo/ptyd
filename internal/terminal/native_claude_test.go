package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/native"
)

// This witness starts only a private Claude supervisor and a fake local
// Messages stream. It never imports a worker from the user's configuration.
func TestInstalledClaudeImportKeepsActiveWorker(t *testing.T) {
	binary := os.Getenv("PTYD_CLAUDE_TEST_BIN")
	if binary == "" {
		t.Skip("set PTYD_CLAUDE_TEST_BIN to run the installed-CLI witness")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "ptyd-claude-")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	profile := filepath.Join(root, "profile")
	workspace := filepath.Join(root, "workspace")
	bin := filepath.Join(root, "bin")
	for _, p := range []string{profile, workspace, bin} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	active := make(chan struct{})
	showOutput := make(chan struct{})
	release := make(chan struct{})
	var activeOnce, releaseOnce sync.Once
	var requests atomic.Int32
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if strings.HasSuffix(r.URL.Path, "/count_tokens") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"input_tokens":1}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			http.Error(w, "fixture endpoint", 404)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		event := func(kind string, data map[string]any) {
			data["type"] = kind
			raw, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
			flusher.Flush()
		}
		event("message_start", map[string]any{"message": map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "model": "fixture", "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}})
		activeOnce.Do(func() { close(active) })
		select {
		case <-r.Context().Done():
			return
		case <-showOutput:
		}
		event("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
		event("content_block_delta", map[string]any{"index": 0, "delta": map[string]any{"type": "text_delta", "text": "PTYD_CLAUDE_ACTIVE_STREAM\n"}})
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		event("content_block_stop", map[string]any{"index": 0})
		event("message_delta", map[string]any{"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 1}})
		event("message_stop", map[string]any{})
	}))
	defer model.Close()
	defer finish()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CLAUDE_") || strings.HasPrefix(key, "ANTHROPIC_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	t.Setenv("ANTHROPIC_API_KEY", "ptyd-local-fixture-key")
	t.Setenv("ANTHROPIC_BASE_URL", model.URL)
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	t.Setenv("DISABLE_AUTOUPDATER", "1")
	config := map[string]any{"hasCompletedOnboarding": true, "theme": "dark", "projects": map[string]any{workspace: map[string]any{"hasTrustDialogAccepted": true}}, "customApiKeyResponses": map[string]any{"approved": []string{"ptyd-local-fixture-key", "ptyd-local-fixture-key"[2:]}, "rejected": []any{}}}
	raw, _ := json.Marshal(config)
	if err = os.WriteFile(filepath.Join(profile, ".claude.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Pin cleanup to this private configuration even if a test changes env later.
	fixtureEnv := os.Environ()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, binary, "daemon", "stop", "--any")
		stop.Env = fixtureEnv
		stop.Dir = workspace
		out, e := stop.CombinedOutput()
		if e != nil {
			t.Errorf("private supervisor cleanup: %v %s", e, out)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	launch := exec.CommandContext(ctx, binary, "--bg", "--name", "ptyd-native-fixture", "--model", "haiku", "--permission-mode", "plan", "fixture stream only")
	launch.Dir = workspace
	launch.Env = fixtureEnv
	if out, e := launch.CombinedOutput(); e != nil {
		t.Fatalf("private native launch: %v %s", e, out)
	}
	select {
	case <-active:
	case <-ctx.Done():
		t.Fatal("local Claude stream never started")
	}
	if err = os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nprintf '{\"status\":\"stopped\"}\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// Use exactly the binary under test for discovery and attach.
	if err = os.Symlink(binary, filepath.Join(bin, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	discovery := new(native.Discoverer)
	listing := discovery.Fresh(ctx)
	var sessions []native.Session
	for _, s := range listing.Sessions {
		if s.Ref.Provider == native.Claude {
			sessions = append(sessions, s)
		}
	}
	if len(sessions) != 1 {
		t.Fatalf("fixture namespace does not contain exactly one session: %+v", listing)
	}
	before := sessions[0]
	if before.Action != native.Attach || before.WorkerPID <= 0 {
		t.Fatalf("not active: %+v", before)
	}
	identity, err := procIdentity(before.WorkerPID)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	defer m.CloseAll()
	imported, err := m.ImportAgent(ctx, discovery, ImportRequest{Ref: before.Ref, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := m.Attach(imported.Terminal.ID, AttachOptions{Cols: 120, Rows: 40, Rendered: true})
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close()
	for {
		frame, e := m.Screen(imported.Terminal.ID, true)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(strings.Join(frame.Lines, "\n"), "Claude Code") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("native frontend did not render: %+v", frame)
		}
		time.Sleep(30 * time.Millisecond)
	}
	close(showOutput)
	for {
		frame, e := m.Screen(imported.Terminal.ID, true)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(strings.Join(frame.Lines, "\n"), "PTYD_CLAUDE_ACTIVE_STREAM") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("native frontend did not show active output: %+v", frame)
		}
		time.Sleep(30 * time.Millisecond)
	}
	attachment.Close()
	after, err := discovery.Lookup(ctx, before.Ref)
	if err != nil {
		t.Fatal(err)
	}
	afterIdentity, err := procIdentity(after.WorkerPID)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorkerPID != before.WorkerPID || identity != afterIdentity || after.Action != native.Attach || after.Status != "busy" || requests.Load() != 1 {
		t.Fatalf("native worker/turn changed: before=%+v after=%+v requests=%d", before, after, requests.Load())
	}
	finish()
	t.Log("real Claude frontend rendered an active local stream; detach preserved the native process generation, conversation id and single model request")
}
