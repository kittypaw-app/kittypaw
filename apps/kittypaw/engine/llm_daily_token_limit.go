package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
	"github.com/jinto/kittypaw/store"
)

var ErrDailyTokenLimitExceeded = errors.New("daily token limit exceeded")

// DailyTokenLimiter reserves account-wide daily LLM tokens across concurrent
// provider calls. Persisted usage remains the source of truth; reservations
// close the gap before the usage recorder has committed a successful call.
type DailyTokenLimiter struct {
	mu       sync.Mutex
	day      string
	reserved uint64
}

type DailyTokenLimitSnapshot struct {
	Limit     uint64 `json:"limit"`
	Used      uint64 `json:"used"`
	Reserved  uint64 `json:"reserved"`
	Remaining uint64 `json:"remaining"`
	DayWindow string `json:"day_window"`
}

func NewDailyTokenLimiter() *DailyTokenLimiter {
	return &DailyTokenLimiter{}
}

func (l *DailyTokenLimiter) Snapshot(limit, used uint64) DailyTokenLimitSnapshot {
	if l == nil {
		return DailyTokenLimitSnapshot{Limit: limit, Used: used}
	}
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetDayLocked(now)
	remaining := uint64(0)
	if limit > 0 && used < limit {
		available := limit - used
		if l.reserved < available {
			remaining = available - l.reserved
		}
	}
	return DailyTokenLimitSnapshot{
		Limit:     limit,
		Used:      used,
		Reserved:  l.reserved,
		Remaining: remaining,
		DayWindow: l.day,
	}
}

func NewDailyTokenLimitedProvider(inner llm.Provider, limiter *DailyTokenLimiter, st *store.Store, limit uint64, model core.ModelConfig) llm.Provider {
	if inner == nil || limiter == nil || st == nil || limit == 0 {
		return inner
	}
	if _, ok := inner.(*dailyTokenLimitedProvider); ok {
		return inner
	}
	policy := rateLimitPolicyForModel(model)
	policy.ReserveOutputTokens = true
	policy.CountCacheReadInputTokens = true
	return &dailyTokenLimitedProvider{
		inner:   inner,
		limiter: limiter,
		store:   st,
		limit:   limit,
		policy:  policy,
	}
}

type dailyTokenLimitedProvider struct {
	inner   llm.Provider
	limiter *DailyTokenLimiter
	store   *store.Store
	limit   uint64
	policy  llmRateLimitPolicy
}

func (p *dailyTokenLimitedProvider) Generate(ctx context.Context, messages []core.LlmMessage) (*llm.Response, error) {
	reservation, err := p.reserve(ctx, messages, nil)
	if err != nil {
		return nil, err
	}
	defer reservation.settle()
	return p.inner.Generate(ctx, messages)
}

func (p *dailyTokenLimitedProvider) GenerateWithTools(ctx context.Context, messages []core.LlmMessage, tools []llm.Tool) (*llm.Response, error) {
	reservation, err := p.reserve(ctx, messages, tools)
	if err != nil {
		return nil, err
	}
	defer reservation.settle()
	return p.inner.GenerateWithTools(ctx, messages, tools)
}

func (p *dailyTokenLimitedProvider) ContextWindow() int { return p.inner.ContextWindow() }

func (p *dailyTokenLimitedProvider) MaxTokens() int { return p.inner.MaxTokens() }

func (p *dailyTokenLimitedProvider) reserve(ctx context.Context, messages []core.LlmMessage, tools []llm.Tool) (*dailyTokenReservation, error) {
	cost := estimateLLMCallTokens(p.inner, messages, tools, p.policy).totalTokens()
	return p.limiter.reserve(ctx, p.store, p.limit, cost)
}

type dailyTokenReservation struct {
	limiter *DailyTokenLimiter
	day     string
	amount  uint64
}

func (l *DailyTokenLimiter) reserve(ctx context.Context, st *store.Store, limit, amount uint64) (*dailyTokenReservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l == nil || st == nil || limit == 0 {
		return &dailyTokenReservation{}, nil
	}
	now := time.Now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.resetDayLocked(now)

	stats, err := st.TodayStats()
	if err != nil {
		return nil, fmt.Errorf("daily token limit check: %w", err)
	}
	used := uint64(0)
	if stats.TotalTokens > 0 {
		used = uint64(stats.TotalTokens)
	}
	if wouldExceedLimit(used, l.reserved, amount, limit) {
		return nil, dailyTokenLimitError(limit, used, l.reserved, amount)
	}
	l.reserved += amount
	return &dailyTokenReservation{
		limiter: l,
		day:     l.day,
		amount:  amount,
	}, nil
}

func (l *DailyTokenLimiter) resetDayLocked(now time.Time) {
	day := now.Format("2006-01-02")
	if l.day == "" || l.day != day {
		l.day = day
		l.reserved = 0
	}
}

func (r *dailyTokenReservation) settle() {
	if r == nil || r.limiter == nil || r.amount == 0 {
		return
	}
	l := r.limiter
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.day != r.day {
		return
	}
	if l.reserved >= r.amount {
		l.reserved -= r.amount
		return
	}
	l.reserved = 0
}

func wouldExceedLimit(used, reserved, requested, limit uint64) bool {
	if limit == 0 {
		return false
	}
	if used > limit {
		return true
	}
	if reserved > limit-used {
		return true
	}
	return requested > limit-used-reserved
}

func dailyTokenLimitError(limit, used, reserved, requested uint64) error {
	return fmt.Errorf("%w: limit=%d used=%d reserved=%d requested=%d", ErrDailyTokenLimitExceeded, limit, used, reserved, requested)
}
