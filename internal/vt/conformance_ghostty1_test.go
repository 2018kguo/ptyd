// Translated from ghostty (MIT), src/terminal/Terminal.zig at commit 521cf2c
// (tests "Terminal: ..." between lines 4296 and 9048).

package vt

import (
	"fmt"
	"strings"
)

func init() { registerSuite("ghostty", ghostty1Cases) }

const (
	g1Grid  = "ABC\r\nDEF\r\nGHI"
	g1Osc8  = "\x1b]8;;http://example.com\x1b\\"
	g1Osc8e = "\x1b]8;;\x1b\\"
	g1Fam   = "\U0001F468‍\U0001F469‍\U0001F467" // man ZWJ woman ZWJ girl
	g2027   = "\x1b[?2027h"
	gLRM    = "DECLRMM left/right margins"
	gGraph  = "grapheme clustering (mode 2027, VS15/VS16 width changes)"
	gOSC    = "hyperlinks / semantic prompts / OSC state"
)

// g1Cell checks the rune at x, y (0 for blank, wideTail for a right half).
func g1Cell(t *Terminal, x, y int, r rune) string {
	if got := t.baseRune(x, y); got != r {
		return fmt.Sprintf("cell %d,%d rune got %U want %U", x, y, got, r)
	}
	return ""
}

// g1Cells runs several g1Cell checks: triples of x, y, rune.
func g1Cells(t *Terminal, want ...[3]int) string {
	for _, w := range want {
		if msg := g1Cell(t, w[0], w[1], rune(w[2])); msg != "" {
			return msg
		}
	}
	return ""
}

const g1Tail = int(wideTail)

// g1DenseLinks prints A..J on the current row, each with its own OSC 8 URI.
func g1DenseLinks() string {
	var b strings.Builder
	for i := range 10 {
		fmt.Fprintf(&b, "\x1b]8;;http://example.com/%d\x1b\\%c\x1b]8;;\x1b\\", i, 'A'+i)
	}
	return b.String()
}

// setCursorPos (original test) checkpoints.
const (
	g1SCP3  = "\x1b[0;0H\x1b[81;81H"
	g1SCP4  = g1SCP3 + "\x1b[0;80Hc"
	g1SCP5  = g1SCP4 + "\x1b[0;80H"
	g1SCP6  = g1SCP5 + "\x1b[?6h\x1b[81;81H"
	g1SCP7  = g1SCP6 + "\x1b[10;80r\x1b[0;0H"
	g1SCP8  = g1SCP7 + "\x1b[1;1H"
	g1SCP9  = g1SCP8 + "\x1b[100;0H"
	g1SCP10 = g1SCP9 + "\x1b[10;11r\x1b[2;0H"
)

