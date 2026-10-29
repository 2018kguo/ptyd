package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func runSearch(args []string) int {
	fs, addr := newFlags("search", "[--group group] [--all] [--json] text", "Search current terminal screens and retained scrollback, without attaching. Text is literal and case-insensitive.")
	group := fs.String("group", "", "Search only this group")
	all := fs.Bool("all", false, "Include finished and saved terminals")
	asJSON := fs.Bool("json", false, "Print matches and coverage as JSON")
	_ = fs.Parse(args)
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		fs.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c := apiAt(*addr)
	options := terminal.OverviewOptions{Limit: terminal.MaxOverviewLimit, ActiveOnly: !*all}
	if *group != "" {
		g, err := c.FindGroup(ctx, *group)
		if err != nil {
			return fail(err)
		}
		options.GroupID = g.ID
	}
	overview, err := c.Overview(ctx, options)
	if err != nil {
		return fail(err)
	}
	result := c.SearchContents(ctx, overview, query)
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			return fail(err)
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "TERMINAL\tNAME\tMATCH IN\tTEXT")
		for _, hit := range result.Matches {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", hit.TerminalID, displayText(hit.Name, 50), hit.Source, displayText(hit.Text, 160))
		}
		w.Flush()
		if !result.Complete {
			fmt.Fprintf(os.Stderr, "Search incomplete: read %d of %d terminals. Use --group to narrow the search; unavailable screens are listed in --json.\n", result.Checked, result.Candidates)
		}
	}
	if !result.Complete {
		return 1
	}
	return 0
}
