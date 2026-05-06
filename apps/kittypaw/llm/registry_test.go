package llm

import (
	"os"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestNewProviderClaude(t *testing.T) {
	p, err := NewProvider("anthropic", "test-key", "claude-3-opus-20240229", 1024)
	if err != nil {
		t.Fatalf("NewProvider(anthropic) error: %v", err)
	}
	if _, ok := p.(*ClaudeProvider); !ok {
		t.Errorf("expected *ClaudeProvider, got %T", p)
	}
}

func TestNewProviderOpenAI(t *testing.T) {
	p, err := NewProvider("openai", "test-key", "gpt-4", 1024)
	if err != nil {
		t.Fatalf("NewProvider(openai) error: %v", err)
	}
	if _, ok := p.(*OpenAIProvider); !ok {
		t.Errorf("expected *OpenAIProvider, got %T", p)
	}
}

func TestNewProviderOllama(t *testing.T) {
	p, err := NewProvider("ollama", "", "llama3", 1024)
	if err != nil {
		t.Fatalf("NewProvider(ollama) error: %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for ollama, got %T", p)
	}
	if op.baseURL != ollamaDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, ollamaDefaultBaseURL)
	}
}

func TestNewProviderGemini(t *testing.T) {
	p, err := NewProvider("gemini", "test-key", "gemini-3.1-pro-preview", 1024)
	if err != nil {
		t.Fatalf("NewProvider(gemini) error: %v", err)
	}
	if _, ok := p.(*GeminiProvider); !ok {
		t.Errorf("expected *GeminiProvider, got %T", p)
	}
}

func TestNewProviderUnknown(t *testing.T) {
	_, err := NewProvider("unknown", "key", "model", 1024)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewProviderAliases(t *testing.T) {
	// "claude" alias → ClaudeProvider
	p, err := NewProvider("claude", "key", "model", 1024)
	if err != nil {
		t.Fatalf("NewProvider(claude) error: %v", err)
	}
	if _, ok := p.(*ClaudeProvider); !ok {
		t.Errorf("expected *ClaudeProvider for alias 'claude', got %T", p)
	}

	// "gpt" alias → OpenAIProvider
	p, err = NewProvider("gpt", "key", "model", 1024)
	if err != nil {
		t.Fatalf("NewProvider(gpt) error: %v", err)
	}
	if _, ok := p.(*OpenAIProvider); !ok {
		t.Errorf("expected *OpenAIProvider for alias 'gpt', got %T", p)
	}
}

func TestNewProviderFromConfig(t *testing.T) {
	p, err := NewProviderFromConfig(core.LLMConfig{
		Provider:  "anthropic",
		APIKey:    "key",
		Model:     "claude-3-opus-20240229",
		MaxTokens: 2048,
	})
	if err != nil {
		t.Fatalf("NewProviderFromConfig() error: %v", err)
	}
	cp, ok := p.(*ClaudeProvider)
	if !ok {
		t.Fatalf("expected *ClaudeProvider, got %T", p)
	}
	if cp.maxTokens != 2048 {
		t.Errorf("maxTokens = %d, want 2048", cp.maxTokens)
	}
}

func TestNewProviderCerebras(t *testing.T) {
	// Cerebras Cloud is OpenAI-compatible — provider name resolves to an
	// OpenAIProvider in chat mode pointed at api.cerebras.ai with the free
	// tier's 8K context cap baked in.
	p, err := NewProvider("cerebras", "test-key", "qwen-3-235b", 1024)
	if err != nil {
		t.Fatalf("NewProvider(cerebras) error: %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for cerebras, got %T", p)
	}
	if op.apiMode != openAIAPIModeChat {
		t.Errorf("apiMode = %q, want chat", op.apiMode)
	}
	if op.baseURL != cerebrasDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, cerebrasDefaultBaseURL)
	}
	if op.contextWindow != cerebrasFreeContextWindow {
		t.Errorf("contextWindow = %d, want %d (free-tier cap)", op.contextWindow, cerebrasFreeContextWindow)
	}
}

