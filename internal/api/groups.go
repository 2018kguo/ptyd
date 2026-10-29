package api

import (
	"net/http"
	"strconv"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func (s *Server) registerGroupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /groups", s.handleGroups)
	mux.HandleFunc("POST /groups", s.handleCreateGroup)
	mux.HandleFunc("GET /groups/{group}", s.handleGroup)
	mux.HandleFunc("PATCH /groups/{group}", s.handleUpdateGroup)
	mux.HandleFunc("DELETE /groups/{group}", s.handleRemoveGroup)
	mux.HandleFunc("PATCH /terminals/{id}", s.handleUpdateTerminal)
	mux.HandleFunc("POST /terminals/{id}/relaunch", s.handleRelaunch)
}
func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	rows, err := s.mgr.Groups()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var cfg terminal.GroupConfig
	if !decodeJSON(w, r, &cfg) {
		return
	}
	g, err := s.mgr.CreateGroup(cfg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}
func (s *Server) handleGroup(w http.ResponseWriter, r *http.Request) {
	g, err := s.mgr.Group(terminal.GroupID(r.PathValue("group")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var u terminal.GroupUpdate
	if !decodeJSON(w, r, &u) {
		return
	}
	g, err := s.mgr.UpdateGroup(terminal.GroupID(r.PathValue("group")), u)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}
func (s *Server) handleRemoveGroup(w http.ResponseWriter, r *http.Request) {
	force := false
	if raw := r.URL.Query().Get("force"); raw != "" {
		var err error
		force, err = strconv.ParseBool(raw)
		if err != nil {
			writeError(w, 400, "force must be true or false")
			return
		}
	}
	if err := s.mgr.RemoveGroup(terminal.GroupID(r.PathValue("group")), force); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "removed"})
}
func (s *Server) handleUpdateTerminal(w http.ResponseWriter, r *http.Request) {
	var u terminal.TerminalUpdate
	if !decodeJSON(w, r, &u) {
		return
	}
	info, err := s.mgr.UpdateTerminal(terminal.ID(r.PathValue("id")), u)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, info)
}

func (s *Server) handleRelaunch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Policy terminal.RestorePolicy `json:"policy"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	info, err := s.mgr.Relaunch(terminal.ID(r.PathValue("id")), req.Policy)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}
