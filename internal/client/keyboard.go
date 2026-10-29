package client

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"time"

	"golang.org/x/term"
)

// ProbeKeyboard verifies the viewing terminal rather than inferring support
// from modes the child asked Phosphor to enable. Unrelated input is preserved.
func ProbeKeyboard(ctx context.Context, in, out *os.File) (bool, []byte) {
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return false, nil
	}
	raw, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return false, nil
	}
	defer term.Restore(int(in.Fd()), raw)
	input, err := NewInput(ctx, in, nil)
	if err != nil {
		return false, nil
	}
	var scan sequenceReader
	var pending []byte
	supported := false
	emit := func(p []byte, osc bool) {
		if !osc && keyboardReply(p) {
			supported = true
			return
		}
		pending = append(pending, p...)
	}
	finish := func() (bool, []byte) {
		scan.feed(input.Remainder(), emit)
		return supported, append(pending, scan.flush()...)
	}
	if _, err := io.WriteString(out, "\x1b[?u"); err != nil {
		return finish()
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return finish()
		case <-timer.C:
			return finish()
		case p, ok := <-input.Bytes:
			if !ok {
				return finish()
			}
			scan.feed(p, emit)
			if supported || len(pending) >= 4096 {
				return finish()
			}
		}
	}
}

func keyboardReply(p []byte) bool {
	if len(p) < 5 || len(p) > 12 || !bytes.HasPrefix(p, []byte("\x1b[?")) || p[len(p)-1] != 'u' {
		return false
	}
	for _, b := range p[3 : len(p)-1] {
		if b < '0' || b > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(string(p[3:len(p)-1]), 10, 16)
	return err == nil
}
