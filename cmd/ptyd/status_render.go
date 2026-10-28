package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type statusLine struct{ text, style string }

func textWidth(s string) int {
	n := 0
	for _, r := range s {
		n += max(phosphor.CodepointWidth(r), 0)
	}
	return n
}
func clipStatus(s string, width int) string {
	if width < 1 {
		return ""
	}
	if textWidth(s) <= width {
		return displayText(s, width)
	}
	return displayText(s, max(width-1, 0)) + "…"
}
func statusCell(s string, width int) string {
	s = clipStatus(s, width)
	return s + strings.Repeat(" ", max(width-textWidth(s), 0))
}

func sessionApp(row terminal.SessionSummary) string {
	provider := row.Agent
	if row.External != nil {
		provider = string(row.External.Ref.Provider)
	} else if row.Source.Provider != "" {
		provider = string(row.Source.Provider)
	}
	switch provider {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	}
	return "Terminal"
}
func sessionState(row terminal.SessionSummary) string {
	if row.External != nil {
		s := row.External
		switch s.Action {
		case native.Background:
			return "Needs /bg"
		case native.Resume:
			return "Saved"
		case native.Unavailable:
			return "Unavailable"
		}
		if s.WaitingFor != "" {
			return "Needs input"
		}
		switch strings.ToLower(s.Status) {
		case "active", "busy", "working":
			return "Working"
		case "waiting":
			return "Needs input"
		case "idle":
			return "Ready"
		}
		return "Ready"
	}
	if row.State == terminal.OverviewRunning {
		if row.Attached > 0 {
			return fmt.Sprintf("Open · %d viewing", row.Attached)
		}
		return "Running"
	}
	if row.State == terminal.OverviewStopped {
		return "Saved"
	}
	if row.ExitCode != nil && *row.ExitCode != 0 {
		return "Exited with error"
	}
	return "Finished"
}
func sessionName(row terminal.SessionSummary) string {
	synthetic := row.External != nil && row.Name == row.External.Ref.Key()
	if row.Source.Provider != "" && len(row.Source.ID) >= 8 && row.Name == string(row.Source.Provider)+"-"+row.Source.ID[:8] {
		synthetic = true
	}
	if row.Name != "" && !synthetic {
		return row.Name
	}
	if row.CWD != "" {
		return filepath.Base(row.CWD)
	}
	return sessionApp(row) + " session"
}
func sessionGroup(row terminal.SessionSummary) string {
	if row.External == nil {
		if row.State == terminal.OverviewRunning {
			return "Open terminals"
		}
		return "History"
	}
	switch row.External.Action {
	case native.Attach:
		return "Agents"
	case native.Background, native.Unavailable:
		return "Needs attention"
	default:
		return "History"
	}
}
func friendlyCWD(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if path == home {
			return "~"
		}
		if strings.HasPrefix(path, home+"/") {
			return "~" + strings.TrimPrefix(path, home)
		}
	}
	return path
}

func statusNotice(v statusView) statusLine {
	if v.err != nil {
		var response *client.HTTPError
		switch {
		case errors.Is(v.err, client.ErrOverviewCapabilities), errors.As(v.err, &response) && response.Status == 404:
			return statusLine{"Restart ptyd after upgrading, or check PTYD_ADDR. Press r to retry.", "33"}
		case errors.Is(v.err, client.ErrDaemonDown):
			return statusLine{"Ptyd is offline. Start it with ptyd, then press r.", "33"}
		default:
			return statusLine{"Refresh failed. Showing the last received list.", "33"}
		}
	} else if v.snapshot == nil || v.snapshot.query != v.query || v.snapshot.history != v.history {
		return statusLine{"Updating sessions…", "2"}
	} else if v.notice != "" {
		return statusLine{v.notice, "33"}
	} else if len(v.snapshot.view.DiscoveryIssues) > 0 || v.snapshot.view.DiscoveryTruncated {
		return statusLine{"Some agents could not be discovered. Press ? for help.", "33"}
	}
	return statusLine{}
}

