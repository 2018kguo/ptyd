package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
)

// End-to-end tests run the real binary, so they cover kill -9 of the
// daemon process itself.

var ptydBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ptyd-e2e")
	if err != nil {
		panic(err)
	}
	ptydBin = filepath.Join(dir, "ptyd")
	// Daemons save their tokens here, and the CLI and d.do read them.
	os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	os.Unsetenv("PTYD_TOKEN")
	if out, err := exec.Command("go", "build", "-o", ptydBin, ".").CombinedOutput(); err != nil {
		panic(fmt.Sprintf("build: %v\n%s", err, out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type daemon struct {
	t    *testing.T
	cmd  *exec.Cmd
	addr string
	log  *bytes.Buffer
}

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func startDaemon(t *testing.T, addr string, args ...string) *daemon {
	t.Helper()
	return startDaemonEnv(t, addr, nil, args...)
}

func startDaemonEnv(t *testing.T, addr string, env []string, args ...string) *daemon {
	t.Helper()
	d := &daemon{t: t, addr: addr, log: &bytes.Buffer{}}
	d.cmd = exec.Command(ptydBin, append([]string{"--bind", addr}, args...)...)
	d.cmd.Env = append(os.Environ(), env...)
	d.cmd.Stdout, d.cmd.Stderr = d.log, d.log
	if err := d.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if d.cmd.ProcessState == nil {
			_ = d.cmd.Process.Signal(syscall.SIGTERM)
			_ = d.cmd.Wait()
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if resp, err := http.Get("http://" + addr + "/healthz"); err == nil {
			resp.Body.Close()
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not start: %s", d.log)
		}
	}
}

func (d *daemon) kill9() {
	_ = d.cmd.Process.Signal(syscall.SIGKILL)
	_ = d.cmd.Wait()
}

func (d *daemon) stop() {
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	_ = d.cmd.Wait()
}

func (d *daemon) do(method, path string, body any, out any) int {
	d.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://"+d.addr+path, r)
	req.Header.Set("X-Ptyd-Token", client.Token(d.addr))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		d.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			d.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

type screen struct {
	Cols, Rows int
	CursorX    int      `json:"cursor_x"`
	CursorY    int      `json:"cursor_y"`
	AltScreen  bool     `json:"alt_screen"`
	Title      string   `json:"title"`
	Lines      []string `json:"lines"`
	Scrollback []string `json:"scrollback"`
}

type info struct {
	ID       string `json:"id"`
	PID      int    `json:"pid"`
	Status   string `json:"status"`
	Restored int    `json:"restored"`
	Notice   string `json:"notice"`
}

func (d *daemon) screen(id string) screen {
	var s screen
	if code := d.do("GET", "/terminals/"+id+"/screen?scrollback=1", nil, &s); code != 200 {
		d.t.Fatalf("screen %s: %d", id, code)
	}
	return s
}

func (d *daemon) waitScreen(id string, cond func(screen) bool) screen {
	d.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		s := d.screen(id)
		if cond(s) {
			return s
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("screen of %s never matched: %q scrollback %q\n%s", id, s.Lines, s.Scrollback, d.log)
		}
	}
}

func eqScreen(a, b screen) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

func processGone(pid int) bool {
	return syscall.Kill(pid, 0) == syscall.ESRCH
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !processGone(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("process %d outlived its daemon", pid)
		}
	}
}

// kill -9 of the daemon: the ghost shows the screen as it was.
func TestKill9KeepsScreen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persist")
	addr := freeAddr(t)
	d := startDaemon(t, addr, "--persist-dir", dir)
	var ti info
	d.do("POST", "/terminals", map[string]any{
		"name": "app", "command": "sh", "cols": 40, "rows": 8,
		"args": []string{"-c", `i=0; while [ $i -lt 30 ]; do echo "row $i"; i=$((i+1)); done; printf '\033]2;t1\007\033[?1049h\033[3;3Hin the alt screen'; exec cat`},
	}, &ti)
	want := d.waitScreen(ti.ID, func(s screen) bool { return s.AltScreen && strings.Contains(s.Lines[2], "alt screen") })
	d.kill9()
	waitGone(t, ti.PID)

	d2 := startDaemon(t, addr, "--persist-dir", dir)
	var got info
	d2.do("GET", "/terminals/"+ti.ID, nil, &got)
	if got.Status != "exited" {
		t.Fatalf("ghost status %q", got.Status)
	}
	if s := d2.screen(ti.ID); !eqScreen(s, want) {
		t.Fatalf("after kill -9:\n got %+v\nwant %+v", s, want)
	}
}

// A graceful stop checkpoints; the next daemon shows the same screen.
func TestGracefulRestartKeepsScreen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persist")
	addr := freeAddr(t)
	d := startDaemon(t, addr, "--persist-dir", dir)
	var ti info
	d.do("POST", "/terminals", map[string]any{"name": "app", "command": "sh", "cols": 30, "rows": 5,
		"args": []string{"-c", `echo one; echo two; printf '\033[1;32mgreen'; exec cat`}}, &ti)
	id := ti.ID
	want := d.waitScreen(id, func(s screen) bool { return strings.Contains(strings.Join(s.Lines, "\n"), "green") })
	d.stop()
	d2 := startDaemon(t, addr, "--persist-dir", dir)
	if s := d2.screen(id); !eqScreen(s, want) {
		t.Fatalf("after restart:\n got %+v\nwant %+v", s, want)
	}
}

