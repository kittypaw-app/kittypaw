package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

var ErrLLMRateLimitExceeded = errors.New("llm rate limit exceeded")

type LLMRateLimiterRegistry struct {
	mu       sync.Mutex
	limiters map[string]*llmRateLimiter
}

func NewLLMRateLimiterRegistry() *LLMRateLimiterRegistry {
	return &LLMRateLimiterRegistry{limiters: make(map[string]*llmRateLimiter)}
}

func NewRateLimitedProvider(inner llm.Provider, registry *LLMRateLimiterRegistry, model core.ModelConfig) llm.Provider {
	if inner == nil || registry == nil || !model.RateLimit.Enabled() {
		return inner
	}
	if _, ok := inner.(*rateLimitedProvider); ok {
		return inner
	}
	return &rateLimitedProvider{
		inner:   inner,
		limiter: registry.limiterFor(model),
		policy:  rateLimitPolicyForModel(model),
	}
}

func (r *LLMRateLimiterRegistry) limiterFor(model core.ModelConfig) *llmRateLimiter {
	key := rateLimiterKey(model)
	r.mu.Lock()
	defer r.mu.Unlock()
	limiter := r.limiters[key]
	if limiter == nil {
		limiter = &llmRateLimiter{}
		r.limiters[key] = limiter
	}
	limiter.update(key, model)
	return limiter
}

