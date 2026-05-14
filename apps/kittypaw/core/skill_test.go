package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProvenanceRoundtrip(t *testing.T) {
	baseDir := t.TempDir()

	skill := &SkillManifest{
		Name:        "provenance-test",
		Version:     1,
		Description: "test provenance fields",
		Enabled:     true,
		Format:      SkillFormatScript,
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

func TestScriptAndMarkdownSkillFormatNamesPreserveStorageValues(t *testing.T) {
	if SkillFormatScript != "native" {
		t.Fatalf("SkillFormatScript storage value = %q, want native", SkillFormatScript)
	}
	if SkillFormatMarkdown != "skillmd" {
		t.Fatalf("SkillFormatMarkdown storage value = %q, want skillmd", SkillFormatMarkdown)
	}
}

func TestSkillNamingDoesNotExposeLegacyAliases(t *testing.T) {
	checks := []struct {
		path      string
		forbidden []string
	}{
		{
			path: "skill.go",
			forbidden: []string{
				"SkillFormat" + "Native",
				"SkillFormat" + "Md",
				"type Skill =",
				"type Script" + "Skill",
				"type Markdown" + "Skill",
				"Script" + "SkillWithCode",
			},
		},
		{
			path:      "github.go",
			forbidden: []string{"SourceFormat" + "SkillMd"},
		},
	}

	for _, check := range checks {
		raw, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("read %s: %v", check.path, err)
		}
		source := string(raw)
		for _, symbol := range check.forbidden {
			if strings.Contains(source, symbol) {
				t.Fatalf("%s still exposes legacy symbol %q", check.path, symbol)
			}
		}
	}
}

func TestSkillManifestIsTheStorageEnvelope(t *testing.T) {
	raw, err := os.ReadFile("skill.go")
	if err != nil {
		t.Fatalf("read skill.go: %v", err)
	}
	if !strings.Contains(string(raw), "type SkillManifest struct") {
		t.Fatal("skill.go should expose SkillManifest as the stored skill metadata envelope")
	}
}

func TestSkillProvenanceEmpty(t *testing.T) {
	baseDir := t.TempDir()

	skill := &SkillManifest{
		Name:    "no-provenance",
		Version: 1,
		Enabled: true,
		Format:  SkillFormatScript,
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
