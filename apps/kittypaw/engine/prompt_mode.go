package engine

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jinto/kittypaw/core"
)

// IsMarkdownSkill returns true if the skill uses SKILL.md prompt-based
// execution rather than JavaScript sandbox execution.
func IsMarkdownSkill(skill *core.SkillManifest) bool {
	return skill.Format == core.SkillFormatMarkdown
}

// FilterSkillsByPermissions returns only the skill globals whose Name appears
// in the allowed list. This enforces SKILL.md permission scoping — only declared
// capabilities are exposed to the LLM.
func FilterSkillsByPermissions(registry []core.SkillMeta, allowed []string) []core.SkillMeta {
	if len(allowed) == 0 {
		return nil
	}

	set := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		set[name] = true
	}

	var filtered []core.SkillMeta
	for _, s := range registry {
		if set[s.Name] {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

// PromptModeSkillResources describes the installed bundle directories that a
// prompt-mode skill may reference through its declared tools.
type PromptModeSkillResources struct {
	Root string
	Dirs []string
}

// BuildPromptModeSystemPrompt constructs the system prompt for executing a
// SKILL.md skill. It includes the skill's instructions (body) and documents
// only the permitted skill globals.
func BuildPromptModeSystemPrompt(skill *core.SkillManifest, body string) string {
	return BuildPromptModeSystemPromptWithResources(skill, body, PromptModeSkillResources{})
}

// BuildPromptModeSystemPromptWithResources constructs the system prompt and
// includes installed bundled resource guidance when the skill has resources.
func BuildPromptModeSystemPromptWithResources(skill *core.SkillManifest, body string, resources PromptModeSkillResources) string {
	allowed := FilterSkillsByPermissions(core.SkillRegistry, skill.Permissions.Primitives)

	var sb strings.Builder
	sb.WriteString("You are executing a skill in prompt mode.\n\n")
	sb.WriteString("## Skill Instructions\n\n")
	sb.WriteString(body)
	sb.WriteString("\n\n")

	if len(allowed) > 0 {
		sb.WriteString("## Available Tools\n\n")
		sb.WriteString("You may ONLY use the following tools. Any other tool calls will be rejected.\n\n")
		for _, s := range allowed {
			sb.WriteString(fmt.Sprintf("### %s\n", s.Name))
			for _, m := range s.Methods {
				sb.WriteString(fmt.Sprintf("- tool `%s` for `%s`\n", promptModeToolName(s.Name, m.Name), m.Signature))
			}
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("## Tools\n\nNo tools are available. Respond with text only.\n\n")
	}

	if strings.TrimSpace(resources.Root) != "" && len(resources.Dirs) > 0 {
		sb.WriteString("## Bundled Resources\n\n")
		sb.WriteString(fmt.Sprintf("Resource root: `%s`\n\n", resources.Root))
		sb.WriteString("Available directories:\n")
		for _, dir := range resources.Dirs {
			sb.WriteString(fmt.Sprintf("- `%s/`\n", dir))
		}
		sb.WriteString("\nUse absolute paths under the resource root when reading bundled references or inspecting scripts/assets. ")
		sb.WriteString("Use File tools only when File is listed above. Use Shell tools only when Shell is listed above. ")
		sb.WriteString("Do not assume bundled scripts are executable or safe to run; inspect them first and run them only when the skill instructions and available permissions require it.\n\n")
	}

	sb.WriteString("## Rules\n")
	sb.WriteString("- Follow the skill instructions above to produce a response.\n")
	sb.WriteString("- Do NOT call any tools not listed above.\n")

	return sb.String()
}

func promptModeSkillResources(baseDir string, skill *core.SkillManifest) (PromptModeSkillResources, error) {
	if skill == nil {
		return PromptModeSkillResources{}, nil
	}
	dirs := make([]string, 0, len(skill.ResourceDirs))
	for _, dir := range skill.ResourceDirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return PromptModeSkillResources{}, nil
	}
	sort.Strings(dirs)

	root, ok, err := core.SkillResourceRootPath(baseDir, skill)
	if err != nil || !ok {
		return PromptModeSkillResources{}, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return PromptModeSkillResources{}, fmt.Errorf("skill %q bundled resource root: %w", skill.Name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return PromptModeSkillResources{}, fmt.Errorf("skill %q bundled resource root must not be a symlink", skill.Name)
	}
	if !info.IsDir() {
		return PromptModeSkillResources{}, fmt.Errorf("skill %q bundled resource root is not a directory", skill.Name)
	}
	return PromptModeSkillResources{Root: root, Dirs: dirs}, nil
}
