package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func (c API) Overview(ctx context.Context, options terminal.OverviewOptions) (terminal.Overview, error) {
	q := url.Values{}
	if options.Limit > 0 {
		q.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Query != "" {
		q.Set("q", options.Query)
	}
	if len(options.Tags) > 0 {
		q.Set("tag", strings.Join(options.Tags, ","))
	}
	var view terminal.Overview
	err := c.DoContext(ctx, "GET", "/overview?"+q.Encode(), nil, &view)
	var response *HTTPError
	if errors.As(err, &response) && response.Status == 404 {
		return view, fmt.Errorf("restart updated daemon binary or check PTYD_ADDR: %w (no overview at %s)", err, c.Addr)
	}
	return view, err
}
