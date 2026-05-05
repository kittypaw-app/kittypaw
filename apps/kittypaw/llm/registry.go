package llm

import (
	"fmt"
	"os"
	"strings"

	"github.com/jinto/kittypaw/core"
)

const (
	ollamaDefaultBaseURL = "http://localhost:11434/v1/chat/completions"

	// Cerebras Cloud is OpenAI Chat Completions-compatible. The free tier
	// (1M tokens/day, no expiry) caps context at 8,192 tokens; paid tiers
	// can lift this via ModelConfig.ContextWindow.
	cerebrasDefaultBaseURL    = "https://api.cerebras.ai/v1/chat/completions"
	cerebrasFreeContextWindow = 8192

	// Groq, DeepSeek, OpenRouter, Mistral are also OpenAI Chat Completions-
	// compatible. Context windows vary per model so no default cap is forced —
	// callers can opt into one via ModelConfig.ContextWindow.
	groqDefaultBaseURL       = "https://api.groq.com/openai/v1/chat/completions"
	deepseekDefaultBaseURL   = "https://api.deepseek.com/v1/chat/completions"
	openRouterDefaultBaseURL = "https://openrouter.ai/api/v1/chat/completions"
	// Mistral La Plateforme: free Experiment plan (1B tokens/month, phone-
	// verified, no card). magistral-medium-latest emits list-of-blocks
	// content (handled by extractContent in openai.go).
	mistralDefaultBaseURL = "https://api.mistral.ai/v1/chat/completions"

	// LM Studio (OpenAI Chat Completions-compatible, no auth). dev-models
	// harness forwards localhost:11600 → emac:1234 over OpenSSH ControlMaster
	// (`make dev-models-tunnel-lms`); production callers can override via
	// ModelConfig.BaseURL. See docs/DEV_MODELS.md.
	lmstudioDefaultBaseURL = "http://localhost:11600/v1/chat/completions"
)

// Option is a functional option for NewProvider.
type Option func(*providerOpts)

type providerOpts struct {
	baseURL       string
	contextWindow int
}

// WithProviderBaseURL sets a custom base URL for the provider.
func WithProviderBaseURL(url string) Option {
	return func(o *providerOpts) {
		o.baseURL = url
	}
}

// WithProviderContextWindow sets a custom context window size.
func WithProviderContextWindow(size int) Option {
	return func(o *providerOpts) {
		o.contextWindow = size
	}
}

// NewProvider creates a Provider from config parameters.
// If apiKey is empty, falls back to the standard environment variable
// for the given provider (ANTHROPIC_API_KEY, OPENAI_API_KEY).
func NewProvider(provider, apiKey, model string, maxTokens int, opts ...Option) (Provider, error) {
	if apiKey == "" {
		apiKey = envAPIKey(provider)
	}

	var o providerOpts
	for _, opt := range opts {
		opt(&o)
	}

	switch strings.ToLower(provider) {
	case "anthropic", "claude":
		return NewClaude(apiKey, model, maxTokens), nil

	case "openai", "gpt":
		var openaiOpts []OpenAIOption
		if o.baseURL != "" {
			openaiOpts = append(openaiOpts, WithBaseURL(o.baseURL))
		}
		if o.contextWindow > 0 {
			openaiOpts = append(openaiOpts, WithContextWindow(o.contextWindow))
		}
		return NewOpenAI(apiKey, model, maxTokens, openaiOpts...), nil

	case "gemini", "google":
		return NewGemini(apiKey, model, maxTokens), nil

	case "ollama":
		baseURL := ollamaDefaultBaseURL
		if o.baseURL != "" {
			baseURL = o.baseURL
		}
		return NewOpenAI(apiKey, model, maxTokens,
			WithBaseURL(baseURL),
		), nil

	case "lmstudio":
		baseURL := lmstudioDefaultBaseURL
		if o.baseURL != "" {
			baseURL = o.baseURL
		}
		return NewOpenAI(apiKey, model, maxTokens,
			WithBaseURL(baseURL),
		), nil

	case "cerebras":
		baseURL := cerebrasDefaultBaseURL
		if o.baseURL != "" {
			baseURL = o.baseURL
		}
		contextWindow := cerebrasFreeContextWindow
		if o.contextWindow > 0 {
			contextWindow = o.contextWindow
		}
		return NewOpenAI(apiKey, model, maxTokens,
			WithBaseURL(baseURL),
			WithContextWindow(contextWindow),
		), nil

	case "groq", "deepseek", "openrouter", "mistral":
		// `provider` was already lowercased by the outer switch.
		baseURL := openAICompatibleBaseURL(provider)
		if o.baseURL != "" {
			baseURL = o.baseURL
		}
		openaiOpts := []OpenAIOption{WithBaseURL(baseURL)}
		if o.contextWindow > 0 {
			openaiOpts = append(openaiOpts, WithContextWindow(o.contextWindow))
		}
		// Groq's thinking models (qwen/qwen3-32b, openai/gpt-oss-*) leak
		// <think> tokens into `content` unless reasoning_format is set.
		// Sending it to non-thinking Groq models (llama-3.3-70b,
		// llama-3.1-8b) returns 400 — the whitelist gates this.
		// See MODEL_GUIDE.md § 5.13 for measurement evidence.
		if provider == "groq" && groqSupportsReasoningFormat(model) {
			openaiOpts = append(openaiOpts, WithReasoningFormat("parsed"))
		}
		return NewOpenAI(apiKey, model, maxTokens, openaiOpts...), nil

	default:
		return nil, fmt.Errorf("llm: unknown provider %q", provider)
	}
}

