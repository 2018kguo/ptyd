package api

import (
	"net/http"
	"strconv"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := terminal.DefaultOverviewLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > terminal.MaxOverviewLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and %d", terminal.MaxOverviewLimit)
			return
		}
		limit = n
	}
	tags := splitNonEmpty(q.Get("tag"), ",")
	options := terminal.OverviewOptions{GroupID: terminal.GroupID(q.Get("group")), Limit: limit, Query: q.Get("q"), Tags: tags}
	if raw := q.Get("active"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "active must be true or false")
			return
		}
		options.ActiveOnly = enabled
	}
	if options.GroupID != "" {
		if _, err := s.mgr.Group(options.GroupID); err != nil {
			writeErr(w, err)
			return
		}
	}
	view := s.mgr.Overview(options)
	if raw := q.Get("discover"); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "discover must be true or false")
			return
		}
		if enabled {
			view = terminal.BuildDiscoveredOverview(s.mgr.List(), s.native.List(r.Context()), options)
		}
	}
	writeJSON(w, http.StatusOK, view)
}
