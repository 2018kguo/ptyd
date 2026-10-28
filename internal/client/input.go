package client

import (
	"context"
	"fmt"
	"os"
	"runtime"
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
}

// NewInput starts a reader without changing the file's mode or descriptor flags.
// Cancellation wakes an idle poll immediately; it does not close the caller's
// file or wait for another keystroke.
func NewInput(ctx context.Context, in *os.File) (*Input, error) {
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
	bytes := make(chan []byte)
	done := make(chan struct{})
	stopWake := context.AfterFunc(ctx, func() { _, _ = wakeWrite.Write([]byte{1}) })
	go func() {
		defer close(done)
		defer close(bytes)
		defer cancel()
		defer wakeRead.Close()
		defer wakeWrite.Close()
		defer stopWake()
		// Poll uses the descriptor directly, so keep its os.File alive until
		// the reader stops instead of allowing its finalizer to close it.
		defer runtime.KeepAlive(in)
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
				select {
				case bytes <- buf[:n]:
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
	return &Input{Bytes: bytes, cancel: cancel, done: done}, nil
}

// Close cancels and joins the reader. It is safe to call more than once or from
// several goroutines. Once it returns, another owner may read the file.
func (in *Input) Close() {
	in.cancel()
	<-in.done
}