// groqSupportsReasoningFormat returns true for Groq-hosted models that
// accept the non-standard `reasoning_format` request field. Measured
// 2026-05-05 against console.groq.com:
//
//   - qwen/qwen3-32b              → accepted, parsed/hidden cleansed content
//   - openai/gpt-oss-120b         → accepted, parsed/hidden cleansed content
//   - openai/gpt-oss-safeguard-20b → accepted [추정 — same family]
//   - openai/gpt-oss-20b           → accepted [추정 — same family]
//   - llama-3.3-70b-versatile     → 400 "reasoning_format is not supported"
//   - llama-3.1-8b-instant        → 400 "reasoning_format is not supported"
//   - meta-llama/*                → 400 [추정 — Llama family]
//
// Prefix match is conservative: Groq adds new models routinely and a
// future thinking-style "qwen/qwen3.5-..." or "openai/gpt-oss-200b" will
// pick up the option without code changes; non-Groq deepseek/openrouter
// models never reach this branch.
func groqSupportsReasoningFormat(model string) bool {
	return strings.HasPrefix(model, "qwen/") ||
		strings.HasPrefix(model, "openai/gpt-oss")
}

// openAICompatibleBaseURL returns the canonical Chat Completions endpoint for
// providers that share the OpenAI wire shape but differ only in host / path.
func openAICompatibleBaseURL(provider string) string {
	switch strings.ToLower(provider) {
	case "groq":
		return groqDefaultBaseURL
	case "deepseek":
		return deepseekDefaultBaseURL
	case "openrouter":
		return openRouterDefaultBaseURL
	case "mistral":
		return mistralDefaultBaseURL
	default:
		return ""
	}
}

// NewProviderFromConfig creates a Provider from an LLMConfig.
func NewProviderFromConfig(cfg core.LLMConfig) (Provider, error) {
	var opts []Option
	if cfg.BaseURL != "" {
		opts = append(opts, WithProviderBaseURL(cfg.BaseURL))
	}
	return NewProvider(cfg.Provider, cfg.APIKey, cfg.Model, int(cfg.MaxTokens), opts...)
}

// NewProviderFromModelConfig creates a Provider from a ModelConfig.
func NewProviderFromModelConfig(cfg core.ModelConfig) (Provider, error) {
	var opts []Option
	if cfg.BaseURL != "" {
		opts = append(opts, WithProviderBaseURL(cfg.BaseURL))
	}
	if cfg.ContextWindow > 0 {
		opts = append(opts, WithProviderContextWindow(int(cfg.ContextWindow)))
	}
	return NewProvider(cfg.Provider, cfg.APIKey, cfg.Model, int(cfg.MaxTokens), opts...)
}

// envAPIKey returns the standard API key environment variable for a provider.
func envAPIKey(provider string) string {
	switch strings.ToLower(provider) {
	case "anthropic", "claude":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai", "gpt":
		return os.Getenv("OPENAI_API_KEY")
	case "gemini", "google":
		return os.Getenv("GEMINI_API_KEY")
	case "cerebras":
		return os.Getenv("CEREBRAS_API_KEY")
	case "groq":
		return os.Getenv("GROQ_API_KEY")
	case "deepseek":
		return os.Getenv("DEEPSEEK_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	case "mistral":
		return os.Getenv("MISTRAL_API_KEY")
	default:
		return ""
	}
}
