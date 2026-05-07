package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSkillProvenanceRoundtrip(t *testing.T) {
	baseDir := t.TempDir()

	skill := &Skill{
		Name:        "provenance-test",
		Version:     1,
		Description: "test provenance fields",
		Enabled:     true,
		Format:      SkillFormatNative,
		Trigger:     SkillTrigger{Type: "manual"},
		SourceURL:   "https://github.com/user/repo",
		SourceHash:  "sha256:abc123def456",
		SourceText:  "# My Skill\nDo something useful",
	}

	if err := SaveSkillTo(baseDir, skill, `return "ok"`); err != nil {
		t.Fatalf("SaveSkillTo: %v", err)
	}

	loaded, _, err := LoadSkillFrom(baseDir, "provenance-test")
	if err != nil {
		t.Fatalf("LoadSkillFrom: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded skill is nil")
	}

	if loaded.SourceURL != "https://github.com/user/repo" {
		t.Errorf("SourceURL = %q, want %q", loaded.SourceURL, "https://github.com/user/repo")
	}
	if loaded.SourceHash != "sha256:abc123def456" {
		t.Errorf("SourceHash = %q, want %q", loaded.SourceHash, "sha256:abc123def456")
	}
	if loaded.SourceText != "# My Skill\nDo something useful" {
		t.Errorf("SourceText = %q, want %q", loaded.SourceText, "# My Skill\nDo something useful")
	}
}

func TestSkillProvenanceEmpty(t *testing.T) {
	baseDir := t.TempDir()

	skill := &Skill{
		Name:    "no-provenance",
		Version: 1,
		Enabled: true,
		Format:  SkillFormatNative,
		Trigger: SkillTrigger{Type: "manual"},
	}

	if err := SaveSkillTo(baseDir, skill, ""); err != nil {
		t.Fatalf("SaveSkillTo: %v", err)
	}

	loaded, _, err := LoadSkillFrom(baseDir, "no-provenance")
	if err != nil {
		t.Fatalf("LoadSkillFrom: %v", err)
	}

	if loaded.SourceURL != "" {
		t.Errorf("SourceURL should be empty, got %q", loaded.SourceURL)
	}
	if loaded.SourceHash != "" {
		t.Errorf("SourceHash should be empty, got %q", loaded.SourceHash)
	}
	if loaded.SourceText != "" {
		t.Errorf("SourceText should be empty, got %q", loaded.SourceText)
	}
}

func TestPackagesDirFrom(t *testing.T) {
	baseDir := t.TempDir()

	dir, err := PackagesDirFrom(baseDir)
	if err != nil {
		t.Fatalf("PackagesDirFrom: %v", err)
	}

	want := filepath.Join(baseDir, "packages")
	if dir != want {
		t.Errorf("PackagesDirFrom = %q, want %q", dir, want)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("PackagesDirFrom result is not a directory")
	}
}

func TestPackagesDirFromIsolation(t *testing.T) {
	base1 := t.TempDir()
	base2 := t.TempDir()

	dir1, _ := PackagesDirFrom(base1)
	dir2, _ := PackagesDirFrom(base2)

	if dir1 == dir2 {
		t.Error("different baseDirs should produce different package directories")
	}
}

func TestRegistryEntryHash(t *testing.T) {
	entry := RegistryEntry{
		ID:   "test-pkg",
		Name: "Test",
		Hash: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	}

	if entry.Hash == "" {
		t.Error("Hash field should be populated")
	}
}
