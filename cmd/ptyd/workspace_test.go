package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceCreateLaunchDetachAndReattach(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d := startDaemon(t, freeAddr(t))
	w := d.open(120, 28, "attach")
	w.waitText("Make room for your next piece of work.")
	w.keys("n")
	w.waitText("Name your new group")
	w.keys("release\r")
	w.waitText("This group is ready.")
	w.keys("n")
	w.waitText("Start a terminal")
	w.keys("s")
	w.waitText("release / shell")
	w.keys("printf '\\033[48;2;40;42;54mCOMPOSER\\033[0m\\n'\r")
	w.waitText("COMPOSER")
	// The actual terminal output keeps its RGB background; the footer occupies
	// its own row rather than being erased by the app's next repaint.
	w.mu.Lock()
	colored := false
	for y := 0; y < 27; y++ {
		for x := 0; x < 120; x++ {
			c := w.screen.Cell(x, y)
			if c.Rune == 'C' && c.Style.Bg == phosphor.RGBColor(40, 42, 54) {
				colored = true
			}
		}
	}
	w.mu.Unlock()
	if !colored {
		t.Fatal("attached view lost the composer background")
	}
	w.keys("jk")
	w.waitText("LIVE PREVIEW")
	w.waitText("Returned to your workspace.")
	var terms []terminal.Info
	d.do("GET", "/terminals", nil, &terms)
	if len(terms) != 1 || terms[0].Status != terminal.StatusRunning || terms[0].HumanControl {
		t.Fatalf("return stopped work or kept control: %+v", terms)
	}
	pid, id := terms[0].PID, terms[0].ID
	w.keys("v")
	w.waitText("viewing")
	w.keys("printf SHOULD_NOT_BE_SENT\r")
	w.keys("jk")
	w.waitText("Returned to your workspace.")
	var screen terminal.Screen
	d.do("GET", "/terminals/"+string(id)+"/screen", nil, &screen)
	if strings.Contains(strings.Join(screen.Lines, "\n"), "SHOULD_NOT_BE_SENT") {
		t.Fatal("watcher typed into terminal")
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	again := d.open(80, 24, "attach", "release")
	again.waitText("shell")
	again.keys("\r")
	again.waitText("release / shell")
	again.keys("printf REATTACHED\\n\r")
	again.waitText("REATTACHED")
	again.keys("jk")
	again.waitText("Returned to your workspace.")
	again.keys("q")
	if code := again.waitExit(); code != 0 {
		t.Fatal(code)
	}
	var info terminal.Info
	d.do("GET", "/terminals/"+string(id), nil, &info)
	if info.PID != pid || info.Status != terminal.StatusRunning || info.HumanControl {
		t.Fatalf("reattach changed process: %+v", info)
	}
}

func TestWorkspaceSearchPasteAndBackspace(t *testing.T) {
	d := startDaemon(t, freeAddr(t))
	for _, name := range []string{"alpha", "beta"} {
		if _, err, code := d.cli(nil, "new", "-d", name); code != 0 {
			t.Fatal(err)
		}
	}
	w := d.open(80, 24, "attach")
	w.waitText("alpha")
	w.keys("/\x1b[200~betax\x1b[201~")
	w.waitText("No groups match") // no matching group yet
	w.keys("\x7f")
	w.waitText("beta")
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		text := strings.Join(w.lines(), "\n")
		if !strings.Contains(text, "alpha") && !strings.Contains(text, "betax") {
			break
		}
		if time.Now().After(end) {
			t.Fatalf("editing did not update search:\n%s", text)
		}
	}
	w.keys("\r")
	w.keys("\x1b[200~qxn\x1b[201~")
	time.Sleep(150 * time.Millisecond)
	select {
	case <-w.exited:
		t.Fatal("normal-mode paste ran a command")
	default:
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}

func TestGroupCLIContextSurvivesMoveAndDaemonRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "persist")
	d := startDaemon(t, freeAddr(t), "--persist-dir", dir)
	groups := []string{}
	for _, name := range []string{"implementation", "review"} {
		id, err, code := d.cli(nil, "new", "-d", "--cwd", t.TempDir(), name)
		if code != 0 {
			t.Fatal(err)
		}
		groups = append(groups, strings.TrimSpace(id))
	}
	script := fmt.Sprintf("%s term new -d --restore none -n child -- cat; exec cat", ptydBin)
	id, stderr, code := d.cli(nil, "term", "new", "-d", "--restore", "none", "--group", groups[0], "-n", "manager", "--", "sh", "-c", script)
	if code != 0 {
		t.Fatal(stderr)
	}
	id = strings.TrimSpace(id)
	var list []terminal.Info
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		d.do("GET", "/terminals", nil, &list)
		if len(list) == 2 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("child did not inherit daemon address")
		}
	}
	for _, item := range list {
		if string(item.GroupID) != groups[0] {
			t.Fatalf("child escaped inherited group: %+v", item)
		}
	}
	if _, err, code := d.cli(nil, "term", "move", id, groups[1]); code != 0 {
		t.Fatal(err)
	}
	stale := []string{"PTYD_GROUP_ID=" + groups[0], "PTYD_TERMINAL_ID=" + id}
	child, stderr, code := d.cli(stale, "term", "new", "-d", "--restore", "none", "-n", "reviewer", "--", "cat")
	if code != 0 {
		t.Fatal(stderr)
	}
	var info terminal.Info
	d.do("GET", "/terminals/"+strings.TrimSpace(child), nil, &info)
	if string(info.GroupID) != groups[1] {
		t.Fatalf("stale env overrode current membership: %+v", info)
	}
	// Names are scoped, while full ids keep existing agent integrations stable.
	if _, err, code := d.cli(stale, "term", "rename", "reviewer", "renamed"); code != 0 {
		t.Fatal(err)
	}
	if _, err, code := d.cli(nil, "term", "rename", "--group", groups[1], "renamed", "reviewed"); code != 0 {
		t.Fatal(err)
	}
	d.stop()
	d = startDaemon(t, d.addr, "--persist-dir", dir)
	out, err, code := d.cli(nil, "term", "ls", "--json", "--group", groups[1])
	if code != 0 {
		t.Fatal(err)
	}
	if e := json.Unmarshal([]byte(out), &list); e != nil {
		t.Fatal(e)
	}
	if len(list) != 2 {
		t.Fatalf("lost membership across restart: %s", out)
	}
	for _, info := range list {
		if string(info.GroupID) != groups[1] {
			t.Fatal(info)
		}
	}
	// A name resembling an id must not bypass the inherited group scope.
	if _, err, code := d.cli(nil, "term", "new", "-d", "--group", groups[0], "-n", "term-review", "--", "cat"); code != 0 {
		t.Fatal(err)
	}
	if _, err, code := d.cli(stale, "term", "kill", "term-review"); code == 0 || !strings.Contains(err, "no terminal") {
		t.Fatalf("ID-like name escaped group scope: %s %d", err, code)
	}

	// CLI group IDs take precedence over another group's deliberately matching name.
	var shadow terminal.Group
	if code := d.do("POST", "/groups", terminal.GroupConfig{Name: groups[0], CWD: t.TempDir()}, &shadow); code != 201 {
		t.Fatal(code)
	}
	c := client.API{Addr: d.addr, Token: client.Token(d.addr)}
	found, e := c.FindGroup(context.Background(), groups[0])
	if e != nil || string(found.ID) != groups[0] {
		t.Fatalf("name shadowed stable id: %+v %v", found, e)
	}
}

