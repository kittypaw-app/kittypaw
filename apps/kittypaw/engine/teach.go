package engine

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/jinto/kittypaw/core"
	"github.com/jinto/kittypaw/llm"
)

// maxCodeSize caps generated code at 64KB to catch truncated LLM output.
const maxCodeSize = 64 * 1024

// TeachResult holds the output of the teach pipeline before user approval.
type TeachResult struct {
	SkillName   string            `json:"skill_name"`
	Code        string            `json:"code"`
	SyntaxOK    bool              `json:"syntax_ok"`
	SyntaxError string            `json:"syntax_error,omitempty"`
	Description string            `json:"description"`
	Trigger     core.SkillTrigger `json:"trigger"`
	Permissions []string          `json:"permissions"`
}

// HandleTeach runs the full teach pipeline: LLM code generation → fence stripping →
// syntax check → metadata derivation. Returns a TeachResult for user review.
// Does not save the skill — call ApproveSkill to persist.
func HandleTeach(ctx context.Context, desc, chatID string, s *AccountRuntime) (*TeachResult, error) {
	if strings.TrimSpace(desc) == "" {
		return nil, fmt.Errorf("description is empty")
	}

	raw, err := generateCode(ctx, desc, chatID, s.Provider)
	if err != nil {
		return nil, err
	}

	code := stripFences(raw)
	ok, syntaxErr := SyntaxCheck(ctx, code, nil)

	return &TeachResult{
		SkillName:   slugify(desc),
		Code:        code,
		SyntaxOK:    ok,
		SyntaxError: syntaxErr,
		Description: desc,
		Trigger:     inferTrigger(desc),
		Permissions: DetectPermissions(code),
	}, nil
}

// ApproveSkill validates the teach result and persists the skill to disk.
// For schedule triggers, validates the cron expression. Refuses to save
// skills that failed syntax check.
func ApproveSkill(baseDir string, result *TeachResult) error {
	if !result.SyntaxOK {
		return fmt.Errorf("cannot approve skill with syntax error: %s", result.SyntaxError)
	}

	if result.Trigger.Type == "schedule" {
		if parseCronInterval(result.Trigger.Cron) == 0 {
			return fmt.Errorf("invalid schedule expression: %q", result.Trigger.Cron)
		}
	}

	skill := &core.SkillManifest{
		Name:        result.SkillName,
		Version:     1,
		Description: result.Description,
		Enabled:     true,
		Format:      core.SkillFormatScript,
		Trigger:     result.Trigger,
		Permissions: core.SkillPermissions{
			Primitives: result.Permissions,
		},
	}
	dir, err := core.ResolveBaseDir(baseDir)
	if err != nil {
		return err
	}
	return core.SaveSkillTo(dir, skill, result.Code)
}

// buildTeachPrompt generates the system prompt for skill code generation.
// It dynamically includes all available globals from core.SkillRegistry,
// ensuring no drift between what the sandbox provides and what the LLM is told.
func buildTeachPrompt() string {
	var globals strings.Builder
	for _, skill := range core.SkillRegistry {
		for _, m := range skill.Methods {
			globals.WriteString("  ")
			globals.WriteString(m.Signature)
			globals.WriteString("\n")
		}
	}

	return fmt.Sprintf(`You are a JavaScript code generator for KittyPaw skills.

Your task: generate a single JavaScript function body that implements the user's request.

## Rules
- Output ONLY pure JavaScript code. NO markdown fences, NO explanations.
- Use ES2020 syntax (const, let, arrow functions, template literals, optional chaining).
- The runtime is goja, a synchronous JavaScript engine — NOT Node.js. Critically:
  * NEVER use the "async" keyword. NEVER use the "await" keyword.
  * NO Promise, .then(), or .catch() chains either.
  * Every global below is SYNCHRONOUS — call them as plain functions.
    Right: const r = Http.get(url);   const data = JSON.parse(r.body);
    Wrong: const r = await Http.get(url);   // SyntaxError in goja
- No require(), import, export.
- No Node.js or browser APIs (no process, window, document, fetch, setTimeout).
- The code runs inside a sandboxed function — write statements, not a module.

## Available Globals
These are pre-injected into the sandbox. Call them directly:

%s
## Context Object
Access the event that triggered this skill:
  context.event_type  — channel source (e.g. "telegram", "web_chat")
  context.event_text  — the message text
  context.chat_id     — channel-specific chat ID

## Output
Return the final result with "return". The return value is sent back to the user.
If the skill is a side-effect (sending a message, writing a file), return a confirmation string.`, globals.String())
}

