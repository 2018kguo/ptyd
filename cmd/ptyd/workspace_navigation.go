package main

import (
	"fmt"
	"slices"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

const maxWorkspacePins = 7

type workspaceHomeState struct {
	query   string
	history bool
}
type workspacePane struct {
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
		v.notice = "Seven panes are pinned. Press v on one to unpin it."
		return
	}
	if v.pins == nil {
		v.pins = map[terminal.GroupID][]terminal.ID{}
	}
	v.pins[v.group] = append(v.pins[v.group], id)
	v.notice = "Pane pinned."
}
func (v *workspaceView) enterGroup(id terminal.GroupID) {
	query := ""
	focus := v.previewFocus
	selected := ""
	if focus != "" && v.snapshot != nil {
		for _, p := range v.snapshot.Panes {
			if p.Row.ID == focus && p.Row.GroupID == id {
				selected = string(focus)
				break
			}
		}
	}
	if v.group == "" {
		v.home = workspaceHomeState{v.query, v.history}
		if v.snapshot != nil && v.snapshot.Search != nil {
			for _, hit := range v.snapshot.Search.Matches {
				if hit.GroupID == id {
					query = v.query
					break
				}
			}
		}
	}
	v.group = id
	v.rows = nil
	v.snapshot = nil
	v.selected = selected
	v.previewFocus = ""
	v.query = query
	v.history = true
	v.outside = false
}
func (v *workspaceView) toGroups() {
	previous := v.group
	v.previewFocus = terminal.ID(v.selected)
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

func workspacePaneLabel(p workspacePane, pinned, selected bool, name string) string {
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
	return fmt.Sprintf("%s %s · %s", icon, name, workspaceTerminalIcon(p.Row)+" "+workspaceTerminalState(p.Row))
}
