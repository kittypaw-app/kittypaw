package server

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/jinto/kittypaw/channel"
	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/store"
)

// ---------------------------------------------------------------------------
// GET /api/bootstrap
// ---------------------------------------------------------------------------

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	required, err := s.localAuthRequired()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read local auth store")
		return
	}

	apiKey, ok := s.browserAPIToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !isLocalhost(r) && (!required || apiKey == "") {
		writeError(w, http.StatusForbidden, "bootstrap only allowed from localhost or an authenticated default account session")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"api_key": apiKey,
		"ws_url":  websocketURL(r, "/ws"),
	})
}

type setupAccountContext struct {
	ID     string
	Config *core.Config
	Store  *store.Store
}

func (s *Server) setupAccount(r *http.Request) (*setupAccountContext, error) {
	required, err := s.localAuthRequired()
	if err != nil {
		return nil, err
	}
	if required {
		acct, err := s.requestAccount(r)
		if err != nil {
			return nil, err
		}
		cfg := acct.Session.Config
		if cfg == nil && acct.Deps != nil && acct.Deps.Account != nil {
			cfg = acct.Deps.Account.Config
		}
		if acct.Deps == nil || acct.Deps.Store == nil || cfg == nil {
			return nil, fmt.Errorf("account setup dependencies unavailable")
		}
		if cfgPath, err := core.ConfigPathForAccount(acct.ID); err == nil {
			if loaded, loadErr := core.LoadConfig(cfgPath); loadErr == nil {
				cfg = loaded
				if secrets, secretErr := core.LoadAccountSecrets(acct.ID); secretErr == nil {
					core.HydrateRuntimeSecrets(cfg, secrets)
				}
			}
		}
		return &setupAccountContext{ID: acct.ID, Config: cfg, Store: acct.Deps.Store}, nil
	}

	s.configMu.RLock()
	cfgCopy := *s.config
	s.configMu.RUnlock()
	if secrets, secretErr := core.LoadAccountSecrets(s.defaultAccountID()); secretErr == nil {
		core.HydrateRuntimeSecrets(&cfgCopy, secrets)
	}
	return &setupAccountContext{
		ID:     s.defaultAccountID(),
		Config: &cfgCopy,
		Store:  s.store,
	}, nil
}

// ---------------------------------------------------------------------------
// GET /api/setup/status
// ---------------------------------------------------------------------------

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	completed := s.isOnboardingCompletedFor(acct.Store, acct.Config)
	cfg := acct.Config

	// Determine existing LLM provider from live config.
	var existingProvider *string
	if cfg.LLM.BaseURL == core.OpenRouterBaseURL {
		p := "openrouter"
		existingProvider = &p
	} else if cfg.LLM.BaseURL != "" {
		p := "local"
		existingProvider = &p
	} else if cfg.LLM.APIKey != "" && cfg.LLM.Provider != "" {
		p := cfg.LLM.Provider
		existingProvider = &p
	}

	// Check configured channels.
	hasTelegram := false
	var telegramChatID *string
	hasKakao := false

	for _, ch := range cfg.Channels {
		switch ch.ChannelType {
		case core.ChannelTelegram:
			hasTelegram = true
		case core.ChannelKakaoTalk:
			hasKakao = true
		}
	}

	// Also check pending setup state (wizard in progress).
	if !hasTelegram {
		if v, ok, _ := acct.Store.GetUserContext("setup:telegram_bot_token"); ok && v != "" {
			hasTelegram = true
		}
	}
	if hasTelegram {
		if v, ok, _ := acct.Store.GetUserContext("setup:telegram_chat_id"); ok && v != "" {
			masked := maskValue(v)
			telegramChatID = &masked
		}
	}
	defaultWorkspacePath := ""
	if p, err := core.DefaultWorkspacePath(acct.ID); err == nil {
		defaultWorkspacePath = p
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"completed":              completed,
		"existing_provider":      existingProvider,
		"has_telegram":           hasTelegram,
		"telegram_chat_id":       telegramChatID,
		"has_kakao":              hasKakao,
		"kakao_available":        true,
		"default_workspace_path": defaultWorkspacePath,
	})
}

