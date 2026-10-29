package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceSearchCancelsObsoleteReads(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	view := phosphor.New(80, 24)
	view.Write([]byte("fresh content"))
	snapshot := view.Snapshot()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/groups":
			json.NewEncoder(w).Encode([]terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: "work"}, Running: 1, Terminals: 1}})
		case "/overview":
			json.NewEncoder(w).Encode(terminal.Overview{Counts: terminal.OverviewCounts{Total: 1}, Sessions: []terminal.SessionSummary{{ID: "one", GroupID: "work", Name: "codex", State: terminal.OverviewRunning}}})
		case "/terminals/one":
			json.NewEncoder(w).Encode(terminal.Info{ID: "one", GroupID: "work", Name: "codex", Status: terminal.StatusRunning})
		case "/terminals/one/screen":
			if reads.Add(1) == 1 {
				close(started)
				<-r.Context().Done()
				close(canceled)
				return
			}
			w.Write(snapshot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queries := make(chan workspaceQuery, 1)
	updates := make(chan workspaceSnapshot, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		refreshWorkspace(ctx, client.API{Addr: strings.TrimPrefix(server.URL, "http://")}, queries, updates)
	}()
	queries <- workspaceQuery{Group: "work", Search: "stale", History: true}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("search did not begin")
	}
	queries <- workspaceQuery{Group: "work", Search: "fresh", History: true}
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("new query did not cancel obsolete screen read")
	}
	select {
	case result := <-updates:
		if result.Query.Search != "fresh" || result.Search == nil || !result.Search.Complete || len(result.Overview.Sessions) != 1 {
			t.Fatalf("published obsolete/incomplete result: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement query did not finish")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not stop")
	}
}
