package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// The attach protocol. A client sends GET /terminals/{id}/attach with
// "Connection: Upgrade" and "Upgrade: ptyd-attach" (plus the token
// header), and optionally ?cols=&rows= (its window size) and ?readonly=1.
// The server answers 101 Switching Protocols (with "X-Ptyd-Readonly: 1"
// for a read-only attachment, such as a ghost) and the connection carries:
//
//   - server to client: raw terminal output, starting with a repaint of
//     the screen and its scrollback. EOF means the terminal's output
//     ended.
//   - client to server: frames of one type byte, a 4-byte big-endian
//     length and a payload. FrameInput carries keystrokes; FrameResize
//     carries cols and rows as two big-endian uint16s.
const (
	AttachProtocol = "ptyd-attach"
	FrameInput     = 'i'
	FrameResize    = 'r'
	maxFrame       = 1 << 20
)

func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), AttachProtocol) {
		writeError(w, http.StatusBadRequest, "attach requires Upgrade: %s", AttachProtocol)
		return
	}
	q := r.URL.Query()
	cols, _ := strconv.ParseUint(q.Get("cols"), 10, 16)
	rows, _ := strconv.ParseUint(q.Get("rows"), 10, 16)
	at, err := s.mgr.Attach(terminal.ID(r.PathValue("id")), uint16(cols), uint16(rows), q.Get("readonly") == "1")
	if err != nil {
		writeErr(w, err)
		return
	}
	defer at.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "connection cannot be upgraded")
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	ro := ""
	if at.ReadOnly() {
		ro = "X-Ptyd-Readonly: 1\r\n"
	}
	if _, err := fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n%s\r\n", AttachProtocol, ro); err != nil {
		return
	}

	// The request context ends on daemon shutdown; ctx also ends when
	// either direction fails, and closing the connection unblocks both.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	go func() {
		defer cancel()
		_ = readFrames(brw.Reader, at)
	}()
	for {
		b, err := at.Read(ctx)
		if err != nil {
			return
		}
		if _, err := conn.Write(b); err != nil {
			return
		}
	}
}

// readFrames applies the client's input and resize frames until the
// connection ends.
func readFrames(r *bufio.Reader, at *terminal.Attachment) error {
	var hdr [5]byte
	buf := make([]byte, 0, 4096)
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return err
		}
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > maxFrame {
			return errors.New("frame too large")
		}
		buf = buf[:0]
		if cap(buf) < int(n) {
			buf = make([]byte, n)
		}
		buf = buf[:n]
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		switch hdr[0] {
		case FrameInput:
			if err := at.Write(buf); err != nil && !errors.Is(err, terminal.ErrNotRunning) {
				return err
			}
		case FrameResize:
			if n == 4 {
				_ = at.Resize(binary.BigEndian.Uint16(buf), binary.BigEndian.Uint16(buf[2:]))
			}
		}
	}
}

// AppendFrame appends one client frame to b.
func AppendFrame(b []byte, typ byte, payload []byte) []byte {
	b = append(b, typ)
	b = binary.BigEndian.AppendUint32(b, uint32(len(payload)))
	return append(b, payload...)
}
