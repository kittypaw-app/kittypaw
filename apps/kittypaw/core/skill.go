package core

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// SkillFormat distinguishes between skill packaging standards.
type SkillFormat string

const (
	// SkillBundleDirName is the installed subdirectory that stores optional
	// SKILL.md bundled resources.
	SkillBundleDirName = "bundle"

	// SkillFormatScript is KittyPaw's JavaScript sandbox automation format.
	// The persisted value remains "native" for backward compatibility with
	// existing account skill metadata.
	SkillFormatScript SkillFormat = "native"

	// SkillFormatMarkdown is the SKILL.md prompt-mode format from AgentSkills.
	// The persisted value remains "skillmd" for backward compatibility.
	SkillFormatMarkdown SkillFormat = "skillmd"
)

// ModelTier classifies the LLM tier a skill requires.
type ModelTier string

const (
	ModelTierAutomation ModelTier = "automation"
	ModelTierAnalysis   ModelTier = "analysis"
)

// SkillManifest is the persisted metadata envelope for an installed skill.
// Format determines whether the runtime executes JavaScript code or SKILL.md
// prompt-mode instructions.
type SkillManifest struct {
	Name        string           `toml:"name"        json:"name"`
	Version     uint32           `toml:"version"     json:"version"`
	Description string           `toml:"description" json:"description"`
	CreatedAt   string           `toml:"created_at"  json:"created_at"`
	UpdatedAt   string           `toml:"updated_at"  json:"updated_at"`
	Enabled     bool             `toml:"enabled"     json:"enabled"`
	Trigger     SkillTrigger     `toml:"trigger"     json:"trigger"`
	Permissions SkillPermissions `toml:"permissions" json:"permissions"`
	Format      SkillFormat      `toml:"format"      json:"format"`
	ModelTier   *ModelTier       `toml:"model_tier"  json:"model_tier,omitempty"`

	// Provenance fields — track the original source of installed skills.
	// Empty for skills created via the teach pipeline.
	SourceURL  string `toml:"source_url,omitempty"  json:"source_url,omitempty"`
	SourceHash string `toml:"source_hash,omitempty" json:"source_hash,omitempty"` // SHA256
	SourceText string `toml:"source_text,omitempty" json:"source_text,omitempty"` // original SKILL.md content

	// Bundled resources are copied under ResourceRoot relative to the skill
	// directory. ResourceDirs lists the top-level resource directories present
	// inside that root, usually references, scripts, and/or assets.
	ResourceRoot string   `toml:"resource_root,omitempty" json:"resource_root,omitempty"`
	ResourceDirs []string `toml:"resource_dirs,omitempty" json:"resource_dirs,omitempty"`
}

// SkillTrigger defines how a skill is activated.
type SkillTrigger struct {
	Type     string         `toml:"type"     json:"type"`
	Cron     string         `toml:"cron"     json:"cron,omitempty"`
	Natural  string         `toml:"natural"  json:"natural,omitempty"`
	Keyword  string         `toml:"keyword"  json:"keyword,omitempty"`
	RunAt    string         `toml:"run_at"   json:"run_at,omitempty"` // RFC 3339 UTC
	Delivery DeliveryTarget `toml:"delivery" json:"delivery,omitempty"`
}

// DeliveryTarget identifies where a background skill or notification should be
// delivered. It is intentionally channel-generic so scheduled skills can replay
// to the chat/conversation that created them.
type DeliveryTarget struct {
	AccountID      string `toml:"account_id,omitempty"       json:"account_id,omitempty"`
	Channel        string `toml:"channel,omitempty"          json:"channel,omitempty"`
	ChatID         string `toml:"chat_id,omitempty"          json:"chat_id,omitempty"`
	ConversationID string `toml:"conversation_id,omitempty"  json:"conversation_id,omitempty"`
	ChannelUserID  string `toml:"channel_user_id,omitempty"  json:"channel_user_id,omitempty"`
	ReplyToMessage string `toml:"reply_to_message,omitempty" json:"reply_to_message,omitempty"`
}

