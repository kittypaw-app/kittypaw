package core

import (
	"regexp"
	"strings"
)

var sensitivePromptSnippetPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bsk-[a-z0-9][a-z0-9_-]{6,}\b`),
	regexp.MustCompile(`(?i)\bgh[pousr]_[a-z0-9_]{8,}\b`),
	regexp.MustCompile(`(?i)\bxox[baprs]-[a-z0-9-]{8,}\b`),
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}\b`),
	regexp.MustCompile(`(?i)\b(api[_ -]?key|access[_ -]?token|refresh[_ -]?token|bot[_ -]?token|password|secret|oauth[_ -]?token|credential)\b\s*(=|:|is|는|은)\s*\S+`),
	regexp.MustCompile(`(?i)(\?|&)(api[_-]?key|key|token|access[_-]?token|refresh[_-]?token)=`),
}

// SafePromptSummarySnippet normalizes and caps text for prompt summaries.
// It returns an empty string for credential-looking snippets so summaries do
// not duplicate secrets into durable compaction rows or future prompts.
func SafePromptSummarySnippet(text string, maxRunes int) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" || LooksSensitivePromptSnippet(text) {
		return ""
	}
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}

func LooksSensitivePromptSnippet(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, pattern := range sensitivePromptSnippetPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}
