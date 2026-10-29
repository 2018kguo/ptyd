package client

import (
	"bytes"
	"strings"
	"testing"
)

func TestAttachedTitlePreservesNativeTerminalOutput(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"text", "hello 雪\r\n\x1b[38;2;13;22;31mcolor\x1b[0m", "hello 雪\r\n\x1b[38;2;13;22;31mcolor\x1b[0m"},
		{"title BEL", "before\x1b]2;Editor\aafter", "before\x1b]2;ptyd · Editor · jk: return\aafter"},
		{"title ST", "\x1b]0;Codex\x1b\\", "\x1b]0;ptyd · Codex · jk: return\x1b\\"},
		{"repeated Escape", "\x1b\x1b]2;Codex\a", "\x1b\x1b]2;ptyd · Codex · jk: return\a"},
		{"restarted OSC", "\x1b]8;;old\x1b]2;Codex\a", "\x1b]8;;old\x1b]2;ptyd · Codex · jk: return\a"},
		{"clipboard", "\x1b]52;c;clipboard\x1b\\", "\x1b]52;c;clipboard\x1b\\"},
		{"link", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\"},
		{"DCS", "\x1bPpayload\x1b]2;literal\a\x1b\\", "\x1bPpayload\x1b]2;literal\a\x1b\\"},
		{"APC", "\x1b_Gpayload\x1b]2;literal\a\x1b\\", "\x1b_Gpayload\x1b]2;literal\a\x1b\\"},
		{"partial", "x\x1b]2", "x\x1b]2"},
		{"cancelled", "\x1b]2;discard\x18ok", "\x1b]2;ptyd · discard\x18ok"},
		{"invalid header", "\x1b]9\x1b[31mtext", "\x1b]9\x1b[31mtext"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for cut := 0; cut <= len(tc.input); cut++ {
				var out bytes.Buffer
				w := titleWriter{out: &out}
				if _, err := w.Write([]byte(tc.input[:cut])); err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write([]byte(tc.input[cut:])); err != nil {
					t.Fatal(err)
				}
				w.flush()
				if out.String() != tc.want {
					t.Fatalf("cut%d changed output: got%q want%q", cut, out.String(), tc.want)
				}
			}
		})
	}
}
func TestWindowLabelCannotInjectTerminalControls(t *testing.T) {
	title := windowTitle("Editor\x1b]52;c;bad\a\nname")
	if strings.ContainsAny(title, "\x1b\a\n") || !strings.HasPrefix(title, "ptyd · ") || !strings.Contains(title, "jk: return") {
		t.Fatalf("invalid title%q", title)
	}
}
func TestWindowTitleParsingKeepsBoundedHeader(t *testing.T) {
	var out bytes.Buffer
	w := titleWriter{out: &out}
	input := "\x1b]" + strings.Repeat("1", 10000) + ";payload\a"
	for _, b := range []byte(input) {
		if _, err := w.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
		if len(w.header) > 16 {
			t.Fatal("unbounded OSC header")
		}
	}
	w.flush()
	if out.String() != input {
		t.Fatal("oversized unrelated command changed")
	}
}