func statusFrame(v statusView, cols, rows int) string {
	width := max(cols-1, 1)
	lines := []statusLine{{"ptyd  /  Sessions", "1;36"}}
	add := func(text, style string) { lines = append(lines, statusLine{text, style}) }
	if v.snapshot == nil {
		add("Finding your sessions…", "2")
	} else {
		view := v.snapshot.view
		count := fmt.Sprintf("%d sessions", view.Counts.Total)
		if view.Counts.Total == 1 {
			count = "1 session"
		}
		if v.history {
			count += " · history shown"
		} else if view.HistoryHidden > 0 {
			count += fmt.Sprintf(" · %d saved in history", view.HistoryHidden)
		}
		if view.Truncated {
			count += fmt.Sprintf(" · showing %d", len(view.Sessions))
		}
		add(count, "2")
	}
	lines = append(lines, statusNotice(v))
	switch v.mode {
	case statusSearch:
		add("Search  /"+v.query, "1")
	case statusCommand:
		add(":"+v.command, "1")
	default:
		if v.query != "" {
			add("Search: "+v.query, "2")
		} else {
			add("/ Search by name or folder", "2")
		}
	}
	add("", "")
	wide := cols >= 70
	nameWidth := max(width-19, 10)
	if wide {
		nameWidth = max(width-30, 12)
	}
	head := "  " + statusCell("SESSION", nameWidth) + "  "
	if wide {
		head += statusCell("APP", 8) + "  "
	}
	add(head+"STATUS", "2")
	available := max(rows-17, 1)
	selected := v.selectedIndex()
	start := max(selected-available+1, 0)
	group := ""
	for i := start; i < len(v.sessions()) && i < start+available; i++ {
		row := v.sessions()[i]
		next := sessionGroup(row)
		if next != group {
			add(next, "1;2")
			group = next
		}
		marker, style := "  ", ""
		if row.External != nil && row.External.Action != native.Attach {
			style = "33"
		}
		if row.ID == v.selected {
			marker, style = "› ", "1;7"
		}
		text := marker + statusCell(sessionName(row), nameWidth) + "  "
		if wide {
			text += statusCell(sessionApp(row), 8) + "  "
		}
		add(text+sessionState(row), style)
	}
	if v.snapshot != nil && len(v.sessions()) == 0 {
		if v.err != nil || len(v.snapshot.view.DiscoveryIssues) > 0 || v.snapshot.view.DiscoveryTruncated {
			add("No sessions in this list. Discovery is incomplete.", "33")
		} else if v.query != "" {
			add("No sessions match this search.", "2")
		} else if v.history {
			add("No saved sessions.", "2")
		} else {
			add("No live sessions right now.", "1")
			add("Start one with ptyd new, or press h for saved work.", "2")
		}
	}
	if len(v.sessions()) > 0 {
		row := v.sessions()[selected]
		add("", "")
		add(sessionName(row), "1")
		add(sessionApp(row)+" · "+friendlyCWD(row.CWD), "2")
		if row.External != nil && row.External.Action == native.Background {
			add("In the original Claude tab, run /bg. Then press r here.", "33")
		} else if row.External != nil && row.External.Action == native.Resume {
			add("Saved conversation. Resume explicitly with ptyd import --resume.", "2")
		} else if row.External != nil && row.External.Action == native.Unavailable {
			add(row.External.Reason, "33")
		} else if row.State == terminal.OverviewRunning || row.External != nil {
			add("Enter or i to open · quick jk returns to this list", "32")
		} else {
			add("Saved terminal. Enter views its last screen.", "2")
		}

	}
	historyKey := "h history"
	if v.history {
		historyKey = "h live sessions"
	}
	footer := []statusLine{{"", ""}, {"j/k move · Enter open · / search · " + historyKey + " · ? help · q quit", "2"}, {"In an attached session: type j then k quickly to return", "2"}}
	if len(lines) > max(rows-len(footer), 0) {
		lines = lines[:max(rows-len(footer), 0)]
	}
	lines = append(lines, footer...)
	if v.mode == statusHelp {
		lines = []statusLine{{"ptyd  /  Controls", "1;36"}, {"", ""}, {"i / Enter   Open selected session", ""}, {"j / k       Move between sessions", ""}, {"gg / G      First / last session", ""}, {"/           Search by name, app or folder", ""}, {"h           Show or hide saved history", ""}, {"r           Refresh", ""}, {":b name     Open a shown session by name or id", ""}, {":bn / :bp   Open next / previous session", ""}, {":ls         Clear search", ""}, {":q          Leave ptyd; work keeps running", ""}, {"", ""}, {"While attached: quick jk returns to sessions.", "1;32"}, {"The window title identifies the ptyd view.", "2"}, {"Literal jk: pause between letters or use bracketed paste.", "2"}, {"Claude foreground tabs need /bg before ptyd can open them.", "2"}, {"", ""}, {"q, ? or Esc returns to sessions", "2"}}
		if v.snapshot != nil {
			for _, issue := range v.snapshot.view.DiscoveryIssues {
				lines = append(lines, statusLine{string(issue.Provider) + ": " + issue.Message, "33"})
			}
		}
		if len(lines) > max(rows, 1) {
			lines = append(lines[:max(rows-1, 0)], statusLine{"q, ? or Esc returns to sessions", "2"})
		}
	}
	if v.mode != statusHelp && rows < 16 {
		compact := []statusLine{}
		headerRows := 0
		if rows >= 5 {
			header := "ptyd · Sessions"
			if v.mode == statusSearch || v.query != "" {
				header = "Search  /" + v.query
			} else if v.mode == statusCommand {
				header = ":" + v.command
			}
			compact = append(compact, statusLine{header, "1;36"})
			headerRows = 1
			if notice := statusNotice(v); notice.text != "" {
				compact = append(compact, notice)
				headerRows++
			}
		}
		footerRows := min(2, max(rows-1, 0))
		available := max(rows-headerRows-footerRows, 1)
		start := max(v.selectedIndex()-available+1, 0)
		for i := start; i < len(v.sessions()) && i < start+available; i++ {
			row := v.sessions()[i]
			marker, style := "  ", ""
			if row.ID == v.selected {
				marker, style = "› ", "1;7"
			}
			compact = append(compact, statusLine{marker + sessionName(row), style})
		}
		if len(v.sessions()) == 0 {
			text := "No sessions. h shows history."
			if v.err != nil || v.snapshot == nil || len(v.snapshot.view.DiscoveryIssues) > 0 || v.snapshot.view.DiscoveryTruncated {
				text = "Sessions unavailable. r retries."
			}
			compact = append(compact, statusLine{text, "2"})
		}
		if footerRows > 0 {
			compact = append(compact, statusLine{"i open · / find · h history · q quit", "2"})
		}
		if footerRows > 1 {
			compact = append(compact, statusLine{"Attached: quick jk returns here", "2"})
		}
		lines = compact
	}
	var out strings.Builder
	out.WriteString("\x1b[H\x1b[2J")
	for i, line := range lines {
		if i >= max(rows, 1) {
			break
		}
		if i > 0 {
			out.WriteString("\r\n")
		}
		if v.color && line.style != "" {
			out.WriteString("\x1b[" + line.style + "m")
		}
		out.WriteString(clipStatus(line.text, width))
		if v.color && line.style != "" {
			out.WriteString("\x1b[0m")
		}
	}
	return out.String()
}
