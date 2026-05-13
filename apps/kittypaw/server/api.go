package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
	"github.com/jinto/kittypaw/store"
)

var safeSkillName = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)

// ---------------------------------------------------------------------------
// JSON helpers
// ---------------------------------------------------------------------------

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("writeJSON encode", "error", err)
	}
}

// writeError writes a structured {"error": msg} response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody deserializes the request body into dst and reports errors.
// Limits request body to 1 MB to prevent memory exhaustion.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// GET /health
// ---------------------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()

	channels := make([]string, 0, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		channels = append(channels, string(ch.ChannelType))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"version":  s.version,
		"model":    cfg.LLM.Model,
		"channels": channels,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/status
// ---------------------------------------------------------------------------

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.store.TodayStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	byModel, err := s.store.TodayLLMUsageByModel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if byModel == nil {
		byModel = []store.LLMUsageByModel{}
	}
	runtimeSnapshot := engine.RuntimeAdmissionSnapshot{}
	if runtime := s.defaultRuntime(); runtime != nil && runtime.Admission != nil {
		runtimeSnapshot = runtime.Admission.Snapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total_runs":         stats.TotalRuns,
		"successful":         stats.Successful,
		"failed":             stats.Failed,
		"auto_retries":       stats.AutoRetries,
		"total_tokens":       stats.TotalTokens,
		"estimated_cost_usd": stats.EstimatedCostUSD,
		"llm_usage_by_model": byModel,
		"runtime":            runtimeSnapshot,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/executions
// ---------------------------------------------------------------------------

func (s *Server) handleExecutions(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	skill := r.URL.Query().Get("skill")
	if skill != "" && !safeSkillName.MatchString(skill) {
		writeError(w, http.StatusBadRequest, "invalid skill name")
		return
	}

	var (
		execs any
		err   error
	)
	if skill != "" {
		execs, err = s.store.SearchExecutions(skill, limit)
	} else {
		execs, err = s.store.RecentExecutions(limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if execs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"executions": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": execs})
}

// ---------------------------------------------------------------------------
// GET /api/v1/chat/history
// ---------------------------------------------------------------------------

func (s *Server) handleChatHistory(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if conversationID == "" {
		conversationID = store.DefaultConversationID
	}
	turns, err := s.store.ListConversationTurnsForConversation(conversationID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	summary, err := s.store.ConversationSummaryForConversation(conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	turnsOut := any(turns)
	if turns == nil {
		turnsOut = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"summary": summary,
		"turns":   turnsOut,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/chat/forget
// ---------------------------------------------------------------------------

func (s *Server) handleChatForget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversation_id"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID == "" {
		conversationID = store.DefaultConversationID
	}
	deleted, err := s.store.ForgetConversationByID(conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"turns_deleted": deleted,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/chat/compact
// ---------------------------------------------------------------------------

func (s *Server) handleChatCompact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeepRecent     int    `json:"keep_recent"`
		ConversationID string `json:"conversation_id"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID == "" {
		conversationID = store.DefaultConversationID
	}
	compacted, err := s.store.CompactConversationByID(conversationID, body.KeepRecent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"turns_compacted": compacted,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/conversations
// ---------------------------------------------------------------------------

func (s *Server) handleConversationsList(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	conversations, err := s.store.ListConversations(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := any(conversations)
	if conversations == nil {
		out = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": out})
}

// ---------------------------------------------------------------------------
// POST /api/v1/conversations
// ---------------------------------------------------------------------------

func (s *Server) handleConversationsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID              string `json:"id"`
		Title           string `json:"title"`
		DefaultStaffID  string `json:"default_staff_id"`
		SourceChannel   string `json:"source_channel"`
		SourceSessionID string `json:"source_session_id"`
		ChatID          string `json:"chat_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	conversation, err := s.store.CreateConversation(store.CreateConversationRequest{
		ID:              body.ID,
		ScopeType:       "general",
		ScopeID:         strings.TrimSpace(body.ID),
		Title:           body.Title,
		DefaultStaffID:  body.DefaultStaffID,
		SourceChannel:   body.SourceChannel,
		SourceSessionID: body.SourceSessionID,
		ChatID:          body.ChatID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"conversation": conversation})
}

// ---------------------------------------------------------------------------
// GET /api/v1/conversations/{id}
// ---------------------------------------------------------------------------

func (s *Server) handleConversationInfo(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(chi.URLParam(r, "id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	conversation, ok, err := s.store.Conversation(conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversation})
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/conversations/{id}
// ---------------------------------------------------------------------------

func (s *Server) handleConversationUpdate(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(chi.URLParam(r, "id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	var body struct {
		DefaultStaffID *string `json:"default_staff_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.DefaultStaffID == nil {
		writeError(w, http.StatusBadRequest, "default_staff_id is required")
		return
	}
	staffID := strings.TrimSpace(*body.DefaultStaffID)
	if staffID != "" {
		sess := s.defaultRuntime()
		if sess == nil {
			writeError(w, http.StatusInternalServerError, "session unavailable")
			return
		}
		base, err := core.ResolveBaseDir(sess.BaseDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resolved, ok, err := core.ResolveStaffReference(base, staffID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !ok {
			writeError(w, http.StatusBadRequest, "staff not found")
			return
		}
		staffID = resolved
	}
	conversation, err := s.store.SetConversationDefaultStaff(conversationID, staffID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation": conversation})
}

// ---------------------------------------------------------------------------
// GET /api/v1/conversations/{id}/messages
// ---------------------------------------------------------------------------

func (s *Server) handleConversationMessages(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(chi.URLParam(r, "id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation id is required")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	if _, ok, err := s.store.Conversation(conversationID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	turns, err := s.store.ListConversationTurnsForConversation(conversationID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := any(turns)
	if turns == nil {
		out = []any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": out})
}

// ---------------------------------------------------------------------------
// GET /api/v1/skills
// ---------------------------------------------------------------------------

func (s *Server) handleSkills(w http.ResponseWriter, _ *http.Request) {
	skills, err := core.LoadAllSkillsFrom(s.defaultRuntime().BaseDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type skillItem struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Enabled      bool   `json:"enabled"`
		Version      uint32 `json:"version"`
		Trigger      string `json:"trigger"`
		Cron         string `json:"cron,omitempty"`
		RunAt        string `json:"run_at,omitempty"`
		LastRun      string `json:"last_run,omitempty"`
		FailureCount int    `json:"failure_count"`
		NextRun      string `json:"next_run,omitempty"`
		Due          bool   `json:"due"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
	}
	items := make([]skillItem, 0, len(skills))
	now := time.Now()
	for _, sk := range skills {
		lastRun, _ := s.store.GetLastRun(sk.Skill.Name)
		failureCount, _ := s.store.GetFailureCount(sk.Skill.Name)
		status := engine.SkillScheduleStateFor(&sk.Skill, lastRun, failureCount, now)
		item := skillItem{
			Name:         sk.Skill.Name,
			Description:  sk.Skill.Description,
			Enabled:      sk.Skill.Enabled,
			Version:      sk.Skill.Version,
			Trigger:      sk.Skill.Trigger.Type,
			Cron:         sk.Skill.Trigger.Cron,
			RunAt:        sk.Skill.Trigger.RunAt,
			FailureCount: failureCount,
			Due:          status.Due,
			CreatedAt:    sk.Skill.CreatedAt,
			UpdatedAt:    sk.Skill.UpdatedAt,
		}
		if lastRun != nil {
			item.LastRun = lastRun.UTC().Format(time.RFC3339)
		}
		if status.NextRun != nil {
			item.NextRun = status.NextRun.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": items})
}

// ---------------------------------------------------------------------------
// POST /api/v1/skills/run
// ---------------------------------------------------------------------------

func (s *Server) handleSkillsRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// Build a synthetic event that triggers the named skill.
	payload := core.ChatPayload{
		ChatID: "api",
		Text:   "/run " + body.Name,
	}
	raw, _ := json.Marshal(payload)
	event := core.Event{Type: core.EventWebChat, Payload: raw}

	output, err := s.defaultRuntime().Run(r.Context(), event, nil)
	if err != nil {
		if isRuntimeAdmissionBusy(err) {
			writeError(w, http.StatusTooManyRequests, "runtime busy")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": output})
}

// ---------------------------------------------------------------------------
// POST /api/v1/skills/teach
// ---------------------------------------------------------------------------

func (s *Server) handleSkillsTeach(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Description string `json:"description"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Description == "" {
		writeError(w, http.StatusBadRequest, "description is required")
		return
	}

	result, err := engine.HandleTeach(r.Context(), body.Description, "api", s.defaultRuntime())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// POST /api/v1/skills/teach/approve
// ---------------------------------------------------------------------------

func (s *Server) handleTeachApprove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Code        string `json:"code"`
		Trigger     string `json:"trigger"`
		Schedule    string `json:"schedule"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == "" || body.Code == "" {
		writeError(w, http.StatusBadRequest, "name and code are required")
		return
	}

	trigger := body.Trigger
	if trigger == "" {
		trigger = "manual"
	}
	validTriggers := map[string]bool{"manual": true, "schedule": true, "keyword": true, "once": true, "natural": true}
	if !validTriggers[trigger] {
		writeError(w, http.StatusBadRequest, "invalid trigger type: "+trigger)
		return
	}
	// Validate syntax before saving — don't trust client-supplied code.
	ok, syntaxErr := engine.SyntaxCheck(r.Context(), body.Code, nil)
	if !ok {
		writeError(w, http.StatusBadRequest, "syntax check failed: "+syntaxErr)
		return
	}

	result := &engine.TeachResult{
		SkillName:   body.Name,
		Code:        body.Code,
		SyntaxOK:    true,
		Description: body.Description,
		Trigger:     core.SkillTrigger{Type: trigger, Cron: body.Schedule},
		Permissions: engine.DetectPermissions(body.Code),
	}
	if err := engine.ApproveSkill(s.defaultRuntime().BaseDir, result); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "name": body.Name})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/skills/{name}
// ---------------------------------------------------------------------------

func (s *Server) handleSkillsDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := core.DeleteSkillFrom(s.defaultRuntime().BaseDir, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------------------------------------------------------------------------
// POST /api/v1/skills/{name}/enable
// ---------------------------------------------------------------------------

func (s *Server) handleSkillEnable(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := core.EnableSkillFrom(s.defaultRuntime().BaseDir, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------------------------------------------------------------------------
// POST /api/v1/skills/{name}/disable
// ---------------------------------------------------------------------------

func (s *Server) handleSkillDisable(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := core.DisableSkillFrom(s.defaultRuntime().BaseDir, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------------------------------------------------------------------------
// POST /api/v1/skills/{name}/explain
// ---------------------------------------------------------------------------

func (s *Server) handleSkillExplain(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	skill, code, err := core.LoadSkillFrom(s.defaultRuntime().BaseDir, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if skill == nil {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}

	// Ask the LLM to explain the skill.
	prompt := "Explain the following JavaScript skill in plain language.\n\nName: " + skill.Name +
		"\nDescription: " + skill.Description +
		"\nCode:\n```js\n" + code + "\n```"

	messages := []core.LlmMessage{
		{Role: core.RoleUser, Content: prompt},
	}
	resp, err := s.defaultRuntime().Provider.Generate(engine.WithLLMCallKind(r.Context(), "skill.explain"), messages)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        skill.Name,
		"explanation": resp.Content,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/chat
// ---------------------------------------------------------------------------

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text            string `json:"text"`
		SessionID       string `json:"session_id"`        // legacy wire name
		SourceSessionID string `json:"source_session_id"` // preferred transport/source name
		ConversationID  string `json:"conversation_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	sessionID := strings.TrimSpace(body.SourceSessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(body.SessionID)
	}
	if sessionID == "" {
		sessionID = "api"
	}
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID != "" {
		if _, ok, err := s.store.ConversationScope(conversationID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		} else if !ok {
			if _, ok, err := s.store.Conversation(conversationID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			} else if !ok {
				writeError(w, http.StatusNotFound, "conversation not found")
				return
			}
		}
	}
	chatID := sessionID
	if conversationID != "" {
		chatID = conversationID
	}

	payload := core.ChatPayload{
		ChatID:          chatID,
		Text:            body.Text,
		SourceSessionID: sessionID,
		ConversationID:  conversationID,
	}
	raw, _ := json.Marshal(payload)
	event := core.Event{Type: core.EventWebChat, Payload: raw}

	output, err := s.defaultRuntime().Run(r.Context(), event, nil)
	if err != nil {
		if isRuntimeAdmissionBusy(err) {
			writeError(w, http.StatusTooManyRequests, "runtime busy")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"response": output})
}

// ---------------------------------------------------------------------------
// GET /api/v1/config/check
// ---------------------------------------------------------------------------

func (s *Server) handleConfigCheck(w http.ResponseWriter, _ *http.Request) {
	cfg := s.getConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"channels":       len(cfg.Channels),
		"runners":        len(cfg.Runners),
		"models":         len(cfg.Models),
		"mcp_servers":    len(cfg.MCPServers),
		"staff":          len(cfg.Staff),
		"autonomy_level": string(cfg.AutonomyLevel),
		"features": map[string]any{
			"progressive_retry":  cfg.Features.ProgressiveRetry,
			"context_compaction": cfg.Features.ContextCompaction,
			"model_routing":      cfg.Features.ModelRouting,
			"background_runners": cfg.Features.BackgroundRunners,
			"daily_token_limit":  cfg.Features.DailyTokenLimit,
		},
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/memory
// ---------------------------------------------------------------------------

func (s *Server) handleMemoryList(w http.ResponseWriter, r *http.Request) {
	limit := memoryLimitFromRequest(r, 50, 500)
	rows, err := s.store.ListUserMemory(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		writeJSON(w, http.StatusOK, map[string]any{"memory": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory": rows})
}

// ---------------------------------------------------------------------------
// GET /api/v1/memory/export
// ---------------------------------------------------------------------------

func (s *Server) handleMemoryExport(w http.ResponseWriter, r *http.Request) {
	limit := memoryLimitFromRequest(r, 100, 500)
	rows, err := s.store.ListUserMemory(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows == nil {
		writeJSON(w, http.StatusOK, map[string]any{"memory": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"memory": rows})
}

// ---------------------------------------------------------------------------
// GET /api/v1/memory/search?q=...
// ---------------------------------------------------------------------------

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}

	limit := memoryLimitFromRequest(r, 20, 100)
	results, err := s.store.SearchUserMemory(q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if results == nil {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/memory/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleMemoryDelete(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "memory key is required")
		return
	}
	deleted, err := s.store.DeleteUserMemory(key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "memory not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": true})
}

// ---------------------------------------------------------------------------
// POST /api/v1/memory/forget-all
// ---------------------------------------------------------------------------

func (s *Server) handleMemoryForgetAll(w http.ResponseWriter, _ *http.Request) {
	deleted, err := s.store.DeletePromptSafeUserMemory()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": deleted})
}

func memoryLimitFromRequest(r *http.Request, defaultLimit, maxLimit int) int {
	limit := defaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxLimit {
			limit = n
		}
	}
	return limit
}

// ---------------------------------------------------------------------------
// GET /api/v1/chat/checkpoints
// ---------------------------------------------------------------------------

func (s *Server) handleCheckpointsList(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.URL.Query().Get("conversation_id"))
	if conversationID == "" {
		conversationID = store.DefaultConversationID
	}
	cps, err := s.store.ListCheckpointsForConversation(conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cps == nil {
		writeJSON(w, http.StatusOK, map[string]any{"checkpoints": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checkpoints": cps})
}

// ---------------------------------------------------------------------------
// POST /api/v1/chat/checkpoints
// ---------------------------------------------------------------------------

func (s *Server) handleCheckpointsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label          string `json:"label"`
		ConversationID string `json:"conversation_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID == "" {
		conversationID = store.DefaultConversationID
	}
	cpID, err := s.store.CreateCheckpointForConversation(body.Label, conversationID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"success": true,
		"id":      cpID,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/checkpoints/{id}/rollback
// ---------------------------------------------------------------------------

func (s *Server) handleCheckpointRollback(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	cpID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid checkpoint id")
		return
	}

	deleted, err := s.store.RollbackToCheckpoint(cpID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"turns_deleted": deleted,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/reload
// ---------------------------------------------------------------------------

// handleReload reloads config.toml and reconciles each account's channel set.
//
// Load-bearing sync contract (pinned by TestHandleReload_WaitsForReconcile):
// callers — notably cli/cmd_setup.maybeReloadServer -> runChat — assume this
// handler returns only AFTER spawner.Reconcile completes, so the subsequent
// chat REPL connects to a server that already sees the new channel set. Do
// NOT convert Reconcile to a goroutine without first updating the CLI
// wiring AND TestHandleReload_WaitsForReconcile.
//
// Validation contract (symmetric with StartChannels and AddAccount): the
// proposed config is checked against live accounts BEFORE any state swap.
// A duplicate Telegram bot_token or a team-space account with channels is
// rejected with 409 Conflict, leaving s.config and the spawner untouched.
// Pinned by TestHandleReload_DuplicateTelegramToken_Rejects and
// the team-space account channels rejection test.
//
// Serialization contract: the entire validate→swap→reconcile sequence
// runs under accountMu. Releasing the lock between snapshot and reconcile
// would open a TOCTOU window where a concurrent AddAccount validates
// against the stale default-account channel list, passes, and spawns a
// duplicate bot that this reload was about to introduce.
func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	defaultID := s.defaultAccountID()
	cfgPath, err := core.ConfigPathForAccount(defaultID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg, err := core.LoadConfig(cfgPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reload failed: "+err.Error())
		return
	}
	secrets, err := core.LoadAccountSecrets(defaultID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "reload secrets failed: "+err.Error())
		return
	}
	core.HydrateRuntimeSecrets(cfg, secrets)

	s.accountMu.Lock()

	// Build the would-be-final snapshot (selected default account substituted with
	// the proposed cfg, all other accounts as-is) and run the same validators
	// StartChannels / AddAccount do.
	snapshot := make(map[string][]core.ChannelConfig, len(s.accountList)+1)
	accounts := make([]*core.Account, 0, len(s.accountList)+1)
	defaultSeen := false
	for _, peer := range s.accountList {
		if peer == nil || peer.Config == nil {
			continue
		}
		if peer.ID == defaultID {
			// Substitute a proposed copy so validators see the would-be-final
			// state without mutating the live pointer.
			proposedAccount := *peer
			proposedCfg := *cfg
			proposedAccount.Config = &proposedCfg
			accounts = append(accounts, &proposedAccount)
			snapshot[peer.ID] = accountChannelsForValidation(peer.ID, cfg.Channels, "")
			defaultSeen = true
		} else {
			accounts = append(accounts, peer)
			snapshot[peer.ID] = accountChannelsForValidation(peer.ID, peer.Config.Channels, "")
		}
	}
	if !defaultSeen {
		accounts = append(accounts, &core.Account{ID: defaultID, Config: cfg})
		snapshot[defaultID] = accountChannelsForValidation(defaultID, cfg.Channels, "")
	}

	if err := core.ValidateAccountChannels(snapshot); err != nil {
		slog.Error("reload rejected", "reason", "channel_duplicate", "error", err)
		s.accountMu.Unlock()
		writeError(w, http.StatusConflict, "channel validation: "+err.Error())
		return
	}
	if err := core.ValidateTeamSpaceAccounts(accounts); err != nil {
		slog.Error("reload rejected", "reason", "team_space_account_with_channels", "error", err)
		s.accountMu.Unlock()
		writeError(w, http.StatusConflict, "team space validation: "+err.Error())
		return
	}
	if err := core.ValidateTeamSpaceMemberships(accounts); err != nil {
		slog.Error("reload rejected", "reason", "team_space_membership", "error", err)
		s.accountMu.Unlock()
		writeError(w, http.StatusConflict, "team-space membership validation: "+err.Error())
		return
	}

	oldScheduler, err := s.applyAccountConfigLocked(defaultID, cfg)
	if err != nil {
		slog.Error("reload apply failed", "account", defaultID, "error", err)
		s.accountMu.Unlock()
		writeError(w, http.StatusInternalServerError, "reload apply failed: "+err.Error())
		return
	}
	slog.Info("config reloaded")

	result := map[string]any{"success": true}
	var warnings []string
	if reconcile := s.reconcileFunc(); reconcile != nil {
		if err := reconcile(defaultID, cfg.Channels); err != nil {
			slog.Warn("reload: channel reconcile partial failure", "error", err)
			warnings = append(warnings, err.Error())
		}
	}
	s.accountMu.Unlock()
	if oldScheduler != nil {
		oldScheduler.Wait()
	}

	if s.postReloadHook != nil {
		if err := s.postReloadHook(r.Context()); err != nil {
			slog.Warn("reload: post reload hook failed", "error", err)
			warnings = append(warnings, err.Error())
		}
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	writeJSON(w, http.StatusOK, result)
}

// reconcileFunc returns the effective reconcile function: a test-injected
// hook if set, otherwise the live spawner's Reconcile method, otherwise nil
// (pre-StartChannels case, where reload is still safe but a no-op).
func (s *Server) reconcileFunc() func(string, []core.ChannelConfig) error {
	if s.reloadReconcile != nil {
		return s.reloadReconcile
	}
	if s.spawner != nil {
		return s.spawner.Reconcile
	}
	return nil
}

// ---------------------------------------------------------------------------
// GET /api/v1/channels
// ---------------------------------------------------------------------------

func (s *Server) handleChannels(w http.ResponseWriter, _ *http.Request) {
	if s.spawner == nil {
		writeJSON(w, http.StatusOK, []ChannelStatus{})
		return
	}
	writeJSON(w, http.StatusOK, s.spawner.List())
}

// ---------------------------------------------------------------------------
// POST /api/v1/install
// ---------------------------------------------------------------------------

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		MdMode string `json:"md_mode"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Source == "" {
		writeError(w, http.StatusBadRequest, "source is required")
		return
	}

	cfg := s.getConfig()
	mdMode := req.MdMode
	if mdMode == "" {
		mdMode = cfg.SkillInstall.MdExecutionMode
	}
	if mdMode == "" {
		mdMode = "prompt" // final fallback
	}

	// Determine if source is GitHub URL or local path.
	var result *core.InstallResult
	var err error

	if owner, repo, parseErr := core.ParseGitHubURL(req.Source); parseErr == nil {
		// GitHub URL — resolve source.
		source, resolveErr := core.ResolveGitHubSource("https://raw.githubusercontent.com", owner, repo)
		if resolveErr != nil {
			writeError(w, http.StatusBadRequest, resolveErr.Error())
			return
		}
		// For native packages, install from temp dir.
		if source.Format == core.SourceFormatNative {
			defer os.RemoveAll(source.TempDir)
			result, err = core.InstallSkillSource(s.defaultRuntime().BaseDir, source.TempDir, core.InstallOptions{
				SourceURL: source.SourceURL,
			})
		} else {
			// For SKILL.md, write to temp dir and install.
			tmpDir, tmpErr := os.MkdirTemp("", "kittypaw-install-")
			if tmpErr != nil {
				writeError(w, http.StatusInternalServerError, tmpErr.Error())
				return
			}
			defer os.RemoveAll(tmpDir)
			if wErr := os.WriteFile(filepath.Join(tmpDir, "SKILL.md"), []byte(source.SkillMdContent), 0o644); wErr != nil {
				writeError(w, http.StatusInternalServerError, wErr.Error())
				return
			}
			result, err = core.InstallSkillSource(s.defaultRuntime().BaseDir, tmpDir, core.InstallOptions{
				MdExecutionMode: mdMode,
				SourceURL:       source.SourceURL,
			})
		}
	} else {
		// Local path — validate before passing to installer.
		cleanPath := filepath.Clean(req.Source)
		if !filepath.IsAbs(cleanPath) {
			writeError(w, http.StatusBadRequest, "local install path must be absolute")
			return
		}
		fi, statErr := os.Lstat(cleanPath)
		if statErr != nil {
			writeError(w, http.StatusBadRequest, "source path not found")
			return
		}
		if !fi.IsDir() {
			writeError(w, http.StatusBadRequest, "source path must be a directory")
			return
		}
		result, err = core.InstallSkillSource(s.defaultRuntime().BaseDir, cleanPath, core.InstallOptions{
			MdExecutionMode: mdMode,
		})
	}

	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// GET /api/v1/search?q=...
// ---------------------------------------------------------------------------

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("q")

	cfg := s.getConfig()
	registryURL := core.DefaultRegistryURL
	if cfg.Registry.URL != "" {
		registryURL = cfg.Registry.URL
	}

	rc, err := core.NewRegistryClient(registryURL)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "registry unavailable: " + err.Error()})
		return
	}

	entries, err := rc.FetchIndex()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "fetch registry: " + err.Error()})
		return
	}

	results := core.SearchEntries(entries, keyword)
	writeJSON(w, 200, map[string]any{"results": results})
}

// ---------------------------------------------------------------------------
// GET /api/v1/packages — list installed packages with config schema
// ---------------------------------------------------------------------------

func (s *Server) handlePackagesList(w http.ResponseWriter, _ *http.Request) {
	pkgManager := s.defaultPackageManager()
	packages, err := pkgManager.ListInstalled()
	if err != nil {
		writeError(w, 500, "list packages: "+err.Error())
		return
	}

	type pkgResp struct {
		Meta         core.PackageMeta  `json:"meta"`
		ConfigSchema []configFieldDTO  `json:"config_schema"`
		ConfigValues map[string]string `json:"config_values"`
	}

	var result []pkgResp
	for _, pkg := range packages {
		vals, _ := pkgManager.GetConfig(pkg.Meta.ID)
		schema := configFieldsToDTO(pkg.Config)
		masked := maskSecrets(pkg.Config, vals)
		result = append(result, pkgResp{
			Meta:         pkg.Meta,
			ConfigSchema: schema,
			ConfigValues: masked,
		})
	}
	if result == nil {
		result = []pkgResp{}
	}
	writeJSON(w, 200, map[string]any{"packages": result})
}

// ---------------------------------------------------------------------------
// GET /api/v1/packages/{id} — package detail with README
// ---------------------------------------------------------------------------

func (s *Server) handlePackageDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, 400, "package id is required")
		return
	}

	pkgManager := s.defaultPackageManager()
	pkg, _, err := pkgManager.LoadPackage(id)
	if err != nil {
		writeError(w, 404, "package not found: "+err.Error())
		return
	}

	vals, _ := pkgManager.GetConfig(id)
	schema := configFieldsToDTO(pkg.Config)
	masked := maskSecrets(pkg.Config, vals)

	// Read README if available.
	var readme string
	pkgDir, dirErr := core.PackagesDirFrom(s.defaultRuntime().BaseDir)
	if dirErr == nil {
		if data, readErr := os.ReadFile(filepath.Join(pkgDir, id, "README.md")); readErr == nil {
			readme = string(data)
		}
	}

	writeJSON(w, 200, map[string]any{
		"meta":          pkg.Meta,
		"config_schema": schema,
		"config_values": masked,
		"permissions":   pkg.Permissions,
		"readme":        readme,
	})
}

// ---------------------------------------------------------------------------
// POST /api/v1/packages/{id}/config — batch set config values
// ---------------------------------------------------------------------------

func (s *Server) handlePackageConfigSet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, 400, "package id is required")
		return
	}

	var req struct {
		Values map[string]string `json:"values"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Values) == 0 {
		writeError(w, 400, "values is required")
		return
	}

	// Validate all keys exist before writing any.
	pkgManager := s.defaultPackageManager()
	pkg, _, err := pkgManager.LoadPackage(id)
	if err != nil {
		writeError(w, 404, "package not found: "+err.Error())
		return
	}

	knownKeys := make(map[string]bool)
	for _, f := range pkg.Config {
		knownKeys[f.Key] = true
	}
	for k := range req.Values {
		if !knownKeys[k] {
			writeError(w, 400, "unknown config key: "+k)
			return
		}
	}

	for k, v := range req.Values {
		if err := pkgManager.SetConfig(id, k, v); err != nil {
			writeError(w, 500, "set config "+k+": "+err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/packages/{id} — uninstall a package
// ---------------------------------------------------------------------------

func (s *Server) handlePackageUninstall(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, 400, "package id is required")
		return
	}
	if err := s.defaultPackageManager().Uninstall(id); err != nil {
		writeError(w, 500, "uninstall: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// ---------------------------------------------------------------------------
// POST /api/v1/packages/install-from-registry — install by registry ID
// ---------------------------------------------------------------------------

func (s *Server) handlePackageInstallFromRegistry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string            `json:"id"`
		Config map[string]string `json:"config"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.ID == "" {
		writeError(w, 400, "id is required")
		return
	}

	cfg := s.getConfig()
	registryURL := core.DefaultRegistryURL
	if cfg.Registry.URL != "" {
		registryURL = cfg.Registry.URL
	}

	rc, err := core.NewRegistryClient(registryURL)
	if err != nil {
		writeError(w, 500, "registry unavailable: "+err.Error())
		return
	}

	entry, err := rc.FindEntry(req.ID)
	if err != nil {
		writeError(w, 404, "package not found in registry: "+err.Error())
		return
	}

	pkgManager := s.defaultPackageManager()
	pkg, err := pkgManager.InstallFromRegistry(rc, *entry)
	if err != nil {
		writeError(w, 500, "install failed: "+err.Error())
		return
	}

	// Set config values if provided.
	for k, v := range req.Config {
		if setErr := pkgManager.SetConfig(pkg.Meta.ID, k, v); setErr != nil {
			slog.Warn("post-install config set failed", "package", pkg.Meta.ID, "key", k, "error", setErr)
		}
	}

	writeJSON(w, 200, map[string]any{
		"meta":          pkg.Meta,
		"config_schema": configFieldsToDTO(pkg.Config),
	})
}

// ---------------------------------------------------------------------------
// Package API helpers
// ---------------------------------------------------------------------------

// configFieldDTO is the JSON representation with resolved type.
type configFieldDTO struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"`
	Source   string   `json:"source,omitempty"`
}

func configFieldsToDTO(fields []core.ConfigField) []configFieldDTO {
	out := make([]configFieldDTO, len(fields))
	for i, f := range fields {
		out[i] = configFieldDTO{
			Key:      f.Key,
			Label:    f.Label,
			Type:     f.ResolvedType(),
			Default:  f.Default,
			Required: f.Required,
			Options:  f.Options,
			Source:   f.Source,
		}
	}
	return out
}

func maskSecrets(fields []core.ConfigField, vals map[string]string) map[string]string {
	masked := make(map[string]string, len(vals))
	for k, v := range vals {
		masked[k] = v
	}
	for _, f := range fields {
		if f.IsSecret() {
			if v, ok := masked[f.Key]; ok && v != "" {
				masked[f.Key] = "****"
			}
		}
	}
	return masked
}
