package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

const usage = `ptyd keeps groups of terminals running while agents and people come and go.

Usage:
  ptyd                            open your groups (starts the local daemon if needed)
  ptyd new [-d] name [-- command]  create a group and enter its workspace
  ptyd ls [--json|--plain] [--all] browse groups; print a snapshot with --plain
  ptyd attach [group]             enter a group or choose one
  ptyd rename group name          rename a group
  ptyd archive [--undo] group      hide or return finished work
  ptyd remove [--force] group      remove a group; --force stops its terminals
  ptyd term new|ls|attach|rename|move|restore|kill|import
                                  operate on individual terminals
  ptyd status [--json] [--group group] [--discover]
                                  structured terminal overview for agents
  ptyd agents [--json]             discover agents in original terminals
  ptyd serve [flags]              run the daemon in the foreground
  ptyd token                      print the local API token

Quick jk goes back one level: terminal, group, groups, shell. In a group,
v pins a live pane; Enter takes control. Work keeps running. Commands use PTYD_ADDR and the
local daemon's saved token. Use ptyd COMMAND --help for options.
`

func defaultAddr() string {
	if a := os.Getenv("PTYD_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:4242"
}

// commands are the subcommands; anything else runs the daemon.
var commands = map[string]func(args []string) int{
	"new":    runGroupNew,
	"ls":     runGroupList,
	"status": runStatus,
	"ui":     runUI,
	"agents": runAgents,

	"attach":  runGroupAttach,
	"term":    runTerm,
	"rename":  runGroupRename,
	"archive": runGroupArchive,
	"remove":  runGroupRemove,

	"token": runToken,
	"help": func([]string) int {
		fmt.Print(usage)
		return 0
	},
}

// newFlags returns a flag set for a subcommand with the --addr flag.
func newFlags(name, args, help string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	addr := fs.String("addr", defaultAddr(), "Daemon address host:port (default $PTYD_ADDR or 127.0.0.1:4242)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: ptyd %s %s\n\n%s\n\n", name, args, help)
		fs.PrintDefaults()
	}
	return fs, addr
}

func apiAt(addr string) client.API {
	return client.API{Addr: addr, Token: client.Token(addr)}
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "ptyd: %v\n", err)
	return 1
}

// label names a terminal for messages: its name and id.
func label(t terminal.Info) string {
	return fmt.Sprintf("%s (%s)", t.Name, t.ID)
}

// envPassed are the variables of the caller's environment a new terminal
// gets on top of the daemon's: those that describe the user's terminal
// and locale.
var envPassed = []string{"TERM", "COLORTERM", "LANG", "LC_ALL", "LC_CTYPE"}

func runNew(args []string) int {
	fs, addr := newFlags("term new", "[-n name] [-d] [--restore auto|none|rerun|agent] [command [args...]]",
		"Start a terminal running command ($SHELL by default) in the current directory and\n"+
			"attach to it. With -d, or when stdout is not a terminal, print its id instead.")
	var name string
	fs.StringVar(&name, "n", "", "Name (default: the command, or the directory for a shell)")
	fs.StringVar(&name, "name", "", "Same as -n")
	group := fs.String("group", "", "Group name or id (default: inherited group, otherwise Inbox)")
	detach := fs.Bool("d", false, "Do not attach; print the new terminal's id")
	restore := fs.String("restore", "auto", "What to do after a daemon restart: rerun the command, resume the exact\nagent session (claude, codex), or none. auto picks agent for a supported agent, else rerun")
	_ = fs.Parse(args)

	cwd, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	cfg := terminal.CreateConfig{Name: name, CWD: cwd, Restore: terminal.RestorePolicy(*restore), Env: map[string]string{}}
	if cmd := fs.Args(); len(cmd) > 0 {
		cfg.Command, cfg.Args = cmd[0], cmd[1:]
		// The daemon runs elsewhere: a path is relative to here.
		if strings.Contains(cfg.Command, "/") {
			cfg.Command, _ = filepath.Abs(cfg.Command)
		}
		if cfg.Name == "" {
			cfg.Name = filepath.Base(cfg.Command)
		}
	} else {
		cfg.Command = os.Getenv("SHELL")
		if cfg.Name == "" {
			cfg.Name = filepath.Base(cwd)
		}
	}
	for _, k := range envPassed {
		if v, ok := os.LookupEnv(k); ok {
			cfg.Env[k] = v
		}
	}
	if err := client.TerminalEnvironment().Apply(&cfg); err != nil {
		return fail(err)
	}
	interactive := !*detach && term.IsTerminal(int(os.Stdout.Fd()))
	if cols, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cfg.Cols, cfg.Rows = uint16(cols), uint16(max(rows-1, 1))
	}
	var initial []byte
	if interactive {
		cfg.TerminalColors, initial = client.ProbeColors(context.Background(), os.Stdin, os.Stdout)
	}
	c := apiAt(*addr)
	if err := ensureDaemon(c); err != nil {
		return fail(err)
	}
	c = apiAt(*addr)
	g, err := groupContext(c, *group)
	if err != nil {
		return fail(err)
	}
	cfg.GroupID = g.ID
	var info terminal.Info
	if err := c.Do("POST", "/terminals", cfg, &info); err != nil {
		return fail(err)
	}
	if !interactive {
		fmt.Println(info.ID)
		return 0
	}
	code, _ := attach(context.Background(), c, info, client.Options{InitialInput: initial, Human: true})
	return code
}

