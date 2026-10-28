package client

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/2018kguo/phosphor"
	"golang.org/x/term"
)

func TestColorProbePreservesKeysAndTerminalMode(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	u := newUserTerminal(t, 80, 24)
	palette := phosphor.DefaultPalette()
	palette.Foreground = phosphor.RGB{R: 216, G: 222, B: 233}
	palette.Background = phosphor.RGB{R: 40, G: 44, B: 52}
	u.mu.Lock()
	u.screen = phosphor.New(80, 24, phosphor.WithPalette(palette), phosphor.WithReply(probeReply(func(p []byte) (int, error) {
		// A key arriving among color replies is replayed, never lost.
		if bytes.HasPrefix(p, []byte("\x1b]10;")) {
			u.master.Write([]byte("i"))
		}
		return u.master.Write(p)
	})))
	u.mu.Unlock()
	before, err := term.GetState(int(u.slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	colors, pending := ProbeColors(context.Background(), u.slave, u.slave)
	if colors == nil || colors.Foreground != "#d8dee9" || colors.Background != "#282c34" || string(pending) != "i" {
		t.Fatalf("profile=%+v keys=%q", colors, pending)
	}
	after, err := term.GetState(int(u.slave.Fd()))
	if err != nil || *before != *after {
		t.Fatal("probe left terminal raw")
	}
}

type probeReply func([]byte) (int, error)

func (f probeReply) Write(p []byte) (int, error) { return f(p) }
func TestColorRepliesAcrossEveryReadSplit(t *testing.T) {
	for _, input := range []string{
		"x\x1b]10;rgb:d8d8/dede/e9e9\x1b\\y\x1b]11;rgb:28/2c/34\a",
		"\x1b[200~paste\x1b]11;rgb:ff/ff/ff\a\x1b[201~\x1b]10;rgb:d8d8/dede/e9e9\x1b\\\x1b]11;rgb:28/2c/34\a",
		"\x1bPpayload\x1b]11;rgb:ff/ff/ff\a\x1b\\\x1b]10;rgb:d8d8/dede/e9e9\x1b\\\x1b]11;rgb:28/2c/34\a",
	} {
		for cut := 0; cut <= len(input); cut++ {
			var parser sequenceReader
			values := map[string]string{}
			var keys bytes.Buffer
			emit := func(p []byte, osc bool) {
				if osc {
					if slot, value, ok := parseColorReply(p); ok {
						values[slot] = value
						return
					}
				}
				keys.Write(p)
			}
			parser.feed([]byte(input[:cut]), emit)
			parser.feed([]byte(input[cut:]), emit)
			keys.Write(parser.flush())
			if values["10"] != "#d8dee9" || values["11"] != "#282c34" || len(values) != 2 {
				t.Fatalf("cut%d: %v", cut, values)
			}
			if strings.Contains(input, "paste") && !strings.Contains(keys.String(), "paste\x1b]11;rgb:ff/ff/ff\a") {
				t.Fatal("probe consumed pasted bytes")
			}
			if strings.Contains(input, "payload") && !strings.Contains(keys.String(), "payload\x1b]11;rgb:ff/ff/ff\a") {
				t.Fatal("probe consumed opaque DCS")
			}
		}
	}
	for _, input := range []string{"\x1b]11;rgb:gg/ff/ff\a", "\x1b]12;rgb:ff/ff/ff\a", "\x1b]11;rgb:fffff/ff/ff\a"} {
		if _, _, ok := parseColorReply([]byte(input)); ok {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestNonTTYColorProbeDoesNotConsumeInput(t *testing.T) {
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer out.Close()
	out.Write([]byte("keys"))
	if colors, pending := ProbeColors(context.Background(), in, out); colors != nil || pending != nil {
		t.Fatal("non-TTY probe")
	}
	b := make([]byte, 4)
	in.Read(b)
	if string(b) != "keys" {
		t.Fatal("probe consumed pipe input")
	}
}
