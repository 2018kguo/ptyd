package main

import (
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspacePinnedPanesStayLiveAcrossSelectionAndReturn(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PTYD_REDUCED_MOTION", "1")
	d := startDaemon(t, freeAddr(t))
	if _, err, code := d.cli(nil, "new", "-d", "panes"); code != 0 {
		t.Fatal(err)
	}
	ids := []string{}
	for _, name := range []string{"alpha", "beta"} {
		id, err, code := d.cli(nil, "term", "new", "-d", "--group", "panes", "-n", name, "--", "sh", "-c", "printf '"+strings.ToUpper(name)+"-READY\\n'; exec cat")
		if code != 0 {
			t.Fatal(err)
		}
		ids = append(ids, strings.TrimSpace(id))
	}
	w := d.open(140, 42, "attach", "panes")
	w.waitText("ALPHA-READY")
	w.keys("v")
	w.waitText("Pane pinned.")
	w.keys("j")
	w.waitText("BETA-READY")
	w.waitText("ALPHA-READY")
	w.keys("v")
	w.waitText("2 pinned")
	for i, id := range ids {
		data := []string{"ALPHA-UPDATED", "BETA-UPDATED"}[i]
		d.do("POST", "/terminals/"+id+"/write", map[string]any{"data": data, "submit": true}, nil)
	}
	w.waitText("ALPHA-UPDATED")
	w.waitText("BETA-UPDATED")
	for _, id := range ids {
		var info terminal.Info
		d.do("GET", "/terminals/"+id, nil, &info)
		var screen terminal.Screen
		d.do("GET", "/terminals/"+id+"/screen", nil, &screen)
		if info.Attached != 0 || info.HumanControl || screen.Cols != 80 || screen.Rows != 24 {
			t.Fatal("pinned pane claimed control or resized a process")
		}
	}
	w.keys("jk")
	w.waitText("Your groups")
	w.keys("\r")
	w.waitText("2 pinned")
	w.waitText("ALPHA-UPDATED")
	w.waitText("BETA-UPDATED")
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	again := d.open(140, 42, "attach", "panes")
	again.waitText("2 pinned")
	again.waitText("ALPHA-UPDATED")
	again.waitText("BETA-UPDATED")
	again.keys("q")
	if code := again.waitExit(); code != 0 {
		t.Fatal(code)
	}
}

func TestWorkspaceIndependentBrowsersKeepOtherGroupPins(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const addr = "127.0.0.1:41998"
	a, b := loadWorkspaceLayouts(addr), loadWorkspaceLayouts(addr)
	if err := a.save("group-a", []terminal.ID{"one"}); err != nil {
		t.Fatal(err)
	}
	if err := b.save("group-b", []terminal.ID{"two"}); err != nil {
		t.Fatal(err)
	}
	loaded := loadWorkspaceLayouts(addr)
	if len(loaded.pins["group-a"]) != 1 || len(loaded.pins["group-b"]) != 1 {
		t.Fatalf("one browser overwrote another group's pins: %+v", loaded.pins)
	}
	lock, err := os.OpenFile(a.path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err = b.save("group-b", nil); err == nil {
		t.Fatal("simultaneous save ignored the other browser's lock")
	}
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err = b.save("group-b", nil); err != nil {
		t.Fatal(err)
	}
	loaded = loadWorkspaceLayouts(addr)
	if len(loaded.pins["group-a"]) != 1 || len(loaded.pins["group-b"]) != 0 {
		t.Fatalf("unpin removed another group's pins: %+v", loaded.pins)
	}
}
func TestWorkspacePinLayoutsAreBoundedAndPrivate(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	addr := "127.0.0.1:41997"
	l := loadWorkspaceLayouts(addr)
	if err := l.save("group-layout", []terminal.ID{"term-one", "term-two"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(l.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("pane layout is not private: %v", err)
	}
	loaded := loadWorkspaceLayouts(addr)
	if len(loaded.pins["group-layout"]) != 2 {
		t.Fatal("pane layout did not survive reopening")
	}
	if err = os.WriteFile(l.path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := loadWorkspaceLayouts(addr)
	if bad.notice == "" {
		t.Fatal("corrupt pane preferences were silently discarded")
	}
	if err = bad.save("group-layout", nil); err == nil {
		t.Fatal("save overwrote unreadable preferences")
	}
}