// Snapshot returns current account-local LLM limiter state for status
// surfaces. Disabled axes are reported with a zero limit and zero remaining.
func (r *LLMRateLimiterRegistry) Snapshot() []LLMRateLimitSnapshot {
	if r == nil {
		return nil
	}
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LLMRateLimitSnapshot, 0, len(r.limiters))
	for _, limiter := range r.limiters {
		out = append(out, limiter.snapshot(now))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func rateLimiterKey(model core.ModelConfig) string {
	provider := normalizeRateLimiterPart(model.Provider)
	credential := normalizeRateLimiterPart(rateLimiterCredential(model))
	baseURL := normalizeRateLimiterURL(model.BaseURL)
	if pool := normalizeRateLimiterPart(model.RateLimit.Pool); pool != "" {
		return strings.Join([]string{"pool", provider, credential, baseURL, pool}, "\x00")
	}
	upstreamModel := normalizeRateLimiterPart(strings.TrimPrefix(strings.TrimSpace(model.Model), "models/"))
	return strings.Join([]string{
		"model",
		provider,
		credential,
		baseURL,
		upstreamModel,
	}, "\x00")
}

func rateLimiterCredential(model core.ModelConfig) string {
	if credential := strings.TrimSpace(model.Credential); credential != "" {
		return credential
	}
	if provider := strings.TrimSpace(model.Provider); provider != "" {
		return provider
	}
	return strings.TrimSpace(model.Name)
}

func normalizeRateLimiterPart(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeRateLimiterURL(value string) string {
	return strings.TrimRight(normalizeRateLimiterPart(value), "/")
}

type rateLimitedProvider struct {
	inner   llm.Provider
	limiter *llmRateLimiter
	policy  llmRateLimitPolicy
}

func (p *rateLimitedProvider) Generate(ctx context.Context, messages []core.LlmMessage) (*llm.Response, error) {
	reservation, err := p.limiter.acquire(ctx, p.cost(messages))
	if err != nil {
		return nil, err
	}
	resp, err := p.inner.Generate(ctx, messages)
	reservation.settle(responseUsage(resp), err)
	return resp, err
}

func (p *rateLimitedProvider) GenerateWithTools(ctx context.Context, messages []core.LlmMessage, tools []llm.Tool) (*llm.Response, error) {
	reservation, err := p.limiter.acquire(ctx, p.costWithTools(messages, tools))
	if err != nil {
		return nil, err
	}
	resp, err := p.inner.GenerateWithTools(ctx, messages, tools)
	reservation.settle(responseUsage(resp), err)
	return resp, err
}

func (p *rateLimitedProvider) ContextWindow() int { return p.inner.ContextWindow() }

func (p *rateLimitedProvider) MaxTokens() int { return p.inner.MaxTokens() }

func (p *rateLimitedProvider) cost(messages []core.LlmMessage) llmRateLimitCost {
	return estimateLLMCallTokens(p.inner, messages, nil, p.policy)
}

type llmRateLimitPolicy struct {
	Provider                  string
	ReserveOutputTokens       bool
	CountCacheReadInputTokens bool
}

func rateLimitPolicyForModel(model core.ModelConfig) llmRateLimitPolicy {
	provider := normalizeRateLimiterPart(model.Provider)
	policy := llmRateLimitPolicy{
		Provider:                  provider,
		ReserveOutputTokens:       true,
		CountCacheReadInputTokens: true,
	}
	switch provider {
	case "anthropic", "claude":
		policy.ReserveOutputTokens = false
		policy.CountCacheReadInputTokens = false
	case "gemini", "google":
		policy.ReserveOutputTokens = false
	}
	return policy
}

func estimateLLMCallTokens(provider llm.Provider, messages []core.LlmMessage, tools []llm.Tool, policy llmRateLimitPolicy) llmRateLimitCost {
	output := uint64(0)
	if policy.ReserveOutputTokens && provider != nil {
		if maxTokens := provider.MaxTokens(); maxTokens > 0 {
			output = uint64(maxTokens)
		}
	}
	input := estimateLLMInputTokens(messages)
	if len(tools) > 0 {
		input += estimateLLMToolTokens(tools)
	}
	return llmRateLimitCost{
		InputTokens:  input,
		OutputTokens: output,
	}
}

func (p *rateLimitedProvider) costWithTools(messages []core.LlmMessage, tools []llm.Tool) llmRateLimitCost {
	return estimateLLMCallTokens(p.inner, messages, tools, p.policy)
}

type llmRateLimiter struct {
	mu sync.Mutex

	cfg    core.ModelRateLimitConfig
	meta   llmRateLimiterMeta
	policy llmRateLimitPolicy

	inFlight uint32

	requestBucket llmTokenBucket
	inputBucket   llmTokenBucket
	outputBucket  llmTokenBucket
	tokenBucket   llmTokenBucket

	dayWindow string
	dayReqs   uint64
	dayTokens uint64
}

type llmRateLimiterMeta struct {
	Key        string
	Pool       string
	Provider   string
	Credential string
	Model      string
	BaseURL    string
}

// LLMRateLimitSnapshot is a JSON-ready view of a configured LLM limiter.
type LLMRateLimitSnapshot struct {
	Key                            string `json:"key"`
	Pool                           string `json:"pool,omitempty"`
	Provider                       string `json:"provider"`
	Credential                     string `json:"credential,omitempty"`
	Model                          string `json:"model"`
	BaseURL                        string `json:"base_url,omitempty"`
	InFlight                       uint32 `json:"in_flight"`
	MaxConcurrentLimit             uint32 `json:"max_concurrent_limit"`
	RequestsPerMinuteLimit         uint64 `json:"requests_per_minute_limit"`
	RequestsPerMinuteRemaining     uint64 `json:"requests_per_minute_remaining"`
	InputTokensPerMinuteLimit      uint64 `json:"input_tokens_per_minute_limit"`
	InputTokensPerMinuteRemaining  uint64 `json:"input_tokens_per_minute_remaining"`
	OutputTokensPerMinuteLimit     uint64 `json:"output_tokens_per_minute_limit"`
	OutputTokensPerMinuteRemaining uint64 `json:"output_tokens_per_minute_remaining"`
	TokensPerMinuteLimit           uint64 `json:"tokens_per_minute_limit"`
	TokensPerMinuteRemaining       uint64 `json:"tokens_per_minute_remaining"`
	RequestsPerDayLimit            uint64 `json:"requests_per_day_limit"`
	RequestsPerDayUsed             uint64 `json:"requests_per_day_used"`
	TokensPerDayLimit              uint64 `json:"tokens_per_day_limit"`
	TokensPerDayUsed               uint64 `json:"tokens_per_day_used"`
	DayWindow                      string `json:"day_window"`
}

type llmRateLimitCost struct {
	InputTokens  uint64
	OutputTokens uint64
}

func (c llmRateLimitCost) totalTokens() uint64 {
	return c.InputTokens + c.OutputTokens
}

type llmRateLimitReservation struct {
	limiter   *llmRateLimiter
	cost      llmRateLimitCost
	policy    llmRateLimitPolicy
	dayWindow string
}

func (l *llmRateLimiter) update(key string, model core.ModelConfig) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cfg = model.RateLimit
	l.meta = llmRateLimiterMeta{
		Key:        key,
		Pool:       strings.TrimSpace(model.RateLimit.Pool),
		Provider:   strings.TrimSpace(model.Provider),
		Credential: strings.TrimSpace(model.Credential),
		Model:      strings.TrimSpace(model.Model),
		BaseURL:    strings.TrimSpace(model.BaseURL),
	}
	l.policy = rateLimitPolicyForModel(model)
}

func (l *llmRateLimiter) acquire(ctx context.Context, cost llmRateLimitCost) (*llmRateLimitReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetWindowsLocked(now)

	cfg := l.cfg
	if cfg.MaxConcurrent > 0 && l.inFlight >= cfg.MaxConcurrent {
		return nil, rateLimitError("max_concurrent", uint64(cfg.MaxConcurrent), uint64(l.inFlight), 1)
	}
	if !l.requestBucket.canConsume(uint64(cfg.RequestsPerMinute), 1, now) {
		return nil, rateLimitError("requests_per_minute", uint64(cfg.RequestsPerMinute), l.requestBucket.used(uint64(cfg.RequestsPerMinute), now), 1)
	}
	if !l.inputBucket.canConsume(cfg.InputTokensPerMinute, cost.InputTokens, now) {
		return nil, rateLimitError("input_tokens_per_minute", cfg.InputTokensPerMinute, l.inputBucket.used(cfg.InputTokensPerMinute, now), cost.InputTokens)
	}
	if !l.outputBucket.canConsume(cfg.OutputTokensPerMinute, cost.OutputTokens, now) {
		return nil, rateLimitError("output_tokens_per_minute", cfg.OutputTokensPerMinute, l.outputBucket.used(cfg.OutputTokensPerMinute, now), cost.OutputTokens)
	}
	if !l.tokenBucket.canConsume(cfg.TokensPerMinute, cost.totalTokens(), now) {
		return nil, rateLimitError("tokens_per_minute", cfg.TokensPerMinute, l.tokenBucket.used(cfg.TokensPerMinute, now), cost.totalTokens())
	}
	if exceedsLimit(l.dayReqs, 1, uint64(cfg.RequestsPerDay)) {
		return nil, rateLimitError("requests_per_day", uint64(cfg.RequestsPerDay), l.dayReqs, 1)
	}
	if exceedsLimit(l.dayTokens, cost.totalTokens(), cfg.TokensPerDay) {
		return nil, rateLimitError("tokens_per_day", cfg.TokensPerDay, l.dayTokens, cost.totalTokens())
	}

	l.inFlight++
	l.requestBucket.consume(uint64(cfg.RequestsPerMinute), 1, now)
	l.inputBucket.consume(cfg.InputTokensPerMinute, cost.InputTokens, now)
	l.outputBucket.consume(cfg.OutputTokensPerMinute, cost.OutputTokens, now)
	l.tokenBucket.consume(cfg.TokensPerMinute, cost.totalTokens(), now)
	l.dayReqs++
	l.dayTokens += cost.totalTokens()

	return &llmRateLimitReservation{
		limiter:   l,
		cost:      cost,
		policy:    l.policy,
		dayWindow: l.dayWindow,
	}, nil
}

func (l *llmRateLimiter) resetWindowsLocked(now time.Time) {
	day := now.Format("2006-01-02")
	if l.dayWindow == "" || l.dayWindow != day {
		l.dayWindow = day
		l.dayReqs = 0
		l.dayTokens = 0
	}
}

func (r *llmRateLimitReservation) settle(usage *llm.TokenUsage, callErr error) {
	if r == nil || r.limiter == nil {
		return
	}
	l := r.limiter
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight > 0 {
		l.inFlight--
	}
	if usage == nil && callErr == nil {
		return
	}
	actual := actualCost(usage, r.policy)
	cfg := l.cfg
	l.inputBucket.adjust(cfg.InputTokensPerMinute, r.cost.InputTokens, actual.InputTokens, now)
	l.outputBucket.adjust(cfg.OutputTokensPerMinute, r.cost.OutputTokens, actual.OutputTokens, now)
	l.tokenBucket.adjust(cfg.TokensPerMinute, r.cost.totalTokens(), actual.totalTokens(), now)
	if l.dayWindow == r.dayWindow {
		adjustCounter(&l.dayTokens, r.cost.totalTokens(), actual.totalTokens())
	}
}

func (l *llmRateLimiter) snapshot(now time.Time) LLMRateLimitSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetWindowsLocked(now)
	cfg := l.cfg
	return LLMRateLimitSnapshot{
		Key:                            l.meta.Key,
		Pool:                           l.meta.Pool,
		Provider:                       l.meta.Provider,
		Credential:                     l.meta.Credential,
		Model:                          l.meta.Model,
		BaseURL:                        l.meta.BaseURL,
		InFlight:                       l.inFlight,
		MaxConcurrentLimit:             cfg.MaxConcurrent,
		RequestsPerMinuteLimit:         uint64(cfg.RequestsPerMinute),
		RequestsPerMinuteRemaining:     l.requestBucket.remaining(uint64(cfg.RequestsPerMinute), now),
		InputTokensPerMinuteLimit:      cfg.InputTokensPerMinute,
		InputTokensPerMinuteRemaining:  l.inputBucket.remaining(cfg.InputTokensPerMinute, now),
		OutputTokensPerMinuteLimit:     cfg.OutputTokensPerMinute,
		OutputTokensPerMinuteRemaining: l.outputBucket.remaining(cfg.OutputTokensPerMinute, now),
		TokensPerMinuteLimit:           cfg.TokensPerMinute,
		TokensPerMinuteRemaining:       l.tokenBucket.remaining(cfg.TokensPerMinute, now),
		RequestsPerDayLimit:            uint64(cfg.RequestsPerDay),
		RequestsPerDayUsed:             l.dayReqs,
		TokensPerDayLimit:              cfg.TokensPerDay,
		TokensPerDayUsed:               l.dayTokens,
		DayWindow:                      l.dayWindow,
	}
}

