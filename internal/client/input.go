package client

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// Input reads from a file until EOF, a read error, or cancellation. The caller
// owns the file and must give this reader exclusive access until Close returns.
// Each received byte slice belongs to the receiver.
type Input struct {
	Bytes  <-chan []byte
	cancel context.CancelFunc
	done   <-chan struct{}
	mu     sync.Mutex
	unread []byte
}

// NewInput starts a reader without changing the file's mode or descriptor flags.
// Cancellation wakes an idle poll immediately; it does not close the caller's
// file or wait for another keystroke. Initial bytes are copied and delivered
// before reading the file.
func NewInput(ctx context.Context, in *os.File, initial []byte) (*Input, error) {
	conn, err := in.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("input file: %w", err)
	}
	var fd int
	if err := conn.Control(func(value uintptr) { fd = int(value) }); err != nil {
		return nil, fmt.Errorf("input descriptor: %w", err)
	}
	wakeRead, wakeWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("input wakeup: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	chunks := make(chan []byte)
	done := make(chan struct{})
	input := &Input{Bytes: chunks, cancel: cancel, done: done}
	pending := bytes.Clone(initial)
	stopWake := context.AfterFunc(ctx, func() { _, _ = wakeWrite.Write([]byte{1}) })
	go func() {
		defer close(done)
		defer close(chunks)
		defer cancel()
		defer wakeRead.Close()
		defer wakeWrite.Close()
		defer stopWake()
		// Poll uses the descriptor directly, so keep its os.File alive until
		// the reader stops instead of allowing its finalizer to close it.
		defer runtime.KeepAlive(in)
		defer func() { input.unread = pending }()
		if len(pending) > 0 {
			select {
			case chunks <- pending:
				pending = nil
			case <-ctx.Done():
				return
			}
		}
		fds := []unix.PollFd{
			{Fd: int32(fd), Events: unix.POLLIN},
			{Fd: int32(wakeRead.Fd()), Events: unix.POLLIN},
		}
		for ctx.Err() == nil {
			_, err := unix.Poll(fds, -1)
			if err == syscall.EINTR {
				continue
			}
			if err != nil || ctx.Err() != nil || fds[1].Revents != 0 {
				return
			}
			if fds[0].Revents&unix.POLLNVAL != 0 {
				return
			}
			if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) == 0 {
				continue
			}
			buf := make([]byte, 4096)
			n, err := unix.Read(fd, buf)
			if n > 0 {
				pending = buf[:n]
				select {
				case chunks <- pending:
					pending = nil
				case <-ctx.Done():
					return
				}
			}
			if err == syscall.EINTR || err == syscall.EAGAIN {
				continue
			}
			if err != nil || n == 0 {
				return
			}
		}
	}()
	return input, nil
}

// Close cancels and joins the reader. It is safe to call more than once or from
// several goroutines. It discards undelivered bytes; use Remainder instead when
// handing them to another owner.
func (in *Input) Close() {
	in.cancel()
	<-in.done
	in.mu.Lock()
	in.unread = nil
	in.mu.Unlock()
}

// Remainder stops and joins the reader, then transfers any bytes it read but
// did not deliver. Call it instead of Close for a handoff. Later calls return
// no bytes. Once it returns, another owner may read the file.
func (in *Input) Remainder() []byte {
	in.cancel()
	<-in.done
	in.mu.Lock()
	defer in.mu.Unlock()
	pending := in.unread
	in.unread = nil
	return pending
}
