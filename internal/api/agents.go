package api

import (
	"errors"
	"net/http"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.native.List(r.Context()))
}
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var request terminal.ImportRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	result, err := s.mgr.ImportAgent(r.Context(), s.native, request)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, terminal.ErrImportDeclined) {
			status = http.StatusConflict
		}
		writeError(w, status, "%v", err)
		return
	}
	status := http.StatusCreated
	if result.Reused {
		status = http.StatusOK
	}
	writeJSON(w, status, result)
}
