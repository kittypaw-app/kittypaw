package core

import (
	"fmt"
	"sync/atomic"
	"time"
)

// AccountHealth is the observable liveness of an account runtime inside the
// single-server multi-account server. It is NOT a process-level health —
// the server can only tell you "my own goroutines for this account are
// looking OK right now", not "the account is reachable end-to-end".
//
// States and their meanings:
//
//   - Ready: steady state. Scheduler ticks and dispatch loop iterations
//     are completing without panic on this account's goroutines.
//   - Degraded: at least one goroutine panicked recently and was caught
//     by a defer recover(). The account may self-heal on the next tick
//     that completes cleanly — callers should not treat Degraded as
//     terminal. This preserves account-level panic isolation.
//   - Stopped: the server is shutting this account down. Terminal — once
//     Stopped, the state never moves back to Ready or Degraded, so a
//     late goroutine that wakes up and tries to MarkReady cannot make
//     the account appear live again.
type AccountHealth int32

const (
	AccountHealthReady AccountHealth = iota
	AccountHealthDegraded
	AccountHealthStopped
)

// String returns a stable, human-readable label. The default branch keeps
// the numeric tag so structured logs still carry signal if the enum
// grows and an older binary observes a new value.
func (h AccountHealth) String() string {
	switch h {
	case AccountHealthReady:
		return "Ready"
	case AccountHealthDegraded:
		return "Degraded"
	case AccountHealthStopped:
		return "Stopped"
	default:
		return fmt.Sprintf("AccountHealth(%d)", int32(h))
	}
}

// HealthState is a concurrency-safe health tracker. One instance per
// account runtime. Reads are racy only in the sense that they see a
// snapshot — callers never need a mutex because every field is atomic.
//
// The lastPanic timestamp is retained across MarkReady on purpose: it is
// an audit breadcrumb ("this account panicked 43 minutes ago") rather
// than a live flag. Observers that want the combined signal should
// compare Load() to the timestamp themselves.
type HealthState struct {
	state       atomic.Int32 // holds a AccountHealth value
	lastPanicNs atomic.Int64 // unix nano of most recent panic, 0 if never
}

// NewHealthState returns a HealthState initialized to Ready.
func NewHealthState() *HealthState {
	s := &HealthState{}
	s.state.Store(int32(AccountHealthReady))
	return s
}

// Load returns the current health state.
func (s *HealthState) Load() AccountHealth {
	return AccountHealth(s.state.Load())
}

// LastPanic returns the time of the most recent MarkDegraded call, or
// the zero Time if the account has never been Degraded.
func (s *HealthState) LastPanic() time.Time {
	ns := s.lastPanicNs.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// MarkDegraded records a panic at now and transitions to Degraded. No-op
// if the account is already Stopped — shutdown wins over recovery so a
// late goroutine can't resurrect a Stopped account in the health readout.
func (s *HealthState) MarkDegraded(now time.Time) {
	for {
		cur := s.state.Load()
		if cur == int32(AccountHealthStopped) {
			return
		}
		if s.state.CompareAndSwap(cur, int32(AccountHealthDegraded)) {
			s.lastPanicNs.Store(now.UnixNano())
			return
		}
	}
}

// MarkReady transitions back to Ready. No-op if Stopped. Intended to be
// called after a scheduler tick or event dispatch completes without
// panic, so transient panics self-heal instead of latching forever.
func (s *HealthState) MarkReady() {
	for {
		cur := s.state.Load()
		if cur == int32(AccountHealthStopped) {
			return
		}
		if s.state.CompareAndSwap(cur, int32(AccountHealthReady)) {
			return
		}
	}
}

// MarkStopped flips the account into the terminal Stopped state. After
// this call, MarkReady and MarkDegraded are no-ops.
func (s *HealthState) MarkStopped() {
	s.state.Store(int32(AccountHealthStopped))
}