func TestNewProviderCerebrasBaseURLOverride(t *testing.T) {
	// Custom base_url (paid tier, regional endpoint, mock) wins over the
	// default — pin via NewProviderFromModelConfig path.
	p, err := NewProviderFromModelConfig(core.ModelConfig{
		Provider:  "cerebras",
		APIKey:    "k",
		Model:     "qwen-3-235b",
		MaxTokens: 1024,
		BaseURL:   "http://localhost:9999/v1/chat/completions",
	})
	if err != nil {
		t.Fatalf("NewProviderFromModelConfig(cerebras) error: %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.baseURL != "http://localhost:9999/v1/chat/completions" {
		t.Errorf("baseURL = %q, want override", op.baseURL)
	}
}

func TestNewProviderCerebrasContextWindowOverride(t *testing.T) {
	// Paid-tier callers can lift the 8K cap via ModelConfig.ContextWindow.
	p, err := NewProviderFromModelConfig(core.ModelConfig{
		Provider:      "cerebras",
		APIKey:        "k",
		Model:         "qwen-3-235b",
		MaxTokens:     1024,
		ContextWindow: 65536,
	})
	if err != nil {
		t.Fatalf("NewProviderFromModelConfig(cerebras) error: %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.contextWindow != 65536 {
		t.Errorf("contextWindow = %d, want 65536 (override)", op.contextWindow)
	}
}

func TestNewProviderCerebrasAPIKeyFromEnv(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "env-key")
	p, err := NewProvider("cerebras", "", "qwen-3-235b", 1024)
	if err != nil {
		t.Fatalf("NewProvider(cerebras) error: %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.apiKey != "env-key" {
		t.Errorf("apiKey = %q, want %q (from CEREBRAS_API_KEY)", op.apiKey, "env-key")
	}
}

func TestEnvAPIKeyCerebras(t *testing.T) {
	// Direct envAPIKey lookup — protects the table from silent typos in the
	// env var name.
	t.Setenv("CEREBRAS_API_KEY", "v")
	if got := envAPIKey("cerebras"); got != "v" {
		t.Errorf("envAPIKey(cerebras) = %q, want v", got)
	}
	// Case-insensitive: provider name normalisation matches the switch.
	t.Setenv("CEREBRAS_API_KEY", "")
	_ = os.Unsetenv("CEREBRAS_API_KEY")
}

func TestNewProviderGroq(t *testing.T) {
	p, err := NewProvider("groq", "test-key", "llama-3.3-70b-versatile", 1024)
	if err != nil {
		t.Fatalf("NewProvider(groq): %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for groq, got %T", p)
	}
	if op.apiMode != openAIAPIModeChat {
		t.Errorf("apiMode = %q, want chat", op.apiMode)
	}
	if op.baseURL != groqDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, groqDefaultBaseURL)
	}
}

