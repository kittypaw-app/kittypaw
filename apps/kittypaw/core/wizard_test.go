package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestResolveLLMConfig_Anthropic(t *testing.T) {
	p, m, u := ResolveLLMConfig("anthropic", "", "")
	if p != "anthropic" {
		t.Errorf("provider = %q, want anthropic", p)
	}
	if m != ClaudeDefaultModel {
		t.Errorf("model = %q, want %q", m, ClaudeDefaultModel)
	}
	if u != "" {
		t.Errorf("baseURL = %q, want empty", u)
	}
}

func TestResolveLLMConfig_OpenRouter(t *testing.T) {
	p, m, u := ResolveLLMConfig("openrouter", "", "")
	if p != "openai" {
		t.Errorf("provider = %q, want openai", p)
	}
	if m != OpenRouterDefaultModel {
		t.Errorf("model = %q, want %q", m, OpenRouterDefaultModel)
	}
	if u != OpenRouterBaseURL {
		t.Errorf("baseURL = %q, want %q", u, OpenRouterBaseURL)
	}
}

func TestResolveLLMConfig_OpenAI(t *testing.T) {
	p, m, u := ResolveLLMConfig("openai", "", "")
	if p != "openai" {
		t.Errorf("provider = %q, want openai", p)
	}
	if m != OpenAIDefaultModel {
		t.Errorf("model = %q, want %q", m, OpenAIDefaultModel)
	}
	if u != "" {
		t.Errorf("baseURL = %q, want empty", u)
	}
}

func TestResolveLLMConfig_Gemini(t *testing.T) {
	p, m, u := ResolveLLMConfig("gemini", "", "")
	if p != "gemini" {
		t.Errorf("provider = %q, want gemini", p)
	}
	if m != GeminiDefaultModel {
		t.Errorf("model = %q, want %q", m, GeminiDefaultModel)
	}
	if u != "" {
		t.Errorf("baseURL = %q, want empty", u)
	}
}

func TestHostedModelChoicesStartWithDefaults(t *testing.T) {
	if got := ClaudeModelChoices()[0]; got != ClaudeDefaultModel {
		t.Errorf("ClaudeModelChoices()[0] = %q, want %q", got, ClaudeDefaultModel)
	}
	if got := OpenAIModelChoices()[0]; got != OpenAIDefaultModel {
		t.Errorf("OpenAIModelChoices()[0] = %q, want %q", got, OpenAIDefaultModel)
	}
	if got := GeminiModelChoices()[0]; got != GeminiDefaultModel {
		t.Errorf("GeminiModelChoices()[0] = %q, want %q", got, GeminiDefaultModel)
	}
}

func TestResolveLLMConfig_Local(t *testing.T) {
	p, m, u := ResolveLLMConfig("local", "http://myhost:1234/v1", "llama3")
	if p != "openai" {
		t.Errorf("provider = %q, want openai", p)
	}
	if m != "llama3" {
		t.Errorf("model = %q, want llama3", m)
	}
	want := "http://myhost:1234/v1/chat/completions"
	if u != want {
		t.Errorf("baseURL = %q, want %q", u, want)
	}
}

func TestResolveLLMConfig_LocalDefaultURL(t *testing.T) {
	_, _, u := ResolveLLMConfig("local", "", "phi3")
	want := OllamaDefaultBaseURL + "/chat/completions"
	if u != want {
		t.Errorf("baseURL = %q, want %q", u, want)
	}
}

func TestResolveLLMConfig_LocalFullURL(t *testing.T) {
	// User pastes full URL with /chat/completions — must not double-append.
	_, _, u := ResolveLLMConfig("local", "http://myhost:1234/v1/chat/completions", "llama3")
	want := "http://myhost:1234/v1/chat/completions"
	if u != want {
		t.Errorf("baseURL = %q, want %q", u, want)
	}
}

func TestResolveLLMConfig_Claude(t *testing.T) {
	p, _, _ := ResolveLLMConfig("claude", "", "")
	if p != "anthropic" {
		t.Errorf("provider = %q, want anthropic", p)
	}
}

func TestMergeWizardSettings_LLMAnthropic(t *testing.T) {
	base := DefaultConfig()
	w := WizardResult{
		LLMProvider: "anthropic",
		LLMAPIKey:   "sk-test",
		LLMModel:    "claude-sonnet-4-20250514",
	}
	cfg := MergeWizardSettings(&base, w)
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider = %q", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Errorf("LLM.APIKey = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.BaseURL != "" {
		t.Errorf("LLM.BaseURL = %q, want empty", cfg.LLM.BaseURL)
	}
	if !cfg.FreeformFallback {
		t.Error("FreeformFallback should be true")
	}
}