func responseUsage(resp *llm.Response) *llm.TokenUsage {
	if resp == nil {
		return nil
	}
	return resp.Usage
}

func actualCost(usage *llm.TokenUsage, policy llmRateLimitPolicy) llmRateLimitCost {
	if usage == nil {
		return llmRateLimitCost{}
	}
	input := nonNegativeTokens(usage.InputTokens) + nonNegativeTokens(usage.CacheCreationInputTokens)
	if policy.CountCacheReadInputTokens {
		input += nonNegativeTokens(usage.CacheReadInputTokens)
	}
	return llmRateLimitCost{
		InputTokens:  input,
		OutputTokens: nonNegativeTokens(usage.OutputTokens),
	}
}

func nonNegativeTokens(tokens int64) uint64 {
	if tokens <= 0 {
		return 0
	}
	return uint64(tokens)
}

func adjustCounter(counter *uint64, reserved, actual uint64) {
	switch {
	case actual > reserved:
		*counter += actual - reserved
	case reserved > actual:
		delta := reserved - actual
		if *counter >= delta {
			*counter -= delta
		} else {
			*counter = 0
		}
	}
}

type llmTokenBucket struct {
	limit     uint64
	available float64
	updatedAt time.Time
}

func (b *llmTokenBucket) refill(limit uint64, now time.Time) {
	if limit == 0 {
		b.limit = 0
		b.available = 0
		b.updatedAt = now
		return
	}
	if b.updatedAt.IsZero() || b.limit == 0 {
		b.limit = limit
		b.available = float64(limit)
		b.updatedAt = now
		return
	}
	if b.limit != limit {
		b.limit = limit
		if b.available > float64(limit) {
			b.available = float64(limit)
		}
		b.updatedAt = now
		return
	}
	if elapsed := now.Sub(b.updatedAt); elapsed > 0 {
		b.available += elapsed.Seconds() * float64(limit) / 60
		if b.available > float64(limit) {
			b.available = float64(limit)
		}
		b.updatedAt = now
	}
}

