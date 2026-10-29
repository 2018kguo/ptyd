package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func clickWindowText(t *testing.T, w *window, text string, double bool) {
	t.Helper()
	for y, line := range w.lines() {
		if at := strings.Index(line, text); at >= 0 {
			x := textWidth(line[:at]) + 1
			packet := fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", x, y+1, x, y+1)
			if double {
				packet += packet
			}
			w.keys(packet)
			return
		}
	}
	t.Fatalf("click target %q is not visible", text)
}

func TestWorkspaceMouseSelectOpenAndBack(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PTYD_REDUCED_MOTION", "1")
	d := startDaemon(t, freeAddr(t))
	if _, err, code := d.cli(nil, "new", "-d", "mouse"); code != 0 {
		t.Fatal(err)
	}
	ids := []string{}
	for _, name := range []string{"alpha", "beta"} {
		script := "printf '" + strings.ToUpper(name) + "_READY\\r\\n'; exec cat"
		if name == "beta" {
			script = "printf '\\033[?1000h\\033[?1006hBETA_READY\\r\\n'; exec cat"
		}
		id, err, code := d.cli(nil, "term", "new", "-d", "--group", "mouse", "-n", name, "--", "sh", "-c", script)
		if code != 0 {
			t.Fatal(err)
		}
		ids = append(ids, strings.TrimSpace(id))
	}
	w := d.open(140, 42, "attach", "mouse")
	w.waitText("ALPHA_READY")
	clickWindowText(t, w, "beta", false)
	w.waitText("BETA_READY")
	var selected terminal.Info
	d.do("GET", "/terminals/"+ids[1], nil, &selected)
	if selected.HumanControl || selected.Attached != 0 {
		t.Fatal("single click took control")
	}
	w.keys(" ")
	clickWindowText(t, w, "beta", true)
	w.waitText("jk: back")
	clickWindowText(t, w, "jk: back", false)
	w.waitText("TERMINALS")
	w.waitText("BETA_READY")
	clickWindowText(t, w, "jk back", false)
	w.waitText("Your groups")
	w.waitText("ALPHA_READY")
	w.waitText("BETA_READY")
	w.keys(" ")
	clickWindowText(t, w, "ALPHA_READY", true)
	w.waitText("jk: back")
	w.keys("jk")
	w.waitText("TERMINALS")
	clickWindowText(t, w, "q quit", false)
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	w.mu.Lock()
	modes := w.screen.Modes()
	w.mu.Unlock()
	if modes.Mouse != 0 || modes.MouseEncoding != 0 {
		t.Fatal("workspace left mouse capture enabled")
	}
	for _, id := range ids {
		var info terminal.Info
		d.do("GET", "/terminals/"+id, nil, &info)
		if info.Status != terminal.StatusRunning || info.HumanControl {
			t.Fatal("mouse back ended a program")
		}
	}
}

func TestWorkspaceMouseSceneRespectsDialogsAndInput(t *testing.T) {
	v := workspaceView{group: "work", selected: "one", color: true, groups: []terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: "work"}}}, rows: []terminal.SessionSummary{{ID: "one", GroupID: "work", Name: "codex", State: terminal.OverviewRunning}}, snapshot: &workspaceSnapshot{Query: workspaceQuery{Group: "work"}, Panes: []workspacePane{{Row: terminal.SessionSummary{ID: "one", GroupID: "work", Name: "codex"}, Screen: phosphor.New(80, 24)}}}}
	for _, mode := range []workspaceMode{wsNormal, wsHelp, wsLaunch, wsSearch, wsCommand} {
		v.mode = mode
		scene := renderWorkspace(v, 120, 30)
		for _, h := range scene.Hits {
			if h.Rect.x < 0 || h.Rect.y < 0 || h.Rect.x+h.Rect.w > 120 || h.Rect.y+h.Rect.h > 30 {
				t.Fatal("hit outside frame", h)
			}
			if (mode == wsSearch || mode == wsCommand) && h.Rect.y == 28 {
				t.Fatal("input field covers an active footer button")
			}
			if (mode == wsHelp || mode == wsLaunch) && h.Kind != workspaceHitKey {
				t.Fatal("dialog left underlying content clickable")
			}
		}
	}
	for _, group := range []terminal.GroupID{"", "work"} {
		for _, mode := range []workspaceMode{wsSearch, wsCommand} {
			v.group, v.mode = group, mode
			enter, back := false, false
			for _, h := range renderWorkspace(v, 120, 30).Hits {
				if h.Rect.y == 26 && h.Kind == workspaceHitKey {
					enter = enter || h.Key.Rune == '\r'
					back = back || h.Key.Back
				}
			}
			if !enter || !back {
				t.Fatalf("group %q mode %d lost visible input actions", group, mode)
			}
		}
	}
	var mouse workspaceMouse
	h := workspaceHit{Rect: workspaceRect{0, 0, 5, 2}, Kind: workspaceHitTerminal, ID: "one"}
	now := time.Unix(100, 0)
	if _, double, ok := mouse.click(client.MouseEvent{X: 1, Y: 1}, []workspaceHit{h}, now); !ok || double {
		t.Fatal("first click opened")
	}
	if _, double, ok := mouse.click(client.MouseEvent{X: 1, Y: 1}, []workspaceHit{h}, now.Add(100*time.Millisecond)); !ok || !double {
		t.Fatal("second click did not open")
	}
}
