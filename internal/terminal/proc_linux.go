package terminal

import (
	"fmt"
	"os"
	"strings"
)

// procIdentity names a process uniquely across pid reuse and reboots:
// the boot id plus the process's start time in clock ticks since boot.
func procIdentity(pid int) (string, error) {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	// Fields after the parenthesized command name; starttime is field 22.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", fmt.Errorf("unparsable /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("unparsable /proc/%d/stat", pid)
	}
	return fmt.Sprintf("boot %s start %s", strings.TrimSpace(string(boot)), fields[19]), nil
}
