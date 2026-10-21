package vt

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Conformance cases translated from other emulators' test suites (see
// testdata/conformance/NOTICE.md for sources and licenses). Each suite
// file registers its cases with registerSuite; TestConformance runs them
// all and, with VT_CONF_REPORT=1, prints per-suite counts.

// confCase is one expectation about the state after feeding in to a
// fresh cols x rows terminal. Unset expectations are not checked.
type confCase struct {
	name       string
	cols, rows int
	// in is fed with Write. rz(cols, rows) markers inside it resize the
	// terminal at that point.
	in string

	// screen is the visible screen as text: rows joined by "\n", trailing
	// blanks removed from each row and trailing blank rows dropped (like
	// ghostty's plainString). nil-equivalent is noScreen.
	screen *string
	// lines, if non-nil, must equal Lines() exactly (one entry per row).
	lines []string
	// cur is the expected cursor (x, y), 0-based.
	cur *[2]int
	// pending is the expected wrap-pending state.
	pending *bool
	// scrollback, if non-nil, must equal ScrollbackLines().
	scrollback []string
	// alt is the expected AltScreen().
	alt *bool
	// check runs arbitrary assertions; it returns "" when they hold.
	check func(t *Terminal) string

	// gap, when set, says why the case is out of scope (a feature the
	// emulator deliberately does not model). Such cases are counted but
	// not checked.
	gap string
}

type confSuite struct {
	name  string
	cases []confCase
}

var confSuites []confSuite

func registerSuite(name string, cases []confCase) {
	confSuites = append(confSuites, confSuite{name, cases})
}

// Expectation helpers.
func scr(s string) *string { return &s }
func at(x, y int) *[2]int  { return &[2]int{x, y} }
func yes() *bool           { b := true; return &b }
func no() *bool            { b := false; return &b }

// rz returns a marker that resizes the terminal when reached in in.
func rz(cols, rows int) string { return fmt.Sprintf("\x00R%d,%d\x00", cols, rows) }

// idx and rgbColor build expected colors.
func idx(n int) Color             { return colorIndexed | Color(n) }
func rgbColor(r, g, b int) Color  { return rgb(r, g, b) }
func ulStyle(a Attr) int          { return int((a & ulMask) >> ulShift) }
func hasAttr(c Cell, a Attr) bool { return c.A&a != 0 }

// cellAt returns the cell at x, y of the visible screen (a zero Cell for
// rows never drawn on).
func (t *Terminal) cellAt(x, y int) Cell {
	row := t.screen()[y]
	if row == nil {
		return Cell{}
	}
	return row[x]
}

// cellText returns the characters at x, y: "" for a blank or the right
// half of a wide character.
func (t *Terminal) cellText(x, y int) string {
	c := t.cellAt(x, y)
	switch {
	case c.R == 0 || c.R == wideTail:
		return ""
	}
	return t.cellString(c)
}

// baseRune returns the rune stored at x, y without combining marks (0 for
// a blank, wideTail for the right half of a wide character).
func (t *Terminal) baseRune(x, y int) rune {
	c := t.cellAt(x, y)
	if c.R >= clusterBase {
		r, _ := utf8.DecodeRuneInString(t.cellString(c))
		return r
	}
	return c.R
}

// plainString is the screen as ghostty's plainString renders it.
func plainString(t *Terminal) string {
	return strings.TrimRight(strings.Join(t.Lines(), "\n"), "\n")
}

// feedConf writes in to term, honoring rz markers.
func feedConf(term *Terminal, in string) {
	for in != "" {
		i := strings.Index(in, "\x00R")
		if i < 0 {
			term.Write([]byte(in))
			return
		}
		term.Write([]byte(in[:i]))
		rest := in[i+2:]
		j := strings.IndexByte(rest, 0)
		c, r, _ := strings.Cut(rest[:j], ",")
		cols, _ := strconv.Atoi(c)
		rows, _ := strconv.Atoi(r)
		term.Resize(cols, rows)
		in = rest[j+1:]
	}
}

