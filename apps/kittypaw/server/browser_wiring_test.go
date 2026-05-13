package server

import (
	"errors"
	"testing"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/engine"
)

func TestBuildAccountRuntimeWiresBrowserController(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Workspace.LiveIndex = false
	td := buildAccountDeps(t, root, "alice", &cfg)

	sess := buildAccountRuntime(td, core.NewAccountRegistry(root, "alice"), nil)
	if sess.BrowserController == nil {
		t.Fatal("BrowserController not wired")
	}
}

func TestBuildAccountRuntimeWiresAdmission(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Workspace.LiveIndex = false
	cfg.Runtime.MaxConcurrentTurnsPerAccount = 2
	cfg.Runtime.MaxQueuedTurnsPerAccount = 0
	td := buildAccountDeps(t, root, "alice", &cfg)

	sess := buildAccountRuntime(td, core.NewAccountRegistry(root, "alice"), nil)
	first, err := sess.Admission.Acquire(t.Context(), engine.RuntimeAdmissionRequest{AccountID: "alice", ScopeKey: "one"})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer first.Release()
	second, err := sess.Admission.Acquire(t.Context(), engine.RuntimeAdmissionRequest{AccountID: "alice", ScopeKey: "two"})
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer second.Release()
	if _, err := sess.Admission.Acquire(t.Context(), engine.RuntimeAdmissionRequest{AccountID: "alice", ScopeKey: "three"}); !errors.Is(err, engine.ErrRuntimeAdmissionBusy) {
		t.Fatalf("third Acquire err = %v, want ErrRuntimeAdmissionBusy", err)
	}
}
