package server

import (
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

func TestAccountRouter_RouteRegistered(t *testing.T) {
	alice := &engine.AccountRuntime{BaseDir: "/tmp/alice"}
	bob := &engine.AccountRuntime{BaseDir: "/tmp/bob"}

	r := NewAccountRouter()
	r.Register("alice", alice)
	r.Register("bob", bob)

	got := r.Route(core.Event{AccountID: "alice", Type: core.EventTelegram})
	if got != alice {
		t.Errorf("Route(alice) got %p, want %p", got, alice)
	}
	got = r.Route(core.Event{AccountID: "bob", Type: core.EventTelegram})
	if got != bob {
		t.Errorf("Route(bob) got %p, want %p", got, bob)
	}
}

// TestAccountRouter_NoFallback enforces C1: empty or unknown AccountID must
// drop — never fall through to a default account (cross-account leak risk).
func TestAccountRouter_NoFallback(t *testing.T) {
	alice := &engine.AccountRuntime{BaseDir: "/tmp/alice"}
	r := NewAccountRouter()
	r.Register("alice", alice)
	r.Register("default", alice) // default exists, but unknown must still drop

	tests := []struct {
		name  string
		event core.Event
	}{
		{"empty_account_id", core.Event{AccountID: "", Type: core.EventTelegram}},
		{"unknown_account", core.Event{AccountID: "charlie", Type: core.EventTelegram}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Route(tt.event)
			if got != nil {
				t.Errorf("Route() = %p, want nil (drop, no fallback)", got)
			}
		})
	}

	if n := r.DropCount(); n != 2 {
		t.Errorf("DropCount = %d, want 2", n)
	}
}

func TestAccountRouter_RemoveAndAccountIDs(t *testing.T) {
	r := NewAccountRouter()
	r.Register("alice", &engine.AccountRuntime{})
	r.Register("bob", &engine.AccountRuntime{})

	if got := r.Route(core.Event{AccountID: "alice"}); got == nil {
		t.Error("alice should be routable")
	}

	if !r.Remove("alice") {
		t.Error("Remove(alice) = false, want true")
	}
	if r.Remove("alice") {
		t.Error("Remove(alice) second call = true, want false")
	}

	if got := r.Route(core.Event{AccountID: "alice"}); got != nil {
		t.Error("alice should be gone after Remove")
	}

	ids := r.AccountIDs()
	if len(ids) != 1 || ids[0] != "bob" {
		t.Errorf("AccountIDs() = %v, want [bob]", ids)
	}
}

// TestAccountRouter_MismatchCounters locks in the AC-T7 metric surface:
// RecordMismatch bumps a per-account counter; MismatchCount reads only that
// account's count and returns 0 for untouched accounts without allocating.
// The /metrics endpoint surfaces this as
// `account_routing_mismatch_total{from=<accountID>}`.
func TestAccountRouter_MismatchCounters(t *testing.T) {
	r := NewAccountRouter()

	if n := r.MismatchCount("alice"); n != 0 {
		t.Errorf("MismatchCount(alice) initial = %d, want 0", n)
	}

	r.RecordMismatch("alice")
	r.RecordMismatch("alice")
	r.RecordMismatch("bob")

	if n := r.MismatchCount("alice"); n != 2 {
		t.Errorf("MismatchCount(alice) = %d, want 2", n)
	}
	if n := r.MismatchCount("bob"); n != 1 {
		t.Errorf("MismatchCount(bob) = %d, want 1", n)
	}
	if n := r.MismatchCount("charlie"); n != 0 {
		t.Errorf("MismatchCount(charlie) = %d, want 0 (unseen account)", n)
	}
}

func TestAccountRouter_ConcurrentAccess(t *testing.T) {
	r := NewAccountRouter()
	sess := &engine.AccountRuntime{}
	r.Register("alice", sess)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = r.Route(core.Event{AccountID: "alice"})
				_ = r.Route(core.Event{AccountID: "unknown"})
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if dc := r.DropCount(); dc != 1000 {
		t.Errorf("DropCount = %d, want 1000", dc)
	}
}
