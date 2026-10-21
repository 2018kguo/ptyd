package client

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/terminal"
	"github.com/2018kguo/ptyd/internal/vt"
)

// userTerminal is the test's stand-in for the user's terminal window: a
// PTY whose slave side the client uses as stdin/stdout, and whose master
// output is rendered by an emulator.
type userTerminal struct {
	master, slave *os.File
	mu            sync.Mutex
	screen        *vt.Terminal
	raw           strings.Builder
}

func newUserTerminal(t *testing.T, cols, rows int) *userTerminal {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		t.Fatal(err)
	}
	u := &userTerminal{master: master, slave: slave, screen: vt.New(cols, rows, vt.Options{})}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := master.Read(buf)
			u.mu.Lock()
			u.screen.Write(buf[:n])
			u.raw.Write(buf[:n])
			u.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { master.Close(); slave.Close() })
	return u
}

func (u *userTerminal) text() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return strings.Join(u.screen.Lines(), "\n")
}

func (u *userTerminal) rawOutput() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.raw.String()
}

func (u *userTerminal) resize(t *testing.T, cols, rows int) {
	if err := pty.Setsize(u.master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		t.Fatal(err)
	}
	u.mu.Lock()
	u.screen.Resize(cols, rows)
	u.mu.Unlock()
}

func (u *userTerminal) typeKeys(t *testing.T, s string) {
	if _, err := u.master.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

type result struct {
	msg string
	err error
}

func startDaemon(t *testing.T) (*terminal.Manager, string) {
	t.Helper()
	mgr := terminal.NewManager()
	srv := httptest.NewServer(api.New(mgr, api.Config{Token: "tok"}).Handler())
	t.Cleanup(func() { mgr.CloseAll(); srv.Close() })
	return mgr, strings.TrimPrefix(srv.URL, "http://")
}

func attach(u *userTerminal, addr, id string, winch chan os.Signal) chan result {
	done := make(chan result, 1)
	go func() {
		msg, err := Attach(context.Background(), Options{Addr: addr, Token: "tok", ID: id}, u.slave, u.slave, winch)
		done <- result{msg, err}
	}()
	return done
}

func TestAttachInteractiveShell(t *testing.T) {
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{
		Name: "sh", Command: "sh", Cols: 60, Rows: 10,
		Env: map[string]string{"PS1": "$ ", "ENV": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Output from before the attach is repainted.
	_ = mgr.Write(info.ID, "echo before-attach\r")
	eventually(t, "early output", func() bool {
		scr, _ := mgr.Screen(info.ID, false)
		return strings.Contains(strings.Join(scr.Lines, "\n"), "before-attach\n$")
	})

	u := newUserTerminal(t, 80, 24)
	before, err := term.GetState(int(u.slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	winch := make(chan os.Signal, 1)
	done := attach(u, addr, string(info.ID), winch)

	eventually(t, "repaint", func() bool { return strings.Contains(u.text(), "before-attach") })
	// The attacher's size wins.
	eventually(t, "resize to the attacher", func() bool {
		scr, _ := mgr.Screen(info.ID, false)
		return scr.Cols == 80 && scr.Rows == 24
	})

	u.typeKeys(t, "echo typed-$((40+2))\r")
	eventually(t, "live output", func() bool { return strings.Contains(u.text(), "typed-42") })

	// A window resize propagates.
	u.resize(t, 100, 30)
	winch <- os.Interrupt
	eventually(t, "resize", func() bool {
		scr, _ := mgr.Screen(info.ID, false)
		return scr.Cols == 100 && scr.Rows == 30
	})
	u.typeKeys(t, "stty size\r")
	eventually(t, "program sees new size", func() bool { return strings.Contains(u.text(), "30 100") })

	// A real full-screen program, through the passthrough.
	if _, err := exec.LookPath("less"); err == nil {
		u.typeKeys(t, "seq 1 500 | LESS= less\r")
		eventually(t, "less page", func() bool { return strings.HasPrefix(u.text(), "1\n2\n3\n") })
		u.typeKeys(t, " ")
		eventually(t, "less next page", func() bool { return strings.HasPrefix(u.text(), "30\n31\n") })
		u.typeKeys(t, "q")
		eventually(t, "back at the prompt", func() bool { return strings.Contains(u.text(), "typed-42") })
	}

	// A lone Ctrl-\ reaches the program after the detach window.
	u.typeKeys(t, "cat -v\r")
	time.Sleep(200 * time.Millisecond)
	u.typeKeys(t, "\x1c")
	time.Sleep(detachWindow + 200*time.Millisecond)
	// SIGQUIT ends cat; nothing detached.
	select {
	case r := <-done:
		t.Fatalf("detached on a single Ctrl-\\: %+v", r)
	default:
	}

	// Ctrl-\ twice detaches and restores the local terminal.
	u.typeKeys(t, "\x1c")
	time.Sleep(50 * time.Millisecond)
	u.typeKeys(t, "\x1c")
	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("did not detach")
	}
	if r.err != nil || !strings.Contains(r.msg, "detached") {
		t.Fatalf("detach result %+v", r)
	}
	after, _ := term.GetState(int(u.slave.Fd()))
	if *after != *before {
		t.Fatal("local terminal mode not restored")
	}
	eventually(t, "local modes reset", func() bool { return strings.HasSuffix(u.rawOutput(), resetLocal) })
	// The terminal keeps running without the client.
	if got, _ := mgr.Get(info.ID); got.Status != terminal.StatusRunning {
		t.Fatalf("terminal status after detach: %s", got.Status)
	}

	// Attach again; leaving the shell ends the attachment.
	u2 := newUserTerminal(t, 100, 30)
	done = attach(u2, addr, string(info.ID), make(chan os.Signal))
	eventually(t, "second repaint", func() bool { return strings.Contains(u2.text(), "typed-42") })
	u2.typeKeys(t, "exit\r")
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attachment did not end with the terminal")
	}
	if r.err != nil || !strings.Contains(r.msg, "ended") {
		t.Fatalf("end result %+v", r)
	}
}

// Several clients can attach at once; all see the output.
func TestMultipleAttachers(t *testing.T) {
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "cat", Command: "cat", Cols: 40, Rows: 5})
	if err != nil {
		t.Fatal(err)
	}
	a, b := newUserTerminal(t, 40, 5), newUserTerminal(t, 40, 5)
	da := attach(a, addr, string(info.ID), make(chan os.Signal))
	db := attach(b, addr, string(info.ID), make(chan os.Signal))
	time.Sleep(200 * time.Millisecond)
	a.typeKeys(t, "from-a\r")
	b.typeKeys(t, "from-b\r")
	for _, u := range []*userTerminal{a, b} {
		eventually(t, "both inputs on both screens", func() bool {
			s := u.text()
			return strings.Count(s, "from-a") == 2 && strings.Count(s, "from-b") == 2
		})
	}
	a.typeKeys(t, "\x1c\x1c")
	b.typeKeys(t, "\x1c\x1c")
	<-da
	<-db
}

func TestAttachErrors(t *testing.T) {
	_, addr := startDaemon(t)
	u := newUserTerminal(t, 80, 24)
	_, err := Attach(context.Background(), Options{Addr: addr, Token: "tok", ID: "nope"}, u.slave, u.slave, nil)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown id: %v", err)
	}
	_, err = Attach(context.Background(), Options{Addr: addr, Token: "wrong", ID: "nope"}, u.slave, u.slave, nil)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("bad token: %v", err)
	}
}
