package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
)

// cli runs a ptyd command against d and returns its stdout, stderr and
// exit code.
func (d *daemon) cli(env []string, args ...string) (string, string, int) {
	d.t.Helper()
	cmd := exec.Command(ptydBin, args...)
	cmd.Env = append(append(os.Environ(), "PTYD_ADDR="+d.addr), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		d.t.Fatal(err)
	}
	return stdout.String(), stderr.String(), cmd.ProcessState.ExitCode()
}

// window is a user's terminal window running a ptyd command: a PTY whose
// output is rendered by an emulator.
type window struct {
	t      *testing.T
	cmd    *exec.Cmd
	ptmx   *os.File
	cooked *term.State
	mu     sync.Mutex
	screen *phosphor.Terminal
	exited chan struct{}
}

func (d *daemon) open(cols, rows int, args ...string) *window {
	d.t.Helper()
	w := &window{t: d.t, screen: phosphor.New(cols, rows), exited: make(chan struct{})}
	w.cmd = exec.Command(ptydBin, args...)
	w.cmd.Env = append(os.Environ(), "PTYD_ADDR="+d.addr)
	ptmx, err := pty.StartWithSize(w.cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		d.t.Fatal(err)
	}
	w.ptmx = ptmx
	if w.cooked, err = term.GetState(int(ptmx.Fd())); err != nil {
		d.t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
			w.mu.Lock()
			w.screen.Write(buf[:n])
			w.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	go func() { _ = w.cmd.Wait(); close(w.exited) }()
	d.t.Cleanup(func() {
		_ = w.cmd.Process.Kill()
		<-w.exited
		ptmx.Close()
	})
	return w
}

func (w *window) keys(s string) {
	if _, err := w.ptmx.Write([]byte(s)); err != nil {
		w.t.Fatal(err)
	}
}

// lines returns the window's rows with trailing blanks trimmed.
func (w *window) lines() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := w.screen.Lines()
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return lines
}

func (w *window) scrollback() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.screen.ScrollbackLines(), "\n")
}

func (w *window) waitText(sub string) {
	w.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(strings.Join(w.lines(), "\n"), sub); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			w.t.Fatalf("window never showed %q:\n%s", sub, strings.Join(w.lines(), "\n"))
		}
	}
}

// waitExit waits for the command to end and checks that the window is
// back in the mode it started in (not raw) with the modes a program
// might have left on reset.
func (w *window) waitExit() int {
	w.t.Helper()
	select {
	case <-w.exited:
	case <-time.After(10 * time.Second):
		w.t.Fatalf("command did not exit:\n%s", strings.Join(w.lines(), "\n"))
	}
	if st, err := term.GetState(int(w.ptmx.Fd())); err != nil || *st != *w.cooked {
		w.t.Fatalf("terminal left in another mode (%v)", err)
	}
	time.Sleep(100 * time.Millisecond) // the last output
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.screen.Modes().AltScreen {
		w.t.Fatal("left in the alternate screen")
	}
	if !w.screen.Cursor().Visible {
		w.t.Fatal("cursor left hidden")
	}
	return w.cmd.ProcessState.ExitCode()
}

