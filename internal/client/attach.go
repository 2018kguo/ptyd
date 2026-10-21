// Package client implements `ptyd attach`, an interactive client for a
// terminal running in the daemon.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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
// scroll region and cursor shape.
const resetLocal = "\x1b[?2026l\x1b[0m\x1b[?1049l\x1b[?25h\x1b[?1l\x1b>" +
	"\x1b[?9l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1006l\x1b[?1015l" +
	"\x1b[?1004l\x1b[?2004l\x1b[<99u\x1b[r\x1b[0 q"

// Options configure Attach.
type Options struct {
	Addr     string // host:port of the daemon
	Token    string
	ID       string
	ReadOnly bool
}

// Attach connects in and out (the user's terminal) to a terminal in the
// daemon until the user detaches or the terminal's output ends. winch
// signals a local window size change. It returns a short message for the
// user.
func Attach(ctx context.Context, opts Options, in, out *os.File, winch <-chan os.Signal) (string, error) {
	cols, rows, err := term.GetSize(int(out.Fd()))
	if err != nil {
		cols, rows = 80, 24
	}
	conn, br, readOnly, err := dial(ctx, opts, cols, rows)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if term.IsTerminal(int(in.Fd())) {
		state, err := term.MakeRaw(int(in.Fd()))
		if err != nil {
			return "", fmt.Errorf("raw mode: %w", err)
		}
		defer func() {
			_, _ = io.WriteString(out, resetLocal)
			_ = term.Restore(int(in.Fd()), state)
		}()
	}

	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(out, br)
		ended <- err
	}()
	input := make(chan []byte)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			buf := make([]byte, 4096)
			n, err := in.Read(buf)
			if n > 0 {
				select {
				case input <- buf[:n]:
				case <-done:
					return
				}
			}
			if err != nil {
				close(input)
				return
			}
		}
	}()

	send := func(typ byte, payload []byte) error {
		_, err := conn.Write(api.AppendFrame(nil, typ, payload))
		return err
	}
	var held []byte // a first detach key press, not yet forwarded
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return "detached from " + opts.ID, nil
		case err := <-ended:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				return "", fmt.Errorf("connection: %w", err)
			}
			return opts.ID + " ended", nil
		case <-winch:
			if c, r, err := term.GetSize(int(out.Fd())); err == nil && !readOnly {
				_ = send(api.FrameResize, binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint16(nil, uint16(c)), uint16(r)))
			}
		case <-timer.C:
			if held != nil && !readOnly {
				_ = send(api.FrameInput, held)
			}
			held = nil
		case p, ok := <-input:
			if !ok {
				return "detached from " + opts.ID + " (input closed)", nil
			}
			switch {
			case isDetachKey(p) && held != nil, isDoubleDetachKey(p):
				return "detached from " + opts.ID, nil
			case isDetachKey(p):
				held = p
				timer.Reset(detachWindow)
				continue
			case held != nil && isDetachRelease(p):
				held = append(held, p...)
				continue
			}
			if held != nil {
				p = append(held, p...)
				held = nil
				timer.Stop()
			}
			if !readOnly {
				if err := send(api.FrameInput, p); err != nil {
					return "", fmt.Errorf("connection: %w", err)
				}
			}
		}
	}
}

// The detach key, Ctrl-\, as the legacy control byte, the kitty keyboard
// protocol (press, with or without event types) and xterm
// modifyOtherKeys encodings.
var detachKeys = [][]byte{[]byte("\x1c"), []byte("\x1b[92;5u"), []byte("\x1b[92;5:1u"), []byte("\x1b[27;5;92~")}

func isDetachKey(p []byte) bool {
	for _, k := range detachKeys {
		if bytes.Equal(p, k) {
			return true
		}
	}
	return false
}

func isDoubleDetachKey(p []byte) bool {
	for _, k := range detachKeys {
		if len(p) == 2*len(k) && bytes.Equal(p[:len(k)], k) && bytes.Equal(p[len(k):], k) {
			return true
		}
	}
	return false
}

func isDetachRelease(p []byte) bool { return bytes.Equal(p, []byte("\x1b[92;5:3u")) }

// dial performs the upgrade handshake.
func dial(ctx context.Context, opts Options, cols, rows int) (net.Conn, *bufio.Reader, bool, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", opts.Addr)
	if err != nil {
		return nil, nil, false, fmt.Errorf("connect to ptyd at %s: %w", opts.Addr, err)
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
		var e struct {
			Error string `json:"error"`
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return nil, nil, false, fmt.Errorf("attach %s: %s", opts.ID, e.Error)
		}
		return nil, nil, false, fmt.Errorf("attach %s: %s", opts.ID, resp.Status)
	}
	return conn, br, resp.Header.Get("X-Ptyd-Readonly") == "1", nil
}
