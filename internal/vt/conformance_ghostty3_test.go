// Translated from ghostty (MIT), src/terminal/Terminal.zig (tests from
// "insert mode does nothing at the end of the line" to the end of the file)
// and src/terminal/stream_terminal.zig (byte-stream tests) at commit 521cf2c.

package vt

import (
	"fmt"
	"strings"
)

func init() { registerSuite("ghostty", ghostty3Cases) }

// g3all runs several checks and returns the first failure.
func g3all(fs ...func(*Terminal) string) func(*Terminal) string {
	return func(t *Terminal) string {
		for _, f := range fs {
			if msg := f(t); msg != "" {
				return msg
			}
		}
		return ""
	}
}

// g3rune checks the rune stored at x, y (0 blank, wideTail right half).
func g3rune(x, y int, r rune) func(*Terminal) string {
	return func(t *Terminal) string {
		if got := t.baseRune(x, y); got != r {
			return fmt.Sprintf("cell %d,%d rune got %q (%d) want %q (%d)", x, y, got, got, r, r)
		}
		return ""
	}
}

// g3curRune checks the rune under the cursor.
func g3curRune(r rune) func(*Terminal) string {
	return func(t *Terminal) string {
		x, y, _ := t.Cursor()
		if got := t.baseRune(x, y); got != r {
			return fmt.Sprintf("cell under cursor %d,%d got %q want %q", x, y, got, r)
		}
		return ""
	}
}

// g3bg checks the background of cells x0..x1-1 on row y.
func g3bg(x0, x1, y int, c Color) func(*Terminal) string {
	return func(t *Terminal) string {
		for x := x0; x < x1; x++ {
			if got := t.cellAt(x, y).Bg; got != c {
				return fmt.Sprintf("cell %d,%d bg got %#x want %#x", x, y, got, c)
			}
		}
		return ""
	}
}

// g3cell checks fg, bg and attributes of one cell.
func g3cell(x, y int, fg, bg Color, a Attr) func(*Terminal) string {
	return func(t *Terminal) string {
		c := t.cellAt(x, y)
		if c.Fg != fg || c.Bg != bg || c.A != a {
			return fmt.Sprintf("cell %d,%d style got fg %#x bg %#x attr %#x want fg %#x bg %#x attr %#x",
				x, y, c.Fg, c.Bg, c.A, fg, bg, a)
		}
		return ""
	}
}

// g3is checks an arbitrary predicate.
func g3is(msg string, ok func(*Terminal) bool) func(*Terminal) string {
	return func(t *Terminal) string {
		if !ok(t) {
			return msg
		}
		return ""
	}
}

// g3uniqueStyles reproduces "resize with high unique style per cell":
// every cell gets its own direct background color.
func g3uniqueStyles(wrapping bool) string {
	var b strings.Builder
	if wrapping {
		for i := 0; i < 30*30; i++ {
			fmt.Fprintf(&b, "\x1b[48;2;%d;%d;0mx", i>>8, i&0xff)
		}
	} else {
		for y := 0; y < 30; y++ {
			for x := 0; x < 30; x++ {
				// setCursorPos(y, x) with 0 clamped to 1 like ghostty.
				fmt.Fprintf(&b, "\x1b[%d;%dH\x1b[48;2;%d;%d;0mx", max(y, 1), max(x, 1), x, y)
			}
		}
	}
	return b.String() + rz(60, 30)
}

// g3resizePendingWrap expands "resize pending wrap live and saved cursors".
func g3resizePendingWrap() []confCase {
	type rc struct {
		text    string
		cols    int
		pending bool
		want    string
	}
	cases := []rc{
		{"ABCD", 6, false, "ABCDX"},
		{"ABCD", 3, false, "ABC\nDX"},
		{"ABCD", 2, true, "AB\nCD\nX"},
		{"ABCD", 4, true, "ABCD\nX"},
		{"ABCDEFGH", 6, false, "ABCDEF\nGHX"},
		{"AB界", 6, false, "AB界X"},
		{"ABC", 6, false, "ABCX"},
	}
	var out []confCase
	for _, c := range cases {
		for _, restore := range []bool{false, true} {
			pre, post := "", ""
			if restore {
				pre, post = "\x1b7", "\x1b8"
			}
			base := fmt.Sprintf("resize pending wrap live and saved cursors (%s to %d cols, saved %v)", c.text, c.cols, restore)
			// Checkpoint 1: pending wrap after the resize.
			out = append(out, confCase{
				name: base, cols: 4, rows: 5,
				in:      c.text + pre + rz(c.cols, 6) + post,
				pending: &c.pending,
			})
			// Checkpoint 2: where the next character lands.
			out = append(out, confCase{
				name: base + "#2", cols: 4, rows: 5,
				in:     c.text + pre + rz(c.cols, 6) + post + "X",
				screen: scr(c.want),
			})
		}
	}
	return out
}

const (
	g3gapLR    = "DECLRMM left/right margins"
	g3gapProt  = "protected cells (DECSCA/SPA, DECSED/DECSEL)"
	g3gapCOLM  = "DECCOLM 132-column mode"
	g3gapCS    = "national/other charsets"
	g3gapOSC   = "hyperlinks / semantic prompts / OSC state"
	g3gapPal   = "palette and dynamic colors"
	g3gapRep   = "device reports / replies"
	g3gapImg   = "images (sixel/kitty)"
	g3red      = "\x1b[48;2;255;0;0m"
	g3redFg7f  = "\x1b[38;2;255;0;127m"
	g3redBg7f  = "\x1b[48;2;255;0;127m"
	g3abcDefGh = "ABC\r\nDEF\r\nGHI"
)

