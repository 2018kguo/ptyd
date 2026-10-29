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
	return renderWorkspace(v, cols, rows).ANSI
}

func renderWorkspace(v workspaceView, cols, rows int) workspaceScene {
	var hits []workspaceHit
	hit := func(h workspaceHit) { hits = append(hits, h) }
	cols = max(cols, 1)
	rows = max(rows, 1)
	if cols < 40 || rows < 16 {
		return workspaceScene{ANSI: "\x1b[?2026h\x1b[H\x1b[2J\x1b[0m" + clipStatus("ptyd · enlarge to 40×16", cols) + "\r\n" + clipStatus("q detaches · work keeps running", cols) + "\x1b[?2026l"}
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
	headingWidth := cols - workspaceBrandReserve(cols) - 10
	if cols >= 96 {
		headingWidth = min(headingWidth, cols/2-10)
	}
	headingStyle := "1"
	if v.group != "" {
		headingStyle += ";" + v.theme.identity(string(v.group))
	}
	put(8, 1, clipStatus("/  "+heading, headingWidth), headingStyle)
	title := "ptyd · " + heading
	fmt.Fprintf(&out, "\x1b]2;%s\x1b\\", displayText(title, 160))
	if v.group == "" {
		workspaceHome(&preview, v, cols, rows, put, line, hit)
	} else {
		g, ok := v.groupInfo()
		if ok {
			put(2, 2, clipStatus(friendlyCWD(g.CWD), cols-workspaceBrandReserve(cols)-4), "2")
			summary := fmt.Sprintf("%d running  ·  %s", g.Running, workspaceCount(g.Terminals, "terminal"))
			if g.Attention > 0 {
				summary += fmt.Sprintf("  ·  %d need attention", g.Attention)
			}
			if cols >= 96 {
				put(max(cols-textWidth(summary)-workspaceBrandReserve(cols)-3, cols/2), 1, clipStatus(summary, cols/2-workspaceBrandReserve(cols)-3), "36")
			} else {
				put(2, 3, clipStatus(summary, cols-4), "36")
			}
		}
		if ok && g.Archived {
			put(2, 3, "Archived · return to groups and press a to unarchive.", "33")
		}
		if v.outside {
			put(2, 3, "Outside agents shown. Enter imports a supported conversation into this group.", "33")
		}
		left := cols
		if cols >= 96 {
			left = workspaceSidebarWidth(cols)
		}
		line(4)
		if left < cols {
			workspaceBox(workspaceRect{1, 4, left, rows - 9}, "2", put)
		}
		put(2, 4, " TERMINALS ", "1;36")
		if left < cols {
			put(left+2, 4, workspaceWatchHeading(v, workspaceRect{left + 3, 6, cols - left - 6, rows - 11}), "1;36")
		}
		// Rows start at 6 and end above the box's bottom edge at rows-6.
		rowHeight := 2
		if strings.TrimSpace(v.query) != "" {
			rowHeight = 3
		}
		available := max((rows-12)/rowHeight, 1)
		if left == cols {
			available = min(max(len(v.rows), 1), max((rows-13)/(rowHeight+1), 1))
		}
		start := max(v.index()-available+1, 0)
		for i := start; i < len(v.rows) && i < start+available; i++ {
			r := v.rows[i]
			y := 6 + (i-start)*rowHeight
			style := ""
			mark := "  "
			if string(r.ID) == v.selected {
				style = v.theme.selected()
				mark = "› "
			}
			status := sessionApp(r) + " · " + workspaceTerminalIcon(r) + " " + workspaceTerminalState(r)
			name := v.sessionTitle(r)
			if v.isCaptain(r) {
				name = "★ " + name
			}
			if slices.Contains(v.watched(), r.ID) {
				name = "◆ " + name
			}
			nameWidth := max(left-6, 5)
			put(2, y, statusCell("", max(left-2, 1)), style)
			nameStyle := v.theme.identity(string(r.ID))
			if style != "" {
				nameStyle = style + ";" + nameStyle
			}
			put(2, y, mark+statusCell(name, nameWidth), nameStyle)
			if v.mode == wsNormal || v.mode == wsSearch {
				hit(workspaceHit{Rect: workspaceRect{2, y, left - 3, rowHeight}, Kind: workspaceHitTerminal, ID: string(r.ID), Group: v.group})
			}
			if style == "" {
				if r.Attention > 0 {
					style = "33"
				} else if r.State == terminal.OverviewRunning {
					style = "32"
				} else {
					style = "2"
				}
			}
			put(4, y+1, clipStatus(status, left-7), style)
			if rowHeight == 3 {
				if hit := v.contentMatch(r.ID); hit != nil {
					put(4, y+2, clipStatus(hit.Source+": "+hit.Text, left-7), "2")
				}
			}
		}
		if len(v.rows) == 0 && v.snapshot != nil {
			heading := "This group is ready."
			if v.query != "" {
				heading = "No terminals match your search."
				if v.snapshot.Search != nil && !v.snapshot.Search.Complete {
					heading = "No confirmed matches; search is incomplete."
				}
			} else if v.outside {
				heading = "No terminals received in this view."
			} else if !v.history && v.snapshot.Overview.HistoryHidden > 0 {
				heading = "Press h to see finished terminals."
			}
			leftPut := func(y int, text, style string) {
				if y < rows-5 {
					put(3, y, clipStatus(text, left-6), style)
				}
			}
			leftPut(7, heading, "1")
			leftPut(9, "n  Start an agent or shell", "36")
			leftPut(11, "a  Bring an existing agent here", "2")
			leftPut(13, "Or ask an agent to use this group.", "2")
		}
		if left < cols {
			workspaceWatchPanel(&preview, v, workspaceRect{left + 3, 6, max(cols-left-6, 1), max(rows-11, 1)}, cols, put, hit)
		}
		if left == cols {
			y := 7 + available*rowHeight
			area := workspaceRect{2, y, cols - 4, max(rows-y-5, 1)}
			if n := len(v.watchPanes()); n > 1 {
				label := fmt.Sprintf(" %d/%d panes ", min(n, workspacePaneCapacity(area)), n)
				put(cols-textWidth(label)-2, 4, label, "2")
			}
			workspaceWatchPanel(&preview, v, area, cols, put, hit)
		}
		if row, ok := v.row(); ok {
			detail := v.sessionCaption(row).Label(v.titles[row.ID]) + " · " + workspaceTerminalState(row)
			hint := detail
			if row.State != terminal.OverviewRunning && row.External == nil {
				hint = detail + " · R relaunches"
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
			put(2, rows-4, clipStatus(hint, cols-4), "2")
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
	if strings.TrimSpace(v.query) != "" {
		if v.snapshot == nil || !v.snapshot.Query.matches(v) {
			notice = "Searching terminal contents…"
		} else if v.snapshot.Search != nil && !v.snapshot.Search.Complete {
			notice = fmt.Sprintf("Search incomplete · read %d of %d terminals. Narrow to a group or open unreadable screens.", v.snapshot.Search.Checked, v.snapshot.Search.Candidates)
		}
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
		style := "33"
		if notice == "Returned to your workspace." {
			style = "2"
		}
		put(2, max(rows-5, 3), clipStatus(notice, cols-4), style)
	}
	line(rows - 3)
	workspaceKeys(v, cols, rows-2, put, func(h workspaceHit) {
		if v.mode != wsSearch && v.mode != wsCommand {
			hit(h)
		}
	})
	if v.mode != wsNormal && v.mode != wsSearch && v.mode != wsCommand {
		x, y := max((cols-min(cols-4, 70))/2, 1), max((rows-14)/2, 2)
		width := max(min(cols-4, 70), 1)
		for i := 0; i < 12 && y+i < rows-2; i++ {
			put(x, y+i, strings.Repeat(" ", width), v.theme.onSurface(""))
		}
		workspaceBox(workspaceRect{x, y, width, 12}, "36", put)
		pagePut := put
		put := func(px, py int, text, style string) {
			if px > x && px < x+width-1 && py > y && py < y+11 {
				pagePut(px, py, clipStatus(text, x+width-1-px), v.theme.onSurface(style))
			}
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
			put(x+2, y+4, "Starts its agent again in this group.", "")
		case wsConfirm:
			label = "Close and remove this work?"
			hint = "Type close, then Enter. jk / Esc goes back."
			put(x+2, y+4, clipStatus(workspaceTarget(v), width-4), "1")
			put(x+2, y+5, "This stops managed programs and removes saved screens.", "")
		case wsLaunch:
			label = "Start a terminal"
			put(x+3, y+3, "c   Codex", "1")
			put(x+3, y+4, "a   Claude", "1")
			put(x+3, y+5, "s   Shell", "1")
			put(x+3, y+6, "r   Custom command", "1")
			access, detail := "Full (YOLO)", "Codex + Claude: skip approvals"
			if v.agentAccess == workspaceCLISettings {
				access, detail = "CLI settings", "Uses your CLI permission settings"
			}
			put(x+3, y+8, "f   Access: "+access, "1;36")
			put(x+3, y+9, detail, "2")
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
				style := ""
				if i == v.aux {
					style = v.theme.selected()
				}
				put(x+3, y+3+i-first, clipStatus(mark+available[i].Name, width-6), style)
				hit(workspaceHit{Rect: workspaceRect{x + 2, y + 3 + i - first, width - 4, 1}, Kind: workspaceHitDestination, ID: string(available[i].ID)})
			}
			if len(available) == 0 {
				put(x+3, y+4, "Create another group first.", "")
			}
			hint = "j/k choose · Enter moves it · jk / Esc goes back"
		case wsHelp:
			label = "Your workspace"
			help := []string{"j/k  Move       Enter  Open", "jk   Back       v      Pin / unpin", "n    New        c      Captain", "r/m  Rename/move  x    Close", "h    Finished   a      Outside / archive", "/    Search screens and scrollback", "Click selects; double-click opens", "q    Quit; work keeps running"}
			for i, text := range help {
				put(x+2, y+2+i, text, "")
			}
			hint = "jk, Esc, q or ? returns"
		}
		if v.mode == wsLaunch {
			for _, choice := range []struct {
				row int
				key rune
			}{{3, 'c'}, {4, 'a'}, {5, 's'}, {6, 'r'}, {8, 'f'}} {
				hit(workspaceHit{Rect: workspaceRect{x + 2, y + choice.row, width - 4, 1}, Kind: workspaceHitKey, Key: workspaceKey{Rune: choice.key}})
			}
		}
		put(x+2, y+1, label, "1;36")
		put(x+2, y+10, clipStatus(hint, width-4), "2")
		if v.mode == wsNewGroup || v.mode == wsRun || v.mode == wsRename || v.mode == wsConfirm || v.mode == wsResume {
			text := workspaceInputTail(v.text, width-7)
			put(x+2, y+7, "› "+text, "1")
			fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", y+8, x+5+textWidth(text))
		}
	} else if v.mode == wsSearch || v.mode == wsCommand {
		prefix := "/ "
		hint := "Enter applies · jk cancels"
		if cols >= 80 {
			hint += " · screens + scrollback"
		}
		if v.mode == wsCommand {
			prefix = ": "
			hint = "Enter runs the command · jk cancels"
		}
		put(2, rows-4, clipStatus(hint, cols-4), "2")
		enterWidth := textWidth(strings.Split(hint, " · ")[0])
		hit(workspaceHit{Rect: workspaceRect{2, rows - 4, enterWidth, 1}, Kind: workspaceHitKey, Key: workspaceKey{Rune: '\r'}})
		if at := strings.Index(hint, "jk cancels"); at >= 0 {
			hit(workspaceHit{Rect: workspaceRect{2 + textWidth(hint[:at]), rows - 4, textWidth("jk cancels"), 1}, Kind: workspaceHitKey, Key: workspaceKey{Back: true}})
		}
		text := workspaceInputTail(v.text, cols-7)
		put(1, rows-2, strings.Repeat(" ", max(cols-2, 0)), v.theme.onSurface(""))
		put(2, rows-2, prefix+text, v.theme.onSurface("1"))
		fmt.Fprintf(&out, "\x1b[%d;%dH\x1b[?25h", rows-1, 3+textWidth(prefix+text))
	}
	out.WriteString("\x1b[0m\x1b[?7h\x1b[?2026l")
	out.WriteString(workspaceBrand(cols, rows, v.brandPhase, v.color))
	return workspaceScene{ANSI: start + preview.String() + out.String(), Hits: hits}
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
