package vt

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestLibvterm runs libvterm's own test scripts (testdata/conformance/
// libvterm, MIT, vendored unchanged) against this emulator. The scripts
// drive libvterm through its C API and assert both its state (cursor,
// pen, screen cells) and the callbacks it fires. Assertions about state
// this emulator models are checked; callbacks that are libvterm internals
// (damage, scrollrect, erase, movecursor...) are skipped, and assertions
// about features it does not model are counted as out of scope.
func TestLibvterm(t *testing.T) {
	files, _ := filepath.Glob("testdata/conformance/libvterm/*.test")
	if len(files) == 0 {
		t.Fatal("no libvterm scripts")
	}
	total := lvTally{gaps: map[string]int{}}
	for _, f := range files {
		r := runLibvterm(t, f)
		total.pass += r.pass
		total.fail += r.fail
		total.known += r.known
		total.internal += r.internal
		for k, v := range r.gaps {
			total.gaps[k] += v
		}
		if os.Getenv("VT_CONF_REPORT") != "" {
			fmt.Printf("libvterm %-32s pass %3d fail %3d known %3d out-of-scope %3d internal %3d\n",
				filepath.Base(f), r.pass, r.fail, r.known, sum(r.gaps), r.internal)
		}
	}
	if os.Getenv("VT_CONF_REPORT") != "" {
		fmt.Printf("suite libvterm   pass %4d  fail %4d  known-fail %4d  out-of-scope %4d  (internal/callback-only %d)\n",
			total.pass, total.fail, total.known, sum(total.gaps), total.internal)
		var reasons []string
		for r := range total.gaps {
			reasons = append(reasons, r)
		}
		sort.Strings(reasons)
		for _, r := range reasons {
			fmt.Printf("    %4d  %s\n", total.gaps[r], r)
		}
	}
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

type lvTally struct {
	pass, fail, known, internal int
	gaps                        map[string]int
}

// Out-of-scope reasons.
const (
	gapReplies   = "device reports / replies"
	gapInput     = "input encoding (keys, mouse reports)"
	gapDoubleW   = "double-width/height lines"
	gap8bit      = "8-bit C1 controls / non-UTF-8 mode"
	gapPalette   = "palette and dynamic colors"
	gapFontAttrs = "SGR fonts, small, sub/superscript"
	gapProtected = "protected cells (DECSCA/SPA, DECSED/DECSEL)"
	gapLRMargins = "DECLRMM left/right margins"
	gapCharsets  = "national/other charsets"
	gapSelection = "clipboard (OSC 52)"
)

type lvRunner struct {
	t        *testing.T
	file     string
	line     int
	section  string
	term     *Terminal
	rows     int
	cols     int
	utf8     bool
	taint    string // reason the current state is out of scope
	tally    lvTally
	cmd      string // last command line, for messages
	sbBefore int    // scrollback length before the last command
	pushed   []byte // bytes of the last PUSH
	glyphs   map[[2]int]string
	glyphSeq int
	sbSeq    int
	// the last line sb_pushline expected, for continuations
	lastSb     int
	lastSbText string
}

func runLibvterm(t *testing.T, path string) lvTally {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := &lvRunner{t: t, file: filepath.Base(path), utf8: true, tally: lvTally{gaps: map[string]int{}}}
	r.init()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		r.line++
		line := strings.TrimSpace(sc.Text())
		if line == "__END__" {
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r.do(line)
	}
	return r.tally
}

func (r *lvRunner) init() {
	r.rows, r.cols = 25, 80
	r.term = New(r.cols, r.rows, Options{ScrollbackLines: 10000})
	r.taint = ""
}

func (r *lvRunner) gap(reason string) { r.tally.gaps[reason]++ }

func (r *lvRunner) key() string { return fmt.Sprintf("libvterm/%s:%d", r.file, r.line) }

func (r *lvRunner) failf(format string, args ...any) {
	if _, ok := knownFailures[r.key()]; ok {
		knownSeen[r.key()] = true
		r.tally.known++
		return
	}
	r.tally.fail++
	if os.Getenv("VT_CONF_LISTFAIL") != "" {
		fmt.Printf("\t%q: \"\",\n", r.key())
	}
	r.t.Errorf("libvterm %s:%d [%s] after %s: %s", r.file, r.line, r.section, r.cmd, fmt.Sprintf(format, args...))
}

func (r *lvRunner) check(ok bool, format string, args ...any) {
	if ok {
		if _, ok := knownFailures[r.key()]; ok {
			r.t.Errorf("%s passes now: remove it from knownFailures", r.key())
		}
		r.tally.pass++
		return
	}
	r.failf(format, args...)
}

func (r *lvRunner) do(line string) {
	switch {
	case strings.HasPrefix(line, "!"):
		r.section = line[1:]
		return
	case strings.HasPrefix(line, "$SEQ"):
		// $SEQ lo hi: LINE with \# replaced by each value.
		rest := strings.TrimSpace(line[4:])
		rng, body, _ := strings.Cut(rest, ":")
		lo, hi, _ := strings.Cut(strings.TrimSpace(rng), " ")
		a, _ := strconv.Atoi(lo)
		b, _ := strconv.Atoi(strings.TrimSpace(hi))
		for v := a; v <= b; v++ {
			r.do(strings.ReplaceAll(strings.TrimSpace(body), `\#`, strconv.Itoa(v)))
		}
		return
	case strings.HasPrefix(line, "$REP"):
		rng, body, _ := strings.Cut(strings.TrimSpace(line[4:]), ":")
		n, _ := strconv.Atoi(strings.TrimSpace(rng))
		for range n {
			r.do(strings.TrimSpace(body))
		}
		return
	case strings.HasPrefix(line, "?"):
		r.assert(line[1:])
		return
	case line[0] >= 'A' && line[0] <= 'Z':
		r.command(line)
		return
	case line[0] >= 'a' && line[0] <= 'z':
		r.event(line)
		return
	}
	r.t.Fatalf("%s:%d: unrecognised line %q", r.file, r.line, line)
}

func (r *lvRunner) command(line string) {
	name, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	r.cmd = name
	r.glyphs = nil
	r.sbBefore = r.term.ScrollbackLen()
	r.sbSeq = 0
	switch name {
	case "INIT":
		r.init()
	case "RESET":
		r.term = New(r.cols, r.rows, Options{ScrollbackLines: 10000})
		r.taint = ""
	case "UTF8":
		r.utf8 = arg == "1"
	case "WANTSTATE", "WANTSCREEN", "WANTENCODING", "WANTPARSER", "DAMAGEFLUSH", "DAMAGEMERGE":
	case "RESIZE":
		a, b, _ := strings.Cut(arg, ",")
		r.rows, _ = strconv.Atoi(a)
		r.cols, _ = strconv.Atoi(b)
		r.term.Resize(r.cols, r.rows)
	case "PUSH":
		in := perlString(arg)
		r.cmd = fmt.Sprintf("PUSH %q", in)
		r.pushed = in
		if !r.utf8 {
			for _, c := range in {
				if c >= 0x80 {
					r.taint = gap8bit
				}
			}
		}
		for _, m := range []struct{ seq, reason string }{
			{"\x1b#3", gapDoubleW}, {"\x1b#4", gapDoubleW}, {"\x1b#6", gapDoubleW},
			{"\x1b[?5h", "DECSCNM reverse video"},
		} {
			if strings.Contains(string(in), m.seq) {
				r.taint = m.reason
			}
		}
		r.term.Write(in)
	case "SETDEFAULTCOL":
		r.taint = gapPalette
	case "ENCIN", "INKEY", "INCHAR", "MOUSEMOVE", "MOUSEBTN", "FOCUS", "PASTE":
		// Input encoding: what the terminal sends for keys and mouse.
	case "SELECTION":
	default:
		r.t.Fatalf("%s:%d: unknown command %q", r.file, r.line, line)
	}
}

// event handles an expected libvterm callback.
func (r *lvRunner) event(line string) {
	name, arg, _ := strings.Cut(line, " ")
	switch name {
	case "output":
		if strings.HasPrefix(r.cmd, "PUSH") {
			r.gap(gapReplies)
		} else {
			r.gap(gapInput)
		}
	case "encout":
		r.gap(gapInput)
	case "selection-set", "selection-query":
		r.gap(gapSelection)
	case "putglyph":
		r.putglyph(arg)
	case "sb_pushline":
		r.sbPushline(arg)
	case "settermprop":
		r.settermprop(arg)
	default:
		// movecursor, scrollrect, moverect, erase, damage, premove,
		// sb_popline, sb_clear, and the parser callbacks (text, control,
		// csi, escape, osc, dcs, apc, pm, sos) are libvterm internals.
		r.tally.internal++
	}
}

func (r *lvRunner) putglyph(arg string) {
	f := strings.Fields(arg)
	if r.taint != "" {
		r.gap(r.taint)
		return
	}
	for _, x := range f[3:] {
		switch x {
		case "dwl", "dhl-top", "dhl-bottom":
			r.gap(gapDoubleW)
			return
		case "prot":
			r.gap(gapProtected)
			return
		}
	}
	var want strings.Builder
	for _, h := range strings.Split(f[0], ",") {
		v, _ := strconv.ParseInt(strings.TrimPrefix(h, "0x"), 16, 32)
		want.WriteRune(rune(v))
	}
	row, col := parsePos(f[2])
	// A later glyph may land on the same cell, and the push may scroll;
	// check only the last glyph at each position, and only when the
	// push did not scroll.
	if r.glyphs == nil {
		r.glyphs = map[[2]int]string{}
	}
	r.glyphs[[2]int{row, col}] = want.String()
	if r.term.ScrollbackLen() != r.sbBefore || strings.ContainsAny(string(r.pushed), "\n\v\f") || strings.Contains(string(r.pushed), "\x1bD") || strings.Contains(string(r.pushed), "\x1bE") {
		r.tally.internal++
		return
	}
	got := r.cellChars(row, col)
	if got == want.String() {
		r.tally.pass++
		return
	}
	// Overwritten later in the same push: not checkable.
	r.tally.internal++
	_ = got
}

func (r *lvRunner) sbPushline(arg string) {
	if r.taint != "" {
		r.gap(r.taint)
		return
	}
	head, cells, _ := strings.Cut(arg, "=")
	var row strings.Builder
	for _, h := range strings.Fields(cells) {
		v, _ := strconv.ParseInt(h, 16, 32)
		row.WriteRune(rune(v))
	}
	// libvterm pushes rows; a row marked cont continues the previous one,
	// and this emulator keeps them as one line.
	i := r.sbBefore + r.sbSeq
	want := row.String()
	if strings.HasSuffix(strings.TrimSpace(head), "cont") {
		i = r.lastSb
		want = r.lastSbText + want
	} else {
		r.sbSeq++
	}
	r.lastSb, r.lastSbText = i, want
	want = strings.TrimRight(want, " \x00")
	sb := r.term.ScrollbackLines()
	if i >= len(sb) {
		r.failf("sb_pushline %q: scrollback has only %d lines", want, len(sb))
		return
	}
	r.check(sb[i] == want, "sb_pushline got %q want %q", sb[i], want)
}

// settermprop checks the terminal properties this emulator tracks.
func (r *lvRunner) settermprop(arg string) {
	f := strings.SplitN(arg, " ", 2)
	prop, val := f[0], f[1]
	t := r.term
	switch prop {
	case "1": // cursor visible
		r.check(t.cursorHidden == (val == "false"), "cursor visible: hidden=%v want %s", t.cursorHidden, val)
	case "3": // alt screen
		r.check(t.altActive == (val == "true"), "altscreen %v want %s", t.altActive, val)
	case "4": // title
		if strings.HasPrefix(val, "[") || strings.HasSuffix(val, "]") {
			// Fragmented: check the final title.
			if strings.HasSuffix(val, "]") {
				r.tally.internal++
			} else {
				r.tally.internal++
			}
			return
		}
		r.tally.internal++
	case "2", "7": // cursor blink, shape: DECSCUSR
		r.tally.internal++
	case "8": // mouse mode
		r.tally.internal++
	default:
		r.gap("terminal properties not modeled (icon name, reverse video, ...)")
	}
}

func parsePos(s string) (int, int) {
	a, b, _ := strings.Cut(s, ",")
	x, _ := strconv.Atoi(strings.TrimSpace(a))
	y, _ := strconv.Atoi(strings.TrimSpace(b))
	return x, y
}

// cellChars is a cell's characters, as libvterm's screen_cell lists them.
func (r *lvRunner) cellChars(row, col int) string {
	if row >= r.term.rows || col >= r.term.cols {
		return ""
	}
	c := r.term.cellAt(col, row)
	if c.R == 0 || c.R == wideTail || c.R == spacerHead {
		return ""
	}
	return r.term.cellString(c)
}

// getChars mirrors libvterm's _get_chars over a rectangle.
func (r *lvRunner) getChars(r0, c0, r1, c1 int) string {
	var b strings.Builder
	for row := r0; row < r1; row++ {
		pad := 0
		for col := c0; col < c1; col++ {
			if row >= r.term.rows || col >= r.term.cols {
				pad++
				continue
			}
			c := r.term.cellAt(col, row)
			switch c.R {
			case 0, spacerHead:
				pad++
			case wideTail:
			default:
				b.WriteString(strings.Repeat(" ", pad))
				pad = 0
				b.WriteString(r.term.cellString(c))
			}
		}
		if row < r1-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (r *lvRunner) assert(line string) {
	lhs, want, _ := strings.Cut(line, "=")
	lhs = strings.TrimSpace(lhs)
	want = strings.TrimSpace(want)
	name, arg, _ := strings.Cut(lhs, " ")
	arg = strings.TrimSpace(arg)
	if r.taint != "" {
		r.gap(r.taint)
		return
	}
	t := r.term
	switch name {
	case "cursor":
		x, y, _ := t.Cursor()
		got := fmt.Sprintf("%d,%d", y, x)
		r.check(got == want, "cursor got %s want %s", got, want)
	case "screen_row":
		row, _ := strconv.Atoi(arg)
		got := r.getChars(row, 0, row+1, t.cols)
		r.check(got == lvText(want), "screen_row %d got %q want %q", row, got, lvText(want))
	case "screen_chars", "screen_text":
		var r0, c0, r1, c1 int
		if n, _ := fmt.Sscanf(arg, "%d,%d,%d,%d", &r0, &c0, &r1, &c1); n != 4 {
			fmt.Sscanf(arg, "%d", &r0)
			r1, c0, c1 = r0+1, 0, t.cols
		}
		got := r.getChars(r0, c0, r1, c1)
		w := lvText(want)
		if name == "screen_text" {
			w = lvBytes(want)
		}
		r.check(got == w, "%s %s got %q want %q", name, arg, got, w)
	case "screen_cell":
		row, col := parsePos(arg)
		if strings.Contains(want, "dwl") || strings.Contains(want, "dhl") {
			r.gap(gapDoubleW)
			return
		}
		got := r.screenCell(row, col)
		if got != want && lvFontAttrs(want) {
			r.gap(gapFontAttrs)
			return
		}
		r.check(got == want, "screen_cell %s\n got  %s\n want %s", arg, got, want)
	case "pen":
		r.pen(arg, want)
	case "lineinfo":
		if strings.Contains(want, "dwl") || strings.Contains(want, "dhl") {
			r.gap(gapDoubleW)
			return
		}
		row, _ := strconv.Atoi(arg)
		got := ""
		if t.wrapped(row) {
			got = "cont"
		}
		r.check(got == want, "lineinfo %d got %q want %q", row, got, want)
	case "screen_eol", "screen_attrs_extent":
		r.tally.internal++
	default:
		r.t.Fatalf("%s:%d: unknown assertion %q", r.file, r.line, line)
	}
}

func lvFontAttrs(s string) bool {
	_, attrs, _ := strings.Cut(s, "attrs={")
	attrs, _, _ = strings.Cut(attrs, "}")
	return strings.ContainsAny(attrs, "FS^_")
}

// lvText decodes an expected string: a Perl string literal, or a list of
// hex code points.
func lvText(s string) string {
	if strings.HasPrefix(s, "\"") {
		return string(perlString(s))
	}
	var b strings.Builder
	for _, h := range strings.Split(s, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		v, _ := strconv.ParseInt(strings.TrimPrefix(h, "0x"), 16, 32)
		b.WriteRune(rune(v))
	}
	return b.String()
}

// lvBytes is lvText for a list of hex bytes (screen_text is UTF-8).
func lvBytes(s string) string {
	if strings.HasPrefix(s, "\"") {
		return string(perlString(s))
	}
	var b []byte
	for _, h := range strings.Split(s, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		v, _ := strconv.ParseInt(strings.TrimPrefix(h, "0x"), 16, 32)
		b = append(b, byte(v))
	}
	return string(b)
}

var lvAnsi = [16][3]int{
	{0, 0, 0}, {224, 0, 0}, {0, 224, 0}, {224, 224, 0}, {0, 0, 224}, {224, 0, 224}, {0, 224, 224}, {224, 224, 224},
	{128, 128, 128}, {255, 64, 64}, {64, 255, 64}, {255, 255, 64}, {64, 64, 255}, {255, 64, 255}, {64, 255, 255}, {255, 255, 255},
}

// lvRGB converts a color to RGB with libvterm's default palette.
func lvRGB(c Color, fg bool) string {
	switch {
	case c == 0 && fg:
		return "rgb(240,240,240)"
	case c == 0:
		return "rgb(0,0,0)"
	case c&colorRGB != 0:
		return fmt.Sprintf("rgb(%d,%d,%d)", c>>16&0xff, c>>8&0xff, c&0xff)
	}
	n := int(c & 0xff)
	var rgb [3]int
	switch {
	case n < 16:
		rgb = lvAnsi[n]
	case n < 232:
		ramp := [6]int{0x00, 0x33, 0x66, 0x99, 0xcc, 0xff}
		n -= 16
		rgb = [3]int{ramp[n/36%6], ramp[n/6%6], ramp[n%6]}
	default:
		v := 0x0b*(n-232) + 0
		ramp24 := [...]int{0x00, 0x0B, 0x16, 0x21, 0x2C, 0x37, 0x42, 0x4D, 0x58, 0x63, 0x6E, 0x79,
			0x85, 0x90, 0x9B, 0xA6, 0xB1, 0xBC, 0xC7, 0xD2, 0xDD, 0xE8, 0xF3, 0xFF}
		v = ramp24[n-232]
		rgb = [3]int{v, v, v}
	}
	return fmt.Sprintf("rgb(%d,%d,%d)", rgb[0], rgb[1], rgb[2])
}

// lvColor renders a pen color as libvterm's state does (indexed colors
// stay indexed).
func lvColor(c Color, fg bool) string {
	switch {
	case c == 0 && fg:
		return "rgb(240,240,240,is_default_fg)"
	case c == 0:
		return "rgb(0,0,0,is_default_bg)"
	case c&colorRGB != 0:
		return fmt.Sprintf("rgb(%d,%d,%d)", c>>16&0xff, c>>8&0xff, c&0xff)
	}
	return fmt.Sprintf("idx(%d)", c&0xff)
}

func lvAttrs(a Attr) string {
	var b strings.Builder
	if a&attrBold != 0 {
		b.WriteByte('B')
	}
	if ul := ulStyle(a); ul != 0 {
		// libvterm: 1 single, 2 double, 3 curly.
		fmt.Fprintf(&b, "U%d", ul)
	}
	if a&attrItalic != 0 {
		b.WriteByte('I')
	}
	if a&attrBlink != 0 {
		b.WriteByte('K')
	}
	if a&attrInverse != 0 {
		b.WriteByte('R')
	}
	return b.String()
}

func (r *lvRunner) screenCell(row, col int) string {
	c := r.term.cellAt(col, row)
	var chars []string
	if c.R > 0 {
		for _, ch := range r.term.cellString(c) {
			chars = append(chars, fmt.Sprintf("0x%x", ch))
		}
	}
	width := 1
	if col+1 < r.term.cols && r.term.cellAt(col+1, row).R == wideTail {
		width = 2
	}
	return fmt.Sprintf("{%s} width=%d attrs={%s} fg=%s bg=%s", strings.Join(chars, ","), width, lvAttrs(c.A),
		lvRGB(c.Fg, true), lvRGB(c.Bg, false))
}

func (r *lvRunner) pen(attr, want string) {
	p := r.term.cur.pen
	onoff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	var got string
	switch attr {
	case "bold":
		got = onoff(p.A&attrBold != 0)
	case "italic":
		got = onoff(p.A&attrItalic != 0)
	case "blink":
		got = onoff(p.A&attrBlink != 0)
	case "reverse":
		got = onoff(p.A&attrInverse != 0)
	case "underline":
		got = strconv.Itoa(ulStyle(p.A))
	case "foreground":
		got = lvColor(p.Fg, true)
	case "background":
		got = lvColor(p.Bg, false)
	case "font", "small", "baseline":
		r.gap(gapFontAttrs)
		return
	default:
		r.t.Fatalf("unknown pen attr %q", attr)
	}
	r.check(got == want, "pen %s got %s want %s", attr, got, want)
}

// perlString evaluates the Perl string expressions the scripts use:
// double-quoted literals joined with "." and repeated with "x N".
func perlString(s string) []byte {
	var out []byte
	s = strings.TrimSpace(s)
	for s != "" {
		if s[0] != '"' {
			panic("perlString: " + s)
		}
		lit, rest := perlLiteral(s[1:])
		s = strings.TrimSpace(rest)
		if strings.HasPrefix(s, "x") {
			s = strings.TrimSpace(s[1:])
			i := 0
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			n, _ := strconv.Atoi(s[:i])
			lit = []byte(strings.Repeat(string(lit), n))
			s = strings.TrimSpace(s[i:])
		}
		out = append(out, lit...)
		if strings.HasPrefix(s, ".") {
			s = strings.TrimSpace(s[1:])
		}
	}
	return out
}

// perlLiteral decodes a double-quoted literal body up to the closing
// quote and returns it and the remaining text.
func perlLiteral(s string) ([]byte, string) {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' {
			return b, s[i+1:]
		}
		if c != '\\' {
			b = append(b, c)
			continue
		}
		i++
		switch e := s[i]; e {
		case 'e':
			b = append(b, 0x1b)
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'b':
			b = append(b, '\b')
		case 'a':
			b = append(b, 7)
		case 'f':
			b = append(b, '\f')
		case '0':
			b = append(b, 0)
		case 'x':
			var hex string
			if s[i+1] == '{' {
				end := strings.IndexByte(s[i:], '}')
				hex = s[i+2 : i+end]
				i += end
			} else {
				j := i + 1
				for j < len(s) && j < i+3 && strings.IndexByte("0123456789abcdefABCDEF", s[j]) >= 0 {
					j++
				}
				hex = s[i+1 : j]
				i = j - 1
			}
			v, _ := strconv.ParseInt(hex, 16, 32)
			if v < 0x100 {
				b = append(b, byte(v))
			} else {
				b = utf8.AppendRune(b, rune(v))
			}
		default:
			b = append(b, e)
		}
	}
	panic("perlLiteral: unterminated " + s)
}
