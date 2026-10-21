package vt

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// checkInvariants returns what is wrong with t's internal state, or "".
func (t *Terminal) checkInvariants() string {
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if len(t.main) != t.rows {
		fail("main has %d rows, want %d", len(t.main), t.rows)
	}
	if t.altActive && t.alt == nil {
		fail("alt screen active without rows")
	}
	if t.alt != nil && len(t.alt) != t.rows {
		fail("alt has %d rows, want %d", len(t.alt), t.rows)
	}
	for _, w := range [][]bool{t.mainWrap, t.altWrap} {
		if w != nil && (len(w) != t.rows || w[t.rows-1]) {
			fail("wrap flags %v for %d rows", w, t.rows)
		}
	}
	nClusters := 0
	if t.x != nil {
		nClusters = len(t.x.clusters)
	}
	for name, scr := range map[string][][]Cell{"main": t.main, "alt": t.alt} {
		for y, row := range scr {
			if row == nil {
				continue
			}
			if len(row) != t.cols {
				fail("%s row %d has %d cells, want %d", name, y, len(row), t.cols)
				continue
			}
			for x, c := range row {
				switch {
				case c.R == wideTail:
					if x == 0 || row[x-1].R <= 0 || t.runeWidth(row[x-1].R) != 2 {
						fail("%s %d,%d: right half without a left half", name, x, y)
					}
				case c.R >= clusterBase:
					if int(c.R-clusterBase) >= nClusters {
						fail("%s %d,%d: dangling cluster %d", name, x, y, c.R-clusterBase)
						continue
					}
					fallthrough
				case c.R > 0:
					if t.runeWidth(c.R) == 2 && (x+1 >= t.cols || row[x+1].R != wideTail) {
						fail("%s %d,%d: wide character without its right half", name, x, y)
					}
				case c.R < 0:
					fail("%s %d,%d: bad rune %d", name, x, y, c.R)
				}
			}
		}
	}
	inBounds := func(name string, c cursor) {
		if c.x < 0 || c.x >= t.cols || c.y < 0 || c.y >= t.rows {
			fail("%s %d,%d outside %dx%d", name, c.x, c.y, t.cols, t.rows)
		}
	}
	inBounds("cursor", t.cur)
	if t.cur.wrapPending && (t.cur.x != t.cols-1 && t.cur.x != t.right || !t.autowrap) {
		fail("pending wrap at x=%d (autowrap %v)", t.cur.x, t.autowrap)
	}
	for i, s := range t.saved {
		if s.valid {
			inBounds(fmt.Sprintf("saved[%d]", i), s.cursor)
		}
	}
	if t.saved1049.valid {
		inBounds("saved1049", t.saved1049.cursor)
	}
	if t.top < 0 || t.top > t.bot || t.bot >= t.rows {
		fail("region %d-%d", t.top, t.bot)
	}
	if t.left < 0 || t.left > t.right || t.right >= t.cols || !t.lrmm && !t.fullWidth() {
		fail("margins %d-%d (enabled %v)", t.left, t.right, t.lrmm)
	}
	n := 0
	for _, line := range t.sb[t.sbStart:] {
		n += len(line) + 24
	}
	if n != t.sbBytes {
		fail("sbBytes %d, lines hold %d", t.sbBytes, n)
	}
	if t.ScrollbackLen() > max(t.sbMaxLines, 0) {
		fail("%d scrollback lines over the limit %d", t.ScrollbackLen(), t.sbMaxLines)
	}
	if t.sbOpen && t.ScrollbackLen() == 0 {
		fail("open scrollback line without scrollback")
	}
	if tabs := t.tabs(); tabs != nil && len(tabs) != (t.cols+63)/64 {
		fail("%d tab words for %d columns", len(tabs), t.cols)
	}
	return strings.Join(errs, "\n")
}

// fuzzOps splits fuzz input into writes and resizes: a 0x00 byte followed
// by two bytes resizes to (b1%maxCols+1, b2%maxRows+1).
func fuzzOps(in []byte, maxCols, maxRows int, write func([]byte), resize func(c, r int)) {
	for len(in) > 0 {
		i := bytes.IndexByte(in, 0)
		if i < 0 {
			write(in)
			return
		}
		write(in[:i])
		if i+2 >= len(in) {
			return
		}
		resize(int(in[i+1])%maxCols+1, int(in[i+2])%maxRows+1)
		in = in[i+3:]
	}
}

