package vt

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func feed(cols, rows int, in string) *Terminal {
	t := New(cols, rows, Options{})
	t.Write([]byte(in))
	return t
}

func TestBasics(t *testing.T) {
	cases := []struct {
		name, in string
		want     []string
		x, y     int
	}{
		{"text and newline", "ab\r\ncd", []string{"ab", "cd", ""}, 2, 1},
		{"pending wrap", "abcde", []string{"abcde", "", ""}, 4, 0},
		{"wrap on next char", "abcdef", []string{"abcde", "f", ""}, 1, 1},
		{"CR clears pending wrap", "abcde\rX", []string{"Xbcde", "", ""}, 1, 0},
		{"scroll", "1\r\n2\r\n3\r\n4", []string{"2", "3", "4"}, 1, 2},
		{"cup and erase", "hello\x1b[1;3H\x1b[K", []string{"he", "", ""}, 2, 0},
		{"wide char", "a漢b", []string{"a漢b", "", ""}, 4, 0},
		{"wide char wraps early", "abcd漢", []string{"abcd", "漢", ""}, 2, 1},
		{"overwrite wide half", "漢\x1b[1;2Hx", []string{" x", "", ""}, 2, 0},
		{"insert line", "1\r\n2\r\n3\x1b[2;1H\x1b[L", []string{"1", "", "2"}, 0, 1},
		{"delete chars", "abcde\x1b[1;2H\x1b[2P", []string{"ade", "", ""}, 1, 0},
		{"insert chars", "abc\x1b[1;2H\x1b[2@", []string{"a  bc", "", ""}, 1, 0},
		{"erase chars", "abcde\x1b[1;2H\x1b[3X", []string{"a   e", "", ""}, 1, 0},
		{"dec graphics", "\x1b(0lqk\x1b(Bx", []string{"┌─┐x", "", ""}, 4, 0},
		{"split utf8", "\xe6\xbc\xa2", []string{"漢", "", ""}, 2, 0},
		{"invalid utf8", "a\xffb", []string{"a�b", "", ""}, 3, 0},
		{"rep", "x\x1b[3b", []string{"xxxx", "", ""}, 4, 0},
		{"reverse index scrolls", "1\r\n2\x1b[H\x1bM", []string{"", "1", "2"}, 0, 0},
		{"scroll region", "\x1b[1;2r\x1b[2;1Ha\nb\nc", []string{" b", "  c", ""}, 3, 1},
		{"alt screen keeps cursor", "main\x1b[?1049hALT", []string{"    A", "LT", ""}, 2, 1},
		{"alt screen", "main\x1b[?1049h\x1b[HALT", []string{"ALT", "", ""}, 3, 0},
		{"alt screen exit", "main\x1b[?1049hALT\x1b[?1049l", []string{"main", "", ""}, 4, 0},
		{"osc ignored", "a\x1b]0;title\x07b\x1b]8;;http://x\x1b\\c", []string{"abc", "", ""}, 3, 0},
		{"dcs ignored", "a\x1bP1$r0m\x1b\\b", []string{"ab", "", ""}, 2, 0},
		{"cut sequence then text", "a\x1b[3", []string{"a", "", ""}, 1, 0},
	}
	for _, c := range cases {
		term := feed(5, 3, c.in)
		if got := term.Lines(); !slices.Equal(got, c.want) {
			t.Errorf("%s: lines %q, want %q", c.name, got, c.want)
		}
		if x, y, _ := term.Cursor(); x != c.x || y != c.y {
			t.Errorf("%s: cursor %d,%d want %d,%d", c.name, x, y, c.x, c.y)
		}
	}
}

func TestScrollbackAndModes(t *testing.T) {
	term := feed(10, 2, "\x1b]2;my title\x07\x1b[?2004h\x1b[?1h\x1b[>1u\x1b[?25l1\r\n2\r\n3\r\n4")
	if got := term.ScrollbackLines(); !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf("scrollback %q", got)
	}
	if term.Title() != "my title" || !term.bracketed || !term.appCursor || len(term.kitty) != 1 || !term.cursorHidden {
		t.Fatalf("modes not tracked: %+v", term)
	}
	// Scrollback respects its line budget.
	small := New(10, 2, Options{ScrollbackLines: 3})
	for i := range 100 {
		fmt.Fprintf(small, "%d\r\n", i)
	}
	if got := small.ScrollbackLines(); !slices.Equal(got, []string{"96", "97", "98"}) {
		t.Fatalf("bounded scrollback %q", got)
	}
	// And its byte budget.
	byBytes := New(200, 2, Options{ScrollbackBytes: 4096})
	for range 1000 {
		fmt.Fprintf(byBytes, "%s\r\n", strings.Repeat("x", 150))
	}
	if byBytes.sbBytes > 4096 || byBytes.ScrollbackLen() == 0 {
		t.Fatalf("scrollback bytes %d lines %d", byBytes.sbBytes, byBytes.ScrollbackLen())
	}
	// The alternate screen does not feed scrollback.
	alt := feed(10, 2, "\x1b[?1049h1\r\n2\r\n3\r\n4")
	if alt.ScrollbackLen() != 0 {
		t.Fatalf("alt screen scrolled into scrollback")
	}
}