func TestWorkspaceRejectsStaleSnapshotsAndScrollsMoveMenu(t *testing.T) {
	v := workspaceView{group: "group-new", query: "current", selected: "chosen", history: true}
	v.refresh(workspaceSnapshot{Query: workspaceQuery{Group: "group-old", History: true}, Overview: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "wrong"}}}})
	if v.snapshot != nil || len(v.rows) != 0 || v.selected != "chosen" {
		t.Fatal("stale async view replaced current navigation")
	}
	v.mode = wsMove
	v.color = true
	v.aux = 11
	for i := 0; i < 15; i++ {
		v.groups = append(v.groups, terminal.GroupSummary{Group: terminal.Group{ID: terminal.GroupID(fmt.Sprint(i)), Name: fmt.Sprintf("destination-%02d", i)}})
	}
	screen := phosphor.New(100, 26)
	screen.Write([]byte(workspaceFrame(v, 100, 26)))
	text := strings.Join(screen.Lines(), "\n")
	if !strings.Contains(text, "› destination-11") {
		t.Fatalf("selected move destination off screen:\n%s", text)
	}
	for _, size := range [][2]int{{80, 24}, {40, 16}, {30, 10}, {1, 1}} {
		frame := workspaceFrame(v, size[0], size[1])
		model := phosphor.New(size[0], size[1])
		model.Write([]byte(frame))
		if size[0] >= 40 && !strings.Contains(strings.Join(model.Lines(), "\n"), "Move to another group") {
			t.Fatalf("modal missing at %v", size)
		}
	}
}

