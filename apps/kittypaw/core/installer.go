package core

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxSkillBundleBytes int64 = 25 * 1024 * 1024

var skillBundleResourceDirNames = []string{"assets", "references", "scripts"}

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

	resourceDirs, err := detectSkillBundleResourceDirs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("install: bundled resources: %w", err)
	}
	var stagedBundleRoot string
	if len(resourceDirs) > 0 {
		skillsDir, err := SkillsDirFrom(baseDir)
		if err != nil {
			return nil, err
		}
		stagedBundleRoot, err = os.MkdirTemp(skillsDir, "."+meta.Name+"-bundle-*")
		if err != nil {
			return nil, fmt.Errorf("install: stage bundled resources: %w", err)
		}
		defer os.RemoveAll(stagedBundleRoot)
		if _, err := copySkillBundleResources(sourcePath, stagedBundleRoot, maxSkillBundleBytes); err != nil {
			return nil, fmt.Errorf("install: bundled resources: %w", err)
		}
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
	if len(resourceDirs) > 0 {
		skill.ResourceRoot = SkillBundleDirName
		skill.ResourceDirs = resourceDirs
	}

	// The "code" for prompt mode is the SKILL.md body (instructions for the LLM).
	if err := SaveSkillTo(baseDir, skill, body); err != nil {
		// Cleanup: remove partial skill directory.
		_ = DeleteSkillFrom(baseDir, meta.Name)
		return nil, fmt.Errorf("install: save skill: %w", err)
	}
	if len(resourceDirs) > 0 {
		resourceRoot, ok, err := SkillResourceRootPath(baseDir, skill)
		if err != nil {
			_ = DeleteSkillFrom(baseDir, meta.Name)
			return nil, fmt.Errorf("install: bundled resources: %w", err)
		}
		if !ok {
			_ = DeleteSkillFrom(baseDir, meta.Name)
			return nil, fmt.Errorf("install: bundled resources: missing resource root")
		}
		if err := replaceSkillBundle(resourceRoot, stagedBundleRoot); err != nil {
			return nil, fmt.Errorf("install: bundled resources: %w", err)
		}
	} else if err := removeInstalledSkillBundle(baseDir, meta.Name); err != nil {
		return nil, fmt.Errorf("install: remove bundled resources: %w", err)
	}

	return &InstallResult{
		SkillName: meta.Name,
		Format:    SourceFormatMarkdownSkill,
		Mode:      "prompt",
	}, nil
}

func removeInstalledSkillBundle(baseDir, skillName string) error {
	skillDir, err := SkillDirPathFrom(baseDir, skillName)
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(skillDir, SkillBundleDirName))
}

func replaceSkillBundle(destRoot, stagedRoot string) error {
	if strings.TrimSpace(stagedRoot) == "" {
		return fmt.Errorf("staged bundle root required")
	}
	parent := filepath.Dir(destRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	var backupRoot string
	if info, err := os.Lstat(destRoot); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("existing bundle root must not be a symlink")
		}
		backupRoot = filepath.Join(parent, "."+filepath.Base(destRoot)+"-backup-"+time.Now().UTC().Format("20060102150405.000000000"))
		if err := os.Rename(destRoot, backupRoot); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(stagedRoot, destRoot); err != nil {
		if backupRoot != "" {
			_ = os.Rename(backupRoot, destRoot)
		}
		return err
	}
	if backupRoot != "" {
		_ = os.RemoveAll(backupRoot)
	}
	return nil
}

func detectSkillBundleResourceDirs(sourcePath string) ([]string, error) {
	var dirs []string
	for _, name := range skillBundleResourceDirNames {
		path := filepath.Join(sourcePath, name)
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s must not be a symlink", name)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s must be a directory", name)
		}
		dirs = append(dirs, name)
	}
	sort.Strings(dirs)
	return dirs, nil
}

func copySkillBundleResources(sourcePath, destRoot string, maxBytes int64) ([]string, error) {
	dirs, err := detectSkillBundleResourceDirs(sourcePath)
	if err != nil {
		return nil, err
	}
	if len(dirs) == 0 {
		return nil, nil
	}
	if err := os.RemoveAll(destRoot); err != nil {
		return nil, fmt.Errorf("remove existing bundle: %w", err)
	}
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create bundle root: %w", err)
	}

	sourceRoot, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve source root: %w", err)
	}
	sourceRoot = filepath.Clean(sourceRoot)
	destRootAbs, err := filepath.Abs(destRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve bundle root: %w", err)
	}
	destRootAbs = filepath.Clean(destRootAbs)

	var total int64
	for _, dir := range dirs {
		start := filepath.Join(sourceRoot, dir)
		if err := filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.Type()&os.ModeSymlink != 0 {
				rel, _ := filepath.Rel(sourceRoot, path)
				return fmt.Errorf("%s must not be a symlink", rel)
			}
			rel, err := filepath.Rel(sourceRoot, path)
			if err != nil {
				return fmt.Errorf("resolve bundled resource path: %w", err)
			}
			destPath, err := skillBundleDestPath(destRootAbs, rel)
			if err != nil {
				return err
			}
			if d.IsDir() {
				return os.MkdirAll(destPath, 0o755)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s has unsupported file type", rel)
			}
			total += info.Size()
			if maxBytes > 0 && total > maxBytes {
				return fmt.Errorf("bundle too large: %d bytes exceeds max %d", total, maxBytes)
			}
			return copySkillBundleFile(path, destPath, info.Mode().Perm())
		}); err != nil {
			return nil, err
		}
	}
	return dirs, nil
}

func skillBundleDestPath(destRoot, rel string) (string, error) {
	rel = filepath.Clean(rel)
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid bundled resource path %q", rel)
	}
	return filepath.Join(destRoot, rel), nil
}

func copySkillBundleFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(dst, mode); err != nil {
		return err
	}
	return nil
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
