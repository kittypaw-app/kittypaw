package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

type rateLimitMockProvider struct {
	mu        sync.Mutex
	calls     int
	resp      *llm.Response
	err       error
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	maxTokens int
}

func (p *rateLimitMockProvider) Generate(ctx context.Context, messages []core.LlmMessage) (*llm.Response, error) {
	return p.call(ctx)
}

func (p *rateLimitMockProvider) GenerateWithTools(ctx context.Context, messages []core.LlmMessage, tools []llm.Tool) (*llm.Response, error) {
	return p.call(ctx)
}

func (p *rateLimitMockProvider) ContextWindow() int { return 1000 }

func (p *rateLimitMockProvider) MaxTokens() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	return 100
}

func (p *rateLimitMockProvider) call(ctx context.Context) (*llm.Response, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()

	if p.started != nil {
		p.startOnce.Do(func() { close(p.started) })
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.resp != nil {
		return p.resp, p.err
	}
	if p.err != nil {
		return nil, p.err
	}
	return &llm.Response{
		Content: "ok",
		Usage: &llm.TokenUsage{
			Model:        "test-model",
			InputTokens:  1,
			OutputTokens: 1,
		},
	}, nil
}

func (p *rateLimitMockProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func TestRateLimitedProviderRejectsRequestLimitBeforeProviderCall(t *testing.T) {
	inner := &rateLimitMockProvider{}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "test",
		Model:    "test-model",
		RateLimit: core.ModelRateLimitConfig{
			RequestsPerMinute: 1,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := provider.Generate(context.Background(), messages); !errors.Is(err, ErrLLMRateLimitExceeded) {
		t.Fatalf("second generate error = %v, want ErrLLMRateLimitExceeded", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner calls = %d, want 1", got)
	}
}

func TestRateLimitedProviderSettlesTokenReservation(t *testing.T) {
	inner := &rateLimitMockProvider{
		maxTokens: 100,
		resp: &llm.Response{
			Content: "ok",
			Usage: &llm.TokenUsage{
				Model:        "test-model",
				InputTokens:  10,
				OutputTokens: 20,
			},
		},
	}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "test",
		Model:    "test-model",
		RateLimit: core.ModelRateLimitConfig{
			TokensPerMinute: 140,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("second generate after usage settlement: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner calls = %d, want 2", got)
	}
}

func TestRateLimitedProviderRejectsWhenMaxConcurrentReached(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	inner := &rateLimitMockProvider{started: started, release: release}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "test",
		Model:    "test-model",
		RateLimit: core.ModelRateLimitConfig{
			MaxConcurrent: 1,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	firstErr := make(chan error, 1)
	go func() {
		_, err := provider.Generate(context.Background(), messages)
		firstErr <- err
	}()
	<-started

	if _, err := provider.Generate(context.Background(), messages); !errors.Is(err, ErrLLMRateLimitExceeded) {
		t.Fatalf("concurrent generate error = %v, want ErrLLMRateLimitExceeded", err)
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner calls = %d, want 1", got)
	}
}

func TestRateLimitedProviderIncludesToolsInInputTokenLimit(t *testing.T) {
	inner := &rateLimitMockProvider{}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "test",
		Model:    "test-model",
		RateLimit: core.ModelRateLimitConfig{
			InputTokensPerMinute: 50,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hi"}}
	tools := []llm.Tool{{
		Name:        "search_memory",
		Description: strings.Repeat("long schema description ", 30),
		InputSchema: map[string]any{
			"type":        "object",
			"description": strings.Repeat("schema field detail ", 30),
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": strings.Repeat("query field ", 30),
				},
			},
		},
	}}

	if _, err := provider.GenerateWithTools(context.Background(), messages, tools); !errors.Is(err, ErrLLMRateLimitExceeded) {
		t.Fatalf("generate with tools error = %v, want ErrLLMRateLimitExceeded", err)
	}
	if got := inner.callCount(); got != 0 {
		t.Fatalf("inner calls = %d, want 0", got)
	}
}

func TestRateLimiterDefaultKeyIgnoresModelAlias(t *testing.T) {
	registry := NewLLMRateLimiterRegistry()
	first := NewRateLimitedProvider(&rateLimitMockProvider{}, registry, core.ModelConfig{
		ID:         "fast-alias",
		Provider:   "openai",
		Credential: "openai-prod",
		Model:      "gpt-5.5",
		RateLimit: core.ModelRateLimitConfig{
			RequestsPerMinute: 1,
		},
	})
	secondInner := &rateLimitMockProvider{}
	second := NewRateLimitedProvider(secondInner, registry, core.ModelConfig{
		ID:         "careful-alias",
		Provider:   "openai",
		Credential: "openai-prod",
		Model:      "gpt-5.5",
		RateLimit: core.ModelRateLimitConfig{
			RequestsPerMinute: 1,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := first.Generate(context.Background(), messages); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := second.Generate(context.Background(), messages); !errors.Is(err, ErrLLMRateLimitExceeded) {
		t.Fatalf("second alias generate error = %v, want ErrLLMRateLimitExceeded", err)
	}
	if got := secondInner.callCount(); got != 0 {
		t.Fatalf("second inner calls = %d, want 0", got)
	}
}

func TestRateLimiterExplicitPoolSharesQuotaAcrossModels(t *testing.T) {
	registry := NewLLMRateLimiterRegistry()
	first := NewRateLimitedProvider(&rateLimitMockProvider{}, registry, core.ModelConfig{
		ID:         "sonnet",
		Provider:   "anthropic",
		Credential: "anthropic-prod",
		Model:      "claude-sonnet-4-6",
		RateLimit: core.ModelRateLimitConfig{
			Pool:              "anthropic-tier-3",
			RequestsPerMinute: 1,
		},
	})
	secondInner := &rateLimitMockProvider{}
	second := NewRateLimitedProvider(secondInner, registry, core.ModelConfig{
		ID:         "opus",
		Provider:   "anthropic",
		Credential: "anthropic-prod",
		Model:      "claude-opus-4-2",
		RateLimit: core.ModelRateLimitConfig{
			Pool:              "anthropic-tier-3",
			RequestsPerMinute: 1,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := first.Generate(context.Background(), messages); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := second.Generate(context.Background(), messages); !errors.Is(err, ErrLLMRateLimitExceeded) {
		t.Fatalf("second pooled generate error = %v, want ErrLLMRateLimitExceeded", err)
	}
	if got := secondInner.callCount(); got != 0 {
		t.Fatalf("second inner calls = %d, want 0", got)
	}
}

func TestRateLimitedProviderRefundsTokenReservationOnProviderError(t *testing.T) {
	upstreamErr := errors.New("upstream unavailable")
	inner := &rateLimitMockProvider{
		maxTokens: 100,
		err:       upstreamErr,
	}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "openai",
		Model:    "gpt-5.5",
		RateLimit: core.ModelRateLimitConfig{
			TokensPerMinute: 120,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); !errors.Is(err, upstreamErr) {
		t.Fatalf("first generate error = %v, want upstream error", err)
	}
	inner.err = nil
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("second generate after failed reservation refund: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner calls = %d, want 2", got)
	}
}

func TestRateLimitedProviderAnthropicCacheReadDoesNotConsumeInputLimit(t *testing.T) {
	inner := &rateLimitMockProvider{
		maxTokens: 100,
		resp: &llm.Response{
			Content: "ok",
			Usage: &llm.TokenUsage{
				Model:                    "claude-sonnet-4-6",
				InputTokens:              10,
				CacheCreationInputTokens: 0,
				CacheReadInputTokens:     1000,
				OutputTokens:             1,
			},
		},
	}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
		RateLimit: core.ModelRateLimitConfig{
			InputTokensPerMinute: 50,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("second generate after cache read settlement: %v", err)
	}
	if got := inner.callCount(); got != 2 {
		t.Fatalf("inner calls = %d, want 2", got)
	}
}

func TestRateLimitedProviderGoogleUsesGeminiTokenPolicy(t *testing.T) {
	inner := &rateLimitMockProvider{
		maxTokens: 100,
		resp: &llm.Response{
			Content: "ok",
			Usage: &llm.TokenUsage{
				Model:        "gemini-3.1-pro-preview",
				InputTokens:  10,
				OutputTokens: 10,
			},
		},
	}
	provider := NewRateLimitedProvider(inner, NewLLMRateLimiterRegistry(), core.ModelConfig{
		ID:       "main",
		Provider: "google",
		Model:    "gemini-3.1-pro-preview",
		RateLimit: core.ModelRateLimitConfig{
			OutputTokensPerMinute: 50,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner calls = %d, want 1", got)
	}
}

func TestRateLimiterSnapshotShowsUsage(t *testing.T) {
	registry := NewLLMRateLimiterRegistry()
	provider := NewRateLimitedProvider(&rateLimitMockProvider{}, registry, core.ModelConfig{
		ID:         "main",
		Provider:   "openai",
		Credential: "openai-prod",
		Model:      "gpt-5.5",
		BaseURL:    "https://api.openai.com/v1",
		RateLimit: core.ModelRateLimitConfig{
			Pool:              "openai-default",
			RequestsPerMinute: 10,
			TokensPerMinute:   1000,
		},
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); err != nil {
		t.Fatalf("generate: %v", err)
	}

	snapshots := registry.Snapshot()
	if len(snapshots) != 1 {
		t.Fatalf("Snapshot len = %d, want 1", len(snapshots))
	}
	got := snapshots[0]
	if got.Provider != "openai" || got.Model != "gpt-5.5" || got.Pool != "openai-default" || got.RequestsPerMinuteLimit != 10 {
		t.Fatalf("snapshot = %+v", got)
	}
	if got.RequestsPerMinuteRemaining >= got.RequestsPerMinuteLimit {
		t.Fatalf("RequestsPerMinuteRemaining = %d, want below limit %d", got.RequestsPerMinuteRemaining, got.RequestsPerMinuteLimit)
	}
}

func TestDailyTokenLimitedProviderRejectsWhenExistingUsageWouldExceedLimit(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	if err := st.RecordLLMCallUsage(&store.LLMCallUsageRecord{
		CallKind:     "chat",
		Provider:     "openai",
		Model:        "gpt-5.5",
		StartedAt:    now,
		FinishedAt:   now,
		InputTokens:  45,
		OutputTokens: 45,
	}); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	inner := &rateLimitMockProvider{maxTokens: 20}
	provider := NewDailyTokenLimitedProvider(inner, NewDailyTokenLimiter(), st, 100, core.ModelConfig{
		Provider: "openai",
		Model:    "gpt-5.5",
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	if _, err := provider.Generate(context.Background(), messages); !errors.Is(err, ErrDailyTokenLimitExceeded) {
		t.Fatalf("generate error = %v, want ErrDailyTokenLimitExceeded", err)
	}
	if got := inner.callCount(); got != 0 {
		t.Fatalf("inner calls = %d, want 0", got)
	}
}

func TestDailyTokenLimitedProviderReservesConcurrentCalls(t *testing.T) {
	st := openTestStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	inner := &rateLimitMockProvider{maxTokens: 100, started: started, release: release}
	provider := NewDailyTokenLimitedProvider(inner, NewDailyTokenLimiter(), st, 150, core.ModelConfig{
		Provider: "openai",
		Model:    "gpt-5.5",
	})

	messages := []core.LlmMessage{{Role: core.RoleUser, Content: "hello"}}
	firstErr := make(chan error, 1)
	go func() {
		_, err := provider.Generate(context.Background(), messages)
		firstErr <- err
	}()
	<-started

	if _, err := provider.Generate(context.Background(), messages); !errors.Is(err, ErrDailyTokenLimitExceeded) {
		t.Fatalf("second concurrent generate error = %v, want ErrDailyTokenLimitExceeded", err)
	}
	close(release)
	if err := <-firstErr; err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if got := inner.callCount(); got != 1 {
		t.Fatalf("inner calls = %d, want 1", got)
	}
}
