package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// TokenFile is where the daemon listening on addr keeps its token:
// $XDG_STATE_HOME/ptyd/token-<port>, by default under ~/.local/state.
func TokenFile(addr string) (string, error) { return statePath(addr, "token-") }

// PersistDir is the default --persist-dir of the daemon listening on
// addr: $XDG_STATE_HOME/ptyd/terminals-<port>.
func PersistDir(addr string) (string, error) { return statePath(addr, "terminals-") }

func statePath(addr, prefix string) (string, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("address %q: %w", addr, err)
	}
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "ptyd", prefix+port), nil
}

// NewToken returns a random token.
func NewToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteToken saves the token of the daemon listening on addr for the CLI,
// readable only by this user (0600 in a 0700 directory). It returns the
// file's path.
func WriteToken(addr, token string) (string, error) {
	path, err := TokenFile(addr)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(token + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return path, err
}

// Token returns the token to send to the daemon at addr: $PTYD_TOKEN if
// set, else the one the daemon saved in TokenFile.
func Token(addr string) string {
	if t := os.Getenv("PTYD_TOKEN"); t != "" {
		return t
	}
	path, err := TokenFile(addr)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// API is a client for the daemon's HTTP API, for the command line.
type API struct {
	Addr  string
	Token string
}

// HTTPError retains the response status for operations that can explain how
// to recover from an unsupported endpoint.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// Do sends a request and decodes a JSON reply into out (if not nil). Its
// errors are meant for a person: a daemon that is not running, a token
// that was refused, or the daemon's own error message.
func (c API) Do(method, path string, body, out any) error {
	return c.DoContext(context.Background(), method, path, body, out)
}

// DoContext lets live clients cancel an in-flight refresh when they close.
func (c API) DoContext(ctx context.Context, method, path string, body, out any) error {
	raw, err := c.readContext(ctx, method, path, body, 64<<20)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func (c API) readContext(ctx context.Context, method, path string, body any, limit int64) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.Addr+path, r)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("X-Ptyd-Token", c.Token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, c.connErr(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, c.statusErr(resp.StatusCode, raw)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response exceeds the %d-byte read budget", limit)
	}
	return raw, nil
}

// ErrDaemonDown means nothing answers at the daemon's address.
var ErrDaemonDown = errors.New("ptyd is not running")

func (c API) connErr(err error) error {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		return fmt.Errorf("ptyd at %s: %w", c.Addr, err)
	}
	return fmt.Errorf("%w at %s (start it with `ptyd serve`, or set PTYD_ADDR)", ErrDaemonDown, c.Addr)
}

func (c API) statusErr(status int, body []byte) error {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || e.Error == "" {
		e.Error = http.StatusText(status)
	}
	if status == http.StatusUnauthorized {
		where := "$PTYD_TOKEN"
		if os.Getenv("PTYD_TOKEN") == "" {
			where, _ = TokenFile(c.Addr)
			if c.Token == "" {
				return fmt.Errorf("ptyd at %s needs a token: set PTYD_TOKEN to the one it was started with (none found in %s)", c.Addr, where)
			}
		}
		return fmt.Errorf("ptyd at %s refused the token from %s: set PTYD_TOKEN to the one it was started with", c.Addr, where)
	}
	return &HTTPError{Status: status, Message: e.Error}
}

// List returns all terminals, oldest first.
func (c API) List() ([]terminal.Info, error) {
	var list []terminal.Info
	err := c.Do("GET", "/terminals", nil, &list)
	return list, err
}

// Find returns the terminal target names: its id, its number (3 for
// term-3-ab12), a unique name, or a unique id prefix.
func (c API) Find(target string) (terminal.Info, error) {
	list, err := c.List()
	if err != nil {
		return terminal.Info{}, err
	}
	return Find(list, target)
}

// Find picks the terminal target names from list (see API.Find).
func Find(list []terminal.Info, target string) (terminal.Info, error) {
	match := func(f func(terminal.Info) bool) []terminal.Info {
		var out []terminal.Info
		for _, t := range list {
			if f(t) {
				out = append(out, t)
			}
		}
		return out
	}
	for _, f := range []func(terminal.Info) bool{
		func(t terminal.Info) bool { return string(t.ID) == target },
		func(t terminal.Info) bool { return t.Name == target },
		func(t terminal.Info) bool { return strings.HasPrefix(string(t.ID), "term-"+target+"-") },
		func(t terminal.Info) bool { return strings.HasPrefix(string(t.ID), target) },
	} {
		switch m := match(f); len(m) {
		case 0:
		case 1:
			return m[0], nil
		default:
			ids := make([]string, len(m))
			for i, t := range m {
				ids[i] = string(t.ID)
			}
			return terminal.Info{}, fmt.Errorf("%q matches %d terminals (%s); use the id", target, len(m), strings.Join(ids, ", "))
		}
	}
	return terminal.Info{}, fmt.Errorf("no terminal %q (see `ptyd term ls`)", target)
}
