package main

import (
	"fmt"
	"strings"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type workspaceRect struct{ x, y, w, h int }

// Preview rows are cropped off-screen with Phosphor's canonical renderer.
// Tiles paint left to right; chrome paints last over temporary cluster staging.
func workspacePreviewLayer(out *strings.Builder, view *phosphor.Terminal, r workspaceRect, color bool, hostCols int, focus ...int) int {
	if r.w < 1 || r.h < 1 {
		return 0
	}
	cols, rows := view.Size()
	first := workspacePreviewStart(view, r.h)
	if len(focus) > 0 && focus[0] >= 0 {
		first = min(max(focus[0]-r.h/2, 0), max(rows-r.h, 0))
	}
	row := phosphor.New(cols, 1, phosphor.WithScrollback(0))
	var sourceANSI, rendered []byte
	for y := 0; y < r.h && first+y < rows; y++ {
		row.Resize(cols, 1)
		row.Write([]byte("\x1b[0m\x1b[H\x1b[2K"))
		sourceANSI = view.AppendRowANSI(sourceANSI[:0], first+y)
		row.Write(sourceANSI)
		// Clip stored cells before changing width; this preserves cluster
		// histories rather than asking a terminal to reconstruct each cell.
		if r.w < cols {
			row.Write([]byte(fmt.Sprintf("\x1b[0m\x1b[1;%dH\x1b[K", r.w+1)))
		}
		row.Write([]byte("\x1b[1;1H"))
		row.Resize(min(r.w, hostCols-r.x), 1)
		rendered = row.AppendRowANSI(rendered[:0], 0)
		data := client.OffsetRowANSI(rendered, r.x)
		if !color {
			data = client.WithoutANSIColors(data)
		}
		fmt.Fprintf(out, "\x1b[%d;%dH", r.y+y+1, r.x+1)
		out.Write(data)
	}
	out.WriteString("\x1b[0m\x1b[0\"q\x1b]8;;\x1b\\\x1b[?2027l\x1b[?7l")
	return first
}

// Full-screen apps draw footers and input-box backgrounds below the cursor.
// Keep their last painted row in view, while sparse shells stay near the top.
func workspacePreviewStart(view *phosphor.Terminal, height int) int {
	cols, rows := view.Size()
	if height >= rows {
		return 0
	}
	last := view.Cursor().Y
scan:
	for y := rows - 1; y > last; y-- {
		for x := 0; x < cols; x++ {
			c := view.Cell(x, y)
			if workspacePaintedCell(c) {
				last = y
				break scan
			}
		}
	}
	return min(max(last-max(height, 1)+1, 0), max(rows-max(height, 1), 0))
}

func workspacePaintedCell(c phosphor.Cell) bool {
	return c.Width == 0 || (c.Rune != 0 && c.Rune != ' ') || c.Combining != "" || c.Style.Bg != phosphor.DefaultColor || c.Style.Underline != phosphor.UnderlineNone || c.Style.Attrs&(phosphor.AttrInverse|phosphor.AttrStrikethrough|phosphor.AttrOverline) != 0
}

func workspacePreviewRight(view *phosphor.Terminal, r workspaceRect, first int) int {
	cols, rows := view.Size()
	for x := cols - 1; x >= r.w; x-- {
		for y := first; y < min(first+r.h, rows); y++ {
			if workspacePaintedCell(view.Cell(x, y)) {
				return x - r.w + 1
			}
		}
	}
	return 0
}

func workspaceHome(out *strings.Builder, v workspaceView, cols, rows int, put func(int, int, string, string), line func(int), hit func(workspaceHit)) {
	gs := v.visibleGroups()
	running, attention, saved := 0, 0, 0
	for _, g := range v.groups {
		running += g.Running
		attention += g.Attention
		if g.ID == terminal.DefaultGroup && g.Running == 0 {
			saved = g.Terminals
		}
	}
	put(2, 2, clipStatus("Choose a group. Preview its work before opening it.", cols-workspaceBrandReserve(cols)-4), "2")
	summary := fmt.Sprintf("%s · %d running", workspaceCount(len(gs), "group"), running)
	if attention > 0 {
		summary += fmt.Sprintf(" · %d attention", attention)
	}
	if cols >= 96 {
		put(max(cols-textWidth(summary)-workspaceBrandReserve(cols)-3, 30), 1, summary, "36")
	} else {
		put(2, 3, summary, "36")
	}
	left := cols
	if cols >= 96 {
		left = workspaceSidebarWidth(cols)
	}
	line(4)
	if left < cols {
		workspaceBox(workspaceRect{1, 4, left, rows - 9}, "2", put)
	}
	put(2, 4, " GROUPS ", "1;36")
	if v.snapshot == nil && v.err == nil {
		put(4, 6, "Finding your groups…", "36")
	}
	if v.snapshot != nil && len(gs) == 0 {
		if v.query != "" {
			label := "No groups match your search."
			if v.snapshot.Search != nil && !v.snapshot.Search.Complete {
				label = "No confirmed matches. Some screens were unreadable."
			}
			put(4, 7, label, "1")
		} else {
			put(4, 7, "Make room for your next piece of work.", "1")
		}
		put(4, 9, "Press n to create a group, then launch an agent, a shell or a command.", "36")
		put(4, 11, "Groups stay here after you detach.", "2")
		if saved > 0 {
			put(4, 13, fmt.Sprintf("h  Browse %d saved terminals in Inbox", saved), "2")
		}
		return
	}
	available := max((rows-12)/2, 1)
	if left == cols {
		available = min(len(gs), min(available, max((rows-22)/2, 1)))
	}
	start := max(v.index()-available+1, 0)
	for i := start; i < len(gs) && i < start+available; i++ {
		g := gs[i]
		y := 6 + (i-start)*2
		mark, style := "  ", "1;"+v.theme.identity(string(g.ID))
		if string(g.ID) == v.selected {
			mark, style = "› ", v.theme.selected()+";"+v.theme.identity(string(g.ID))
		}
		name := g.Name
		if g.Archived {
			name += " · archived"
		}
		put(2, y, statusCell(mark+workspaceGroupIcon(g)+" "+name, max(left-5, 1)), style)
		if v.mode == wsNormal || v.mode == wsSearch {
			hit(workspaceHit{Rect: workspaceRect{2, y, left - 4, 2}, Kind: workspaceHitGroup, ID: string(g.ID)})
		}
		detail := fmt.Sprintf("%d running · %s", g.Running, workspaceCount(g.Terminals, "terminal"))
		state := "2"
		if g.Running > 0 {
			state = "32"
		}
		if g.Attention > 0 {
			detail += fmt.Sprintf(" · %d attention", g.Attention)
			state = "33"
		}
		put(4, y+1, clipStatus(detail, max(left-7, 1)), state)
	}
	if len(gs) == 0 {
		return
	}
	selected := gs[min(v.index(), len(gs)-1)]
	put(2, rows-4, friendlyCWD(selected.CWD), "2")
	x, y, w, h := left+3, 6, cols-left-6, rows-12
	if left < cols {
		heading := " WORKSPACE PREVIEW "
		if v.snapshot != nil && v.snapshot.Query.Selected == v.selected && len(v.snapshot.Panes) > 0 {
			heading = fmt.Sprintf(" WORKSPACE PREVIEW · %d/%d panes ", min(len(v.watchPanes()), workspacePaneCapacity(workspaceRect{w: w, h: h - 2})), len(v.snapshot.Overview.Sessions))
		}
		put(left+2, 4, heading, "1;36")
	} else {
		x, y, w = 3, 7+available*2, cols-6
		h = rows - y - 6
	}
	if v.snapshot == nil || v.snapshot.Query.Selected != v.selected {
		put(x, y, "Loading workspace preview…", "2")
		return
	}
	put(x, y, clipStatus(selected.Name+" · "+friendlyCWD(selected.CWD), w), "1;"+v.theme.identity(string(selected.ID)))
	y += 2
	h -= 2
	if h < 3 {
		return
	}
	if len(v.watchPanes()) > 0 {
		workspaceWatchPanel(out, v, workspaceRect{x, y, w, h}, cols, put, hit)
	} else if v.snapshot.Search != nil && !v.snapshot.Search.Complete {
		put(x, y, "No confirmed content matches in this preview.", "2")
	} else {
		put(x, y, "No terminal here to preview. Enter, then n to start one.", "2")
	}

}

func workspaceCount(n int, noun string) string {
	if n != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s", n, noun)
}
