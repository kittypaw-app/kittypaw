package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSourceFormatMarkdownSkill(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody"), 0o644)

	f, err := DetectSourceFormat(dir)
	if err != nil {
		t.Fatalf("DetectSourceFormat: %v", err)
	}
	if f != SourceFormatMarkdownSkill {
		t.Errorf("format = %q, want skillmd", f)
	}
}

func TestDetectSourceFormatNative(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.toml"), []byte("[meta]\nid=\"x\"\nname=\"x\"\nversion=\"1\""), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(""), 0o644)

	f, err := DetectSourceFormat(dir)
	if err != nil {
		t.Fatalf("DetectSourceFormat: %v", err)
	}
	if f != SourceFormatNative {
		t.Errorf("format = %q, want native", f)
	}
}

func TestDetectSourceFormatNone(t *testing.T) {
	dir := t.TempDir()
	_, err := DetectSourceFormat(dir)
	if err == nil {
		t.Error("expected error for empty directory")
	}
}

func TestDetectSourceFormatPreferSkillMd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\nbody"), 0o644)
	os.WriteFile(filepath.Join(dir, "package.toml"), []byte("[meta]"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(""), 0o644)

	f, _ := DetectSourceFormat(dir)
	if f != SourceFormatMarkdownSkill {
		t.Errorf("when both exist, SKILL.md should take priority, got %q", f)
	}
}

func TestComputeSHA256(t *testing.T) {
	data := []byte("hello world")
	hash := ComputeSHA256(data)

	want := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if hash != want {
		t.Errorf("ComputeSHA256 = %q, want %q", hash, want)
	}
}

func TestVerifySHA256(t *testing.T) {
	data := []byte("test data")
	hash := ComputeSHA256(data)

	if err := VerifySHA256(data, hash); err != nil {
		t.Errorf("VerifySHA256 should pass for matching hash: %v", err)
	}

	if err := VerifySHA256(data, "sha256:wrong"); err == nil {
		t.Error("VerifySHA256 should fail for mismatched hash")
	}

	if err := VerifySHA256(data, ""); err != nil {
		t.Error("VerifySHA256 should skip when expected is empty")
	}
}

func TestInstallLocalSkillMd(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := t.TempDir()

	skillMd := `---
name: local-skill
description: test local install
permissions:
  - Http
trigger:
  type: manual
---

Do things.
`
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte(skillMd), 0o644)

	result, err := InstallSkillSource(baseDir, srcDir, InstallOptions{MdExecutionMode: "prompt"})
	if err != nil {
		t.Fatalf("InstallSkillSource: %v", err)
	}
	if result.SkillName != "local-skill" {
		t.Errorf("SkillName = %q, want local-skill", result.SkillName)
	}
	if result.Format != SourceFormatMarkdownSkill {
		t.Errorf("Format = %q", result.Format)
	}

	// Verify the skill was saved
	skill, code, err := LoadSkillFrom(baseDir, "local-skill")
	if err != nil {
		t.Fatalf("LoadSkillFrom: %v", err)
	}
	if skill == nil {
		t.Fatal("skill should exist after install")
	}
	if skill.Format != SkillFormatMarkdown {
		t.Errorf("skill Format = %q, want skillmd", skill.Format)
	}
	if skill.SourceURL != "" {
		t.Errorf("SourceURL should be empty for local install, got %q", skill.SourceURL)
	}
	if code == "" {
		t.Error("code (SKILL.md body) should not be empty")
	}
}

func TestInstallLocalNative(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := t.TempDir()
	writeTestPackage(t, srcDir, "native-pkg", "1.0.0")

	result, err := InstallSkillSource(baseDir, srcDir, InstallOptions{})
	if err != nil {
		t.Fatalf("InstallSkillSource: %v", err)
	}
	if result.Format != SourceFormatNative {
		t.Errorf("Format = %q, want native", result.Format)
	}
}

func TestInstallCleanupOnFailure(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := t.TempDir()

	// Write an invalid SKILL.md (no name)
	os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\ndescription: no name\n---\nbody"), 0o644)

	_, err := InstallSkillSource(baseDir, srcDir, InstallOptions{MdExecutionMode: "prompt"})
	if err == nil {
		t.Error("expected error for invalid SKILL.md")
	}

	// Verify no partial files remain
	skillsDir := filepath.Join(baseDir, "skills")
	entries, _ := os.ReadDir(skillsDir)
	if len(entries) > 0 {
		t.Errorf("cleanup failed: found %d entries in skills dir after failed install", len(entries))
	}
}

func TestInstallSymlinkRejection(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := t.TempDir()

	// Create a real SKILL.md in another dir
	realDir := t.TempDir()
	os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte("---\nname: x\n---\nbody"), 0o644)

	// Create a symlink to it
	os.Symlink(filepath.Join(realDir, "SKILL.md"), filepath.Join(srcDir, "SKILL.md"))

	_, err := InstallSkillSource(baseDir, srcDir, InstallOptions{MdExecutionMode: "prompt"})
	if err == nil {
		t.Error("expected error for symlinked SKILL.md")
	}
}