// ---------------------------------------------------------------------------
// POST /api/setup/llm
// ---------------------------------------------------------------------------

func (s *Server) handleSetupLlm(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Provider   string `json:"provider"`
		APIKey     string `json:"api_key"`
		LocalURL   string `json:"local_url"`
		LocalModel string `json:"local_model"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	providerChoice := strings.ToLower(body.Provider)

	// Validate provider-specific requirements.
	switch providerChoice {
	case "claude", "anthropic":
		if body.APIKey == "" {
			writeError(w, http.StatusBadRequest, "api_key is required for Claude")
			return
		}
	case "openai", "gpt":
		if body.APIKey == "" {
			writeError(w, http.StatusBadRequest, "api_key is required for OpenAI")
			return
		}
	case "gemini", "google":
		if body.APIKey == "" {
			writeError(w, http.StatusBadRequest, "api_key is required for Gemini")
			return
		}
	case "openrouter":
		if body.APIKey == "" {
			writeError(w, http.StatusBadRequest, "api_key is required for OpenRouter")
			return
		}
	case "local":
		if body.LocalURL == "" || body.LocalModel == "" {
			writeError(w, http.StatusBadRequest, "local_url and local_model are required")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "invalid provider")
		return
	}

	provider, model, baseURL := core.ResolveLLMConfig(providerChoice, body.LocalURL, body.LocalModel)
	apiKey := body.APIKey
	if providerChoice == "local" {
		apiKey = ""
	}

	_ = acct.Store.SetUserContext("setup:llm_provider", provider, "setup")
	_ = acct.Store.SetUserContext("setup:llm_api_key", apiKey, "setup")
	_ = acct.Store.SetUserContext("setup:llm_model", model, "setup")
	_ = acct.Store.SetUserContext("setup:llm_base_url", baseURL, "setup")

	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "provider": body.Provider})
}

// ---------------------------------------------------------------------------
// POST /api/setup/telegram
// ---------------------------------------------------------------------------

func (s *Server) handleSetupTelegram(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		BotToken string `json:"bot_token"`
		ChatID   string `json:"chat_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.BotToken == "" || body.ChatID == "" {
		writeError(w, http.StatusBadRequest, "bot_token and chat_id are required")
		return
	}
	if !core.ValidateTelegramToken(body.BotToken) {
		writeError(w, http.StatusBadRequest, "invalid bot token format")
		return
	}

	wizard := wizardResultFromStore(acct.Store)
	wizard.TelegramBotToken = body.BotToken
	wizard.TelegramChatID = body.ChatID
	proposed := core.MergeWizardSettings(acct.Config, wizard)
	if err := s.validateAccountConfigUpdateWithKakaoAPIURL(acct.ID, proposed, wizard.APIServerURL); err != nil {
		slog.Error("setup: telegram rejected", "account", acct.ID, "error", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	// Immediately spawn the Telegram channel so the user gets instant
	// feedback during unauthenticated local onboarding — no reload required
	// (AC3). Authenticated multi-account setup defers spawning until
	// /complete, where validation, live config update, and reconcile are
	// serialized with AddAccount/Reload.
	authRequired, authErr := s.localAuthRequired()
	if authErr != nil {
		writeError(w, http.StatusInternalServerError, "read local auth store")
		return
	}

	_ = acct.Store.SetUserContext("setup:telegram_bot_token", body.BotToken, "setup")
	_ = acct.Store.SetUserContext("setup:telegram_chat_id", body.ChatID, "setup")

	if s.spawner != nil && !authRequired {
		chCfg := core.ChannelConfig{ChannelType: core.ChannelTelegram, Token: body.BotToken}
		accountID := acct.ID
		ch, err := channel.FromConfig(accountID, chCfg)
		if err != nil {
			slog.Warn("setup: telegram channel create failed", "error", err)
		} else if err := s.spawner.TrySpawn(accountID, ch, chCfg); err != nil {
			slog.Warn("setup: telegram channel spawn failed", "error", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"saved": true})
}

// ---------------------------------------------------------------------------
// POST /api/setup/telegram/chat-id
// ---------------------------------------------------------------------------

func (s *Server) handleSetupTelegramChatID(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Token == "" {
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

// ---------------------------------------------------------------------------
// POST /api/setup/kakao/register
// ---------------------------------------------------------------------------

func (s *Server) handleSetupKakaoRegister(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	apiURL, _, _ := acct.Store.GetUserContext("setup:api_server_url")
	if apiURL == "" {
		apiURL = core.DefaultAPIServerURL
	}
	apiURL = strings.TrimRight(apiURL, "/")

	accountID := acct.ID
	secrets, err := core.LoadAccountSecrets(accountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load secrets: "+err.Error())
		return
	}
	mgr := core.NewAPITokenManager("", secrets)

	relayURL, ok := mgr.LoadKakaoRelayBaseURL(apiURL)
	if !ok || relayURL == "" {
		writeError(w, http.StatusServiceUnavailable, "Kakao relay URL not configured — login to the API server first")
		return
	}

	reg, err := core.RegisterRelaySession(relayURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "relay register: "+err.Error())
		return
	}

	wsURL := core.WSURLFromRelay(relayURL, reg.Token)
	if err := mgr.SaveKakaoRelayWSURL(apiURL, wsURL); err != nil {
		writeError(w, http.StatusInternalServerError, "save kakao ws url: "+err.Error())
		return
	}
	if err := secrets.Set("channel/kakao", "ws_url", wsURL); err != nil {
		writeError(w, http.StatusInternalServerError, "save kakao channel secret: "+err.Error())
		return
	}

	_ = acct.Store.SetUserContext("setup:kakao_relay_base", relayURL, "setup")
	_ = acct.Store.SetUserContext("setup:kakao_relay_token", reg.Token, "setup")
	// Persist apiURL so generateConfig writes it to the bare "kittypaw-api"
	// namespace that InjectKakaoWSURL reads at server start time — without this,
	// users who only complete the Kakao step end up with an unroutable channel.
	_ = acct.Store.SetUserContext("setup:api_server_url", apiURL, "setup")

	writeJSON(w, http.StatusOK, map[string]any{
		"pair_code":   reg.PairCode,
		"channel_url": reg.ChannelURL,
	})
}

// ---------------------------------------------------------------------------
// GET /api/setup/kakao/pair-status
// ---------------------------------------------------------------------------

func (s *Server) handleSetupKakaoPairStatus(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Already configured: no further pairing needed.
	for _, ch := range acct.Config.Channels {
		if ch.ChannelType == core.ChannelKakaoTalk {
			writeJSON(w, http.StatusOK, map[string]any{"paired": true})
			return
		}
	}

	// Wizard in progress: ask the relay whether the user has completed pairing.
	relayBase, _, _ := acct.Store.GetUserContext("setup:kakao_relay_base")
	token, _, _ := acct.Store.GetUserContext("setup:kakao_relay_token")
	if relayBase == "" || token == "" {
		writeJSON(w, http.StatusOK, map[string]any{"paired": false})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"paired": core.CheckRelayPairStatus(relayBase, token),
	})
}

// ---------------------------------------------------------------------------
// POST /api/setup/api-server
// ---------------------------------------------------------------------------

func (s *Server) handleSetupAPIServer(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	body.URL = strings.TrimRight(body.URL, "/")
	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	_ = acct.Store.SetUserContext("setup:api_server_url", body.URL, "setup")
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "url": body.URL})
}

