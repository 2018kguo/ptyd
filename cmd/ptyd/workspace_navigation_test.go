package main

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceBackSequenceFraming(t *testing.T) {
	now := time.Unix(100, 0)
	var p workspaceInput
	keys := p.feed([]byte("j"), now)
	if len(keys) != 1 || keys[0].Rune != 'j' {
		t.Fatal("j navigation was delayed")
	}
	keys = p.feed([]byte("k"), now.Add(50*time.Millisecond))
	if len(keys) != 1 || !keys[0].Back {
		t.Fatal("split jk did not go back")
	}
	p.feed([]byte("j"), now)
	keys = p.feed([]byte("k"), now.Add(client.BackKeyWindow))
	if len(keys) != 1 || keys[0].Rune != 'k' || keys[0].Back {
		t.Fatal("slow keys became back")
	}
	for _, raw := range []string{"\x1b[200~jk\x1b[201~", "j\x1b[A k", "j\x1b[<0;1;2Mk", "j\x1b[106;5uk"} {
		p = workspaceInput{}
		for i := range raw {
			for _, key := range p.feed([]byte(raw[i:i+1]), now) {
				if key.Back {
					t.Fatalf("framed input became back: %q", raw)
				}
			}
		}
	}
}

func TestWorkspaceBackWhileLaunchResponseIsDelayed(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d := startDaemon(t, freeAddr(t))
	if _, err, code := d.cli(nil, "new", "-d", "delayed"); code != 0 {
		t.Fatal(err)
	}
	created, release := make(chan struct{}), make(chan struct{})
	target, _ := url.Parse("http://" + d.addr)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method == "POST" && resp.Request.URL.Path == "/terminals" {
			close(created)
			select {
			case <-release:
			case <-resp.Request.Context().Done():
			}
		}
		return nil
	}
	server := httptest.NewServer(proxy)
	defer server.Close()
	defer close(release)
	t.Setenv("PTYD_TOKEN", client.Token(d.addr))
	front := &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}
	w := front.open(120, 30, "attach", "delayed")
	w.waitText("This group is ready.")
	w.keys("ns")
	select {
	case <-created:
	case <-time.After(5 * time.Second):
		t.Fatal("launch was not received by the daemon")
	}
	w.keys("jk")
	w.waitText("Your groups")
	w.waitText("Stopped waiting.")
	w.keys("\r")
	w.waitText("shell ·")
	var all []terminal.Info
	d.do("GET", "/terminals", nil, &all)
	if len(all) != 1 || all[0].Status != terminal.StatusRunning || all[0].HumanControl {
		t.Fatalf("back stopped work or a late response took control: %+v", all)
	}
	w.keys("jk")
	w.waitText("Your groups")
	w.keys("jk")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}
func TestWorkspaceBackReturnsThroughHierarchy(t *testing.T) {
	v := workspaceView{group: "work", selected: "one", history: true, mode: wsRename, text: "unfinished", groups: []terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: "work"}}}}
	if v.back() || v.mode != wsNormal || v.group != "work" || v.text != "" {
		t.Fatal("back did not dismiss only the prompt")
	}
	if v.back() || v.group != "" || v.selected != "work" {
		t.Fatal("back did not return to the parent group")
	}
	if !v.back() {
		t.Fatal("back at root did not leave the browser")
	}
	v = workspaceView{group: "work", mode: wsSearch, query: "new", searchBefore: "original"}
	if v.back() || v.query != "original" || v.group != "work" {
		t.Fatal("canceling search lost the prior filter")
	}
}
func TestWorkspaceJKBackFromTerminalGroupAndPrompts(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d := startDaemon(t, freeAddr(t))
	if _, err, code := d.cli(nil, "new", "-d", "project"); code != 0 {
		t.Fatal(err)
	}
	id, err, code := d.cli(nil, "term", "new", "-d", "--group", "project", "-n", "agent", "--", "cat")
	if code != 0 {
		t.Fatal(err)
	}
	id = strings.TrimSpace(id)
	w := d.open(120, 30, "attach", "project")
	w.waitText("agent")
	w.keys("n")
	w.waitText("Start a terminal")
	w.keys("jk")
	w.waitNoText("Start a terminal")
	w.keys("r")
	w.waitText("Choose a new name")
	w.keys("jkxclose\r")
	w.waitNoText("Choose a new name")
	var before terminal.Info
	if code := d.do("GET", "/terminals/"+id, nil, &before); code != http.StatusOK {
		t.Fatal("canceled prompt executed queued commands against the terminal")
	}
	w.keys("\r")
	w.waitText("project / agent")
	w.keys("jk")
	w.waitText("Returned to your workspace.")
	w.keys("jk")
	w.waitText("Your groups")
	if !strings.Contains(strings.Join(w.lines(), "\n"), "project") {
		t.Fatal("back lost the parent selection")
	}
	w.keys("jk")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	var info terminal.Info
	d.do("GET", "/terminals/"+id, nil, &info)
	if info.Status != terminal.StatusRunning || info.HumanControl {
		t.Fatal("back stopped work or retained keyboard control")
	}
	fast := d.open(120, 30, "attach", "project")
	fast.waitText("agent")
	fast.keys("jkjk")
	if code := fast.waitExit(); code != 0 {
		t.Fatalf("repeated back did not cross group and root: %d", code)
	}
	split := d.open(120, 30, "attach", "project")
	split.waitText("agent")
	split.keys("jkj")
	split.waitText("Your groups")
	split.keys("k")
	if code := split.waitExit(); code != 0 {
		t.Fatalf("repeated back across reads did not leave: %d", code)
	}
}
