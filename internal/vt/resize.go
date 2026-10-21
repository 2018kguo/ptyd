package vt

import (
	"bytes"
	"unicode/utf8"
)

// point is a cursor position carried through a resize.
type point struct {
	x, y    int
	pending bool
}

// Resize changes the screen size. On the main screen, lines that soft
// wrapped are rewrapped to the new width (reflow); when the content needs
// more rows than the screen has, blank rows below the cursor go first, then
// rows from the top go to the scrollback, and when it needs fewer, lines
// come back from the scrollback. The alternate screen is not reflowed: its
// rows are truncated or padded. The cursor and saved cursors follow the
// characters they were on.
func (t *Terminal) Resize(cols, rows int) {
	cols, rows = max(cols, 1), max(rows, 1)
	if cols == t.cols && rows == t.rows {
		return
	}
	oldCols := t.cols
	if cols != oldCols {
		t.dropLineErases()
	}

	// The positions on each screen.
	var mainPts, altPts []*cursor
	if t.saved[0].valid {
		mainPts = append(mainPts, &t.saved[0].cursor)
	}
	if t.saved[1].valid {
		altPts = append(altPts, &t.saved[1].cursor)
	}
	if t.altActive {
		if t.saved1049.valid {
			mainPts = append(mainPts, &t.saved1049.cursor)
		}
		altPts = append(altPts, &t.cur)
	} else {
		mainPts = append(mainPts, &t.cur)
	}

	// Main screen: reflow, then fit to the height.
	pts := make([]point, len(mainPts))
	for i, c := range mainPts {
		pts[i] = point{c.x, c.y, c.wrapPending}
	}
	scr, wrap := t.main, t.mainWrap
	if cols != oldCols && !t.autowrap {
		// Nothing rewraps: the scrollback's last line ends where it is.
		t.sbOpen = false
	}
	if cols != oldCols && t.autowrap && t.sbOpen {
		// The line the screen starts with began in the scrollback: take it
		// back so it is rewrapped whole.
		back, backWrap := t.popLine()
		if wrap == nil {
			wrap = make([]bool, len(scr))
		}
		scr = append(back, scr...)
		wrap = append(backWrap, wrap...)
		for i := range pts {
			pts[i].y += len(back)
		}
	}
	t.cols, t.rows = cols, rows
	switch {
	case cols != oldCols && t.autowrap:
		scr, wrap = t.rewrap(scr, wrap, oldCols, cols, pts)
	case cols != oldCols:
		// Without autowrap nothing wraps, so rows are cut instead.
		for y, row := range scr {
			scr[y] = t.resizeRow(row, oldCols)
		}
		wrap = nil
	}
	if wrap == nil {
		wrap = make([]bool, len(scr))
	}
	cy := 0
	if len(pts) > 0 {
		cy = pts[len(pts)-1].y // the cursor, or the main screen's saved one
	}
	// Blank rows below the cursor go first.
	for len(scr) > rows && len(scr)-1 > cy && zeroRow(scr[len(scr)-1]) {
		scr, wrap = scr[:len(scr)-1], wrap[:len(wrap)-1]
	}
	if extra := len(scr) - rows; extra > 0 {
		for i, row := range scr[:extra] {
			t.pushScrollback(row, wrap[i], rowAt(scr, i+1))
		}
		scr, wrap = scr[extra:], wrap[extra:]
		for i := range pts {
			pts[i].y -= extra
		}
	} else if extra < 0 {
		back, backWrap := t.popScrollback(-extra)
		scr = append(back, scr...)
		wrap = append(backWrap, wrap...)
		for i := range pts {
			pts[i].y += len(back)
		}
	}
	t.main, t.mainWrap = fitRows(scr, wrap, rows)
	for i, c := range mainPts {
		t.place(c, pts[i])
	}

	// The alternate screen (also when kept by mode 47): truncated or
	// padded.
	if t.alt != nil {
		for y, row := range t.alt {
			t.alt[y] = t.resizeRow(row, oldCols)
		}
		altY := t.cur.y
		if !t.altActive {
			altY = 0
		}
		scr, wrap := t.alt, t.altWrap
		if wrap == nil {
			wrap = make([]bool, len(scr))
		}
		for len(scr) > rows && len(scr)-1 > altY && zeroRow(scr[len(scr)-1]) {
			scr, wrap = scr[:len(scr)-1], wrap[:len(wrap)-1]
		}
		extra := max(len(scr)-rows, 0)
		scr, wrap = scr[extra:], wrap[extra:]
		t.alt, t.altWrap = fitRows(scr, wrap, rows)
		for _, c := range altPts {
			p := point{c.x, c.y - extra, c.wrapPending}
			if c.wrapPending && cols > oldCols {
				p.x, p.pending = c.x+1, false
			}
			t.place(c, p)
		}
	}
	if !t.altActive && t.alt != nil && t.altBlank() {
		t.alt, t.altWrap = nil, nil // a kept alternate screen cut to nothing
	}
	if t.altWrap != nil && !t.anyWrap(t.altWrap) {
		t.altWrap = nil
	}
	if t.mainWrap != nil && !t.anyWrap(t.mainWrap) {
		t.mainWrap = nil
	}
	t.top, t.bot, t.left, t.right = 0, rows-1, 0, cols-1
	// Saved cursors of a screen not in use (the alternate one's DECSC
	// slot) are just kept in bounds.
	for _, c := range []*cursor{&t.saved[0].cursor, &t.saved[1].cursor, &t.saved1049.cursor} {
		t.place(c, point{c.x, c.y, c.wrapPending})
	}
	t.resizeTabs(oldCols)
}