func runAttach(args []string) int {
	fs, addr := newFlags("term attach", "[--readonly] <terminal>",
		"Attach this terminal to a ptyd terminal. Detach with "+client.DetachKeyHelp+".\n"+
			"<terminal> is an id, its number, a name or a unique id prefix.")
	readOnly := fs.Bool("readonly", false, "Watch without sending input or resizing")
	group := fs.String("group", "", "Resolve terminal names inside this group")
	raw := fs.Bool("raw", false, "Pass terminal output through without a status row")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	info, err := findTerminal(c, fs.Arg(0), *group)
	if err != nil {
		return fail(err)
	}
	code, _ := attach(context.Background(), c, info, client.Options{ReadOnly: *readOnly, Raw: *raw, Human: true})
	return code
}

// attach attaches to t and tells the user how it ended. Its exit code is
// the program's if the program exited.
func attach(parent context.Context, c client.API, t terminal.Info, opts client.Options) (int, []byte) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	ctx, stop := signal.NotifyContext(parent, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	opts.Addr, opts.Token, opts.ID = c.Addr, c.Token, string(t.ID)
	opts.Label = t.Name
	if t.GroupID != "" {
		var g terminal.Group
		if err := c.Do("GET", "/groups/"+string(t.GroupID), nil, &g); err == nil {
			opts.Label = g.Name + " / " + t.Name
		}
	}
	result, err := client.Attach(ctx, opts, os.Stdin, os.Stdout, winch)
	var pending []byte
	if result.Detached && err == nil {
		pending = result.PendingInput
	}
	if err != nil {
		return fail(err), pending
	}
	if result.Detached {
		fmt.Fprintf(os.Stderr, "[detached from %s]\n", label(t))
		return 0, pending
	}
	// The output ended: the program exited, or the daemon went away.
	info, err := waitExited(c, t.ID)
	switch {
	case errors.Is(err, client.ErrDaemonDown):
		fmt.Fprintf(os.Stderr, "[lost the connection: ptyd at %s stopped]\n", c.Addr)
		return 1, pending
	case err != nil:
		fmt.Fprintf(os.Stderr, "[lost the connection to ptyd: %v]\n", err)
		return 1, pending
	case info.Status == terminal.StatusRunning:
		fmt.Fprintf(os.Stderr, "[the connection to ptyd closed; %s is still running]\n", label(t))
		return 1, pending
	case info.ExitCode == nil:
		msg := fmt.Sprintf("[%s is not running; this is its last window]", label(t))
		if info.Notice != "" {
			msg += "\n[" + info.Notice + "]"
		}
		msg += fmt.Sprintf("\n[start it again: ptyd term restore %s]", t.ID)
		fmt.Fprintln(os.Stderr, msg)
		return 1, pending
	case *info.ExitCode < 0:
		fmt.Fprintln(os.Stderr, "[terminal exited (killed by a signal)]")
		return 1, pending
	default:
		fmt.Fprintf(os.Stderr, "[terminal exited (code %d)]\n", *info.ExitCode)
		return min(*info.ExitCode, 255), pending
	}
}

// waitExited returns the terminal's state once it is no longer running,
// or after a short wait: its output ends just before its exit status is
// known.
func waitExited(c client.API, id terminal.ID) (terminal.Info, error) {
	var info terminal.Info
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		err := c.Do("GET", "/terminals/"+string(id), nil, &info)
		if err != nil || info.Status != terminal.StatusRunning || time.Now().After(deadline) {
			return info, err
		}
	}
}

