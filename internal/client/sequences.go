package client

import "bytes"

// sequenceReader recognizes complete terminal control strings across reads.
// Ordinary bytes, cancelled controls and paste contents remain ordinary input.
// A viewer only retains bounded OSC payloads; large graphics strings are skipped.
type sequenceReader struct {
	state    byte
	code     byte
	buf      []byte
	escaped  bool
	paste    bool
	pasteEnd int
	overflow bool
}

func (s *sequenceReader) feed(p []byte, emit func([]byte, bool)) {
	for len(p) > 0 {
		b := p[0]
		p = p[1:]
		if s.paste {
			emit([]byte{b}, false)
			const end = "\x1b[201~"
			if b == end[s.pasteEnd] {
				s.pasteEnd++
			} else if b == end[0] {
				s.pasteEnd = 1
			} else {
				s.pasteEnd = 0
			}
			if s.pasteEnd == len(end) {
				s.paste = false
				s.pasteEnd = 0
			}
			continue
		}
		if s.state == 0 {
			if b == 0x1b {
				s.buf = []byte{b}
				s.state = 1
				continue
			}
			n := bytes.IndexByte(p, 0x1b)
			if n < 0 {
				n = len(p)
			}
			emit(append([]byte{b}, p[:n]...), false)
			p = p[n:]
			continue
		}
		if !s.overflow {
			s.buf = append(s.buf, b)
			if len(s.buf) > 1<<20 {
				s.buf = nil
				s.overflow = true
			}
		}
		switch s.state {
		case 1:
			switch b {
			case '[':
				s.state = 2
			case ']', 'P', '_', '^', 'X':
				s.code = b
				s.state = 3
			default:
				s.finish(emit, false)
			}
		case 2:
			if b >= 0x40 && b <= 0x7e || b < 0x20 {
				s.paste = bytes.Equal(s.buf, []byte("\x1b[200~"))
				s.finish(emit, false)
			}
		case 3:
			if b == 0x18 || b == 0x1a {
				s.finish(emit, false)
				continue
			}
			if b == 7 && s.code == ']' || s.escaped && b == '\\' {
				s.finish(emit, s.code == ']')
				continue
			}
			s.escaped = b == 0x1b
		}
	}
}
func (s *sequenceReader) finish(emit func([]byte, bool), osc bool) {
	if !s.overflow {
		emit(s.buf, osc)
	}
	s.buf = nil
	s.state = 0
	s.escaped = false
	s.overflow = false
}
func (s *sequenceReader) flush() []byte { b := s.buf; s.buf = nil; s.state = 0; return b }
