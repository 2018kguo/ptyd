package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

// Captain is a local workspace designation, shared by the UI and CLI on this
// machine. The terminal remains an ordinary session with the same controls.
type captainSelection struct {
	GroupID    terminal.GroupID `json:"group_id"`
	TerminalID terminal.ID      `json:"terminal_id"`
	Name       string           `json:"name,omitempty"`
	State      terminal.Status  `json:"state,omitempty"`
	Available  bool             `json:"available"`
	Reason     string           `json:"reason,omitempty"`
}

func chooseCaptain(ctx context.Context, c client.API, group terminal.GroupID, id terminal.ID) error {
	if id != "" {
		var info terminal.Info
		if err := c.DoContext(ctx, "GET", "/terminals/"+string(id), nil, &info); err != nil {
			return err
		}
		if info.GroupID != group {
			return fmt.Errorf("choose a terminal in this group; %s belongs to another group", id)
		}
	}
	return loadWorkspaceLayouts(c.Addr).saveCaptain(group, id)
}

func readCaptain(ctx context.Context, c client.API, group terminal.GroupID) (captainSelection, error) {
	layout := loadWorkspaceLayouts(c.Addr)
	result := captainSelection{GroupID: group, TerminalID: layout.captains[group]}
	if layout.readErr != nil {
		return result, layout.readErr
	}
	if result.TerminalID == "" {
		result.Reason = "No captain designated."
		return result, nil
	}
	var info terminal.Info
	err := c.DoContext(ctx, "GET", "/terminals/"+string(result.TerminalID), nil, &info)
	if err != nil {
		var response *client.HTTPError
		if errors.As(err, &response) && response.Status == 404 {
			result.Reason = "The captain was removed. Designate another terminal."
			return result, nil
		}
		return result, err
	}
	if info.GroupID != group {
		result.Reason = "The captain moved to another group. Designate another terminal."
		return result, nil
	}
	result.Name, result.State, result.Available = info.Name, info.Status, true
	return result, nil
}

func runCaptain(args []string) int {
	fs, addr := newFlags("captain", "[--group group] [--json] [--clear | terminal]", "Read or designate the group's captain. This is a saved workspace choice on this machine.\nInside ptyd, defaults to the current group; otherwise Inbox.")
	group := fs.String("group", "", "Group name or id")
	clear := fs.Bool("clear", false, "Remove the captain designation")
	asJSON := fs.Bool("json", false, "Print the designation and current availability as JSON")
	_ = fs.Parse(args)
	if fs.NArg() > 1 || (*clear && fs.NArg() != 0) {
		fs.Usage()
		return 2
	}
	c := apiAt(*addr)
	g, err := groupContext(c, *group)
	if err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if *clear || fs.NArg() == 1 {
		var id terminal.ID
		if !*clear {
			info, err := findTerminal(c, fs.Arg(0), string(g.ID))
			if err != nil {
				return fail(err)
			}
			id = info.ID
		}
		if err = chooseCaptain(ctx, c, g.ID, id); err != nil {
			return fail(err)
		}
	}
	result, err := readCaptain(ctx, c, g.ID)
	if err != nil {
		return fail(err)
	}
	if *asJSON {
		return failUI(json.NewEncoder(os.Stdout).Encode(result))
	}
	if result.Available {
		fmt.Printf("★ Captain · %s · %s · %s\n", displayText(result.Name, 80), result.TerminalID, result.State)
	} else {
		fmt.Println(result.Reason)
	}
	return 0
}

func (v workspaceView) isCaptain(row terminal.SessionSummary) bool {
	return row.ID != "" && v.captains[row.GroupID] == row.ID && row.External == nil && row.Local == nil
}
