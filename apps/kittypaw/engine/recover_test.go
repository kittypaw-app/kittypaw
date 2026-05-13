package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

func TestRecoverAccountPanic_MarksDegradedAndRecordsStamp(t *testing.T) {
	sess := &AccountRuntime{
		AccountID: "alice",
		Health:    core.NewHealthState(),
	}

	RecoverAccountPanic(sess, "test.site", "boom")

	if got := sess.Health.Load(); got != core.AccountHealthDegraded {
		t.Errorf("Health = %v, want Degraded", got)
	}
	if sess.Health.LastPanic().IsZero() {
		t.Errorf("LastPanic should be set after recover")
	}
}

func TestRecoverAccountPanic_NilSessionSafe(t *testing.T) {
	// Must not panic — callers include dispatchLoop fallbacks where
	// the session lookup may have returned nil just before the crash.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecoverAccountPanic panicked on nil session: %v", r)
		}
	}()
	RecoverAccountPanic(nil, "test.site", "boom")
}

func TestRecoverAccountPanic_NilHealthSafe(t *testing.T) {
	// Bare-struct test fixtures omit Health; the helper should still
	// log instead of crashing.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecoverAccountPanic panicked on nil Health: %v", r)
		}
	}()
	RecoverAccountPanic(&AccountRuntime{AccountID: "alice"}, "test.site", "boom")
}

func TestMarkAccountReady_TransitionsDegradedToReady(t *testing.T) {
	sess := &AccountRuntime{
		AccountID: "alice",
		Health:    core.NewHealthState(),
	}
	sess.Health.MarkDegraded(time.Now())

	MarkAccountReady(sess)

	if got := sess.Health.Load(); got != core.AccountHealthReady {
		t.Errorf("Health after MarkAccountReady = %v, want Ready", got)
	}
	// LastPanic is audit history; recovery should not erase it.
	if sess.Health.LastPanic().IsZero() {
		t.Errorf("LastPanic should persist after recovery")
	}
}

func TestMarkAccountReady_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MarkAccountReady panicked: %v", r)
		}
	}()
	MarkAccountReady(nil)
	MarkAccountReady(&AccountRuntime{})
}

// TestAccountPanicIsolation_AC_T8 demonstrates the invariant the
// family-multi-account spec requires in AC-T8: a panic in one account's
// goroutine, caught by a deferred RecoverAccountPanic, does not prevent a
// sibling account's goroutine from continuing to make progress. This is
// the minimum empirical proof that the recover helpers glue together
// into the isolation contract the spec demands.
func TestAccountPanicIsolation_AC_T8(t *testing.T) {
	alice := &AccountRuntime{AccountID: "alice", Health: core.NewHealthState()}
	bob := &AccountRuntime{AccountID: "bob", Health: core.NewHealthState()}

	var bobTicks int32
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				RecoverAccountPanic(alice, "test.alice.scheduler", r)
			}
		}()
		panic("alice simulated panic")
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				RecoverAccountPanic(bob, "test.bob.scheduler", r)
			}
		}()
		// Simulate the "next 5 ticks" — AC-T8 asks for "tick_count ≥
		// expected" after the sibling panics.
		for i := 0; i < 5; i++ {
			atomic.AddInt32(&bobTicks, 1)
		}
		MarkAccountReady(bob)
	}()

	wg.Wait()

	if got := alice.Health.Load(); got != core.AccountHealthDegraded {
		t.Errorf("alice Health = %v, want Degraded", got)
	}
	if got := bob.Health.Load(); got != core.AccountHealthReady {
		t.Errorf("bob Health = %v, want Ready (bob never panicked)", got)
	}
	if ticks := atomic.LoadInt32(&bobTicks); ticks != 5 {
		t.Errorf("bob ticks = %d, want 5 (alice's panic must not gate bob)", ticks)
	}
}

// TestSchedulerTickRecovers verifies that tickOnce — the wrapper the
// Scheduler.Start loop actually calls every minute — survives a panic
// inside checkAndRun. If this guard ever regresses, a single bad skill
// load would silently kill the scheduler goroutine for the whole
// server lifetime.
func TestSchedulerTickRecovers(t *testing.T) {
	// Build a minimal Scheduler whose session has no Store — checkAndRun
	// will nil-deref on Store.GetLastRun or LoadAllSkillsFrom and panic.
	// tickOnce must catch that panic rather than propagate it.
	sess := &AccountRuntime{
		AccountID: "alice",
		Health:    core.NewHealthState(),
		Config:    &core.Config{},
	}
	s := NewScheduler(sess, nil)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tickOnce leaked panic to caller: %v", r)
		}
	}()

	// checkAndRun tries to load skills from an empty BaseDir and would
	// nil-deref on Store if any skill were due. tickOnce's recover
	// block must catch whatever surfaces — the assertion is that no
	// panic escapes to this test goroutine.
	s.tickOnce(context.Background())
}
