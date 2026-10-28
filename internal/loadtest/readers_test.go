//go:build loadtest

package loadtest

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientSSEFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
		lines            int64
	}{
		{"HTTP", "unavailable", "HTTP 503", http.StatusServiceUnavailable, 0},
		{"EOF", "event: line\ndata: {}\n\nevent: end\ndata: {}\n\n", "EOF", http.StatusOK, 1},
		{"scanner", strings.Repeat("x", (1<<20)+1), "token too long", http.StatusOK, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			resp, err := http.Get(srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			var count atomic.Int64
			err = drainSSE(context.Background(), resp, &count)
			if err == nil || !strings.Contains(err.Error(), tc.want) || count.Load() != tc.lines {
				t.Fatalf("drain: error=%v lines=%d; want %q, %d", err, count.Load(), tc.want, tc.lines)
			}
		})
	}
}

func TestClientAttachEOF(t *testing.T) {
	conn, peer := net.Pipe()
	go func() {
		defer peer.Close()
		_, _ = peer.Write([]byte("abc"))
	}()
	var count atomic.Int64
	if err := drainAttach(context.Background(), conn, conn, &count); !errors.Is(err, io.EOF) || count.Load() != 3 {
		t.Fatalf("unexpected EOF was accepted: error=%v bytes=%d", err, count.Load())
	}
}

func TestClientTeardown(t *testing.T) {
	t.Run("attach", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(errLoadStopped)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		peer, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		var count atomic.Int64
		done := make(chan error, 1)
		go func() { done <- drainAttach(ctx, conn, conn, &count) }()
		cancel(errLoadStopped)
		assertLoadClientStopped(t, done)
	})
	t.Run("SSE", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}))
		defer srv.Close()
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(errLoadStopped)
		req, err := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var count atomic.Int64
		done := make(chan error, 1)
		go func() { done <- drainSSE(ctx, resp, &count) }()
		cancel(errLoadStopped)
		assertLoadClientStopped(t, done)
		if _, err := loadDelivered([]int64{0}, []int64{count.Load()}); err == nil {
			t.Fatal("healthy but empty stream passed measured progress check")
		}
	})
}

func assertLoadClientStopped(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("intentional teardown failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("teardown left a reader blocked")
	}
}

func TestClientTeardownKeepsRealErrors(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errLoadStopped)
	broken := errors.New("broken reader")
	for _, err := range []error{nil, io.EOF, io.ErrUnexpectedEOF, io.ErrClosedPipe, broken} {
		if got := loadReadError(ctx, err); got == nil {
			t.Fatalf("teardown hid a real read failure: %v", err)
		}
	}
	other, stop := context.WithCancel(context.Background())
	stop()
	if loadReadError(other, context.Canceled) == nil {
		t.Fatal("unexpected cancellation was accepted")
	}
}

func TestClientMeasuredProgress(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after []int64
		want          int64
		fail          bool
	}{
		{"no clients", nil, nil, 0, false},
		{"all delivered", []int64{10, 20}, []int64{15, 25}, 10, false},
		{"one stalled", []int64{10, 20}, []int64{15, 20}, 0, true},
		{"warmup only", []int64{10}, []int64{10}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := loadDelivered(tc.before, tc.after)
			if got != tc.want || (err != nil) != tc.fail {
				t.Fatalf("delivered=%d error=%v, want %d fail=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}
