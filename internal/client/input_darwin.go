package client

import (
	"os"

	"golang.org/x/sys/unix"
)

// FlushInput drops keys typed before a newly imported native view is ready.
// The input reader must be stopped first so it cannot race this handoff.
func FlushInput(in *os.File) error {
	return unix.IoctlSetPointerInt(int(in.Fd()), unix.TIOCFLUSH, 1 /* FREAD: flush the input queue only */)
}
