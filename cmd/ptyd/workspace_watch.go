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
	visible := min(len(panes), workspacePaneLimit(r.h))
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
	for i, p := range panes {
		y := r.y + i*(r.h+1)/visible
		height := (i+1)*(r.h+1)/visible - i*(r.h+1)/visible - 1
		workspacePreviewPane(out, v, p, workspaceRect{r.x, y, r.w, height}, cols, put)
	}
}

func workspacePaneLimit(height int) int { return max((height+1)/5, 1) }
func workspacePreviewPane(out *strings.Builder, v workspaceView, p workspacePinnedPreview, box workspaceRect, cols int, put func(int, int, string, string)) {
	focused := string(p.Row.ID) == v.selected
	pinned := slices.Contains(v.watched(), p.Row.ID)
	style := "2"
	if focused {
		style = "36"
	}
	workspaceBox(box, style, put)
	put(box.x+2, box.y, clipStatus(" "+workspacePaneLabel(p, pinned, focused)+" ", box.w-4), "1;"+style)
	padding := 1
	if box.h >= 7 {
		padding = 2
	}
	body := workspaceRect{box.x + 2, box.y + padding, box.w - 4, box.h - 2*padding}
	if body.h < 1 {
		return
	}
	first := 0
	if p.Screen != nil {
		first = workspacePreviewLayer(out, p.Screen, body, v.color, cols)
	} else if p.Err != nil {
		put(body.x, body.y, clipStatus("Preview unavailable: "+p.Err.Error(), body.w), "33")
	} else {
		put(body.x, body.y, clipStatus("Loading live pane…", body.w), "2")
	}
	label := "Live"
	if p.Row.State != terminal.OverviewRunning {
		label = "Saved screen"
	}
	if pinned {
		label += " · ◆ pinned"
	}
	if p.Screen != nil {
		if first > 0 {
			label += fmt.Sprintf(" · ↑ %d rows above", first)
		}
		if hidden := workspacePreviewRight(p.Screen, body, first); hidden > 0 {
			label += fmt.Sprintf(" · → %d cols hidden", hidden)
		}
	}
	put(box.x+2, box.y+box.h-1, clipStatus(" "+label+" ", box.w-4), "2")
}

func workspaceWatchHeading(v workspaceView, r workspaceRect) string {
	n := len(v.watchPanes())
	visible := min(n, workspacePaneLimit(r.h))
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
