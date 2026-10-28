package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestOverviewAuthenticationAndLimits(t *testing.T) {
	mgr := terminal.NewManager()
	defer mgr.CloseAll()
	h := New(mgr, Config{Token: "test-token"}).Handler()
	for _, tc := range []struct {
		path, token string
		code        int
	}{
		{"/overview", "", 401}, {"/overview", "wrong", 401},
		{"/overview", "test-token", 200},
		{"/overview?limit=0", "test-token", 400},
		{"/overview?limit=501", "test-token", 400},
		{"/overview?limit=all", "test-token", 400},
		{"/overview?limit=2&q=worker&tag=test", "test-token", 200},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("X-Ptyd-Token", tc.token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
		if tc.code == http.StatusOK {
			var view terminal.Overview
			if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil || view.Counts.Total != 0 || view.Sessions == nil {
				t.Fatalf("empty overview: %s (%v)", rec.Body, err)
			}
		}
	}
}