// FuzzStream feeds arbitrary output with resizes in between and checks
// the state stays consistent and that Repaint, with and without the
// scrollback, reproduces it on a fresh terminal.
func FuzzStream(f *testing.F) {
	f.Add([]byte("\x1b[2;4r\x1b[?1049h漢\x1b[3M\x1b[?6h\x1b[5;5H"), uint8(10), uint8(4))
	f.Add([]byte("hello world this wraps\x00\x05\x03more text é\x00\x0c\x05"), uint8(8), uint8(3))
	f.Add([]byte("\x1b[41mcolored line that wraps around\r\n\x1b[0mnext\x00\x03\x09\x1b[3g\x1bH\tX"), uint8(9), uint8(2))
	f.Add([]byte("\x1b[?69h\x1b[2;5s\x1b[2;4rabcdefgh\x1bD\x1bM\x1b[L\x1b[2M\x1b[@\x1b[2P\x1b[S\x1b[T\x1b[?6h\x1b[1;1HX\x1b[?69l"), uint8(8), uint8(6))
	f.Add([]byte("\x1b[?45h\x1b[?7h0123456789\b\b\x1b[5D\x1b(0lqk\x1bN`\x1b[?47h47\x1b[?47l"), uint8(6), uint8(3))
	for _, seed := range []string{"vim.raw", "less.raw"} {
		if b, err := readTestdata(seed); err == nil && len(b) > 0 {
			f.Add(b[:min(len(b), 4096)], uint8(99), uint8(29))
		}
	}
	f.Fuzz(func(t *testing.T, in []byte, c, r uint8) {
		cols, rows := 1+int(c%100), 1+int(r%30)
		src := New(cols, rows, Options{ScrollbackLines: 50})
		fuzzOps(in, 100, 30, func(p []byte) {
			src.Write(p)
			if msg := src.checkInvariants(); msg != "" {
				t.Fatalf("after write: %s", msg)
			}
		}, func(c, r int) {
			src.Resize(c, r)
			if msg := src.checkInvariants(); msg != "" {
				t.Fatalf("after resize to %dx%d: %s", c, r, msg)
			}
		})
		checkRepaint(t, src)
	})
}

// FuzzTokens is FuzzStream over the alphabet of TestRepaintRoundTrip (one
// token per input byte), which reaches deep states much faster than raw
// bytes do. Bytes 0xf0 and up resize the terminal.
func FuzzTokens(f *testing.F) {
	f.Add([]byte{1, 2, 3, 0xf3, 40, 50, 60}, uint8(10), uint8(4))
	f.Fuzz(func(t *testing.T, sel []byte, c, r uint8) {
		cols, rows := 1+int(c%40), 1+int(r%12)
		src := New(cols, rows, Options{ScrollbackLines: 20})
		resized := false
		for i, b := range sel {
			if b >= 0xf0 {
				src.Resize(1+int(b-0xf0)*3+i%3, 1+(int(b)+i)%12)
				resized = true
			} else {
				src.Write([]byte(tokens[int(b)%len(tokens)]))
			}
			if msg := src.checkInvariants(); msg != "" {
				t.Fatalf("after op %d: %s", i, msg)
			}
		}
		checkRepaint(t, src)
		if !resized {
			// Without resizes the scrollback must come back byte for byte.
			dst := New(src.cols, src.rows, Options{ScrollbackLines: 20})
			dst.Write(src.Repaint(true))
			if got, want := state(dst), state(src); got != want {
				t.Fatalf("repaint state differs:\n%s", lineDiff(got, want))
			}
		}
	})
}

// checkRepaint checks that Repaint, with and without the scrollback,
// reproduces src on a fresh terminal of its size.
func checkRepaint(t *testing.T, src *Terminal) {
	t.Helper()
	cols, rows := src.Size()
	{
		for _, sb := range []bool{false, true} {
			rep := src.Repaint(sb)
			dst := New(cols, rows, Options{ScrollbackLines: max(src.sbMaxLines, 1)})
			dst.Write(rep)
			if got, want := stateOf(dst, false), stateOf(src, false); got != want {
				t.Fatalf("repaint(%v) state differs:\n%s\nrepaint %q", sb, lineDiff(got, want), rep)
			}
			// (A one-column terminal cannot show the wide characters of
			// lines it scrolled away when wider.)
			if sb && cols > 1 {
				// Lines scrolled away at another width rewrap on replay, and
				// a wide character that wraps leaves a blank at the end of
				// the row (kept in the line as a space): compare the text
				// without spaces.
				if got, want := noSpaces(dst.ScrollbackLines()), noSpaces(src.ScrollbackLines()); !slices.Equal(got, want) {
					t.Fatalf("repaint scrollback\n got  %q\n want %q", got, want)
				}
				if dst.sbOpen != src.sbOpen {
					t.Fatalf("repaint open line %v, want %v", dst.sbOpen, src.sbOpen)
				}
			}
		}
	}
}

// FuzzChunking checks that how output is split into writes does not
// matter.
func FuzzChunking(f *testing.F) {
	f.Add([]byte("\x1b[1;31mred\x1b]2;title\x07\x1b[?1049h\x1b[2;3H漢\x1b[38;2;1;2;3mx é"), uint8(3))
	f.Fuzz(func(t *testing.T, in []byte, step uint8) {
		whole := New(9, 5, Options{})
		whole.Write(in)
		split := New(9, 5, Options{})
		n := 1 + int(step%13)
		for rest := in; len(rest) > 0; {
			k := min(len(rest), n)
			split.Write(rest[:k])
			rest = rest[k:]
		}
		if a, b := state(whole), state(split); a != b {
			t.Fatalf("chunking by %d changed the result:\n%s", n, lineDiff(b, a))
		}
	})
}

func noSpaces(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.ReplaceAll(l, " ", "")
	}
	return out
}

func readTestdata(name string) ([]byte, error) { return os.ReadFile(filepath.Join("testdata", name)) }
