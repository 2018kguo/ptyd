package terminal

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
)

// pollable returns the PTY master as a file Go's netpoller can wait on, so
// a terminal blocked reading its output parks a goroutine instead of
// pinning an OS thread. creack/pty hands back a blocking file (its ioctls
// go through File.Fd, which switches the file to blocking mode), so this
// re-wraps a non-blocking duplicate and closes the original.
//
// Linux only: kqueue cannot wait on PTYs on macOS (golang/go#22099), so
// there each terminal keeps a thread blocked in read.
func pollable(f *os.File) *os.File {
	if runtime.GOOS != "linux" {
		return f
	}
	// ForkLock keeps a concurrent exec from inheriting the duplicate
	// before it is marked close-on-exec.
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(f.Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return f
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return f
	}
	_ = f.Close()
	return os.NewFile(uintptr(fd), f.Name())
}

// setSize resizes the PTY. Unlike pty.Setsize it does not call File.Fd,
// which would switch a pollable file back to blocking mode.
func setSize(f *os.File, cols, rows uint16) error {
	ws := pty.Winsize{Cols: cols, Rows: rows}
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}
