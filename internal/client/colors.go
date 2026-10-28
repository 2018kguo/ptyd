package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/2018kguo/ptyd/internal/terminal"
	"golang.org/x/term"
)

// ProbeColors asks the actual viewing terminal before starting a frontend.
// It has exclusive input ownership only during this bounded startup probe;
// unrelated keys and unfinished frames are returned for the normal reader.
func ProbeColors(ctx context.Context, in, out *os.File) (*terminal.TerminalColors, []byte) {
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) || os.Getenv("TERM") == "dumb" {
		return nil, nil
	}
	raw, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return nil, nil
	}
	defer term.Restore(int(in.Fd()), raw)
	input, err := NewInput(ctx, in, nil)
	if err != nil {
		return nil, nil
	}
	var scan sequenceReader
	var pending []byte
	values := map[string]string{}
	emit := func(p []byte, osc bool) {
		if osc {
			if slot, color, ok := parseColorReply(p); ok {
				values[slot] = color
				return
			}
		}
		pending = append(pending, p...)
	}
	finish := func() (*terminal.TerminalColors, []byte) {
		rest := input.Remainder()
		scan.feed(rest, emit)
		pending = append(pending, scan.flush()...)
		if values["10"] != "" && values["11"] != "" {
			return &terminal.TerminalColors{Foreground: values["10"], Background: values["11"]}, pending
		}
		return nil, pending
	}
	if _, err := io.WriteString(out, "\x1b]10;?\x1b\\\x1b]11;?\x1b\\"); err != nil {
		return finish()
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	count := 0
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
			count += len(p)
			scan.feed(p, emit)
			if len(values) == 2 || count >= 4096 {
				return finish()
			}
		}
	}
}
func parseColorReply(p []byte) (slot, color string, ok bool) {
	if !bytes.HasPrefix(p, []byte("\x1b]")) {
		return "", "", false
	}
	s := strings.TrimSuffix(strings.TrimSuffix(string(p[2:]), "\a"), "\x1b\\")
	slot, s, found := strings.Cut(s, ";")
	if !found || (slot != "10" && slot != "11") || !strings.HasPrefix(s, "rgb:") {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(s, "rgb:"), "/")
	if len(parts) != 3 {
		return "", "", false
	}
	rgb := [3]uint8{}
	for i, v := range parts {
		if len(v) < 1 || len(v) > 4 {
			return "", "", false
		}
		n, err := strconv.ParseUint(v, 16, 16)
		if err != nil {
			return "", "", false
		}
		maximum := uint64(1)<<(len(v)*4) - 1
		rgb[i] = uint8((n*255 + maximum/2) / maximum)
	}
	return slot, fmt.Sprintf("#%02x%02x%02x", rgb[0], rgb[1], rgb[2]), true
}
