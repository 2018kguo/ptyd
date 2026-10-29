package client

import (
	"bytes"
	"io"
	"strings"
	"unicode"
)

const titlePush = "\x1b[22;0t"
const titlePop = "\x1b[23;0t"

func windowTitle(label string) string {
	var clean strings.Builder
	for _, r := range label {
		if !unicode.IsControl(r) {
			clean.WriteRune(r)
		}
	}
	text := []rune(clean.String())
	if len(text) > 120 {
		text = append(text[:119], '…')
	}
	return "ptyd · " + string(text) + " · jk: return"
}

func setWindowTitle(w io.Writer, label string) {
	_, _ = io.WriteString(w, "\x1b]2;"+windowTitle(label)+"\x1b\\")
}

// titleWriter leaves terminal output as a byte stream. Only window-title OSC
// commands gain the ptyd prefix and return hint, so the app can still update
// its title without hiding which terminal view owns this window. No screen
// rendering, cursor slots, colors or clipboard commands are intercepted.
type titleWriter struct {
	out    io.Writer
	state  byte
	header []byte // OSC command number, bounded before forwarding
	title  bool
}

func (w *titleWriter) Write(p []byte) (int, error) {
	if w.state == 0 && bytes.IndexByte(p, 0x1b) < 0 {
		return w.out.Write(p)
	}
	var out bytes.Buffer
	for _, b := range p {
		switch w.state {
		case 0:
			if b == 0x1b {
				w.state = 1
			} else {
				out.WriteByte(b)
			}
		case 1:
			if b == 0x1b {
				out.WriteByte(0x1b)
				continue
			}
			if b == ']' {
				w.header = []byte{0x1b, ']'}
				w.state = 2
				continue
			}
			out.WriteByte(0x1b)
			out.WriteByte(b)
			if b == 'P' || b == '_' || b == '^' || b == 'X' {
				w.state = 5
			} else {
				w.state = 0
			}
		case 2:
			w.header = append(w.header, b)
			if b == ';' {
				w.title = bytes.Equal(w.header, []byte("\x1b]0;")) || bytes.Equal(w.header, []byte("\x1b]2;"))
				out.Write(w.header)
				w.header = nil
				if w.title {
					out.WriteString("ptyd · ")
				}
				w.state = 3
			} else if len(w.header) > 16 || b < '0' || b > '9' {
				if b == 0x1b {
					out.Write(w.header[:len(w.header)-1])
				} else {
					out.Write(w.header)
				}
				w.header = nil
				w.title = false
				if b == 7 || b == 0x18 || b == 0x1a {
					w.state = 0
				} else if b == 0x1b {
					w.state = 4
				} else {
					w.state = 3
				}
			}
		case 3:
			if b == 0x1b {
				w.state = 4
				continue
			}
			if b == 7 && w.title {
				out.WriteString(" · jk: return")
			}
			out.WriteByte(b)
			if b == 7 || b == 0x18 || b == 0x1a {
				w.state = 0
				w.title = false
			}
		case 4:
			if b == ']' {
				w.header = []byte{0x1b, ']'}
				w.state = 2
				w.title = false
				continue
			}
			if b == 0x1b {
				out.WriteByte(0x1b)
				w.state = 1
				w.title = false
				continue
			}
			if b == '\\' && w.title {
				out.WriteString(" · jk: return")
			}
			out.WriteByte(0x1b)
			out.WriteByte(b)
			// An Escape other than ST cancels OSC and starts a new control.
			if b == '\\' {
				w.state = 0
				w.title = false
			} else if b == 'P' || b == '_' || b == '^' || b == 'X' {
				w.state = 5
				w.title = false
			} else {
				w.state = 0
				w.title = false
			}
		case 5:
			out.WriteByte(b)
			if b == 0x1b {
				w.state = 6
			} else if b == 0x18 || b == 0x1a {
				w.state = 0
			}
		case 6:
			out.WriteByte(b)
			if b == '\\' {
				w.state = 0
			} else if b != 0x1b {
				w.state = 5
			}
		}
	}
	if _, err := w.out.Write(out.Bytes()); err != nil {
		return 0, err
	}
	return len(p), nil
}
func (w *titleWriter) flush() {
	if len(w.header) > 0 {
		_, _ = w.out.Write(w.header)
		w.header = nil
	} else if w.state == 1 || w.state == 4 {
		_, _ = w.out.Write([]byte{0x1b})
	}
}
