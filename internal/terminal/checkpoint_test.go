package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/benchdata"
)

func persistManager(t *testing.T, dir string) *Manager {
	t.Helper()
	m := NewManagerWithOptions(Options{PersistDir: dir})
	t.Cleanup(m.CloseAll)
	return m
}

// equalScreens compares everything Screen reports.
func equalScreens(a, b Screen) bool {
	return a.Cols == b.Cols && a.Rows == b.Rows && a.CursorX == b.CursorX && a.CursorY == b.CursorY &&
		a.CursorVisible == b.CursorVisible && a.AltScreen == b.AltScreen && a.Title == b.Title &&
		slices.Equal(a.Lines, b.Lines) && slices.Equal(a.Scrollback, b.Scrollback)
}

// A daemon that dies without warning (as with kill -9) leaves enough on
// disk to rebuild the exact screen: the next daemon shows it for the ghost.
func TestScreenSurvivesCrash(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := persistManager(t, dir)
	info, err := m1.Create(CreateConfig{Name: "app", Command: "sh", Cols: 30, Rows: 6,
		Args: []string{"-c", `i=0; while [ $i -lt 20 ]; do echo "line $i"; i=$((i+1)); done; printf '\033]2;my title\007\033[?1049h\033[2;4H\033[1;33mfull screen\033[?25l'; exec cat`}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alt screen", func() bool {
		s, _ := m1.Screen(info.ID, true)
		return s.AltScreen && strings.Contains(s.Lines[1], "full screen")
	})
	// Resized after the output: the tail records it.
	if err := m1.Resize(info.ID, 40, 8); err != nil {
		t.Fatal(err)
	}
	want, _ := m1.Screen(info.ID, true)

	// "Crash": m1 is abandoned as is; a new daemon reads the same dir.
	m2 := persistManager(t, dir)
	got, err := m2.Screen(info.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !equalScreens(got, want) {
		t.Fatalf("ghost screen differs\n got %+v\nwant %+v", got, want)
	}
	if !slices.Contains(got.Scrollback, "line 0") || got.Title != "my title" || got.Cols != 40 {
		t.Fatalf("ghost screen lost state: %+v", got)
	}
}

// A graceful shutdown checkpoints the screen before the program is
// terminated, so whatever it prints on the way out is not saved.
func TestShutdownCheckpointExcludesExitNoise(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := NewManagerWithOptions(Options{PersistDir: dir})
	info, err := m1.Create(CreateConfig{Name: "app", Command: "sh", Cols: 30, Rows: 4,
		Args: []string{"-c", `trap 'echo GOODBYE-NOISE; exit 0' TERM; echo working; while :; do sleep 0.05; done`}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool { s, _ := m1.Screen(info.ID, false); return s.Lines[0] == "working" })
	want, _ := m1.Screen(info.ID, true)
	m1.CloseAll()

	tdir := filepath.Join(dir, string(info.ID))
	if gens := tailGens(tdir); len(gens) != 0 {
		t.Fatalf("tails left after a final checkpoint: %v", gens)
	}
	m2 := persistManager(t, dir)
	got, _ := m2.Screen(info.ID, true)
	if !equalScreens(got, want) {
		t.Fatalf("screen after shutdown %+v, want %+v", got, want)
	}
}

// kill9Output is what writeTailThenDie stores: more than one mapped
// window, in PTY-read sized records, with a resize in the middle.
var kill9Output = benchdata.Claude(3<<20, 80, 24)

// writeTailThenDie stores kill9Output in a tail in dir, the way a daemon
// does, and SIGKILLs itself before anything closes or unmaps the file.
func writeTailThenDie(dir string) {
	mapTails = os.Getenv("PTYD_KILL9_MAP") == "1"
	p := &persister{dir: dir}
	for i, out := 0, kill9Output; len(out) > 0; i++ {
		n := min(len(out), 1024)
		p.appendLocked(recData, out[:n])
		out = out[n:]
		if i == 1000 {
			p.appendLocked(recResize, []byte{0, 100, 0, 30})
		}
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
	select {}
}

// What a daemon stored in its tail before a kill -9 is on disk for the
// next one: the stores into the mapped file are in the page cache the
// moment they are made.
func TestTailSurvivesKill9(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mapped := range []string{"0", "1"} {
		dir := t.TempDir()
		cmd := exec.Command(self, "-test.run", "^$")
		cmd.Env = append(os.Environ(), "PTYD_KILL9_TAIL="+dir, "PTYD_KILL9_MAP="+mapped)
		err = cmd.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("helper did not die by SIGKILL: %v", err)
		}
		got, _, err := loadScreen(dir, 80, 24)
		if err != nil {
			t.Fatal(err)
		}
		want := phosphor.New(80, 24)
		want.Write(kill9Output[:1001*1024])
		want.Resize(100, 30)
		want.Write(kill9Output[1001*1024:])
		if !bytes.Equal(got.Repaint(true), want.Repaint(true)) {
			t.Fatalf("mapped=%s: screen after kill -9 differs:\n got %q\nwant %q", mapped, got.Lines(), want.Lines())
		}
	}
}

// Truncating the tail anywhere (a torn record, or output cut inside an
// escape sequence) still yields a usable screen, never an error.
func TestTornTailReplays(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := persistManager(t, dir)
	info, err := m1.Create(CreateConfig{Name: "app", Command: "sh", Cols: 20, Rows: 4,
		Args: []string{"-c", `printf 'ab\033[1;31mcd\033]2;title\007\033[2;1H漢字\033[0m'; exec cat`}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool { s, _ := m1.Screen(info.ID, false); return s.Lines[1] == "漢字" })
	tdir := filepath.Join(dir, string(info.ID))
	gens := tailGens(tdir)
	if len(gens) != 1 {
		t.Fatalf("tails %v", gens)
	}
	raw, err := os.ReadFile(filepath.Join(tdir, "tail.0"))
	if err != nil {
		t.Fatal(err)
	}
	// The live tail is mapped a window at a time: past its records it
	// reads as zeros, which is where a crash leaves it.
	if scr, _, err := loadScreen(tdir, 20, 4); err != nil || scr.Lines()[1] != "漢字" {
		t.Fatalf("padded tail: %q %v", scr.Lines(), err)
	}
	raw = bytes.TrimRight(raw, "\x00")
	for cut := range len(raw) {
		cdir := filepath.Join(t.TempDir(), "c")
		_ = os.MkdirAll(cdir, 0o700)
		_ = os.WriteFile(filepath.Join(cdir, "tail.0"), raw[:cut], 0o600)
		if _, _, err := loadScreen(cdir, 20, 4); err != nil {
			t.Fatalf("cut %d: %v", cut, err)
		}
	}
	// Whole records cut inside an escape sequence: write the bytes as two
	// records split mid-sequence and make sure the result is right.
	cdir := t.TempDir()
	p := &persister{dir: cdir}
	p.appendLocked(recData, []byte("x\x1b[1;3"))
	p.appendLocked(recData, []byte("1mred\x1b]2;ti"))
	p.appendLocked(recData, []byte("tle\x07!"))
	p.tail.close()
	scr, _, err := loadScreen(cdir, 20, 2)
	if err != nil || scr.Lines()[0] != "xred!" || scr.Title() != "title" {
		t.Fatalf("split records: %q %q %v", scr.Lines(), scr.Title(), err)
	}
}

// Output past the tail cap triggers a checkpoint that drops the old tail,
// keeping disk use bounded, and the replay still matches.
func TestTailCapTriggersCheckpoint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := persistManager(t, dir)
	info, err := m1.Create(CreateConfig{Name: "flood", Command: "sh", Cols: 80, Rows: 10,
		Args: []string{"-c", `head -c 6000000 /dev/zero | tr '\0' 'x' | fold -w 79; echo; echo FLOOD-END; exec cat`}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := m1.Wait(ctx, info.ID, "^FLOOD-END$", -1, 0); err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(dir, string(info.ID))
	waitFor(t, "checkpoint", func() bool { _, err := os.Stat(filepath.Join(tdir, ckptFile)); return err == nil })
	var total int64
	entries, _ := os.ReadDir(tdir)
	for _, e := range entries {
		fi, _ := e.Info()
		total += fi.Size()
	}
	if total > 2*maxTailBytes+1<<20 {
		t.Fatalf("persisted %d bytes for one terminal", total)
	}
	want, _ := m1.Screen(info.ID, true)
	m2 := persistManager(t, dir)
	got, _ := m2.Screen(info.ID, true)
	if !equalScreens(got, want) {
		t.Fatalf("replayed screen differs: got %q want %q", got.Lines, want.Lines)
	}
}

// After output settles, a checkpoint is taken and nothing stays armed:
// an idle terminal has no timer and no open tail file.
func TestIdleTerminalSettles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m := persistManager(t, dir)
	info, err := m.Create(CreateConfig{Name: "idle", Command: "sh", Args: []string{"-c", "echo hi; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	term, _ := m.get(info.ID)
	waitFor(t, "output", func() bool { return latestSeq(m, info.ID) >= 0 })
	deadline := time.Now().Add(ckptSettle + 2*time.Second)
	for {
		term.persist.timerMu.Lock()
		armed := term.persist.timer != nil
		term.persist.timerMu.Unlock()
		term.scrMu.Lock()
		open := term.persist.tail != nil
		term.scrMu.Unlock()
		// The tail is closed when a checkpoint starts; the file lands
		// once it is written.
		_, err := os.Stat(filepath.Join(dir, string(info.ID), ckptFile))
		if !armed && !open && err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still armed=%v tail open=%v checkpoint %v after settling", armed, open, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Persisted screens may hold secrets: owner-only permissions, and gone
// once the terminal is deleted.
func TestPersistedScreenPermissionsAndDelete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m := persistManager(t, dir)
	info, err := m.Create(CreateConfig{Name: "s", Command: "sh", Args: []string{"-c", "echo secret; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(dir, string(info.ID))
	waitFor(t, "checkpoint", func() bool { _, err := os.Stat(filepath.Join(tdir, ckptFile)); return err == nil })
	_ = m.Write(info.ID, "more\r")
	waitFor(t, "tail", func() bool { return len(tailGens(tdir)) > 0 })
	term, err := m.get(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		// Checkpoints replace files and remove old tails. Inspect one
		// stable generation so every listed file can still be checked.
		term.ckptMu.Lock()
		defer term.ckptMu.Unlock()
		fi, err := os.Stat(tdir)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %v", fi.Mode().Perm())
		}
		entries, err := os.ReadDir(tdir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				t.Fatal(err)
			}
			if fi.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode %v", e.Name(), fi.Mode().Perm())
			}
		}
	}()
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tdir); !os.IsNotExist(err) {
		t.Fatalf("terminal dir survives delete: %v", err)
	}
}

// A ghost can be attached read-only: the client gets the saved screen,
// then EOF, as nothing more will happen in it.
func TestAttachGhost(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	m1 := persistManager(t, dir)
	info, err := m1.Create(CreateConfig{Name: "s", Command: "sh", Cols: 20, Rows: 3, Args: []string{"-c", "echo saved-text; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool { s, _ := m1.Screen(info.ID, false); return s.Lines[0] == "saved-text" })
	m2 := persistManager(t, dir)
	at, err := m2.Attach(info.ID, 80, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	defer at.Close()
	if !at.ReadOnly() {
		t.Fatal("ghost attachment is writable")
	}
	b, _ := at.Read(context.Background())
	if !strings.Contains(string(b), "saved-text") {
		t.Fatalf("ghost repaint %q", b)
	}
	if _, err := at.Read(context.Background()); err != io.EOF {
		t.Fatalf("after a ghost's screen: %v, want EOF", err)
	}
}
