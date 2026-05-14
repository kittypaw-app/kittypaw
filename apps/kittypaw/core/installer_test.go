package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestInstallLocalSkillMdPreservesBundledResources(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := t.TempDir()

	skillMd := `---
name: bundled-skill
description: test resource bundle
permissions:
  - File
---

Use references/policy.md when needed.
`
	if err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte(skillMd), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"references/policy.md", "scripts/render.sh", "assets/template.txt"} {
		full := filepath.Join(srcDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("resource: "+path), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := InstallSkillSource(baseDir, srcDir, InstallOptions{MdExecutionMode: "prompt"}); err != nil {
		t.Fatalf("InstallSkillSource: %v", err)
	}
	skill, _, err := LoadSkillFrom(baseDir, "bundled-skill")
	if err != nil {
		t.Fatalf("LoadSkillFrom: %v", err)
	}
	if skill.ResourceRoot != SkillBundleDirName {
		t.Fatalf("ResourceRoot = %q, want %q", skill.ResourceRoot, SkillBundleDirName)
	}
	for _, want := range []string{"assets", "references", "scripts"} {
		if !containsString(skill.ResourceDirs, want) {
			t.Fatalf("ResourceDirs = %#v, missing %q", skill.ResourceDirs, want)
		}
	}
	root, ok, err := SkillResourceRootPath(baseDir, skill)
	if err != nil {
		t.Fatalf("SkillResourceRootPath: %v", err)
	}
	if !ok {
		t.Fatal("resource root should be available")
	}
	got, err := os.ReadFile(filepath.Join(root, "references", "policy.md"))
	if err != nil {
		t.Fatalf("read copied reference: %v", err)
	}
	if string(got) != "resource: references/policy.md" {
		t.Fatalf("copied reference = %q", got)
	}
}

func TestInstallLocalSkillMdRejectsBundledResourceSymlink(t *testing.T) {
	baseDir := t.TempDir()
	srcDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(srcDir, "SKILL.md"), []byte("---\nname: linked-skill\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(srcDir, "SKILL.md"), filepath.Join(srcDir, "references", "leak.md")); err != nil {
		t.Fatal(err)
	}

	_, err := InstallSkillSource(baseDir, srcDir, InstallOptions{MdExecutionMode: "prompt"})
	if err == nil {
		t.Fatal("expected error for symlinked bundled resource")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink rejection", err)
	}
	if skill, _, loadErr := LoadSkillFrom(baseDir, "linked-skill"); loadErr != nil {
		t.Fatalf("LoadSkillFrom after failed install: %v", loadErr)
	} else if skill != nil {
		t.Fatal("failed install should not leave loadable skill")
	}
	if fileExists(filepath.Join(baseDir, "skills", "linked-skill")) {
		t.Fatal("partial skill directory remained after failed bundled resource install")
	}
}

func TestReinstallSkillMdPreservesExistingSkillWhenBundleCopyFails(t *testing.T) {
	baseDir := t.TempDir()
	firstSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstSrc, "SKILL.md"), []byte(`---
name: reinstall-bundle
description: original install
permissions:
  - File
---

Original instructions.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(firstSrc, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(firstSrc, "references", "policy.md"), []byte("original resource"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkillSource(baseDir, firstSrc, InstallOptions{MdExecutionMode: "prompt"}); err != nil {
		t.Fatalf("initial InstallSkillSource: %v", err)
	}

	badSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(badSrc, "SKILL.md"), []byte(`---
name: reinstall-bundle
description: replacement install
permissions:
  - File
---

Replacement instructions.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(badSrc, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(badSrc, "SKILL.md"), filepath.Join(badSrc, "references", "leak.md")); err != nil {
		t.Fatal(err)
	}

	_, err := InstallSkillSource(baseDir, badSrc, InstallOptions{MdExecutionMode: "prompt"})
	if err == nil {
		t.Fatal("expected reinstall error for symlinked bundled resource")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink rejection", err)
	}

	skill, body, err := LoadSkillFrom(baseDir, "reinstall-bundle")
	if err != nil {
		t.Fatalf("LoadSkillFrom: %v", err)
	}
	if skill == nil {
		t.Fatal("existing skill was removed after failed reinstall")
	}
	if !strings.Contains(body, "Original instructions") {
		t.Fatalf("body = %q, want original instructions", body)
	}
	root, ok, err := SkillResourceRootPath(baseDir, skill)
	if err != nil {
		t.Fatalf("SkillResourceRootPath: %v", err)
	}
	if !ok {
		t.Fatal("existing resource root should remain")
	}
	got, err := os.ReadFile(filepath.Join(root, "references", "policy.md"))
	if err != nil {
		t.Fatalf("read existing resource: %v", err)
	}
	if string(got) != "original resource" {
		t.Fatalf("resource = %q, want original resource", got)
	}
}

func TestRollbackSkillMdRestoresBundledResources(t *testing.T) {
	baseDir := t.TempDir()
	firstSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstSrc, "SKILL.md"), []byte(`---
name: rollback-bundle
description: original install
permissions:
  - File
---

Use the original policy.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(firstSrc, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(firstSrc, "references", "policy.md"), []byte("policy v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkillSource(baseDir, firstSrc, InstallOptions{MdExecutionMode: "prompt"}); err != nil {
		t.Fatalf("initial InstallSkillSource: %v", err)
	}

	secondSrc := t.TempDir()
	if err := os.WriteFile(filepath.Join(secondSrc, "SKILL.md"), []byte(`---
name: rollback-bundle
description: replacement install
permissions:
  - File
---

Use the replacement policy.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(secondSrc, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondSrc, "references", "policy.md"), []byte("policy v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSkillSource(baseDir, secondSrc, InstallOptions{MdExecutionMode: "prompt"}); err != nil {
		t.Fatalf("replacement InstallSkillSource: %v", err)
	}

	if err := RollbackSkillFrom(baseDir, "rollback-bundle"); err != nil {
		t.Fatalf("RollbackSkillFrom: %v", err)
	}
	skill, body, err := LoadSkillFrom(baseDir, "rollback-bundle")
	if err != nil {
		t.Fatalf("LoadSkillFrom: %v", err)
	}
	if !strings.Contains(body, "original policy") {
		t.Fatalf("body = %q, want original policy", body)
	}
	root, ok, err := SkillResourceRootPath(baseDir, skill)
	if err != nil {
		t.Fatalf("SkillResourceRootPath: %v", err)
	}
	if !ok {
		t.Fatal("resource root should be restored")
	}
	got, err := os.ReadFile(filepath.Join(root, "references", "policy.md"))
	if err != nil {
		t.Fatalf("read restored resource: %v", err)
	}
	if string(got) != "policy v1" {
		t.Fatalf("restored resource = %q, want policy v1", got)
	}
}

func TestCopySkillBundleResourcesEnforcesSizeLimit(t *testing.T) {
	srcDir := t.TempDir()
	destDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(srcDir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "references", "large.md"), []byte("too large"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := copySkillBundleResources(srcDir, destDir, 4)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !strings.Contains(err.Error(), "bundle too large") {
		t.Fatalf("error = %v, want bundle too large", err)
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
