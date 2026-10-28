package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	"github.com/coder/websocket"
)

// This optional witness uses the installed Codex binary, an isolated profile,
// and a local fake Responses stream. No account credentials or real provider
// are used. It keeps a turn active across native view attach and detach.
func TestInstalledCodexImportKeepsActiveWorker(t *testing.T) {
	binary := os.Getenv("PTYD_CODEX_TEST_BIN")
	if binary == "" {
		t.Skip("set PTYD_CODEX_TEST_BIN to run the installed-CLI witness")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "ptyd-codex-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	home := filepath.Join(root, "profile")
	workspace := filepath.Join(root, "workspace")
	bin := filepath.Join(root, "bin")
	for _, p := range []string{home, workspace, bin} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	active := make(chan struct{})
	showOutput := make(chan struct{})
	release := make(chan struct{})
	providerEnded := make(chan struct{})
	var endedOnce sync.Once
	var activeOnce sync.Once
	var releaseOnce sync.Once
	var requests atomic.Int32
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if !strings.HasSuffix(r.URL.Path, "/responses") {
			http.Error(w, "fixture only accepts Responses requests", 404)
			return
		}
		defer endedOnce.Do(func() { close(providerEnded) })
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		event := func(kind string, data map[string]any) {
			data["type"] = kind
			raw, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, raw)
			flusher.Flush()
		}
		event("response.created", map[string]any{"response": map[string]any{"id": "resp_fixture", "object": "response", "status": "in_progress", "output": []any{}}})
		activeOnce.Do(func() { close(active) })
		select {
		case <-r.Context().Done():
			return
		case <-showOutput:
		}
		item := map[string]any{"id": "msg_fixture", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}
		event("response.output_item.added", map[string]any{"output_index": 0, "item": item})
		event("response.content_part.added", map[string]any{"output_index": 0, "content_index": 0, "item_id": "msg_fixture", "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
		event("response.output_text.delta", map[string]any{"output_index": 0, "content_index": 0, "item_id": "msg_fixture", "delta": "PTYD_ACTIVE_STREAM\n"})
		activeOnce.Do(func() { close(active) })
		select {
		case <-r.Context().Done():
			return
		case <-release:
		}
		item["status"] = "completed"
		item["content"] = []any{map[string]any{"type": "output_text", "text": "PTYD_ACTIVE_STREAM\n", "annotations": []any{}}}
		event("response.output_item.done", map[string]any{"output_index": 0, "item": item})
		event("response.completed", map[string]any{"response": map[string]any{"id": "resp_fixture", "object": "response", "status": "completed", "model": "ptyd-fixture", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	}))
	defer model.Close()
	defer finish()
	config := fmt.Sprintf("check_for_update_on_startup = false\nmodel = \"ptyd-fixture\"\nmodel_provider = \"fixture\"\ncli_auth_credentials_store = \"file\"\n[model_providers.fixture]\nname = \"Local fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\n[projects.%q]\ntrust_level = \"trusted\"\n", model.URL, workspace)
	if err = os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	// Keep this test process and every spawned frontend on the fixture profile.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "CHATGPT_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("CODEX_HOME", home)
	socket := filepath.Join(root, "rpc.sock")
	log, err := os.Create(filepath.Join(root, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(binary, "app-server", "--listen", "unix://"+socket)
	cmd.Dir = workspace
	cmd.Env = os.Environ()
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(stopped) }()
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-stopped:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-stopped
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var conn *websocket.Conn
	for conn == nil && ctx.Err() == nil {
		tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
		conn, _, err = websocket.Dial(ctx, "ws://localhost/rpc", &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr}})
		if err != nil {
			time.Sleep(30 * time.Millisecond)
		}
	}
	if conn == nil {
		raw, _ := os.ReadFile(filepath.Join(root, "daemon.log"))
		t.Fatalf("fixture daemon failed: %v %s", err, raw)
	}
	defer conn.CloseNow()
	seq := 0
	call := func(method string, params any, out any) {
		t.Helper()
		seq++
		id := seq
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if e := conn.Write(ctx, websocket.MessageText, raw); e != nil {
			t.Fatal(e)
		}
		for {
			_, raw, e := conn.Read(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var response struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if e = json.Unmarshal(raw, &response); e != nil {
				t.Fatal(e)
			}
			if response.ID != id {
				continue
			}
			if len(response.Error) > 0 {
				t.Fatalf("%s: %s", method, response.Error)
			}
			if out != nil {
				if e = json.Unmarshal(response.Result, out); e != nil {
					t.Fatal(e)
				}
			}
			return
		}
	}
	call("initialize", map[string]any{"clientInfo": map[string]string{"name": "codex_cli_rs", "version": "test"}}, nil)
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"method":"initialized"}`)); err != nil {
		t.Fatal(err)
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	call("thread/start", map[string]any{"model": "ptyd-fixture", "modelProvider": "fixture", "cwd": workspace, "approvalPolicy": "never", "sandbox": "read-only", "threadSource": "cli"}, &started)
	if started.Thread.ID == "" {
		t.Fatal("fixture thread has no id")
	}
	call("thread/name/set", map[string]string{"threadId": started.Thread.ID, "name": "ptyd-native-fixture"}, nil)
	call("turn/start", map[string]any{"threadId": started.Thread.ID, "input": []any{map[string]any{"type": "text", "text": "fixture stream only", "text_elements": []any{}}}}, nil)
	select {
	case <-active:
	case <-ctx.Done():
		raw, _ := os.ReadFile(filepath.Join(root, "daemon.log"))
		t.Fatalf("local fixture stream never started: %s", raw)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	status, _ := json.Marshal(map[string]string{"status": "running", "socketPath": socket})
	wrapper := fmt.Sprintf("#!/bin/sh\nif [ \"$1 $2 $3\" = 'app-server daemon version' ]; then\ncat <<'JSON'\n%s\nJSON\nelse\nexec %s \"$@\"\nfi\n", status, quote(binary))
	if err = os.WriteFile(filepath.Join(bin, "codex"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nprintf '[]\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	discovery := new(native.Discoverer)
	ref := native.Ref{Provider: native.Codex, ID: started.Thread.ID}
	// An independent existing CLI stays open while the second view attaches.
	original := NewManager()
	defer original.CloseAll()
	old, err := original.Create(CreateConfig{Name: "original-cli", Command: binary, Args: []string{"resume", "--remote", "unix://" + socket, "--", ref.ID}, CWD: workspace, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	for {
		frame, e := original.Screen(old.ID, false)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(strings.Join(frame.Lines, "\n"), "Working") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("original frontend never joined active fixture: %+v", frame)
		}
		time.Sleep(30 * time.Millisecond)
	}
	oldIdentity, err := procIdentity(old.PID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := discovery.Lookup(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if before.Status != "active" || before.WorkerPID <= 0 {
		t.Fatalf("fixture is not active: %+v", before)
	}
	identity, err := procIdentity(before.WorkerPID)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	defer m.CloseAll()
	imported, err := m.ImportAgent(ctx, discovery, ImportRequest{Ref: ref, Cols: 120, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := m.Attach(imported.Terminal.ID, 120, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	defer attachment.Close()
	for {
		frame, e := m.Screen(imported.Terminal.ID, true)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(strings.Join(frame.Lines, "\n"), "Working") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("native frontend did not join the active turn: %+v", frame)
		}
		time.Sleep(30 * time.Millisecond)
	}
	close(showOutput)
	for {
		frame, e := m.Screen(imported.Terminal.ID, true)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(strings.Join(frame.Lines, "\n"), "PTYD_ACTIVE_STREAM") {
			break
		}
		if ctx.Err() != nil {
			t.Fatalf("native frontend did not show active response: %+v", frame)
		}
		time.Sleep(30 * time.Millisecond)
	}
	oldAfter, err := procIdentity(old.PID)
	if err != nil || oldAfter != oldIdentity {
		t.Fatal("import interrupted the original native CLI")
	}
	// The setup RPC client must not accidentally keep the thread alive.
	_ = conn.CloseNow()
	if err = original.Close(old.ID); err != nil {
		t.Fatal(err)
	}
	// Closing the original owned fixture tab hands viewing over to ptyd.
	attachment.Close()
	// Let native disconnect handling run with no frontend or observer, and
	// catch provider cancellation before discovery opens another connection.
	select {
	case <-providerEnded:
		t.Fatal("native response stopped when its last client disconnected")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(200 * time.Millisecond):
	}
	after, err := discovery.Lookup(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	afterIdentity, err := procIdentity(after.WorkerPID)
	if err != nil {
		t.Fatal(err)
	}
	if after.WorkerPID != before.WorkerPID || afterIdentity != identity || after.Status != "active" || requests.Load() != 1 {
		t.Fatalf("worker/turn changed: before=%+v after=%+v requests=%d", before, after, requests.Load())
	}
	listing := discovery.Fresh(ctx)
	if len(listing.Sessions) != 1 || listing.Sessions[0].Ref != ref {
		t.Fatalf("frontend forked or started another conversation: %+v", listing)
	}
	finish()
	t.Log("real Codex frontend rendered an active local stream; detach preserved the native process generation, conversation id and single model request")
}
