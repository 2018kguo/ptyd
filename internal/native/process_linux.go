package native

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type linuxProcessStat struct {
	pid, ppid, group, tty, foreground int
	name, generation                  string
}

func parseLinuxProcessStat(raw string) (linuxProcessStat, error) {
	left, right := strings.IndexByte(raw, '('), strings.LastIndex(raw, ") ")
	if left < 1 || right < left {
		return linuxProcessStat{}, fmt.Errorf("invalid process stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(raw[:left]))
	if err != nil {
		return linuxProcessStat{}, err
	}
	fields := strings.Fields(raw[right+2:])
	if len(fields) < 20 {
		return linuxProcessStat{}, fmt.Errorf("short process stat")
	}
	values := make([]int, 5)
	for i, index := range []int{1, 2, 4, 5, 19} {
		values[i], err = strconv.Atoi(fields[index])
		if err != nil {
			return linuxProcessStat{}, err
		}
	}
	return linuxProcessStat{pid: pid, ppid: values[0], group: values[1], tty: values[2], foreground: values[3], name: raw[left+1 : right], generation: fields[19]}, nil
}
func boundedProcessFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxOutput+1))
	if len(b) > maxOutput {
		return nil, fmt.Errorf("process metadata exceeds limit")
	}
	return b, err
}
func localProcesses(ctx context.Context) ([]LocalProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	parents := map[int]int{}
	var candidates []linuxProcessStat
	failed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		info, err := os.Stat(dir)
		if err != nil {
			if processInspectionFailure(err) {
				failed++
			}
			continue
		}
		uid, ok := info.Sys().(*syscall.Stat_t)
		if !ok || uid.Uid != uint32(os.Getuid()) {
			continue
		}
		raw, err := boundedProcessFile(filepath.Join(dir, "stat"))
		if err != nil {
			if processInspectionFailure(err) {
				failed++
			}
			continue
		}
		p, err := parseLinuxProcessStat(string(raw))
		if err != nil {
			failed++
			continue
		}
		parents[p.pid] = p.ppid
		if p.tty != 0 && p.group == p.foreground {
			candidates = append(candidates, p)
		}
	}
	var out []LocalProcess
	for _, p := range candidates {
		dir := fmt.Sprintf("/proc/%d", p.pid)
		raw, err := boundedProcessFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			if processInspectionFailure(err) {
				failed++
			}
			continue
		}
		chunks := bytes.Split(bytes.TrimSuffix(raw, []byte{0}), []byte{0})
		args := make([]string, len(chunks))
		for i, b := range chunks {
			args[i] = string(b)
		}
		provider, ok := interactiveProcess(p.name, args)
		if !ok {
			continue
		}
		cwd, err := os.Readlink(filepath.Join(dir, "cwd"))
		if processInspectionFailure(err) {
			failed++
		}
		tty, err := os.Readlink(filepath.Join(dir, "fd/0"))
		if processInspectionFailure(err) {
			failed++
		}
		after, err := boundedProcessFile(filepath.Join(dir, "stat"))
		if err != nil {
			if processInspectionFailure(err) {
				failed++
			}
			continue
		}
		current, err := parseLinuxProcessStat(string(after))
		if err != nil {
			failed++
		}
		if err != nil || current.generation != p.generation {
			continue
		}
		out = append(out, LocalProcess{Provider: provider, PID: p.pid, Group: p.group, Generation: strings.TrimSpace(string(boot)) + "-" + p.generation, CWD: cwd, Terminal: tty, Ancestors: processAncestors(p.pid, parents)})
	}
	return out, processInspectionError(failed)
}
