package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
)

func TestKeyboardProbeChecksViewerAndPreservesKeys(t *testing.T) {
	u := newUserTerminal(t, 80, 24)
	u.mu.Lock()
	u.screen = phosphor.New(80, 24, phosphor.WithReply(probeReply(func(p []byte) (int, error) {
		if keyboardReply(p) {
			u.master.Write([]byte("x"))
		}
		return u.master.Write(p)
	})))
	u.mu.Unlock()
	supported, pending := ProbeKeyboard(context.Background(), u.slave, u.slave)
	if !supported || string(pending) != "x" {
		t.Fatalf("support=%v, keys=%q", supported, pending)
	}
	for _, packet := range []string{"\x1b[?u", "\x1b[?999999u", "\x1b[27u", "\x1b[?1;2u"} {
		if keyboardReply([]byte(packet)) {
			t.Fatalf("accepted %q", packet)
		}
	}
}

func TestMouseReportFirstEscapeNeverBecomesAnInterrupt(t *testing.T) {
	packet := "\x1b[<65;203;16M"
	for split := 1; split < len(packet); split++ {
		f := detachFilter{mouseRows: 40, disambiguateEscape: true, filterKeyboardReplies: true}
		first, _, _ := f.feed([]byte(packet[:split]), time.Now())
		timed := f.flushSequence()
		rest, detached, _ := f.feed([]byte(packet[split:]), time.Now())
		if len(first) != 0 || len(timed) != 0 || string(rest) != packet || detached {
			t.Fatalf("split%d writes %q / %q / %q", split, first, timed, rest)
		}
	}
	f := detachFilter{disambiguateEscape: true}
	if out, _, _ := f.feed([]byte("\x1b[27u"), time.Now()); string(out) != "\x1b[27u" {
		t.Fatal("encoded Escape delayed")
	}
	legacy := detachFilter{}
	legacy.feed([]byte{0x1b}, time.Now())
	if string(legacy.flushSequence()) != "\x1b" {
		t.Fatal("legacy Escape swallowed")
	}
	f = detachFilter{filterKeyboardReplies: true, disambiguateEscape: true}
	out, _, _ := f.feed([]byte("\x1b[?0u\x1b[200~\x1b[?0u\x1b[201~"), time.Now())
	if !strings.HasPrefix(string(out), "\x1b[200~") || !strings.Contains(string(out), "\x1b[?0u") {
		t.Fatal("late reply or literal paste mishandled")
	}
}