func (b *llmTokenBucket) canConsume(limit, amount uint64, now time.Time) bool {
	if limit == 0 {
		return true
	}
	b.refill(limit, now)
	if amount > limit {
		return false
	}
	return b.available+1e-9 >= float64(amount)
}

func (b *llmTokenBucket) consume(limit, amount uint64, now time.Time) {
	if limit == 0 || amount == 0 {
		return
	}
	b.refill(limit, now)
	b.available -= float64(amount)
}

func (b *llmTokenBucket) adjust(limit, reserved, actual uint64, now time.Time) {
	if limit == 0 || reserved == actual {
		return
	}
	if actual > reserved {
		b.charge(limit, actual-reserved, now)
		return
	}
	b.refund(limit, reserved-actual, now)
}

func (b *llmTokenBucket) charge(limit, amount uint64, now time.Time) {
	if limit == 0 || amount == 0 {
		return
	}
	b.refill(limit, now)
	b.available -= float64(amount)
}

func (b *llmTokenBucket) refund(limit, amount uint64, now time.Time) {
	if limit == 0 || amount == 0 {
		return
	}
	b.refill(limit, now)
	b.available += float64(amount)
	if b.available > float64(limit) {
		b.available = float64(limit)
	}
}

func (b *llmTokenBucket) remaining(limit uint64, now time.Time) uint64 {
	if limit == 0 {
		return 0
	}
	b.refill(limit, now)
	if b.available <= 0 {
		return 0
	}
	if b.available >= float64(limit) {
		return limit
	}
	return uint64(math.Floor(b.available + 1e-9))
}

