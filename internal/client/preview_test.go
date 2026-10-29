package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestPreviewReadsExistingExportsWithoutAttaching(t *testing.T) {
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "preview", Command: "sh", Args: []string{"-c", "printf '\\033[48;2;40;42;54mCOLOR\\033[0m\\n'; exec cat"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "fixture", func() bool {
		s, _ := mgr.Screen(info.ID, false)
		return strings.Contains(strings.Join(s.Lines, "\n"), "COLOR")
	})
	c := API{Addr: addr, Token: "tok"}
	view, err := c.Preview(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Cell(0, 0).Rune != 'C' || view.Cell(0, 0).Style.Bg != phosphor.RGBColor(40, 42, 54) {
		t.Fatal("preview lost style or content")
	}
	after, _ := mgr.Get(info.ID)
	screen, _ := mgr.Screen(info.ID, false)
	if after.Attached != 0 || after.HumanControl || after.PID != info.PID || screen.Cols != 80 || screen.Rows != 24 {
		t.Fatal("preview changed live terminal ownership or geometry")
	}
}
func TestPreviewRejectsExcessiveScreenBeforeReadingExport(t *testing.T) {
	var exports atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "ansi" {
			exports.Add(1)
			return
		}
		fmt.Fprint(w, `{"cols":65535,"rows":65535}`)
	}))
	defer srv.Close()
	c := API{Addr: strings.TrimPrefix(srv.URL, "http://")}
	if _, err := c.Preview(context.Background(), "term-large"); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatal(err)
	}
	if exports.Load() != 0 {
		t.Fatal("oversized preview still requested export")
	}
}
