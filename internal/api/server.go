package api

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// maxBodyBytes caps request bodies.
const maxBodyBytes = 1 << 20

// Config holds server-level options.
type Config struct {
	Addr  string // host:port to listen on
	Token string // if non-empty, required as X-Ptyd-Token on every request
}

// Server is the HTTP API for ptyd.
type Server struct {
	mgr    *terminal.Manager
	token  string
	httpd  *http.Server
	cancel context.CancelFunc // ends long-lived requests (streams, waits) on Shutdown
}

// New constructs (but does not start) a Server.
func New(mgr *terminal.Manager, cfg Config) *Server {
	base, cancel := context.WithCancel(context.Background())
	s := &Server{mgr: mgr, token: cfg.Token, cancel: cancel}
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.httpd = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	return s
}

// Handler exposes the wrapped http.Handler (with middleware applied)
// so tests can drive routes via httptest without binding a real port.
func (s *Server) Handler() http.Handler {
	return s.httpd.Handler
}

// Listen binds the configured address. Separating this from Serve lets the
// caller surface bind errors synchronously before logging "listening on …".
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.httpd.Addr)
}

// Serve takes over an already-bound listener. Returns nil on graceful
// shutdown, otherwise the underlying server error.
func (s *Server) Serve(ln net.Listener) error {
	err := s.httpd.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown stops accepting connections, ends open streams and waits, and
// waits for in-flight requests to finish (or until ctx expires).
func (s *Server) Shutdown(ctx context.Context) error {
	s.cancel()
	return s.httpd.Shutdown(ctx)
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /terminals", s.handleList)
	mux.HandleFunc("POST /terminals", s.handleCreate)
	mux.HandleFunc("GET /terminals/{id}", s.handleGet)
	mux.HandleFunc("GET /terminals/{id}/output", s.handleOutput)
	mux.HandleFunc("GET /terminals/{id}/stream", s.handleStream)
	mux.HandleFunc("GET /terminals/{id}/screen", s.handleScreen)
	mux.HandleFunc("GET /terminals/{id}/attach", s.handleAttach)
	mux.HandleFunc("POST /terminals/{id}/write", s.handleWrite)
	mux.HandleFunc("POST /terminals/{id}/keys", s.handleKeys)
	mux.HandleFunc("POST /terminals/{id}/wait", s.handleWait)
	mux.HandleFunc("POST /terminals/{id}/ack", s.handleAck)
	mux.HandleFunc("POST /terminals/{id}/resize", s.handleResize)
	mux.HandleFunc("POST /terminals/{id}/signal", s.handleSignal)
	mux.HandleFunc("POST /terminals/{id}/restart", s.handleRestart)
	mux.HandleFunc("POST /terminals/{id}/restore", s.handleRestore)
	mux.HandleFunc("DELETE /terminals/{id}", s.handleDelete)
}

// authExempt reports whether path is served without the token: the
// /healthz liveness probe and /metrics, so local monitoring can scrape.
func authExempt(path string) bool {
	return path == "/healthz" || path == "/metrics"
}

func (s *Server) middleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" && !authExempt(r.URL.Path) {
			tok := r.Header.Get("X-Ptyd-Token")
			if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
				writeError(w, http.StatusUnauthorized, "missing or invalid token")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		h.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleMetrics returns operational stats for the daemon. Auth-exempt
// like /healthz.
func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.mgr.Metrics())
}