func (b *llmTokenBucket) used(limit uint64, now time.Time) uint64 {
	if limit == 0 {
		return 0
	}
	remaining := b.remaining(limit, now)
	if remaining >= limit {
		return 0
	}
	return limit - remaining
}

func exceedsLimit(current, add, limit uint64) bool {
	if limit == 0 {
		return false
	}
	if add > limit {
		return true
	}
	return current > limit-add
}

func rateLimitError(axis string, limit, used, requested uint64) error {
	return fmt.Errorf("%w: %s limit=%d used=%d requested=%d", ErrLLMRateLimitExceeded, axis, limit, used, requested)
}

func estimateLLMInputTokens(messages []core.LlmMessage) uint64 {
	total := 0
	for _, msg := range messages {
		total += 4
		total += EstimateTokens(msg.Content)
		for _, block := range msg.ContentBlocks {
			total += estimateContentBlockTokens(block)
		}
	}
	if total < 0 {
		return 0
	}
	return uint64(total)
}

func estimateLLMToolTokens(tools []llm.Tool) uint64 {
	total := 0
	for _, tool := range tools {
		total += 4
		total += EstimateTokens(tool.Name)
		total += EstimateTokens(tool.Description)
		if len(tool.InputSchema) > 0 {
			if data, err := json.Marshal(tool.InputSchema); err == nil {
				total += EstimateTokens(string(data))
			}
		}
	}
	if total < 0 {
		return 0
	}
	return uint64(total)
}

func estimateContentBlockTokens(block core.ContentBlock) int {
	switch block.Type {
	case core.BlockTypeText:
		return EstimateTokens(block.Text)
	case core.BlockTypeToolResult:
		return EstimateTokens(block.ToolUseID) + EstimateTokens(block.Content)
	case core.BlockTypeToolUse:
		total := EstimateTokens(block.ID) + EstimateTokens(block.Name)
		if len(block.Input) > 0 {
			if data, err := json.Marshal(block.Input); err == nil {
				total += EstimateTokens(string(data))
			}
		}
		return total
	default:
		total := EstimateTokens(block.Text) + EstimateTokens(block.Content) + EstimateTokens(block.Name)
		if len(block.Input) > 0 {
			if data, err := json.Marshal(block.Input); err == nil {
				total += EstimateTokens(string(data))
			}
		}
		return total
	}
}
