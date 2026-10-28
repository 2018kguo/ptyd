package client

import (
	"context"
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
