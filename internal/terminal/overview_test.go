package terminal

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOverviewCountsOrderAndBounds(t *testing.T) {
	zero, failure := 0, 2
	infos := []Info{
		{ID: "ok", Status: StatusExited, ExitCode: &zero},
		{ID: "running", Status: StatusRunning, Pending: "$ ", LatestSeq: 42},
		{ID: "stopped", Status: StatusExited, Notice: "resume declined"},
		{ID: "failed", Status: StatusExited, ExitCode: &failure, Attention: []AttentionEvent{{Line: Line{Text: "old"}}, {Line: Line{Text: "build failed"}}}},
	}
	before, _ := json.Marshal(infos)
	view := BuildOverview(infos, OverviewOptions{Limit: 3})
	if view.Counts != (OverviewCounts{Total: 4, Running: 1, Exited: 2, Stopped: 1, Attention: 1}) || !view.Truncated {
		t.Fatalf("counts: %+v", view)
	}
	var ids []ID
	for _, row := range view.Sessions {
		ids = append(ids, row.ID)
	}
	if !slices.Equal(ids, []ID{"failed", "stopped", "running"}) {
		t.Fatalf("order: %v", ids)
	}
	if got := view.Sessions[0].Preview; got == nil || got.Source != "watch" || got.Text != "build failed" {
		t.Fatalf("preview: %+v", got)
	}
	if view.Sessions[2].LatestSeq != 42 || view.Sessions[2].Preview.Source != "pending" {
		t.Fatal("cursor/prompt lost")
	}
	after, _ := json.Marshal(infos)
	if string(before) != string(after) {
		t.Fatal("projection mutated registry values")
	}
}

func TestOverviewFiltersAndEmptyJSON(t *testing.T) {
	infos := []Info{
		{ID: "b", Name: "BUILD", CWD: "/project", Tags: []string{"test"}, Status: StatusRunning},
		{ID: "a", Name: "build", CWD: "/other", Tags: []string{"dev"}, Status: StatusRunning},
	}
	view := BuildOverview(infos, OverviewOptions{Query: " PROJECT ", Tags: []string{"test", "else"}})
	if len(view.Sessions) != 1 || view.Sessions[0].ID != "b" || view.Counts.Total != 1 {
		t.Fatalf("filter: %+v", view)
	}
	view = BuildOverview(infos, OverviewOptions{Query: "missing"})
	b, _ := json.Marshal(view)
	if view.Counts.Total != 0 || view.Truncated || !strings.Contains(string(b), `"sessions":[]`) {
		t.Fatalf("empty: %s", b)
	}
}

func TestOverviewPreviewAndProjection(t *testing.T) {
	infos := []Info{{ID: "t", Command: "/bin/sh", Args: []string{"secret-argument"}, Watches: []WatchSpec{{Pattern: "secret-pattern"}},
		Status: StatusRunning, Pending: strings.Repeat("界", 200), CreatedAt: time.Now()}}
	view := BuildOverview(infos, OverviewOptions{})
	p := view.Sessions[0].Preview
	if p == nil || !p.Truncated || len([]rune(p.Text)) != overviewPreviewRunes || view.Sessions[0].Command != "sh" {
		t.Fatalf("preview: %+v", view)
	}
	b, _ := json.Marshal(view)
	if strings.Contains(string(b), "secret") || strings.Contains(string(b), "watches") || strings.Contains(string(b), "args") {
		t.Fatalf("detail leaked: %s", b)
	}
	infos[0].Pending = strings.Repeat("x", overviewPreviewRunes)
	if BuildOverview(infos, OverviewOptions{}).Sessions[0].Preview.Truncated {
		t.Fatal("exact bound clipped")
	}
}

func TestOverviewLimits(t *testing.T) {
	infos := make([]Info, MaxOverviewLimit+20)
	for i := range infos {
		infos[i].Status = StatusRunning
	}
	for _, tc := range []struct{ limit, want int }{{0, DefaultOverviewLimit}, {-1, DefaultOverviewLimit}, {2, 2}, {10000, MaxOverviewLimit}} {
		view := BuildOverview(infos, OverviewOptions{Limit: tc.limit})
		if len(view.Sessions) != tc.want || !view.Truncated || view.Counts.Total != len(infos) {
			t.Fatalf("limit %d: %+v", tc.limit, view.Counts)
		}
	}
}

func TestOverviewUnobservedIdle(t *testing.T) {
	now := time.Now()
	infos := []Info{{ID: "silent", Status: StatusRunning}, {ID: "observed", Status: StatusRunning, LastOutputAt: &now, IdleSeconds: 12}}
	view := BuildOverview(infos, OverviewOptions{})
	for _, row := range view.Sessions {
		if row.ID == "silent" && row.IdleSeconds != nil {
			t.Fatal("invented output time")
		}
		if row.ID == "observed" && (row.IdleSeconds == nil || *row.IdleSeconds != 12) {
			t.Fatal("lost observed idle time")
		}
	}
}

func BenchmarkOverview(b *testing.B) {
	infos := make([]Info, 1000)
	for i := range infos {
		infos[i] = Info{ID: ID(strings.Repeat("t", 10)), Name: "worker", Command: "claude", CWD: "/project", Status: StatusRunning, Pending: strings.Repeat("x", 160)}
	}
	b.ReportAllocs()
	for b.Loop() {
		BuildOverview(infos, OverviewOptions{})
	}
}

// A fleet of 1,000 sessions, with one watch each and an outstanding match on
// every tenth session. Both encoders start from the same canonical metadata.
func BenchmarkOverviewJSON(b *testing.B) {
	now := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	infos := make([]Info, 1000)
	for i := range infos {
		infos[i] = Info{ID: ID(fmt.Sprintf("term-%d-abcd", i)), Name: fmt.Sprintf("worker-%d", i),
			CWD: "/work/project", Command: "claude", Args: []string{"--model", "default"}, Tags: []string{"agent"},
			PID: 1000 + i, Status: StatusRunning, CreatedAt: now, LastOutputAt: &now, IdleSeconds: 1.5, LatestSeq: 4999, Pending: "ready> ",
			Watches: []WatchSpec{{Name: "question", Pattern: "[?] $", Once: true}}}
		if i%10 == 0 {
			infos[i].Attention = []AttentionEvent{{Watch: "question", Line: Line{Seq: 4999, Text: "Continue with the operation? ", At: now}, At: now}}
		}
	}
	b.Run("FullList", func(b *testing.B) {
		b.ReportAllocs()
		data, _ := json.Marshal(infos)
		b.ReportMetric(float64(len(data)), "response-B")
		for b.Loop() {
			if _, err := json.Marshal(infos); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Overview50", func(b *testing.B) {
		b.ReportAllocs()
		data, _ := json.Marshal(BuildOverview(infos, OverviewOptions{}))
		b.ReportMetric(float64(len(data)), "response-B")
		for b.Loop() {
			if _, err := json.Marshal(BuildOverview(infos, OverviewOptions{})); err != nil {
				b.Fatal(err)
			}
		}
	})
}
