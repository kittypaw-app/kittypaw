package server

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

// AccountRouter dispatches inbound events to account-scoped engine runtimes.
//
// Lookup is strict by design: events with an empty AccountID or a AccountID
// that does not match a registered runtime are dropped. There is NO default
// fallback — a silent fallback in a multi-account deployment would route
// another user's messages into the default account's runner state (privacy
// leak). See the account-routing privacy constraint.
type AccountRouter struct {
	mu        sync.RWMutex
	runtimes  map[string]*engine.AccountRuntime
	dropCount atomic.Int64
	// mismatchCount tracks per-account chat_id ownership violations (AC-T7).
	// Keyed by the account ID the event claimed, not the real owner —
	// `account_routing_mismatch_total{from=<accountID>}` is the spec's
	// external-facing metric label, so the local key mirrors that shape.
	mismatchCount sync.Map // map[string]*atomic.Int64
}

// NewAccountRouter returns an empty router. Callers must Register runtimes
// before events arrive; unregistered accounts route to nil (drop).
func NewAccountRouter() *AccountRouter {
	return &AccountRouter{runtimes: make(map[string]*engine.AccountRuntime)}
}

// Register adds or replaces the runtime for accountID.
func (r *AccountRouter) Register(accountID string, runtime *engine.AccountRuntime) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runtimes[accountID] = runtime
}

// Remove deletes the runtime for accountID. Returns true if one was present.
func (r *AccountRouter) Remove(accountID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runtimes[accountID]; !ok {
		return false
	}
	delete(r.runtimes, accountID)
	return true
}

// Route returns the runtime matching event.AccountID, or nil if the event
// should be dropped. Empty or unknown AccountID increments the drop counter
// and logs an account_routing_drop event. Callers MUST check for nil and
// stop processing rather than substitute a default.
func (r *AccountRouter) Route(event core.Event) *engine.AccountRuntime {
	if event.AccountID == "" {
		r.dropCount.Add(1)
		slog.Warn("account_routing_drop",
			"reason", "empty_account_id",
			"type", event.Type,
		)
		return nil
	}
	r.mu.RLock()
	runtime, ok := r.runtimes[event.AccountID]
	r.mu.RUnlock()
	if !ok {
		r.dropCount.Add(1)
		slog.Warn("account_routing_drop",
			"reason", "unknown_account",
			"account", event.AccountID,
			"type", event.Type,
		)
		return nil
	}
	return runtime
}

// AccountIDs returns a snapshot of registered account IDs.
func (r *AccountRouter) AccountIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.runtimes))
	for id := range r.runtimes {
		ids = append(ids, id)
	}
	return ids
}

// Runtime returns the account runtime registered for accountID, or nil if none.
// Unlike Route, this does not count drops — use it for administrative
// lookups (HTTP handlers, tests) rather than event dispatch.
func (r *AccountRouter) Runtime(accountID string) *engine.AccountRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.runtimes[accountID]
}

// DropCount returns the cumulative number of events dropped because their
// AccountID was empty or unknown.
func (r *AccountRouter) DropCount() int64 {
	return r.dropCount.Load()
}

// RecordMismatch increments the per-account chat_id ownership violation
// counter. Callers use this *after* a successful Route() when the routed
// runtime's Config.AllowedChatIDs rejects the event's chat_id — the event
// must be dropped and not fed to AccountRuntime.Run (AC-T7).
func (r *AccountRouter) RecordMismatch(accountID string) {
	v, _ := r.mismatchCount.LoadOrStore(accountID, &atomic.Int64{})
	v.(*atomic.Int64).Add(1)
}

// MismatchCount returns the cumulative mismatch count for accountID, or 0
// when no mismatches have been recorded for that account. Used by tests and
// the /metrics endpoint to expose `account_routing_mismatch_total{from=...}`.
func (r *AccountRouter) MismatchCount(accountID string) int64 {
	v, ok := r.mismatchCount.Load(accountID)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}