func runLs(args []string) int {
	fs, addr := newFlags("term ls", "[--json] [--group name]", "List terminals. Inside a ptyd terminal, the inherited group is the default.")
	json := fs.Bool("json", false, "Return terminal metadata as JSON")
	group := fs.String("group", "", "Group name or id; omit outside ptyd to list all terminals")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	list, err := c.List()
	if err != nil {
		return fail(err)
	}
	if *group != "" || os.Getenv("PTYD_GROUP_ID") != "" || os.Getenv("PTYD_TERMINAL_ID") != "" {
		g, err := groupContext(c, *group)
		if err != nil {
			return fail(err)
		}
		rows := list[:0]
		for _, t := range list {
			if t.GroupID == g.ID {
				rows = append(rows, t)
			}
		}
		list = rows
	}
	if *json {
		return encodeJSON(os.Stdout, list)
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no terminals (start one with ptyd term new)")
		return 0
	}
	printList(os.Stdout, list)
	return 0
}

func printList(w io.Writer, list []terminal.Info) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tIDLE\tATTACHED\tCOMMAND")
	for _, t := range list {
		status := string(t.Status)
		switch {
		case t.Status != terminal.StatusExited:
		case t.ExitCode == nil:
			status = "ghost"
		case *t.ExitCode < 0:
			status = "killed"
		default:
			status = fmt.Sprintf("exited %d", *t.ExitCode)
		}
		idle := "-"
		if t.LastOutputAt != nil && t.Status == terminal.StatusRunning {
			idle = shortDuration(time.Duration(t.IdleSeconds * float64(time.Second)))
		}
		attached := "-"
		if t.Attached > 0 {
			attached = fmt.Sprint(t.Attached)
		}
		cmd := strings.Join(append([]string{filepath.Base(t.Command)}, t.Args...), " ")
		if r := []rune(cmd); len(r) > 50 {
			cmd = string(r[:49]) + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, status, idle, attached, cmd)
	}
	_ = tw.Flush()
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func runKill(args []string) int {
	fs, addr := newFlags("term kill", "[--group name] <terminal>...", "Stop terminals (SIGTERM, then SIGKILL after 2s) and forget them.")
	group := fs.String("group", "", "Resolve names within this group")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	code := 0
	for _, target := range fs.Args() {
		info, err := findTerminal(c, target, *group)
		if err == nil {
			err = c.Do("DELETE", "/terminals/"+string(info.ID), nil, nil)
		}
		if err != nil {
			code = fail(err)
			continue
		}
		fmt.Printf("killed %s\n", label(info))
	}
	return code
}

func runToken(args []string) int {
	fs, addr := newFlags("token", "", "Print the token the commands send to the daemon.")
	_ = fs.Parse(args)
	tok := client.Token(*addr)
	if tok == "" {
		path, _ := client.TokenFile(*addr)
		return fail(fmt.Errorf("no token: PTYD_TOKEN is unset and %s does not exist (is ptyd running?)", path))
	}
	fmt.Println(tok)
	return 0
}

func runRestore(args []string) int {
	fs, addr := newFlags("term restore", "[--policy auto|rerun|agent] <terminal>",
		"Start a stopped terminal (a ghost) again under the same id, below its last window,\n"+
			"and attach. The policy defaults to the terminal's own, else auto. An exited\n"+
			"terminal is replaced by a new one running the same command.")
	group := fs.String("group", "", "Resolve names within this group")
	policy := fs.String("policy", "", "rerun the command, agent to resume the exact agent session, or auto")
	detach := fs.Bool("d", false, "Do not attach")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	t, err := findTerminal(c, fs.Arg(0), *group)
	if err != nil {
		return fail(err)
	}
	p := terminal.RestorePolicy(*policy)
	if p == "" && t.Restore == "" {
		p = terminal.RestoreAuto
	}
	var info terminal.Info
	err = c.Do("POST", "/terminals/"+string(t.ID)+"/relaunch", map[string]any{"policy": p}, &info)
	if err != nil {
		return fail(err)
	}
	if *detach || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println(info.ID)
		return 0
	}
	code, _ := attach(context.Background(), c, info, client.Options{Human: true})
	return code
}
