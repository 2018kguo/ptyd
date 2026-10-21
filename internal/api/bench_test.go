package api

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// benchTerminals is how many live terminals the HTTP benchmarks run against.
const benchTerminals = 200

// benchServer starts benchTerminals terminals that each print 5000 lines
// and then sit idle, and returns the handler plus the last terminal.
func benchServer(b *testing.B) (http.Handler, terminal.Info) {
	b.Helper()
	mgr := terminal.NewManager()
	b.Cleanup(mgr.CloseAll)
	for i := range benchTerminals {
		_, err := mgr.Create(terminal.CreateConfig{
			Name: fmt.Sprintf("b%d", i), Command: "sh",
			Args: []string{"-c", "i=0; while [ $i -lt 5000 ]; do echo line $i; i=$((i+1)); done; exec cat"},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	var info terminal.Info
	for _, info = range mgr.List() {
		for info.LatestSeq < 4999 {
			if time.Now().After(deadline) {
				b.Fatalf("%s only reached seq %d", info.ID, info.LatestSeq)
			}
			time.Sleep(20 * time.Millisecond)
			info, _ = mgr.Get(info.ID)
		}
	}
	return New(mgr, Config{}).Handler(), info
}

func serve(b *testing.B, h http.Handler, path string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != http.StatusOK {
		b.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
	}
}

func BenchmarkHTTP(b *testing.B) {
	h, info := benchServer(b)
	id, latest := info.ID, info.LatestSeq
	b.Run("List", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serve(b, h, "/terminals")
		}
	})
	b.Run("OutputSinceRecent", func(b *testing.B) {
		path := "/terminals/" + string(id) + "/output?since=" + strconv.FormatInt(latest-10, 10)
		b.ReportAllocs()
		for b.Loop() {
			serve(b, h, path)
		}
	})
	b.Run("OutputTail100", func(b *testing.B) {
		path := "/terminals/" + string(id) + "/output?tail=100"
		b.ReportAllocs()
		for b.Loop() {
			serve(b, h, path)
		}
	})
	b.Run("Search", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serve(b, h, "/search?q=line+4999$&limit=10")
		}
	})
	b.Run("Metrics", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			serve(b, h, "/metrics")
		}
	})
}

// BenchmarkStream is the SSE endpoint end to end: a terminal prints a
// burst of streamLines lines per op and one client reads them all from
// the stream.
// streamLines is below the subscriber's buffer, so none are dropped.
func BenchmarkStream(b *testing.B) {
	const streamLines = 200
	mgr := terminal.NewManager()
	b.Cleanup(mgr.CloseAll)
	info, err := mgr.Create(terminal.CreateConfig{Name: "s", Command: "sh",
		Args: []string{"-c", "stty -echo; s=$(i=0; while [ $i -lt " + strconv.Itoa(streamLines) + " ]; do echo line $i; i=$((i+1)); done); while read x; do printf '%s\\n' \"$s\"; done"}})
	if err != nil {
		b.Fatal(err)
	}
	srv := httptest.NewServer(New(mgr, Config{}).Handler())
	b.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/terminals/"+string(info.ID)+"/stream", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.Fatal(err)
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	readLines := func(n int) {
		for got := 0; got < n; {
			l, err := br.ReadSlice('\n')
			if err != nil {
				b.Fatal(err)
			}
			if bytes.HasPrefix(l, []byte("event: line")) {
				got++
			}
		}
	}
	time.Sleep(200 * time.Millisecond) // let stty run
	b.ReportAllocs()
	for b.Loop() {
		if err := mgr.Write(info.ID, "\r"); err != nil {
			b.Fatal(err)
		}
		readLines(streamLines)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*streamLines), "ns/line")
}

// BenchmarkEncodeList is the JSON encoding of GET /terminals at N=1000.
func BenchmarkEncodeList(b *testing.B) {
	infos := make([]terminal.Info, 1000)
	now := time.Now()
	for i := range infos {
		infos[i] = terminal.Info{ID: terminal.ID(fmt.Sprintf("term-%d-abcd", i)), Name: "worker", CWD: "/home/user/src/project",
			Command: "claude", Tags: []string{"agent"}, PID: 1000 + i, Status: terminal.StatusRunning, CreatedAt: now,
			LastOutputAt: &now, IdleSeconds: 1.5, LatestSeq: 4999, Pending: "> "}
	}
	b.ReportAllocs()
	for b.Loop() {
		rec := httptest.NewRecorder()
		writeJSON(rec, http.StatusOK, infos)
	}
}
