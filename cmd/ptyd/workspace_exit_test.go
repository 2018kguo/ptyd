package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceProgramExitReturnsToGroupAndRelaunches(t *testing.T) {
	for _, code := range []int{0, 7} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			d := startDaemon(t, freeAddr(t))
			if _, err, status := d.cli(nil, "new", "-d", "exits"); status != 0 {
				t.Fatal(err)
			}
			id, err, status := d.cli(nil, "term", "new", "-d", "--group", "exits", "-n", "worker", "--", "sh", "-c", fmt.Sprintf("printf 'PROGRAM_READY\\n'; read line; printf 'PROGRAM_DONE\\n'; exit %d", code))
			if status != 0 {
				t.Fatal(err)
			}
			id = strings.TrimSpace(id)
			w := d.open(120, 30, "attach", "exits")
			w.waitText("worker")
			w.keys("\r")
			w.waitText("jk: back")
			w.waitText("PROGRAM_READY")
			w.keys("finish\r")
			w.waitText("TERMINALS")
			w.waitText("R relaunch")
			notice := "worker finished."
			if code != 0 {
				notice = "worker exited (code 7)."
			}
			w.waitText(notice)
			w.waitText("PROGRAM_DONE")
			var ended terminal.Info
			d.do("GET", "/terminals/"+id, nil, &ended)
			if ended.Status != terminal.StatusExited || ended.ExitCode == nil || *ended.ExitCode != code || ended.HumanControl || ended.Attached != 0 {
				t.Fatalf("ended view retained control or lost status: %+v", ended)
			}
			w.keys("\r")
			w.waitText("jk: back")
			w.waitText("finished")
			w.keys("xclose\rR")
			var still terminal.Info
			if d.do("GET", "/terminals/"+id, nil, &still) != http.StatusOK {
				t.Fatal("history view executed workspace commands")
			}
			w.keys("jk")
			w.waitText("TERMINALS")
			w.waitText("R relaunch")
			w.keys("R")
			w.waitText("jk: back")
			var all []terminal.Info
			d.do("GET", "/terminals", nil, &all)
			running := 0
			for _, info := range all {
				if info.Status == terminal.StatusRunning {
					running++
					if string(info.ID) == id || info.PID == ended.PID || !info.HumanControl {
						t.Fatalf("relaunch did not open a fresh process: %+v", info)
					}
				}
			}
			if running != 1 {
				t.Fatalf("relaunch has %d running terminals", running)
			}
			w.keys("finish\rq")
			w.waitText("TERMINALS")
			w.waitText("R relaunch")
			w.keys("q")
			if status := w.waitExit(); status != 0 {
				t.Fatal(status)
			}
		})
	}
}

func TestWaitExitedHonorsCancellationDuringRequest(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, `{"id":"one","status":"exited","exit_code":0}`)
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := waitExited(ctx, client.API{Addr: strings.TrimPrefix(server.URL, "http://")}, "one")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata request was not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("metadata read ignored caller cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("metadata read waited for the server after cancellation")
	}
}

func TestWorkspaceFinishedTerminalStaysActionable(t *testing.T) {
	code := 0
	for _, group := range []terminal.GroupID{"work", "moved"} {
		v := workspaceView{generation: 1, group: "work", selected: "other", query: "different", history: false}
		v.showEnded(terminal.Info{ID: "done", Name: "codex", GroupID: group, Status: terminal.StatusExited, ExitCode: &code})
		row, ok := v.row()
		if !ok || row.ID != "done" || row.State != terminal.OverviewExited || v.group != group || !v.history || v.query != "" {
			t.Fatalf("finished terminal is not actionable: %+v", v)
		}
		host := phosphor.New(40, 16)
		host.Write([]byte(workspaceFrame(v, 40, 16)))
		if !strings.Contains(host.Line(14), "R relaunch") || !strings.Contains(host.Line(14), "jk back") {
			t.Fatalf("small window hid relaunch: %s", host.Line(14))
		}
		v.refresh(workspaceSnapshot{Query: workspaceQuery{Group: group, History: true}, Overview: terminal.Overview{Sessions: []terminal.SessionSummary{{ID: "done", State: terminal.OverviewRunning}}}})
		if row, ok := v.row(); !ok || row.State != terminal.OverviewExited {
			t.Fatal("refresh from before attachment erased the finished state")
		}
	}
}

func TestWorkspaceExitDiscardsTypeaheadBeforeReturning(t *testing.T) {
	d := startDaemon(t, freeAddr(t))
	if _, err, code := d.cli(nil, "new", "-d", "queued"); code != 0 {
		t.Fatal(err)
	}
	id, err, code := d.cli(nil, "term", "new", "-d", "--group", "queued", "-n", "worker", "--", "sh", "-c", "printf 'PROGRAM_READY\\n'; read line; exit 0")
	if code != 0 {
		t.Fatal(err)
	}
	id = strings.TrimSpace(id)
	waiting, release := make(chan struct{}), make(chan struct{})
	once := sync.OnceFunc(func() { close(waiting) })
	target, _ := url.Parse("http://" + d.addr)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.Request.Method == "GET" && resp.Request.URL.Path == "/terminals/"+id {
			once()
			select {
			case <-release:
			case <-resp.Request.Context().Done():
			}
		}
		return nil
	}
	server := httptest.NewServer(proxy)
	defer server.Close()
	stopWait := sync.OnceFunc(func() { close(release) })
	defer stopWait()
	t.Setenv("PTYD_TOKEN", client.Token(d.addr))
	front := &daemon{t: t, addr: strings.TrimPrefix(server.URL, "http://")}
	w := front.open(120, 30, "attach", "queued")
	w.waitText("worker")
	w.keys("\r")
	w.waitText("jk: back")
	w.waitText("PROGRAM_READY")
	w.keys("finish\r")
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("exit did not reach metadata read")
	}
	w.keys("qxclose\r")
	stopWait()
	w.waitText("TERMINALS")
	w.waitText("R relaunch")
	var ended terminal.Info
	if d.do("GET", "/terminals/"+id, nil, &ended) != http.StatusOK || ended.Status != terminal.StatusExited {
		t.Fatal("program typeahead executed workspace commands")
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}
