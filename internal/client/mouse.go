package client

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MouseEvent uses terminal coordinates (1-based) and the xterm button/modifier
// encoding. SGR releases use a separate final byte; legacy releases use button3.
type MouseEvent struct {
	Button, X, Y int
	Release      bool
}

func (m MouseEvent) LeftPress() bool { return m.Button == 0 && !m.Release }

// ParseMouseReport accepts one complete SGR, legacy X10/UTF-8 or urxvt report.
// It does not reinterpret partial, malformed or oversized coordinates as keys.
func ParseMouseReport(p []byte) (MouseEvent, bool) {
	if bytes.HasPrefix(p, []byte("\x1b[M")) {
		at := 3
		var values [3]int
		for i := range values {
			if at >= len(p) || !utf8.FullRune(p[at:]) {
				return MouseEvent{}, false
			}
			r, n := utf8.DecodeRune(p[at:])
			if r == utf8.RuneError && n == 1 {
				r = rune(p[at])
			}
			values[i] = int(r) - 32
			at += n
		}
		if at != len(p) || values[0] < 0 || values[1] < 1 || values[2] < 1 {
			return MouseEvent{}, false
		}
		return MouseEvent{Button: values[0], X: values[1], Y: values[2], Release: values[0]&3 == 3}, true
	}
	if len(p) < 7 || len(p) > 128 || !bytes.HasPrefix(p, []byte("\x1b[")) {
		return MouseEvent{}, false
	}
	final := p[len(p)-1]
	if final != 'M' && final != 'm' {
		return MouseEvent{}, false
	}
	body := string(p[2 : len(p)-1])
	sgr := strings.HasPrefix(body, "<")
	body = strings.TrimPrefix(body, "<")
	parts := strings.Split(body, ";")
	if len(parts) != 3 {
		return MouseEvent{}, false
	}
	var values [3]int
	for i, part := range parts {
		if part == "" {
			return MouseEvent{}, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return MouseEvent{}, false
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil || n > 65535 {
			return MouseEvent{}, false
		}
		values[i] = n
	}
	if !sgr {
		values[0] -= 32
	}
	if values[0] < 0 || values[1] < 1 || values[2] < 1 {
		return MouseEvent{}, false
	}
	return MouseEvent{Button: values[0], X: values[1], Y: values[2], Release: final == 'm' || (!sgr && values[0]&3 == 3)}, true
}
