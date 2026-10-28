package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
	"golang.org/x/term"
)

func runStatus(args []string) int { return runOverview(args, false) }
func runUI(args []string) int     { return runOverview(args, true) }

func runOverview(args []string, interactive bool) int {
	name := "status"
	if interactive {
		name = "ui"
	}
	fs, addr := newFlags(name, "[--json | --watch] [--discover] [--limit N] [--filter text] [--tag tags]",
		"See attention, running and stopped sessions. --json returns a compact snapshot.\n"+
			"--watch opens a Vim-style picker: j/k, gg/G, i or Enter, /, : commands, ? for help.")
	asJSON := fs.Bool("json", false, "Print structured JSON for agents")
	discover := fs.Bool("discover", interactive, "Include native Codex and Claude sessions started outside ptyd")
	watch := fs.Bool("watch", interactive, "Open the live terminal overview")
	limit := fs.Int("limit", terminal.DefaultOverviewLimit, "Maximum rows (counts include every match)")
	query := fs.String("filter", "", "Match id, name, directory, command or agent")
	tags := fs.String("tag", "", "Match any of these comma-separated tags")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (*asJSON && *watch) || *limit < 1 || *limit > terminal.MaxOverviewLimit {
		fs.Usage()
		return 2
	}
	options := terminal.OverviewOptions{Limit: *limit, Query: *query, Discover: *discover}
	for _, tag := range strings.Split(*tags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			options.Tags = append(options.Tags, tag)
		}
	}
	c := apiAt(*addr)
	if *watch {
		if err := watchStatus(c, options, os.Stdin, os.Stdout); err != nil {
			return fail(err)
		}
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	view, err := c.Overview(ctx, options)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(view); err != nil {
			return fail(err)
		}
	} else {
		printStatus(os.Stdout, view)
	}
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			displayText(string(row.ID), 80), displayText(row.Name, 64), overviewState(row),
			overviewIdle(row), row.Attention, row.Attached,
			displayText(row.CWD, 60), displayText(preview, 80))
	}
	_ = tw.Flush()
	for _, issue := range view.DiscoveryIssues {
		fmt.Fprintf(w, "%s: %s\n", issue.Provider, displayText(issue.Message, 200))
	}
}

