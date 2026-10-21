package terminal

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// procIdentity names a process uniquely across pid reuse and reboots:
// the boot time plus the process's start time.
func procIdentity(pid int) (string, error) {
	boot, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return "", err
	}
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if int(kp.Proc.P_pid) != pid {
		return "", fmt.Errorf("process %d not found", pid)
	}
	st := kp.Proc.P_starttime
	return fmt.Sprintf("boot %d.%06d start %d.%06d", boot.Sec, boot.Usec, st.Sec, st.Usec), nil
}
