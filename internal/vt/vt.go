// Package vt is a compact VT/xterm screen model. It is fed a program's raw
// terminal output and keeps what a terminal would show: the main and
// alternate screens, cursor, colors and attributes, scroll region, the
// input modes a program has switched on, and a bounded scrollback. It can
// serialize that state as a byte sequence that repaints it on a fresh
// terminal (Repaint).
//
// It is written for ptyd's scale constraints rather than as a general
// emulator: a cell is 16 bytes, rows are allocated only once something is
// drawn on them, the alternate screen exists only while it is active, and
// scrollback is stored as serialized lines under a byte budget. It never
// replies to queries and keeps no timers.
//
// A Terminal is not safe for concurrent use.
package vt

import (
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Color is a cell color: 0 is the terminal default, colorIndexed|n is
// palette entry n, and colorRGB|0xRRGGBB is a 24-bit color.
type Color uint32

const (
	colorIndexed Color = 1 << 24
	colorRGB     Color = 2 << 24
)

// Attr holds SGR attributes.
type Attr uint16

const (
	attrBold Attr = 1 << iota
	attrFaint
	attrItalic
	attrBlink
	attrInverse
	attrHidden
	attrStrike
	attrOverline
	// The underline style (0 none, 1 single, 2 double, 3 curly, 4 dotted,
	// 5 dashed) lives in the top bits.
	ulShift      = 12
	ulMask  Attr = 7 << ulShift
)

// Cell is one screen position. R is 0 for a blank and wideTail for the
// right half of a double-width character.
type Cell struct {
	R  rune
	Fg Color
	Bg Color
	A  Attr
}

const wideTail rune = -1

// Defaults for Options.
const (
	DefaultScrollbackLines = 2000
	DefaultScrollbackBytes = 256 << 10
	maxOSC                 = 4096
	maxParams              = 32
)

var widthCond = func() *runewidth.Condition {
	c := runewidth.NewCondition()
	c.EastAsianWidth = false
	return c
}()

type cursor struct {
	x, y        int
	pen         Cell
	wrapPending bool
	origin      bool
	g           [2]bool // G0/G1 designated as DEC special graphics
	gl          int     // 0: G0 invoked into GL, 1: G1
}

type savedCursor struct {
	cursor
	valid bool
}

// Terminal is the screen model.
type Terminal struct {
	cols, rows int
	main, alt  [][]Cell // alt is nil unless the alternate screen is active
	altActive  bool
	cur        cursor
	saved      [2]savedCursor // DECSC slots: main, alt
	saved1049  savedCursor    // cursor saved by ?1049h
	top, bot   int            // scroll region, inclusive

	// modes
	autowrap     bool
	insert       bool
	newline      bool // LNM
	cursorHidden bool
	appCursor    bool
	appKeypad    bool
	bracketed    bool
	focus        bool
	mouse        int // 0, 9, 1000, 1002 or 1003
	mouseEnc     int // 0, 1005, 1006 or 1015
	cursorStyle  int // DECSCUSR parameter, 0 = default
	kitty        []uint16
	title        string
	lastChar     rune

	// scrollback, oldest first; each entry is a serialized line
	sb               [][]byte
	sbStart, sbBytes int
	sbMaxLines       int
	sbMaxBytes       int
	scratch          []byte

	// parser
	state    uint8
	utf8buf  [utf8.UTFMax]byte
	utf8n    int
	params   [maxParams]int
	sub      [maxParams]bool // params[i] followed a ':'
	nparams  int
	paramSet bool
	marker   byte // private marker: ? > < =
	inter    byte // last intermediate byte
	osc      []byte
	oscOver  bool
}

const (
	stGround uint8 = iota
	stEsc
	stEscInter
	stCSI
	stCSIIgnore
	stOSC
	stOSCEsc
	stStr // DCS, SOS, PM, APC: ignored until ST
	stStrEsc
)

// Options configure a Terminal.
type Options struct {
	ScrollbackLines int // default DefaultScrollbackLines; negative disables
	ScrollbackBytes int // default DefaultScrollbackBytes
}

// New returns a blank terminal of the given size.
func New(cols, rows int, opts Options) *Terminal {
	cols, rows = max(cols, 1), max(rows, 1)
	if opts.ScrollbackLines == 0 {
		opts.ScrollbackLines = DefaultScrollbackLines
	}
	if opts.ScrollbackBytes <= 0 {
		opts.ScrollbackBytes = DefaultScrollbackBytes
	}
	return &Terminal{
		cols: cols, rows: rows,
		main:       make([][]Cell, rows),
		bot:        rows - 1,
		autowrap:   true,
		sbMaxLines: max(opts.ScrollbackLines, 0),
		sbMaxBytes: opts.ScrollbackBytes,
	}
}

// Size returns the screen size.
func (t *Terminal) Size() (cols, rows int) { return t.cols, t.rows }

// AltScreen reports whether the alternate screen is active.
func (t *Terminal) AltScreen() bool { return t.altActive }

// Title returns the window title last set with OSC 0 or 2.
func (t *Terminal) Title() string { return t.title }

// Cursor returns the cursor position (0-based) and whether it is visible.
func (t *Terminal) Cursor() (x, y int, visible bool) {
	return t.cur.x, t.cur.y, !t.cursorHidden
}

func (t *Terminal) screen() [][]Cell {
	if t.altActive {
		return t.alt
	}
	return t.main
}

// Write feeds program output. Sequences may be split across calls. It
// always consumes all of p.
func (t *Terminal) Write(p []byte) (int, error) {
	for i := 0; i < len(p); i++ {
		c := p[i]
		if t.state == stGround {
			if t.utf8n > 0 {
				t.utf8Continue(c)
				continue
			}
			if c >= 0x20 && c < 0x7f {
				// Fast path: a run of printable ASCII.
				j := i + 1
				for j < len(p) && p[j] >= 0x20 && p[j] < 0x7f {
					j++
				}
				t.printASCII(p[i:j])
				i = j - 1
				continue
			}
			if c >= 0x80 {
				i += t.printUTF8(p[i:]) - 1
				continue
			}
		}
		t.step(c)
	}
	return len(p), nil
}

// printUTF8 prints the run of non-ASCII characters at the start of p
// (p[0] >= 0x80) and returns how many bytes it consumed. Complete, valid
// sequences are decoded at once; anything else (invalid, or cut at the
// end of p) goes through the byte-at-a-time decoder, which ends the run.
func (t *Terminal) printUTF8(p []byte) int {
	i := 0
	for i < len(p) && p[i] >= 0x80 {
		r, size := utf8.DecodeRune(p[i:])
		if size == 1 {
			t.utf8Start(p[i])
			return i + 1
		}
		t.printRune(r)
		i += size
	}
	return i
}

func (t *Terminal) utf8Start(c byte) {
	t.utf8buf[0] = c
	t.utf8n = 1
	if c < 0xc2 || c > 0xf4 { // stray continuation or invalid lead
		t.utf8n = 0
		t.printRune(utf8.RuneError)
	}
}

func (t *Terminal) utf8Continue(c byte) {
	if c&0xc0 != 0x80 {
		// Truncated sequence: emit a replacement, then process c afresh.
		t.utf8n = 0
		t.printRune(utf8.RuneError)
		t.Write([]byte{c})
		return
	}
	t.utf8buf[t.utf8n] = c
	t.utf8n++
	if utf8.FullRune(t.utf8buf[:t.utf8n]) {
		r, _ := utf8.DecodeRune(t.utf8buf[:t.utf8n])
		t.utf8n = 0
		t.printRune(r)
	}
}

func (t *Terminal) printRune(r rune) {
	w := widthCond.RuneWidth(r)
	if w == 0 {
		return // combining marks and other zero-width runes are dropped
	}
	t.print(r, w)
}

// step runs one byte through the escape-sequence state machine.
func (t *Terminal) step(c byte) {
	// C0 controls act in every state but strings; ESC, CAN and SUB abort.
	switch t.state {
	case stOSC, stOSCEsc, stStr, stStrEsc:
	default:
		if c < 0x20 && c != 0x1b {
			if c == 0x18 || c == 0x1a {
				t.state = stGround
				return
			}
			t.execute(c)
			return
		}
	}
	switch t.state {
	case stGround:
		if c == 0x1b {
			t.state = stEsc
		}
		// 0x7f (DEL) is ignored.
	case stEsc:
		t.inter = 0
		switch {
		case c == 0x1b:
		case c == '[':
			t.nparams, t.paramSet, t.marker, t.inter = 0, false, 0, 0
			t.params[0], t.sub[0] = 0, false
			t.state = stCSI
		case c == ']':
			t.osc, t.oscOver = t.osc[:0], false
			t.state = stOSC
		case c == 'P' || c == 'X' || c == '^' || c == '_':
			t.state = stStr
		case c >= 0x20 && c <= 0x2f:
			t.inter = c
			t.state = stEscInter
		case c >= 0x30 && c <= 0x7e:
			t.state = stGround
			t.escDispatch(c)
		default:
			t.state = stGround
		}
	case stEscInter:
		switch {
		case c == 0x1b:
			t.state = stEsc
		case c >= 0x20 && c <= 0x2f:
			t.inter = c
		case c >= 0x30 && c <= 0x7e:
			t.state = stGround
			t.escDispatch(c)
		default:
			t.state = stGround
		}
	case stCSI:
		switch {
		case c >= '0' && c <= '9':
			if t.nparams < maxParams {
				v := t.params[t.nparams]*10 + int(c-'0')
				t.params[t.nparams] = min(v, 1<<16)
			}
			t.paramSet = true
		case c == ';' || c == ':':
			if t.nparams < maxParams-1 {
				t.nparams++
				t.params[t.nparams] = 0
				t.sub[t.nparams] = c == ':'
			} else {
				t.nparams = maxParams
			}
			t.paramSet = true
		case c >= '<' && c <= '?':
			if t.paramSet || t.marker != 0 {
				t.state = stCSIIgnore
			} else {
				t.marker = c
			}
		case c >= 0x20 && c <= 0x2f:
			t.inter = c
		case c >= 0x40 && c <= 0x7e:
			t.state = stGround
			if t.paramSet {
				t.nparams = min(t.nparams+1, maxParams)
			}
			t.csiDispatch(c)
		case c == 0x1b:
			t.state = stEsc
		default:
			t.state = stCSIIgnore
		}
	case stCSIIgnore:
		if c >= 0x40 && c <= 0x7e {
			t.state = stGround
		} else if c == 0x1b {
			t.state = stEsc
		}
	case stOSC:
		switch c {
		case 0x07:
			t.state = stGround
			t.oscDispatch()
		case 0x1b:
			t.state = stOSCEsc
		default:
			if len(t.osc) < maxOSC {
				t.osc = append(t.osc, c)
			} else {
				t.oscOver = true
			}
		}
	case stOSCEsc:
		if c == '\\' {
			t.state = stGround
			t.oscDispatch()
			return
		}
		// ESC that is not ST ends the string and starts a new sequence.
		t.state = stEsc
		t.step(c)
	case stStr:
		if c == 0x1b {
			t.state = stStrEsc
		}
	case stStrEsc:
		if c == '\\' {
			t.state = stGround
			return
		}
		t.state = stEsc
		t.step(c)
	}
}

func (t *Terminal) execute(c byte) {
	switch c {
	case '\b':
		t.cur.wrapPending = false
		if t.cur.x > 0 {
			t.cur.x--
		}
	case '\t':
		t.cur.wrapPending = false
		t.cur.x = min((t.cur.x/8+1)*8, t.cols-1)
	case '\n', '\v', '\f':
		t.cur.wrapPending = false
		t.index()
		if t.newline {
			t.cur.x = 0
		}
	case '\r':
		t.cur.wrapPending = false
		t.cur.x = 0
	case 0x0e:
		t.cur.gl = 1
	case 0x0f:
		t.cur.gl = 0
	}
}

func (t *Terminal) escDispatch(c byte) {
	switch t.inter {
	case 0:
	case '(', ')':
		g := 0
		if t.inter == ')' {
			g = 1
		}
		t.cur.g[g] = c == '0'
		return
	case '#':
		if c == '8' { // DECALN: fill the screen with E
			scr := t.screen()
			for y := range scr {
				row := make([]Cell, t.cols)
				for x := range row {
					row[x].R = 'E'
				}
				scr[y] = row
			}
			t.top, t.bot = 0, t.rows-1
			t.cur.x, t.cur.y, t.cur.wrapPending = 0, 0, false
		}
		return
	default:
		return
	}
	switch c {
	case '7':
		t.saveCursor()
	case '8':
		t.restoreCursor()
	case 'D':
		t.cur.wrapPending = false
		t.index()
	case 'E':
		t.cur.wrapPending = false
		t.cur.x = 0
		t.index()
	case 'M':
		t.cur.wrapPending = false
		t.reverseIndex()
	case 'c':
		t.reset()
	case '=':
		t.appKeypad = true
	case '>':
		t.appKeypad = false
	}
}

func (t *Terminal) reset() {
	sb, sbStart, sbBytes := t.sb, t.sbStart, t.sbBytes
	*t = Terminal{
		cols: t.cols, rows: t.rows,
		main:       make([][]Cell, t.rows),
		bot:        t.rows - 1,
		autowrap:   true,
		sb:         sb,
		sbStart:    sbStart,
		sbBytes:    sbBytes,
		sbMaxLines: t.sbMaxLines,
		sbMaxBytes: t.sbMaxBytes,
		osc:        t.osc[:0],
	}
}

func (t *Terminal) saveCursor() {
	i := 0
	if t.altActive {
		i = 1
	}
	t.saved[i] = savedCursor{cursor: t.cur, valid: true}
}

func (t *Terminal) restoreCursor() {
	i := 0
	if t.altActive {
		i = 1
	}
	s := t.saved[i]
	if !s.valid {
		t.cur = cursor{}
		return
	}
	t.setCursor(s.cursor)
}

// setCursor installs a saved cursor, keeping it valid for the current
// size, region and modes.
func (t *Terminal) setCursor(c cursor) {
	t.cur = c
	t.cur.x = min(t.cur.x, t.cols-1)
	t.cur.y = min(t.cur.y, t.rows-1)
	t.cur.wrapPending = t.cur.wrapPending && t.autowrap
	if t.cur.origin {
		t.cur.y = min(max(t.cur.y, t.top), t.bot)
	}
}

// param returns parameter i (ignoring sub-parameters), or def if it is
// absent or zero.
func (t *Terminal) param(i, def int) int {
	if i >= t.nparams || t.params[i] == 0 {
		return def
	}
	return t.params[i]
}

func (t *Terminal) csiDispatch(c byte) {
	switch t.marker {
	case '?':
		switch c {
		case 'h', 'l':
			for i := 0; i < t.nparams; i++ {
				if !t.sub[i] {
					t.decMode(t.params[i], c == 'h')
				}
			}
		case 'J':
			t.eraseDisplay(t.param(0, 0))
		case 'K':
			t.eraseLine(t.param(0, 0))
		}
		return
	case '>':
		if c == 'u' && t.inter == 0 { // kitty keyboard: push flags
			if len(t.kitty) >= 16 {
				t.kitty = t.kitty[1:]
			}
			t.kitty = append(t.kitty, uint16(t.param(0, 0)))
		}
		return
	case '<':
		if c == 'u' && t.inter == 0 { // kitty keyboard: pop n
			n := min(t.param(0, 1), len(t.kitty))
			t.kitty = t.kitty[:len(t.kitty)-n]
			if len(t.kitty) == 0 {
				t.kitty = nil
			}
		}
		return
	case '=':
		if c == 'u' && t.inter == 0 { // kitty keyboard: set flags
			f := uint16(t.param(0, 0))
			if len(t.kitty) == 0 {
				t.kitty = append(t.kitty, 0)
			}
			top := &t.kitty[len(t.kitty)-1]
			switch t.param(1, 1) {
			case 1:
				*top = f
			case 2:
				*top |= f
			case 3:
				*top &^= f
			}
		}
		return
	}
	switch t.inter {
	case 0:
	case ' ':
		if c == 'q' {
			t.cursorStyle = t.param(0, 0)
		}
		return
	case '!':
		if c == 'p' {
			t.softReset()
		}
		return
	default:
		return
	}
	switch c {
	case '@':
		t.insertCells(t.param(0, 1))
	case 'A':
		t.cursorUp(t.param(0, 1))
	case 'B', 'e':
		t.cursorDown(t.param(0, 1))
	case 'C', 'a':
		t.setX(t.cur.x + t.param(0, 1))
	case 'D':
		t.setX(t.cur.x - t.param(0, 1))
	case 'E':
		t.cursorDown(t.param(0, 1))
		t.cur.x = 0
	case 'F':
		t.cursorUp(t.param(0, 1))
		t.cur.x = 0
	case 'G', '`':
		t.setX(t.param(0, 1) - 1)
	case 'H', 'f':
		t.moveTo(t.param(1, 1)-1, t.param(0, 1)-1)
	case 'd':
		t.moveTo(t.cur.x, t.param(0, 1)-1)
	case 'I':
		for range t.param(0, 1) {
			t.execute('\t')
		}
	case 'Z':
		t.cur.wrapPending = false
		t.cur.x = max((t.cur.x-1)/8*8, 0)
	case 'J':
		t.eraseDisplay(t.param(0, 0))
	case 'K':
		t.eraseLine(t.param(0, 0))
	case 'L':
		t.insertLines(t.param(0, 1))
	case 'M':
		t.deleteLines(t.param(0, 1))
	case 'P':
		t.deleteCells(t.param(0, 1))
	case 'X':
		n := min(t.param(0, 1), t.cols-t.cur.x)
		t.cur.wrapPending = false
		t.fillCells(t.cur.y, t.cur.x, t.cur.x+n)
	case 'S':
		t.scrollUp(t.top, t.bot, t.param(0, 1))
	case 'T':
		if t.nparams <= 1 {
			t.scrollDown(t.top, t.bot, t.param(0, 1))
		}
	case 'b':
		if t.lastChar != 0 {
			w := widthCond.RuneWidth(t.lastChar)
			for range min(t.param(0, 1), t.cols*t.rows) {
				t.print(t.lastChar, w)
			}
		}
	case 'm':
		t.sgr()
	case 'h', 'l':
		for i := 0; i < t.nparams; i++ {
			switch t.params[i] {
			case 4:
				t.insert = c == 'h'
			case 20:
				t.newline = c == 'h'
			}
		}
	case 'r':
		top, bot := t.param(0, 1)-1, t.param(1, t.rows)-1
		bot = min(bot, t.rows-1)
		if top < bot {
			t.top, t.bot = top, bot
			t.moveTo(0, 0)
		}
	case 's':
		t.saveCursor()
	case 'u':
		t.restoreCursor()
	}
}

func (t *Terminal) softReset() {
	t.cursorHidden = false
	t.insert = false
	t.cur.origin = false
	t.autowrap = true
	t.appCursor = false
	t.appKeypad = false
	t.top, t.bot = 0, t.rows-1
	t.cur.pen = Cell{}
	t.cur.g, t.cur.gl = [2]bool{}, 0
	t.cur.wrapPending = false
	t.saved[0], t.saved[1] = savedCursor{}, savedCursor{}
}

func (t *Terminal) decMode(mode int, on bool) {
	switch mode {
	case 1:
		t.appCursor = on
	case 6:
		t.cur.origin = on
		t.moveTo(0, 0)
	case 7:
		t.autowrap = on
		if !on {
			t.cur.wrapPending = false
		}
	case 25:
		t.cursorHidden = !on
	case 9, 1000, 1002, 1003:
		if on {
			t.mouse = mode
		} else if t.mouse == mode {
			t.mouse = 0
		}
	case 1005, 1006, 1015:
		if on {
			t.mouseEnc = mode
		} else if t.mouseEnc == mode {
			t.mouseEnc = 0
		}
	case 1004:
		t.focus = on
	case 2004:
		t.bracketed = on
	case 47, 1047:
		t.setAlt(on, mode == 1047 && !on)
	case 1048:
		if on {
			t.saveCursor()
		} else {
			t.restoreCursor()
		}
	case 1049:
		if on {
			if !t.altActive {
				t.saved1049 = savedCursor{cursor: t.cur, valid: true}
			}
			t.setAlt(true, false)
			t.clearScreen()
		} else if t.altActive {
			t.setAlt(false, true)
			if t.saved1049.valid {
				t.setCursor(t.saved1049.cursor)
			}
		}
	}
}

// setAlt switches screens. Leaving the alternate screen frees it.
func (t *Terminal) setAlt(on, clearOnExit bool) {
	if on == t.altActive {
		return
	}
	t.altActive = on
	if on {
		t.alt = make([][]Cell, t.rows)
	} else {
		t.alt = nil
	}
	_ = clearOnExit // the alternate screen is dropped on exit either way
	t.cur.wrapPending = false
}

func (t *Terminal) clearScreen() {
	t.eraseRows(0, t.rows)
}

// eraseRows erases rows [from, to), reusing rows already allocated.
func (t *Terminal) eraseRows(from, to int) {
	scr := t.screen()
	for y := from; y < to; y++ {
		if row := scr[y]; row != nil {
			t.blank(row)
		} else {
			scr[y] = t.blankRow()
		}
	}
}

// blank erases row in place with the current background.
func (t *Terminal) blank(row []Cell) {
	if t.cur.pen.Bg == 0 {
		clear(row)
		return
	}
	blank := Cell{Bg: t.cur.pen.Bg}
	for x := range row {
		row[x] = blank
	}
}

// blankRow returns an erased row: nil when the erase color is the default.
func (t *Terminal) blankRow() []Cell {
	if t.cur.pen.Bg == 0 {
		return nil
	}
	row := make([]Cell, t.cols)
	for x := range row {
		row[x].Bg = t.cur.pen.Bg
	}
	return row
}

// row returns screen row y, allocating it if it is still blank.
func (t *Terminal) row(y int) []Cell {
	scr := t.screen()
	if scr[y] == nil {
		scr[y] = make([]Cell, t.cols)
	}
	return scr[y]
}

func (t *Terminal) fillCells(y, from, to int) {
	from, to = max(from, 0), min(to, t.cols)
	if from >= to {
		return
	}
	scr := t.screen()
	if scr[y] == nil && t.cur.pen.Bg == 0 {
		return
	}
	row := t.row(y)
	blank := Cell{Bg: t.cur.pen.Bg}
	// Erasing half of a wide character erases all of it.
	if from > 0 && row[from].R == wideTail {
		row[from-1] = blank
	}
	if to < t.cols && row[to].R == wideTail {
		row[to] = blank
	}
	for x := from; x < to; x++ {
		row[x] = blank
	}
}

var decGraphics = [...]rune{
	'◆', '▒', '␉', '␌', '␍', '␊', '°', '±', '␤', '␋', '┘', '┐', '┌', '└', '┼', '⎺',
	'⎻', '─', '⎼', '⎽', '├', '┤', '┴', '┬', '│', '≤', '≥', 'π', '≠', '£', '·',
}

func (t *Terminal) print(r rune, w int) {
	if t.cur.g[t.cur.gl] && r >= 0x5f && r <= 0x7e {
		if r == 0x5f {
			r = ' '
		} else {
			r = decGraphics[r-0x60]
		}
	}
	t.lastChar = r
	if t.cur.wrapPending && t.autowrap {
		t.cur.x = 0
		t.index()
	}
	t.cur.wrapPending = false
	if w > t.cols {
		return
	}
	if t.cur.x+w > t.cols {
		if !t.autowrap {
			t.cur.x = t.cols - w
		} else {
			t.fillCells(t.cur.y, t.cur.x, t.cols)
			t.cur.x = 0
			t.index()
		}
	}
	row := t.row(t.cur.y)
	x := t.cur.x
	if t.insert {
		copy(row[x+w:], row[x:])
	}
	pen := t.cur.pen
	// Overwriting part of a wide character blanks its other half.
	if row[x].R == wideTail && x > 0 {
		row[x-1] = Cell{Fg: row[x-1].Fg, Bg: row[x-1].Bg, A: row[x-1].A}
	}
	if end := x + w; end < t.cols && row[end].R == wideTail {
		row[end] = Cell{Fg: row[end].Fg, Bg: row[end].Bg, A: row[end].A}
	}
	pen.R = r
	row[x] = pen
	if w == 2 {
		pen.R = wideTail
		row[x+1] = pen
	}
	if t.insert {
		fixWide(row) // the shift may have pushed half a wide character off
	}
	if x+w >= t.cols {
		t.cur.x = t.cols - 1
		t.cur.wrapPending = t.autowrap
	} else {
		t.cur.x = x + w
	}
}

// printASCII prints a run of printable ASCII. The common case (no
// charset mapping, insert mode or wide characters in the way) writes
// cells directly; anything else goes through print.
func (t *Terminal) printASCII(p []byte) {
	for len(p) > 0 {
		if t.cur.g[t.cur.gl] || t.insert || t.cur.wrapPending || !t.autowrap {
			t.print(rune(p[0]), 1)
			p = p[1:]
			continue
		}
		row := t.row(t.cur.y)
		x := t.cur.x
		n := min(len(p), t.cols-x)
		if row[x].R == wideTail || (x+n < t.cols && row[x+n].R == wideTail) {
			t.print(rune(p[0]), 1)
			p = p[1:]
			continue
		}
		pen := t.cur.pen
		for i, b := range p[:n] {
			pen.R = rune(b)
			row[x+i] = pen
		}
		t.lastChar = rune(p[n-1])
		if x+n >= t.cols {
			t.cur.x = t.cols - 1
			t.cur.wrapPending = true
		} else {
			t.cur.x = x + n
		}
		p = p[n:]
	}
}

func (t *Terminal) index() {
	if t.cur.y == t.bot {
		t.scrollUp(t.top, t.bot, 1)
	} else if t.cur.y < t.rows-1 {
		t.cur.y++
	}
}

func (t *Terminal) reverseIndex() {
	if t.cur.y == t.top {
		t.scrollDown(t.top, t.bot, 1)
	} else if t.cur.y > 0 {
		t.cur.y--
	}
}

// scrollUp moves rows top..bot up by n. Rows leaving the top of the main
// screen go to scrollback when the region starts at the first row.
func (t *Terminal) scrollUp(top, bot, n int) {
	n = min(n, bot-top+1)
	if n <= 0 {
		return
	}
	scr := t.screen()
	if top == 0 && !t.altActive {
		for _, row := range scr[:n] {
			t.pushScrollback(row)
		}
	}
	t.rotate(top, bot, n)
}

// rotate moves rows top..bot up by n rows (down by -n), 0 < |n| <=
// bot-top+1. The rows that leave at one end come back blank at the other,
// so programs that scroll, insert, delete and erase lines all the time
// do not allocate a row each time.
func (t *Terminal) rotate(top, bot, n int) {
	scr := t.screen()
	up := n > 0
	n = max(n, -n)
	// The rows leaving, kept to be reused (a small batch suffices for
	// the common one-line case without allocating).
	var spare [8][]Cell
	recycled := spare[:0]
	if n <= len(spare) {
		if up {
			recycled = append(recycled, scr[top:top+n]...)
		} else {
			recycled = append(recycled, scr[bot-n+1:bot+1]...)
		}
	}
	in := scr[top : top+n] // where blank rows come in
	if up {
		copy(scr[top:], scr[top+n:bot+1])
		in = scr[bot-n+1 : bot+1]
	} else {
		copy(scr[top+n:bot+1], scr[top:bot+1-n])
	}
	for i := range in {
		if i < len(recycled) && recycled[i] != nil {
			t.blank(recycled[i])
			in[i] = recycled[i]
		} else {
			in[i] = t.blankRow()
		}
	}
}

func (t *Terminal) scrollDown(top, bot, n int) {
	n = min(n, bot-top+1)
	if n <= 0 {
		return
	}
	t.rotate(top, bot, -n)
}

func (t *Terminal) insertLines(n int) {
	if t.cur.y < t.top || t.cur.y > t.bot {
		return
	}
	t.scrollDown(t.cur.y, t.bot, n)
	t.cur.x, t.cur.wrapPending = 0, false
}

func (t *Terminal) deleteLines(n int) {
	if t.cur.y < t.top || t.cur.y > t.bot {
		return
	}
	if n = min(n, t.bot-t.cur.y+1); n > 0 {
		t.rotate(t.cur.y, t.bot, n)
	}
	t.cur.x, t.cur.wrapPending = 0, false
}

func (t *Terminal) insertCells(n int) {
	t.shiftCells(min(n, t.cols-t.cur.x))
}

func (t *Terminal) deleteCells(n int) {
	t.shiftCells(-min(n, t.cols-t.cur.x))
}

// shiftCells inserts (n > 0) or deletes (n < 0) blank cells at the cursor,
// shifting the rest of the line.
func (t *Terminal) shiftCells(n int) {
	t.cur.wrapPending = false
	if n == 0 || t.screen()[t.cur.y] == nil && t.cur.pen.Bg == 0 {
		return
	}
	row, x := t.row(t.cur.y), t.cur.x
	blank := Cell{Bg: t.cur.pen.Bg}
	if n > 0 {
		copy(row[x+n:], row[x:])
		for i := x; i < x+n; i++ {
			row[i] = blank
		}
	} else {
		copy(row[x:], row[x-n:])
		for i := t.cols + n; i < t.cols; i++ {
			row[i] = blank
		}
	}
	fixWide(row)
}

// fixWide blanks halves of wide characters that lost their other half.
func fixWide(row []Cell) {
	for x := range row {
		c := &row[x]
		switch {
		case c.R == wideTail:
			if x == 0 || row[x-1].R <= 0 || widthCond.RuneWidth(row[x-1].R) != 2 {
				c.R = 0
			}
		case c.R > 0x7f && widthCond.RuneWidth(c.R) == 2:
			if x+1 == len(row) || row[x+1].R != wideTail {
				c.R = 0
			}
		}
	}
}

func (t *Terminal) eraseDisplay(mode int) {
	t.cur.wrapPending = false
	switch mode {
	case 0:
		t.fillCells(t.cur.y, t.cur.x, t.cols)
		t.eraseRows(t.cur.y+1, t.rows)
	case 1:
		t.eraseRows(0, t.cur.y)
		t.fillCells(t.cur.y, 0, t.cur.x+1)
	case 2:
		t.clearScreen()
	case 3:
		t.ClearScrollback()
	}
}

func (t *Terminal) eraseLine(mode int) {
	t.cur.wrapPending = false
	switch mode {
	case 0:
		t.fillCells(t.cur.y, t.cur.x, t.cols)
	case 1:
		t.fillCells(t.cur.y, 0, t.cur.x+1)
	case 2:
		t.eraseRows(t.cur.y, t.cur.y+1)
	}
}

func (t *Terminal) setX(x int) {
	t.cur.wrapPending = false
	t.cur.x = min(max(x, 0), t.cols-1)
}

// moveTo positions the cursor, honoring origin mode.
func (t *Terminal) moveTo(x, y int) {
	t.cur.wrapPending = false
	minY, maxY := 0, t.rows-1
	if t.cur.origin {
		y += t.top
		minY, maxY = t.top, t.bot
	}
	t.cur.x = min(max(x, 0), t.cols-1)
	t.cur.y = min(max(y, minY), maxY)
}

func (t *Terminal) cursorUp(n int) {
	t.cur.wrapPending = false
	lim := 0
	if t.cur.y >= t.top {
		lim = t.top
	}
	t.cur.y = max(t.cur.y-n, lim)
}

func (t *Terminal) cursorDown(n int) {
	t.cur.wrapPending = false
	lim := t.rows - 1
	if t.cur.y <= t.bot {
		lim = t.bot
	}
	t.cur.y = min(t.cur.y+n, lim)
}

func (t *Terminal) sgr() {
	if t.nparams == 0 {
		t.cur.pen = Cell{}
		return
	}
	p := &t.cur.pen
	for i := 0; i < t.nparams; i++ {
		v := t.params[i]
		// Skip the sub-parameters of a parameter we do not interpret.
		next := i + 1
		for next < t.nparams && t.sub[next] {
			next++
		}
		switch {
		case v == 0:
			*p = Cell{}
		case v == 1:
			p.A |= attrBold
		case v == 2:
			p.A |= attrFaint
		case v == 3:
			p.A |= attrItalic
		case v == 4:
			style := 1
			if next > i+1 {
				style = t.params[i+1]
			}
			p.A = p.A&^ulMask | Attr(min(style, 5))<<ulShift
		case v == 5 || v == 6:
			p.A |= attrBlink
		case v == 7:
			p.A |= attrInverse
		case v == 8:
			p.A |= attrHidden
		case v == 9:
			p.A |= attrStrike
		case v == 21:
			p.A = p.A&^ulMask | 2<<ulShift
		case v == 22:
			p.A &^= attrBold | attrFaint
		case v == 23:
			p.A &^= attrItalic
		case v == 24:
			p.A &^= ulMask
		case v == 25:
			p.A &^= attrBlink
		case v == 27:
			p.A &^= attrInverse
		case v == 28:
			p.A &^= attrHidden
		case v == 29:
			p.A &^= attrStrike
		case v >= 30 && v <= 37:
			p.Fg = colorIndexed | Color(v-30)
		case v == 38, v == 48, v == 58:
			c, end := t.extColor(i, next)
			switch v {
			case 38:
				p.Fg = c
			case 48:
				p.Bg = c
			}
			next = end
		case v == 39:
			p.Fg = 0
		case v >= 40 && v <= 47:
			p.Bg = colorIndexed | Color(v-40)
		case v == 49:
			p.Bg = 0
		case v == 53:
			p.A |= attrOverline
		case v == 55:
			p.A &^= attrOverline
		case v >= 90 && v <= 97:
			p.Fg = colorIndexed | Color(v-90+8)
		case v >= 100 && v <= 107:
			p.Bg = colorIndexed | Color(v-100+8)
		}
		i = next - 1
	}
}

// extColor parses the color after an extended-color parameter at i, in
// either the colon form (38:5:n, 38:2::r:g:b, 38:2:r:g:b) or the legacy
// semicolon form (38;5;n, 38;2;r;g;b). It returns the color and the index
// of the next parameter to interpret.
func (t *Terminal) extColor(i, next int) (Color, int) {
	if next > i+1 { // colon form: everything is in sub-parameters
		args := t.params[i+1 : next]
		switch {
		case args[0] == 5 && len(args) >= 2:
			return colorIndexed | Color(args[1]&0xff), next
		case args[0] == 2 && len(args) >= 5:
			return rgb(args[2], args[3], args[4]), next
		case args[0] == 2 && len(args) == 4:
			return rgb(args[1], args[2], args[3]), next
		}
		return 0, next
	}
	if i+1 >= t.nparams {
		return 0, i + 1
	}
	switch t.params[i+1] {
	case 5:
		if i+2 < t.nparams {
			return colorIndexed | Color(t.params[i+2]&0xff), i + 3
		}
	case 2:
		if i+4 < t.nparams {
			return rgb(t.params[i+2], t.params[i+3], t.params[i+4]), i + 5
		}
	}
	return 0, t.nparams
}

func rgb(r, g, b int) Color {
	return colorRGB | Color(r&0xff)<<16 | Color(g&0xff)<<8 | Color(b&0xff)
}

func (t *Terminal) oscDispatch() {
	if t.oscOver {
		return
	}
	s := t.osc
	switch {
	case len(s) >= 2 && (s[0] == '0' || s[0] == '2') && s[1] == ';':
		t.title = string(s[2:])
	}
}

// pushScrollback serializes row onto the scrollback, evicting the oldest
// lines past the line and byte budgets.
func (t *Terminal) pushScrollback(row []Cell) {
	if t.sbMaxLines <= 0 {
		return
	}
	t.scratch = appendRow(t.scratch[:0], row, true)
	line := append([]byte(nil), t.scratch...)
	if len(t.scratch) > 4096 {
		t.scratch = nil // do not keep a large buffer on an idle terminal
	}
	t.sb = append(t.sb, line)
	t.sbBytes += len(line) + 24
	for n := len(t.sb) - t.sbStart; n > t.sbMaxLines || (t.sbBytes > t.sbMaxBytes && n > 1); n-- {
		t.sbBytes -= len(t.sb[t.sbStart]) + 24
		t.sb[t.sbStart] = nil
		t.sbStart++
	}
	if t.sbStart > 64 && t.sbStart*2 > len(t.sb) {
		n := copy(t.sb, t.sb[t.sbStart:])
		clear(t.sb[n:])
		t.sb = t.sb[:n]
		t.sbStart = 0
	}
}

// ScrollbackLen returns the number of retained scrollback lines.
func (t *Terminal) ScrollbackLen() int { return len(t.sb) - t.sbStart }

// ClearScrollback drops all scrollback.
func (t *Terminal) ClearScrollback() {
	t.sb, t.sbStart, t.sbBytes = nil, 0, 0
}

// Resize changes the screen size. Content is not reflowed: rows are
// truncated or padded; when the height shrinks, blank rows below the
// cursor go first, then rows from the top (into scrollback on the main
// screen).
func (t *Terminal) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	if cols == t.cols && rows == t.rows {
		return
	}
	oldCols := t.cols
	t.cols = cols
	resizeRows := func(scr [][]Cell, cy *int, toScrollback bool) [][]Cell {
		for y, row := range scr {
			if row == nil {
				continue
			}
			if cols < oldCols {
				row = row[:cols:cols]
				fixWide(row)
			} else if cols > oldCols {
				row = append(row, make([]Cell, cols-oldCols)...)
			}
			scr[y] = row
		}
		for len(scr) > rows && len(scr)-1 > *cy && zeroRow(scr[len(scr)-1]) {
			scr = scr[:len(scr)-1]
		}
		if extra := len(scr) - rows; extra > 0 {
			if toScrollback {
				for _, row := range scr[:extra] {
					t.pushScrollback(row)
				}
			}
			scr = append(scr[:0], scr[extra:]...)
			*cy -= extra
		}
		for len(scr) < rows {
			scr = append(scr, nil)
		}
		return scr[:rows:rows]
	}
	mainY, altY := t.cur.y, t.cur.y
	if t.altActive {
		mainY = t.saved1049.y
	}
	t.main = resizeRows(t.main, &mainY, true)
	if t.altActive {
		t.alt = resizeRows(t.alt, &altY, false)
		t.saved1049.y = max(mainY, 0)
		t.cur.y = altY
	} else {
		t.cur.y = mainY
	}
	t.rows = rows
	t.top, t.bot = 0, rows-1
	t.cur.y = min(max(t.cur.y, 0), rows-1)
	t.cur.x = min(t.cur.x, cols-1)
	t.cur.wrapPending = false
	for i := range t.saved {
		t.saved[i].x = min(t.saved[i].x, cols-1)
		t.saved[i].y = min(t.saved[i].y, rows-1)
	}
	t.saved1049.x = min(t.saved1049.x, cols-1)
	t.saved1049.y = min(t.saved1049.y, rows-1)
}

