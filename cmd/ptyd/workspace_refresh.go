package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

// One refresh at a time; a new navigation/search request cancels obsolete reads.
func refreshWorkspace(ctx context.Context, c client.API, queries <-chan workspaceQuery, updates chan workspaceSnapshot) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	var q workspaceQuery
	select {
	case q = <-queries:
	case <-ctx.Done():
		return
	}
	for ctx.Err() == nil {
		budget := 5 * time.Second
		if q.Outside {
			budget = 12 * time.Second
		}
		req, cancel := context.WithTimeout(ctx, budget)
		done := make(chan workspaceSnapshot, 1)
		go func(query workspaceQuery) { done <- fetchWorkspaceSnapshot(req, c, query) }(q)
		select {
		case next := <-queries:
			cancel()
			<-done
			q = debounceWorkspaceSearch(ctx, queries, q, next)
			continue
		case s := <-done:
			cancel()
			if ctx.Err() != nil {
				return
			}
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- s:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			cancel()
			<-done
			return
		}
		select {
		case next := <-queries:
			q = debounceWorkspaceSearch(ctx, queries, q, next)
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}
func debounceWorkspaceSearch(ctx context.Context, queries <-chan workspaceQuery, old, next workspaceQuery) workspaceQuery {
	if next.Search == old.Search || strings.TrimSpace(next.Search) == "" {
		return next
	}
	timer := time.NewTimer(120 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case q := <-queries:
			if q.Search != next.Search {
				timer.Reset(120 * time.Millisecond)
			}
			next = q
			if strings.TrimSpace(next.Search) == "" {
				return next
			}
		case <-timer.C:
			return next
		case <-ctx.Done():
			return next
		}
	}
}

func fetchWorkspaceSnapshot(ctx context.Context, c client.API, q workspaceQuery) workspaceSnapshot {
	s := workspaceSnapshot{Query: q}
	s.Groups, s.Err = c.Groups(ctx)
	if s.Err != nil {
		return s
	}
	home := q.Group == ""
	group := q.Group
	if home {
		group = terminal.GroupID(q.Selected)
	}
	options := terminal.OverviewOptions{GroupID: group, Limit: terminal.MaxOverviewLimit, Discover: q.Outside, ActiveOnly: !q.History}
	searching := strings.TrimSpace(q.Search) != ""
	if home && searching {
		options.GroupID = ""
	}
	if home && !searching {
		valid := false
		for _, g := range s.Groups {
			if group == g.ID && (q.History || (!g.Archived && (g.ID != terminal.DefaultGroup || g.Running > 0))) {
				valid = true
				break
			}
		}
		if !valid {
			group = ""
		}
		if group == "" {
			for _, g := range s.Groups {
				if q.History || (!g.Archived && (g.ID != terminal.DefaultGroup || g.Running > 0)) {
					group = g.ID
					break
				}
			}
		}
		options.GroupID = group
		options.ActiveOnly = false
	}
	if group == "" && !searching {
		return s
	}
	var overview terminal.Overview
	overview, s.Err = c.Overview(ctx, options)
	if s.Err != nil {
		return s
	}
	if searching {
		scan, stop := context.WithTimeout(ctx, 2*time.Second)
		matches := c.SearchContents(scan, overview, q.Search)
		stop()
		s.Search = &matches
		s.Matches = make(map[terminal.ID]*client.ContentMatch, len(matches.Matches))
		groups := map[terminal.GroupID]bool{}
		for i := range matches.Matches {
			hit := &matches.Matches[i]
			s.Matches[hit.TerminalID] = hit
			groups[hit.GroupID] = true
		}
		if home {
			filtered := s.Groups[:0]
			for _, g := range s.Groups {
				if groups[g.ID] || workspaceGroupMatches(g, q.Search) {
					filtered = append(filtered, g)
				}
			}
			s.Groups = filtered
			valid := false
			for _, g := range s.Groups {
				if g.ID == group {
					valid = true
					break
				}
			}
			if !valid {
				group = ""
				for _, g := range s.Groups {
					if q.History || (!g.Archived && (g.ID != terminal.DefaultGroup || g.Running > 0)) {
						group = g.ID
						break
					}
				}
			}
		}
		var rows []terminal.SessionSummary
		groupMatch := false
		for _, g := range s.Groups {
			if home && g.ID == group && workspaceGroupMatches(g, q.Search) {
				groupMatch = true
			}
		}
		if groupMatch {
			// A group-name match may lie beyond the global search cap. Read its
			// members directly rather than presenting a falsely empty preview.
			members, err := c.Overview(ctx, terminal.OverviewOptions{GroupID: group, Limit: terminal.MaxOverviewLimit, ActiveOnly: !q.History})
			if err != nil {
				s.Err = err
				return s
			}
			overview.Sessions = members.Sessions
		}
		for _, r := range overview.Sessions {
			if (!home || r.GroupID == group) && (s.Matches[r.ID] != nil || groupMatch) {
				rows = append(rows, r)
			}
		}
		overview.Sessions = rows
	}
	if home {
		s.Query.Selected = string(group)
	}
	s.Overview = overview
	if home {
		s.Overview.Sessions = slices.Clone(overview.Sessions)
		slices.SortStableFunc(s.Overview.Sessions, func(a, b terminal.SessionSummary) int {
			priority := func(r terminal.SessionSummary) int {
				if r.Attention > 0 {
					return 0
				}
				if r.State == terminal.OverviewRunning {
					return 1
				}
				return 2
			}
			return priority(a) - priority(b)
		})
	}
	fetchWorkspaceScreens(ctx, c, &s)
	fetchWorkspaceTitles(ctx, c, &s)
	return s
}

