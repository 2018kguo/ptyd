package client

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHomeRowControlsAcrossEveryReadBoundary(t *testing.T) {
	cases := []struct {
		name, input, forward, tail string
		detach                     bool
	}{
		{"plain", "sent\rjk:q\r", "sent\r", ":q\r", true},
		{"kitty", "\x1b[106u\x1b[107u?q", "", "?q", true},
		{"kitty releases", "\x1b[106;1:1u\x1b[106;1:3u\x1b[107;1:1u", "", "", true},
		{"modifyOtherKeys", "\x1b[27;1;106~\x1b[27;1;107~", "", "", true},
		{"paste", "\x1b[200~雪 jk\n:q\r\x1b[201~", "\x1b[200~雪 jk\n:q\r\x1b[201~", "", false},
		{"paste then detach", "\x1b[200~jk\x1b[201~jkq", "\x1b[200~jk\x1b[201~", "q", true},
		{"alt", "\x1bj\x1bk", "\x1bj\x1bk", "", false},
		{"modified", "\x1b[106;5u\x1b[107;5u", "\x1b[106;5u\x1b[107;5u", "", false},
		{"repeat", "\x1b[106;1:2u\x1b[107;1:2u", "\x1b[106;1:2u\x1b[107;1:2u", "", false},
		{"native controls", "\x1c\x1c\x03\x1b\x1b\x1b[24~", "\x1c\x1c\x03\x1b\x1b\x1b[24~", "", false},
		{"mouse", "\x1b[Mjk!", "\x1b[Mjk!", "", false},
		{"UTF8 mouse", "\x1b[M \xc4\x80jk", "\x1b[M \xc4\x80jk", "", false},
		{"kitty text", "\x1b[106;1;106u\x1b[107;1;107u", "", "", true},
		{"kitty layout", "\x1b[106::106;1u\x1b[107::107;1u", "", "", true},
		{"OSC", "\x1b]52;c;jk\a", "\x1b]52;c;jk\a", "", false},
		{"DCS", "\x1bP1+rjk\x1b\\", "\x1bP1+rjk\x1b\\", "", false},
		{"OSC title", "\x1b]ljk\x1b\\", "\x1b]ljk\x1b\\", "", false},
		{"OSC icon", "\x1b]Ljk\x1b\\", "\x1b]Ljk\x1b\\", "", false},
		{"DCS version", "\x1bP>|terminal jk\x1b\\", "\x1bP>|terminal jk\x1b\\", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for cut := 0; cut <= len(tc.input); cut++ {
				var f detachFilter
				now := time.Unix(1, 0)
				out, detached, tail := f.feed([]byte(tc.input[:cut]), now)
				if detached {
					tail = append(bytes.Clone(tail), []byte(tc.input[cut:])...)
				} else {
					next, d, rest := f.feed([]byte(tc.input[cut:]), now.Add(time.Millisecond))
					out = append(out, next...)
					detached, tail = d, rest
				}
				if !detached {
					out = append(out, f.flushSequence()...)
				}
				if string(out) != tc.forward || detached != tc.detach || string(tail) != tc.tail {
					t.Fatalf("cut %d: forward=%q detach=%v tail=%q", cut, out, detached, tail)
				}
			}
		})
	}
}

func TestLiteralJKAndPrefixDeadline(t *testing.T) {
	for _, delay := range []time.Duration{detachWindow, detachWindow + time.Second} {
		var f detachFilter
		now := time.Unix(1, 0)
		if out, d, _ := f.feed([]byte("j"), now); len(out) != 0 || d {
			t.Fatal("prefix not held")
		}
		out, d, _ := f.feed([]byte("k"), now.Add(delay))
		if string(out) != "jk" || d {
			t.Fatalf("late k became a command: %q %v", out, d)
		}
	}
	var f detachFilter
	now := time.Unix(1, 0)
	f.feed([]byte("j"), now)
	if out := f.expire(now.Add(detachWindow)); string(out) != "j" {
		t.Fatalf("lone j lost: %q", out)
	}
	if out, d, _ := f.feed([]byte("next"), now.Add(time.Second)); string(out) != "next" || d {
		t.Fatal("timeout corrupted later input")
	}
}

func TestControlParserBoundsAndBytewisePaste(t *testing.T) {
	for _, input := range []string{
		"\x1b[" + strings.Repeat("1", 10000) + "jk",
		"\x1b]52;c;" + strings.Repeat("jk", 10000) + "\a",
		"\x1b[200~" + strings.Repeat("雪jk", 10000) + "\x1b[201~",
	} {
		var f detachFilter
		var got []byte
		for _, b := range []byte(input) {
			out, d, _ := f.feed([]byte{b}, time.Unix(1, 0))
			if d {
				t.Fatal("framed content became a local command")
			}
			got = append(got, out...)
			if len(f.pending) > maxKeySequence || len(f.held) > maxKeySequence {
				t.Fatal("parser retained unbounded data")
			}
		}
		got = append(got, f.flushSequence()...)
		if string(got) != input {
			t.Fatal("parser changed input bytes")
		}
	}
}

func TestPartialEscapeTimeoutReleasesAllOriginalBytes(t *testing.T) {
	var f detachFilter
	f.feed([]byte("j\x1b["), time.Unix(1, 0))
	if got := string(f.flushSequence()); got != "j\x1b[" {
		t.Fatalf("timeout lost bytes: %q", got)
	}
	if out, d, _ := f.feed([]byte("k"), time.Unix(1, 0)); string(out) != "k" || d {
		t.Fatal("timed-out prefix stayed armed")
	}
}

func TestTimedOutFragmentsKeepTheirInputFraming(t *testing.T) {
	for _, input := range []string{"\x1b[200~jk\x1b[201~", "\x1b[M \xc4\x80jk"} {
		for cut := 1; cut < len(input); cut++ {
			var f detachFilter
			now := time.Unix(1, 0)
			out, d, _ := f.feed([]byte(input[:cut]), now)
			if d {
				t.Fatalf("early detach at cut%d", cut)
			}
			out = append(out, f.flushSequence()...)
			next, d, _ := f.feed([]byte(input[cut:]), now.Add(time.Second))
			out = append(out, next...)
			out = append(out, f.flushSequence()...)
			if d || string(out) != input {
				t.Fatalf("cut%d lost framing: detach%v bytes%q", cut, d, out)
			}
		}
	}
}
func TestAltPrefixesAndBareEscapeDoNotDisableDetach(t *testing.T) {
	for _, prefix := range []string{"\x1b]", "\x1bP", "\x1b_", "\x1b^", "\x1bX", "\x1b"} {
		var f detachFilter
		now := time.Unix(1, 0)
		out, _, _ := f.feed([]byte(prefix), now)
		out = append(out, f.flushSequence()...)
		next, d, _ := f.feed([]byte("jk"), now.Add(time.Second))
		out = append(out, next...)
		if !d || string(out) != prefix {
			t.Fatalf("Alt prefix latched input mode: prefix%q detach%v out%q", prefix, d, out)
		}
	}
}