var ghostty1Cases = []confCase{
	// cursor positioning with origin + left/right margins
	{name: "setCursorPos saturates overflowing origin offsets", cols: 10, rows: 10,
		in: "\x1b[3;8r\x1b[?69h\x1b[4;9s\x1b[?6h\x1b[99999;99999H", cur: at(8, 7), gap: gLRM},
	{name: "setCursorRow and setCursorCol in origin mode", cols: 10, rows: 10,
		in: "\x1b[3;8r\x1b[?69h\x1b[4;9s\x1b[?6h\x1b[2;2H\x1b[1G", cur: at(3, 3), gap: gLRM},
	{name: "setCursorRow and setCursorCol in origin mode #2", cols: 10, rows: 10,
		in: "\x1b[3;8r\x1b[?69h\x1b[4;9s\x1b[?6h\x1b[2;2H\x1b[1G\x1b[1d", cur: at(3, 2), gap: gLRM},
	{name: "cursorRowRelative and cursorColRelative in origin mode", cols: 10, rows: 10,
		in: "\x1b[3;8r\x1b[?69h\x1b[4;9s\x1b[?6h\x1b[2;2H\x1b[2a\x1b[2e\x1b[100a\x1b[100e", cur: at(8, 7), gap: gLRM},
	{name: "cursorRowRelative and cursorColRelative stop at screen edge", cols: 10, rows: 10,
		in: "\x1b[3;4H\x1b[2a\x1b[1e", cur: at(5, 3)},
	{name: "cursorRowRelative and cursorColRelative stop at screen edge #2", cols: 10, rows: 10,
		in: "\x1b[3;4H\x1b[2a\x1b[1e\x1b[65535a\x1b[65535e", cur: at(9, 9)},

	// input
	{name: "input with no control characters", cols: 40, rows: 40,
		in: "hello", cur: at(5, 0), screen: scr("hello")},
	{name: "input with basic wraparound", cols: 5, rows: 40,
		in: "helloworldabc12", cur: at(4, 2), pending: yes(), screen: scr("hello\nworld\nabc12")},
	{name: "input that forces scroll", cols: 1, rows: 5,
		in: "abcdef", cur: at(0, 4), screen: scr("b\nc\nd\ne\nf")},
	{name: "zero-width character at start", cols: 80, rows: 80,
		in: "‍", cur: at(0, 0)},
	{name: "zero-width character attaches to pending wrap cell", cols: 2, rows: 2,
		in: "\x1b[?2027lxå̲", screen: scr("xå̲")},
	{name: "print single very long line", cols: 5, rows: 5,
		in: strings.Repeat("x", 1000)}, // upstream asserts only that it does not crash

	// wide characters
	{name: "print wide char", cols: 80, rows: 80,
		in: "\U0001F600", cur: at(2, 0),
		check: func(t *Terminal) string { return g1Cells(t, [3]int{0, 0, 0x1F600}, [3]int{1, 0, g1Tail}) }},
	{name: "print wide char at edge creates spacer head", cols: 10, rows: 10,
		in: "\x1b[1;10H\U0001F600", cur: at(2, 1),
		check: func(t *Terminal) string {
			return g1Cells(t, [3]int{9, 0, 0}, [3]int{0, 1, 0x1F600}, [3]int{1, 1, g1Tail})
		}},
	{name: "print wide char in single-width terminal", cols: 1, rows: 80,
		in: "\U0001F600", cur: at(0, 0), pending: yes(),
		check: func(t *Terminal) string { return g1Cell(t, 0, 0, 0) }},
	{name: "print over wide char at 0,0", cols: 80, rows: 80,
		in: "\U0001F600\x1b[0;0HA", cur: at(1, 0),
		check: func(t *Terminal) string { return g1Cells(t, [3]int{0, 0, 'A'}, [3]int{1, 0, 0}) }},
	{name: "print over wide char at col 0 corrupts previous row", cols: 10, rows: 3,
		in: strings.Repeat("中", 10) + "\x1b[2;1HA",
		check: func(t *Terminal) string {
			return g1Cells(t, [3]int{0, 1, 'A'}, [3]int{8, 0, 0x4e2d}, [3]int{9, 0, g1Tail})
		}},
	{name: "print over wide spacer tail", cols: 5, rows: 5,
		in: "橋\x1b[1;2HX", screen: scr(" X"),
		check: func(t *Terminal) string { return g1Cells(t, [3]int{0, 0, 0}, [3]int{1, 0, 'X'}) }},
	{name: "print over wide char with bold", cols: 80, rows: 80,
		in: "\x1b[1m\U0001F600\x1b[0;0H\x1b[0mA",
		check: func(t *Terminal) string {
			// upstream: the style map is empty, i.e. no cell keeps the bold style.
			for x := range 2 {
				if c := t.cellAt(x, 0); c.A != 0 {
					return fmt.Sprintf("cell %d,0 attrs %v, want none (bold dropped)", x, c.A)
				}
			}
			return g1Cells(t, [3]int{0, 0, 'A'}, [3]int{1, 0, 0})
		}},
	{name: "print over wide char with bg color", cols: 80, rows: 80,
		in: "\x1b[48;2;255;0;0m\U0001F600\x1b[0;0H\x1b[0mA",
		check: func(t *Terminal) string {
			for x := range 2 {
				if c := t.cellAt(x, 0); c.Bg != 0 {
					return fmt.Sprintf("cell %d,0 bg %#x, want default (bg style dropped)", x, c.Bg)
				}
			}
			return ""
		}},
	{name: "print multicodepoint grapheme, disabled mode 2027", cols: 80, rows: 80,
		in: g1Fam, cur: at(6, 0),
		check: func(t *Terminal) string {
			return g1Cells(t, [3]int{0, 0, 0x1F468}, [3]int{1, 0, g1Tail}, [3]int{2, 0, 0x1F469},
				[3]int{3, 0, g1Tail}, [3]int{4, 0, 0x1F467}, [3]int{5, 0, g1Tail})
		}},
	{name: "enabling grapheme mode handles stored breaks", cols: 5, rows: 1,
		in: "\x1b[?2027la​" + g2027 + "́", screen: scr("a​́"), gap: gGraph},
	{name: "graphemeWidth parity", cols: 80, rows: 5,
		in: g2027 + "❤️", cur: at(2, 0), gap: gGraph},
	{name: "VS16 doesn't make character with 2027 disabled", cols: 5, rows: 5,
		in: "\x1b[?2027l❤️", screen: scr("❤️"),
		check: func(t *Terminal) string { return g1Cell(t, 0, 0, 0x2764) }},
	{name: "print invalid VS16 non-grapheme", cols: 80, rows: 80,
		in: "x️", cur: at(1, 0),
		check: func(t *Terminal) string { return g1Cells(t, [3]int{0, 0, 'x'}, [3]int{1, 0, 0}) }},

	// mode 2027 grapheme clustering (out of scope)
	{name: "variation selectors apply to preceding codepoint", cols: 5, rows: 5,
		in: g2027 + "\U0001F3F4‍☠️", gap: gGraph},
	{name: "print multicodepoint grapheme, mode 2027", cols: 80, rows: 80,
		in: g2027 + g1Fam, cur: at(2, 0), gap: gGraph},
	{name: "keypad sequence VS15", cols: 80, rows: 80, in: g2027 + "#︎", cur: at(1, 0), gap: gGraph},
	{name: "keypad sequence VS16", cols: 80, rows: 80, in: g2027 + "#️", cur: at(2, 0), gap: gGraph},
	{name: "Fitzpatrick skin tone next valid base", cols: 80, rows: 80,
		in: g2027 + "\U0001F44B\U0001F3FF", cur: at(2, 0), gap: gGraph},
	{name: "Fitzpatrick skin tone next to non-base", cols: 80, rows: 80,
		in: g2027 + "\"\U0001F3FF\"", cur: at(4, 0), gap: gGraph},
	{name: "multicodepoint grapheme marks dirty on every codepoint", cols: 80, rows: 80,
		in: g2027 + g1Fam, cur: at(2, 0), gap: gGraph},
	{name: "VS15 to make narrow character", cols: 5, rows: 5,
		in: g2027 + "☔︎", cur: at(1, 0), screen: scr("☔︎"), gap: gGraph},
	{name: "VS15 on already narrow emoji", cols: 5, rows: 5,
		in: g2027 + "⛈︎", cur: at(1, 0), screen: scr("⛈︎"), gap: gGraph},
	{name: "print invalid VS15 following emoji is wide", cols: 80, rows: 80,
		in: g2027 + "\U0001F9E0︎", cur: at(2, 0), gap: gGraph},
	{name: "print invalid VS15 in emoji ZWJ sequence", cols: 80, rows: 80,
		in: g2027 + "\U0001F469︎‍\U0001F466", cur: at(2, 0), gap: gGraph},
	{name: "VS15 to make narrow character with pending wrap", cols: 4, rows: 5,
		in: g2027 + "\U0001F34B☔︎", cur: at(3, 0), pending: no(), gap: gGraph},
	{name: "VS15 narrows wide cell under cursor with wraparound disabled", cols: 5, rows: 5,
		in: g2027 + "\x1b[?7l\x1b[1;4H☔\x1b[?69h\x1b[1;4s\x1b[1;4H︎", cur: at(3, 0), gap: gGraph},
	{name: "VS15 narrows wide cell under restored pending cursor", cols: 5, rows: 5,
		in: g2027 + "\x1b[?69h\x1b[1;4s\x1b[1;4HX\x1b7\x1b[1;5s\x1b[1;4H☔\x1b8︎", cur: at(4, 0), gap: gGraph},
	{name: "VS16 to make wide character on next line", cols: 3, rows: 5,
		in: g2027 + "\x1b[2C#️", cur: at(2, 1), gap: gGraph},
	{name: "VS16 to make wide character on next line with hyperlink", cols: 3, rows: 5,
		in: g2027 + g1Osc8 + "\x1b[2C#️", gap: gGraph},
	{name: "VS16 widening when the spacer tail grows the page", cols: 20, rows: 10,
		in: g2027 + g1Osc8 + "#️", gap: gGraph},
	{name: "grapheme transfer when widening wraps to the next line", cols: 3, rows: 5,
		in: g2027 + "\x1b[2C☺‍❤", gap: gGraph},
	{name: "grapheme transfer when widening wraps below the scroll region", cols: 3, rows: 3,
		in: g2027 + "\x1b[1;2r\x1b[3;3H❤‍\U0001F44D", gap: gGraph},
	{name: "grapheme transfer when widening scrolls a single row", cols: 5, rows: 1,
		in: g2027 + "\x1b[1;5Há\x1bD", gap: gGraph},
	{name: "grapheme transfer when widening wraps to a page without grapheme capacity", cols: 80, rows: 24,
		in: g2027 + "\x1b[24;80H☺‍❤", gap: gGraph},
	{name: "VS16 to make wide character with pending wrap", cols: 3, rows: 5,
		in: g2027 + "\x1b[1C#️", gap: gGraph},
	{name: "VS16 to make wide character with mode 2027", cols: 5, rows: 5,
		in: g2027 + "❤️", cur: at(2, 0), gap: gGraph},
	{name: "VS16 repeated with mode 2027", cols: 5, rows: 5,
		in: g2027 + "❤️❤️", cur: at(4, 0), gap: gGraph},
	{name: "print invalid VS16 grapheme", cols: 80, rows: 80, in: g2027 + "x️", cur: at(1, 0), gap: gGraph},
	{name: "print invalid VS16 with second char", cols: 80, rows: 80, in: g2027 + "x️y", cur: at(2, 0), gap: gGraph},
	{name: "print grapheme o with nonspacing mark should be narrow", cols: 5, rows: 5,
		in: g2027 + "ò", cur: at(1, 0), gap: gGraph},
	{name: "print Devanagari grapheme should be wide", cols: 5, rows: 5,
		in: g2027 + "क्‍ष", cur: at(2, 0), gap: gGraph},
	{name: "print Devanagari grapheme should be wide on next line", cols: 3, rows: 5,
		in: g2027 + "\x1b[2Cक्‍ष", cur: at(2, 1), gap: gGraph},
	{name: "print Devanagari grapheme should be wide on next page", cols: 80, rows: 24,
		in: g2027 + "\x1b[24;80Hक्‍ष", cur: at(2, 23), gap: gGraph},
	{name: "print invalid VS16 with second char (combining)", cols: 80, rows: 80,
		in: g2027 + "n️̃", cur: at(1, 0), gap: gGraph},
	{name: "overwrite grapheme should clear grapheme data", cols: 5, rows: 5,
		in: g2027 + "⛈︎\x1b[1;1HA", screen: scr("A"), gap: gGraph},
	{name: "overwrite multicodepoint grapheme clears grapheme data", cols: 10, rows: 10,
		in: g2027 + g1Fam + "\x1b[1;1HX", cur: at(1, 0), screen: scr("X"), gap: gGraph},
	{name: "overwrite multicodepoint grapheme tail clears grapheme data", cols: 10, rows: 10,
		in: g2027 + g1Fam + "\x1b[1;2HX", cur: at(2, 0), screen: scr(" X"), gap: gGraph},
	{name: "print breaks valid grapheme cluster with Prepend + ASCII for speed", cols: 5, rows: 5,
		in: g2027 + "_؀1", cur: at(3, 0), gap: gGraph},

	// scrollback viewport
	{name: "print writes to bottom if scrolled", cols: 5, rows: 2,
		in: "hello\x1b[H\x1bD\x1bD\x1bD", screen: scr("")},
	{name: "print writes to bottom if scrolled #2", cols: 5, rows: 2,
		in: "hello\x1b[H\x1bD\x1bD\x1bDA", screen: scr("\nA"),
		check: func(t *Terminal) string {
			// upstream: scrolling the viewport to the top shows "hello".
			if sb := t.ScrollbackLines(); len(sb) == 0 || sb[0] != "hello" {
				return fmt.Sprintf("scrollback %q, want first line \"hello\"", sb)
			}
			return ""
		}},

	// charsets
	{name: "print charset", cols: 80, rows: 80,
		in: "\x1b)0\x1b*0\x1b+0`\x1b(B`\x1b(B`\x1b(0`", screen: scr("```◆")},
	{name: "print charset outside of ASCII", cols: 80, rows: 80,
		in: "\x1b)0\x1b*0\x1b+0\x1b(0`\U0001F600", screen: scr("◆\U0001F600")},
	{name: "print wide codepoint in a charset prints it unmapped", cols: 10, rows: 2,
		in: "\x1b(0`\U0001F600a", cur: at(4, 0), screen: scr("◆\U0001F600▒"),
		check: func(t *Terminal) string { return g1Cells(t, [3]int{1, 0, 0x1F600}, [3]int{2, 0, g1Tail}) }},
	{name: "print single shift is used up by a combining character", cols: 10, rows: 2,
		in: "\x1b*A#\x1bŃ#", cur: at(2, 0),
		check: func(t *Terminal) string { return g1Cell(t, 1, 0, '#') }, gap: "national/other charsets"},
	{name: "VS16 moving a character to the next line keeps it unmapped", cols: 3, rows: 5,
		in: g2027 + "\x1b[2C#\x1b(A️", gap: gGraph},
	{name: "print invoke charset", cols: 80, rows: 80,
		in: "\x1b)0`\x0e``\x0f`", screen: scr("`◆◆`")},
	// Upstream single-shifts G1, which has no escape sequence; SS2 with G2 is the equivalent.
	{name: "print invoke charset single", cols: 80, rows: 80,
		in: "\x1b*0`\x1bN``", screen: scr("`◆`")},
	{name: "print kitty unicode placeholder", cols: 10, rows: 10,
		in: "\U0010EEEE", cur: at(1, 0), gap: "images (sixel/kitty)"},

	// wrapping
	{name: "soft wrap", cols: 3, rows: 80, in: "hello", cur: at(2, 1), screen: scr("hel\nlo")},
	{name: "soft wrap with semantic prompt", cols: 3, rows: 80, in: "\x1b]133;A\x07hello", gap: gOSC},
	{name: "disabled wraparound with wide char and one space", cols: 5, rows: 5,
		in: "\x1b[?7lAAAA\U0001F6A8", cur: at(4, 0), screen: scr("AAAA"),
		check: func(t *Terminal) string { return g1Cell(t, 4, 0, 0) }},
	{name: "disabled wraparound with wide char and no space", cols: 5, rows: 5,
		in: "\x1b[?7lAAAAA\U0001F6A8", cur: at(4, 0), screen: scr("AAAAA"),
		check: func(t *Terminal) string { return g1Cell(t, 4, 0, 'A') }},
	{name: "disabled wraparound with wide grapheme and half space", cols: 5, rows: 5,
		in: g2027 + "\x1b[?7lAAAA❤️", cur: at(4, 0), screen: scr("AAAA❤"), gap: gGraph},
	{name: "print with wraparound disabled clears pending wrap after CBT", cols: 6, rows: 2,
		in: "\x1b[?7habcdef\x1b[?7l\x1b[Z", pending: yes()},
	{name: "print with wraparound disabled clears pending wrap after CBT #2", cols: 6, rows: 2,
		in: "\x1b[?7habcdef\x1b[?7l\x1b[Zx", pending: no()},
	{name: "print with wraparound disabled clears pending wrap after CBT #3", cols: 6, rows: 2,
		in: "\x1b[?7habcdef\x1b[?7l\x1b[Zx\x1b[?7hy", cur: at(2, 0), screen: scr("xycdef")},
	{name: "print with wraparound disabled clears pending wrap after CBT #4", cols: 6, rows: 2,
		in: "\x1b[?7labcdef\x1b[?7l\x1b[Z", pending: yes()},
	{name: "print with wraparound disabled clears pending wrap after CBT #5", cols: 6, rows: 2,
		in: "\x1b[?7labcdef\x1b[?7l\x1b[Zx", pending: no()},
	{name: "print with wraparound disabled clears pending wrap after CBT #6", cols: 6, rows: 2,
		in: "\x1b[?7labcdef\x1b[?7l\x1b[Zx\x1b[?7hy", cur: at(2, 0), screen: scr("xycdef")},
	{name: "print right margin wrap", cols: 10, rows: 5,
		in: "123456789\x1b[?69h\x1b[3;5s\x1b[1;5HXY", screen: scr("1234X6789\n  Y"), gap: gLRM},
	{name: "print right margin wrap dirty tracking", cols: 10, rows: 5,
		in: "123456789\x1b[?69h\x1b[3;5s\x1b[1;5HXY", screen: scr("1234X6789\n  Y"), gap: gLRM},
	{name: "print right margin outside", cols: 10, rows: 5,
		in: "123456789\x1b[?69h\x1b[3;5s\x1b[1;6HXY", screen: scr("12345XY89"), gap: gLRM},
	{name: "print right margin outside wrap", cols: 10, rows: 5,
		in: "123456789\x1b[?69h\x1b[3;5s\x1b[1;10HXY", screen: scr("123456789X\n  Y"), gap: gLRM},
	{name: "print wide char at right margin does not create spacer head", cols: 10, rows: 10,
		in: "\x1b[?69h\x1b[3;5s\x1b[1;5H\U0001F600", cur: at(4, 1), gap: gLRM},

	// hyperlinks
	{name: "print with hyperlink", cols: 80, rows: 80, in: g1Osc8 + "123456", gap: gOSC},
	{name: "print over cell with same hyperlink", cols: 80, rows: 80, in: g1Osc8 + "123456\x1b[1;1H123456", gap: gOSC},
	{name: "print and end hyperlink", cols: 80, rows: 80, in: g1Osc8 + "123" + g1Osc8e + "456", gap: gOSC},
	{name: "print and change hyperlink", cols: 80, rows: 80,
		in: "\x1b]8;;http://one.example.com\x1b\\123\x1b]8;;http://two.example.com\x1b\\456", gap: gOSC},
	{name: "overwrite hyperlink", cols: 80, rows: 80,
		in: "\x1b]8;;http://one.example.com\x1b\\123\x1b[1;1H" + g1Osc8e + "456", gap: gOSC},
	{name: "print wide char at right edge with hyperlink", cols: 10, rows: 5,
		in: g1Osc8 + "\x1b[1;10H中", cur: at(2, 1),
		check: func(t *Terminal) string {
			return g1Cells(t, [3]int{9, 0, 0}, [3]int{0, 1, 0x4e2d}, [3]int{1, 1, g1Tail})
		}},

	// linefeed / carriage return / backspace
	{name: "linefeed and carriage return", cols: 80, rows: 80,
		in: "hello\r\nworld", cur: at(5, 1), screen: scr("hello\nworld")},
	{name: "linefeed unsets pending wrap", cols: 5, rows: 80, in: "hello", pending: yes()},
	{name: "linefeed unsets pending wrap #2", cols: 5, rows: 80, in: "hello\n", pending: no()},
	{name: "linefeed mode automatic carriage return", cols: 10, rows: 10,
		in: "\x1b[20h123456\nX", screen: scr("123456\nX")},
	{name: "carriage return unsets pending wrap", cols: 5, rows: 80, in: "hello", pending: yes()},
	{name: "carriage return unsets pending wrap #2", cols: 5, rows: 80, in: "hello\r", pending: no()},
	{name: "carriage return origin mode moves to left margin", cols: 5, rows: 80,
		in: "\x1b[?69h\x1b[3;5s\x1b[?6h\x1b[1;1H\r", cur: at(2, 0), gap: gLRM},
	{name: "carriage return left of left margin moves to zero", cols: 5, rows: 80,
		in: "\x1b[?69h\x1b[3;5s\x1b[1;2H\r", cur: at(0, 0), gap: gLRM},
	{name: "carriage return right of left margin moves to left margin", cols: 5, rows: 80,
		in: "\x1b[?69h\x1b[3;5s\x1b[1;4H\r", cur: at(2, 0), gap: gLRM},
	{name: "backspace", cols: 80, rows: 80, in: "hello\by", cur: at(5, 0), screen: scr("helly")},

	// tabs
	{name: "horizontal tabs", cols: 20, rows: 5, in: "1\t", cur: at(8, 0)},
	{name: "horizontal tabs #2", cols: 20, rows: 5, in: "1\t\t", cur: at(16, 0)},
	{name: "horizontal tabs #3", cols: 20, rows: 5, in: "1\t\t\t", cur: at(19, 0)},
	{name: "horizontal tabs #4", cols: 20, rows: 5, in: "1\t\t\t\t", cur: at(19, 0)},
	{name: "horizontal tabs starting on tabstop", cols: 20, rows: 5,
		in: "\x1b[0;9HX\x1b[0;9H\tA", screen: scr("        X       A")},
	{name: "horizontal tabs with right margin", cols: 20, rows: 5,
		in: "\x1b[?69h\x1b[3;6s\x1b[1;1HX\tA", screen: scr("X    A"), gap: gLRM},
	{name: "horizontal tabs back", cols: 20, rows: 5, in: "\x1b[1;20H\x1b[Z", cur: at(16, 0)},
	{name: "horizontal tabs back #2", cols: 20, rows: 5, in: "\x1b[1;20H\x1b[Z\x1b[Z", cur: at(8, 0)},
	{name: "horizontal tabs back #3", cols: 20, rows: 5, in: "\x1b[1;20H\x1b[Z\x1b[Z\x1b[Z", cur: at(0, 0)},
	{name: "horizontal tabs back #4", cols: 20, rows: 5, in: "\x1b[1;20H\x1b[Z\x1b[Z\x1b[Z\x1b[Z", cur: at(0, 0)},
	{name: "horizontal tabs back starting on tabstop", cols: 20, rows: 5,
		in: "\x1b[1;9HX\x1b[1;9H\x1b[ZA", screen: scr("A       X")},
	{name: "horizontal tabs with left margin in origin mode", cols: 20, rows: 5,
		in: "\x1b[?69h\x1b[3;6s\x1b[?6h\x1b[1;2HX\x1b[ZA", screen: scr("  AX"), gap: gLRM},
	{name: "horizontal tab back with cursor before left margin", cols: 20, rows: 5,
		in: "\x1b[?6h\x1b7\x1b[?69h\x1b[5;0s\x1b8\x1b[ZX", screen: scr("X"), gap: gLRM},

	// cursor position
	{name: "cursorPos resets wrap", cols: 5, rows: 5, in: "ABCDE", pending: yes()},
	{name: "cursorPos resets wrap #2", cols: 5, rows: 5, in: "ABCDE\x1b[1;1H", pending: no()},
	{name: "cursorPos resets wrap #3", cols: 5, rows: 5, in: "ABCDE\x1b[1;1HX", screen: scr("XBCDE")},
	{name: "cursorPos off the screen", cols: 5, rows: 5,
		in: "\x1b[500;500HX", screen: scr("\n\n\n\n    X")},
	{name: "cursorPos relative to origin", cols: 5, rows: 5,
		in: "\x1b[3;4r\x1b[?6h\x1b[1;1HX", screen: scr("\n\nX")},
	{name: "cursorPos relative to origin with left/right", cols: 5, rows: 5,
		in: "\x1b[3;4r\x1b[?69h\x1b[3;5s\x1b[?6h\x1b[1;1HX", screen: scr("\n\n  X"), gap: gLRM},
	{name: "cursorPos limits with full scroll region", cols: 5, rows: 5,
		in: "\x1b[3;4r\x1b[?69h\x1b[3;5s\x1b[?6h\x1b[500;500HX", screen: scr("\n\n\n    X"), gap: gLRM},
	{name: "setCursorPos (original test)", cols: 80, rows: 80, in: "", cur: at(0, 0)},
	{name: "setCursorPos (original test) #2", cols: 80, rows: 80, in: "\x1b[0;0H", cur: at(0, 0)},
	{name: "setCursorPos (original test) #3", cols: 80, rows: 80, in: g1SCP3, cur: at(79, 79)},
	{name: "setCursorPos (original test) #4", cols: 80, rows: 80, in: g1SCP4, pending: yes()},
	{name: "setCursorPos (original test) #5", cols: 80, rows: 80, in: g1SCP5, pending: no()},
	{name: "setCursorPos (original test) #6", cols: 80, rows: 80, in: g1SCP6, cur: at(79, 79)},
	{name: "setCursorPos (original test) #7", cols: 80, rows: 80, in: g1SCP7, cur: at(0, 9)},
	{name: "setCursorPos (original test) #8", cols: 80, rows: 80, in: g1SCP8, cur: at(0, 9)},
	{name: "setCursorPos (original test) #9", cols: 80, rows: 80, in: g1SCP9, cur: at(0, 79)},
	{name: "setCursorPos (original test) #10", cols: 80, rows: 80, in: g1SCP10, cur: at(0, 10)},

	// margins
	{name: "setTopAndBottomMargin simple", cols: 5, rows: 5,
		in: g1Grid + "\x1b[0;0r\x1b[1T", screen: scr("\nABC\nDEF\nGHI")},
	{name: "setTopAndBottomMargin top only", cols: 5, rows: 5,
		in: g1Grid + "\x1b[2;0r\x1b[1T", screen: scr("ABC\n\nDEF\nGHI")},
	{name: "setTopAndBottomMargin top and bottom", cols: 5, rows: 5,
		in: g1Grid + "\x1b[1;2r\x1b[1T", screen: scr("\nABC\nGHI")},
	{name: "setTopAndBottomMargin top equal to bottom", cols: 5, rows: 5,
		in: g1Grid + "\x1b[2;2r\x1b[1T", screen: scr("\nABC\nDEF\nGHI")},
	{name: "setLeftAndRightMargin simple", cols: 5, rows: 5,
		in: g1Grid + "\x1b[?69h\x1b[0;0s\x1b[1X", screen: scr(" BC\nDEF\nGHI"), gap: gLRM},
	{name: "setLeftAndRightMargin left only", cols: 5, rows: 5,
		in: g1Grid + "\x1b[?69h\x1b[2;0s\x1b[1;2H\x1b[L", screen: scr("A\nDBC\nGEF\n HI"), gap: gLRM},
	{name: "setLeftAndRightMargin left and right", cols: 5, rows: 5,
		in: g1Grid + "\x1b[?69h\x1b[1;2s\x1b[1;2H\x1b[L", screen: scr("  C\nABF\nDEI\nGH"), gap: gLRM},
	{name: "setLeftAndRightMargin left equal right", cols: 5, rows: 5,
		in: g1Grid + "\x1b[?69h\x1b[2;2s\x1b[1;2H\x1b[L", screen: scr("\nABC\nDEF\nGHI"), gap: gLRM},
	{name: "setLeftAndRightMargin mode 69 unset", cols: 5, rows: 5,
		in: g1Grid + "\x1b[?69l\x1b[1;2s\x1b[1;2H\x1b[L", screen: scr("\nABC\nDEF\nGHI")},

	// insertLines
	{name: "insertLines simple", cols: 5, rows: 5,
		in: g1Grid + "\x1b[2;2H\x1b[L", screen: scr("ABC\n\nDEF\nGHI")},
	{name: "insertLines colors with bg color", cols: 5, rows: 5,
		in: g1Grid + "\x1b[2;2H\x1b[48;2;255;0;0m\x1b[L", screen: scr("ABC\n\nDEF\nGHI"),
		check: func(t *Terminal) string {
			for x := range 5 {
				if c := t.cellAt(x, 1); c.Bg != rgbColor(0xff, 0, 0) {
					return fmt.Sprintf("cell %d,1 bg %#x, want rgb ff0000", x, c.Bg)
				}
			}
			return ""
		}},
	{name: "insertLines handles style refs", cols: 5, rows: 3,
		in: "ABC\r\nDEF\r\n\x1b[1mGHI\x1b[0m\x1b[2;2H\x1b[L", screen: scr("ABC\n\nDEF")},
	{name: "insertLines outside of scroll region", cols: 5, rows: 5,
		in: g1Grid + "\x1b[3;4r\x1b[2;2H\x1b[L", screen: scr("ABC\nDEF\nGHI")},
	{name: "insertLines top/bottom scroll region", cols: 5, rows: 5,
		in: g1Grid + "\r\n123\x1b[1;3r\x1b[2;2H\x1b[L", screen: scr("ABC\n\nDEF\n123")},
	// Upstream linefeeds until the content straddles a page boundary; the
	// count only matters to its page list, so any count >= rows will do.
	{name: "insertLines across page boundary marks all shifted rows dirty", cols: 10, rows: 5,
		in:     strings.Repeat("\n", 300) + "1AAAA\r\n2BBBB\r\n3CCCC\r\n4DDDD\r\n5EEEE\x1b[1;1H\x1b[L",
		screen: scr("\n1AAAA\n2BBBB\n3CCCC\n4DDDD")},
	{name: "insertLines hyperlink-dense row crosses page boundary", cols: 10, rows: 5,
		in: strings.Repeat("\n", 300) + "\x1b[1;1H0\x1b[2;1H1\x1b[4;1H3\x1b[5;1H4\x1b[3;1H" +
			g1DenseLinks() + "\x1b[1;1H\x1b[L",
		screen: scr("\n0\n1\nABCDEFGHIJ\n3")},
	{name: "insertLines (legacy test)", cols: 2, rows: 5,
		in: "A\r\nB\r\nC\r\nD\r\nE\x1b[2;1H\x1b[2L", screen: scr("A\n\n\nB\nC")},
}
