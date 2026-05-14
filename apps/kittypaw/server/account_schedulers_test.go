package server

import (
	"context"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

func TestServerNewCreatesSchedulerPerAccount(t *testing.T) {
	root := t.TempDir()
	teamCfg := core.DefaultConfig()
	teamCfg.IsFamily = true
	teamCfg.TeamSpace.Members = []string{"alice", "bob"}
	aliceCfg := core.DefaultConfig()
	bobCfg := core.DefaultConfig()

	teamDeps := buildAccountDeps(t, root, "team", &teamCfg)
	aliceDeps := buildAccountDeps(t, root, "alice", &aliceCfg)
	bobDeps := buildAccountDeps(t, root, "bob", &bobCfg)

	srv := New([]*AccountDeps{teamDeps, aliceDeps, bobDeps}, "test")

	if srv.schedulers == nil {
		t.Fatal("schedulers is nil")
	}
	if got := srv.schedulers.Len(); got != 3 {
		t.Fatalf("scheduler count = %d, want 3", got)
	}
	for _, accountID := range []string{"team", "alice", "bob"} {
		if !srv.schedulers.Has(accountID) {
			t.Fatalf("scheduler for %q missing", accountID)
		}
	}
}

func TestAddRemoveAccountMaintainsScheduler(t *testing.T) {
	root := t.TempDir()
	srv := newServerForAdminTest(t, root, nil)

	alice := accountForDirectAdd(root, "alice", false, nil)
	if err := srv.AddAccount(alice); err != nil {
		t.Fatalf("AddAccount: %v", err)
	}
	if !srv.schedulers.Has("alice") {
		t.Fatal("scheduler for alice missing after AddAccount")
	}

	if err := srv.RemoveAccount("alice"); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if srv.schedulers.Has("alice") {
		t.Fatal("scheduler for alice still registered after RemoveAccount")
	}
}

func TestAccountSchedulersReplaceReturnsPreviousScheduler(t *testing.T) {
	schedulers := NewAccountSchedulers()
	first := engine.NewScheduler(&engine.AccountRuntime{}, nil)
	second := engine.NewScheduler(&engine.AccountRuntime{}, nil)
	third := engine.NewScheduler(&engine.AccountRuntime{}, nil)

	schedulers.Register("alice", first)
	if got := schedulers.Replace("alice", second); got != first {
		t.Fatalf("first Replace returned %p, want first scheduler %p", got, first)
	}
	first.Wait()
	if got := schedulers.Replace("alice", third); got != second {
		t.Fatalf("second Replace returned %p, want second scheduler %p", got, second)
	}
	second.Wait()
	if !schedulers.Has("alice") {
		t.Fatal("alice scheduler missing after Replace")
	}
	if got := schedulers.Len(); got != 1 {
		t.Fatalf("scheduler count = %d, want 1", got)
	}
}

func TestAccountSchedulersReplaceWhileRunningStartsReplacement(t *testing.T) {
	schedulers := NewAccountSchedulers()
	first := engine.NewScheduler(&engine.AccountRuntime{Config: &core.Config{}}, nil)
	second := engine.NewScheduler(&engine.AccountRuntime{Config: &core.Config{}}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	schedulers.Register("alice", first)
	schedulers.StartAll(ctx)

	old := schedulers.Replace("alice", second)
	if old != first {
		t.Fatalf("Replace returned %p, want first scheduler %p", old, first)
	}
	old.Wait()

	schedulers.StopAll()
	cancel()
	schedulers.WaitAll()
}

func TestAccountSchedulersSnapshotIncludesRegisteredAccounts(t *testing.T) {
	schedulers := NewAccountSchedulers()
	cfg := core.DefaultConfig()
	cfg.Runtime.MaxConcurrentScheduledJobs = 3
	schedulers.Register("alice", engine.NewScheduler(&engine.AccountRuntime{Config: &cfg}, nil))

	snapshot := schedulers.Snapshot()
	alice, ok := snapshot["alice"]
	if !ok {
		t.Fatalf("Snapshot missing alice: %+v", snapshot)
	}
	if alice.Capacity != 3 {
		t.Fatalf("alice Capacity = %d, want 3", alice.Capacity)
	}
}
