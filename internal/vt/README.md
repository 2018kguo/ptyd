# internal/vt

A compact VT/xterm screen model in pure Go. It is fed a program's raw
output and keeps what a terminal would show: main and alternate screens,
scrollback, cursor and saved cursors, colors and attributes, scroll region
and left/right margins, tab stops, character sets, hyperlinks, and the
input modes the program switched on. It can serialize all of that as a byte
stream that repaints it on a fresh terminal (`Repaint`), which is how ptyd
attaches clients and writes checkpoints.

It is built for ptyd's scale: thousands of mostly idle terminals per
daemon. A cell is 16 bytes, rows are allocated only once drawn on, the
alternate screen exists only while in use, scrollback is kept as
serialized lines under a byte budget, and redrawing a frame does not
allocate. It never answers queries and keeps no timers.

## API

```go
t := vt.New(cols, rows, vt.Options{ScrollbackLines: 2000, ScrollbackBytes: 256 << 10})
t.Write(output)              // io.Writer; sequences may be split across writes
t.Resize(cols, rows)         // reflows the main screen
t.Lines()                    // visible rows as text
t.ScrollbackLines()          // scrollback as text, one entry per logical line
t.Cursor()                   // x, y, visible
t.AltScreen(); t.Title(); t.Size(); t.ScrollbackLen(); t.ClearScrollback()
t.Repaint(withScrollback)    // bytes that recreate this state on a fresh terminal
t.ArchiveScreen()            // ptyd-specific: keep the old screen above a restarted program
```

A `Terminal` is not safe for concurrent use.

## What it models

- Printing: UTF-8 (invalid input becomes U+FFFD; C1 controls sent as
  UTF-8 code points act as their ESC forms), wcwidth widths (East Asian
  ambiguous = 1), wide characters and the spacer they leave at a wrap,
  combining marks, ZWJ and variation selectors attached to the previous
  character, DEC special graphics in G0-G3 with SI/SO, LS2/LS3 and
  SS2/SS3, REP, insert mode, autowrap with xterm's pending-wrap semantics,
  reverse wraparound (45 and 1045).
- Cursor: CUU/CUD/CUF/CUB, CNL/CPL, CHA/HPA/HPR/HPB, VPA/VPR/VPB, CUP/HVP,
  origin mode, DECSC/DECRC, SCOSC/SCORC, 1048, IND/RI/NEL, HT/CHT/CBT with
  HTS and TBC.
- Editing: ICH/DCH/ECH, IL/DL, SU/SD, ED/EL (BCE: erased cells take the
  current background), DECALN, character protection (DECSCA, SPA/EPA) with
  DECSED/DECSEL.
- Regions: DECSTBM, DECLRMM/DECSLRM (left/right margins) with all the
  operations they constrain.
- Screens: alternate screen modes 47, 1047, 1049 (47 keeps its contents
  between switches), scrollback fed from the top of the main screen.
- Modes kept for Repaint: DECAWM, IRM, LNM, DECOM, DECTCEM, DECCKM,
  DECKPAM, DECSCNM, mouse tracking and encoding, focus events, bracketed
  paste, kitty keyboard flags, DECSCUSR, XTSAVE/XTRESTORE, and the window
  title (OSC 0/2).
- SGR: bold, faint, italic, underline styles (single, double, curly,
  dotted, dashed), blink, inverse, hidden, strike, overline, 16/256/RGB
  colors in both the `;` and `:` forms.
- Hyperlinks (OSC 8) on cells, through Repaint and the scrollback.
- Soft wraps: rows remember that they wrapped; the scrollback stores
  logical lines; Repaint recreates the wraps; Resize rewraps the main
  screen to the new width (narrowing and widening back is exact) and
  brings lines back from the scrollback when the screen grows.
- RIS, DECSTR.

## What it does not

- Replies to queries (DA, DSR/CPR, DECRQM, DECRQSS, XTVERSION, OSC color
  queries). By design for ptyd: an attached client's terminal answers.
- Palette and dynamic colors (OSC 4/10/11/12/104...), DECSCNM is tracked
  but not applied to cells.
- Rectangular area operations (DECCRA, DECERA, DECFRA, DECSERA, DECIC,
  DECDC, DECBI, DECFI), DECCOLM (132 columns), double-width and
  double-height lines, national character sets, SGR fonts and
  sub/superscript, underline color (SGR 58 is parsed and dropped).
- Grapheme clustering (mode 2027): widths are per code point, as in xterm
  and libvterm, so a ZWJ emoji sequence takes the width of its parts.
- Sixel and kitty graphics, semantic prompts (OSC 133), OSC 7, OSC 52.
- The alternate screen is not reflowed on resize (it is cut or padded;
  full-screen programs redraw on SIGWINCH).
- Mode 41 (more(1) tab fix), DECSCL conformance levels, 8-bit C1 bytes
  outside UTF-8.

## Conformance

Five external suites run as ordinary Go tests (`VT_CONF_REPORT=1 go test
-run 'Conformance|Libvterm|Alacritty' -v` prints the table). Sources and
licenses are in `testdata/conformance/NOTICE.md`.

| Suite | How | Pass | Fail | Deliberate difference | Out of scope |
|---|---|---|---|---|---|
| libvterm `t/*.test` | scripts run unchanged by an interpreter | 695 | 0 | 17 | 241 |
| ghostty `Terminal.zig`, `stream_terminal.zig` | translated cases | 458 | 0 | 7 | 84 |
| wezterm `term/src/test` | translated cases | 128 | 0 | 6 | 4 |
| esctest2 (xterm) | independently written cases (GPL source not used) | 410 | 0 | 12 | 215 |
| alacritty ref tests | recordings + reference grids, per line | 1507 / 1557 lines, 37 / 45 tests | 0 | 8 tests | |

