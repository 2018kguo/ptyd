package client

import (
	"context"
	"errors"
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
func TestPreviewBoundsDecodedSnapshot(t *testing.T) {
	var requests atomic.Int32
	large := phosphor.New(1024, 257).Snapshot()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write(large) }))
	defer srv.Close()
	c := API{Addr: strings.TrimPrefix(srv.URL, "http://")}
	if _, err := c.Preview(context.Background(), "term-large"); !errors.Is(err, phosphor.ErrSnapshotLimit) {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("preview did not use a single snapshot read")
	}
}

func TestPreviewRetainsProgramPaletteAndClusterModes(t *testing.T) {
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "palette", Command: "sh", Args: []string{"-c", "printf '\\033]4;1;rgb:12/34/56\\007\\033[31mX\\033[0m\\033[?2027h👩‍💻\\033[?2027l'; exec cat"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "palette fixture", func() bool { s, _ := mgr.Screen(info.ID, false); return strings.Contains(s.Lines[0], "👩") })
	view, err := (API{Addr: addr, Token: "tok"}).Preview(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	palette := view.Palette()
	cell := view.Cell(0, 0)
	if got := palette.Resolve(cell.Style.Fg, false); got != (phosphor.RGB{R: 18, G: 52, B: 86}) {
		t.Fatalf("program palette changed: %+v", got)
	}
	if view.Cell(1, 0).Text() != "👩‍💻" || view.Modes().GraphemeClustering {
		t.Fatal("snapshot lost historical cluster modes")
	}
}
