// Translated from wezterm (MIT), term/src/test/{mod,c0,c1,csi}.rs at commit e2a98e1

package vt

import (
	"fmt"
	"slices"
	"strings"
)

func init() { registerSuite("wezterm", weztermCases) }

// wzAll mirrors wezterm's assert_all_contents: scrollback followed by the
// visible rows must equal all (trailing blanks trimmed). It runs in on a
// fresh terminal with wezterm's scrollback limit sb (0 = no scrollback).
func wzAll(name string, cols, rows, sb int, in string, all ...string) confCase {
	n := len(all) - rows
	if n < 0 {
		n = 0
	}
	screen := strings.TrimRight(strings.Join(all[n:], "\n"), "\n")
	return confCase{
		name: name, cols: cols, rows: rows, in: in,
		screen: &screen,
		check: func(_ *Terminal) string {
			lim := sb
			if lim == 0 {
				lim = -1
			}
			t := New(cols, rows, Options{ScrollbackLines: lim})
			feedConf(t, in)
			got := append(t.ScrollbackLines(), t.Lines()...)
			if !slices.Equal(got, all) {
				return fmt.Sprintf("all lines (scrollback limit %d)\n got  %q\n want %q", sb, got, all)
			}
			return ""
		},
	}
}

// wzCellsBg checks every cell is blank or the rune of texts[y] at x
// (spaces mean blank) and has the given colors.
func wzCellsBg(rows, cols int, texts []string, fg, bg Color) func(t *Terminal) string {
	return func(t *Terminal) string {
		for y := 0; y < rows; y++ {
			line := []rune(texts[y])
			for x := 0; x < cols; x++ {
				want := ""
				if x < len(line) && line[x] != ' ' {
					want = string(line[x])
				}
				got := t.cellText(x, y)
				if got == " " {
					got = ""
				}
				if got != want {
					return fmt.Sprintf("cell %d,%d text %q want %q", x, y, got, want)
				}
				c := t.cellAt(x, y)
				if c.Fg != fg || c.Bg != bg {
					return fmt.Sprintf("cell %d,%d fg %#x bg %#x want fg %#x bg %#x", x, y, c.Fg, c.Bg, fg, bg)
				}
			}
		}
		return ""
	}
}

const (
	wzDecGfxIn  = "abcdefghijklmnopqrstuvwxyz"
	wzDecGfxOut = "▒␉␌␍␊°±␤␋┘┐┌└┼⎺⎻─⎼⎽├┤┴┬│≤≥"
	wzAltPre    = "M\no\nn\nk\ne\ny\x1b[?1049h1\n2\n3\n4\n5"
	wzJamo      = "한"
)

