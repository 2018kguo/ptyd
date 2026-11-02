package terminal

import (
	"fmt"
	"strings"
	"testing"
	"unsafe"
)

// Lines handed out alias the ring's text arena; later writes, evictions
// and compaction must never change their text.
func TestRingArenaTextIsImmutable(t *testing.T) {
	r := NewRing(64, 4<<10)
	var held []Line
	var want []string
	for i := range 2000 {
		text := fmt.Sprintf("\x1b[3%dmline %d %s\x1b[0m", i%8, i, strings.Repeat("z", i%97))
		r.Write([]byte(text + "\r\n"))
		if i%37 == 0 {
			l := r.Snapshot(-1, 1).Lines[0]
			held = append(held, l)
			want = append(want, stripANSI(text))
		}
		if i%101 == 0 {
			r.Compact()
		}
	}
	for i, l := range held {
		if l.Text != want[i] {
			t.Fatalf("held line %d changed: %q, want %q", l.Seq, l.Text, want[i])
		}
	}
}

// Short lines share arena allocations instead of allocating one each.
func TestRingArenaSharesAllocations(t *testing.T) {
	r := newBenchRing()
	chunk := []byte(strings.Repeat("a short log line\r\n", 400))
	r.Write(chunk) // grow slots and the arena
	if n := testing.AllocsPerRun(20, func() { r.Write(chunk) }); n > 20 {
		t.Fatalf("400 short lines allocated %.0f times", n)
	}
}

// Attention events outlive ring lines, so they must not pin the arena.
func TestAttentionDoesNotAliasArena(t *testing.T) {
	term := fakeTerminal(NewManager(), 0, 80, 24)
	watches, err := compileWatches([]WatchSpec{{Name: "hit", Pattern: "hit"}})
	if err != nil {
		t.Fatal(err)
	}
	term.watches = watches
	term.ring.Write([]byte("a hit here\n"))
	line := term.ring.Snapshot(-1, 1).Lines[0]
	term.processLine(line)
	term.mu.Lock()
	got := term.attention[len(term.attention)-1].Line.Text
	term.mu.Unlock()
	if got != line.Text {
		t.Fatalf("attention text %q, want %q", got, line.Text)
	}
	if unsafe.StringData(got) == unsafe.StringData(line.Text) {
		t.Fatal("attention event aliases the ring arena")
	}
}
