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
	view := s.mgr.Overview(terminal.OverviewOptions{Limit: limit, Query: q.Get("q"), Tags: tags})
	writeJSON(w, http.StatusOK, view)
}
