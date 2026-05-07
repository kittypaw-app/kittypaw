package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/store"
)

type kanbanStoreContextKey struct{}

func (s *Server) requireKanbanAPIAccess(next http.Handler) http.Handler {
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

		ctx := context.WithValue(r.Context(), kanbanStoreContextKey{}, st)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) kanbanStore(r *http.Request) *store.Store {
	if st, ok := r.Context().Value(kanbanStoreContextKey{}).(*store.Store); ok && st != nil {
		return st
	}
	return s.store
}

func (s *Server) handleKanbanProjectsList(w http.ResponseWriter, r *http.Request) {
	projects, err := s.kanbanStore(r).ListKanbanProjects(false)
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

	project, err := s.kanbanStore(r).CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     body.Slug,
		Name:     body.Name,
		RootPath: filepath.Clean(body.RootPath),
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	board, err := s.kanbanStore(r).GetDefaultKanbanBoard(project.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project, "default_board": board})
}

func (s *Server) handleKanbanProjectShow(w http.ResponseWriter, r *http.Request) {
	project, err := kanbanResolveProject(s.kanbanStore(r), chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project})
}

func (s *Server) handleKanbanProjectBoardsList(w http.ResponseWriter, r *http.Request) {
	project, err := kanbanResolveProject(s.kanbanStore(r), chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	boards, err := s.kanbanStore(r).ListKanbanBoards(project.ID)
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
	project, err := kanbanResolveProject(s.kanbanStore(r), chi.URLParam(r, "project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	milestones, err := s.kanbanStore(r).ListKanbanMilestones(project.ID)
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
	project, err := kanbanResolveProject(s.kanbanStore(r), chi.URLParam(r, "project"))
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
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	targetDate, ok := kanbanValidateDate(w, body.TargetDate)
	if !ok {
		return
	}
	milestone, err := s.kanbanStore(r).CreateKanbanMilestone(store.CreateKanbanMilestoneRequest{
		ProjectID:   project.ID,
		Title:       title,
		Description: strings.TrimSpace(body.Description),
		TargetDate:  targetDate,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"milestone": milestone})
}

func (s *Server) handleKanbanTasksCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project   string `json:"project"`
		Board     string `json:"board"`
		Milestone string `json:"milestone"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		Status    string `json:"status"`
		Priority  int    `json:"priority"`
		Assignee  string `json:"assignee"`
		CreatedBy string `json:"created_by"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Project) == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	project, err := kanbanResolveProject(s.kanbanStore(r), body.Project)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	status, ok := kanbanValidateStatus(w, body.Status, true)
	if !ok {
		return
	}
	boardID, err := kanbanResolveBoardID(s.kanbanStore(r), project.ID, body.Board)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	milestoneID, err := kanbanResolveMilestoneID(s.kanbanStore(r), project.ID, body.Milestone)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	task, err := s.kanbanStore(r).CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID:   project.ID,
		BoardID:     boardID,
		MilestoneID: milestoneID,
		Title:       strings.TrimSpace(body.Title),
		Body:        strings.TrimSpace(body.Body),
		Status:      status,
		Priority:    body.Priority,
		Assignee:    strings.TrimSpace(body.Assignee),
		CreatedBy:   strings.TrimSpace(body.CreatedBy),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"task": task})
}

