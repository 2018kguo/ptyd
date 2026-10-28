package client

import (
	"context"

	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func (c API) Agents(ctx context.Context) (native.Listing, error) {
	var out native.Listing
	err := c.DoContext(ctx, "GET", "/agents", nil, &out)
	return out, err
}
func (c API) ImportAgent(ctx context.Context, request terminal.ImportRequest) (terminal.ImportResult, error) {
	var out terminal.ImportResult
	err := c.DoContext(ctx, "POST", "/agents/import", request, &out)
	return out, err
}
