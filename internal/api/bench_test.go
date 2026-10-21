package api

import (
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
