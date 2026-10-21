package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func post(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// Omitting "since" must mean "all retained output", as it does for /output
// and /stream. It used to mean since=0, silently skipping the first line.
func TestWaitWithoutSinceMatchesFirstLine(t *testing.T) {
	srv, mgr := startTestServer(t, terminal.Options{}, "")
	info, err := mgr.Create(terminal.CreateConfig{Name: "w", Command: "sh", Args: []string{"-c", "echo FIRST; sleep 5"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	code, body := post(t, srv.URL+"/terminals/"+string(info.ID)+"/wait", `{"pattern":"^FIRST$","timeout_seconds":1}`)
	if code != 200 || !strings.Contains(string(body), `"matched":true`) {
		t.Fatalf("status=%d body=%s", code, body)
	}
}

func TestKeysPreserveLiteralCase(t *testing.T) {
	for in, want := range map[string]string{"A": "A", "é": "é", "ctrl+C": "\x03", "Enter": "\r"} {
		got, err := keyToBytes(in)
		if err != nil || got != want {
			t.Errorf("keyToBytes(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestErrorStatusCodes(t *testing.T) {
	srv, mgr := startTestServer(t, terminal.Options{}, "")
	exited, err := mgr.Create(terminal.CreateConfig{Name: "e", Command: "sh", Args: []string{"-c", "true"}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cases := []struct {
		path, body string
		want       int
	}{
		{"/terminals/nope/write", `{"data":"x"}`, 404},
		{"/terminals/nope/resize", `{"cols":80,"rows":24}`, 404},
		{"/terminals/nope/signal", `{"signal":"int"}`, 404},
		{"/terminals/nope/wait", `{"pattern":"x"}`, 404},
		{"/terminals/nope/restart", ``, 404},
		{"/terminals/" + string(exited.ID) + "/write", `{"data":"x"}`, 409},
		{"/terminals/" + string(exited.ID) + "/resize", `{"cols":0,"rows":24}`, 400},
		{"/terminals", `{"name":"big","command":"sh","args":["` + strings.Repeat("a", 2<<20) + `"]}`, 413},
	}
	for _, c := range cases {
		code, body := post(t, srv.URL+c.path, c.body)
		if code != c.want {
			t.Errorf("POST %s: status %d, want %d (%.200s)", c.path, code, c.want, body)
		}
		var e map[string]string
		if json.Unmarshal(body, &e) != nil || e["error"] == "" {
			t.Errorf("POST %s: error body not {\"error\":...}: %.200s", c.path, body)
		}
	}
}

// Shutdown must not hang on open SSE streams or pending waits.
func TestShutdownEndsStreams(t *testing.T) {
	mgr := terminal.NewManager()
	info, err := mgr.Create(terminal.CreateConfig{Name: "s", Command: "sh", Args: []string{"-c", "sleep 30"}})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close(info.ID)
	s := New(mgr, Config{Addr: "127.0.0.1:0"})
	ln, err := s.Listen()
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ln)
	resp, err := http.Get("http://" + ln.Addr().String() + "/terminals/" + string(info.ID) + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	go io.Copy(io.Discard, bufio.NewReader(resp.Body))
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v after %v", err, time.Since(start))
	}
}