// TestGroqSupportsReasoningFormat pins the whitelist: thinking models
// (qwen/, openai/gpt-oss prefix) get reasoning_format=parsed; everyone
// else (Llama family) does not. Without the gate Groq returns 400
// "reasoning_format is not supported with this model" for Llama models.
// See MODEL_GUIDE.md § 5.13.
func TestGroqSupportsReasoningFormat(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"qwen/qwen3-32b", true},
		{"openai/gpt-oss-120b", true},
		{"openai/gpt-oss-20b", true},
		{"openai/gpt-oss-safeguard-20b", true},
		{"llama-3.3-70b-versatile", false},
		{"llama-3.1-8b-instant", false},
		{"meta-llama/llama-4-scout-17b-16e-instruct", false},
		{"groq/compound", false},
		{"whisper-large-v3", false},
		{"", false},
	}
	for _, c := range cases {
		if got := groqSupportsReasoningFormat(c.model); got != c.want {
			t.Errorf("groqSupportsReasoningFormat(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

// TestNewProviderGroq_QwenAutoReasoningFormat: registry case "groq" with
// a thinking model ID auto-applies WithReasoningFormat("parsed"). Without
// this the agent loop sees raw <think> tokens in content and exposes them
// to the chat user (UX broken). § 5.13 measurement evidence.
func TestNewProviderGroq_QwenAutoReasoningFormat(t *testing.T) {
	p, err := NewProvider("groq", "k", "qwen/qwen3-32b", 1024)
	if err != nil {
		t.Fatalf("NewProvider(groq, qwen3-32b): %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.reasoningFormat != "parsed" {
		t.Errorf("reasoningFormat = %q, want %q (qwen/ prefix → auto)",
			op.reasoningFormat, "parsed")
	}
}

func TestNewProviderGroq_GptOssAutoReasoningFormat(t *testing.T) {
	p, err := NewProvider("groq", "k", "openai/gpt-oss-120b", 1024)
	if err != nil {
		t.Fatalf("NewProvider(groq, gpt-oss-120b): %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.reasoningFormat != "parsed" {
		t.Errorf("reasoningFormat = %q, want %q (openai/gpt-oss prefix → auto)",
			op.reasoningFormat, "parsed")
	}
}

// TestNewProviderGroq_LlamaNoReasoningFormat: Llama family on Groq does
// NOT get reasoning_format. Sending it would 400 (measured 2026-05-05).
func TestNewProviderGroq_LlamaNoReasoningFormat(t *testing.T) {
	p, err := NewProvider("groq", "k", "llama-3.3-70b-versatile", 1024)
	if err != nil {
		t.Fatalf("NewProvider(groq, llama-3.3): %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.reasoningFormat != "" {
		t.Errorf("reasoningFormat = %q, want empty (llama → no auto, would 400)",
			op.reasoningFormat)
	}
}

// TestNewProviderDeepSeek_NoReasoningFormat / OpenRouter regression:
// reasoning_format is Groq-only. deepseek/openrouter models (even those
// with qwen/ prefix on OpenRouter) must NOT receive the field. The gate
// in registry.go is `strings.ToLower(provider) == "groq"` — pinning that
// here.
func TestNewProviderDeepSeek_NoReasoningFormat(t *testing.T) {
	p, err := NewProvider("deepseek", "k", "deepseek-chat", 1024)
	if err != nil {
		t.Fatalf("NewProvider(deepseek): %v", err)
	}
	if op := p.(*OpenAIProvider); op.reasoningFormat != "" {
		t.Errorf("reasoningFormat = %q, want empty (deepseek)", op.reasoningFormat)
	}
}

func TestNewProviderOpenRouter_NoReasoningFormat(t *testing.T) {
	// Even with a Qwen-prefix model on OpenRouter, the option must NOT
	// be applied — OpenRouter's wire is plain OpenAI-compat and an
	// unknown field could 400 depending on the upstream router.
	p, err := NewProvider("openrouter", "k", "qwen/qwen3-235b-a22b-instruct-2507", 1024)
	if err != nil {
		t.Fatalf("NewProvider(openrouter): %v", err)
	}
	if op := p.(*OpenAIProvider); op.reasoningFormat != "" {
		t.Errorf("reasoningFormat = %q, want empty (openrouter)", op.reasoningFormat)
	}
}

func TestNewProviderGroqAPIKeyFromEnv(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "groq-env-key")
	p, err := NewProvider("groq", "", "llama-3.3-70b-versatile", 1024)
	if err != nil {
		t.Fatalf("NewProvider(groq): %v", err)
	}
	if op := p.(*OpenAIProvider); op.apiKey != "groq-env-key" {
		t.Errorf("apiKey = %q, want from GROQ_API_KEY", op.apiKey)
	}
}

func TestNewProviderDeepSeek(t *testing.T) {
	p, err := NewProvider("deepseek", "test-key", "deepseek-chat", 1024)
	if err != nil {
		t.Fatalf("NewProvider(deepseek): %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for deepseek, got %T", p)
	}
	if op.baseURL != deepseekDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, deepseekDefaultBaseURL)
	}
}

func TestNewProviderDeepSeekAPIKeyFromEnv(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "ds-env-key")
	p, err := NewProvider("deepseek", "", "deepseek-chat", 1024)
	if err != nil {
		t.Fatalf("NewProvider(deepseek): %v", err)
	}
	if op := p.(*OpenAIProvider); op.apiKey != "ds-env-key" {
		t.Errorf("apiKey = %q, want from DEEPSEEK_API_KEY", op.apiKey)
	}
}

func TestNewProviderMistral(t *testing.T) {
	p, err := NewProvider("mistral", "test-key", "mistral-medium-latest", 1024)
	if err != nil {
		t.Fatalf("NewProvider(mistral): %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for mistral, got %T", p)
	}
	if op.apiMode != openAIAPIModeChat {
		t.Errorf("apiMode = %q, want chat", op.apiMode)
	}
	if op.baseURL != mistralDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, mistralDefaultBaseURL)
	}
	// Mistral 모델은 reasoning_format 비대상 (Groq 전용 옵션). 누설 ❌.
	if op.reasoningFormat != "" {
		t.Errorf("reasoningFormat = %q, want empty (Groq-only option)", op.reasoningFormat)
	}
}

func TestNewProviderMistralAPIKeyFromEnv(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "mistral-env-key")
	p, err := NewProvider("mistral", "", "mistral-medium-latest", 1024)
	if err != nil {
		t.Fatalf("NewProvider(mistral): %v", err)
	}
	if op := p.(*OpenAIProvider); op.apiKey != "mistral-env-key" {
		t.Errorf("apiKey = %q, want from MISTRAL_API_KEY", op.apiKey)
	}
}

// TestNewProviderMistral_NoOPENAIKeyContamination: MISTRAL_API_KEY 비어
// 있고 OPENAI_API_KEY가 설정되어 있어도 mistral provider는 OPENAI 키를
// 가져오면 안 됨 (provider identity 분리 — 각 vendor 키는 별도 source).
func TestNewProviderMistral_NoOPENAIKeyContamination(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-env-key")
	t.Setenv("MISTRAL_API_KEY", "")
	_ = os.Unsetenv("MISTRAL_API_KEY")

	p, err := NewProvider("mistral", "", "mistral-medium-latest", 1024)
	if err != nil {
		t.Fatalf("NewProvider(mistral): %v", err)
	}
	if op := p.(*OpenAIProvider); op.apiKey == "openai-env-key" {
		t.Errorf("apiKey leaked from OPENAI_API_KEY: %q (mistral must not read OpenAI key)", op.apiKey)
	}
}

