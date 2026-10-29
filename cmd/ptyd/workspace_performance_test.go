package main

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
)

func TestWorkspaceStopsPreviewReadsWhileAttached(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PTYD_REDUCED_MOTION", "1")
	d := startDaemon(t, freeAddr(t))
	if _, stderr, code := d.cli(nil, "new", "-d", "idle"); code != 0 {
		t.Fatal(stderr)
	}
	if _, stderr, code := d.cli(nil, "term", "new", "--group", "idle", "-d", "-n", "worker", "--", "sh", "-c", "printf 'WORKER_READY'; exec cat"); code != 0 {
		t.Fatal(stderr)
	}
	target, _ := url.Parse("http://" + d.addr)
	forward := httputil.NewSingleHostReverseProxy(target)
	var reads atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && !strings.HasSuffix(r.URL.Path, "/attach") {
			reads.Add(1)
		}
		forward.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	addr := strings.TrimPrefix(proxy.URL, "http://")
	if _, err := client.WriteToken(addr, client.Token(d.addr)); err != nil {
		t.Fatal(err)
	}
	view := &daemon{t: t, addr: addr}
	w := view.open(140, 42, "attach", "idle")
	w.waitText("WORKER_READY")
	w.keys("\r")
	w.waitText("jk: back")
	before := reads.Load()
	time.Sleep(1700 * time.Millisecond)
	if after := reads.Load(); after != before {
		t.Fatalf("hidden browser kept polling: %d -> %d", before, after)
	}
	w.keys("jk")
	w.waitText("TERMINALS")
	w.waitText("WORKER_READY")
	if reads.Load() <= before {
		t.Fatal("preview polling did not resume")
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}
