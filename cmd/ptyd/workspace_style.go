package main

import (
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type workspaceTheme struct{ selection, surface string }

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
	return workspaceTheme{selection: "1;" + blend(16), surface: blend(6)}
}
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

func workspaceKeys(v workspaceView, cols, y int, put func(int, int, string, string)) {
	hints := []workspaceKeyHint{{"j/k", "move"}, {"Enter", "open"}, {"n", "new"}, {"/", "search"}, {"jk", "back"}, {"?", "help"}, {"q", "quit"}}
	if v.group != "" {
		hints = slices.Insert(hints, 2, workspaceKeyHint{"v", "pin"})
	}
	switch v.mode {
	case wsHelp:
		hints = []workspaceKeyHint{{"jk", "back"}, {"q", "close"}}
	case wsLaunch:
		hints = []workspaceKeyHint{{"c/a/s/r", "choose"}, {"f", "access"}, {"jk", "back"}}
	case wsMove:
		hints = []workspaceKeyHint{{"j/k", "choose"}, {"Enter", "move"}, {"jk", "back"}}
	case wsNewGroup:
		hints = []workspaceKeyHint{{"Enter", "create"}, {"jk", "cancel"}}
	case wsRun:
		hints = []workspaceKeyHint{{"Enter", "start"}, {"jk", "cancel"}}
	case wsRename:
		hints = []workspaceKeyHint{{"Enter", "save"}, {"jk", "cancel"}}
	case wsConfirm, wsResume:
		hints = []workspaceKeyHint{{"Enter", "confirm"}, {"jk", "cancel"}}
	}
	width := func() int {
		n := 0
		for _, h := range hints {
			n += textWidth(h.key) + 1 + textWidth(h.label) + 2
		}
		return max(n-2, 0)
	}
	for _, key := range []string{"/", "n", "j/k", "v"} {
		if width() <= cols-4 {
			break
		}
		for i, h := range hints {
			if h.key == key {
				hints = append(hints[:i], hints[i+1:]...)
				break
			}
		}
	}
	x := 2
	for _, h := range hints {
		put(x, y, h.key, "1;36")
		x += textWidth(h.key) + 1
		put(x, y, h.label, "2")
		x += textWidth(h.label) + 2
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
