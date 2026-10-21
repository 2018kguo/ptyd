package vt

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// appendSGR appends the escape sequence that sets exactly c's style,
// starting from a reset.
func appendSGR(b []byte, c Cell) []byte {
	b = append(b, "\x1b[0"...)
	for _, a := range [...]struct {
		bit  Attr
		code string
	}{
		{attrBold, ";1"}, {attrFaint, ";2"}, {attrItalic, ";3"}, {attrBlink, ";5"},
		{attrInverse, ";7"}, {attrHidden, ";8"}, {attrStrike, ";9"}, {attrOverline, ";53"},
	} {
		if c.A&a.bit != 0 {
			b = append(b, a.code...)
		}
	}
	switch ul := (c.A & ulMask) >> ulShift; ul {
	case 0:
	case 1:
		b = append(b, ";4"...)
	default:
		b = append(b, ";4:"...)
		b = strconv.AppendInt(b, int64(ul), 10)
	}
	b = appendColor(b, c.Fg, 30)
	b = appendColor(b, c.Bg, 40)
	return append(b, 'm')
}

func appendColor(b []byte, c Color, base int) []byte {
	switch {
	case c == 0:
		return b
	case c&colorRGB != 0:
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(base+8), 10)
		b = append(b, ";2;"...)
		b = strconv.AppendInt(b, int64(c>>16&0xff), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(c>>8&0xff), 10)
		b = append(b, ';')
		return strconv.AppendInt(b, int64(c&0xff), 10)
	}
	n := int(c & 0xff)
	b = append(b, ';')
	switch {
	case n < 8:
		return strconv.AppendInt(b, int64(base+n), 10)
	case n < 16:
		return strconv.AppendInt(b, int64(base+60+n-8), 10)
	}
	b = strconv.AppendInt(b, int64(base+8), 10)
	b = append(b, ";5;"...)
	return strconv.AppendInt(b, int64(n), 10)
}

func styleOf(c Cell) Cell { return Cell{Fg: c.Fg, Bg: c.Bg, A: c.A} }

// appendRow appends one row's characters with the escape sequences for
// their styles, starting and ending in the default style. With trim,
// trailing default blanks are left out.
func (t *Terminal) appendRow(b []byte, row []Cell, trim bool) []byte {
	if row == nil {
		if !trim {
			for range t.cols {
				b = append(b, ' ')
			}
		}
		return b
	}
	end := len(row)
	if trim {
		for end > 0 && row[end-1] == (Cell{}) {
			end--
		}
	}
	var style Cell
	for _, c := range row[:end] {
		if c.R == wideTail {
			continue
		}
		if s := styleOf(c); s != style {
			b = appendSGR(b, s)
			style = s
		}
		switch {
		case c.R == 0:
			b = append(b, ' ')
		case c.R >= clusterBase:
			b = append(b, t.x.clusters[c.R-clusterBase]...)
		default:
			b = utf8.AppendRune(b, c.R)
		}
	}
	if style != (Cell{}) {
		b = append(b, "\x1b[0m"...)
	}
	return b
}

// appendWrapped appends a row that continues on next: all its cells (blanks
// as spaces, so the row is full and the next character wraps), but for a
// blank the wide character starting next left behind.
func (t *Terminal) appendWrapped(b []byte, row, next []Cell) []byte {
	end := t.wrapEnd(row, next)
	if row == nil {
		for range end {
			b = append(b, ' ')
		}
		return b
	}
	return t.appendRow(b, row[:end], false)
}

func appendCUP(b []byte, y, x int) []byte {
	b = append(b, "\x1b["...)
	b = strconv.AppendInt(b, int64(y+1), 10)
	b = append(b, ';')
	b = strconv.AppendInt(b, int64(x+1), 10)
	return append(b, 'H')
}

func appendMode(b []byte, mode int, on bool) []byte {
	b = append(b, "\x1b[?"...)
	b = strconv.AppendInt(b, int64(mode), 10)
	if on {
		return append(b, 'h')
	}
	return append(b, 'l')
}

func appendCharsets(b []byte, c cursor) []byte {
	for i, g := range c.g {
		b = append(b, 0x1b, "()*+"[i], 'B')
		if g {
			b[len(b)-1] = '0'
		}
	}
	return append(b, [...]string{"\x0f", "\x0e", "\x1bn", "\x1bo"}[c.gl]...)
}