// displayText cannot carry terminal controls from a session into the overview.
func displayText(text string, columns int) string {
	var b strings.Builder
	width := 0
	for _, r := range text {
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

func padStatus(text string, columns int) string {
	text = displayText(text, columns)
	width := 0
	for _, r := range text {
		width += max(phosphor.CodepointWidth(r), 0)
	}
	return text + strings.Repeat(" ", max(columns-width, 0))
}

type statusResult struct {
	query string
	view  terminal.Overview
	err   error
}

const statusEnter = "\x1b[?1049h\x1b[?25l\x1b[?2027l\x1b[?2004h"

type statusView struct {
	snapshot  *statusSnapshot
	selected  terminal.ID
	query     string
	mode      statusMode
	command   string
	gotoFirst bool
	err       error
	notice    string
}

type statusSnapshot struct {
	query string
	view  terminal.Overview
}

func (v statusView) sessions() []terminal.SessionSummary {
	if v.snapshot == nil {
		return nil
	}
	return v.snapshot.view.Sessions
}

func (v *statusView) refresh(result statusResult) {
	if result.query != v.query {
		return
	}
	v.err = result.err
	if result.err != nil {
		return
	} // retain the last successful snapshot
	v.snapshot = &statusSnapshot{query: result.query, view: result.view}
	for _, row := range v.sessions() {
		if row.ID == v.selected {
			return
		}
	}
	v.selected = ""
	if len(v.sessions()) > 0 {
		v.selected = v.sessions()[0].ID
	}
}

func (v *statusView) selectedIndex() int {
	for i, row := range v.sessions() {
		if row.ID == v.selected {
			return i
		}
	}
	return 0
}

func (v *statusView) move(delta int) {
	if len(v.sessions()) == 0 {
		return
	}
	i := min(max(v.selectedIndex()+delta, 0), len(v.sessions())-1)
	v.selected = v.sessions()[i].ID
}

// refreshStatus has one request in flight and keeps at most one unread result.
// Navigation and typing never wait for the daemon.
func refreshStatus(ctx context.Context, c client.API, options terminal.OverviewOptions, queries <-chan string, updates chan statusResult) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	requestBudget := 2 * time.Second
	if options.Discover {
		// Cold discovery allows bounded native probes to finish and report
		// partial failure. Input/navigation stay on the independent UI loop.
		requestBudget = 12 * time.Second
	}
	for {
		req, cancel := context.WithTimeout(ctx, requestBudget)
		view, err := c.Overview(req, options)
		cancel()
		if ctx.Err() != nil {
			return
		}
		result := statusResult{query: options.Query, view: view, err: err}
		select {
		case <-updates:
		default:
		}
		select {
		case updates <- result:
		case <-ctx.Done():
			return
		}
		select {
		case <-ctx.Done():
			return
		case query := <-queries:
			options.Query = query
		case <-ticker.C:
			select {
			case query := <-queries:
				options.Query = query
			default:
			}
		}
	}
}

func queueStatusQuery(queries chan string, query string) {
	select {
	case <-queries:
	default:
	}
	queries <- query
}

func statusFrame(v statusView, cols, rows int) string {
	header := "ptyd | Loading sessions…"
	if v.snapshot != nil {
		header = "ptyd | " + overviewCounts(v.snapshot.view)
	}
	lines := []string{header}
	if v.err != nil {
		lines = append(lines, "STALE / unavailable: "+v.err.Error())
	} else if v.snapshot == nil || v.snapshot.query != v.query {
		lines = append(lines, "Loading filter; rows below are from the last successful snapshot.")
	} else if v.notice != "" {
		lines = append(lines, v.notice)
	} else if v.snapshot != nil && len(v.snapshot.view.DiscoveryIssues) > 0 {
		issue := v.snapshot.view.DiscoveryIssues[0]
		lines = append(lines, string(issue.Provider)+": "+issue.Message)
	} else {
		lines = append(lines, "Choose a session. Use i to enter and quick jk to return.")
	}
	filter := "Filter: " + v.query
	switch v.mode {
	case statusSearch:
		filter = "/" + v.query + " | Enter applies, Esc returns"
	case statusCommand:
		filter = ":" + v.command
	default:
		filter = "NORMAL | " + filter
	}
	lines = append(lines, filter, "  "+padStatus("ID", 20)+" "+padStatus("NAME", 20)+" "+padStatus("STATE", 12)+" IDLE  ATTN  CLIENTS")
	available := max(rows-11, 1)
	selected := v.selectedIndex()
	start := max(selected-available+1, 0)
	for i := start; i < len(v.sessions()) && i < start+available; i++ {
		row := v.sessions()[i]
		marker := "  "
		if row.ID == v.selected {
			marker = "> "
		}
		line := marker + padStatus(string(row.ID), 20) + " " + padStatus(row.Name, 20) + " " + padStatus(overviewState(row), 12)
		line += padStatus(overviewIdle(row), 6)
		line += fmt.Sprintf("%4d %7d", row.Attention, row.Attached)
		lines = append(lines, line)
	}
	if v.snapshot != nil && v.snapshot.query == v.query && len(v.sessions()) == 0 {
		lines = append(lines, "No matching sessions. Start one with ptyd new.")
	}
	if len(v.sessions()) > 0 {
		row := v.sessions()[selected]
		detail := "Command: " + row.Command
		if row.Agent != "" {
			detail += " | Agent: " + row.Agent
		}
		lines = append(lines, "", "Directory: "+row.CWD, detail)
		if row.Preview != nil {
			preview := row.Preview.Source + ": " + row.Preview.Text
			if row.Preview.Truncated {
				preview += "…"
			}
			lines = append(lines, preview)
		}
	}
	lines = append(lines, "", "j/k move | i/Enter attach | / search | : command | ? help | q quit", "Attached: jk returns here | gg/G first/last")
	if v.mode == statusHelp {
		lines = []string{"ptyd controls", "", "j / k       Select the next / previous session", "gg / G      Select the first / last session", "i / Enter   Attach to the selected session", "/           Search by name, directory, command or agent", "Esc         Leave search or command entry", "r           Refresh", "", ":b name     Attach to a shown session by exact name or id", ":bn / :bp   Attach to the next / previous shown session", ":ls         Clear search and list sessions", ":q          Leave the picker; sessions keep running", ":help / ?   Show this help", "", "While attached: type j then k within 200 ms to return.", "To type literal jk, pause between letters or use bracketed paste.", "", "q, ? or Esc returns to the picker"}
	}
	footerRows := 2
	if v.mode == statusHelp {
		footerRows = 1
	}
	if rows >= footerRows && len(lines) > rows {
		footer := append([]string(nil), lines[len(lines)-footerRows:]...)
		lines = append(lines[:rows-footerRows], footer...)
	}
	var out strings.Builder
	out.WriteString("\x1b[H\x1b[2J")
	for i, line := range lines {
		if i >= max(rows, 1) {
			break
		}
		if i > 0 {
			out.WriteString("\r\n")
		}
		out.WriteString(displayText(line, max(cols-1, 1)))
	}
	return out.String()
}

func watchStatus(c client.API, options terminal.OverviewOptions, in, out *os.File) error {
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return fmt.Errorf("--watch needs an interactive terminal; use status or status --json")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	raw, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return err
	}
	defer func() {
		if raw != nil {
			_ = term.Restore(int(in.Fd()), raw)
		}
		_, _ = io.WriteString(out, "\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
	}()
	input, err := client.NewInput(ctx, in, nil)
	if err != nil {
		return err
	}
	defer func() { input.Close() }()
	_, _ = io.WriteString(out, statusEnter)
	queries := make(chan string, 1)
	updates := make(chan statusResult, 1)
	go refreshStatus(ctx, c, options, queries, updates)
	v := statusView{query: options.Query}
	var pending []byte
	pasting := false
	escapeExpired := false
	escapeTimer := time.NewTimer(time.Hour)
	escapeTimer.Stop()
	defer escapeTimer.Stop()
	draw := func() {
		cols, rows, err := term.GetSize(int(out.Fd()))
		if err != nil {
			cols, rows = 80, 24
		}
		_, _ = io.WriteString(out, statusFrame(v, cols, rows))
	}
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	showTerminal := func(row terminal.SessionSummary, initial []byte) error {
		_ = term.Restore(int(in.Fd()), raw)
		raw = nil
		_, _ = io.WriteString(out, "\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
		code, unread := attach(ctx, c, terminal.Info{ID: row.ID, Name: row.Name}, false, initial)
		v.notice = ""
		if code != 0 {
			v.notice = fmt.Sprintf("Attachment to %s ended with code %d. Its last window is in local scrollback.", row.ID, code)
		}
		if ctx.Err() != nil {
			return nil
		}
		raw, err = term.MakeRaw(int(in.Fd()))
		if err != nil {
			return err
		}
		nextInput, err := client.NewInput(ctx, in, unread)
		if err != nil {
			return err
		}
		input = nextInput
		_, _ = io.WriteString(out, statusEnter)
		v.selected = row.ID
		queueStatusQuery(queries, v.query)
		return nil
	}
	type importResult struct {
		result terminal.ImportResult
		err    error
	}
	imports := make(chan importResult)
	var importCancel context.CancelFunc
	defer func() {
		if importCancel != nil {
			importCancel()
		}
	}()

	draw()
	for {
		select {
		case imported := <-imports:
			importCancel()
			importCancel = nil
			if imported.err != nil {
				v.notice = imported.err.Error()
				draw()
				continue
			}
			input.Close()
			pending = nil
			if err = client.FlushInput(in); err != nil {
				return err
			}
			row := terminal.SessionSummary{ID: imported.result.Terminal.ID, Name: imported.result.Terminal.Name, Source: imported.result.Terminal.Source}
			if err = showTerminal(row, nil); err != nil {
				return err
			}
			if row.Source.Provider != "" {
				v.selected = terminal.ID("native:" + imported.result.Session.Ref.Key())
			}
			draw()
		case <-ctx.Done():
			return nil
		case result := <-updates:
			v.refresh(result)
			draw()
		case <-resize:
			draw()
		case <-escapeTimer.C:
			if !pasting && len(pending) == 1 && pending[0] == 0x1b {
				escapeExpired = true
				v.cancelPrompt()
				draw()
			}
		case chunk, ok := <-input.Bytes:
			if !ok {
				return nil
			}
			if importCancel != nil {
				for _, b := range chunk {
					if b == 'q' || b == 3 {
						return nil
					}
				}
				continue
			}
			if escapeExpired && len(pending) == 1 && len(chunk) > 0 && chunk[0] != '[' && chunk[0] != 'O' {
				pending = nil
			}
			escapeExpired = false
			pending = append(pending, chunk...)
			for len(pending) > 0 {
				if pasting {
					end := []byte("\x1b[201~")
					if bytes.HasPrefix(pending, end) {
						pasting = false
						pending = pending[len(end):]
						continue
					}
					if bytes.HasPrefix(end, pending) {
						break
					}
					r, n := utf8.DecodeRune(pending)
					if !utf8.FullRune(pending) {
						break
					}
					pending = pending[n:]
					// Paste can edit a prompt, but never execute a command or
					// navigate the normal-mode picker, even if it has newlines.
					if v.mode == statusSearch || v.mode == statusCommand {
						if r == '\r' || r == '\n' || r == '\t' {
							r = ' '
						}
						if !unicode.IsControl(r) {
							before := v.query
							v.key(r)
							if before != v.query {
								queueStatusQuery(queries, v.query)
							}
						}
					}
					continue
				}
				r, n := utf8.DecodeRune(pending)
				if r == utf8.RuneError && !utf8.FullRune(pending) {
					break
				}
				if r == 0x1b {
					consumed, direction := statusEscape(pending)
					if consumed == 0 {
						escapeTimer.Reset(100 * time.Millisecond)
						break
					}
					escapeTimer.Stop()
					pasting = bytes.Equal(pending[:consumed], []byte("\x1b[200~"))
					pending = pending[consumed:]
					if direction != 0 {
						v.move(direction)
					} else if consumed == 1 {
						v.cancelPrompt()
					}
					continue
				}
				pending = pending[n:]
				previousQuery := v.query
				action := v.key(r)
				if previousQuery != v.query {
					queueStatusQuery(queries, v.query)
				}
				switch action {
				case statusQuit:
					return nil
				case statusRefresh:
					queueStatusQuery(queries, v.query)
				case statusAttach:
					if len(v.sessions()) == 0 || v.snapshot == nil || v.snapshot.query != v.query {
						continue
					}
					row := v.sessions()[v.selectedIndex()]
					if row.External != nil {
						if row.External.Action != native.Attach {
							v.notice = row.External.Reason
							if v.notice == "" {
								v.notice = "Use ptyd import --resume " + row.External.Ref.Key() + " to restart this stopped conversation."
							}
							continue
						}
						operation, cancelOperation := context.WithCancel(ctx)
						importCancel = cancelOperation
						request := terminal.ImportRequest{Ref: row.External.Ref}
						v.notice = "Opening native session… q cancels."
						draw()
						go func() {
							result, err := c.ImportAgent(operation, request)
							select {
							case imports <- importResult{result, err}:
							case <-operation.Done():
							}
						}()
						for _, b := range pending {
							if b == 'q' || b == 3 {
								return nil
							}
						}
						pending = nil
						continue
					}

					initial := append([]byte(nil), pending...)
					initial = append(initial, input.Remainder()...)
					pending = nil
					if err = showTerminal(row, initial); err != nil {
						return err
					}
				}
			}
			draw()
		}
	}
}

// statusEscape consumes one CSI/SS3 key sequence, including split writes.
// Unknown keys are ignored as a whole rather than leaking into the filter.
func statusEscape(p []byte) (consumed, direction int) {
	if len(p) < 2 {
		return 0, 0
	}
	if p[1] != '[' && p[1] != 'O' {
		return 1, 0
	}
	if len(p) >= 3 && bytes.Equal(p[:3], []byte("\x1b[M")) {
		n, complete := client.MouseReportLength(p)
		if !complete {
			return 0, 0
		}
		return n, 0
	}
	for i := 2; i < len(p); i++ {
		if i >= 128 {
			return i, 0
		}
		if p[i] >= 0x40 && p[i] <= 0x7e {
			switch p[i] {
			case 'A':
				return i + 1, -1
			case 'B':
				return i + 1, 1
			}
			return i + 1, 0
		}
		if p[i] < 0x20 || p[i] > 0x3f {
			return i + 1, 0
		}
	}
	return 0, 0
}
