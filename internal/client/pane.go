package client

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"

	"github.com/2018kguo/phosphor"
)

// pane keeps app geometry and cursor state separate from the host terminal.
// Apps can clear, scroll or switch screens without painting over the status row.
// Only attached human views allocate this model; the API byte stream stays raw.
type pane struct {
	history      []historyRow
	historyBytes int
	mu           sync.Mutex
	screen       *phosphor.Terminal
	out          io.Writer
	label        string
	finished     bool
	cols, rows   int
	changed      bool
	lastModes    phosphor.Modes
	haveModes    bool
	sequences    sequenceReader
	err          error
	title        string
	initialized  bool
}

func newPane(out io.Writer, label string, cols, rows int) *pane {
	p := &pane{out: out, label: label, cols: cols, rows: rows, changed: true}
	p.screen = phosphor.New(max(cols, 1), max(rows-1, 1), phosphor.WithBell(func() { _, p.err = io.WriteString(out, "\a") }), phosphor.WithScrollbackOutput(historyOutput{p}))
	return p
}
func (p *pane) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.screen.Write(b)
	p.sequences.feed(b, func(seq []byte, osc bool) {
		if !osc || len(seq) < 4 {
			return
		}
		command, _, _ := strings.Cut(string(seq[2:]), ";")
		// These are host actions rather than screen drawing. Palette changes and
		// titles are represented by the model and never escape the app rectangle.
		switch command {
		case "7", "9", "52", "777":
			_, p.err = p.out.Write(seq)
		}
	})
	p.changed = true
	return len(b), p.err
}
func (p *pane) resize(cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cols, p.rows = max(cols, 1), max(rows, 2)
	p.haveModes = false
	p.changed = true
}
func (p *pane) draw(force bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	force = force || !p.haveModes
	if !p.changed && !force {
		return p.err
	}
	if p.screen.Modes().SyncUpdate && !force {
		return p.err
	}
	b := []byte("\x1b[?2026h\x1b[?25l\x1b[?6l\x1b[r\x1b[?7l\x1b[4l")
	b = appendMode(b, 2027, p.screen.Modes().GraphemeClustering)
	var historyAdded bool
	b, historyAdded = p.appendHistory(b)
	force = force || historyAdded
	view := p.screen
	if cols, rows := view.Size(); cols != p.cols || rows != p.rows-1 {
		view = phosphor.New(1, 1)
		limits := phosphor.SnapshotLimits{MaxBytes: 128 << 20, MaxCells: int(min(uint64(^uint(0)>>1), uint64(65535)*65535))}
		if err := view.RestoreSnapshot(p.screen.Snapshot(), limits); err != nil {
			return err
		}
		view.Resize(p.cols, p.rows-1)
		force = true
	}
	for y := 0; y < p.rows-1; y++ {
		if !force && !view.RowDirty(y) {
			continue
		}
		b = fmt.Appendf(b, "\x1b[%d;1H", y+1)
		b = view.AppendRowANSI(b, y)
	}
	b = fmt.Appendf(b, "\x1b[%d;1H\x1b[0m\x1b[7m", p.rows)
	state := "attached"
	if p.finished {
		state = "finished"
	}
	text := paneLabel(p.label, state, p.cols)
	b = append(b, text...)
	b = append(b, strings.Repeat(" ", max(p.cols-displayWidth(text), 0))...)
	b = append(b, "\x1b[0m"...)
	modes := p.screen.Modes()
	if !p.haveModes || modes != p.lastModes {
		for _, m := range []struct {
			code int
			on   bool
		}{{1, modes.ApplicationCursorKeys}, {1004, modes.FocusEvents}, {2004, modes.BracketedPaste}, {2027, modes.GraphemeClustering}} {
			b = appendMode(b, m.code, m.on)
		}
		if modes.ApplicationKeypad {
			b = append(b, "\x1b="...)
		} else {
			b = append(b, "\x1b>"...)
		}
		for _, m := range []int{9, 1000, 1002, 1003, 1005, 1006, 1015, 1016} {
			b = appendMode(b, m, false)
		}
		if modes.Mouse != 0 {
			b = appendMode(b, int(modes.Mouse), true)
		}
		if modes.MouseEncoding != 0 {
			b = appendMode(b, int(modes.MouseEncoding), true)
		}
		b = fmt.Appendf(b, "\x1b[=%du", modes.KittyKeyboard)
		p.haveModes = true
		p.lastModes = modes
	}
	title := p.screen.Title()
	if title == "" {
		title = p.label
	}
	if title != p.title {
		b = append(b, "\x1b]2;"...)
		b = append(b, windowTitle(title)...)
		b = append(b, "\x1b\\"...)
		p.title = title
	}
	cur := view.Cursor()
	shape := int(max(cur.Shape, phosphor.CursorBlock)) * 2
	if cur.Blink {
		shape--
	}
	b = fmt.Appendf(b, "\x1b[%d q\x1b[%d;%dH", shape, min(cur.Y+1, p.rows-1), min(cur.X+1, p.cols))
	b = appendMode(b, 25, cur.Visible)
	b = append(b, "\x1b[?2026l"...)
	_, p.err = p.out.Write(b)
	p.screen.ClearDirty()
	p.changed = false
	return p.err
}
func appendMode(b []byte, code int, on bool) []byte {
	b = fmt.Appendf(b, "\x1b[?%d", code)
	if on {
		return append(b, 'h')
	}
	return append(b, 'l')
}
func displayWidth(s string) int {
	n := 0
	for _, r := range s {
		n += max(phosphor.CodepointWidth(r), 0)
	}
	return n
}
func paneLabel(label, state string, cols int) string {
	var clean strings.Builder
	for _, r := range label {
		if !unicode.IsControl(r) {
			clean.WriteRune(r)
		}
	}
	hint := "jk: sessions"
	prefix := " ptyd · " + state + " · "
	if cols < displayWidth(prefix)+displayWidth(hint)+6 {
		return clipPane("ptyd · "+hint, cols)
	}
	budget := max(cols-displayWidth(prefix)-displayWidth(hint)-5, 0)
	return prefix + clipPane(clean.String(), budget) + "  ·  " + hint
}
func clipPane(s string, width int) string {
	if displayWidth(s) <= width {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		w := max(phosphor.CodepointWidth(r), 0)
		if n+w > max(width-1, 0) {
			break
		}
		b.WriteRune(r)
		n += w
	}
	if width > 0 {
		b.WriteRune('…')
	}
	return b.String()
}

