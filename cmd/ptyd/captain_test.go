package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceCaptainIsSharedWithCLIAndSurvivesReopen(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PTYD_REDUCED_MOTION", "1")
	d := startDaemon(t, freeAddr(t))
	for _, name := range []string{"work", "elsewhere"} {
		if _, stderr, code := d.cli(nil, "new", "-d", name); code != 0 {
			t.Fatal(stderr)
		}
	}
	ids := []string{}
	for _, name := range []string{"leader", "worker"} {
		id, stderr, code := d.cli(nil, "term", "new", "--group", "work", "-d", "-n", name, "--", "sh", "-c", "printf '"+name+" ready'; exec cat")
		if code != 0 {
			t.Fatal(stderr)
		}
		ids = append(ids, strings.TrimSpace(id))
	}
	w := d.open(180, 40, "attach", "work")
	w.waitText("leader ready")
	w.keys("c")
	w.waitText("Captain designated.")
	w.waitText("★ Captain")
	read := func(want string) captainSelection {
		t.Helper()
		out, stderr, code := d.cli(nil, "captain", "--group", "work", "--json")
		var result captainSelection
		if code != 0 || json.Unmarshal([]byte(out), &result) != nil || string(result.TerminalID) != want {
			t.Fatalf("captain: %s %s (%d)", out, stderr, code)
		}
		return result
	}
	if got := read(ids[0]); !got.Available || got.Name != "leader" {
		t.Fatal(got)
	}
	w.keys("v")
	w.waitText("Pane pinned.")
	if _, stderr, code := d.cli(nil, "captain", "--group", "work", ids[1]); code != 0 {
		t.Fatal(stderr)
	}
	w.waitText("★ worker")
	w.keys("j\r")
	w.waitText("work / ★ worker · Captain")
	w.keys("jk")
	w.waitText("TERMINALS")
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	again := d.open(180, 40, "attach", "work")
	again.waitText("★ worker")
	again.keys("q")
	if code := again.waitExit(); code != 0 {
		t.Fatal(code)
	}
	if _, stderr, code := d.cli(nil, "term", "move", "--group", "work", ids[1], "elsewhere"); code != 0 {
		t.Fatal(stderr)
	}
	if got := read(ids[1]); got.Available || !strings.Contains(got.Reason, "moved") {
		t.Fatal(got)
	}
	if _, _, code := d.cli(nil, "captain", "--group", "work", ids[1]); code == 0 {
		t.Fatal("assigned captain from another group")
	}
	if _, stderr, code := d.cli(nil, "captain", "--group", "work", "--clear"); code != 0 {
		t.Fatal(stderr)
	}
	read("")
}

func TestWorkspaceCaptainSavesMergeWithSevenPins(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, b := loadWorkspaceLayouts("127.0.0.1:41996"), loadWorkspaceLayouts("127.0.0.1:41996")
	ids := []terminal.ID{"one", "two", "three", "four", "five", "six", "seven"}
	if err := a.save("work", ids); err != nil {
		t.Fatal(err)
	}
	if err := b.saveCaptain("work", "one"); err != nil {
		t.Fatal(err)
	}
	if err := a.save("work", ids[:6]); err != nil {
		t.Fatal(err)
	}
	loaded := loadWorkspaceLayouts("127.0.0.1:41996")
	if len(loaded.pins["work"]) != 6 || loaded.captains["work"] != "one" {
		t.Fatal("concurrent clients overwrote pins or captain")
	}
	if err := b.saveCaptain("work", ""); err != nil {
		t.Fatal(err)
	}
	loaded = loadWorkspaceLayouts("127.0.0.1:41996")
	if len(loaded.pins["work"]) != 6 || loaded.captains["work"] != "" {
		t.Fatal("clearing captain removed pins")
	}
}