func TestWorkspaceCommandsResolveNamesWithoutGuessing(t *testing.T) {
	d := startDaemon(t, freeAddr(t))
	_, stderr, code := d.cli(nil, "new", "-d", "work")
	if code != 0 {
		t.Fatal(stderr)
	}
	for range 2 {
		if _, stderr, code = d.cli(nil, "term", "new", "-d", "--group", "work", "-n", "duplicate", "--", "cat"); code != 0 {
			t.Fatal(stderr)
		}
	}
	w := d.open(100, 26, "attach", "work")
	w.waitText("duplicate")
	w.keys(":b duplicate\r")
	w.waitText("matches 2 terminals")
	w.keys("q")
	if code = w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}

func TestWorkspaceShowsIncompleteOutsideDiscovery(t *testing.T) {
	v := workspaceView{group: "work", outside: true, snapshot: &workspaceSnapshot{Overview: terminal.Overview{
		DiscoveryIssues: []native.Issue{{Provider: native.Claude, Message: "provider failed"}}, DiscoveryTruncated: true,
	}}}
	model := phosphor.New(120, 28)
	model.Write([]byte(workspaceFrame(v, 120, 28)))
	text := strings.Join(model.Lines(), "\n")
	if !strings.Contains(text, "Outside listing incomplete") || !strings.Contains(text, "provider failed") {
		t.Fatalf("partial discovery looks complete:\n%s", text)
	}
}

func TestGroupsStartLocalDaemonAutomatically(t *testing.T) {
	d := &daemon{t: t, addr: freeAddr(t)}
	id, stderr, code := d.cli(nil, "new", "-d", "automatic")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(id), "group-") {
		t.Fatalf("auto-start failed: %s %s %d", id, stderr, code)
	}
	var metrics terminal.Metrics
	d.do("GET", "/metrics", nil, &metrics)
	if metrics.PID <= 0 {
		t.Fatal("no owned daemon pid")
	}
	process, err := os.FindProcess(metrics.PID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = process.Signal(syscall.SIGTERM)
		c := client.API{Addr: d.addr}
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			err := c.DoContext(ctx, "GET", "/healthz", nil, nil)
			cancel()
			if err != nil {
				return
			}
		}
		t.Error("owned auto-start daemon did not stop")
	})
	out, stderr, code := d.cli(nil, "ls", "--json")
	if code != 0 || !strings.Contains(out, "automatic") {
		t.Fatalf("new daemon group missing: %s %s %d", out, stderr, code)
	}
	w := d.open(80, 24)
	w.waitText("Your groups")
	w.waitText("automatic")
	w.keys("q")
	if code = w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}

func TestGroupListKeepsOldInboxBehindHistory(t *testing.T) {
	v := workspaceView{groups: []terminal.GroupSummary{
		{Group: terminal.Group{ID: terminal.DefaultGroup, Name: "Inbox"}, Terminals: 80},
		{Group: terminal.Group{ID: "work", Name: "work"}},
		{Group: terminal.Group{ID: "archived", Name: "archived", Archived: true}},
	}}
	if groups := v.visibleGroups(); len(groups) != 1 || groups[0].ID != "work" {
		t.Fatalf("old history displaced live groups: %+v", groups)
	}
	v.history = true
	if groups := v.visibleGroups(); len(groups) != 3 {
		t.Fatalf("history disappeared: %+v", groups)
	}
	v.history = false
	v.groups[0].Running = 1
	if groups := v.visibleGroups(); len(groups) != 2 {
		t.Fatalf("active Inbox hidden: %+v", groups)
	}
}
