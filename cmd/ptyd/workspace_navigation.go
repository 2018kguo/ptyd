package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

const maxWorkspacePins = 3

type workspaceHomeState struct {
	query   string
	history bool
}
type workspacePinnedPreview struct {
	Row     terminal.SessionSummary
	Screen  *phosphor.Terminal
	Err     error
	Removed bool
}

func (v workspaceView) watched() []terminal.ID {
	if v.group == "" {
		return nil
	}
	return v.pins[v.group]
}
func (v *workspaceView) unpin(id terminal.ID) {
	ids := v.pins[v.group]
	for i, item := range ids {
		if item == id {
			v.pins[v.group] = slices.Delete(ids, i, i+1)
			return
		}
	}
}
func (v *workspaceView) togglePin(id terminal.ID) {
	if slices.Contains(v.watched(), id) {
		v.unpin(id)
		v.notice = "Pane unpinned."
		return
	}
	if len(v.watched()) >= maxWorkspacePins {
		v.notice = "Three panes are pinned. Press v on one to unpin it."
		return
	}
	if v.pins == nil {
		v.pins = map[terminal.GroupID][]terminal.ID{}
	}
	v.pins[v.group] = append(v.pins[v.group], id)
	v.notice = "Pane pinned."
}
func (v *workspaceView) enterGroup(id terminal.GroupID) {
	if v.group == "" {
		v.home = workspaceHomeState{v.query, v.history}
	}
	v.group = id
	v.rows = nil
	v.snapshot = nil
	v.selected = ""
	v.query = ""
	v.history = true
	v.outside = false
}
func (v *workspaceView) toGroups() {
	previous := v.group
	v.group = ""
	v.rows = nil
	v.snapshot = nil
	v.query = v.home.query
	v.history = v.home.history
	v.selected = string(previous)
	v.outside = false
	for _, g := range v.groups {
		if g.ID == previous && (g.Archived || (g.ID == terminal.DefaultGroup && g.Running == 0)) {
			v.history = true
		}
	}
}

// back dismisses one overlay, then the group, then the browser itself.
func (v *workspaceView) back() bool {
	if v.mode != wsNormal {
		if v.mode == wsSearch {
			v.query = v.searchBefore
		}
		v.mode = wsNormal
		v.text = ""
		v.target = ""
		return false
	}
	if v.group != "" {
		v.toGroups()
		return false
	}
	return true
}

func (v *workspaceView) showEnded(info terminal.Info) {
	// Keep the completed terminal selected and actionable, even if the old
	// view hid finished records or filtered out a newly launched agent.
	if info.GroupID != "" && info.GroupID != v.group {
		v.enterGroup(info.GroupID)
	}
	v.history, v.query = true, ""
	v.selected = string(info.ID)
	row := terminal.BuildOverview([]terminal.Info{info}, terminal.OverviewOptions{Limit: 1}).Sessions[0]
	found := false
	for i := range v.rows {
		if v.rows[i].ID == info.ID {
			v.rows[i] = row
			found = true
			break
		}
	}
	if !found {
		v.rows = append(v.rows, row)
	}
	state := "finished"
	if info.ExitCode == nil {
		state = "stopped"
	} else if *info.ExitCode != 0 {
		state = fmt.Sprintf("exited (code %d)", *info.ExitCode)
	}
	v.notice = fmt.Sprintf("%s %s. R relaunches · Enter opens history.", sessionName(row), state)
}

func fetchWorkspaceScreens(ctx context.Context, c client.API, s *workspaceSnapshot) {
	var wg sync.WaitGroup
	if s.PreviewID != "" {
		wg.Go(func() { s.Preview, s.PreviewErr = c.Preview(ctx, terminal.ID(s.PreviewID)) })
	}
	ids := s.Query.Watches
	s.Pinned = make([]workspacePinnedPreview, 0, len(ids))
	for _, id := range ids {
		if string(id) == s.PreviewID {
			continue
		}
		s.Pinned = append(s.Pinned, workspacePinnedPreview{Row: terminal.SessionSummary{ID: id}})
	}
	for i := range s.Pinned {
		wg.Go(func() {
			p := &s.Pinned[i]
			var info terminal.Info
			if p.Err = c.DoContext(ctx, "GET", "/terminals/"+string(p.Row.ID), nil, &info); p.Err != nil {
				var response *client.HTTPError
				p.Removed = errors.As(p.Err, &response) && response.Status == 404
				return
			}
			if info.GroupID != s.Query.Group {
				p.Removed = true
				return
			}
			p.Row = terminal.BuildOverview([]terminal.Info{info}, terminal.OverviewOptions{Limit: 1}).Sessions[0]
			p.Screen, p.Err = c.Preview(ctx, p.Row.ID)
		})
	}
	wg.Wait()
}

func (v workspaceView) watchPanes() []workspacePinnedPreview {
	if v.snapshot == nil || v.snapshot.Query.Group != v.group {
		return nil
	}
	var out []workspacePinnedPreview
	add := func(id terminal.ID) {
		for _, p := range out {
			if p.Row.ID == id {
				return
			}
		}
		p := workspacePinnedPreview{Row: terminal.SessionSummary{ID: id, Name: string(id)}}
		for _, r := range v.rows {
			if r.ID == id {
				p.Row = r
			}
		}
		if v.snapshot.PreviewID == string(id) {
			p.Screen, p.Err = v.snapshot.Preview, v.snapshot.PreviewErr
		} else {
			for _, saved := range v.snapshot.Pinned {
				if saved.Row.ID == id {
					p = saved
					break
				}
			}
		}
		if !p.Removed {
			out = append(out, p)
		}
	}
	for _, id := range v.watched() {
		add(id)
	}
	if r, ok := v.row(); ok && r.External == nil && r.Local == nil {
		add(r.ID)
	}
	return out
}
func workspacePaneLabel(p workspacePinnedPreview, pinned, selected bool) string {
	icon := " "
	if pinned {
		icon = "◆"
	}
	if selected {
		icon = "›"
		if pinned {
			icon = "◆›"
		}
	}
	return fmt.Sprintf("%s %s · %s", icon, sessionName(p.Row), workspaceTerminalIcon(p.Row)+" "+workspaceTerminalState(p.Row))
}
