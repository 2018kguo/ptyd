package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

// Preview reads one atomic snapshot without attaching, claiming input or resizing.
func (c API) Preview(ctx context.Context, id terminal.ID) (*phosphor.Terminal, error) {
	raw, err := c.readContext(ctx, "GET", "/terminals/"+url.PathEscape(string(id))+"/screen?format=snapshot", nil, 4<<20)
	if err != nil {
		return nil, fmt.Errorf("cannot read preview; ensure the daemon is updated: %w", err)
	}
	view := phosphor.New(1, 1, phosphor.WithScrollback(0))
	if err = view.RestoreSnapshot(raw, phosphor.SnapshotLimits{MaxBytes: 4 << 20, MaxCells: 256 << 10}); err != nil {
		return nil, fmt.Errorf("preview unavailable or too large; open the terminal to view it: %w", err)
	}
	return view, nil
}

func (c API) TerminalTitle(ctx context.Context, id terminal.ID) (string, error) {
	raw, err := c.readContext(ctx, "GET", "/terminals/"+url.PathEscape(string(id))+"/screen", nil, 1<<20)
	if err != nil {
		return "", err
	}
	var screen struct {
		Title string `json:"title"`
	}
	err = json.Unmarshal(raw, &screen)
	return screen.Title, err
}
