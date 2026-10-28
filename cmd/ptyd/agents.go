package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
	"golang.org/x/term"
)

func runAgents(args []string) int {
	fs, addr := newFlags("agents", "[--json]", "Discover existing Codex and Claude sessions without starting workers.\nUse ptyd ui for the combined picker.")
	asJSON := fs.Bool("json", false, "Print native sessions, import actions and discovery failures as JSON")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	listing, err := apiAt(*addr).Agents(ctx)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if err = json.NewEncoder(os.Stdout).Encode(listing); err != nil {
			return fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tNAME\tSTATE\tACTION\tDIRECTORY")
	for _, s := range listing.Sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Ref.Key(), displayText(s.Name, 50), displayText(s.Status, 24), s.Action, displayText(s.CWD, 60))
	}
	_ = tw.Flush()
	for _, issue := range listing.Issues {
		fmt.Fprintf(os.Stderr, "%s: %s\n", issue.Provider, displayText(issue.Message, 300))
	}
	if listing.Truncated {
		fmt.Fprintln(os.Stderr, "Native discovery was truncated; some sessions are omitted.")
	}
	return 0
}
func runImport(args []string) int {
	fs, addr := newFlags("import", "[-d] [-n name] [--resume] <codex:UUID|claude:UUID>", "Open a ptyd frontend for an exact existing agent conversation.\nClaude foreground sessions must first run /bg in their original tab.\nClosing this frontend does not stop its native daemon's worker.")
	detached := fs.Bool("d", false, "Print the terminal id without attaching")
	name := fs.String("n", "", "Name for the terminal")
	resume := fs.Bool("resume", false, "Allow restarting a stopped Claude background conversation")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	provider, id, ok := strings.Cut(fs.Arg(0), ":")
	ref := native.Ref{Provider: native.Provider(provider), ID: id}
	if !ok {
		return fail(fmt.Errorf("use the exact provider:UUID printed by ptyd agents"))
	}
	if err := ref.Validate(); err != nil {
		return fail(err)
	}
	request := terminal.ImportRequest{Ref: ref, Name: *name, Resume: *resume, TerminalEnv: client.TerminalEnvironment()}
	if cols, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		request.Cols = uint16(cols)
		request.Rows = uint16(rows)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	c := apiAt(*addr)
	imported, err := c.ImportAgent(ctx, request)
	if err != nil {
		return fail(err)
	}
	if *detached || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Println(imported.Terminal.ID)
		return 0
	}
	code, _ := attach(context.Background(), c, imported.Terminal, client.Options{})
	return code
}
