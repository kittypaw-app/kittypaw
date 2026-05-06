package core

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Provider defaults — used by both web and CLI wizards.
const (
	ClaudeDefaultModel     = "claude-sonnet-4-6"
	OpenAIDefaultModel     = "gpt-5.5"
	GeminiDefaultModel     = "gemini-3.1-pro-preview"
	OpenRouterBaseURL      = "https://openrouter.ai/api/v1/chat/completions"
	OpenRouterDefaultModel = "qwen/qwen3-235b-a22b:free"
	OllamaDefaultBaseURL   = "http://localhost:11434/v1"
	DefaultAPIServerURL    = "https://portal.kittypaw.app"
)

var (
	claudeModelChoices = []string{
		ClaudeDefaultModel,
		"claude-opus-4-7",
		"claude-haiku-4-5-20251001",
	}
	openAIModelChoices = []string{
		OpenAIDefaultModel,
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.4-nano",
	}
	geminiModelChoices = []string{
		GeminiDefaultModel,
		"gemini-3-flash-preview",
		"gemini-3.1-flash-lite-preview",
		"gemini-2.5-pro",
		"gemini-2.5-flash",
	}
)

func ClaudeModelChoices() []string {
	return append([]string(nil), claudeModelChoices...)
}

func OpenAIModelChoices() []string {
	return append([]string(nil), openAIModelChoices...)
}

func GeminiModelChoices() []string {
	return append([]string(nil), geminiModelChoices...)
}

// DefaultWorkspacePath returns the account-scoped user workspace suggested
// during onboarding. This is separate from ConfigDir: users should be able to
// find and manage these files directly.
func DefaultWorkspacePath(accountID string) (string, error) {
	if err := ValidateAccountID(accountID); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Documents", "kittypaw", accountID), nil
}

// WizardResult holds all values collected by a setup wizard (CLI or web).
// Zero-value fields mean "not configured / keep existing".
type WizardResult struct {
	// LLM — these are internal (resolved) values, not user-facing provider names.
	// Use ResolveLLMConfig to convert user-facing choices before populating.
	LLMProvider string
	LLMAPIKey   string
	LLMModel    string
	LLMBaseURL  string
	// Additional named models for the /model command. setup keeps these
	// alongside the primary "main" model instead of dropping them on
	// reconfigure.
	LLMExtraModels  []ModelConfig
	LLMExtraAPIKeys map[string]string // keyed by model id from LLMExtraModels

	// Telegram
	TelegramBotToken string
	TelegramChatID   string

	// KakaoTalk
	KakaoEnabled    bool
	KakaoRelayWSURL string

	// Web search
	FirecrawlKey string

	// Workspace & permissions
	WorkspacePath string
	HTTPAccess    bool

	// KittyPaw API server
	APIServerURL string
}

// ResolveLLMConfig converts a user-facing provider choice into internal
// config values (provider, model, baseURL). modelName overrides the default
// for hosted providers and is required for local/Ollama. localURL is only
// consulted for local/Ollama.
func ResolveLLMConfig(provider, localURL, modelName string) (internalProvider, model, baseURL string) {
	switch strings.ToLower(provider) {
	case "claude", "anthropic":
		model := ClaudeDefaultModel
		if modelName != "" {
			model = modelName
		}
		return "anthropic", model, ""
	case "openai", "gpt":
		model := OpenAIDefaultModel
		if modelName != "" {
			model = modelName
		}
		return "openai", model, ""
	case "gemini", "google":
		model := GeminiDefaultModel
		if modelName != "" {
			model = modelName
		}
		return "gemini", model, ""
	case "openrouter":
		return "openai", OpenRouterDefaultModel, OpenRouterBaseURL
	case "local", "ollama":
		u := strings.TrimRight(localURL, "/")
		if u == "" {
			u = OllamaDefaultBaseURL
		}
		u = strings.TrimSuffix(u, "/chat/completions")
		return "openai", modelName, u + "/chat/completions"
	default:
		return provider, "", ""
	}
}

