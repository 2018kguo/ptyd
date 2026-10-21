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
	for _, name := range []string{"vim.raw", "less.raw"} {
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
