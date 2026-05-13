package server

import (
	"runtime"
	"testing"
	"time"

	"github.com/jinto/kittypaw/core"
)

// TestBuildAccountRuntime_LiveIndexEnabled: default config wires a
// LiveIndexer onto AccountDeps.
func TestBuildAccountRuntime_LiveIndexEnabled(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	td := buildAccountDeps(t, root, "default", &cfg)

	registry := core.NewAccountRegistry(root, "default")
	eventCh := make(chan core.Event, 4)
	_ = buildAccountRuntime(td, registry, eventCh)

	if td.LiveIndexer == nil {
		t.Fatal("expected LiveIndexer to be wired when live_index=true")
	}
	// Give the startup goroutine a window to run before cleanup closes
	// the store underneath it.
	time.Sleep(80 * time.Millisecond)
	t.Cleanup(func() { _ = td.Close() })
}

// TestBuildAccountRuntime_LiveIndexDisabled: config with live_index=false
// leaves LiveIndexer nil (v1 behavior preserved).
func TestBuildAccountRuntime_LiveIndexDisabled(t *testing.T) {
	root := t.TempDir()
	cfg := core.DefaultConfig()
	cfg.Workspace.LiveIndex = false
	td := buildAccountDeps(t, root, "default", &cfg)

	registry := core.NewAccountRegistry(root, "default")
	eventCh := make(chan core.Event, 4)
	_ = buildAccountRuntime(td, registry, eventCh)

	if td.LiveIndexer != nil {
		t.Fatal("expected LiveIndexer to be nil when live_index=false")
	}
	time.Sleep(80 * time.Millisecond)
	t.Cleanup(func() { _ = td.Close() })
}

// TestAccountDeps_Close_NoGoroutineLeak: close after buildAccountRuntime
// tears down LiveIndexer cleanly.
func TestAccountDeps_Close_NoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	for range 3 {
		root := t.TempDir()
		cfg := core.DefaultConfig()
		td := buildAccountDeps(t, root, "default", &cfg)
		registry := core.NewAccountRegistry(root, "default")
		eventCh := make(chan core.Event, 4)
		_ = buildAccountRuntime(td, registry, eventCh)

		// Give the startup goroutine a window to call AddWorkspace + Start.
		time.Sleep(50 * time.Millisecond)

		if err := td.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}

	time.Sleep(150 * time.Millisecond)
	after := runtime.NumGoroutine()

	// Slack for test-framework goroutines; leak bar is "no growth trend".
	if after > before+3 {
		t.Errorf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestWorkspaceConfig_DefaultsOn: a fresh DefaultConfig has LiveIndex=true
// so operators don't have to opt in to the default behavior.
func TestWorkspaceConfig_DefaultsOn(t *testing.T) {
	cfg := core.DefaultConfig()
	if !cfg.Workspace.LiveIndex {
		t.Errorf("DefaultConfig.Workspace.LiveIndex: got false, want true")
	}
}