// MergeWizardSettings applies wizard results onto an existing config.
// Fields with zero values in WizardResult are left unchanged.
func MergeWizardSettings(existing *Config, w WizardResult) *Config {
	cfg := *existing
	cfg.FreeformFallback = true

	// LLM — when provider is set, apply all LLM fields unconditionally
	// (including empty values) to avoid stale keys when switching providers.
	if w.LLMProvider != "" {
		credential := wizardLLMCredential(w)
		main := ModelConfig{
			ID:         "main",
			Provider:   w.LLMProvider,
			Model:      w.LLMModel,
			Credential: credential,
			MaxTokens:  4096,
			BaseURL:    w.LLMBaseURL,
		}
		cfg.LLM.Default = "main"
		cfg.LLM.Models = mergeWizardModels(existing, main, w.LLMExtraModels)
		cfg.LLM.Provider = w.LLMProvider
		cfg.LLM.APIKey = w.LLMAPIKey
		cfg.LLM.BaseURL = w.LLMBaseURL
	}
	if w.LLMModel != "" {
		cfg.LLM.Model = w.LLMModel
	}
	if cfg.LLM.MaxTokens == 0 {
		cfg.LLM.MaxTokens = 4096
	}

	// Channels — only replace wizard-managed types when setup values exist.
	hasTelegram := w.TelegramBotToken != ""

	var kept []ChannelConfig
	for _, ch := range cfg.Channels {
		if ch.ChannelType == ChannelTelegram && hasTelegram {
			continue
		}
		if ch.ChannelType == ChannelKakaoTalk && w.KakaoEnabled {
			continue
		}
		kept = append(kept, ch)
	}

	if hasTelegram {
		kept = append(kept, ChannelConfig{
			ID:             "telegram",
			ChannelType:    ChannelTelegram,
			AllowedChatIDs: compactNonEmpty([]string{w.TelegramChatID}),
			Token:          w.TelegramBotToken,
		})
		if w.TelegramChatID != "" {
			cfg.AllowedChatIDs = []string{w.TelegramChatID}
		}
	}

	if w.KakaoEnabled {
		kept = append(kept, ChannelConfig{
			ID:          "kakao",
			ChannelType: ChannelKakaoTalk,
			// KakaoWSURL is injected at runtime from secrets
		})
	}

	cfg.Channels = kept

	// Web search backend
	if w.FirecrawlKey != "" {
		cfg.Web.FirecrawlKey = w.FirecrawlKey
		if cfg.Web.SearchBackend == "" || cfg.Web.SearchBackend == "duckduckgo" {
			cfg.Web.SearchBackend = "firecrawl"
		}
	}

	// Sandbox defaults
	if cfg.Sandbox.AllowedHosts == nil {
		cfg.Sandbox.AllowedHosts = []string{}
	}

	// Workspace → sandbox allowed paths
	if w.WorkspacePath != "" {
		cfg.Workspace.Default = "home"
		cfg.Workspace.Roots = []WorkspaceRoot{{
			Alias:  "home",
			Path:   w.WorkspacePath,
			Access: "read_write",
		}}
		cfg.Sandbox.AllowedPaths = []string{w.WorkspacePath}
	}

	return &cfg
}

func mergeWizardModels(existing *Config, main ModelConfig, extras []ModelConfig) []ModelConfig {
	models := []ModelConfig{main}
	seen := map[string]bool{main.ModelID(): true}

	if existing != nil {
		for _, m := range existing.LLM.Models {
			id := m.ModelID()
			if id == "" || id == main.ModelID() || seen[id] {
				continue
			}
			models = append(models, m)
			seen[id] = true
		}
	}

	for _, m := range extras {
		id := m.ModelID()
		if id == "" || id == main.ModelID() {
			continue
		}
		replaced := false
		for i := range models {
			if models[i].ModelID() == id {
				models[i] = m
				replaced = true
				break
			}
		}
		if !replaced {
			models = append(models, m)
		}
		seen[id] = true
	}
	return models
}

