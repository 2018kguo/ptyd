package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// The workspace uses the host palette: bright text for focus, muted context,
// cyan navigation, amber attention. Every status also has a plain-text label.
func workspaceFrame(v workspaceView, cols, rows int) string {
	cols = max(cols, 1)
	rows = max(rows, 1)
	if cols < 40 || rows < 16 {
		return "\x1b[?2026h\x1b[H\x1b[2J\x1b[0m" + clipStatus("ptyd · enlarge to 40×16", cols) + "\r\n" + clipStatus("q detaches · work keeps running", cols) + "\x1b[?2026l"
	}
	var out, preview strings.Builder
	const start = "\x1b[?2026h\x1b[?25l\x1b[?7l\x1b[0m\x1b[H\x1b[2J"
	put := func(x, y int, text, style string) {
		if x < 0 || y < 0 || x >= cols || y >= rows {
			return
		}
		fmt.Fprintf(&out, "\x1b[%d;%dH", y+1, x+1)
		if v.color && style != "" {
			out.WriteString("\x1b[" + style + "m")
		}
		out.WriteString(clipStatus(text, cols-x))
		out.WriteString("\x1b[0m")
	}
	line := func(y int) { put(1, y, strings.Repeat("─", max(cols-2, 0)), "2") }
	heading := "Your groups"
	if g, ok := v.groupInfo(); ok {
		heading = g.Name
	}
	put(2, 1, "ptyd", "1;36")
	put(8, 1, "/  "+heading, "1")
	title := "ptyd · " + heading
	fmt.Fprintf(&out, "\x1b]2;%s\x1b\\", displayText(title, 160))
	footer := "j/k preview   Enter open   n new group   / search   jk back   ? help   q quit"
	if v.group != "" {
		footer = "j/k select   Enter open   v pin pane   n new   jk back   ? help   q quit"
	}
	if cols < 96 {
		footer = "j/k  Enter  n new  jk back  ?  q quit"
		if v.group != "" {
			footer = "j/k  Enter  v pin  jk back  ?  q quit"
		}
	}
	if v.group == "" {
		workspaceHome(&preview, v, cols, rows, put, line)
	} else {
		g, ok := v.groupInfo()
		if ok {
			put(2, 2, clipStatus(friendlyCWD(g.CWD), cols-workspaceBrandReserve(cols)-4), "2")
			summary := fmt.Sprintf("%d running  ·  %s", g.Running, workspaceCount(g.Terminals, "terminal"))
			if g.Attention > 0 {
				summary += fmt.Sprintf("  ·  %d need attention", g.Attention)
			}
			put(max(cols-textWidth(summary)-workspaceBrandReserve(cols)-3, 2), 1, summary, "36")
		}
		if ok && g.Archived {
			put(2, 3, "Archived · return to groups and press a to unarchive.", "33")
		}
		if v.outside {
			put(2, 3, "Outside agents shown. Enter imports a supported conversation into this group.", "33")
		}
		left := cols
		if cols >= 96 {
			left = min(max(cols/3, 30), 44)
		}
		line(4)
		put(2, 4, " TERMINALS ", "1;36")
		if left < cols {
			put(left+2, 4, workspaceWatchHeading(v, workspaceRect{left + 3, 6, cols - left - 6, rows - 12}), "2")
		}
		available := max(rows-10, 1)
		if left == cols {
			available = max((rows-13)/2, 1)
		}
		start := max(v.index()-available+1, 0)
		for i := start; i < len(v.rows) && i < start+available; i++ {
			r := v.rows[i]
			y := 6 + i - start
			style := ""
			mark := "  "
			if string(r.ID) == v.selected {
				style = "1;7"
				mark = "› "
			}
			status := workspaceTerminalIcon(r) + " " + workspaceTerminalState(r)
			name := sessionName(r)
			if slices.Contains(v.watched(), r.ID) {
				name = "◆ " + name
			}
			nameWidth := max(left-16, 5)
			put(2, y, mark+statusCell(name, max(left-5, 1)), style)
			put(2, y, mark+statusCell(name, nameWidth), style)
			if style == "" {
				if r.Attention > 0 {
					style = "33"
				} else if r.State == terminal.OverviewRunning {
					style = "32"
				} else {
					style = "2"
				}
			}
			put(max(left-10, 2), y, status, style)
		}
		if len(v.rows) == 0 && v.snapshot != nil {
			heading := "This group is ready."
			if v.query != "" {
				heading = "No terminals match your search."
			} else if v.outside {
				heading = "No terminals received in this view."
			} else if !v.history && v.snapshot.Overview.HistoryHidden > 0 {
				heading = "Press h to see finished terminals."
			}
			put(3, 7, heading, "1")
			put(3, 9, "n  Start an agent or shell", "36")
			put(3, 11, "a  Bring an existing agent here", "2")
			put(3, 13, "Or ask an agent to use this group.", "2")
		}
		if left < cols {
			for y := 5; y < rows-5; y++ {
				put(left, y, "│", "2")
			}
			workspaceWatchPanel(&preview, v, workspaceRect{left + 3, 6, max(cols-left-6, 1), max(rows-12, 1)}, cols, put)
		}
		if left == cols {
			y := 7 + available
			area := workspaceRect{3, y + 1, cols - 6, max(rows-y-6, 1)}
			put(2, y, workspaceWatchHeading(v, area), "2")
			workspaceWatchPanel(&preview, v, area, cols, put)
		}
		if row, ok := v.row(); ok {
			detail := sessionName(row) + " · " + sessionApp(row) + " · " + workspaceTerminalState(row)
			put(2, rows-4, detail, "1")
			hint := "Enter takes control · v pin/unpin pane · r rename · m move · x close"
			if row.State != terminal.OverviewRunning && row.External == nil {
				hint = "Enter opens saved screen · v pin pane · R relaunch · x remove"
			}
			if row.HumanControl {
				hint = "Another human has control · v pins its live pane"
			}
			if row.Local != nil {
				hint = row.Local.Guidance()
			} else if row.External != nil && row.External.Action == "resume" {
				hint = "Enter to resume this stopped conversation (confirmation required)"
			} else if row.External != nil && row.External.Action != "attach" {
				hint = row.External.Reason
			}
			put(2, rows-3, hint, "2")
		}
	}
	if v.query != "" {
		put(2, 3, "/ "+v.query, "36")
	}
	notice := v.notice
	if v.err != nil {
		notice = "Refresh failed · showing the last received view. " + v.err.Error()
	}
	if v.snapshot != nil && v.group != "" && v.snapshot.Overview.Truncated {
		notice = "Only the first 500 matches are shown. Use / to narrow this group."
	}
	if v.snapshot != nil && v.snapshot.Overview.ProcessIssue != "" {
		notice = v.snapshot.Overview.ProcessIssue
	}
	if v.outside && v.snapshot != nil {
		o := v.snapshot.Overview
		if len(o.DiscoveryIssues) > 0 {
			issue := o.DiscoveryIssues[0]
			notice = "Outside listing incomplete · " + string(issue.Provider) + ": " + issue.Message
		}
		if o.DiscoveryTruncated {
			notice = "Outside listing incomplete · some agents were omitted. " + notice
		}
	}
	if notice != "" {
		put(2, max(rows-5, 3), notice, "33")
	}
	line(rows - 2)
	put(2, rows-1, footer, "2")
	if v.mode != wsNormal && v.mode != wsSearch && v.mode != wsCommand {
		x, y := max((cols-min(cols-4, 70))/2, 1), max((rows-14)/2, 2)
		width := max(min(cols-4, 70), 1)
		for i := 0; i < 12 && y+i < rows-2; i++ {
			put(x, y+i, strings.Repeat(" ", width), "7")
		}
		label, hint := "", ""
		switch v.mode {
		case wsNewGroup:
			label = "Name your new group"
			hint = "Enter creates it · jk / Esc goes back"
		case wsRun:
			label = "Run a command"
			hint = "Runs in your shell · Enter starts it · jk / Esc goes back"
		case wsRename:
			label = "Choose a new name"
			hint = "The process keeps running · jk / Esc goes back"
		case wsResume:
			label = "Resume this stopped conversation?"
			hint = "Type resume, then Enter. jk / Esc goes back."
			put(x+2, y+4, "Starts its agent again in this group.", "7")
		case wsConfirm:
			label = "Close and remove this work?"
			hint = "Type close, then Enter. jk / Esc goes back."
			put(x+2, y+4, clipStatus(workspaceTarget(v), width-4), "1;7")
			put(x+2, y+5, "This stops managed programs and removes saved screens.", "7")
		case wsLaunch:
			label = "Start a terminal"
			put(x+3, y+3, "c   Codex", "1;7")
			put(x+3, y+5, "a   Claude", "1;7")
			put(x+3, y+7, "s   Shell", "1;7")
			put(x+3, y+9, "r   Custom command", "1;7")
			hint = "Choose a letter · jk / Esc goes back"
		case wsMove:
			label = "Move to another group"
			available := []terminal.GroupSummary{}
			for _, g := range v.groups {
				if g.ID != v.group && !g.Archived {
					available = append(available, g)
				}
			}
			first := max(v.aux-6, 0)
			for i := first; i < len(available) && i < first+7; i++ {
				mark := "  "
				if i == v.aux {
					mark = "› "
				}
				put(x+3, y+3+i-first, clipStatus(mark+available[i].Name, width-6), "1;7")
			}
			if len(available) == 0 {
				put(x+3, y+4, "Create another group first.", "7")
			}
			hint = "j/k choose · Enter moves it · jk / Esc goes back"
		case wsHelp:
			label = "Your workspace"
			help := []string{"j/k  Move          Enter  Open / take control", "jk   Back one level     v  Pin / unpin live pane", "n    New           r      Rename", "m    Move terminal x      Close / remove", "jk   Back anywhere h      Show saved / archived work", "a    Outside agents / archive a group", "/    Search        :      Command", "q    Quit browser; work keeps running"}
			for i, text := range help {
				put(x+2, y+2+i, text, "7")
			}
			hint = "jk, Esc, q or ? returns"
		}
		put(x+2, y+1, label, "1;7")
		put(x+2, y+10, clipStatus(hint, width-4), "7")
		if v.mode == wsNewGroup || v.mode == wsRun || v.mode == wsRename || v.mode == wsConfirm || v.mode == wsResume {
			put(x+2, y+7, "› "+v.text, "1;7")
			fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", y+8, min(x+4+textWidth(v.text), cols))
		}
	} else if v.mode == wsSearch || v.mode == wsCommand {
		prefix := "/ "
		if v.mode == wsCommand {
			prefix = ": "
		}
		put(1, rows-1, strings.Repeat(" ", max(cols-2, 0)), "7")
		put(2, rows-1, prefix+v.text, "1;7")
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", rows, min(3+textWidth(prefix+v.text), cols))
	}
	out.WriteString("\x1b[0m\x1b[?7h\x1b[?2026l")
	out.WriteString(workspaceBrand(cols, rows, v.brandPhase, v.color))
	return start + preview.String() + out.String()
}
func workspaceTerminalState(r terminal.SessionSummary) string {
	if r.External != nil || r.Local != nil {
		return sessionState(r)
	}
	if r.Attention > 0 {
		return "Attention"
	}
	if r.HumanControl {
		return "Human"
	}
	if r.State == terminal.OverviewRunning {
		return "Running"
	}
	if r.ExitCode == nil {
		return "Saved"
	}
	if *r.ExitCode == 0 {
		return "Finished"
	}
	return fmt.Sprintf("Exit %d", *r.ExitCode)
}

func workspaceTarget(v workspaceView) string {
	if v.group == "" {
		for _, g := range v.groups {
			if string(g.ID) == v.target {
				return fmt.Sprintf("%s · %d terminals", g.Name, g.Terminals)
			}
		}
	}
	for _, r := range v.rows {
		if string(r.ID) == v.target {
			return r.Name + " · " + string(r.ID)
		}
	}
	return v.target
}
