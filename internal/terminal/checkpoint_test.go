package terminal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
	p.tail.Close()
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
	if fi, _ := os.Stat(tdir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(tdir)
	for _, e := range entries {
		if fi, _ := e.Info(); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", e.Name(), fi.Mode().Perm())
		}
	}
	if err := m.Close(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tdir); !os.IsNotExist(err) {
		t.Fatalf("terminal dir survives delete: %v", err)
	}
}

// A ghost can be attached read-only: the client sees the saved screen.
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
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := at.Read(ctx); err != context.DeadlineExceeded {
		t.Fatalf("ghost attachment should stay open until the client leaves: %v", err)
	}
}
