package main

import (
	"bytes"
	"fmt"
	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
	"os"
	"path/filepath"
	"strings"
)

// statusEscape consumes one CSI/SS3 key sequence, including split writes.
// Unknown keys are ignored as a whole rather than leaking into the filter.
func statusEscape(p []byte) (consumed, direction int) {
	if len(p) < 2 {
		return 0, 0
	}
	if strings.ContainsRune("]P_^X", rune(p[1])) {
		for i := 2; i < len(p); i++ {
			if p[i] == 7 && p[1] == ']' || i > 2 && p[i] == '\\' && p[i-1] == 0x1b {
				return i + 1, 0
			}
		}
		return 0, 0
	}
	if p[1] != '[' && p[1] != 'O' {
		return 1, 0
	}
	if len(p) >= 3 && bytes.Equal(p[:3], []byte("\x1b[M")) {
		n, complete := client.MouseReportLength(p)
		if !complete {
			return 0, 0
		}
		return n, 0
	}
	for i := 2; i < len(p); i++ {
		if i >= 128 {
			return i, 0
		}
		if p[i] >= 0x40 && p[i] <= 0x7e {
			switch p[i] {
			case 'A':
				return i + 1, -1
			case 'B':
				return i + 1, 1
			}
			return i + 1, 0
		}
		if p[i] < 0x20 || p[i] > 0x3f {
			return i + 1, 0
		}
	}
	return 0, 0
}
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
	provider := sessionProvider(row)
	switch provider {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	}
	return "Terminal"
}

func sessionProvider(row terminal.SessionSummary) string {
	provider := row.Agent
	if row.External != nil {
		provider = string(row.External.Ref.Provider)
	} else if row.Source.Provider != "" {
		provider = string(row.Source.Provider)
	} else if row.Local != nil {
		provider = string(row.Local.Provider)
	}
	if provider == "" {
		command := filepath.Base(row.Command)
		if command == "codex" || command == "claude" {
			provider = command
		}
	}
	return provider
}

func (v workspaceView) sessionTitle(row terminal.SessionSummary) string {
	return v.sessionCaption(row).Title(v.titles[row.ID])
}
func (v workspaceView) sessionLabel(row terminal.SessionSummary) string {
	return v.sessionCaption(row).Session(v.titles[row.ID])
}
func (v workspaceView) sessionCaption(row terminal.SessionSummary) client.SessionCaption {
	return client.SessionCaption{Name: sessionName(row), Provider: sessionProvider(row), Captain: v.isCaptain(row)}
}
func sessionState(row terminal.SessionSummary) string {
	if row.Local != nil {
		return "Original tab"
	}
	if row.External != nil {
		s := row.External
		switch s.Action {
		case native.Background:
			return "Needs /bg"
		case native.Resume:
			return "Saved"
		case native.Unavailable:
			if s.Status == "not_saved" {
				return "Not saved yet"
			}
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

const statusEnter = "\x1b[?1049h\x1b[?25l\x1b[?2027l\x1b[?2004h\x1b[?9l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1015l\x1b[?1016l\x1b[?1000h\x1b[?1006h"
