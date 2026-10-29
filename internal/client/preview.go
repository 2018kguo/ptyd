package client

import (
	"context"
	"fmt"
	"net/url"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

// Preview reads the existing screen exports. It never attaches, claims input,
// or resizes a PTY, and works with daemons that predate the live group browser.
func (c API) Preview(ctx context.Context, id terminal.ID) (*phosphor.Terminal, error) {
	path := "/terminals/" + url.PathEscape(string(id)) + "/screen"
	var size struct{ Cols, Rows int }
	if err := c.DoContext(ctx, "GET", path, nil, &size); err != nil {
		return nil, err
	}
	if size.Cols < 1 || size.Rows < 1 || size.Cols > 4096 || size.Rows > 4096 || int64(size.Cols)*int64(size.Rows) > 256<<10 {
		return nil, fmt.Errorf("screen is too large for a preview; open the terminal to view it")
	}
	raw, err := c.readContext(ctx, "GET", path+"?format=ansi", nil, 4<<20)
	if err != nil {
		return nil, err
	}
	view := phosphor.New(size.Cols, size.Rows, phosphor.WithScrollback(0))
	view.Write(raw)
	return view, nil
}
