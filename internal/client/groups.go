package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func (c API) Groups(ctx context.Context) ([]terminal.GroupSummary, error) {
	var out []terminal.GroupSummary
	err := c.DoContext(ctx, "GET", "/groups", nil, &out)
	var status *HTTPError
	if errors.As(err, &status) && status.Status == http.StatusNotFound {
		err = fmt.Errorf("the running daemon does not support groups; update and restart it after saving running work")
	}
	return out, err
}
func (c API) FindGroup(ctx context.Context, target string) (terminal.Group, error) {
	rows, err := c.Groups(ctx)
	if err != nil {
		return terminal.Group{}, err
	}
	for _, r := range rows {
		if string(r.ID) == target {
			return r.Group, nil
		}
	}
	for _, r := range rows {
		if strings.EqualFold(r.Name, target) {
			return r.Group, nil
		}
	}
	var matches []terminal.Group
	for _, r := range rows {
		if strings.HasPrefix(string(r.ID), target) {
			matches = append(matches, r.Group)
		}
	}
	if target != "" && len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return terminal.Group{}, fmt.Errorf("%q matches several groups; use the full name or id", target)
	}
	return terminal.Group{}, fmt.Errorf("group %q was not found; run ptyd ls or create it with ptyd new", target)
}
