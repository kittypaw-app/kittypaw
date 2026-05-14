package engine

import (
	"fmt"
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

// BuildPromptModeSystemPrompt constructs the system prompt for executing a
// SKILL.md skill. It includes the skill's instructions (body) and documents
// only the permitted skill globals.
func BuildPromptModeSystemPrompt(skill *core.SkillManifest, body string) string {
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

	sb.WriteString("## Rules\n")
	sb.WriteString("- Follow the skill instructions above to produce a response.\n")
	sb.WriteString("- Do NOT call any tools not listed above.\n")

	return sb.String()
}