func (s *Server) handleKanbanTasksList(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.URL.Query().Get("project")) == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	project, err := kanbanResolveProject(s.kanbanStore(r), r.URL.Query().Get("project"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	status, ok := kanbanValidateStatus(w, r.URL.Query().Get("status"), true)
	if !ok {
		return
	}
	boardID, err := kanbanResolveBoardID(s.kanbanStore(r), project.ID, r.URL.Query().Get("board"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	milestoneID, err := kanbanResolveMilestoneID(s.kanbanStore(r), project.ID, r.URL.Query().Get("milestone"))
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	tasks, err := s.kanbanStore(r).ListKanbanTasks(store.KanbanTaskListFilter{
		ProjectID:   project.ID,
		BoardID:     boardID,
		MilestoneID: milestoneID,
		Status:      status,
	})
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	if tasks == nil {
		writeJSON(w, http.StatusOK, map[string]any{"tasks": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleKanbanStaleRunsList(w http.ResponseWriter, r *http.Request) {
	staleAfterRaw := strings.TrimSpace(r.URL.Query().Get("stale_after"))
	if staleAfterRaw == "" {
		writeError(w, http.StatusBadRequest, "stale_after is required")
		return
	}
	staleAfter, err := time.ParseDuration(staleAfterRaw)
	if err != nil || staleAfter <= 0 {
		writeError(w, http.StatusBadRequest, "positive stale_after duration is required")
		return
	}

	limit := 50
	if limitRaw := strings.TrimSpace(r.URL.Query().Get("limit")); limitRaw != "" {
		parsed, err := strconv.Atoi(limitRaw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "positive limit is required")
			return
		}
		limit = parsed
	}

	projectID := ""
	if projectArg := strings.TrimSpace(r.URL.Query().Get("project")); projectArg != "" {
		project, err := kanbanResolveProject(s.kanbanStore(r), projectArg)
		if err != nil {
			kanbanWriteStoreError(w, err)
			return
		}
		projectID = project.ID
	}

	cutoff := time.Now().UTC().Add(-staleAfter).Format("2006-01-02T15:04:05Z")
	staleRuns, err := s.kanbanStore(r).ListStaleKanbanRuns(store.KanbanStaleRunFilter{
		ProjectID:   projectID,
		StaleBefore: cutoff,
		Limit:       limit,
	})
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stale_runs":   kanbanSliceOrEmpty(staleRuns),
		"stale_before": cutoff,
	})
}

func (s *Server) handleKanbanTaskShow(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	task, err := s.kanbanStore(r).GetKanbanTask(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	comments, err := s.kanbanStore(r).ListKanbanComments(task.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	events, err := s.kanbanStore(r).ListKanbanEvents(task.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	runs, err := s.kanbanStore(r).ListKanbanRuns(task.ID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task":     task,
		"comments": kanbanSliceOrEmpty(comments),
		"events":   kanbanSliceOrEmpty(events),
		"runs":     kanbanSliceOrEmpty(runs),
	})
}

func (s *Server) handleKanbanTaskUpdate(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	task, err := kanbanResolveTask(s.kanbanStore(r), taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor          string  `json:"actor"`
		Title          *string `json:"title"`
		Body           *string `json:"body"`
		Status         *string `json:"status"`
		Priority       *int    `json:"priority"`
		Assignee       *string `json:"assignee"`
		Milestone      *string `json:"milestone"`
		ClearMilestone bool    `json:"clear_milestone"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Milestone != nil && body.ClearMilestone {
		writeError(w, http.StatusBadRequest, "milestone and clear_milestone are mutually exclusive")
		return
	}

	req := store.UpdateKanbanTaskRequest{
		Actor:          strings.TrimSpace(body.Actor),
		ClearMilestone: body.ClearMilestone,
	}
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "title is required")
			return
		}
		req.Title = &title
	}
	if body.Body != nil {
		bodyText := strings.TrimSpace(*body.Body)
		req.Body = &bodyText
	}
	if body.Status != nil {
		status, ok := kanbanValidateStatus(w, *body.Status, false)
		if !ok {
			return
		}
		req.Status = &status
	}
	if body.Priority != nil {
		priority := *body.Priority
		req.Priority = &priority
	}
	if body.Assignee != nil {
		assignee := strings.TrimSpace(*body.Assignee)
		req.Assignee = &assignee
	}
	if body.Milestone != nil {
		milestoneArg := strings.TrimSpace(*body.Milestone)
		if milestoneArg == "" {
			writeError(w, http.StatusBadRequest, "milestone is required when supplied")
			return
		}
		milestoneID, err := kanbanResolveMilestoneID(s.kanbanStore(r), task.ProjectID, milestoneArg)
		if err != nil {
			kanbanWriteStoreError(w, err)
			return
		}
		req.MilestoneID = &milestoneID
	}

	updated, err := s.kanbanStore(r).UpdateKanbanTask(task.ID, req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": updated})
}

func (s *Server) handleKanbanTaskClaim(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor   string `json:"actor"`
		WorkDir string `json:"work_dir"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	workDir := strings.TrimSpace(body.WorkDir)
	workDirProvider := ""
	if workDir != "" {
		workDirProvider = store.KanbanWorkDirManual
	}
	run, err := s.kanbanStore(r).ClaimKanbanTask(taskID, store.ClaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(body.Actor),
		WorkDir:         workDir,
		WorkDirProvider: workDirProvider,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleKanbanTaskHeartbeat(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor string `json:"actor"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	run, err := s.kanbanStore(r).HeartbeatKanbanTask(taskID, store.HeartbeatKanbanTaskRequest{
		Actor: strings.TrimSpace(body.Actor),
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleKanbanTaskComplete(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor        string          `json:"actor"`
		Summary      string          `json:"summary"`
		Metadata     json.RawMessage `json:"metadata"`
		MetadataJSON string          `json:"metadata_json"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	summary := strings.TrimSpace(body.Summary)
	if summary == "" {
		writeError(w, http.StatusBadRequest, "summary is required")
		return
	}
	metadata, ok := kanbanMetadataJSON(w, body.Metadata, body.MetadataJSON)
	if !ok {
		return
	}
	if err := s.kanbanStore(r).CompleteKanbanTask(taskID, store.CompleteKanbanTaskRequest{
		Actor:        strings.TrimSpace(body.Actor),
		Summary:      summary,
		MetadataJSON: metadata,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.kanbanStore(r).GetKanbanTask(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) handleKanbanTaskFail(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor        string          `json:"actor"`
		Summary      string          `json:"summary"`
		Error        string          `json:"error"`
		Metadata     json.RawMessage `json:"metadata"`
		MetadataJSON string          `json:"metadata_json"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	errorText := strings.TrimSpace(body.Error)
	if errorText == "" {
		writeError(w, http.StatusBadRequest, "error is required")
		return
	}
	metadata, ok := kanbanMetadataJSON(w, body.Metadata, body.MetadataJSON)
	if !ok {
		return
	}
	if err := s.kanbanStore(r).FailKanbanTask(taskID, store.FailKanbanTaskRequest{
		Actor:        strings.TrimSpace(body.Actor),
		Summary:      strings.TrimSpace(body.Summary),
		Error:        errorText,
		MetadataJSON: metadata,
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.kanbanStore(r).GetKanbanTask(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) handleKanbanTaskCancel(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor        string          `json:"actor"`
		Reason       string          `json:"reason"`
		Metadata     json.RawMessage `json:"metadata"`
		MetadataJSON string          `json:"metadata_json"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	metadata, ok := kanbanMetadataJSON(w, body.Metadata, body.MetadataJSON)
	if !ok {
		return
	}
	task, err := s.kanbanStore(r).CancelKanbanTask(taskID, store.CancelKanbanTaskRequest{
		Actor:        strings.TrimSpace(body.Actor),
		Reason:       reason,
		MetadataJSON: metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) handleKanbanTaskReclaim(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor        string          `json:"actor"`
		Reason       string          `json:"reason"`
		WorkDir      string          `json:"work_dir"`
		Metadata     json.RawMessage `json:"metadata"`
		MetadataJSON string          `json:"metadata_json"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	actor := strings.TrimSpace(body.Actor)
	if actor == "" {
		writeError(w, http.StatusBadRequest, "actor is required")
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	metadata, ok := kanbanMetadataJSON(w, body.Metadata, body.MetadataJSON)
	if !ok {
		return
	}
	workDir := strings.TrimSpace(body.WorkDir)
	workDirProvider := ""
	if workDir != "" {
		workDir = filepath.Clean(workDir)
		workDirProvider = store.KanbanWorkDirManual
	}
	run, err := s.kanbanStore(r).ReclaimKanbanTask(taskID, store.ReclaimKanbanTaskRequest{
		Actor:           actor,
		Reason:          reason,
		WorkDir:         workDir,
		WorkDirProvider: workDirProvider,
		MetadataJSON:    metadata,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleKanbanTaskArchive(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor string `json:"actor"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	task, err := s.kanbanStore(r).ArchiveKanbanTask(taskID, strings.TrimSpace(body.Actor))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) handleKanbanTaskBlock(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor  string `json:"actor"`
		Reason string `json:"reason"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Reason) == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}
	if err := s.kanbanStore(r).BlockKanbanTask(taskID, store.BlockKanbanTaskRequest{
		Actor:  strings.TrimSpace(body.Actor),
		Reason: strings.TrimSpace(body.Reason),
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.kanbanStore(r).GetKanbanTask(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) handleKanbanTaskUnblock(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Actor   string `json:"actor"`
		Comment string `json:"comment"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	if err := s.kanbanStore(r).UnblockKanbanTask(taskID, store.UnblockKanbanTaskRequest{
		Actor:   strings.TrimSpace(body.Actor),
		Comment: strings.TrimSpace(body.Comment),
	}); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.kanbanStore(r).GetKanbanTask(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) handleKanbanTaskCommentsList(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	comments, err := s.kanbanStore(r).ListKanbanComments(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": kanbanSliceOrEmpty(comments)})
}

func (s *Server) handleKanbanTaskCommentsCreate(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	comment, err := s.kanbanStore(r).AddKanbanTaskComment(taskID, strings.TrimSpace(body.Author), body.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"comment": comment})
}

func (s *Server) handleKanbanTaskRunsList(w http.ResponseWriter, r *http.Request) {
	taskID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), taskID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	runs, err := s.kanbanStore(r).ListKanbanRuns(taskID)
	if err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": kanbanSliceOrEmpty(runs)})
}

func (s *Server) handleKanbanTaskLinksCreate(w http.ResponseWriter, r *http.Request) {
	parentID := strings.TrimSpace(chi.URLParam(r, "task"))
	if _, err := kanbanResolveTask(s.kanbanStore(r), parentID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	var body struct {
		ChildID string `json:"child_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.ChildID) == "" {
		writeError(w, http.StatusBadRequest, "child_id is required")
		return
	}
	childID := strings.TrimSpace(body.ChildID)
	if _, err := kanbanResolveTask(s.kanbanStore(r), childID); err != nil {
		kanbanWriteStoreError(w, err)
		return
	}
	if err := s.kanbanStore(r).LinkKanbanTasks(parentID, childID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"linked": true})
}

func kanbanResolveProject(st *store.Store, projectArg string) (*store.KanbanProject, error) {
	projectArg = strings.TrimSpace(projectArg)
	if projectArg == "" {
		return nil, sql.ErrNoRows
	}
	return st.GetKanbanProject(projectArg)
}

func kanbanResolveBoardID(st *store.Store, projectID, boardArg string) (string, error) {
	boardArg = strings.TrimSpace(boardArg)
	if boardArg == "" {
		return "", nil
	}
	board, err := st.GetKanbanBoard(projectID, boardArg)
	if err != nil {
		return "", err
	}
	return board.ID, nil
}

func kanbanResolveMilestoneID(st *store.Store, projectID, milestoneArg string) (string, error) {
	milestoneArg = strings.TrimSpace(milestoneArg)
	if milestoneArg == "" {
		return "", nil
	}
	milestone, err := st.GetKanbanMilestone(projectID, milestoneArg)
	if err != nil {
		return "", err
	}
	return milestone.ID, nil
}

func kanbanResolveTask(st *store.Store, taskID string) (*store.KanbanTask, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, sql.ErrNoRows
	}
	return st.GetKanbanTask(taskID)
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

func kanbanValidateStatus(w http.ResponseWriter, status string, allowEmpty bool) (string, bool) {
	status = strings.TrimSpace(status)
	if status == "" && allowEmpty {
		return "", true
	}
	switch status {
	case store.KanbanStatusTriage,
		store.KanbanStatusTodo,
		store.KanbanStatusReady,
		store.KanbanStatusRunning,
		store.KanbanStatusBlocked,
		store.KanbanStatusDone,
		store.KanbanStatusArchived:
		return status, true
	default:
		writeError(w, http.StatusBadRequest, "unknown kanban status")
		return "", false
	}
}

func kanbanSliceOrEmpty[T any](items []T) any {
	if items == nil {
		return []any{}
	}
	return items
}

func kanbanMetadataJSON(w http.ResponseWriter, raw json.RawMessage, fallback string) (string, bool) {
	if len(raw) != 0 && string(raw) != "null" {
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, raw); err != nil {
			writeError(w, http.StatusBadRequest, "metadata must be valid JSON")
			return "", false
		}
		return compacted.String(), true
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return "", true
	}
	if !json.Valid([]byte(fallback)) {
		writeError(w, http.StatusBadRequest, "metadata_json must be valid JSON")
		return "", false
	}
	return fallback, true
}

func kanbanWriteStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}
