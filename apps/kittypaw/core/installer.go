package core

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// InstallOptions configures the skill install behavior.
type InstallOptions struct {
	// MdExecutionMode controls how SKILL.md files are handled:
	// "prompt" — store as-is, execute via LLM with permission scoping
	// "native" — convert to JS via teach pipeline (requires LLM, not handled here)
	// Empty means the caller must prompt the user to choose.
	MdExecutionMode string

	// SourceURL is set when installing from a remote source (GitHub).
	SourceURL string
}

// InstallResult describes what was installed.
type InstallResult struct {
	SkillName string
	Format    SourceFormat
	Mode      string // "prompt" or "native" (for SkillMd) or "" (for Native packages)
}

// InstallSkillSource detects the format of a local source directory and
// installs it under baseDir. For SKILL.md in prompt mode, it saves the skill
// directly. For native packages, it delegates to PackageManager.
//
// The source directory is not modified. On failure, any partial state is cleaned up.
func InstallSkillSource(baseDir, sourcePath string, opts InstallOptions) (*InstallResult, error) {
	// Reject symlinks in source files.
	for _, name := range []string{"SKILL.md", "package.toml", "main.js"} {
		p := filepath.Join(sourcePath, name)
		fi, err := os.Lstat(p)
		if err != nil {
			continue // file doesn't exist, skip
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("install: %s must not be a symlink", name)
		}
	}

	format, err := DetectSourceFormat(sourcePath)
	if err != nil {
		return nil, err
	}

	switch format {
	case SourceFormatMarkdownSkill:
		return installSkillMd(baseDir, sourcePath, opts)
	case SourceFormatNative:
		return installNative(baseDir, sourcePath, opts)
	default:
		return nil, fmt.Errorf("install: unsupported format %q", format)
	}
}

// installSkillMd handles SKILL.md installation in prompt mode.
// For native mode (JS conversion), the caller should use the teach pipeline instead.
func installSkillMd(baseDir, sourcePath string, opts InstallOptions) (*InstallResult, error) {
	data, err := os.ReadFile(filepath.Join(sourcePath, "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("install: read SKILL.md: %w", err)
	}

	meta, body, err := ParseSkillMd(data)
	if err != nil {
		return nil, fmt.Errorf("install: %w", err)
	}

	if opts.MdExecutionMode == "" {
		return nil, fmt.Errorf("install: md_execution_mode must be set (prompt or native)")
	}

	if opts.MdExecutionMode == "native" {
		// Native conversion requires an LLM call — return metadata for the caller
		// to invoke the teach pipeline.
		return &InstallResult{
			SkillName: meta.Name,
			Format:    SourceFormatMarkdownSkill,
			Mode:      "native",
		}, fmt.Errorf("install: native conversion requires teach pipeline (not implemented in installer)")
	}

	// Prompt mode: save SKILL.md content as the skill's "code".
	skill := &SkillManifest{
		Name:        meta.Name,
		Version:     1,
		Description: meta.Description,
		Enabled:     true,
		Format:      SkillFormatMarkdown,
		Trigger:     meta.Trigger,
		Permissions: SkillPermissions{
			Primitives: meta.Permissions,
		},
		SourceURL:  opts.SourceURL,
		SourceHash: ComputeSHA256(data),
		SourceText: string(data),
	}

	// The "code" for prompt mode is the SKILL.md body (instructions for the LLM).
	if err := SaveSkillTo(baseDir, skill, body); err != nil {
		// Cleanup: remove partial skill directory.
		_ = DeleteSkillFrom(baseDir, meta.Name)
		return nil, fmt.Errorf("install: save skill: %w", err)
	}

	return &InstallResult{
		SkillName: meta.Name,
		Format:    SourceFormatMarkdownSkill,
		Mode:      "prompt",
	}, nil
}

// installNative handles package.toml + main.js installation via PackageManager.
func installNative(baseDir, sourcePath string, opts InstallOptions) (*InstallResult, error) {
	pm := NewPackageManagerFrom(baseDir, nil)
	pkg, err := pm.Install(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("install native: %w", err)
	}

	return &InstallResult{
		SkillName: pkg.Meta.ID,
		Format:    SourceFormatNative,
	}, nil
}

// DetectSourceFormat examines a directory and returns the skill source format.
// SKILL.md takes priority over package.toml when both exist.
func DetectSourceFormat(dir string) (SourceFormat, error) {
	if fileExists(filepath.Join(dir, "SKILL.md")) {
		return SourceFormatMarkdownSkill, nil
	}
	if fileExists(filepath.Join(dir, "package.toml")) && fileExists(filepath.Join(dir, "main.js")) {
		return SourceFormatNative, nil
	}
	return "", fmt.Errorf("no supported skill files found (expected SKILL.md or package.toml + main.js)")
}

// ComputeSHA256 returns the SHA256 hash of data as "sha256:<hex>".
func ComputeSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h)
}

// VerifySHA256 checks that data matches the expected hash.
// If expected is empty, verification is skipped (no hash to compare against).
func VerifySHA256(data []byte, expected string) error {
	if expected == "" {
		return nil
	}
	actual := ComputeSHA256(data)
	if actual != expected {
		return fmt.Errorf("SHA256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
