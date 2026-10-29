package client

import (
	"bytes"
	"fmt"

	"github.com/2018kguo/phosphor"
)

// Keep separate buffers for the last painted row and its replacement. Comparing
// exact rendered bytes includes colors, hyperlinks and Unicode join modes.
type paneRow struct {
	previous, next []byte
	changed        bool
}

func (r paneRow) current() []byte {
	if r.changed {
		return r.next
	}
	return r.previous
}

type paneScroll struct {
	top, bottom, offset int // half-open region; positive offset scrolls up
}

func (s paneScroll) source(y int) int {
	if y < s.top || y >= s.bottom {
		return y
	}
	if source := y + s.offset; source >= s.top && source < s.bottom {
		return source
	}
	return -1 // Newly exposed rows must be painted, including styled blanks.
}

func (p *pane) appendDamage(b []byte, view *phosphor.Terminal, force bool) []byte {
	_, height := view.Size()
	if len(p.painted) != height {
		p.painted = make([]paneRow, height)
		force = true
	}
	for y := range p.painted {
		r := &p.painted[y]
		r.changed = force || view.RowDirty(y)
		if r.changed {
			r.next = view.AppendRowANSI(r.next[:0], y)
		}
	}
	var scroll paneScroll
	if !force {
		scroll = choosePaneScroll(p.painted)
	}
	if scroll.offset != 0 {
		direction := 'S'
		if scroll.offset < 0 {
			direction = 'T'
		}
		b = fmt.Appendf(b, "\x1b[0m\x1b[%d;%dr\x1b[%d%c\x1b[r", scroll.top+1, scroll.bottom, max(scroll.offset, -scroll.offset), direction)
	}
	for y, r := range p.painted {
		source := scroll.source(y)
		if !force && source >= 0 && bytes.Equal(r.current(), p.painted[source].previous) {
			continue
		}
		b = fmt.Appendf(b, "\x1b[%d;1H", y+1)
		b = append(b, r.current()...)
	}
	for i := range p.painted {
		r := &p.painted[i]
		if r.changed {
			r.previous, r.next = r.next, r.previous
		}
	}
	return b
}

// Try small wheel/keyboard scrolls in both directions. An exact matching run
// can move in the host terminal instead of retransmitting its rows. Limit the
// search work, and only use a move when its total byte cost beats repainting.
func choosePaneScroll(rows []paneRow) paneScroll {
	best := paneScroll{}
	bestCost := panePaintCost(rows, best)
	if bestCost == 0 {
		return best
	}
	for offset := -min(16, len(rows)-1); offset <= min(16, len(rows)-1); offset++ {
		if offset == 0 {
			continue
		}
		start, longest, length := 0, 0, 0
		for y := max(-offset, 0); y < min(len(rows)-offset, len(rows)); y++ {
			if bytes.Equal(rows[y].current(), rows[y+offset].previous) {
				length++
				if length > longest {
					start, longest = y-length+1, length
				}
			} else {
				length = 0
			}
		}
		if longest < 3 {
			continue
		}
		s := paneScroll{top: min(start, start+offset), bottom: max(start+longest, start+longest+offset), offset: offset}
		// Covers region setup/reset and scroll command; row positions are a
		// conservative fixed allowance rather than another serialization pass.
		if cost := panePaintCost(rows, s) + 40; cost < bestCost {
			best, bestCost = s, cost
		}
	}
	return best
}

func panePaintCost(rows []paneRow, scroll paneScroll) int {
	cost := 0
	for y, row := range rows {
		source := scroll.source(y)
		if source < 0 || !bytes.Equal(row.current(), rows[source].previous) {
			cost += len(row.current()) + 12
		}
	}
	return cost
}
