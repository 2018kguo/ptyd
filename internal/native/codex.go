package native

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type codexRPC struct {
	conn    *websocket.Conn
	seq     int
	peerPID int
}

func dialCodex(ctx context.Context, endpoint string) (*codexRPC, error) {
	peer := 0
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
		if err != nil {
			return nil, err
		}
		peer, err = peerPID(conn)
		if err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}}
	conn, response, err := websocket.Dial(ctx, "ws://localhost/rpc", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("connect to the running Codex daemon: %w", err)
	}
	conn.SetReadLimit(maxOutput)
	rpc := &codexRPC{conn: conn, peerPID: peer}
	var init json.RawMessage
	if err = rpc.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "ptyd", "version": "dev"}, "capabilities": map[string]bool{"experimentalApi": false}}, &init); err != nil {
		conn.CloseNow()
		return nil, err
	}
	if err = rpc.send(ctx, map[string]string{"method": "initialized"}); err != nil {
		conn.CloseNow()
		return nil, err
	}
	return rpc, nil
}
func (r *codexRPC) send(ctx context.Context, message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return r.conn.Write(ctx, websocket.MessageText, raw)
}
func (r *codexRPC) call(ctx context.Context, method string, params, out any) error {
	r.seq++
	id := r.seq
	if err := r.send(ctx, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for range 10000 {
		kind, raw, err := r.conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("Codex %s: %w", method, err)
		}
		if kind != websocket.MessageText {
			return fmt.Errorf("Codex returned a non-JSON protocol message")
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err = json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("decode Codex response: %w", err)
		}
		if len(envelope.ID) == 0 {
			continue
		}
		var received int
		if json.Unmarshal(envelope.ID, &received) != nil || received != id {
			return fmt.Errorf("Codex returned an unexpected response id")
		}
		if envelope.Error != nil {
			return fmt.Errorf("Codex %s: %s", method, envelope.Error.Message)
		}
		if len(envelope.Result) == 0 {
			return fmt.Errorf("Codex %s has no result", method)
		}
		if err = json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("decode Codex %s: %w", method, err)
		}
		return nil
	}
	return fmt.Errorf("Codex emitted too many unrelated messages; refresh discovery")
}

func codexSessions(ctx context.Context) ([]Session, bool, error) {
	var daemon struct {
		Status     string `json:"status"`
		SocketPath string `json:"socketPath"`
	}
	if err := commandJSON(ctx, "codex", []string{"app-server", "daemon", "version"}, &daemon); err != nil {
		return nil, false, err
	}
	if daemon.Status != "running" || !filepath.IsAbs(daemon.SocketPath) {
		return nil, false, fmt.Errorf("Codex shared daemon is not running; open Codex normally before importing")
	}
	// Pin this daemon instance's protected socket. The public rendezvous symlink
	// can change during an upgrade; import must not connect to a different worker.
	endpoint, err := filepath.EvalSymlinks(daemon.SocketPath)
	if err != nil {
		return nil, false, err
	}
	stat, err := os.Stat(endpoint)
	if err != nil {
		return nil, false, err
	}
	if stat.Mode()&os.ModeSocket == 0 {
		return nil, false, fmt.Errorf("Codex endpoint is not a Unix socket")
	}
	rpc, err := dialCodex(ctx, endpoint)
	if err != nil {
		return nil, false, err
	}
	defer rpc.conn.CloseNow()
	var loaded struct {
		Data []string `json:"data"`
	}
	if err = rpc.call(ctx, "thread/loaded/list", map[string]any{}, &loaded); err != nil {
		return nil, false, err
	}
	truncated := len(loaded.Data) > maxSessions
	ids := loaded.Data[:min(len(loaded.Data), maxSessions)]
	out := make([]Session, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		ref := Ref{Codex, id}
		if ref.Validate() != nil {
			return nil, false, fmt.Errorf("Codex reported an unsupported session id")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		var response struct {
			Thread struct {
				ID        string          `json:"id"`
				Name      string          `json:"name"`
				CWD       string          `json:"cwd"`
				Source    json.RawMessage `json:"source"`
				CreatedAt int64           `json:"createdAt"`
				Status    struct {
					Type        string   `json:"type"`
					ActiveFlags []string `json:"activeFlags"`
				} `json:"status"`
			} `json:"thread"`
		}
		if err = rpc.call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &response); err != nil {
			return nil, false, err
		}
		t := response.Thread
		if t.ID != id {
			return nil, false, fmt.Errorf("Codex returned a different session than requested")
		}
		var source string
		if json.Unmarshal(t.Source, &source) != nil {
			continue
		} // native subagents are not independent foreground sessions
		if source != "cli" && source != "vscode" && source != "appServer" {
			continue
		}
		s := Session{Ref: ref, Name: t.Name, CWD: t.CWD, Status: t.Status.Type, StartedAt: time.Unix(t.CreatedAt, 0), Endpoint: endpoint, WorkerPID: rpc.peerPID}
		switch t.Status.Type {
		case "idle", "active":
			s.Action = Attach
		default:
			s.Action = Unavailable
			s.Reason = "Codex has not confirmed a loaded, attachable session; open it in Codex and refresh."
		}
		if len(t.Status.ActiveFlags) > 0 {
			s.Status = t.Status.ActiveFlags[0]
			s.WaitingFor = strings.Join(t.Status.ActiveFlags, ", ")
		}
		out = append(out, s)
	}
	return out, truncated, nil
}
