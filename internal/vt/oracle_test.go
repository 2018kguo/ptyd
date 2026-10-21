package vt

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	cvt "github.com/charmbracelet/x/vt"
)

var oscPattern = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// oracle runs the same bytes through charmbracelet/x/vt, an independent
// emulator, and returns its screen text and cursor. It departs from xterm
// in corners (wide characters at the margin, ED 1, DCH below the scroll
// region, DECRC without DECSC, NEL, the cursor after ?1049h) and draws OSC
// titles as text in some states, so it is only consulted on real program
// output, with OSC strings removed.
func oracle(cols, rows int, in []byte) ([]string, int, int) {
	in = oscPattern.ReplaceAll(in, nil)
	e := cvt.NewEmulator(cols, rows)
	// Drain replies to queries. The emulator is never closed: its Close
	// races with Read, so each call leaks one blocked goroutine.
	go io.Copy(io.Discard, e)
	e.Write(in)
	lines := make([]string, rows)
	for y := range rows {
		var b strings.Builder
		for x := 0; x < cols; x++ {
			c := e.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue
			}
			if c.Content == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Content)
			}
			x += max(c.Width, 1) - 1
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	pos := e.CursorPosition()
	return lines, pos.X, pos.Y
}

func compareWithOracle(t *testing.T, name string, cols, rows int, in []byte) {
	t.Helper()
	term := New(cols, rows, Options{})
	term.Write(oscPattern.ReplaceAll(in, nil))
	want, wx, wy := oracle(cols, rows, in)
	got := term.Lines()
	x, y, _ := term.Cursor()
	if !slices.Equal(got, want) || x != wx || y != wy {
		var diff strings.Builder
		for i := range got {
			if got[i] != want[i] {
				fmt.Fprintf(&diff, "row %d:\n  got  %q\n  want %q\n", i, got[i], want[i])
			}
		}
		t.Fatalf("%s: differs from oracle (cursor %d,%d vs %d,%d)\n%s", name, x, y, wx, wy, diff.String())
	}
}

// Real full-screen programs (vim, less) recorded through ptyd, plus any
// recordings in $PTYD_VT_RECORDINGS (*.raw, replayed at 120x40), must
// render the same as in the oracle.
func TestOracleRecordings(t *testing.T) {
	type rec struct {
		path       string
		cols, rows int
	}
	var recs []rec
	// rec-nano.raw is left out: at cuts inside nano's line redraws the
	// oracle drops a cell where a wide character is split (this emulator
	// blanks it, as xterm and ghostty do).
	for _, name := range []string{"vim.raw", "less.raw", "rec-nvim.raw", "rec-vim.raw", "rec-less.raw", "rec-man.raw"} {
		recs = append(recs, rec{filepath.Join("testdata", name), 100, 30})
	}
	if dir := os.Getenv("PTYD_VT_RECORDINGS"); dir != "" {
		more, _ := filepath.Glob(filepath.Join(dir, "*.raw"))
		for _, p := range more {
			recs = append(recs, rec{p, 120, 40})
		}
	}
	for _, r := range recs {
		in, err := os.ReadFile(r.path)
		if err != nil {
			t.Fatal(err)
		}
		// Every prefix, so intermediate frames agree too.
		for cut := 0; cut <= len(in); cut += max(len(in)/50, 1) {
			compareWithOracle(t, fmt.Sprintf("%s[:%d]", r.path, cut), r.cols, r.rows, in[:cut])
		}
		compareWithOracle(t, r.path, r.cols, r.rows, in)
	}
}

// oracleTokens is the alphabet of the differential fuzzer: sequences whose
// meaning charmbracelet/x/vt and xterm agree on, so any difference is a
// bug in one of the two emulators. Queries are left out (nothing reads
// the oracle's replies), and so are wide characters (the oracle loses them
// at the right margin), scroll regions (DECSTBM homes the oracle's cursor
// to the top margin, with or without origin mode), DECAWM off and insert
// mode, where it departs from xterm.
var oracleTokens = []string{
	"a", "bc", "hello ", "xyz", "0123456789", " ", "é", "\r", "\n", "\r\n", "\b", "\t",
	"\x1b[H", "\x1b[2;3H", "\x1b[5;1H", "\x1b[1;8H", "\x1b[A", "\x1b[2A", "\x1b[B", "\x1b[3B", "\x1b[C", "\x1b[4C", "\x1b[D", "\x1b[2D",
	"\x1b[3G", "\x1b[2d", "\x1b[K", "\x1b[1K", "\x1b[2K", "\x1b[J", "\x1b[2J",
	"\x1b[L", "\x1b[2L", "\x1b[M", "\x1b[@", "\x1b[2@", "\x1b[P", "\x1b[3P", "\x1b[2X", "\x1b[S", "\x1b[T",
	"\x1b[m", "\x1b[1m", "\x1b[31m", "\x1b[44m", "\x1b[7m", "\x1bD", "\x1bM",
}

// FuzzOracle compares the screen text and cursor with charmbracelet/x/vt
// on streams of oracleTokens.
func FuzzOracle(f *testing.F) {
	f.Add([]byte{0, 1, 2, 30, 14, 5, 6}, uint8(10), uint8(5))
	f.Fuzz(func(t *testing.T, sel []byte, c, r uint8) {
		cols, rows := 2+int(c%30), 1+int(r%10)
		// Emulators disagree on what controls do to a pending wrap, and
		// charmbracelet on the last column with autowrap off (the
		// conformance suites cover both), so a CR comes first there.
		var in []byte
		term := New(cols, rows, Options{})
		for _, b := range sel {
			tok := oracleTokens[int(b)%len(oracleTokens)]
			if (term.cur.wrapPending || term.cur.x == cols-1) && (tok[0] < 0x20 || tok[0] == 0x1b) {
				tok = "\r" + tok
			}
			term.Write([]byte(tok))
			in = append(in, tok...)
		}
		e := cvt.NewEmulator(cols, rows)
		e.Write(in)
		got := term.Lines()
		x, y, _ := term.Cursor()
		pos := e.CursorPosition()
		for row := range rows {
			var b strings.Builder
			for col := 0; col < cols; col++ {
				cell := e.CellAt(col, row)
				if cell == nil || cell.Width == 0 {
					continue
				}
				if cell.Content == "" {
					b.WriteByte(' ')
				} else {
					b.WriteString(cell.Content)
				}
				col += max(cell.Width, 1) - 1
			}
			if want := strings.TrimRight(b.String(), " "); got[row] != want {
				t.Fatalf("%dx%d %q: row %d\n got  %q\n want %q", cols, rows, in, row, got[row], want)
			}
		}
		if x != pos.X || y != pos.Y {
			t.Fatalf("%dx%d %q: cursor %d,%d, oracle %d,%d", cols, rows, in, x, y, pos.X, pos.Y)
		}
	})
}
