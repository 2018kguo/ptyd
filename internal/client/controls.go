package client

import (
	"bytes"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DetachKeyHelp names the home-row sequence, typed in order rather than held.
const DetachKeyHelp = "jk (j, then k quickly)"

const (
	detachWindow   = 200 * time.Millisecond
	sequenceWindow = 40 * time.Millisecond
	maxKeySequence = 128
)

// detachFilter keeps a possible j until k follows or the short deadline ends.
// It recognizes input frames before shortcuts: pasted text, mouse coordinates,
// escape strings and modified keys must never become local commands.
// All clocks and output are supplied by Attach; this parser performs no I/O.
type detachFilter struct {
	held, pending []byte
	pendingSent   int
	deadline      time.Time
	pasteEnd      int
	paste         bool
	stringCode    byte
	stringEsc     bool
	longCSI       bool
}

func (f *detachFilter) release(out []byte) []byte {
	out = append(out, f.held...)
	f.held = nil
	f.deadline = time.Time{}
	return out
}

func (f *detachFilter) expire(now time.Time) []byte {
	if len(f.held) > 0 && !now.Before(f.deadline) {
		return f.release(nil)
	}
	return nil
}

// flushSequence forwards partial bytes once, retaining their framing so slow
// paste/mouse/keyboard continuations cannot become local commands.
func (f *detachFilter) flushSequence() []byte {
	out := f.release(nil)
	out = append(out, f.pending[f.pendingSent:]...)
	f.pendingSent = len(f.pending)
	return out
}

func (f *detachFilter) feed(p []byte, now time.Time) (out []byte, detached bool, tail []byte) {
	out = f.expire(now)
	sent := f.pendingSent
	if len(f.pending) == 1 && sent == 1 && len(p) > 0 && !bytes.ContainsAny(p[:1], "[O]P_^X") {
		// A timed-out bare Escape already reached the app. A later ordinary
		// letter is a fresh key, rather than an indefinitely pending Alt key.
		f.pending, sent = nil, 0
	}
	if len(f.pending) > 0 {
		p = append(f.pending, p...)
	}
	f.pending, f.pendingSent = nil, 0
	for len(p) > 0 {
		if f.paste {
			b := p[0]
			out = append(out, b)
			p = p[1:]
			const end = "\x1b[201~"
			if b == end[f.pasteEnd] {
				f.pasteEnd++
			} else if b == end[0] {
				f.pasteEnd = 1
			} else {
				f.pasteEnd = 0
			}
			if f.pasteEnd == len(end) {
				f.paste, f.pasteEnd = false, 0
			}
			continue
		}
		if f.stringCode != 0 {
			b := p[0]
			out = append(out, b)
			p = p[1:]
			if b == 0x18 || b == 0x1a || (b == 7 && f.stringCode == ']') || (f.stringEsc && b == '\\') {
				f.stringCode = 0
			}
			f.stringEsc = b == 0x1b
			continue
		}
		if f.longCSI {
			b := p[0]
			out = append(out, b)
			p = p[1:]
			if b >= 0x40 && b <= 0x7e {
				f.longCSI = false
			}
			continue
		}
		n := 1
		if p[0] == 0x1b {
			if len(p) < 2 {
				f.pending, f.pendingSent = bytes.Clone(p), sent
				break
			}
			n = 2
			switch p[1] {
			case '[':
				n = 0
				for i := 2; i < len(p) && i < maxKeySequence; i++ {
					if p[i] >= 0x40 && p[i] <= 0x7e || p[i] < 0x20 || p[i] > 0x3f {
						n = i + 1
						break
					}
				}
				if n == 0 {
					if len(p) < maxKeySequence {
						f.pending, f.pendingSent = bytes.Clone(p), sent
						break
					}
					n, f.longCSI = maxKeySequence, true
				}
			case 'O':
				if len(p) < 3 {
					f.pending, f.pendingSent = bytes.Clone(p), sent
					break
				}
				n = 3
			case ']', 'P', '_', '^', 'X':
				if len(p) < 3 {
					f.pending, f.pendingSent = bytes.Clone(p), sent
					break
				}
				// Bare Alt+] / Alt+P are keys too. Recognize response headers
				// before entering an escape string, not just the Alt prefix.
				if (p[2] >= '0' && p[2] <= '9') || (p[1] == 'P' && strings.ContainsRune("+>!", rune(p[2]))) || (p[1] == '_' && p[2] == 'G') || (p[1] == ']' && (p[2] == 'l' || p[2] == 'L')) {
					f.stringCode = p[1]
				}
			}
			if len(f.pending) > 0 {
				break
			}
		}
		if n == 3 && bytes.Equal(p[:n], []byte("\x1b[M")) {
			var complete bool
			n, complete = MouseReportLength(p)
			if !complete {
				f.pending, f.pendingSent = bytes.Clone(p), sent
				break
			}
		}
		key := p[:n]
		p = p[n:]
		letter, event := homeRowKey(key)
		forwardKey := key
		if sent > 0 {
			letter, event = 0, 0
			forwardKey = key[sent:]
			sent = 0
		}
		if letter == 'j' && event == 3 && len(f.held) > 0 && len(f.held)+len(key) <= maxKeySequence {
			f.held = append(f.held, key...)
			continue
		}
		if letter == 'k' && event == 1 && len(f.held) > 0 {
			f.held = nil
			f.deadline = time.Time{}
			return out, true, p
		}
		out = f.release(out)
		if letter == 'j' && event == 1 {
			f.held = bytes.Clone(key)
			f.deadline = now.Add(detachWindow)
			continue
		}
		out = append(out, forwardKey...)
		if bytes.Equal(key, []byte("\x1b[200~")) {
			f.paste = true
		}
	}
	return out, false, nil
}

// Only unmodified literal j/k events count. Unknown/modified encodings retain
// their original bytes, including the control keys apps use themselves.
func homeRowKey(p []byte) (letter byte, event byte) {
	if len(p) == 1 && (p[0] == 'j' || p[0] == 'k') {
		return p[0], 1
	}
	if len(p) < 4 || p[0] != 0x1b || p[1] != '[' {
		return 0, 0
	}
	body := string(p[2 : len(p)-1])
	parts := strings.Split(body, ";")
	var code string
	event = 1
	switch p[len(p)-1] {
	case 'u':
		if len(parts) > 3 {
			return 0, 0
		}
		codes := strings.Split(parts[0], ":")
		if len(codes) > 3 || codes[0] == "" {
			return 0, 0
		}
		for _, value := range codes {
			if value == "" {
				continue
			}
			point, err := strconv.ParseUint(value, 10, 32)
			if err != nil || point > utf8.MaxRune {
				return 0, 0
			}
		}
		code = codes[0]
		if len(parts) == 3 && parts[2] != "" && parts[2] != code {
			return 0, 0
		}
		if len(parts) >= 2 {
			switch parts[1] {
			case "", "1", "1:1":
			case "1:2":
				event = 2
			case "1:3":
				event = 3
			default:
				return 0, 0
			}
		}
	case '~':
		if len(parts) != 3 || parts[0] != "27" || parts[1] != "1" {
			return 0, 0
		}
		code = parts[2]
	default:
		return 0, 0
	}
	switch code {
	case "106":
		return 'j', event
	case "107":
		return 'k', event
	}
	return 0, 0
}

// MouseReportLength frames legacy X10 and UTF-8 mouse fields conservatively.
// Invalid UTF-8 bytes remain single legacy fields. In an ambiguous legacy
// packet this can include a following key; bytes still pass through unchanged
// rather than interpreting mouse data as a local shortcut.
func MouseReportLength(p []byte) (int, bool) {
	n := 3
	for range 3 {
		if len(p) <= n || !utf8.FullRune(p[n:]) {
			return 0, false
		}
		_, size := utf8.DecodeRune(p[n:])
		n += size
	}
	return n, true
}
