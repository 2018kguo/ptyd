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

const usage = `ptyd runs terminals in the background, like tmux, with a local HTTP API.

Usage:
  ptyd [flags]                     run the daemon (flags: ptyd -h)
  ptyd new [-n name] [-d] [--restore auto|none|rerun|agent] [command [args...]]
                                   start a terminal ($SHELL by default) and attach
  ptyd ls                          list terminals
  ptyd attach [--readonly] <terminal>
  ptyd restore [--policy auto|rerun|agent] <terminal>
                                   start a stopped terminal again and attach
  ptyd kill <terminal>...
  ptyd token                       print the token, for curl -H "X-Ptyd-Token: $(ptyd token)"

<terminal> is an id, its number (3 for term-3-ab12), a name or a unique id
prefix. Press Ctrl-\ twice to detach. Commands reach the daemon at $PTYD_ADDR
(default 127.0.0.1:4242) with $PTYD_TOKEN, or the token the daemon saved
in ~/.local/state/ptyd.
`

func defaultAddr() string {
	if a := os.Getenv("PTYD_ADDR"); a != "" {
		return a
	}
	return "127.0.0.1:4242"
}

// commands are the subcommands; anything else runs the daemon.
var commands = map[string]func(args []string) int{
	"new":     runNew,
	"ls":      runLs,
	"attach":  runAttach,
	"kill":    runKill,
	"restore": runRestore,
	"token":   runToken,
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
	fs, addr := newFlags("new", "[-n name] [-d] [--restore auto|none|rerun|agent] [command [args...]]",
		"Start a terminal running command ($SHELL by default) in the current directory and\n"+
			"attach to it. With -d, or when stdout is not a terminal, print its id instead.")
	var name string
	fs.StringVar(&name, "n", "", "Name (default: the command, or the directory for a shell)")
	fs.StringVar(&name, "name", "", "Same as -n")
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
	interactive := !*detach && term.IsTerminal(int(os.Stdout.Fd()))
	if cols, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cfg.Cols, cfg.Rows = uint16(cols), uint16(rows)
	}
	c := apiAt(*addr)
	var info terminal.Info
	if err := c.Do("POST", "/terminals", cfg, &info); err != nil {
		return fail(err)
	}
	if !interactive {
		fmt.Println(info.ID)
		return 0
	}
	return attach(c, info, false)
}

func runAttach(args []string) int {
	fs, addr := newFlags("attach", "[--readonly] <terminal>",
		"Attach this terminal to a ptyd terminal. Detach with "+client.DetachKeyHelp+".\n"+
			"<terminal> is an id, its number, a name or a unique id prefix.")
	readOnly := fs.Bool("readonly", false, "Watch without sending input or resizing")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	info, err := c.Find(fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	return attach(c, info, *readOnly)
}

// attach attaches to t and tells the user how it ended. Its exit code is
// the program's if the program exited.
func attach(c client.API, t terminal.Info, readOnly bool) int {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	detached, err := client.Attach(ctx, client.Options{Addr: c.Addr, Token: c.Token, ID: string(t.ID), ReadOnly: readOnly}, os.Stdin, os.Stdout, winch)
	if err != nil {
		return fail(err)
	}
	if detached {
		fmt.Fprintf(os.Stderr, "[detached from %s]\n", label(t))
		return 0
	}
	// The output ended: the program exited, or the daemon went away.
	info, err := waitExited(c, t.ID)
	switch {
	case errors.Is(err, client.ErrDaemonDown):
		fmt.Fprintf(os.Stderr, "[lost the connection: ptyd at %s stopped]\n", c.Addr)
		return 1
	case err != nil:
		fmt.Fprintf(os.Stderr, "[lost the connection to ptyd: %v]\n", err)
		return 1
	case info.Status == terminal.StatusRunning:
		fmt.Fprintf(os.Stderr, "[the connection to ptyd closed; %s is still running]\n", label(t))
		return 1
	case info.ExitCode == nil:
		msg := fmt.Sprintf("[%s is not running; this is its last window]", label(t))
		if info.Notice != "" {
			msg += "\n[" + info.Notice + "]"
		}
		msg += fmt.Sprintf("\n[start it again: ptyd restore %s]", t.ID)
		fmt.Fprintln(os.Stderr, msg)
		return 1
	case *info.ExitCode < 0:
		fmt.Fprintln(os.Stderr, "[terminal exited (killed by a signal)]")
		return 1
	default:
		fmt.Fprintf(os.Stderr, "[terminal exited (code %d)]\n", *info.ExitCode)
		return min(*info.ExitCode, 255)
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
	fs, addr := newFlags("ls", "", "List terminals, oldest first.")
	_ = fs.Parse(args)
	list, err := apiAt(*addr).List()
	if err != nil {
		return fail(err)
	}
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "no terminals (start one with `ptyd new`)")
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
	fs, addr := newFlags("kill", "<terminal>...", "Stop terminals (SIGTERM, then SIGKILL after 2s) and forget them.")
	_ = fs.Parse(args)
	if fs.NArg() == 0 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	code := 0
	for _, target := range fs.Args() {
		info, err := c.Find(target)
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
	fs, addr := newFlags("restore", "[--policy auto|rerun|agent] <terminal>",
		"Start a stopped terminal (a ghost) again under the same id, below its last window,\n"+
			"and attach. The policy defaults to the terminal's own, else auto.")
	policy := fs.String("policy", "", "rerun the command, agent to resume the exact agent session, or auto")
	detach := fs.Bool("d", false, "Do not attach")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	t, err := c.Find(fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	p := terminal.RestorePolicy(*policy)
	if p == "" && t.Restore == "" {
		p = terminal.RestoreAuto
	}
	var info terminal.Info
	if err := c.Do("POST", "/terminals/"+string(t.ID)+"/restore", map[string]any{"policy": p}, &info); err != nil {
		return fail(err)
	}
	if *detach || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println(info.ID)
		return 0
	}
	return attach(c, info, false)
}
