package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
	"golang.org/x/term"
)

func ensureDaemon(c client.API) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := c.DoContext(ctx, "GET", "/healthz", nil, nil)
	if err == nil {
		return nil
	}
	if !errors.Is(err, client.ErrDaemonDown) {
		return err
	}
	host, _, e := net.SplitHostPort(c.Addr)
	if e != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return err
	}
	binary, e := os.Executable()
	if e != nil {
		return e
	}
	tokenPath, e := client.TokenFile(c.Addr)
	if e != nil {
		return e
	}
	dir := filepath.Dir(tokenPath)
	if e = os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	log, e := os.OpenFile(filepath.Join(dir, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer log.Close()
	cmd := exec.Command(binary, "serve", "--bind", c.Addr)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		return e
	}
	_ = cmd.Process.Release()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(40 * time.Millisecond) {
		check, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		e = c.DoContext(check, "GET", "/healthz", nil, nil)
		cancel()
		if e == nil {
			return nil
		}
	}
	return fmt.Errorf("ptyd could not start; read %s", filepath.Join(dir, "daemon.log"))
}
func groupContext(c client.API, explicit string) (terminal.Group, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if explicit != "" {
		return c.FindGroup(ctx, explicit)
	}
	if id := os.Getenv("PTYD_TERMINAL_ID"); id != "" {
		var info terminal.Info
		err := c.DoContext(ctx, "GET", "/terminals/"+id, nil, &info)
		if err == nil && info.GroupID != "" {
			return c.FindGroup(ctx, string(info.GroupID))
		}
		if err != nil {
			var response *client.HTTPError
			if !errors.As(err, &response) || response.Status != 404 {
				return terminal.Group{}, err
			}
		}
	}
	if id := os.Getenv("PTYD_GROUP_ID"); id != "" {
		return c.FindGroup(ctx, id)
	}
	return c.FindGroup(ctx, string(terminal.DefaultGroup))
}
func runGroupNew(args []string) int {
	fs, addr := newFlags("new", "[-d] [--cwd directory] name [-- command [args...]]", "Create a persistent group and enter its workspace. -d prints its id.\nAn optional command starts the group's first terminal.")
	detached := fs.Bool("d", false, "Create in the background and print the group id")
	cwd := fs.String("cwd", "", "Default directory for terminals in this group")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	argv := fs.Args()
	name := argv[0]
	argv = argv[1:]
	if len(argv) > 0 {
		if argv[0] != "--" || len(argv) == 1 {
			fs.Usage()
			return 2
		}
		argv = argv[1:]
	}
	if *cwd == "" {
		*cwd, _ = os.Getwd()
	}
	abs, err := filepath.Abs(*cwd)
	if err != nil {
		return fail(err)
	}
	c := apiAt(*addr)
	if err = ensureDaemon(c); err != nil {
		return fail(err)
	}
	c = apiAt(*addr)
	var g terminal.Group
	if err = c.Do("POST", "/groups", terminal.GroupConfig{Name: name, CWD: abs}, &g); err != nil {
		return fail(err)
	}
	var initial terminal.ID
	if len(argv) > 0 {
		cfg := terminal.CreateConfig{GroupID: g.ID, Name: filepath.Base(argv[0]), Command: argv[0], Args: argv[1:], CWD: g.CWD, Restore: terminal.RestoreAuto}
		_ = client.TerminalEnvironment().Apply(&cfg)
		if !*detached && term.IsTerminal(int(os.Stdout.Fd())) {
			cfg.TerminalColors, _ = client.ProbeColors(context.Background(), os.Stdin, os.Stdout)
		}
		var info terminal.Info
		if err = c.Do("POST", "/terminals", cfg, &info); err != nil {
			return fail(fmt.Errorf("group %q was created, but its terminal could not start: %w", g.Name, err))
		}
		initial = info.ID
	}
	if *detached || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println(g.ID)
		return 0
	}
	return failUI(workspace(c, g.ID, initial, os.Stdin, os.Stdout))
}
func runGroupAttach(args []string) int {
	fs, addr := newFlags("attach", "[group]", "Enter a group's workspace. Without a name, choose from your groups.")
	_ = fs.Parse(args)
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	if err := ensureDaemon(c); err != nil {
		return fail(err)
	}
	c = apiAt(*addr)
	var id terminal.GroupID
	if fs.NArg() == 1 {
		g, err := c.FindGroup(context.Background(), fs.Arg(0))
		if err != nil {
			return fail(err)
		}
		id = g.ID
	}
	return failUI(workspace(c, id, "", os.Stdin, os.Stdout))
}
func runGroupList(args []string) int {
	fs, addr := newFlags("ls", "[--json] [--all]", "List persistent groups. Attach a group with ptyd attach name.")
	asJSON := fs.Bool("json", false, "Return structured group counts and stable ids")
	all := fs.Bool("all", false, "Include archived groups and saved Inbox terminals")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	if err := ensureDaemon(c); err != nil {
		return fail(err)
	}
	c = apiAt(*addr)
	rows, err := c.Groups(context.Background())
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return encodeJSON(os.Stdout, rows)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "GROUP\tRUNNING\tTERMINALS\tATTENTION\tDIRECTORY")
	for _, g := range rows {
		if !*all && (g.Archived || (g.ID == terminal.DefaultGroup && g.Running == 0)) {
			continue
		}
		name := g.Name
		if g.Archived {
			name += " (archived)"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\n", displayText(name, 64), g.Running, g.Terminals, g.Attention, displayText(g.CWD, 80))
	}
	_ = tw.Flush()
	return 0
}
func encodeJSON(w io.Writer, value any) int {
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return fail(err)
	}
	return 0
}
func failUI(err error) int {
	if err != nil {
		return fail(err)
	}
	return 0
}
func runGroupRename(args []string) int {
	fs, addr := newFlags("rename", "group name", "Rename a group without changing its id or terminals.")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	g, err := c.FindGroup(context.Background(), fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	name := fs.Arg(1)
	var out terminal.Group
	if err = c.Do("PATCH", "/groups/"+string(g.ID), terminal.GroupUpdate{Name: &name}, &out); err != nil {
		return fail(err)
	}
	fmt.Println(out.Name)
	return 0
}
func runGroupArchive(args []string) int {
	fs, addr := newFlags("archive", "[--undo] group", "Archive finished work. Running terminals must stop first.")
	undo := fs.Bool("undo", false, "Return an archived group to the group list")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	g, err := c.FindGroup(context.Background(), fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	value := !*undo
	return failUI(c.Do("PATCH", "/groups/"+string(g.ID), terminal.GroupUpdate{Archived: &value}, nil))
}
func runGroupRemove(args []string) int {
	fs, addr := newFlags("remove", "[--force] group", "Remove an empty group. --force stops and removes every terminal in it.")
	force := fs.Bool("force", false, "Stop all member terminals and remove their saved state")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	g, err := c.FindGroup(context.Background(), fs.Arg(0))
	if err != nil {
		return fail(err)
	}
	path := "/groups/" + string(g.ID)
	if *force {
		path += "?force=true"
	}
	return failUI(c.Do("DELETE", path, nil, nil))
}
func runTerm(args []string) int {
	commands := map[string]func([]string) int{"new": runNew, "ls": runLs, "attach": runAttach, "kill": runKill, "restore": runRestore, "import": runImport, "rename": runTermRename, "move": runTermMove}
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: ptyd term new|ls|attach|rename|move|restore|kill|import")
		return 2
	}
	if command, ok := commands[args[0]]; ok {
		return command(args[1:])
	}
	return fail(fmt.Errorf("unknown terminal command %q", args[0]))
}
func runTermRename(args []string) int { return updateTerm(args, false) }
func runTermMove(args []string) int   { return updateTerm(args, true) }
func updateTerm(args []string, move bool) int {
	name, usage := "term rename", "terminal name"
	if move {
		name, usage = "term move", "terminal group"
	}
	fs, addr := newFlags(name, usage, "Change terminal metadata without restarting its process.")
	group := fs.String("group", "", "Resolve the terminal name in this group")
	_ = fs.Parse(args)
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	t, err := findTerminal(c, fs.Arg(0), *group)
	if err != nil {
		return fail(err)
	}
	u := terminal.TerminalUpdate{}
	value := fs.Arg(1)
	if move {
		g, err := c.FindGroup(context.Background(), value)
		if err != nil {
			return fail(err)
		}
		u.GroupID = &g.ID
	} else {
		u.Name = &value
	}
	return failUI(c.Do("PATCH", "/terminals/"+string(t.ID), u, nil))
}

// Terminal commands scope names to inherited group context; full ids remain global.
func findTerminal(c client.API, target, group string) (terminal.Info, error) {
	list, err := c.List()
	if err != nil {
		return terminal.Info{}, err
	}
	for _, t := range list {
		if string(t.ID) == target {
			return t, nil
		}
	}
	g, err := groupContext(c, group)
	if err != nil {
		return terminal.Info{}, err
	}
	var rows []terminal.Info
	for _, t := range list {
		if t.GroupID == g.ID {
			rows = append(rows, t)
		}
	}
	return client.Find(rows, target)
}