func nonBlank(lines []string) []string {
	var out []string
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// The human CLI: new, ls, attach by name or number, kill, and its errors.
func TestCLIManageTerminals(t *testing.T) {
	d := startDaemon(t, freeAddr(t))
	id, stderr, code := d.cli(nil, "term", "new", "-d", "-n", "alpha", "--", "sh", "-c", "echo hi; exec cat")
	id = strings.TrimSpace(id)
	if code != 0 || !strings.HasPrefix(id, "term-") {
		t.Fatalf("new -d: %q %q %d", id, stderr, code)
	}
	// Without -d but with stdout not a terminal, new prints the id too.
	id2, _, _ := d.cli(nil, "term", "new", "-n", "alpha", "--", "cat")
	id2 = strings.TrimSpace(id2)
	if !strings.HasPrefix(id2, "term-2-") {
		t.Fatalf("second new: %q", id2)
	}

	out, _, _ := d.cli(nil, "term", "ls")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") ||
		!strings.Contains(lines[1], id) || !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[1], "running") ||
		!strings.Contains(lines[1], "sh -c echo hi; exec cat") {
		t.Fatalf("ls:\n%s", out)
	}

	if _, stderr, code := d.cli(nil, "term", "kill", "alpha"); code != 1 || !strings.Contains(stderr, `"alpha" matches 2 terminals`) {
		t.Fatalf("ambiguous name: %q %d", stderr, code)
	}
	if _, stderr, code := d.cli(nil, "term", "attach", "nope"); code != 1 || !strings.Contains(stderr, `no terminal "nope"`) {
		t.Fatalf("unknown terminal: %q %d", stderr, code)
	}
	if out, _, code := d.cli(nil, "term", "kill", "2", id); code != 0 || !strings.Contains(out, "killed alpha ("+id2+")") || !strings.Contains(out, "killed alpha ("+id+")") {
		t.Fatalf("kill by number and id: %q %d", out, code)
	}
	if out, stderr, _ := d.cli(nil, "term", "ls"); out != "" || !strings.Contains(stderr, "no terminals") {
		t.Fatalf("ls after kill: %q %q", out, stderr)
	}
	if _, _, code := d.cli(nil, "bogus"); code != 2 {
		t.Fatalf("unknown command: exit %d", code)
	}
}

// The daemon makes a token and saves it for the CLI, owner-only; an
// explicit PTYD_TOKEN wins; errors say what is wrong.
func TestCLITokenAndErrors(t *testing.T) {
	d := startDaemon(t, freeAddr(t))
	path, _ := client.TokenFile(d.addr)
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file %s: %v %v", path, fi.Mode(), err)
	}
	if di, err := os.Stat(filepath.Dir(path)); err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("token dir: %v %v", di.Mode(), err)
	}
	b, _ := os.ReadFile(path)
	if tok, _, _ := d.cli(nil, "token"); tok != string(b) || len(tok) != 33 {
		t.Fatalf("token %q, file %q", tok, b)
	}
	if code := d.do("GET", "/terminals", nil, nil); code != 200 {
		t.Fatalf("with the saved token: %d", code)
	}
	if _, stderr, code := d.cli([]string{"PTYD_TOKEN=wrong"}, "term", "ls"); code != 1 || !strings.Contains(stderr, "refused the token from $PTYD_TOKEN") {
		t.Fatalf("wrong token: %q", stderr)
	}
	d.stop()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file left after a graceful stop: %v", err)
	}
	if _, stderr, code := d.cli(nil, "term", "ls"); code != 1 || !strings.Contains(stderr, "ptyd is not running at "+d.addr) {
		t.Fatalf("daemon down: %q", stderr)
	}

	d = startDaemonEnv(t, freeAddr(t), []string{"PTYD_TOKEN=chosen"})
	if tok, _, _ := d.cli(nil, "token"); tok != "chosen\n" {
		t.Fatalf("explicit token not saved: %q", tok)
	}
}