func TestResize(t *testing.T) {
	term := feed(5, 4, "1\r\n2\r\n3")
	term.Resize(3, 2) // blank row 4 goes first, then row 1 to scrollback
	if got := term.Lines(); !slices.Equal(got, []string{"2", "3"}) {
		t.Fatalf("lines after shrink %q", got)
	}
	if got := term.ScrollbackLines(); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("scrollback after shrink %q", got)
	}
	if x, y, _ := term.Cursor(); x != 1 || y != 1 {
		t.Fatalf("cursor %d,%d", x, y)
	}
	term.Resize(6, 3)
	term.Write([]byte("\x1b[3;1Habcdef"))
	if got := term.Lines(); !slices.Equal(got, []string{"2", "3", "abcdef"}) {
		t.Fatalf("lines after grow %q", got)
	}
}

func TestArchiveScreen(t *testing.T) {
	term := feed(10, 3, "old1\r\nold2\x1b[?1049hALT")
	term.ArchiveScreen()
	if got := term.ScrollbackLines(); !slices.Equal(got, []string{"old1", "old2"}) {
		t.Fatalf("scrollback %q", got)
	}
	if term.AltScreen() || !slices.Equal(term.Lines(), []string{"", "", ""}) {
		t.Fatalf("screen not reset: %q", term.Lines())
	}
}

// Idle and lightly used terminals cost little: rows exist only once drawn
// on, and the alternate screen only while active.
func TestLazyAllocation(t *testing.T) {
	term := New(200, 50, Options{})
	term.Write([]byte("$ "))
	allocated := 0
	for _, row := range term.main {
		if row != nil {
			allocated++
		}
	}
	if allocated != 1 || term.alt != nil {
		t.Fatalf("%d rows allocated, alt %v", allocated, term.alt != nil)
	}
	term.Write([]byte("\x1b[?1049hx\x1b[?1049l"))
	if term.alt != nil {
		t.Fatal("alternate screen kept after exit")
	}
}

// --- invariants over random input ---

// tokens is the alphabet of the random streams: text, controls, and the
// sequences full-screen programs use.
var tokens = []string{
	"a", "hello", "x y", "漢字", "🙂", "é", "\r", "\n", "\r\n", "\b", "\t",
	"\x1b[H", "\x1b[2;3H", "\x1b[10;80H", "\x1b[A", "\x1b[3B", "\x1b[2C", "\x1b[D", "\x1b[5G", "\x1b[2d", "\x1b[E", "\x1b[F",
	"\x1b[K", "\x1b[1K", "\x1b[2K", "\x1b[J", "\x1b[1J", "\x1b[2J", "\x1b[3J",
	"\x1b[L", "\x1b[2M", "\x1b[@", "\x1b[3P", "\x1b[2X", "\x1b[S", "\x1b[2T", "\x1b[2b",
	"\x1b[m", "\x1b[1;31m", "\x1b[4:3m", "\x1b[38;5;200m", "\x1b[48;2;1;2;3m", "\x1b[38:2::10:20:30m", "\x1b[7;9;53m", "\x1b[22;24;27m", "\x1b[94;103m", "\x1b[39;49m",
	"\x1b[2;4r", "\x1b[r", "\x1b[?6h", "\x1b[?6l", "\x1b[?7l", "\x1b[?7h", "\x1b[4h", "\x1b[4l",
	"\x1b[?1049h", "\x1b[?1049l", "\x1b[?47h", "\x1b[?47l", "\x1b[?1047h", "\x1b[?1047l",
	"\x1b7", "\x1b8", "\x1b[s", "\x1b[u", "\x1bM", "\x1bD", "\x1bE",
	"\x1b(0", "\x1b(B", "\x1b)0", "\x0e", "\x0f", "lqk",
	"\x1b]2;t\x07", "\x1b[?1h", "\x1b[?2004h", "\x1b[?1000h", "\x1b[?1006h", "\x1b[?1004h", "\x1b[?25l", "\x1b[?25h",
	"\x1b[>3u", "\x1b[<u", "\x1b[=5;1u", "\x1b[2 q", "\x1b=", "\x1b>", "\x1b[!p",
}

