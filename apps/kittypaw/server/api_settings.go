package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/go-chi/chi/v5"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

const (
	userLocalePreferenceKey = "pref.lang"
	defaultUILocale         = "en"
)

func normalizeUILocale(value string) (string, bool) {
	locale := strings.ToLower(strings.TrimSpace(value))
	if locale == "" {
		return defaultUILocale, true
	}
	switch locale {
	case "ko", "ja", "en":
		return locale, true
	default:
		return defaultUILocale, false
	}
}

func (s *Server) handleSettingsLLM(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	var body struct {
		Provider   string `json:"provider"`
		APIKey     string `json:"api_key"`
		Model      string `json:"model"`
		LocalURL   string `json:"local_url"`
		LocalModel string `json:"local_model"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	providerChoice := strings.ToLower(strings.TrimSpace(body.Provider))
	switch providerChoice {
	case "claude", "anthropic", "openai", "gpt", "gemini", "google", "openrouter":
		if strings.TrimSpace(body.APIKey) == "" {
			writeError(w, http.StatusBadRequest, "api_key is required")
			return
		}
	case "local", "ollama":
		if strings.TrimSpace(body.LocalModel) == "" {
			writeError(w, http.StatusBadRequest, "local_model is required")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid provider")
		return
	}

	model := strings.TrimSpace(body.Model)
	if providerChoice == "local" || providerChoice == "ollama" {
		model = strings.TrimSpace(body.LocalModel)
	}
	provider, resolvedModel, baseURL := core.ResolveLLMConfig(providerChoice, body.LocalURL, model)
	apiKey := strings.TrimSpace(body.APIKey)
	if providerChoice == "local" || providerChoice == "ollama" {
		apiKey = ""
	}

	wizard := core.WizardResult{
		LLMProvider: provider,
		LLMAPIKey:   apiKey,
		LLMModel:    resolvedModel,
		LLMBaseURL:  baseURL,
	}
	if err := s.applySettingsWizard(acct, wizard); err != nil {
		writeError(w, statusFromSettingsError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "provider": provider})
}

func (s *Server) handleSettingsTelegram(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	var body struct {
		BotToken string `json:"bot_token"`
		ChatID   string `json:"chat_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	body.BotToken = strings.TrimSpace(body.BotToken)
	body.ChatID = strings.TrimSpace(body.ChatID)
	if body.BotToken == "" || body.ChatID == "" {
		writeError(w, http.StatusBadRequest, "bot_token and chat_id are required")
		return
	}
	if !core.ValidateTelegramToken(body.BotToken) {
		writeError(w, http.StatusBadRequest, "invalid bot token format")
		return
	}

	wizard := core.WizardResult{
		TelegramBotToken: body.BotToken,
		TelegramChatID:   body.ChatID,
	}
	if err := s.applySettingsWizard(acct, wizard); err != nil {
		writeError(w, statusFromSettingsError(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

func (s *Server) handleSettingsLocaleGet(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if acct.Session == nil || acct.Session.Store == nil {
		writeError(w, http.StatusInternalServerError, "account store unavailable")
		return
	}

	locale := defaultUILocale
	saved := false
	value, ok, err := acct.Session.Store.GetUserContext(userLocalePreferenceKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ok {
		normalized, valid := normalizeUILocale(value)
		locale = normalized
		saved = valid && strings.TrimSpace(value) != ""
	}
	writeJSON(w, http.StatusOK, map[string]any{"locale": locale, "saved": saved})
}

func (s *Server) handleSettingsLocalePost(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	var body struct {
		Locale string `json:"locale"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	locale, valid := normalizeUILocale(body.Locale)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid locale")
		return
	}
	if acct.Session == nil || acct.Session.Store == nil {
		writeError(w, http.StatusInternalServerError, "account store unavailable")
		return
	}
	if err := acct.Session.Store.SetUserContext(userLocalePreferenceKey, locale, "user"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "locale": locale})
}

func (s *Server) handleSettingsTelegramChatID(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	result, err := s.telegramPairingChatID(r.Context(), acct.ID, strings.TrimSpace(body.Token))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleSettingsStaffRoutes(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if acct.Deps == nil || acct.Deps.Store == nil || acct.Session == nil {
		writeError(w, http.StatusInternalServerError, "account store unavailable")
		return
	}
	conversations, err := acct.Deps.Store.ListConversations(100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	base, err := core.ResolveBaseDir(acct.Session.BaseDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	staff, err := core.ListStaffRecords(base)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conversations == nil {
		conversations = []store.ConversationRecord{}
	}
	if staff == nil {
		staff = []core.StaffRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"conversations": conversations,
		"staff":         staff,
	})
}

func (s *Server) handleSettingsStaffRouteUpdate(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if acct.Deps == nil || acct.Deps.Store == nil || acct.Session == nil {
		writeError(w, http.StatusInternalServerError, "account store unavailable")
		return
	}
	var body struct {
		ConversationID string `json:"conversation_id"`
		DefaultStaffID string `json:"default_staff_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	conversationID := strings.TrimSpace(body.ConversationID)
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "conversation_id is required")
		return
	}
	staffID := strings.TrimSpace(body.DefaultStaffID)
	if staffID != "" {
		base, err := core.ResolveBaseDir(acct.Session.BaseDir)
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
	conversation, err := acct.Deps.Store.SetConversationDefaultStaff(conversationID, staffID)
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

func (s *Server) handleSettingsWorkspacesList(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	if acct.Deps == nil || acct.Deps.Store == nil {
		writeError(w, http.StatusInternalServerError, "account store unavailable")
		return
	}
	wss, err := acct.Deps.Store.ListWorkspaces()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type workspaceJSON struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Alias     string `json:"alias"`
		RootPath  string `json:"root_path"`
		CreatedAt string `json:"created_at"`
	}
	out := make([]workspaceJSON, len(wss))
	for i, ws := range wss {
		out[i] = workspaceJSON{
			ID:        ws.ID,
			Name:      ws.Name,
			Alias:     ws.Name,
			RootPath:  ws.RootPath,
			CreatedAt: ws.CreatedAt,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSettingsDirectoriesBrowse(w http.ResponseWriter, r *http.Request) {
	if _, status, err := s.settingsAccount(r); err != nil {
		writeError(w, status, err.Error())
		return
	}
	dir, browseStatus, err := settingsBrowseDirectoryPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, browseStatus, err.Error())
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsPermission(err) {
			writeError(w, http.StatusForbidden, "directory is not readable")
			return
		}
		writeError(w, http.StatusBadRequest, "directory is not readable")
		return
	}

	type directoryEntryJSON struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	out := make([]directoryEntryJSON, 0, len(entries))
	truncated := false
	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		isDir := entry.IsDir()
		if !isDir && entry.Type()&os.ModeSymlink != 0 {
			if info, statErr := os.Stat(fullPath); statErr == nil && info.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		out = append(out, directoryEntryJSON{Name: entry.Name(), Path: fullPath})
		if len(out) >= 500 {
			truncated = true
			break
		}
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      dir,
		"parent":    parent,
		"entries":   out,
		"truncated": truncated,
	})
}

func (s *Server) handleSettingsWorkspacesCreate(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}

	var body struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Alias string `json:"alias"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	ws, createStatus, err := s.createSettingsWorkspace(acct, body.Path, firstNonEmpty(body.Alias, body.Name))
	if err != nil {
		writeError(w, createStatus, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":        ws.ID,
		"name":      ws.Name,
		"alias":     ws.Name,
		"root_path": ws.RootPath,
	})
}

func (s *Server) handleSettingsWorkspacesDelete(w http.ResponseWriter, r *http.Request) {
	acct, status, err := s.settingsAccount(r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "workspace id is required")
		return
	}
	if acct.Deps == nil || acct.Deps.Store == nil || acct.Session == nil {
		writeError(w, http.StatusInternalServerError, "account workspace dependencies unavailable")
		return
	}

	if acct.Deps.LiveIndexer != nil {
		acct.Deps.LiveIndexer.RemoveWorkspace(id)
	}
	if acct.Session.Indexer != nil {
		if err := acct.Session.Indexer.Remove(id); err != nil {
			slog.Warn("settings workspace delete: index removal failed",
				"account", acct.ID, "id", id, "error", err)
		}
	}
	if err := acct.Deps.Store.DeleteWorkspace(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := acct.Session.RefreshAllowedPaths(); err != nil {
		slog.Error("settings workspace delete: cache refresh failed, denying all paths",
			"account", acct.ID, "error", err)
		acct.Session.ClearAllowedPaths()
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (s *Server) settingsAccount(r *http.Request) (*requestAccount, int, error) {
	if acct, err := s.requestAccount(r); err == nil {
		if s.isSettingsReady(acct) {
			return acct, http.StatusOK, nil
		}
		return nil, http.StatusConflict, fmt.Errorf("run kittypaw setup first")
	}

	required, err := s.localAuthRequired()
	if err != nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("read local auth store")
	}
	if required {
		return nil, http.StatusUnauthorized, fmt.Errorf("unauthorized")
	}
	if !isLocalhost(r) {
		return nil, http.StatusForbidden, fmt.Errorf("access restricted to localhost")
	}

	deps := s.activeAccountDeps()
	if len(deps) != 1 {
		return nil, http.StatusUnauthorized, fmt.Errorf("unauthorized")
	}
	acct, err := s.requestAccountFromDeps(deps[0])
	if err != nil {
		return nil, http.StatusUnauthorized, err
	}
	if !s.isSettingsReady(acct) {
		return nil, http.StatusConflict, fmt.Errorf("run kittypaw setup first")
	}
	return acct, http.StatusOK, nil
}

func (s *Server) isSettingsReady(acct *requestAccount) bool {
	if acct == nil || acct.Deps == nil || acct.Deps.Store == nil {
		return false
	}
	cfg := acct.Session.Config
	if cfg == nil && acct.Deps.Account != nil {
		cfg = acct.Deps.Account.Config
	}
	return s.isOnboardingCompletedFor(acct.Deps.Store, cfg)
}

func settingsBrowseDirectoryPath(requestedPath string) (string, int, error) {
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" || requestedPath == "~" || strings.HasPrefix(requestedPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", http.StatusInternalServerError, fmt.Errorf("home directory unavailable")
		}
		if requestedPath == "" || requestedPath == "~" {
			requestedPath = home
		} else {
			requestedPath = filepath.Join(home, strings.TrimPrefix(requestedPath, "~/"))
		}
	}
	if !filepath.IsAbs(requestedPath) {
		return "", http.StatusBadRequest, fmt.Errorf("absolute path is required")
	}
	canonical := filepath.Clean(requestedPath)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", http.StatusBadRequest, fmt.Errorf("path does not exist or is not a directory")
	}
	return canonical, http.StatusOK, nil
}

func (s *Server) createSettingsWorkspace(acct *requestAccount, requestedPath, alias string) (*store.Workspace, int, error) {
	if acct == nil || acct.Deps == nil || acct.Deps.Store == nil || acct.Session == nil {
		return nil, http.StatusInternalServerError, fmt.Errorf("account workspace dependencies unavailable")
	}

	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return nil, http.StatusBadRequest, fmt.Errorf("absolute path is required")
	}

	canonical := filepath.Clean(requestedPath)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, http.StatusBadRequest, fmt.Errorf("path does not exist or is not a directory")
	}

	alias = strings.TrimSpace(alias)
	if alias == "" {
		alias = filepath.Base(canonical)
	}
	ws := &store.Workspace{
		ID:       fmt.Sprintf("ws-%d", time.Now().UnixNano()),
		Name:     alias,
		RootPath: canonical,
	}
	if err := acct.Deps.Store.SaveWorkspace(ws); err != nil {
		return nil, http.StatusConflict, fmt.Errorf("workspace already registered or path conflict")
	}
	if err := acct.Session.RefreshAllowedPaths(); err != nil {
		slog.Error("settings workspace create: cache refresh failed",
			"account", acct.ID, "error", err)
	}

	if acct.Session.Indexer != nil {
		live := acct.Deps.LiveIndexer
		go func() {
			if _, err := acct.Session.Indexer.Index(context.Background(), ws.ID, canonical); err != nil {
				slog.Warn("settings workspace create: indexing failed",
					"account", acct.ID, "id", ws.ID, "error", err)
			}
			if live != nil {
				if err := live.AddWorkspace(ws.ID, canonical); err != nil {
					slog.Warn("settings workspace create: live indexer add failed",
						"account", acct.ID, "id", ws.ID, "error", err)
				}
			}
		}()
	}
	return ws, http.StatusCreated, nil
}

func (s *Server) applySettingsWizard(acct *requestAccount, wizard core.WizardResult) error {
	base, cfgPath, err := settingsBaseConfig(acct)
	if err != nil {
		return err
	}
	merged := core.MergeWizardSettings(base, wizard)

	accountID := acct.ID
	s.accountMu.Lock()

	if err := s.validateAccountConfigUpdateWithKakaoAPIURLLocked(accountID, merged, wizard.APIServerURL); err != nil {
		s.accountMu.Unlock()
		return err
	}
	if err := core.SaveWizardSecrets(accountID, wizard, merged); err != nil {
		s.accountMu.Unlock()
		return err
	}
	if err := core.WriteConfigAtomic(merged, cfgPath); err != nil {
		s.accountMu.Unlock()
		return err
	}
	if wizard.APIServerURL != "" {
		s.saveSetupAPIServerURL(accountID, wizard.APIServerURL)
	}
	oldScheduler, err := s.applyAccountConfigLocked(accountID, merged)
	if err != nil {
		s.accountMu.Unlock()
		return err
	}
	if s.spawner != nil {
		if err := s.spawner.Reconcile(accountID, merged.Channels); err != nil {
			s.accountMu.Unlock()
			if oldScheduler != nil {
				oldScheduler.Wait()
			}
			return err
		}
	}
	s.accountMu.Unlock()
	if oldScheduler != nil {
		oldScheduler.Wait()
	}
	return nil
}

func settingsBaseConfig(acct *requestAccount) (*core.Config, string, error) {
	cfgPath, err := core.ConfigPathForAccount(acct.ID)
	if err != nil {
		return nil, "", err
	}

	cfg := core.DefaultConfig()
	data, err := os.ReadFile(cfgPath)
	switch {
	case err == nil:
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return nil, "", fmt.Errorf("existing config.toml has syntax errors: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		if acct.Session != nil && acct.Session.Config != nil {
			cfg = *acct.Session.Config
		} else if acct.Deps != nil && acct.Deps.Account != nil && acct.Deps.Account.Config != nil {
			cfg = *acct.Deps.Account.Config
		}
	default:
		return nil, "", err
	}

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return nil, "", err
	}
	return &cfg, cfgPath, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func statusFromSettingsError(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "validation"):
		return http.StatusConflict
	case strings.Contains(msg, "syntax"):
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