// Attach, detach, reattach and the program exiting, in a real terminal
// window: after a detach the window is blank but for the message and
// what it showed is in its scrollback; when the program exits its last
// window stays with the exit code below it. Either way the terminal is
// back in cooked mode with modes reset.
func TestCLIAttachDetachExit(t *testing.T) {
	d := startDaemon(t, freeAddr(t))
	w := d.open(60, 10, "term", "new", "-n", "w", "--", "sh")
	w.keys("PS1='$ '; printf '\\033[?1h\\033[?2004h\\033[?25l'; echo typed-$((40+2))\r")
	w.waitText("typed-42")
	w.keys("jk")
	if code := w.waitExit(); code != 0 {
		t.Fatalf("detach exit code %d", code)
	}
	if got := nonBlank(w.lines()); len(got) != 1 || !strings.HasPrefix(got[0], "[detached from w (term-") || w.lines()[0] != got[0] {
		t.Fatalf("window after detach: %q", w.lines())
	}
	if !strings.Contains(w.scrollback(), "typed-42") {
		t.Fatalf("detached window not in the scrollback: %q", w.scrollback())
	}

	w = d.open(60, 10, "term", "attach", "w")
	w.waitText("typed-42")
	w.keys("printf '\\033[?1049h\\033[?1000h'; exit 3\r")
	if code := w.waitExit(); code != 3 {
		t.Fatalf("exit code %d", code)
	}
	lines := nonBlank(w.lines())
	if n := len(lines); n < 2 || lines[n-1] != "[terminal exited (code 3)]" || !strings.Contains(strings.Join(lines, "\n"), "typed-42") {
		t.Fatalf("window after exit: %q", w.lines())
	}
}

// kill -9 of the daemon while attached: the client says so and exits;
// after a restart the terminal comes back and can be attached again. A
// ghost shows its last window and returns.
func TestCLIDaemonKilledWhileAttached(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persist")
	addr := freeAddr(t)
	d := startDaemon(t, addr, "--persist-dir", dir)
	if _, stderr, code := d.cli(nil, "term", "new", "-d", "-n", "old", "--restore", "none", "--", "sh", "-c", "echo old-window; exec cat"); code != 0 {
		t.Fatal(stderr)
	}
	w := d.open(80, 10, "term", "new", "-n", "k", "--restore", "rerun", "--", "sh")
	w.keys("echo before-crash-$((1+1))\r")
	w.waitText("before-crash-2")
	d.kill9()
	if code := w.waitExit(); code != 1 {
		t.Fatalf("exit code %d", code)
	}
	w.waitText("[lost the connection: ptyd at " + addr + " stopped]")

	d = startDaemon(t, addr, "--persist-dir", dir)
	w = d.open(60, 10, "term", "attach", "k")
	w.waitText("before-crash-2")
	w.keys("echo after-$((1+1))\r")
	w.waitText("after-2")
	w.keys("jk")
	w.waitExit()

	w = d.open(60, 10, "term", "attach", "old")
	if code := w.waitExit(); code != 1 {
		t.Fatalf("ghost attach exit code %d", code)
	}
	w.waitText("old-window")
	w.waitText("is not running; this is its last window]")
	w.waitText("[start it again: ptyd term restore ")

	// ptyd term restore brings the ghost back below its last window.
	w = d.open(60, 10, "term", "restore", "old")
	w.waitText("ptyd: restarted sh after a daemon restart")
	w.keys("typed-after-restore\r")
	w.waitText("typed-after-restore\ntyped-after-restore")
	w.keys("jk")
	if code := w.waitExit(); code != 0 {
		t.Fatalf("restore attach exit code %d", code)
	}
}

// ptyd term restore on a command that finished runs it again in its place.
func TestCLIRestoreExited(t *testing.T) {
	d := startDaemon(t, freeAddr(t), "--persist-dir=")
	if _, stderr, code := d.cli(nil, "term", "new", "-d", "-n", "job", "--", "sh", "-c", "exit 3"); code != 0 {
		t.Fatal(stderr)
	}
	for out := ""; !strings.Contains(out, "exited 3"); out, _, _ = d.cli(nil, "term", "ls") {
		time.Sleep(10 * time.Millisecond)
	}
	if _, stderr, code := d.cli(nil, "term", "restore", "-d", "job"); code != 0 {
		t.Fatal(stderr)
	}
	out, _, _ := d.cli(nil, "term", "ls")
	if strings.Count(out, " job ") != 1 {
		t.Fatalf("want one job terminal, got:\n%s", out)
	}
}
