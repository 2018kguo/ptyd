package terminal

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/2018kguo/phosphor"
)

func screenText(m *Manager, id ID) string {
	scr, _ := m.Screen(id, false)
	return strings.Join(scr.Lines, "\n")
}

// The daemon tracks the window a full-screen program draws, including the
// alternate screen and what is left after the program exits it.
func TestScreenTracksFullScreenProgram(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name: "fs", Command: "sh", Cols: 40, Rows: 10,
		Args: []string{"-c", `printf 'before\r\n\033[?1049h\033[2J\033[3;5Hin alt\033[31m red'; read x; printf '\033[?1049lafter'; exec cat`},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	waitFor(t, "alt screen", func() bool { return strings.Contains(screenText(m, info.ID), "in alt red") })
	scr, _ := m.Screen(info.ID, false)
	if !scr.AltScreen || scr.Lines[2] != "    in alt red" || scr.CursorY != 2 {
		t.Fatalf("alt screen state: %+v", scr)
	}
	// The repaint reproduces it on a fresh emulator of the same size.
	ansi, _ := m.ScreenANSI(info.ID, true)
	fresh := phosphor.New(40, 10)
	fresh.Write(ansi)
	if !slices.Equal(fresh.Lines(), scr.Lines) {
		t.Fatalf("repaint gives %q, want %q", fresh.Lines(), scr.Lines)
	}
	_ = m.Write(info.ID, "\r")
	waitFor(t, "main screen", func() bool {
		return strings.Contains(screenText(m, info.ID), "beforeafter") || strings.Contains(screenText(m, info.ID), "after")
	})
	scr, _ = m.Screen(info.ID, false)
	if scr.AltScreen || scr.Lines[0] != "before" {
		t.Fatalf("main screen state: %+v", scr)
	}
	// Resizing reaches the emulator too.
	if err := m.Resize(info.ID, 20, 5); err != nil {
		t.Fatal(err)
	}
	if scr, _ = m.Screen(info.ID, false); scr.Cols != 20 || scr.Rows != 5 {
		t.Fatalf("size after resize %dx%d", scr.Cols, scr.Rows)
	}
}

// A real full-screen program: less draws a page on the alternate screen.
func TestScreenWithLess(t *testing.T) {
	if _, err := exec.LookPath("less"); err != nil {
		t.Skip("less not installed")
	}
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name: "less", Command: "sh", Cols: 60, Rows: 12,
		Args: []string{"-c", "seq 1 100 | less"},
		Env:  map[string]string{"LESS": "", "TERM": "xterm-256color"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	waitFor(t, "first page", func() bool { return strings.HasPrefix(screenText(m, info.ID), "1\n2\n3") })
	_ = m.Write(info.ID, " ")
	waitFor(t, "second page", func() bool {
		scr, _ := m.Screen(info.ID, false)
		return scr.Lines[0] == "12" && scr.Lines[10] == "22"
	})
}
