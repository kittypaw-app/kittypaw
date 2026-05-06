package server

import "net/http"

func (s *Server) handleKanbanProjectsList(w http.ResponseWriter, _ *http.Request) {
	projects, err := s.store.ListKanbanProjects(false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		writeJSON(w, http.StatusOK, map[string]any{"projects": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}