// ArchiveScreen resets the terminal for a new program while keeping what
// the screen showed in view above it, as if the old content had been
// printed and the new program started on the next line: the main screen
// down to its last non-blank row, then, if a full-screen program was
// showing the alternate screen, that too. Whatever does not fit above
// the new program's first row scrolls into scrollback.
func (t *Terminal) ArchiveScreen() {
	var old [][]Cell
	keep := func(scr [][]Cell) {
		last := -1
		for y, row := range scr {
			if !rowBlank(row) {
				last = y
			}
		}
		old = append(old, scr[:last+1]...)
	}
	keep(t.main)
	if t.altActive {
		keep(t.alt)
	}
	t.reset()
	if over := len(old) - (t.rows - 1); over > 0 {
		for _, row := range old[:over] {
			t.pushScrollback(row)
		}
		old = old[over:]
	}
	copy(t.main, old)
	t.cur.y = len(old)
}

func rowBlank(row []Cell) bool {
	for _, c := range row {
		if c != (Cell{}) && !(c.R == ' ' && c.Fg == 0 && c.Bg == 0 && c.A == 0) {
			return false
		}
	}
	return true
}

// zeroRow reports whether row is blank in the default style (nil rows
// are).
func zeroRow(row []Cell) bool {
	for _, c := range row {
		if c != (Cell{}) {
			return false
		}
	}
	return true
}
