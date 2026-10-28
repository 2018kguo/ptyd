package main

import (
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
	"github.com/2018kguo/ptyd/internal/terminal"
	"golang.org/x/term"
)

func runStatus(args []string) int {
	fs, addr := newFlags("status", "[--json | --watch] [--limit N] [--filter text] [--tag tags]",
		"See attention, running and stopped sessions. --json returns a compact snapshot.\n"+
			"--watch opens a live selector: arrows or j/k, Enter to attach, / to filter, q to quit.")
	asJSON := fs.Bool("json", false, "Print structured JSON for agents")
	watch := fs.Bool("watch", false, "Open the live terminal overview")
	limit := fs.Int("limit", terminal.DefaultOverviewLimit, "Maximum rows (counts include every match)")
	query := fs.String("filter", "", "Match id, name, directory, command or agent")
	tags := fs.String("tag", "", "Match any of these comma-separated tags")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (*asJSON && *watch) || *limit < 1 || *limit > terminal.MaxOverviewLimit {
		fs.Usage()
		return 2
	}
	options := terminal.OverviewOptions{Limit: *limit, Query: *query}
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

const statusEnter = "\x1b[?1049h\x1b[?25l\x1b[?2027l"

type statusView struct {
	snapshot *statusSnapshot
	selected terminal.ID
	query    string
	editing  bool
	err      error
	notice   string
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
	for {
		req, cancel := context.WithTimeout(ctx, 2*time.Second)
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
	} else {
		lines = append(lines, "Attention is recorded watch matches; running does not imply an agent is working.")
	}
	filter := "Filter: " + v.query
	if v.editing {
		filter += " | Enter applies, Esc returns"
	}
	lines = append(lines, filter, "  "+padStatus("ID", 20)+padStatus("NAME", 20)+padStatus("STATE", 12)+" IDLE  ATTN  CLIENTS")
	available := max(rows-10, 1)
	selected := v.selectedIndex()
	start := max(selected-available+1, 0)
	for i := start; i < len(v.sessions()) && i < start+available; i++ {
		row := v.sessions()[i]
		marker := "  "
		if row.ID == v.selected {
			marker = "> "
		}
		line := marker + padStatus(string(row.ID), 20) + padStatus(row.Name, 20) + padStatus(overviewState(row), 12)
		line += padStatus(overviewIdle(row), 6)
		line += fmt.Sprintf("%4d %7d", row.Attention, row.Attached)
		lines = append(lines, line)
	}
	if v.snapshot != nil && v.snapshot.query == v.query && len(v.sessions()) == 0 {
		lines = append(lines, "No matching sessions. Start one with ptyd new.")
	}
	if len(v.sessions()) > 0 {
		row := v.sessions()[selected]
		lines = append(lines, "", "Directory: "+row.CWD, "Command: "+row.Command+" | Agent: "+row.Agent)
		if row.Preview != nil {
			preview := row.Preview.Source + ": " + row.Preview.Text
			if row.Preview.Truncated {
				preview += "…"
			}
			lines = append(lines, preview)
		}
	}
	lines = append(lines, "", "↑/↓ or j/k select | Enter attach | Ctrl-\\ twice returns | / filter | r refresh | q quit")
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
		_, _ = io.WriteString(out, "\x1b[0m\x1b[?25h\x1b[?1049l")
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
	draw()
	for {
		select {
		case <-ctx.Done():
			return nil
		case result := <-updates:
			v.refresh(result)
			draw()
		case <-resize:
			draw()
		case <-escapeTimer.C:
			if len(pending) > 0 && pending[0] == 0x1b {
				pending = nil
				v.editing = false
				draw()
			}
		case bytes, ok := <-input.Bytes:
			if !ok {
				return nil
			}
			pending = append(pending, bytes...)
			for len(pending) > 0 {
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
					pending = pending[consumed:]
					if direction != 0 {
						v.move(direction)
					} else if consumed == 1 {
						v.editing = false
					}
					continue
				}
				pending = pending[n:]
				if v.editing {
					switch r {
					case 0x03:
						return nil
					case '\r', '\n':
						v.editing = false
					case 0x7f, 0x08:
						if v.query != "" {
							_, n := utf8.DecodeLastRuneInString(v.query)
							v.query = v.query[:len(v.query)-n]
							queueStatusQuery(queries, v.query)
						}
					default:
						if !unicode.IsControl(r) && utf8.RuneCountInString(v.query) < 200 {
							v.query += string(r)
							queueStatusQuery(queries, v.query)
						}
					}
					continue
				}
				switch r {
				case 'q', 0x03:
					return nil
				case 'j':
					v.move(1)
				case 'k':
					v.move(-1)
				case '/':
					v.editing = true
				case 'r':
					queueStatusQuery(queries, v.query)
				case '\r', '\n':
					if len(v.sessions()) == 0 || v.snapshot == nil || v.snapshot.query != v.query {
						continue
					}
					row := v.sessions()[v.selectedIndex()]
					initial := append([]byte(nil), pending...)
					initial = append(initial, input.Remainder()...)
					pending = nil
					_ = term.Restore(int(in.Fd()), raw)
					raw = nil
					_, _ = io.WriteString(out, "\x1b[0m\x1b[?25h\x1b[?1049l")
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
	for i := 2; i < len(p); i++ {
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
