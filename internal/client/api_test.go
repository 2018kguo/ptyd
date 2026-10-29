package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestSearchReusesConnectionsAcrossWorkerBatches(t *testing.T) {
	raw := phosphor.New(80, 24).Snapshot()
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
	overview := terminal.Overview{Counts: terminal.OverviewCounts{Total: 32}}
	for i := 0; i < 32; i++ {
		overview.Sessions = append(overview.Sessions, terminal.SessionSummary{ID: terminal.ID(fmt.Sprint(i))})
	}
	for range 6 {
		if result := c.SearchContents(context.Background(), overview, "absent"); !result.Complete {
			t.Fatal(result)
		}
	}
	if n := connections.Load(); n > 8 {
		t.Fatalf("192 reads opened %d connections", n)
	}
}

func TestTerminalTitleHasItsOwnReadBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", (1<<20)+1)) }))
	defer server.Close()
	c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
	if _, err := c.TerminalTitle(context.Background(), "one"); err == nil || !strings.Contains(err.Error(), "read budget") {
		t.Fatal("title read was not capped", err)
	}
}
