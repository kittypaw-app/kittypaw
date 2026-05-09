package server

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/store"
)

type projectsStoreContextKey struct{}

func (s *Server) requireProjectsAPIAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required, err := s.apiAuthRequired()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read local auth store")
			return
		}

		st := s.store
		if required {
			acct, acctErr := s.requestAccount(r)
			if acctErr == nil {
				if acct.Deps == nil || acct.Deps.Store == nil {
					writeError(w, http.StatusInternalServerError, "account store unavailable")
					return
				}
				st = acct.Deps.Store
			} else if !s.apiTokenAccepted(requestAuthToken(r)) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}

		ctx := context.WithValue(r.Context(), projectsStoreContextKey{}, st)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) projectsStore(r *http.Request) *store.Store {
	if st, ok := r.Context().Value(projectsStoreContextKey{}).(*store.Store); ok && st != nil {
		return st
	}
	return s.store
}

func (s *Server) handleProjectsList(w http.ResponseWriter, r *http.Request) {
	projects, err := s.projectsStore(r).ListProjects(false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if projects == nil {
		projects = []store.Project{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (s *Server) handleProjectsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key      string `json:"key"`
		Name     string `json:"name"`
		RootPath string `json:"root_path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	project, err := s.projectsStore(r).CreateProject(store.CreateProjectRequest{
		Key:       body.Key,
		Name:      body.Name,
		RootPath:  body.RootPath,
		CreatedBy: "api",
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	class, _ := store.ClassifyProjectFolder(project.RootPath)
	writeJSON(w, http.StatusCreated, map[string]any{"project": project, "folder_class": class})
}

func (s *Server) handleProjectShow(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectsStore(r).GetProject(chi.URLParam(r, "project"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project})
}

func (s *Server) handleProjectBoard(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectsStore(r).GetProject(chi.URLParam(r, "project"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	board, err := s.projectsStore(r).ProjectBoard(project.ID)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"board": board})
}

func (s *Server) handleProjectBriefDraftsList(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectsStore(r).GetProject(chi.URLParam(r, "project"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	drafts, err := s.projectsStore(r).ListProjectBriefDrafts(project.ID)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	if drafts == nil {
		drafts = []store.ProjectBriefDraft{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"drafts": drafts})
}

func (s *Server) handleProjectBriefDraftsCreate(w http.ResponseWriter, r *http.Request) {
	project, err := s.projectsStore(r).GetProject(chi.URLParam(r, "project"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	var body struct {
		Title               string `json:"title"`
		BriefJSON           string `json:"brief_json"`
		ProposedTicketsJSON string `json:"proposed_tickets_json"`
		CreatedBy           string `json:"created_by"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	draft, err := s.projectsStore(r).CreateProjectBriefDraft(store.CreateProjectBriefDraftRequest{
		ProjectID:           project.ID,
		Title:               body.Title,
		BriefJSON:           body.BriefJSON,
		ProposedTicketsJSON: body.ProposedTicketsJSON,
		CreatedBy:           body.CreatedBy,
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"draft": draft})
}

func (s *Server) handleProjectBriefDraftUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title               *string `json:"title"`
		BriefJSON           *string `json:"brief_json"`
		ProposedTicketsJSON *string `json:"proposed_tickets_json"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	draft, err := s.projectsStore(r).UpdateProjectBriefDraft(chi.URLParam(r, "draft"), store.UpdateProjectBriefDraftRequest{
		Title:               body.Title,
		BriefJSON:           body.BriefJSON,
		ProposedTicketsJSON: body.ProposedTicketsJSON,
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": draft})
}

func (s *Server) handleProjectBriefDraftCommit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	result, err := s.projectsStore(r).CommitProjectBriefDraft(chi.URLParam(r, "draft"), body.ActorID)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleTicketsList(w http.ResponseWriter, r *http.Request) {
	projectRef := strings.TrimSpace(r.URL.Query().Get("project"))
	if projectRef == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	project, err := s.projectsStore(r).GetProject(projectRef)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	tickets, err := s.projectsStore(r).ListTickets(store.TicketListFilter{
		ProjectID:       project.ID,
		Status:          strings.TrimSpace(r.URL.Query().Get("status")),
		IncludeArchived: r.URL.Query().Get("include_archived") == "1",
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	if tickets == nil {
		tickets = []store.Ticket{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (s *Server) handleTicketsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string   `json:"project"`
		Title     string   `json:"title"`
		Body      string   `json:"body"`
		Status    string   `json:"status"`
		Priority  int      `json:"priority"`
		Labels    []string `json:"labels"`
		CreatedBy string   `json:"created_by"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	project, err := s.projectsStore(r).GetProject(body.Project)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	ticket, err := s.projectsStore(r).CreateTicket(store.CreateTicketRequest{
		ProjectID: project.ID,
		Title:     body.Title,
		Body:      body.Body,
		Status:    body.Status,
		Priority:  body.Priority,
		Labels:    body.Labels,
		CreatedBy: body.CreatedBy,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket})
}

func (s *Server) handleTicketShow(w http.ResponseWriter, r *http.Request) {
	ticket, err := s.projectsStore(r).GetTicket(chi.URLParam(r, "ticket"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (s *Server) handleTicketActionsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		ActorID string `json:"actor_id"`
		Message string `json:"message"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	actionType := strings.TrimSpace(body.Type)
	if actionType == "" {
		actionType = "move"
	}
	if actionType != "move" {
		writeError(w, http.StatusBadRequest, "unsupported ticket action")
		return
	}
	ticket, err := s.projectsStore(r).MoveTicket(chi.URLParam(r, "ticket"), store.MoveTicketRequest{
		ActorID: body.ActorID,
		Status:  body.Status,
		Message: body.Message,
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (s *Server) handleTicketArchive(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	ticket, err := s.projectsStore(r).ArchiveTicket(chi.URLParam(r, "ticket"), body.ActorID)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (s *Server) handleTicketJobsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": []any{}})
}

func (s *Server) handleTicketJobsPlan(w http.ResponseWriter, r *http.Request) {
	ticket, err := s.projectsStore(r).GetTicket(chi.URLParam(r, "ticket"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	if err := s.projectsStore(r).EnsureDefaultDrivers(); err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	var body struct {
		DriverID      string `json:"driver_id"`
		Mode          string `json:"mode"`
		PromptSummary string `json:"prompt_summary"`
		PromptText    string `json:"prompt_text"`
		CreatedBy     string `json:"created_by"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	driverID := strings.TrimSpace(body.DriverID)
	if driverID == "" {
		driverID = "codex"
	}
	job, err := s.projectsStore(r).PlanJob(store.PlanJobRequest{
		ProjectID:     ticket.ProjectID,
		TicketID:      ticket.ID,
		DriverID:      driverID,
		Mode:          body.Mode,
		PromptSummary: body.PromptSummary,
		PromptText:    body.PromptText,
		CreatedBy:     body.CreatedBy,
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job": job})
}

func (s *Server) handleJobShow(w http.ResponseWriter, r *http.Request) {
	job, err := s.projectsStore(r).GetJob(chi.URLParam(r, "job"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleJobApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	job, err := s.projectsStore(r).ApproveJob(chi.URLParam(r, "job"), body.ActorID)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleJobStart(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusConflict, "driver execution is not available in MVP 1")
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
		Reason  string `json:"reason"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	job, err := s.projectsStore(r).CancelJob(chi.URLParam(r, "job"), body.ActorID, body.Reason)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	events, err := s.projectsStore(r).ListJobEvents(chi.URLParam(r, "job"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleDriversList(w http.ResponseWriter, r *http.Request) {
	if err := s.projectsStore(r).EnsureDefaultDrivers(); err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	drivers, err := s.projectsStore(r).ListDrivers()
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"drivers": drivers})
}

func (s *Server) handleDriversCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID                 string `json:"id"`
		DisplayName        string `json:"display_name"`
		Command            string `json:"command"`
		SupportedModesJSON string `json:"supported_modes_json"`
		DefaultArgsJSON    string `json:"default_args_json"`
		Enabled            *bool  `json:"enabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	driver, err := s.projectsStore(r).UpsertDriver(store.UpsertDriverRequest{
		ID:                 body.ID,
		DisplayName:        body.DisplayName,
		Command:            body.Command,
		SupportedModesJSON: body.SupportedModesJSON,
		DefaultArgsJSON:    body.DefaultArgsJSON,
		Enabled:            enabled,
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"driver": driver})
}

func (s *Server) handleDriverUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName        string `json:"display_name"`
		Command            string `json:"command"`
		SupportedModesJSON string `json:"supported_modes_json"`
		DefaultArgsJSON    string `json:"default_args_json"`
		Enabled            *bool  `json:"enabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	driver, err := s.projectsStore(r).UpsertDriver(store.UpsertDriverRequest{
		ID:                 chi.URLParam(r, "driver"),
		DisplayName:        body.DisplayName,
		Command:            body.Command,
		SupportedModesJSON: body.SupportedModesJSON,
		DefaultArgsJSON:    body.DefaultArgsJSON,
		Enabled:            enabled,
	})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"driver": driver})
}

func projectsWriteStoreError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}
