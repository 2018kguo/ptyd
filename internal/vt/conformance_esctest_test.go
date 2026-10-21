package vt

import (
	"fmt"
	"strings"
)

// Independent cases covering behaviors exercised by esctest2 (GPL-2.0, not
// vendored; no code or text copied). Each case states, in our own terms,
// the screen and cursor facts xterm produces for a sequence of control
// functions; esctest2 observes the same facts through DECRQCRA checksums
// and CPR. The terminal is 80x24 unless a case depends on its size.

func init() { registerSuite("esctest", esctestCases) }

// escRect checks the rectangle whose top-left cell is (col, row), 1-based,
// against lines. A ' ' in lines matches a blank cell or a space.
func escRect(col, row int, lines ...string) func(*Terminal) string {
	return func(t *Terminal) string {
		for dy, line := range lines {
			for dx, want := range []rune(line) {
				x, y := col-1+dx, row-1+dy
				c := t.cellAt(x, y)
				got := c.R
				if got == 0 {
					got = ' '
				}
				if got != want {
					return fmt.Sprintf("cell (%d,%d) got %q want %q", x+1, y+1, string(got), string(want))
				}
			}
		}
		return ""
	}
}

// escAll combines checks; the first failure wins.
func escAll(fs ...func(*Terminal) string) func(*Terminal) string {
	return func(t *Terminal) string {
		for _, f := range fs {
			if msg := f(t); msg != "" {
				return msg
			}
		}
		return ""
	}
}

