package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestStaffDraftBuildsDevelopmentPM(t *testing.T) {
	draft := buildStaffDraft("개발PM", "test")
	if draft.ID != "dev-pm" {
		t.Fatalf("draft ID = %q, want dev-pm", draft.ID)
	}
	if draft.DisplayName != "개발 PM" {
		t.Fatalf("display name = %q, want 개발 PM", draft.DisplayName)
	}
	if !strings.Contains(draft.Description, "요구사항") {
		t.Fatalf("description = %q, want PM responsibilities", draft.Description)
	}
	if !strings.Contains(draft.Soul, "개발 PM") {
		t.Fatalf("SOUL draft = %q, want display name", draft.Soul)
	}
	if !containsString(draft.Aliases, "개발PM") {
		t.Fatalf("aliases = %#v, want 개발PM", draft.Aliases)
	}
}

func TestStaffDraftSaveLoadAndClear(t *testing.T) {
	baseDir := t.TempDir()
	draft := buildStaffDraft("개발PM", "test")

	if err := savePendingStaffDraft(baseDir, "conv-1", draft); err != nil {
		t.Fatalf("savePendingStaffDraft() error = %v", err)
	}
	loaded, ok, err := loadPendingStaffDraft(baseDir, "conv-1")
	if err != nil || !ok {
		t.Fatalf("loadPendingStaffDraft() = ok %v err %v", ok, err)
	}
	if loaded.ID != "dev-pm" || loaded.DisplayName != "개발 PM" {
		t.Fatalf("loaded draft = %+v", loaded)
	}
	if core.StaffHasSoul(baseDir, "dev-pm") {
		t.Fatal("draft save created active SOUL.md, want only draft")
	}

	if err := clearPendingStaffDraft(baseDir, "conv-1"); err != nil {
		t.Fatalf("clearPendingStaffDraft() error = %v", err)
	}
	if _, ok, err := loadPendingStaffDraft(baseDir, "conv-1"); err != nil || ok {
		t.Fatalf("load after clear = ok %v err %v, want ok false nil", ok, err)
	}
}

func TestStaffDraftCommitCreatesMetaAndSoul(t *testing.T) {
	baseDir := t.TempDir()
	draft := buildStaffDraft("개발PM", "test")
	if err := savePendingStaffDraft(baseDir, "conv-1", draft); err != nil {
		t.Fatalf("savePendingStaffDraft() error = %v", err)
	}

	if err := commitStaffDraft(baseDir, draft); err != nil {
		t.Fatalf("commitStaffDraft() error = %v", err)
	}

	meta, err := core.ReadStaffMetaFile(baseDir, "dev-pm")
	if err != nil {
		t.Fatalf("ReadStaffMetaFile(dev-pm): %v", err)
	}
	if meta.DisplayName != "개발 PM" || meta.Description != draft.Description || meta.ActivatedAt == "" {
		t.Fatalf("staff meta = %+v", meta)
	}

	soulPath := filepath.Join(baseDir, "staff", "dev-pm", "SOUL.md")
	data, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("read committed SOUL.md: %v", err)
	}
	if string(data) != draft.Soul {
		t.Fatalf("SOUL.md = %q, want draft soul", string(data))
	}

	resolved, ok, err := core.ResolveStaffReference(baseDir, "개발PM")
	if err != nil || !ok || resolved != "dev-pm" {
		t.Fatalf("ResolveStaffReference(개발PM) = %q ok=%v err=%v, want dev-pm true nil", resolved, ok, err)
	}
}

func TestStaffDraftCommitAliasCollisionDoesNotPartiallyCreate(t *testing.T) {
	baseDir := t.TempDir()
	if err := core.WriteStaffDraft(baseDir, core.StaffMetaFile{
		ID:          "finance",
		DisplayName: "재무",
		Aliases:     []string{"개발PM"},
		Description: "재무",
	}, "finance soul"); err != nil {
		t.Fatalf("seed staff draft: %v", err)
	}
	if err := core.ActivateStaffDraft(baseDir, "finance"); err != nil {
		t.Fatalf("activate finance: %v", err)
	}
	draft := buildStaffDraft("개발PM", "test")
	err := savePendingStaffDraft(baseDir, "conv-1", draft)
	if err == nil {
		t.Fatal("savePendingStaffDraft() error = nil, want alias collision")
	}
	if _, statErr := os.Stat(filepath.Join(baseDir, "staff", "dev-pm", "SOUL.md")); !os.IsNotExist(statErr) {
		t.Fatalf("SOUL.md after failed commit stat err = %v, want not exist", statErr)
	}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