// TestEnvAPIKeyMistral pins the table entry — case-insensitive lookup,
// reads MISTRAL_API_KEY only.
func TestEnvAPIKeyMistral(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "v")
	if got := envAPIKey("mistral"); got != "v" {
		t.Errorf("envAPIKey(mistral) = %q, want v", got)
	}
}

func TestNewProviderOpenRouter(t *testing.T) {
	p, err := NewProvider("openrouter", "test-key", "qwen/qwen3-235b-a22b-instruct-2507", 1024)
	if err != nil {
		t.Fatalf("NewProvider(openrouter): %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for openrouter, got %T", p)
	}
	if op.baseURL != openRouterDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, openRouterDefaultBaseURL)
	}
}

func TestNewProviderOpenRouterAPIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "or-env-key")
	p, err := NewProvider("openrouter", "", "qwen/qwen3-235b", 1024)
	if err != nil {
		t.Fatalf("NewProvider(openrouter): %v", err)
	}
	if op := p.(*OpenAIProvider); op.apiKey != "or-env-key" {
		t.Errorf("apiKey = %q, want from OPENROUTER_API_KEY", op.apiKey)
	}
}

func TestNewProviderBaseURLOverridesAcrossOpenAICompatible(t *testing.T) {
	// Custom base_url wins for groq / deepseek / openrouter / mistral the
	// same way it does for cerebras / ollama / openai.
	for _, prov := range []string{"groq", "deepseek", "openrouter", "mistral"} {
		p, err := NewProviderFromModelConfig(core.ModelConfig{
			Provider:  prov,
			APIKey:    "k",
			Model:     "m",
			MaxTokens: 256,
			BaseURL:   "http://custom/v1/chat/completions",
		})
		if err != nil {
			t.Fatalf("NewProviderFromModelConfig(%s): %v", prov, err)
		}
		op := p.(*OpenAIProvider)
		if op.baseURL != "http://custom/v1/chat/completions" {
			t.Errorf("%s baseURL = %q, want override", prov, op.baseURL)
		}
	}
}

func TestNewProviderFromModelConfig(t *testing.T) {
	p, err := NewProviderFromModelConfig(core.ModelConfig{
		Provider:      "openai",
		APIKey:        "key",
		Model:         "gpt-4",
		MaxTokens:     512,
		BaseURL:       "http://custom:8080/v1",
		ContextWindow: 32000,
	})
	if err != nil {
		t.Fatalf("NewProviderFromModelConfig() error: %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider, got %T", p)
	}
	if op.baseURL != "http://custom:8080/v1" {
		t.Errorf("baseURL = %q, want %q", op.baseURL, "http://custom:8080/v1")
	}
	if op.contextWindow != 32000 {
		t.Errorf("contextWindow = %d, want 32000", op.contextWindow)
	}
}

func TestNewProviderLMStudio(t *testing.T) {
	// LM Studio HTTP server is OpenAI Chat Completions-compatible (no auth).
	// dev-models harness opens an SSH tunnel localhost:11600 → emac:1234,
	// so the default base URL points at the tunnel. Provider resolves to
	// OpenAIProvider in chat mode — same wire as ollama / cerebras / groq.
	p, err := NewProvider("lmstudio", "", "qwen3-30b-a3b-instruct-2507", 1024)
	if err != nil {
		t.Fatalf("NewProvider(lmstudio) error: %v", err)
	}
	op, ok := p.(*OpenAIProvider)
	if !ok {
		t.Fatalf("expected *OpenAIProvider for lmstudio, got %T", p)
	}
	if op.baseURL != lmstudioDefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", op.baseURL, lmstudioDefaultBaseURL)
	}
}

func TestNewProviderLMStudioBaseURLOverride(t *testing.T) {
	// Custom base_url (different SSH tunnel port, alt LM Studio host, mock)
	// must win over the default. Pinned via NewProviderFromModelConfig path
	// — same override contract as ollama / cerebras.
	p, err := NewProviderFromModelConfig(core.ModelConfig{
		Provider:  "lmstudio",
		APIKey:    "",
		Model:     "qwen3-30b-a3b-instruct-2507",
		MaxTokens: 1024,
		BaseURL:   "http://localhost:9999/v1/chat/completions",
	})
	if err != nil {
		t.Fatalf("NewProviderFromModelConfig(lmstudio) error: %v", err)
	}
	op := p.(*OpenAIProvider)
	if op.baseURL != "http://localhost:9999/v1/chat/completions" {
		t.Errorf("baseURL = %q, want override", op.baseURL)
	}
}