func (t DeliveryTarget) IsZero() bool {
	return strings.TrimSpace(t.AccountID) == "" &&
		strings.TrimSpace(t.Channel) == "" &&
		strings.TrimSpace(t.ChatID) == "" &&
		strings.TrimSpace(t.ConversationID) == "" &&
		strings.TrimSpace(t.ChannelUserID) == "" &&
		strings.TrimSpace(t.ReplyToMessage) == ""
}

// SkillPermissions declares what a skill is allowed to do.
type SkillPermissions struct {
	Primitives   []string `toml:"primitives"    json:"primitives"`
	AllowedHosts []string `toml:"allowed_hosts" json:"allowed_hosts"`
}

// SkillsDir returns the directory where skills are stored, creating it if needed.
func SkillsDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return SkillsDirFrom(dir)
}

// SkillsDirFrom returns the skills directory under baseDir, creating it if needed.
func SkillsDirFrom(baseDir string) (string, error) {
	skillsDir := filepath.Join(baseDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return "", err
	}
	return skillsDir, nil
}

// SkillDirPathFrom returns the installed directory for a skill under baseDir.
func SkillDirPathFrom(baseDir, name string) (string, error) {
	if err := ValidateSkillName(name); err != nil {
		return "", err
	}
	dir, err := SkillsDirFrom(baseDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// SkillResourceRootPath returns the absolute installed resource root for a
// skill manifest. The manifest stores ResourceRoot relative to its skill
// directory so accounts can move without persisting stale absolute paths.
func SkillResourceRootPath(baseDir string, skill *SkillManifest) (string, bool, error) {
	if skill == nil || strings.TrimSpace(skill.ResourceRoot) == "" {
		return "", false, nil
	}
	root := filepath.Clean(strings.TrimSpace(skill.ResourceRoot))
	if root == "." || filepath.IsAbs(root) || root == ".." || strings.HasPrefix(root, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("invalid skill resource root %q", skill.ResourceRoot)
	}
	skillDir, err := SkillDirPathFrom(baseDir, skill.Name)
	if err != nil {
		return "", false, err
	}
	return filepath.Join(skillDir, root), true, nil
}

// SaveSkill writes a skill manifest and its executable body to disk.
func SaveSkill(skill *SkillManifest, jsCode string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	return SaveSkillTo(dir, skill, jsCode)
}

// SaveSkillTo writes a skill manifest to the skills directory under baseDir.
func SaveSkillTo(baseDir string, skill *SkillManifest, jsCode string) error {
	if err := ValidateSkillName(skill.Name); err != nil {
		return err
	}

	skillDir, err := SkillDirPathFrom(baseDir, skill.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}

	// Archive current version if it exists
	tomlPath := filepath.Join(skillDir, skill.Name+".skill.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		archiveDir := filepath.Join(skillDir, "archive")
		if err := os.MkdirAll(archiveDir, 0o755); err != nil {
			return err
		}
		stamp := time.Now().Format("20060102-150405")
		archiveBase := fmt.Sprintf("%s.v%d", stamp, skill.Version-1)
		_ = copyFile(tomlPath, filepath.Join(archiveDir, archiveBase+".skill.toml"))
		jsPath := filepath.Join(skillDir, skill.Name+".js")
		_ = copyFile(jsPath, filepath.Join(archiveDir, archiveBase+".js"))
		if err := archiveSkillBundle(skillDir, filepath.Join(archiveDir, archiveBase+".bundle")); err != nil {
			return err
		}
	}

	// Write TOML
	skill.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if skill.CreatedAt == "" {
		skill.CreatedAt = skill.UpdatedAt
	}

	f, err := os.Create(tomlPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(skill); err != nil {
		return err
	}

	// Write JS
	jsPath := filepath.Join(skillDir, skill.Name+".js")
	return os.WriteFile(jsPath, []byte(jsCode), 0o644)
}

// LoadSkill loads a single skill manifest by name. Returns nil, nil if not found.
func LoadSkill(name string) (*SkillManifest, string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, "", err
	}
	return LoadSkillFrom(dir, name)
}

// LoadSkillFrom loads a skill manifest from the skills directory under baseDir.
func LoadSkillFrom(baseDir, name string) (*SkillManifest, string, error) {
	if err := ValidateSkillName(name); err != nil {
		return nil, "", err
	}
	skillDir, err := SkillDirPathFrom(baseDir, name)
	if err != nil {
		return nil, "", err
	}
	return loadSkillFrom(skillDir, name)
}

// LoadAllSkills loads all skill manifests from the skills directory.
func LoadAllSkills() ([]SkillManifestWithCode, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	return LoadAllSkillsFrom(dir)
}

// LoadAllSkillsFrom loads all skill manifests from the skills directory under baseDir.
func LoadAllSkillsFrom(baseDir string) ([]SkillManifestWithCode, error) {
	dir, err := SkillsDirFrom(baseDir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var skills []SkillManifestWithCode
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill, code, err := loadSkillFrom(filepath.Join(dir, entry.Name()), entry.Name())
		if err != nil || skill == nil {
			continue
		}
		skills = append(skills, SkillManifestWithCode{Manifest: *skill, Code: code})
	}
	return skills, nil
}

// SkillManifestWithCode bundles a persisted skill manifest with its code or
// prompt body.
type SkillManifestWithCode struct {
	Manifest SkillManifest
	Code     string
}

// EnableSkill sets enabled=true for a skill on disk.
func EnableSkill(name string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	return EnableSkillFrom(dir, name)
}

// EnableSkillFrom sets enabled=true for a skill under baseDir.
func EnableSkillFrom(baseDir, name string) error {
	skill, code, err := LoadSkillFrom(baseDir, name)
	if err != nil {
		return err
	}
	if skill == nil {
		return fmt.Errorf("skill %q not found", name)
	}
	skill.Enabled = true
	return SaveSkillTo(baseDir, skill, code)
}

// DisableSkill sets enabled=false for a skill on disk.
func DisableSkill(name string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	return DisableSkillFrom(dir, name)
}

// DisableSkillFrom sets enabled=false for a skill under baseDir.
func DisableSkillFrom(baseDir, name string) error {
	skill, code, err := LoadSkillFrom(baseDir, name)
	if err != nil {
		return err
	}
	if skill == nil {
		return fmt.Errorf("skill %q not found", name)
	}
	skill.Enabled = false
	return SaveSkillTo(baseDir, skill, code)
}

// DeleteSkill removes a skill directory entirely.
func DeleteSkill(name string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	return DeleteSkillFrom(dir, name)
}

// DeleteSkillFrom removes a skill directory under baseDir.
func DeleteSkillFrom(baseDir, name string) error {
	if err := ValidateSkillName(name); err != nil {
		return err
	}
	skillDir, err := SkillDirPathFrom(baseDir, name)
	if err != nil {
		return err
	}
	return os.RemoveAll(skillDir)
}

// RollbackSkill restores the most recent archived version of a skill.
func RollbackSkill(name string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	return RollbackSkillFrom(dir, name)
}

// RollbackSkillFrom restores the most recent archived version under baseDir.
func RollbackSkillFrom(baseDir, name string) error {
	if err := ValidateSkillName(name); err != nil {
		return err
	}
	dir, err := SkillsDirFrom(baseDir)
	if err != nil {
		return err
	}

	archiveDir := filepath.Join(dir, name, "archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		return fmt.Errorf("no archive for skill %q", name)
	}

	// Find latest TOML archive and restore files with the same archive prefix.
	var latestToml, latestBase string
	for i := len(entries) - 1; i >= 0; i-- {
		n := entries[i].Name()
		if strings.HasSuffix(n, ".skill.toml") {
			latestToml = filepath.Join(archiveDir, n)
			latestBase = strings.TrimSuffix(n, ".skill.toml")
			break
		}
	}

	if latestToml == "" {
		return fmt.Errorf("no archived version found for skill %q", name)
	}

	skillDir := filepath.Join(dir, name)
	var archived SkillManifest
	if data, err := os.ReadFile(latestToml); err != nil {
		return err
	} else if err := toml.Unmarshal(data, &archived); err != nil {
		return fmt.Errorf("parse archived skill %q: %w", name, err)
	}
	if err := copyFile(latestToml, filepath.Join(skillDir, name+".skill.toml")); err != nil {
		return err
	}
	latestJs := filepath.Join(archiveDir, latestBase+".js")
	if latestJs != "" {
		if err := copyFile(latestJs, filepath.Join(skillDir, name+".js")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return restoreArchivedSkillBundle(baseDir, skillDir, archiveDir, latestBase, &archived)
}

func archiveSkillBundle(skillDir, destRoot string) error {
	srcRoot := filepath.Join(skillDir, SkillBundleDirName)
	info, err := os.Lstat(srcRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill bundle root must not be a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("skill bundle root is not a directory")
	}
	return copySkillBundleDirectory(srcRoot, destRoot)
}

func restoreArchivedSkillBundle(baseDir, skillDir, archiveDir, archiveBase string, archived *SkillManifest) error {
	if archived == nil || strings.TrimSpace(archived.ResourceRoot) == "" || len(archived.ResourceDirs) == 0 {
		return os.RemoveAll(filepath.Join(skillDir, SkillBundleDirName))
	}
	destRoot, ok, err := SkillResourceRootPath(baseDir, archived)
	if err != nil {
		return err
	}
	if !ok {
		return os.RemoveAll(filepath.Join(skillDir, SkillBundleDirName))
	}
	srcRoot := filepath.Join(archiveDir, archiveBase+".bundle")
	info, err := os.Lstat(srcRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("archived bundle missing for skill %q", archived.Name)
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archived bundle root must not be a symlink")
	}
	if !info.IsDir() {
		return fmt.Errorf("archived bundle root is not a directory")
	}
	if err := os.RemoveAll(destRoot); err != nil {
		return err
	}
	return copySkillBundleDirectory(srcRoot, destRoot)
}

func copySkillBundleDirectory(srcRoot, destRoot string) error {
	srcAbs, err := filepath.Abs(srcRoot)
	if err != nil {
		return err
	}
	srcAbs = filepath.Clean(srcAbs)
	destAbs, err := filepath.Abs(destRoot)
	if err != nil {
		return err
	}
	destAbs = filepath.Clean(destAbs)
	if err := os.RemoveAll(destAbs); err != nil {
		return err
	}
	return filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			rel, _ := filepath.Rel(srcAbs, path)
			return fmt.Errorf("%s must not be a symlink", rel)
		}
		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(destAbs, 0o755)
		}
		destPath, err := skillBundleDestPath(destAbs, rel)
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
		return copySkillBundleFile(path, destPath, info.Mode().Perm())
	})
}

// MatchTrigger checks if an event text activates a skill's keyword trigger.
func MatchTrigger(skill *SkillManifest, eventText string) bool {
	if skill.Trigger.Keyword == "" {
		return false
	}
	return strings.Contains(
		strings.ToLower(eventText),
		strings.ToLower(skill.Trigger.Keyword),
	)
}

func loadSkillFrom(skillDir, name string) (*SkillManifest, string, error) {
	tomlPath := filepath.Join(skillDir, name+".skill.toml")
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", err
	}

	var skill SkillManifest
	if err := toml.Unmarshal(data, &skill); err != nil {
		return nil, "", fmt.Errorf("parse skill %q: %w", name, err)
	}

	jsPath := filepath.Join(skillDir, name+".js")
	jsData, err := os.ReadFile(jsPath)
	if err != nil {
		jsData = nil // Skill without JS is valid (metadata only)
	}

	return &skill, string(jsData), nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}
