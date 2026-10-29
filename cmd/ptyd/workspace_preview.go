package main

import (
	"fmt"
	"strings"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type workspaceRect struct{ x, y, w, h int }

// Only model cells are painted into this rectangle. The source export's cursor,
// palette, title, clipboard and terminal modes never escape into the workspace.
func workspacePaintPreview(out *strings.Builder, view *phosphor.Terminal, r workspaceRect, color bool) {
	cols, rows := view.Size()
	first := min(max(view.Cursor().Y-r.h+2, 0), max(rows-r.h, 0))
	palette := view.Palette()
	if view.Modes().GraphemeClustering {
		out.WriteString("\x1b[?2027h")
	} else {
		out.WriteString("\x1b[?2027l")
	}
	for y := 0; y < r.h && first+y < rows; y++ {
		fmt.Fprintf(out, "\x1b[%d;%dH\x1b[0m", r.y+y+1, r.x+1)
		var previous phosphor.Style
		for x := 0; x < r.w; x++ {
			cell := view.Cell(x, first+y)
			width := max(cell.Width, 1)
			if x >= cols || x+width > r.w {
				cell = phosphor.Cell{}
				width = 1
			}
			style := cell.Style
			if view.Modes().ReverseVideo {
				style.Attrs ^= phosphor.AttrInverse
			}
			if color && (x == 0 || style != previous) {
				workspacePreviewStyle(out, style, palette)
				previous = style
			}
			if cell.Rune == 0 || style.Attrs&phosphor.AttrHidden != 0 {
				out.WriteString(strings.Repeat(" ", width))
			} else {
				out.WriteString(cell.Text())
			}
			x += width - 1
		}
	}
	out.WriteString("\x1b[0m\x1b[?2027l\x1b[?7l")
}
func workspacePreviewStyle(out *strings.Builder, s phosphor.Style, palette phosphor.Palette) {
	out.WriteString("\x1b[0")
	for _, a := range []struct {
		attr phosphor.Attr
		code int
	}{
		{phosphor.AttrBold, 1}, {phosphor.AttrFaint, 2}, {phosphor.AttrItalic, 3},
		{phosphor.AttrInverse, 7}, {phosphor.AttrStrikethrough, 9}, {phosphor.AttrOverline, 53},
	} {
		if s.Attrs&a.attr != 0 {
			fmt.Fprintf(out, ";%d", a.code)
		}
	}
	if s.Underline != phosphor.UnderlineNone {
		fmt.Fprintf(out, ";4:%d", s.Underline)
	}
	base := phosphor.DefaultPalette()
	for i, c := range []phosphor.Color{s.Fg, s.Bg} {
		code := 38 + i*10
		if c.Kind() == phosphor.ColorIndexed && palette.Colors[c.Index()] == base.Colors[c.Index()] {
			fmt.Fprintf(out, ";%d;5;%d", code, c.Index())
			continue
		}
		if c.Kind() == phosphor.ColorDefault && palette.Resolve(c, i == 1) == base.Resolve(c, i == 1) {
			continue
		}
		rgb := palette.Resolve(c, i == 1)
		fmt.Fprintf(out, ";%d;2;%d;%d;%d", code, rgb.R, rgb.G, rgb.B)
	}
	out.WriteString("m")
}

func workspaceHome(out *strings.Builder, v workspaceView, cols, rows int, put func(int, int, string, string), line func(int)) {
	gs := v.visibleGroups()
	running, attention, saved := 0, 0, 0
	for _, g := range v.groups {
		running += g.Running
		attention += g.Attention
		if g.ID == terminal.DefaultGroup && g.Running == 0 {
			saved = g.Terminals
		}
	}
	put(2, 2, "Choose a group. Preview its work before opening it.", "2")
	summary := fmt.Sprintf("%s · %d running", workspaceCount(len(gs), "group"), running)
	if attention > 0 {
		summary += fmt.Sprintf(" · %d attention", attention)
	}
	if cols >= 96 {
		put(max(cols-textWidth(summary)-3, 30), 1, summary, "36")
	} else {
		put(2, 3, summary, "36")
	}
	line(4)
	put(2, 4, " GROUPS ", "1;36")
	if v.snapshot == nil && v.err == nil {
		put(4, 6, "Finding your groups…", "36")
	}
	if v.snapshot != nil && len(gs) == 0 {
		if v.query != "" {
			put(4, 7, "No groups match your search.", "1")
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
	left := cols
	if cols >= 96 {
		left = min(max(cols/3, 30), 42)
	}
	available := max((rows-11)/2, 1)
	if left == cols {
		available = min(len(gs), min(available, max((rows-22)/2, 1)))
	}
	start := max(v.index()-available+1, 0)
	for i := start; i < len(gs) && i < start+available; i++ {
		g := gs[i]
		y := 6 + (i-start)*2
		mark, style := "  ", "1"
		if string(g.ID) == v.selected {
			mark, style = "› ", "1;7"
		}
		name := g.Name
		if g.Archived {
			name += " · archived"
		}
		put(2, y, statusCell(mark+name, max(left-5, 1)), style)
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
	put(2, rows-3, "Enter opens this group · r rename · a archive · x remove", "2")
	x, y, w, h := left+3, 6, cols-left-6, rows-12
	if left < cols {
		for yy := 5; yy < rows-5; yy++ {
			put(left, yy, "│", "2")
		}
		put(left+2, 4, " WORKSPACE PREVIEW ", "1;36")
	} else {
		x, y, w = 3, 7+available*2, cols-6
		h = rows - y - 6
	}
	if v.snapshot == nil || v.snapshot.Query.Selected != v.selected {
		put(x, y, "Loading workspace preview…", "2")
		return
	}
	put(x, y, clipStatus(selected.Name+" · "+friendlyCWD(selected.CWD), w), "1")
	y += 2
	h -= 2
	shown := min(len(v.snapshot.Overview.Sessions), min(4, max(h/3, 0)))
	if left == cols {
		shown = 0
	}
	for i := 0; i < shown; i++ {
		r := v.snapshot.Overview.Sessions[i]
		style := "2"
		if r.State == terminal.OverviewRunning {
			style = "32"
		}
		if r.Attention > 0 {
			style = "33"
		}
		mark := "  "
		if string(r.ID) == v.snapshot.PreviewID {
			mark = "› "
		}
		put(x, y+i, clipStatus(mark+sessionName(r)+" · "+workspaceTerminalState(r), w), style)
	}
	y += shown + 1
	h -= shown + 1
	if h < 2 {
		return
	}
	put(x, y, "LIVE PREVIEW · read only", "2")
	y += 2
	h -= 2
	if v.snapshot.Preview != nil {
		workspacePaintPreview(out, v.snapshot.Preview, workspaceRect{x, y, w, max(h, 1)}, v.color)
	} else if v.snapshot.PreviewErr != nil {
		put(x, y, "Preview unavailable: "+v.snapshot.PreviewErr.Error(), "33")
	} else {
		put(x, y, "No running terminal here. Enter, then n to start one.", "2")
	}
}

func workspaceCount(n int, noun string) string {
	if n != 1 {
		noun += "s"
	}
	return fmt.Sprintf("%d %s", n, noun)
}