Before this work the same harness gave libvterm 653 / 55 failing, ghostty
331 / 45, wezterm 108 / 24, esctest 253 / 25 (with 104 more left/right
margin and 40 protection cases out of scope), alacritty 28 / 45 tests.

"Deliberate difference" cases are listed with a reason in
`conformance_known_test.go`. They are places where that emulator departs
from xterm (for example wezterm lets the cursor column equal the width,
alacritty ignores SGR 21 and keeps a pending wrap across EL, libvterm
brightens bold colors in the pen and caps combining marks at six) or where
the upstream harness fakes state. A listed case that starts passing, or an
entry that no longer matches a failure, fails the test.

"Out of scope" counts the cases for the features listed above as not
modeled: mostly device replies, input encoding (libvterm), palette,
rectangular operations, grapheme clustering and window operations.
Callback-only expectations in the libvterm scripts (damage, scrollrect,
movecursor...) test libvterm internals and are not counted (307).

## Other tests

- `TestRepaintRoundTrip`, `TestChunkingInvariant`, `TestCutAnywhere`:
  random streams over an alphabet of the sequences programs use.
- Fuzzers: `FuzzTokens` (that alphabet plus resizes, invariants after
  every step, exact Repaint round trip), `FuzzStream` (raw bytes plus
  resizes), `FuzzChunking`, `FuzzWrite`, and `FuzzOracle`, a differential
  fuzzer against `charmbracelet/x/vt` on the subset where it agrees with
  xterm.
- `TestRecordings`: nvim, vim, less, nano and man sessions recorded at
  100x30 (`record_test.go`), cut at 40 points: invariants, Repaint round
  trip, resizes. `TestOracleRecordings` compares them with the oracle.
- `TestReflowRoundTrip`: narrowing and restoring the width gives back the
  exact screen, scrollback and cursor.
- `TestRedrawDoesNotAllocate` pins zero allocations for a redraw frame.

Fuzzing time so far, about 2.2 hours on 4-6 workers: FuzzTokens about 55
minutes (it found every bug fixed in the margins and spacer-head commits:
stale saved cursors after resize, the scrollback staying joined to a row
it no longer continued, pending wraps left away from the edge), FuzzStream
30 minutes (4.3M runs), FuzzOracle 20 minutes (16.6M runs, clean),
FuzzChunking 10 minutes (9.2M runs, clean). Crashers are kept under
`testdata/fuzz` and run with every `go test`.

## Performance and memory

PERF-RESULTS

## Library readiness

The emulator is ready on the inside and not yet on the outside.

Ready: its screen semantics now match xterm closely on every suite we
could run (no unexplained failures left in about 3,200 checked cases),
the state survives a serialize/replay round trip byte for byte under
fuzzing, resize reflow is exact, and it is small and fast: no allocations
per redraw frame, about 2.8 KB per idle 80x24 terminal, unchanged by this
work. Nothing comparable in Go has that combination; the closest,
`charmbracelet/x/vt`, disagreed with xterm often enough here that the
differential fuzzer had to avoid regions, origin mode, insert mode, DECAWM
off and wide characters.

Not ready: the API is ptyd's.

- `Cell` exposes the internal encoding (sentinel runes, cluster and link
  indexes) and the attribute and color constants are unexported, so a
  caller can read text but not styles.
- No way to answer queries. Headless users (tests, recorders, bots) need
  at least DA1/DA2, DSR/CPR, DECRQM and XTVERSION, or programs stall
  waiting for replies.
- No damage tracking, so a renderer has to repaint everything.
- `ArchiveScreen` is ptyd-specific; modes (mouse, bracketed paste,
  kitty flags) are not readable.

Recommendation: extract it, after one API pass and one feature (the
reply hook), not before. Make it a separate module (for example
`github.com/2018kguo/vt`) with ptyd depending on it, so ptyd keeps the
test suites, fuzzers and benchmarks running against the real thing.
Proposed surface:

```go
func New(cols, rows int, opts ...Option) *Terminal
// Options: WithScrollback(lines, bytes), WithReply(func([]byte)) for DA,
// DSR/CPR, DECRQM, XTVERSION and color queries, WithBell(func()).

func (t *Terminal) Write(p []byte) (int, error)
func (t *Terminal) Resize(cols, rows int)
func (t *Terminal) Size() (cols, rows int)
func (t *Terminal) Cell(x, y int) Cell          // value type, no internals
func (t *Terminal) Cursor() Cursor              // X, Y, Visible, Shape, PendingWrap
func (t *Terminal) Line(y int) string; Lines() []string
func (t *Terminal) Scrollback() Scrollback      // Len(), Line(i) string, Cells(i) []Cell
func (t *Terminal) Modes() Modes                // alt screen, mouse, bracketed paste, keypad, kitty flags...
func (t *Terminal) Title() string
func (t *Terminal) AppendRepaint(dst []byte, withScrollback bool) []byte

type Cell struct{ /* unexported */ }
func (Cell) Text() string; Width() int; Style() Style; Hyperlink() string
type Style struct{ Fg, Bg Color; Attrs Attr; Underline Underline }
type Color uint32 // Kind() Default/Indexed/RGB, Index(), RGB()
```

ptyd's `ArchiveScreen` stays in ptyd as a small helper over that API
(or as an exported option with a narrow contract). Before tagging v1:
the reply hook, cell and style accessors, examples, and fuzzing in CI.
Optional later: palette and dynamic colors (OSC 4/10/11) so Repaint
restores them, grapheme clustering (mode 2027) for terminals that turn it
on, damage tracking.
