package terminal

import (
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/2018kguo/phosphor"
)

// TerminalColors is the viewing terminal's default foreground/background pair.
// The API uses #RRGGBB; absence keeps the emulator's default palette.
type TerminalColors struct {
	Foreground string `json:"foreground"`
	Background string `json:"background"`
}

// Palette validates the color pair and applies it to the default palette.
func (c *TerminalColors) Palette() (phosphor.Palette, error) {
	p := phosphor.DefaultPalette()
	if c == nil {
		return p, nil
	}
	parse := func(s string) (phosphor.RGB, error) {
		if len(s) != 7 || s[0] != '#' {
			return phosphor.RGB{}, fmt.Errorf("terminal colors must use #RRGGBB")
		}
		b, e := hex.DecodeString(s[1:])
		if e != nil {
			return phosphor.RGB{}, fmt.Errorf("terminal colors must use #RRGGBB")
		}
		return phosphor.RGB{R: b[0], G: b[1], B: b[2]}, nil
	}
	var err error
	p.Foreground, err = parse(c.Foreground)
	if err != nil {
		return p, err
	}
	p.Background, err = parse(c.Background)
	return p, err
}

// queryReplies is called under scrMu. A raw viewer supplies replies itself;
// otherwise the canonical emulator answers, including before the first viewer.
// The bounded lazy worker keeps a child that floods queries without reading
// stdin from blocking PTY output or growing an unbounded reply queue.
type queryReplies struct{ terminal *terminal }

func (w *queryReplies) Write(p []byte) (int, error) {
	t := w.terminal
	if t == nil || len(p) == 0 {
		return len(p), nil
	}
	for a := range t.attachers {
		if a.rawResponder && (a.human || t.humans.Load() == 0) {
			return len(p), nil
		}
	}
	if t.replies == nil {
		t.replies = &replyQueue{wake: make(chan struct{}, 1)}
		go t.sendReplies(t.replies)
	}
	q := t.replies
	q.mu.Lock()
	if len(q.buf)+len(p) <= maxAttachBuffer {
		q.buf = append(q.buf, p...)
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return len(p), nil
}

type replyQueue struct {
	mu   sync.Mutex
	buf  []byte
	wake chan struct{}
}

func (t *terminal) sendReplies(q *replyQueue) {
	for {
		select {
		case <-t.done:
			return
		case <-q.wake:
			q.mu.Lock()
			data := q.buf
			q.buf = nil
			q.mu.Unlock()
			if len(data) > 0 {
				if _, err := t.ptmx.Write(data); err != nil {
					return
				}
			}
		}
	}
}