// Repaint returns a byte sequence that, written to a terminal of the same
// size in its default state, reproduces this one: the scrollback (when
// withScrollback is set) followed by the main screen, then the alternate
// screen if active, the scroll region, saved cursor, input modes the
// program enabled, title, pen and cursor. Feeding it to a New Terminal of
// the same size yields an equal screen.
func (t *Terminal) Repaint(withScrollback bool) []byte {
	b := make([]byte, 0, 256+t.rows*(t.cols+16))
	// Synchronized update, normalize the receiving terminal, hide the
	// cursor while painting.
	b = append(b, "\x1b[?2026h\x1b[?1049l\x1b[?5l\x1b[0m\x1b[r\x1b[?6l\x1b[?7h\x1b[4l\x1b(B\x1b)B\x1b*B\x1b+B\x0f\x1b[?25l\x1b[H\x1b[2J"...)
	sbOpen := false
	if withScrollback {
		lines := t.sb[t.sbStart:]
		for i, line := range lines {
			b = append(b, line...)
			if i < len(lines)-1 || !t.sbOpen {
				b = append(b, "\r\n"...)
			}
		}
		sbOpen = t.sbOpen && len(lines) > 0
	}
	// The main screen flows in as lines, so with scrollback above it ends
	// up exactly on screen. A full-width last row leaves a pending wrap,
	// never a scroll.
	b = t.appendScreen(b, t.main, t.mainWrap, sbOpen, false)
	if t.altActive {
		// Enter the alternate screen from the main screen's saved cursor so
		// leaving it later restores the right position.
		if t.saved1049.valid {
			b = appendCUP(b, t.saved1049.y, t.saved1049.x)
			b = append(b, "\x1b[?1049h"...)
		} else {
			b = append(b, "\x1b[?1047h"...)
		}
		b = append(b, "\x1b[H\x1b[2J"...)
		b = t.appendScreen(b, t.alt, t.altWrap, false, true)
	} else if t.alt != nil {
		// An alternate screen left with mode 47 keeps its contents.
		b = append(b, "\x1b[?47h\x1b[H\x1b[2J"...)
		b = t.appendScreen(b, t.alt, t.altWrap, false, true)
		b = append(b, "\x1b[?47l"...)
	}
	if t.tabs() != nil {
		b = append(b, "\x1b[3g"...)
		for x := range t.cols {
			if t.isTab(x) {
				b = appendCUP(b, 0, x)
				b = append(b, "\x1bH"...)
			}
		}
	}
	var savedModes []savedMode
	if t.x != nil {
		savedModes = t.x.savedModes
	}
	for _, m := range savedModes {
		b = appendMode(b, m.mode, m.on)
		b = append(b, "\x1b[?"...)
		b = strconv.AppendInt(b, int64(m.mode), 10)
		b = append(b, 's')
	}
	for _, m := range savedModes {
		on, _ := t.modeValue(m.mode)
		b = appendMode(b, m.mode, on && m.mode != 25)
	}
	if t.top != 0 || t.bot != t.rows-1 {
		b = append(b, "\x1b["...)
		b = strconv.AppendInt(b, int64(t.top+1), 10)
		b = append(b, ';')
		b = strconv.AppendInt(b, int64(t.bot+1), 10)
		b = append(b, 'r')
	}
	slot := 0
	if t.altActive {
		slot = 1
	}
	if s := t.saved[slot]; s.valid {
		// Origin mode homes the cursor, so it goes first and the position
		// is given relative to the region.
		if s.origin {
			b = append(b, "\x1b[?6h"...)
			b = appendCUP(b, s.y-t.top, s.x)
		} else {
			b = appendCUP(b, s.y, s.x)
		}
		b = appendSGR(b, s.pen)
		b = appendCharsets(b, s.cursor)
		b = append(b, "\x1b7"...)
		if s.origin {
			b = append(b, "\x1b[?6l"...)
		}
		b = append(b, "\x1b(B\x1b)B\x1b*B\x1b+B\x0f"...)
	}
	// Cursor. Origin mode homes the cursor when switched on, so it is
	// enabled first and followed by a region-relative CUP.
	x, y := t.cur.x, t.cur.y
	if t.cur.wrapPending {
		// Reprint the last cell to recreate the pending wrap.
		row := t.screen()[y]
		cx := t.cols - 1
		if row != nil && cx > 0 && row[cx].R == wideTail {
			cx--
		}
		b = appendCUP(b, y, cx)
		b = append(b, "\x1b[0m"...)
		if row == nil {
			b = append(b, ' ')
		} else {
			b = t.appendRow(b, row[cx:], false)
		}
		if t.cur.origin {
			b = append(b, "\x1b[?6h"...)
			// Origin mode homes the cursor; the pending wrap cannot be
			// recreated in that combination, so land on the last column.
			b = appendCUP(b, y-t.top, t.cols-1)
		}
	} else if t.cur.origin {
		b = append(b, "\x1b[?6h"...)
		b = appendCUP(b, y-t.top, x)
	} else {
		b = appendCUP(b, y, x)
	}
	b = appendSGR(b, t.cur.pen)
	b = appendCharsets(b, t.cur)
	if t.ss != 0 {
		b = append(b, 0x1b, 'L'+t.ss) // SS2 is ESC N, SS3 ESC O
	}
	if !t.autowrap {
		b = appendMode(b, 7, false)
	}
	if t.reverseVideo {
		b = appendMode(b, 5, true)
	}
	if t.revWrap {
		b = appendMode(b, 45, true)
	}
	if t.revWrapExt {
		b = appendMode(b, 1045, true)
	}
	if t.insert {
		b = append(b, "\x1b[4h"...)
	}
	if t.newline {
		b = append(b, "\x1b[20h"...)
	}
	if t.appCursor {
		b = appendMode(b, 1, true)
	}
	if t.appKeypad {
		b = append(b, "\x1b="...)
	}
	if t.mouse != 0 {
		b = appendMode(b, t.mouse, true)
	}
	if t.mouseEnc != 0 {
		b = appendMode(b, t.mouseEnc, true)
	}
	if t.focus {
		b = appendMode(b, 1004, true)
	}
	if t.bracketed {
		b = appendMode(b, 2004, true)
	}
	for _, f := range t.kitty {
		b = append(b, "\x1b[>"...)
		b = strconv.AppendInt(b, int64(f), 10)
		b = append(b, 'u')
	}
	if t.cursorStyle != 0 {
		b = append(b, "\x1b["...)
		b = strconv.AppendInt(b, int64(t.cursorStyle), 10)
		b = append(b, " q"...)
	}
	if t.title != "" {
		b = append(b, "\x1b]2;"...)
		b = append(b, t.title...)
		b = append(b, '\a')
	}
	if !t.cursorHidden {
		b = append(b, "\x1b[?25h"...)
	}
	return append(b, "\x1b[?2026l"...)
}

