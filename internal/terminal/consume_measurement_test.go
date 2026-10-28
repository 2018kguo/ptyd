package terminal

import (
	"bytes"
	"context"
	"testing"

	"github.com/2018kguo/phosphor"
)

type consumeMeasurementReply func()

func (f consumeMeasurementReply) Write(p []byte) (int, error) {
	f()
	return len(p), nil
}

func TestConsumeCountsParsedBytesBeforeClientOverflow(t *testing.T) {
	m := NewManager()
	term := fakeTerminal(m, 0, 80, 24)
	a := &attacher{wake: make(chan struct{}, 1)}
	term.attachers = map[*attacher]struct{}{a: {}}
	var consumed int64
	replies := 0
	var earlyCredit *int64
	term.screen = phosphor.New(80, 24, phosphor.WithReply(consumeMeasurementReply(func() {
		// A reply happens during parsing. This read's bytes must not be
		// credited until parsing and the remaining output work finish.
		if got := m.bytesEmitted.Load(); got != consumed {
			earlyCredit = &got
		}
		replies++
	})))
	input := []byte("\x1b]0;early-state-must-survive\a\x1b[?1049h\x1b[31m")
	input = append(input, bytes.Repeat([]byte("log line with terminal state retained\r\n"), 65000)...)
	input = append(input, "\x1b[2J\x1b[Hfinal-sentinel\x1b[5n"...)
	total := len(input)
	reference := phosphor.New(80, 24)
	reference.Write(input)
	for len(input) > 0 {
		n := min(readBufSize, len(input))
		term.consume(input[:n])
		if earlyCredit != nil {
			t.Fatalf("credited unparsed bytes: got %d, want %d", *earlyCredit, consumed)
		}
		consumed += int64(n)
		if got := m.bytesEmitted.Load(); got != consumed {
			t.Fatalf("count=%d, want %d", got, consumed)
		}
		input = input[n:]
	}
	if !a.overflow || len(a.buf) != 0 {
		t.Fatal("fixture did not overflow slow attacher")
	}
	if term.screen.Title() != reference.Title() || !term.screen.Modes().AltScreen || replies == 0 {
		t.Fatal("parser skipped terminal state or the reply query")
	}
	for y := range 24 {
		for x := range 80 {
			if term.screen.Cell(x, y) != reference.Cell(x, y) {
				t.Fatalf("parsed screen differs at %d,%d", x, y)
			}
		}
	}
	if term.ring.LatestSeq() != 64999 || term.ring.n != ringCapacity {
		t.Fatalf("line processing or retention changed: latest=%d retained=%d", term.ring.LatestSeq(), term.ring.n)
	}
	at := &Attachment{t: term, a: a}
	paint, err := at.Read(context.Background())
	if err != nil || !bytes.Contains(paint, []byte("final-sentinel")) {
		t.Fatalf("overflow repaint missing final state: %v", err)
	}
	if consumed != int64(total) {
		t.Fatal("not all input consumed")
	}
}
