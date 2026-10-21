// Package vt is a compact VT/xterm screen model. It is fed a program's raw
// terminal output and keeps what a terminal would show: the main and
// alternate screens, cursor, colors and attributes, scroll region, tab
// stops, the input modes a program has switched on, and a bounded
// scrollback. It can serialize that state as a byte sequence that repaints
// it on a fresh terminal (Repaint).
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

// Cell is one screen position. R is 0 for a blank, wideTail for the right
// half of a double-width character, and clusterBase+i for a character with
// combining marks (the Terminal's clusters[i]).
type Cell struct {
	R  rune
	Fg Color
	Bg Color
	A  Attr
}

const (
	wideTail    rune = -1
	clusterBase rune = 0x40000000
)

// Defaults for Options.
const (
	DefaultScrollbackLines = 2000
	DefaultScrollbackBytes = 256 << 10
	maxOSC                 = 4096
	maxParams              = 32
	maxCluster             = 32 // bytes of a character with its combining marks
	maxLogicalLine         = 32 << 10
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
	g           [4]bool // G0-G3 designated as DEC special graphics
	gl          uint8   // which of G0-G3 is invoked into GL
}

type savedCursor struct {
	cursor
	valid bool
}

// Terminal is the screen model.
type Terminal struct {
	cols, rows int
	main, alt  [][]Cell // alt is nil unless the alternate screen is in use
	// Soft-wrap flags, nil until a row wraps: wrap[y] means row y
	// continues on row y+1.
	mainWrap, altWrap []bool
	altActive         bool
	cur               cursor
	saved             [2]savedCursor // DECSC slots: main, alt
	saved1049         savedCursor    // cursor saved by ?1049h
	top, bot          int            // scroll region, inclusive
	left, right       int            // left and right margins, inclusive
	lrmm              bool           // DECLRMM: left and right margins enabled
	ss                uint8          // pending single shift (2 or 3)
	x                 *extra         // state few programs use, nil until one does

	// modes
	autowrap     bool
	revWrap      bool // 45: BS and CUB continue on the previous wrapped line
	revWrapExt   bool // 1045: ... on any previous line, and top to bottom
	insert       bool
	newline      bool // LNM
	cursorHidden bool
	reverseVideo bool // DECSCNM
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

	// scrollback, oldest first; each entry is a serialized logical line
	// (rows joined where they soft-wrapped)
	sb               [][]byte
	sbStart, sbBytes int
	sbMaxLines       int
	sbMaxBytes       int
	sbOpen           bool // the last line continues on screen row 0
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

// extra holds the state few programs use, kept out of Terminal so an idle
// terminal stays small.
type extra struct {
	tabs       []uint64    // tab stops, nil for every 8 columns
	savedModes []savedMode // XTSAVE
	// characters with combining marks, referenced by cells
	clusters   []string
	clusterCap int
}

// ext returns t.x, allocating it.
func (t *Terminal) ext() *extra {
	if t.x == nil {
		t.x = &extra{}
	}
	return t.x
}

type savedMode struct {
	mode int
	on   bool
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
		right:      cols - 1,
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

// wraps returns the active screen's soft-wrap flags (nil if none is set).
func (t *Terminal) wraps() []bool {
	if t.altActive {
		return t.altWrap
	}
	return t.mainWrap
}

// setWrap sets whether row y continues on row y+1.
func (t *Terminal) setWrap(y int, on bool) {
	w := t.wraps()
	if w == nil {
		if !on {
			return
		}
		w = make([]bool, t.rows)
		if t.altActive {
			t.altWrap = w
		} else {
			t.mainWrap = w
		}
	}
	w[y] = on
}

// wrapped reports whether row y continues the row above it (for row 0 of
// the main screen, the last scrollback line).
func (t *Terminal) wrapped(y int) bool {
	if y == 0 {
		return !t.altActive && t.sbOpen
	}
	w := t.wraps()
	return w != nil && w[y-1]
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
		i += size
		if r < 0xa0 {
			// A C1 control (U+0080-U+009F): it may start a sequence, so
			// the rest goes through the state machine.
			t.c1(r)
			return i
		}
		t.printRune(r)
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
		r, size := utf8.DecodeRune(t.utf8buf[:t.utf8n])
		t.utf8n = 0
		if size == 1 {
			// Invalid (overlong, surrogate, out of range).
			t.printRune(utf8.RuneError)
			return
		}
		if r < 0xa0 {
			t.c1(r)
			return
		}
		t.printRune(r)
	}
}

// c1 runs a C1 control received as a UTF-8 code point: like xterm, it is
// the 7-bit ESC Fe equivalent.
func (t *Terminal) c1(r rune) {
	t.step(0x1b)
	t.step(byte(r - 0x40))
}

func (t *Terminal) printRune(r rune) {
	w := widthCond.RuneWidth(r)
	if w == 0 {
		t.combine(r)
		return
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
		t.cursorLeft(1)
	case '\t':
		t.cur.wrapPending = false
		t.cur.x = t.nextTab(t.cur.x)
	case '\n', '\v', '\f':
		t.cur.wrapPending = false
		t.index()
		if t.newline {
			t.carriageReturn()
		}
	case '\r':
		t.carriageReturn()
	case 0x0e:
		t.cur.gl = 1
	case 0x0f:
		t.cur.gl = 0
	}
}

func (t *Terminal) escDispatch(c byte) {
	switch t.inter {
	case 0:
	case '(', ')', '*', '+':
		t.cur.g[t.inter-'('] = c == '0'
		return
	case '#':
		if c == '8' {
			t.decaln()
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
		t.index()
		t.carriageReturn()
	case 'H':
		t.setTab(t.cur.x, true)
	case 'M':
		t.cur.wrapPending = false
		t.reverseIndex()
	case 'N':
		t.ss = 2
	case 'O':
		t.ss = 3
	case 'n':
		t.cur.gl = 2
	case 'o':
		t.cur.gl = 3
	case 'c':
		t.reset()
	case '=':
		t.appKeypad = true
	case '>':
		t.appKeypad = false
	}
}

// decaln fills the screen with E (the alignment test) and resets the
// margins.
func (t *Terminal) decaln() {
	scr := t.screen()
	for y := range scr {
		row := scr[y]
		if row == nil {
			row = make([]Cell, t.cols)
			scr[y] = row
		}
		for x := range row {
			row[x] = Cell{R: 'E'}
		}
	}
	clear(t.wraps())
	t.top, t.bot, t.left, t.right = 0, t.rows-1, 0, t.cols-1
	t.cur.x, t.cur.y, t.cur.wrapPending = 0, 0, false
}

func (t *Terminal) reset() {
	sb, sbStart, sbBytes := t.sb, t.sbStart, t.sbBytes
	*t = Terminal{
		cols: t.cols, rows: t.rows,
		main:       make([][]Cell, t.rows),
		bot:        t.rows - 1,
		right:      t.cols - 1,
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
	t.cur.wrapPending = t.cur.wrapPending && t.autowrap && (t.cur.x == t.cols-1 || t.cur.x == t.right)
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
		if t.inter != 0 {
			return
		}
		switch c {
		case 'h', 'l':
			for i := 0; i < t.nparams; i++ {
				if !t.sub[i] {
					t.decMode(t.params[i], c == 'h')
				}
			}
		case 's', 'r':
			for i := 0; i < t.nparams; i++ {
				if !t.sub[i] {
					t.xtSaveMode(t.params[i], c == 's')
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
		edge := t.cols - 1
		if t.cur.x <= t.right {
			edge = t.right
		}
		t.setX(min(t.cur.x+t.param(0, 1), edge))
	case 'D', 'j':
		t.cursorLeft(t.param(0, 1))
	case 'E':
		t.cursorDown(t.param(0, 1))
		t.carriageReturn()
	case 'F':
		t.cursorUp(t.param(0, 1))
		t.carriageReturn()
	case 'G', '`':
		_, y := t.relPos()
		t.moveTo(t.param(0, 1)-1, y)
	case 'H', 'f':
		t.moveTo(t.param(1, 1)-1, t.param(0, 1)-1)
	case 'd':
		x, _ := t.relPos()
		t.moveTo(x, t.param(0, 1)-1)
	case 'k':
		t.cursorUp(t.param(0, 1))
	case 'I':
		t.cur.wrapPending = false
		for range min(t.param(0, 1), t.cols) {
			t.cur.x = t.nextTab(t.cur.x)
		}
	case 'Z':
		t.cur.wrapPending = false
		lim := 0
		if t.cur.origin {
			lim = t.left
		}
		for range min(t.param(0, 1), t.cols) {
			t.cur.x = max(t.prevTab(t.cur.x), min(lim, t.cur.x))
		}
	case 'g':
		switch t.param(0, 0) {
		case 0:
			t.setTab(t.cur.x, false)
		case 3:
			t.ext().tabs = make([]uint64, (t.cols+63)/64)
		}
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
		t.setWrap(t.cur.y, false) // as ghostty: the row no longer wraps
	case 'S':
		t.scrollUp(t.top, t.bot, t.param(0, 1))
	case 'T':
		if t.nparams <= 1 {
			t.scrollDown(t.top, t.bot, t.param(0, 1))
		}
	case 'b':
		if t.lastChar != 0 {
			w := t.runeWidth(t.lastChar)
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
		if !t.lrmm {
			t.saveCursor()
			break
		}
		// DECSLRM
		left, right := t.param(0, 1)-1, min(t.param(1, t.cols), t.cols)-1
		if left < right {
			t.left, t.right = left, right
			t.moveTo(0, 0)
		}
	case 'u':
		t.restoreCursor()
	}
}

func (t *Terminal) softReset() {
	t.cursorHidden = false
	t.insert = false
	t.cur.origin = false
	t.autowrap = true
	t.revWrap, t.revWrapExt = false, false
	t.appCursor = false
	t.appKeypad = false
	t.top, t.bot, t.left, t.right = 0, t.rows-1, 0, t.cols-1
	t.cur.pen = Cell{}
	t.cur.g, t.cur.gl, t.ss = [4]bool{}, 0, 0
	t.cur.wrapPending = false
	t.saved[0], t.saved[1] = savedCursor{}, savedCursor{}
}

// modeValue reports the state of a DEC private mode XTSAVE can save, and
// whether it is one.
func (t *Terminal) modeValue(mode int) (on, ok bool) {
	switch mode {
	case 1:
		return t.appCursor, true
	case 5:
		return t.reverseVideo, true
	case 6:
		return t.cur.origin, true
	case 7:
		return t.autowrap, true
	case 45:
		return t.revWrap, true
	case 69:
		return t.lrmm, true
	case 1045:
		return t.revWrapExt, true
	case 25:
		return !t.cursorHidden, true
	case 9, 1000, 1002, 1003:
		return t.mouse == mode, true
	case 1005, 1006, 1015:
		return t.mouseEnc == mode, true
	case 1004:
		return t.focus, true
	case 2004:
		return t.bracketed, true
	}
	return false, false
}

// xtSaveMode saves (XTSAVE) or restores (XTRESTORE) a DEC private mode.
func (t *Terminal) xtSaveMode(mode int, save bool) {
	x := t.ext()
	for i, s := range x.savedModes {
		if s.mode != mode {
			continue
		}
		if save {
			x.savedModes[i].on, _ = t.modeValue(mode)
		} else {
			t.decMode(mode, s.on)
		}
		return
	}
	if on, ok := t.modeValue(mode); ok && save && len(x.savedModes) < 16 {
		x.savedModes = append(x.savedModes, savedMode{mode, on})
	}
}

// tabs returns the custom tab stops (nil: every 8 columns).
func (t *Terminal) tabs() []uint64 {
	if t.x == nil {
		return nil
	}
	return t.x.tabs
}

func (t *Terminal) decMode(mode int, on bool) {
	switch mode {
	case 1:
		t.appCursor = on
	case 5:
		t.reverseVideo = on
	case 6:
		t.cur.origin = on
		t.moveTo(0, 0)
	case 7:
		t.autowrap = on
		if !on {
			t.cur.wrapPending = false
		}
	case 45:
		t.revWrap = on
	case 69:
		t.lrmm = on
		if !on {
			t.left, t.right = 0, t.cols-1
			t.cur.wrapPending = t.cur.wrapPending && t.cur.x == t.cols-1
		}
	case 1045:
		t.revWrapExt = on
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
		t.setAlt(on, mode == 1047)
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
			t.setAlt(true, true)
			t.clearScreen()
		} else if t.altActive {
			saved := t.saved1049
			t.setAlt(false, true)
			if saved.valid {
				t.setCursor(saved.cursor)
			}
		}
	}
}

// setAlt switches screens. Leaving the alternate screen frees it, unless
// it was left with mode 47 (which keeps its contents for the next switch)
// and holds something.
func (t *Terminal) setAlt(on, clearOnExit bool) {
	if on == t.altActive {
		return
	}
	t.altActive = on
	if on {
		if t.alt == nil {
			t.alt = make([][]Cell, t.rows)
		}
	} else {
		t.saved1049.valid = false
		if clearOnExit || t.altBlank() {
			t.alt, t.altWrap = nil, nil
		}
	}
	t.cur.wrapPending = false
}

func (t *Terminal) altBlank() bool {
	for _, row := range t.alt {
		if !zeroRow(row) {
			return false
		}
	}
	return true
}

func (t *Terminal) clearScreen() {
	t.eraseRows(0, t.rows)
}

// eraseRows erases rows [from, to), reusing rows already allocated.
func (t *Terminal) eraseRows(from, to int) {
	scr := t.screen()
	w := t.wraps()
	for y := from; y < to; y++ {
		if row := scr[y]; row != nil {
			t.blank(row)
		} else {
			scr[y] = t.blankRow()
		}
		if w != nil {
			w[y] = false
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

// fillCells erases cells [from, to) of row y with the current background.
// Erasing to the end of the row ends a soft wrap there.
func (t *Terminal) fillCells(y, from, to int) {
	from, to = max(from, 0), min(to, t.cols)
	if from >= to {
		return
	}
	if to == t.cols {
		if w := t.wraps(); w != nil {
			w[y] = false
		}
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

// charset returns whether the character set for the next printed
// character is DEC special graphics, consuming a single shift.
func (t *Terminal) graphics() bool {
	if t.ss != 0 {
		g := t.cur.g[t.ss]
		t.ss = 0
		return g
	}
	return t.cur.g[t.cur.gl]
}

func (t *Terminal) print(r rune, w int) {
	if t.graphics() && r >= 0x5f && r <= 0x7e {
		if r == 0x5f {
			r = ' '
		} else {
			r = decGraphics[r-0x60]
		}
	}
	t.lastChar = r
	if t.cur.wrapPending && t.autowrap {
		t.wrapLine()
		t.cur.x = t.left
	}
	t.cur.wrapPending = false
	if w > t.cols {
		return
	}
	// Text runs up to the right margin, unless the cursor is already
	// past it.
	edge := t.cols - 1
	if t.cur.x <= t.right {
		edge = t.right
	}
	if t.cur.x+w > edge+1 {
		if !t.autowrap {
			t.cur.x = max(edge+1-w, 0)
		} else {
			t.fillCells(t.cur.y, t.cur.x, edge+1)
			t.wrapLine()
			t.cur.x = t.left
			if t.cur.x+w > t.right+1 {
				return // a wide character in a one-column region
			}
			edge = t.right
		}
	}
	row := t.row(t.cur.y)
	x := t.cur.x
	if t.insert && x+w <= edge {
		copy(row[x+w:edge+1], row[x:edge+1-w])
	}
	pen := t.cur.pen
	// Overwriting part of a wide character blanks its other half.
	if row[x].R == wideTail && x > 0 {
		row[x-1] = Cell{}
	}
	if end := x + w; end < t.cols && row[end].R == wideTail {
		row[end] = Cell{}
	}
	pen.R = r
	row[x] = pen
	if w == 2 {
		pen.R = wideTail
		row[x+1] = pen
	}
	if t.insert {
		t.fixWide(row) // the shift may have pushed half a wide character off
	}
	if x+w > edge {
		t.cur.x = edge
		t.cur.wrapPending = t.autowrap
	} else {
		t.cur.x = x + w
	}
}

// wrapLine moves the cursor to the next line for autowrap and marks the
// row it left as continuing there.
func (t *Terminal) wrapLine() {
	y := t.cur.y
	if !t.fullWidth() {
		t.index() // soft wraps are only kept for full rows
		return
	}
	if t.top == t.bot {
		// A one-row screen: the row scrolls away as it wraps.
		t.setWrap(y, true)
	}
	if !t.index() {
		return
	}
	if t.cur.y != y {
		t.setWrap(y, true)
	} else if y > t.top {
		t.setWrap(y-1, true) // the region scrolled
	}
}

// combine adds a zero-width character (a combining mark, joiner or
// variation selector) to the character before the cursor.
func (t *Terminal) combine(r rune) {
	x, y := t.cur.x, t.cur.y
	if !t.cur.wrapPending && !(x == t.cols-1 && !t.autowrap) {
		x--
	}
	row := t.screen()[y]
	if x < 0 || row == nil {
		return
	}
	if row[x].R == wideTail && x > 0 {
		x--
	}
	c := &row[x]
	if c.R <= 0 {
		if x == t.cols-1 && !t.autowrap && x > 0 && row[x-1].R > 0 {
			c = &row[x-1]
		} else {
			return
		}
	}
	s := t.cellString(*c)
	if len(s)+utf8.RuneLen(r) > maxCluster {
		return
	}
	c.R = t.intern(s + string(r))
	t.lastChar = c.R
}

// printASCII prints a run of printable ASCII. The common case (no
// charset mapping, insert mode or wide characters in the way) writes
// cells directly; anything else goes through print.
func (t *Terminal) printASCII(p []byte) {
	for len(p) > 0 {
		if t.cur.g[t.cur.gl] || t.ss != 0 || t.insert || t.cur.wrapPending || !t.autowrap {
			t.print(rune(p[0]), 1)
			p = p[1:]
			continue
		}
		row := t.row(t.cur.y)
		x := t.cur.x
		edge := t.cols - 1
		if x <= t.right {
			edge = t.right
		}
		n := min(len(p), edge+1-x)
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
		if x+n > edge {
			t.cur.x = edge
			t.cur.wrapPending = true
		} else {
			t.cur.x = x + n
		}
		p = p[n:]
	}
}

// index moves the cursor down a line, scrolling at the bottom margin. It
// reports whether the cursor moved or the region scrolled.
func (t *Terminal) index() bool {
	if t.cur.y == t.bot {
		if !t.inMargins(t.cur.x) {
			return false
		}
		t.scrollUp(t.top, t.bot, 1)
		return true
	}
	if t.cur.y < t.rows-1 {
		t.cur.y++
		return true
	}
	return false
}

func (t *Terminal) reverseIndex() {
	if t.cur.y == t.top {
		if t.inMargins(t.cur.x) {
			t.scrollDown(t.top, t.bot, 1)
		}
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
	if !t.fullWidth() {
		t.scrollRect(top, bot, n)
		return
	}
	scr := t.screen()
	if top == 0 && !t.altActive {
		w := t.mainWrap
		for i, row := range scr[:n] {
			t.pushScrollback(row, w != nil && w[i], rowAt(scr, i+1))
		}
		open := t.sbOpen
		t.rotate(top, bot, n)
		t.sbOpen = open
		return
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
	// Soft wraps move with their rows; a row whose next row changed no
	// longer continues onto it.
	if w := t.wraps(); w != nil {
		if up {
			copy(w[top:], w[top+n:bot+1])
			clear(w[max(bot-n, top) : bot+1])
		} else {
			copy(w[top+n:bot+1], w[top:bot+1-n])
			clear(w[top : top+n])
			w[bot] = false
		}
		if top > 0 {
			w[top-1] = false
		}
	}
	if top == 0 && !t.altActive {
		t.sbOpen = false
	}
}

func (t *Terminal) scrollDown(top, bot, n int) {
	n = min(n, bot-top+1)
	if n <= 0 {
		return
	}
	if !t.fullWidth() {
		t.scrollRect(top, bot, -n)
		return
	}
	t.rotate(top, bot, -n)
}

// scrollRect moves the cells between the left and right margins of rows
// top..bot up by n rows (down by -n), 0 < |n| <= bot-top+1, blanking the
// rows that come in. Nothing goes to the scrollback.
func (t *Terminal) scrollRect(top, bot, n int) {
	scr := t.screen()
	l, r := t.left, t.right+1
	move := func(dst, src int) {
		if scr[src] == nil && scr[dst] == nil {
			return
		}
		d := t.row(dst)
		if s := scr[src]; s != nil {
			copy(d[l:r], s[l:r])
		} else {
			clear(d[l:r])
		}
	}
	erase := func(y int) {
		if scr[y] == nil && t.cur.pen.Bg == 0 {
			return
		}
		row := t.row(y)
		for x := l; x < r; x++ {
			row[x] = Cell{Bg: t.cur.pen.Bg}
		}
	}
	if n > 0 {
		for y := top; y+n <= bot; y++ {
			move(y, y+n)
		}
		for y := bot - n + 1; y <= bot; y++ {
			erase(y)
		}
	} else {
		n = -n
		for y := bot; y-n >= top; y-- {
			move(y, y-n)
		}
		for y := top; y < top+n; y++ {
			erase(y)
		}
	}
	for y := max(top-1, 0); y <= bot; y++ {
		if scr[y] != nil {
			t.fixWide(scr[y])
		}
		t.setWrap(y, false)
	}
}

func (t *Terminal) insertLines(n int) {
	if t.cur.y < t.top || t.cur.y > t.bot || !t.inMargins(t.cur.x) {
		return
	}
	t.scrollDown(t.cur.y, t.bot, n)
	t.cur.x, t.cur.wrapPending = t.left, false
}

func (t *Terminal) deleteLines(n int) {
	if t.cur.y < t.top || t.cur.y > t.bot || !t.inMargins(t.cur.x) {
		return
	}
	if n = min(n, t.bot-t.cur.y+1); n > 0 {
		if t.fullWidth() {
			t.rotate(t.cur.y, t.bot, n)
		} else {
			t.scrollRect(t.cur.y, t.bot, n)
		}
	}
	t.cur.x, t.cur.wrapPending = t.left, false
}

func (t *Terminal) insertCells(n int) {
	t.cur.wrapPending = false
	if t.inMargins(t.cur.x) {
		t.shiftCells(min(n, t.right+1-t.cur.x))
	}
}

func (t *Terminal) deleteCells(n int) {
	t.cur.wrapPending = false
	if t.inMargins(t.cur.x) {
		t.shiftCells(-min(n, t.right+1-t.cur.x))
		t.setWrap(t.cur.y, false) // as ghostty: the row no longer wraps
	}
}

// inMargins reports whether column x is within the left and right margins.
func (t *Terminal) inMargins(x int) bool { return x >= t.left && x <= t.right }

// fullWidth reports whether the left and right margins span the screen.
func (t *Terminal) fullWidth() bool { return t.left == 0 && t.right == t.cols-1 }

// carriageReturn moves the cursor to the left margin, or to the first
// column from left of it.
func (t *Terminal) carriageReturn() {
	t.cur.wrapPending = false
	if t.cur.origin || t.cur.x >= t.left {
		t.cur.x = t.left
	} else {
		t.cur.x = 0
	}
}

// relPos returns the cursor position as CUP takes it (relative to the
// margins in origin mode), 0-based.
func (t *Terminal) relPos() (int, int) {
	if t.cur.origin {
		return t.cur.x - t.left, t.cur.y - t.top
	}
	return t.cur.x, t.cur.y
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
	// Starting on the right half of a wide character splits it: the
	// left half goes too.
	if x > 0 && row[x].R == wideTail {
		row[x-1], row[x] = blank, blank
	}
	end := t.right + 1
	if n > 0 {
		copy(row[x+n:end], row[x:end])
		for i := x; i < x+n; i++ {
			row[i] = blank
		}
	} else {
		copy(row[x:end], row[x-n:end])
		for i := end + n; i < end; i++ {
			row[i] = blank
		}
	}
	t.fixWide(row)
}

// fixWide blanks halves of wide characters that lost their other half.
func (t *Terminal) fixWide(row []Cell) {
	for x := range row {
		c := &row[x]
		switch {
		case c.R == wideTail:
			if x == 0 || row[x-1].R <= 0 || t.runeWidth(row[x-1].R) != 2 {
				c.R = 0
			}
		case c.R > 0x7f && t.runeWidth(c.R) == 2:
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
	minX, maxX, minY, maxY := 0, t.cols-1, 0, t.rows-1
	if t.cur.origin {
		x += t.left
		y += t.top
		minX, maxX, minY, maxY = t.left, t.right, t.top, t.bot
	}
	t.cur.x = min(max(x, minX), maxX)
	t.cur.y = min(max(y, minY), maxY)
}

// cursorLeft moves the cursor left n columns (BS, CUB). With reverse
// wraparound it goes on from the end of the previous row when this one
// continues it (45), or from any previous row and from the top margin to
// the bottom one (1045), as xterm does.
func (t *Terminal) cursorLeft(n int) {
	left := 0
	if t.cur.x >= t.left {
		left = t.left // stop at the left margin unless already left of it
	}
	if !t.autowrap || !t.revWrap && !t.revWrapExt {
		t.setX(max(t.cur.x-n, left))
		return
	}
	ext := t.revWrapExt
	if t.cur.wrapPending {
		// The pending wrap counts as one step.
		t.cur.wrapPending = false
		if n--; n == 0 {
			return
		}
	}
	if t.cur.x == left && !ext && t.cur.y <= t.top {
		t.cur.x, t.cur.y = left, t.top
		return
	}
	for {
		step := min(t.cur.x-left, n)
		t.cur.x -= step
		if n -= step; n == 0 {
			return
		}
		switch {
		case t.cur.y == t.top:
			if !ext {
				return
			}
			t.cur.x, t.cur.y = t.right, t.bot
		case t.cur.y == 0, !ext && !t.wrapped(t.cur.y):
			return
		default:
			t.cur.x, t.cur.y = t.right, t.cur.y-1
		}
		n--
	}
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

// --- tab stops ---

func (t *Terminal) isTab(x int) bool {
	tabs := t.tabs()
	if tabs == nil {
		return x%8 == 0
	}
	return tabs[x/64]&(1<<(x%64)) != 0
}

// setTab sets or clears the tab stop at column x.
func (t *Terminal) setTab(x int, on bool) {
	if t.tabs() == nil {
		if t.isTab(x) == on {
			return
		}
		t.ext().tabs = defaultTabs(t.cols)
	}
	tabs := t.x.tabs
	if on {
		tabs[x/64] |= 1 << (x % 64)
	} else {
		tabs[x/64] &^= 1 << (x % 64)
	}
}

func defaultTabs(cols int) []uint64 {
	tabs := make([]uint64, (cols+63)/64)
	for x := 0; x < cols; x += 8 {
		tabs[x/64] |= 1 << (x % 64)
	}
	return tabs
}

// nextTab returns the next tab stop after x, or the last column.
func (t *Terminal) nextTab(x int) int {
	limit := t.cols - 1
	if x <= t.right {
		limit = t.right // tabs stop at the right margin
	}
	if x >= limit {
		return x
	}
	for x++; x < limit; x++ {
		if t.isTab(x) {
			return x
		}
	}
	return limit
}

// prevTab returns the tab stop before x, or the first column.
func (t *Terminal) prevTab(x int) int {
	for x--; x > 0; x-- {
		if t.isTab(x) {
			return x
		}
	}
	return 0
}

// resizeTabs keeps custom tab stops across a width change; new columns
// get the default stops.
func (t *Terminal) resizeTabs(oldCols int) {
	old := t.tabs()
	if old == nil {
		return
	}
	tabs := defaultTabs(t.cols)
	for x := 0; x < min(oldCols, t.cols); x++ {
		tabs[x/64] = tabs[x/64]&^(1<<(x%64)) | old[x/64]&(1<<(x%64))
	}
	t.x.tabs = tabs
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

// --- characters with combining marks ---

// cellString returns the characters a cell shows.
func (t *Terminal) cellString(c Cell) string {
	if c.R >= clusterBase {
		return t.x.clusters[c.R-clusterBase]
	}
	return string(c.R)
}

// runeWidth returns the width of a cell's character.
func (t *Terminal) runeWidth(r rune) int {
	if r >= clusterBase {
		r, _ = utf8.DecodeRuneInString(t.x.clusters[r-clusterBase])
	}
	return widthCond.RuneWidth(r)
}

// intern returns the cell rune for a character with combining marks.
// Recent entries are reused; when the table is full, entries no cell
// refers to any more are dropped.
func (t *Terminal) intern(s string) rune {
	x := t.ext()
	for i := len(x.clusters) - 1; i >= max(len(x.clusters)-16, 0); i-- {
		if x.clusters[i] == s {
			return clusterBase + rune(i)
		}
	}
	if len(x.clusters) >= x.clusterCap {
		t.compactClusters()
	}
	x.clusters = append(x.clusters, s)
	return clusterBase + rune(len(x.clusters)-1)
}

// compactClusters drops the clusters no cell refers to, renumbering the
// rest, and sets the next compaction point.
func (t *Terminal) compactClusters() {
	x := t.x
	if len(x.clusters) > 0 {
		remap := make([]rune, len(x.clusters))
		var kept []string
		ref := func(r *rune) {
			if *r < clusterBase {
				return
			}
			i := *r - clusterBase
			if remap[i] == 0 {
				kept = append(kept, x.clusters[i])
				remap[i] = clusterBase + rune(len(kept)-1)
			}
			*r = remap[i]
		}
		for _, scr := range [][][]Cell{t.main, t.alt} {
			for _, row := range scr {
				for i := range row {
					ref(&row[i].R)
				}
			}
		}
		ref(&t.lastChar)
		x.clusters = kept
	}
	x.clusterCap = max(64, 2*len(x.clusters))
}

// --- scrollback ---

// pushScrollback serializes row onto the scrollback, evicting the oldest
// lines past the line and byte budgets. A row that continues on the next
// one (wrapped, with next the row below) is kept whole, and the next row
// is appended to its line.
func (t *Terminal) pushScrollback(row []Cell, wrapped bool, next []Cell) {
	if t.sbMaxLines <= 0 {
		return
	}
	if wrapped {
		// Kept whole, a blank a wide character left at the end included:
		// the row below may still change, so leaving the blank out could
		// make the line rewrap differently on replay.
		t.scratch = t.appendWrapped(t.scratch[:0], row, nil)
	} else {
		t.scratch = t.appendRow(t.scratch[:0], row, true)
	}
	join := t.sbOpen && len(t.sb) > t.sbStart && len(t.sb[len(t.sb)-1])+len(t.scratch) <= maxLogicalLine
	if join && !wrapped && contentEnd(row) < t.cols {
		// Replayed, this row starts by wrapping, so something must be
		// printed, and the wrap may scroll in a row painted in the current
		// background: clear the rest of it.
		if len(t.scratch) == 0 {
			t.scratch = append(t.scratch, ' ')
		}
		t.scratch = append(t.scratch, "\x1b[K"...)
	}
	if n := len(t.sb); join {
		line := append(t.sb[n-1], t.scratch...)
		if !wrapped && cap(line) > len(line)+len(line)/4 {
			line = append([]byte(nil), line...)
		}
		t.sbBytes += len(line) - len(t.sb[n-1])
		t.sb[n-1] = line
	} else {
		line := append([]byte(nil), t.scratch...)
		t.sb = append(t.sb, line)
		t.sbBytes += len(line) + 24
	}
	t.sbOpen = wrapped
	if len(t.scratch) > 4096 {
		t.scratch = nil // do not keep a large buffer on an idle terminal
	}
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

// ScrollbackLen returns the number of retained scrollback lines. A line
// that wrapped on screen is one line.
func (t *Terminal) ScrollbackLen() int { return len(t.sb) - t.sbStart }

// ClearScrollback drops all scrollback.
func (t *Terminal) ClearScrollback() {
	t.sb, t.sbStart, t.sbBytes, t.sbOpen = nil, 0, 0, false
}

func rowBlank(row []Cell) bool {
	for _, c := range row {
		if c != (Cell{}) && !(c.R == ' ' && c.Fg == 0 && c.Bg == 0 && c.A == 0) {
			return false
		}
	}
	return true
}

// rowAt returns scr[i], or nil past the end.
func rowAt(scr [][]Cell, i int) []Cell {
	if i < len(scr) {
		return scr[i]
	}
	return nil
}

// wrapEnd returns how much of a row that continues on next is part of
// the line: all of it, but for a blank left at its end because the wide
// character starting next did not fit there.
func (t *Terminal) wrapEnd(row, next []Cell) int {
	if len(next) < 2 || next[1].R != wideTail {
		return t.cols
	}
	// Replayed, the wide character wraps and erases that blank in its own
	// background, so leave it out only if that recreates it.
	blank := Cell{Bg: next[0].Bg}
	if row == nil && blank == (Cell{}) || row != nil && row[t.cols-1] == blank {
		return t.cols - 1
	}
	return t.cols
}

// contentEnd returns the length of row without its trailing default
// blanks.
func contentEnd(row []Cell) int {
	end := len(row)
	for end > 0 && row[end-1] == (Cell{}) {
		end--
	}
	return end
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
