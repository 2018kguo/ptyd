package main

import (
	"context"
	"fmt"
	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
)

func runUI(args []string) int { return runGroupAttach(args) }
func runStatus(args []string) int {
	fs, addr := newFlags("status", "[--json] [--group name] [--discover] [--limit N] [--filter text] [--tag tags]", "Read a terminal overview. Use ptyd or ptyd attach for a live workspace.")
	asJSON := fs.Bool("json", false, "Return structured terminal state")
	discover := fs.Bool("discover", false, "Include original agent conversations and terminal processes")
	limit := fs.Int("limit", terminal.DefaultOverviewLimit, "Maximum rows; counts cover every match")
	filter := fs.String("filter", "", "Match terminal names, ids, commands and directories")
	tags := fs.String("tag", "", "Match any comma-separated tag")
	group := fs.String("group", "", "Limit to a group name or id")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *limit < 1 || *limit > terminal.MaxOverviewLimit {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	options := terminal.OverviewOptions{Limit: *limit, Query: *filter, Discover: *discover}
	if *group != "" {
		g, err := c.FindGroup(context.Background(), *group)
		if err != nil {
			return fail(err)
		}
		options.GroupID = g.ID
	}
	for _, t := range strings.Split(*tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			options.Tags = append(options.Tags, t)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	view, err := c.Overview(ctx, options)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return encodeJSON(os.Stdout, view)
	}
	printStatus(os.Stdout, view)
	return 0
}
func overviewState(row terminal.SessionSummary) string {
	if row.External != nil {
		return "outside: " + displayText(row.External.Status, 24)
	}
	if row.State == terminal.OverviewRunning {
		return "running"
	}
	if row.State == terminal.OverviewStopped {
		return "stopped"
	}
	if row.ExitCode == nil {
		return string(row.State)
	}
	if *row.ExitCode < 0 {
		return "killed"
	}
	return fmt.Sprintf("exited %d", *row.ExitCode)
}
func overviewCounts(view terminal.Overview) string {
	c := view.Counts
	text := fmt.Sprintf("%d sessions | %d attention | %d running | %d stopped | %d exited", c.Total, c.Attention, c.Running, c.Stopped, c.Exited)
	if c.External > 0 {
		text += fmt.Sprintf(" | %d outside ptyd", c.External)
	}
	if view.DiscoveryTruncated {
		text += " | native discovery truncated"
	}
	if len(view.DiscoveryIssues) > 0 {
		text += " | native discovery incomplete"
	}
	if view.Truncated {
		text += fmt.Sprintf(" | showing %d", len(view.Sessions))
	}
	return text
}
func overviewIdle(row terminal.SessionSummary) string {
	if row.IdleSeconds == nil || row.State != terminal.OverviewRunning {
		return "-"
	}
	return shortDuration(time.Duration(*row.IdleSeconds * float64(time.Second)))
}
func printStatus(w io.Writer, view terminal.Overview) {
	fmt.Fprintln(w, overviewCounts(view))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tSTATE\tIDLE\tATTENTION\tATTACHED\tDIRECTORY\tPREVIEW")
	for _, row := range view.Sessions {
		preview := ""
		if row.Preview != nil {
			preview = row.Preview.Text
			if row.Preview.Truncated {
				preview += "…"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n", displayText(string(row.ID), 80), displayText(row.Name, 64), overviewState(row), overviewIdle(row), row.Attention, row.Attached, displayText(row.CWD, 60), displayText(preview, 80))
	}
	_ = tw.Flush()
	for _, issue := range view.DiscoveryIssues {
		fmt.Fprintf(w, "%s: %s\n", issue.Provider, displayText(issue.Message, 200))
	}
}

func displayText(text string, columns int) string {
	var b strings.Builder
	width := 0
	for _, r := range // displayText cannot carry terminal controls from a session into the overview.
	text {
		if unicode.IsControl(r) {
			r = ' '
		}
		w := max(phosphor.CodepointWidth(r), 0)
		if width+w > columns {
			break
		}
		b.WriteRune(r)
		width += w
	}
	return b.String()
}
