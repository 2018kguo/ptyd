package client

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func inputFlags(t *testing.T, file *os.File) int {
	t.Helper()
	conn, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags int
	var flagErr error
	if err := conn.Control(func(fd uintptr) {
		flags, flagErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if flagErr != nil {
		t.Fatal(flagErr)
	}
	return flags
}

func receiveInput(t *testing.T, input *Input) []byte {
	t.Helper()
	select {
	case p, ok := <-input.Bytes:
		if !ok {
			t.Fatal("input ended before the next bytes")
		}
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("input reader did not deliver bytes")
		return nil
	}
}

func waitInputClosed(t *testing.T, input *Input) {
	t.Helper()
	select {
	case p, ok := <-input.Bytes:
		if ok {
			t.Fatalf("unexpected input while stopping: %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("input reader did not stop")
	}
	input.Close()
}

func TestInputCancellationLeavesNextBytesForNextOwner(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	flags := inputFlags(t, read)
	ctx, cancel := context.WithCancel(context.Background())
	input, err := NewInput(ctx, read)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if _, err := write.WriteString("first"); err != nil {
		t.Fatal(err)
	}
	if got := string(receiveInput(t, input)); got != "first" {
		t.Fatalf("first input = %q", got)
	}
	cancel()
	waitInputClosed(t, input)
	if got := inputFlags(t, read); got != flags {
		t.Fatalf("descriptor flags changed from %#x to %#x", flags, got)
	}
	if _, err := write.WriteString("next"); err != nil {
		t.Fatal(err)
	}
	next, err := NewInput(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if got := string(receiveInput(t, next)); got != "next" {
		t.Fatalf("next owner received %q", got)
	}
}

func TestInputCloseJoinsIdleReaderAndIsConcurrent(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	input, err := NewInput(context.Background(), read)
	if err != nil {
		t.Fatal(err)
	}
	var owners sync.WaitGroup
	for range 8 {
		owners.Go(input.Close)
	}
	done := make(chan struct{})
	go func() { owners.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("closing an idle reader waited for input")
	}
	if _, err := read.Stat(); err != nil {
		t.Fatalf("reader closed the caller's file: %v", err)
	}
}

func TestInputEOFAndReadErrorReleaseOwnership(t *testing.T) {
	t.Run("EOF", func(t *testing.T) {
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		input, err := NewInput(context.Background(), read)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		if _, err := write.WriteString("final"); err != nil {
			t.Fatal(err)
		}
		if err := write.Close(); err != nil {
			t.Fatal(err)
		}
		if got := string(receiveInput(t, input)); got != "final" {
			t.Fatalf("last input before EOF = %q", got)
		}
		waitInputClosed(t, input)
		if _, err := read.Stat(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("read error", func(t *testing.T) {
		directory, err := os.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer directory.Close()
		input, err := NewInput(context.Background(), directory)
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		waitInputClosed(t, input)
		if _, err := directory.Stat(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("closed file", func(t *testing.T) {
		file, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		if input, err := NewInput(context.Background(), file); err == nil {
			input.Close()
			t.Fatal("accepted a closed input file")
		}
	})
}

func TestAttachReturnsInputOwnershipOnEveryDetach(t *testing.T) {
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "cat", Command: "cat", Cols: 40, Rows: 5})
	if err != nil {
		t.Fatal(err)
	}
	u := newUserTerminal(t, 40, 5)
	state, err := term.MakeRaw(int(u.slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer term.Restore(int(u.slave.Fd()), state)
	for range 5 {
		before := len(u.rawOutput())
		done := attach(u, addr, string(info.ID), make(chan os.Signal))
		eventually(t, "attached window", func() bool { return len(u.rawOutput()) > before })
		u.typeKeys(t, "\x1c\x1c")
		select {
		case result := <-done:
			if result.err != nil || !result.detached {
				t.Fatalf("detach: %+v", result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("attachment did not detach")
		}
		// A dashboard now owns the same file. A leftover attach reader
		// must not consume this first key or any later cycle's input.
		u.typeKeys(t, "v")
		// The user can type while the dashboard redraws, before it starts
		// its next reader. Those bytes must stay queued for that owner.
		time.Sleep(20 * time.Millisecond)
		view, err := NewInput(context.Background(), u.slave)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(view.Close)
		got := receiveInput(t, view)
		view.Close()
		if string(got) != "v" {
			t.Fatalf("dashboard received %q", got)
		}
	}
}
