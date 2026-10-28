package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestOverviewUnsupportedDaemon(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
	_, err := c.Overview(context.Background(), terminal.OverviewOptions{})
	if err == nil || !strings.Contains(err.Error(), "updated daemon binary") || !strings.Contains(err.Error(), "PTYD_ADDR") {
		t.Fatalf("no recovery advice: %v", err)
	}
}

func TestOverviewRequiresFilterSupportFromDaemon(t *testing.T) {
	for _, supported := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("active") != "true" {
				t.Error("client omitted active filter")
			}
			json.NewEncoder(w).Encode(terminal.Overview{ActiveOnly: supported, Sessions: []terminal.SessionSummary{}})
		}))
		c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
		_, err := c.Overview(context.Background(), terminal.OverviewOptions{ActiveOnly: true})
		server.Close()
		if supported && err != nil || !supported && !errors.Is(err, ErrOverviewCapabilities) {
			t.Fatalf("supported=%v err=%v", supported, err)
		}
	}
}