func randomStream(rng *rand.Rand, n int) string {
	var b strings.Builder
	for range n {
		b.WriteString(tokens[rng.IntN(len(tokens))])
	}
	return b.String()
}

// state is a comparable summary of everything Repaint promises to carry.
func state(t *Terminal) string {
	var b strings.Builder
	dump := func(name string, scr [][]Cell) {
		for y, row := range scr {
			cells := make([]Cell, t.cols)
			copy(cells, row)
			for x := range cells {
				if cells[x].R == 0 {
					cells[x].R = ' '
				}
			}
			fmt.Fprintf(&b, "%s%d %v\n", name, y, cells)
		}
	}
	dump("main", t.main)
	if t.altActive {
		dump("alt", t.alt)
		fmt.Fprintf(&b, "saved1049 %v %d,%d\n", t.saved1049.valid, t.saved1049.x, t.saved1049.y)
	}
	cur := t.cur
	if cur.origin {
		cur.wrapPending = false
	}
	fmt.Fprintf(&b, "cur %+v region %d-%d\n", cur, t.top, t.bot)
	slot := 0
	if t.altActive {
		slot = 1
	}
	s := t.saved[slot]
	if s.valid {
		s.wrapPending = false
		if s.origin { // restoring clamps into the region
			s.y = min(max(s.y, t.top), t.bot)
		}
	}
	fmt.Fprintf(&b, "saved %+v\n", s)
	fmt.Fprintf(&b, "modes %v %v %v %v %v %v %v %v %d %d %d %v %q\n", t.autowrap, t.insert, t.newline, t.cursorHidden,
		t.appCursor, t.appKeypad, t.bracketed, t.focus, t.mouse, t.mouseEnc, t.cursorStyle, t.kitty, t.title)
	fmt.Fprintf(&b, "scrollback %q\n", bytes.Join(t.sb[t.sbStart:], []byte("\n")))
	return b.String()
}

// Repaint must reproduce the state on a fresh terminal of the same size.
func TestRepaintRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	for i := range 3000 {
		cols, rows := 1+rng.IntN(12), 1+rng.IntN(8)
		in := randomStream(rng, rng.IntN(60))
		src := feed(cols, rows, in)
		dst := New(cols, rows, Options{})
		dst.Write(src.Repaint(true))
		if got, want := state(dst), state(src); got != want {
			t.Fatalf("case %d (%dx%d) input %q\nrepaint %q\n%s", i, cols, rows, in, src.Repaint(true), lineDiff(got, want))
		}
	}
}

// How output is split into writes must not matter.
func TestChunkingInvariant(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 10))
	for range 500 {
		in := []byte(randomStream(rng, 80))
		whole := New(9, 5, Options{})
		whole.Write(in)
		split := New(9, 5, Options{})
		for rest := in; len(rest) > 0; {
			n := min(len(rest), 1+rng.IntN(7))
			split.Write(rest[:n])
			rest = rest[n:]
		}
		if state(whole) != state(split) {
			t.Fatalf("chunking changed the result for %q", in)
		}
	}
}

// A stream cut anywhere, even inside an escape sequence, leaves a sane
// terminal that keeps working.
func TestCutAnywhere(t *testing.T) {
	in := []byte("\x1b[1;31mred\x1b]2;title\x07\x1b[?1049h\x1b[2;3H漢\x1b[38;2;1;2;3mx")
	for cut := range len(in) {
		term := New(10, 3, Options{})
		term.Write(in[:cut])
		term.Write([]byte("\x18\x1b[0m\x1b[?1049l\x1b[Hok"))
		if !strings.HasPrefix(term.Lines()[0], "ok") {
			t.Fatalf("cut at %d: %q", cut, term.Lines())
		}
	}
}

func FuzzWrite(f *testing.F) {
	f.Add([]byte("\x1b[2;4r\x1b[?1049h漢\x1b[3M\x1b[?6h\x1b[5;5H"), uint8(10), uint8(4))
	f.Fuzz(func(t *testing.T, in []byte, c, r uint8) {
		cols, rows := 1+int(c%40), 1+int(r%20)
		src := New(cols, rows, Options{})
		src.Write(in)
		_ = src.Lines()
		dst := New(cols, rows, Options{})
		dst.Write(src.Repaint(true))
		if !slices.Equal(dst.Lines(), src.Lines()) {
			t.Fatalf("repaint lines differ: %q vs %q", dst.Lines(), src.Lines())
		}
	})
}

func lineDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	var b strings.Builder
	for i := range max(len(g), len(w)) {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			fmt.Fprintf(&b, "got:  %s\nwant: %s\n", gl, wl)
		}
	}
	return b.String()
}