func TestMergeWizardSettings_ProviderSwitchClearsAPIKey(t *testing.T) {
	// Regression: switching from Claude to local must clear the API key.
	base := DefaultConfig()
	base.LLM.Provider = "anthropic"
	base.LLM.APIKey = "old-claude-key"
	base.LLM.BaseURL = ""

	w := WizardResult{
		LLMProvider: "openai",
		LLMAPIKey:   "", // local provider → empty key
		LLMModel:    "llama3",
		LLMBaseURL:  "http://localhost:11434/v1/chat/completions",
	}
	cfg := MergeWizardSettings(&base, w)
	if cfg.LLM.APIKey != "" {
		t.Errorf("LLM.APIKey = %q, want empty (stale key should be cleared)", cfg.LLM.APIKey)
	}
}

func TestMergeWizardSettings_LLMOpenRouter(t *testing.T) {
	base := DefaultConfig()
	w := WizardResult{
		LLMProvider: "openai",
		LLMAPIKey:   "or-key",
		LLMModel:    OpenRouterDefaultModel,
		LLMBaseURL:  OpenRouterBaseURL,
	}
	cfg := MergeWizardSettings(&base, w)
	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM.Provider = %q", cfg.LLM.Provider)
	}
	if cfg.LLM.BaseURL != OpenRouterBaseURL {
		t.Errorf("LLM.BaseURL = %q", cfg.LLM.BaseURL)
	}
}

func TestMergeWizardSettings_PreservesExtraModelsOnReconfigure(t *testing.T) {
	base := DefaultConfig()
	base.LLM.Default = "main"
	base.LLM.Models = []ModelConfig{
		{
			ID:         "main",
			Provider:   "anthropic",
			Model:      ClaudeDefaultModel,
			Credential: "anthropic",
			MaxTokens:  4096,
		},
		{
			ID:         "groq-qwen",
			Provider:   "openai",
			Model:      "qwen/qwen3-32b",
			Credential: "groq",
			BaseURL:    "https://api.groq.com/openai/v1/chat/completions",
			MaxTokens:  4096,
		},
	}

	cfg := MergeWizardSettings(&base, WizardResult{
		LLMProvider: "openai",
		LLMAPIKey:   "sk-openai",
		LLMModel:    OpenAIDefaultModel,
	})

	if cfg.LLM.Default != "main" {
		t.Fatalf("LLM.Default = %q, want main", cfg.LLM.Default)
	}
	if len(cfg.LLM.Models) != 2 {
		t.Fatalf("models len = %d, want 2: %#v", len(cfg.LLM.Models), cfg.LLM.Models)
	}
	if cfg.LLM.Models[0].ID != "main" || cfg.LLM.Models[0].Model != OpenAIDefaultModel {
		t.Fatalf("main model = %#v", cfg.LLM.Models[0])
	}
	if cfg.LLM.Models[1].ID != "groq-qwen" || cfg.LLM.Models[1].BaseURL == "" {
		t.Fatalf("extra model not preserved: %#v", cfg.LLM.Models[1])
	}
}

func TestMergeWizardSettings_AppendsExtraModels(t *testing.T) {
	base := DefaultConfig()
	cfg := MergeWizardSettings(&base, WizardResult{
		LLMProvider: "anthropic",
		LLMAPIKey:   "sk-anthropic",
		LLMModel:    ClaudeDefaultModel,
		LLMExtraModels: []ModelConfig{{
			ID:         "openai-fast",
			Provider:   "openai",
			Model:      OpenAIDefaultModel,
			Credential: "openai",
			MaxTokens:  4096,
		}},
	})

	if len(cfg.LLM.Models) != 2 {
		t.Fatalf("models len = %d, want 2: %#v", len(cfg.LLM.Models), cfg.LLM.Models)
	}
	if cfg.LLM.Models[1].ID != "openai-fast" {
		t.Fatalf("extra model = %#v", cfg.LLM.Models[1])
	}
}

func TestSaveWizardSecretsTo_ExtraModelAPIKeys(t *testing.T) {
	secrets := &SecretsStore{path: filepath.Join(t.TempDir(), "secrets.json"), data: make(map[string]map[string]string)}
	cfg := DefaultConfig()
	err := SaveWizardSecretsTo(secrets, WizardResult{
		LLMProvider: "anthropic",
		LLMAPIKey:   "sk-anthropic",
		LLMModel:    ClaudeDefaultModel,
		LLMExtraModels: []ModelConfig{{
			ID:         "groq-qwen",
			Provider:   "openai",
			Model:      "qwen/qwen3-32b",
			Credential: "groq-qwen",
			BaseURL:    "https://api.groq.com/openai/v1/chat/completions",
		}},
		LLMExtraAPIKeys: map[string]string{"groq-qwen": "gsk-test"},
	}, &cfg)
	if err != nil {
		t.Fatalf("SaveWizardSecretsTo: %v", err)
	}
	if got, ok := secrets.Get("llm/anthropic", "api_key"); !ok || got != "sk-anthropic" {
		t.Fatalf("main key = (%q, %v)", got, ok)
	}
	if got, ok := secrets.Get("llm/groq-qwen", "api_key"); !ok || got != "gsk-test" {
		t.Fatalf("extra key = (%q, %v)", got, ok)
	}
}

