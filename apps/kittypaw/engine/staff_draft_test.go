package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	st := openTestStore(t)
	draft := buildStaffDraft("개발PM", "test")

	if err := savePendingStaffDraft(st, "conv-1", draft); err != nil {
		t.Fatalf("savePendingStaffDraft() error = %v", err)
	}
	loaded, ok, err := loadPendingStaffDraft(st, "conv-1")
	if err != nil || !ok {
		t.Fatalf("loadPendingStaffDraft() = ok %v err %v", ok, err)
	}
	if loaded.ID != "dev-pm" || loaded.DisplayName != "개발 PM" {
		t.Fatalf("loaded draft = %+v", loaded)
	}

	if err := clearPendingStaffDraft(st, "conv-1"); err != nil {
		t.Fatalf("clearPendingStaffDraft() error = %v", err)
	}
	if _, ok, err := loadPendingStaffDraft(st, "conv-1"); err != nil || ok {
		t.Fatalf("load after clear = ok %v err %v, want ok false nil", ok, err)
	}
}

func TestStaffDraftCommitCreatesMetadataSoulAndAliases(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	draft := buildStaffDraft("개발PM", "test")

	if err := commitStaffDraft(baseDir, st, draft); err != nil {
		t.Fatalf("commitStaffDraft() error = %v", err)
	}

	meta, ok, err := st.GetStaffMeta("dev-pm")
	if err != nil || !ok {
		t.Fatalf("GetStaffMeta(dev-pm) = ok %v err %v", ok, err)
	}
	if meta.DisplayName != "개발 PM" || meta.Description != draft.Description || !meta.Active {
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

	resolved, ok, err := st.ResolveStaffID("개발PM")
	if err != nil || !ok || resolved != "dev-pm" {
		t.Fatalf("ResolveStaffID(개발PM) = %q ok=%v err=%v, want dev-pm true nil", resolved, ok, err)
	}
}

func TestStaffDraftCommitAliasCollisionDoesNotPartiallyCreate(t *testing.T) {
	st := openTestStore(t)
	baseDir := t.TempDir()
	if err := st.UpsertStaffMetaWithDisplayName("finance", "재무", "재무", "[]", "test"); err != nil {
		t.Fatalf("seed staff meta: %v", err)
	}
	if err := st.ReplaceStaffAliases("finance", []string{"개발PM"}); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	draft := buildStaffDraft("개발PM", "test")

	err := commitStaffDraft(baseDir, st, draft)
	if err == nil {
		t.Fatal("commitStaffDraft() error = nil, want alias collision")
	}
	if _, ok, getErr := st.GetStaffMeta("dev-pm"); getErr != nil || ok {
		t.Fatalf("GetStaffMeta(dev-pm) after failed commit = ok %v err %v, want absent", ok, getErr)
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