// ---------------------------------------------------------------------------
// POST /api/setup/workspace
// ---------------------------------------------------------------------------

func (s *Server) handleSetupWorkspace(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	requestedPath := filepath.Clean(body.Path)
	if body.Path == "" || !filepath.IsAbs(requestedPath) {
		writeError(w, http.StatusBadRequest, "absolute path is required")
		return
	}

	info, err := os.Stat(requestedPath)
	if err != nil || !info.IsDir() {
		defaultWorkspacePath, defaultErr := core.DefaultWorkspacePath(acct.ID)
		if defaultErr != nil || requestedPath != filepath.Clean(defaultWorkspacePath) || !os.IsNotExist(err) {
			writeError(w, http.StatusBadRequest, "path does not exist or is not a directory")
			return
		}
		if mkdirErr := os.MkdirAll(requestedPath, 0o755); mkdirErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to create default workspace directory")
			return
		}
		info, err = os.Stat(requestedPath)
		if err != nil || !info.IsDir() {
			writeError(w, http.StatusInternalServerError, "failed to create default workspace directory")
			return
		}
	}

	canonical, err := filepath.EvalSymlinks(requestedPath)
	if err != nil {
		canonical = requestedPath
	}

	_ = acct.Store.SetUserContext("setup:workspace_path", canonical, "setup")
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "path": canonical})
}

