package main

import (
	"strings"
	"testing"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

// Every control the dock advertises does what its label says, in the group
// list and inside a group, and the dock shows all of them at once.
func TestWorkspaceAdvertisedControlsWork(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d := startDaemon(t, freeAddr(t))
	w := d.open(100, 32, "attach")
	dock := func() string {
		lines := w.lines()
		return strings.Join(lines[len(lines)-4:], "\n")
	}
	wantDock := func(labels ...string) {
		t.Helper()
		for _, label := range labels {
			w.waitText(label)
			if !strings.Contains(dock(), label) {
				t.Fatalf("dock lacks %q:\n%s", label, dock())
			}
		}
	}
	w.waitText("Make room for your next piece of work.")

	w.keys("n")
	w.waitText("Name your new group")
	w.keys("beta\r")
	w.waitText("This group is ready.")
	w.keys("jk")
	w.waitText("Your groups")
	wantDock("Enter open", "n new group", "r rename", "a archive", "x close", "h show archived", "/ search", "q quit")
	w.keys("n")
	w.waitText("Name your new group")
	w.keys("alpha\r")
	w.waitText("This group is ready.")

	// A finished shell: Enter shows history, R relaunches.
	w.keys("n")
	w.waitText("Start a terminal")
	w.keys("s")
	w.waitText("alpha / shell")
	w.keys("exit 0\r")
	w.waitText("✓ Finished")
	wantDock("Enter history", "R relaunch", "n new", "v pin", "c captain", "r rename", "m move", "x close", "a outside agents", "/ search", "jk back", "q quit")
	w.keys("R")
	w.waitText("attached")
	w.keys("jk")
	w.waitText("Returned to your workspace.")
	wantDock("Enter open")

	w.keys("v")
	w.waitText("Pane pinned.")
	wantDock("v unpin")
	w.keys("c")
	w.waitText("Captain designated.")
	wantDock("c clear captain")
	w.keys("r")
	w.waitText("Enter save")
	w.keys("renamed\r")
	w.waitText("Renamed to renamed.")
	w.keys("a")
	w.waitText("Outside agents shown.")
	wantDock("a hide outside")
	w.keys("a")
	wantDock("a outside agents")
	w.keys("/zzz\r")
	w.waitText("No terminals match")
	w.keys("/\x7f\x7f\x7f\r")
	w.waitNoText("No terminals match")
	w.keys("m")
	w.waitText("Move to another group")
	w.keys("\r")
	w.waitText("Moved to beta.")

	// The group list: archive hides a group until h shows archived ones.
	w.keys("jk")
	w.waitText("Your groups")
	w.waitText("2 groups")
	w.keys("a")
	w.waitText("Updated group archive.")
	w.waitText("1 group ")
	w.keys("h")
	wantDock("h hide archived")
	w.waitText("alpha · archived")
	w.keys("h")
	wantDock("h show archived")
	w.waitNoText("alpha · archived")
	w.keys("x")
	w.waitText("Enter confirm")
	w.keys("close\r")
	w.waitText("Closed.")
	var groups []terminal.GroupSummary
	d.do("GET", "/groups", nil, &groups)
	if len(groups) != 2 {
		t.Fatalf("x did not close the selected group: %+v", groups)
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}

// A narrow window wraps the dock onto more rows instead of dropping controls.
func TestWorkspaceDockWrapsInsteadOfDropping(t *testing.T) {
	v := workspaceView{group: "work", selected: "one", groups: []terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: "work"}}}, rows: []terminal.SessionSummary{{ID: "one", GroupID: "work", Name: "shell"}}}
	all := workspaceDock(v, 1000)[0]
	for _, size := range [][2]int{{40, 16}, {80, 24}, {120, 30}} {
		cols, rows := size[0], size[1]
		host := phosphor.New(cols, rows)
		host.Write([]byte(workspaceFrame(v, cols, rows)))
		screen := strings.Join(host.Lines(), "\n")
		for _, h := range all {
			if !strings.Contains(screen, h.key+" "+h.label) {
				t.Fatalf("%dx%d dropped %q:\n%s", cols, rows, h.key+" "+h.label, screen)
			}
		}
	}
}