var ghostty3Cases = append([]confCase{
	// ---- Terminal.zig: insert mode ----
	{name: "insert mode does nothing at the end of the line", cols: 5, rows: 2,
		in: "hello\x1b[4hX", screen: scr("hello\nX")},
	{name: "insert mode with wide characters", cols: 5, rows: 2,
		in: "hello\x1b[1;2H\x1b[4h😀", screen: scr("h😀el")},
	{name: "insert mode with wide characters at end", cols: 5, rows: 2,
		in: "well\x1b[4h😀", screen: scr("well\n😀")},
	{name: "insert mode pushing off wide character", cols: 5, rows: 2,
		in: "123😀\x1b[4h\x1b[1;1HX", screen: scr("X123")},

	// ---- deleteChars (DCH) ----
	{name: "deleteChars", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H\x1b[2P", screen: scr("ADE")},
	// "deleteChars zero count" calls the API with 0, which has no byte
	// form (CSI 0 P means 1); not translated.
	{name: "deleteChars more than half", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H\x1b[3P", screen: scr("AE")},
	{name: "deleteChars more than line width", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H\x1b[10P", screen: scr("A")},
	{name: "deleteChars should shift left", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H\x1b[1P", screen: scr("ACDE")},
	{name: "deleteChars resets pending wrap", cols: 5, rows: 5,
		in: "ABCDE", pending: yes()},
	{name: "deleteChars resets pending wrap#2", cols: 5, rows: 5,
		in: "ABCDE\x1b[1P", pending: no()},
	{name: "deleteChars resets pending wrap#3", cols: 5, rows: 5,
		in: "ABCDE\x1b[1PX", screen: scr("ABCDX")},
	{name: "deleteChars resets wrap", cols: 5, rows: 5,
		in: "ABCDE123\x1b[1;1H\x1b[1PX", screen: scr("XCDE\n123")},
	{name: "deleteChars simple operation", cols: 10, rows: 10,
		in: "ABC123\x1b[1;3H\x1b[2P", screen: scr("AB23")},
	{name: "deleteChars preserves background sgr", cols: 10, rows: 10,
		in: "ABC123\x1b[1;3H" + g3red + "\x1b[2P", screen: scr("AB23"),
		check: g3bg(8, 10, 0, rgbColor(0xff, 0, 0))},
	{name: "deleteChars outside scroll region", cols: 6, rows: 10,
		in: "ABC123\x1b[?69h\x1b[3;5s\x1b[2P", screen: scr("ABC123"), gap: g3gapLR},
	{name: "deleteChars inside scroll region", cols: 6, rows: 10,
		in: "ABC123\x1b[?69h\x1b[3;5s\x1b[1;4H\x1b[1P", screen: scr("ABC2 3"), gap: g3gapLR},
	{name: "deleteChars split wide character from spacer tail", cols: 6, rows: 10,
		in: "A橋123\x1b[1;3H\x1b[1P", screen: scr("A 123")},
	{name: "deleteChars split wide character from wide", cols: 6, rows: 10,
		in:    "橋123\x1b[1;1H\x1b[1P",
		check: g3all(g3rune(0, 0, 0), g3rune(1, 0, '1'))},
	{name: "deleteChars split wide character from end", cols: 6, rows: 10,
		in:    "A橋123\x1b[1;1H\x1b[1P",
		check: g3all(g3rune(0, 0, 0x6A4B), g3rune(1, 0, wideTail))},
	{name: "deleteChars with a spacer head at the end", cols: 5, rows: 10,
		in:    "0123橋123\x1b[1;1H\x1b[1P",
		check: g3rune(3, 0, 0)},
	{name: "deleteChars split wide character tail", cols: 5, rows: 5,
		in: "\x1b[1;4H橋\r\x1b[4P0", screen: scr("0")},
	{name: "deleteChars wide char boundary conditions", cols: 8, rows: 1,
		in: "😀a😀b😀", screen: scr("😀a😀b😀")},
	{name: "deleteChars wide char boundary conditions#2", cols: 8, rows: 1,
		in: "😀a😀b😀\x1b[1;2H\x1b[3P", screen: scr("  b😀")},
	{name: "deleteChars wide char wrap boundary conditions", cols: 8, rows: 3,
		in: ".......😀abcde😀......", screen: scr(".......\n😀abcde\n😀......")},
	{name: "deleteChars wide char wrap boundary conditions#2", cols: 8, rows: 3,
		in: ".......😀abcde😀......\x1b[2;2H\x1b[3P", screen: scr(".......\n cde\n😀......")},
	{name: "deleteChars wide char across right margin", cols: 8, rows: 3,
		in: "123456橋\x1b[?69h\x1b[2;7s\x1b[1;2H\x1b[1P", screen: scr("13456"), gap: g3gapLR},

	// ---- saveCursor / restoreCursor ----
	// Upstream also saves GR=G3; only the pen and origin mode are translated.
	{name: "saveCursor", cols: 3, rows: 3,
		in: "\x1b[1m\x1b[?6h\x1b7\x1b[0m\x1b[?6l\x1b8",
		check: g3all(
			g3is("pen not bold after restore", func(t *Terminal) bool { return t.cur.pen.A&attrBold != 0 }),
			g3is("origin mode not restored", func(t *Terminal) bool { return t.cur.origin }))},
	{name: "saveCursor position", cols: 10, rows: 5,
		in: "\x1b[1;5HA\x1b7\x1b[1;1HB\x1b8X", screen: scr("B   AX")},
	{name: "saveCursor pending wrap state", cols: 5, rows: 5,
		in: "\x1b[1;5HA\x1b7\x1b[1;1HB\x1b8X", screen: scr("B   A\nX")},
	{name: "saveCursor origin mode", cols: 10, rows: 5,
		in: "\x1b[?6h\x1b7\x1b[?69h\x1b[3;5s\x1b[2;4r\x1b8X", screen: scr("X"), gap: g3gapLR},
	{name: "saveCursor resize", cols: 10, rows: 5,
		in: "\x1b[1;10H\x1b7" + rz(5, 5) + "\x1b8X", screen: scr("    X")},
	{name: "saveCursor protected pen", cols: 10, rows: 5,
		in: "\x1bV\x1b[1;10H\x1b7\x1bW\x1b8", gap: g3gapProt},
	{name: "setProtectedMode", cols: 3, rows: 3,
		in: "\x1bV\x1b[1\"q\x1b[0\"q", gap: g3gapProt},

	// ---- eraseLine (EL) ----
	{name: "eraseLine simple erase right", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;3H\x1b[0K", screen: scr("AB")},
	{name: "eraseLine resets pending wrap", cols: 5, rows: 5,
		in: "ABCDE\x1b[0K", pending: no()},
	{name: "eraseLine resets pending wrap#2", cols: 5, rows: 5,
		in: "ABCDE\x1b[0KB", screen: scr("ABCDB")},
	{name: "eraseLine resets wrap", cols: 5, rows: 5,
		in: "ABCDE123\x1b[1;1H\x1b[0KX", screen: scr("X\n123")},
	{name: "eraseLine right preserves background sgr", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H" + g3red + "\x1b[0K", screen: scr("A"),
		check: g3bg(1, 5, 0, rgbColor(0xff, 0, 0))},
	{name: "eraseLine right wide character", cols: 10, rows: 5,
		in: "AB橋DE\x1b[1;4H\x1b[0K", screen: scr("AB")},
	{name: "eraseLine right protected attributes respected with iso", cols: 5, rows: 5,
		in: "\x1bVABC\x1b[1;1H\x1b[0K", screen: scr("ABC"), gap: g3gapProt},
	{name: "eraseLine right protected attributes ignored with dec most recent", cols: 5, rows: 5,
		in: "\x1bVABC\x1b[1\"q\x1b[0\"q\x1b[1;2H\x1b[0K", screen: scr("A"), gap: g3gapProt},
	{name: "eraseLine right protected attributes ignored with dec set", cols: 5, rows: 5,
		in: "\x1b[1\"qABC\x1b[1;2H\x1b[0K", screen: scr("A"), gap: g3gapProt},
	{name: "eraseLine right protected requested", cols: 10, rows: 5,
		in: "12345678\x1b[1;6H\x1b[1\"qX\x1b[1;4H\x1b[?0K", screen: scr("123  X"), gap: g3gapProt},
	{name: "eraseLine simple erase left", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;3H\x1b[1K", screen: scr("   DE")},
	{name: "eraseLine left resets wrap", cols: 5, rows: 5,
		in: "ABCDE\x1b[1K", pending: no()},
	{name: "eraseLine left resets wrap#2", cols: 5, rows: 5,
		in: "ABCDE\x1b[1KB", screen: scr("    B")},
	{name: "eraseLine left preserves background sgr", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H" + g3red + "\x1b[1K", screen: scr("  CDE"),
		check: g3bg(0, 2, 0, rgbColor(0xff, 0, 0))},
	{name: "eraseLine left wide character", cols: 10, rows: 5,
		in: "AB橋DE\x1b[1;3H\x1b[1K", screen: scr("    DE")},
	{name: "eraseLine left protected attributes respected with iso", cols: 5, rows: 5,
		in: "\x1bVABC\x1b[1;1H\x1b[1K", screen: scr("ABC"), gap: g3gapProt},
	{name: "eraseLine left protected attributes ignored with dec most recent", cols: 5, rows: 5,
		in: "\x1bVABC\x1b[1\"q\x1b[0\"q\x1b[1;2H\x1b[1K", screen: scr("  C"), gap: g3gapProt},
	{name: "eraseLine left protected attributes ignored with dec set", cols: 5, rows: 5,
		in: "\x1b[1\"qABC\x1b[1;2H\x1b[1K", screen: scr("  C"), gap: g3gapProt},
	{name: "eraseLine left protected requested", cols: 10, rows: 5,
		in: "123456789\x1b[1;6H\x1b[1\"qX\x1b[1;8H\x1b[?1K", screen: scr("     X  9"), gap: g3gapProt},
	{name: "eraseLine complete preserves background sgr", cols: 5, rows: 5,
		in: "ABCDE\x1b[1;2H" + g3red + "\x1b[2K", screen: scr(""),
		check: g3bg(0, 5, 0, rgbColor(0xff, 0, 0))},
	{name: "eraseLine complete resets wrap", cols: 5, rows: 5,
		in: "ABCDE123\x1b[1;1H\x1b[2KX" + rz(10, 5), screen: scr("X\n123")},
	{name: "eraseLine complete clears kitty placeholder flag", cols: 5, rows: 5,
		in: "\U0010EEEE\x1b[2K", gap: g3gapImg},
	{name: "eraseLine complete protected attributes respected with iso", cols: 5, rows: 5,
		in: "\x1bVABC\x1b[1;1H\x1b[2K", screen: scr("ABC"), gap: g3gapProt},
	{name: "eraseLine complete protected attributes ignored with dec most recent", cols: 5, rows: 5,
		in: "\x1bVABC\x1b[1\"q\x1b[0\"q\x1b[1;2H\x1b[2K", screen: scr(""), gap: g3gapProt},
	{name: "eraseLine complete protected attributes ignored with dec set", cols: 5, rows: 5,
		in: "\x1b[1\"qABC\x1b[1;2H\x1b[2K", screen: scr(""), gap: g3gapProt},
	{name: "eraseLine complete protected requested", cols: 10, rows: 5,
		in: "123456789\x1b[1;6H\x1b[1\"qX\x1b[1;8H\x1b[?2K", screen: scr("     X"), gap: g3gapProt},

	// ---- tabs ----
	{name: "tabClear single", cols: 30, rows: 5,
		in: "\t\x1b[0g\x1b[1;1H\t", cur: at(16, 0)},
	{name: "tabClear all", cols: 30, rows: 5,
		in: "\x1b[3g\x1b[1;1H\t", cur: at(29, 0)},

	// ---- printRepeat (REP) ----
	{name: "printRepeat simple", cols: 5, rows: 5,
		in: "A\x1b[1b", screen: scr("AA")},
	{name: "printRepeat wrap", cols: 5, rows: 5,
		in: "    A\x1b[1b", screen: scr("    A\nA")},
	{name: "printRepeat no previous character", cols: 5, rows: 5,
		in: "\x1b[1b", screen: scr("")},

	// ---- printSlice ----
	{name: "printSlice simple ascii", cols: 10, rows: 3,
		in: "hello", cur: at(5, 0), screen: scr("hello")},
	// Upstream also enables mode 2027; it does not affect these cells.
	{name: "printSlice charset batched fill", cols: 5, rows: 2,
		in: "\x1b)0\x0e\x1b[41m\x1b[2J" +
			"\x1b[1;1H\x1b[0mlqqqkmqqqj" +
			"\x1b[1;1H\x1b[0mlqqqkmqqqj" +
			"\x1b[1;1H\x1b[1mlqqqkmqqqj" +
			"\x1b[1;1H\x1b[1mlqqqkmqqqj" +
			"\x1b[1;1H\x1b[0mlqqqkmqqqj",
		screen: scr("┌───┐\n└───┘"), pending: yes(),
		check: func(t *Terminal) string {
			for y := 0; y < 2; y++ {
				for x := 0; x < 5; x++ {
					if msg := g3cell(x, y, 0, 0, 0)(t); msg != "" {
						return msg
					}
				}
			}
			return ""
		}},
	{name: "printSlice charset batched fill (bold pass)", cols: 5, rows: 2,
		in: "\x1b)0\x0e\x1b[41m\x1b[2J" +
			"\x1b[1;1H\x1b[0mlqqqkmqqqj" +
			"\x1b[1;1H\x1b[1mlqqqkmqqqj",
		screen: scr("┌───┐\n└───┘"), pending: yes(),
		check: func(t *Terminal) string {
			for y := 0; y < 2; y++ {
				for x := 0; x < 5; x++ {
					if msg := g3cell(x, y, 0, 0, attrBold)(t); msg != "" {
						return msg
					}
				}
			}
			return ""
		}},
	{name: "printSlice charset single shift and repeat", cols: 10, rows: 2,
		in: "\x1b(0\x1b*A\x1bN#q\x1b[2b\x1b(B\x1b[2b", screen: scr("£───qq"), gap: g3gapCS},
	{name: "printSlice wraps and scrolls", cols: 5, rows: 2,
		in: "abcdefghijkl", screen: scr("fghij\nkl"), cur: at(2, 1), pending: no()},
	{name: "printSlice pending wrap state", cols: 5, rows: 2,
		in: "abcde", cur: at(4, 0), pending: yes(), screen: scr("abcde")},

	// ---- printAttributes: the pen it reports, checked on a printed cell ----
	{name: "printAttributes", cols: 5, rows: 5,
		in: "\x1b[38;2;1;2;3mX", check: g3cell(0, 0, rgbColor(1, 2, 3), 0, 0)},
	{name: "printAttributes#2", cols: 5, rows: 5,
		in: "\x1b[1m\x1b[48;2;1;2;3mX", check: g3cell(0, 0, 0, rgbColor(1, 2, 3), attrBold)},
	{name: "printAttributes#3", cols: 5, rows: 5,
		in: "\x1b[1m\x1b[2m\x1b[3m\x1b[4m\x1b[5m\x1b[7m\x1b[8m\x1b[9m\x1b[53m\x1b[38;2;100;200;255m\x1b[48;2;101;102;103mX",
		check: g3cell(0, 0, rgbColor(100, 200, 255), rgbColor(101, 102, 103),
			attrBold|attrFaint|attrItalic|attrBlink|attrInverse|attrHidden|attrStrike|attrOverline|1<<ulShift)},
	{name: "printAttributes#4", cols: 5, rows: 5,
		in: "\x1b[4mX", check: g3cell(0, 0, 0, 0, 1<<ulShift)},
	{name: "printAttributes#5", cols: 5, rows: 5,
		in: "\x1b[4:2mX", check: g3cell(0, 0, 0, 0, 2<<ulShift)},
	{name: "printAttributes#6", cols: 5, rows: 5,
		in: "\x1b[4:3mX", check: g3cell(0, 0, 0, 0, 3<<ulShift)},
	{name: "printAttributes#7", cols: 5, rows: 5,
		in: "\x1b[4:4mX", check: g3cell(0, 0, 0, 0, 4<<ulShift)},
	{name: "printAttributes#8", cols: 5, rows: 5,
		in: "\x1b[4:5mX", check: g3cell(0, 0, 0, 0, 5<<ulShift)},
	{name: "printAttributes#9", cols: 5, rows: 5,
		in: "\x1b[1;4:5;38;2;1;2;3m\x1b[0mX", check: g3cell(0, 0, 0, 0, 0)},

	// ---- eraseDisplay (ED) ----
	{name: "eraseDisplay simple erase below", cols: 5, rows: 5,
		in: g3abcDefGh + "\x1b[2;2H\x1b[0J", screen: scr("ABC\nD")},
	{name: "eraseDisplay erase below preserves SGR bg", cols: 5, rows: 5,
		in: g3abcDefGh + "\x1b[2;2H" + g3red + "\x1b[0J", screen: scr("ABC\nD"),
		check: g3bg(1, 5, 1, rgbColor(0xff, 0, 0))},
	{name: "eraseDisplay below split multi-cell", cols: 5, rows: 5,
		in: "AB橋C\r\nDE橋F\r\nGH橋I\x1b[2;4H\x1b[0J", screen: scr("AB橋C\nDE")},
	{name: "eraseDisplay below protected attributes respected with iso", cols: 5, rows: 5,
		in: "\x1bV" + g3abcDefGh + "\x1b[2;2H\x1b[0J", screen: scr("ABC\nDEF\nGHI"), gap: g3gapProt},
	{name: "eraseDisplay below protected attributes ignored with dec most recent", cols: 5, rows: 5,
		in: "\x1bV" + g3abcDefGh + "\x1b[1\"q\x1b[0\"q\x1b[2;2H\x1b[0J", screen: scr("ABC\nD"), gap: g3gapProt},
	{name: "eraseDisplay below protected attributes ignored with dec set", cols: 5, rows: 5,
		in: "\x1b[1\"q" + g3abcDefGh + "\x1b[2;2H\x1b[0J", screen: scr("ABC\nD"), gap: g3gapProt},
	{name: "eraseDisplay below protected attributes respected with force", cols: 5, rows: 5,
		in: "\x1b[1\"q" + g3abcDefGh + "\x1b[2;2H\x1b[?0J", screen: scr("ABC\nDEF\nGHI"), gap: g3gapProt},
	{name: "eraseDisplay simple erase above", cols: 5, rows: 5,
		in: g3abcDefGh + "\x1b[2;2H\x1b[1J", screen: scr("\n  F\nGHI")},
	{name: "eraseDisplay erase above preserves SGR bg", cols: 5, rows: 5,
		in: g3abcDefGh + "\x1b[2;2H" + g3red + "\x1b[1J", screen: scr("\n  F\nGHI"),
		check: g3bg(0, 2, 1, rgbColor(0xff, 0, 0))},
	{name: "eraseDisplay above split multi-cell", cols: 5, rows: 5,
		in: "AB橋C\r\nDE橋F\r\nGH橋I\x1b[2;3H\x1b[1J", screen: scr("\n    F\nGH橋I")},
	{name: "eraseDisplay above protected attributes respected with iso", cols: 5, rows: 5,
		in: "\x1bV" + g3abcDefGh + "\x1b[2;2H\x1b[1J", screen: scr("ABC\nDEF\nGHI"), gap: g3gapProt},
	{name: "eraseDisplay above protected attributes ignored with dec most recent", cols: 5, rows: 5,
		in: "\x1bV" + g3abcDefGh + "\x1b[1\"q\x1b[0\"q\x1b[2;2H\x1b[1J", screen: scr("\n  F\nGHI"), gap: g3gapProt},
	{name: "eraseDisplay above protected attributes ignored with dec set", cols: 5, rows: 5,
		in: "\x1b[1\"q" + g3abcDefGh + "\x1b[2;2H\x1b[1J", screen: scr("\n  F\nGHI"), gap: g3gapProt},
	{name: "eraseDisplay above protected attributes respected with force", cols: 5, rows: 5,
		in: "\x1b[1\"q" + g3abcDefGh + "\x1b[2;2H\x1b[?1J", screen: scr("ABC\nDEF\nGHI"), gap: g3gapProt},
	{name: "eraseDisplay protected complete", cols: 10, rows: 5,
		in: "A\r\n123456789\x1b[2;6H\x1b[1\"qX\x1b[2;4H\x1b[?2J", screen: scr("\n     X"), gap: g3gapProt},
	{name: "eraseDisplay protected below", cols: 10, rows: 5,
		in: "A\r\n123456789\x1b[2;6H\x1b[1\"qX\x1b[2;4H\x1b[?0J", screen: scr("A\n123  X"), gap: g3gapProt},
	// .scroll_complete is CSI 22 J (kitty's "scroll screen into scrollback, then clear").
	{name: "eraseDisplay scroll complete", cols: 10, rows: 5,
		in: "A\r\n\x1b[22J", screen: scr("")},
	{name: "eraseDisplay protected above", cols: 10, rows: 3,
		in: "A\r\n123456789\x1b[2;6H\x1b[1\"qX\x1b[2;8H\x1b[?1J", screen: scr("\n     X  9"), gap: g3gapProt},
	// Upstream asserts the cursor style survives ED 2; checked by printing.
	{name: "eraseDisplay complete preserves cursor", cols: 5, rows: 5,
		in: "\x1b[1mAAAA\x1b[2JX", check: g3all(g3rune(4, 0, 'X'), g3cell(4, 0, 0, 0, attrBold))},

	// ---- fullReset (RIS) ----
	{name: "fullReset with a non-empty pen", cols: 80, rows: 80,
		in: g3redFg7f + g3redBg7f + "\x1bc",
		check: g3all(g3cell(0, 0, 0, 0, 0),
			g3is("pen not default after RIS", func(t *Terminal) bool { return t.cur.pen == Cell{} }))},
	{name: "fullReset with a non-empty saved cursor", cols: 80, rows: 80,
		in: g3redFg7f + g3redBg7f + "\x1b7\x1bc",
		check: g3all(g3cell(0, 0, 0, 0, 0),
			g3is("pen not default after RIS", func(t *Terminal) bool { return t.cur.pen == Cell{} }))},
	{name: "fullReset origin mode", cols: 10, rows: 10,
		in: "\x1b[3;5H\x1b[?6h\x1bc", cur: at(0, 0),
		check: g3is("origin mode still set after RIS", func(t *Terminal) bool { return !t.cur.origin })},

	// ---- softReset (DECSTR) ----
	// Upstream sets keypad_keys (DECNKM); ESC = sets the same keypad state.
	// reverse_wrap (?45) is set but its reset is not checked here.
	{name: "softReset modes", cols: 10, rows: 10,
		in: "\x1b[4h\x1b[?6h\x1b[?7l\x1b[?45h\x1b[?25l\x1b[?1h\x1b=\x1b[?2004h\x1b[!p",
		check: g3all(
			g3is("insert still set", func(t *Terminal) bool { return !t.insert }),
			g3is("origin still set", func(t *Terminal) bool { return !t.cur.origin }),
			g3is("wraparound not set", func(t *Terminal) bool { return t.autowrap }),
			g3is("cursor not visible", func(t *Terminal) bool { return !t.cursorHidden }),
			g3is("cursor keys still set", func(t *Terminal) bool { return !t.appCursor }),
			g3is("keypad keys still set", func(t *Terminal) bool { return !t.appKeypad }),
			g3is("bracketed paste was reset", func(t *Terminal) bool { return t.bracketed }))},
	{name: "softReset margins", cols: 10, rows: 10,
		in:    "\x1b[3;4r\x1b[!p",
		check: g3is("scroll region not reset", func(t *Terminal) bool { return t.top == 0 && t.bot == 9 })},
	{name: "softReset margins#2", cols: 10, rows: 10,
		in: "\x1b[3;4r\x1b[?69h\x1b[5;6s\x1b[!p", gap: g3gapLR},
	{name: "softReset keeps the cursor and screen", cols: 10, rows: 10,
		in: "hello\x1b[6;5H\x1b[!p", cur: at(4, 5), screen: scr("hello")},
	{name: "softReset saved cursor", cols: 10, rows: 10,
		in: "\x1b[6;5H\x1b[1m\x1b7\x1b[!p\x1b8", cur: at(0, 0),
		check: g3is("pen still bold", func(t *Terminal) bool { return t.cur.pen.A&attrBold == 0 })},
	{name: "softReset pen, protection, and charsets", cols: 10, rows: 10,
		in: "\x1b[1m\x1b[1\"q\x1b(0\x1b[!p",
		check: g3all(
			g3is("pen not default", func(t *Terminal) bool { return t.cur.pen == Cell{} }),
			g3is("G0 still DEC special graphics", func(t *Terminal) bool { return !t.cur.g[0] }))},
	{name: "softReset ends ISO protection for erase", cols: 10, rows: 10,
		in: "\x1bVA\x1bW\x1b[!p\x1b[2J", screen: scr(""), gap: g3gapProt},
	{name: "softReset palette", cols: 10, rows: 10,
		in: "\x1b]4;1;rgb:ff/00/00\x1b\\\x1b]4;200;rgb:ff/00/00\x1b\\\x1b[!p", gap: g3gapPal},

	// ---- resize ----
	{name: "resize less cols with wide char then print", cols: 3, rows: 3,
		in: "x😀" + rz(2, 3) + "\x1b[1;2H😀"},
	{name: "resize less cols without reflow cutting wide char tail", cols: 3, rows: 1,
		in: "a一\x1b[?7l" + rz(2, 1), check: g3rune(1, 0, 0)},
	{name: "resize with left and right margin set", cols: 70, rows: 23,
		in: "\x1b[?69h0\x1b[?40h" + rz(70, 23) + "\x1b[2;0s\x1b[1850b" + rz(70, 23), gap: g3gapLR},
	{name: "resize with wraparound off", cols: 4, rows: 2,
		in: "\x1b[?7l0123" + rz(2, 2), screen: scr("01")},
	{name: "resize with wraparound on", cols: 4, rows: 2,
		in: "\x1b[?7h0123" + rz(2, 2), screen: scr("01\n23")},
	{name: "resize with high unique style per cell", cols: 30, rows: 30,
		in: g3uniqueStyles(false)},
	{name: "resize with high unique style per cell with wrapping", cols: 30, rows: 30,
		in: g3uniqueStyles(true)},
	{name: "resize with reflow and saved cursor", cols: 2, rows: 3,
		in: "1A2B\x1b[2;2H", screen: scr("1A\n2B"), check: g3curRune('B')},
	{name: "resize with reflow and saved cursor#2", cols: 2, rows: 3,
		in: "1A2B\x1b[2;2H\x1b7" + rz(5, 3) + "\x1b8", screen: scr("1A2B"), check: g3curRune('B')},
	{name: "resize with reflow and saved cursor pending wrap", cols: 2, rows: 3,
		in: "1A2B", screen: scr("1A\n2B"), check: g3curRune('B')},
	{name: "resize with reflow and saved cursor pending wrap#2", cols: 2, rows: 3,
		in: "1A2B\x1b7" + rz(5, 3) + "\x1b8", screen: scr("1A2B")},
	{name: "resize with reflow and saved cursor pending wrap#3", cols: 2, rows: 3,
		in: "1A2B\x1b7" + rz(5, 3) + "\x1b8X", screen: scr("1A2BX")},
	{name: "saved cursor survives repeated widening", cols: 4, rows: 5,
		in: "abc\r\nAAA|\x1b7" + rz(5, 5) + rz(6, 5) + "\x1b8X", screen: scr("abc\nAAA|X")},

	// ---- DECCOLM ----
	{name: "DECCOLM without DEC mode 40", cols: 5, rows: 5, in: "\x1b[?3h", gap: g3gapCOLM},
	{name: "DECCOLM unset", cols: 5, rows: 5, in: "\x1b[?40h\x1b[?3l", gap: g3gapCOLM},
	{name: "DECCOLM resets pending wrap", cols: 5, rows: 5, in: "ABCDE\x1b[?40h\x1b[?3l", gap: g3gapCOLM},
	{name: "DECCOLM preserves SGR bg", cols: 5, rows: 5, in: g3red + "\x1b[?40h\x1b[?3l", gap: g3gapCOLM},
	{name: "DECCOLM resets scroll region", cols: 5, rows: 5,
		in: "\x1b[?69h\x1b[2;3r\x1b[3;5s\x1b[?40h\x1b[?3l", gap: g3gapCOLM},

	// ---- alternate screen modes ----
	{name: "mode 47 alt screen plain", cols: 5, rows: 5,
		in: "1A\x1b[?47h", alt: yes(), screen: scr("")},
	{name: "mode 47 alt screen plain#2", cols: 5, rows: 5,
		in: "1A\x1b[?47h2B", alt: yes(), screen: scr("  2B")},
	{name: "mode 47 alt screen plain#3", cols: 5, rows: 5,
		in: "1A\x1b[?47h2B\x1b[?47l", alt: no(), screen: scr("1A")},
	{name: "mode 47 alt screen plain#4", cols: 5, rows: 5,
		in: "1A\x1b[?47h2B\x1b[?47l\x1b[?47h", alt: yes(), screen: scr("  2B")},
	{name: "mode 47 copies cursor both directions", cols: 5, rows: 5,
		in: g3redFg7f + "\x1b[?47h", alt: yes(),
		check: g3is("pen lost its style entering alt", func(t *Terminal) bool { return t.cur.pen.Fg != 0 })},
	{name: "mode 47 copies cursor both directions#2", cols: 5, rows: 5,
		in: g3redFg7f + "\x1b[?47h\x1b[38;2;0;255;0m\x1b[?47l", alt: no(),
		check: g3is("pen lost its style leaving alt", func(t *Terminal) bool { return t.cur.pen.Fg != 0 })},
	{name: "mode 1047 alt screen plain", cols: 5, rows: 5,
		in: "1A\x1b[?1047h", alt: yes(), screen: scr("")},
	{name: "mode 1047 alt screen plain#2", cols: 5, rows: 5,
		in: "1A\x1b[?1047h2B", alt: yes(), screen: scr("  2B")},
	{name: "mode 1047 alt screen plain#3", cols: 5, rows: 5,
		in: "1A\x1b[?1047h2B\x1b[?1047l", alt: no(), screen: scr("1A")},
	{name: "mode 1047 alt screen plain#4", cols: 5, rows: 5,
		in: "1A\x1b[?1047h2B\x1b[?1047l\x1b[?1047h", alt: yes(), screen: scr("")},
	{name: "mode 1047 copies cursor both directions", cols: 5, rows: 5,
		in: g3redFg7f + "\x1b[?1047h", alt: yes(),
		check: g3is("pen lost its style entering alt", func(t *Terminal) bool { return t.cur.pen.Fg != 0 })},
	{name: "mode 1047 copies cursor both directions#2", cols: 5, rows: 5,
		in: g3redFg7f + "\x1b[?1047h\x1b[38;2;0;255;0m\x1b[?1047l", alt: no(),
		check: g3is("pen lost its style leaving alt", func(t *Terminal) bool { return t.cur.pen.Fg != 0 })},
	{name: "mode 1049 alt screen plain", cols: 5, rows: 5,
		in: "1A\x1b[?1049h", alt: yes(), screen: scr("")},
	{name: "mode 1049 alt screen plain#2", cols: 5, rows: 5,
		in: "1A\x1b[?1049h2B", alt: yes(), screen: scr("  2B")},
	{name: "mode 1049 alt screen plain#3", cols: 5, rows: 5,
		in: "1A\x1b[?1049h2B\x1b[?1049l", alt: no(), screen: scr("1A")},
	{name: "mode 1049 alt screen plain#4", cols: 5, rows: 5,
		in: "1A\x1b[?1049h2B\x1b[?1049lC", alt: no(), screen: scr("1AC")},
	{name: "mode 1049 alt screen plain#5", cols: 5, rows: 5,
		in: "1A\x1b[?1049h2B\x1b[?1049lC\x1b[?1049h", alt: yes(), screen: scr("")},

	{name: "deleteLines wide char at right margin with full clear", cols: 80, rows: 24,
		in: "\x1b[10;39H中\x1b[?69h\x1b[5;39s\x1b[24S", gap: g3gapLR},
	// Upstream marks row 1 as a prompt first (not expressible as bytes
	// beyond OSC 133); the observable fact is that ED 2 with no prompt on
	// screen pushes nothing into the scrollback.
	{name: "eraseDisplay complete ignores stale prompt on recycled row", cols: 10, rows: 3,
		in: "hello\x1b[2;3r\x1b[3;1H\n\x1b[r\x1b[2J", scrollback: []string{}},
	{name: "eraseDisplay complete at prompt not on last row scrolls", cols: 10, rows: 15,
		in: strings.Repeat("x\r\n", 10) + "\x1b]133;A\x07$ \x1b]133;B\x07\x1b[1;1H\x1b[2J", gap: g3gapOSC},

	// ---- stream_terminal.zig byte-stream tests ----
	{name: "stream: basic print", cols: 10, rows: 10,
		in: "Hello", cur: at(5, 0), screen: scr("Hello")},
	{name: "stream: semantic failure is sticky while processing continues", cols: 10, rows: 2,
		in: "\x1b]2;unavailable\x1b\\ignored\x1bcOK", screen: scr("OK")},
	{name: "stream: cursor movement", cols: 10, rows: 10,
		in: "Hello\x1b[1;1H", cur: at(0, 0)},
	{name: "stream: cursor movement#2", cols: 10, rows: 10,
		in: "Hello\x1b[1;1H\x1b[2;3H", cur: at(2, 1)},
	{name: "stream: erase operations", cols: 20, rows: 10,
		in: "Hello World", cur: at(11, 0)},
	{name: "stream: erase operations#2", cols: 20, rows: 10,
		in: "Hello World\x1b[1;6H\x1b[K", screen: scr("Hello")},
	{name: "stream: tabs", cols: 80, rows: 10,
		in: "A\tB", cur: at(9, 0), screen: scr("A       B")},
	{name: "stream: modes", cols: 80, rows: 24,
		in: "\x1b[?7l", check: g3is("wraparound still set", func(t *Terminal) bool { return !t.autowrap })},
	{name: "stream: modes#2", cols: 80, rows: 24,
		in: "\x1b[?7l\x1b[?7h", check: g3is("wraparound not set", func(t *Terminal) bool { return t.autowrap })},
	{name: "stream: scrolling regions", cols: 80, rows: 24,
		in:    "\x1b[5;20r",
		check: g3is("scroll region not 4..19", func(t *Terminal) bool { return t.top == 4 && t.bot == 19 })},
	{name: "stream: charsets", cols: 80, rows: 24,
		in: "\x1b(0`", screen: scr("◆")},
	{name: "stream: alt screen", cols: 10, rows: 5,
		in: "Primary\x1b[?1049h", alt: yes()},
	{name: "stream: alt screen#2", cols: 10, rows: 5,
		in: "Primary\x1b[?1049hAlt\x1b[?1049l", alt: no(), screen: scr("Primary")},
	{name: "stream: cursor save and restore", cols: 80, rows: 24,
		in: "\x1b[10;15H", cur: at(14, 9)},
	{name: "stream: cursor save and restore#2", cols: 80, rows: 24,
		in: "\x1b[10;15H\x1b7\x1b[1;1H", cur: at(0, 0)},
	{name: "stream: cursor save and restore#3", cols: 80, rows: 24,
		in: "\x1b[10;15H\x1b7\x1b[1;1H\x1b8", cur: at(14, 9)},
	{name: "stream: attributes", cols: 80, rows: 24,
		in: "\x1b[1mBold\x1b[0m", screen: scr("Bold")},
	{name: "stream: DECRQSS responses", cols: 80, rows: 24,
		in: "\x1b[1m\x1bP$qm\x1b\\\x1b[0;53m\x1bP$qm\x1b\\", gap: g3gapRep},
	{name: "stream: DECRQSS without write effect is ignored", cols: 80, rows: 24,
		in: "\x1bP$qm\x1b\\", gap: g3gapRep},
	{name: "stream: XTGETTCAP responses", cols: 80, rows: 24,
		in: "\x1bP+q536D756C78\x1b\\", gap: g3gapRep},
	{name: "stream: XTGETTCAP without write effect is ignored", cols: 80, rows: 24,
		in: "\x1bP+q536D756C78\x1b\\", gap: g3gapRep},
	{name: "stream: XTGETTCAP TN responses", cols: 80, rows: 24,
		in: "\x1bP+q544E\x1b\\", gap: g3gapRep},
	{name: "stream: DECALN screen alignment", cols: 10, rows: 3,
		in: "\x1b#8", screen: scr("EEEEEEEEEE\nEEEEEEEEEE\nEEEEEEEEEE"), cur: at(0, 0)},
	{name: "stream: full reset", cols: 80, rows: 24,
		in:  "Hello\x1b[10;20H\x1b[5;20r\x1b[?7l\x1b_25a1;r;cp=e0a0;AAAAAAAAAAAAAA==\x1b\\\x1bc",
		cur: at(0, 0),
		check: g3all(
			g3is("scroll region not reset", func(t *Terminal) bool { return t.top == 0 && t.bot == 23 }),
			g3is("wraparound not set", func(t *Terminal) bool { return t.autowrap }))},
	{name: "stream: ignores query actions", cols: 80, rows: 24,
		in: "\x1b[c\x1b[5n\x1b[6n\x1b]4;0;?\x1b\\\x1b]21;foreground=?\x1b\\\x1b]52;c;%%%invalid-base64%%%\x1b\\" +
			"\x1b_Ga=p,i=999\x1b\\\x1b_25a1;r;cp=41;%%%invalid%%%\x1b\\Test",
		screen: scr("Test")},

	// Palette / dynamic colors (OSC 4/104/10/11/12/110/111/112, kitty OSC 21).
	{name: "stream: OSC 4 set and reset palette", cols: 10, rows: 10,
		in: "\x1b]4;0;rgb:ff/00/00\x1b\\\x1b]104;0\x1b\\", gap: g3gapPal},
	{name: "stream: OSC 104 reset all palette colors", cols: 10, rows: 10,
		in: "\x1b]4;0;rgb:ff/00/00\x1b\\\x1b]4;1;rgb:00/ff/00\x1b\\\x1b]4;2;rgb:00/00/ff\x1b\\\x1b]104\x1b\\", gap: g3gapPal},
	{name: "stream: OSC 10 set and reset foreground color", cols: 10, rows: 10,
		in: "\x1b]10;rgb:ff/00/00\x1b\\\x1b]110\x1b\\", gap: g3gapPal},
	{name: "stream: OSC 11 set and reset background color", cols: 10, rows: 10,
		in: "\x1b]11;rgb:00/ff/00\x1b\\\x1b]111\x1b\\", gap: g3gapPal},
	{name: "stream: OSC 12 set and reset cursor color", cols: 10, rows: 10,
		in: "\x1b]12;rgb:00/00/ff\x1b\\\x1b]112\x1b\\", gap: g3gapPal},
	{name: "stream: OSC color query responses", cols: 10, rows: 10,
		in: "\x1b]4;0;?\x1b\\\x1b]10;?\x1b\\\x1b]11;?\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol set palette", cols: 10, rows: 10,
		in: "\x1b]21;1=rgb:00/ff/00\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol reset palette", cols: 10, rows: 10,
		in: "\x1b]21;1=rgb:00/ff/00\x1b\\\x1b]21;1=\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol set foreground", cols: 10, rows: 10,
		in: "\x1b]21;foreground=rgb:ff/00/00\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol set background", cols: 10, rows: 10,
		in: "\x1b]21;background=rgb:00/ff/00\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol set cursor", cols: 10, rows: 10,
		in: "\x1b]21;cursor=rgb:00/00/ff\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol reset foreground", cols: 10, rows: 10,
		in: "\x1b]21;foreground=rgb:ff/00/00\x1b\\\x1b]21;foreground=\x1b\\", gap: g3gapPal},
	{name: "stream: kitty color protocol query responses", cols: 10, rows: 10,
		in: "\x1b]21;foreground=?;background=?\x1b\\", gap: g3gapPal},
	{name: "stream: palette dirty flag set on color change", cols: 10, rows: 10,
		in: "\x1b]4;0;rgb:ff/00/00\x1b\\\x1b]104;0\x1b\\\x1b]21;1=rgb:00/ff/00\x1b\\", gap: g3gapPal},

	// Semantic prompts (OSC 133): fresh-line behavior moves the cursor.
	{name: "stream: semantic prompt fresh line", cols: 10, rows: 10,
		in: "Hello\x1b]133;L\x07", cur: at(0, 1), gap: g3gapOSC},
	{name: "stream: semantic prompt fresh line new prompt", cols: 10, rows: 10,
		in: "Hello\x1b]133;A\x07", cur: at(0, 1), gap: g3gapOSC},
	{name: "stream: semantic prompt prompt_start", cols: 10, rows: 10,
		in: "Hello\x1b]133;P\x07", cur: at(5, 0), gap: g3gapOSC},
	{name: "stream: semantic prompt new_command", cols: 10, rows: 10,
		in: "Hello\x1b]133;N\x07", cur: at(0, 1), gap: g3gapOSC},
	{name: "stream: semantic prompt new_command at column zero", cols: 10, rows: 10,
		in: "\x1b]133;N\x07", cur: at(0, 0), gap: g3gapOSC},

	// Effects with no callback leave the terminal usable.
	{name: "stream: bell effect callback", cols: 80, rows: 24,
		in: "\x07AfterBell", screen: scr("AfterBell")},
	{name: "stream: desktop_notification effect callback", cols: 80, rows: 24,
		in: "\x1b]9;Ignored\x1b\\AfterNotification", screen: scr("AfterNotification")},
	{name: "stream: clipboard_write effect callback", cols: 80, rows: 24,
		in: "\x1b]52;c;aGVsbG8=\x1b\\AfterClipboard", screen: scr("AfterClipboard")},
	{name: "stream: kitty clipboard malformed packets are silently dropped", cols: 80, rows: 24,
		in: "\x1b]5522;loc=primary\x1b\\\x1b]5522;type=bobr\x1b\\\x1b]5522;type=read:bare\x1b\\" +
			"\x1b]5522;type=wdata:mime=!!!;R2hvc3Q=\x1b\\\x1b]5522;type=wdata:mime=dGV4dC9wbGFpbg==;R2hvc3Q=\x1b\\" +
			"\x1b]5522;type=walias:mime=dGV4dC9wbGFpbg==;VEVYVA==\x1b\\ok",
		screen: scr("ok")},

	// DECRQM ANSI: the mode state part is checkable; the replies are not.
	{name: "stream: request mode DECRQM ANSI responses", cols: 80, rows: 24,
		in: "\x1b[4h", check: g3is("IRM not set", func(t *Terminal) bool { return t.insert })},
	{name: "stream: request mode DECRQM ANSI responses#2", cols: 80, rows: 24,
		in: "\x1b[4h\x1b[4l", check: g3is("IRM still set", func(t *Terminal) bool { return !t.insert })},
	{name: "stream: request mode DECRQM ANSI responses#3", cols: 80, rows: 24,
		in: "\x1b[20h", check: g3is("LNM not set", func(t *Terminal) bool { return t.newline })},
	{name: "stream: request mode DECRQM ANSI responses#4", cols: 80, rows: 24,
		in: "\x1b[20h\x1b[20l", check: g3is("LNM still set", func(t *Terminal) bool { return !t.newline })},
	{name: "stream: request mode DECRQM ANSI responses#5", cols: 80, rows: 24,
		in:    "\x1b[4h\x1b[?4l",
		check: g3is("DEC ?4 reset cleared ANSI IRM", func(t *Terminal) bool { return t.insert })},
	{name: "stream: request mode DECRQM ANSI responses#6", cols: 80, rows: 24,
		in: "\x1b[4h\x1b[4$p\x1b[?4$p\x1b[9999$p\x1b[1$p\x1b[19$p", gap: g3gapRep},

	{name: "stream: CSI W with intermediate but no params", cols: 80, rows: 24,
		in: "\x1b[?W"},

	// Window title (OSC 2).
	{name: "stream: window_title effect is called", cols: 80, rows: 24,
		in:    "\x1b]2;Hello World\x1b\\",
		check: func(t *Terminal) string { return g3title(t, "Hello World") }},
	{name: "stream: window_title effect not called without callback", cols: 80, rows: 24,
		in: "\x1b]2;Hello World\x1b\\Test", screen: scr("Test"),
		check: func(t *Terminal) string { return g3title(t, "Hello World") }},
	{name: "stream: window_title effect with empty title", cols: 80, rows: 24,
		in:    "\x1b]2;\x1b\\",
		check: func(t *Terminal) string { return g3title(t, "") }},
	{name: "stream: window_title not changed by cancelled OSC", cols: 80, rows: 24,
		in:    "\x1b]2;before\x07\x1b]2;can\x18",
		check: func(t *Terminal) string { return g3title(t, "before") }},
	{name: "stream: window_title not changed by cancelled OSC#2", cols: 80, rows: 24,
		in:    "\x1b]2;before\x07\x1b]2;can\x18\x1b]2;sub\x1a",
		check: func(t *Terminal) string { return g3title(t, "before") }},
	{name: "stream: window_title not changed by cancelled OSC#3", cols: 80, rows: 24,
		in:    "\x1b]2;before\x07\x1b]2;can\x18\x1b]2;sub\x1a\x1b]2;after\x1b\\",
		check: func(t *Terminal) string { return g3title(t, "after") }},

	{name: "stream: device status: readonly ignores all", cols: 80, rows: 24,
		in: "\x1b[5n\x1b[6n\x1b[?996n\x1b[?998nTest", screen: scr("Test")},
	{name: "stream: device attributes: readonly ignores", cols: 80, rows: 24,
		in: "\x1b[c\x1b[>c\x1b[=cTest", screen: scr("Test")},
	{name: "stream: kitty dnd: state updates work without write_pty effect", cols: 80, rows: 24,
		in: "\x1b]72;t=q\x1b\\\x1b]72;t=a\x1b\\ok", screen: scr("ok")},
}, g3resizePendingWrap()...)

func g3title(t *Terminal, want string) string {
	if got := t.Title(); got != want {
		return fmt.Sprintf("title got %q want %q", got, want)
	}
	return ""
}