var weztermCases = []confCase{
	// ---- c0.rs ----
	{name: "test_bs", cols: 4, rows: 3, in: "\b", cur: at(0, 0)},
	{name: "test_bs#2", cols: 4, rows: 3, in: "\bab\b", cur: at(1, 0)},
	{name: "test_lf", cols: 10, rows: 3, in: "hello\n", cur: at(5, 1)},
	{name: "test_cr", cols: 10, rows: 3, in: "hello\r", cur: at(0, 0)},
	{name: "test_tab", cols: 25, rows: 3, in: "\t", cur: at(8, 0)},
	{name: "test_tab#2", cols: 25, rows: 3, in: "\t\t", cur: at(16, 0)},
	{name: "test_tab#3", cols: 25, rows: 3, in: "\t\t\t", cur: at(24, 0)},
	{name: "test_tab#4", cols: 25, rows: 3, in: "\t\t\t\t", cur: at(24, 0)},

	// ---- c1.rs ----
	{name: "test_ind", cols: 4, rows: 4, in: "a\r\nb\x1bD", cur: at(1, 2), screen: scr("a\nb")},
	{name: "test_ind#2", cols: 4, rows: 4, in: "a\r\nb\x1bD\x1bD", cur: at(1, 3)},
	{name: "test_ind#3", cols: 4, rows: 4, in: "a\r\nb\x1bD\x1bD\x1bD", cur: at(1, 3), screen: scr("b")},
	{name: "test_nel", cols: 4, rows: 4, in: "a\r\nb\x1bE", cur: at(0, 2)},
	{name: "test_nel#2", cols: 4, rows: 4, in: "a\r\nb\x1bE\x1bE", cur: at(0, 3)},
	{name: "test_nel#3", cols: 4, rows: 4, in: "a\r\nb\x1bE\x1bE\x1bE", cur: at(0, 3), screen: scr("b")},
	{name: "test_hts", cols: 25, rows: 3, in: "boo\x1bH\r\n", cur: at(0, 1)},
	{name: "test_hts#2", cols: 25, rows: 3, in: "boo\x1bH\r\n\t", cur: at(3, 1)},
	{name: "test_hts#3", cols: 25, rows: 3, in: "boo\x1bH\r\n\t\t", cur: at(8, 1)},
	{name: "test_hts#4", cols: 25, rows: 3, in: "boo\x1bH\r\n\t\t" + rz(80, 4) + "\x1b[2;1H\t", cur: at(3, 1)},
	{name: "test_hts#5", cols: 25, rows: 3, in: "boo\x1bH\r\n\t\t" + rz(80, 4) + "\x1b[2;1H\t\t", cur: at(8, 1)},
	{name: "test_hts#6", cols: 25, rows: 3, in: "boo\x1bH\r\n\t\t" + rz(80, 4) + "\x1b[2;1H\t\t\t", cur: at(16, 1)},
	{name: "test_hts#7", cols: 25, rows: 3, in: "boo\x1bH\r\n\t\t" + rz(80, 4) + "\x1b[2;1H\t\t\t\t", cur: at(24, 1)},
	{name: "test_hts#8", cols: 25, rows: 3, in: "boo\x1bH\r\n\t\t" + rz(80, 4) + "\x1b[2;1H\t\t\t\t\t", cur: at(32, 1)},
	{name: "test_ri", cols: 2, rows: 4, in: "a\r\nb\r\nc\r\nd.", screen: scr("a\nb\nc\nd."), cur: at(1, 3)},
	{name: "test_ri#2", cols: 2, rows: 4, in: "a\r\nb\r\nc\r\nd.\x1bM", cur: at(1, 2)},
	{name: "test_ri#3", cols: 2, rows: 4, in: "a\r\nb\r\nc\r\nd.\x1bM\x1bM", cur: at(1, 1)},
	{name: "test_ri#4", cols: 2, rows: 4, in: "a\r\nb\r\nc\r\nd.\x1bM\x1bM\x1bM", cur: at(1, 0)},
	{name: "test_ri#5", cols: 2, rows: 4, in: "a\r\nb\r\nc\r\nd.\x1bM\x1bM\x1bM\x1bM", cur: at(1, 0), screen: scr("\na\nb\nc")},

	// ---- csi.rs ----
	{name: "test_789", cols: 8, rows: 1, in: "\x1b[40m\x1b[Kfoo\x1b[2P", screen: scr("foo"),
		check: wzCellsBg(1, 8, []string{"foo"}, 0, idx(0))},
	{name: "test_vpa", cols: 4, rows: 3, in: "a\r\nb\r\nc", cur: at(1, 2)},
	{name: "test_vpa#2", cols: 4, rows: 3, in: "a\r\nb\r\nc\x1b[d", cur: at(1, 0)},
	{name: "test_vpa#3", cols: 4, rows: 3, in: "a\r\nb\r\nc\x1b[d\r\n\r\n", cur: at(0, 2)},
	{name: "test_vpa#4", cols: 4, rows: 3, in: "a\r\nb\r\nc\x1b[d\r\n\r\n\x1b[2d", cur: at(0, 1)},
	{name: "test_vpa#5", cols: 4, rows: 3, in: "a\r\nb\r\nc\x1b[d\r\n\r\n\x1b[2d\x1b[-2d", cur: at(0, 1)},
	{name: "test_rep", cols: 4, rows: 3, in: "h\x1b[1;2H\x1b[2ba", screen: scr("hhha")},
	{name: "test_irm", cols: 8, rows: 3, in: "foo\x1b[1;1H\x1b[4hBAR", screen: scr("BARfoo")},
	{name: "test_ich", cols: 4, rows: 3, in: "hey!wat?\x1b[1;2H\x1b[2@", screen: scr("h  e\nwat?")},
	{name: "test_ich#2", cols: 4, rows: 3, in: "hey!wat?\x1b[1;2H\x1b[2@\x1b[12@", screen: scr("h\nwat?")},
	{name: "test_ich#3", cols: 4, rows: 3, in: "hey!wat?\x1b[1;2H\x1b[2@\x1b[12@\x1b[-12@", screen: scr("h\nwat?")},
	{name: "test_ech", cols: 4, rows: 3, in: "hey!wat?\x1b[1;2H\x1b[2X", screen: scr("h  !\nwat?")},
	{name: "test_ech#2", cols: 4, rows: 3, in: "hey!wat?\x1b[1;2H\x1b[2X\x1b[12X", screen: scr("h\nwat?")},
	{name: "test_ech#3", cols: 4, rows: 3, in: "hey!wat?\x1b[1;2H\x1b[2X\x1b[12X\x1b[-12X", screen: scr("h\nwat?")},
	{name: "test_dch", cols: 12, rows: 1, in: "hello world\x1b[1;2H\x1b[P", screen: scr("hllo world")},
	{name: "test_dch#2", cols: 12, rows: 1, in: "hello world\x1b[1;2H\x1b[P\x1b[1;5H\x1b[2P", screen: scr("hlloorld")},
	{name: "test_dch#3", cols: 12, rows: 1, in: "hello world\x1b[1;2H\x1b[P\x1b[1;5H\x1b[2P\x1b[-2P", screen: scr("hlloorld")},
	{name: "test_cup", cols: 4, rows: 3, in: "\x1b[2;2H", cur: at(1, 1)},
	{name: "test_cup#2", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[0;0H", cur: at(0, 0)},
	{name: "test_cup#3", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[0;0H\x1b[3;3H", cur: at(2, 2)},
	{name: "test_cup#4", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[0;0H\x1b[3;3H\x1b[0;0H", cur: at(0, 0)},
	{name: "test_cup#5", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[0;0H\x1b[3;3H\x1b[0;0H\x1b[501;501H", cur: at(4, 2)},
	{name: "test_hvp", cols: 4, rows: 3, in: "\x1b[2;2f", cur: at(1, 1)},
	{name: "test_hvp#2", cols: 4, rows: 3, in: "\x1b[2;2f\x1b[0;0f", cur: at(0, 0)},
	{name: "test_hvp#3", cols: 4, rows: 3, in: "\x1b[2;2f\x1b[0;0f\x1b[3;3f", cur: at(2, 2)},
	{name: "test_hvp#4", cols: 4, rows: 3, in: "\x1b[2;2f\x1b[0;0f\x1b[3;3f\x1b[0;0f", cur: at(0, 0)},
	{name: "test_hvp#5", cols: 4, rows: 3, in: "\x1b[2;2f\x1b[0;0f\x1b[3;3f\x1b[0;0f\x1b[501;501f", cur: at(4, 2)},
	{name: "test_dl", cols: 1, rows: 3, in: "a\r\nb\r\nc\x1b[2;1H\x1b[1M", screen: scr("a\nc"), cur: at(0, 1)},
	{name: "test_dl#2", cols: 1, rows: 3, in: "a\r\nb\r\nc\x1b[2;1H\x1b[1M\x1b[1;1H\x1b[2M", screen: scr("")},
	{name: "test_dl#3", cols: 1, rows: 3, in: "a\r\nb\r\nc\x1b[2;1H\x1b[1M\x1b[1;1H\x1b[2M1\r\n2\r\n3\x1b[2;1H\x1b[-2M", screen: scr("1\n2\n3")},
	{name: "test_cha", cols: 4, rows: 3, in: "\x1b[2;2H", cur: at(1, 1)},
	{name: "test_cha#2", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[G", cur: at(0, 1)},
	{name: "test_cha#3", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[G\x1b[2G", cur: at(1, 1)},
	{name: "test_cha#4", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[G\x1b[2G\x1b[0G", cur: at(0, 1)},
	{name: "test_cha#5", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[G\x1b[2G\x1b[0G\x1b[-1G", cur: at(0, 1)},
	{name: "test_cha#6", cols: 4, rows: 3, in: "\x1b[2;2H\x1b[G\x1b[2G\x1b[0G\x1b[-1G\x1b[100G", cur: at(4, 1)},
	{name: "test_ed", cols: 3, rows: 3, in: "abc\r\ndef\r\nghi\x1b[3;2H\x1b[J", screen: scr("abc\ndef\ng")},
	{name: "test_ed#2 BCE", cols: 3, rows: 3, in: "abc\r\ndef\r\nghi\x1b[3;2H\x1b[J\x1b[44m\x1b[2J", screen: scr(""),
		check: wzCellsBg(3, 3, []string{"", "", ""}, 0, idx(4))},
	wzAll("test_ed_erase_scrollback", 3, 3, 3, "abc\r\ndef\r\nghi\r\n111\r\n222\r\na\x1b[3J", "111", "222", "a"),
	wzAll("test_ed_erase_scrollback#2", 3, 3, 3, "abc\r\ndef\r\nghi\r\n111\r\n222\r\na\x1b[3Jb", "111", "222", "ab"),

	// ---- mod.rs ----
	{name: "test_semantic_1539", cols: 10, rows: 5, in: "\x1b]133;I\x1b\\prompt\r\nwoot", screen: scr("prompt\nwoot")},
	{name: "test_semantic", cols: 10, rows: 5, gap: "hyperlinks / semantic prompts / OSC state",
		in: "hello\x1b]133;L\x1b\\there\x1b[3;1H\x1b]133;L\x1b\\three\x1b]133;A\x1b\\> \x1b]133;B\x1b\\ls -l\r\n\x1b]133;C\x1b\\some file"},
	{name: "issue_1161", cols: 5, rows: 1, in: "x　x", screen: scr("x　x")},
	{name: "basic_output", cols: 10, rows: 5, in: "\x1b[2;2H\x1b[?7lhello, world!", screen: scr("\n hello, w!")},
	{name: "basic_output#2", cols: 10, rows: 5, in: "\x1b[2;2H\x1b[?7lhello, world!\x1b[?7h\x1b[1J\x1b[2;2Hhello, world!",
		screen: scr("\n hello, wo\nrld!")},
	{name: "basic_output#3", cols: 10, rows: 5, in: "\x1b[2;2H\x1b[?7lhello, world!\x1b[?7h\x1b[1J\x1b[2;2Hhello, world!\x1b[1J",
		screen: scr("")},
	{name: "basic_output#4", cols: 10, rows: 5, in: "\x1b[2;2H\x1b[?7lhello, world!\x1b[?7h\x1b[1J\x1b[2;2Hhello, world!\x1b[1J\x1b[3;1Hwoot\x1b[3;3H\x1b[K",
		screen: scr("\n\nwo")},
	{name: "basic_output#5", cols: 10, rows: 5, in: "\x1b[2;2H\x1b[?7lhello, world!\x1b[?7h\x1b[1J\x1b[2;2Hhello, world!\x1b[1J\x1b[3;1Hwoot\x1b[3;3H\x1b[K\x1b[1K",
		screen: scr("")},
	{name: "cursor_movement_damage", cols: 3, rows: 2, in: "fooo.", screen: scr("foo\no."), cur: at(2, 1)},
	{name: "cursor_movement_damage#2", cols: 3, rows: 2, in: "fooo.\x1b[2;1H\b", cur: at(0, 1)},
	{name: "cursor_movement_damage#3", cols: 3, rows: 2, in: "fooo.\x1b[2;1H\b\x1b[1;1H", cur: at(0, 0)},
	{name: "scroll_up_within_left_and_right_margins", cols: 5, rows: 5, gap: "DECLRMM left/right margins",
		in:     "111\r\n222\r\n333\r\n44444\r\n555\x1b[?69h\x1b[2;4s\x1b[3;5r\x1b[5;2H\n",
		screen: scr("111\n222\n34444\n455\n5")},
	{name: "scroll_down_within_left_and_right_margins", cols: 5, rows: 5, gap: "DECLRMM left/right margins",
		in:     "111\r\n222\r\n333\r\n44444\r\n555\x1b[?69h\x1b[2;4s\x1b[3;6r\x1b[3;2H\x1b[L",
		screen: scr("111\n222\n3\n433\n54444")},
	{name: "test_delete_lines", cols: 3, rows: 5, in: "111\r\n222\r\n333\r\n444\r\n555", screen: scr("111\n222\n333\n444\n555")},
	{name: "test_delete_lines#2", cols: 3, rows: 5, in: "111\r\n222\r\n333\r\n444\r\n555\x1b[2;1H\x1b[2M", screen: scr("111\n444\n555")},
	{name: "test_delete_lines#3", cols: 3, rows: 5, in: "111\r\n222\r\n333\r\n444\r\n555\x1b[2;1H\x1b[2M\x1b[4;1Haaa\r\nbbb\x1b[2;1H",
		screen: scr("111\n444\n555\naaa\nbbb")},
	{name: "test_delete_lines#4", cols: 3, rows: 5, in: "111\r\n222\r\n333\r\n444\r\n555\x1b[2;1H\x1b[2M\x1b[4;1Haaa\r\nbbb\x1b[2;1H\x1b[2;4r\x1b[2;1H\x1b[2M",
		screen: scr("111\naaa\n\n\nbbb")},
	{name: "test_delete_lines#5", cols: 3, rows: 5, in: "111\r\n222\r\n333\r\n444\r\n555\x1b[2;1H\x1b[2M\x1b[4;1Haaa\r\nbbb\x1b[2;1H\x1b[2;4r\x1b[2;1H\x1b[2M\x1b[1;5r\x1b[1M",
		screen: scr("aaa\n\n\nbbb")},
	{name: "test_dec_special_graphics", cols: 50, rows: 2, in: "\x1b(0ABC" + wzDecGfxIn + "DEF\r\n\x1b(Bhello",
		screen: scr("ABC" + wzDecGfxOut + "DEF\nhello")},
	{name: "test_dec_special_graphics#2", cols: 50, rows: 2, in: "\x1b)0\x0eSO-ABC" + wzDecGfxIn + "DEF\r\n\x0fSI-hello",
		screen: scr("SO-ABC" + wzDecGfxOut + "DEF\nSI-hello")},
	{name: "test_dec_double_width", cols: 50, rows: 4, gap: "double-width/height lines",
		in: "\x1b#3line1\r\nline2\x1b#4\r\nli\x1b#6ne3\r\n\x1b#5line4", screen: scr("line1\nline2\nline3\nline4")},
	{name: "test_resize_2162_by_2_then_up_1", cols: 20, rows: 4, in: "some long long text",
		screen: scr("some long long text"), cur: at(19, 0)},
	{name: "test_resize_2162_by_2_then_up_1#2", cols: 20, rows: 4, in: "some long long text" + rz(18, 4),
		screen: scr("some long long tex\nt"), cur: at(1, 1)},
	{name: "test_resize_2162_by_2_then_up_1#3", cols: 20, rows: 4, in: "some long long text" + rz(18, 4) + rz(20, 3),
		screen: scr("some long long text"), cur: at(19, 0)},
	{name: "test_resize_2162_by_2_then_up_1#4", cols: 20, rows: 4, in: "some long long text" + rz(18, 4) + rz(20, 3) + rz(20, 4),
		screen: scr("some long long text"), cur: at(19, 0)},
	{name: "test_resize_2162_by_2", cols: 20, rows: 4, in: "some long long text" + rz(18, 4),
		screen: scr("some long long tex\nt"), cur: at(1, 1)},
	{name: "test_resize_2162_by_2#2", cols: 20, rows: 4, in: "some long long text" + rz(18, 4) + rz(20, 4),
		screen: scr("some long long text"), cur: at(19, 0)},
	{name: "test_resize_2162", cols: 20, rows: 4, in: "some long long text" + rz(19, 4),
		screen: scr("some long long text"), cur: at(19, 0)},
	{name: "test_resize_2162#2", cols: 20, rows: 4, in: "some long long text" + rz(19, 4) + rz(20, 4),
		screen: scr("some long long text"), cur: at(19, 0)},
	{name: "test_resize_wrap", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n", screen: scr("111\n2222\naa\n333")},
	{name: "test_resize_wrap#2", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8), screen: scr("111\n2222a\na\n333")},
	{name: "test_resize_wrap#3", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8), screen: scr("111\n2222aa\n333")},
	{name: "test_resize_wrap#4", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8) + rz(7, 8), screen: scr("111\n2222aa\n333")},
	{name: "test_resize_wrap#5", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8) + rz(7, 8) + rz(8, 8), screen: scr("111\n2222aa\n333")},
	{name: "test_resize_wrap#6", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8) + rz(7, 8) + rz(8, 8) + rz(7, 8),
		screen: scr("111\n2222aa\n333")},
	{name: "test_resize_wrap#7", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8) + rz(7, 8) + rz(8, 8) + rz(7, 8) + rz(6, 8),
		screen: scr("111\n2222aa\n333")},
	{name: "test_resize_wrap#8", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8) + rz(7, 8) + rz(8, 8) + rz(7, 8) + rz(6, 8) + rz(5, 8),
		screen: scr("111\n2222a\na\n333")},
	{name: "test_resize_wrap#9", cols: 4, rows: 8, in: "111\r\n2222aa\r\n333\r\n" + rz(5, 8) + rz(6, 8) + rz(7, 8) + rz(8, 8) + rz(7, 8) + rz(6, 8) + rz(5, 8) + rz(4, 8),
		screen: scr("111\n2222\naa\n333")},
	{name: "test_resize_wrap_issue_971", cols: 4, rows: 4, in: "====\r\nSS\r\n", screen: scr("====\nSS")},
	{name: "test_resize_wrap_issue_971#2", cols: 4, rows: 4, in: "====\r\nSS\r\n" + rz(6, 4), screen: scr("====\nSS")},
	{name: "test_resize_wrap_sgc_issue_978", cols: 4, rows: 4, in: "\x1b(0qqqq\x1b(B\r\nSS\r\n", screen: scr("────\nSS")},
	{name: "test_resize_wrap_sgc_issue_978#2", cols: 4, rows: 4, in: "\x1b(0qqqq\x1b(B\r\nSS\r\n" + rz(6, 4), screen: scr("────\nSS")},
	{name: "test_resize_wrap_dectcm_issue_978", cols: 4, rows: 4, in: "\x1b[?25l====\x1b[?25h\r\nSS\r\n", screen: scr("====\nSS")},
	{name: "test_resize_wrap_dectcm_issue_978#2", cols: 4, rows: 4, in: "\x1b[?25l====\x1b[?25h\r\nSS\r\n" + rz(6, 4), screen: scr("====\nSS")},
	{name: "test_resize_wrap_escape_code_issue_978", cols: 4, rows: 4, in: "====\x1b[0m\r\nSS\r\n", screen: scr("====\nSS")},
	{name: "test_resize_wrap_escape_code_issue_978#2", cols: 4, rows: 4, in: "====\x1b[0m\r\nSS\r\n" + rz(6, 4), screen: scr("====\nSS")},
	wzAll("test_scrollup", 1, 2, 4, "1\n", "1", ""),
	wzAll("test_scrollup#2", 1, 2, 4, "1\n2\n", "1", "2", ""),
	wzAll("test_scrollup#3", 1, 2, 4, "1\n2\n3\n", "1", "2", "3", ""),
	wzAll("test_scrollup#4", 1, 2, 4, "1\n2\n3\n4\n", "1", "2", "3", "4", ""),
	wzAll("test_scrollup#5", 1, 2, 4, "1\n2\n3\n4\n5\n", "1", "2", "3", "4", "5", ""),
	wzAll("test_scrollup#6", 1, 2, 4, "1\n2\n3\n4\n5\n6\n", "2", "3", "4", "5", "6", ""),
	wzAll("test_scrollup#7", 1, 2, 4, "1\n2\n3\n4\n5\n6\n7\n", "3", "4", "5", "6", "7", ""),
	wzAll("test_scrollup#8", 1, 2, 4, "1\n2\n3\n4\n5\n6\n7\n8\n", "4", "5", "6", "7", "8", ""),
	{name: "test_ri (UTF-8 C1 RI)", cols: 1, rows: 3, gap: "8-bit C1 controls", in: "1\n\u008d\n", screen: scr("1")},
	wzAll("test_scroll_margins", 1, 3, 10, "1\n2\n3\n4\n", "1", "2", "3", "4", ""),
	wzAll("test_scroll_margins#2", 1, 3, 10, "1\n2\n3\n4\n\x1b[1;2rz\n", "1", "2", "z", "4", ""),
	wzAll("test_scroll_margins#3", 1, 3, 10, "1\n2\n3\n4\n\x1b[1;2rz\na\n", "1", "2", "z", "a", "", ""),
	wzAll("test_scroll_margins#4", 1, 3, 10, "1\n2\n3\n4\n\x1b[1;2rz\na\n\x1b[2;1HW\n", "1", "2", "z", "a", "W", "", ""),
	wzAll("test_emoji_with_modifier", 5, 3, 0, "\U0001f44b\r\n\U0001f44b\U0001f3ff", "\U0001f44b", "\U0001f44b\U0001f3ff", ""),
	wzAll("test_1573", 5, 2, 0, wzJamo+"\r\n", wzJamo, ""),
	wzAll("test_region_scroll", 1, 5, 10, "1\n2\n3\n4\n5\x1b[2;3r\x1b[3;1H\na", "1", "3", "a", "4", "5"),
	wzAll("test_region_scroll#2", 1, 5, 10, "1\n2\n3\n4\n5\x1b[2;3r\x1b[3;1H\na\x1b[1;2r\x1b[2;1H\nb",
		"1", "3", "b", "a", "4", "5"),
	wzAll("test_region_scroll#3", 1, 5, 10, "1\n2\n3\n4\n5\x1b[2;3r\x1b[3;1H\na\x1b[1;2r\x1b[2;1H\nb\x1b[2;1H\x1b[3M",
		"1", "3", "", "a", "4", "5"),
	wzAll("test_region_scroll#4", 1, 5, 10, "1\n2\n3\n4\n5\x1b[2;3r\x1b[3;1H\na\x1b[1;2r\x1b[2;1H\nb\x1b[2;1H\x1b[3M\x1b[1;5r\x1b[5;1H\nX",
		"1", "3", "", "a", "4", "5", "X"),
	// In the alternate screen wezterm's all_lines is the alt screen alone.
	{name: "test_alt_screen_region_scroll", cols: 1, rows: 5, in: wzAltPre + "\x1b[2;3r\x1b[3;1H\na",
		screen: scr("1\n3\na\n4\n5"), alt: yes()},
	{name: "test_alt_screen_region_scroll#2", cols: 1, rows: 5, in: wzAltPre + "\x1b[2;3r\x1b[3;1H\na\x1b[1;2r\x1b[2;1H\nb",
		screen: scr("3\nb\na\n4\n5"), alt: yes()},
	{name: "test_alt_screen_region_scroll#3", cols: 1, rows: 5, in: wzAltPre + "\x1b[2;3r\x1b[3;1H\na\x1b[1;2r\x1b[2;1H\nb\x1b[1;5r\x1b[5;1H\nX",
		screen: scr("b\na\n4\n5\nX"), alt: yes()},
	func() confCase {
		c := wzAll("test_alt_screen_region_scroll#4", 1, 5, 10,
			wzAltPre+"\x1b[2;3r\x1b[3;1H\na\x1b[1;2r\x1b[2;1H\nb\x1b[1;5r\x1b[5;1H\nX\x1b[?1049l",
			"M", "o", "n", "k", "e", "y")
		c.alt = no()
		return c
	}(),
	wzAll("test_region_scrollback_limit", 1, 4, 2, "1\n2\n3\n4\x1b[1;2r\x1b[2;1HA\nB\nC\nD", "A", "B", "C", "D", "3", "4"),
	{name: "test_hyperlinks", cols: 5, rows: 3, gap: "hyperlinks / semantic prompts / OSC state",
		in:     "\x1b]8;;http://example.com\x1b\\hello\x1b]8;;\x1b\\\x1b]8;;http://example.com\x1b\\he\x1b[my!!\x1b]8;id=w00t;http://example.com/other\x1b\\wo\x1b[!p00t",
		screen: scr("hello\nhey!!\nwo00t")},
}