func (p *pane) restore(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	limits := phosphor.SnapshotLimits{MaxBytes: 128 << 20, MaxCells: int(min(uint64(^uint(0)>>1), uint64(65535)*65535))}
	if err := p.screen.RestoreSnapshot(data, limits); err != nil {
		return fmt.Errorf("attached screen: %w", err)
	}
	p.changed = true
	p.haveModes = false
	if !p.initialized {
		p.initialized = true
		return p.screen.WriteScrollback(historyOutput{p})
	}
	clear(p.history)
	p.history = p.history[:0]
	p.historyBytes = 0
	return nil
}

// One bounded queue of exact scroll events: equal text and style changes are
// distinct rows, even when the emulator's retained history has wrapped.
type historyRow struct {
	data []byte
	cols int
}
type historyOutput struct{ p *pane }

func (w historyOutput) Write(row []byte) (int, error) {
	const budget = 1 << 20
	p := w.p
	if len(row) > budget {
		return len(row), nil
	}
	for p.historyBytes+len(row) > budget && len(p.history) > 0 {
		p.historyBytes -= len(p.history[0].data)
		p.history[0] = historyRow{}
		p.history = p.history[1:]
	}
	cols, _ := p.screen.Size()
	p.history = append(p.history, historyRow{data: append([]byte(nil), row...), cols: cols})
	p.historyBytes += len(row)
	return len(row), nil
}
func (p *pane) appendHistory(b []byte) ([]byte, bool) {
	added := len(p.history) > 0
	emit := func(row []byte) {
		b = append(b, "\x1b[1;1H"...)
		b = append(b, row...)
		b = fmt.Appendf(b, "\x1b[%d;1H\r\n", p.rows)
	}
	for _, row := range p.history {
		if row.cols == p.cols {
			emit(row.data)
			continue
		}
		// A window resize may overtake queued output. Reflow old-width rows before
		// painting them; the ANSI row format requires matching source/host widths.
		tmp := phosphor.New(row.cols, 1, phosphor.WithScrollback(0))
		tmp.Write(row.data)
		end := 0
		for x := 0; x < row.cols; x++ {
			c := tmp.Cell(x, 0)
			if c.Rune != 0 || c.Style != (phosphor.Style{}) || c.Link != (phosphor.Hyperlink{}) {
				end = x
			}
		}
		tmp.Write([]byte(fmt.Sprintf("\x1b[?7h\x1b[1;%dH", end+1)))
		tmp.Resize(p.cols, max(row.cols, 1))
		for y := 0; y <= tmp.Cursor().Y; y++ {
			emit(tmp.AppendRowANSI(nil, y))
		}
	}
	clear(p.history)
	p.history = p.history[:0]
	p.historyBytes = 0
	return b, added
}

func (p *pane) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finished = true
	p.changed = true
	p.haveModes = false // EOF must flush even an unfinished synchronized update.
}
