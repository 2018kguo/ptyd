package client

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/terminal"
	"github.com/creack/pty"
	"golang.org/x/term"
)

// Synthetic full-screen redraws model TUIs which repaint a scrolling transcript
// between fixed header/composer rows. The host is an emulator, not a GPU/window:
// this measures CPU, allocations and emitted bytes, not physical display latency.
func paneScrollFrames(cols, rows int, direction int) [][]byte {
	frames := make([][]byte, 64)
	for i := range frames {
		offset := i
		if direction < 0 {
			offset = len(frames) - i
		}
		b := []byte("\x1b[?2026h\x1b[H\x1b[0mAgent transcript\x1b[K\r\n\x1b[2K\r\n")
		for y := 2; y < rows-3; y++ {
			b = fmt.Appendf(b, "\x1b[0m\x1b[38;2;170;180;200m%06d \x1b[0mRead project files and check the working tree. \x1b[36m雪 👩‍💻\x1b[0m\x1b[K\r\n", offset+y)
		}
		b = append(b, "\x1b[48;2;60;64;72m\x1b[2K > Draft a reply\r\n\x1b[2K\x1b[0m\r\n\x1b[2K\x1b[?2026l"...)
		frames[i] = b
	}
	return frames
}

// This local test process behaves like a mouse-driven TUI. No agent, model or
// user's daemon is involved. Each wheel event redraws the transcript and marks
// a new frame; the observer waits for its synchronized update to be complete.
func TestPaneScrollWorker(t *testing.T) {
	if os.Getenv("PTYD_SCROLL_WORKER") != "1" {
		t.Skip("subprocess fixture")
	}
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		os.Exit(2)
	}
	defer term.Restore(int(os.Stdin.Fd()), state)
	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		os.Exit(3)
	}
	frames := paneScrollFrames(cols, rows, 1)
	paint := func(i int) {
		frame := bytes.Replace(frames[i%len(frames)], []byte("Agent transcript"), []byte(fmt.Sprintf("FRAME:%08d", i)), 1)
		os.Stdout.Write(frame)
	}
	fmt.Print("\x1b[?1000h\x1b[?1006h")
	paint(0)
	reader := bufio.NewReader(os.Stdin)
	for i := 1; ; i++ {
		if _, err := reader.ReadBytes('M'); err != nil {
			break
		}
		paint(i)
	}
	os.Exit(0)
}

// Keep the measurement fixture self-contained so the same *bench_test.go file
// compiles against both revisions without changing their production/test code.
type paneScrollProbe struct {
	master, slave *os.File
	mu            sync.Mutex
	screen        *phosphor.Terminal
	updated       chan struct{}
}

func waitScrollPaint(t testing.TB, u *paneScrollProbe, i int) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		u.mu.Lock()
		painted := strings.HasPrefix(u.screen.Line(0), fmt.Sprintf("FRAME:%08d", i)) && !u.screen.Modes().SyncUpdate
		u.mu.Unlock()
		if painted {
			return
		}
		select {
		case <-u.updated:
		case <-deadline.C:
			t.Fatalf("scroll frame %d did not finish painting", i)
		}
	}
}

func scrollTerminal(t testing.TB, cols, rows int, framed bool) *paneScrollProbe {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		master.Close()
		slave.Close()
		t.Fatal(err)
	}
	u := &paneScrollProbe{master: master, slave: slave, screen: phosphor.New(cols, rows), updated: make(chan struct{}, 1)}
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := master.Read(buf)
			u.mu.Lock()
			u.screen.Write(buf[:n])
			u.mu.Unlock()
			select {
			case u.updated <- struct{}{}:
			default:
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { master.Close(); slave.Close() })
	if !framed {
		cmd := exec.Command(binary, "-test.run=^TestPaneScrollWorker$")
		cmd.Env = append(os.Environ(), "PTYD_SCROLL_WORKER=1")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = u.slave, u.slave, u.slave
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	} else {
		mgr := terminal.NewManager()
		server := httptest.NewServer(api.New(mgr, api.Config{Token: "tok"}).Handler())
		t.Cleanup(func() { mgr.CloseAll(); server.Close() })
		addr := strings.TrimPrefix(server.URL, "http://")
		info, err := mgr.Create(terminal.CreateConfig{Name: "scroll fixture", Command: binary, Args: []string{"-test.run=^TestPaneScrollWorker$"}, Cols: uint16(cols), Rows: uint16(rows - 1), Env: map[string]string{"PTYD_SCROLL_WORKER": "1"}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := Attach(ctx, Options{Addr: addr, Token: "tok", ID: string(info.ID), Label: "scroll witness"}, u.slave, u.slave, nil)
			done <- err
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("scroll attachment did not stop")
			}
		})
	}
	waitScrollPaint(t, u, 0)
	return u
}

// Real local PTY input -> app -> (daemon/TCP/pane for framed) -> host emulator.
// This includes batching and paint scheduling but excludes a GUI's GPU/display.
// Direct and framed views give the app identical dimensions (the latter adds
// the persistent status row). Sequential events model continuous scrolling.
func BenchmarkAttachScrollLatency(b *testing.B) {
	for _, size := range [][2]int{{120, 30}, {240, 64}} {
		for _, framed := range []bool{false, true} {
			b.Run(fmt.Sprintf("%dx%d/framed%t", size[0], size[1], framed), func(b *testing.B) {
				rows := size[1] - 1
				if framed {
					rows++
				}
				u := scrollTerminal(b, size[0], rows, framed)
				samples := make([]time.Duration, 0, b.N)
				b.ResetTimer()
				for i := 1; i <= b.N; i++ {
					start := time.Now()
					if _, err := u.master.Write([]byte("\x1b[<65;5;5M")); err != nil {
						b.Fatal(err)
					}
					waitScrollPaint(b, u, i)
					samples = append(samples, time.Since(start))
				}
				b.StopTimer()
				slices.Sort(samples)
				b.ReportMetric(float64(samples[len(samples)/2])/float64(time.Millisecond), "p50-ms")
				b.ReportMetric(float64(samples[(len(samples)-1)*95/100])/float64(time.Millisecond), "p95-ms")
			})
		}
	}
}

type paneBenchHost struct {
	screen *phosphor.Terminal
	bytes  int64
}

func (h *paneBenchHost) Write(b []byte) (int, error) {
	h.bytes += int64(len(b))
	return h.screen.Write(b)
}

func BenchmarkPaneScroll(b *testing.B) {
	for _, size := range [][2]int{{120, 30}, {240, 64}} {
		for _, direction := range []int{1, -1} {
			name := fmt.Sprintf("%dx%d/direction%d", size[0], size[1], direction)
			frames := paneScrollFrames(size[0], size[1]-1, direction)
			b.Run(name+"/direct", func(b *testing.B) {
				h := &paneBenchHost{screen: phosphor.New(size[0], size[1]-1)}
				h.Write(frames[0])
				h.bytes = 0
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					h.Write(frames[i%len(frames)])
				}
				b.StopTimer()
				b.ReportMetric(float64(h.bytes)/float64(b.N), "wire-B/frame")
			})
			b.Run(name+"/framed", func(b *testing.B) {
				h := &paneBenchHost{screen: phosphor.New(size[0], size[1])}
				p := newPane(h, "group / agent", size[0], size[1])
				p.Write(frames[0])
				p.draw(false)
				h.bytes = 0
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					p.Write(frames[i%len(frames)])
					if err := p.draw(false); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(h.bytes)/float64(b.N), "wire-B/frame")
			})
		}
	}
}