// handleList returns all terminals, oldest first. ?tag=a,b keeps only
// terminals carrying at least one of the tags.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	terms := s.mgr.List()
	if tags := splitNonEmpty(r.URL.Query().Get("tag"), ","); len(tags) > 0 {
		out := terms[:0]
		for _, t := range terms {
			if anyTagMatch(t.Tags, tags) {
				out = append(out, t)
			}
		}
		terms = out
	}
	writeJSON(w, http.StatusOK, terms)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var cfg terminal.CreateConfig
	if !decodeJSON(w, r, &cfg) {
		return
	}
	info, err := s.mgr.Create(cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	info, err := s.mgr.Get(terminal.ID(r.PathValue("id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleOutput(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	since := parseSince(r.URL.Query().Get("since"))
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	out, err := s.mgr.Output(id, since, tail)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported by ResponseWriter")
		return
	}

	subID, ch, snap, err := s.mgr.Subscribe(id, 256)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer s.mgr.Unsubscribe(id, subID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	since := parseSince(r.URL.Query().Get("since"))
	sse := sseWriter{w: w}
	for _, line := range snap.Lines {
		if line.Seq <= since {
			continue
		}
		sse.send("line", line)
	}
	flusher.Flush()

	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()

	end := func() {
		sse.send("end", map[string]string{"reason": "terminal closed"})
		flusher.Flush()
	}
	for {
		select {
		case line, ok := <-ch:
			// Send the lines already queued behind this one, then flush
			// once: a flush is a write to the connection, and one per line
			// was most of the cost of a busy stream.
		batch:
			for n := 0; ok; n++ {
				sse.send("line", line)
				if n == cap(ch) {
					break
				}
				select {
				case line, ok = <-ch:
				default:
					break batch
				}
			}
			if !ok {
				end()
				return
			}
			flusher.Flush()
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// handleScreen returns the current screen: JSON text by default, or with
// ?format=ansi the bytes that repaint it on a terminal of the same size.
// ?scrollback=1 includes the scrollback above the screen.
func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	withScrollback := r.URL.Query().Get("scrollback") == "1"
	switch r.URL.Query().Get("format") {
	case "", "text":
		scr, err := s.mgr.Screen(id, withScrollback)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, scr)
	case "ansi":
		b, err := s.mgr.ScreenANSI(id, withScrollback)
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(b)
	default:
		writeError(w, http.StatusBadRequest, "format must be text or ansi")
	}
}

type writeRequest struct {
	Data   string `json:"data"`
	Submit bool   `json:"submit,omitempty"` // append \r to send the line
}

func (s *Server) handleWrite(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	var req writeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	data := req.Data
	if req.Submit {
		data += "\r"
	}
	if err := s.mgr.Write(id, data); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type keysRequest struct {
	Keys []string `json:"keys"`
}

// keyMap translates symbolic key names to byte sequences. Names are
// case-insensitive and "ctrl+x" / "c-x" / "^x" are all accepted.
var keyMap = map[string]string{
	"enter": "\r", "return": "\r",
	"tab": "\t",
	"esc": "\x1b", "escape": "\x1b",
	"space":     " ",
	"backspace": "\x7f",
	"up":        "\x1b[A",
	"down":      "\x1b[B",
	"right":     "\x1b[C",
	"left":      "\x1b[D",
	"home":      "\x1b[H",
	"end":       "\x1b[F",
	"pgup":      "\x1b[5~",
	"pgdn":      "\x1b[6~",
	"delete":    "\x1b[3~",
}

func keyToBytes(name string) (string, error) {
	if utf8.RuneCountInString(name) == 1 {
		return name, nil // a literal character, case preserved
	}
	n := strings.ToLower(strings.TrimSpace(name))
	if v, ok := keyMap[n]; ok {
		return v, nil
	}
	// ctrl modifier — accepts "ctrl+c", "c-c", "^c"
	switch {
	case strings.HasPrefix(n, "ctrl+"):
		n = n[len("ctrl+"):]
	case strings.HasPrefix(n, "c-"):
		n = n[len("c-"):]
	case strings.HasPrefix(n, "^"):
		n = n[1:]
	default:
		return "", fmt.Errorf("unknown key %q", name)
	}
	if len(n) != 1 {
		return "", fmt.Errorf("ctrl modifier expects single char, got %q", name)
	}
	c := n[0]
	switch {
	case c >= 'a' && c <= 'z':
		return string([]byte{c - 'a' + 1}), nil
	case c == '@':
		return "\x00", nil
	case c == '[':
		return "\x1b", nil
	case c == '\\':
		return "\x1c", nil
	case c == ']':
		return "\x1d", nil
	case c == '^':
		return "\x1e", nil
	case c == '_':
		return "\x1f", nil
	}
	return "", fmt.Errorf("unsupported ctrl key %q", name)
}

func (s *Server) handleKeys(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	var req keysRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Keys) == 0 {
		writeError(w, http.StatusBadRequest, "keys required")
		return
	}
	var sb strings.Builder
	for _, k := range req.Keys {
		bytes, err := keyToBytes(k)
		if err != nil {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
		sb.WriteString(bytes)
	}
	if err := s.mgr.Write(id, sb.String()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type waitRequest struct {
	Pattern        string  `json:"pattern"`
	TimeoutSeconds float64 `json:"timeout_seconds"`
	Since          *int64  `json:"since"` // omitted: all retained output
	BeforeContext  int     `json:"before_context"`
}

type waitResponse struct {
	Matched bool            `json:"matched"`
	Match   *terminal.Line  `json:"match,omitempty"`
	Context []terminal.Line `json:"context,omitempty"`
	Reason  string          `json:"reason,omitempty"`
}

func (s *Server) handleWait(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	var req waitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Pattern == "" {
		writeError(w, http.StatusBadRequest, "pattern required")
		return
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 30
	}
	timeout := time.Duration(req.TimeoutSeconds * float64(time.Second))
	since := int64(-1)
	if req.Since != nil {
		since = *req.Since
	}

	// Use the request context so client disconnects also cancel the wait.
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	line, before, err := s.mgr.Wait(ctx, id, req.Pattern, since, req.BeforeContext)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			writeJSON(w, http.StatusOK, waitResponse{Matched: false, Reason: "timeout"})
		case errors.Is(err, context.Canceled):
			writeJSON(w, http.StatusOK, waitResponse{Matched: false, Reason: "canceled"})
		case errors.Is(err, terminal.ErrNotRunning):
			writeJSON(w, http.StatusOK, waitResponse{Matched: false, Reason: "exited"})
		default:
			writeErr(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, waitResponse{Matched: true, Match: &line, Context: before})
}

type ackRequest struct {
	Watch string `json:"watch"`
}

func (s *Server) handleAck(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	var req ackRequest // body optional: empty acks every watch
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.mgr.Ack(id, req.Watch); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type resizeRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s *Server) handleResize(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	var req resizeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.mgr.Resize(id, req.Cols, req.Rows); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type signalRequest struct {
	Signal string `json:"signal"`
}

var signalMap = map[string]syscall.Signal{
	"int":   syscall.SIGINT,
	"term":  syscall.SIGTERM,
	"kill":  syscall.SIGKILL,
	"hup":   syscall.SIGHUP,
	"quit":  syscall.SIGQUIT,
	"usr1":  syscall.SIGUSR1,
	"usr2":  syscall.SIGUSR2,
	"winch": syscall.SIGWINCH,
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	id := terminal.ID(r.PathValue("id"))
	var req signalRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	sig, ok := signalMap[strings.ToLower(req.Signal)]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid signal %q (valid: %s)", req.Signal, knownSignals())
		return
	}
	if err := s.mgr.Signal(id, sig); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	info, err := s.mgr.Restart(terminal.ID(r.PathValue("id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

type restoreRequest struct {
	Policy terminal.RestorePolicy `json:"policy"`
}

// handleRestore restores a ghost under the same id with its policy (or
// the one in the body). A declined restore is 409 with the reason; the
// terminal stays a ghost.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	info, err := s.mgr.Restore(terminal.ID(r.PathValue("id")), req.Policy)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Close(terminal.ID(r.PathValue("id"))); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "closed"})
}

func anyTagMatch(tags, filter []string) bool {
	for _, f := range filter {
		for _, t := range tags {
			if t == f {
				return true
			}
		}
	}
	return false
}

func splitNonEmpty(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q (regex) required")
		return
	}
	withinSecs, _ := strconv.ParseFloat(r.URL.Query().Get("within_seconds"), 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tags := splitNonEmpty(r.URL.Query().Get("tag"), ",")
	hits, err := s.mgr.Search(q, time.Duration(withinSecs*float64(time.Second)), limit, tags)
	if err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": hits, "count": len(hits)})
}

// parseSince returns sinceSeq from a query parameter. Empty -> -1 (return
// everything), since Seq starts at 0.
func parseSince(s string) int64 {
	if s == "" {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

func knownSignals() string {
	keys := make([]string, 0, len(signalMap))
	for k := range signalMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// sseWriter writes server-sent events, reusing one encoding buffer.
type sseWriter struct {
	w   io.Writer
	buf bytes.Buffer
	enc *json.Encoder
}

func (s *sseWriter) send(event string, data any) {
	if s.enc == nil {
		s.enc = json.NewEncoder(&s.buf)
	}
	s.buf.Reset()
	s.buf.WriteString("event: ")
	s.buf.WriteString(event)
	s.buf.WriteString("\ndata: ")
	if err := s.enc.Encode(data); err != nil { // the JSON and a newline
		log.Printf("sse marshal: %v", err)
		return
	}
	s.buf.WriteByte('\n')
	_, _ = s.w.Write(s.buf.Bytes())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// writeErr maps a Manager error to a status: unknown id 404, ghost or
// exited terminal 409, anything else (validation, bad regex) 400.
func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, terminal.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, terminal.ErrGhostTerminal), errors.Is(err, terminal.ErrNotRunning),
		errors.Is(err, terminal.ErrNotGhost), errors.Is(err, terminal.ErrRestoreDeclined):
		status = http.StatusConflict
	}
	writeError(w, status, "%v", err)
}

// decodeJSON decodes the request body into v. An empty body leaves v
// zero. On failure it writes 413 or 400 and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	err := json.NewDecoder(r.Body).Decode(v)
	if err == nil || errors.Is(err, io.EOF) {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds %d bytes", maxBodyBytes)
	} else {
		writeError(w, http.StatusBadRequest, "invalid request body: %v", err)
	}
	return false
}
