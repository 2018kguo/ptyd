// Package client implements `ptyd attach`, an interactive client for a
// terminal running in the daemon.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/2018kguo/ptyd/internal/api"
)

// DetachKeyHelp describes the detach key for help text.
const DetachKeyHelp = "Ctrl-\\ twice"

// detachWindow is how long after a first Ctrl-\ a second one detaches.
// After it, the held Ctrl-\ is passed through to the program.
const detachWindow = time.Second

// resetLocal undoes modes a program may have switched on in the local
// terminal through the passthrough stream: synchronized output, SGR, the
// alternate screen, hidden cursor, application cursor keys and keypad,
// mouse reporting, focus events, bracketed paste, kitty keyboard flags,
// scroll region, autowrap, insert mode, charsets and cursor shape. The
// cursor stays where the program left it on the main screen: leaving the
// alternate screen restores the cursor saved for the main screen, so a
// save (DECSC) goes first in case the alternate screen was not on, and
// resetting the scroll region homes the cursor, so it is wrapped in a
// save and restore.
const resetLocal = "\x1b[?2026l\x1b[?2027l\x1b[0m\x1b7\x1b[?1049l\x1b[?25h\x1b[?1l\x1b>" +
	"\x1b[?9l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1006l\x1b[?1015l" +
	"\x1b[?1004l\x1b[?2004l\x1b[<99u\x1b7\x1b[r\x1b8\x1b[?7h\x1b[4l\x1b(B\x0f\x1b[0 q"

// scrollOff returns bytes that move the cursor's row and everything above
// it into the local terminal's scrollback and leave a blank window with
// the cursor at the top-left: a newline, erase below, and enough newlines
// to scroll the rest away. Unlike erasing the display, this keeps the
// window in the scrollback in every terminal.
func scrollOff(rows int) string {
	return "\r\n\x1b[J" + strings.Repeat("\n", max(rows-1, 0)) + "\x1b[H"
}

// Options configure Attach.
type Options struct {
	Addr     string // host:port of the daemon
	Token    string
	ID       string
	ReadOnly bool
	// InitialInput precedes bytes read from the local terminal.
	InitialInput []byte
}

// AttachResult describes the handoff back to the caller. PendingInput contains
// bytes after an explicit detach gesture and any later bytes the reader had not
// delivered. Failures and ordinary program exit never return queued input.
type AttachResult struct {
	Detached     bool
	PendingInput []byte
}

