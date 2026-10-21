package vt

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAlacrittyRef replays alacritty's reference tests (recorded program
// output plus the grid alacritty produced, Apache-2.0/MIT; converted to
// testdata/conformance/alacritty/*/expected.json.gz by a script that keeps
// each line's text, styles and wrap flag) and compares every line.
func TestAlacrittyRef(t *testing.T) {
	dirs, _ := filepath.Glob("testdata/conformance/alacritty/*/expected.json.gz")
	if len(dirs) == 0 {
		t.Fatal("no alacritty ref tests")
	}
	var tests, testsPass, lines, linesPass, known int
	for _, p := range dirs {
		dir := filepath.Dir(p)
		name := filepath.Base(dir)
		var want struct {
			Cols, Rows, History int
			Lines               []struct {
				History bool
				Text    string
				Wrap    bool
				Style   string
			}
		}
		if err := json.Unmarshal(gunzip(t, p), &want); err != nil {
			t.Fatal(err)
		}
		sb := want.History
		if sb == 0 {
			sb = -1
		}
		term := New(want.Cols, want.Rows, Options{ScrollbackLines: max(sb, -1), ScrollbackBytes: 64 << 20})
		term.Write(gunzip(t, filepath.Join(dir, "recording.gz")))

		var errs []string
		var hist []string
		var logical strings.Builder
		y := 0
		for _, l := range want.Lines {
			if l.History {
				logical.WriteString(l.Text)
				if !l.Wrap {
					hist = append(hist, logical.String())
					logical.Reset()
				}
				continue
			}
			lines++
			text, style := term.alacrittyRow(y)
			wrap := y < term.rows-1 && term.wrapped(y+1)
			switch {
			case text != l.Text:
				errs = append(errs, fmt.Sprintf("row %d text\n got  %q\n want %q", y, text, l.Text))
			case style != l.Style:
				errs = append(errs, fmt.Sprintf("row %d style (%q)\n got  %s\n want %s", y, text, style, l.Style))
			case wrap != l.Wrap:
				errs = append(errs, fmt.Sprintf("row %d wrap got %v want %v", y, wrap, l.Wrap))
			default:
				linesPass++
			}
			y++
		}
		if logical.Len() > 0 {
			hist = append(hist, logical.String())
		}
		if len(hist) > 0 {
			lines++
			got := term.ScrollbackLines()
			// alacritty's history limit counts rows, ours lines: compare
			// the newest lines.
			n := min(len(got), len(hist))
			if strings.Join(got[len(got)-n:], "\n") != strings.Join(hist[len(hist)-n:], "\n") || n == 0 {
				errs = append(errs, fmt.Sprintf("scrollback\n got  %q\n want %q", got, hist))
			} else {
				linesPass++
			}
		}
		tests++
		key := "alacritty/" + name
		_, isKnown := knownFailures[key]
		switch {
		case len(errs) == 0 && isKnown:
			t.Errorf("%s passes now: remove it from knownFailures", key)
			testsPass++
		case len(errs) == 0:
			testsPass++
		case isKnown:
			known++
		default:
			if os.Getenv("VT_CONF_LISTFAIL") != "" {
				fmt.Printf("\t%q: \"\",\n", key)
			}
			t.Errorf("%s: %d mismatches, first:\n%s", key, len(errs), strings.Join(errs[:min(len(errs), 3)], "\n"))
		}
	}
	if os.Getenv("VT_CONF_REPORT") != "" {
		fmt.Printf("suite alacritty  tests pass %d/%d (known-fail %d), lines pass %d/%d\n", testsPass, tests, known, linesPass, lines)
	}
}

func gunzip(t *testing.T, path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// alacrittyRow renders row y as the converter renders alacritty's grid:
// the text (blanks as spaces, trailing default blanks trimmed) and the
// run-length encoded styles of the same cells.
func (t *Terminal) alacrittyRow(y int) (string, string) {
	row := t.screen()[y]
	if row == nil {
		return "", ""
	}
	end := len(row)
	for end > 0 && (row[end-1] == Cell{} || row[end-1] == Cell{R: ' '}) {
		end--
	}
	var text strings.Builder
	var styles []string
	for _, c := range row[:end] {
		if c.R == wideTail {
			continue
		}
		if c.R == 0 {
			text.WriteByte(' ')
		} else {
			text.WriteString(t.cellString(c))
		}
		styles = append(styles, alacrittyColor(c.Fg)+"/"+alacrittyColor(c.Bg)+"/"+alacrittyFlags(c.A))
	}
	var rle []string
	for i := 0; i < len(styles); {
		j := i
		for j < len(styles) && styles[j] == styles[i] {
			j++
		}
		rle = append(rle, fmt.Sprintf("%d*%s", j-i, styles[i]))
		i = j
	}
	return text.String(), strings.Join(rle, " ")
}

func alacrittyColor(c Color) string {
	switch {
	case c == 0:
		return "-"
	case c&colorRGB != 0:
		return fmt.Sprintf("r%06x", uint32(c&0xffffff))
	}
	return fmt.Sprintf("i%d", c&0xff)
}

// alacrittyFlags lists the attributes alacritty models, sorted as the
// converter sorts them (alacritty has no blink or overline).
func alacrittyFlags(a Attr) string {
	var f []string
	if a&attrBold != 0 {
		f = append(f, "B")
	}
	if a&attrFaint != 0 {
		f = append(f, "D")
	}
	if a&attrHidden != 0 {
		f = append(f, "H")
	}
	if a&attrItalic != 0 {
		f = append(f, "I")
	}
	if a&attrInverse != 0 {
		f = append(f, "R")
	}
	if a&attrStrike != 0 {
		f = append(f, "S")
	}
	if ul := ulStyle(a); ul != 0 {
		f = append(f, fmt.Sprintf("U%d", ul))
	}
	return strings.Join(f, "")
}
