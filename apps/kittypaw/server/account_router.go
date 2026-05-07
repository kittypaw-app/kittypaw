package server

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

// AccountRouter dispatches inbound events to account-scoped engine sessions.
//
// Lookup is strict by design: events with an empty AccountID or a AccountID
// that does not match a registered session are dropped. There is NO default
// fallback — a silent fallback in a multi-account deployment would route
// another user's messages into the default account's runner state (privacy
// leak). See the account-routing privacy constraint.
type AccountRouter struct {
	mu        sync.RWMutex
	sessions  map[string]*engine.Session
	dropCount atomic.Int64
	// mismatchCount tracks per-account chat_id ownership violations (AC-T7).
	// Keyed by the account ID the event claimed, not the real owner —
	// `account_routing_mismatch_total{from=<accountID>}` is the spec's
	// external-facing metric label, so the local key mirrors that shape.
	mismatchCount sync.Map // map[string]*atomic.Int64
}

// NewAccountRouter returns an empty router. Callers must Register sessions
// before events arrive; unregistered accounts route to nil (drop).
func NewAccountRouter() *AccountRouter {
	return &AccountRouter{sessions: make(map[string]*engine.Session)}
}

// Register adds or replaces the session for accountID.
func (r *AccountRouter) Register(accountID string, sess *engine.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[accountID] = sess
}

// Remove deletes the session for accountID. Returns true if one was present.
func (r *AccountRouter) Remove(accountID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[accountID]; !ok {
		return false
	}
	delete(r.sessions, accountID)
	return true
}

// Route returns the session matching event.AccountID, or nil if the event
// should be dropped. Empty or unknown AccountID increments the drop counter
// and logs an account_routing_drop event. Callers MUST check for nil and
// stop processing rather than substitute a default.
func (r *AccountRouter) Route(event core.Event) *engine.Session {
	if event.AccountID == "" {
		r.dropCount.Add(1)
		slog.Warn("account_routing_drop",
			"reason", "empty_account_id",
			"type", event.Type,
		)
		return nil
	}
	r.mu.RLock()
	sess, ok := r.sessions[event.AccountID]
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
	return sess
}

// Sessions returns a snapshot of registered account IDs.
func (r *AccountRouter) Sessions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	return ids
}

// Session returns the session registered for accountID, or nil if none.
// Unlike Route, this does not count drops — use it for administrative
// lookups (HTTP handlers, tests) rather than event dispatch.
func (r *AccountRouter) Session(accountID string) *engine.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[accountID]
}

// DropCount returns the cumulative number of events dropped because their
// AccountID was empty or unknown.
func (r *AccountRouter) DropCount() int64 {
	return r.dropCount.Load()
}

// RecordMismatch increments the per-account chat_id ownership violation
// counter. Callers use this *after* a successful Route() when the routed
// session's Config.AllowedChatIDs rejects the event's chat_id — the event
// must be dropped and not fed to Session.Run (AC-T7).
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