// Attach connects in and out (the user's terminal) to a terminal in the
// daemon until the user detaches (Detached is true) or the attachment
// ends: the terminal's output ended, or the connection to the daemon
// broke. winch signals a local window size change.
//
// The local window is handed over cleanly in both directions: on attach
// what it showed scrolls into its scrollback; on detach the terminal's
// window does too and the user gets a blank window; when the attachment
// ends, the last window stays in view with the cursor on a clean line
// below it. Either way the modes the program switched on are reset.
func Attach(ctx context.Context, opts Options, in, out *os.File, winch <-chan os.Signal) (result AttachResult, err error) {
	cols, rows, err := term.GetSize(int(out.Fd()))
	isTerm := err == nil
	if !isTerm {
		cols, rows = 80, 24
	}
	conn, br, readOnly, err := dial(ctx, opts, cols, rows)
	if err != nil {
		return result, err
	}
	defer conn.Close()

	if term.IsTerminal(int(in.Fd())) {
		state, err := term.MakeRaw(int(in.Fd()))
		if err != nil {
			return result, fmt.Errorf("raw mode: %w", err)
		}
		defer func() { _ = term.Restore(int(in.Fd()), state) }()
	}
	if isTerm {
		// The prompt the user attached from is at the cursor.
		_, _ = io.WriteString(out, "\x1b[J"+strings.Repeat("\n", max(rows-1, 0)))
	}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, br)
		close(copied)
	}()
	defer func() {
		// Stop the output before writing to the local terminal.
		conn.Close()
		<-copied
		if !isTerm {
			return
		}
		end := "\r\n\x1b[J"
		if result.Detached {
			_, rows, err := term.GetSize(int(out.Fd()))
			if err != nil {
				rows = 24
			}
			end = scrollOff(rows)
		}
		// CAN cancels a sequence the output may have been cut off in.
		_, _ = io.WriteString(out, "\x18"+resetLocal+end)
	}()
	input, err := NewInput(ctx, in, opts.InitialInput)
	if err != nil {
		return result, err
	}
	defer input.Close()

	send := func(typ byte, payload []byte) error {
		_, err := conn.Write(api.AppendFrame(nil, typ, payload))
		return err
	}
	var held []byte // a first detach key press, not yet forwarded
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			result.Detached = true
			return result, nil
		case <-copied:
			return result, nil
		case <-winch:
			if c, r, err := term.GetSize(int(out.Fd())); err == nil && !readOnly {
				_ = send(api.FrameResize, binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint16(nil, uint16(c)), uint16(r)))
			}
		case <-timer.C:
			if held != nil && !readOnly {
				_ = send(api.FrameInput, held)
			}
			held = nil
		case p, ok := <-input.Bytes:
			if !ok {
				result.Detached = true
				return result, nil
			}
			for len(p) > 0 {
				index, n, release := nextDetachEvent(p)
				if index > 0 {
					prefix := p[:index]
					if held != nil {
						prefix = append(held, prefix...)
						held = nil
						timer.Stop()
					}
					if !readOnly {
						if err := send(api.FrameInput, prefix); err != nil {
							return result, nil // the connection broke
						}
					}
				}
				p = p[index:]
				if len(p) == 0 {
					break
				}
				key := p[:n]
				p = p[n:]
				if release {
					if held != nil {
						held = append(held, key...)
					} else if !readOnly {
						if err := send(api.FrameInput, key); err != nil {
							return result, nil
						}
					}
					continue
				}
				if held != nil {
					held = nil // both presses and their release events are consumed
					result.Detached = true
					result.PendingInput = append(bytes.Clone(p), input.Remainder()...)
					return result, nil
				}
				held = bytes.Clone(key)
				timer.Reset(detachWindow)
			}
		}
	}
}

// The detach key, Ctrl-\, as the legacy control byte, the kitty keyboard
// protocol (press, with or without event types) and xterm
// modifyOtherKeys encodings.
var detachKeys = [][]byte{[]byte("\x1c"), []byte("\x1b[92;5u"), []byte("\x1b[92;5:1u"), []byte("\x1b[27;5;92~")}

// nextDetachEvent recognizes complete detach tokens within a read. Terminal
// reads may coalesce a command, both presses, and the caller's next key.
func nextDetachEvent(p []byte) (index, size int, release bool) {
	index = len(p)
	for _, key := range detachKeys {
		if i := bytes.Index(p, key); i >= 0 && i < index {
			index, size = i, len(key)
		}
	}
	keyup := []byte("\x1b[92;5:3u")
	if i := bytes.Index(p, keyup); i >= 0 && i < index {
		index, size, release = i, len(keyup), true
	}
	return
}

// dial performs the upgrade handshake.
func dial(ctx context.Context, opts Options, cols, rows int) (net.Conn, *bufio.Reader, bool, error) {
	var d net.Dialer
	c := API{Addr: opts.Addr, Token: opts.Token}
	conn, err := d.DialContext(ctx, "tcp", opts.Addr)
	if err != nil {
		return nil, nil, false, c.connErr(err)
	}
	q := url.Values{}
	q.Set("cols", fmt.Sprint(cols))
	q.Set("rows", fmt.Sprint(rows))
	if opts.ReadOnly {
		q.Set("readonly", "1")
	}
	req, _ := http.NewRequest("GET", "http://"+opts.Addr+"/terminals/"+url.PathEscape(opts.ID)+"/attach?"+q.Encode(), nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", api.AttachProtocol)
	if opts.Token != "" {
		req.Header.Set("X-Ptyd-Token", opts.Token)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, false, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, false, fmt.Errorf("attach handshake: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer conn.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, false, c.statusErr(resp.StatusCode, body)
	}
	return conn, br, resp.Header.Get("X-Ptyd-Readonly") == "1", nil
}