func TestMergeWizardSettings_Telegram(t *testing.T) {
	base := DefaultConfig()
	base.Channels = []ChannelConfig{
		{ChannelType: ChannelWeb},
	}
	w := WizardResult{
		TelegramBotToken: "123:abc",
		TelegramChatID:   "99999",
	}
	cfg := MergeWizardSettings(&base, w)

	if len(cfg.Channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(cfg.Channels))
	}
	// Web channel preserved.
	if cfg.Channels[0].ChannelType != ChannelWeb {
		t.Errorf("channels[0] = %q, want web", cfg.Channels[0].ChannelType)
	}
	// Telegram added.
	if cfg.Channels[1].ChannelType != ChannelTelegram {
		t.Errorf("channels[1] = %q, want telegram", cfg.Channels[1].ChannelType)
	}
	if cfg.Channels[1].Token != "123:abc" {
		t.Errorf("telegram token = %q", cfg.Channels[1].Token)
	}
	if len(cfg.AllowedChatIDs) != 1 || cfg.AllowedChatIDs[0] != "99999" {
		t.Errorf("AllowedChatIDs = %v", cfg.AllowedChatIDs)
	}
}

func TestMergeWizardSettings_TelegramReplaces(t *testing.T) {
	base := DefaultConfig()
	base.Channels = []ChannelConfig{
		{ChannelType: ChannelTelegram, Token: "old-token"},
	}
	w := WizardResult{TelegramBotToken: "new-token"}
	cfg := MergeWizardSettings(&base, w)

	if len(cfg.Channels) != 1 {
		t.Fatalf("channels = %d, want 1", len(cfg.Channels))
	}
	if cfg.Channels[0].Token != "new-token" {
		t.Errorf("token = %q, want new-token", cfg.Channels[0].Token)
	}
}

