package native

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// kern.procargs2 contains argv followed by environment. Stop at argc: no
// environment value or prompt is retained or returned by discovery.
func darwinArgv(raw []byte) ([]string, error) {
	if len(raw) < 4 {
		return nil, fmt.Errorf("missing argument count")
	}
	argc := int(binary.NativeEndian.Uint32(raw[:4]))
	raw = raw[4:]
	if argc < 1 || argc > 4096 {
		return nil, fmt.Errorf("invalid argument count")
	}
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return nil, fmt.Errorf("missing executable path")
	}
	raw = raw[end+1:]
	raw = bytes.TrimLeft(raw, "\x00")
	args := make([]string, 0, argc)
	for range argc {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return nil, fmt.Errorf("truncated argument list")
		}
		args = append(args, string(raw[:end]))
		raw = raw[end+1:]
	}
	return args, nil
}
func localProcesses(ctx context.Context) ([]LocalProcess, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}
	parents := make(map[int]int, len(procs))
	for _, p := range procs {
		parents[int(p.Proc.P_pid)] = int(p.Eproc.Ppid)
	}
	var out []LocalProcess
	var ids []string
	failed := 0
	for _, p := range procs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := unix.ByteSliceToString(p.Proc.P_comm[:])
		if p.Eproc.Tdev < 0 || p.Eproc.Pgid != p.Eproc.Tpgid {
			continue
		}
		raw, err := unix.SysctlRaw("kern.procargs2", int(p.Proc.P_pid))
		if err != nil {
			if processInspectionFailure(err) {
				failed++
			}
			continue
		}
		args, err := darwinArgv(raw)
		if err != nil {
			failed++
			continue
		}
		provider, ok := interactiveProcess(name, args)
		if !ok {
			continue
		}
		pid := int(p.Proc.P_pid)
		start := p.Proc.P_starttime
		out = append(out, LocalProcess{Provider: provider, PID: pid, Group: int(p.Eproc.Pgid), Generation: fmt.Sprintf("%d.%06d", start.Sec, start.Usec), StartedAt: time.Unix(start.Sec, int64(start.Usec)*1000), Ancestors: processAncestors(pid, parents)})
		ids = append(ids, strconv.Itoa(pid))
	}
	if len(ids) == 0 {
		return out, processInspectionError(failed)
	}
	// lsof reads only cwd and stdin paths for these known same-user processes.
	// NUL fields keep spaces and newlines in paths separate from record framing.
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-a", "-p", strings.Join(ids, ","), "-d", "cwd,0", "-F0pfn")
	cmd.WaitDelay = time.Second
	var stdout, stderr cappedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return out, fmt.Errorf("read terminal paths: %w", err)
	}
	dirs, ttys := darwinProcessPaths(stdout.Bytes())
	live := out[:0]
	for _, p := range out {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", p.PID)
		if processInspectionFailure(err) {
			failed++
		}
		if err != nil || int(kp.Proc.P_pid) != p.PID || fmt.Sprintf("%d.%06d", kp.Proc.P_starttime.Sec, kp.Proc.P_starttime.Usec) != p.Generation {
			continue
		}
		p.CWD = dirs[p.PID]
		p.Terminal = ttys[p.PID]
		if p.CWD == "" {
			failed++
		}
		live = append(live, p)
	}
	return live, processInspectionError(failed)
}
func darwinProcessPaths(raw []byte) (map[int]string, map[int]string) {
	dirs, ttys := map[int]string{}, map[int]string{}
	pid := 0
	file := ""
	for field := range bytes.SplitSeq(raw, []byte{0}) {
		field = bytes.TrimLeft(field, "\n")
		if len(field) == 0 {
			continue
		}
		switch field[0] {
		case 'p':
			pid, _ = strconv.Atoi(string(field[1:]))
			file = ""
		case 'f':
			file = string(field[1:])
		case 'n':
			if file == "cwd" {
				dirs[pid] = string(field[1:])
			} else if file == "0" && bytes.HasPrefix(field[1:], []byte("/dev/")) {
				ttys[pid] = string(field[1:])
			}
		}
	}
	return dirs, ttys
}