// appendScreen paints rows from the top-left corner, recreating soft
// wraps: a row that continues on the next is painted whole and the next
// row follows without a line break, so the receiving terminal wraps it.
// With open, the first row continues a line already painted. With
// addressed, rows are reached with cursor moves (blank rows skipped);
// otherwise they flow in separated by CR LF.
func (t *Terminal) appendScreen(b []byte, scr [][]Cell, wrap []bool, open, addressed bool) []byte {
	for y, row := range scr {
		cont := open && y == 0 || y > 0 && wrap != nil && wrap[y-1]
		next := y < len(scr)-1 && wrap != nil && wrap[y]
		if addressed && !cont {
			if !next && zeroRow(row) {
				continue
			}
			b = appendCUP(b, y, 0)
		}
		n := len(b)
		if next {
			b = t.appendWrapped(b, row, scr[y+1])
		} else {
			b = t.appendRow(b, row, true)
		}
		if cont && len(b) == n {
			// Something must be printed for the wrap to happen.
			c := Cell{R: ' '}
			if row != nil {
				c = styleOf(row[0])
				c.R = ' '
			}
			b = t.appendRow(b, []Cell{c}, false)
		}
		if cont && !next && contentEnd(row) < t.cols {
			// The wrap may have scrolled in a row painted in the current
			// background: clear the rest of it.
			b = append(b, "\x1b[K"...)
		}
		if !addressed && !next && y < len(scr)-1 {
			b = append(b, "\r\n"...)
		}
	}
	return b
}

// rowText returns a row's characters with trailing blanks removed.
func (t *Terminal) rowText(row []Cell) string {
	var sb strings.Builder
	for _, c := range row {
		switch {
		case c.R == wideTail:
		case c.R == 0:
			sb.WriteByte(' ')
		case c.R >= clusterBase:
			sb.WriteString(t.x.clusters[c.R-clusterBase])
		default:
			sb.WriteRune(c.R)
		}
	}
	return strings.TrimRight(sb.String(), " ")
}

// Lines returns the visible screen as text, one string per row, with
// trailing blanks removed.
func (t *Terminal) Lines() []string {
	scr := t.screen()
	out := make([]string, len(scr))
	for y, row := range scr {
		out[y] = t.rowText(row)
	}
	return out
}

// ScrollbackLines returns the scrollback as text, oldest first.
func (t *Terminal) ScrollbackLines() []string {
	out := make([]string, 0, t.ScrollbackLen())
	for _, line := range t.sb[t.sbStart:] {
		out = append(out, strings.TrimRight(stripSGR(line), " "))
	}
	return out
}

// stripSGR removes the escape sequences (SGR and EL) a scrollback line
// holds.
func stripSGR(line []byte) string {
	var sb strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == 0x1b {
			for i++; i < len(line) && (line[i] < 0x40 || line[i] > 0x7e || line[i] == '['); i++ {
			}
			continue
		}
		sb.WriteByte(line[i])
	}
	return sb.String()
}
