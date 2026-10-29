package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestContentSearchJoinsSoftWrapsWithoutJoiningHardBreaks(t *testing.T) {
	for _, test := range []struct {
		input, query, source string
		rows                 int
	}{
		{"abcdefghijkl", "ghij", "screen", 3},
		{"abcdefghijklmnopq", "ghij", "scrollback", 2},
		{"abc雪", "abc雪", "screen", 3},
		{"abc😀", "abc😀", "screen", 3},
		{"abcdef  ghij", "f  g", "screen", 3},
		{"abcdef  ghijklmnop", "f  g", "scrollback", 2},
		{"abcdef\r\nghij", "fgh", "", 3},
		{"\x1b[?2027h雪é雪éABCD", "éAB", "screen", 3},
	} {
		view := phosphor.New(8, test.rows)
		view.Write([]byte(test.input))
		hit, err := MatchContents(terminal.SessionSummary{}, view, test.query)
		if err != nil {
			t.Fatal(err)
		}
		if test.source == "" {
			if hit != nil {
				t.Fatalf("hard break became a match: %+v", hit)
			}
		} else if hit == nil || hit.Source != test.source || !strings.Contains(hit.Text, test.query) {
			t.Fatalf("%q / %q: %+v", test.input, test.query, hit)
		}
	}
}

func TestContentSearchKeepsInventoryFailuresIncomplete(t *testing.T) {
	for _, overview := range []terminal.Overview{
		{DiscoveryTruncated: true},
		{ProcessIssue: "process inventory failed"},
		{DiscoveryIssues: []native.Issue{{Message: "native inventory failed"}}},
	} {
		if got := (API{}).SearchContents(context.Background(), overview, "needle"); got.Complete {
			t.Fatalf("failed inventory certified complete: %+v", overview)
		}
	}
}

func TestContentSearchOmitsWideWrapSpacers(t *testing.T) {
	for _, input := range []string{"abc雪", "abc😀", "abc雪defgh", "abc😀defgh"} {
		for _, rows := range []int{2, 3} {
			view := phosphor.New(4, rows)
			view.Write([]byte(input))
			query := []rune(input)[:4]
			if hit, err := MatchContents(terminal.SessionSummary{}, view, string(query)); hit == nil || err != nil {
				t.Fatalf("wide wrap missed %q in %d rows; history %q, screen %q", input, rows, view.ScrollbackLines(), view.Lines())
			}
		}
	}
}

func TestContentSearchFindsScreenHistoryAndLiteralUnicode(t *testing.T) {
	view := phosphor.New(80, 4)
	view.Write([]byte("invoice ARCHIVE_TOKEN\r\none\r\ntwo\r\nthree\r\n\x1b[32mvisible [build] 雪\x1b[0m"))
	row := terminal.SessionSummary{ID: "term-one", GroupID: "work", Name: "codex", CWD: "/project"}
	for _, test := range []struct {
		query, source string
		row           int
	}{{"archive_token", "scrollback", -1}, {"[BUILD] 雪", "screen", 3}, {"codex", "session", -1}, {"no such phrase", "", 0}} {
		hit, err := MatchContents(row, view, test.query)
		if err != nil {
			t.Fatal(err)
		}
		if test.source == "" {
			if hit != nil {
				t.Fatal("invented a match", hit)
			}
			continue
		}
		if hit == nil || hit.Source != test.source || hit.ScreenRow != test.row || !strings.Contains(strings.ToLower(hit.Text), strings.ToLower(test.query)) {
			t.Fatalf("%q: %+v", test.query, hit)
		}
	}
	long := strings.Repeat("İ", 180) + " NEEDLE"
	hit := contentMatch(row, "screen", long, "needle", 0)
	if !strings.Contains(hit.Text, "NEEDLE") {
		t.Fatal("case-fold byte changes corrupted snippet", hit.Text)
	}
}

func TestContentSearchReadsFreshSnapshotsAndReportsPartialCoverage(t *testing.T) {
	view := phosphor.New(80, 4)
	view.Write([]byte("fresh_token"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "snapshot" {
			t.Error("not an exact snapshot read")
		}
		if strings.Contains(r.URL.Path, "unreadable") {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write(view.Snapshot())
	}))
	defer server.Close()
	c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
	overview := terminal.Overview{Counts: terminal.OverviewCounts{Total: 2}, Sessions: []terminal.SessionSummary{{ID: "one", Name: "claude"}, {ID: "unreadable", Name: "codex"}}}
	got := c.SearchContents(context.Background(), overview, "fresh_token")
	if got.Complete || got.Checked != 1 || got.Candidates != 2 || len(got.Issues) != 1 || len(got.Matches) != 1 || got.Matches[0].TerminalID != "one" {
		t.Fatalf("coverage concealed a failure: %+v", got)
	}
	// No newline or LatestSeq change: a full-screen app can replace its text.
	view.Write([]byte("\x1b[H\x1b[2Kreplacement_token"))
	overview.Sessions = overview.Sessions[:1]
	overview.Counts.Total = 1
	got = c.SearchContents(context.Background(), overview, "replacement_token")
	if !got.Complete || len(got.Matches) != 1 {
		t.Fatalf("reused stale terminal contents: %+v", got)
	}
	overview.Truncated = true
	overview.Counts.Total = 600
	got = c.SearchContents(context.Background(), overview, "replacement_token")
	if got.Complete || got.Candidates != 600 {
		t.Fatal("capped listing claimed full coverage")
	}
}

func TestContentSearchCapsLargeHistoryBoundary(t *testing.T) {
	view := phosphor.New(40000, 2)
	view.Write([]byte(strings.Repeat("a", 40000) + strings.Repeat("b", 40000) + "z"))
	if hit, err := MatchContents(terminal.SessionSummary{}, view, "not-present"); hit != nil || err == nil {
		t.Fatal("large boundary was not reported incomplete", hit, err)
	}
	if hit, err := MatchContents(terminal.SessionSummary{}, view, "aaaa"); hit == nil || hit.Source != "scrollback" || err != nil {
		t.Fatal("cap discarded confirmed history matches", hit, err)
	}
	padded := phosphor.New(40000, 1)
	padded.Write([]byte("x" + strings.Repeat(" ", 39999) + "y"))
	if hit, err := MatchContents(terminal.SessionSummary{}, padded, "xy"); hit != nil || err == nil {
		t.Fatal("trimmed spaces bypassed the reconstruction budget", hit, err)
	}
}

func TestContentSearchBoundsRequestsAndCancels(t *testing.T) {
	started := make(chan struct{}, 4)
	var active, peak atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
	overview := terminal.Overview{Counts: terminal.OverviewCounts{Total: 100}}
	for i := 0; i < 100; i++ {
		overview.Sessions = append(overview.Sessions, terminal.SessionSummary{ID: terminal.ID(fmt.Sprintf("term%d", i))})
	}
	done := make(chan ContentSearch, 1)
	go func() { done <- c.SearchContents(ctx, overview, "needle") }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case got := <-done:
		if got.Complete || peak.Load() > 4 || len(got.Issues) != 100 {
			t.Fatalf("unbounded or falsely complete search: peak=%d result=%+v", peak.Load(), got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("search ignored cancellation")
	}
}
