package vt

// knownFailures lists conformance cases this emulator fails, keyed
// "suite/name" (libvterm: "libvterm/file:line"), with why. A listed case
// that starts passing fails the test until it is removed from the list,
// and so does an entry that no longer matches a failing case.
//
// Notes starting "divergent:" are deliberate: the upstream emulator
// departs from xterm (or from the other suites) there, and this emulator
// follows xterm. "gap:" marks a feature that is not modeled.
var knownFailures = map[string]string{
	// Reverse wraparound: esctest checks the pre-patch-383 xterm meaning of
	// mode 45 (BS wraps to any previous line and from the top margin to the
	// bottom). This emulator follows current xterm and ghostty: 45 wraps
	// only across soft wraps, 1045 to any line and top to bottom.
	"esctest/BS_AfterNoWrappedInlines":   "divergent: pre-patch-383 xterm reverse wrap",
	"esctest/BS_AfterOneWrappedInline":   "divergent: pre-patch-383 xterm reverse wrap",
	"esctest/BS_ReverseWrapGoesToBottom": "divergent: pre-patch-383 xterm reverse wrap",
	"esctest/BS_WrapsInWraparoundMode":   "divergent: pre-patch-383 xterm reverse wrap",
	"esctest/CUB_AfterNoWrappedInlines":  "divergent: pre-patch-383 xterm reverse wrap",
	"esctest/CUB_AfterOneWrappedInline":  "divergent: pre-patch-383 xterm reverse wrap",
	"esctest/DECSCL_RISOnChange":         "divergent: esctest itself lists this as an xterm bug; DECSCL is ignored",
	"esctest/DECSCL_RISOnChange#2":       "divergent: esctest itself lists this as an xterm bug; DECSCL is ignored",
	"esctest/DECSET_MoreFix":             "gap: mode 41 (more(1) tab fix) is not modeled",
	"esctest/DECSET_MoreFix#2":           "gap: mode 41 (more(1) tab fix) is not modeled",
	"esctest/DECSET_MoreFix#3":           "gap: mode 41 (more(1) tab fix) is not modeled",
	"esctest/DECSET_MoreFix#4":           "gap: mode 41 (more(1) tab fix) is not modeled",

	"ghostty/disabled wraparound with wide char and no space":                 "divergent: ghostty drops a wide character that does not fit with DECAWM off; libvterm and this emulator back up to fit it",
	"ghostty/disabled wraparound with wide char and one space":                "divergent: as above",
	"ghostty/eraseDisplay scroll complete":                                    "gap: CSI 22 J (kitty/ghostty scroll-and-clear extension)",
	"ghostty/cursorLeft reverse wrap with pending wrap above top margin":      "divergent: a restored pending wrap is kept only at the right margin (Repaint can only recreate it there); ghostty keeps it anywhere",
	"ghostty/print wide char in single-width terminal":                        "divergent: ghostty sets a pending wrap for a character that cannot be printed at all",
	"ghostty/print with wraparound disabled clears pending wrap after CBT #4": "divergent: ghostty keeps the pending-wrap flag while DECAWM is off; it has no effect then",
	"ghostty/print with wraparound disabled clears pending wrap after CBT":    "divergent: as above",

	"libvterm/13state_edit.test:81":     "divergent: IL leaves the column alone in libvterm; xterm and ghostty move to the left margin",
	"libvterm/16state_resize.test:36":   "divergent: libvterm's state layer does not reflow; this emulator always reflows the main screen",
	"libvterm/30state_pen.test:111":     "divergent: libvterm turns bold+ANSI color into the bright color in the pen; xterm keeps the color and leaves brightening to rendering",
	"libvterm/30state_pen.test:114":     "divergent: as above",
	"libvterm/61screen_unicode.test:35": "divergent: libvterm keeps at most 6 code points per cell; this emulator keeps up to 32 bytes",
	"libvterm/61screen_unicode.test:41": "divergent: as above",
	"libvterm/63screen_resize.test:103": "divergent: the libvterm harness invents scrollback lines (ABCDE) to pop; this terminal has none",
	"libvterm/63screen_resize.test:104": "divergent: as above",
	"libvterm/63screen_resize.test:105": "divergent: as above",
	"libvterm/63screen_resize.test:106": "divergent: as above",
	"libvterm/64screen_pen.test:46":     "divergent: libvterm erases with the pen's foreground too; xterm (BCE) only with its background",
	"libvterm/64screen_pen.test:47":     "divergent: as above",
	"libvterm/69screen_reflow.test:74":  "divergent: when a reflow frees rows this emulator brings lines back from the scrollback (as on any resize taller, like xterm and ghostty); libvterm leaves blank rows below",
	"libvterm/69screen_reflow.test:75":  "divergent: as above",
	"libvterm/69screen_reflow.test:76":  "divergent: as above",
	"libvterm/69screen_reflow.test:78":  "divergent: as above",
	"libvterm/69screen_reflow.test:79":  "divergent: as above",

	"wezterm/test_cha#6":                                "divergent: wezterm lets the cursor column equal the width; xterm clamps to the last column",
	"wezterm/test_cup#5":                                "divergent: as above",
	"wezterm/test_hvp#5":                                "divergent: as above",
	"wezterm/scroll_up_within_left_and_right_margins":   "divergent: wezterm scrolls one column past the right margin; xterm (and esctest's DECSLRM cases) stop at it",
	"wezterm/scroll_down_within_left_and_right_margins": "divergent: as above",
	"wezterm/test_resize_2162":                          "divergent: as above (this emulator keeps the cursor on the last column with a pending wrap)",

	"alacritty/decaln_reset":      "divergent: alacritty's DECALN leaves the cursor where it was; xterm homes it",
	"alacritty/deccolm_reset":     "gap: DECCOLM (132-column mode) is not modeled",
	"alacritty/erase_in_line":     "divergent: alacritty's EL keeps a pending wrap; xterm and ghostty clear it",
	"alacritty/grid_reset":        "divergent: alacritty's RIS blanks the scrollback; this emulator keeps it",
	"alacritty/saved_cursor":      "divergent: alacritty does not save the character sets with the cursor (DECSC, 1049); xterm does",
	"alacritty/saved_cursor_alt":  "divergent: alacritty keeps a pending wrap across switching to the alternate screen",
	"alacritty/selective_erasure": "divergent: alacritty ignores DECSED; with nothing protected it erases like ED",
	"alacritty/underline":         "divergent: alacritty ignores SGR 21; xterm and ECMA-48 make it double underline",
}