func SaveWizardSecrets(accountID string, w WizardResult, cfg *Config) error {
	secrets, err := LoadAccountSecrets(accountID)
	if err != nil {
		return err
	}
	return SaveWizardSecretsTo(secrets, w, cfg)
}

func SaveWizardSecretsTo(secrets *SecretsStore, w WizardResult, cfg *Config) error {
	if secrets == nil {
		return nil
	}
	if w.LLMProvider != "" && w.LLMAPIKey != "" {
		credential := wizardLLMCredential(w)
		if err := secrets.Set("llm/"+credential, "api_key", w.LLMAPIKey); err != nil {
			return err
		}
	}
	for _, model := range w.LLMExtraModels {
		id := model.ModelID()
		key := strings.TrimSpace(w.LLMExtraAPIKeys[id])
		if id == "" || key == "" {
			continue
		}
		if err := secrets.Set("llm/"+model.SecretID(), "api_key", key); err != nil {
			return err
		}
	}
	if w.TelegramBotToken != "" {
		if err := secrets.Set("channel/telegram", "bot_token", w.TelegramBotToken); err != nil {
			return err
		}
	}
	if w.APIServerURL != "" {
		if err := secrets.Set("kittypaw-api", "api_url", w.APIServerURL); err != nil {
			return err
		}
	}
	if w.KakaoRelayWSURL != "" {
		apiURL := w.APIServerURL
		if apiURL == "" {
			apiURL = DefaultAPIServerURL
		}
		if err := NewAPITokenManager("", secrets).SaveKakaoRelayWSURL(apiURL, w.KakaoRelayWSURL); err != nil {
			return err
		}
		if err := secrets.Set("channel/kakao", "ws_url", w.KakaoRelayWSURL); err != nil {
			return err
		}
	}
	if w.FirecrawlKey != "" {
		if err := secrets.Set("web/firecrawl", "api_key", w.FirecrawlKey); err != nil {
			return err
		}
	}
	if cfg != nil && cfg.Server.APIKey != "" {
		if err := secrets.Set("local-server", "api_key", cfg.Server.APIKey); err != nil {
			return err
		}
	}
	return nil
}

func HydrateRuntimeSecrets(cfg *Config, secrets *SecretsStore) {
	if cfg == nil || secrets == nil {
		return
	}
	if key, ok := secrets.Get("local-server", "api_key"); ok {
		cfg.Server.APIKey = key
	}
	if key, ok := secrets.Get("web/firecrawl", "api_key"); ok {
		cfg.Web.FirecrawlKey = key
	}
	if key, ok := secrets.Get("web/tavily", "api_key"); ok {
		cfg.Web.TavilyAPIKey = key
	}
	if key, ok := secrets.Get("stt/"+cfg.STT.Provider, "api_key"); ok {
		cfg.STT.APIKey = key
	}
	if model, ok := cfg.RuntimeDefaultModel(secrets); ok {
		cfg.LLM.Provider = model.Provider
		cfg.LLM.APIKey = model.APIKey
		cfg.LLM.Model = model.Model
		cfg.LLM.MaxTokens = model.MaxTokens
		cfg.LLM.BaseURL = model.BaseURL
	}
	for i := range cfg.LLM.Models {
		cfg.LLM.Models[i] = HydrateModelSecrets(cfg.LLM.Models[i], secrets)
	}
	for i := range cfg.Models {
		cfg.Models[i] = HydrateModelSecrets(cfg.Models[i], secrets)
	}
}

func wizardLLMCredential(w WizardResult) string {
	if w.LLMBaseURL == OpenRouterBaseURL {
		return "openrouter"
	}
	if w.LLMBaseURL != "" {
		return "local"
	}
	return w.LLMProvider
}

func compactNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

// WriteConfigAtomic encodes cfg as TOML and writes it to cfgPath
// via a temporary file and atomic rename.
func WriteConfigAtomic(cfg *Config, cfgPath string) error {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmpPath := cfgPath + ".tmp"
	if err := os.WriteFile(tmpPath, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write tmp config: %w", err)
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}