func (t *Terminal) anyWrap(w []bool) bool {
	for _, b := range w {
		if b {
			return true
		}
	}
	return false
}

// place moves c to p, clamped to the new size.
func (t *Terminal) place(c *cursor, p point) {
	c.x = min(max(p.x, 0), t.cols-1)
	c.y = min(max(p.y, 0), t.rows-1)
	c.wrapPending = p.pending && t.autowrap && c.x == t.cols-1
}

// fitRows pads or cuts scr and wrap to exactly rows entries. The wrap
// flags are dropped when none is set.
func fitRows(scr [][]Cell, wrap []bool, rows int) ([][]Cell, []bool) {
	out := make([][]Cell, rows)
	copy(out, scr)
	var w []bool
	for i, b := range wrap[:min(len(wrap), rows)] {
		if b && i < rows-1 {
			if w == nil {
				w = make([]bool, rows)
			}
			w[i] = true
		}
	}
	return out, w
}

// resizeRow truncates or pads row to t.cols (nil stays nil).
func (t *Terminal) resizeRow(row []Cell, oldCols int) []Cell {
	switch {
	case row == nil:
		return nil
	case t.cols < oldCols:
		row = row[:t.cols:t.cols]
		t.fixWide(row)
	case t.cols > oldCols:
		row = append(row, make([]Cell, t.cols-oldCols)...)
	}
	return row
}

// rewrap reflows scr (oldCols wide, wrap flags as in t.mainWrap) to cols
// columns, returning the new rows and their wrap flags (one per row).
// Points on the screen are moved with the characters they were on.
func (t *Terminal) rewrap(scr [][]Cell, wrap []bool, oldCols, cols int, pts []point) ([][]Cell, []bool) {
	var out [][]Cell
	var outWrap []bool
	var cells []Cell
	var starts []int // offset in cells of each row of the line
	orig := append([]point(nil), pts...)
	for y := 0; y < len(scr); {
		// The logical line is rows y..e.
		e := y
		for e < len(scr)-1 && wrap != nil && wrap[e] {
			e++
		}
		cells, starts = cells[:0], starts[:0]
		for r := y; r <= e; r++ {
			starts = append(starts, len(cells))
			row := scr[r]
			if row == nil {
				row = make([]Cell, oldCols)
			}
			n := len(row)
			// A blank left at the end of a row because the wide character
			// after it did not fit is not part of the line.
			if r < e && row[n-1] == (Cell{}) && scr[r+1] != nil && len(scr[r+1]) > 1 && scr[r+1][1].R == wideTail {
				n--
			}
			cells = append(cells, row[:n]...)
		}
		length := len(cells)
		for length > 0 && cells[length-1] == (Cell{}) {
			length--
		}
		// Offsets of the points on this line.
		type pref struct {
			i   int
			off int
		}
		var mine []pref
		for i, p := range orig {
			if p.y >= y && p.y <= e {
				off := starts[p.y-y] + p.x
				if p.pending {
					off++
				}
				mine = append(mine, pref{i, off})
			}
		}
		// Lay the cells out in rows of cols.
		row := make([]Cell, cols)
		col := 0
		placed := make(map[int][2]int, len(mine))
		for i := 0; i < length; i++ {
			c := cells[i]
			if c.R == wideTail {
				continue
			}
			w := 1
			if i+1 < len(cells) && cells[i+1].R == wideTail {
				w = 2
			}
			if w > cols {
				c, w = Cell{Fg: c.Fg, Bg: c.Bg, A: c.A}, 1
			}
			if col+w > cols {
				out = append(out, row)
				outWrap = append(outWrap, true)
				row = make([]Cell, cols)
				col = 0
			}
			for _, m := range mine {
				if m.off == i || (w == 2 && m.off == i+1) {
					placed[m.i] = [2]int{len(out), col + m.off - i}
				}
			}
			row[col] = c
			if w == 2 {
				row[col+1] = cells[i+1]
			}
			col += w
		}
		if length == 0 && scr[y] == nil && y == e {
			row = nil
		}
		out = append(out, row)
		outWrap = append(outWrap, false)
		last := len(out) - 1
		for _, m := range mine {
			p := &pts[m.i]
			if at, ok := placed[m.i]; ok && m.off < length {
				p.x, p.y, p.pending = at[1], at[0], false
				continue
			}
			// After the end of the line.
			x := col + m.off - length
			p.y, p.pending = last, false
			switch {
			case x < cols:
				p.x = x
			case x == cols && m.off == length:
				p.x, p.pending = cols-1, true
			default:
				p.x = cols - 1
			}
		}
		y = e + 1
	}
	return out, outWrap
}