// generateCode calls the LLM with the TEACH_PROMPT to produce JavaScript skill code.
func generateCode(ctx context.Context, desc, chatID string, provider llm.Provider) (string, error) {
	messages := []core.LlmMessage{
		{Role: core.RoleSystem, Content: buildTeachPrompt()},
		{Role: core.RoleUser, Content: desc},
	}

	resp, err := provider.Generate(WithLLMCallKind(ctx, "teach"), messages)
	if err != nil {
		return "", fmt.Errorf("LLM generation failed: %w", err)
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("LLM returned empty response")
	}
	return resp.Content, nil
}

// SyntaxCheck parses code through goja's compiler to verify it is syntactically
// valid JavaScript. This is parse-only — the code is NOT executed. Runtime
// errors (undefined variables, wrong arguments to skill stubs) are caught when
// the skill actually runs in the sandbox.
func SyntaxCheck(_ context.Context, code string, _ interface{}) (ok bool, errMsg string) {
	if strings.TrimSpace(code) == "" {
		return false, "code is empty"
	}
	if len(code) > maxCodeSize {
		return false, fmt.Sprintf("code exceeds %dKB size limit", maxCodeSize/1024)
	}

	// Wrap in IIFE like the sandbox does (sandbox/exec.go:101) so that
	// "return" statements at the top level are valid syntax.
	wrapped := fmt.Sprintf("(function(){\n%s\n})()", code)
	_, err := goja.Compile("skill.js", wrapped, false)
	if err != nil {
		return false, err.Error()
	}
	return true, ""
}

// stripFences removes the first markdown code fence from LLM output.
// Only strips the outermost fence pair, preserving inline backticks.
func stripFences(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")

	// Find first opening fence line: starts with ``` (optionally with language tag)
	openIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			openIdx = i
			break
		}
	}
	if openIdx == -1 {
		return raw // no fences found
	}

	// Find matching closing fence after the opening
	closeIdx := -1
	for i := openIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "```" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		// Opening fence but no closing — take everything after opening
		return strings.TrimSpace(strings.Join(lines[openIdx+1:], "\n"))
	}

	return strings.Join(lines[openIdx+1:closeIdx], "\n")
}

// asciiWordRe matches runs of ASCII alphanumeric characters.
var asciiWordRe = regexp.MustCompile(`[a-zA-Z0-9]+`)

// slugify converts a description into a valid skill name.
// Extracts English/numeric words, joins with hyphens, lowercased.
// Falls back to "skill-{unix_seconds}-{random}" for non-ASCII-only input.
func slugify(desc string) string {
	words := asciiWordRe.FindAllString(desc, -1)
	if len(words) == 0 {
		return fmt.Sprintf("skill-%d-%s", time.Now().Unix(), randomSuffix())
	}

	for i, w := range words {
		words[i] = strings.ToLower(w)
	}
	slug := strings.Join(words, "-")

	// Truncate overly long slugs
	if len(slug) > 60 {
		slug = slug[:60]
		slug = strings.TrimRight(slug, "-")
	}
	return slug
}

func randomSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 4)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// DetectPermissions scans JS code for references to known skill globals
// from core.SkillRegistry and returns a sorted, deduplicated list.
func DetectPermissions(code string) []string {
	seen := make(map[string]bool)
	for _, skill := range core.SkillRegistry {
		// Match "SkillName." pattern — covers all method calls
		if strings.Contains(code, skill.Name+".") {
			seen[skill.Name] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}

	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// Schedule pattern regexes for inferTrigger.
var (
	everyRe     = regexp.MustCompile(`every\s+(\d+[smhd])\b`)
	dailyKoRe   = regexp.MustCompile(`매일|매\s*일`)
	weeklyKoRe  = regexp.MustCompile(`매주|주마다|매\s*주`)
	keywordEnRe = regexp.MustCompile(`(?i)when\s+(?:someone\s+)?says\s+(\S+)`)
)

// inferTrigger examines the description and infers a SkillTrigger.
// Detects schedule patterns (English/Korean), keyword patterns, or defaults to manual.
func inferTrigger(desc string) core.SkillTrigger {
	lower := strings.ToLower(desc)

	// Schedule: "every Xm/h/d"
	if m := everyRe.FindStringSubmatch(lower); m != nil {
		return core.SkillTrigger{Type: "schedule", Cron: "every " + m[1], Natural: desc}
	}

	// Schedule: Korean daily
	if dailyKoRe.MatchString(desc) {
		return core.SkillTrigger{Type: "schedule", Cron: "every 24h", Natural: desc}
	}

	// Schedule: Korean weekly
	if weeklyKoRe.MatchString(desc) {
		return core.SkillTrigger{Type: "schedule", Cron: "every 168h", Natural: desc}
	}

	// Keyword: "when someone says X"
	if m := keywordEnRe.FindStringSubmatch(desc); m != nil {
		return core.SkillTrigger{Type: "keyword", Keyword: m[1], Natural: desc}
	}

	return core.SkillTrigger{Type: "manual", Natural: desc}
}
