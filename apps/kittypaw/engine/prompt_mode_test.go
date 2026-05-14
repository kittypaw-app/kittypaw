package engine

import (
	"os"
	"strings"
	"testing"

	"github.com/jinto/kittypaw/core"
)

func TestFilterSkillsByPermissions(t *testing.T) {
	allowed := []string{"Http", "Storage"}
	filtered := FilterSkillsByPermissions(core.SkillRegistry, allowed)

	if len(filtered) != 2 {
		t.Fatalf("len = %d, want 2", len(filtered))
	}

	names := make(map[string]bool)
	for _, s := range filtered {
		names[s.Name] = true
	}
	if !names["Http"] {
		t.Error("Http should be in filtered list")
	}
	if !names["Storage"] {
		t.Error("Storage should be in filtered list")
	}
	if names["File"] {
		t.Error("File should NOT be in filtered list")
	}
}

func TestFilterSkillsByPermissionsEmpty(t *testing.T) {
	filtered := FilterSkillsByPermissions(core.SkillRegistry, nil)
	if len(filtered) != 0 {
		t.Errorf("nil permissions should return empty, got %d", len(filtered))
	}

	filtered = FilterSkillsByPermissions(core.SkillRegistry, []string{})
	if len(filtered) != 0 {
		t.Errorf("empty permissions should return empty, got %d", len(filtered))
	}
}

func TestFilterSkillsByPermissionsAll(t *testing.T) {
	var all []string
	for _, s := range core.SkillRegistry {
		all = append(all, s.Name)
	}
	filtered := FilterSkillsByPermissions(core.SkillRegistry, all)
	if len(filtered) != len(core.SkillRegistry) {
		t.Errorf("all permissions should return all skills, got %d/%d", len(filtered), len(core.SkillRegistry))
	}
}

func TestBuildPromptModeSystemPrompt(t *testing.T) {
	skill := &core.SkillManifest{
		Name:        "test-skill",
		Format:      core.SkillFormatMarkdown,
		Permissions: core.SkillPermissions{Primitives: []string{"Http", "Storage"}},
	}
	body := "You are a helpful assistant that fetches weather data."

	prompt := BuildPromptModeSystemPrompt(skill, body)

	if !strings.Contains(prompt, "weather data") {
		t.Error("prompt should contain SKILL.md body")
	}
	if !strings.Contains(prompt, "Http") {
		t.Error("prompt should mention allowed skills")
	}
	if !strings.Contains(prompt, "Storage") {
		t.Error("prompt should mention allowed skills")
	}
	if strings.Contains(prompt, "File") {
		t.Error("prompt should NOT mention disallowed skills")
	}
}

func TestIsMarkdownSkill(t *testing.T) {
	script := &core.SkillManifest{Format: core.SkillFormatScript}
	markdown := &core.SkillManifest{Format: core.SkillFormatMarkdown}

	if IsMarkdownSkill(script) {
		t.Error("script skill should not be markdown skill")
	}
	if !IsMarkdownSkill(markdown) {
		t.Error("markdown skill should be markdown skill")
	}
}

func TestPromptModeLegacyWrapperRemoved(t *testing.T) {
	raw, err := os.ReadFile("prompt_mode.go")
	if err != nil {
		t.Fatalf("read prompt_mode.go: %v", err)
	}
	if strings.Contains(string(raw), "IsPrompt"+"ModeSkill") {
		t.Fatal("prompt_mode.go still exposes legacy prompt-mode skill wrapper")
	}
}
