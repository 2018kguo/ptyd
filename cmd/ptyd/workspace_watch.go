package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

// A wide workspace uses four columns, with up to eight tiles. Narrower windows
// use fewer columns; a hidden selected tile replaces the last visible tile.
func workspaceWatchPanel(out *strings.Builder, v workspaceView, r workspaceRect, cols int, put func(int, int, string, string), hit func(workspaceHit)) {
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
	visible := min(len(panes), workspacePaneCapacity(r))
	if visible < len(panes) {
		focused := -1
		for i, p := range panes {
			if string(p.Row.ID) == v.selected || (v.group == "" && p.Row.ID == v.previewFocus) {
				focused = i
			}
		}
		if focused >= visible {
			panes = append(append([]workspacePane(nil), panes[:visible-1]...), panes[focused])
		} else {
			panes = panes[:visible]
		}
	}
	columns := min(workspacePaneColumns(r), (visible+1)/2)
	if visible == 2 {
		columns = min(workspacePaneColumns(r), 2)
	}
	lines := (visible + columns - 1) / columns
	for i, p := range panes {
		x, y := i%columns, i/columns
		left, top := x*(r.w+2)/columns, y*(r.h+1)/lines
		width := (x+1)*(r.w+2)/columns - left - 2
		height := (y+1)*(r.h+1)/lines - top - 1
		workspacePreviewPane(out, v, p, workspaceRect{r.x + left, r.y + top, width, height}, cols, put, hit)
	}
}

func workspacePaneColumns(r workspaceRect) int { return min(max((r.w+2)/36, 1), 4) }
func workspacePaneCapacity(r workspaceRect) int {
	return min(workspacePaneColumns(r)*max((r.h+1)/5, 1), maxWorkspacePins+1)
}
func workspacePreviewPane(out *strings.Builder, v workspaceView, p workspacePane, box workspaceRect, cols int, put func(int, int, string, string), hit func(workspaceHit)) {
	focused := string(p.Row.ID) == v.selected || (v.group == "" && v.previewFocus == p.Row.ID)
	pinned := slices.Contains(v.watched(), p.Row.ID)
	accent := v.theme.identity(string(p.Row.ID))
	style := "2;" + accent
	if focused {
		style = accent
	}
	workspaceBox(box, style, put)
	if v.mode == wsNormal || v.mode == wsSearch {
		hit(workspaceHit{Rect: box, Kind: workspaceHitPane, ID: string(p.Row.ID), Group: p.Row.GroupID})
	}
	label := workspacePaneLabel(p, pinned, focused, v.sessionLabel(p.Row))
	if v.isCaptain(p.Row) {
		label = "★ Captain · " + label
	}
	put(box.x+2, box.y, clipStatus(" "+label+" ", box.w-4), "1;"+accent)
	padding := 1
	body := workspaceRect{box.x + 1, box.y + padding, box.w - 2, box.h - 2*padding}
	if body.h < 1 {
		return
	}
	if hit := v.contentMatch(p.Row.ID); hit != nil && hit.Source != "screen" {
		put(body.x, body.y, clipStatus(hit.Source+": "+hit.Text, body.w), "2")
		body.y++
		body.h--
	}
	first := 0
	if p.Screen != nil {
		focus := -1
		if v.snapshot.Search != nil {
			if hit, _ := client.MatchContents(p.Row, p.Screen, v.query); hit != nil && hit.Source == "screen" {
				focus = hit.ScreenRow
			}
		}
		first = workspacePreviewLayer(out, p.Screen, body, v.color, cols, focus)
	} else if p.Err != nil {
		put(body.x, body.y, clipStatus("Preview unavailable: "+p.Err.Error(), body.w), "33")
	} else {
		put(body.x, body.y, clipStatus("Loading live pane…", body.w), "2")
	}
	label = "Live"
	if p.Row.State != terminal.OverviewRunning {
		label = "Saved screen"
	}
	if pinned {
		label += " · ◆ pinned"
	}
	if hit := v.contentMatch(p.Row.ID); hit != nil {
		label += " · match in " + hit.Source
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
	visible := min(n, workspacePaneCapacity(r))
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

func (v workspaceView) watchPanes() []workspacePane {
	if v.snapshot == nil || v.snapshot.Query.Group != v.group {
		return nil
	}
	if v.group == "" {
		if v.snapshot.Query.Selected != v.selected {
			return nil
		}
		var panes []workspacePane
		for _, p := range v.snapshot.Panes {
			if !p.Removed {
				panes = append(panes, p)
			}
		}
		return panes
	}
	var out []workspacePane
	add := func(id terminal.ID) {
		for _, p := range out {
			if p.Row.ID == id {
				return
			}
		}
		for _, p := range v.snapshot.Panes {
			if p.Row.ID == id {
				if !p.Removed {
					out = append(out, p)
				}
				return
			}
		}
		for _, r := range v.rows {
			if r.ID == id && r.External == nil && r.Local == nil {
				out = append(out, workspacePane{Row: r})
				return
			}
		}
	}
	for _, id := range v.watched() {
		add(id)
	}
	if r, ok := v.row(); ok && r.External == nil && r.Local == nil {
		add(r.ID)
	}
	return out
}
