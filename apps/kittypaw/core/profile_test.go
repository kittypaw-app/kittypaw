package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStaff_MissingSoul(t *testing.T) {
	base := t.TempDir()
	// No SOUL.md exists — should fallback to default preset, no error.
	staff, err := LoadStaff(base, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if staff.ID != "default" {
		t.Errorf("ID = %q, want %q", staff.ID, "default")
	}
	if staff.Soul == "" {
		t.Error("expected fallback preset Soul, got empty")
	}
	// Should match the default-assistant preset.
	preset := Presets["default-assistant"]
	if staff.Soul != preset.Soul {
		t.Errorf("Soul = %q, want default-assistant preset", staff.Soul)
	}
}

func TestLoadStaff_ExistingSoul(t *testing.T) {
	base := t.TempDir()
	staffDir := filepath.Join(base, "staff", "mybot")
	if err := os.MkdirAll(staffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const soul = "custom staff soul"
	if err := os.WriteFile(filepath.Join(staffDir, "SOUL.md"), []byte(soul), 0o644); err != nil {
		t.Fatal(err)
	}

	staff, err := LoadStaff(base, "mybot")
	if err != nil {
		t.Fatalf("LoadStaff() error = %v", err)
	}
	if staff.ID != "mybot" || staff.Soul != soul {
		t.Fatalf("staff = %+v, want ID mybot and soul %q", staff, soul)
	}
}

func TestLoadStaff_WithUserMD(t *testing.T) {
	base := t.TempDir()
	staffDir := filepath.Join(base, "staff", "bot")
	if err := os.MkdirAll(staffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staffDir, "SOUL.md"), []byte("soul text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staffDir, "USER.md"), []byte("user likes cats"), 0o644); err != nil {
		t.Fatal(err)
	}

	staff, err := LoadStaff(base, "bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if staff.Soul != "soul text" {
		t.Errorf("Soul = %q", staff.Soul)
	}
	if staff.UserMD != "user likes cats" {
		t.Errorf("UserMD = %q", staff.UserMD)
	}
}

func TestValidateStaffID_Invalid(t *testing.T) {
	if err := ValidateStaffID("../evil"); err == nil {
		t.Fatal("expected invalid StaffID error")
	}
}

func TestEnsureDefaultStaffCreatesStaffDir(t *testing.T) {
	base := t.TempDir()
	if err := EnsureDefaultStaff(base); err != nil {
		t.Fatalf("EnsureDefaultStaff() error = %v", err)
	}

	soulPath := filepath.Join(base, "staff", "default", "SOUL.md")
	data, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("SOUL.md not created under staff/: %v", err)
	}
	if len(data) == 0 {
		t.Error("SOUL.md is empty")
	}
	// Should contain the default-assistant preset content.
	if string(data) != Presets["default-assistant"].Soul {
		t.Error("SOUL.md content does not match default-assistant preset")
	}
}

func TestEnsureDefaultStaff_Idempotent(t *testing.T) {
	base := t.TempDir()
	if err := EnsureDefaultStaff(base); err != nil {
		t.Fatal(err)
	}

	// Write custom content to SOUL.md.
	soulPath := filepath.Join(base, "staff", "default", "SOUL.md")
	custom := "My custom persona"
	if err := os.WriteFile(soulPath, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	// Second call should NOT overwrite.
	if err := EnsureDefaultStaff(base); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Errorf("SOUL.md was overwritten: got %q, want %q", string(data), custom)
	}
}

// --- T2: ApplyStaffPreset / DetectStaffDirty / StaffPresetStatus ---

func TestApplyStaffPreset(t *testing.T) {
	base := t.TempDir()
	if err := ApplyStaffPreset(base, "mybot", "friendly-assistant"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SOUL.md should match the preset.
	soulPath := filepath.Join(base, "staff", "mybot", "SOUL.md")
	data, err := os.ReadFile(soulPath)
	if err != nil {
		t.Fatalf("SOUL.md not created: %v", err)
	}
	if string(data) != Presets["friendly-assistant"].Soul {
		t.Error("SOUL.md content does not match friendly-assistant preset")
	}

	// .preset_meta should exist.
	metaPath := filepath.Join(base, "staff", "mybot", ".preset_meta")
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf(".preset_meta not created: %v", err)
	}
}

func TestApplyStaffPreset_InvalidPreset(t *testing.T) {
	base := t.TempDir()
	err := ApplyStaffPreset(base, "mybot", "nonexistent-preset")
	if err == nil {
		t.Fatal("expected error for unknown preset ID")
	}
}

func TestDetectStaffDirty_Clean(t *testing.T) {
	base := t.TempDir()
	if err := ApplyStaffPreset(base, "bot", "default-assistant"); err != nil {
		t.Fatal(err)
	}
	dirty, err := DetectStaffDirty(base, "bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dirty {
		t.Error("expected clean (not dirty) after fresh apply")
	}
}

func TestDetectStaffDirty_Modified(t *testing.T) {
	base := t.TempDir()
	if err := ApplyStaffPreset(base, "bot", "default-assistant"); err != nil {
		t.Fatal(err)
	}

	// Modify SOUL.md.
	soulPath := filepath.Join(base, "staff", "bot", "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("modified soul"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirty, err := DetectStaffDirty(base, "bot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !dirty {
		t.Error("expected dirty after modification")
	}
}

func TestStaffPresetStatus_Preset(t *testing.T) {
	base := t.TempDir()
	if err := ApplyStaffPreset(base, "bot", "professional-assistant"); err != nil {
		t.Fatal(err)
	}
	status := StaffPresetStatus(base, "bot")
	if status.Kind != StatusPreset {
		t.Errorf("Kind = %v, want StatusPreset", status.Kind)
	}
	if status.PresetID != "professional-assistant" {
		t.Errorf("PresetID = %q, want %q", status.PresetID, "professional-assistant")
	}
}

func TestStaffPresetStatus_Custom(t *testing.T) {
	base := t.TempDir()
	if err := ApplyStaffPreset(base, "bot", "default-assistant"); err != nil {
		t.Fatal(err)
	}
	// Modify SOUL.md.
	soulPath := filepath.Join(base, "staff", "bot", "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("custom persona"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := StaffPresetStatus(base, "bot")
	if status.Kind != StatusCustom {
		t.Errorf("Kind = %v, want StatusCustom", status.Kind)
	}
	if status.PresetID != "default-assistant" {
		t.Errorf("PresetID = %q, want %q (original preset)", status.PresetID, "default-assistant")
	}
}

func TestStaffPresetStatus_Unknown(t *testing.T) {
	base := t.TempDir()
	// Create a staff member without .preset_meta.
	staffDir := filepath.Join(base, "staff", "manual")
	if err := os.MkdirAll(staffDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staffDir, "SOUL.md"), []byte("hand-written"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := StaffPresetStatus(base, "manual")
	if status.Kind != StatusUnknown {
		t.Errorf("Kind = %v, want StatusUnknown", status.Kind)
	}
}

func TestPresets_NonEmpty(t *testing.T) {
	expected := []string{"default-assistant", "friendly-assistant", "professional-assistant"}
	for _, id := range expected {
		p, ok := Presets[id]
		if !ok {
			t.Errorf("preset %q not found", id)
			continue
		}
		if p.Soul == "" {
			t.Errorf("preset %q has empty Soul", id)
		}
		if p.Name == "" {
			t.Errorf("preset %q has empty Name", id)
		}
	}
}