func TestMergeWizardSettings_EmptyPreservesExisting(t *testing.T) {
	base := DefaultConfig()
	base.LLM.Provider = "anthropic"
	base.LLM.APIKey = "existing-key"
	base.LLM.Model = "existing-model"
	base.LLM.BaseURL = "existing-url"
	base.Sandbox.AllowedPaths = []string{"/old/path"}

	w := WizardResult{} // all zeros
	cfg := MergeWizardSettings(&base, w)

	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("LLM.Provider = %q, want anthropic", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "existing-key" {
		t.Errorf("LLM.APIKey = %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "existing-model" {
		t.Errorf("LLM.Model = %q", cfg.LLM.Model)
	}
	// BaseURL preserved when provider not set in wizard.
	if cfg.LLM.BaseURL != "existing-url" {
		t.Errorf("LLM.BaseURL = %q, want existing-url", cfg.LLM.BaseURL)
	}
	if len(cfg.Sandbox.AllowedPaths) != 1 || cfg.Sandbox.AllowedPaths[0] != "/old/path" {
		t.Errorf("AllowedPaths = %v", cfg.Sandbox.AllowedPaths)
	}
}

func TestMergeWizardSettings_Workspace(t *testing.T) {
	base := DefaultConfig()
	w := WizardResult{WorkspacePath: "/home/user/projects"}
	cfg := MergeWizardSettings(&base, w)

	if len(cfg.Sandbox.AllowedPaths) != 1 || cfg.Sandbox.AllowedPaths[0] != "/home/user/projects" {
		t.Errorf("AllowedPaths = %v", cfg.Sandbox.AllowedPaths)
	}
}

func TestMergeWizardSettings_AllowedHostsNonNil(t *testing.T) {
	base := DefaultConfig()
	base.Sandbox.AllowedHosts = nil
	w := WizardResult{}
	cfg := MergeWizardSettings(&base, w)
	if cfg.Sandbox.AllowedHosts == nil {
		t.Error("AllowedHosts should not be nil")
	}
}

func TestMergeWizardSettings_Firecrawl(t *testing.T) {
	base := DefaultConfig()
	w := WizardResult{FirecrawlKey: "fc-test-key"}
	cfg := MergeWizardSettings(&base, w)

	if cfg.Web.FirecrawlKey != "fc-test-key" {
		t.Errorf("expected firecrawl key 'fc-test-key', got %q", cfg.Web.FirecrawlKey)
	}
	if cfg.Web.SearchBackend != "firecrawl" {
		t.Errorf("expected search_backend 'firecrawl', got %q", cfg.Web.SearchBackend)
	}
}

func TestMergeWizardSettings_FirecrawlPreservesExplicitBackend(t *testing.T) {
	base := DefaultConfig()
	base.Web.SearchBackend = "tavily"
	base.Web.TavilyAPIKey = "tv-key"
	w := WizardResult{FirecrawlKey: "fc-key"}
	cfg := MergeWizardSettings(&base, w)

	if cfg.Web.FirecrawlKey != "fc-key" {
		t.Errorf("expected firecrawl key set, got %q", cfg.Web.FirecrawlKey)
	}
	// Tavily was explicitly set — should NOT be overwritten to firecrawl.
	if cfg.Web.SearchBackend != "tavily" {
		t.Errorf("expected search_backend preserved as 'tavily', got %q", cfg.Web.SearchBackend)
	}
}

func TestMergeWizardSettings_EmptyTelegramPreservesExisting(t *testing.T) {
	base := DefaultConfig()
	base.Channels = append(base.Channels, ChannelConfig{
		ChannelType: ChannelTelegram,
		Token:       "123456:ABCDEF",
	})
	base.AllowedChatIDs = []string{"99887766"}

	cfg := MergeWizardSettings(&base, WizardResult{})

	found := false
	for _, ch := range cfg.Channels {
		if ch.ChannelType == ChannelTelegram && ch.Token == "123456:ABCDEF" {
			found = true
		}
	}
	if !found {
		t.Error("existing Telegram channel should be preserved when WizardResult is empty")
	}
	if len(cfg.AllowedChatIDs) == 0 || cfg.AllowedChatIDs[0] != "99887766" {
		t.Errorf("existing AllowedChatIDs should be preserved, got %v", cfg.AllowedChatIDs)
	}
}

func TestMergeWizardSettings_EmptyKakaoPreservesExisting(t *testing.T) {
	base := DefaultConfig()
	base.Channels = append(base.Channels, ChannelConfig{
		ChannelType: ChannelKakaoTalk,
	})

	cfg := MergeWizardSettings(&base, WizardResult{})

	found := false
	for _, ch := range cfg.Channels {
		if ch.ChannelType == ChannelKakaoTalk {
			found = true
		}
	}
	if !found {
		t.Error("existing Kakao channel should be preserved when WizardResult is empty")
	}
}

func TestMergeWizardSettings_EmptyFirecrawlPreservesExisting(t *testing.T) {
	base := DefaultConfig()
	base.Web.FirecrawlKey = "fc-existing-key"
	base.Web.SearchBackend = "firecrawl"

	cfg := MergeWizardSettings(&base, WizardResult{})

	if cfg.Web.FirecrawlKey != "fc-existing-key" {
		t.Errorf("expected firecrawl key preserved, got %q", cfg.Web.FirecrawlKey)
	}
	if cfg.Web.SearchBackend != "firecrawl" {
		t.Errorf("expected search_backend preserved as 'firecrawl', got %q", cfg.Web.SearchBackend)
	}
}

func TestWriteConfigAtomic(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	cfg := DefaultConfig()
	cfg.LLM.Default = "main"
	cfg.LLM.Models = []ModelConfig{{
		ID:         "main",
		Provider:   "anthropic",
		Model:      "claude-test",
		Credential: "anthropic",
	}}
	cfg.LLM.APIKey = "test-key"

	if err := WriteConfigAtomic(&cfg, cfgPath); err != nil {
		t.Fatalf("WriteConfigAtomic: %v", err)
	}

	// Verify file exists and is readable TOML.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var loaded Config
	if err := toml.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if loaded.LLM.APIKey != "" {
		t.Errorf("loaded api_key = %q, want empty", loaded.LLM.APIKey)
	}
	model := loaded.DefaultModel()
	if model == nil || model.Provider != "anthropic" || model.Model != "claude-test" {
		t.Errorf("loaded default model = %#v", model)
	}

	// Verify permissions.
	info, _ := os.Stat(cfgPath)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}

	// Verify no tmp file left behind.
	tmpPath := cfgPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after successful write")
	}
}

func TestWriteConfigAtomic_NoPartialOnFailure(t *testing.T) {
	// Write to a non-existent directory should fail.
	cfgPath := filepath.Join(t.TempDir(), "nonexistent", "config.toml")
	cfg := DefaultConfig()
	err := WriteConfigAtomic(&cfg, cfgPath)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
	if !strings.Contains(err.Error(), "write tmp config") {
		t.Errorf("unexpected error: %v", err)
	}
}
