// Package client implements `ptyd attach`, an interactive client for a
// terminal running in the daemon.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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
	Addr         string // host:port of the daemon
	Token        string
	ID           string
	ReadOnly     bool
	Label        string
	HoldReadOnly bool
	Raw          bool // pass bytes directly; no reserved status row
	rendered     bool
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
	framed := isTerm && !opts.Raw && opts.Label != "" && rows >= 2
	opts.rendered = framed
	appRows := rows
	if framed {
		appRows--
	}
	conn, br, display, err := dial(ctx, opts, cols, appRows)
	readOnly := display.readOnly
	if err != nil {
		return result, err
	}
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopClose()

	interactiveInput := term.IsTerminal(int(in.Fd()))
	if interactiveInput {
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
	var output io.Writer = out
	var frame *pane
	var frameTicks <-chan time.Time
	var titles *titleWriter
	if isTerm && opts.Label != "" {
		_, _ = io.WriteString(out, titlePush)
		setWindowTitle(out, opts.Label)
		if framed {
			frame = newPane(out, opts.Label, cols, rows)

			output = frame
			tick := time.NewTicker(time.Second / 60)
			defer tick.Stop()
			frameTicks = tick.C
		} else {
			titles = &titleWriter{out: out}
			output = titles
		}
	}
	var copied chan struct{}
	var copyErr error
	defer func() {
		// Stop the output before writing to the local terminal.
		conn.Close()
		if copied != nil {
			<-copied
		}
		if !isTerm {
			return
		}
		if frame != nil {
			frame.mu.Lock()
			frame.screen.Write([]byte("\x18" + resetLocal))
			frame.changed = true
			frame.mu.Unlock()
			_ = frame.draw(true)
			frame.mu.Lock()
			cur := frame.screen.Cursor()
			_, _ = fmt.Fprintf(out, "\x1b[%d;1H\x1b[0m\x1b[K\x1b[%d;%dH", frame.rows, min(cur.Y+1, frame.rows-1), min(cur.X+1, frame.cols))
			frame.mu.Unlock()
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
		if titles != nil || frame != nil {
			_, _ = io.WriteString(out, titlePop)
		}
	}()
	copied = make(chan struct{})
	go func() {
		if display.rendered {
			copyErr = copyPane(br, frame)
		} else {
			_, _ = io.Copy(output, br)
		}
		if titles != nil {
			titles.flush()
		}
		close(copied)
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
	var controls detachFilter
	if framed {
		controls.mouseRows = appRows
		controls.filterColorReplies = true
	}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	sequenceTimer := time.NewTimer(time.Hour)
	sequenceTimer.Stop()
	defer sequenceTimer.Stop()
	sendInput := func(p []byte) error {
		if len(p) == 0 || readOnly {
			return nil
		}
		return send(api.FrameInput, p)
	}
	outputDone := (<-chan struct{})(copied)
	for {
		select {
		case <-ctx.Done():
			result.Detached = true
			return result, nil
		case <-outputDone:
			if copyErr != nil && !errors.Is(copyErr, io.EOF) {
				return result, copyErr
			}
			if opts.HoldReadOnly && readOnly {
				outputDone = nil
				continue
			}
			return result, nil
		case <-frameTicks:
			if err := frame.draw(false); err != nil {
				return result, err
			}
		case <-winch:
			if c, r, e := term.GetSize(int(out.Fd())); e == nil {
				if frame != nil {
					frame.resize(c, r)
					r = max(r-1, 1)
					controls.mouseRows = r
				}
				if !readOnly {
					_ = send(api.FrameResize, binary.BigEndian.AppendUint16(binary.BigEndian.AppendUint16(nil, uint16(c)), uint16(r)))
				}
			}
		case <-timer.C:
			if err := sendInput(controls.expire(time.Now())); err != nil {
				return result, nil
			}
		case <-sequenceTimer.C:
			if err := sendInput(controls.flushSequence()); err != nil {
				return result, nil
			}
		case p, ok := <-input.Bytes:
			if !ok {
				_ = sendInput(controls.flushSequence())
				result.Detached = true
				return result, nil
			}
			if !interactiveInput {
				if err := sendInput(p); err != nil {
					return result, nil
				}
				continue
			}
			forward, detached, tail := controls.feed(p, time.Now())
			if err := sendInput(forward); err != nil {
				return result, nil
			}
			if detached {
				result.Detached = true
				result.PendingInput = append(bytes.Clone(tail), input.Remainder()...)
				return result, nil
			}
			timer.Stop()
			if !controls.deadline.IsZero() {
				timer.Reset(time.Until(controls.deadline))
			}
			sequenceTimer.Stop()
			if len(controls.pending) > 0 {
				sequenceTimer.Reset(sequenceWindow)
			}

		}
	}
}

type attachDisplay struct {
	readOnly bool
	rendered bool
}

// dial performs the upgrade handshake.
func dial(ctx context.Context, opts Options, cols, rows int) (net.Conn, *bufio.Reader, attachDisplay, error) {
	var d net.Dialer
	c := API{Addr: opts.Addr, Token: opts.Token}
	conn, err := d.DialContext(ctx, "tcp", opts.Addr)
	if err != nil {
		return nil, nil, attachDisplay{}, c.connErr(err)
	}
	q := url.Values{}
	q.Set("cols", fmt.Sprint(cols))
	q.Set("rows", fmt.Sprint(rows))
	if opts.ReadOnly {
		q.Set("readonly", "1")
	}
	if opts.rendered {
		q.Set("rendered", "1")
	}
	req, _ := http.NewRequest("GET", "http://"+opts.Addr+"/terminals/"+url.PathEscape(opts.ID)+"/attach?"+q.Encode(), nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", api.AttachProtocol)
	if opts.Token != "" {
		req.Header.Set("X-Ptyd-Token", opts.Token)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, nil, attachDisplay{}, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, nil, attachDisplay{}, fmt.Errorf("attach handshake: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer conn.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, nil, attachDisplay{}, c.statusErr(resp.StatusCode, body)
	}
	display := attachDisplay{readOnly: resp.Header.Get("X-Ptyd-Readonly") == "1"}
	if opts.rendered {
		if resp.Header.Get("X-Ptyd-Rendered") != "1" {
			conn.Close()
			return nil, nil, attachDisplay{}, fmt.Errorf("daemon cannot provide a framed screen; restart the updated daemon or use ptyd attach --raw")
		}
		display.rendered = true
	}
	return conn, br, display, nil
}

func copyPane(r io.Reader, p *pane) error {
	var header [5]byte
	initial := true
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(header[1:])
		if n > 64<<20 {
			return fmt.Errorf("attached frame exceeds64MiB")
		}
		data := make([]byte, int(n))
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		switch header[0] {
		case api.FrameSnapshot:
			if err := p.restore(data); err != nil {
				return err
			}
			initial = false
		case api.FrameOutput:
			if initial {
				return fmt.Errorf("attached stream omitted initial snapshot")
			}
			if _, err := p.Write(data); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown attached frame %q", header[0])
		}
	}
}