// Reuse preview titles, then read at most sixteen nearby default-named agents.
// This is presentation metadata: failed title reads leave the saved name usable.
func fetchWorkspaceTitles(ctx context.Context, c client.API, s *workspaceSnapshot) {
	s.Titles = map[terminal.ID]string{}
	for _, p := range s.Panes {
		if !p.Removed && p.Screen != nil {
			s.Titles[p.Row.ID] = p.Screen.Title()
		}
	}
	selected := 0
	for i, row := range s.Overview.Sessions {
		if string(row.ID) == s.Query.Selected {
			selected = i
			break
		}
	}
	start := max(selected-15, 0)
	var ids []terminal.ID
	for _, row := range s.Overview.Sessions[start:min(start+16, len(s.Overview.Sessions))] {
		if _, read := s.Titles[row.ID]; !read && row.External == nil && row.Local == nil && client.UsesTerminalTitle(row.Name, sessionProvider(row)) {
			ids = append(ids, row.ID)
		}
	}
	budget, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan terminal.ID, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	for range min(4, len(ids)) {
		wg.Go(func() {
			for id := range jobs {
				if title, err := c.TerminalTitle(budget, id); err == nil {
					mu.Lock()
					s.Titles[id] = title
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
}
func workspaceGroupMatches(g terminal.GroupSummary, query string) bool {
	return strings.Contains(strings.ToLower(g.Name+" "+g.CWD), strings.ToLower(strings.TrimSpace(query)))
}

func fetchWorkspaceScreens(ctx context.Context, c client.API, s *workspaceSnapshot) {
	group := s.Query.Group
	if group == "" {
		group = terminal.GroupID(s.Query.Selected)
	}
	add := func(id terminal.ID) {
		for _, p := range s.Panes {
			if p.Row.ID == id {
				return
			}
		}
		row := terminal.SessionSummary{ID: id, Name: string(id), GroupID: group}
		for _, r := range s.Overview.Sessions {
			if r.ID == id {
				row = r
				break
			}
		}
		if row.External == nil && row.Local == nil {
			s.Panes = append(s.Panes, workspacePane{Row: row})
		}
	}
	if s.Query.Group == "" {
		for _, r := range s.Overview.Sessions {
			if len(s.Panes) >= maxWorkspacePins+1 {
				break
			}
			add(r.ID)
		}
		if s.Query.Focus != "" {
			found := false
			for _, p := range s.Panes {
				found = found || p.Row.ID == s.Query.Focus
			}
			if !found {
				for _, r := range s.Overview.Sessions {
					if r.ID == s.Query.Focus && r.External == nil && r.Local == nil {
						if len(s.Panes) == maxWorkspacePins+1 {
							s.Panes = s.Panes[:maxWorkspacePins]
						}
						add(r.ID)
						break
					}
				}
			}
		}
	} else {
		for _, id := range s.Query.Watches {
			add(id)
		}
		selected := false
		for _, r := range s.Overview.Sessions {
			if string(r.ID) == s.Query.Selected {
				add(r.ID)
				selected = true
				break
			}
		}
		if !selected && len(s.Overview.Sessions) > 0 {
			add(s.Overview.Sessions[0].ID)
		}
	}
	var wg sync.WaitGroup
	permits := make(chan struct{}, 4)
	for i := range s.Panes {
		wg.Go(func() {
			p := &s.Panes[i]
			select {
			case permits <- struct{}{}:
				defer func() { <-permits }()
			case <-ctx.Done():
				p.Err = ctx.Err()
				return
			}
			var info terminal.Info
			if p.Err = c.DoContext(ctx, "GET", "/terminals/"+string(p.Row.ID), nil, &info); p.Err != nil {
				var response *client.HTTPError
				p.Removed = errors.As(p.Err, &response) && response.Status == 404
				return
			}
			if info.GroupID != group {
				p.Removed = true
				return
			}
			p.Row = terminal.BuildOverview([]terminal.Info{info}, terminal.OverviewOptions{Limit: 1}).Sessions[0]
			p.Screen, p.Err = c.Preview(ctx, p.Row.ID)
		})
	}
	wg.Wait()
}

func (v workspaceView) contentMatch(id terminal.ID) *client.ContentMatch {
	if v.snapshot == nil {
		return nil
	}
	return v.snapshot.Matches[id]
}
