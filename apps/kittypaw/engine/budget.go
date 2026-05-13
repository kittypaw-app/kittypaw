package engine

import (
	"sync/atomic"

	"github.com/jinto/kittypaw/llm"
)

// SharedTokenBudget tracks shared token consumption across LLM-spending
// features (orchestration delegation, MoA fan-out + synthesis,
// File.summary). Thread-safe via atomic CAS. A zero limit means unlimited.
//
// The budget is advisory post-hoc: TrySpend is called after a provider
// returns, so concurrent children may briefly overshoot the limit.
type SharedTokenBudget struct {
	limit uint64
	used  atomic.Uint64
}

// NewSharedBudget creates a budget with the given token limit.
// A limit of 0 means unlimited.
func NewSharedBudget(limit uint64) *SharedTokenBudget {
	return &SharedTokenBudget{limit: limit}
}

// TrySpend attempts to deduct amount tokens from the budget using an
// atomic compare-and-swap loop. Returns true if the spend succeeded,
// false if it would exceed the limit.
func (b *SharedTokenBudget) TrySpend(amount uint64) bool {
	if b.limit == 0 {
		b.used.Add(amount)
		return true // unlimited
	}

	for {
		cur := b.used.Load()
		if cur+amount > b.limit {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+amount) {
			return true
		}
		// CAS failed — another goroutine spent concurrently; retry.
	}
}

// TrySpendFromUsage is a convenience wrapper that deducts input+output
// tokens from an LLM response's usage. Negative token counts (from
// erroneous LLM responses) are treated as zero to prevent uint64 overflow.
func (b *SharedTokenBudget) TrySpendFromUsage(usage *llm.TokenUsage) bool {
	if usage == nil {
		return true
	}
	return b.TrySpend(uint64(tokenUsageTotal(usage)))
}

func tokenUsageTotal(usage *llm.TokenUsage) int64 {
	if usage == nil {
		return 0
	}
	in := usage.InputTokens
	out := usage.OutputTokens
	if in < 0 {
		in = 0
	}
	if out < 0 {
		out = 0
	}
	return in + out
}

// Remaining returns how many tokens are left. Returns ^uint64(0) (max)
// when the budget is unlimited.
func (b *SharedTokenBudget) Remaining() uint64 {
	if b.limit == 0 {
		return ^uint64(0) // unlimited sentinel
	}
	cur := b.used.Load()
	if cur >= b.limit {
		return 0
	}
	return b.limit - cur
}

// Used returns the total tokens consumed so far.
func (b *SharedTokenBudget) Used() uint64 {
	return b.used.Load()
}
