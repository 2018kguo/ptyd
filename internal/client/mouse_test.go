package client

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFooterMouseBoundsFollowPaintAndCoordinateUnits(t *testing.T) {
	var output bytes.Buffer
	p := newPane(&output, "work / shell", 80, 24)
	p.Write([]byte("\x1b[?1000h\x1b[?1016hready"))
	if err := p.draw(true); err != nil {
		t.Fatal(err)
	}
	rows, from, to := p.backButton()
	f := detachFilter{mouseRows: rows, backFrom: from, backTo: to}
	packet := "\x1b[<0;76;24M"
	got, detached, _ := f.feed([]byte(packet), time.Now())
	if detached || string(got) != packet {
		t.Fatal("pixel coordinates were treated as footer cells")
	}
	p.Write([]byte("\x1b[?1016l\x1b[?1006h"))
	if err := p.draw(true); err != nil {
		t.Fatal(err)
	}
	if rows, from, _ = p.backButton(); rows != 23 || from == 0 {
		t.Fatal("painted cell-mode footer is not clickable")
	}
	p.resize(80, 30)
	if rows, from, _ = p.backButton(); rows != 0 || from != 0 {
		t.Fatal("resize reused footer targets before repaint")
	}
}

func TestMouseReportParser(t *testing.T) {
	for _, test := range []struct {
		packet string
		want   MouseEvent
	}{
		{"\x1b[<0;72;24M", MouseEvent{Button: 0, X: 72, Y: 24}},
		{"\x1b[<0;72;24m", MouseEvent{Button: 0, X: 72, Y: 24, Release: true}},
		{"\x1b[<65;7;8M", MouseEvent{Button: 65, X: 7, Y: 8}},
		{"\x1b[M \\8", MouseEvent{Button: 0, X: 60, Y: 24}},
		{"\x1b[32;72;24M", MouseEvent{Button: 0, X: 72, Y: 24}},
	} {
		got, ok := ParseMouseReport([]byte(test.packet))
		if !ok || got != test.want {
			t.Fatalf("%q got %+v,%v", test.packet, got, ok)
		}
	}
	for _, packet := range []string{"\x1b[<0;1;", "\x1b[<0;-1;2M", "\x1b[<0;0;2M", "\x1b[<0;1;99999999999M", "\x1b[<0;1;2;3M", "\x1b[<0;x;2M"} {
		if _, ok := ParseMouseReport([]byte(packet)); ok {
			t.Fatalf("accepted malformed packet %q", packet)
		}
	}
}

func TestFooterMouseBackUsesDetachFraming(t *testing.T) {
	now := time.Unix(100, 0)
	packet := "\x1b[<0;76;24Mtail"
	for split := 0; split <= len(packet); split++ {
		f := detachFilter{mouseRows: 23, backFrom: 72, backTo: 79}
		a, first, tail := f.feed([]byte(packet[:split]), now)
		var b []byte
		var second bool
		if first {
			tail = append(tail, packet[split:]...)
		} else {
			b, second, tail = f.feed([]byte(packet[split:]), now)
		}
		if len(a)+len(b) != 0 || !(first || second) || string(tail) != "tail" {
			t.Fatalf("split%d leaked mouse/tail: %q %q %v %q", split, a, b, first || second, tail)
		}
	}
	for _, button := range []int{0, 4, 32, 64, 65} {
		for _, row := range []int{23, 24} {
			f := detachFilter{mouseRows: 23, backFrom: 72, backTo: 79}
			p := fmt.Sprintf("\x1b[<%d;76;%dM", button, row)
			out, detached, _ := f.feed([]byte(p), now)
			if detached != (button == 0 && row == 24) {
				t.Fatalf("wrong footer action for %q", p)
			}
			if row == 23 && string(out) != p {
				t.Fatal("changed app mouse input")
			}
		}
	}
	f := detachFilter{mouseRows: 23, backFrom: 72, backTo: 79}
	p := "\x1b[200~" + packet + "\x1b[201~"
	out, detached, _ := f.feed([]byte(p), now)
	if detached || !strings.Contains(string(out), packet) {
		t.Fatal("pasted mouse lookalike activated back")
	}
}