var esctestCases = []confCase{
	{name: "BS_Basic", cols: 80, rows: 24, in: "\x1b[3;3H\b", cur: at(1, 2)},
	{name: "BS_NoWrapByDefault", cols: 80, rows: 24, in: "\x1b[3;1H\b", cur: at(0, 2)},
	{name: "BS_WrapsInWraparoundMode", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[3;1H\b", cur: at(79, 1)},
	{name: "BS_InitialReverseWraparound", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[1;1H\x1bE\b", cur: at(0, 1)},
	{name: "BS_ReverseWrapRequiresDECAWM", cols: 80, rows: 24, in: "\x1b[?7l\x1b[?45h\x1b[3;1H\b", cur: at(0, 2)},
	{name: "BS_ReverseWrapRequiresDECAWM#2", cols: 80, rows: 24, in: "\x1b[?7l\x1b[?45h\x1b[3;1H\b\x1b[?7h\x1b[?45l\x1b[3;1H\b", cur: at(0, 2)},
	{name: "BS_ReverseWrapWithLeftRight", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[?69h\x1b[5;10s\x1b[3;5H\b", gap: "DECLRMM left/right margins"},
	{name: "BS_ReversewrapFromLeftEdgeToRightMargin", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[?69h\x1b[5;10s\x1b[3;1H\b", gap: "DECLRMM left/right margins"},
	{name: "BS_ReverseWrapGoesToBottom", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[2;5r\x1b[2;1H\b", cur: at(79, 4)},
	{name: "BS_StopsAtLeftMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[1;5H\b\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "BS_MovesLeftWhenLeftOfLeftMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[1;4H\b\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "BS_StopsAtOrigin", cols: 80, rows: 24, in: "\x1b[1;1H\b", cur: at(0, 0)},
	{name: "BS_CursorStartsInDoWrapPosition", cols: 80, rows: 24, in: "\x1b[1;79Hab\bX", check: escRect(79, 1, "Xb")},
	{name: "BS_ReverseWrapStartingInDoWrapPosition", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[1;79Hab\bX", check: escRect(79, 1, "aX")},
	{name: "BS_AfterNoWrappedInlines", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[3;1H******************************************************************************\n******************************************************************************\n\x1b[5;5H\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b", cur: at(4, 2)},
	{name: "BS_AfterOneWrappedInline", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[3;1H********************************************************************************************************************************************************************\n********************************************************************************************************************************************************************\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b\b", cur: at(8, 2)},
	{name: "CBT_OneTabStopByDefault", cols: 80, rows: 24, in: "\x1b[1;17H\x1b[Z", cur: at(8, 0)},
	{name: "CBT_ExplicitParameter", cols: 80, rows: 24, in: "\x1b[1;25H\x1b[2Z", cur: at(8, 0)},
	{name: "CBT_StopsAtLeftEdge", cols: 80, rows: 24, in: "\x1b[2;25H\x1b[5Z", cur: at(0, 1)},
	{name: "CBT_IgnoresRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;30s\x1b[9;7H\x1b[2Z", gap: "DECLRMM left/right margins"},
	{name: "CHA_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[G", cur: at(0, 2)},
	{name: "CHA_ExplicitParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[10G", cur: at(9, 2)},
	{name: "CHA_OutOfBoundsLarge", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[9999G", cur: at(79, 2)},
	{name: "CHA_ZeroParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[0G", cur: at(0, 2)},
	{name: "CHA_IgnoresScrollRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[3;5H\x1b[1G", gap: "DECLRMM left/right margins"},
	{name: "CHA_RespectsOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[9;7H\x1b[?6h\x1b[1;2H\x1b[1GX\x1b[?6l\x1b[r\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "CHT_OneTabStopByDefault", cols: 80, rows: 24, in: "\x1b[I", cur: at(8, 0)},
	{name: "CHT_ExplicitParameter", cols: 80, rows: 24, in: "\x1b[2I", cur: at(16, 0)},
	{name: "CHT_IgnoresScrollingRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;30s\x1b[9;7H\x1b[2I", gap: "DECLRMM left/right margins"},
	{name: "CNL_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[E", cur: at(0, 3)},
	{name: "CNL_ExplicitParam", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[2E", cur: at(0, 4)},
	{name: "CNL_StopsAtBottomLine", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[24E", cur: at(0, 23)},
	{name: "CNL_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[?69h\x1b[5;10s\x1b[6;7H\x1b[24E", gap: "DECLRMM left/right margins"},
	{name: "CNL_StopsAtBottomMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[2;4r\x1b[?69h\x1b[5;10s\x1b[3;7H\x1b[99E", gap: "DECLRMM left/right margins"},
	{name: "CPL_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[F", cur: at(0, 1)},
	{name: "CPL_ExplicitParam", cols: 80, rows: 24, in: "\x1b[5;6H\x1b[2F", cur: at(0, 2)},
	{name: "CPL_StopsAtTopLine", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[24F", cur: at(0, 0)},
	{name: "CPL_StopsAtTopLineWhenBegunAboveScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[?69h\x1b[5;10s\x1b[3;7H\x1b[24F", gap: "DECLRMM left/right margins"},
	{name: "CPL_StopsAtTopMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[2;4r\x1b[?69h\x1b[5;10s\x1b[3;7H\x1b[99F", gap: "DECLRMM left/right margins"},
	{name: "CR_Basic", cols: 80, rows: 24, in: "\x1b[3;3H\r", cur: at(0, 2)},
	{name: "CR_MovesToLeftMarginWhenRightOfLeftMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[1;6H\r\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "CR_MovesToLeftOfScreenWhenLeftOfLeftMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[1;4H\r\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "CR_StaysPutWhenAtLeftMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[1;5H\r\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "CR_MovesToLeftMarginWhenLeftOfLeftMarginInOriginMode", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[?6h\x1b[1;4H\r\x1b[?69lx\x1b[?6l", gap: "DECLRMM left/right margins"},
	{name: "CUB_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[D", cur: at(3, 2)},
	{name: "CUB_ExplicitParam", cols: 80, rows: 24, in: "\x1b[4;5H\x1b[2D", cur: at(2, 3)},
	{name: "CUB_StopsAtLeftEdge", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[99D", cur: at(0, 2)},
	{name: "CUB_StopsAtLeftEdgeWhenBegunLeftOfScrollRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[3;4H\x1b[99D", gap: "DECLRMM left/right margins"},
	{name: "CUB_StopsAtLeftMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[3;7H\x1b[99D", gap: "DECLRMM left/right margins"},
	{name: "CUB_AfterNoWrappedInlines", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[3;1H******************************************************************************\n******************************************************************************\n\x1b[5;5H\x1b[160D", cur: at(4, 2)},
	{name: "CUB_AfterOneWrappedInline", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?45h\x1b[3;1H********************************************************************************************************************************************************************\n********************************************************************************************************************************************************************\x1b[400D", cur: at(8, 2)},
	{name: "CUD_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[B", cur: at(4, 3)},
	{name: "CUD_ExplicitParam", cols: 80, rows: 24, in: "\x1b[3;1H\x1b[2B", cur: at(0, 4)},
	{name: "CUD_StopsAtBottomLine", cols: 80, rows: 24, in: "\x1b[3;1H\x1b[24B", cur: at(0, 23)},
	{name: "CUD_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[6;1H\x1b[24B", cur: at(0, 23)},
	{name: "CUD_StopsAtBottomMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[2;4r\x1b[3;1H\x1b[99B", cur: at(0, 3)},
	{name: "CUF_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[C", cur: at(5, 2)},
	{name: "CUF_ExplicitParam", cols: 80, rows: 24, in: "\x1b[2;1H\x1b[2C", cur: at(2, 1)},
	{name: "CUF_StopsAtRightSide", cols: 80, rows: 24, in: "\x1b[3;1H\x1b[80C", cur: at(79, 2)},
	{name: "CUF_StopsAtRightEdgeWhenBegunRightOfScrollRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[3;12H\x1b[80C", gap: "DECLRMM left/right margins"},
	{name: "CUF_StopsAtRightMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;10s\x1b[3;7H\x1b[80C", gap: "DECLRMM left/right margins"},
	{name: "CUP_DefaultParams", cols: 80, rows: 24, in: "\x1b[3;6H", cur: at(5, 2)},
	{name: "CUP_DefaultParams#2", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[H", cur: at(0, 0)},
	{name: "CUP_RowOnly", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[2H", cur: at(0, 1)},
	{name: "CUP_ColumnOnly", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[;2H", cur: at(1, 0)},
	{name: "CUP_ZeroIsTreatedAsOne", cols: 80, rows: 24, in: "\x1b[3;6H\x1b[0;0H", cur: at(0, 0)},
	{name: "CUP_OutOfBoundsParams", cols: 80, rows: 24, in: "\x1b[34;90H", cur: at(79, 23)},
	{name: "CUP_RespectsOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[9;7H\x1b[?6h\x1b[1;1HX\x1b[?6l\x1b[r\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "HVP_DefaultParams", cols: 80, rows: 24, in: "\x1b[3;6f", cur: at(5, 2)},
	{name: "HVP_DefaultParams#2", cols: 80, rows: 24, in: "\x1b[3;6f\x1b[f", cur: at(0, 0)},
	{name: "HVP_RowOnly", cols: 80, rows: 24, in: "\x1b[3;6f\x1b[2f", cur: at(0, 1)},
	{name: "HVP_ColumnOnly", cols: 80, rows: 24, in: "\x1b[3;6f\x1b[;2f", cur: at(1, 0)},
	{name: "HVP_ZeroIsTreatedAsOne", cols: 80, rows: 24, in: "\x1b[3;6f\x1b[0;0f", cur: at(0, 0)},
	{name: "HVP_OutOfBoundsParams", cols: 80, rows: 24, in: "\x1b[34;90f", cur: at(79, 23)},
	{name: "HVP_RespectsOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[9;7f\x1b[?6h\x1b[1;1fX\x1b[?6l\x1b[r\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "CUU_DefaultParam", cols: 80, rows: 24, in: "\x1b[3;5H\x1b[A", cur: at(4, 1)},
	{name: "CUU_ExplicitParam", cols: 80, rows: 24, in: "\x1b[3;1H\x1b[2A", cur: at(0, 0)},
	{name: "CUU_StopsAtTopLine", cols: 80, rows: 24, in: "\x1b[3;1H\x1b[99A", cur: at(0, 0)},
	{name: "CUU_StopsAtTopLineWhenBegunAboveScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[3;1H\x1b[99A", cur: at(0, 0)},
	{name: "CUU_StopsAtTopMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[2;4r\x1b[3;1H\x1b[99A", cur: at(0, 1)},
	{name: "HPA_DefaultParams", cols: 80, rows: 24, in: "\x1b[6`", cur: at(5, 0)},
	{name: "HPA_DefaultParams#2", cols: 80, rows: 24, in: "\x1b[6`\x1b[`", cur: at(0, 0)},
	{name: "HPA_StopsAtRightEdge", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[90`", cur: at(79, 5)},
	{name: "HPA_DoesNotChangeRow", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[2`", cur: at(1, 5)},
	{name: "HPA_IgnoresOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[9;7H\x1b[?6h\x1b[2`", gap: "DECLRMM left/right margins"},
	{name: "HPR_DefaultParams", cols: 80, rows: 24, in: "\x1b[1;6H\x1b[a", cur: at(6, 0)},
	{name: "HPR_StopsAtRightEdge", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[90a", cur: at(79, 5)},
	{name: "HPR_DoesNotChangeRow", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[2a", cur: at(6, 5)},
	{name: "HPR_IgnoresOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[?6h\x1b[2;2HX\x1b[2aY\x1b[?6l\x1b[?69h\x1b[r", gap: "DECLRMM left/right margins"},
	{name: "HTS_Basic", cols: 80, rows: 24, in: "\x1b[3g\x1b[1;20H\x1bH\x1b[1;1H\t", cur: at(19, 0)},
	{name: "HTS_8bit", cols: 80, rows: 24, in: "\x1b[3g\x1b[1;20H\x1b G\x1b F\x1b[1;1H\t", gap: "8-bit C1 controls"},
	{name: "TBC_Default", cols: 80, rows: 24, in: "\t", cur: at(8, 0)},
	{name: "TBC_Default#2", cols: 80, rows: 24, in: "\t\x1b[g\x1b[1;1H\t", cur: at(16, 0)},
	{name: "TBC_0", cols: 80, rows: 24, in: "\t", cur: at(8, 0)},
	{name: "TBC_0#2", cols: 80, rows: 24, in: "\t\x1b[0g\x1b[1;1H\t", cur: at(16, 0)},
	{name: "TBC_3", cols: 80, rows: 24, in: "\x1b[3g\x1b[1;30H\x1bH\x1b[1;1H\t", cur: at(29, 0)},
	{name: "TBC_NoOp", cols: 80, rows: 24, in: "\x1b[1;10H\x1b[0g\x1b[1;1H\t", cur: at(8, 0)},
	{name: "TBC_NoOp#2", cols: 80, rows: 24, in: "\x1b[1;10H\x1b[0g\x1b[1;1H\t\t", cur: at(16, 0)},
	{name: "VPA_DefaultParams", cols: 80, rows: 24, in: "\x1b[6d", cur: at(0, 5)},
	{name: "VPA_DefaultParams#2", cols: 80, rows: 24, in: "\x1b[6d\x1b[d", cur: at(0, 0)},
	{name: "VPA_StopsAtBottomEdge", cols: 80, rows: 24, in: "\x1b[5;6H\x1b[34d", cur: at(5, 23)},
	{name: "VPA_DoesNotChangeColumn", cols: 80, rows: 24, in: "\x1b[5;6H\x1b[2d", cur: at(5, 1)},
	{name: "VPA_IgnoresOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[9;7H\x1b[?6h\x1b[2d", gap: "DECLRMM left/right margins"},
	{name: "VPR_DefaultParams", cols: 80, rows: 24, in: "\x1b[6;1H\x1b[e", cur: at(0, 6)},
	{name: "VPR_StopsAtBottomEdge", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[34e", cur: at(4, 23)},
	{name: "VPR_DoesNotChangeColumn", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[2e", cur: at(4, 7)},
	{name: "VPR_IgnoresOriginMode", cols: 80, rows: 24, in: "\x1b[6;11r\x1b[?69h\x1b[5;10s\x1b[?6h\x1b[2;2HX\x1b[2eY\x1b[?6l\x1b[?69h\x1b[r", gap: "DECLRMM left/right margins"},
	{name: "REP_DefaultParam", cols: 80, rows: 24, in: "a\x1b[b", check: escRect(1, 1, "aa ")},
	{name: "REP_ExplicitParam", cols: 80, rows: 24, in: "a\x1b[2b", check: escRect(1, 1, "aaa ")},
	{name: "REP_RespectsLeftRightMargins", cols: 80, rows: 24, in: "\x1b[?69h\x1b[2;4s\x1b[1;2Ha\x1b[3b\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "REP_RespectsTopBottomMargins", cols: 80, rows: 24, in: "\x1b[2;4r\x1b[4;78Ha\x1b[3b", check: escRect(1, 3, "                                                                             aaa", "a                                                                               ")},
	{name: "DCH_DefaultParam", cols: 80, rows: 24, in: "abcd\x1b[1;2H\x1b[P", check: escRect(1, 1, "acd ")},
	{name: "DCH_ExplicitParam", cols: 80, rows: 24, in: "abcd\x1b[1;2H\x1b[2P", check: escRect(1, 1, "ad  ")},
	{name: "DCH_RespectsMargins", cols: 80, rows: 24, in: "abcde\x1b[?69h\x1b[2;4s\x1b[1;3H\x1b[P\x1b[?69l", check: escRect(1, 1, "abd e"), gap: "DECLRMM left/right margins"},
	{name: "DCH_DeleteAllWithMargins", cols: 80, rows: 24, in: "abcde\x1b[?69h\x1b[2;4s\x1b[1;3H\x1b[99P\x1b[?69l", check: escRect(1, 1, "ab  e"), gap: "DECLRMM left/right margins"},
	{name: "DCH_DoesNothingOutsideLeftRightMargin", cols: 80, rows: 24, in: "abcde\x1b[?69h\x1b[2;4s\x1b[1;1H\x1b[99P\x1b[?69l", check: escRect(1, 1, "abcde"), gap: "DECLRMM left/right margins"},
	{name: "DCH_WorksOutsideTopBottomMargin", cols: 80, rows: 24, in: "abcde\x1b[2;3r\x1b[1;1H\x1b[99P\x1b[r", check: escRect(1, 1, "     ")},
	{name: "ECH_DefaultParam", cols: 80, rows: 24, in: "abc\x1b[1;1H\x1b[X", check: escRect(1, 1, " bc")},
	{name: "ECH_ExplicitParam", cols: 80, rows: 24, in: "abc\x1b[1;1H\x1b[2X", check: escRect(1, 1, "  c")},
	{name: "ECH_IgnoresScrollRegion", cols: 80, rows: 24, in: "abcdefg\x1b[?69h\x1b[2;4s\x1b[1;3H\x1b[4X\x1b[?69l", check: escRect(1, 1, "ab    g"), gap: "DECLRMM left/right margins"},
	{name: "ECH_OutsideScrollRegion", cols: 80, rows: 24, in: "abcdefg\x1b[?69h\x1b[2;4s\x1b[1;1H\x1b[4X\x1b[?69l", check: escRect(1, 1, "    efg"), gap: "DECLRMM left/right margins"},
	{name: "ECH_doesNotRespectDECPRotection", cols: 80, rows: 24, in: "ab\x1b[1\"qc\x1b[0\"q\x1b[1;1H\x1b[3X", check: escRect(1, 1, "   "), gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "ECH_respectsISOProtection", cols: 80, rows: 24, in: "ab\x1bVc\x1bW\x1b[1;1H\x1b[3X", check: escRect(1, 1, "  c"), gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "ICH_DefaultParam", cols: 80, rows: 24, in: "\x1b[1;1Habcdefg\x1b[1;2H\x1b[@", check: escRect(1, 1, "a bcdefg")},
	{name: "ICH_ExplicitParam", cols: 80, rows: 24, in: "\x1b[1;1Habcdefg\x1b[1;2H\x1b[2@", check: escRect(1, 1, "a  bcdefg")},
	{name: "ICH_IsNoOpWhenCursorBeginsOutsideScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcdefg\x1b[?69h\x1b[2;5s\x1b[1;1H\x1b[10@\x1b[?69l", check: escRect(1, 1, "abcdefg"), gap: "DECLRMM left/right margins"},
	{name: "ICH_ScrollOffRightEdge", cols: 80, rows: 24, in: "\x1b[1;74Habcdefg\x1b[1;75H\x1b[@", check: escAll(escRect(74, 1, "a bcdef"), escRect(1, 2, " "))},
	{name: "ICH_ScrollEntirelyOffRightEdge", cols: 80, rows: 24, in: "\x1b[1;1Hxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\x1b[1;1H\x1b[80@", check: escAll(escRect(1, 1, "                                                                                "), escRect(1, 2, " "))},
	{name: "ICH_ScrollOffRightMarginInScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcdefg\x1b[?69h\x1b[2;5s\x1b[1;3H\x1b[@\x1b[?69l", check: escRect(1, 1, "ab cdfg"), gap: "DECLRMM left/right margins"},
	{name: "IL_DefaultParam", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[3;2H\x1b[L", check: escRect(1, 1, "abcde", "fghij", "     ", "klmno")},
	{name: "IL_ExplicitParam", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[3;2H\x1b[2L", check: escRect(1, 1, "abcde", "fghij", "     ", "     ", "klmno")},
	{name: "IL_ScrollsOffBottom", cols: 80, rows: 24, in: "\x1b[1;1H0001\x1b[2;1H0002\x1b[3;1H0003\x1b[4;1H0004\x1b[5;1H0005\x1b[6;1H0006\x1b[7;1H0007\x1b[8;1H0008\x1b[9;1H0009\x1b[10;1H0010\x1b[11;1H0011\x1b[12;1H0012\x1b[13;1H0013\x1b[14;1H0014\x1b[15;1H0015\x1b[16;1H0016\x1b[17;1H0017\x1b[18;1H0018\x1b[19;1H0019\x1b[20;1H0020\x1b[21;1H0021\x1b[22;1H0022\x1b[23;1H0023\x1b[24;1H0024\x1b[2;1H\x1b[L", check: escRect(1, 1, "0001", "    ", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023")},
	{name: "IL_RespectsScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1HfGHIj\x1b[3;1HkLMNo\x1b[4;1HpQRSt\x1b[5;1Huvwxy\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;3H\x1b[L\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "f   j", "kGHIo", "pLMNt", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "IL_RespectsScrollRegion_Over", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1HfGHIj\x1b[3;1HkLMNo\x1b[4;1HpQRSt\x1b[5;1Huvwxy\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;3H\x1b[99L\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "f   j", "k   o", "p   t", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "IL_AboveScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1HfGHIj\x1b[3;1HkLMNo\x1b[4;1HpQRSt\x1b[5;1Huvwxy\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;3H\x1b[1;1H\x1b[L\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "fGHIj", "kLMNo", "pQRSt", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "DL_DefaultParam", cols: 80, rows: 24, in: "\x1b[1;1H0001\x1b[2;1H0002\x1b[3;1H0003\x1b[4;1H0004\x1b[5;1H0005\x1b[6;1H0006\x1b[7;1H0007\x1b[8;1H0008\x1b[9;1H0009\x1b[10;1H0010\x1b[11;1H0011\x1b[12;1H0012\x1b[13;1H0013\x1b[14;1H0014\x1b[15;1H0015\x1b[16;1H0016\x1b[17;1H0017\x1b[18;1H0018\x1b[19;1H0019\x1b[20;1H0020\x1b[21;1H0021\x1b[22;1H0022\x1b[23;1H0023\x1b[24;1H0024\x1b[2;1H\x1b[M", check: escRect(1, 1, "0001", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023", "0024", "    ")},
	{name: "DL_ExplicitParam", cols: 80, rows: 24, in: "\x1b[1;1H0001\x1b[2;1H0002\x1b[3;1H0003\x1b[4;1H0004\x1b[5;1H0005\x1b[6;1H0006\x1b[7;1H0007\x1b[8;1H0008\x1b[9;1H0009\x1b[10;1H0010\x1b[11;1H0011\x1b[12;1H0012\x1b[13;1H0013\x1b[14;1H0014\x1b[15;1H0015\x1b[16;1H0016\x1b[17;1H0017\x1b[18;1H0018\x1b[19;1H0019\x1b[20;1H0020\x1b[21;1H0021\x1b[22;1H0022\x1b[23;1H0023\x1b[24;1H0024\x1b[2;1H\x1b[2M", check: escRect(1, 1, "0001", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023", "0024", "    ", "    ")},
	{name: "DL_DeleteMoreThanVisible", cols: 80, rows: 24, in: "\x1b[1;1H0001\x1b[2;1H0002\x1b[3;1H0003\x1b[4;1H0004\x1b[5;1H0005\x1b[6;1H0006\x1b[7;1H0007\x1b[8;1H0008\x1b[9;1H0009\x1b[10;1H0010\x1b[11;1H0011\x1b[12;1H0012\x1b[13;1H0013\x1b[14;1H0014\x1b[15;1H0015\x1b[16;1H0016\x1b[17;1H0017\x1b[18;1H0018\x1b[19;1H0019\x1b[20;1H0020\x1b[21;1H0021\x1b[22;1H0022\x1b[23;1H0023\x1b[24;1H0024\x1b[2;1H\x1b[48M", check: escRect(1, 1, "0001", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ")},
	{name: "DL_InScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2;4r\x1b[2;3H\x1b[M\x1b[r", check: escRect(1, 1, "abcde", "klmno", "pqrst", "     ", "uvwxy")},
	{name: "DL_OutsideScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2;4r\x1b[1;3H\x1b[M\x1b[r", check: escRect(1, 1, "abcde", "fghij", "klmno", "pqrst", "uvwxy")},
	{name: "DL_InLeftRightScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;3H\x1b[M\x1b[?69l", check: escRect(1, 1, "abcde", "flmnj", "kqrso", "pvwxt", "u   y"), gap: "DECLRMM left/right margins"},
	{name: "DL_OutsideLeftRightScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;1H\x1b[M\x1b[?69l", check: escRect(1, 1, "abcde", "fghij", "klmno", "pqrst", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "DL_InLeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;3H\x1b[M\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "flmnj", "kqrso", "p   t", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "DL_ClearOutLeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;3H\x1b[99M\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "f   j", "k   o", "p   t", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "DL_OutsideLeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[1;1H\x1b[M\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "fghij", "klmno", "pqrst", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "EL_Default", cols: 80, rows: 24, in: "\x1b[1;1Habcdefghij\x1b[1;5H\x1b[K", check: escRect(1, 1, "abcd      ")},
	{name: "EL_0", cols: 80, rows: 24, in: "\x1b[1;1Habcdefghij\x1b[1;5H\x1b[0K", check: escRect(1, 1, "abcd      ")},
	{name: "EL_1", cols: 80, rows: 24, in: "\x1b[1;1Habcdefghij\x1b[1;5H\x1b[1K", check: escRect(1, 1, "     fghij")},
	{name: "EL_2", cols: 80, rows: 24, in: "\x1b[1;1Habcdefghij\x1b[1;5H\x1b[2K", check: escRect(1, 1, "          ")},
	{name: "EL_IgnoresScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcdefghij\x1b[1;5H\x1b[?69h\x1b[2;4s\x1b[1;5H\x1b[2K\x1b[?69l", check: escRect(1, 1, "          "), gap: "DECLRMM left/right margins"},
	{name: "EL_doesNotRespectDECProtection", cols: 80, rows: 24, in: "ab\x1b[1\"qc\x1b[0\"q\x1b[1;1H\x1b[2K", check: escRect(1, 1, "   "), gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "EL_respectsISOProtection", cols: 80, rows: 24, in: "ab\x1bVc\x1bW\x1b[1;1H\x1b[2K", check: escRect(1, 1, "  c"), gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "ED_Default", cols: 80, rows: 24, in: "\x1b[1;1Ha\x1b[3;1Hbcd\x1b[5;1He\x1b[3;2H\x1b[J", check: escRect(1, 1, "a  ", "   ", "b  ", "   ", "   ")},
	{name: "ED_0", cols: 80, rows: 24, in: "\x1b[1;1Ha\x1b[3;1Hbcd\x1b[5;1He\x1b[3;2H\x1b[0J", check: escRect(1, 1, "a  ", "   ", "b  ", "   ", "   ")},
	{name: "ED_1", cols: 80, rows: 24, in: "\x1b[1;1Ha\x1b[3;1Hbcd\x1b[5;1He\x1b[3;2H\x1b[1J", check: escRect(1, 1, "   ", "   ", "  d", "   ", "e  ")},
	{name: "ED_2", cols: 80, rows: 24, in: "\x1b[1;1Ha\x1b[3;1Hbcd\x1b[5;1He\x1b[3;2H\x1b[2J", check: escRect(1, 1, "   ", "   ", "   ", "   ", "   ")},
	{name: "ED_3", cols: 80, rows: 24, in: "\x1b[1;1Ha\x1b[3;1Hbcd\x1b[5;1He\x1b[3;2H\x1b[3J", check: escRect(1, 1, "a  ", "   ", "bcd", "   ", "e  ")},
	{name: "ED_0_WithScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[3;2H\x1b[?69h\x1b[2;4s\x1b[2;3r\x1b[2;3H\x1b[0J\x1b[?69l\x1b[r", check: escRect(1, 1, "abcde", "fg   ", "     "), gap: "DECLRMM left/right margins"},
	{name: "ED_1_WithScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[3;2H\x1b[?69h\x1b[2;4s\x1b[2;3r\x1b[2;3H\x1b[1J\x1b[?69l\x1b[r", check: escRect(1, 1, "     ", "   ij", "klmno"), gap: "DECLRMM left/right margins"},
	{name: "ED_2_WithScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[3;2H\x1b[?69h\x1b[2;4s\x1b[2;3r\x1b[2;3H\x1b[2J\x1b[?69l\x1b[r", check: escRect(1, 1, "     ", "     ", "     "), gap: "DECLRMM left/right margins"},
	{name: "ED_doesNotRespectDECProtection", cols: 80, rows: 24, in: "ab\x1b[1\"qc\x1b[0\"q\x1b[1;1H\x1b[0J", check: escRect(1, 1, "   "), gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "ED_respectsISOProtection", cols: 80, rows: 24, in: "ab\x1bVc\x1bW\x1b[1;1H\x1b[0J", check: escRect(1, 1, "  c"), gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "SU_DefaultParam", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[S", check: escRect(1, 1, "fghij", "klmno", "pqrst", "uvwxy", "     ")},
	{name: "SU_ExplicitParam", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2S", check: escRect(1, 1, "klmno", "pqrst", "uvwxy", "     ", "     ")},
	{name: "SU_CanClearScreen", cols: 80, rows: 24, in: "\x1b[1;1H0001\x1b[2;1H0002\x1b[3;1H0003\x1b[4;1H0004\x1b[5;1H0005\x1b[6;1H0006\x1b[7;1H0007\x1b[8;1H0008\x1b[9;1H0009\x1b[10;1H0010\x1b[11;1H0011\x1b[12;1H0012\x1b[13;1H0013\x1b[14;1H0014\x1b[15;1H0015\x1b[16;1H0016\x1b[17;1H0017\x1b[18;1H0018\x1b[19;1H0019\x1b[20;1H0020\x1b[21;1H0021\x1b[22;1H0022\x1b[23;1H0023\x1b[24;1H0024\x1b[24S", check: escRect(1, 1, "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ")},
	{name: "SU_RespectsTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2;4r\x1b[2;3H\x1b[2S\x1b[r", check: escRect(1, 1, "abcde", "pqrst", "     ", "     ", "uvwxy")},
	{name: "SU_OutsideTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2;4r\x1b[1;1H\x1b[2S\x1b[r", check: escRect(1, 1, "abcde", "pqrst", "     ", "     ", "uvwxy")},
	{name: "SU_RespectsLeftRightScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;3H\x1b[2S\x1b[?69l", check: escRect(1, 1, "almne", "fqrsj", "kvwxo", "p   t", "u   y"), gap: "DECLRMM left/right margins"},
	{name: "SU_OutsideLeftRightScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;1H\x1b[2S\x1b[r\x1b[?69l", check: escRect(1, 1, "almne", "fqrsj", "kvwxo", "p   t", "u   y"), gap: "DECLRMM left/right margins"},
	{name: "SU_LeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;1H\x1b[2S\x1b[r\x1b[?69l", check: escRect(1, 1, "abcde", "fqrsj", "k   o", "p   t", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "SU_BigScrollLeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;1H\x1b[99S\x1b[r\x1b[?69l", check: escRect(1, 1, "abcde", "f   j", "k   o", "p   t", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "SD_DefaultParam", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[T", check: escRect(1, 1, "     ", "abcde", "fghij", "klmno", "pqrst")},
	{name: "SD_ExplicitParam", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2T", check: escRect(1, 1, "     ", "     ", "abcde", "fghij", "klmno")},
	{name: "SD_CanClearScreen", cols: 80, rows: 24, in: "\x1b[1;1H0001\x1b[2;1H0002\x1b[3;1H0003\x1b[4;1H0004\x1b[5;1H0005\x1b[6;1H0006\x1b[7;1H0007\x1b[8;1H0008\x1b[9;1H0009\x1b[10;1H0010\x1b[11;1H0011\x1b[12;1H0012\x1b[13;1H0013\x1b[14;1H0014\x1b[15;1H0015\x1b[16;1H0016\x1b[17;1H0017\x1b[18;1H0018\x1b[19;1H0019\x1b[20;1H0020\x1b[21;1H0021\x1b[22;1H0022\x1b[23;1H0023\x1b[24;1H0024\x1b[24T", check: escRect(1, 1, "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ", "    ")},
	{name: "SD_RespectsTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2;4r\x1b[2;3H\x1b[2T\x1b[r", check: escRect(1, 1, "abcde", "     ", "     ", "fghij", "uvwxy")},
	{name: "SD_OutsideTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[2;4r\x1b[1;1H\x1b[2T\x1b[r", check: escRect(1, 1, "abcde", "     ", "     ", "fghij", "uvwxy")},
	{name: "SD_RespectsLeftRightScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;3H\x1b[2T\x1b[?69l", check: escRect(1, 1, "a   e", "f   j", "kbcdo", "pghit", "ulmny"), gap: "DECLRMM left/right margins"},
	{name: "SD_OutsideLeftRightScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;1H\x1b[2T\x1b[r\x1b[?69l", check: escRect(1, 1, "a   e", "f   j", "kbcdo", "pghit", "ulmny", " qrs ", " vwx "), gap: "DECLRMM left/right margins"},
	{name: "SD_LeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;1H\x1b[2T\x1b[r\x1b[?69l", check: escRect(1, 1, "abcde", "f   j", "k   o", "pghit", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "SD_BigScrollLeftRightAndTopBottomScrollRegion", cols: 80, rows: 24, in: "\x1b[1;1Habcde\x1b[2;1Hfghij\x1b[3;1Hklmno\x1b[4;1Hpqrst\x1b[5;1Huvwxy\x1b[2;3H\x1b[?69h\x1b[2;4s\x1b[2;4r\x1b[2;1H\x1b[99T\x1b[r\x1b[?69l", check: escRect(1, 1, "abcde", "f   j", "k   o", "p   t", "uvwxy"), gap: "DECLRMM left/right margins"},
	{name: "DECALN_FillsScreen", cols: 80, rows: 24, in: "\x1b#8", check: escAll(escRect(1, 1, "E"), escRect(80, 1, "E"), escRect(1, 24, "E"), escRect(80, 24, "E"), escRect(40, 12, "E"))},
	{name: "DECALN_MovesCursorHome", cols: 80, rows: 24, in: "\x1b[5;5H\x1b#8", cur: at(0, 0)},
	{name: "DECALN_ClearsMargins", cols: 80, rows: 24, in: "\x1b[?69h\x1b[2;3s\x1b[4;5r\x1b#8\x1b[4;2H\x1b[A", cur: at(1, 2), gap: "DECLRMM left/right margins"},
	{name: "DECALN_ClearsMargins#2", cols: 80, rows: 24, in: "\x1b[?69h\x1b[2;3s\x1b[4;5r\x1b#8\x1b[5;2H\x1b[B", cur: at(1, 5), gap: "DECLRMM left/right margins"},
	{name: "DECALN_ClearsMargins#3", cols: 80, rows: 24, in: "\x1b[?69h\x1b[2;3s\x1b[4;5r\x1b#8\x1b[4;2H\x1b[D", cur: at(0, 3), gap: "DECLRMM left/right margins"},
	{name: "DECALN_ClearsMargins#4", cols: 80, rows: 24, in: "\x1b[?69h\x1b[2;3s\x1b[4;5r\x1b#8\x1b[4;3H\x1b[C", cur: at(3, 3), gap: "DECLRMM left/right margins"},
	{name: "vttest DECALN fill", cols: 80, rows: 24, in: "junk\x1b[10;10Hmore\x1b#8", screen: scr("EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE\nEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE"), cur: at(0, 0)},
	{name: "vttest DECALN resets margins/cursor", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;3H\x1b#8\x1b[4;2H\x1b[A", cur: at(1, 2)},
	{name: "vttest DECALN resets margins/cursor#2", cols: 80, rows: 24, in: "\x1b[4;5r\x1b#8\x1b[5;2H\x1b[B", cur: at(1, 5)},
	{name: "vttest DECALN resets margins/cursor#3", cols: 80, rows: 24, in: "\x1b[4;5r\x1b#8\x1b[24;1H\n", cur: at(0, 23), check: escAll(escRect(1, 23, "E"), escRect(1, 24, " "))},
	{name: "vttest DECALN then ED 2", cols: 80, rows: 24, in: "\x1b#8\x1b[2J", screen: scr(""), cur: at(0, 0)},
	{name: "DECSTBM_ScrollsOnNewline", cols: 80, rows: 24, in: "\x1b[2;3r\x1b[2;1H1\r\n2", check: escRect(1, 2, "1", "2")},
	{name: "DECSTBM_ScrollsOnNewline#2", cols: 80, rows: 24, in: "\x1b[2;3r\x1b[2;1H1\r\n2\r\n", cur: at(0, 2), check: escRect(1, 2, "2", " ")},
	{name: "DECSTBM_NewlineBelowRegion", cols: 80, rows: 24, in: "\x1b[2;3r\x1b[2;1H1\r\n2\x1b[4;1H\r\n", check: escRect(1, 2, "1", "2")},
	{name: "DECSTBM_MovsCursorToOrigin", cols: 80, rows: 24, in: "\x1b[2;3H\x1b[2;3r", cur: at(0, 0)},
	{name: "DECSTBM_TopBelowBottom", cols: 80, rows: 24, in: "\x1b[3;3r0000\r\n0001\r\n0002\r\n0003\r\n0004\r\n0005\r\n0006\r\n0007\r\n0008\r\n0009\r\n0010\r\n0011\r\n0012\r\n0013\r\n0014\r\n0015\r\n0016\r\n0017\r\n0018\r\n0019\r\n0020\r\n0021\r\n0022\r\n0023", check: escRect(1, 1, "0000", "0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023")},
	{name: "DECSTBM_TopBelowBottom#2", cols: 80, rows: 24, in: "\x1b[3;3r0000\r\n0001\r\n0002\r\n0003\r\n0004\r\n0005\r\n0006\r\n0007\r\n0008\r\n0009\r\n0010\r\n0011\r\n0012\r\n0013\r\n0014\r\n0015\r\n0016\r\n0017\r\n0018\r\n0019\r\n0020\r\n0021\r\n0022\r\n0023\x1b[24;1H\n", check: escRect(1, 1, "0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023", "    ")},
	{name: "DECSTBM_DefaultRestores", cols: 80, rows: 24, in: "\x1b[2;3r\x1b[2;1H1\r\n2", cur: at(1, 2), check: escRect(1, 2, "1", "2")},
	{name: "DECSTBM_DefaultRestores#2", cols: 80, rows: 24, in: "\x1b[2;3r\x1b[2;1H1\r\n2\x1b[r\x1b[3;2H\r\n", cur: at(0, 3), check: escRect(1, 2, "1", "2")},
	{name: "DECSTBM_CursorBelowRegionAtBottomTriesToScroll", cols: 80, rows: 24, in: "\x1b[2;3r\x1b[2;1H1\r\n2\x1b[24;1H3\r\n", cur: at(0, 23), check: escAll(escRect(1, 2, "1", "2"), escRect(1, 24, "3"))},
	{name: "DECSTBM_MaxSizeOfRegionIsPageSize", cols: 80, rows: 24, in: "\x1b[2;1Hx\x1b[1;34r\x1b[24;1H\r\n", cur: at(0, 23), check: escRect(1, 1, "x", " ")},
	{name: "DECSTBM_TopOfZeroIsTopOfScreen", cols: 80, rows: 24, in: "\x1b[0;3r\x1b[2;1H1\r\n2\r\n3\r\n4", check: escRect(1, 1, "2", "3", "4")},
	{name: "DECSTBM_BottomOfZeroIsBottomOfScreen", cols: 80, rows: 24, in: "\x1b[3;1Hx\x1b[2;0r\x1b[24;1H\r\n", cur: at(0, 23), check: escRect(1, 2, "x", " ")},
	{name: "vttest BCE EL", cols: 80, rows: 24, in: "abc\x1b[44m\x1b[1;2H\x1b[0K", check: escAll(escRect(1, 1, "a"), func(t *Terminal) string {
		for _, p := range [][2]int{{1, 0}, {79, 0}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(4) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(4)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	})},
	{name: "vttest BCE ED", cols: 80, rows: 24, in: "abc\x1b[41m\x1b[2J", check: func(t *Terminal) string {
		for _, p := range [][2]int{{0, 0}, {79, 23}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(1) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(1)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	}},
	{name: "vttest BCE ECH", cols: 80, rows: 24, in: "abcd\x1b[42m\x1b[1;2H\x1b[2X", check: escAll(escRect(1, 1, "a  d"), func(t *Terminal) string {
		for _, p := range [][2]int{{1, 0}, {2, 0}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(2) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(2)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	})},
	{name: "vttest BCE DCH", cols: 80, rows: 24, in: "abcd\x1b[43m\x1b[1;1H\x1b[1P", check: escAll(escRect(1, 1, "bcd"), func(t *Terminal) string {
		for _, p := range [][2]int{{79, 0}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(3) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(3)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	})},
	{name: "vttest BCE ICH", cols: 80, rows: 24, in: "abcd\x1b[45m\x1b[1;1H\x1b[1@", check: escAll(escRect(1, 1, " abcd"), func(t *Terminal) string {
		for _, p := range [][2]int{{0, 0}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(5) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(5)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	})},
	{name: "vttest BCE IL", cols: 80, rows: 24, in: "abcd\x1b[46m\x1b[1;1H\x1b[1L", check: escAll(escRect(1, 2, "abcd"), func(t *Terminal) string {
		for _, p := range [][2]int{{0, 0}, {79, 0}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(6) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(6)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	})},
	{name: "vttest BCE SU", cols: 80, rows: 24, in: "abcd\x1b[44m\x1b[1S", check: func(t *Terminal) string {
		for _, p := range [][2]int{{0, 23}, {79, 23}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(4) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(4)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	}},
	{name: "vttest BCE scroll on LF", cols: 80, rows: 24, in: "\x1b[44m\x1b[24;1H\n", check: func(t *Terminal) string {
		for _, p := range [][2]int{{0, 23}, {79, 23}} {
			if c := t.cellAt(p[0], p[1]); c.Bg != idx(4) || c.R != 0 && c.R != ' ' {
				return fmt.Sprintf("cell %d,%d: %q bg %#x, want blank with bg idx(4)", p[0], p[1], string(c.R), c.Bg)
			}
		}
		return ""
	}},
	// IND / LF / NEL / RI
	{name: "IND_Basic", cols: 80, rows: 24, in: "\x1b[3;5H\x1bD", cur: at(4, 3)},
	{name: "IND_Scrolls", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\x1bD", cur: at(1, 23), check: escRect(2, 22, " ", "a", "b")},
	{name: "IND_Scrolls#2", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\x1bD\x1bD", cur: at(1, 23), check: escRect(2, 22, "a", "b", " ")},
	{name: "IND_ScrollsInTopBottomRegionStartingAbove", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[3;2H\x1bD\x1bD\x1bD", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "IND_ScrollsInTopBottomRegionStartingWithin", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[4;2H\x1bD\x1bD", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "IND_MovesDoesNotScrollOutsideLeftRight", cols: 80, rows: 24, in: "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[5;3Hx\x1b[5;6H\x1bD", gap: "DECLRMM left/right margins"},
	{name: "IND_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[6;1Hx" + strings.Repeat("\x1bD", 24), check: escAll(escRect(1, 6, "x"), func(t *Terminal) string {
		if _, y, _ := t.Cursor(); y != 23 {
			return fmt.Sprintf("cursor row %d want 23", y)
		}
		return ""
	})},
	{name: "IND_8bit", cols: 80, rows: 24, in: "\x1b[3;5H\x1b G\x84\x1b F", gap: "8-bit C1 controls"},

	{name: "LF_Basic", cols: 80, rows: 24, in: "\x1b[3;5H\n", cur: at(4, 3)},
	{name: "LF_Scrolls", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\n", cur: at(1, 23), check: escRect(2, 22, " ", "a", "b")},
	{name: "LF_Scrolls#2", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\n\n", cur: at(1, 23), check: escRect(2, 22, "a", "b", " ")},
	{name: "LF_ScrollsInTopBottomRegionStartingAbove", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[3;2H\n\n\n", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "LF_ScrollsInTopBottomRegionStartingWithin", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[4;2H\n\n", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "LF_MovesDoesNotScrollOutsideLeftRight", cols: 80, rows: 24, in: "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[5;3Hx\x1b[5;6H\n", gap: "DECLRMM left/right margins"},
	{name: "LF_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[6;1Hx" + strings.Repeat("\n", 24), check: escAll(escRect(1, 6, "x"), func(t *Terminal) string {
		if _, y, _ := t.Cursor(); y != 23 {
			return fmt.Sprintf("cursor row %d want 23", y)
		}
		return ""
	})},

	{name: "NEL_Basic", cols: 80, rows: 24, in: "\x1b[3;5H\x1bE", cur: at(0, 3)},
	{name: "NEL_Scrolls", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\x1bE", cur: at(0, 23), check: escRect(2, 22, " ", "a", "b")},
	{name: "NEL_Scrolls#2", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\x1bE\x1bE", cur: at(0, 23), check: escRect(2, 22, "a", "b", " ")},
	{name: "NEL_ScrollsInTopBottomRegionStartingAbove", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[3;2H\x1bE\x1bE\x1bE", cur: at(0, 4), check: escRect(2, 4, "x", " ")},
	{name: "NEL_ScrollsInTopBottomRegionStartingWithin", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[4;2H\x1bE\x1bE", cur: at(0, 4), check: escRect(2, 4, "x", " ")},
	{name: "NEL_MovesDoesNotScrollOutsideLeftRight", cols: 80, rows: 24, in: "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[5;3Hx\x1b[5;6H\x1bE", gap: "DECLRMM left/right margins"},
	{name: "NEL_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[6;1Hx" + strings.Repeat("\x1bE", 24), cur: at(0, 23), check: escRect(1, 6, "x")},
	{name: "NEL_8bit", cols: 80, rows: 24, in: "\x1b[3;5H\x1b G\x85\x1b F", gap: "8-bit C1 controls"},

	{name: "RI_Basic", cols: 80, rows: 24, in: "\x1b[3;5H\x1bM", cur: at(4, 1)},
	{name: "RI_Scrolls", cols: 80, rows: 24, in: "\x1b[1;2Ha\x1b[2;2Hb\x1b[2;2H\x1bM", cur: at(1, 0), check: escRect(2, 1, "a", "b", " ")},
	{name: "RI_Scrolls#2", cols: 80, rows: 24, in: "\x1b[1;2Ha\x1b[2;2Hb\x1b[2;2H\x1bM\x1bM", cur: at(1, 0), check: escRect(2, 1, " ", "a", "b")},
	{name: "RI_ScrollsInTopBottomRegionStartingBelow", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[4;2Hx\x1b[6;2H\x1bM\x1bM\x1bM", cur: at(1, 3), check: escRect(2, 4, " ", "x")},
	{name: "RI_ScrollsInTopBottomRegionStartingWithin", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[4;2Hx\x1b[5;2H\x1bM\x1bM", cur: at(1, 3), check: escRect(2, 4, " ", "x")},
	{name: "RI_MovesDoesNotScrollOutsideLeftRight", cols: 80, rows: 24, in: "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[5;3Hx\x1b[2;6H\x1bM", gap: "DECLRMM left/right margins"},
	{name: "RI_StopsAtTopLineWhenBegunAboveScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[3;1Hx" + strings.Repeat("\x1bM", 24), check: escAll(escRect(1, 3, "x"), func(t *Terminal) string {
		if _, y, _ := t.Cursor(); y != 0 {
			return fmt.Sprintf("cursor row %d want 0", y)
		}
		return ""
	})},
	{name: "RI_8bit", cols: 80, rows: 24, in: "\x1b[3;5H\x1b G\x8d\x1b F", gap: "8-bit C1 controls"},

	// FF / VT behave like LF
	{name: "FF_Basic", cols: 80, rows: 24, in: "\x1b[3;5H\f", cur: at(4, 3)},
	{name: "FF_Scrolls", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\f", cur: at(1, 23), check: escRect(2, 22, " ", "a", "b")},
	{name: "FF_Scrolls#2", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\f\f", cur: at(1, 23), check: escRect(2, 22, "a", "b", " ")},
	{name: "FF_ScrollsInTopBottomRegionStartingAbove", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[3;2H\f\f\f", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "FF_ScrollsInTopBottomRegionStartingWithin", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[4;2H\f\f", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "FF_MovesDoesNotScrollOutsideLeftRight", cols: 80, rows: 24, in: "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[5;3Hx\x1b[5;6H\f", gap: "DECLRMM left/right margins"},
	{name: "FF_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[6;1Hx" + strings.Repeat("\f", 24), check: escAll(escRect(1, 6, "x"), func(t *Terminal) string {
		if _, y, _ := t.Cursor(); y != 23 {
			return fmt.Sprintf("cursor row %d want 23", y)
		}
		return ""
	})},

	{name: "VT_Basic", cols: 80, rows: 24, in: "\x1b[3;5H\v", cur: at(4, 3)},
	{name: "VT_Scrolls", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\v", cur: at(1, 23), check: escRect(2, 22, " ", "a", "b")},
	{name: "VT_Scrolls#2", cols: 80, rows: 24, in: "\x1b[23;2Ha\x1b[24;2Hb\x1b[23;2H\v\v", cur: at(1, 23), check: escRect(2, 22, "a", "b", " ")},
	{name: "VT_ScrollsInTopBottomRegionStartingAbove", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[3;2H\v\v\v", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "VT_ScrollsInTopBottomRegionStartingWithin", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[5;2Hx\x1b[4;2H\v\v", cur: at(1, 4), check: escRect(2, 4, "x", " ")},
	{name: "VT_MovesDoesNotScrollOutsideLeftRight", cols: 80, rows: 24, in: "\x1b[2;5r\x1b[?69h\x1b[2;5s\x1b[5;3Hx\x1b[5;6H\v", gap: "DECLRMM left/right margins"},
	{name: "VT_StopsAtBottomLineWhenBegunBelowScrollRegion", cols: 80, rows: 24, in: "\x1b[4;5r\x1b[6;1Hx" + strings.Repeat("\v", 24), check: escAll(escRect(1, 6, "x"), func(t *Terminal) string {
		if _, y, _ := t.Cursor(); y != 23 {
			return fmt.Sprintf("cursor row %d want 23", y)
		}
		return ""
	})},

	// RIS
	{name: "RIS_ClearsScreen", cols: 80, rows: 24, in: "x\x1bc", check: escRect(1, 1, " ")},
	{name: "RIS_CursorToOrigin", cols: 80, rows: 24, in: "\x1b[6;5H\x1bc", cur: at(0, 0)},
	{name: "RIS_ResetTabs", cols: 80, rows: 24, in: "\x1bH\x1b[C\x1bH\x1b[C\x1bH\x1bc\t", cur: at(8, 0)},
	{name: "RIS_ResetTitleMode", cols: 80, rows: 24, in: "\x1b[>2;3T\x1b[>0;1t\x1bc\x1b]2;ab\x1b\\\x1b]2;a\x1b\\", check: func(t *Terminal) string {
		if t.Title() != "a" {
			return fmt.Sprintf("title %q want %q", t.Title(), "a")
		}
		return ""
	}},
	{name: "RIS_ExitAltScreen", cols: 80, rows: 24, in: "m\x1b[?47h\x1b[1;1Ha\x1bc", alt: no(), check: escRect(1, 1, " ")},
	{name: "RIS_ExitAltScreen#2", cols: 80, rows: 24, in: "m\x1b[?47h\x1b[1;1Ha\x1bc\x1b[?47h", alt: yes(), check: escRect(1, 1, " ")},
	{name: "RIS_ResetDECCOLM", cols: 80, rows: 24, in: "\x1b[?40h\x1b[?3h\x1bc", gap: "DECCOLM 132-column mode"},
	{name: "RIS_ResetDECOM", cols: 80, rows: 24, in: "\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h\x1bc\x1b[1;1HX", gap: "DECLRMM left/right margins"},
	{name: "RIS_RemoveMargins", cols: 80, rows: 24, in: "\x1b[?69h\x1b[3;5s\x1b[4;6r\x1bc\x1b[4;3H\x1b[D", gap: "DECLRMM left/right margins"},

	// DECSTR
	{name: "DECSTR_DECSC", cols: 80, rows: 24, in: "\x1b[6;5H\x1b7\x1b[!p\x1b8", cur: at(0, 0)},
	{name: "DECSTR_IRM", cols: 80, rows: 24, in: "\x1b[4h\x1b[!p\x1b[1;1Ha\x1b[1;1Hb", check: escRect(1, 1, "b")},
	{name: "DECSTR_DECOM", cols: 80, rows: 24, in: "\x1b[3;4r\x1b[?6h\x1b[!p\x1b[?69h\x1b[3;4s\x1b[4;5r\x1b[1;1HX", gap: "DECLRMM left/right margins"},
	{name: "DECSTR_DECAWM", cols: 80, rows: 24, in: "\x1b[?7h\x1b[!p\x1b[1;79Hxxx", cur: at(1, 1)},
	{name: "DECSTR_ReverseWraparound", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[!p\x1b[2;1H\b", cur: at(0, 1)},
	{name: "DECSTR_STBM", cols: 80, rows: 24, in: "\x1b[3;4r\x1b[!p\x1b[4;1H\r\n", cur: at(0, 4)},
	{name: "DECSTR_DECSCA", cols: 80, rows: 24, in: "\x1b[1\"q\x1b[!p\x1b[1;1HX\x1b[?2J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSTR_DECSASD", cols: 80, rows: 24, in: "\x1b[1$}\x1b[!p", cur: at(0, 0)},
	{name: "DECSTR_DECSASD#2", cols: 80, rows: 24, in: "\x1b[1$}\x1b[!pX", check: escRect(1, 1, "X")},
	{name: "DECSTR_DECRLM", cols: 80, rows: 24, in: "\x1b[?34h\x1b[!p\x1b[1;2Hab", check: escRect(2, 1, "ab")},
	{name: "DECSTR_DECLRMM", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;6s\x1b[!p\x1b[5;5Hab", gap: "DECLRMM left/right margins"},
	{name: "DECSTR_CursorStaysPut", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[!p", cur: at(4, 5)},

	// save/restore cursor: DECSC/DECRC, SCOSC/SCORC, DECSET 1048
	{name: "DECRC SaveRestoreCursor_Basic", cols: 80, rows: 24, in: "\x1b[6;5H\x1b7\x1b[1;1H\x1b8", cur: at(4, 5)},
	{name: "DECRC SaveRestoreCursor_MoveToHomeWhenNotSaved", cols: 80, rows: 24, in: "\x1b[!p\x1b[6;5H\x1b8", cur: at(0, 0)},
	{name: "DECRC SaveRestoreCursor_Reset", cols: 80, rows: 24, in: "a\x1b7\x1b[!pb\x1b8c", check: escRect(1, 1, "cb")},
	{name: "DECRC SaveRestoreCursor_ResetsOriginMode", cols: 80, rows: 24, in: "\x1b[6;5H\x1b7\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h\x1b8\x1b[1;1HX", gap: "DECLRMM left/right margins"},
	{name: "DECRC SaveRestoreCursor_WorksInLRM", cols: 80, rows: 24, in: "\x1b[3;2H\x1b7\x1b[?69h\x1b[1;10s\x1b[6;5H\x1b7\x1b[5;4H\x1b8", gap: "DECLRMM left/right margins"},
	{name: "DECRC SaveRestoreCursor_AltVsMain", cols: 80, rows: 24, in: "\x1b[3;2H\x1b7\x1b[?47h\x1b[7;6H\x1b7\x1b[?47l\x1b8", cur: at(1, 2), alt: no()},
	{name: "DECRC SaveRestoreCursor_AltVsMain#2", cols: 80, rows: 24, in: "\x1b[3;2H\x1b7\x1b[?47h\x1b[7;6H\x1b7\x1b[?47l\x1b8\x1b[?47h\x1b8", cur: at(5, 6), alt: yes()},
	{name: "DECRC SaveRestoreCursor_Protection", cols: 80, rows: 24, in: "\x1b[1\"q\x1b7\x1b[0\"q\x1b8a\x1b[1;1;1;1${", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECRC SaveRestoreCursor_Wrap", cols: 80, rows: 24, in: "\x1b[?7h\x1b7\x1b[?7l\x1b8\x1b[1;79Habcd", cur: at(79, 0)},
	{name: "DECRC SaveRestoreCursor_ReverseWrapNotAffected", cols: 80, rows: 24, in: "\x1b[?1045h\x1b7\x1b[?1045l\x1b8\x1b[2;1H\b", cur: at(0, 1)},
	{name: "DECRC SaveRestoreCursor_InsertNotAffected", cols: 80, rows: 24, in: "\x1b[4h\x1b7\x1b[4l\x1b8\x1b[1;1Ha\x1b[1;1Hb", check: escRect(1, 1, "b ")},
	{name: "SCORC SaveRestoreCursor_Basic", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[s\x1b[1;1H\x1b[u", cur: at(4, 5)},
	{name: "SCORC SaveRestoreCursor_MoveToHomeWhenNotSaved", cols: 80, rows: 24, in: "\x1b[!p\x1b[6;5H\x1b[u", cur: at(0, 0)},
	{name: "SCORC SaveRestoreCursor_Reset", cols: 80, rows: 24, in: "a\x1b[s\x1b[!pb\x1b[uc", check: escRect(1, 1, "cb")},
	{name: "SCORC SaveRestoreCursor_ResetsOriginMode", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[s\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h\x1b[u\x1b[1;1HX", gap: "DECLRMM left/right margins"},
	{name: "SCORC SaveRestoreCursor_WorksInLRM", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[s\x1b[?69h\x1b[1;10s\x1b[6;5H\x1b[s\x1b[5;4H\x1b[u", gap: "DECLRMM left/right margins"},
	{name: "SCORC SaveRestoreCursor_AltVsMain", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[s\x1b[?47h\x1b[7;6H\x1b[s\x1b[?47l\x1b[u", cur: at(1, 2), alt: no()},
	{name: "SCORC SaveRestoreCursor_AltVsMain#2", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[s\x1b[?47h\x1b[7;6H\x1b[s\x1b[?47l\x1b[u\x1b[?47h\x1b[u", cur: at(5, 6), alt: yes()},
	{name: "SCORC SaveRestoreCursor_Protection", cols: 80, rows: 24, in: "\x1b[1\"q\x1b[s\x1b[0\"q\x1b[ua\x1b[1;1;1;1${", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "SCORC SaveRestoreCursor_Wrap", cols: 80, rows: 24, in: "\x1b[?7h\x1b[s\x1b[?7l\x1b[u\x1b[1;79Habcd", cur: at(79, 0)},
	{name: "SCORC SaveRestoreCursor_ReverseWrapNotAffected", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[s\x1b[?1045l\x1b[u\x1b[2;1H\b", cur: at(0, 1)},
	{name: "SCORC SaveRestoreCursor_InsertNotAffected", cols: 80, rows: 24, in: "\x1b[4h\x1b[s\x1b[4l\x1b[u\x1b[1;1Ha\x1b[1;1Hb", check: escRect(1, 1, "b ")},
	{name: "DECSET 1048 SaveRestoreCursor_Basic", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[?1048h\x1b[1;1H\x1b[?1048l", cur: at(4, 5)},
	{name: "DECSET 1048 SaveRestoreCursor_MoveToHomeWhenNotSaved", cols: 80, rows: 24, in: "\x1b[!p\x1b[6;5H\x1b[?1048l", cur: at(0, 0)},
	{name: "DECSET 1048 SaveRestoreCursor_Reset", cols: 80, rows: 24, in: "a\x1b[?1048h\x1b[!pb\x1b[?1048lc", check: escRect(1, 1, "cb")},
	{name: "DECSET 1048 SaveRestoreCursor_ResetsOriginMode", cols: 80, rows: 24, in: "\x1b[6;5H\x1b[?1048h\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h\x1b[?1048l\x1b[1;1HX", gap: "DECLRMM left/right margins"},
	{name: "DECSET 1048 SaveRestoreCursor_WorksInLRM", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[?1048h\x1b[?69h\x1b[1;10s\x1b[6;5H\x1b[?1048h\x1b[5;4H\x1b[?1048l", gap: "DECLRMM left/right margins"},
	{name: "DECSET 1048 SaveRestoreCursor_AltVsMain", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[?1048h\x1b[?47h\x1b[7;6H\x1b[?1048h\x1b[?47l\x1b[?1048l", cur: at(1, 2), alt: no()},
	{name: "DECSET 1048 SaveRestoreCursor_AltVsMain#2", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[?1048h\x1b[?47h\x1b[7;6H\x1b[?1048h\x1b[?47l\x1b[?1048l\x1b[?47h\x1b[?1048l", cur: at(5, 6), alt: yes()},
	{name: "DECSET 1048 SaveRestoreCursor_Protection", cols: 80, rows: 24, in: "\x1b[1\"q\x1b[?1048h\x1b[0\"q\x1b[?1048la\x1b[1;1;1;1${", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSET 1048 SaveRestoreCursor_Wrap", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?1048h\x1b[?7l\x1b[?1048l\x1b[1;79Habcd", cur: at(79, 0)},
	{name: "DECSET 1048 SaveRestoreCursor_ReverseWrapNotAffected", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[?1048h\x1b[?1045l\x1b[?1048l\x1b[2;1H\b", cur: at(0, 1)},
	{name: "DECSET 1048 SaveRestoreCursor_InsertNotAffected", cols: 80, rows: 24, in: "\x1b[4h\x1b[?1048h\x1b[4l\x1b[?1048l\x1b[1;1Ha\x1b[1;1Hb", check: escRect(1, 1, "b ")},

	// SGR
	{name: "SGR_Bold", cols: 80, rows: 24, in: "x\x1b[1my", check: func(t *Terminal) string {
		a, b := t.cellAt(0, 0), t.cellAt(1, 0)
		if a.Fg != 0 || a.Bg != 0 || hasAttr(a, attrBold) {
			return fmt.Sprintf("cell 1 got %+v want default colors, not bold", a)
		}
		if b.Fg != 0 || b.Bg != 0 || !hasAttr(b, attrBold) {
			return fmt.Sprintf("cell 2 got %+v want default colors, bold", b)
		}
		return ""
	}},

	// SM / RM
	{name: "SM_IRM", cols: 80, rows: 24, in: "abc\x1b[1;1H\x1b[4hX", check: escRect(1, 1, "Xabc")},
	{name: "SM_IRM_DoesNotWrapUnlessCursorAtMargin", cols: 80, rows: 24, in: strings.Repeat("a", 79) + "b\x1b[1;1H\x1b[4h", check: escRect(1, 2, " ")},
	{name: "SM_IRM_DoesNotWrapUnlessCursorAtMargin#2", cols: 80, rows: 24, in: strings.Repeat("a", 79) + "b\x1b[1;1H\x1b[4hX", check: escRect(1, 2, " ")},
	{name: "SM_IRM_DoesNotWrapUnlessCursorAtMargin#3", cols: 80, rows: 24, in: strings.Repeat("a", 79) + "b\x1b[1;1H\x1b[4hX\x1b[1;80HYZ", check: escRect(1, 2, "Z")},
	{name: "SM_IRM_TruncatesAtRightMargin", cols: 80, rows: 24, in: "\x1b[1;5Habcdef\x1b[?69h\x1b[5;10s\x1b[1;7H\x1b[4hX", gap: "DECLRMM left/right margins"},
	{name: "SM_LNM", cols: 80, rows: 24, in: "\x1b[20l\x1b[1;5H\n", cur: at(4, 1)},
	{name: "SM_LNM#2", cols: 80, rows: 24, in: "\x1b[20h\x1b[1;5H\n", cur: at(0, 1)},
	{name: "SM_LNM#3", cols: 80, rows: 24, in: "\x1b[20l\x1b[1;5H\v", cur: at(4, 1)},
	{name: "SM_LNM#4", cols: 80, rows: 24, in: "\x1b[20h\x1b[1;5H\v", cur: at(0, 1)},
	{name: "SM_LNM#5", cols: 80, rows: 24, in: "\x1b[20l\x1b[1;5H\f", cur: at(4, 1)},
	{name: "SM_LNM#6", cols: 80, rows: 24, in: "\x1b[20h\x1b[1;5H\f", cur: at(0, 1)},
	{name: "RM_IRM", cols: 80, rows: 24, in: "\x1b[4h\x1b[1;1HX\x1b[1;1HW\x1b[1;1H\x1b[4lYZ", check: escRect(1, 1, "YZ")},

	// xterm save/restore of DEC private modes
	{name: "XtermSave_SaveSetState", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?7s\x1b[?7l\x1b[?7r\x1b[1;79Hxxx", cur: at(1, 1)},
	{name: "XtermSave_SaveResetState", cols: 80, rows: 24, in: "\x1b[?7l\x1b[?7s\x1b[?7h\x1b[?7r\x1b[1;79Hxxx", cur: at(79, 0)},

	// DECSET
	{name: "DECSET_DECCOLM", cols: 80, rows: 24, in: "\x1b[?95l\x1b[?40h\x1b[5;5Hxyz\x1b[1;2r\x1b[?69h\x1b[1;2s\x1b[?3h", gap: "DECCOLM 132-column mode"},
	{name: "DECSET_DECOM", cols: 80, rows: 24, in: "\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h\x1b[1;1HX", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECOM_SoftReset", cols: 80, rows: 24, in: "\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h\x1b[!p\x1b[1G\x1b[1dX", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECOM_DECRQCRA", cols: 80, rows: 24, in: "\x1b[5;5HX\x1b[5;7r\x1b[?69h\x1b[5;7s\x1b[?6h", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECAWM", cols: 80, rows: 24, in: "\x1b[1;79H\x1b[?7habc", check: escRect(1, 2, "c")},
	{name: "DECSET_DECAWM#2", cols: 80, rows: 24, in: "\x1b[1;79H\x1b[?7habc\x1b[1;79H\x1b[?7lABC", check: escAll(escRect(79, 1, "AC"), escRect(1, 2, "c"))},
	{name: "DECSET_DECAWM_CursorAtRightMargin", cols: 80, rows: 24, in: "\x1b[?7h" + strings.Repeat("x", 78), cur: at(78, 0)},
	{name: "DECSET_DECAWM_CursorAtRightMargin#2", cols: 80, rows: 24, in: "\x1b[?7h" + strings.Repeat("x", 79), cur: at(79, 0), pending: no()},
	{name: "DECSET_DECAWM_CursorAtRightMargin#3", cols: 80, rows: 24, in: "\x1b[?7h" + strings.Repeat("x", 80), cur: at(79, 0), pending: yes()},
	{name: "DECSET_DECAWM_CursorAtRightMargin#4", cols: 80, rows: 24, in: "\x1b[?7h" + strings.Repeat("x", 81), cur: at(1, 1)},
	{name: "DECSET_DECAWM_OnRespectsLeftRightMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;9s\x1b[5;9r\x1b[9;8H\x1b[?7habcdef", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECAWM_OffRespectsLeftRightMargin", cols: 80, rows: 24, in: "\x1b[?69h\x1b[5;9s\x1b[5;9r\x1b[9;8H\x1b[?7labcdef", gap: "DECLRMM left/right margins"},
	{name: "DECSET_Allow80To132", cols: 80, rows: 24, in: "\x1b[?40h\x1b[?3h", gap: "DECCOLM 132-column mode"},
	{name: "DECSET_DECAWM_TabDoesNotWrapAround", cols: 80, rows: 24, in: "\x1b[?7h" + strings.Repeat("\t", 12), cur: at(79, 0)},
	{name: "DECSET_DECAWM_NoLineWrapOnTabWithLeftRightMargin", cols: 80, rows: 24, in: "\x1b[?7h\x1b[?69h\x1b[10;20s\t\t\t\t", gap: "DECLRMM left/right margins"},
	{name: "DECSET_MoreFix", cols: 80, rows: 24, in: "\x1b[?41h\r\n" + strings.Repeat("x", 80) + "\t", cur: at(8, 2)},
	{name: "DECSET_MoreFix#2", cols: 80, rows: 24, in: "\x1b[?41h\r\n" + strings.Repeat("x", 80) + "\t1", check: escRect(9, 3, "1")},
	{name: "DECSET_MoreFix#3", cols: 80, rows: 24, in: "\x1b[?41h\r\n" + strings.Repeat("x", 80) + "\t1\x1b[?41l\r\n" + strings.Repeat("x", 80) + "\t", cur: at(79, 3)},
	{name: "DECSET_MoreFix#4", cols: 80, rows: 24, in: "\x1b[?41h\r\n" + strings.Repeat("x", 80) + "\t1\x1b[?41l\r\n" + strings.Repeat("x", 80) + "\t2", check: escRect(1, 5, "2")},
	{name: "DECSET_ReverseWraparound_BS", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[?7h\x1b[2;1H\b", cur: at(79, 0)},
	{name: "DECSET_ReverseWraparoundLastCol_BS", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[?7h\x1b[1;79Ha", cur: at(79, 0)},
	{name: "DECSET_ReverseWraparoundLastCol_BS#2", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[?7h\x1b[1;79Hab", cur: at(79, 0)},
	{name: "DECSET_ReverseWraparoundLastCol_BS#3", cols: 80, rows: 24, in: "\x1b[?1045h\x1b[?7h\x1b[1;79Hab\b", cur: at(79, 0)},
	{name: "DECSET_ReverseWraparound_Multi", cols: 80, rows: 24, in: "\x1b[1;79Habcd\x1b[?1045h\x1b[?7h\x1b[4D", cur: at(78, 0)},
	{name: "DECSET_ResetReverseWraparoundDisablesIt", cols: 80, rows: 24, in: "\x1b[?1045l\x1b[?7h\x1b[2;1H\b", cur: at(0, 1)},
	{name: "DECSET_ReverseWraparound_RequiresDECAWM", cols: 80, rows: 24, in: "\x1b[2;1H\x1b[?1045h\x1b[?7l\b", cur: at(0, 1)},
	{name: "DECSET_OPT_ALTBUF", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1047h", cur: at(3, 1), alt: yes()},
	{name: "DECSET_OPT_ALTBUF#2", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1047h\x1b[2J\x1b[2;1Hdef\r\ndef", cur: at(3, 2), alt: yes(), check: escRect(1, 1, "   ", "def", "def")},
	{name: "DECSET_OPT_ALTBUF#3", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1047h\x1b[2J\x1b[2;1Hdef\r\ndef\x1b[?1047l", cur: at(3, 2), alt: no(), check: escRect(1, 1, "abc", "abc", "   ")},
	{name: "DECSET_OPT_ALTBUF#4", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1047h\x1b[2J\x1b[2;1Hdef\r\ndef\x1b[?1047l\x1b[?1047h", cur: at(3, 2), alt: yes(), check: escRect(1, 1, "   ", "   ", "   ")},
	{name: "DECSET_ALTBUF", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?47h", cur: at(3, 1), alt: yes()},
	{name: "DECSET_ALTBUF#2", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?47h\x1b[2J\x1b[2;1Hdef\r\ndef", cur: at(3, 2), alt: yes(), check: escRect(1, 1, "   ", "def", "def")},
	{name: "DECSET_ALTBUF#3", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?47h\x1b[2J\x1b[2;1Hdef\r\ndef\x1b[?47l", cur: at(3, 2), alt: no(), check: escRect(1, 1, "abc", "abc", "   ")},
	{name: "DECSET_ALTBUF#4", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?47h\x1b[2J\x1b[2;1Hdef\r\ndef\x1b[?47l\x1b[?47h", cur: at(3, 2), alt: yes(), check: escRect(1, 1, "   ", "def", "def")},
	{name: "DECSET_OPT_ALTBUF_CURSOR", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1049h", alt: yes()},
	{name: "DECSET_OPT_ALTBUF_CURSOR#2", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1049h\x1b[2J\x1b[2;1Hdef\r\ndef", cur: at(3, 2), alt: yes(), check: escRect(1, 1, "   ", "def", "def")},
	{name: "DECSET_OPT_ALTBUF_CURSOR#3", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1049h\x1b[2J\x1b[2;1Hdef\r\ndef\x1b[?1049l", cur: at(3, 1), alt: no(), check: escRect(1, 1, "abc", "abc", "   ")},
	{name: "DECSET_OPT_ALTBUF_CURSOR#4", cols: 80, rows: 24, in: "abc\r\nabc\x1b[?1049h\x1b[2J\x1b[2;1Hdef\r\ndef\x1b[?1049l\x1b[?1049h", alt: yes(), check: escRect(1, 1, "   ", "   ", "   ")},
	{name: "DECSET_DECLRMM", cols: 80, rows: 24, in: "\x1b[?69h\x1b[2;4sabcdefgh\x1b[?69l", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECLRMM_MarginsResetByDECSTR", cols: 80, rows: 24, in: "\x1b[2;4s\x1b[!p\x1b[?69h\x1b[3;3Habc", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECLRMM_ModeResetByDECSTR", cols: 80, rows: 24, in: "\x1b[?69h\x1b[!p\x1b[2;4s\x1b[3;3Habc", gap: "DECLRMM left/right margins"},
	{name: "DECSET_DECNCSM", cols: 80, rows: 24, in: "\x1b[?3l\x1b[?95h\x1b[1;1H1\x1b[?3h", gap: "DECCOLM 132-column mode"},
	{name: "DECSET_SaveRestoreCursor", cols: 80, rows: 24, in: "\x1b[3;2H\x1b[?1048h\x1b[5;5H\x1b[?1048l", cur: at(1, 2)},

	// vttest-style checks
	{name: "vttest wrap at right margin with DECAWM", cols: 80, rows: 24, in: "\x1b[?7h" + strings.Repeat("0123456789", 8) + "ABCDE", screen: scr(strings.Repeat("0123456789", 8) + "\nABCDE"), cur: at(5, 1)},
	{name: "vttest no wrap without DECAWM", cols: 80, rows: 24, in: "\x1b[?7l" + strings.Repeat("0123456789", 8) + "ABCDE", screen: scr(strings.Repeat("0123456789", 7) + "012345678E"), cur: at(79, 0)},
	{name: "vttest tab stops after RIS", cols: 80, rows: 24, in: "\x1b[3g\x1bc\t\t\t", cur: at(24, 0)},
	{name: "vttest tab stops after RIS#2", cols: 80, rows: 24, in: "\x1b[1;4H\x1bH\x1bc\t", cur: at(8, 0)},
	{name: "APC_Basic", cols: 80, rows: 24, in: "\x1b_xyz\x1b\\A", check: escRect(1, 1, "A  ")},
	{name: "PM_Basic", cols: 80, rows: 24, in: "\x1b^xyz\x1b\\A", check: escRect(1, 1, "A  ")},
	{name: "SOS_Basic", cols: 80, rows: 24, in: "\x1bXxyz\x1b\\A", check: escRect(1, 1, "A  ")},
	{name: "APC_8bit", cols: 80, rows: 24, in: "\x1b G\x9fxyz\x9cA\x1b F", gap: "8-bit C1 controls"},
	{name: "PM_8bit", cols: 80, rows: 24, in: "\x1b G\x9exyz\x9cA\x1b F", gap: "8-bit C1 controls"},
	{name: "SOS_8bit", cols: 80, rows: 24, in: "\x1b G\x98xyz\x9cA\x1b F", gap: "8-bit C1 controls"},
	{name: "DCS_Unrecognized", cols: 80, rows: 24, in: "\x1bPz0\x1b\\", check: escRect(1, 1, " ")},
	{name: "ChangeColor_Multiple", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_RGB", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_Hash3", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_Hash6", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_Hash9", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_Hash12", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_RGBI", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_CIEXYZ", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_CIEuvY", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_CIExyY", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_CIELab", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_CIELuv", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeColor_TekHVC", cols: 80, rows: 24, in: "\x1b]4;1;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_Multiple", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_RGB", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_Hash3", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_Hash6", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_Hash9", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_Hash12", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_RGBI", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_CIEXYZ", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_CIEuvY", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_CIExyY", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_CIELab", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_CIELuv", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeDynamicColor_TekHVC", cols: 80, rows: 24, in: "\x1b]10;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_Multiple", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_Multiple2", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_RGB", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_Hash3", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_Hash6", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_Hash9", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_Hash12", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_RGBI", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_CIEXYZ", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_CIEuvY", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_CIExyY", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_CIELab", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_CIELuv", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ChangeSpecialColor_TekHVC", cols: 80, rows: 24, in: "\x1b]5;0;rgb:ff/00/00\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetColor_Standard", cols: 80, rows: 24, in: "\x1b]104;1\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetColor_All", cols: 80, rows: 24, in: "\x1b]104\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetSpecialColor_Single", cols: 80, rows: 24, in: "\x1b]105;0\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetSpecialColor_Single2", cols: 80, rows: 24, in: "\x1b]105;0\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetSpecialColor_Multiple", cols: 80, rows: 24, in: "\x1b]105;0\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetSpecialColor_Multiple2", cols: 80, rows: 24, in: "\x1b]105;0\x1b\\", gap: "palette and dynamic colors"},
	{name: "ResetSpecialColor_Dynamic", cols: 80, rows: 24, in: "\x1b]110\x1b\\", gap: "palette and dynamic colors"},
	{name: "DA_NoParameter", cols: 80, rows: 24, in: "\x1b[c", gap: "device reports / replies"},
	{name: "DA_0", cols: 80, rows: 24, in: "\x1b[0c", gap: "device reports / replies"},
	{name: "DA2_NoParameter", cols: 80, rows: 24, in: "\x1b[>c", gap: "device reports / replies"},
	{name: "DA2_0", cols: 80, rows: 24, in: "\x1b[>0c", gap: "device reports / replies"},
	{name: "DECID_Basic", cols: 80, rows: 24, in: "\x1bZ", gap: "device reports / replies"},
	{name: "DECID_8bit", cols: 80, rows: 24, in: "\x1b G\x9a\x1b F", gap: "8-bit C1 controls"},
	{name: "DECDSR_DECXCPR", cols: 80, rows: 24, in: "\x1b[?6n", gap: "device reports / replies"},
	{name: "DECDSR_DSRPrinterPort", cols: 80, rows: 24, in: "\x1b[?15n", gap: "device reports / replies"},
	{name: "DECDSR_DSRUDKLocked", cols: 80, rows: 24, in: "\x1b[?25n", gap: "device reports / replies"},
	{name: "DECDSR_DSRKeyboard", cols: 80, rows: 24, in: "\x1b[?26n", gap: "device reports / replies"},
	{name: "DECDSR_DSRDECLocatorStatus", cols: 80, rows: 24, in: "\x1b[?55n", gap: "device reports / replies"},
	{name: "DECDSR_LocatorType", cols: 80, rows: 24, in: "\x1b[?56n", gap: "device reports / replies"},
	{name: "DECDSR_DECMSR", cols: 80, rows: 24, in: "\x1b[?62n", gap: "device reports / replies"},
	{name: "DECDSR_DECCKSR", cols: 80, rows: 24, in: "\x1b[?63;1n", gap: "device reports / replies"},
	{name: "DECDSR_DSRDataIntegrity", cols: 80, rows: 24, in: "\x1b[?75n", gap: "device reports / replies"},
	{name: "DECDSR_DSRMultipleSessionStatus", cols: 80, rows: 24, in: "\x1b[?85n", gap: "device reports / replies"},
	{name: "DECRQM", cols: 80, rows: 24, in: "\x1b[?1$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_KAM", cols: 80, rows: 24, in: "\x1b[2$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_IRM", cols: 80, rows: 24, in: "\x1b[4$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_SRM", cols: 80, rows: 24, in: "\x1b[12$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_LNM", cols: 80, rows: 24, in: "\x1b[20$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_GATM", cols: 80, rows: 24, in: "\x1b[1$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_SRTM", cols: 80, rows: 24, in: "\x1b[5$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_VEM", cols: 80, rows: 24, in: "\x1b[7$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_HEM", cols: 80, rows: 24, in: "\x1b[10$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_PUM", cols: 80, rows: 24, in: "\x1b[11$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_FEAM", cols: 80, rows: 24, in: "\x1b[13$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_FETM", cols: 80, rows: 24, in: "\x1b[14$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_MATM", cols: 80, rows: 24, in: "\x1b[15$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_TTM", cols: 80, rows: 24, in: "\x1b[16$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_SATM", cols: 80, rows: 24, in: "\x1b[17$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_TSM", cols: 80, rows: 24, in: "\x1b[18$p", gap: "device reports / replies"},
	{name: "DECRQM_ANSI_EBM", cols: 80, rows: 24, in: "\x1b[19$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECCKM", cols: 80, rows: 24, in: "\x1b[?1$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECCOLM", cols: 80, rows: 24, in: "\x1b[?3$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECSCLM", cols: 80, rows: 24, in: "\x1b[?4$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECSCNM", cols: 80, rows: 24, in: "\x1b[?5$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECOM", cols: 80, rows: 24, in: "\x1b[?6$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECAWM", cols: 80, rows: 24, in: "\x1b[?7$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECARM", cols: 80, rows: 24, in: "\x1b[?8$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECPFF", cols: 80, rows: 24, in: "\x1b[?18$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECPEX", cols: 80, rows: 24, in: "\x1b[?19$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECTCEM", cols: 80, rows: 24, in: "\x1b[?25$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECRLM", cols: 80, rows: 24, in: "\x1b[?34$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECHEBM", cols: 80, rows: 24, in: "\x1b[?35$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECHEM", cols: 80, rows: 24, in: "\x1b[?36$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECNRCM", cols: 80, rows: 24, in: "\x1b[?42$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECNAKB", cols: 80, rows: 24, in: "\x1b[?57$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECVCCM", cols: 80, rows: 24, in: "\x1b[?61$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECPCCM", cols: 80, rows: 24, in: "\x1b[?64$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECNKM", cols: 80, rows: 24, in: "\x1b[?66$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECBKM", cols: 80, rows: 24, in: "\x1b[?67$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECKBUM", cols: 80, rows: 24, in: "\x1b[?68$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECLRMM", cols: 80, rows: 24, in: "\x1b[?69$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECXRLM", cols: 80, rows: 24, in: "\x1b[?73$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECKPM", cols: 80, rows: 24, in: "\x1b[?81$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECNCSM", cols: 80, rows: 24, in: "\x1b[?95$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECRLCM", cols: 80, rows: 24, in: "\x1b[?96$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECCRTSM", cols: 80, rows: 24, in: "\x1b[?97$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECARSM", cols: 80, rows: 24, in: "\x1b[?98$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECMCM", cols: 80, rows: 24, in: "\x1b[?99$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECAAM", cols: 80, rows: 24, in: "\x1b[?100$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECCANSM", cols: 80, rows: 24, in: "\x1b[?101$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECNULM", cols: 80, rows: 24, in: "\x1b[?102$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECHDPXM", cols: 80, rows: 24, in: "\x1b[?103$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECESKM", cols: 80, rows: 24, in: "\x1b[?104$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECOSCNM", cols: 80, rows: 24, in: "\x1b[?106$p", gap: "device reports / replies"},
	{name: "DECRQM_DEC_DECHCCM", cols: 80, rows: 24, in: "\x1b[?60$p", gap: "device reports / replies"},
	{name: "DECRQSS_DECELF", cols: 80, rows: 24, in: "\x1bP$q+q\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECLFKC", cols: 80, rows: 24, in: "\x1bP$q*}\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSASD", cols: 80, rows: 24, in: "\x1bP$q$}\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSACE", cols: 80, rows: 24, in: "\x1bP$q*x\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSCA", cols: 80, rows: 24, in: "\x1bP$q\"q\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSCL", cols: 80, rows: 24, in: "\x1bP$q\"p\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSTBM", cols: 80, rows: 24, in: "\x1bP$qr\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_SGR", cols: 80, rows: 24, in: "\x1bP$qm\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSCUSR", cols: 80, rows: 24, in: "\x1bP$q q\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSLRM", cols: 80, rows: 24, in: "\x1bP$qs\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSLPP", cols: 80, rows: 24, in: "\x1bP$qt\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSMKR", cols: 80, rows: 24, in: "\x1bP$q+r\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSNLS", cols: 80, rows: 24, in: "\x1bP$q*|\x1b\\", gap: "device reports / replies"},
	{name: "DECRQSS_DECSSDT", cols: 80, rows: 24, in: "\x1bP$q$~\x1b\\", gap: "device reports / replies"},
	{name: "DECSCL_Level2DoesntSupportDECRQM", cols: 80, rows: 24, in: "Hello world.\x1b[62;1\"p\x1b[4$p", gap: "device reports / replies"},
	{name: "DSCSCL_Level2Supports7BitControls", cols: 80, rows: 24, in: "\x1b[62;1\"p\x1b[2;2H", cur: at(1, 1)},
	{name: "DSCSCL_Level3_SupportsDECRQMDoesntSupportDECSLRM", cols: 80, rows: 24, in: "\x1b[63;1\"p\x1b[?69h\x1b[5;6s\x1b[1;5Habc", gap: "DECLRMM left/right margins"},
	{name: "DECSCL_Level4_SupportsDECSLRMDoesntSupportDECNCSM", cols: 80, rows: 24, in: "\x1b[64;1\"p\x1b[?40h\x1b[?3l\x1b[?95h1\x1b[?3h", gap: "DECCOLM 132-column mode"},
	{name: "DECSCL_Level5_SupportsDECNCSM", cols: 80, rows: 24, in: "\x1b[65;1\"p\x1b[?3l\x1b[?95h1\x1b[?3h", gap: "DECCOLM 132-column mode"},
	{name: "DECSCL_RISOnChange", cols: 80, rows: 24, in: "x\x1b[6;5H\x1b7\x1b[4h\x1b[61\"p", check: escRect(1, 1, " ")},
	{name: "DECSCL_RISOnChange#2", cols: 80, rows: 24, in: "x\x1b[6;5H\x1b7\x1b[4h\x1b[61\"p\x1b8", cur: at(0, 0)},
	{name: "DECSCL_RISOnChange#3", cols: 80, rows: 24, in: "x\x1b[6;5H\x1b7\x1b[4h\x1b[61\"p\x1b8\x1b[1;1Ha\x1b[1;1Hb", check: escRect(1, 1, "b")},
	{name: "S8C1T_CSI", cols: 80, rows: 24, in: "\x1b G\x9b2;3H\x1b F", gap: "8-bit C1 controls"},
	{name: "S8C1T_DCS", cols: 80, rows: 24, in: "\x1b G\x90$qm\x9c\x1b F", gap: "8-bit C1 controls"},
	{name: "S8C1T_SPA_EPA", cols: 80, rows: 24, in: "\x1b G\x96a\x97\x1b F", gap: "8-bit C1 controls"},
	{name: "SMTitle_SetHexQueryUTF8", cols: 80, rows: 24, in: "\x1b[>0;1t\x1b]2;title\x07\x1b[21t", gap: "window ops"},
	{name: "SMTitle_SetUTF8QueryUTF8", cols: 80, rows: 24, in: "\x1b[>0;1t\x1b]2;title\x07\x1b[21t", gap: "window ops"},
	{name: "SMTitle_SetUTF8QueryHex", cols: 80, rows: 24, in: "\x1b[>0;1t\x1b]2;title\x07\x1b[21t", gap: "window ops"},
	{name: "SMTitle_SetHexQueryHex", cols: 80, rows: 24, in: "\x1b[>0;1t\x1b]2;title\x07\x1b[21t", gap: "window ops"},
	{name: "ManipulateSelectionData_default", cols: 80, rows: 24, in: "\x1b]52;c;YWJj\x07", gap: "hyperlinks / semantic prompts / OSC state"},
	{name: "XtermWinops_IconifyDeiconfiy", cols: 80, rows: 24, in: "\x1b[2t\x1b[1t", gap: "window ops"},
	{name: "XtermWinops_MoveToXY", cols: 80, rows: 24, in: "\x1b[3;10;20t", gap: "window ops"},
	{name: "XtermWinops_MoveToXY_Defaults", cols: 80, rows: 24, in: "\x1b[3t", gap: "window ops"},
	{name: "XtermWinops_ResizePixels_BothParameters", cols: 80, rows: 24, in: "\x1b[4;400;600t", gap: "window ops"},
	{name: "XtermWinops_ResizePixels_OmittedHeight", cols: 80, rows: 24, in: "\x1b[4;;600t", gap: "window ops"},
	{name: "XtermWinops_ResizePixels_OmittedWidth", cols: 80, rows: 24, in: "\x1b[4;400t", gap: "window ops"},
	{name: "XtermWinops_ResizePixels_ZeroWidth", cols: 80, rows: 24, in: "\x1b[4;400;0t", gap: "window ops"},
	{name: "XtermWinops_ResizePixels_ZeroHeight", cols: 80, rows: 24, in: "\x1b[4;0;600t", gap: "window ops"},
	{name: "XtermWinops_ResizeChars_BothParameters", cols: 80, rows: 24, in: "\x1b[8;20;40t", gap: "window ops"},
	{name: "XtermWinops_ResizeChars_ZeroWidth", cols: 80, rows: 24, in: "\x1b[8;20;0t", gap: "window ops"},
	{name: "XtermWinops_ResizeChars_ZeroHeight", cols: 80, rows: 24, in: "\x1b[8;0;40t", gap: "window ops"},
	{name: "XtermWinops_ResizeChars_DefaultWidth", cols: 80, rows: 24, in: "\x1b[8;20t", gap: "window ops"},
	{name: "XtermWinops_ResizeChars_DefaultHeight", cols: 80, rows: 24, in: "\x1b[8;;40t", gap: "window ops"},
	{name: "XtermWinops_MaximizeWindow_HorizontallyAndVertically", cols: 80, rows: 24, in: "\x1b[9;1t", gap: "window ops"},
	{name: "XtermWinops_MaximizeWindow_Horizontally", cols: 80, rows: 24, in: "\x1b[9;3t", gap: "window ops"},
	{name: "XtermWinops_MaximizeWindow_Vertically", cols: 80, rows: 24, in: "\x1b[9;2t", gap: "window ops"},
	{name: "XtermWinops_Fullscreen", cols: 80, rows: 24, in: "\x1b[10;1t", gap: "window ops"},
	{name: "XtermWinops_ReportIconLabel", cols: 80, rows: 24, in: "\x1b[20t", gap: "window ops"},
	{name: "XtermWinops_ReportWindowLabel", cols: 80, rows: 24, in: "\x1b[21t", gap: "window ops"},
	{name: "XtermWinops_PushIconAndWindow_PopIconAndWindow", cols: 80, rows: 24, in: "\x1b[22;0t\x1b[23;0t", gap: "window ops"},
	{name: "XtermWinops_PushIconAndWindow_PopIcon", cols: 80, rows: 24, in: "\x1b[22;0t\x1b[23;1t", gap: "window ops"},
	{name: "XtermWinops_PushIconAndWindow_PopWindow", cols: 80, rows: 24, in: "\x1b[22;0t\x1b[23;2t", gap: "window ops"},
	{name: "XtermWinops_PushIcon_PopIcon", cols: 80, rows: 24, in: "\x1b[22;1t\x1b[23;1t", gap: "window ops"},
	{name: "XtermWinops_PushWindow_PopWindow", cols: 80, rows: 24, in: "\x1b[22;2t\x1b[23;2t", gap: "window ops"},
	{name: "XtermWinops_PushIconThenWindowThenPopBoth", cols: 80, rows: 24, in: "\x1b[22;1t\x1b[22;2t\x1b[23;0t", gap: "window ops"},
	{name: "XtermWinops_PushMultiplePopMultiple_Icon", cols: 80, rows: 24, in: "\x1b[22;1t\x1b[22;1t\x1b[23;1t\x1b[23;1t", gap: "window ops"},
	{name: "XtermWinops_PushMultiplePopMultiple_Window", cols: 80, rows: 24, in: "\x1b[22;2t\x1b[22;2t\x1b[23;2t\x1b[23;2t", gap: "window ops"},
	{name: "XtermWinops_DECSLPP", cols: 80, rows: 24, in: "\x1b[30t", gap: "window ops"},
	{name: "DECBI_Basic", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1H\x1b6", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECBI_NoWrapOnLeftEdge", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1H\x1b6", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECBI_Scrolls", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1H\x1b6", gap: "DECLRMM left/right margins"},
	{name: "DECBI_LeftOfMargin", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1H\x1b6", gap: "DECLRMM left/right margins"},
	{name: "DECBI_WholeScreenScrolls", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1H\x1b6", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFI_Basic", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;80H\x1b9", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFI_NoWrapOnRightEdge", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;80H\x1b9", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFI_Scrolls", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;80H\x1b9", gap: "DECLRMM left/right margins"},
	{name: "DECFI_RightOfMargin", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;80H\x1b9", gap: "DECLRMM left/right margins"},
	{name: "DECFI_WholeScreenScrolls", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;80H\x1b9", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECIC_DefaultParam", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECIC_ExplicitParam", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECIC_CursorWithinTopBottom", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "DECLRMM left/right margins"},
	{name: "DECIC_IsNoOpWhenCursorBeginsOutsideScrollRegion", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "DECLRMM left/right margins"},
	{name: "DECIC_ScrollOffRightEdge", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECIC_ScrollEntirelyOffRightEdge", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECIC_ScrollOffRightMarginInScrollRegion", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'}", gap: "DECLRMM left/right margins"},
	{name: "DECDC_DefaultParam", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECDC_ExplicitParam", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECDC_CursorWithinTopBottom", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "DECLRMM left/right margins"},
	{name: "DECDC_IsNoOpWhenCursorBeginsOutsideScrollRegion", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "DECLRMM left/right margins"},
	{name: "DECDC_DeleteAll", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECDC_DeleteWithLeftRightMargins", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "DECLRMM left/right margins"},
	{name: "DECDC_DeleteAllWithLeftRightMargins", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2H\x1b[2'~", gap: "DECLRMM left/right margins"},
	{name: "DECCRA_nonOverlappingSourceAndDest", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_overlappingSourceAndDest", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_destinationPartiallyOffscreen", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_defaultValuesInSource", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_defaultValuesInDest", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_invalidSourceRectDoesNothing", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_respectsOriginMode", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "DECLRMM left/right margins"},
	{name: "DECCRA_ignoresMargins", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "DECLRMM left/right margins"},
	{name: "DECCRA_overlyLargeSourceClippedToScreenSize", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECCRA_cursorDoesNotMove", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;1;2;2;1;5;5;1$v", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECERA_basic", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECERA_invalidRectDoesNothing", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECERA_defaultArgs", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECERA_respectsOriginMode", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "DECLRMM left/right margins"},
	{name: "DECERA_overlyLargeSourceClippedToScreenSize", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECERA_cursorDoesNotMove", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECERA_ignoresMargins", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3$z", gap: "DECLRMM left/right margins"},
	{name: "DECFRA_basic", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFRA_invalidRectDoesNothing", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFRA_defaultArgs", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFRA_respectsOriginMode", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "DECLRMM left/right margins"},
	{name: "DECFRA_overlyLargeSourceClippedToScreenSize", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFRA_cursorDoesNotMove", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECFRA_ignoresMargins", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[42;1;2;2;3$x", gap: "DECLRMM left/right margins"},
	{name: "DECSERA_basic", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECSERA_invalidRectDoesNothing", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECSERA_defaultArgs", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECSERA_respectsOriginMode", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "DECLRMM left/right margins"},
	{name: "DECSERA_overlyLargeSourceClippedToScreenSize", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECSERA_cursorDoesNotMove", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "rectangular area ops (DECCRA/DECERA/DECFRA/...)"},
	{name: "DECSERA_ignoresMargins", cols: 80, rows: 24, in: "abcdefgh\x1b[2;1Hijklmnop\x1b[3;1Hqrstuvwx\x1b[1;2;2;3${", gap: "DECLRMM left/right margins"},
	{name: "DECSERA_doesNotRespectISOProtect", cols: 80, rows: 24, in: "a\x1bVb\x1bW\x1b[1;1;1;2${", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_Default", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_0", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_1", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_2", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_3", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_0_WithScrollRegion", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_1_WithScrollRegion", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_2_WithScrollRegion", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_Default_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_DECSCA_2", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_0_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_1_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_2_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_3_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_0_WithScrollRegion_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_1_WithScrollRegion_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_2_WithScrollRegion_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSED_doesNotRespectISOProtect", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?J", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_Default", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_0", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_1", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_2", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_IgnoresScrollRegion", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_Default_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_0_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_1_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_2_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_IgnoresScrollRegion_Protection", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
	{name: "DECSEL_doesNotRespectISOProtect", cols: 80, rows: 24, in: "a\x1b[1\"qb\x1b[0\"qc\x1b[1;2H\x1b[?K", gap: "protected cells (DECSCA/SPA, DECSED/DECSEL)"},
}
