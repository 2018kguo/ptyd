package client

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type ContentMatch struct {
	TerminalID terminal.ID      `json:"terminal_id"`
	GroupID    terminal.GroupID `json:"group_id"`
	Name       string           `json:"name"`
	Source     string           `json:"source"` // screen, scrollback or session metadata
	Text       string           `json:"text"`
	ScreenRow  int              `json:"screen_row"` // -1 unless the match is on the visible screen
}

type ContentSearchIssue struct {
	TerminalID terminal.ID `json:"terminal_id"`
	Message    string      `json:"message"`
}

type ContentSearch struct {
	Matches    []ContentMatch       `json:"matches"`
	Issues     []ContentSearchIssue `json:"issues"`
	Checked    int                  `json:"checked"`
	Candidates int                  `json:"candidates"`
	Complete   bool                 `json:"complete"`
}

// SearchContents reads the same exact snapshots used by live panes. Four workers
// bound transient models and HTTP requests; models are released after matching.
// It never attaches, resizes, claims input or reads another program's files.
func (c API) SearchContents(ctx context.Context, overview terminal.Overview, query string) ContentSearch {
	needle := strings.ToLower(strings.TrimSpace(query))
	out := ContentSearch{Matches: []ContentMatch{}, Issues: []ContentSearchIssue{}, Candidates: max(overview.Counts.Total, len(overview.Sessions))}
	type result struct {
		match   *ContentMatch
		issue   string
		checked bool
	}
	results := make([]result, len(overview.Sessions))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(4, len(results)) {
		wg.Go(func() {
			for {
				i := int(next.Add(1)) - 1
				if i >= len(results) {
					return
				}
				row := overview.Sessions[i]
				r := &results[i]
				if ctx.Err() != nil {
					r.issue = ctx.Err().Error()
					continue
				}
				if row.External != nil || row.Local != nil {
					r.issue = "Original terminal contents are unavailable; import its conversation to search the managed screen."
					r.match = matchContentMetadata(row, needle)
					continue
				}
				view, err := c.Preview(ctx, row.ID)
				if err != nil {
					r.issue = err.Error()
					r.match = matchContentMetadata(row, needle)
					continue
				}
				r.checked = true
				r.match, err = MatchContents(row, view, needle)
				if err != nil {
					r.issue = err.Error()
				}
			}
		})
	}
	wg.Wait()
	if overview.ProcessIssue != "" {
		out.Issues = append(out.Issues, ContentSearchIssue{Message: overview.ProcessIssue})
	}
	for _, issue := range overview.DiscoveryIssues {
		out.Issues = append(out.Issues, ContentSearchIssue{Message: string(issue.Provider) + ": " + issue.Message})
	}
	for i, r := range results {
		if r.match != nil {
			out.Matches = append(out.Matches, *r.match)
		}
		if r.issue != "" {
			out.Issues = append(out.Issues, ContentSearchIssue{overview.Sessions[i].ID, r.issue})
		}
		if r.checked {
			out.Checked++
		}
	}
	out.Complete = !overview.Truncated && !overview.DiscoveryTruncated && len(out.Issues) == 0 && ctx.Err() == nil
	return out
}

// MatchContents prefers a visible hit, then the most recent retained logical
// history line. Session fields remain searchable as a secondary convenience.
func MatchContents(row terminal.SessionSummary, view *phosphor.Terminal, query string) (*ContentMatch, error) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil, nil
	}
	boundary := ""
	cols, height := view.Size()
	history := view.ScrollbackLen()
	for end := height; end > 0; {
		start := end - 1
		for start > 0 && view.Wrapped(start) {
			start--
		}
		var line strings.Builder
		for y := start; y < end; y++ {
			if y+1 == end {
				line.WriteString(view.Line(y))
			} else {
				last := cols
				for last > 0 && emptyContentCell(view.Cell(last-1, y)) {
					last--
				}
				for x := 0; x < last; x++ {
					appendContentCell(&line, view.Cell(x, y))
				}
			}
		}
		text := line.String()
		if strings.Contains(strings.ToLower(text), needle) {
			return contentMatch(row, "screen", text, needle, start), nil
		}
		if start == 0 && view.Wrapped(0) && !view.Modes().AltScreen && history > 0 {
			boundary = text
		}
		end = start
	}
	for i := history - 1; i >= 0; i-- {
		text := view.ScrollbackLine(i)
		if strings.Contains(strings.ToLower(text), needle) {
			return contentMatch(row, "scrollback", text, needle, -1), nil
		}
	}
	if hit := matchContentMetadata(row, needle); hit != nil {
		return hit, nil
	}
	if title := view.Title(); strings.Contains(strings.ToLower(title), needle) {
		return contentMatch(row, "session", title, needle, -1), nil
	}
	if boundary == "" || view.ScrollbackLine(history-1) == "" {
		return nil, nil
	}
	// Public history cells are much larger than serialized text. Bound both
	// padded geometry and serialized input before reconstructing a boundary.
	// The whole checkpoint bound is conservative: other history can make this
	// one boundary partial. Plain history/metadata hits above remain usable.
	if cols > 4096 || len(view.ScrollbackLine(history-1)) > 32<<10 || len(view.Snapshot()) > 128<<10 {
		return nil, fmt.Errorf("screen/history wrap boundary exceeds the search memory budget")
	}
	var joined strings.Builder
	for _, cells := range view.ScrollbackCells(history - 1) {
		last := len(cells)
		for last > 0 && emptyContentCell(cells[last-1]) {
			last--
		}
		for _, cell := range cells[:last] {
			appendContentCell(&joined, cell)
		}
	}
	joined.WriteString(boundary)
	text := joined.String()
	if strings.Contains(strings.ToLower(text), needle) {
		return contentMatch(row, "scrollback", text, needle, -1), nil
	}
	return nil, nil
}

// An empty trailing cell is display padding, including the spacer before a
// wrapping wide character. Literal spaces have Rune ' ' and remain text.
func emptyContentCell(cell phosphor.Cell) bool { return cell.Rune == 0 && cell.Width != 0 }

func appendContentCell(b *strings.Builder, cell phosphor.Cell) {
	if cell.Width == 0 {
		return
	}
	if cell.Rune == 0 {
		b.WriteByte(' ')
	} else {
		b.WriteRune(cell.Rune)
		b.WriteString(cell.Combining)
	}
}
func matchContentMetadata(row terminal.SessionSummary, needle string) *ContentMatch {
	if needle == "" {
		return nil
	}
	for _, text := range []string{row.Name, row.CWD, row.Command, row.Agent, string(row.ID)} {
		if strings.Contains(strings.ToLower(text), needle) {
			return contentMatch(row, "session", text, needle, -1)
		}
	}
	return nil
}
func contentMatch(row terminal.SessionSummary, source, text, needle string, screenRow int) *ContentMatch {
	lower := strings.ToLower(text)
	index := strings.Index(lower, needle)
	at := 0
	if index >= 0 {
		at = utf8.RuneCountInString(lower[:index])
	}
	runes := []rune(text)
	start := max(at-24, 0)
	end := min(start+160, len(runes))
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(runes) {
		snippet += "…"
	}
	return &ContentMatch{TerminalID: row.ID, GroupID: row.GroupID, Name: row.Name, Source: source, Text: snippet, ScreenRow: screenRow}
}
