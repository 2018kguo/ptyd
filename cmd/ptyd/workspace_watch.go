package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// Panes are stacked so their full-width row layers never overwrite one another.
func workspaceWatchPanel(out *strings.Builder, v workspaceView, r workspaceRect, cols int, put func(int, int, string, string)) {
	panes := v.watchPanes()
	if len(panes) == 0 {
		if row, ok := v.row(); ok {
			hint := "Select a terminal to preview it."
			if row.Local != nil {
				hint = row.Local.Guidance()
			} else if row.External != nil {
				hint = row.External.Reason
			}
			put(r.x, r.y, clipStatus(hint, r.w), "2")
		}
		return
	}
	visible := min(len(panes), max(r.h/3, 1))
	if visible < len(panes) {
		focused := -1
		for i, p := range panes {
			if string(p.Row.ID) == v.selected {
				focused = i
			}
		}
		if focused >= visible {
			panes = append(append([]workspacePinnedPreview(nil), panes[:visible-1]...), panes[focused])
		} else {
			panes = panes[:visible]
		}
	}
	height := max(r.h/visible, 1)
	for i, p := range panes {
		y := r.y + i*height
		focused := string(p.Row.ID) == v.selected
		pinned := slices.Contains(v.watched(), p.Row.ID)
		style := "2"
		if focused {
			style = "1;36"
		} else if pinned {
			style = "36"
		}
		if len(panes) > 1 {
			put(r.x, y, strings.Repeat("─", max(r.w, 1)), "2")
		}
		put(r.x, y, clipStatus(workspacePaneLabel(p, pinned, focused), r.w), style)
		h := height - 2
		if h < 1 {
			continue
		}
		if p.Screen != nil {
			workspacePreviewLayer(out, p.Screen, workspaceRect{r.x, y + 2, r.w, h}, v.color, cols)
		} else if p.Err != nil {
			put(r.x, y+2, clipStatus("Preview unavailable: "+p.Err.Error(), r.w), "33")
		} else {
			put(r.x, y+2, "Loading live pane…", "2")
		}
	}
}
func workspaceWatchHeading(v workspaceView, r workspaceRect) string {
	n := len(v.watchPanes())
	visible := min(n, max(r.h/3, 1))
	if n > visible {
		return fmt.Sprintf(" WATCH PANES · %d/%d visible ", visible, n)
	}
	if len(v.watched()) > 0 {
		return fmt.Sprintf(" WATCH PANES · ◆ %d pinned ", len(v.watched()))
	}
	return " LIVE PREVIEW · read only "
}
func workspaceTerminalIcon(r terminal.SessionSummary) string {
	if r.Attention > 0 {
		return "!"
	}
	if r.HumanControl {
		return "◉"
	}
	if r.State == terminal.OverviewRunning {
		return "●"
	}
	if r.State == terminal.OverviewStopped || r.ExitCode == nil {
		return "◇"
	}
	if *r.ExitCode != 0 {
		return "!"
	}
	return "✓"
}
func workspaceGroupIcon(g terminal.GroupSummary) string {
	if g.Attention > 0 {
		return "!"
	}
	if g.Archived {
		return "◇"
	}
	if g.Running > 0 {
		return "●"
	}
	return "□"
}