// dropLineErases removes the erases (EL) after wrapped rows in the
// scrollback. They clear what a wrap may scroll in when a line is replayed
// at the width it was written at; at any other width one could land on a
// pending wrap and erase the last character.
func (t *Terminal) dropLineErases() {
	for i, line := range t.sb[t.sbStart:] {
		if bytes.Contains(line, eraseLine) {
			n := len(line)
			line = bytes.ReplaceAll(line, eraseLine, nil)
			t.sb[t.sbStart+i] = line
			t.sbBytes -= n - len(line)
		}
	}
}

var eraseLine = []byte("\x1b[K")

// popScrollback takes up to n rows from the end of the scrollback,
// rewrapped to the current width, for the top of the screen. A line that
// does not fit whole leaves its beginning in the scrollback.
func (t *Terminal) popScrollback(n int) ([][]Cell, []bool) {
	var rows [][]Cell
	var wraps []bool
	for len(rows) < n && t.ScrollbackLen() > 0 {
		lr, lw := t.popLine()
		if need := n - len(rows); len(lr) > need {
			head := len(lr) - need
			for i, r := range lr[:head] {
				t.pushScrollback(r, true, lr[i+1])
			}
			lr, lw = lr[head:], lw[head:]
		}
		rows = append(lr, rows...)
		wraps = append(lw, wraps...)
	}
	return rows, wraps
}

// popLine removes the last scrollback line and returns it as rows of the
// current width, with their wrap flags (the last row's says whether the
// line went on onto the screen).
func (t *Terminal) popLine() ([][]Cell, []bool) {
	last := len(t.sb) - 1
	line := t.sb[last]
	open := t.sbOpen
	t.sb[last] = nil
	t.sb = t.sb[:last]
	t.sbBytes -= len(line) + 24
	t.sbOpen = false
	if t.sbStart > len(t.sb) {
		t.sbStart = len(t.sb)
	}
	rows, wraps := t.parseLine(line)
	wraps[len(wraps)-1] = open
	return rows, wraps
}

// parseLine turns a serialized scrollback line back into rows of the
// current width, with their wrap flags.
func (t *Terminal) parseLine(line []byte) ([][]Cell, []bool) {
	n := utf8.RuneCount(line)
	tmp := New(t.cols, 2*n/t.cols+2, Options{ScrollbackLines: -1})
	tmp.Write(line)
	k := tmp.cur.y + 1
	rows := make([][]Cell, k)
	wraps := make([]bool, k)
	for y := range k {
		row := tmp.main[y]
		if row == nil {
			continue
		}
		for x := range row {
			if r := row[x].R; r >= clusterBase {
				row[x].R = t.intern(tmp.x.clusters[r-clusterBase])
			}
		}
		rows[y] = row
		if tmp.mainWrap != nil {
			wraps[y] = tmp.mainWrap[y]
		}
	}
	return rows, wraps
}

// ArchiveScreen resets the terminal for a new program while keeping what
// the screen showed in view above it, as if the old content had been
// printed and the new program started on the next line: the main screen
// down to its last non-blank row, then, if a full-screen program was
// showing the alternate screen, that too. Whatever does not fit above
// the new program's first row scrolls into scrollback.
func (t *Terminal) ArchiveScreen() {
	var old [][]Cell
	var oldWrap []bool
	keep := func(scr [][]Cell, wrap []bool) {
		last := -1
		for y, row := range scr {
			if !rowBlank(row) {
				last = y
			}
		}
		old = append(old, scr[:last+1]...)
		for y := range last + 1 {
			oldWrap = append(oldWrap, wrap != nil && wrap[y] && y < last)
		}
	}
	keep(t.main, t.mainWrap)
	if t.altActive {
		keep(t.alt, t.altWrap)
	}
	sbOpen, x := t.sbOpen, t.x
	t.reset()
	t.sbOpen = sbOpen
	if x != nil && x.clusters != nil {
		// The kept rows may refer to characters with combining marks.
		t.x = &extra{clusters: x.clusters, clusterCap: x.clusterCap}
	}
	if over := len(old) - (t.rows - 1); over > 0 {
		for i, row := range old[:over] {
			t.pushScrollback(row, oldWrap[i], rowAt(old, i+1))
		}
		old, oldWrap = old[over:], oldWrap[over:]
	}
	copy(t.main, old)
	for y, w := range oldWrap {
		if w {
			t.setWrap(y, true)
		}
	}
	t.cur.y = len(old)
}
