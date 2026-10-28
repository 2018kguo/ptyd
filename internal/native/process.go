package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// LocalProcess is an observed terminal tab, not a verified conversation. It
// cannot authorize import, resume or input. Arguments and prompts stay private.
type LocalProcess struct {
	Provider   Provider  `json:"provider"`
	PID        int       `json:"pid"`
	Generation string    `json:"generation"`
	Terminal   string    `json:"terminal"`
	CWD        string    `json:"cwd"`
	StartedAt  time.Time `json:"started_at,omitzero"`
	Group      int       `json:"-"`
	Ancestors  []int     `json:"ancestors,omitempty"`
}

func (p LocalProcess) Key() string {
	return fmt.Sprintf("process:%s:%d:%s", p.Provider, p.PID, p.Generation)
}
func (p LocalProcess) Guidance() string {
	if p.Provider == Claude {
		return "Run /bg in the original Claude tab, let the handoff finish, then refresh. This running process is missing from Claude's native session list."
	}
	return "Keep using the original Codex tab; this process has no verified live attach route. Start future sessions with ptyd new -- codex."
}

// Only the command family and helper names are examined; argv is never kept.
func interactiveProcess(name string, args []string) (Provider, bool) {
	if len(args) == 0 {
		return "", false
	}
	p := Provider(name)
	if p != Codex && p != Claude {
		p = Provider(filepath.Base(args[0]))
	}
	if p != Codex && p != Claude {
		return "", false
	}
	first := ""
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if p == Claude && (arg == "-p" || arg == "--print") {
			return "", false
		}
		switch arg {
		case "--help", "-h", "--version", "-v":
			return "", false
		}
		switch arg {
		case "--config", "-m", "--model", "--profile", "-C", "--cd", "--add-dir", "--agent", "--effort", "--permission-mode", "--session-id", "--remote", "--remote-auth-token-env", "--settings", "--setting-sources", "--mcp-config", "--plugin-dir", "--output-format", "--input-format":
			i++
			continue
		}
		if p == Codex && (arg == "-p" || arg == "-c") {
			i++
			continue
		}
		if (arg == "--resume" || arg == "-r") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if first == "" {
			first = arg
		}
	}
	switch first {
	case "app-server", "exec", "exec-server", "code-mode-host", "mcp-server", "mcp", "login", "logout", "completion", "debug", "sandbox", "apply", "cloud", "features", "daemon", "bg-spare", "bg-pty-host", "agents", "update", "install", "doctor", "auth", "plugin", "setup-token":
		return "", false
	}
	return p, true
}
func processAncestors(pid int, parents map[int]int) []int {
	var out []int
	seen := map[int]bool{pid: true}
	for len(out) < 128 {
		pid = parents[pid]
		if pid <= 1 || seen[pid] {
			break
		}
		seen[pid] = true
		out = append(out, pid)
	}
	return out
}
func (p LocalProcess) groupKey() string {
	if p.Terminal == "" || p.Group <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", p.Terminal, p.Group)
}
func unlistedProcesses(rows []LocalProcess, sessions []Session) []LocalProcess {
	known := map[int]bool{}
	for _, s := range sessions {
		if s.WorkerPID > 0 {
			known[s.WorkerPID] = true
		}
	}
	knownGroups := map[string]bool{}
	for _, p := range rows {
		if known[p.PID] && p.groupKey() != "" {
			knownGroups[p.groupKey()] = true
		}
	}
	out := make([]LocalProcess, 0, len(rows))
	groups := map[string]int{}
	for _, p := range rows {
		if known[p.PID] || knownGroups[p.groupKey()] {
			continue
		}
		if p.Terminal != "" && p.Group > 0 {
			key := fmt.Sprintf("%s:%d", p.Terminal, p.Group)
			if i, ok := groups[key]; ok {
				if len(p.Ancestors) > len(out[i].Ancestors) {
					out[i] = p
				}
				continue
			}
			groups[key] = len(out)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}
func discoverProcesses(ctx context.Context, sessions []Session) ([]LocalProcess, string, bool) {
	rows, err := localProcesses(ctx)
	issue := ""
	if err != nil {
		issue = "Original terminal tabs could not be fully inspected: " + err.Error()
	}
	rows = unlistedProcesses(rows, sessions)
	truncated := len(rows) > maxSessions
	return rows[:min(len(rows), maxSessions)], issue, truncated
}
func (p LocalProcess) Name() string {
	name := filepath.Base(p.CWD)
	if name == "." || name == "/" || p.CWD == "" {
		name = string(p.Provider)
	}
	tty := strings.TrimPrefix(p.Terminal, "/dev/")
	if tty != "" {
		name += " · " + tty
	}
	return name
}

// Exit races are expected; permission and unsupported metadata failures make
// inventory partial. Error text never includes argv or environment contents.
func processInspectionFailure(err error) bool {
	return err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ESRCH)
}
func processInspectionError(failed int) error {
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%d process records could not be inspected", failed)
}
