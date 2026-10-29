package main

import (
	"fmt"
	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
	"strings"
	"testing"
)

var benchmarkFrame string

func BenchmarkWorkspaceFrame(b *testing.B) {
	for _, n := range []int{1, 8} {
		b.Run(fmt.Sprintf("panes%d", n), func(b *testing.B) {
			v := workspaceView{group: "work", selected: fmt.Sprintf("term-%d", n), color: true, pins: map[terminal.GroupID][]terminal.ID{}, groups: []terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: "Benchmark workspace"}, Running: n, Terminals: n}}}
			s := workspaceSnapshot{Query: workspaceQuery{Group: "work", Selected: v.selected}}
			for i := 1; i <= n; i++ {
				id := terminal.ID(fmt.Sprintf("term-%d", i))
				row := terminal.SessionSummary{ID: id, GroupID: "work", Name: fmt.Sprintf("codex %d", i), Command: "codex", State: terminal.OverviewRunning}
				v.rows = append(v.rows, row)
				if i < n {
					v.pins["work"] = append(v.pins["work"], id)
				}
				p := phosphor.New(160, 48, phosphor.WithScrollback(0))
				for y := 0; y < 48; y++ {
					p.Write([]byte(fmt.Sprintf("\x1b[%d;1H\x1b[38;2;120;190;160m%02d \x1b[0m%s", y+1, y, strings.Repeat("dense output content  ", 8))))
				}
				s.Panes = append(s.Panes, workspacePane{Row: row, Screen: p})
			}
			s.Overview.Sessions = v.rows
			v.snapshot = &s
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkFrame = workspaceFrame(v, 240, 80)
			}
			b.ReportMetric(float64(len(benchmarkFrame)), "output-B/frame")
		})
	}
}
