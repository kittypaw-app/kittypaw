package server

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/store"
)

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

func (s *Server) handleKanbanProjectsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		RootPath string `json:"root_path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	body.Slug = strings.TrimSpace(body.Slug)
	body.Name = strings.TrimSpace(body.Name)
	body.RootPath = strings.TrimSpace(body.RootPath)
	if body.Slug == "" {
		writeError(w, http.StatusBadRequest, "slug is required")
		return
	}
	if body.RootPath == "" || !filepath.IsAbs(body.RootPath) {
		writeError(w, http.StatusBadRequest, "absolute root_path is required")
		return
	}

	project, err := s.store.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     body.Slug,
		Name:     body.Name,
		RootPath: filepath.Clean(body.RootPath),
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	board, err := s.store.GetDefaultKanbanBoard(project.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project, "default_board": board})
}

func (s *Server) handleKanbanProjectShow(w http.ResponseWriter, r *http.Request) {
	project, err := kanbanResolveProject(s.store, chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project})
}

func (s *Server) handleKanbanProjectBoardsList(w http.ResponseWriter, r *http.Request) {
	project, err := kanbanResolveProject(s.store, chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	boards, err := s.store.ListKanbanBoards(project.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	if boards == nil {
		writeJSON(w, http.StatusOK, map[string]any{"boards": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
}

func (s *Server) handleKanbanProjectMilestonesList(w http.ResponseWriter, r *http.Request) {
	project, err := kanbanResolveProject(s.store, chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	milestones, err := s.store.ListKanbanMilestones(project.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	if milestones == nil {
		writeJSON(w, http.StatusOK, map[string]any{"milestones": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"milestones": milestones})
}

func (s *Server) handleKanbanProjectMilestonesCreate(w http.ResponseWriter, r *http.Request) {
	project, err := kanbanResolveProject(s.store, chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		TargetDate  string `json:"target_date"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	targetDate, ok := kanbanValidateDate(w, body.TargetDate)
	if !ok {
		return
	}
	milestone, err := s.store.CreateKanbanMilestone(store.CreateKanbanMilestoneRequest{
		ProjectID:   project.ID,
		Title:       strings.TrimSpace(body.Title),
		Description: strings.TrimSpace(body.Description),
		TargetDate:  targetDate,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"milestone": milestone})
}

func kanbanResolveProject(st *store.Store, projectArg string) (*store.KanbanProject, error) {
	projectArg = strings.TrimSpace(projectArg)
	if projectArg == "" {
		return nil, sql.ErrNoRows
	}
	return st.GetKanbanProject(projectArg)
}

func kanbanValidateDate(w http.ResponseWriter, value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		writeError(w, http.StatusBadRequest, "target_date must use YYYY-MM-DD")
		return "", false
	}
	return value, true
}

func kanbanWriteStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
