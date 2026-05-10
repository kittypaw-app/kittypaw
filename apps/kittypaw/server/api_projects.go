package server

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/store"
)

type projectsStoreContextKey struct{}

type projectsRequestContext struct {
	Store   *store.Store
	Session *engine.Session
	Account *core.Account
}

func (s *Server) requireProjectsAPIAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required, err := s.apiAuthRequired()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read local auth store")
			return
		}

		ctxValue := projectsRequestContext{Store: s.store, Session: s.session}
		if required {
			acct, acctErr := s.requestAccount(r)
			if acctErr == nil {
				if acct.Deps == nil || acct.Deps.Store == nil {
					writeError(w, http.StatusInternalServerError, "account store unavailable")
					return
				}
				ctxValue = projectsRequestContext{Store: acct.Deps.Store, Session: acct.Session, Account: acct.Deps.Account}
			} else if !s.apiTokenAccepted(requestAuthToken(r)) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}

		ctx := context.WithValue(r.Context(), projectsStoreContextKey{}, ctxValue)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) projectsStore(r *http.Request) *store.Store {
	if ctxValue, ok := r.Context().Value(projectsStoreContextKey{}).(projectsRequestContext); ok && ctxValue.Store != nil {
		return ctxValue.Store
	}
	if st, ok := r.Context().Value(projectsStoreContextKey{}).(*store.Store); ok && st != nil {
		return st
	}
	return s.store
}

func (s *Server) projectsSession(r *http.Request) *engine.Session {
	if ctxValue, ok := r.Context().Value(projectsStoreContextKey{}).(projectsRequestContext); ok && ctxValue.Session != nil {
		return ctxValue.Session
	}
	return s.session
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
	st := s.projectsStore(r)
	project, err := st.CreateProject(store.CreateProjectRequest{
		Key:       body.Key,
		Name:      body.Name,
		RootPath:  body.RootPath,
		CreatedBy: "api",
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.refreshProjectFileRoot(r, project)
	class, _ := store.ClassifyProjectFolder(project.RootPath)
	kickoff := projectKickoffMessage(class)
	if kickoff != "" {
		if err := st.AddConversationTurn(&core.ConversationTurn{
			Role:      core.RoleAssistant,
			Content:   kickoff,
			Channel:   "project",
			ChatID:    project.ProjectConversationID,
			Timestamp: core.NowTimestamp(),
		}); err != nil {
			slog.Warn("record project kickoff message failed", "project", project.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project, "folder_class": class, "kickoff_message": kickoff})
}

func projectKickoffMessage(class store.ProjectFolderClass) string {
	if class == store.ProjectFolderNonEmpty {
		return "내용을 파악해서 티켓 초안을 만들까요?"
	}
	return "이 프로젝트에서 무엇을 만들까요?"
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

func (s *Server) handleProjectGitInit(w http.ResponseWriter, r *http.Request) {
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime == nil {
		writeError(w, http.StatusInternalServerError, "project job runtime unavailable")
		return
	}
	status, err := runtime.InitProjectGit(r.Context(), chi.URLParam(r, "project"))
	if err != nil {
		writeProjectJobAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"git": status})
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
	jobs, err := s.projectsStore(r).ListJobs(store.JobListFilter{TicketID: chi.URLParam(r, "ticket")})
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
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

func (s *Server) handleJobStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeBody(w, r, &body) {
		return
	}
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime == nil {
		writeError(w, http.StatusInternalServerError, "project job runtime unavailable")
		return
	}
	job, err := runtime.StartJob(r.Context(), chi.URLParam(r, "job"), engine.StartProjectJobOptions{ActorID: body.ActorID})
	if err != nil {
		writeProjectJobAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
		Reason  string `json:"reason"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime != nil {
		job, err := runtime.CancelJob(r.Context(), chi.URLParam(r, "job"), body.ActorID, body.Reason)
		if err != nil {
			writeProjectJobAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": job})
		return
	}
	job, err := s.projectsStore(r).CancelJob(chi.URLParam(r, "job"), body.ActorID, body.Reason)
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleJobInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ActorID string `json:"actor_id"`
		Text    string `json:"text"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime == nil {
		writeError(w, http.StatusInternalServerError, "project job runtime unavailable")
		return
	}
	result, err := runtime.AppendJobInput(r.Context(), chi.URLParam(r, "job"), body.ActorID, body.Text)
	if err != nil {
		writeProjectJobAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "job": result.Job, "event": result.Event})
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	runtime := s.projectsSession(r).ProjectJobRuntime
	if runtime != nil {
		logs, err := runtime.JobLogs(chi.URLParam(r, "job"))
		if err != nil {
			projectsWriteStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, logs)
		return
	}
	job, err := s.projectsStore(r).GetJob(chi.URLParam(r, "job"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	events, err := s.projectsStore(r).ListJobEvents(chi.URLParam(r, "job"))
	if err != nil {
		projectsWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job, "log_tail": job.LogTail, "events": events})
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

func writeProjectJobAPIError(w http.ResponseWriter, err error) {
	var jobErr *store.ProjectJobError
	if errors.As(err, &jobErr) {
		writeJSON(w, http.StatusConflict, map[string]string{"code": jobErr.Code, "error": jobErr.Error()})
		return
	}
	projectsWriteStoreError(w, err)
}

func (s *Server) refreshProjectFileRoot(r *http.Request, project *store.Project) {
	if project == nil {
		return
	}
	sess := s.session
	live := s.liveIndexer
	if required, err := s.apiAuthRequired(); err == nil && required {
		if acct, acctErr := s.requestAccount(r); acctErr == nil {
			sess = acct.Session
			if acct.Deps != nil {
				live = acct.Deps.LiveIndexer
			}
		}
	}
	if sess == nil {
		return
	}
	if err := sess.RefreshAllowedPaths(); err != nil {
		slog.Error("project create: allowed path refresh failed", "project_id", project.ID, "error", err)
	}
	if sess.Indexer == nil {
		return
	}
	go func(projectID, rootPath string) {
		if _, err := sess.Indexer.Index(context.Background(), projectID, rootPath); err != nil {
			slog.Warn("project create: indexing failed", "project_id", projectID, "error", err)
		}
		if live != nil {
			if err := live.AddWorkspace(projectID, rootPath); err != nil {
				slog.Warn("project create: live indexer add failed", "project_id", projectID, "error", err)
			}
		}
	}(project.ID, project.RootPath)
}
