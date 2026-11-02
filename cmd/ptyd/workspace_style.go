package main

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type workspaceTheme struct {
	selection, surface string
	background         phosphor.RGB
}

func workspaceThemeFor(colors *terminal.TerminalColors) workspaceTheme {
	if colors == nil || len(colors.Foreground) != 7 || len(colors.Background) != 7 || colors.Foreground[0] != '#' || colors.Background[0] != '#' {
		return workspaceTheme{}
	}
	fg, e1 := hex.DecodeString(strings.TrimPrefix(colors.Foreground, "#"))
	bg, e2 := hex.DecodeString(strings.TrimPrefix(colors.Background, "#"))
	if e1 != nil || e2 != nil || len(fg) != 3 || len(bg) != 3 {
		return workspaceTheme{}
	}
	blend := func(percent int) string {
		rgb := [3]int{}
		for i := range rgb {
			rgb[i] = (int(bg[i])*(100-percent) + int(fg[i])*percent) / 100
		}
		return fmt.Sprintf("48;2;%d;%d;%d", rgb[0], rgb[1], rgb[2])
	}
	return workspaceTheme{selection: "1;" + blend(16), surface: blend(6), background: phosphor.RGB{R: bg[0], G: bg[1], B: bg[2]}}
}
func (t workspaceTheme) identity(id string) string { return client.IdentityStyle(id, t.background) }
func (t workspaceTheme) selected() string {
	if t.selection == "" {
		return "1;36"
	}
	return t.selection
}
func (t workspaceTheme) onSurface(style string) string {
	if t.surface == "" {
		return style
	}
	if style == "" {
		return t.surface
	}
	return t.surface + ";" + style
}

func workspaceBox(r workspaceRect, style string, put func(int, int, string, string)) {
	if r.w < 2 || r.h < 2 {
		return
	}
	put(r.x, r.y, "╭"+strings.Repeat("─", r.w-2)+"╮", style)
	for y := r.y + 1; y < r.y+r.h-1; y++ {
		put(r.x, y, "│", style)
		put(r.x+r.w-1, y, "│", style)
	}
	put(r.x, r.y+r.h-1, "╰"+strings.Repeat("─", r.w-2)+"╯", style)
}

type workspaceKeyHint struct{ key, label string }

func workspaceSidebarWidth(cols int) int { return min(max(cols/5, 26), 32) }

// workspaceDock lists every control that works in the current view and for the
// current selection, wrapped into as many rows as cols needs.
func workspaceDock(v workspaceView, cols int) [][]workspaceKeyHint {
	var hints []workspaceKeyHint
	add := func(key, label string) { hints = append(hints, workspaceKeyHint{key, label}) }
	switch v.mode {
	case wsSearch:
		return [][]workspaceKeyHint{nil}
	case wsLaunch:
		add("c/a/s/r", "choose")
		add("f", "access")
		add("jk", "back")
	case wsMove:
		add("j/k", "choose")
		add("Enter", "move")
		add("jk", "back")
	case wsNewGroup:
		add("Enter", "create")
		add("jk", "cancel")
	case wsRun:
		add("Enter", "start")
		add("jk", "cancel")
	case wsRename:
		add("Enter", "save")
		add("jk", "cancel")
	case wsConfirm, wsResume:
		add("Enter", "confirm")
		add("jk", "cancel")
	default:
		add("j/k", "move")
		if v.group == "" {
			selected := false
			archived := false
			for _, g := range v.groups {
				if string(g.ID) == v.selected {
					selected, archived = true, g.Archived
				}
			}
			if selected {
				add("Enter", "open")
			}
			add("n", "new group")
			if selected {
				add("r", "rename")
				if archived {
					add("a", "unarchive")
				} else {
					add("a", "archive")
				}
				add("x", "close")
			}
			if v.history {
				add("h", "hide archived")
			} else {
				add("h", "show archived")
			}
		} else {
			row, ok := v.row()
			managed := ok && row.External == nil && row.Local == nil
			switch {
			case ok && row.External != nil && row.External.Action == native.Attach:
				add("Enter", "import")
			case ok && row.External != nil && row.External.Action == native.Resume:
				add("Enter", "resume")
			case managed && row.State != terminal.OverviewRunning:
				add("Enter", "history")
				add("R", "relaunch")
			case managed:
				add("Enter", "open")
			}
			add("n", "new")
			if managed {
				if slices.Contains(v.watched(), row.ID) {
					add("v", "unpin")
				} else {
					add("v", "pin")
				}
				if v.isCaptain(row) {
					add("c", "clear captain")
				} else {
					add("c", "captain")
				}
				add("r", "rename")
				add("m", "move")
				add("x", "close")
			}
			if v.outside {
				add("a", "hide outside")
			} else {
				add("a", "outside agents")
			}
		}
		add("/", "search")
		if v.group != "" {
			add("jk", "back")
		}
		add("q", "quit")
	}
	lines := [][]workspaceKeyHint{nil}
	width := 0
	for _, h := range hints {
		w := textWidth(h.key) + 1 + textWidth(h.label)
		if width > 0 && width+2+w > cols-4 {
			lines = append(lines, nil)
			width = 0
		}
		if width > 0 {
			width += 2
		}
		width += w
		lines[len(lines)-1] = append(lines[len(lines)-1], h)
	}
	return lines
}

// workspaceKeys draws dock (from workspaceDock) from row y down; each hint is clickable.
func workspaceKeys(dock [][]workspaceKeyHint, y int, put func(int, int, string, string), hit func(workspaceHit)) {
	for i, hints := range dock {
		x := 2
		for _, h := range hints {
			var key workspaceKey
			valid := true
			switch h.key {
			case "jk":
				key.Back = true
			case "Enter":
				key.Rune = '\r'
			default:
				if len([]rune(h.key)) == 1 {
					key.Rune = []rune(h.key)[0]
				} else {
					valid = false
				}
			}
			if valid {
				hit(workspaceHit{Rect: workspaceRect{x, y + i, textWidth(h.key) + 1 + textWidth(h.label), 1}, Kind: workspaceHitKey, Key: key})
			}
			put(x, y+i, h.key, "1;36")
			x += textWidth(h.key) + 1
			put(x, y+i, h.label, "2")
			x += textWidth(h.label) + 2
		}
	}
}

func workspaceInputTail(text string, width int) string {
	// Sanitize the whole input before taking its end; the input budget counts
	// runes, so a display-column cap would discard the suffix of wide text.
	text = displayText(text, len(text))
	if textWidth(text) <= width {
		return text
	}
	if width < 2 {
		return ""
	}
	runes := []rune(text)
	i := len(runes)
	used := 1
	for i > 0 {
		w := max(phosphor.CodepointWidth(runes[i-1]), 0)
		if used+w > width {
			break
		}
		used += w
		i--
	}
	for i < len(runes) && phosphor.CodepointWidth(runes[i]) == 0 {
		i++
	}
	return "…" + string(runes[i:])
}
