package client

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/2018kguo/phosphor"
)

type paneStatusKey struct {
	cols                   int
	label, state           string
	identity, groupID      string
	foreground, background phosphor.RGB
}

func (p *pane) appendStatus(b []byte, force bool) []byte {
	state := "attached"
	if p.viewOnly {
		state = "viewing"
	}
	if p.finished {
		state = "finished"
	}
	palette := p.screen.Palette()
	if p.viewerPalette != nil {
		palette = *p.viewerPalette
	}
	label := p.label
	if p.caption != nil {
		label = p.caption.Label(p.screen.Title())
	}
	key := paneStatusKey{cols: p.cols, label: label, state: state, identity: p.identity, groupID: p.groupID, foreground: palette.Foreground, background: palette.Background}
	if key != p.statusKey || len(p.status) == 0 {
		p.statusKey = key
		p.status = appendPaneStatus(p.status[:0], key)
		force = true
	}
	if force {
		b = fmt.Appendf(b, "\x1b[%d;1H", p.rows)
		b = append(b, p.status...)
	}
	return b
}

func appendPaneStatus(b []byte, key paneStatusKey) []byte {
	bg, fg := key.background, key.foreground
	mix := func(percent int) phosphor.RGB {
		channel := func(a, b uint8) uint8 { return uint8((int(a)*(100-percent) + int(b)*percent) / 100) }
		return phosphor.RGB{R: channel(bg.R, fg.R), G: channel(bg.G, fg.G), B: channel(bg.B, fg.B)}
	}
	surface, muted := mix(7), mix(65)
	accent := phosphor.RGB{R: 31, G: 115, B: 106}
	if 299*int(bg.R)+587*int(bg.G)+114*int(bg.B) < 128000 {
		accent = phosphor.RGB{R: 137, G: 198, B: 190}
	}
	if key.identity != "" {
		accent = IdentityColor(key.identity, bg)
	}
	if key.state == "finished" {
		accent = muted
	}
	b = fmt.Appendf(b, "\x1b[0m\x1b]8;;\x1b\\\x1b[?2027l\x1b[48;2;%d;%d;%dm\x1b[2K", surface.R, surface.G, surface.B)
	put := func(text string, color phosphor.RGB, bold bool) {
		weight := 22
		if bold {
			weight = 1
		}
		b = fmt.Appendf(b, "\x1b[%d;38;2;%d;%d;%dm%s", weight, color.R, color.G, color.B, text)
	}
	if key.cols < 18 {
		text := "ptyd · jk: back"
		if key.cols < displayWidth(text) {
			text = "jk: back"
		}
		text = clipPane(text, key.cols)
		put(text, fg, false)
		put(strings.Repeat(" ", max(key.cols-displayWidth(text), 0)), fg, false)
		return append(b, "\x1b[0m"...)
	}
	var clean strings.Builder
	for _, r := range key.label {
		if !unicode.IsControl(r) {
			clean.WriteRune(r)
		}
	}
	hint := "jk: back"
	right := hint
	if key.cols >= 48 {
		right = key.state + "   " + hint
	}
	left := " ● ptyd"
	name := clipPane(clean.String(), max(key.cols-displayWidth(left)-displayWidth(right)-7, 0))
	if name != "" {
		left += "  │  " + name
	}
	// The name has lower priority than the return hint at small widths.
	padding := max(key.cols-displayWidth(left)-displayWidth(right)-1, 1)
	put(" ● ", accent, false)
	put("ptyd", accent, true)
	if name != "" {
		put("  │  ", muted, false)
		if group, terminal, ok := strings.Cut(name, " / "); ok {
			groupColor := muted
			if key.groupID != "" {
				groupColor = IdentityColor(key.groupID, bg)
			}
			put(group, groupColor, false)
			put(" / ", muted, false)
			put(terminal, accent, true)
		} else {
			put(name, accent, true)
		}
	}
	put(strings.Repeat(" ", padding), muted, false)
	if right != hint {
		put(key.state+"   ", muted, false)
	}
	from, _ := paneBackColumns(key.cols)
	b = fmt.Appendf(b, "\x1b[%dG", from)
	put(hint, accent, true)
	put(" ", fg, false)
	return append(b, "\x1b[0m"...)
}

func paneBackColumns(cols int) (int, int) {
	if cols >= 18 {
		return cols - displayWidth("jk: back"), cols - 1
	}
	if cols >= displayWidth("ptyd · jk: back") {
		return displayWidth("ptyd · ") + 1, displayWidth("ptyd · jk: back")
	}
	return 1, min(cols, displayWidth("jk: back"))
}
func (p *pane) backButton() (rows, from, to int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.haveModes || len(p.status) == 0 || p.statusKey.cols != p.cols || p.lastModes.MouseEncoding == 1016 {
		return 0, 0, 0
	}
	from, to = paneBackColumns(p.cols)
	return p.rows - 1, from, to
}

func (p *pane) escapeDisambiguated() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.haveModes && p.lastModes.KittyKeyboard&1 != 0
}