// ---------------------------------------------------------------------------
// POST /api/setup/http-access
// ---------------------------------------------------------------------------

func (s *Server) handleSetupHttpAccess(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := acct.Store.GrantCapability("http"); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"granted": true})
}

// ---------------------------------------------------------------------------
// POST /api/setup/complete
// ---------------------------------------------------------------------------

func (s *Server) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.isOnboardingCompletedFor(acct.Store, acct.Config) {
		writeError(w, http.StatusConflict, "already completed")
		return
	}

	cfg, cfgPath, wizard, err := s.mergedSetupConfigFor(acct.ID, acct.Store)
	if err != nil {
		slog.Error("setup: merge config failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to generate config: "+err.Error())
		return
	}

	accountID := acct.ID
	s.accountMu.Lock()

	if err := s.validateAccountConfigUpdateWithKakaoAPIURLLocked(accountID, cfg, wizard.APIServerURL); err != nil {
		s.accountMu.Unlock()
		slog.Error("setup: complete rejected", "account", accountID, "error", err)
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	if err := core.WriteConfigAtomic(cfg, cfgPath); err != nil {
		s.accountMu.Unlock()
		slog.Error("setup: write config failed", "account", accountID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to write config: "+err.Error())
		return
	}
	if wizard.APIServerURL != "" {
		s.saveSetupAPIServerURL(accountID, wizard.APIServerURL)
	}

	oldScheduler, err := s.applyAccountConfigLocked(accountID, cfg)
	if err != nil {
		s.accountMu.Unlock()
		slog.Error("setup: config apply failed", "account", accountID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to apply config: "+err.Error())
		return
	}
	slog.Info("setup: config reloaded after onboarding", "account", accountID)

	// Reconcile channels with the generated config. Reconcile is held under
	// accountMu, matching handleReload/AddAccount's validate→swap→reconcile
	// serialization contract.
	if s.spawner != nil {
		if rErr := s.spawner.Reconcile(accountID, cfg.Channels); rErr != nil {
			slog.Warn("setup: channel reconcile partial failure", "error", rErr)
		}
	}
	_ = acct.Store.SetUserContext("onboarding_completed", "true", "system")
	s.accountMu.Unlock()
	if oldScheduler != nil {
		oldScheduler.Wait()
	}

	writeJSON(w, http.StatusOK, map[string]any{"completed": true})
}

// ---------------------------------------------------------------------------
// POST /api/setup/reset
// ---------------------------------------------------------------------------

func (s *Server) handleSetupReset(w http.ResponseWriter, r *http.Request) {
	if !isLocalhost(r) {
		writeError(w, http.StatusForbidden, "reset only allowed from localhost")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !isLocalhostOrigin(origin) {
		writeError(w, http.StatusForbidden, "cross-origin reset not allowed")
		return
	}

	acct, err := s.setupAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	_ = acct.Store.SetUserContext("onboarding_completed", "false", "system")
	writeJSON(w, http.StatusOK, map[string]any{"reset": true})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (s *Server) isOnboardingCompleted() bool {
	return s.isOnboardingCompletedFor(s.store, s.getConfig())
}

func (s *Server) isOnboardingCompletedFor(st *store.Store, cfg *core.Config) bool {
	v, ok, _ := st.GetUserContext("onboarding_completed")
	if ok && v == "true" {
		return true
	}
	// CLI `kittypaw setup` writes config.toml but doesn't set the DB flag.
	// Treat a configured LLM as onboarding complete.
	return cfg != nil && (cfg.LLM.APIKey != "" || cfg.LLM.BaseURL != "")
}

// requireOnboardingIncomplete blocks mutating setup endpoints after
// onboarding is complete.
func (s *Server) requireOnboardingIncomplete(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acct, err := s.setupAccount(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if s.isOnboardingCompletedFor(acct.Store, acct.Config) {
			writeError(w, http.StatusForbidden, "setup already completed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireSetupMutationAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		required, err := s.localAuthRequired()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "read local auth store")
			return
		}
		if required {
			if _, ok := s.webSessionAccountID(r); !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !isLocalhost(r) {
			writeError(w, http.StatusForbidden, "access restricted to localhost")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalhost(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isLocalhostOrigin(origin string) bool {
	return strings.HasPrefix(origin, "http://localhost") ||
		strings.HasPrefix(origin, "http://127.0.0.1") ||
		strings.HasPrefix(origin, "https://localhost") ||
		strings.HasPrefix(origin, "https://127.0.0.1")
}

// requireLocalhost blocks requests that don't originate from loopback.
func (s *Server) requireLocalhost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLocalhost(r) {
			writeError(w, http.StatusForbidden, "access restricted to localhost")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func maskValue(v string) string {
	if len(v) <= 4 {
		return "***"
	}
	return "***" + v[len(v)-4:]
}

func wizardResultFromStore(st *store.Store) core.WizardResult {
	var w core.WizardResult
	if v, ok, _ := st.GetUserContext("setup:llm_provider"); ok {
		w.LLMProvider = v
	}
	if v, ok, _ := st.GetUserContext("setup:llm_api_key"); ok {
		w.LLMAPIKey = v
	}
	if v, ok, _ := st.GetUserContext("setup:llm_model"); ok {
		w.LLMModel = v
	}
	if v, ok, _ := st.GetUserContext("setup:llm_base_url"); ok {
		w.LLMBaseURL = v
	}
	if v, ok, _ := st.GetUserContext("setup:telegram_bot_token"); ok {
		w.TelegramBotToken = v
	}
	if v, ok, _ := st.GetUserContext("setup:telegram_chat_id"); ok {
		w.TelegramChatID = v
	}
	if v, ok, _ := st.GetUserContext("setup:workspace_path"); ok {
		w.WorkspacePath = v
	}
	if v, ok, _ := st.GetUserContext("setup:api_server_url"); ok {
		w.APIServerURL = v
	}
	// Kakao has no toggle field in the web wizard: a successful /setup/kakao/register
	// leaves a relay token in the store. Treat that as the "enabled" signal so
	// MergeWizardSettings includes the channel in the final config.
	if v, ok, _ := st.GetUserContext("setup:kakao_relay_token"); ok && v != "" {
		w.KakaoEnabled = true
	}
	return w
}

func (s *Server) mergedSetupConfigFor(accountID string, st *store.Store) (*core.Config, string, core.WizardResult, error) {
	cfgPath, err := core.ConfigPathForAccount(accountID)
	if err != nil {
		return nil, "", core.WizardResult{}, err
	}

	cfg := core.DefaultConfig()
	if data, readErr := os.ReadFile(cfgPath); readErr == nil {
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return nil, "", core.WizardResult{}, fmt.Errorf("existing config.toml has syntax errors: %w", err)
		}
	}

	w := wizardResultFromStore(st)
	merged := core.MergeWizardSettings(&cfg, w)
	return merged, cfgPath, w, nil
}

func (s *Server) saveSetupAPIServerURL(accountID, apiServerURL string) {
	// Save API server URL to secrets for package source bindings.
	// Open the per-account store fresh on every call: a long-lived
	// reference would carry stale in-memory state between web setup
	// steps (e.g. /kakao/register followed by /complete) and the
	// second Set's persist would overwrite the first step's writes.
	if apiServerURL != "" {
		if secrets, err := core.LoadAccountSecrets(accountID); err == nil {
			_ = secrets.Set("kittypaw-api", "api_url", apiServerURL)
		}
	}
}