// Restore policies across kill -9: rerun terminals come back under the
// same id (once per restart, also in a crash loop); none stays a ghost
// until restored explicitly.
func TestRestorePoliciesAcrossKill9(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persist")
	addr := freeAddr(t)
	d := startDaemon(t, addr, "--persist-dir", dir)
	var rerun, none info
	d.do("POST", "/terminals", map[string]any{"name": "r", "command": "sh", "restore": "rerun",
		"args": []string{"-c", `echo "started $$"; exec cat`}}, &rerun)
	d.do("POST", "/terminals", map[string]any{"name": "n", "command": "sh",
		"args": []string{"-c", `echo "plain $$"; exec cat`}}, &none)
	d.waitScreen(rerun.ID, func(s screen) bool { return strings.Contains(strings.Join(s.Lines, "\n"), "started") })
	d.waitScreen(none.ID, func(s screen) bool { return strings.Contains(strings.Join(s.Lines, "\n"), "plain") })

	pids := []int{rerun.PID}
	for round := 1; round <= 2; round++ {
		d.kill9()
		waitGone(t, pids[len(pids)-1])
		d = startDaemon(t, addr, "--persist-dir", dir)
		var got info
		d.do("GET", "/terminals/"+rerun.ID, nil, &got)
		if got.Status != "running" || got.Restored != round {
			t.Fatalf("round %d: rerun terminal %+v", round, got)
		}
		pids = append(pids, got.PID)
		d.waitScreen(rerun.ID, func(s screen) bool {
			return len(regexp.MustCompile(`started \d+`).FindAllString(strings.Join(append(s.Scrollback, s.Lines...), "\n"), -1)) == round+1
		})
		var g info
		d.do("GET", "/terminals/"+none.ID, nil, &g)
		if g.Status != "exited" {
			t.Fatalf("round %d: none policy terminal %+v", round, g)
		}
	}
	for _, pid := range pids[:len(pids)-1] {
		if !processGone(pid) {
			t.Fatalf("an earlier run (pid %d) is still alive", pid)
		}
	}

	// Explicit restore of the none ghost, with a policy.
	if code := d.do("POST", "/terminals/"+none.ID+"/restore", nil, nil); code != 400 {
		t.Fatalf("restore without a policy: %d", code)
	}
	var restored info
	if code := d.do("POST", "/terminals/"+none.ID+"/restore", map[string]string{"policy": "rerun"}, &restored); code != 201 || restored.Status != "running" || restored.ID != none.ID {
		t.Fatalf("explicit restore: %d %+v", code, restored)
	}
	if code := d.do("POST", "/terminals/"+none.ID+"/restore", map[string]string{"policy": "rerun"}, nil); code != 409 {
		t.Fatalf("restoring a running terminal: %d", code)
	}
}
