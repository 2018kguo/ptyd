package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceEightPanesUseFourColumnsAndTwoRows(t *testing.T) {
	v := workspaceView{group: "work", selected: "eight", color: true, pins: map[terminal.GroupID][]terminal.ID{"work": {"one", "two", "three", "four", "five", "six", "seven"}}, snapshot: &workspaceSnapshot{Query: workspaceQuery{Group: "work"}}}
	for i, id := range []terminal.ID{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		screen := phosphor.New(60, 12)
		screen.Write([]byte(fmt.Sprintf("\x1b[48;2;%d;42;54m\x1b[2J\x1b[?2027h👩‍💻\x1b[?2027l %s\r\n\x1b[?2027hA☔︎\x1b[0m", i+40, strings.ToUpper(string(id)))))
		row := terminal.SessionSummary{ID: id, GroupID: "work", Name: string(id), State: terminal.OverviewRunning}
		v.rows = append(v.rows, row)
		v.snapshot.Panes = append(v.snapshot.Panes, workspacePane{Row: row, Screen: screen})
	}
	for _, group := range []terminal.GroupID{"work", ""} {
		v.group = group
		v.snapshot.Query.Group = group
		if group == "" {
			v.selected = "work"
			v.groups = []terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: "work"}, Terminals: 8, Running: 8}}
			v.snapshot.Query.Selected = "work"
			v.snapshot.Overview.Sessions = v.rows
		}
		scene := renderWorkspace(v, 240, 50)
		host := phosphor.New(240, 50)
		host.Write([]byte(scene.ANSI))
		var panes []workspaceHit
		for _, h := range scene.Hits {
			if h.Kind == workspaceHitPane {
				panes = append(panes, h)
			}
		}
		if len(panes) != 8 {
			t.Fatalf("group %q: got %d panes", group, len(panes))
		}
		for i, h := range panes {
			if i%4 > 0 && (h.Rect.y != panes[i-1].Rect.y || h.Rect.x <= panes[i-1].Rect.x+panes[i-1].Rect.w) {
				t.Fatal("not four distinct columns")
			}
			if i >= 4 && (h.Rect.x != panes[i-4].Rect.x || h.Rect.y <= panes[i-4].Rect.y+panes[i-4].Rect.h) {
				t.Fatal("not two distinct rows")
			}
			// An adjacent tile must not erase this tile's colors or mixed-mode
			// Unicode clusters while canonical rows are composed left to right.
			got := host.Cell(h.Rect.x+1, h.Rect.y+1)
			want := v.snapshot.Panes[i].Screen.Cell(0, 0)
			if got.Text() != want.Text() || got.Width != want.Width || got.Style.Bg != want.Style.Bg {
				t.Fatalf("tile%d changed: got %+v want %+v", i, got, want)
			}
			if !strings.Contains(host.Line(h.Rect.y+1), strings.ToUpper(h.ID)) {
				t.Fatalf("tile%d text lost", i)
			}
		}
	}
}