// runConf runs one case and returns its failures.
func runConf(c confCase) []string {
	term := New(c.cols, c.rows, Options{})
	feedConf(term, c.in)
	var errs []string
	if c.screen != nil {
		if got := plainString(term); got != *c.screen {
			errs = append(errs, fmt.Sprintf("screen\n got  %q\n want %q", got, *c.screen))
		}
	}
	if c.lines != nil {
		if got := term.Lines(); !slices.Equal(got, c.lines) {
			errs = append(errs, fmt.Sprintf("lines\n got  %q\n want %q", got, c.lines))
		}
	}
	if c.cur != nil {
		if x, y, _ := term.Cursor(); x != c.cur[0] || y != c.cur[1] {
			errs = append(errs, fmt.Sprintf("cursor got %d,%d want %d,%d", x, y, c.cur[0], c.cur[1]))
		}
	}
	if c.pending != nil && term.cur.wrapPending != *c.pending {
		errs = append(errs, fmt.Sprintf("wrap pending got %v want %v", term.cur.wrapPending, *c.pending))
	}
	if c.scrollback != nil {
		if got := term.ScrollbackLines(); !slices.Equal(got, c.scrollback) {
			errs = append(errs, fmt.Sprintf("scrollback\n got  %q\n want %q", got, c.scrollback))
		}
	}
	if c.alt != nil && term.AltScreen() != *c.alt {
		errs = append(errs, fmt.Sprintf("alt screen got %v want %v", term.AltScreen(), *c.alt))
	}
	if c.check != nil {
		if msg := c.check(term); msg != "" {
			errs = append(errs, msg)
		}
	}
	return errs
}

// knownSeen records the knownFailures entries that matched a failure.
var knownSeen = map[string]bool{}

// TestMain also checks, on a full run, that every knownFailures entry
// still names a failing case.
// VT_CONF_IGNORE_KNOWN=1 reports the known failures too.
func TestMain(m *testing.M) {
	if os.Getenv("VT_CONF_IGNORE_KNOWN") != "" {
		knownFailures = nil
	}
	code := m.Run()
	if code == 0 && flag.Lookup("test.run").Value.String() == "" {
		for k := range knownFailures {
			if !knownSeen[k] {
				fmt.Printf("knownFailures entry %q matched no failing case: remove it\n", k)
				code = 1
			}
		}
	}
	os.Exit(code)
}

func TestConformance(t *testing.T) {
	type tally struct{ pass, fail, known, gap int }
	counts := map[string]*tally{}
	gaps := map[string]map[string]int{}
	for _, s := range confSuites {
		n := counts[s.name]
		if n == nil {
			n = &tally{}
			counts[s.name] = n
			gaps[s.name] = map[string]int{}
		}
		for _, c := range s.cases {
			if c.gap != "" {
				n.gap++
				gaps[s.name][c.gap]++
				continue
			}
			errs := runConf(c)
			if len(errs) == 0 {
				if _, ok := knownFailures[s.name+"/"+c.name]; ok {
					t.Errorf("%s/%s passes now: remove it from knownFailures", s.name, c.name)
				}
				n.pass++
				continue
			}
			key := s.name + "/" + c.name
			if _, ok := knownFailures[key]; ok {
				knownSeen[key] = true
				n.known++
				continue
			}
			n.fail++
			if os.Getenv("VT_CONF_LISTFAIL") != "" {
				fmt.Printf("\t%q: \"\",\n", key)
			}
			t.Errorf("%s (%dx%d, in %q):\n%s", key, c.cols, c.rows, c.in, strings.Join(errs, "\n"))
		}
	}
	if os.Getenv("VT_CONF_REPORT") != "" {
		names := make([]string, 0, len(counts))
		for n := range counts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			c := counts[n]
			fmt.Printf("suite %-10s pass %4d  fail %4d  known-fail %4d  out-of-scope %4d\n", n, c.pass, c.fail, c.known, c.gap)
			reasons := make([]string, 0, len(gaps[n]))
			for r := range gaps[n] {
				reasons = append(reasons, r)
			}
			sort.Strings(reasons)
			for _, r := range reasons {
				fmt.Printf("    %4d  %s\n", gaps[n][r], r)
			}
		}
	}
}
